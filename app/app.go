// Package app hosts the Wails App: the bound-method API the untrusted Svelte
// frontend calls. Every argument crossing the IPC boundary is validated here —
// session ids against a charset allowlist, worktree paths against the
// configured project roots — and all tmux/git work is done via argv through
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
	"github.com/Miniature-Pug/perch/internal/config"
	gitpkg "github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
	"github.com/Miniature-Pug/perch/internal/worktree"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	switch model.Tool(tool) {
	case model.ToolClaude:
		return agent.NewClaude(), true
	case model.ToolOpencode:
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
//
// Security rationale: git.WorktreePath (internal/git/worktree.go:65-88)
// incorporates a config-supplied worktreeDir. When worktreeDir is absolute
// (e.g. "/etc") the result is "/etc/<handle>", and when it is relative with
// ".." components (e.g. "../../escape") the cleaned result can land outside
// projectPath. In both cases the derived treePath escapes the project root
// but may still be within a configured root — that is the correct boundary
// (e.g. the default case produces a sibling directory that IS under the root
// but not under projectPath). Paths that resolve outside every configured
// root are rejected.
//
// Symlink handling: both the lexical (unresolved) form of each root AND its
// EvalSymlinks-resolved form are checked. When a configured root is itself a
// symlink (e.g. "/sym" → "/real"), treePath is derived from the unresolved
// root path ("/sym/proj__worktrees/feat-x"), so checking only the resolved
// root ("/real") would wrongly reject it. Accepting under EITHER form does
// not open an escape: a hostile worktree_dir (absolute "/etc" or dotdot
// "../../escape") produces a treePath whose Clean form is under NEITHER the
// unresolved NOR the resolved form of any legitimate root, so it is still
// rejected.
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

// CreateAgent creates a linked worktree for branch under projectPath, seeds it,
// launches the agent in a detached tmux session, stamps @perch_session, and
// writes the shadow record. Returns the created session id (UUID for claude,
// empty for opencode which self-assigns its id). projectPath is validated
// under roots; branch via git.ValidRef; tool against known adapters; and the
// derived worktree path is checked to stay under a configured root.
func (a *App) CreateAgent(tool, projectPath, branch string) (string, error) {
	// Gate 1: projectPath must exist and be under a configured root.
	if err := validateWorktreeUnderRoots(projectPath, a.roots); err != nil {
		return "", err
	}
	// Gate 2: branch must be a valid git ref.
	if err := gitpkg.ValidRef(branch); err != nil {
		return "", fmt.Errorf("invalid branch: %w", err)
	}
	// Gate 3: tool must be a known agent adapter.
	adapter, ok := adapterFor(tool)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", tool)
	}

	ctx := context.Background()

	// Load config (best-effort; defaults apply on any error).
	var cfg *config.Config
	if gp, err := config.DefaultGlobalPath(); err == nil {
		if c, cerr := config.Load(gp, projectPath); cerr == nil {
			cfg = c
		}
	}

	worktreeDir, base := "", "HEAD"
	var files config.Files
	if cfg != nil {
		worktreeDir = cfg.WorktreeDir
		files = cfg.Files
		if cfg.BaseBranch != "" {
			base = cfg.BaseBranch
		}
	}

	handle := gitpkg.SlugifyBranch(branch)
	treePath, err := gitpkg.WorktreePath(projectPath, handle, worktreeDir)
	if err != nil {
		return "", err
	}

	// CRITICAL #2 — treePath containment guard.
	//
	// git.WorktreePath (internal/git/worktree.go:65-88) can return a path
	// outside projectPath when worktreeDir is set in config:
	//   - absolute worktreeDir: result is worktreeDir/<handle>, which may be
	//     completely outside the project (e.g. "/etc/<handle>").
	//   - relative worktreeDir with "..": Clean(projectPath/worktreeDir/<handle>)
	//     can escape projectPath (e.g. worktreeDir="../../escape" → sibling dir).
	//   - default (worktreeDir=""): result is <parent>/<base>__worktrees/<handle>,
	//     a sibling directory outside projectPath but inside its parent (the root).
	//
	// treePath does not exist yet, so EvalSymlinks cannot be used on it.
	// containedUnderRoots checks lexically that the cleaned path is at/under
	// one of the configured roots. A root-escaped treePath is rejected here
	// before any git/filesystem mutation occurs.
	if !containedUnderRoots(treePath, a.roots) {
		return "", fmt.Errorf("derived worktree path %q escapes all configured roots; check worktree_dir in config", treePath)
	}

	// Create the linked worktree.
	if err := gitpkg.AddWorktree(ctx, a.run, projectPath, branch, treePath, base); err != nil {
		return "", err
	}
	// Seed files into the worktree.
	if err := worktree.Seed(projectPath, treePath, files); err != nil {
		return "", fmt.Errorf("seed worktree: %w", err)
	}

	// Build the argv for the agent launch.
	bin := adapter.Name()
	if cfg != nil {
		bin = cfg.AgentBinary(model.Tool(tool))
	}

	var sid string
	var argv []string
	if model.Tool(tool) == model.ToolClaude {
		sid, err = newSessionID()
		if err != nil {
			return "", err
		}
		argv = append([]string{bin}, adapter.NewArgs(agent.NewOpts{SessionID: sid})...)
	} else {
		argv = append([]string{bin}, adapter.NewArgs(agent.NewOpts{})...)
	}

	// Launch the agent in a tmux pane.
	sessName := tmux.SessionName(projectPath)
	winName := tmux.WindowName(branch)
	paneID, err := a.tmux.Launch(ctx, sessName, winName, treePath, argv)
	if err != nil {
		return "", err
	}

	// Stamp @perch_session so the pane is visible to ListSessions.
	if sid != "" {
		_ = a.tmux.SetPaneOption(ctx, paneID, tmux.OptionPerchSession, sid)
	}

	// Write the shadow window record (best-effort; errors are non-fatal).
	baseDir, _ := state.StateDir()
	bootID, _ := a.tmux.BootID(ctx)
	if baseDir != "" {
		_ = state.SaveWindow(baseDir, model.Window{
			PaneKey:     paneID,
			Tool:        model.Tool(tool),
			SessionID:   sid,
			Tree:        treePath,
			TmuxSession: sessName,
			TmuxWindow:  winName,
			BootID:      bootID,
			Updated:     time.Now().Unix(),
		})
	}

	a.emit("sessions-changed")
	return sid, nil
}

// NewApp builds the production App. emit is a no-op until startup installs the
// wails runtime closure, so methods that emit are safe to call pre-startup
// (e.g. in headless tests that set their own emit).
func NewApp(roots []string) *App {
	return &App{
		tmux:    tmux.New(),
		run:     proc.ExecRunner{},
		roots:   roots,
		emit:    func(string, ...any) {},
		bridges: map[string]*ptyEntry{},
	}
}

// startup is the Wails OnStartup hook. It captures the runtime context and
// installs the production emit seam (runtime.EventsEmit). This is the ONLY place
// the wails runtime context is bound; all other code uses the emit seam.
func (a *App) startup(ctx context.Context) {
	a.emit = func(event string, data ...any) {
		wailsruntime.EventsEmit(ctx, event, data...)
	}
}

// shutdown closes every live attach pty. The agent sessions persist on the tmux
// server; only the GUI's attach clients are torn down.
func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, e := range a.bridges {
		if e.bridge != nil {
			_ = e.bridge.Close()
		}
		delete(a.bridges, id)
	}
}
