// internal/agent/claude_monitor.go
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/safe"
)

type ClaudeMonitor struct {
	adapter  Adapter
	listener *hooklistener.Listener
	ownedLn  bool // true when this monitor created the listener; Teardown then closes it
	events   chan Event
	cwd      string

	mu       sync.Mutex
	state    State
	lastTool string
	// exited is set once the AgentExit sentinel is translated to StateExited.
	// Like the opencode monitor's exited flag, this field makes StateExited
	// terminal. A straggler hook, for example a fire-and-forget Stop curl
	// that raced the AgentExit curl and lands on the listener channel AFTER
	// it, must not clobber StateExited back to done or running. Guarded by
	// mu.
	exited bool
	// ctx is the monitor's lifetime context, captured in Start. Approve
	// selects on ctx, so a reliable clearing-event send never blocks past
	// teardown.
	ctx context.Context
}

func newClaudeMonitor(a Adapter) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, events: make(chan Event, monitorEventChanBuf)}
}
func NewClaudeMonitorWithListener(a Adapter, l *hooklistener.Listener) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, listener: l, events: make(chan Event, monitorEventChanBuf)}
}
func (m *ClaudeMonitor) Events() <-chan Event { return m.events }

// Capabilities reports what this monitor supports.
func (m *ClaudeMonitor) Capabilities() Caps {
	return Caps{Approvals: true, Attention: true}
}

func (m *ClaudeMonitor) Start(ctx context.Context) {
	// Capture the pump's context, so Approve can select on the same
	// cancellation signal when it emits its clearing event. See Approve.
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	go func() {
		defer safe.Recover("claude-monitor")
		for {
			select {
			case <-ctx.Done():
				return
			case he, ok := <-m.listener.Events():
				if !ok {
					return
				}
				m.translateAndEmit(ctx, he)
			}
		}
	}()
}

func (m *ClaudeMonitor) translateAndEmit(ctx context.Context, he hooklistener.HookEvent) {
	var ev Event
	switch he.Type {
	case "SessionStart":
		ev = Event{Kind: "state", State: StateRunning, SessionID: he.SessionID}
	case "Stop":
		// Stop means the agent finished its turn. Emit StateDone, and let
		// dispatchNotify fire the "Turn complete" ambient toast.
		ev = Event{Kind: "state", State: StateDone}
	case "StopFailure":
		ev = Event{Kind: "state", State: StateErrored, Err: he.ErrorType}
	case hookEventAgentExit:
		// The exit sentinel (see exit_sentinel.go) fired. The claude
		// PROCESS is gone, while the login shell survives, so no pty:exit
		// event will fire. Surface StateExited instead. StateExited is
		// terminal, and distinct from StateErrored. The state-write code
		// below ARMS the exited guard on this event, and RE-CHECKS the
		// guard on every later event. claude fires no hook once its
		// process is dead, so AgentExit is normally the last event. But a
		// fire-and-forget Stop curl that raced the AgentExit curl could
		// land on this listener AFTER it. The guard keeps that straggler
		// from clobbering StateExited back to done. This mirrors the
		// opencode monitor.
		ev = Event{Kind: "state", State: StateExited, Err: exitReason(he.ErrorType)}
	// NOTE: this switch intentionally has no "Notification" case. perch
	// hooks exactly four events: PreToolUse, Stop, StopFailure, and
	// SessionStart. "Notification" is not among them, and it is NOT
	// installed in perchMonitorEvents. So the hook never fires, and a case
	// for it would be unreachable. No UI surface consumes a claude
	// attention-notification signal. Leaving out the dead case keeps the
	// code honest.
	case "PreToolUse":
		if he.ToolName == toolAskUserQuestion {
			// AskUserQuestion is the agent asking the USER to choose. It
			// is not a request to act on the system. So it is an
			// attention SIGNAL, not an approval. Surface
			// StateAwaitingInput, the "question" sidebar signal plus a
			// blocking notification, and auto-allow the hook right away.
			// This lets claude render the question in its own pane TUI,
			// where the user answers. Gating this behind perch's approval
			// card would double-prompt and block the agent for no reason.
			// Auto-allow is safe because the tool has no system side
			// effect to approve. ExitPlanMode is deliberately NOT treated
			// this way: auto-allowing it would skip the user's plan
			// review. So ExitPlanMode stays on the normal approval path
			// below.
			m.listener.Decide(he.ReqID, hooklistener.Decision{Allow: true})
			ev = Event{Kind: "question", State: StateAwaitingInput}
		} else {
			sum := he.ToolName
			if n := len(he.ToolInput); n > 0 {
				if n < toolInputSummaryCutoff {
					sum += ": " + string(he.ToolInput)
				} else {
					// The full input ships separately (Input, below, up
					// to MaxApprovalInputLen). So a long input is
					// ELLIPSIZED into the summary instead of dropped.
					// Otherwise the approval card would show only the
					// bare tool name, and the user could not tell what
					// the agent is asking to run.
					sum += ": " + ellipsizeInput(string(he.ToolInput), toolInputSummaryCutoff)
				}
			}
			fullInput := string(he.ToolInput)
			// Compute the hash of the FULL, untruncated input before
			// truncation, so two inputs that share a 4096-byte prefix
			// produce distinct hashes.
			h := sha256.Sum256([]byte(fullInput))
			inputHash := hex.EncodeToString(h[:])
			input := fullInput
			if len(input) > MaxApprovalInputLen {
				input = input[:MaxApprovalInputLen]
			}
			ev = Event{Kind: "approval", State: StateAwaitingApproval,
				Approval: &ApprovalReq{ReqID: he.ReqID, Tool: he.ToolName, Summary: sum, Input: input, InputHash: inputHash}}
		}
	default:
		return
	}
	// Track state and lastTool (mutex-guarded) BEFORE emitting, so a
	// reader that observes the event on the channel also observes the
	// updated state.
	m.mu.Lock()
	// StateExited is terminal (bug F32). Once the AgentExit sentinel has
	// translated, drop any straggler hook, so it cannot clobber StateExited
	// back to done or running, and cannot emit a non-exited state event.
	// Unlike opencode, this pump runs in a single goroutine, so this
	// function has no check-then-act window on its own. But two
	// near-simultaneous loopback POSTs (a fire-and-forget Stop curl and
	// the AgentExit curl) can land on the listener out of order. This
	// guard keeps AgentExit terminal regardless.
	if m.exited {
		m.mu.Unlock()
		return
	}
	if ev.State == StateExited {
		m.exited = true
	}
	if ev.State != "" {
		m.state = ev.State
	}
	if ev.Approval != nil && ev.Approval.Tool != "" {
		m.lastTool = ev.Approval.Tool
	}
	m.mu.Unlock()
	select {
	case m.events <- ev:
	case <-ctx.Done():
	}
}

