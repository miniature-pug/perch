package tmux

import (
	"path/filepath"
	"strings"
	"unicode"
)

// sanitize produces a tmux-safe name from s. It keeps [A-Za-z0-9_/-] as-is,
// maps every other rune (including '.' and ':' which tmux silently rewrites or
// treats as separators) to '-', collapses consecutive '-' into one, and trims
// leading/trailing '-'. An empty result returns "unnamed".
//
// Residual collision risk: distinct inputs can sanitize to the same name
// (e.g. "a.b" and "a:b" both become "a-b"). tmux then returns "duplicate
// session", which the caller (Connect, M4-B) surfaces — acceptable in v1.
func sanitize(s string) string {
	var b strings.Builder
	prev := '-' // treat start as if preceded by a dash to skip leading dashes
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/' {
			b.WriteRune(r)
			prev = r
		} else {
			// Collapse consecutive replacements into a single dash.
			if prev != '-' {
				b.WriteByte('-')
				prev = '-'
			}
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "unnamed"
	}
	return result
}

// SessionName returns a tmux-safe session name derived from the base of
// projectPath.
func SessionName(projectPath string) string {
	return sanitize(filepath.Base(projectPath))
}

// WindowName returns a tmux-safe window name from a worktree basename or branch
// name.
func WindowName(treeOrBranch string) string {
	return sanitize(treeOrBranch)
}
