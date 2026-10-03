// Package app hosts the Wails App. The App is the bound-method API that the
// untrusted Svelte frontend calls. This package checks every argument that
// crosses the IPC boundary: workspace ids against a charset allowlist, and
// worktree paths against the configured project roots. Subprocesses always
// spawn through argv (internal/proc or internal/pty), never through a shell.
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

// Named constants for values used in many places, or values that were formerly
// magic numbers.
const (
	// pty defaults. OpenWorkspace and OpenShell use them the same way.
	defaultPtyCols = 220
	defaultPtyRows = 50

	// fsChangeChanBuf is the buffer size of the internal fs-change signal channel.
	fsChangeChanBuf = 64

	// Persistent-file names relative to the config directory.
	perchSettingsFile = "settings.json"
	perchLayoutFile   = "layout.json"

	// pty event-name prefixes. The full event name is the prefix plus the paneID.
	ptyDataEventPrefix = "pty:data:"
	ptyExitEventPrefix = "pty:exit:"

	// homeShellPaneID is the reserved pane id for the home-screen shell.
	// OpenShell bypasses root-containment for this pane id, because its cwd
	// comes from HomeShellCwd (OS-derived), not from user IPC input. This id
	// must match the frontend ShellDrawer paneId.
	homeShellPaneID = "shell-home"

	// File-permission modes.
	settingsFileMode = 0o600

	// uiGitTimeout limits a git subprocess call that the UI starts, so a stuck
	// or very large git operation cannot hang a frontend binding forever. The
	// value stays generous so large real repositories still finish.
	uiGitTimeout = 120 * time.Second

	// UUIDv4 byte masks applied in newWorkspaceID.
	// RFC 4122 §4.4: the version nibble is 0100 (0x40), cleared with 0x0f.
	// The variant bits are 10xx (0x80), cleared with 0x3f.
	uuidVersion4    = 0x40
	uuidVersionMask = 0x0f
	uuidVariantRFC  = 0x80
	uuidVariantMask = 0x3f
)

// fsDebounce is the coalescing window for fs:changed events emitted to the frontend.
const fsDebounce = 150 * time.Millisecond

// titleDebounce is the coalescing window for native OS window-title refreshes.
// State events can arrive in bursts (many workspaces transitioning at once); the
// debounce collapses a burst into a single WindowSetTitle call.
const titleDebounce = 150 * time.Millisecond

// Settings defaults. This is the source of truth for settings defaults. The
// frontend mirrors these values in frontend/src/lib/constants.ts.
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

// spawnPtyFunc and newMonitorFunc are injectable seams. NewApp wires them to
// the real functions; tests replace them for headless execution.
type spawnPtyFunc func(ctx context.Context, cwd string, argv []string, env []string, dataEvent, exitEvent string,
	emit internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error)

type newMonitorFunc func(tool string, adapter agent.Adapter) (agent.Monitor, error)

// newWatcherFunc is an injectable seam for the fs watcher. NewApp wires it to
// the real fspkg.Watch function; tests replace it with a fake for headless
// execution.
type newWatcherFunc func(absRoot string, onChange func(string)) (*fspkg.Watcher, error)

// agentAdapterFunc is an injectable seam that returns the Adapter for a tool
// name. NewApp wires it to the real agentAdapter function. Tests use a fake so
// Detect() gives the same result regardless of what is on the host PATH.
type agentAdapterFunc func(tool string) agent.Adapter

// App is the Wails bound object.
type App struct {
	store *registry.Store
	roots []string

	// startup sets ctx. CopyPath and other host-side runtime calls use it.
	ctx context.Context

	// run is the process runner for git invocations. When run is nil, runner()
	// falls back to a real ExecRunner. Tests can inject a fake.
	run proc.Runner

	// emit delivers events to the frontend. Production code wires it to a
	// runtime.EventsEmit closure in startup. Tests inject a capture seam.
	emit internalpty.EmitFunc

	mu       sync.Mutex
	bridges  map[string]*internalpty.Bridge // paneID → Bridge
	monitors map[string]agent.Monitor       // workspaceID → Monitor

	// settingsMu guards the GetSettings→check-duplicate→append→SaveSettings
	// read-modify-write sequence in Approve, and the GetSettings read in
	// maybeAutoApprove. Code must never acquire settingsMu while a.mu is held.
	// The lock order is a.mu first, then settingsMu; never nest a.mu inside
	// settingsMu. This lock stops concurrent Approve(always) calls from losing
	// rules.
	settingsMu sync.Mutex

	// pending maps a composed approval reqID ("<raw>:<workspaceID>") to the
	// in-flight ApprovalReq. The pump adds an entry when it surfaces a card.
	// Approve consumes the entry to resolve the tool and input for an
	// always-rule; it never trusts frontend-supplied values. mu guards this map.
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

	// notifier delivers OS desktop notifications. startup constructs it via
	// notify.New(); tests inject a *notify.FakeNotifier. A nil notifier means no
	// OS notifications; this is safe because every call site checks
	// notifier != nil.
	notifier notify.Notifier

	// focused tracks whether the Wails window has OS focus now. NewApp
	// sets the default to true, so OS notifications stay suppressed until the
	// frontend reports a focus change. SetWindowFocus (a bound method) updates
	// this field when the window gains or loses focus.
	focused bool

	// baselineEnv is the app's os.Environ(), captured at construction. The
	// env-sync endpoint computes each delta against this reference. baselineEnv
	// never changes after construction.
	baselineEnv []string

	// perchBin is the running binary's own absolute path (os.Executable, with
	// symlinks resolved), captured once at startup. The binary lives at
	// bin/perch and launches by absolute path, so it is not on PATH. The
	// drawer's reload button types this quoted absolute path. The workspace
	// drawer also prepends its directory to PATH and exports PERCH_BIN, so
	// `perch reload` resolves regardless of PATH. perchBin stays empty when
	// os.Executable fails; callers then fall back to a bare `perch`.
	perchBin string

	// envOverlay holds, per workspace id, the KEY=VALUE environment delta that a
	// `perch reload` captured from the session terminal. mergeEnv applies it on
	// top of os.Environ() at every agent-pane and drawer spawn. envOverlay lives
	// in memory only; it is never persisted to disk, because the payload may
	// hold secrets. mu guards this map.
	envOverlay map[string][]string

	// envsync is the app-owned loopback endpoint that receives a session
	// terminal's environment and drives the relaunch. startup stands it up. It
	// stays nil in tests that never call startup; drawers then inject no
	// env-sync handles.
	envsync *envsync.Listener

	// titleMu guards titleTimer, the debounce timer that coalesces rapid state
	// changes into a single native OS window-title refresh (WIN #6). It is a
	// leaf lock: code must never hold it while acquiring a.mu. refreshWindowTitle
	// (the timer callback) takes a.mu only after scheduleTitleUpdate has released
	// titleMu. Concurrent event-pump goroutines can call scheduleTitleUpdate at
	// once; titleMu serializes the Stop-and-reset step so they cannot race the
	// timer.
	titleMu    sync.Mutex
	titleTimer *time.Timer

	// opMu guards opLocks. opLocks holds one mutex per workspace id while any
	// lifecycle operation on that id is running or waiting. OpenWorkspace,
	// CloseWorkspace, RemoveWorkspace, ForceRemoveWorkspace and the per-id
	// body of CleanupSessions hold it for their whole run, so two lifecycle
	// operations on one workspace never interleave (APP-10): a concurrent
	// reopen cannot leak a pty or monitor, and a close or remove cannot run
	// while an open is half done. The event pump never takes it, so holding it
	// while tearing down a monitor cannot deadlock the pump. Entries are
	// reference-counted and dropped when the last holder or waiter releases.
	opMu    sync.Mutex
	opLocks map[string]*opLock
}

