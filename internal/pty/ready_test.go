package pty

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- marker detection -----------------------------------------------------

// feedSplit feeds stream to a fresh gate in chunks cut at the given offsets.
func feedSplit(stream []byte, cuts ...int) *readyGate {
	g := newReadyGate(0, nil)
	prev := 0
	for _, c := range cuts {
		g.observe(stream[prev:c])
		prev = c
	}
	g.observe(stream[prev:])
	return g
}

func TestReadyGate_DetectsBracketedPasteAcrossEverySplit(t *testing.T) {
	cases := []struct {
		name   string
		stream string
		want   bool
	}{
		{"enable", "\x1b]0;title\a\r\n$ \x1b[?2004h", true},
		{"enable then disable", "x\x1b[?2004hls\r\n\x1b[?2004l\r", false},
		{"disable then enable", "\x1b[?2004l\r\nout\r\n$ \x1b[?2004h", true},
		{"combined DECSET is ignored", "\x1b[?2004;1h$ ", false},
		{"truncated marker", "$ \x1b[?2004", false},
		{"no marker", "hello world, no escapes here", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := []byte(tc.stream)
			if g := feedSplit(s); g.bpOn.Load() != tc.want {
				t.Fatalf("whole: got %v want %v", !tc.want, tc.want)
			}
			for i := 0; i <= len(s); i++ {
				for j := i; j <= len(s); j++ {
					if g := feedSplit(s, i, j); g.bpOn.Load() != tc.want {
						t.Fatalf("cuts %d,%d: got %v want %v", i, j, !tc.want, tc.want)
					}
				}
			}
			g := newReadyGate(0, nil) // byte by byte
			for i := range s {
				g.observe(s[i : i+1])
			}
			if g.bpOn.Load() != tc.want {
				t.Fatalf("byte-by-byte: got %v want %v", !tc.want, tc.want)
			}
		})
	}
}

func TestReadyGate_ObserveDoesNotAllocate(t *testing.T) {
	g := newReadyGate(0, nil)
	chunks := [][]byte{
		bytes.Repeat([]byte("\x1b[32mgreen\x1b[0m "), 500),
		[]byte("\x1b[?20"), []byte("04h"), []byte("x"), []byte("\x1b[?2004l"),
	}
	if n := testing.AllocsPerRun(100, func() {
		for _, c := range chunks {
			g.observe(c)
		}
	}); n != 0 {
		t.Fatalf("observe allocated %v times per run", n)
	}
}

// The pump hands the observer the chunk but emits exactly the bytes read.
func TestPump_ObserverDoesNotAlterOutput(t *testing.T) {
	pr, pw := io.Pipe()
	var mu sync.Mutex
	var got []byte
	emit := func(_ string, data ...any) {
		mu.Lock()
		defer mu.Unlock()
		for _, v := range data[0].([]int) {
			got = append(got, byte(v))
		}
	}
	g := newReadyGate(0, nil)
	done := make(chan struct{})
	go func() {
		pump(pr, "d", emit, 4, intsPayload, g.observe)
		close(done)
	}()
	want := "$ \x1b[?2004hrest"
	_, _ = pw.Write([]byte(want))
	_ = pw.Close()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if string(got) != want {
		t.Fatalf("emitted %q, want %q", got, want)
	}
	if !g.bpOn.Load() {
		t.Fatal("marker split by maxChunk=4 not detected")
	}
}

// --- WaitShellReady decision logic with a fake tty probe -------------------

type fakeTTY struct {
	mu        sync.Mutex
	pgrp      int
	canonical bool
}

func (f *fakeTTY) probe() (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pgrp, f.canonical, nil
}

// fakePID has no /proc entry, so childInGroup reports false for it.
const fakePID = -7

