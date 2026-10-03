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
	"sort"
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
	// ptyMinDim is the smallest pty dimension the backend accepts. The
	// frontend already clamps (PTY_MAX_DIM in constants.ts), but the backend
	// must not trust that: a 0 dimension is invalid for a terminal. The upper
	// bound is the uint16 parameter type itself.
	ptyMinDim = 1
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
	// maybeAutoApprove. settingsMu and a.mu are never nested, in either
	// order: no code holds one while acquiring the other. This lock stops
	// concurrent Approve(always) calls from losing rules.
	settingsMu sync.Mutex

	// pending maps a composed approval reqID ("<raw>:<workspaceID>") to the
	// in-flight ApprovalReq. The pump adds an entry when it surfaces a card.
	// Approve consumes the entry to resolve the tool and input for an
	// always-rule; it never trusts frontend-supplied values. mu guards this map.
	pending map[string]agent.ApprovalReq
	// pendingSeq records the order in which pending approvals arrived, so
	// PendingApprovals returns them in a stable order (APP-22a). pendingNext
	// is the last sequence number handed out. mu guards both.
	pendingSeq  map[string]uint64
	pendingNext uint64

	cancels map[string]context.CancelFunc // workspaceID → pump/translation canceller
	// launches maps a workspace id to its current open's launch state (the
	// line to type, whether the agent reported in). mu guards the map.
	launches map[string]*launchState

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

	// envUnset holds, per workspace id, the baseline variable names a
	// `perch reload` found unset in the session terminal. They are dropped
	// from os.Environ() at every agent-pane and drawer spawn, so `unset FOO`
	// followed by a reload reaches the agent. mu guards this map.
	envUnset map[string][]string

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
	// titleClosed is set by shutdown; scheduleTitleUpdate then does nothing.
	// titleMu guards it.
	titleClosed bool

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
		roots:        normalizeRoots(roots),
		run:          proc.ExecRunner{},
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		pending:      map[string]agent.ApprovalReq{},
		cancels:      map[string]context.CancelFunc{},
		spawnPty:     internalpty.SpawnBase64,
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

