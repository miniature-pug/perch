// Package app hosts the Wails App: the bound-method API the untrusted Svelte
// frontend calls. Every argument crossing the IPC boundary is validated here —
// session ids against a charset allowlist, worktree paths against the
// configured project roots — and all git work is done via argv through
// internal/proc, never a shell.
package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/agent"
	gitpkg "github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ptyEntry pairs a live attach Bridge with its frontend tab id.
type ptyEntry struct {
	bridge *internalpty.Bridge
}

// App is the Wails bound object. It is constructed by NewApp and its context is
// captured in startup so the production emit seam can call wails runtime.
type App struct {
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
	// stopOnce guards close(stopPoll) so double-shutdown (e.g. in tests) cannot panic.
	stopOnce sync.Once
}

// putBridge registers e under tabID. If a prior entry exists its bridge is
// closed after the lock is released (close-and-replace: never orphan a
// displaced pty). Never hold a.mu across bridge.Close (it kills a process /
// closes fds and may block).
func (a *App) putBridge(tabID string, e *ptyEntry) {
	a.mu.Lock()
	old := a.bridges[tabID]
	a.bridges[tabID] = e
	a.mu.Unlock()
	if old != nil && old.bridge != nil {
		_ = old.bridge.Close() // close-and-replace: never leak a displaced bridge
	}
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

// ListSessions stub — returns empty until app rewrite (Phase 3).
func (a *App) ListSessions() ([]SessionInfo, error) {
	return nil, nil
}

// liveSession looks up id in the currently-live perch sessions.
func (a *App) liveSession(id string) (SessionInfo, bool, error) {
	if err := validateSessionID(id); err != nil {
		return SessionInfo{}, false, err
	}
	return SessionInfo{}, false, nil
}

// OpenTerminal stub — not yet implemented.
func (a *App) OpenTerminal(tabID, sessionID string) error {
	return fmt.Errorf("not implemented")
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

// KillSession stub — returns nil.
func (a *App) KillSession(id string) error {
	return nil
}

// DiffResult is the frontend view of a worktree's uncommitted diff.
type DiffResult struct {
	Patch   string `json:"patch"`
	Files   int    `json:"files"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Diff returns the uncommitted diff + stat for worktreePath. The path is
// validated to live under a configured root before git is invoked via argv.
func (a *App) Diff(worktreePath string) (DiffResult, error) {
	if err := validateWorktreeUnderRoots(worktreePath, a.roots); err != nil {
		return DiffResult{}, err
	}
	patch, err := gitpkg.Diff(context.Background(), a.run, worktreePath)
	if err != nil {
		return DiffResult{}, err
	}
	st, err := gitpkg.DiffStat(context.Background(), a.run, worktreePath)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{Patch: patch, Files: st.Files, Added: st.Added, Removed: st.Removed}, nil
}

// ── CreateAgent helpers ───────────────────────────────────────────────────────

// newSessionID generates a random UUID v4 used as the claude --session-id.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// adapterFor returns the Adapter for a known tool name, or (nil, false) for an
// unknown tool.
func adapterFor(tool string) (agent.Adapter, bool) {
	switch tool {
	case "claude":
		return agent.NewClaude(), true
	case "opencode":
		return agent.NewOpencode(), true
	default:
		return nil, false
	}
}

// containedUnderRoots reports whether the lexically-cleaned treePath is at or
// under one of the configured roots. This is a LEXICAL containment check,
// intended for paths that do not yet exist (e.g. a to-be-created worktree
// directory). Because treePath is not yet on disk, filepath.EvalSymlinks
// cannot be called on it.
func containedUnderRoots(treePath string, roots []string) bool {
	clean := filepath.Clean(treePath)
	if !filepath.IsAbs(clean) {
		return false
	}
	contained := func(root string) bool {
		return clean == root || strings.HasPrefix(clean, root+string(filepath.Separator))
	}
	for _, root := range roots {
		rootClean := filepath.Clean(root)
		if contained(rootClean) {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(rootClean); err == nil {
			if contained(resolved) {
				return true
			}
		}
	}
	return false
}

// CreateAgent stub — not yet implemented.
func (a *App) CreateAgent(tool, projectPath, branch string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

// NewApp builds the production App. emit is a no-op until startup installs the
// wails runtime closure, so methods that emit are safe to call pre-startup
// (e.g. in headless tests that set their own emit).
func NewApp(roots []string) *App {
	return &App{
		run:     proc.ExecRunner{},
		roots:   roots,
		emit:    func(string, ...any) {},
		bridges: map[string]*ptyEntry{},
	}
}

// pollInterval is the state-sync tick (spec §3.3, ~1s).
const pollInterval = time.Second

// sessionsSignature is a cheap order-stable fingerprint of the session set used
// to suppress no-op sessions-changed emits.
func sessionsSignature(ss []SessionInfo) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.ID)
		b.WriteByte('=')
		b.WriteString(s.Status)
		b.WriteByte(';')
	}
	return b.String()
}

// pollOnce reads the live sessions once and emits sessions-changed only if the
// signature changed since the last emit. Errors are swallowed: a transient tmux
// hiccup must not kill the poller.
func (a *App) pollOnce() {
	// no-op until app rewrite (Phase 3)
}

// startPolling runs pollOnce every pollInterval until stopPoll is closed. Called
// from startup in a goroutine.
func (a *App) startPolling() {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-a.stopPoll:
			return
		case <-t.C:
			a.pollOnce()
		}
	}
}

// startup is the Wails OnStartup hook. It captures the runtime context,
// installs the production emit seam (runtime.EventsEmit), and starts the
// background sessions poller (spec §3.3).
func (a *App) startup(ctx context.Context) {
	// Wails calls OnStartup exactly once, before the frontend can invoke any
	// bound method or before any pump goroutine is spawned, so this assignment
	// is ordered-safe without a mutex (happens-before any reader of a.emit).
	a.emit = func(event string, data ...any) {
		wailsruntime.EventsEmit(ctx, event, data...)
	}
	a.stopPoll = make(chan struct{})
	go a.startPolling()
}

// shutdown stops the background poller and closes every live attach pty. The
// agent sessions persist on the tmux server; only the GUI's attach clients are
// torn down. shutdown is safe to call more than once: stopOnce guards
// close(stopPoll) so a second call cannot panic.
func (a *App) shutdown(_ context.Context) {
	a.stopOnce.Do(func() {
		if a.stopPoll != nil {
			close(a.stopPoll)
		}
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, e := range a.bridges {
		if e.bridge != nil {
			_ = e.bridge.Close()
		}
		delete(a.bridges, id)
	}
}
