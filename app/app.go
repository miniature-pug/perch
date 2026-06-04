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
	"github.com/Miniature-Pug/perch/internal/discover"
	fspkg "github.com/Miniature-Pug/perch/internal/fs"
	gitpkg "github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/notify"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/registry"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// spawnPtyFunc and newMonitorFunc are injectable seams (real funcs in NewApp,
// replaced in tests for headless execution).
type spawnPtyFunc func(ctx context.Context, cwd string, argv []string, dataEvent, exitEvent string,
	emit internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error)

type newMonitorFunc func(tool string, adapter agent.Adapter) (agent.Monitor, error)

// newWatcherFunc is an injectable seam for the fs watcher (real fspkg.Watch in
// NewApp, replaced with a fake in tests for headless execution).
type newWatcherFunc func(absRoot string, onChange func(string)) (*fspkg.Watcher, error)

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
	// M-12 fix: prevents concurrent Approve(always) calls from losing rules.
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
		debounce:     150 * time.Millisecond,
		settingsPath: filepath.Join(registry.DefaultConfigDir(), "settings.json"),
		layoutPath:   filepath.Join(registry.DefaultConfigDir(), "layout.json"),
		focused:      true, // default: assume focused until the frontend reports otherwise
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
	// L-12: reset pending approvals on shutdown so stale entries cannot outlive
	// their workspaces.
	a.pending = map[string]agent.ApprovalReq{}
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
	Pattern string `json:"pattern"` // truncated display value; NOT the security boundary
	// Hash is the hex-encoded sha256 of the FULL (untruncated) tool input at the
	// time the user clicked "always". maybeAutoApprove matches on Hash, not Pattern,
	// so two inputs sharing a 4096-byte prefix cannot collide (M-13 fix).
	Hash string `json:"hash,omitempty"`
}

