// Package status defines the canonical state constants (StateWorking,
// StateWaiting, StateDone) shared between the agent monitors and the
// opencode/claude plugin hand-ports.
package status

// valid states emitted by the agent monitors.
const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateDone    = "done"
)
