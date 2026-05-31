// Package model defines the shared domain vocabulary for perch.
// All types are plain data structs; no I/O or shell-outs occur here.
package model

// Tool identifies which AI coding agent runs in a window.
// Using a named type (not a bare string) means "claude"/"opencode" are defined
// in exactly one place — preventing drift across config, state, adapters, and
// tmux options.
type Tool string

const (
	ToolClaude   Tool = "claude"
	ToolOpencode Tool = "opencode"
)

// Valid reports whether t is a known tool.
func (t Tool) Valid() bool {
	return t == ToolClaude || t == ToolOpencode
}

// Project is a git repository discovered under the scan root.
// Frecency rank and last-accessed timestamp are not stored here; they live in
// internal/state keyed by Path.
type Project struct {
	// Path is the absolute path to the repository root.
	Path string
	// Name is the display name (typically the repository's base directory name).
	Name string
	// IsGit is false only for a non-git directory opened via the open-anywhere
	// escape hatch; all normally discovered projects are git repos.
	IsGit bool
}

// Tree is a working directory that perch can run a session in.
// For a git project this is the main checkout or one of its linked worktrees;
// for a non-git path it is just that directory.
type Tree struct {
	// Path is the absolute path to this working directory.
	Path string
	// Branch is the checked-out branch name (empty for non-git trees).
	Branch string
	// IsMain is true for the project's primary checkout.
	// Linked worktrees have IsMain == false.
	// Distinguishing these two is safety-critical: perch never removes the main
	// checkout (§7.2 hard-error guardrail).
	IsMain bool
	// Project is the owning repository. Nil for non-git trees.
	Project *Project
}

// Session is an AI agent conversation. Every session is already bound to a
// directory by the tool itself; perch reads that binding rather than inventing it.
// Display metadata (Title, Updated) is populated by the adapters in internal/agent.
type Session struct {
	// ID is the session identifier as assigned by the tool.
	ID string
	// Tool identifies which agent owns this session.
	Tool Tool
	// Directory is the absolute path the tool has bound this session to.
	Directory string
	// Title is a short human-readable label for the session.
	Title string
	// Updated is the last-activity timestamp as unix seconds.
	Updated int64
}

// Window is the on-disk wire format for the live-window shadow record written
// under $XDG_STATE_HOME/perch/windows/<paneKey>.json.
// One file per running window; isolated files allow concurrent perch instances
// to launch/kill windows without clobbering each other.
// JSON tags are the durable wire contract — do not change them without a
// migration plan.
type Window struct {
	// PaneKey is the tmux pane id (e.g. "%17").
	PaneKey string `json:"pane_key"`
	// Tool is the agent running in this window.
	Tool Tool `json:"tool"`
	// SessionID is the agent-assigned conversation identifier.
	SessionID string `json:"session_id"`
	// Tree is the absolute path to the working directory for this window.
	Tree string `json:"tree"`
	// TmuxSession is the tmux session name.
	TmuxSession string `json:"tmux_session"`
	// TmuxWindow is the tmux window name.
	TmuxWindow string `json:"tmux_window"`
	// BootID is the tmux server's #{start_time} at the moment this window was
	// created. It is the crash sentinel used by perch resurrect: a mismatch
	// between BootID and the live server's start_time means the server was
	// restarted and this window needs to be recreated.
	BootID string `json:"boot_id"`
	// Updated is the last-write timestamp as unix seconds.
	Updated int64 `json:"updated"`
}
