// Package app hosts the Wails App: the bound-method API the untrusted Svelte
// frontend calls. Every argument crossing the IPC boundary is validated here —
// session ids against a charset allowlist, worktree paths against the
// configured project roots — and all tmux/git work is done via argv through
// internal/proc, never a shell.
package app

import (
	"context"
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

// SessionInfo is the frontend-facing view of one live agent session. It is
// JSON-marshalled and sent to the Svelte sidebar to populate the sessions list.
type SessionInfo struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	Window  string `json:"window"`
	PaneID  string `json:"paneId"`
	Status  string `json:"status"`
	Dir     string `json:"dir"`
}

// ListSessions returns every live perch agent session. Status is derived from
// the pane-dead flag (dead → "exited") and the @perch_pane_status option
// (non-empty passthrough, empty → "idle"). Panes without @perch_session are
// non-perch panes and are excluded. This method backs the sidebar's
// sessions-changed refresh.
func (a *App) ListSessions() ([]SessionInfo, error) {
	panes, err := a.tmux.ListPanesAll(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(panes))
	for _, p := range panes {
		if p.PerchSession == "" {
			continue
		}
		status := p.PerchStatus
		switch {
		case p.Dead:
			status = "exited"
		case status == "":
			status = "idle"
		}
		out = append(out, SessionInfo{
			ID:      p.PerchSession,
			Session: p.Session,
			Window:  p.Window,
			PaneID:  p.ID,
			Status:  status,
			Dir:     p.Path,
		})
	}
	return out, nil
}

// liveSession looks up id in the currently-live perch sessions, enforcing the
// allowlist: the frontend can only act on sessions perch already knows about,
// never an arbitrary tmux target. The id is validated before any tmux call.
func (a *App) liveSession(id string) (SessionInfo, bool, error) {
	if err := validateSessionID(id); err != nil {
		return SessionInfo{}, false, err
	}
	sessions, err := a.ListSessions()
	if err != nil {
		return SessionInfo{}, false, err
	}
	for _, s := range sessions {
		if s.ID == id {
			return s, true, nil
		}
	}
	return SessionInfo{}, false, nil
}

// OpenTerminal spawns a tmux attach pty for the live session id and registers a
// Bridge under tabID. Output flows to the "pty-data:<tabID>" event. The session
// id is validated against the live allowlist before any pty is spawned.
func (a *App) OpenTerminal(tabID, sessionID string) error {
	if err := validateSessionID(tabID); err != nil {
		return fmt.Errorf("invalid tab id: %w", err)
	}
	s, ok, err := a.liveSession(sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown session %q", sessionID)
	}
	event := "pty-data:" + tabID
	br, err := internalpty.Spawn(context.Background(), a.tmux, s.Session, event, a.emit)
	if err != nil {
		return err
	}
	a.putBridge(tabID, &ptyEntry{bridge: br})
	return nil
}

// WriteToPty forwards raw keystroke bytes from xterm.js to the tab's pty.
func (a *App) WriteToPty(tabID string, data []byte) error {
	e, ok := a.getBridge(tabID)
	if !ok || e.bridge == nil {
		return fmt.Errorf("unknown terminal tab %q", tabID)
	}
	_, err := e.bridge.Write(data)
	return err
}

// ResizePty applies addon-fit's reported dimensions to the tab's pty winsize.
func (a *App) ResizePty(tabID string, cols, rows uint16) error {
	e, ok := a.getBridge(tabID)
	if !ok || e.bridge == nil {
		return fmt.Errorf("unknown terminal tab %q", tabID)
	}
	return e.bridge.Resize(cols, rows)
}

// CloseTerminal tears down the tab's attach pty (the agent session survives) and
// removes the registry entry. A nil bridge is tolerated so the registry guard is
// testable without a real pty.
func (a *App) CloseTerminal(tabID string) error {
	e, ok := a.getBridge(tabID)
	if !ok {
		return fmt.Errorf("unknown terminal tab %q", tabID)
	}
	a.removeBridge(tabID)
	if e.bridge == nil {
		return nil
	}
	return e.bridge.Close()
}

// KillSession kills the tmux window backing the agent session id. The id is
// validated against the live allowlist and the kill target is derived from the
// matched session's own tmux session/window — the raw id never enters argv.
func (a *App) KillSession(id string) error {
	s, ok, err := a.liveSession(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	target := tmux.WindowTarget(s.Session, s.Window)
	if err := a.tmux.KillWindow(context.Background(), target); err != nil {
		return err
	}
	a.emit("sessions-changed")
	return nil
}
