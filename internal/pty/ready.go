package pty

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Shell readiness.
//
// A login shell reads its rc files before its line editor starts. Anything
// typed into the pty before then sits in the tty input queue, already echoed
// by the line discipline, where an rc file can eat it: `read -t` drains,
// tcflush/TCSAFLUSH, an OSC 11 colour query that reads the reply, a
// passphrase prompt. The user then sees the launch line on screen and nothing
// happens. WaitShellReady lets a caller hold the launch line back until the
// shell's own line editor is reading.

const (
	// readyPollInterval is how often WaitShellReady samples the tty while it
	// waits. Polling only happens inside WaitShellReady.
	readyPollInterval = 25 * time.Millisecond
	// rawStableFor is how long a shell whose line editor may never enable
	// bracketed paste (bash < 5.1, ksh, unknown shells) must hold the tty in
	// non-canonical mode, as its foreground process group, to count as
	// ready. A cbreak builtin read in an rc file (`read -n 1`, `read -d
	// $'\a'`, zsh `read -k`) looks the same for as long as it lasts, so this
	// fallback is disabled for shells known to emit the marker, and vetoed
	// while the shell waits with a timeout (`-t`). A plain `read -t` stays
	// canonical and never trips it.
	rawStableFor = 750 * time.Millisecond
	// canonicalQuietFor is how long a shell without a raw line editor (dash,
	// sh, `bash --noediting`) must own a canonical tty, with no child in its
	// process group and no pty output, to count as idle at its prompt.
	canonicalQuietFor = time.Second
	// versionProbeTimeout bounds the one-off `bash --version` probe.
	versionProbeTimeout = 2 * time.Second
)

// ShellReadiness is WaitShellReady's verdict.
type ShellReadiness int

const (
	// ShellReady: the shell's line editor is reading a line. Safe to type.
	ShellReady ShellReadiness = iota + 1
	// ShellIdleCanonical: a shell without a raw line editor (dash, sh,
	// `bash --noediting`) has owned a canonical tty, quiet, for
	// canonicalQuietFor. Safe to type, except for the residual case of an rc
	// file blocked in the shell's own builtin `read` (see WaitShellReady).
	ShellIdleCanonical
	// ShellBusy: maxWait elapsed while something else held the tty: a
	// program the rc file runs (a passphrase prompt, a TUI), a builtin
	// prompt (`read -n 1`), or a shell that never showed a ready line
	// editor. The caller must NOT type: the line could land in a prompt.
	ShellBusy
	// ShellClosed: the bridge was closed or its shell exited.
	ShellClosed
	// ShellCancelled: ctx was done first.
	ShellCancelled
)

// CanType reports whether a line typed now reaches the shell's prompt.
func (r ShellReadiness) CanType() bool { return r == ShellReady || r == ShellIdleCanonical }

func (r ShellReadiness) String() string {
	switch r {
	case ShellReady:
		return "ready"
	case ShellIdleCanonical:
		return "idle-canonical"
	case ShellBusy:
		return "busy"
	case ShellClosed:
		return "closed"
	case ShellCancelled:
		return "cancelled"
	}
	return "ShellReadiness(" + strconv.Itoa(int(r)) + ")"
}

// bpPrefix is the DECSET/DECRST 2004 (bracketed paste) prefix. The final
// byte is 'h' (enable) or 'l' (disable). readline >= 8.1 (bash 5.1+), zle,
// fish and reedline (nushell) enable it when the line editor starts reading
// a line, and disable it when the line is accepted.
var bpPrefix = []byte("\x1b[?2004")

const bpLen = 8 // len(bpPrefix) + the final byte

// errTTYUnsupported is returned by ttyState where the foreground process
// group and termios cannot be read from the pty master.
var errTTYUnsupported = errors.New("pty: tty state unsupported on this platform")