func fakeBridge(pgrp int, canonical, markerOnly bool) (*Bridge, *readyGate) {
	f := &fakeTTY{pgrp: pgrp, canonical: canonical}
	g := newReadyGate(fakePID, f.probe)
	g.traitsOnce.Do(func() { g.markerOnly = markerOnly })
	return &Bridge{gate: g}, g
}

func wantResult(t *testing.T, got, want ShellReadiness) {
	t.Helper()
	if got != want {
		t.Fatalf("WaitShellReady = %v, want %v", got, want)
	}
}

func TestWaitShellReady_MarkerWithForegroundShell(t *testing.T) {
	t.Parallel()
	for _, markerOnly := range []bool{false, true} {
		b, g := fakeBridge(fakePID, false, markerOnly)
		go func() {
			time.Sleep(50 * time.Millisecond)
			g.observe([]byte("$ \x1b[?2004h"))
		}()
		start := time.Now()
		wantResult(t, b.WaitShellReady(context.Background(), 5*time.Second), ShellReady)
		if d := time.Since(start); d > rawStableFor/2 {
			t.Fatalf("marker path took %v; looks like the raw-mode fallback fired", d)
		}
	}
}

func TestWaitShellReady_BusyWhileOtherGroupForeground(t *testing.T) {
	t.Parallel()
	b, g := fakeBridge(99, false, false) // e.g. a TUI or passphrase prompt
	g.observe([]byte("\x1b[?2004h"))
	wantResult(t, b.WaitShellReady(context.Background(), 1500*time.Millisecond), ShellBusy)
}

func TestWaitShellReady_MarkerIgnoredInCanonicalMode(t *testing.T) {
	t.Parallel()
	b, g := fakeBridge(fakePID, true, true) // rc printed the marker, then `read`
	g.observe([]byte("\x1b[?2004h"))
	wantResult(t, b.WaitShellReady(context.Background(), 1500*time.Millisecond), ShellBusy)
}

func TestWaitShellReady_RawModeFallback(t *testing.T) {
	t.Parallel()
	b, _ := fakeBridge(fakePID, false, false)
	start := time.Now()
	wantResult(t, b.WaitShellReady(context.Background(), 5*time.Second), ShellReady)
	if d := time.Since(start); d < rawStableFor {
		t.Fatalf("fallback fired after %v, before %v", d, rawStableFor)
	}
}

// For a shell known to emit the marker, raw mode without it is a builtin
// cbreak read in an rc file (`read -n 1`), not the prompt.
func TestWaitShellReady_NoRawFallbackForMarkerShells(t *testing.T) {
	t.Parallel()
	b, _ := fakeBridge(fakePID, false, true)
	wantResult(t, b.WaitShellReady(context.Background(), 1500*time.Millisecond), ShellBusy)
}

func TestWaitShellReady_IdleCanonical(t *testing.T) {
	t.Parallel()
	b, _ := fakeBridge(fakePID, true, false) // dash at its prompt
	start := time.Now()
	wantResult(t, b.WaitShellReady(context.Background(), 5*time.Second), ShellIdleCanonical)
	if d := time.Since(start); d < canonicalQuietFor {
		t.Fatalf("idle after %v, before %v", d, canonicalQuietFor)
	}
}

// Output keeps the canonical shell from counting as idle.
func TestWaitShellReady_IdleCanonicalNeedsQuiet(t *testing.T) {
	t.Parallel()
	b, g := fakeBridge(fakePID, true, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			g.observe([]byte("progress...\r"))
			time.Sleep(100 * time.Millisecond)
		}
	}()
	wantResult(t, b.WaitShellReady(ctx, 1500*time.Millisecond), ShellBusy)
}

func TestWaitShellReady_CanonicalMarkerShellIsBusy(t *testing.T) {
	t.Parallel()
	b, _ := fakeBridge(fakePID, true, true) // bash/zsh in an rc `read -t 2`
	wantResult(t, b.WaitShellReady(context.Background(), 1500*time.Millisecond), ShellBusy)
}

