// Package agent defines the Adapter interface through which perch drives each
// AI coding tool (claude, opencode). Concrete adapters live in this package
// alongside this file; the rest of perch depends only on the interface.
package agent

// Adapter is the seam between perch's orchestration logic and a specific AI
// coding tool. Each tool (claude, opencode) provides one Adapter implementation.
//
// The following methods from master-plan §4 are intentionally absent from this interface:
// ReadyHeuristic and TrustPrompt are deliberately not included here because
// neither has an honest implementation until the TUI milestones that require
// those UI surfaces. They will be added to this interface at those milestones.
type Adapter interface {
	// Name returns the canonical tool identifier — "claude" or "opencode" —
	// matching the model.Tool constants. Used in log messages, session records,
	// and window pane labels.
	Name() string

	// Detect reports whether the tool's CLI binary can be resolved on PATH.
	// A false return means the tool is unavailable; perch skips it rather than
	// returning an error to the user.
	Detect() bool

	// ResumeArgs returns the launch argument slice needed to resume sessionID
	// in the current working directory. The caller is responsible for setting
	// the process working directory before exec; this method only builds args.
	ResumeArgs(sessionID string) []string

	// NewArgs returns the launch argument slice for a brand-new interactive
	// session. perch does not pass a model flag — the harness chooses its own
	// model. For claude this returns nil; for opencode it also returns nil
	// (opencode attach accepts no --model).
	NewArgs() []string
}
