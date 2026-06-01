// Package status implements the `perch status set` CLI action and the
// canonical in-process state machine (Machine) that defines the event →
// status mapping for the opencode/claude plugin hand-port.
package status

import (
	"context"
	"fmt"

	"github.com/Miniature-Pug/perch/internal/tmux"
)

// valid states accepted by Set and fired by Machine.
const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateDone    = "done"
)

// validStates is the closed set of strings accepted by Set.
var validStates = map[string]bool{
	StateWorking: true,
	StateWaiting: true,
	StateDone:    true,
}

// Deps carries the external seams used by Set. Production code passes
// tmux.New(); unit tests inject a Tmux with a FakeRunner.
type Deps struct {
	Tmux tmux.Tmux
}

// Set writes state to the pane option @perch_pane_status on the given pane
// target (typically a raw $TMUX_PANE id such as "%3"). The caller is
// responsible for resolving the pane and handling the empty-pane no-op case.
// State must be one of "working", "waiting", or "done".
func Set(ctx context.Context, deps Deps, pane, state string) error {
	// Validate first so an invalid call never reaches the tmux seam.
	if !validStates[state] {
		return fmt.Errorf("status: invalid state %q (must be working, waiting, or done)", state)
	}
	return deps.Tmux.SetPaneOption(ctx, pane, "@perch_pane_status", state)
}

// ── Machine ───────────────────────────────────────────────────────────────────

// sessionState is the per-session mutable state tracked by Machine.
type sessionState struct {
	lastFired     string // last state that was actually fired; "" initially
	acceptWorking bool   // whether the next busy/retry event may fire "working"
}

// Machine is a pure, in-memory state machine that maps plugin events to
// (state, fire) pairs. It is the canonical reference implementation; the
// opencode perch-status.ts plugin is a faithful hand-port.
//
// A single Machine may track arbitrarily many sessions simultaneously —
// session state is keyed by sessionID so independent sessions never
// cross-contaminate. Use NewMachine to construct a ready-to-use instance.
type Machine struct {
	sessions map[string]*sessionState
}

// NewMachine returns a Machine ready to track events.
func NewMachine() *Machine {
	return &Machine{sessions: make(map[string]*sessionState)}
}

// session returns the mutable state for sessionID, creating it on first access.
// acceptWorking starts true: without any user message the very first busy event
// should still fire "working" (this is the initial, not the re-armed, state).
func (m *Machine) session(id string) *sessionState {
	if m.sessions == nil {
		m.sessions = make(map[string]*sessionState)
	}
	if s, ok := m.sessions[id]; ok {
		return s
	}
	// Go zero-value for bool is false; acceptWorking must start true.
	s := &sessionState{acceptWorking: true}
	m.sessions[id] = s
	return s
}

// OnSessionStatus handles session.status events from the plugin.
// statusType "busy" or "retry" → candidate "working".
// statusType "idle"            → candidate "done".
// Any other value is a no-op (returns ("",false)).
func (m *Machine) OnSessionStatus(sessionID, statusType string) (state string, fire bool) {
	switch statusType {
	case "busy", "retry":
		return m.fireWorking(sessionID)
	case "idle":
		return m.fireDone(sessionID)
	default:
		return "", false
	}
}

// OnSessionIdle handles the opencode session.idle event (falls through to "done").
func (m *Machine) OnSessionIdle(sessionID string) (state string, fire bool) {
	return m.fireDone(sessionID)
}

// OnUserMessage re-arms the working gate. It does not fire any status itself.
// Called on message.updated events with role=user.
func (m *Machine) OnUserMessage(sessionID string) (state string, fire bool) {
	// Re-arm so the next busy event is accepted. lastFired is intentionally
	// left untouched — the dedup check is independent of the arm state.
	m.session(sessionID).acceptWorking = true
	return "", false
}

// OnPermissionAsked fires "waiting" (unless already waiting).
func (m *Machine) OnPermissionAsked(sessionID string) (state string, fire bool) {
	return m.fireWaiting(sessionID)
}

// OnPermissionReplied fires "working" after permission is granted (unless
// already working). Does NOT touch acceptWorking.
func (m *Machine) OnPermissionReplied(sessionID string) (state string, fire bool) {
	return m.fireWorkingUnconditional(sessionID)
}

// ── private fire helpers ──────────────────────────────────────────────────────

// fireWorking fires "working" only when acceptWorking is true and not dedup'd.
// Called for busy/retry events from the session status stream.
func (m *Machine) fireWorking(sessionID string) (string, bool) {
	s := m.session(sessionID)
	if !s.acceptWorking {
		// Stale trailing busy after idle: suppress. The disarm on "done" prevents
		// the agent's final busy→idle flush from re-opening the working state.
		return "", false
	}
	if s.lastFired == StateWorking {
		return "", false // dedup
	}
	s.lastFired = StateWorking
	return StateWorking, true
}

// fireWorkingUnconditional fires "working" regardless of acceptWorking (used by
// permission/question replied events which resume work explicitly).
func (m *Machine) fireWorkingUnconditional(sessionID string) (string, bool) {
	s := m.session(sessionID)
	if s.lastFired == StateWorking {
		return "", false // dedup
	}
	s.lastFired = StateWorking
	return StateWorking, true
}

// fireDone fires "done" and disarms the working gate.
func (m *Machine) fireDone(sessionID string) (string, bool) {
	s := m.session(sessionID)
	if s.lastFired == StateDone {
		return "", false // dedup
	}
	s.lastFired = StateDone
	// Disarm: suppress stale trailing busy events that arrive after idle.
	s.acceptWorking = false
	return StateDone, true
}

// fireWaiting fires "waiting".
func (m *Machine) fireWaiting(sessionID string) (string, bool) {
	s := m.session(sessionID)
	if s.lastFired == StateWaiting {
		return "", false // dedup
	}
	s.lastFired = StateWaiting
	return StateWaiting, true
}