func TestWaitShellReady_NoProcessIsReady(t *testing.T) {
	b := NewBridgeForTest(func() error { return nil })
	wantResult(t, b.WaitShellReady(context.Background(), time.Minute), ShellReady)
}

func TestShellReadiness_CanType(t *testing.T) {
	for r, want := range map[ShellReadiness]bool{
		ShellReady: true, ShellIdleCanonical: true,
		ShellBusy: false, ShellClosed: false, ShellCancelled: false, 0: false,
	} {
		if r.CanType() != want {
			t.Errorf("%v.CanType() = %v", r, !want)
		}
	}
}

// --- shell classification ---------------------------------------------------

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil { //nolint:gosec // an executable test stub
		t.Fatal(err)
	}
}

func TestClassifyShell(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "rc-off"), "# x\nset enable-bracketed-paste Off\n")
	writeFile(t, filepath.Join(dir, "rc-on"), "set enable-bracketed-paste off\nset enable-bracketed-paste on\n")
	writeFile(t, filepath.Join(dir, "rc-none"), "set editing-mode vi\n")
	oldBash := filepath.Join(dir, "old", "bash")
	writeScript(t, oldBash, "#!/bin/sh\necho 'GNU bash, version 4.4.20(1)-release'\n")
	newBash := filepath.Join(dir, "new", "bash")
	writeScript(t, newBash, "#!/bin/sh\necho 'GNU bash, version 5.1.16(1)-release'\n")
	link := filepath.Join(dir, "loginsh")
	if err := os.Symlink("/usr/bin/zsh", link); err != nil {
		t.Fatal(err)
	}
	env := func(rc string) []string { return []string{"INPUTRC=" + filepath.Join(dir, rc), "HOME=" + dir} }
	cases := []struct {
		name string
		spec shellSpec
		want bool
	}{
		{"zsh", shellSpec{path: "/usr/bin/zsh"}, true},
		{"fish", shellSpec{path: "/opt/fish/bin/fish"}, true},
		{"nu", shellSpec{path: "/x/nu"}, true},
		{"symlink to zsh", shellSpec{path: link}, true},
		{"dash", shellSpec{path: "/usr/bin/dash"}, false},
		{"sh", shellSpec{path: "/x/sh"}, false},
		{"ksh", shellSpec{path: "/x/ksh"}, false},
		{"unknown", shellSpec{path: "/x/xonsh"}, false},
		{"bash 4.4", shellSpec{path: oldBash, env: env("rc-none")}, false},
		{"bash 5.1", shellSpec{path: newBash, env: env("rc-none")}, true},
		{"bash 5.1 inputrc off", shellSpec{path: newBash, env: env("rc-off")}, false},
		{"bash 5.1 inputrc off then on", shellSpec{path: newBash, env: env("rc-on")}, true},
		{"bash 5.1 --noediting", shellSpec{path: newBash, args: []string{"--noediting", "-i"}, env: env("rc-none")}, false},
	}
	for _, tc := range cases {
		if got := classifyShell(tc.spec); got != tc.want {
			t.Errorf("%s: markerOnly = %v, want %v", tc.name, got, tc.want)
		}
	}
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".inputrc"), "set enable-bracketed-paste 0\n")
	if !inputrcDisablesBracketedPaste([]string{"HOME=" + home}) {
		t.Error("~/.inputrc with enable-bracketed-paste 0 not detected")
	}
}

// --- WaitShellReady against a real pty -------------------------------------

