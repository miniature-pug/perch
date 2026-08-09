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

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/discover"
	"github.com/miniature-pug/perch/internal/envsync"
	fspkg "github.com/miniature-pug/perch/internal/fs"
	gitpkg "github.com/miniature-pug/perch/internal/git"
	modelpkg "github.com/miniature-pug/perch/internal/model"
	"github.com/miniature-pug/perch/internal/notify"
	"github.com/miniature-pug/perch/internal/proc"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
	"github.com/miniature-pug/perch/internal/safe"
	"github.com/wailsapp/wails/v2/pkg/options"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Named constants for values used in multiple places or formerly magic numbers.
const (
	// pty defaults — used identically by OpenWorkspace and OpenShell.
	defaultPtyCols = 220
	defaultPtyRows = 50

	// fsChangeChanBuf is the buffer size of the internal fs-change signal channel.
	fsChangeChanBuf = 64

	// Persistent-file names relative to the config directory.
	perchSettingsFile = "settings.json"
	perchLayoutFile   = "layout.json"

	// pty event-name prefixes; the full event name is prefix + paneID.
	ptyDataEventPrefix = "pty:data:"
	ptyExitEventPrefix = "pty:exit:"

	// homeShellPaneID is the reserved pane id for the home-screen shell. OpenShell
	// bypasses root-containment for this pane id (its cwd is OS-derived via
	// HomeShellCwd, not user IPC input). Must match the frontend ShellDrawer paneId.
	homeShellPaneID = "shell-home"

	// File-permission modes.
	settingsFileMode = 0o600

	// uiGitTimeout bounds a UI-initiated git subprocess call so a wedged or
	// pathologically large git operation cannot hang a frontend binding forever.
	// Kept generous so legitimate large repositories still complete.
	uiGitTimeout = 120 * time.Second

	// UUIDv4 byte masks applied in newWorkspaceID.
	// RFC 4122 §4.4: version nibble = 0100 (0x40), cleared with 0x0f;
	// variant bits = 10xx (0x80), cleared with 0x3f.
	uuidVersion4    = 0x40
	uuidVersionMask = 0x0f
	uuidVariantRFC  = 0x80
	uuidVariantMask = 0x3f
)

// fsDebounce is the coalescing window for fs:changed events emitted to the frontend.
const fsDebounce = 150 * time.Millisecond

// Settings defaults — source of truth for settings defaults; frontend mirrors these in frontend/src/lib/constants.ts.
const (
	defaultTheme   = "gruvbox"
	defaultDensity = "dense"
	defaultFont    = "geist"
	// defaultStaleThresholdDays is the number of days of inactivity after which a
	// worktree session is considered stale and shown in the cleanup panel banner.
	defaultStaleThresholdDays = 30
	// ptyMinDim / ptyMaxDim bound the pty dimensions the backend accepts. The
	// frontend already clamps to the same range (PTY_MAX_DIM in constants.ts), but
	// the backend must not trust that: a 0 dimension is invalid for a terminal and
	// the cap matches the uint16 max the frontend enforces.
	ptyMinDim = 1
	ptyMaxDim = 65535
)

// spawnPtyFunc and newMonitorFunc are injectable seams (real funcs in NewApp,
// replaced in tests for headless execution).
type spawnPtyFunc func(ctx context.Context, cwd string, argv []string, env []string, dataEvent, exitEvent string,
	emit internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error)

type newMonitorFunc func(tool string, adapter agent.Adapter) (agent.Monitor, error)

// newWatcherFunc is an injectable seam for the fs watcher (real fspkg.Watch in
// NewApp, replaced with a fake in tests for headless execution).
type newWatcherFunc func(absRoot string, onChange func(string)) (*fspkg.Watcher, error)

// agentAdapterFunc is an injectable seam returning the Adapter for a tool
// name (real agentAdapter in NewApp; a fake in tests so Detect() is
// deterministic regardless of what is on the host PATH).
type agentAdapterFunc func(tool string) agent.Adapter

// App is the Wails bound object.
type App struct {
	store *registry.Store
	roots []string

	// ctx is set by startup; used by CopyPath and other host-side runtime calls.
	ctx context.Context

	// run is the process runner for git invocations; nil falls back to a real
	// ExecRunner via runner(). Tests may inject a fake.
	run proc.Runner

	// emit delivers events to the frontend. Production wires this to a
	// runtime.EventsEmit closure in startup; tests inject a capture seam.
	emit internalpty.EmitFunc

	mu       sync.Mutex
	bridges  map[string]*internalpty.Bridge // paneID → Bridge
	monitors map[string]agent.Monitor       // workspaceID → Monitor

	// settingsMu guards the GetSettings→check-duplicate→append→SaveSettings
	// read-modify-write sequence in Approve and the GetSettings read in
	// maybeAutoApprove. It must NEVER be acquired while a.mu is held (lock order:
	// a.mu first, then settingsMu — but never nest a.mu inside settingsMu).
	// Prevents concurrent Approve(always) calls from losing rules.
	settingsMu sync.Mutex

	// pending maps a composed approval reqID ("<raw>:<workspaceID>") to the
	// in-flight ApprovalReq. The pump adds an entry when it surfaces a card;
	// Approve consumes it to resolve the tool+input authoritatively for an
	// always-rule (never trusting frontend-supplied values). Guarded by mu.
	pending map[string]agent.ApprovalReq

	cancels map[string]context.CancelFunc // workspaceID → pump/translation canceller

	spawnPty   spawnPtyFunc
	newMonitor newMonitorFunc
	newWatcher newWatcherFunc
	newAdapter agentAdapterFunc

	// debounce is the coalescing window for fs:changed events.
	debounce time.Duration

	settingsPath string
	layoutPath   string

	// notifier delivers OS desktop notifications. Constructed via notify.New() in
	// startup; tests inject a *notify.FakeNotifier. nil means no OS notifications
	// (safe — all call sites guard with notifier != nil).
	notifier notify.Notifier

	// focused tracks whether the Wails window currently has OS focus.
	// Default true (set in NewApp): OS notifications are suppressed while focused.
	// Updated by SetWindowFocus (bound method called by the frontend on window
	// focus/blur events).
	focused bool

	// baselineEnv is the app's os.Environ() captured at construction — the
	// reference the env-sync endpoint computes each delta against. Immutable.
	baselineEnv []string

	// envOverlay holds, per workspace id, the KEY=VALUE environment delta a
	// `perch reload` captured from the session terminal. It is applied on top of
	// os.Environ() at every agent-pane and drawer spawn (see mergeEnv). It lives
	// in memory ONLY and is NEVER persisted to disk — the payload may hold
	// secrets. Guarded by mu.
	envOverlay map[string][]string

	// envsync is the app-owned loopback endpoint that receives a session
	// terminal's environment and drives the relaunch. Stood up in startup; nil in
	// tests that never call startup (drawers then inject no env-sync handles).
	envsync *envsync.Listener
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
		pending:      map[string]agent.ApprovalReq{},
		cancels:      map[string]context.CancelFunc{},
		spawnPty:     internalpty.Spawn,
		newMonitor:   agent.NewMonitor,
		newWatcher:   fspkg.Watch,
		newAdapter:   agentAdapter,
		debounce:     fsDebounce,
		settingsPath: filepath.Join(registry.DefaultConfigDir(), perchSettingsFile),
		layoutPath:   filepath.Join(registry.DefaultConfigDir(), perchLayoutFile),
		focused:      true, // default: assume focused until the frontend reports otherwise
		baselineEnv:  os.Environ(),
		envOverlay:   map[string][]string{},
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
	a.ctx = ctx
	a.emit = func(event string, data ...any) {
		wailsruntime.EventsEmit(ctx, event, data...)
	}
	// Construct the OS notifier lazily so tests that set a.notifier before
	// startup is called are not overwritten (tests never call startup directly).
	if a.notifier == nil {
		a.notifier = notify.New()
	}
	// Capture the baseline environment (the delta reference) if a construction path
	// left it unset. NewApp already captures it; this guards raw &App{} paths.
	if a.baselineEnv == nil {
		a.baselineEnv = os.Environ()
	}
	// Stand up the env-sync endpoint. Best-effort: a loopback bind failure only
	// disables `perch reload` (the drawer then injects no PERCH_ENVSYNC_* handles
	// and the command prints a friendly "not in a perch session" error), so it
	// must never crash startup. No secret is involved in a bind failure.
	if a.envsync == nil {
		if ls, err := envsync.New(a.baselineEnv, a.onEnvSync); err == nil {
			a.envsync = ls
		} else {
			fmt.Fprintf(os.Stderr, "perch: env-sync endpoint unavailable; `perch reload` disabled: %v\n", err)
		}
	}
}