// shellSpec identifies the spawned program for classifyShell.
type shellSpec struct {
	path string   // resolved executable (exec.Cmd.Path)
	args []string // argv[1:]
	env  []string // the child's environment
}

// readyGate tracks, from the output stream, whether the most recent
// bracketed-paste toggle enabled it, and lets WaitShellReady probe the tty.
//
// observe runs only on the pump goroutine, so tail/tailLen need no lock.
type readyGate struct {
	pid   int
	probe func() (pgrp int, canonical bool, err error)
	spec  shellSpec

	traitsOnce sync.Once
	markerOnly bool // set by traitsOnce: see classifyShell

	bpOn   atomic.Bool   // last complete 2004 toggle seen was 'h'
	chunks atomic.Uint64 // output chunks seen, for the quiet check
	wake   chan struct{} // cap 1; poked when bpOn turns on
	closed chan struct{} // closed on Bridge.Close or pty EOF
	once   sync.Once

	tail    [bpLen - 1]byte // last bytes of the stream, for a split toggle
	tailLen int
}

func newReadyGate(pid int, probe func() (int, bool, error)) *readyGate {
	return &readyGate{
		pid:    pid,
		probe:  probe,
		wake:   make(chan struct{}, 1),
		closed: make(chan struct{}),
	}
}

func (g *readyGate) markClosed() {
	g.once.Do(func() { close(g.closed) })
}

// toggleAt classifies the 2004 toggle starting at p[i]: 1 enable, -1
// disable, 0 neither (incomplete, or a combined DECSET such as ?2004;1h).
func toggleAt(p []byte, i int) int {
	if i+bpLen > len(p) {
		return 0
	}
	switch p[i+bpLen-1] {
	case 'h':
		return 1
	case 'l':
		return -1
	}
	return 0
}

// lastToggle returns the last complete toggle in p (see toggleAt), or 0.
func lastToggle(p []byte) int {
	for {
		i := bytes.LastIndex(p, bpPrefix)
		if i < 0 {
			return 0
		}
		if s := toggleAt(p, i); s != 0 {
			return s
		}
		p = p[:i]
	}
}

// observe scans one output chunk for bracketed-paste toggles, including one
// split across the previous chunk and this one. It never modifies p and does
// not allocate.
func (g *readyGate) observe(p []byte) {
	if len(p) == 0 {
		return
	}
	g.chunks.Add(1)
	state := 0
	if g.tailLen > 0 {
		// A toggle straddling the boundary starts inside the tail and ends
		// within the first bpLen-1 bytes of p.
		var joint [2 * (bpLen - 1)]byte
		n := copy(joint[:], g.tail[:g.tailLen])
		n += copy(joint[n:], p)
		if i := bytes.Index(joint[:n], bpPrefix); i >= 0 && i < g.tailLen {
			state = toggleAt(joint[:n], i)
		}
	}
	if s := lastToggle(p); s != 0 {
		state = s
	}
	switch state {
	case 1:
		if !g.bpOn.Swap(true) {
			select {
			case g.wake <- struct{}{}:
			default:
			}
		}
	case -1:
		g.bpOn.Store(false)
	}
	// Keep the last bpLen-1 bytes of the stream.
	if len(p) >= len(g.tail) {
		g.tailLen = copy(g.tail[:], p[len(p)-len(g.tail):])
		return
	}
	keep := min(g.tailLen, len(g.tail)-len(p))
	copy(g.tail[:], g.tail[g.tailLen-keep:g.tailLen])
	g.tailLen = keep + copy(g.tail[keep:], p)
}