func TestWaitShellReady_ReturnsPromptlyOnCloseOrCancel(t *testing.T) {
	t.Parallel()
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not found")
	}
	for _, tc := range []struct {
		how  string
		want ShellReadiness
	}{{"close", ShellClosed}, {"cancel", ShellCancelled}, {"exit", ShellClosed}} {
		t.Run(tc.how, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			argv := []string{sleepBin, "30"}
			if tc.how == "exit" {
				argv = []string{sleepBin, "0.1"}
			}
			b, err := Spawn(context.Background(), t.TempDir(), argv, nil, "d", "x",
				func(string, ...any) {}, 80, 24)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = b.Close() }()
			res := make(chan ShellReadiness, 1)
			go func() { res <- b.WaitShellReady(ctx, time.Minute) }()
			time.Sleep(100 * time.Millisecond)
			switch tc.how {
			case "close":
				_ = b.Close()
			case "cancel":
				cancel()
			}
			select {
			case r := <-res:
				wantResult(t, r, tc.want)
			case <-time.After(3 * time.Second):
				t.Fatal("WaitShellReady did not return promptly")
			}
		})
	}
}

// rcHelper is run by rc files to reproduce what real rc files do to stdin.
const rcHelper = `import getpass, os, select, sys, termios, time
mode, fd = sys.argv[1], 0
if mode == "tcflush":    # tcflush(TCIFLUSH), as stty/TCSAFLUSH users do
    time.sleep(0.2)
    termios.tcflush(fd, termios.TCIFLUSH)
elif mode == "drain":    # drain typeahead for 0.5s
    end = time.time() + 0.5
    while (left := end - time.time()) > 0:
        if select.select([fd], [], [], left)[0]:
            os.read(fd, 4096)
elif mode == "osc11":    # background-colour query; read the reply raw
    old = termios.tcgetattr(fd)
    new = termios.tcgetattr(fd)
    new[3] &= ~(termios.ICANON | termios.ECHO)
    termios.tcsetattr(fd, termios.TCSANOW, new)
    os.write(1, b"\x1b]11;?\x07")
    if select.select([fd], [], [], 0.3)[0]:
        os.read(fd, 4096)
    termios.tcsetattr(fd, termios.TCSANOW, old)
elif mode == "getpass":  # ssh-add/sudo style: TCSAFLUSH discards typeahead
    with open(sys.argv[2], "w") as f:
        f.write(getpass.getpass("Passphrase: "))
`

// Scenarios. The first five are short; the rest hold the tty for seconds in
// ways that defeat a time-based fallback.
const (
	scPlain      = "plain"
	scSleep      = "sleep1"
	scDrain      = "drain"
	scFlush      = "tcflush"
	scOSC11      = "osc11"
	scReadT2     = "read-t2"     // canonical builtin read with a 2s timeout
	scRawRead    = "read-n1-t3"  // cbreak builtin prompt, 3s timeout
	scOSC11Slow  = "osc11-t1"    // OSC 11 query, builtin raw read, 1s timeout
	scGetpass    = "getpass"     // external passphrase prompt
	scReadPrompt = "read-prompt" // builtin prompt without a timeout
)

// pythonModes are the scenarios rcHelper implements.
var pythonModes = map[string]bool{scDrain: true, scFlush: true, scOSC11: true, scGetpass: true}

var bashBuiltins = map[string]string{
	scDrain:      "read -t 0.5 _junk",
	scOSC11:      `printf '\033]11;?\007'; IFS= read -r -s -t 0.3 -d $'\a' _bg`,
	scReadT2:     "read -t 2 _x",
	scRawRead:    "read -n 1 -t 3 -p 'Start tmux? [y/N] ' _a; echo",
	scOSC11Slow:  `printf '\033]11;?\007'; IFS= read -r -s -t 1 -d $'\a' _bg`,
	scReadPrompt: `read -r -p 'Passphrase: ' _x; printf %s "$_x" > "$PP_OUT"`,
}

var zshBuiltins = map[string]string{
	scDrain:      "read -t 0.5 _junk",
	scOSC11:      `printf '\033]11;?\007'; IFS= read -r -s -t 0.3 -d $'\a' _bg`,
	scReadT2:     "read -t 2 _x",
	scRawRead:    "read -k 1 -t 3 '_a?Update oh-my-zsh? [Y/n] '; echo",
	scOSC11Slow:  `printf '\033]11;?\007'; IFS= read -r -s -t 1 -d $'\a' _bg`,
	scReadPrompt: `read -r '_x?Passphrase: '; printf %s "$_x" > "$PP_OUT"`,
}

