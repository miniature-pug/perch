package pty

import (
	"bytes"
	"context"
	"errors"
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
	// rawStableFor is how long the shell must hold the tty in non-canonical
	// mode, as its foreground process group, before the fallback (for line
	// editors that never enable bracketed paste) treats it as ready. 250ms
	// misfired during an rc file's `read -t`; 750ms did not.
	rawStableFor = 750 * time.Millisecond
)

// bpPrefix is the DECSET/DECRST 2004 (bracketed paste) prefix. The final
// byte is 'h' (enable) or 'l' (disable). readline >= 8.1 (bash 5.1+), zle,
// fish and reedline (nushell) enable it when the line editor starts reading
// a line, and disable it when the line is accepted.
var bpPrefix = []byte("\x1b[?2004")

const bpLen = 8 // len(bpPrefix) + the final byte

// errTTYUnsupported is returned by ttyState where the foreground process
// group and termios cannot be read from the pty master.
var errTTYUnsupported = errors.New("pty: tty state unsupported on this platform")

// readyGate tracks, from the output stream, whether the most recent
// bracketed-paste toggle enabled it, and lets WaitShellReady probe the tty.
//
// observe runs only on the pump goroutine, so tail/tailLen need no lock.
type readyGate struct {
	pid   int
	probe func() (pgrp int, canonical bool, err error)

	bpOn    atomic.Bool   // last complete 2004 toggle seen was 'h'
	scanOff atomic.Bool   // set once a WaitShellReady returns: stop scanning
	wake    chan struct{} // cap 1; poked when bpOn turns on
	closed  chan struct{} // closed on Bridge.Close or pty EOF
	once    sync.Once

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
// split across the previous chunk and this one. It never modifies p, does not
// allocate, and is a single atomic load once scanning is switched off.
func (g *readyGate) observe(p []byte) {
	if len(p) == 0 || g.scanOff.Load() {
		return
	}
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

// WaitShellReady blocks until the spawned shell's line editor is reading
// input, so that a line written next is read by the shell's prompt instead of
// by an rc file. It returns true when either:
//
//   - the most recent bracketed-paste toggle in the output enabled it
//     (ESC[?2004h: bash 5.1+, zsh, fish, nushell), the shell is the tty's
//     foreground process group (TIOCGPGRP on the master equals the shell's
//     pid, which is its pgid as session leader), and the tty is in
//     non-canonical mode, as every such line editor sets it; or
//   - fallback, for line editors that never enable bracketed paste (bash
//     before 5.1, or `set enable-bracketed-paste off`): the shell has been the
//     foreground group with ICANON off for rawStableFor (750ms).
//
// It returns false when maxWait elapses, ctx is done, or the bridge is closed
// or its shell exits; the caller should then write anyway, which is never
// worse than writing without waiting. It also returns false at once for a
// bridge without a process (NewBridgeForTest).
//
// A shell without a line editor that leaves the tty in canonical mode at its
// prompt (dash, `bash --noediting`, sh) satisfies neither condition and
// always runs to maxWait. Where the platform cannot read the tty state
// (non-Linux builds), only the bracketed-paste marker counts.
//
// Residual false positives, where it returns true before the prompt: an rc
// file whose shell builtin itself enables bracketed paste and reads raw input
// (`read -e` in bash 5.1+, `vared` in zsh), or one that prints ESC[?2004h
// itself and then runs a non-canonical builtin read; and, for the fallback
// only, an rc file holding the shell in raw mode for 750ms or more (a long
// `read -n` loop). A program the rc file runs in its own process group
// (`fzf`, `vim`, `tmux new-session`) is never the foreground shell, and a
// well-behaved one disables bracketed paste on exit, so it does not trigger
// readiness. `exec tmux` keeps the shell's pid: readiness then follows the
// shell inside tmux, so the line reaches that shell.
//
// WaitShellReady polls every readyPollInterval only while it waits and starts
// no goroutines. It is meant to be called once, right after Spawn: once it
// returns, the bridge stops scanning output for the marker.
func (b *Bridge) WaitShellReady(ctx context.Context, maxWait time.Duration) bool {
	g := b.gate
	if g == nil || maxWait <= 0 {
		return false
	}
	defer g.scanOff.Store(true)
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	tick := time.NewTicker(readyPollInterval)
	defer tick.Stop()
	var rawSince time.Time
	for {
		select {
		case <-ctx.Done():
			return false
		case <-g.closed:
			return false
		default:
		}
		pgrp, canonical, err := g.probe()
		switch {
		case errors.Is(err, errTTYUnsupported):
			if g.bpOn.Load() {
				return true
			}
		case err != nil:
			return false // pty closed
		case pgrp == g.pid && !canonical:
			if g.bpOn.Load() {
				return true
			}
			now := time.Now()
			if rawSince.IsZero() {
				rawSince = now
			} else if now.Sub(rawSince) >= rawStableFor {
				return true
			}
		default:
			rawSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return false
		case <-g.closed:
			return false
		case <-timer.C:
			return false
		case <-g.wake:
		case <-tick.C:
		}
	}
}
