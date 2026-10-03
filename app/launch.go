package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	fspkg "github.com/miniature-pug/perch/internal/fs"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/safe"
)

const (
	// defaultTerm is the TERM a pane gets when the process environment has
	// none, or "dumb". perch's terminal is xterm.js, so this is right whatever
	// the launching environment says. A desktop-entry launch has no TERM at
	// all, and readline then omits bracketed paste and the shell looks busy.
	defaultTerm = "xterm-256color"
	// defaultColorTerm accompanies a defaulted TERM, unless COLORTERM is set.
	defaultColorTerm = "truecolor"

	// launchKillLine (Ctrl-U) precedes the typed launch line. It discards any
	// keys the user typed into the pane while the gate waited: bash and zsh
	// (emacs and vi modes), fish and dash (canonical VKILL) all treat it as
	// kill-line.
	launchKillLine = "\x15"

	// retypeMaxWait is how long RetypeLaunch waits for the shell to look
	// ready before it types anyway: the user asked explicitly and can see
	// the terminal.
	retypeMaxWait = 3 * time.Second

	launchBusyTitle = "Agent didn't start"
	launchBusyBody  = "The shell is still busy (for example a prompt in your shell startup files). " +
		"Answer it, then use Retype launch."
	// launchRetypeAction is the notify "action" that offers Retype launch.
	launchRetypeAction = "retype-launch"
)

// launchReadyMaxWait caps how long OpenWorkspace waits for the login shell to
// reach its prompt before it gives up on typing the agent launch line. It is
// long because an rc-file prompt (ssh-add passphrase, "update? [Y/n]") holds
// the shell until the user answers it, and the launch then follows by
// itself. A variable so tests can shorten it.
var launchReadyMaxWait = 90 * time.Second

// withTerm returns env with a usable TERM: unchanged when TERM is set to
// something other than "dumb", else with TERM=xterm-256color (and COLORTERM
// when unset). env must be an explicit environment, not nil.
func withTerm(env []string) []string {
	term, colorTerm := "", ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "TERM="); ok {
			term = v
		} else if v, ok := strings.CutPrefix(kv, "COLORTERM="); ok {
			colorTerm = v
		}
	}
	if term != "" && term != "dumb" {
		return env
	}
	inject := []string{"TERM=" + defaultTerm}
	if colorTerm == "" {
		inject = append(inject, "COLORTERM="+defaultColorTerm)
	}
	return mergeEnv(env, inject, nil)
}

// watcherSlot hands the fs watcher from the goroutine that starts it (the
// start walks the tree, which can take seconds) to the workspace's cancel
// function, which may run before the walk is over. Whichever side comes
// second closes the watcher.
type watcherSlot struct {
	mu     sync.Mutex
	w      *fspkg.Watcher
	closed bool
}

// set stores w, or closes it at once when the slot was already closed.
func (s *watcherSlot) set(w *fspkg.Watcher) {
	if w == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = w.Close()
		return
	}
	s.w = w
	s.mu.Unlock()
}

// close closes the stored watcher, and any later one handed to set.
func (s *watcherSlot) close() {
	s.mu.Lock()
	s.closed = true
	w := s.w
	s.w = nil
	s.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

// launchState is one open of a workspace's agent pane: the launch line to
// type, the bridge it goes to and whether the agent has reported in.
type launchState struct {
	ctx context.Context // the open's wctx
	br  *internalpty.Bridge
	cmd string

	started     chan struct{} // closed on the agent's first event
	startedOnce sync.Once

	mu    sync.Mutex // serialises writes and guards typed
	typed bool       // the launch line was typed (auto or retype)
}

func newLaunchState(ctx context.Context, br *internalpty.Bridge, cmd string) *launchState {
	return &launchState{ctx: ctx, br: br, cmd: cmd, started: make(chan struct{})}
}

func (l *launchState) markStarted() { l.startedOnce.Do(func() { close(l.started) }) }

func (l *launchState) hasStarted() bool {
	select {
	case <-l.started:
		return true
	default:
		return false
	}
}

// typeLine writes the launch line (after Ctrl-U) unless this open was
// cancelled or the agent already started. The automatic path (force false)
// also yields to a retype that already typed it; a retype always types.
func (l *launchState) typeLine(force bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx.Err() != nil || l.hasStarted() || (l.typed && !force) {
		return false
	}
	l.typed = true
	_, _ = l.br.Write([]byte(launchKillLine + l.cmd))
	return true
}

// currentLaunch returns the launch state of workspace id, if its agent pane is
// open and l is still the pane's bridge.
func (a *App) currentLaunch(id string) *launchState {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.launches[id]
	if l == nil || a.bridges[paneIDFor(id)] != l.br {
		return nil
	}
	return l
}

// typeLaunchWhenReady types the launch line into the agent pane once its
// login shell is at its prompt. Typed earlier, the line is echoed but can be
// swallowed by an rc file that drains or flushes stdin (read -t, tcflush, an
// OSC 11 query) or answer an rc prompt (ssh-add). It never types while
// something else may own the tty, and it does not outlive the open: l.ctx is
// cancelled by a reopen, CloseWorkspace and shutdown.
func (a *App) typeLaunchWhenReady(l *launchState, id string, maxWait time.Duration) {
	defer safe.Recover("launch-write")
	switch r := l.br.WaitShellReady(l.ctx, maxWait); {
	case r.CanType():
	case r == internalpty.ShellBusy:
		if a.currentLaunch(id) != l {
			return
		}
		a.emit("notify", map[string]any{
			"tier":        "blocking",
			"title":       launchBusyTitle,
			"body":        launchBusyBody,
			"workspaceId": id,
			"action":      launchRetypeAction,
		})
		return
	default: // ShellClosed or ShellCancelled: the pane is gone
		return
	}
	// A reopen may have registered a new bridge before it cancelled this
	// open's context.
	if a.currentLaunch(id) != l {
		return
	}
	l.typeLine(false)
}

// RetypeLaunch types the agent launch line into the workspace's agent pane
// again, for the "Agent didn't start" notification's Retype launch action.
// It refuses when the session is not open or the agent has already reported
// in (the line would land in the agent's own TUI). It returns at once; the
// line is typed from a goroutine bound to the open, after at most
// retypeMaxWait for the shell to look ready.
func (a *App) RetypeLaunch(id string) error {
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	l := a.currentLaunch(id)
	if l == nil || l.ctx.Err() != nil {
		return fmt.Errorf("session %q is not open", id)
	}
	if l.hasStarted() {
		return errors.New("the agent already started in this session")
	}
	go func() {
		defer safe.Recover("launch-retype")
		switch l.br.WaitShellReady(l.ctx, retypeMaxWait) {
		case internalpty.ShellClosed, internalpty.ShellCancelled:
			return
		}
		// Ready, or still busy after retypeMaxWait: type anyway.
		if a.currentLaunch(id) == l {
			l.typeLine(true)
		}
	}()
	return nil
}

// noteAgentEvent records that the agent of the open bound to wctx reported
// in, which ends the window in which RetypeLaunch is allowed.
func (a *App) noteAgentEvent(wctx context.Context, id string) {
	a.mu.Lock()
	l := a.launches[id]
	a.mu.Unlock()
	if l != nil && l.ctx == wctx {
		l.markStarted()
	}
}

// setLaunchLocked registers (or, for an empty cmd, drops) workspace id's
// launch state. The caller holds a.mu.
func (a *App) setLaunchLocked(id string, l *launchState) {
	if l == nil {
		delete(a.launches, id)
		return
	}
	if a.launches == nil {
		a.launches = map[string]*launchState{}
	}
	a.launches[id] = l
}