type shellKind struct {
	name string
	bin  string
	want ShellReadiness // WaitShellReady's result once the rc file is done
	// builtins: rc bodies per scenario using the shell's own builtins; a
	// missing scenario falls back to the python helper, or is skipped.
	builtins map[string]string
	// residual: scenarios this shell cannot get right (documented on
	// WaitShellReady), skipped here.
	residual map[string]bool
	prelude  string
	setup    func(t *testing.T, dir, bin, rc string) (argv, env []string)
}

func bashSetup(inputrc string, flags ...string) func(t *testing.T, dir, bin, rc string) ([]string, []string) {
	return func(t *testing.T, dir, bin, rc string) ([]string, []string) {
		writeFile(t, filepath.Join(dir, ".bashrc"), rc)
		writeFile(t, filepath.Join(dir, "inputrc"), inputrc)
		argv := append([]string{bin}, flags...)
		return append(argv, "--noprofile", "--rcfile", filepath.Join(dir, ".bashrc"), "-i"),
			[]string{"INPUTRC=" + filepath.Join(dir, "inputrc")}
	}
}

// bash without a raw line editor, or without the marker, can mistake an rc
// builtin `read` with no timeout for its prompt (see WaitShellReady).
var nonMarkerResidual = map[string]bool{scReadPrompt: true}

var shellKinds = []shellKind{
	{name: "bash", bin: "bash", want: ShellReady, builtins: bashBuiltins,
		prelude: "PS1='$ '\n", setup: bashSetup("")},
	// readline with bracketed paste off behaves like bash < 5.1: no marker,
	// so readiness comes from the raw-mode fallback.
	{name: "bash-nobp", bin: "bash", want: ShellReady, builtins: bashBuiltins,
		residual: nonMarkerResidual,
		prelude:  "PS1='$ '\n", setup: bashSetup("set enable-bracketed-paste off\n")},
	// No line editor: a canonical prompt, like dash.
	{name: "bash-noediting", bin: "bash", want: ShellIdleCanonical, builtins: bashBuiltins,
		residual: nonMarkerResidual,
		prelude:  "PS1='$ '\n", setup: bashSetup("", "--noediting")},
	{name: "zsh", bin: "zsh", want: ShellReady, builtins: zshBuiltins,
		prelude: "PS1='%% '\n",
		setup: func(t *testing.T, dir, bin, rc string) ([]string, []string) {
			writeFile(t, filepath.Join(dir, ".zshrc"), rc)
			// -d skips /etc/zsh/z{profile,shrc,login}; ZDOTDIR isolates ~.
			return []string{bin, "-d", "-l"}, []string{"ZDOTDIR=" + dir}
		}},
	{name: "fish", bin: "fish", want: ShellReady,
		builtins: map[string]string{
			scReadPrompt: `read -P 'Passphrase: ' _x; printf %s $_x > $PP_OUT`,
		},
		// fish's `read` runs its line editor, bracketed paste included.
		residual: map[string]bool{scReadPrompt: true},
		prelude:  "set -g fish_greeting ''\n",
		setup: func(t *testing.T, dir, bin, rc string) ([]string, []string) {
			cfg := filepath.Join(dir, "config")
			if err := os.MkdirAll(filepath.Join(cfg, "fish"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(cfg, "fish", "config.fish"), rc)
			return []string{bin, "-l"}, []string{
				"XDG_CONFIG_HOME=" + cfg,
				"XDG_DATA_HOME=" + filepath.Join(dir, "data"),
				"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
			}
		}},
	// dash has no `read -t`: the rc line fails at once, as it would for a
	// user, and the line must still run.
	{name: "dash", bin: "dash", want: ShellIdleCanonical,
		builtins: map[string]string{
			scReadT2:     "read -t 2 _x",
			scReadPrompt: `read -r -p 'Passphrase: ' _x; printf %s "$_x" > "$PP_OUT"`,
		},
		prelude: "PS1='$ '\n",
		setup: func(t *testing.T, dir, bin, rc string) ([]string, []string) {
			writeFile(t, filepath.Join(dir, "rc.sh"), rc)
			return []string{bin, "-i"}, []string{"ENV=" + filepath.Join(dir, "rc.sh")}
		}},
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (o *outputCollector) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.buf)
}

