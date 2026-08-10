// Package model defines the shared domain vocabulary for perch.
// All types are plain data structs. This package does no I/O and runs no
// external commands.
package model

// Tool identifies which AI coding agent runs in a pane.
// Tool is a named type, not a bare string, so "claude" and "opencode" stay
// defined in exactly one place. This stops drift across the rest of perch.
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
// Project does not store frecency rank or the last-accessed timestamp.
// perch tracks those separately, keyed by Path.
type Project struct {
	// Path is the absolute path to the repository root.
	Path string
	// Name is the display name (typically the repository's base directory name).
	Name string
}

// Tree is a working directory where perch can run a session.
// For a git project, Tree is the main checkout or one of its linked
// worktrees. For a non-git path, Tree is just that directory.
type Tree struct {
	// Path is the absolute path to this working directory.
	Path string
	// Branch is the checked-out branch name (empty for non-git trees).
	Branch string
	// IsMain is true for the project's primary checkout.
	// Linked worktrees have IsMain == false.
	// This distinction is safety-critical. perch never removes the main
	// checkout (a hard-error guardrail).
	IsMain bool
	// Project is the owning repository. Nil for non-git trees.
	Project *Project
}