// normalizeRoots makes every root absolute and clean, and drops empty and
// duplicate entries. Every path check compares absolute paths against the
// roots, so a relative root (perch ., a relative config entry) would
// otherwise reject everything (APP-4).
func normalizeRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	return out
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
		if ls, err := envsync.NewWithDelta(a.baselineEnv, a.onEnvSync); err == nil {
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
	a.launches = nil
	a.bridges = map[string]*internalpty.Bridge{}
	a.monitors = map[string]agent.Monitor{}
	a.cancels = map[string]context.CancelFunc{}
	// Reset pending approvals on shutdown so stale entries cannot outlive
	// their workspaces.
	a.pending = map[string]agent.ApprovalReq{}
	a.pendingSeq = nil
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
	a.titleClosed = true
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
		// The new pane reuses the old pane's pty:exit event name, so the
		// displaced shell must not announce its exit: the frontend would
		// treat it as the NEW shell exiting and close it (APP-11).
		old.SuppressExit()
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
	if a.titleClosed {
		// shutdown ran: a pump still mid-event must not re-arm the timer and
		// call WindowSetTitle on a torn-down window.
		return
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()

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
		if _, found := a.workspaceForBranch(repoPath, branch); found {
			return WorkspaceVM{}, fmt.Errorf("create worktree: %w", gitpkg.ErrBranchInUse)
		}

		// SlugifyBranch is lossy ("feat/x" and "feat-x" share a handle), so
		// the path is the first free one, never a directory that already
		// exists. AddWorktree still returns ErrWorktreePathExists if one
		// appears in between.
		handle := gitpkg.SlugifyBranch(branch)
		treePath, err := gitpkg.AvailableWorktreePath(repoPath, handle, "")
		if err != nil {
			return WorkspaceVM{}, err
		}
		if !containedUnderRoots(treePath, a.roots) {
			return WorkspaceVM{}, fmt.Errorf("derived worktree path %q escapes all configured roots", treePath)
		}
		// A worktree deleted outside perch stays registered in git: its
		// branch reads as "already used by worktree" and its path as "a
		// missing but already registered worktree". Drop exactly the stale
		// registration in the way (this path or this branch), best-effort.
		// Never `git worktree prune`: it would also drop the registration of
		// any unrelated worktree whose directory is only temporarily missing.
		_ = gitpkg.ForgetStaleWorktrees(ctx, a.runner(), repoPath, treePath, branch)

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
			// Switching the shared checkout moves every open session in it to
			// the new branch, so refuse while another one is open.
			for _, other := range a.store.List() {
				if other.WorktreePath == repoPath && a.isOpen(other.ID) {
					return WorkspaceVM{}, fmt.Errorf("checkout branch: %w", ErrCheckoutInUse)
				}
			}
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

// BranchOwner is WorkspaceForBranch's result: the id of the worktree session
// that tracks the branch, and whether one exists.
type BranchOwner struct {
	ID    string `json:"id"`
	Found bool   `json:"found"`
}

// WorkspaceForBranch reports which worktree session tracks branch in
// repoPath, if any. It considers only worktree sessions (Worktree==true);
// non-worktree sessions may share a branch by design.
//
// It returns one struct, not (id, found): Wails resolves a two-value method
// to its first value unless the second is an error, so a bool second result
// would never reach the frontend (APP-1).
func (a *App) WorkspaceForBranch(repoPath, branch string) BranchOwner {
	id, found := a.workspaceForBranch(repoPath, branch)
	return BranchOwner{ID: id, Found: found}
}

// workspaceForBranch is WorkspaceForBranch for Go callers.
func (a *App) workspaceForBranch(repoPath, branch string) (id string, found bool) {
	for _, w := range a.store.List() {
		if w.Worktree && w.RepoPath == repoPath && w.Branch == branch {
			return w.ID, true
		}
	}
	return "", false
}

// ErrCheckoutInUse is returned by CreateWorkspace when an in-repo session
// would switch the branch of a repository checkout that another open session
// is working in (APP-18). The other session's agent would silently start
// editing the new branch while its record still names the old one.
var ErrCheckoutInUse = errors.New("another open session is working in this checkout; close it or use a worktree session")

// ErrWorktreeMissing is returned by OpenWorkspace when the session's
// directory (or, for a linked worktree, its .git file) no longer exists,
// for example because it was deleted outside perch. Opening it anyway would
// run the agent in an empty, non-git directory. Remove the session instead.
var ErrWorktreeMissing = errors.New("session directory is missing; remove the session")

// OpenWorkspace opens a workspace. It runs these steps:
//  1. It checks that the agent CLI is installed. If it is not, it opens a
//     plain shell, surfaces a blocking "Agent not found" notice, and skips
//     the monitor entirely (no Prepare, no Start, no launch line).
//  2. It calls Monitor.Prepare to get the agent launch command and install
//     the side-channel.
//  3. It spawns a login-shell pty for the workspace, with a usable TERM.
//  4. It starts the monitor's event pump.
//  5. It types the launch command, preceded by Ctrl-U, into the pty once
//     Bridge.WaitShellReady reports the shell at its prompt (from a goroutine
//     bound to the workspace context). If the shell stays busy for
//     launchReadyMaxWait it notifies instead, and RetypeLaunch can type it.
//  6. It forwards monitor events to the frontend.
//
// A per-workspace context binds all goroutines; CloseWorkspace or shutdown
// cancels that context. Opening an already open workspace replaces its
// session (reopen); the conversation resumes through LastSessionID.
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
	return a.openWorkspace(id)
}

// openWorkspace is OpenWorkspace's body. The caller holds id's lifecycle
// lock.
func (a *App) openWorkspace(id string) error {
	w, ok := a.store.Get(id)
	if !ok {
		return fmt.Errorf("unknown workspace %q", id)
	}
	if worktreeGone(w) {
		return fmt.Errorf("open %q: %w", w.WorktreePath, ErrWorktreeMissing)
	}
	if fi, err := os.Stat(w.WorktreePath); err == nil && !fi.IsDir() {
		return fmt.Errorf("open %q: not a directory: %w", w.WorktreePath, ErrWorktreeMissing)
	}

	// OpenWorkspace bumps LastActive when it opens the workspace.
	// ListStaleSessions and the sidebar order both key on LastActive, so an
	// actively opened session must not keep reading as stale (previously
	// LastActive was only ever set at creation). OpenWorkspace persists this
	// change before it spawns the pty, so the refreshed order survives even if
	// a later step fails. Update touches only LastActive and never recreates a
	// record that a concurrent remove already dropped.
	if nw, err := a.store.Update(id, func(w *registry.Workspace) error {
		w.LastActive = time.Now()
		return nil
	}); err == nil {
		w = nw
	} else if errors.Is(err, registry.ErrNotFound) {
		return fmt.Errorf("unknown workspace %q", id)
	}
	// Any other Update error is a failed flush; the in-memory record is
	// unchanged and opening the session is still possible.

	paneID := paneIDFor(id)
	event := ptyDataEventPrefix + paneID
	exitEvent := ptyExitEventPrefix + paneID

	wctx, cancel := context.WithCancel(context.Background())

	// AGT-22: when the agent CLI is missing from PATH, OpenWorkspace neither
	// prepares nor starts a monitor. Prepare would stand up listeners and
	// settings files for an agent that can never connect, and writing the
	// launch line would only print a raw "command not found". The shell still
	// opens, so the user can install the agent from it.
	//
	// adpt is non-nil for every reachable case: newMonitor returns an error
	// for any tool other than claude or opencode, and agentAdapter returns a
	// non-nil adapter for both. A nil adpt is possible only through a test
	// seam; it counts as present.
	adpt := a.newAdapter(w.Agent)
	agentMissing := adpt != nil && !adpt.Detect()

	// OpenWorkspace prepares the monitor before it spawns the pty. Prepare
	// creates the exit listener, and for claude it also writes the hook
	// settings file. PaneEnv then yields the exit sentinel's PERCH_EXIT_TOKEN
	// and PERCH_EXIT_URL. These values must be present in the shell's process
	// environment at spawn time, because env is inherited at exec and is never
	// echoed, unlike the typed launch line. A monitor or prepare failure needs
	// no bridge cleanup, because the pty is not spawned yet.
	var mon agent.Monitor
	var launchCmd string
	var injected []string
	if !agentMissing {
		var err error
		mon, err = a.newMonitor(w.Agent, adpt)
		if err != nil {
			cancel()
			return fmt.Errorf("new monitor: %w", err)
		}
		launchCmd, err = mon.Prepare(wctx, id, w.WorktreePath, w.LastSessionID)
		if err != nil {
			cancel()
			_ = mon.Teardown()
			return fmt.Errorf("monitor prepare: %w", err)
		}
		injected = mon.PaneEnv()
	}

	// OpenWorkspace composes the pane env. os.Environ(), minus the variables
	// a `perch reload` reported as unset, sits under the monitor's pane env
	// (the exit sentinel's PERCH_EXIT_* handles, referenced by name), which
	// sits under any env-sync overlay captured for this workspace, so a
	// `perch reload` reaches the relaunched agent. mergeEnv removes duplicates
	// and protects the sentinel from the overlay.
	overlay, unset := a.envFor(id)
	paneEnv := withTerm(mergeEnv(withoutKeys(os.Environ(), unset), injected, overlay))

	br, err := a.spawnPty(wctx, w.WorktreePath, internalpty.LoginShellArgv(), paneEnv, event, exitEvent, a.emit, defaultPtyCols, defaultPtyRows)
	if err != nil {
		cancel()
		if mon != nil {
			_ = mon.Teardown()
		}
		return fmt.Errorf("spawn pty: %w", err)
	}

	// REQUIRED: start the monitor's event pump (translation/SSE), bound to wctx.
	if mon != nil {
		mon.Start(wctx)
	}

	// OpenWorkspace registers the bridge BEFORE the fs watcher starts: the
	// watcher's initial walk can take seconds on a big worktree, and the
	// Terminal calls ResizePty as soon as the pane mounts. Registered late,
	// that call fails with "unknown pane" and the TUI starts at the default
	// size. The composite cancel below reaches the watcher through slot,
	// because the watcher does not exist yet.
	var slot watcherSlot
	var launch *launchState
	if launchCmd != "" {
		launch = newLaunchState(wctx, br, launchCmd)
	}

	a.mu.Lock()
	oldBr := a.bridges[paneID]
	oldMon := a.monitors[id]
	oldCancel := a.cancels[id]
	a.bridges[paneID] = br
	if mon != nil {
		a.monitors[id] = mon
	} else {
		delete(a.monitors, id)
	}
	if a.cancels == nil {
		a.cancels = map[string]context.CancelFunc{}
	}
	// This composite cancel function cancels wctx, stopping all goroutines, and
	// closes the watcher. CloseWorkspace, reopen displacement, and shutdown all
	// use it.
	a.cancels[id] = func() {
		cancel()
		slot.close()
	}
	a.setLaunchLocked(id, launch)
	// APP-9: the displaced monitor's pending approvals die with it. Left in
	// a.pending, a webview reload would resurrect their cards, and answering
	// one would route to the NEW monitor (Approve routes by workspace id),
	// clearing its state while its real approval stays blocked. They are
	// denied on the old monitor below, before its Teardown.
	stalePending := a.takePendingLocked(id)
	a.mu.Unlock()

	// Disarm the displaced shell's pty:exit emit before oldCancel() runs. On
	// Reopen, the old login shell survives the agent's /exit, and its bridge
	// shares the "pty:exit:pane-<id>" event name with the freshly remounted
	// Terminal. Without the disarm, a stray reaper emit would delete openIds
	// and re-latch the "session has ended" overlay. The disarm must precede
	// oldCancel(), because that cancellation reaps the shell (spawned through
	// exec.CommandContext(wctx, ...)) and can wake its reaper during
	// oldMon.Teardown()'s file I/O, before oldBr.Close() below runs. A genuine
	// agent or shell exit still emits; CloseWorkspace uses the normal Close()
	// with no suppression.
	if oldBr != nil {
		oldBr.SuppressExit()
	}
	if oldMon != nil {
		for _, raw := range stalePending {
			_ = oldMon.Approve(raw, agent.Decision{Allow: false})
		}
	}
	a.emitResolved(id, stalePending)
	if oldCancel != nil {
		oldCancel()
	}
	// OpenWorkspace tears down the old monitor, closing its exit listener,
	// before it SIGKILLs the old pane's process group. This order means a late
	// exit sentinel from the displaced shell has nowhere to land, so it cannot
	// surface a spurious "Agent exited" notification on reopen. Each monitor
	// owns its own side channel (claude: a per-session --settings file), so
	// the old monitor's Teardown cannot disturb the new one.
	if oldMon != nil {
		_ = oldMon.Teardown()
	}
	if oldBr != nil {
		_ = oldBr.Close()
	}
	// The attention count changes when a monitor is replaced or dropped.
	a.scheduleTitleUpdate()

	// OpenWorkspace types the launch line from a goroutine, so neither the
	// watcher walk below nor a busy shell holds the caller up.
	if launch != nil && !agentMissing {
		go a.typeLaunchWhenReady(launch, id, launchReadyMaxWait)
	}

	// OpenWorkspace wires the fs watcher with a debounce goroutine bound to
	// wctx. OpenWorkspace starts the watcher non-fatally: if the watcher fails,
	// OpenWorkspace continues with no watcher, and the failure never fails
	// OpenWorkspace itself. If the pane was closed or displaced during the
	// walk, slot closes the watcher at once.
	slot.set(a.startWatcher(wctx, id, w.WorktreePath))

	if agentMissing {
		a.emit("notify", map[string]any{
			"tier":        "blocking",
			"title":       "Agent not found",
			"body":        fmt.Sprintf("%q is not installed or not on PATH. Install it, then reopen this session.", adpt.Name()),
			"workspaceId": id,
		})
		return nil
	}

	go a.pumpEvents(wctx, id, mon)
	return nil
}

// takePendingLocked removes every pending approval of workspace id and
// returns their raw reqIDs. Pending keys have the form "<raw>:<workspaceID>";
// validateSessionID forbids ':' in ids, so the suffix match is unambiguous.
// The caller holds a.mu.
func (a *App) takePendingLocked(id string) []string {
	suffix := ":" + id
	var raws []string
	for k := range a.pending {
		if strings.HasSuffix(k, suffix) {
			raws = append(raws, k[:len(k)-len(suffix)])
			a.deletePendingLocked(k)
		}
	}
	return raws
}

// emitResolved tells the frontend that the approvals raws of workspace id
// are no longer pending: one agent:event of kind "approval-resolved" per
// approval, carrying the composed id. The caller already removed them from
// a.pending. This covers approvals perch takes away itself (close, reopen,
// relaunch, agent exit), whose own resolution event from the monitor would
// be dropped together with the monitor's pump.
func (a *App) emitResolved(id string, raws []string) {
	for _, raw := range raws {
		a.emit("agent:event", agent.Event{WorkspaceID: id, Kind: "approval-resolved", ResolvedReqID: raw + ":" + id})
	}
}

// deletePendingLocked drops one pending approval and its order record. The
// caller holds a.mu.
func (a *App) deletePendingLocked(key string) {
	delete(a.pending, key)
	delete(a.pendingSeq, key)
}

// pumpEvents forwards monitor events to the frontend until wctx is
// cancelled. It follows a forward-and-continue pattern: it emits the event
// and moves on, never blocking on a user decision. mon.Events() is never
// closed, so cancellation is the only exit.
func (a *App) pumpEvents(wctx context.Context, id string, mon agent.Monitor) {
	defer safe.Recover("workspace-event-pump")
	for {
		select {
		case <-wctx.Done():
			return
		case evt, ok := <-mon.Events():
			if !ok {
				return
			}
			a.noteAgentEvent(wctx, id)
			a.forwardEvent(id, mon, evt)
		}
	}
}

// forwardEvent handles one monitor event for workspace id.
func (a *App) forwardEvent(id string, mon agent.Monitor, evt agent.Event) {
	// Only the workspace's CURRENT monitor speaks for it. A monitor that a
	// reopen, relaunch or close replaced can still deliver a few late events
	// while its pump is being cancelled; forwarding them would, for example,
	// push the old session's "running" onto the new one. Its pending
	// approvals were already resolved toward the frontend when it was
	// replaced. A late approval it raises is denied on it, so its agent does
	// not wait forever.
	a.mu.Lock()
	current := a.monitors[id] == mon
	a.mu.Unlock()
	if !current {
		if evt.Approval != nil {
			_ = mon.Approve(evt.Approval.ReqID, agent.Decision{Allow: false})
		}
		return
	}
	// This stamps WorkspaceID, so the frontend can match events to the
	// correct workspace.
	evt.WorkspaceID = id
	// An approval that is no longer pending (perch's own verdict, an
	// auto-approval, a CloseWorkspace deny, or a hook the agent cancelled
	// because the user answered in its own TUI or it timed out) arrives as
	// exactly one event carrying the RAW reqID. Drop the pending entry, so a
	// reload never resurrects the card, and compose the id the frontend keys
	// its queue on.
	if evt.ResolvedReqID != "" {
		composed := evt.ResolvedReqID + ":" + id
		a.mu.Lock()
		a.deletePendingLocked(composed)
		a.mu.Unlock()
		evt.ResolvedReqID = composed
	}
	// This composes the approval ReqID as "<raw>:<workspaceID>", so Approve()
	// can parse and route it with strings.LastIndex(":"). It copies the
	// ApprovalReq to avoid mutating the monitor's own pointee.
	if evt.Approval != nil {
		rawReqID := evt.Approval.ReqID
		a2 := *evt.Approval
		a2.ReqID = rawReqID + ":" + id
		evt.Approval = &a2
		// Always-allow auto-approval: if this request exactly matches a
		// persisted rule, the app allows it silently and suppresses the card
		// and the blocking notification. A routine notify keeps it visible.
		if a.maybeAutoApprove(id, rawReqID, a2, mon) {
			return
		}
		// This registers the pending approval, so Approve() can resolve the
		// tool and input authoritatively when the user clicks Always. Only
		// the workspace's CURRENT monitor may register one: a displaced
		// monitor's late approval would otherwise be answered by its
		// successor (Approve routes by workspace id). Such an approval is
		// denied on the monitor that raised it and never surfaced.
		a.mu.Lock()
		current := a.monitors[id] == mon
		if current {
			if a.pending == nil {
				a.pending = map[string]agent.ApprovalReq{}
			}
			if a.pendingSeq == nil {
				a.pendingSeq = map[string]uint64{}
			}
			a.pendingNext++
			a.pending[a2.ReqID] = a2
			a.pendingSeq[a2.ReqID] = a.pendingNext
		}
		a.mu.Unlock()
		if !current {
			_ = mon.Approve(rawReqID, agent.Decision{Allow: false})
			return
		}
	}
	// Session-resume: when the agent reports a new session id, the app
	// persists it, so the next OpenWorkspace call can pass it as resumeID. It
	// validates the session id before persisting; an invalid id, for example
	// one containing shell metacharacters, is silently dropped, so it can
	// never be concatenated into a shell launch command later. Update writes
	// only LastSessionID, and returns ErrNotFound instead of resurrecting a
	// record a concurrent remove dropped.
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
	// WIN #6: this is the single point every monitor event flows through,
	// right beside the notify dispatch, so every state transition is observed
	// here. This code recomputes the "need you" count and debounces a native
	// window-title refresh.
	a.scheduleTitleUpdate()
}

// fsChangedMaxPaths caps the paths one fs:changed event lists. The watcher
// reports every file inside a newly created or moved-in directory, so a git
// switch, a tar x or a scaffold can touch thousands of files in one window.
// Past the cap the event carries no paths and truncated=true, and the
// frontend reloads everything it shows.
const fsChangedMaxPaths = 200

// fsBatch collects the paths changed during one debounce window.
type fsBatch struct {
	mu        sync.Mutex
	paths     map[string]struct{}
	truncated bool
}

// add records p, switching to truncated once more than fsChangedMaxPaths
// distinct paths arrive.
func (b *fsBatch) add(p string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.truncated {
		return
	}
	if b.paths == nil {
		b.paths = map[string]struct{}{}
	}
	if _, ok := b.paths[p]; ok {
		return
	}
	if len(b.paths) >= fsChangedMaxPaths {
		b.truncated = true
		b.paths = nil
		return
	}
	b.paths[p] = struct{}{}
}

// take returns the window's sorted paths (never nil) and the truncated flag,
// and starts a new window.
func (b *fsBatch) take() ([]string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.paths))
	for p := range b.paths {
		out = append(out, p)
	}
	sort.Strings(out)
	truncated := b.truncated
	b.paths, b.truncated = nil, false
	return out, truncated
}