func (m *ClaudeMonitor) Approve(reqID string, d Decision) error {
	m.listener.Decide(reqID, hooklistener.Decision{Allow: d.Allow, Always: d.Always})
	// The decision unblocks the hook handler, and the agent resumes. Clear
	// the amber awaiting-approval indicator by advancing the cached state
	// and emitting a clearing event, but ONLY if the monitor is still
	// awaiting approval. A newer real state (StateDone or StateErrored) can
	// arrive on the SSE/hook stream before the user's decision lands. This
	// can happen when the agent's turn ends while the card sits open.
	// Clobbering that state with StateRunning would show a stale "running"
	// feel. The cleared state depends on the decision: allow or always
	// clears to StateRunning, because the tool proceeds; deny clears to
	// StateIdle, because the agent may stop. This uses the same mutex
	// translateAndEmit uses, so m.state stays consistent. This code
	// updates m.state regardless of whether the buffered channel accepts
	// the frame, so CurrentState() is always correct even if the send
	// never lands.
	cleared := StateRunning
	if !d.Allow {
		cleared = StateIdle
	}
	m.mu.Lock()
	if m.state != StateAwaitingApproval {
		// A newer real state already landed. Do not clobber it, and do
		// not emit.
		m.mu.Unlock()
		return nil
	}
	m.state = cleared
	ctx := m.ctx
	m.mu.Unlock()
	// Emit the clearing event with a reliable, cancellable send. This is
	// the same shape the normal pump uses. A non-blocking drop here would
	// leave the frontend stuck on awaiting-approval whenever the 64-slot
	// events channel is momentarily full. Block until the event is
	// delivered, or until the monitor's context is done, so a torn-down
	// monitor never deadlocks the Wails binding thread that calls Approve.
	if ctx == nil {
		// Approve was called before Start captured a context. Fall back
		// to a best-effort, non-blocking send instead of blocking forever.
		select {
		case m.events <- Event{Kind: "state", State: cleared}:
		default:
		}
		return nil
	}
	select {
	case m.events <- Event{Kind: "state", State: cleared}:
	case <-ctx.Done():
	}
	return nil
}

