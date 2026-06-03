// Package app hosts the Wails App: the bound-method API the untrusted Svelte
// frontend calls. Every argument crossing the IPC boundary is validated here —
// workspace ids against a charset allowlist, worktree paths against the
// configured project roots. Subprocesses are always spawned via argv
// (internal/proc or internal/pty), never a shell.
package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	fspkg "github.com/Miniature-Pug/perch/internal/fs"
	gitpkg "github.com/Miniature-Pug/perch/internal/git"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/registry"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// spawnPtyFunc and newMonitorFunc are injectable seams (real funcs in NewApp,
// replaced in tests for headless execution).
type spawnPtyFunc func(ctx context.Context, cwd string, argv []string, event string,
	emit internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error)

type newMonitorFunc func(tool string, adapter agent.Adapter) (agent.Monitor, error)

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

	cancels map[string]context.CancelFunc // workspaceID → pump/translation canceller

	spawnPty   spawnPtyFunc
	newMonitor newMonitorFunc

	settingsPath string
	layoutPath   string
}

// NewApp builds the production App.
func NewApp(store *registry.Store, roots []string) *App {
	return &App{
		store:        store,
		roots:        roots,
		run:          proc.ExecRunner{},
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		cancels:      map[string]context.CancelFunc{},
		spawnPty:     internalpty.Spawn,
		newMonitor:   agent.NewMonitor,
		settingsPath: filepath.Join(registry.DefaultConfigDir(), "settings.json"),
		layoutPath:   filepath.Join(registry.DefaultConfigDir(), "layout.json"),
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

// shutdown cancels every workspace pump, closes every Bridge, and tears down
// every Monitor. Idempotent.
func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	bridges := a.bridges
	monitors := a.monitors
	cancels := a.cancels
	a.bridges = map[string]*internalpty.Bridge{}
	a.monitors = map[string]agent.Monitor{}
	a.cancels = map[string]context.CancelFunc{}
	a.mu.Unlock()

	for _, c := range cancels {
		c()
	}
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

// ListWorkspaces returns all known workspaces from the registry. State and
// Caps come from a live Monitor when one is active; otherwise State=Idle.
func (a *App) ListWorkspaces() []WorkspaceVM {
	ws := a.store.List()
	out := make([]WorkspaceVM, 0, len(ws))
	for _, w := range ws {
		vm := WorkspaceVM{
			ID:           w.ID,
			WorktreePath: w.WorktreePath,
			Agent:        w.Agent,
			Title:        w.Title,
			State:        agent.StateIdle,
		}
		a.mu.Lock()
		m, ok := a.monitors[w.ID]
		a.mu.Unlock()
		if ok {
			vm.State = m.CurrentState()
			vm.Caps = m.Capabilities()
		}
		out = append(out, vm)
	}
	return out
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

// CreateWorkspace validates inputs, resolves/creates the worktree, persists the
// workspace to the registry, and returns its WorkspaceVM. It does NOT start the
// agent — call OpenWorkspace for that. The model arg is reserved for launch-time
// configuration and is not yet plumbed to the agent.
func (a *App) CreateWorkspace(agentName, repoPath, branch, model string) (WorkspaceVM, error) {
	// Gate 1: repoPath must exist under a configured root.
	if err := validateWorktreeUnderRoots(repoPath, a.roots); err != nil {
		return WorkspaceVM{}, err
	}
	// Gate 2: branch must be a valid git ref.
	if err := gitpkg.ValidRef(branch); err != nil {
		return WorkspaceVM{}, fmt.Errorf("invalid branch: %w", err)
	}
	// Gate 3: agent must be known.
	if agentName != "claude" && agentName != "opencode" {
		return WorkspaceVM{}, fmt.Errorf("unknown agent %q", agentName)
	}

	handle := gitpkg.SlugifyBranch(branch)
	treePath, err := gitpkg.WorktreePath(repoPath, handle, "")
	if err != nil {
		return WorkspaceVM{}, err
	}
	if !containedUnderRoots(treePath, a.roots) {
		return WorkspaceVM{}, fmt.Errorf("derived worktree path %q escapes all configured roots", treePath)
	}

	ctx := context.Background()
	// Create the linked worktree; an already-existing branch is not fatal.
	if err := gitpkg.AddWorktree(ctx, a.runner(), repoPath, branch, treePath, "HEAD"); err != nil {
		if !errors.Is(err, gitpkg.ErrBranchExists) {
			return WorkspaceVM{}, fmt.Errorf("create worktree: %w", err)
		}
	}

	id, err := newWorkspaceID()
	if err != nil {
		return WorkspaceVM{}, err
	}

	w := registry.Workspace{
		ID:           id,
		WorktreePath: treePath,
		Agent:        agentName,
		Title:        handle,
		LastActive:   time.Now(),
	}
	if err := a.store.Upsert(w); err != nil {
		return WorkspaceVM{}, fmt.Errorf("persist workspace: %w", err)
	}

	return WorkspaceVM{
		ID:           id,
		WorktreePath: treePath,
		Agent:        agentName,
		Title:        handle,
		State:        agent.StateIdle,
	}, nil
}

// OpenWorkspace spawns a login-shell pty for the workspace, calls Monitor.Prepare
// to obtain the agent launch command and install the side-channel, starts the
// monitor's event pump, writes the launch command into the pty, and forwards
// monitor events to the frontend. All goroutines are bound to a per-workspace
// context cancelled by CloseWorkspace/shutdown.
//
// mon.Start(wctx) is REQUIRED: without it no events ever flow from a real monitor.
func (a *App) OpenWorkspace(id string) error {
	w, ok := a.store.Get(id)
	if !ok {
		return fmt.Errorf("unknown workspace %q", id)
	}

	paneID := "pane-" + id
	event := "pty:data:" + paneID

	wctx, cancel := context.WithCancel(context.Background())

	br, err := a.spawnPty(wctx, w.WorktreePath, internalpty.LoginShellArgv(), event, a.emit, 220, 50)
	if err != nil {
		cancel()
		return fmt.Errorf("spawn pty: %w", err)
	}

	adpt := agentAdapter(w.Agent)
	mon, err := a.newMonitor(w.Agent, adpt)
	if err != nil {
		cancel()
		_ = br.Close()
		return fmt.Errorf("new monitor: %w", err)
	}

	launchCmd, err := mon.Prepare(wctx, id, w.WorktreePath, w.LastSessionID)
	if err != nil {
		cancel()
		_ = br.Close()
		_ = mon.Teardown()
		return fmt.Errorf("monitor prepare: %w", err)
	}

	// REQUIRED: start the monitor's event pump (translation/SSE), bound to wctx.
	mon.Start(wctx)

	a.mu.Lock()
	oldBr := a.bridges[paneID]
	oldMon := a.monitors[id]
	oldCancel := a.cancels[id]
	a.bridges[paneID] = br
	a.monitors[id] = mon
	a.cancels[id] = cancel
	a.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldBr != nil {
		_ = oldBr.Close()
	}
	if oldMon != nil {
		_ = oldMon.Teardown()
	}

	if launchCmd != "" {
		_, _ = br.Write([]byte(launchCmd))
	}

	// Forward monitor events to the frontend. Forward-and-continue: emit and move
	// on, never blocking on a user decision. Exits on wctx cancellation, since
	// mon.Events() is never closed.
	go func() {
		for {
			select {
			case <-wctx.Done():
				return
			case evt, ok := <-mon.Events():
				if !ok {
					return
				}
				a.emit("agent:event", evt)
				a.dispatchNotify(evt)
			}
		}
	}()

	return nil
}

// dispatchNotify translates an agent.Event into a "notify" Wails event at the
// appropriate tier.
func (a *App) dispatchNotify(evt agent.Event) {
	switch {
	case evt.Kind == "state" && evt.State == agent.StateAwaitingApproval:
		a.emit("notify", map[string]any{
			"tier":        "blocking",
			"title":       "Approval needed",
			"body":        "An agent is waiting for your decision.",
			"workspaceId": evt.WorkspaceID,
		})
	case evt.Kind == "state" && evt.State == agent.StateDone:
		a.emit("notify", map[string]any{
			"tier":        "ambient",
			"title":       "Turn complete",
			"body":        "Agent finished a turn.",
			"workspaceId": evt.WorkspaceID,
		})
	case evt.Kind == "state" && evt.State == agent.StateErrored:
		a.emit("notify", map[string]any{
			"tier":        "blocking",
			"title":       "Agent error",
			"body":        evt.Err,
			"workspaceId": evt.WorkspaceID,
		})
	}
}

// WriteToPty forwards keystrokes (a JSON number array from xterm.js) to the
// pane's pty. The []int→[]byte conversion is the inverse of the data pump.
func (a *App) WriteToPty(paneID string, data []int) error {
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	b := make([]byte, len(data))
	for i, v := range data {
		b[i] = byte(v)
	}
	_, err := br.Write(b)
	return err
}

// ResizePty applies new dimensions to the pane's pty.
func (a *App) ResizePty(paneID string, cols, rows uint16) error {
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	return br.Resize(cols, rows)
}

// CloseWorkspace cancels the workspace pump, tears down the monitor, and closes
// the pty — but keeps the workspace record in the registry (it can be reopened).
func (a *App) CloseWorkspace(id string) error {
	paneID := "pane-" + id
	a.mu.Lock()
	br := a.bridges[paneID]
	delete(a.bridges, paneID)
	mon := a.monitors[id]
	delete(a.monitors, id)
	var cancel context.CancelFunc
	if a.cancels != nil {
		cancel = a.cancels[id]
		delete(a.cancels, id)
	}
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if mon != nil {
		_ = mon.Teardown()
	}
	if br != nil {
		_ = br.Close()
	}
	return nil
}

// RemoveWorkspace closes the workspace (pump + monitor + pty) and removes it
// from the registry permanently.
func (a *App) RemoveWorkspace(id string) error {
	_ = a.CloseWorkspace(id)
	return a.store.Remove(id)
}

// OpenShell spawns a $SHELL -l pty for the shell drawer pane (paneID) in cwd.
// Output flows to the "pty:data:<paneID>" event. Separate from agent panes so
// the shell drawer has its own independent pty.
func (a *App) OpenShell(paneID, cwd string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	event := "pty:data:" + paneID
	ctx := context.Background()
	br, err := a.spawnPty(ctx, cwd, internalpty.LoginShellArgv(), event, a.emit, 220, 50)
	if err != nil {
		return fmt.Errorf("OpenShell spawn: %w", err)
	}
	a.putBridge(paneID, br)
	return nil
}

// GetSettings reads settings from disk; returns defaults if the file is absent.
func (a *App) GetSettings() (Settings, error) {
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{Theme: "gruvbox", Density: "dense", Font: "geist"}, nil
		}
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// SaveSettings atomically writes settings to disk.
func (a *App) SaveSettings(s Settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWriteApp(a.settingsPath, data)
}

// GetLayout reads the opaque layout JSON blob from disk.
func (a *App) GetLayout() (string, error) {
	data, err := os.ReadFile(a.layoutPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "{}", nil
		}
		return "", err
	}
	return string(data), nil
}

// SaveLayout atomically writes the opaque layout JSON blob.
func (a *App) SaveLayout(layoutJSON string) error {
	return atomicWriteApp(a.layoutPath, []byte(layoutJSON))
}

// atomicWriteApp writes data to path via temp file + rename (atomic on Linux),
// creating the parent dir if needed.
func atomicWriteApp(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// ListDir returns directory entries under absDir, gitignore-unaware.
func (a *App) ListDir(absDir string) ([]fspkg.Node, error) {
	return fspkg.ListDir(absDir, false)
}

// ReadFile returns the contents of absPath as a string.
func (a *App) ReadFile(absPath string) (string, error) {
	data, err := fspkg.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile atomically writes content to absPath.
func (a *App) WriteFile(absPath, content string) error {
	return fspkg.WriteFile(absPath, []byte(content))
}

// RevealInFiles opens the containing directory of absPath in the system file manager.
func (a *App) RevealInFiles(absPath string) error {
	return fspkg.RevealInFiles(absPath)
}

// Branches returns git branch names for the repo at repo.
func (a *App) Branches(repo string) ([]string, error) {
	return gitpkg.Branches(context.Background(), a.runner(), repo)
}

// Worktrees returns git worktree info for the repo at repo.
func (a *App) Worktrees(repo string) ([]gitpkg.WorktreeInfo, error) {
	return gitpkg.Worktrees(context.Background(), a.runner(), repo)
}

// Approve routes a tool-approval decision to the owning Monitor.
// reqID format: "<raw>:<workspaceID>". decision: "allow"|"deny"|"always".
// On "always", an AlwaysRule is persisted to Settings.
func (a *App) Approve(reqID, decision string) error {
	sep := strings.LastIndex(reqID, ":")
	if sep < 0 {
		return fmt.Errorf("invalid reqID format %q", reqID)
	}
	rawReqID := reqID[:sep]
	workspaceID := reqID[sep+1:]

	a.mu.Lock()
	mon, ok := a.monitors[workspaceID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("no active monitor for workspace %q", workspaceID)
	}

	d := agent.Decision{}
	switch decision {
	case "allow":
		d.Allow = true
	case "always":
		d.Allow = true
		d.Always = true
	case "deny":
		d.Allow = false
	default:
		return fmt.Errorf("unknown decision %q", decision)
	}

	if err := mon.Approve(rawReqID, d); err != nil {
		return err
	}

	if d.Always {
		tool := mon.LastApprovalTool()
		s, _ := a.GetSettings()
		s.AlwaysRules = append(s.AlwaysRules, AlwaysRule{
			Agent: "claude",
			Tool:  tool,
		})
		_ = a.SaveSettings(s)
	}
	return nil
}

// agentAdapter returns the Adapter for a known tool name, or nil for unknown.
func agentAdapter(tool string) agent.Adapter {
	switch tool {
	case "claude":
		return agent.NewClaude()
	case "opencode":
		return agent.NewOpencode()
	default:
		return nil
	}
}
