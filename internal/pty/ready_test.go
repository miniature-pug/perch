package pty

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

func fakeBridge(pid, pgrp int, canonical bool) (*Bridge, *readyGate) {
	f := &fakeTTY{pgrp: pgrp, canonical: canonical}
	g := newReadyGate(pid, f.probe)
	return &Bridge{gate: g}, g
}

func TestWaitShellReady_MarkerWithForegroundShell(t *testing.T) {
	t.Parallel()
	b, g := fakeBridge(42, 42, false)
	go func() {
		time.Sleep(50 * time.Millisecond)
		g.observe([]byte("$ \x1b[?2004h"))
	}()
	start := time.Now()
	if !b.WaitShellReady(context.Background(), 5*time.Second) {
		t.Fatal("not ready")
	}
	if d := time.Since(start); d > rawStableFor/2 {
		t.Fatalf("marker path took %v; looks like the raw-mode fallback fired", d)
	}
}

func TestWaitShellReady_MarkerIgnoredWhileOtherGroupForeground(t *testing.T) {
	t.Parallel()
	b, g := fakeBridge(42, 99, false) // e.g. a TUI the rc file runs
	g.observe([]byte("\x1b[?2004h"))
	if b.WaitShellReady(context.Background(), 300*time.Millisecond) {
		t.Fatal("ready although the shell is not the foreground process group")
	}
}

func TestWaitShellReady_MarkerIgnoredInCanonicalMode(t *testing.T) {
	t.Parallel()
	b, g := fakeBridge(42, 42, true) // rc printed the marker, then `read -t`
	g.observe([]byte("\x1b[?2004h"))
	if b.WaitShellReady(context.Background(), 300*time.Millisecond) {
		t.Fatal("ready although the tty is in canonical mode")
	}
}

func TestWaitShellReady_RawModeFallback(t *testing.T) {
	t.Parallel()
	b, _ := fakeBridge(42, 42, false)
	start := time.Now()
	if !b.WaitShellReady(context.Background(), 5*time.Second) {
		t.Fatal("fallback never fired")
	}
	if d := time.Since(start); d < rawStableFor {
		t.Fatalf("fallback fired after %v, before %v", d, rawStableFor)
	}
}

func TestWaitShellReady_CanonicalShellRunsToCap(t *testing.T) {
	t.Parallel()
	b, _ := fakeBridge(42, 42, true) // dash at its prompt
	start := time.Now()
	if b.WaitShellReady(context.Background(), 200*time.Millisecond) {
		t.Fatal("ready in canonical mode")
	}
	if d := time.Since(start); d < 200*time.Millisecond || d > 2*time.Second {
		t.Fatalf("returned after %v, want about the 200ms cap", d)
	}
}

func TestWaitShellReady_NoProcessReturnsFalse(t *testing.T) {
	b := NewBridgeForTest(func() error { return nil })
	if b.WaitShellReady(context.Background(), time.Minute) {
		t.Fatal("test bridge reported ready")
	}
}

// --- WaitShellReady against a real pty -------------------------------------

func TestWaitShellReady_ReturnsPromptlyOnCloseOrCancel(t *testing.T) {
	t.Parallel()
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not found")
	}
	for _, how := range []string{"close", "cancel", "exit"} {
		t.Run(how, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			argv := []string{sleepBin, "30"}
			if how == "exit" {
				argv = []string{sleepBin, "0.1"}
			}
			b, err := Spawn(context.Background(), t.TempDir(), argv, nil, "d", "x",
				func(string, ...any) {}, 80, 24)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = b.Close() }()
			res := make(chan bool, 1)
			go func() { res <- b.WaitShellReady(ctx, time.Minute) }()
			time.Sleep(100 * time.Millisecond)
			switch how {
			case "close":
				_ = b.Close()
			case "cancel":
				cancel()
			}
			select {
			case ready := <-res:
				if ready {
					t.Fatal("reported ready")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("WaitShellReady did not return promptly")
			}
		})
	}
}

// rcHelper is run by rc files to reproduce what real rc files do to stdin.
const rcHelper = `import os, select, sys, termios, time
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
`

type shellKind struct {
	name string
	bin  string
	// marker: the line editor enables bracketed paste, so WaitShellReady
	// reports ready (via the marker or, for bash-nobp, the raw-mode fallback).
	// Without it (dash) the gate always runs to maxWait.
	ready bool
	// builtins: rc bodies per scenario using the shell's own builtins; a
	// missing scenario falls back to the python helper.
	builtins map[string]string
	prelude  string
	setup    func(t *testing.T, dir, bin, rc string) (argv, env []string)
}

const (
	scPlain = "plain"
	scSleep = "sleep1"
	scDrain = "drain"
	scFlush = "tcflush"
	scOSC11 = "osc11"
)

var bashBuiltins = map[string]string{
	scDrain: "read -t 0.5 _junk",
	scOSC11: `printf '\033]11;?\007'; IFS= read -r -s -t 0.3 -d $'\a' _bg`,
}

func bashSetup(inputrc string) func(t *testing.T, dir, bin, rc string) ([]string, []string) {
	return func(t *testing.T, dir, bin, rc string) ([]string, []string) {
		writeFile(t, filepath.Join(dir, ".bashrc"), rc)
		writeFile(t, filepath.Join(dir, "inputrc"), inputrc)
		return []string{bin, "--noprofile", "--rcfile", filepath.Join(dir, ".bashrc"), "-i"},
			[]string{"INPUTRC=" + filepath.Join(dir, "inputrc")}
	}
}

