// Package agent defines the Adapter interface. perch uses this interface to
// drive each AI coding tool (claude, opencode). Concrete adapters live in
// this package, next to this file. The rest of perch depends only on the
// interface.
package agent

import "strings"

// Adapter is the seam between perch's orchestration logic and a specific AI
// coding tool. Each tool (claude, opencode) provides one Adapter
// implementation.
//
// This interface does not yet include ReadyHeuristic or TrustPrompt. Neither
// method has a working implementation yet. perch will add them at the TUI
// milestones that need those UI surfaces.
type Adapter interface {
	// Name returns the canonical tool identifier: "claude" or "opencode".
	// It matches the model.Tool constants. perch uses it in log messages,
	// session records, and window pane labels.
	Name() string

	// Detect reports whether the tool's CLI binary is on PATH. A false
	// return means the tool is not available. perch then skips the tool
	// instead of showing an error to the user.
	Detect() bool

	// ResumeArgs returns the launch argument list needed to resume
	// sessionID in the current working directory. The caller must set the
	// process working directory before it runs exec. This method only
	// builds the argument list.
	ResumeArgs(sessionID string) []string

	// NewArgs returns the launch argument list for a new interactive
	// session. perch does not pass a model flag; the harness picks its own
	// model. For claude, NewArgs returns nil. For opencode, NewArgs also
	// returns nil, because opencode attach takes no --model flag.
	NewArgs() []string
}

// binNamer is implemented by the concrete adapters (Claude, Opencode): it
// returns the configured binary name or path (the Bin field, or the default).
type binNamer interface{ bin() string }

// launchBin returns the binary a monitor types into the launch line for a.
// It honours the adapter's Bin, so a custom Bin that passes Detect also
// launches (AGT-19). A Bin that is not a plain shell-safe word (whitespace,
// quotes, $, backslash) falls back to the canonical Name rather than risk a
// mangled or injected launch line.
func launchBin(a Adapter, fallback string) string {
	if a == nil {
		return fallback
	}
	if b, ok := a.(binNamer); ok {
		if s := b.bin(); shellSafeWord(s) && !strings.ContainsAny(s, " \t;&|<>()*?[]{}~#") {
			return s
		}
	}
	return a.Name()
}
