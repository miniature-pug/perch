// Package app hosts the Wails App: the bound-method API the untrusted Svelte
// frontend calls. Every argument crossing the IPC boundary is validated here —
// session ids against a charset allowlist, worktree paths against the
// configured project roots — and all tmux/git work is done via argv through
// internal/proc, never a shell.
package app

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maxSessionIDLen = 128

// validateSessionID enforces the perch session-id charset [A-Za-z0-9_-], length
// 1..128 — the same contract internal/tmux applies to @perch_session values, so
// the frontend can never inject a tmux target, delimiter, path, or shell metachar.
func validateSessionID(s string) error {
	if s == "" || len(s) > maxSessionIDLen {
		return fmt.Errorf("invalid session id length")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return fmt.Errorf("invalid character in session id")
		}
	}
	return nil
}

// validateWorktreeUnderRoots rejects any path that is not absolute, not clean,
// does not exist, or — after resolving symlinks — is not contained within one of
// the configured roots. It closes path traversal, symlink escape, and
// arbitrary-directory operations from the frontend.
//
// CONTRACT — the path MUST already exist on disk. filepath.EvalSymlinks is
// called after filepath.Clean so a symlink whose target escapes a root is caught
// even when the raw path looks legitimate; EvalSymlinks errors on a non-existent
// path, which is rejected. Every real caller passes an existing path (Diff on a
// checked-out worktree, CreateAgent on an existing project root), so callers MUST
// NOT hand this a to-be-created path. Each root is itself symlink-resolved so a
// root containing a symlink component still matches; a root that cannot be
// resolved is skipped. The trailing-separator prefix check prevents a sibling
// like "<root>-evil" from matching root "<root>".
func validateWorktreeUnderRoots(p string, roots []string) error {
	if p == "" || !filepath.IsAbs(p) {
		return fmt.Errorf("worktree path must be absolute")
	}
	clean := filepath.Clean(p)
	if clean != p {
		return fmt.Errorf("worktree path must be clean")
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return fmt.Errorf("worktree path %q: %w", p, err)
	}
	for _, root := range roots {
		rootResolved, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		if resolved == rootResolved ||
			strings.HasPrefix(resolved, rootResolved+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("worktree path %q is outside configured roots", p)
}