// opLock is one workspace's lifecycle mutex plus the number of goroutines
// holding or waiting for it.
type opLock struct {
	mu   sync.Mutex
	refs int
}

// lockWorkspace acquires id's lifecycle mutex and returns its release
// function. It is safe on an App built as a bare literal.
func (a *App) lockWorkspace(id string) (unlock func()) {
	a.opMu.Lock()
	if a.opLocks == nil {
		a.opLocks = map[string]*opLock{}
	}
	l := a.opLocks[id]
	if l == nil {
		l = &opLock{}
		a.opLocks[id] = l
	}
	l.refs++
	a.opMu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		a.opMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(a.opLocks, id)
		}
		a.opMu.Unlock()
	}
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

// runner returns the configured process runner. When run is unset, runner
// returns a real ExecRunner, so bare App{} literals work in tests that call
// git methods.
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
	// startup constructs the OS notifier lazily, so it does not overwrite a
	// notifier that a test set before calling startup. Tests never call startup
	// directly.
	if a.notifier == nil {
		a.notifier = notify.New()
	}
	// startup captures the baseline environment (the delta reference) if a
	// construction path left it unset. NewApp already captures it; this guards
	// raw &App{} paths.
	if a.baselineEnv == nil {
		a.baselineEnv = os.Environ()
	}
	// startup captures the running binary's own absolute path once (symlinks
	// resolved), so the drawer's reload button and a manual `perch reload`
	// resolve the actual binary. The binary lives at bin/perch, launches by
	// absolute path, and is not on PATH. On failure startup leaves perchBin
	// empty; callers then fall back to a bare `perch` and behavior does not
	// regress.
	if a.perchBin == "" {
		if exe, err := os.Executable(); err == nil {
			if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
				exe = resolved
			}
			a.perchBin = exe
		}
	}
	// startup stands up the env-sync endpoint on a best-effort basis. A
	// loopback bind failure only disables `perch reload`: the drawer then
	// injects no PERCH_ENVSYNC_* handles, and the command prints a friendly
	// "not in a perch session" error. This failure must never crash startup.
	// No secret is involved in a bind failure.
	if a.envsync == nil {
		if ls, err := envsync.New(a.baselineEnv, a.onEnvSync); err == nil {
			a.envsync = ls
		} else {
			fmt.Fprintf(os.Stderr, "perch: env-sync endpoint unavailable; `perch reload` disabled: %v\n", err)
		}
	}
}