func (m *ClaudeMonitor) CurrentState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == "" {
		return StateIdle
	}
	return m.state
}

func (m *ClaudeMonitor) LastApprovalTool() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastTool
}

// monitorEventChanBuf is the shared event-channel buffer size for both
// ClaudeMonitor and OpencodeMonitor. It is sized to absorb bursts without
// blocking the emitter goroutine.
const monitorEventChanBuf = 64

// toolAskUserQuestion is the claude built-in tool the agent calls to ask
// the user a multiple-choice question. perch treats its PreToolUse event as
// an attention SIGNAL (StateAwaitingInput), and auto-allows it, so claude
// renders the question in its own pane TUI. See the PreToolUse case in
// translateAndEmit.
const toolAskUserQuestion = "AskUserQuestion"

// toolInputSummaryCutoff is the maximum raw ToolInput byte length included
// verbatim in the approval-event Summary. perch ELLIPSIZES an input at or
// above this threshold to this byte budget, the first ~cutoff bytes plus
// "…", instead of dropping it, so the card always shows what the tool is
// about to do. The full, untruncated input is still hashed and shipped
// separately as Input.
const toolInputSummaryCutoff = 120

// summaryEllipsis marks a truncated tool-input summary. Its 3-byte width
// is subtracted from the byte budget, so the ellipsized string stays
// within the cutoff.
const summaryEllipsis = "…"

// ellipsizeInput truncates s to a maxBytes byte budget, INCLUDING the
// ellipsis marker, and appends "…". It cuts on a UTF-8 rune boundary, so it
// never splits a multi-byte rune. ellipsizeInput returns inputs shorter
// than the budget unchanged.
func ellipsizeInput(s string, maxBytes int) string {
	if len(s) < maxBytes {
		return s
	}
	budget := maxBytes - len(summaryEllipsis)
	if budget <= 0 {
		return summaryEllipsis
	}
	truncated := s[:budget]
	// Trim any trailing bytes that form an incomplete final rune.
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + summaryEllipsis
}

// claudeMonitorDirMode is the directory mode used when creating .claude/.
const claudeMonitorDirMode = 0o755

// tokenFileMode is the file mode enforced on any temp file that carries a
// Bearer token. 0600 means owner read/write only, with no group or world
// access.
const tokenFileMode = 0o600

const perchMonitorSentinel = "perch-monitor-hook"

var perchMonitorEvents = []string{"PreToolUse", "Stop", "StopFailure", "SessionStart"}

func (m *ClaudeMonitor) Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (string, error) {
	m.cwd = cwd
	if m.listener == nil {
		l, err := hooklistener.New()
		if err != nil {
			return "", fmt.Errorf("ClaudeMonitor.Prepare: listener: %w", err)
		}
		m.listener = l
		m.ownedLn = true
	}
	if err := m.writeHooks(cwd); err != nil {
		return "", err
	}
	var args []string
	if resumeID != "" {
		args = m.adapter.ResumeArgs(resumeID)
	} else {
		args = m.adapter.NewArgs()
	}
	// perch writes the returned command verbatim into the pane's pty (see
	// pty.Bridge.Write, a raw passthrough with no transformation).
	// exitSentinel is appended, so that when claude exits, gracefully or
	// via SIGKILL or OOM, the shell pings the listener with the captured
	// exit code. PaneEnv() supplies the token and URL that exitSentinel
	// references by name; the command never inlines them, because the
	// interactive shell echoes the line. The trailing newline is what
	// actually submits the command to the shell. Without it, the launch
	// command sits on the prompt unexecuted, and the agent never starts.
	return strings.Join(append([]string{m.adapter.Name()}, args...), " ") + exitSentinel + "\n", nil
}

// PaneEnv supplies the exit sentinel's token and URL through the pane
// shell's process environment, so the launch line can reference them by
// name instead of inlining the bearer token, which the interactive shell
// would echo on screen. Call PaneEnv after Prepare, which creates
// m.listener. claude reuses its existing hook listener.
func (m *ClaudeMonitor) PaneEnv() []string {
	// Prepare writes m.listener, and this method and Start both read it.
	// OpenWorkspace sequences all of this (Prepare, then PaneEnv, then
	// spawn, then Start) on one goroutine, so this unlocked read matches
	// the existing unlocked listener access, with no race.
	return exitPaneEnv(m.listener)
}