// CreateWorkspace validates inputs, resolves/creates the worktree, persists the
// workspace to the registry, and returns its WorkspaceVM. It does NOT start the
// agent — call OpenWorkspace for that. The model arg is stored in the workspace
// record and passed to the agent via Prepare on fresh-start (non-resume) opens.
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

	now := time.Now()
	w := registry.Workspace{
		ID:           id,
		WorktreePath: treePath,
		Agent:        agentName,
		Title:        handle,
		Branch:       branch,
		Model:        model,
		LastActive:   now,
	}
	if err := a.store.Upsert(w); err != nil {
		return WorkspaceVM{}, fmt.Errorf("persist workspace: %w", err)
	}

	return WorkspaceVM{
		ID:           id,
		WorktreePath: treePath,
		Agent:        agentName,
		Title:        handle,
		Branch:       branch,
		PaneID:       paneIDFor(id),
		LastActive:   now,
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
	// L-11: validate workspace id before touching the store.
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	w, ok := a.store.Get(id)
	if !ok {
		return fmt.Errorf("unknown workspace %q", id)
	}

	paneID := "pane-" + id
	event := "pty:data:" + paneID
	exitEvent := "pty:exit:" + paneID

	wctx, cancel := context.WithCancel(context.Background())

	br, err := a.spawnPty(wctx, w.WorktreePath, internalpty.LoginShellArgv(), event, exitEvent, a.emit, 220, 50)
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

	launchCmd, err := mon.Prepare(wctx, id, w.WorktreePath, w.LastSessionID, w.Model)
	if err != nil {
		cancel()
		_ = br.Close()
		_ = mon.Teardown()
		return fmt.Errorf("monitor prepare: %w", err)
	}

	// REQUIRED: start the monitor's event pump (translation/SSE), bound to wctx.
	mon.Start(wctx)

	// Wire the fs watcher with a wctx-bound debounce goroutine. The watcher is
	// started non-fatally: a failure degrades gracefully (no watcher) but never
	// fails OpenWorkspace.
	var watcher *fspkg.Watcher
	if a.newWatcher != nil {
		changes := make(chan string, 64)

		// Debounce goroutine: coalesces raw onChange signals into a single
		// fs:changed emit per debounce window, bound to wctx lifetime.
		go func() {
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
	a.cancels[id] = func() { cancel(); if watcher != nil { _ = watcher.Close() } }
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
				// BUG 4 fix: stamp WorkspaceID so the frontend can match events to
				// the correct workspace (evt.workspaceId == "" before this fix).
				evt.WorkspaceID = id
				// BUG 5 fix: compose the approval ReqID as "<raw>:<workspaceID>" so
				// that Approve() can parse and route it via strings.LastIndex(":").
				// Copy the ApprovalReq to avoid mutating the monitor's own pointee.
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
				// L-10: validate the session id before persisting; an invalid id
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
	agentName := "claude"
	if w, ok := a.store.Get(workspaceID); ok && w.Agent != "" {
		agentName = w.Agent
	}
	// M-12: hold settingsMu (read side) so we see a consistent snapshot of
	// settings and don't race with a concurrent Approve(always) write.
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
		// M-13: the SHA-256 hash of the full (untruncated) tool input is the sole
		// authoritative match key. Pattern is display-only (it is truncated to
		// MaxApprovalInputLen, so two inputs sharing a 4096-byte prefix collide on
		// Pattern — matching on it would be a privilege-escalation hole). A rule
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
// also fires an OS desktop notification. Per SPEC §8, Do-Not-Disturb mutes only
// tiers 2–3 (ambient + routine) and never tier 1 (blocking); since only blocking
// events fire an OS notification, DND has no bearing on the OS-notify path.
func (a *App) dispatchNotify(evt agent.Event) {
	var tier, title, body string
	switch {
	case evt.Kind == "state" && evt.State == agent.StateAwaitingApproval:
		tier, title, body = "blocking", "Approval needed", "An agent is waiting for your decision."
	case evt.Kind == "state" && evt.State == agent.StateDone:
		tier, title, body = "ambient", "Turn complete", "Agent finished a turn."
	case evt.Kind == "state" && evt.State == agent.StateErrored:
		tier, title, body = "blocking", "Agent error", evt.Err
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
	// window is unfocused. DND is deliberately NOT consulted here — SPEC §8 says
	// DND never mutes blocking (tier 1), and only blocking fires an OS notification.
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
	return br.Resize(cols, rows)
}

// CloseWorkspace cancels the workspace pump, tears down the monitor, and closes
// the pty — but keeps the workspace record in the registry (it can be reopened).
func (a *App) CloseWorkspace(id string) error {
	// L-11: validate workspace id before touching the store.
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
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
	// L-12: purge pending approvals belonging to this workspace so that a
	// closed workspace does not accumulate phantom entries in the pending map.
	// Pending keys have the form "<raw>:<workspaceID>" (see Approve / event pump);
	// validateSessionID forbids ':' in ids so the suffix match is unambiguous.
	suffix := ":" + id
	for k := range a.pending {
		if strings.HasSuffix(k, suffix) {
			delete(a.pending, k)
		}
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
	// L-11: validate workspace id before touching the store.
	// CloseWorkspace also validates, but we validate here first so RemoveWorkspace
	// returns a clear validation error rather than silently calling CloseWorkspace
	// (which would return the same error) before the store.Remove.
	if err := validateSessionID(id); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
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
	if err := validateWorktreeUnderRoots(cwd, a.roots); err != nil {
		return fmt.Errorf("invalid shell cwd: %w", err)
	}
	event := "pty:data:" + paneID
	exitEvent := "pty:exit:" + paneID
	ctx := context.Background()
	br, err := a.spawnPty(ctx, cwd, internalpty.LoginShellArgv(), event, exitEvent, a.emit, 220, 50)
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
	// L-5: validate absPath itself (not just its Dir) so a symlink-as-final-component
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
	// L-8: validate before the ctx guard so tests can exercise the security
	// boundary without a Wails runtime (ctx == nil → clipboard no-op after validation).
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
	return gitpkg.Branches(context.Background(), a.runner(), repo)
}

// Worktrees returns git worktree info for the repo at repo.
func (a *App) Worktrees(repo string) ([]gitpkg.WorktreeInfo, error) {
	if err := validateWorktreeUnderRoots(repo, a.roots); err != nil {
		return nil, err
	}
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
		agentName := "claude"
		if w, ok := a.store.Get(workspaceID); ok && w.Agent != "" {
			agentName = w.Agent
		}
		// M-12: hold settingsMu across the entire read-modify-write so that
		// concurrent Approve(always) calls cannot interleave and lose rules.
		// DEADLOCK GUARD: a.mu is released above before settingsMu is taken.
		a.settingsMu.Lock()
		s, _ := a.GetSettings()
		dup := false
		for _, r := range s.AlwaysRules {
			// Dedup on the same authoritative key used for matching (M-13: hash).
			if r.Agent == agentName && r.Tool == req.Tool && r.Hash != "" && r.Hash == req.InputHash {
				dup = true
				break
			}
		}
		if !dup {
			s.AlwaysRules = append(s.AlwaysRules, AlwaysRule{
				Agent:   agentName,
				Tool:    req.Tool,
				Pattern: req.Input,       // truncated display value
				Hash:    req.InputHash,   // M-13: hash of full input, authoritative match key
			})
			_ = a.SaveSettings(s)
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
	return gitpkg.DiffStat(context.Background(), a.runner(), worktree)
}

// Hunks returns the unified hunks for a single file in worktree.
func (a *App) Hunks(worktree, file string) ([]gitpkg.Hunk, error) {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return nil, err
	}
	if err := validateRelFile(file); err != nil {
		return nil, err
	}
	return gitpkg.Hunks(context.Background(), a.runner(), worktree, file)
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
	return gitpkg.StageHunk(context.Background(), a.runner(), worktree, file, index)
}

// DiscardHunk reverses hunk `index` of file in the working tree (git apply --reverse).
func (a *App) DiscardHunk(worktree, file string, index int) error {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return err
	}
	if err := validateRelFile(file); err != nil {
		return err
	}
	return gitpkg.DiscardHunk(context.Background(), a.runner(), worktree, file, index)
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
		pts, err := discover.Projects(
			context.Background(),
			a.runner(),
			root,
			discover.Options{},
			map[string]discover.ProjectStat{},
			time.Now().Unix(),
		)
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
	case "claude":
		return agent.NewClaude()
	case "opencode":
		return agent.NewOpencode()
	default:
		return nil
	}
}