// startWatcher starts the fs watcher for root, bound to wctx, and returns it,
// or nil when there is no watcher seam or the watcher fails to start.
//
// Changes are coalesced into one fs:changed event per debounce window:
//
//	{workspaceId, path: <root>, paths: [<absolute changed paths>], truncated}
//
// paths lists each distinct changed path once (sorted). When more than
// fsChangedMaxPaths changed, paths is empty and truncated is true.
func (a *App) startWatcher(wctx context.Context, id, root string) *fspkg.Watcher {
	if a.newWatcher == nil {
		return nil
	}
	batch := &fsBatch{}
	// signal wakes the debounce goroutine; one pending wake-up is enough,
	// because the batch, not the channel, carries the paths.
	signal := make(chan struct{}, 1)

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
			case <-signal:
				if timer == nil {
					timer = time.NewTimer(a.debounce)
					timerC = timer.C
				}
				// else: within the window, so coalesce (do nothing)
			case <-timerC:
				timer = nil
				timerC = nil
				paths, truncated := batch.take()
				if len(paths) == 0 && !truncated {
					continue
				}
				a.emit("fs:changed", map[string]any{
					"workspaceId": id,
					"path":        root,
					"paths":       paths,
					"truncated":   truncated,
				})
			}
		}
	}()

	onChange := func(p string) {
		batch.add(p)
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	// best-effort: a watcher failure must not fail OpenWorkspace
	wch, werr := a.newWatcher(root, onChange)
	if werr != nil {
		return nil
	}
	return wch
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
		exitedPending := a.takePendingLocked(evt.WorkspaceID)
		a.mu.Unlock()
		a.emitResolved(evt.WorkspaceID, exitedPending)
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

// WriteToPty forwards keystrokes from xterm.js to the pane's pty. Wails
// decodes a JSON string argument into data as standard base64, the same
// encoding pty:data uses in the other direction (internalpty.SpawnBase64);
// a JSON number array also still decodes.
func (a *App) WriteToPty(paneID string, data []byte) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	_, err := br.Write(data)
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

// clampPtyDim raises a pty dimension to at least ptyMinDim. A 0 dimension is
// invalid for a terminal. The uint16 parameter already caps the upper end at
// 65535, the maximum the frontend enforces.
func clampPtyDim(v uint16) uint16 {
	if v < ptyMinDim {
		return ptyMinDim
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
	delete(a.launches, id)
	// This purges pending approvals that belong to this workspace, so a closed
	// workspace does not accumulate phantom entries in the pending map.
	// Pending keys have the form "<raw>:<workspaceID>" (see Approve and the
	// event pump); validateSessionID forbids ':' in ids, so the suffix match
	// is unambiguous. This also collects the raw reqIDs of the still-pending
	// approvals, so CloseWorkspace can deny them through the monitor after it
	// releases a.mu. A blocked claude hook POST or opencode permission call
	// would otherwise hang until its own timeout, once the workspace closes
	// out from under it.
	pendingRaw := a.takePendingLocked(id)
	a.mu.Unlock()
	// A closed session's drawers are gone, so their env-sync token must stop
	// working; the overlay itself is kept for the next open.
	a.forgetEnv(id, false)

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
	a.emitResolved(id, pendingRaw)

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
	// tree up front and treats the worktree as already gone. It drops git's
	// stale registration of that tree, so the branch and the path can host a
	// new session, and drops the record. Only a present-but-dirty tree returns
	// ErrWorktreeDirty.
	if worktreeGone(w) {
		a.closeWorkspace(id)
		a.forgetGoneWorktree(ctx, w)
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
	a.forgetEnv(id, true)
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

// forgetGoneWorktree drops git's registration of w's tree, which
// worktreeGone reported gone, best-effort. `git worktree remove --force`
// clears it when the directory is missing. When the directory still exists
// without its .git file, git refuses that, and ForgetStaleWorktrees drops
// only that entry's admin dir. It never runs `git worktree prune`, which
// would also drop unrelated worktrees whose directories are only
// temporarily missing.
func (a *App) forgetGoneWorktree(ctx context.Context, w registry.Workspace) {
	if err := gitpkg.RemoveWorktree(ctx, a.runner(), w.RepoPath, w.WorktreePath, true); err != nil {
		_ = gitpkg.ForgetStaleWorktrees(ctx, a.runner(), w.RepoPath, w.WorktreePath, "")
	}
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
			a.forgetGoneWorktree(ctx, w)
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

// staleWorkers bounds how many sessions ListStaleSessions inspects at once.
// Each inspection costs three short git processes.
const staleWorkers = 4

// ListStaleSessions returns Worktree==true sessions whose LastActive is older
// than the configured threshold and that are not open right now (an open
// session is in use, however old its LastActive). It always excludes
// non-worktree sessions. For each session it computes clean (no uncommitted
// changes, untracked files included), merged (branch merged into BaseRef,
// falling back to "HEAD" for old records), and the added and removed line
// counts of the uncommitted changes. When an error occurs computing any of
// these, ListStaleSessions treats the value conservatively (dirty, unmerged,
// or zero), so the row shows as unchecked.
//
// The sessions are inspected concurrently (at most staleWorkers at a time),
// and the whole call shares one uiGitTimeout deadline, so a hung git cannot
// wedge the cleanup panel. Rows keep the registry order.
func (a *App) ListStaleSessions() ([]StaleSessionVM, error) {
	days, err := a.staleThreshold()
	if err != nil {
		return nil, fmt.Errorf("ListStaleSessions: read settings: %w", err)
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var cands []registry.Workspace
	for _, w := range a.store.List() {
		if !w.Worktree || !w.LastActive.Before(cutoff) || a.isOpen(w.ID) {
			continue
		}
		cands = append(cands, w)
	}
	out := make([]StaleSessionVM, len(cands))
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	sem := make(chan struct{}, staleWorkers)
	var wg sync.WaitGroup
	for i, w := range cands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer safe.Recover("stale-session-row")
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = a.staleRow(ctx, w)
		}()
	}
	wg.Wait()
	return out, nil
}

// isOpen reports whether workspace id has a live agent pane.
func (a *App) isOpen(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.bridges[paneIDFor(id)]
	return ok
}

// staleRow inspects one stale session. One ChangedFiles call answers both
// "clean" (its status pass lists untracked files too) and the line counts.
func (a *App) staleRow(ctx context.Context, w registry.Workspace) StaleSessionVM {
	row := StaleSessionVM{
		ID:         w.ID,
		Title:      w.Title,
		Branch:     w.Branch,
		Agent:      w.Agent,
		LastActive: w.LastActive,
	}
	if diffs, err := gitpkg.ChangedFiles(ctx, a.runner(), w.WorktreePath); err == nil {
		row.Clean = len(diffs) == 0
		for _, d := range diffs {
			row.Added += d.Added
			row.Removed += d.Removed
		}
	}
	base := w.BaseRef
	if base == "" {
		base = "HEAD"
	}
	if merged, err := gitpkg.BranchMerged(ctx, a.runner(), w.RepoPath, w.Branch, base); err == nil {
		row.Merged = merged
	}
	row.Safe = row.Clean && row.Merged
	return row
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
		// The tree was deleted outside perch: drop git's stale registration
		// of it instead of a plain remove that git would refuse.
		a.forgetGoneWorktree(ctx, w)
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
//
// paneID must be the home shell ("shell-home") or a workspace drawer
// ("shell-<id>" or "shell-<id>_<n>") of a workspace in the registry; any
// other id, including an agent pane "pane-<id>", is rejected. The home shell
// always starts in HomeShellCwd and ignores cwd. A workspace drawer's cwd
// must lie inside that workspace's tree and under a configured root.
func (a *App) OpenShell(paneID, cwd string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	if paneID == homeShellPaneID {
		// The home shell has no workspace and is almost never under a
		// configured root. Its cwd is derived here, not taken from IPC.
		cwd = a.HomeShellCwd()
	} else {
		workspaceID := workspaceIDForShellPane(paneID)
		if workspaceID == "" || validateSessionID(workspaceID) != nil {
			return fmt.Errorf("invalid pane id %q: not a shell drawer pane", paneID)
		}
		// Serialize with the workspace's lifecycle: a drawer the frontend
		// respawns while RemoveWorkspace runs (it closes the shells before
		// git deletes the tree) waits here, then finds the record gone,
		// instead of leaving a shell in a deleted directory.
		unlock := a.lockWorkspace(workspaceID)
		defer unlock()
		w, ok := a.store.Get(workspaceID)
		if !ok {
			return fmt.Errorf("invalid pane id %q: unknown workspace %q", paneID, workspaceID)
		}
		if worktreeGone(w) {
			return fmt.Errorf("open shell %q: %w", w.WorktreePath, ErrWorktreeMissing)
		}
		if err := validateWorktreeUnderRoots(cwd, a.roots); err != nil {
			return fmt.Errorf("invalid shell cwd: %w", err)
		}
		if err := validateWorktreeUnderRoots(cwd, []string{w.WorktreePath}); err != nil {
			return fmt.Errorf("invalid shell cwd: not inside the workspace tree: %w", err)
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
		overlay, unset := a.envFor(workspaceID)
		env = withTerm(mergeEnv(withoutKeys(os.Environ(), unset), injected, overlay))
	} else if t := os.Getenv("TERM"); t == "" || t == "dumb" {
		// The home shell otherwise inherits the process environment as is.
		env = withTerm(os.Environ())
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
	return normalizeSettings(s), nil
}

// defaultSettings is the source of truth for settings defaults; the frontend
// mirrors these in frontend/src/lib/constants.ts. GetSettings returns these
// defaults on first run (no settings file) and when an existing settings
// file is corrupt.
func defaultSettings() Settings {
	return Settings{
		Theme:              defaultTheme,
		Density:            defaultDensity,
		Font:               defaultFont,
		StaleThresholdDays: defaultStaleThresholdDays,
		AlwaysRules:        []AlwaysRule{},
	}
}

// normalizeSettings fills what a stored settings file may lack: an absent or
// null alwaysRules becomes [] (the settings panel reads .length on it), and a
// blank theme, density or font falls back to its default.
func normalizeSettings(s Settings) Settings {
	if s.AlwaysRules == nil {
		s.AlwaysRules = []AlwaysRule{}
	}
	if s.Theme == "" {
		s.Theme = defaultTheme
	}
	if s.Density == "" {
		s.Density = defaultDensity
	}
	if s.Font == "" {
		s.Font = defaultFont
	}
	return s
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
// SaveSettings never writes AlwaysRules: it keeps the rules already on
// disk and ignores the incoming list. Rules change only through
// Approve("always"), ApproveAlways and RemoveAlwaysRule, each of which does
// its own read-modify-write under settingsMu. A frontend preference save
// (theme, density, DND...) carries a snapshot of the rules read earlier, so
// writing that snapshot back would drop a rule granted in between.
func (a *App) SaveSettings(s Settings) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	cur, err := a.GetSettings()
	if err != nil {
		return err
	}
	s.AlwaysRules = cur.AlwaysRules
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
	//   2. The final component must be a plain name, not "." or "..". The
	//      file is then written as that name inside the RESOLVED parent, so
	//      a symlinked root or ancestor (for example /home -> /var/home)
	//      works, and the write lands exactly where the check looked (APP-12).
	cleanAbs := filepath.Clean(absPath)
	if !filepath.IsAbs(cleanAbs) {
		return fmt.Errorf("WriteFile: path must be absolute")
	}
	parentDir := filepath.Dir(cleanAbs)
	if err := validateWorktreeUnderRoots(parentDir, a.roots); err != nil {
		return err
	}
	name := filepath.Base(cleanAbs)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return fmt.Errorf("WriteFile: path %q has no file name", absPath)
	}
	resolvedParent, err := filepath.EvalSymlinks(parentDir)
	if err != nil {
		return fmt.Errorf("WriteFile: resolve parent %q: %w", parentDir, err)
	}
	return fspkg.WriteFile(filepath.Join(resolvedParent, name), []byte(content))
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
	keys := make([]string, 0, len(a.pending))
	for key := range a.pending {
		if strings.LastIndex(key, ":") >= 0 { // skip a malformed key rather than mis-route
			keys = append(keys, key)
		}
	}
	// Arrival order, so a reload rebuilds each workspace's queue in the same
	// order every time (map iteration is random). Entries without a recorded
	// sequence sort first, by key.
	sort.Slice(keys, func(i, j int) bool {
		si, sj := a.pendingSeq[keys[i]], a.pendingSeq[keys[j]]
		if si != sj {
			return si < sj
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		sep := strings.LastIndex(key, ":")
		out = append(out, PendingApprovalVM{
			WorkspaceID: key[sep+1:],
			Req:         a.pending[key],
		})
	}
	return out
}

// Approve routes a tool-approval decision to the owning Monitor. reqID has
// the form "<raw>:<workspaceID>". decision is "allow", "deny", or "always".
// On "always", Approve persists an AlwaysRule to Settings.
func (a *App) Approve(reqID, decision string) error {
	_, err := a.approve(reqID, decision)
	return err
}

// AlwaysGrant is the result of ApproveAlways. Added is true only when this
// grant appended Rule to the settings; when an identical rule already existed
// (or the request carried no input to key a rule on), Added is false and an
// Undo must not remove anything.
type AlwaysGrant struct {
	Rule  AlwaysRule `json:"rule"`
	Added bool       `json:"added"`
}

// ApproveAlways is Approve(reqID, "always") that also reports the rule the
// grant added, so the frontend's Undo can revoke exactly that rule with
// RemoveAlwaysRule instead of diffing two reads of the whole list (which can
// pick up a rule another grant added in between).
func (a *App) ApproveAlways(reqID string) (AlwaysGrant, error) {
	added, err := a.approve(reqID, "always")
	if added == nil {
		return AlwaysGrant{}, err
	}
	return AlwaysGrant{Rule: *added, Added: true}, err
}

// RemoveAlwaysRule deletes the always-allow rule identical to rule (agent,
// tool, pattern and hash) from the CURRENT settings, under settingsMu, so a
// rule granted concurrently is never lost to a stale whole-list write. It
// reports whether a rule was removed; an absent rule is not an error.
func (a *App) RemoveAlwaysRule(rule AlwaysRule) (bool, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	s, err := a.GetSettings()
	if err != nil {
		return false, err
	}
	kept := make([]AlwaysRule, 0, len(s.AlwaysRules))
	removed := false
	for _, r := range s.AlwaysRules {
		if r == rule {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	if !removed {
		return false, nil
	}
	s.AlwaysRules = kept
	if err := a.saveSettingsLocked(s); err != nil {
		return false, err
	}
	return true, nil
}

// approve implements Approve. It returns the always-allow rule it appended,
// or nil when it appended none.
func (a *App) approve(reqID, decision string) (*AlwaysRule, error) {
	sep := strings.LastIndex(reqID, ":")
	if sep < 0 {
		return nil, fmt.Errorf("invalid reqID format %q", reqID)
	}
	rawReqID := reqID[:sep]
	workspaceID := reqID[sep+1:]

	a.mu.Lock()
	mon, ok := a.monitors[workspaceID]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no active monitor for workspace %q", workspaceID)
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
		return nil, fmt.Errorf("unknown decision %q", decision)
	}

	// This reads the pending approval for this exact reqID BEFORE the
	// verdict goes out: mon.Approve makes the monitor emit the approval's
	// resolution, and the event pump then deletes the entry, possibly before
	// this goroutine runs again. tool and input come from the backend's
	// record of what was actually surfaced, never from the frontend, so an
	// always-rule cannot be forged to grant something the user did not see.
	// Resolving by reqID, not a racy "last approval" accessor, stays correct
	// even when many approvals are pending across workspaces.
	a.mu.Lock()
	req, hadPending := a.pending[reqID]
	a.mu.Unlock()

	if err := mon.Approve(rawReqID, d); err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.deletePendingLocked(reqID)
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
			return nil, err
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
			rule := AlwaysRule{
				Agent:   agentName,
				Tool:    req.Tool,
				Pattern: req.Input,     // truncated display value
				Hash:    req.InputHash, // hash of full input, authoritative match key
			}
			s.AlwaysRules = append(s.AlwaysRules, rule)
			// settingsMu is already held here, so this calls the unlocked
			// inner helper to avoid a re-entrant deadlock; SaveSettings would
			// otherwise re-take settingsMu. The request itself is already
			// allowed, so a failed write is reported as such: the user must
			// learn that the rule was not saved and will prompt again.
			if err := a.saveSettingsLocked(s); err != nil {
				a.settingsMu.Unlock()
				return nil, fmt.Errorf("allowed, but could not save the always-allow rule: %w", err)
			}
			a.settingsMu.Unlock()
			return &rule, nil
		}
		a.settingsMu.Unlock()
	}
	return nil, nil
}

// DiffStat returns the per-file summary of every uncommitted change in
// worktree (gitpkg.ChangedFiles), validated against roots. Paths are never
// quoted, renames carry oldPath, and line counts are net against HEAD.
func (a *App) DiffStat(worktree string) ([]gitpkg.FileDiff, error) {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.ChangedFiles(ctx, a.runner(), worktree)
}

// Hunks returns the unified hunks for a single file in worktree. Each hunk
// carries its content id, which the hunk mutators take back.
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

// validateHunkArgs gates the arguments every hunk mutator shares.
func (a *App) validateHunkArgs(worktree, file, id string) error {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return err
	}
	if err := validateRelFile(file); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("hunk id must not be empty")
	}
	return nil
}

// StageHunk stages the working-tree hunk of file whose content id is id
// (Hunk.id from Hunks) with `git apply --cached`. index is the Hunk.index the
// user saw; it only breaks a tie between identical hunks. When that content
// is no longer in the diff (the agent edited the file meanwhile), StageHunk
// changes nothing and returns an error wrapping gitpkg.ErrHunkChanged; the
// frontend re-fetches hunks and tells the user.
func (a *App) StageHunk(worktree, file string, index int, id string) error {
	if err := a.validateHunkArgs(worktree, file, id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.StageHunkChecked(ctx, a.runner(), worktree, file, index, id)
}

// DiscardHunk reverts the working-tree hunk of file whose content id is id
// (`git apply --reverse`), with the same matching rules as StageHunk. It
// never reverts a change the user did not see.
func (a *App) DiscardHunk(worktree, file string, index int, id string) error {
	if err := a.validateHunkArgs(worktree, file, id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.DiscardHunkChecked(ctx, a.runner(), worktree, file, index, id)
}

// UnstageHunk moves the staged hunk of file whose content id is id back to
// the working tree (`git apply --reverse --cached`), with the same matching
// rules as StageHunk. It touches the index only, never the working tree.
func (a *App) UnstageHunk(worktree, file string, index int, id string) error {
	if err := a.validateHunkArgs(worktree, file, id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiGitTimeout)
	defer cancel()
	return gitpkg.UnstageHunkChecked(ctx, a.runner(), worktree, file, index, id)
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
// returns a deduplicated, frontend-ready slice sorted by name, then path.
// DiscoverRepos is for the New Session dialog on a fresh install, when there
// are no existing workspaces.
//
// DiscoverRepos runs best-effort. A root that fails or times out still
// contributes the repositories found before it stopped. It returns an error
// only when it found nothing and every root failed. It returns an empty,
// non-nil slice when it finds no repositories.
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
			// discover.Projects returns the projects found so far with
			// ctx.Err() on a timeout; keep them.
			lastErr = err
		} else {
			okCount++
		}

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

	if okCount == 0 && len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	// Each root's list is sorted on its own; sort the merged list so several
	// roots read as one alphabetical list.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
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