// WaitShellReady blocks until the spawned shell is reading its first command
// line, so that a line written next reaches the shell's prompt instead of an
// rc file. It returns:
//
//   - ShellReady when the most recent bracketed-paste toggle in the output
//     enabled it (ESC[?2004h), the shell is the tty's foreground process
//     group (TIOCGPGRP on the master equals the shell's pid, its pgid as
//     session leader) with no child of its own in that group, and the tty is
//     non-canonical, as every such line editor sets it. For a shell whose
//     line editor may never send the marker (bash < 5.1, ksh, unknown
//     shells, bash with enable-bracketed-paste off in its inputrc) the same
//     foreground state held in non-canonical mode for rawStableFor (750ms)
//     also counts, unless the shell is visibly in a builtin read (see
//     awaitsRcInput: a timed pselect6/ppoll). For zsh, fish, nu and bash >=
//     5.1 only the marker counts.
//   - ShellIdleCanonical when a shell expected to have no raw line editor
//     (any shell not in the marker set: dash, sh, ksh; and `bash
//     --noediting`) has been the foreground group in canonical mode, with no
//     child in its group and no output, for canonicalQuietFor (1s), and is
//     not visibly in a builtin read (a timed wait, or dash's 1-byte reads).
//     For the marker shells canonical mode never counts: their prompt is
//     raw, and a canonical shell is in a builtin `read` from an rc file.
//   - ShellBusy when maxWait elapses first. Do not type then: a prompt may
//     own the tty.
//   - ShellClosed when the bridge is closed or its shell exits, and
//     ShellCancelled when ctx is done.
//
// A bridge without a process (NewBridgeForTest) reports ShellReady at once,
// so app tests that capture writes keep working.
//
// Residual cases:
//   - Non-marker shells, where the syscall probe cannot tell: bash < 5.1 or
//     bash --noediting blocked in an rc builtin `read` without a timeout
//     gets ShellIdleCanonical after 1s (old bash: ShellReady after 750ms for
//     `read -n 1`); ksh and other shells with no -t-style probe likewise.
//     The probe needs Linux and a readable /proc/<pid>/syscall (it is, for
//     our own child, unless ptrace is locked down); a shell with TMOUT set
//     waits with a timeout at its prompt and gets ShellBusy at maxWait.
//   - Marker shells: an rc builtin that itself enables bracketed paste and
//     reads raw input (`read -e` in bash 5.1+, `vared` in zsh, any `read`
//     in fish) gets ShellReady. A marker shell whose editor is off in a way
//     not detected here (zsh `unsetopt zle`, bracketed paste disabled in an
//     $include'd or $if-guarded inputrc) never becomes ready and gets
//     ShellBusy at maxWait.
//   - A program the rc file runs (`fzf`, `ssh-add`, `vim`) is in its own
//     process group or is a child in the shell's, so it never counts as
//     ready. `exec tmux` keeps the shell's pid: readiness then follows the
//     shell inside tmux, so the line reaches that shell.
//
// WaitShellReady polls every readyPollInterval only while it waits and starts
// no goroutines. It may be called more than once, also concurrently (for a
// "retype" after a ShellBusy), and is cheap when the shell is ready.
func (b *Bridge) WaitShellReady(ctx context.Context, maxWait time.Duration) ShellReadiness {
	g := b.gate
	if g == nil {
		return ShellReady
	}
	g.traitsOnce.Do(func() { g.markerOnly = classifyShell(g.spec) })
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	tick := time.NewTicker(readyPollInterval)
	defer tick.Stop()
	var rawSince, quietSince time.Time
	var lastChunks uint64
	for {
		select {
		case <-ctx.Done():
			return ShellCancelled
		case <-g.closed:
			return ShellClosed
		default:
		}
		now := time.Now()
		chunks := g.chunks.Load()
		pgrp, canonical, err := g.probe()
		owns := err == nil && pgrp == g.pid && !childInGroup(g.pid)
		switch {
		case errors.Is(err, errTTYUnsupported):
			if g.bpOn.Load() {
				return ShellReady
			}
		case err != nil:
			return ShellClosed
		case owns && !canonical:
			quietSince = time.Time{}
			if g.bpOn.Load() {
				return ShellReady
			}
			if g.markerOnly || awaitsRcInput(g.pid, false) {
				rawSince = time.Time{}
				break
			}
			if rawSince.IsZero() {
				rawSince = now
			} else if now.Sub(rawSince) >= rawStableFor {
				return ShellReady
			}
		case owns && canonical && !g.markerOnly:
			rawSince = time.Time{}
			if awaitsRcInput(g.pid, true) {
				quietSince = time.Time{}
				break
			}
			if quietSince.IsZero() || chunks != lastChunks {
				quietSince = now
			} else if now.Sub(quietSince) >= canonicalQuietFor {
				return ShellIdleCanonical
			}
		default:
			rawSince, quietSince = time.Time{}, time.Time{}
		}
		lastChunks = chunks
		select {
		case <-ctx.Done():
			return ShellCancelled
		case <-g.closed:
			return ShellClosed
		case <-timer.C:
			return ShellBusy
		case <-g.wake:
		case <-tick.C:
		}
	}
}