// rcBody returns the rc file body for scenario sc, or false to skip it.
func rcBody(k shellKind, sc, python, helper, ppOut string) (string, bool) {
	switch sc {
	case scPlain:
		return "", true
	case scSleep:
		return "sleep 1", true
	}
	if b, ok := k.builtins[sc]; ok {
		return b, true
	}
	if python == "" || !pythonModes[sc] {
		return "", false
	}
	return python + " " + helper + " " + sc + " " + ppOut, true
}

// shellUnderTest is a spawned shell with its rc scenario.
type shellUnderTest struct {
	b      *Bridge
	out    *outputCollector
	marker string // file the launch line creates
	ppOut  string // file a passphrase scenario writes the answer to
}

func (s *shellUnderTest) launchLine() []byte { return []byte("touch '" + s.marker + "'\n") }

func (s *shellUnderTest) launched(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if _, err := os.Stat(s.marker); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// spawnShell starts shell kind k with rc scenario sc, or skips the test.
func spawnShell(t *testing.T, k shellKind, sc string) *shellUnderTest {
	t.Helper()
	bin, err := exec.LookPath(k.bin)
	if err != nil {
		t.Skipf("%s not installed", k.bin)
	}
	if k.residual[sc] {
		t.Skipf("%s/%s is a documented residual case", k.name, sc)
	}
	python, _ := exec.LookPath("python3")
	dir := t.TempDir()
	helper := filepath.Join(dir, "rchelper.py")
	writeFile(t, helper, rcHelper)
	s := &shellUnderTest{
		out:    &outputCollector{},
		marker: filepath.Join(dir, "LAUNCHED"),
		ppOut:  filepath.Join(dir, "passphrase"),
	}
	body, ok := rcBody(k, sc, python, helper, s.ppOut)
	if !ok {
		t.Skipf("no %s scenario for %s (python3 found: %v)", sc, k.name, python != "")
	}
	argv, extra := k.setup(t, dir, bin, k.prelude+body+"\n")
	env := append([]string{
		"HOME=" + dir,
		"PATH=" + os.Getenv("PATH"),
		"TERM=xterm-256color",
		"SHELL=" + bin,
		"LANG=C.UTF-8",
		"PP_OUT=" + s.ppOut,
	}, extra...)
	s.b, err = Spawn(context.Background(), dir, argv, env, "d", "x", s.out.emit, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.b.Close() })
	return s
}

// gatedMaxWait is far longer than any scenario's rc file.
const gatedMaxWait = 15 * time.Second

// TestWaitShellReady_RealShells types the launch line after WaitShellReady
// into real shells whose rc files sleep, drain stdin, flush it, query the
// terminal, or prompt with a builtin read, and asserts the line ran.
// Shells that are not installed are skipped (CI's dev image installs bash,
// dash, zsh, fish and python3).
func TestWaitShellReady_RealShells(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns real shells")
	}
	for _, k := range shellKinds {
		t.Run(k.name, func(t *testing.T) {
			t.Parallel()
			for _, sc := range []string{scPlain, scSleep, scDrain, scFlush, scOSC11,
				scReadT2, scRawRead, scOSC11Slow} {
				t.Run(sc, func(t *testing.T) {
					t.Parallel()
					s := spawnShell(t, k, sc)
					start := time.Now()
					r := s.b.WaitShellReady(context.Background(), gatedMaxWait)
					took := time.Since(start)
					t.Logf("%s/%s: %v after %v", k.name, sc, r, took.Round(time.Millisecond))
					if r != k.want {
						t.Fatalf("WaitShellReady = %v, want %v", r, k.want)
					}
					if _, err := s.b.Write(s.launchLine()); err != nil {
						t.Fatal(err)
					}
					if !s.launched(5 * time.Second) {
						t.Errorf("launch line written after WaitShellReady (%v after %v) did not run", r, took)
					}
				})
			}
		})
	}
}