// onSecondInstance fires (in a Wails-owned goroutine) when a second `perch`
// process launches while one is already running. Raise the window and, if the
// launch carried a workspace query/path, route it to the frontend for selection.
func (a *App) onSecondInstance(data options.SecondInstanceData) {
	if a.ctx == nil {
		return // startup not complete; shouldn't happen but be safe
	}
	wailsruntime.WindowUnminimise(a.ctx)
	wailsruntime.WindowShow(a.ctx)

	// Derive query from the second process's os.Args[1:].
	var query string
	switch {
	case len(data.Args) >= 1 && data.Args[0] == "attach":
		query = strings.TrimSpace(strings.Join(data.Args[1:], " "))
	case len(data.Args) >= 1 && !strings.HasPrefix(data.Args[0], "-"):
		query = strings.TrimSpace(data.Args[0])
	}

	if query != "" {
		a.emit("workspace:attach", map[string]any{"query": query})
	}
}

// SetWindowFocus is a bound method called by the frontend whenever the Wails
// window gains or loses OS focus. It guards OS desktop notifications: they are
// suppressed while the window is focused and enabled when it is unfocused.
func (a *App) SetWindowFocus(focused bool) {
	a.mu.Lock()
	a.focused = focused
	a.mu.Unlock()
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
	// Reset pending approvals on shutdown so stale entries cannot outlive
	// their workspaces.
	a.pending = map[string]agent.ApprovalReq{}
	a.mu.Unlock()

	for _, c := range cancels {
		c()
	}
	// Tear down monitors (closing their exit listeners) BEFORE SIGKILLing the pane
	// process groups, so a late exit sentinel fired during shutdown has nowhere to
	// land and cannot surface a spurious "Agent exited" notification on the way out.
	for _, m := range monitors {
		_ = m.Teardown()
	}
	for _, b := range bridges {
		_ = b.Close()
	}
	// Tear down the env-sync endpoint so its loopback listener does not outlive
	// the app. Guarded: tests that never call startup leave it nil.
	if a.envsync != nil {
		_ = a.envsync.Close()
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

// validateRelFile rejects a repo-relative file path that is empty, absolute, or
// escapes the worktree via "..". Used to gate the `file` arg of the git hunk
// methods before it becomes a git pathspec.
func validateRelFile(file string) error {
	if file == "" || filepath.IsAbs(file) {
		return fmt.Errorf("file must be a non-empty relative path")
	}
	clean := filepath.Clean(file)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("file path %q escapes the worktree", file)
	}
	return nil
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
	b[6] = (b[6] & uuidVersionMask) | uuidVersion4
	b[8] = (b[8] & uuidVariantMask) | uuidVariantRFC
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// WorkspaceVM is the frontend-facing view of one workspace.
type WorkspaceVM struct {
	ID           string      `json:"id"`
	WorktreePath string      `json:"worktreePath"`
	RepoPath     string      `json:"repoPath"`
	Agent        string      `json:"agent"`
	Title        string      `json:"title"`
	Branch       string      `json:"branch"`
	State        agent.State `json:"state"`
	Caps         agent.Caps  `json:"caps"`
	PaneID       string      `json:"paneId"`
	LastActive   time.Time   `json:"lastActive"`
}

// paneIDFor returns the deterministic pane ID for a given workspace ID.
// The formula must stay in sync with OpenWorkspace.
func paneIDFor(workspaceID string) string {
	return "pane-" + workspaceID
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
			RepoPath:     w.RepoPath,
			Agent:        w.Agent,
			Title:        w.Title,
			Branch:       w.Branch,
			PaneID:       paneIDFor(w.ID),
			LastActive:   w.LastActive,
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
	Theme              string       `json:"theme"`
	Density            string       `json:"density"`
	Font               string       `json:"font"`
	DND                bool         `json:"dnd"`
	GlassDisabled      bool         `json:"glassDisabled,omitempty"`
	StaleThresholdDays int          `json:"staleThresholdDays,omitempty"`
	AlwaysRules        []AlwaysRule `json:"alwaysRules"`
}

// AlwaysRule persists an "always allow" approval rule.
type AlwaysRule struct {
	Agent   string `json:"agent"`
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"` // truncated display value; NOT the security boundary
	// Hash is the hex-encoded sha256 of the FULL (untruncated) tool input at the
	// time the user clicked "always". maybeAutoApprove matches on Hash, not Pattern,
	// so two inputs sharing a 4096-byte prefix cannot collide.
	Hash string `json:"hash,omitempty"`
}

// CreateWorkspace validates inputs, resolves/creates the worktree, persists the
// workspace to the registry, and returns its WorkspaceVM. It does NOT start the
// agent — call OpenWorkspace for that.
//
// Three modes (controlled by worktree and baseRef):
//
//	worktree && baseRef != ""  → new branch off baseRef, new linked tree (AddWorktree -b)
//	worktree && baseRef == ""  → existing branch, new linked tree (AddWorktreeExisting)
//	!worktree                  → no new tree; CheckoutBranch if branch != current;
//	                             WorktreePath == RepoPath, Worktree=false
//
// Returns ErrBranchInUse if a worktree session already tracks branch in this repo.
//
// title is the optional user-chosen session name. When blank (after trimming) it
// falls back to the branch slug, so behavior is unchanged when no name is given.
func (a *App) CreateWorkspace(agentName, repoPath, baseRef, branch, title string, worktree bool) (WorkspaceVM, error) {
	// Gate 1: repoPath must exist under a configured root.
	if err := validateWorktreeUnderRoots(repoPath, a.roots); err != nil {
		return WorkspaceVM{}, err
	}
	// Gate 2: branch must be a valid git ref.
	if err := gitpkg.ValidRef(branch); err != nil {
		return WorkspaceVM{}, fmt.Errorf("invalid branch: %w", err)
	}
	// Gate 3: agent must be known.
	if agentName != string(modelpkg.ToolClaude) && agentName != string(modelpkg.ToolOpencode) {
		return WorkspaceVM{}, fmt.Errorf("unknown agent %q", agentName)
	}

	ctx := context.Background()

	// Every mode needs a commit to branch from or check out. On an unborn HEAD
	// (freshly init'd repo, no commits) git errors cryptically deep in worktree
	// add / checkout; surface one clear message up front instead.
	if has, err := gitpkg.HasCommits(ctx, a.runner(), repoPath); err != nil {
		return WorkspaceVM{}, fmt.Errorf("check repository: %w", err)
	} else if !has {
		return WorkspaceVM{}, fmt.Errorf("create workspace: %w", gitpkg.ErrNoCommits)
	}

	var worktreePath string

	if worktree {
		// Collision check: reject if another worktree session already owns this branch.
		if _, found := a.WorkspaceForBranch(repoPath, branch); found {
			return WorkspaceVM{}, fmt.Errorf("create worktree: %w", gitpkg.ErrBranchInUse)
		}

		handle := gitpkg.SlugifyBranch(branch)
		treePath, err := gitpkg.WorktreePath(repoPath, handle, "")
		if err != nil {
			return WorkspaceVM{}, err
		}
		if !containedUnderRoots(treePath, a.roots) {
			return WorkspaceVM{}, fmt.Errorf("derived worktree path %q escapes all configured roots", treePath)
		}

		if baseRef != "" {
			// New-branch mode: git worktree add -b <branch> <tree> <baseRef>.
			if err := gitpkg.ValidRef(baseRef); err != nil {
				return WorkspaceVM{}, fmt.Errorf("invalid baseRef: %w", err)
			}
			if err := gitpkg.AddWorktree(ctx, a.runner(), repoPath, branch, treePath, baseRef); err != nil {
				return WorkspaceVM{}, fmt.Errorf("create worktree: %w", err)
			}
		} else {
			// Existing-branch mode: git worktree add <tree> <branch>.
			if err := gitpkg.AddWorktreeExisting(ctx, a.runner(), repoPath, branch, treePath); err != nil {
				return WorkspaceVM{}, fmt.Errorf("create worktree (existing branch): %w", err)
			}
		}
		worktreePath = treePath
	} else {
		// Non-worktree mode: run in the repo root. Only switch branches when the
		// target differs from the current branch. Bare `git checkout` only fails on
		// *conflict*, so a dirty-but-non-conflicting tree would silently carry
		// uncommitted changes across the switch; refuse instead and ask the user to
		// clean first. Attaching to the current branch needs no switch, so a dirty
		// tree is allowed there.
		current, err := gitpkg.CurrentBranch(ctx, a.runner(), repoPath)
		if err != nil {
			return WorkspaceVM{}, fmt.Errorf("current branch: %w", err)
		}
		if branch != current {
			dirty, err := gitpkg.WorktreeDirty(ctx, a.runner(), repoPath)
			if err != nil {
				return WorkspaceVM{}, fmt.Errorf("check worktree: %w", err)
			}
			if dirty {
				return WorkspaceVM{}, fmt.Errorf("checkout branch: %w", gitpkg.ErrWorktreeDirty)
			}
			if err := gitpkg.CheckoutBranch(ctx, a.runner(), repoPath, branch); err != nil {
				return WorkspaceVM{}, fmt.Errorf("checkout branch: %w", err)
			}
		}
		worktreePath = repoPath
	}

	id, err := newWorkspaceID()
	if err != nil {
		return WorkspaceVM{}, err
	}

	now := time.Now()
	// Resolve the session title: the user-chosen name when given, otherwise the
	// branch slug (the historical default), so behavior is unchanged when blank.
	title = strings.TrimSpace(title)
	if title == "" {
		title = gitpkg.SlugifyBranch(branch)
	}
	w := registry.Workspace{
		ID:           id,
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Worktree:     worktree,
		Agent:        agentName,
		Title:        title,
		Branch:       branch,
		BaseRef:      baseRef,
		LastActive:   now,
	}
	if err := a.store.Upsert(w); err != nil {
		// The git worktree (and, in new-branch mode, the new branch) already exist
		// on disk, but the record did not persist. Left as-is they orphan the tree
		// and, in new-branch mode, block every retry forever with ErrBranchExists.
		// Best-effort roll them back so a retry starts clean. Do NOT mask the
		// original persist error (rollback errors are swallowed intentionally).
		if worktree {
			rollbackCtx := context.Background()
			_ = gitpkg.RemoveWorktree(rollbackCtx, a.runner(), repoPath, worktreePath, true)
			if baseRef != "" {
				// New-branch mode (AddWorktree -b) created this branch; delete it.
				// Existing-branch mode did NOT create the branch, so it is left intact.
				_ = gitpkg.DeleteBranch(rollbackCtx, a.runner(), repoPath, branch, true)
			}
		}
		// Drop the in-memory record too: Upsert wrote it into the map before the
		// failed flush, so without this a phantom workspace (pointing at the now-
		// removed tree) would linger in List() and WorkspaceForBranch. Remove's own
		// flush will fail the same way; its error is intentionally ignored.
		_ = a.store.Remove(id)
		return WorkspaceVM{}, fmt.Errorf("persist workspace: %w", err)
	}

	return WorkspaceVM{
		ID:           id,
		WorktreePath: worktreePath,
		RepoPath:     repoPath,
		Agent:        agentName,
		Title:        title,
		Branch:       branch,
		PaneID:       paneIDFor(id),
		LastActive:   now,
		State:        agent.StateIdle,
	}, nil
}

// SetWorkspaceTitle renames a workspace. It validates id against the same charset
// allowlist used by the other id-taking bound methods, loads the record from the
// store (erroring on an unknown id), rejects a blank title (so the UI keeps the
// old name), and persists the new title. Auto-bound (whole App is bound).
func (a *App) SetWorkspaceTitle(id, title string) error {
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	w, ok := a.store.Get(id)
	if !ok {
		return fmt.Errorf("unknown workspace %q", id)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title must not be blank")
	}
	w.Title = title
	return a.store.Upsert(w)
}

// WorkspaceForBranch returns the ID of the worktree session that is tracking
// branch in repoPath, if any. Only worktree sessions (Worktree==true) are
// considered; non-worktree sessions may share a branch by design.
func (a *App) WorkspaceForBranch(repoPath, branch string) (id string, found bool) {
	for _, w := range a.store.List() {
		if w.Worktree && w.RepoPath == repoPath && w.Branch == branch {
			return w.ID, true
		}
	}
	return "", false
}

// OpenWorkspace spawns a login-shell pty for the workspace, calls Monitor.Prepare
// to obtain the agent launch command and install the side-channel, starts the
// monitor's event pump, writes the launch command into the pty, and forwards
// monitor events to the frontend. All goroutines are bound to a per-workspace
// context cancelled by CloseWorkspace/shutdown.
//
// mon.Start(wctx) is REQUIRED: without it no events ever flow from a real monitor.
func (a *App) OpenWorkspace(id string) error {
	// Validate workspace id before touching the store.
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	w, ok := a.store.Get(id)
	if !ok {
		return fmt.Errorf("unknown workspace %q", id)
	}

	// Bump LastActive on open: ListStaleSessions and sidebar ordering key on it,
	// so a session that is actively opened must not keep reading as stale (it was
	// only ever set at creation). Persist before spawning so the freshened
	// ordering is durable even if a later step fails.
	w.LastActive = time.Now()
	_ = a.store.Upsert(w)

	paneID := paneIDFor(id)
	event := ptyDataEventPrefix + paneID
	exitEvent := ptyExitEventPrefix + paneID

	wctx, cancel := context.WithCancel(context.Background())

	// Prepare the monitor BEFORE spawning the pty: Prepare creates the exit listener
	// (and, for claude, writes the hooks), and PaneEnv then yields the exit
	// sentinel's PERCH_EXIT_TOKEN/PERCH_EXIT_URL that must be present in the shell's
	// PROCESS environment at spawn time (env is inherited at exec and is never
	// echoed, unlike the typed launch line). A monitor/prepare failure needs no
	// bridge cleanup because the pty is not spawned yet.
	adpt := a.newAdapter(w.Agent)
	mon, err := a.newMonitor(w.Agent, adpt)
	if err != nil {
		cancel()
		return fmt.Errorf("new monitor: %w", err)
	}

	launchCmd, err := mon.Prepare(wctx, id, w.WorktreePath, w.LastSessionID)
	if err != nil {
		cancel()
		_ = mon.Teardown()
		return fmt.Errorf("monitor prepare: %w", err)
	}

	// Compose the pane env: os.Environ() underlays the monitor's pane env (the
	// exit sentinel's PERCH_EXIT_* handles, referenced by name) which underlays any
	// env-sync overlay captured for this workspace, so a `perch reload` reaches the
	// relaunched agent. mergeEnv dedups and protects the sentinel from the overlay.
	paneEnv := mergeEnv(os.Environ(), mon.PaneEnv(), a.overlayFor(id))

	br, err := a.spawnPty(wctx, w.WorktreePath, internalpty.LoginShellArgv(), paneEnv, event, exitEvent, a.emit, defaultPtyCols, defaultPtyRows)
	if err != nil {
		cancel()
		_ = mon.Teardown()
		return fmt.Errorf("spawn pty: %w", err)
	}

	// REQUIRED: start the monitor's event pump (translation/SSE), bound to wctx.
	mon.Start(wctx)

	// Wire the fs watcher with a wctx-bound debounce goroutine. The watcher is
	// started non-fatally: a failure degrades gracefully (no watcher) but never
	// fails OpenWorkspace.
	var watcher *fspkg.Watcher
	if a.newWatcher != nil {
		changes := make(chan string, fsChangeChanBuf)

		// Debounce goroutine: coalesces raw onChange signals into a single
		// fs:changed emit per debounce window, bound to wctx lifetime.
		go func() {
			defer safe.Recover("fs-debounce")
			var timer *time.Timer
			var timerC <-chan time.Time
			for {
				select {
				case <-wctx.Done():
					if timer != nil {
						timer.Stop()
					}
					return
				case <-changes:
					if timer == nil {
						timer = time.NewTimer(a.debounce)
						timerC = timer.C
					}
					// else: within window — coalesce (do nothing)
				case <-timerC:
					a.emit("fs:changed", map[string]any{"workspaceId": id, "path": w.WorktreePath})
					timer = nil
					timerC = nil
				}
			}
		}()

		onChange := func(_ string) {
			select {
			case changes <- "":
			default:
			}
		}
		// best-effort: a watcher failure must not fail OpenWorkspace
		if wch, werr := a.newWatcher(w.WorktreePath, onChange); werr == nil {
			watcher = wch
		}
	}

	a.mu.Lock()
	oldBr := a.bridges[paneID]
	oldMon := a.monitors[id]
	oldCancel := a.cancels[id]
	a.bridges[paneID] = br
	a.monitors[id] = mon
	// Composite cancel: cancels the wctx (stopping all goroutines) and closes
	// the watcher. Rides CloseWorkspace, re-open displacement, and shutdown.
	a.cancels[id] = func() {
		cancel()
		if watcher != nil {
			_ = watcher.Close()
		}
	}
	a.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	// Tear down the OLD monitor (closing its exit listener) BEFORE SIGKILLing the
	// old pane's process group, so a late exit sentinel from the displaced shell has
	// nowhere to land and cannot surface a spurious "Agent exited" on reopen.
	if oldMon != nil {
		_ = oldMon.Teardown()
	}
	if oldBr != nil {
		_ = oldBr.Close()
	}

	if launchCmd != "" {
		// adpt is non-nil here for every reachable case: newMonitor above returns
		// an error for any tool other than claude/opencode (bailing before this
		// point), and agentAdapter returns a non-nil adapter for both of those.
		// A nil adpt (only possible via a test seam that decouples newAdapter from
		// newMonitor) deliberately falls through to the write — do NOT rewrite this
		// to `adpt == nil || !adpt.Detect()`, which would nil-panic on adpt.Name().
		if adpt != nil && !adpt.Detect() {
			// Agent CLI is missing from PATH. Skip writing the launch command
			// (which would otherwise surface as a raw shell "command not found")
			// and surface a clear, blocking signal. The shell stays usable.
			a.emit("notify", map[string]any{
				"tier":        "blocking",
				"title":       "Agent not found",
				"body":        fmt.Sprintf("%q is not installed or not on PATH. Install it, then reopen this session.", adpt.Name()),
				"workspaceId": id,
			})
		} else {
			_, _ = br.Write([]byte(launchCmd))
		}
	}

	// Forward monitor events to the frontend. Forward-and-continue: emit and move
	// on, never blocking on a user decision. Exits on wctx cancellation, since
	// mon.Events() is never closed.
	go func() {
		defer safe.Recover("workspace-event-pump")
		for {
			select {
			case <-wctx.Done():
				return
			case evt, ok := <-mon.Events():
				if !ok {
					return
				}
				// Stamp WorkspaceID so the frontend can match events to the correct
				// workspace.
				evt.WorkspaceID = id
				// Compose the approval ReqID as "<raw>:<workspaceID>" so that Approve()
				// can parse and route it via strings.LastIndex(":"). Copy the
				// ApprovalReq to avoid mutating the monitor's own pointee.
				if evt.Approval != nil {
					rawReqID := evt.Approval.ReqID
					a2 := *evt.Approval
					a2.ReqID = rawReqID + ":" + id
					evt.Approval = &a2
					// Always-allow auto-approval: if this request exactly matches a
					// persisted rule, allow it silently and suppress the card +
					// blocking notification (a routine notify keeps it visible).
					if a.maybeAutoApprove(id, rawReqID, a2, mon) {
						continue
					}
					// Register the pending approval so Approve() can resolve the
					// tool+input authoritatively when the user clicks Always.
					a.mu.Lock()
					if a.pending == nil {
						a.pending = map[string]agent.ApprovalReq{}
					}
					a.pending[a2.ReqID] = a2
					a.mu.Unlock()
				}
				// Session-resume: when the agent reports a new session id, persist
				// it so the next OpenWorkspace call can pass it as resumeID.
				// Validate the session id before persisting; an invalid id
				// (e.g. containing shell metacharacters) is silently dropped so it
				// can never be concatenated into a shell launch command later.
				if evt.SessionID != "" && validateSessionID(evt.SessionID) == nil {
					if cur, ok := a.store.Get(id); ok && cur.LastSessionID != evt.SessionID {
						cur.LastSessionID = evt.SessionID
						_ = a.store.Upsert(cur)
					}
				}
				a.emit("agent:event", evt)
				a.dispatchNotify(evt)
			}
		}
	}()

	return nil
}

// maybeAutoApprove auto-allows an incoming approval request when it exactly
// matches a persisted AlwaysRule (same agent, same tool, byte-identical input).
// On a match it allows the request via the monitor, emits a routine-tier
// transparency notification so the auto-approval is never silent, and returns
// true so the caller suppresses the approval card and the blocking notification.
//
// Matching is EXACT input equality — never a glob — so an always-rule can never
// grant more than the byte-identical request the user originally approved.
// Rules with an empty Pattern never match (no tool-wide auto-allow hole). If the
// monitor's Approve fails, it returns false so the card surfaces normally rather
// than the request being silently dropped.
func (a *App) maybeAutoApprove(workspaceID, rawReqID string, req agent.ApprovalReq, mon agent.Monitor) bool {
	if req.Tool == "" || req.Input == "" {
		return false
	}
	agentName := string(modelpkg.ToolClaude)
	if w, ok := a.store.Get(workspaceID); ok && w.Agent != "" {
		agentName = w.Agent
	}
	// Hold settingsMu (read side) so we see a consistent snapshot of settings
	// and don't race with a concurrent Approve(always) write.
	// DEADLOCK GUARD: a.mu must NOT be held before settingsMu is taken; callers
	// of maybeAutoApprove are outside any a.mu critical section.
	a.settingsMu.Lock()
	s, err := a.GetSettings()
	a.settingsMu.Unlock()
	if err != nil {
		return false
	}
	matched := false
	for _, r := range s.AlwaysRules {
		if r.Agent != agentName || r.Tool != req.Tool {
			continue
		}
		// The SHA-256 hash of the full (untruncated) tool input is the sole
		// authoritative match key. Pattern is display-only (it is truncated to
		// MaxApprovalInputLen, so two inputs sharing a 4096-byte prefix collide on
		// Pattern, matching on it would be a privilege-escalation hole). A rule
		// without a Hash, or a request without an InputHash, never auto-approves
		// (fail closed). There is no backward-compat requirement, so no legacy
		// pattern fallback: any pre-Hash rule simply prompts once and is re-saved
		// with a hash.
		if r.Hash != "" && req.InputHash != "" && r.Hash == req.InputHash {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	if err := mon.Approve(rawReqID, agent.Decision{Allow: true}); err != nil {
		return false
	}
	a.emit("notify", map[string]any{
		"tier":        "routine",
		"title":       "Auto-approved",
		"body":        req.Tool + " (always-allow rule)",
		"workspaceId": workspaceID,
	})
	return true
}

// dispatchNotify translates an agent.Event into a "notify" Wails event at the
// appropriate tier and, for blocking-tier events when the window is unfocused,
// also fires an OS desktop notification. Do-Not-Disturb mutes only the ambient
// and routine tiers and never the blocking tier; since only blocking events fire
// an OS notification, DND has no bearing on the OS-notify path.
func (a *App) dispatchNotify(evt agent.Event) {
	var tier, title, body string
	switch {
	case evt.Kind == "approval" && evt.State == agent.StateAwaitingApproval:
		tier, title, body = "blocking", "Approval needed", "An agent is waiting for your decision."
	case evt.Kind == "question" && evt.State == agent.StateAwaitingInput:
		tier, title, body = "blocking", "Question", "An agent is asking you to choose."
	case evt.Kind == "state" && evt.State == agent.StateDone:
		tier, title, body = "ambient", "Turn complete", "Agent finished a turn."
	case evt.Kind == "state" && evt.State == agent.StateErrored:
		tier, title, body = "blocking", "Agent error", evt.Err
	case evt.Kind == "state" && evt.State == agent.StateExited:
		// Prune any pending approval for the exited workspace: a PreToolUse-time
		// crash leaves an unresolved a.pending entry whose reqID's agent is gone;
		// without this it would false-resolve to a live approval card on a webview
		// reload (ListWorkspaces reports StateExited, but pendingApprovals would
		// still surface the dead card). Keys are "<raw>:<workspaceID>".
		a.mu.Lock()
		_, live := a.monitors[evt.WorkspaceID]
		suffix := ":" + evt.WorkspaceID
		for k := range a.pending {
			if strings.HasSuffix(k, suffix) {
				delete(a.pending, k)
			}
		}
		a.mu.Unlock()
		// Suppress a spurious "Agent exited" on INTENTIONAL teardown: CloseWorkspace /
		// displacement / shutdown deregister the monitor (under a.mu) before the pane
		// is torn down, so a late exit sentinel here finds no live monitor and must
		// not notify. A genuine crash keeps its monitor registered (only the agent
		// process, inside the still-alive shell, died) so it still surfaces.
		if !live {
			return
		}
		tier, title, body = "blocking", "Agent exited", evt.Err
		if body == "" {
			body = "The agent process ended."
		}
	default:
		return
	}

	// Always emit the in-app Wails notification event unconditionally.
	a.emit("notify", map[string]any{
		"tier":        tier,
		"title":       title,
		"body":        body,
		"workspaceId": evt.WorkspaceID,
	})

	// OS desktop notification: only for blocking-tier events and only when the
	// window is unfocused. DND is deliberately NOT consulted here: DND never mutes
	// the blocking tier, and only blocking fires an OS notification.
	if tier != "blocking" {
		return
	}
	a.mu.Lock()
	focused := a.focused
	a.mu.Unlock()
	if focused {
		return
	}
	if a.notifier != nil {
		_ = a.notifier.Notify(title, body)
	}
}

// WriteToPty forwards keystrokes (a JSON number array from xterm.js) to the
// pane's pty. The []int→[]byte conversion is the inverse of the data pump.
func (a *App) WriteToPty(paneID string, data []int) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
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
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	return br.Resize(clampPtyDim(cols), clampPtyDim(rows))
}

// clampPtyDim bounds a pty dimension to [ptyMinDim, ptyMaxDim]. A 0 dimension is
// invalid for a terminal and becomes ptyMinDim; anything over ptyMaxDim is capped.
func clampPtyDim(v uint16) uint16 {
	if v < ptyMinDim {
		return ptyMinDim
	}
	if v > ptyMaxDim {
		return ptyMaxDim
	}
	return v
}

// CloseWorkspace cancels the workspace pump, tears down the monitor, and closes
// the pty — but keeps the workspace record in the registry (it can be reopened).
func (a *App) CloseWorkspace(id string) error {
	// Validate workspace id before touching the store.
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	paneID := paneIDFor(id)
	shellPaneID := "shell-" + id
	a.mu.Lock()
	br := a.bridges[paneID]
	delete(a.bridges, paneID)
	// The workspace shell drawer registers its own pty under "shell-<id>" (see
	// OpenShell). Close and drop it here too — otherwise it leaks until shutdown,
	// left running against a now-deleted worktree cwd after RemoveWorkspace.
	shellBr := a.bridges[shellPaneID]
	delete(a.bridges, shellPaneID)
	mon := a.monitors[id]
	delete(a.monitors, id)
	var cancel context.CancelFunc
	if a.cancels != nil {
		cancel = a.cancels[id]
		delete(a.cancels, id)
	}
	// Purge pending approvals belonging to this workspace so that a closed
	// workspace does not accumulate phantom entries in the pending map.
	// Pending keys have the form "<raw>:<workspaceID>" (see Approve / event pump);
	// validateSessionID forbids ':' in ids so the suffix match is unambiguous.
	// Collect the raw reqIDs of the still-pending approvals so we can deny them via
	// the monitor after releasing a.mu — a blocked claude hook POST / opencode
	// permission would otherwise hang until its own timeout when the workspace is
	// closed out from under it.
	suffix := ":" + id
	var pendingRaw []string
	for k := range a.pending {
		if strings.HasSuffix(k, suffix) {
			pendingRaw = append(pendingRaw, k[:len(k)-len(suffix)])
			delete(a.pending, k)
		}
	}
	a.mu.Unlock()

	// Deny each in-flight approval through the monitor BEFORE teardown so the
	// agent's blocked hook returns promptly instead of hanging. Do this before
	// cancel()/Teardown() so the monitor is still live to deliver the verdict.
	// (mon.Approve is safe to call outside a.mu; it does not take a.mu.)
	if mon != nil {
		for _, raw := range pendingRaw {
			_ = mon.Approve(raw, agent.Decision{Allow: false})
		}
	}

	if cancel != nil {
		cancel()
	}
	if mon != nil {
		_ = mon.Teardown()
	}
	if br != nil {
		_ = br.Close()
	}
	if shellBr != nil {
		_ = shellBr.Close()
	}
	return nil
}

// ErrWorktreeDirty aliases the git-package sentinel so app callers and tests can
// match it with errors.Is without importing gitpkg directly.
var ErrWorktreeDirty = gitpkg.ErrWorktreeDirty

// RemoveWorkspace closes the workspace and removes it from the registry. For
// Worktree==true sessions it also removes the linked worktree tree from disk; a
// dirty tree returns ErrWorktreeDirty and leaves the record intact (caller should
// offer a force-confirm calling ForceRemoveWorkspace). The branch is never deleted
// here. For Worktree==false (in-repo permanent) sessions only the registry record
// is dropped — the repo root and its branch are never touched.
func (a *App) RemoveWorkspace(id string) error {
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	w, ok := a.store.Get(id)
	if !ok {
		return nil // already gone — idempotent
	}
	if w.Worktree {
		ctx := context.Background()
		// If the worktree dir was deleted outside perch, WorktreeDirty (git -C
		// <missing> status) would error and the record could never be dropped —
		// leaving a ghost session forever. Detect the missing path up front and
		// treat the worktree as already gone: skip the git remove and drop the
		// record cleanly. Only a present-but-dirty tree returns ErrWorktreeDirty.
		if worktreePathGone(w.WorktreePath) {
			_ = a.CloseWorkspace(id)
			return a.store.Remove(id)
		}
		dirty, err := gitpkg.WorktreeDirty(ctx, a.runner(), w.WorktreePath)
		if err != nil {
			return fmt.Errorf("check worktree dirty: %w", err)
		}
		if dirty {
			return ErrWorktreeDirty
		}
		if err := gitpkg.RemoveWorktree(ctx, a.runner(), w.RepoPath, w.WorktreePath, false); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}
	_ = a.CloseWorkspace(id)
	return a.store.Remove(id)
}

// worktreePathGone reports whether a worktree path no longer exists on disk
// (deleted outside perch). An empty path is treated as gone. A non-ENOENT stat
// error (e.g. permission) is treated as NOT gone so the normal git path runs and
// surfaces the real error rather than silently dropping the record.
func worktreePathGone(path string) bool {
	if path == "" {
		return true
	}
	if _, err := os.Stat(path); err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return false
}

// ForceRemoveWorkspace force-removes the linked worktree tree (discarding any
// uncommitted changes) then drops the registry record. The branch is kept. For
// Worktree==false sessions it behaves like RemoveWorkspace (record-only drop).
func (a *App) ForceRemoveWorkspace(id string) error {
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	w, ok := a.store.Get(id)
	if !ok {
		return nil
	}
	if w.Worktree {
		ctx := context.Background()
		if err := gitpkg.RemoveWorktree(ctx, a.runner(), w.RepoPath, w.WorktreePath, true); err != nil {
			return fmt.Errorf("force-remove worktree: %w", err)
		}
	}
	_ = a.CloseWorkspace(id)
	return a.store.Remove(id)
}

// StaleSessionVM is the frontend-facing view of one stale worktree session.
type StaleSessionVM struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Branch     string    `json:"branch"`
	Agent      string    `json:"agent"`
	LastActive time.Time `json:"lastActive"`
	Added      int       `json:"added"`
	Removed    int       `json:"removed"`
	Clean      bool      `json:"clean"`
	Merged     bool      `json:"merged"`
	Safe       bool      `json:"safe"`
}

// ListStaleSessions returns Worktree==true sessions whose LastActive is older than
// the configured threshold. Non-worktree sessions are always excluded. Per session
// it computes clean (no uncommitted changes), merged (branch merged into BaseRef,
// falling back to "HEAD" for old records), and diffstat. An error computing any of
// these is treated conservatively (dirty/unmerged/zero) so the row shows unchecked.
func (a *App) ListStaleSessions() ([]StaleSessionVM, error) {
	days, err := a.staleThreshold()
	if err != nil {
		return nil, fmt.Errorf("ListStaleSessions: read settings: %w", err)
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	ws := a.store.List()
	ctx := context.Background()
	out := make([]StaleSessionVM, 0)
	for _, w := range ws {
		if !w.Worktree {
			continue
		}
		if !w.LastActive.Before(cutoff) {
			continue
		}
		dirty, err := gitpkg.WorktreeDirty(ctx, a.runner(), w.WorktreePath)
		if err != nil {
			dirty = true
		}
		clean := !dirty
		base := w.BaseRef
		if base == "" {
			base = "HEAD"
		}
		merged, err := gitpkg.BranchMerged(ctx, a.runner(), w.RepoPath, w.Branch, base)
		if err != nil {
			merged = false
		}
		diffs, err := gitpkg.DiffStat(ctx, a.runner(), w.WorktreePath)
		var added, removed int
		if err == nil {
			for _, d := range diffs {
				added += d.Added
				removed += d.Removed
			}
		}
		out = append(out, StaleSessionVM{
			ID:         w.ID,
			Title:      w.Title,
			Branch:     w.Branch,
			Agent:      w.Agent,
			LastActive: w.LastActive,
			Added:      added,
			Removed:    removed,
			Clean:      clean,
			Merged:     merged,
			Safe:       clean && merged,
		})
	}
	return out, nil
}

// CleanupSessions removes the given sessions: stops the agent/pty, removes the
// linked worktree tree (force if force==true), and deletes the branch (-d, or -D if
// force). Non-worktree sessions are record-only (never a git op). Errors are
// accumulated; all ids are attempted before returning.
func (a *App) CleanupSessions(ids []string, force bool) error {
	ctx := context.Background()
	var errs []error
	for _, id := range ids {
		if err := validateSessionID(id); err != nil {
			errs = append(errs, fmt.Errorf("invalid id %q: %w", id, err))
			continue
		}
		w, ok := a.store.Get(id)
		if !ok {
			continue
		}
		if !w.Worktree {
			_ = a.CloseWorkspace(id)
			_ = a.store.Remove(id)
			continue
		}
		// When force==false, check the tree is clean BEFORE tearing anything down.
		// Previously CloseWorkspace ran unconditionally (killing the agent/pty) and
		// only THEN did RemoveWorktree(force=false) fail on a dirty tree — leaving a
		// kept record whose live session was already dead. Mirror RemoveWorkspace:
		// on a dirty tree, skip this id entirely (record + session/monitor stay
		// alive) and record the error. A missing worktree path is treated as clean
		// (already gone) so the record can be dropped. force==true bypasses the
		// check and force-removes below.
		if !force && !worktreePathGone(w.WorktreePath) {
			dirty, derr := gitpkg.WorktreeDirty(ctx, a.runner(), w.WorktreePath)
			if derr != nil {
				errs = append(errs, fmt.Errorf("check worktree dirty %s: %w", id, derr))
				continue
			}
			if dirty {
				errs = append(errs, fmt.Errorf("remove worktree %s: %w", id, ErrWorktreeDirty))
				continue
			}
		}
		_ = a.CloseWorkspace(id)
		if err := gitpkg.RemoveWorktree(ctx, a.runner(), w.RepoPath, w.WorktreePath, force); err != nil {
			// Worktree removal failed (e.g. dirty tree, force=false): keep the record so
			// the session is retryable and the tree is never orphaned. Skip branch delete.
			errs = append(errs, fmt.Errorf("remove worktree %s: %w", id, err))
			continue
		}
		if err := gitpkg.DeleteBranch(ctx, a.runner(), w.RepoPath, w.Branch, force); err != nil {
			// Branch kept (e.g. unmerged with -d) — safe; the tree is already gone, so still
			// drop the record below.
			errs = append(errs, fmt.Errorf("delete branch %s: %w", id, err))
		}
		if err := a.store.Remove(id); err != nil {
			errs = append(errs, fmt.Errorf("remove record %s: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// OpenShell spawns a $SHELL -l pty for the shell drawer pane (paneID) in cwd.
// Output flows to the "pty:data:<paneID>" event. Separate from agent panes so
// the shell drawer has its own independent pty.
func (a *App) OpenShell(paneID, cwd string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	// The home shell pane ("shell-home") has an OS-derived cwd (HomeShellCwd) that is
	// not user IPC input and is almost never under a configured project root, so the
	// root-containment guard is bypassed for it alone. All other panes still validate.
	if paneID != homeShellPaneID {
		if err := validateWorktreeUnderRoots(cwd, a.roots); err != nil {
			return fmt.Errorf("invalid shell cwd: %w", err)
		}
	}
	event := ptyDataEventPrefix + paneID
	exitEvent := ptyExitEventPrefix + paneID
	ctx := context.Background()
	// The home drawer (shell-home) has no workspace: it is a plain login shell that
	// inherits the process environment unchanged (nil env), with no env-sync handles
	// and no overlay. A per-workspace drawer (shell-<id>) instead receives the
	// env-sync handles so `perch reload` run inside it can post its environment, plus
	// any overlay already captured for the workspace (so a reopen carries it too).
	var env []string
	if paneID != homeShellPaneID {
		workspaceID := strings.TrimPrefix(paneID, "shell-")
		var injected []string
		if a.envsync != nil {
			if tok, terr := a.envsync.TokenFor(workspaceID); terr == nil {
				injected = envsyncPaneEnv(a.envsync.URL(), tok, workspaceID)
			}
		}
		env = mergeEnv(os.Environ(), injected, a.overlayFor(workspaceID))
	}
	br, err := a.spawnPty(ctx, cwd, internalpty.LoginShellArgv(), env, event, exitEvent, a.emit, defaultPtyCols, defaultPtyRows)
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
			return defaultSettings(), nil
		}
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		backupPath, renameErr := quarantineCorrupt(a.settingsPath)
		if renameErr == nil {
			fmt.Fprintf(os.Stderr, "perch: settings %s was corrupt and has been quarantined to %s; using defaults\n", a.settingsPath, backupPath)
		} else {
			fmt.Fprintf(os.Stderr, "perch: settings %s was corrupt; quarantine to %s failed (%v); using defaults\n", a.settingsPath, backupPath, renameErr)
		}
		return defaultSettings(), nil
	}
	return s, nil
}

// defaultSettings is the source of truth for settings defaults; the frontend
// mirrors these in frontend/src/lib/constants.ts. Returned on first run (no
// settings file) and when an existing settings file is corrupt.
func defaultSettings() Settings {
	return Settings{Theme: defaultTheme, Density: defaultDensity, Font: defaultFont, StaleThresholdDays: defaultStaleThresholdDays}
}

// quarantineCorrupt renames path to a timestamped .corrupt-* backup and
// returns the backup path. The caller logs the outcome.
func quarantineCorrupt(path string) (string, error) {
	backupPath := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	return backupPath, os.Rename(path, backupPath)
}

// HomeShellCwd returns the working directory for the home shell pane: the process
// cwd (os.Getwd), falling back to the user's home dir, then "/". Never empty.
func (a *App) HomeShellCwd() string {
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		return cwd
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "/"
}

// SaveSettings atomically writes settings to disk. It is the public Wails-bound
// method the frontend calls to persist a whole settings blob (theme/density/font/
// dnd/alwaysRules). It takes settingsMu so a frontend write is serialized with
// Approve(always)'s read-append-write; this prevents torn writes and prevents two
// concurrent appends from losing each other.
//
// DEADLOCK GUARD: settingsMu must not be entered while a.mu is held, and
// saveSettingsLocked must not acquire a.mu (it doesn't — it only marshals+writes).
// Approve already holds settingsMu across its read-modify-write and therefore calls
// saveSettingsLocked directly; calling this public method there would self-deadlock
// (sync.Mutex is not reentrant).
//
// Residual limit (inherent to whole-blob replacement, not a cut corner): the lock
// cannot stop a stale whole-blob overwrite — a frontend SaveSettings carrying a
// snapshot read before an Approve(always) append will still clobber the new rule.
// This is last-writer-wins on a full-document PUT, not a data race. Closing it fully
// would require a version field + compare-and-set; a naive "re-read and preserve
// on-disk AlwaysRules" merge is NOT a valid fix because it would break the frontend's
// legitimate rule-deletion path (setAlwaysRules deliberately sends a shorter list,
// which a preserve-merge would treat as rules to resurrect). The lock is the correct
// fix for the in-scope torn-write / concurrent-append races.
func (a *App) SaveSettings(s Settings) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	return a.saveSettingsLocked(s)
}

// saveSettingsLocked is the unlocked inner write. Callers MUST hold settingsMu.
func (a *App) saveSettingsLocked(s Settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWriteApp(a.settingsPath, data)
}

// staleThreshold returns the configured stale threshold, falling back to the
// default when the stored value is zero (old settings files without the field).
func (a *App) staleThreshold() (int, error) {
	s, err := a.GetSettings()
	if err != nil {
		return 0, err
	}
	if s.StaleThresholdDays <= 0 {
		return defaultStaleThresholdDays, nil
	}
	return s.StaleThresholdDays, nil
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
// creating the parent dir if needed. The file is always created with mode 0600
// (owner read/write only) because it may carry a token-bearing settings payload.
// os.CreateTemp already uses 0600, but we set it explicitly — before any write —
// so the invariant is auditable and consistent with claude_monitor.go's atomicWrite.
func atomicWriteApp(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, registry.ConfigDirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Set settingsFileMode before writing so there is no window where content is readable
	// at a looser mode. Mirror the same invariant as claude_monitor.go atomicWrite.
	if err := os.Chmod(tmpName, settingsFileMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
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

// ListDir returns directory entries under absDir, honoring .gitignore patterns
// in that directory (gitignore-aware). Entries matching any pattern in
// absDir/.gitignore are excluded from the result.
func (a *App) ListDir(absDir string) ([]fspkg.Node, error) {
	if err := validateWorktreeUnderRoots(absDir, a.roots); err != nil {
		return nil, err
	}
	return fspkg.ListDir(absDir, true)
}

// ReadFile returns the contents of absPath as a string.
func (a *App) ReadFile(absPath string) (string, error) {
	if err := validateWorktreeUnderRoots(absPath, a.roots); err != nil {
		return "", err
	}
	data, err := fspkg.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile atomically writes content to absPath.
func (a *App) WriteFile(absPath, content string) error {
	// Validate absPath itself (not just its Dir) so a symlink-as-final-component
	// that resolves outside root is caught.
	//
	// Primary path: absPath resolves successfully via EvalSymlinks (file exists or is
	// a non-dangling symlink). validateWorktreeUnderRoots does the full resolve+check.
	if err := validateWorktreeUnderRoots(absPath, a.roots); err == nil {
		// absPath resolves inside roots — allow.
		return fspkg.WriteFile(absPath, []byte(content))
	}
	// absPath may be a new (not-yet-created) file, OR it may be a symlink
	// (dangling or resolving outside root). Distinguish these cases:
	//
	// If absPath exists as a symlink (even dangling), reject it — a write would
	// follow the symlink to an outside-root target, which is the escape vector.
	if _, lstatErr := os.Lstat(absPath); lstatErr == nil {
		// absPath exists on disk (as a symlink or regular file). If
		// validateWorktreeUnderRoots rejected it above, reject here too.
		return fmt.Errorf("WriteFile: path %q is outside configured roots or escapes via symlink", absPath)
	}
	// absPath does not exist (truly a new file). Validate by checking:
	//   1. The parent dir must exist and resolve inside roots.
	//   2. The cleaned absPath must be lexically under the resolved parent
	//      (guards against ".." or other path escapes in the filename component).
	parentDir := filepath.Dir(absPath)
	if err := validateWorktreeUnderRoots(parentDir, a.roots); err != nil {
		return err
	}
	cleanAbs := filepath.Clean(absPath)
	if !filepath.IsAbs(cleanAbs) {
		return fmt.Errorf("WriteFile: path must be absolute")
	}
	resolvedParent, err := filepath.EvalSymlinks(parentDir)
	if err != nil {
		return fmt.Errorf("WriteFile: resolve parent %q: %w", parentDir, err)
	}
	expectedPrefix := resolvedParent + string(filepath.Separator)
	if cleanAbs != resolvedParent && !strings.HasPrefix(cleanAbs, expectedPrefix) {
		return fmt.Errorf("WriteFile: path %q escapes its parent dir", absPath)
	}
	return fspkg.WriteFile(absPath, []byte(content))
}

// RevealInFiles opens the containing directory of absPath in the system file manager.
func (a *App) RevealInFiles(absPath string) error {
	if err := validateWorktreeUnderRoots(absPath, a.roots); err != nil {
		return err
	}
	return fspkg.RevealInFiles(absPath)
}

// CopyPath copies absPath to the system clipboard via the Wails runtime.
// WebKit2GTK's navigator.clipboard is unreliable, so the copy happens host-side.
func (a *App) CopyPath(absPath string) error {
	// Validate before the ctx guard so tests can exercise the security boundary
	// without a Wails runtime (ctx == nil → clipboard no-op after validation).
	if err := validateWorktreeUnderRoots(absPath, a.roots); err != nil {
		return err
	}
	if a.ctx == nil {
		return nil
	}
	return wailsruntime.ClipboardSetText(a.ctx, absPath)
}

// Branches returns git branch names for the repo at repo.
func (a *App) Branches(repo string) ([]string, error) {
	if err := validateWorktreeUnderRoots(repo, a.roots); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.Branches(ctx, a.runner(), repo)
}

// Approve routes a tool-approval decision to the owning Monitor.
// reqID format: "<raw>:<workspaceID>". decision: "allow"|"deny"|"always".
// On "always", an AlwaysRule is persisted to Settings.
// PendingApprovalVM is one still-undecided approval request, tagged with the
// workspace it belongs to so the frontend can rebuild its per-workspace queue.
type PendingApprovalVM struct {
	WorkspaceID string            `json:"workspaceId"`
	Req         agent.ApprovalReq `json:"req"`
}

// PendingApprovals returns every approval request still awaiting a decision, so
// the frontend can rebuild its approval queue after a reload or a late open (the
// agent:event carrying an approval is a one-shot — if it arrives before the
// workspace is listed or after a webview reload it is otherwise lost and the
// agent's blocked hook wedges forever). The pending map key is the composed
// ReqID "<raw>:<workspaceID>"; derive WorkspaceID the same way Approve routes it
// (strings.LastIndex ":"). The stored ApprovalReq.ReqID is already the composed
// id the frontend keys its queue on, so it is used verbatim as Req.
func (a *App) PendingApprovals() []PendingApprovalVM {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Return an empty (never nil) slice so it marshals to [] not null.
	out := make([]PendingApprovalVM, 0, len(a.pending))
	for key, req := range a.pending {
		sep := strings.LastIndex(key, ":")
		if sep < 0 {
			continue // malformed key — skip rather than mis-route
		}
		out = append(out, PendingApprovalVM{
			WorkspaceID: key[sep+1:],
			Req:         req,
		})
	}
	return out
}

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

	// Consume the pending approval for this exact reqID. tool+input come from
	// the backend's record of what was actually surfaced — never from the
	// frontend — so an always-rule cannot be forged to grant something the user
	// did not see. Resolving by reqID (not a racy "last approval" accessor) is
	// correct even when multiple approvals are pending across workspaces.
	a.mu.Lock()
	req, hadPending := a.pending[reqID]
	delete(a.pending, reqID)
	a.mu.Unlock()

	if d.Always && hadPending && req.Tool != "" && req.Input != "" {
		agentName := string(modelpkg.ToolClaude)
		if w, ok := a.store.Get(workspaceID); ok && w.Agent != "" {
			agentName = w.Agent
		}
		// Hold settingsMu across the entire read-modify-write so that concurrent
		// Approve(always) calls cannot interleave and lose rules.
		// DEADLOCK GUARD: a.mu is released above before settingsMu is taken.
		a.settingsMu.Lock()
		s, err := a.GetSettings()
		if err != nil {
			a.settingsMu.Unlock()
			return err
		}
		dup := false
		for _, r := range s.AlwaysRules {
			// Dedup on the same authoritative key used for matching (the hash).
			if r.Agent == agentName && r.Tool == req.Tool && r.Hash != "" && r.Hash == req.InputHash {
				dup = true
				break
			}
		}
		if !dup {
			s.AlwaysRules = append(s.AlwaysRules, AlwaysRule{
				Agent:   agentName,
				Tool:    req.Tool,
				Pattern: req.Input,     // truncated display value
				Hash:    req.InputHash, // hash of full input, authoritative match key
			})
			// settingsMu is already held here; call the unlocked inner helper to
			// avoid a re-entrant deadlock (SaveSettings would re-take settingsMu).
			_ = a.saveSettingsLocked(s)
		}
		a.settingsMu.Unlock()
	}
	return nil
}

// DiffStat returns per-file diff summary for worktree, validated against roots.
func (a *App) DiffStat(worktree string) ([]gitpkg.FileDiff, error) {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.DiffStat(ctx, a.runner(), worktree)
}

// Hunks returns the unified hunks for a single file in worktree.
func (a *App) Hunks(worktree, file string) ([]gitpkg.Hunk, error) {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return nil, err
	}
	if err := validateRelFile(file); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.Hunks(ctx, a.runner(), worktree, file)
}

// StageHunk applies hunk `index` of file to the index (git apply --cached).
// index is relative to the current Hunks(worktree, file) output; the frontend
// re-fetches hunks after each call so indices stay fresh.
func (a *App) StageHunk(worktree, file string, index int) error {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return err
	}
	if err := validateRelFile(file); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.StageHunk(ctx, a.runner(), worktree, file, index)
}

// DiscardHunk reverses hunk `index` of file in the working tree (git apply --reverse).
func (a *App) DiscardHunk(worktree, file string, index int) error {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return err
	}
	if err := validateRelFile(file); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.DiscardHunk(ctx, a.runner(), worktree, file, index)
}

// UnstageHunk moves the staged hunk at merged Hunks(worktree, file) index `index`
// back to the working tree (git apply --reverse --cached). `index` is the same
// merged-Hunks() index StageHunk/DiscardHunk take — NOT a `git diff --cached`
// position — and MUST identify a Staged==true hunk. It is the inverse of StageHunk
// and touches the index only, never the working-tree content; the frontend
// re-fetches hunks after each call so indices stay fresh.
func (a *App) UnstageHunk(worktree, file string, index int) error {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return err
	}
	if err := validateRelFile(file); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.UnstageHunk(ctx, a.runner(), worktree, file, index)
}

// RepoInfo is a frontend-friendly summary of a discovered git repository.
// JSON tags are frozen — do not rename.
type RepoInfo struct {
	// Path is the absolute path to the repository root (main worktree).
	Path string `json:"path"`
	// Name is the display name (base directory name of the main worktree).
	Name string `json:"name"`
	// Branch is the branch checked out in the main worktree, or "" when
	// the repository has no commits yet.
	Branch string `json:"branch"`
	// Worktrees lists all non-bare working trees (main + linked worktrees).
	// Head is always "" because model.Tree does not carry the commit SHA;
	// the dialog does not need it for a fresh-install selection list.
	Worktrees []gitpkg.WorktreeInfo `json:"worktrees"`
}

// DiscoverRepos discovers git repositories under all configured roots and
// returns a deduplicated, frontend-ready slice ordered by frecency (cold
// start → alphabetical). It is intended for the New Session dialog on a
// fresh install when there are no existing workspaces.
//
// Best-effort: per-root errors are silently skipped. An error is returned only
// when every root failed. An empty (non-nil) slice is returned when no
// repositories are found.
func (a *App) DiscoverRepos() ([]RepoInfo, error) {
	// byPath deduplicates across multiple roots.
	byPath := make(map[string]struct{})
	out := make([]RepoInfo, 0)

	var lastErr error
	okCount := 0

	for _, root := range a.roots {
		ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
		pts, err := discover.Projects(
			ctx,
			a.runner(),
			root,
			discover.Options{},
			map[string]discover.ProjectStat{},
			time.Now().Unix(),
		)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		okCount++

		for _, pt := range pts {
			if _, seen := byPath[pt.Project.Path]; seen {
				continue
			}
			byPath[pt.Project.Path] = struct{}{}

			// Extract the branch from the main working tree.
			var branch string
			for _, tr := range pt.Trees {
				if tr.IsMain {
					branch = tr.Branch
					break
				}
			}

			// Map model.Tree → gitpkg.WorktreeInfo (Head is not available
			// from ProjectTrees; it is left as the zero value "").
			wts := make([]gitpkg.WorktreeInfo, 0, len(pt.Trees))
			for _, tr := range pt.Trees {
				wts = append(wts, gitpkg.WorktreeInfo{
					Path:   tr.Path,
					Branch: tr.Branch,
				})
			}

			out = append(out, RepoInfo{
				Path:      pt.Project.Path,
				Name:      pt.Project.Name,
				Branch:    branch,
				Worktrees: wts,
			})
		}
	}

	if okCount == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

// agentAdapter returns the Adapter for a known tool name, or nil for unknown.
func agentAdapter(tool string) agent.Adapter {
	switch tool {
	case string(modelpkg.ToolClaude):
		return agent.NewClaude()
	case string(modelpkg.ToolOpencode):
		return agent.NewOpencode()
	default:
		return nil
	}
}
