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
	"sync"

	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ptyEntry pairs a live attach Bridge with its frontend tab id.
type ptyEntry struct {
	bridge *internalpty.Bridge
}

// App is the Wails bound object. It is constructed by NewApp and its context is
// captured in startup so the production emit seam can call wails runtime.
type App struct {
	tmux  tmux.Tmux
	run   proc.Runner
	roots []string

	// emit delivers events to the frontend. Production wires this to a
	// runtime.EventsEmit closure in startup; tests inject a capture. This seam is
	// what makes the App bootable headlessly in `go test`.
	emit internalpty.EmitFunc

	mu      sync.Mutex
	bridges map[string]*ptyEntry

	// lastSig + stopPoll are used by the §3.3 poller (Task 10B); declared here so
	// the struct has one canonical definition. lastSig is the fingerprint of the
	// last emitted session set; stopPoll is closed by shutdown to stop the ticker.
	lastSig  string
	stopPoll chan struct{}
}

func (a *App) putBridge(tabID string, e *ptyEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bridges[tabID] = e
}

func (a *App) getBridge(tabID string) (*ptyEntry, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.bridges[tabID]
	return e, ok
}

func (a *App) removeBridge(tabID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.bridges, tabID)
}


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