// TestWaitShellReady_PromptInRcFile: an rc file prompts for a passphrase,
// either through an external program that discards typeahead (getpass, as
// ssh-add and sudo do) or through the shell's own `read`. WaitShellReady must
// report ShellBusy while the prompt waits, so the caller never types the
// launch line into it, and a typable result once the user has answered.
func TestWaitShellReady_PromptInRcFile(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns real shells")
	}
	for _, k := range shellKinds {
		for _, sc := range []string{scGetpass, scReadPrompt} {
			t.Run(k.name+"/"+sc, func(t *testing.T) {
				t.Parallel()
				s := spawnShell(t, k, sc)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				first := make(chan ShellReadiness, 1)
				go func() { first <- s.b.WaitShellReady(ctx, gatedMaxWait) }()

				deadline := time.Now().Add(5 * time.Second)
				for !strings.Contains(s.out.String(), "Passphrase:") {
					if time.Now().After(deadline) {
						t.Fatalf("no passphrase prompt; output %q", s.out.String())
					}
					time.Sleep(20 * time.Millisecond)
				}
				// Longer than canonicalQuietFor and rawStableFor.
				wantResult(t, s.b.WaitShellReady(ctx, 1500*time.Millisecond), ShellBusy)
				select {
				case r := <-first:
					t.Fatalf("WaitShellReady returned %v while the prompt waited", r)
				default:
				}

				if _, err := s.b.Write([]byte("hunter2\n")); err != nil { // the user answers
					t.Fatal(err)
				}
				select {
				case r := <-first:
					if r != k.want {
						t.Fatalf("after the answer WaitShellReady = %v, want %v", r, k.want)
					}
				case <-time.After(gatedMaxWait):
					t.Fatal("WaitShellReady did not return after the answer")
				}
				if _, err := s.b.Write(s.launchLine()); err != nil {
					t.Fatal(err)
				}
				if !s.launched(5 * time.Second) {
					t.Error("launch line did not run after the prompt was answered")
				}
				if got, _ := os.ReadFile(s.ppOut); string(got) != "hunter2" {
					t.Errorf("prompt received %q, want %q", got, "hunter2")
				}
			})
		}
	}
}

// TestWaitShellReady_NegativeControl proves the scenarios above are
// meaningful: written immediately after spawn, as before the gate, the
// launch line is echoed but swallowed by an rc file that drains, flushes,
// reads a terminal reply or prompts.
func TestWaitShellReady_NegativeControl(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns real shells")
	}
	k := shellKinds[0] // bash
	for _, sc := range []string{scDrain, scFlush, scOSC11, scReadT2, scRawRead, scOSC11Slow} {
		t.Run(k.name+"/"+sc, func(t *testing.T) {
			t.Parallel()
			s := spawnShell(t, k, sc)
			if _, err := s.b.Write(s.launchLine()); err != nil {
				t.Fatal(err)
			}
			// Let the rc file run and the prompt come up before judging.
			s.b.WaitShellReady(context.Background(), gatedMaxWait)
			if s.launched(500 * time.Millisecond) {
				t.Errorf("launch line written immediately survived the %s rc file; "+
					"the gated test no longer proves anything", sc)
			}
		})
	}
}
