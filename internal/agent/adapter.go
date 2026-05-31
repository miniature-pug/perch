// Package agent defines the Adapter interface through which perch drives each
// AI coding tool (claude, opencode). Concrete adapters live in this package
// alongside this file; the rest of perch depends only on the interface.
//
// Fault-tolerance contract (§4): adapters never panic. When session records
// cannot be parsed, the adapter skips the unparseable entries and returns the
// partial result alongside any diagnostic error. An adapter failure degrades
// gracefully to "that tool is unavailable" — it never brings down perch.
package agent

import (
	"context"

	"github.com/Miniature-Pug/perch/internal/model"
)

// Adapter is the seam between perch's orchestration logic and a specific AI
// coding tool. Each tool (claude, opencode) provides one Adapter implementation.
//
// The following methods from master-plan §4 are intentionally absent from this interface:
// InstallStatusHook, ReadyHeuristic, and TrustPrompt are deliberately not
// included here because neither has an honest implementation until the setup
// and TUI milestones land: status hooks belong to "perch setup" and
// readiness/trust heuristics require UI surfaces that do not exist yet. They
// will be added to this interface at those milestones, not speculatively now.
type Adapter interface {
	// Name returns the canonical tool identifier — "claude" or "opencode" —
	// matching the model.Tool constants. Used in log messages, session records,
	// and tmux window metadata.
	Name() string

	// Detect reports whether the tool's CLI binary can be resolved on PATH.
	// A false return means the tool is unavailable; perch skips it rather than
	// returning an error to the user.
	Detect() bool

	// ListSessions returns all sessions the tool knows about, regardless of
	// which directory they were started in. Unparseable records are skipped;
	// the partial slice is returned alongside any diagnostic error so callers
	// can display what is available even when the tool's session store is
	// partially corrupt.
	ListSessions(ctx context.Context) ([]model.Session, error)

	// ResumeArgs returns the launch argument slice needed to resume sessionID
	// in the current working directory. The caller is responsible for setting
	// the process working directory before exec; this method only builds args.
	ResumeArgs(sessionID string) []string

	// ForkInto returns the launch argument slice needed to fork sessionID into
	// targetDir — that is, to resume the conversation's context in a different
	// directory. This is used when the user opens a session in a new worktree.
	// Returns an error when the tool does not support forking (e.g. a tool that
	// hard-binds sessions to their origin directory).
	ForkInto(sessionID, targetDir string) ([]string, error)

	// NewArgs returns the launch argument slice for a brand-new interactive
	// session, configured by opts. Fields in opts that do not apply to this
	// tool are silently ignored.
	NewArgs(opts NewOpts) []string
}

// NewOpts carries the per-session configuration for a fresh launch.
type NewOpts struct {
	// Model is the provider/model string passed to the tool's model flag —
	// claude --model / opencode -m. Empty means "use the tool's default."
	Model string

	// Prompt is the initial prompt text. For claude this is a positional
	// argument; for opencode it maps to --prompt.
	Prompt string

	// Agent is the opencode agent identifier (opencode --agent). Not used by
	// claude.
	Agent string

	// SessionID is a UUID that claude uses with --session-id to deterministically
	// name a new session so perch can track it. Not used by opencode.
	SessionID string
}
