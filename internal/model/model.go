// Package model defines the shared domain vocabulary for perch.
// All types are plain data structs; no I/O or shell-outs occur here.
package model

// Tool identifies which AI coding agent runs in a pane.
// Using a named type (not a bare string) means "claude"/"opencode" are defined
// in exactly one place — preventing drift across config, state, and adapters.
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