var shellKinds = []shellKind{
	{name: "bash", bin: "bash", ready: true, builtins: bashBuiltins,
		prelude: "PS1='$ '\n", setup: bashSetup("")},
	// readline with bracketed paste off behaves like bash < 5.1: no marker,
	// so readiness comes from the raw-mode fallback.
	{name: "bash-nobp", bin: "bash", ready: true, builtins: bashBuiltins,
		prelude: "PS1='$ '\n", setup: bashSetup("set enable-bracketed-paste off\n")},
	{name: "zsh", bin: "zsh", ready: true, builtins: bashBuiltins,
		prelude: "PS1='%% '\n",
		setup: func(t *testing.T, dir, bin, rc string) ([]string, []string) {
			writeFile(t, filepath.Join(dir, ".zshrc"), rc)
			// -d skips /etc/zsh/z{profile,shrc,login}; ZDOTDIR isolates ~.
			return []string{bin, "-d", "-l"}, []string{"ZDOTDIR=" + dir}
		}},
	{name: "fish", bin: "fish", ready: true,
		prelude: "set -g fish_greeting ''\n",
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
	{name: "dash", bin: "dash", ready: false,
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

// rcBody returns the rc file body for scenario sc, or "" to skip it.
func rcBody(k shellKind, sc, python, helper string) (string, bool) {
	switch sc {
	case scPlain:
		return "", true
	case scSleep:
		return "sleep 1", true
	}
	if b, ok := k.builtins[sc]; ok {
		return b, true
	}
	if python == "" {
		return "", false
	}
	return python + " " + helper + " " + sc, true
}

type launchRun struct {
	ready   bool
	took    time.Duration
	present bool
}

// launch spawns shell kind k with rc scenario sc and types a launch line that
// creates a marker file, after WaitShellReady unless immediate is set.
func launch(t *testing.T, k shellKind, bin, sc string, immediate bool) launchRun {
	t.Helper()
	python, _ := exec.LookPath("python3")
	dir := t.TempDir()
	helper := filepath.Join(dir, "rchelper.py")
	writeFile(t, helper, rcHelper)
	body, ok := rcBody(k, sc, python, helper)
	if !ok {
		t.Skip("python3 not found")
	}
	argv, extra := k.setup(t, dir, bin, k.prelude+body+"\n")
	env := append([]string{
		"HOME=" + dir,
		"PATH=" + os.Getenv("PATH"),
		"TERM=xterm-256color",
		"SHELL=" + bin,
		"LANG=C.UTF-8",
	}, extra...)

	b, err := Spawn(context.Background(), dir, argv, env, "d", "x", func(string, ...any) {}, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	maxWait := 5 * time.Second
	if !k.ready {
		maxWait = 2500 * time.Millisecond // longer than any scenario's rc
	}
	marker := filepath.Join(dir, "LAUNCHED")
	line := []byte("touch '" + marker + "'\n")
	var r launchRun
	start := time.Now()
	if immediate {
		if _, err := b.Write(line); err != nil {
			t.Fatal(err)
		}
		// Let the rc file run and the prompt come up before judging.
		b.WaitShellReady(context.Background(), maxWait)
		r.took = time.Since(start)
		time.Sleep(500 * time.Millisecond)
	} else {
		r.ready = b.WaitShellReady(context.Background(), maxWait)
		r.took = time.Since(start)
		if _, err := b.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			r.present = true
			break
		}
		if immediate || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return r
}

// TestWaitShellReady_RealShells types the launch line after WaitShellReady
// into real shells whose rc files sleep, drain stdin, flush it, or query the
// terminal, and asserts the line ran. Shells that are not installed are
// skipped.
func TestWaitShellReady_RealShells(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns real shells")
	}
	for _, k := range shellKinds {
		t.Run(k.name, func(t *testing.T) {
			t.Parallel()
			bin, err := exec.LookPath(k.bin)
			if err != nil {
				t.Skipf("%s not installed", k.bin)
			}
			for _, sc := range []string{scPlain, scSleep, scDrain, scFlush, scOSC11} {
				t.Run(sc, func(t *testing.T) {
					t.Parallel()
					r := launch(t, k, bin, sc, false)
					t.Logf("%s/%s: ready=%v after %v", k.name, sc, r.ready, r.took.Round(time.Millisecond))
					if r.ready != k.ready {
						t.Errorf("WaitShellReady = %v, want %v", r.ready, k.ready)
					}
					if !r.present {
						t.Errorf("launch line written after WaitShellReady (ready=%v, %v) did not run",
							r.ready, r.took)
					}
				})
			}
		})
	}
}

// TestWaitShellReady_NegativeControl proves the scenarios above are
// meaningful: written immediately after spawn, as before the gate, the
// launch line is echoed but swallowed by an rc file that drains, flushes or
// reads a terminal reply from stdin.
func TestWaitShellReady_NegativeControl(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns real shells")
	}
	for _, k := range shellKinds[:1] { // bash
		bin, err := exec.LookPath(k.bin)
		if err != nil {
			t.Skipf("%s not installed", k.bin)
		}
		for _, sc := range []string{scDrain, scFlush, scOSC11} {
			t.Run(k.name+"/"+sc, func(t *testing.T) {
				t.Parallel()
				if r := launch(t, k, bin, sc, true); r.present {
					t.Errorf("launch line written immediately survived the %s rc file; "+
						"the gated test no longer proves anything", sc)
				}
			})
		}
	}
}