// RewriteHooks re-installs this monitor's hook group into the worktree
// settings.json. It exists for the Reopen path in app.OpenWorkspace. A
// displaced monitor's Teardown strips EVERY perch hook group, because they
// share one sentinel, including the group this monitor's Prepare tried to
// install. Prepare's merge is idempotent on that shared sentinel, so while
// the old group was still present, Prepare's merge added nothing. So the
// file ends up carrying the OLD listener's address and token, or none at
// all. Call RewriteHooks AFTER the old Teardown, when settings.json no
// longer holds a perch group. RewriteHooks writes THIS monitor's LIVE
// listener address and token, so the reopened agent's hooks POST to a
// listener perch is actually watching. That is what emits SessionStart,
// which becomes StateRunning, and heals the "session has ended" overlay.
//
// RewriteHooks is a no-op before Prepare has run, because no cwd or
// listener exists yet. It is idempotent after that: if this monitor's
// group is somehow already present, mergeMonitorHooks adds nothing.
func (m *ClaudeMonitor) RewriteHooks() error {
	if m.cwd == "" || m.listener == nil {
		return nil
	}
	return m.writeHooks(m.cwd)
}

func (m *ClaudeMonitor) writeHooks(cwd string) error {
	dir := filepath.Join(cwd, ".claude")
	path := filepath.Join(dir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	merged, err := mergeMonitorHooks(existing, m.listener.Addr(), m.listener.Token())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, claudeMonitorDirMode); err != nil {
		return err
	}
	return atomicWrite(path, merged)
}

// mergeMonitorHooks is pure: it does no I/O. It uses an additive-append and
// sentinel idiom: each perch group has a command containing
// perchMonitorSentinel, so Teardown can identify it.
func mergeMonitorHooks(existing []byte, addr, token string) ([]byte, error) {
	var s map[string]any
	if tr := strings.TrimSpace(string(existing)); tr != "" {
		if err := json.Unmarshal(existing, &s); err != nil {
			return nil, err
		}
	}
	if s == nil {
		s = map[string]any{}
	}
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil {
		hm = map[string]any{}
	}
	s["hooks"] = hm

	url := "http://" + addr + "/hook"
	cmd := fmt.Sprintf(`curl -sf -X POST -H "Authorization: Bearer %s" -H "Content-Type: application/json" -d @- %s # %s`,
		token, url, perchMonitorSentinel)
	for _, ev := range perchMonitorEvents {
		arr, _ := hm[ev].([]any)
		if isPerchMonitorGroupPresent(arr) {
			continue // idempotent
		}
		arr = append(arr, map[string]any{
			"matcher": "",
			"hooks":   []any{map[string]any{"type": "command", "command": cmd}},
		})
		hm[ev] = arr
	}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func isPerchMonitorGroupPresent(arr []any) bool {
	for _, item := range arr {
		g, _ := item.(map[string]any)
		if isPerchMonitorGroup(g) {
			return true
		}
	}
	return false
}

func isPerchMonitorGroup(g map[string]any) bool {
	hs, _ := g["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if strings.Contains(fmt.Sprint(hm["command"]), perchMonitorSentinel) {
			return true
		}
	}
	return false
}

func (m *ClaudeMonitor) Teardown() error {
	var rmErr error
	if m.cwd != "" {
		path := filepath.Join(m.cwd, ".claude", "settings.json")
		if data, err := os.ReadFile(path); err == nil {
			rmErr = removeMonitorHooks(path, data)
		}
	}
	var closeErr error
	if m.ownedLn && m.listener != nil {
		closeErr = m.listener.Close()
	}
	return errors.Join(rmErr, closeErr)
}

func removeMonitorHooks(path string, data []byte) error {
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil {
		return nil
	}
	for _, ev := range perchMonitorEvents {
		arr, _ := hm[ev].([]any)
		var kept []any
		for _, item := range arr {
			g, _ := item.(map[string]any)
			if !isPerchMonitorGroup(g) {
				kept = append(kept, item)
			}
		}
		if len(kept) == 0 {
			delete(hm, ev)
		} else {
			hm[ev] = kept
		}
	}
	out, _ := json.MarshalIndent(s, "", "  ")
	return atomicWrite(path, append(out, '\n'))
}

// atomicWrite writes data to path atomically, through a temp file. It
// always creates the file with mode 0600, owner read/write only, because
// settings.json embeds a Bearer token. Preserving a pre-existing looser
// mode would expose the token to other local users.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// Set 0600 on the temp file before writing, so there is no window
	// where the token-bearing content is readable by group or world.
	// os.CreateTemp already uses 0600; this call sets it explicitly to
	// document the invariant.
	if err := os.Chmod(name, tokenFileMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