// onSecondInstance fires in a Wails-owned goroutine when a second `perch`
// process launches while one is already running. It raises the window. If the
// launch carried a workspace query or path, it routes the query to the
// frontend for selection.
func (a *App) onSecondInstance(data options.SecondInstanceData) {
	if a.ctx == nil {
		return // startup has not finished; this should not happen, but check anyway
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

// SetWindowFocus is a bound method that the frontend calls whenever the Wails
// window gains or loses OS focus. It guards OS desktop notifications: perch
// suppresses them while the window is focused and enables them when the
// window is unfocused.
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
	// shutdown tears down monitors (closing their exit listeners) before it
	// SIGKILLs the pane process groups. This order means a late exit sentinel
	// fired during shutdown has nowhere to land, so it cannot surface a
	// spurious "Agent exited" notification on the way out.
	for _, m := range monitors {
		_ = m.Teardown()
	}
	for _, b := range bridges {
		_ = b.Close()
	}
	// shutdown tears down the env-sync endpoint so its loopback listener does
	// not outlive the app. This step is guarded: tests that never call startup
	// leave envsync nil.
	if a.envsync != nil {
		_ = a.envsync.Close()
	}
	// shutdown stops any pending window-title refresh, so the debounce timer
	// does not fire a WindowSetTitle call into a window that shutdown is
	// tearing down.
	a.titleMu.Lock()
	if a.titleTimer != nil {
		a.titleTimer.Stop()
	}
	a.titleMu.Unlock()
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
// escapes the worktree via "..". It gates the `file` arg of the git hunk
// methods before the arg becomes a git pathspec.
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
	// WillResume is true when reopening this session resumes the prior agent
	// conversation instead of starting fresh. This happens when a session id
	// was persisted (registry.Workspace.LastSessionID != ""). ListWorkspaces
	// projects this field read-only for the sidebar "resumes previous
	// conversation" affordance.
	WillResume bool `json:"willResume"`
	// BaseRef is the ref this worktree session was forked from
	// (registry.Workspace.BaseRef). BaseRef is empty for old records and for
	// in-repo (non-worktree) sessions. When BaseRef is empty, the frontend
	// hides the fork-point.
	BaseRef string `json:"baseRef,omitempty"`
}

// paneIDFor returns the deterministic pane ID for a given workspace ID.
// The formula must stay in sync with OpenWorkspace.
func paneIDFor(workspaceID string) string {
	return "pane-" + workspaceID
}

// workspaceIDForShellPane recovers the workspace ID from a shell drawer pane
// ID. A session's default shell is "shell-<id>"; extra shell tabs use
// "shell-<id>_<n>". The workspace UUID is hex plus hyphens and never contains
// '_', so cutting on the first '_' after the "shell-" prefix isolates the id.
// workspaceIDForShellPane returns "" for a non-shell pane; callers guard the
// home shell ("shell-home") separately before calling. Keeping the id
// recoverable from any shell pane lets one session own many shells while
// OpenShell and ReloadAgentEnv still resolve its env-sync token and overlay.
func workspaceIDForShellPane(paneID string) string {
	if !strings.HasPrefix(paneID, "shell-") {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(paneID, "shell-"), "_")
	return id
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
			// WIN #2: reopening resumes the prior conversation iff a session id
			// was persisted. WIN #3: surface the fork point (empty for old/in-repo).
			WillResume: w.LastSessionID != "",
			BaseRef:    w.BaseRef,
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

// needsAttention reports whether a live agent state needs the user's action.
// This is the same "need you" signal the sidebar surfaces: the agent is
// blocked, waiting for an approval decision or for the user to answer a
// question.
func needsAttention(s agent.State) bool {
	return s == agent.StateAwaitingApproval || s == agent.StateAwaitingInput
}

// countNeedsAttention counts how many of the given live states need the user.
// countNeedsAttention is pure: it takes no locks and touches no App state, so
// tests can check the count logic on its own, apart from the untestable GTK
// WindowSetTitle call. See windowTitle and refreshWindowTitle.
func countNeedsAttention(states []agent.State) int {
	n := 0
	for _, s := range states {
		if needsAttention(s) {
			n++
		}
	}
	return n
}

// windowTitle renders the native OS window title for a given attention count.
// It returns "perch" when nothing needs the user, and "perch (N need you)"
// otherwise. windowTitle is pure and has full unit-test coverage; as a
// defensive measure, it treats a non-positive count as zero.
func windowTitle(needCount int) string {
	if needCount <= 0 {
		return "perch"
	}
	return fmt.Sprintf("perch (%d need you)", needCount)
}

// attentionCount snapshots the live monitors under a.mu. It then reads each
// monitor's CurrentState() outside the lock, mirroring ListWorkspaces, so a
// monitor's state accessor is never called while a.mu is held. attentionCount
// returns how many monitors need the user. Registry records without a live
// monitor default to idle and never count.
func (a *App) attentionCount() int {
	a.mu.Lock()
	mons := make([]agent.Monitor, 0, len(a.monitors))
	for _, m := range a.monitors {
		mons = append(mons, m)
	}
	a.mu.Unlock()
	states := make([]agent.State, 0, len(mons))
	for _, m := range mons {
		states = append(states, m.CurrentState())
	}
	return countNeedsAttention(states)
}

// scheduleTitleUpdate coalesces rapid state changes into a single, debounced
// native window-title refresh (WIN #6). Every workspace's event-pump goroutine
// can call it concurrently and safely: titleMu serializes the Stop-and-reset
// step of the shared timer, so concurrent state events cannot race it. The
// refresh recomputes the count and calls WindowSetTitle; it fires titleDebounce
// after the last call in a burst.
func (a *App) scheduleTitleUpdate() {
	a.titleMu.Lock()
	defer a.titleMu.Unlock()
	if a.titleTimer != nil {
		a.titleTimer.Stop()
	}
	a.titleTimer = time.AfterFunc(titleDebounce, a.refreshWindowTitle)
}

// refreshWindowTitle recomputes the attention count across all live monitors,
// then pushes the native OS window title through the Wails runtime. Only the
// GTK call is untestable, because it needs a real display and main thread. The
// count and title logic it delegates to (attentionCount, windowTitle) is pure
// and has unit-test coverage. A nil ctx (in tests, or before startup wires it)
// turns the GTK call into a no-op after the count runs, so tests still
// exercise the pure path. safe.Recover guards the timer goroutine.
func (a *App) refreshWindowTitle() {
	defer safe.Recover("window-title")
	title := windowTitle(a.attentionCount())
	if a.ctx == nil {
		return
	}
	// WindowSetTitle marshals gtk_window_set_title to the main thread on the
	// Linux/GTK backend. This is the same runtime that WindowUnminimise and
	// WindowShow use.
	wailsruntime.WindowSetTitle(a.ctx, title)
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

// CreateWorkspace validates inputs, resolves or creates the worktree, persists
// the workspace to the registry, and returns its WorkspaceVM. CreateWorkspace
// does not start the agent; call OpenWorkspace for that.
//
// Three modes (controlled by worktree and baseRef):
//
//	worktree && baseRef != ""  → new branch off baseRef, new linked tree (AddWorktree -b)
//	worktree && baseRef == ""  → existing branch, new linked tree (AddWorktreeExisting)
//	!worktree                  → no new tree; CheckoutBranch if branch != current;
//	                             WorktreePath == RepoPath, Worktree=false
//
// CreateWorkspace returns ErrBranchInUse if a worktree session already tracks
// branch in this repo.
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
	// (a freshly created repo with no commits), git produces a cryptic error
	// deep inside worktree add or checkout. CreateWorkspace surfaces one clear
	// message up front instead.
	if has, err := gitpkg.HasCommits(ctx, a.runner(), repoPath); err != nil {
		return WorkspaceVM{}, fmt.Errorf("check repository: %w", err)
	} else if !has {
		return WorkspaceVM{}, fmt.Errorf("create workspace: %w", gitpkg.ErrNoCommits)
	}

	var worktreePath string

	if worktree {
		// Collision check: CreateWorkspace rejects the request if another worktree
		// session already owns this branch.
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
		// Non-worktree mode: CreateWorkspace runs in the repo root. It only
		// switches branches when the target differs from the current branch. A
		// bare `git checkout` fails only on conflict, so a dirty-but-non-conflicting
		// tree could silently carry uncommitted changes across the switch.
		// CreateWorkspace refuses instead and asks the user to clean the tree
		// first. Attaching to the current branch needs no switch, so a dirty tree
		// is allowed there.
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
	// CreateWorkspace resolves the session title: the user-chosen name when
	// given, otherwise the branch slug (the historical default). Behavior is
	// unchanged when title is blank.
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
		// The git worktree, and in new-branch mode the new branch, already exist
		// on disk, but the record did not persist. Left as-is, they orphan the
		// tree, and in new-branch mode they block every retry forever with
		// ErrBranchExists. CreateWorkspace rolls them back on a best-effort basis,
		// so a retry starts clean. This rollback must not mask the original
		// persist error; rollback errors are swallowed intentionally.
		if worktree {
			rollbackCtx := context.Background()
			_ = gitpkg.RemoveWorktree(rollbackCtx, a.runner(), repoPath, worktreePath, true)
			if baseRef != "" {
				// New-branch mode (AddWorktree -b) created this branch, so the
				// rollback deletes it. Existing-branch mode did not create the
				// branch, so the rollback leaves it intact.
				_ = gitpkg.DeleteBranch(rollbackCtx, a.runner(), repoPath, branch, true)
			}
		}
		// CreateWorkspace also drops the in-memory record: Upsert wrote it into
		// the map before the failed flush, so without this step a phantom
		// workspace, pointing at the now-removed tree, would linger in List() and
		// WorkspaceForBranch. Remove's own flush fails the same way;
		// CreateWorkspace ignores that error intentionally.
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

// SetWorkspaceTitle renames a workspace. It runs these steps:
//  1. It validates id against the same charset allowlist that the other
//     id-taking bound methods use.
//  2. It loads the record from the store; an unknown id returns an error.
//  3. It rejects a blank title, so the UI keeps the old name.
//  4. It persists the new title.
//
// SetWorkspaceTitle is auto-bound, because Wails binds the whole App.
func (a *App) SetWorkspaceTitle(id, title string) error {
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title must not be blank")
	}
	// Update mutates only the Title of the current record, under the store
	// lock, so a concurrent LastSessionID or LastActive write is never lost,
	// and a removed workspace is never resurrected.
	if _, err := a.store.Update(id, func(w *registry.Workspace) error {
		w.Title = title
		return nil
	}); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return fmt.Errorf("unknown workspace %q", id)
		}
		return err
	}
	return nil
}

// WorkspaceForBranch returns the ID of the worktree session that tracks branch
// in repoPath, if any exists. WorkspaceForBranch considers only worktree
// sessions (Worktree==true); non-worktree sessions may share a branch by
// design.
func (a *App) WorkspaceForBranch(repoPath, branch string) (id string, found bool) {
	for _, w := range a.store.List() {
		if w.Worktree && w.RepoPath == repoPath && w.Branch == branch {
			return w.ID, true
		}
	}
	return "", false
}

// hookRewriter is implemented by monitors whose Prepare installs shared,
// sentinel-keyed hooks in the worktree settings.json (ClaudeMonitor). On
// Reopen, OpenWorkspace re-asserts the live monitor's hooks through this seam,
// after the displaced monitor's Teardown strips every perch hook group (see
// the call site for the reason). Monitors with per-instance side channels,
// such as opencode's SSE server, which assigns itself a fresh port and
// listener on every Prepare, do not implement hookRewriter. These monitors
// have no shared settings.json state to lose, so they have nothing to
// re-assert.
type hookRewriter interface{ RewriteHooks() error }

// OpenWorkspace opens a workspace. It runs these steps:
//  1. It spawns a login-shell pty for the workspace.
//  2. It calls Monitor.Prepare to get the agent launch command and install
//     the side-channel.
//  3. It starts the monitor's event pump.
//  4. It writes the launch command into the pty.
//  5. It forwards monitor events to the frontend.
//
// A per-workspace context binds all goroutines; CloseWorkspace or shutdown
// cancels that context.
//
// mon.Start(wctx) is required. Without it, no events ever flow from a real
// monitor.
func (a *App) OpenWorkspace(id string) error {
	// Validate workspace id before touching the store.
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	// Serialize with every other lifecycle operation on this workspace.
	unlock := a.lockWorkspace(id)
	defer unlock()

	// OpenWorkspace bumps LastActive when it opens the workspace.
	// ListStaleSessions and the sidebar order both key on LastActive, so an
	// actively opened session must not keep reading as stale (previously
	// LastActive was only ever set at creation). OpenWorkspace persists this
	// change before it spawns the pty, so the refreshed order survives even if
	// a later step fails. Update touches only LastActive and never recreates a
	// record that a concurrent remove already dropped.
	w, err := a.store.Update(id, func(w *registry.Workspace) error {
		w.LastActive = time.Now()
		return nil
	})
	if errors.Is(err, registry.ErrNotFound) {
		return fmt.Errorf("unknown workspace %q", id)
	}
	if err != nil {
		// A failed flush keeps the in-memory record unchanged; opening the
		// session is still possible.
		var ok bool
		if w, ok = a.store.Get(id); !ok {
			return fmt.Errorf("unknown workspace %q", id)
		}
	}

	paneID := paneIDFor(id)
	event := ptyDataEventPrefix + paneID
	exitEvent := ptyExitEventPrefix + paneID

	wctx, cancel := context.WithCancel(context.Background())

	// OpenWorkspace prepares the monitor before it spawns the pty. Prepare
	// creates the exit listener, and for claude it also writes the hooks.
	// PaneEnv then yields the exit sentinel's PERCH_EXIT_TOKEN and
	// PERCH_EXIT_URL. These values must be present in the shell's process
	// environment at spawn time, because env is inherited at exec and is never
	// echoed, unlike the typed launch line. A monitor or prepare failure needs
	// no bridge cleanup, because the pty is not spawned yet.
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

	// OpenWorkspace composes the pane env. os.Environ() sits under the
	// monitor's pane env (the exit sentinel's PERCH_EXIT_* handles, referenced
	// by name), which sits under any env-sync overlay captured for this
	// workspace, so a `perch reload` reaches the relaunched agent. mergeEnv
	// removes duplicates and protects the sentinel from the overlay.
	paneEnv := mergeEnv(os.Environ(), mon.PaneEnv(), a.overlayFor(id))

	br, err := a.spawnPty(wctx, w.WorktreePath, internalpty.LoginShellArgv(), paneEnv, event, exitEvent, a.emit, defaultPtyCols, defaultPtyRows)
	if err != nil {
		cancel()
		_ = mon.Teardown()
		return fmt.Errorf("spawn pty: %w", err)
	}

	// REQUIRED: start the monitor's event pump (translation/SSE), bound to wctx.
	mon.Start(wctx)

	// OpenWorkspace wires the fs watcher with a debounce goroutine bound to
	// wctx. OpenWorkspace starts the watcher non-fatally: if the watcher fails,
	// OpenWorkspace continues with no watcher, and the failure never fails
	// OpenWorkspace itself.
	var watcher *fspkg.Watcher
	if a.newWatcher != nil {
		changes := make(chan string, fsChangeChanBuf)

		// This debounce goroutine coalesces raw onChange signals into a single
		// fs:changed emit per debounce window. wctx bounds its lifetime.
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
					// else: within the window, so coalesce (do nothing)
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
	// This composite cancel function cancels wctx, stopping all goroutines, and
	// closes the watcher. CloseWorkspace, reopen displacement, and shutdown all
	// use it.
	a.cancels[id] = func() {
		cancel()
		if watcher != nil {
			_ = watcher.Close()
		}
	}
	a.mu.Unlock()

	// Disarm the displaced shell's pty:exit emit before oldCancel() runs. On
	// Reopen, the old login shell survives the agent's /exit, and its bridge
	// shares the "pty:exit:pane-<id>" event name with the freshly remounted
	// Terminal. Without the disarm, a stray reaper emit would delete openIds
	// and re-latch the "session has ended" overlay. The disarm must precede
	// oldCancel(), because that cancellation reaps the shell (spawned through
	// exec.CommandContext(wctx, ...)) and can wake its reaper during
	// oldMon.Teardown()'s file I/O, before oldBr.Close() below runs. This
	// mirrors, for the bridge, the old-monitor exit-sentinel suppression that
	// the Teardown-before-kill order already provides. A genuine agent or
	// shell exit still emits; CloseWorkspace uses the normal Close() with no
	// suppression.
	if oldBr != nil {
		oldBr.SuppressExit()
	}
	if oldCancel != nil {
		oldCancel()
	}
	// OpenWorkspace tears down the old monitor, closing its exit listener,
	// before it SIGKILLs the old pane's process group. This order means a late
	// exit sentinel from the displaced shell has nowhere to land, so it cannot
	// surface a spurious "Agent exited" notification on reopen.
	if oldMon != nil {
		_ = oldMon.Teardown()
		// oldMon.Teardown() just stripped every perch hook group from the
		// worktree settings.json. All monitors share one sentinel, so
		// removeMonitorHooks cannot tell the new monitor's group from the old
		// one. The new monitor's Prepare (above) already tried to write its
		// group, but its merge is idempotent by that same shared sentinel:
		// while the old group was still present, Prepare added nothing. The
		// file now carries the old, dead listener address and token, or none
		// at all. This code re-asserts the new monitor's hooks now that
		// settings.json is clean, so the reopened agent posts SessionStart to
		// a listener that perch is actually watching. Without this step, the
		// reopened session becomes a silent zombie: no SessionStart fires, so
		// no live event ever heals the overlay, and perch observes no state.
		// This runs here, after teardown, rather than by reordering teardown
		// before Prepare, so a failed new-agent spawn above still rolls back
		// to the old session. The env-sync `perch reload` relaunch (onEnvSync)
		// calls OpenWorkspace on a live agent, so the displaced monitor is not
		// always a dead one.
		if hr, ok := mon.(hookRewriter); ok {
			_ = hr.RewriteHooks()
		}
	}
	if oldBr != nil {
		_ = oldBr.Close()
	}

	if launchCmd != "" {
		// adpt is non-nil here for every reachable case: newMonitor above
		// returns an error for any tool other than claude or opencode, bailing
		// out before this point, and agentAdapter returns a non-nil adapter
		// for both of those tools. A nil adpt is possible only through a test
		// seam that decouples newAdapter from newMonitor; that case
		// deliberately falls through to the write. Do not rewrite this check
		// to `adpt == nil || !adpt.Detect()`, which would nil-panic on
		// adpt.Name().
		if adpt != nil && !adpt.Detect() {
			// The agent CLI is missing from PATH. OpenWorkspace skips writing
			// the launch command, which would otherwise surface as a raw shell
			// "command not found" error, and instead surfaces a clear,
			// blocking signal. The shell stays usable.
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

	// This goroutine forwards monitor events to the frontend. It follows a
	// forward-and-continue pattern: it emits the event and moves on, never
	// blocking on a user decision. It exits on wctx cancellation, since
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
				// This stamps WorkspaceID, so the frontend can match events to
				// the correct workspace.
				evt.WorkspaceID = id
				// This composes the approval ReqID as "<raw>:<workspaceID>", so
				// Approve() can parse and route it with strings.LastIndex(":").
				// It copies the ApprovalReq to avoid mutating the monitor's own
				// pointee.
				if evt.Approval != nil {
					rawReqID := evt.Approval.ReqID
					a2 := *evt.Approval
					a2.ReqID = rawReqID + ":" + id
					evt.Approval = &a2
					// Always-allow auto-approval: if this request exactly
					// matches a persisted rule, OpenWorkspace allows it
					// silently and suppresses the card and the blocking
					// notification. A routine notify keeps it visible.
					if a.maybeAutoApprove(id, rawReqID, a2, mon) {
						continue
					}
					// This registers the pending approval, so Approve() can
					// resolve the tool and input authoritatively when the user
					// clicks Always.
					a.mu.Lock()
					if a.pending == nil {
						a.pending = map[string]agent.ApprovalReq{}
					}
					a.pending[a2.ReqID] = a2
					a.mu.Unlock()
				}
				// Session-resume: when the agent reports a new session id,
				// OpenWorkspace persists it, so the next OpenWorkspace call
				// can pass it as resumeID. OpenWorkspace validates the session
				// id before persisting; an invalid id, for example one
				// containing shell metacharacters, is silently dropped, so it
				// can never be concatenated into a shell launch command later.
				// Update writes only LastSessionID, and returns ErrNotFound
				// instead of resurrecting a record a concurrent remove dropped.
				if evt.SessionID != "" && validateSessionID(evt.SessionID) == nil {
					if cur, ok := a.store.Get(id); ok && cur.LastSessionID != evt.SessionID {
						_, _ = a.store.Update(id, func(w *registry.Workspace) error {
							w.LastSessionID = evt.SessionID
							return nil
						})
					}
				}
				a.emit("agent:event", evt)
				a.dispatchNotify(evt)
				// WIN #6: this is the single point every monitor event flows
				// through, right beside the notify dispatch, so every state
				// transition is observed here. This code recomputes the "need
				// you" count and debounces a native window-title refresh.
				// Recomputing from live monitors keeps the count correct
				// regardless of which event fired; the debounce coalesces
				// bursts.
				a.scheduleTitleUpdate()
			}
		}
	}()

	return nil
}

// maybeAutoApprove auto-allows an incoming approval request when the request
// exactly matches a persisted AlwaysRule (same agent, same tool,
// byte-identical input). On a match, maybeAutoApprove allows the request
// through the monitor, emits a routine-tier transparency notification so the
// auto-approval is never silent, and returns true, so the caller suppresses
// the approval card and the blocking notification.
//
// Matching requires exact input equality, never a glob, so an always-rule can
// never grant more than the byte-identical request the user originally
// approved. Rules with an empty Pattern never match, so there is no tool-wide
// auto-allow hole. If the monitor's Approve call fails, maybeAutoApprove
// returns false, so the card surfaces normally instead of the request being
// silently dropped.
func (a *App) maybeAutoApprove(workspaceID, rawReqID string, req agent.ApprovalReq, mon agent.Monitor) bool {
	if req.Tool == "" || req.Input == "" {
		return false
	}
	agentName := string(modelpkg.ToolClaude)
	if w, ok := a.store.Get(workspaceID); ok && w.Agent != "" {
		agentName = w.Agent
	}
	// maybeAutoApprove holds settingsMu (read side), so it sees a consistent
	// snapshot of settings and does not race a concurrent Approve(always)
	// write. DEADLOCK GUARD: callers must not hold a.mu before they take
	// settingsMu. Callers of maybeAutoApprove are outside any a.mu critical
	// section.
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
		// The SHA-256 hash of the full, untruncated tool input is the sole
		// authoritative match key. Pattern is display-only: it is truncated to
		// MaxApprovalInputLen, so two inputs that share a 4096-byte prefix
		// collide on Pattern, and matching on Pattern would open a
		// privilege-escalation hole. A rule without a Hash, or a request
		// without an InputHash, never auto-approves; this fails closed. There
		// is no backward-compatibility requirement, so there is no legacy
		// pattern fallback: any pre-Hash rule simply prompts once and is
		// re-saved with a hash.
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
// appropriate tier. For blocking-tier events, when the window is unfocused,
// dispatchNotify also fires an OS desktop notification. Do-Not-Disturb mutes
// only the ambient and routine tiers, never the blocking tier. Only blocking
// events fire an OS notification, so DND has no effect on the OS-notify path.
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
		if body == "" {
			// opencode's session.error can carry an empty payload. dispatchNotify
			// must never surface a blocking notification with a blank body;
			// this mirrors the exited default.
			body = "The agent reported an error."
		}
	case evt.Kind == "state" && evt.State == agent.StateExited:
		// This prunes any pending approval for the exited workspace. A
		// PreToolUse-time crash leaves an unresolved a.pending entry whose
		// reqID's agent is gone. Without this pruning step, a webview reload
		// would falsely show a live approval card (ListWorkspaces reports
		// StateExited, but PendingApprovals would still surface the dead
		// card). Keys have the form "<raw>:<workspaceID>".
		a.mu.Lock()
		_, live := a.monitors[evt.WorkspaceID]
		suffix := ":" + evt.WorkspaceID
		for k := range a.pending {
			if strings.HasSuffix(k, suffix) {
				delete(a.pending, k)
			}
		}
		a.mu.Unlock()
		// This suppresses a spurious "Agent exited" notification on
		// intentional teardown. CloseWorkspace, displacement, and shutdown
		// deregister the monitor (under a.mu) before the pane is torn down,
		// so a late exit sentinel here finds no live monitor and must not
		// notify. A genuine crash keeps its monitor registered, because only
		// the agent process, inside the still-alive shell, died, so it still
		// surfaces.
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

	// dispatchNotify always emits the in-app Wails notification event,
	// unconditionally. The source State rides along, so the frontend can
	// identify which blocking notification a later transition supersedes, for
	// example a "Question" from awaiting-input, without re-deriving intent
	// from the title. Kind alone cannot separate a question from a real
	// approval, because both classify as "approval". The frontend never
	// treats this as superseding a pending "Approval needed" notification
	// (state awaiting-approval), which must never be cleared.
	a.emit("notify", map[string]any{
		"tier":        tier,
		"title":       title,
		"body":        body,
		"workspaceId": evt.WorkspaceID,
		"state":       string(evt.State),
	})

	// dispatchNotify fires an OS desktop notification only for blocking-tier
	// events, and only when the window is unfocused. dispatchNotify
	// deliberately does not consult DND here: DND never mutes the blocking
	// tier, and only blocking events fire an OS notification.
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

// CloseWorkspace cancels the workspace pump, tears down the monitor, and
// closes the pty. It keeps the workspace record in the registry, so the
// session can be reopened.
func (a *App) CloseWorkspace(id string) error {
	// Validate workspace id before touching the store.
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	unlock := a.lockWorkspace(id)
	defer unlock()
	a.closeWorkspace(id)
	return nil
}

// closeWorkspace is CloseWorkspace's body. The caller holds id's lifecycle
// lock (lockWorkspace).
func (a *App) closeWorkspace(id string) {
	paneID := paneIDFor(id)
	shellPrefix := "shell-" + id
	a.mu.Lock()
	br := a.bridges[paneID]
	delete(a.bridges, paneID)
	// The workspace's shell drawer registers its ptys under "shell-<id>" (the
	// default terminal) and "shell-<id>_<n>" (extra tabs; see OpenShell and
	// CloseShell). CloseWorkspace closes and drops all of them here. Otherwise
	// a tab would leak until shutdown, left running against a now-deleted
	// worktree cwd after RemoveWorkspace. Go allows delete during range. The
	// exact-or-"_"-delimited match never touches another workspace's shells,
	// because UUID ids differ, or the home shell.
	var shellBrs []*internalpty.Bridge
	for k, sb := range a.bridges {
		if k == shellPrefix || strings.HasPrefix(k, shellPrefix+"_") {
			shellBrs = append(shellBrs, sb)
			delete(a.bridges, k)
		}
	}
	mon := a.monitors[id]
	delete(a.monitors, id)
	var cancel context.CancelFunc
	if a.cancels != nil {
		cancel = a.cancels[id]
		delete(a.cancels, id)
	}
	// This purges pending approvals that belong to this workspace, so a closed
	// workspace does not accumulate phantom entries in the pending map.
	// Pending keys have the form "<raw>:<workspaceID>" (see Approve and the
	// event pump); validateSessionID forbids ':' in ids, so the suffix match
	// is unambiguous. This also collects the raw reqIDs of the still-pending
	// approvals, so CloseWorkspace can deny them through the monitor after it
	// releases a.mu. A blocked claude hook POST or opencode permission call
	// would otherwise hang until its own timeout, once the workspace closes
	// out from under it.
	suffix := ":" + id
	var pendingRaw []string
	for k := range a.pending {
		if strings.HasSuffix(k, suffix) {
			pendingRaw = append(pendingRaw, k[:len(k)-len(suffix)])
			delete(a.pending, k)
		}
	}
	a.mu.Unlock()

	// CloseWorkspace denies each in-flight approval through the monitor before
	// teardown, so the agent's blocked hook returns promptly instead of
	// hanging. It does this before cancel() and Teardown(), so the monitor is
	// still live to deliver the verdict. (mon.Approve is safe to call outside
	// a.mu; it does not take a.mu.)
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
	for _, sb := range shellBrs {
		_ = sb.Close()
	}
	// WIN #6: closing a session removes its monitor from the attention set,
	// but its event pump is now cancelled and can no longer fire the
	// state-change hook. Without a refresh, the native title would go stale,
	// for example stuck at "perch (1 need you)" after the only awaiting
	// session closes. CloseWorkspace debounces a refresh; by the time the
	// refresh fires, the monitor is already removed above, so attentionCount
	// reflects the removal.
	a.scheduleTitleUpdate()
}

// ErrWorktreeDirty aliases the git-package sentinel so app callers and tests can
// match it with errors.Is without importing gitpkg directly.
var ErrWorktreeDirty = gitpkg.ErrWorktreeDirty

// RemoveWorkspace closes the workspace and removes it from the registry. For
// Worktree==true sessions it also removes the linked worktree tree from disk.
// A dirty tree returns ErrWorktreeDirty and leaves the record and the live
// session intact; the caller should then offer a force-confirm that calls
// ForceRemoveWorkspace. RemoveWorkspace never deletes the branch. For
// Worktree==false (in-repo, permanent) sessions, RemoveWorkspace drops only
// the registry record; it never touches the repo root or its branch.
//
// RemoveWorkspace stops the agent, the drawer shells and the fs watcher
// before it deletes the tree, so nothing writes into the directory while git
// removes it. If the git removal then fails, the record stays (retryable)
// but the session is already closed; CleanupSessions makes the same trade.
func (a *App) RemoveWorkspace(id string) error {
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	unlock := a.lockWorkspace(id)
	defer unlock()
	w, ok := a.store.Get(id)
	if !ok {
		return nil // already gone; RemoveWorkspace is idempotent
	}
	if !w.Worktree {
		a.closeWorkspace(id)
		return a.forgetWorkspace(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	// If something deleted the worktree dir outside perch, WorktreeDirty
	// (git -C <missing> status) would error, and the record could never
	// drop, leaving a ghost session forever. This code detects the missing
	// tree up front and treats the worktree as already gone. It prunes git's
	// stale registration, so the branch and the path can host a new session,
	// and drops the record. Only a present-but-dirty tree returns
	// ErrWorktreeDirty.
	if worktreeGone(w) {
		a.closeWorkspace(id)
		_ = gitpkg.PruneWorktrees(ctx, a.runner(), w.RepoPath)
		return a.forgetWorkspace(id)
	}
	dirty, err := gitpkg.WorktreeDirty(ctx, a.runner(), w.WorktreePath)
	if err != nil {
		return fmt.Errorf("check worktree dirty: %w", err)
	}
	if dirty {
		return ErrWorktreeDirty
	}
	a.closeWorkspace(id)
	if err := gitpkg.RemoveWorktree(ctx, a.runner(), w.RepoPath, w.WorktreePath, false); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return a.forgetWorkspace(id)
}

// forgetWorkspace drops id's registry record.
func (a *App) forgetWorkspace(id string) error {
	return a.store.Remove(id)
}

// worktreePathGone reports whether a worktree path no longer exists on disk,
// for example because something deleted it outside perch. worktreePathGone
// treats an empty path as gone. It treats a non-ENOENT stat error, such as a
// permission error, as not gone, so the normal git path runs and surfaces the
// real error instead of silently dropping the record.
func worktreePathGone(path string) bool {
	if path == "" {
		return true
	}
	if _, err := os.Stat(path); err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return false
}

// worktreeGone reports whether w's tree no longer exists as a git worktree:
// its directory is missing, or, for a linked worktree, its ".git" file is.
// The second case covers a directory deleted outside perch and then
// recreated by some tool: git can neither inspect nor remove it ("not a git
// repository", "validation failed"), so callers treat it like a missing
// tree.
func worktreeGone(w registry.Workspace) bool {
	if worktreePathGone(w.WorktreePath) {
		return true
	}
	if !w.Worktree {
		return false
	}
	_, err := os.Lstat(filepath.Join(w.WorktreePath, ".git"))
	return errors.Is(err, os.ErrNotExist)
}

// ForceRemoveWorkspace force-removes the linked worktree tree, discarding any
// uncommitted changes, then drops the registry record. It keeps the branch.
// For Worktree==false sessions, ForceRemoveWorkspace behaves like
// RemoveWorkspace: it only drops the record. Like RemoveWorkspace, it stops
// the session before it deletes the tree.
func (a *App) ForceRemoveWorkspace(id string) error {
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	unlock := a.lockWorkspace(id)
	defer unlock()
	w, ok := a.store.Get(id)
	if !ok {
		return nil
	}
	a.closeWorkspace(id)
	if w.Worktree {
		ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
		defer cancel()
		if worktreeGone(w) {
			_ = gitpkg.PruneWorktrees(ctx, a.runner(), w.RepoPath)
		} else if err := gitpkg.RemoveWorktree(ctx, a.runner(), w.RepoPath, w.WorktreePath, true); err != nil {
			return fmt.Errorf("force-remove worktree: %w", err)
		}
	}
	return a.forgetWorkspace(id)
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

// ListStaleSessions returns Worktree==true sessions whose LastActive is older
// than the configured threshold. It always excludes non-worktree sessions.
// For each session it computes clean (no uncommitted changes), merged
// (branch merged into BaseRef, falling back to "HEAD" for old records), and
// diffstat. When an error occurs computing any of these, ListStaleSessions
// treats the value conservatively (dirty, unmerged, or zero), so the row
// shows as unchecked.
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

// CleanupSessions removes the given sessions. For each session it stops the
// agent and pty, removes the linked worktree tree (force-removing when
// force==true), and deletes the branch (-d, or -D when force==true).
// CleanupSessions only drops the record for non-worktree sessions; it never
// runs a git op on them. CleanupSessions accumulates errors and tries every
// id before it returns.
func (a *App) CleanupSessions(ids []string, force bool) error {
	var errs []error
	for _, id := range ids {
		if err := validateSessionID(id); err != nil {
			errs = append(errs, fmt.Errorf("invalid id %q: %w", id, err))
			continue
		}
		if err := a.cleanupSession(id, force); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// cleanupSession is CleanupSessions' body for one validated id. It holds
// id's lifecycle lock throughout.
func (a *App) cleanupSession(id string, force bool) error {
	unlock := a.lockWorkspace(id)
	defer unlock()
	w, ok := a.store.Get(id)
	if !ok {
		return nil
	}
	if !w.Worktree {
		a.closeWorkspace(id)
		return a.forgetWorkspace(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	var errs []error
	// When force==false, CleanupSessions checks that the tree is clean
	// before it tears anything down. Previously CloseWorkspace ran
	// unconditionally, killing the agent and pty, and only then did
	// RemoveWorktree(force=false) fail on a dirty tree, leaving a kept
	// record whose live session was already dead. This code mirrors
	// RemoveWorkspace: on a dirty tree, it skips this id entirely (the
	// record, session, and monitor all stay alive) and records the error.
	// It treats a missing worktree path as clean, because the tree is
	// already gone, so the record can still be dropped. force==true
	// bypasses this check and force-removes below.
	gone := worktreeGone(w)
	if !force && !gone {
		dirty, derr := gitpkg.WorktreeDirty(ctx, a.runner(), w.WorktreePath)
		if derr != nil {
			return fmt.Errorf("check worktree dirty %s: %w", id, derr)
		}
		if dirty {
			return fmt.Errorf("remove worktree %s: %w", id, ErrWorktreeDirty)
		}
	}
	a.closeWorkspace(id)
	if gone {
		// The tree was deleted outside perch: prune git's stale
		// registration instead of a remove that git would refuse.
		_ = gitpkg.PruneWorktrees(ctx, a.runner(), w.RepoPath)
	} else if err := gitpkg.RemoveWorktree(ctx, a.runner(), w.RepoPath, w.WorktreePath, force); err != nil {
		// Worktree removal failed, for example a dirty tree with
		// force=false. CleanupSessions keeps the record, so the session
		// stays retryable and the tree is never orphaned. It skips the
		// branch delete.
		return fmt.Errorf("remove worktree %s: %w", id, err)
	}
	if err := gitpkg.DeleteBranch(ctx, a.runner(), w.RepoPath, w.Branch, force); err != nil {
		// The branch may stay, for example when it is unmerged with -d;
		// this is safe. The tree is already gone, so CleanupSessions
		// still drops the record below.
		errs = append(errs, fmt.Errorf("delete branch %s: %w", id, err))
	}
	if err := a.forgetWorkspace(id); err != nil {
		errs = append(errs, fmt.Errorf("remove record %s: %w", id, err))
	}
	return errors.Join(errs...)
}

// OpenShell spawns a $SHELL -l pty for the shell drawer pane (paneID) in cwd.
// Output flows to the "pty:data:<paneID>" event. OpenShell is separate from
// agent panes, so the shell drawer has its own independent pty.
func (a *App) OpenShell(paneID, cwd string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	// The home shell pane ("shell-home") has an OS-derived cwd (HomeShellCwd).
	// This cwd is not user IPC input, and it is almost never under a
	// configured project root, so OpenShell bypasses the root-containment
	// guard for this pane alone. All other panes still validate.
	if paneID != homeShellPaneID {
		if err := validateWorktreeUnderRoots(cwd, a.roots); err != nil {
			return fmt.Errorf("invalid shell cwd: %w", err)
		}
	}
	event := ptyDataEventPrefix + paneID
	exitEvent := ptyExitEventPrefix + paneID
	ctx := context.Background()
	// The home drawer (shell-home) has no workspace: it is a plain login shell
	// that inherits the process environment unchanged (nil env), with no
	// env-sync handles and no overlay. A per-workspace drawer (shell-<id>)
	// instead receives the env-sync handles, so a `perch reload` run inside
	// it can post its environment. It also receives any overlay already
	// captured for the workspace, so a reopen carries the overlay too.
	var env []string
	if paneID != homeShellPaneID {
		workspaceID := workspaceIDForShellPane(paneID)
		var injected []string
		if a.envsync != nil {
			if tok, terr := a.envsync.TokenFor(workspaceID); terr == nil {
				injected = envsyncPaneEnv(a.envsync.URL(), tok, workspaceID)
			}
		}
		// When OpenShell knows its own absolute path, it prepends that path's
		// directory to PATH, so a manual `perch reload` typed into the drawer
		// resolves the binary (it lives at bin/perch, off PATH). It also
		// exports PERCH_BIN as a documented escape hatch (`$PERCH_BIN
		// reload`). This only happens when the path is absolute: a bare name
		// or "." would poison PATH. mergeEnv lets this injected PATH override
		// the base PATH; building it from os.Getenv("PATH") preserves the
		// rest of the existing PATH.
		if filepath.IsAbs(a.perchBin) {
			injected = append(injected,
				"PATH="+filepath.Dir(a.perchBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PERCH_BIN="+a.perchBin,
			)
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

// CloseShell tears down one shell pane's pty and drops it from the bridge
// registry. A drawer's per-tab × (close) button calls CloseShell to close a
// single terminal without closing the whole session. CloseShell is
// idempotent: an unknown pane id is a no-op, because CloseWorkspace may
// already have reaped the tab. Unlike CloseWorkspace, CloseShell touches only
// the one pane, so the session's other shells and its agent keep running.
func (a *App) CloseShell(paneID string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	a.mu.Lock()
	br := a.bridges[paneID]
	delete(a.bridges, paneID)
	a.mu.Unlock()
	if br != nil {
		_ = br.Close()
	}
	return nil
}

// GetSettings reads settings from disk. It returns defaults if the file is
// absent.
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
// mirrors these in frontend/src/lib/constants.ts. GetSettings returns these
// defaults on first run (no settings file) and when an existing settings
// file is corrupt.
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

// SaveSettings atomically writes settings to disk. It is the public,
// Wails-bound method the frontend calls to persist a whole settings blob
// (theme, density, font, dnd, alwaysRules). SaveSettings takes settingsMu, so
// a frontend write serializes with Approve(always)'s read-append-write. This
// lock prevents torn writes, and it prevents two concurrent appends from
// losing each other.
//
// DEADLOCK GUARD: code must not enter settingsMu while a.mu is held, and
// saveSettingsLocked must not acquire a.mu; it only marshals and writes.
// Approve already holds settingsMu across its read-modify-write, so it calls
// saveSettingsLocked directly. Calling this public method there would
// self-deadlock, because sync.Mutex is not reentrant.
//
// Residual limit (inherent to whole-blob replacement, not a cut corner): the
// lock cannot stop a stale whole-blob overwrite. A frontend SaveSettings call
// carrying a snapshot read before an Approve(always) append will still
// clobber the new rule. This is last-writer-wins on a full-document PUT, not
// a data race. Closing this gap fully would need a version field and a
// compare-and-set. A naive "re-read and preserve on-disk AlwaysRules" merge
// is not a valid fix, because it would break the frontend's legitimate
// rule-deletion path: setAlwaysRules deliberately sends a shorter list, and a
// preserve-merge would treat the missing rules as rules to resurrect. The
// lock is the correct fix for the in-scope torn-write and concurrent-append
// races.
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

// atomicWriteApp writes data to path through a temp file, then a rename
// (atomic on Linux), creating the parent dir if needed. The file is always
// created with mode 0600 (owner read/write only), because it may carry a
// token-bearing settings payload. os.CreateTemp already uses 0600, but this
// function sets that mode explicitly, before any write, so the invariant is
// auditable and consistent with claude_monitor.go's atomicWrite.
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
	// This sets settingsFileMode before writing, so there is no window where
	// the content is readable at a looser mode. It mirrors the same invariant
	// as claude_monitor.go's atomicWrite.
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

// ListDir returns directory entries under absDir. ListDir honors .gitignore
// patterns in that directory: it excludes entries that match any pattern in
// absDir/.gitignore.
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
	// WriteFile validates absPath itself, not just its Dir, so a
	// symlink-as-final-component that resolves outside root gets caught.
	//
	// Primary path: absPath resolves successfully through EvalSymlinks (the
	// file exists, or is a non-dangling symlink). validateWorktreeUnderRoots
	// does the full resolve-and-check.
	if err := validateWorktreeUnderRoots(absPath, a.roots); err == nil {
		// absPath resolves inside roots, so WriteFile allows it.
		return fspkg.WriteFile(absPath, []byte(content))
	}
	// absPath may be a new, not-yet-created file, or it may be a symlink,
	// dangling or resolving outside root. WriteFile distinguishes these cases:
	//
	// If absPath exists as a symlink, even a dangling one, WriteFile rejects
	// it, because a write would follow the symlink to an outside-root target.
	// That is the escape vector.
	if _, lstatErr := os.Lstat(absPath); lstatErr == nil {
		// absPath exists on disk (as a symlink or regular file). If
		// validateWorktreeUnderRoots rejected it above, reject here too.
		return fmt.Errorf("WriteFile: path %q is outside configured roots or escapes via symlink", absPath)
	}
	// absPath does not exist; this is truly a new file. WriteFile validates
	// it by checking:
	//   1. The parent dir must exist and resolve inside roots.
	//   2. The cleaned absPath must be lexically under the resolved parent,
	//      which guards against ".." or other path escapes in the filename
	//      component.
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
	// CopyPath validates before the ctx guard, so tests can exercise the
	// security boundary without a Wails runtime (ctx == nil results in a
	// clipboard no-op after validation).
	if err := validateWorktreeUnderRoots(absPath, a.roots); err != nil {
		return err
	}
	if a.ctx == nil {
		return nil
	}
	return wailsruntime.ClipboardSetText(a.ctx, absPath)
}

// ClipboardSetText writes s to the system clipboard through the Wails
// runtime. WebKit2GTK's navigator.clipboard is unreliable, so clipboard
// writes route host-side. ClipboardSetText mirrors CopyPath's ctx==nil guard
// (Wails not started results in a no-op), so tests can call it safely,
// headless.
func (a *App) ClipboardSetText(s string) error {
	if a.ctx == nil {
		return nil
	}
	return wailsruntime.ClipboardSetText(a.ctx, s)
}

// ClipboardText reads the system clipboard through the Wails runtime,
// host-side, for the same WebKit2GTK reason as ClipboardSetText.
// ClipboardText returns "" with no error when the Wails runtime is not
// started (ctx == nil), mirroring CopyPath's guard.
func (a *App) ClipboardText() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wailsruntime.ClipboardGetText(a.ctx)
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

// PendingApprovalVM is one still-undecided approval request. It is tagged
// with the workspace it belongs to, so the frontend can rebuild its
// per-workspace queue.
type PendingApprovalVM struct {
	WorkspaceID string            `json:"workspaceId"`
	Req         agent.ApprovalReq `json:"req"`
}

// PendingApprovals returns every approval request still awaiting a decision,
// so the frontend can rebuild its approval queue after a reload or a late
// open. The agent:event that carries an approval is a one-shot: if it
// arrives before the workspace is listed, or after a webview reload, the
// event is otherwise lost, and the agent's blocked hook wedges forever. The
// pending map key is the composed ReqID "<raw>:<workspaceID>";
// PendingApprovals derives WorkspaceID the same way Approve routes it, with
// strings.LastIndex(":"). The stored ApprovalReq.ReqID is already the
// composed id the frontend keys its queue on, so PendingApprovals uses it
// verbatim as Req.
func (a *App) PendingApprovals() []PendingApprovalVM {
	a.mu.Lock()
	defer a.mu.Unlock()
	// This returns an empty, never nil, slice, so it marshals to [] rather
	// than null.
	out := make([]PendingApprovalVM, 0, len(a.pending))
	for key, req := range a.pending {
		sep := strings.LastIndex(key, ":")
		if sep < 0 {
			continue // malformed key: skip rather than mis-route
		}
		out = append(out, PendingApprovalVM{
			WorkspaceID: key[sep+1:],
			Req:         req,
		})
	}
	return out
}

// Approve routes a tool-approval decision to the owning Monitor. reqID has
// the form "<raw>:<workspaceID>". decision is "allow", "deny", or "always".
// On "always", Approve persists an AlwaysRule to Settings.
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

	// This consumes the pending approval for this exact reqID. tool and input
	// come from the backend's record of what was actually surfaced, never
	// from the frontend, so an always-rule cannot be forged to grant
	// something the user did not see. Resolving by reqID, not a racy "last
	// approval" accessor, stays correct even when many approvals are pending
	// across workspaces.
	a.mu.Lock()
	req, hadPending := a.pending[reqID]
	delete(a.pending, reqID)
	a.mu.Unlock()

	if d.Always && hadPending && req.Tool != "" && req.Input != "" {
		agentName := string(modelpkg.ToolClaude)
		if w, ok := a.store.Get(workspaceID); ok && w.Agent != "" {
			agentName = w.Agent
		}
		// Approve holds settingsMu across the entire read-modify-write, so
		// concurrent Approve(always) calls cannot interleave and lose rules.
		// DEADLOCK GUARD: Approve releases a.mu above, before it takes
		// settingsMu.
		a.settingsMu.Lock()
		s, err := a.GetSettings()
		if err != nil {
			a.settingsMu.Unlock()
			return err
		}
		dup := false
		for _, r := range s.AlwaysRules {
			// This dedups on the same authoritative key used for matching: the hash.
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
			// settingsMu is already held here, so this calls the unlocked
			// inner helper to avoid a re-entrant deadlock; SaveSettings would
			// otherwise re-take settingsMu.
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
// index is relative to the current Hunks(worktree, file) output. The
// frontend re-fetches hunks after each call, so indices stay fresh.
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

// UnstageHunk moves the staged hunk at merged Hunks(worktree, file) index
// `index` back to the working tree (git apply --reverse --cached). `index`
// is the same merged-Hunks() index that StageHunk and DiscardHunk take, not
// a `git diff --cached` position, and it must identify a Staged==true hunk.
// UnstageHunk is the inverse of StageHunk, and it touches the index only,
// never the working-tree content. The frontend re-fetches hunks after each
// call, so indices stay fresh.
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
// The JSON tags are frozen; do not rename them.
type RepoInfo struct {
	// Path is the absolute path to the repository root (main worktree).
	Path string `json:"path"`
	// Name is the display name (base directory name of the main worktree).
	Name string `json:"name"`
	// Branch is the branch checked out in the main worktree, or "" when
	// the repository has no commits yet.
	Branch string `json:"branch"`
	// Worktrees lists all non-bare working trees (the main tree and linked
	// worktrees). Head is always "", because model.Tree does not carry the
	// commit SHA. The dialog does not need Head for a fresh-install
	// selection list.
	Worktrees []gitpkg.WorktreeInfo `json:"worktrees"`
}

// DiscoverRepos discovers git repositories under all configured roots and
// returns a deduplicated, frontend-ready slice. It orders the slice by
// frecency, falling back to alphabetical order on a cold start. DiscoverRepos
// is for the New Session dialog on a fresh install, when there are no
// existing workspaces.
//
// DiscoverRepos runs best-effort: it silently skips per-root errors, and it
// returns an error only when every root fails. It returns an empty, non-nil
// slice when it finds no repositories.
func (a *App) DiscoverRepos() ([]RepoInfo, error) {
	// byPath deduplicates across many roots.
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

			// This extracts the branch from the main working tree.
			var branch string
			for _, tr := range pt.Trees {
				if tr.IsMain {
					branch = tr.Branch
					break
				}
			}

			// This maps model.Tree to gitpkg.WorktreeInfo. Head is not
			// available from ProjectTrees, so it is left as the zero value "".
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