// markerShells always enable bracketed paste when their line editor reads.
var markerShells = map[string]bool{"zsh": true, "fish": true, "nu": true}

// classifyShell reports whether spec's line editor is known to enable
// bracketed paste whenever it reads a line, so that WaitShellReady trusts
// only the marker for it. That holds for zsh, fish and nu, and for bash >=
// 5.1 unless it runs with --noediting or its inputrc turns
// enable-bracketed-paste off.
func classifyShell(spec shellSpec) bool {
	path := spec.path
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	name := filepath.Base(path)
	if markerShells[name] {
		return true
	}
	if name != "bash" {
		return false
	}
	for _, a := range spec.args {
		if a == "--noediting" {
			return false
		}
	}
	return bashHasBracketedPaste(path) && !inputrcDisablesBracketedPaste(spec.env)
}

var (
	bashProbeMu    sync.Mutex
	bashProbeCache = map[string]bool{}
	bashVersionRE  = regexp.MustCompile(`version (\d+)\.(\d+)`)
)

// bashHasBracketedPaste reports whether the bash at path is 5.1 or later,
// whose readline enables bracketed paste by default. It runs `bash
// --version` once per path and caches the answer.
func bashHasBracketedPaste(path string) bool {
	bashProbeMu.Lock()
	defer bashProbeMu.Unlock()
	if v, ok := bashProbeCache[path]; ok {
		return v
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version") //nolint:gosec // the shell the caller spawned
	cmd.Env = []string{"LC_ALL=C"}
	out, err := cmd.Output()
	v := false
	if m := bashVersionRE.FindSubmatch(out); err == nil && m != nil {
		major, _ := strconv.Atoi(string(m[1]))
		minor, _ := strconv.Atoi(string(m[2]))
		v = major > 5 || (major == 5 && minor >= 1)
	}
	bashProbeCache[path] = v
	return v
}

// envValue returns the last value of key in env, as exec uses it, or
// os.Getenv(key) for a nil env (the child inherits ours).
func envValue(env []string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// inputrcDisablesBracketedPaste reports whether the inputrc readline would
// load ($INPUTRC, else ~/.inputrc, else /etc/inputrc) sets
// enable-bracketed-paste to anything but on. It ignores $include and $if.
func inputrcDisablesBracketedPaste(env []string) bool {
	candidates := []string{envValue(env, "INPUTRC")}
	if candidates[0] == "" {
		candidates = nil
		if home := envValue(env, "HOME"); home != "" {
			candidates = append(candidates, filepath.Join(home, ".inputrc"))
		}
		candidates = append(candidates, "/etc/inputrc")
	}
	for _, path := range candidates {
		f, err := os.Open(path) //nolint:gosec // readline's own config file
		if err != nil {
			continue
		}
		off := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 3 && fields[0] == "set" && strings.EqualFold(fields[1], "enable-bracketed-paste") {
				v := strings.ToLower(fields[2])
				off = v != "on" && v != "1"
			}
		}
		_ = f.Close()
		return off
	}
	return false
}
