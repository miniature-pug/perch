// Package app hosts the Wails App: the bound-method API the untrusted Svelte
// frontend calls. Every argument crossing the IPC boundary is validated here —
// workspace ids against a charset allowlist, worktree paths against the
// configured project roots. Subprocesses are always spawned via argv
// (internal/proc or internal/pty), never a shell.
package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/registry"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails bound object.
type App struct {
	store *registry.Store
	roots []string

	// run is the process runner for git invocations; nil falls back to a real
	// ExecRunner via runner(). Tests may inject a fake.
	run proc.Runner

	// emit delivers events to the frontend. Production wires this to a
	// runtime.EventsEmit closure in startup; tests inject a capture seam.
	emit internalpty.EmitFunc

	mu       sync.Mutex
	bridges  map[string]*internalpty.Bridge // paneID → Bridge
	monitors map[string]agent.Monitor       // workspaceID → Monitor
}

// NewApp builds the production App.
func NewApp(store *registry.Store, roots []string) *App {
	return &App{
		store:    store,
		roots:    roots,
		run:      proc.ExecRunner{},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
}

// runner returns the configured process runner, defaulting to a real
// ExecRunner when unset (so bare App{} literals in tests work for git methods).
func (a *App) runner() proc.Runner {
	if a.run != nil {
		return a.run
	}
	return proc.ExecRunner{}
}

// startup is the Wails OnStartup hook.
func (a *App) startup(ctx context.Context) {
	a.emit = func(event string, data ...any) {
		wailsruntime.EventsEmit(ctx, event, data...)
	}
}

// shutdown closes every live Bridge and tears down every Monitor. Idempotent.
func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	bridges := a.bridges
	monitors := a.monitors
	a.bridges = map[string]*internalpty.Bridge{}
	a.monitors = map[string]agent.Monitor{}
	a.mu.Unlock()

	for _, b := range bridges {
		_ = b.Close()
	}
	for _, m := range monitors {
		_ = m.Teardown()
	}
}

// putBridge registers b under paneID, closing any displaced bridge.
func (a *App) putBridge(paneID string, b *internalpty.Bridge) {
	a.mu.Lock()
	old := a.bridges[paneID]
	a.bridges[paneID] = b
	a.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (a *App) getBridge(paneID string) (*internalpty.Bridge, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.bridges[paneID]
	return b, ok
}

func (a *App) getMonitor(workspaceID string) (agent.Monitor, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.monitors[workspaceID]
	return m, ok
}

const maxSessionIDLen = 128

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

func newWorkspaceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// WorkspaceVM is the frontend-facing view of one workspace.
type WorkspaceVM struct {
	ID           string      `json:"id"`
	WorktreePath string      `json:"worktreePath"`
	Agent        string      `json:"agent"`
	Title        string      `json:"title"`
	Branch       string      `json:"branch"`
	State        agent.State `json:"state"`
	Caps         agent.Caps  `json:"caps"`
	PaneID       string      `json:"paneId"`
	LastActive   time.Time   `json:"lastActive"`
}

// Settings is the persisted user preference blob.
type Settings struct {
	Theme       string       `json:"theme"`
	Density     string       `json:"density"`
	Font        string       `json:"font"`
	DND         bool         `json:"dnd"`
	AlwaysRules []AlwaysRule `json:"alwaysRules"`
}

// AlwaysRule persists an "always allow" approval rule.
type AlwaysRule struct {
	Agent   string `json:"agent"`
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
}
