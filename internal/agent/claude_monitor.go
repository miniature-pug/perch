// internal/agent/claude_monitor.go
package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/safe"
)

// ClaudeMonitor drives claude through its hook system. Prepare writes a
// PER-SESSION settings file, holding only perch's hooks (with this session's
// listener address and token), into a private directory OUTSIDE the
// worktree, and the launch line loads it with `claude --settings <file>`.
// claude merges hook lists across settings sources, so the user's own
// project and user hooks keep running. Nothing is written into the worktree,
// so a session never dirties `git status`, a token never lands in a
// committable file, two sessions sharing one cwd each reach their own
// listener, and a crash leaves no stale hook behind in the repo
// (AGT-2, AGT-3, APP-2, APP-8).
type ClaudeMonitor struct {
	adapter  Adapter
	listener *hooklistener.Listener
	ownedLn  bool // true when this monitor created the listener; Teardown then closes it
	events   chan Event
	cwd      string
	// settingsDir is the private (0700) per-session directory holding
	// settingsPath (0600). Teardown removes it.
	settingsDir  string
	settingsPath string

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
	// pending holds the raw reqIDs of PermissionRequests perch has surfaced
	// and that are still parked in the listener. awaiting-approval clears
	// only when the LAST one resolves (AGT-16). Guarded by mu.
	pending map[string]struct{}
	// outbox is the ordered queue of events waiting for delivery on events.
	// Every state change appends here under mu, so delivery order always
	// matches the order of state writes. One drain goroutine forwards it to
	// events. Enqueueing never blocks, so Approve can be called from the
	// app's event pump (the only reader of events) without deadlocking it
	// (AGT-15, APP-15). Guarded by mu.
	outbox []Event
	wake   chan struct{}
}

func newClaudeMonitor(a Adapter) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, events: make(chan Event, monitorEventChanBuf),
		pending: map[string]struct{}{}, wake: make(chan struct{}, 1)}
}
func NewClaudeMonitorWithListener(a Adapter, l *hooklistener.Listener) *ClaudeMonitor {
	m := newClaudeMonitor(a)
	m.listener = l
	return m
}
func (m *ClaudeMonitor) Events() <-chan Event { return m.events }

// Capabilities reports what this monitor supports.
func (m *ClaudeMonitor) Capabilities() Caps {
	return Caps{Approvals: true, Attention: true}
}

// HookSettingsPath returns the per-session settings file Prepare wrote, or
// "" before Prepare. The launch line passes it to `claude --settings`.
func (m *ClaudeMonitor) HookSettingsPath() string { return m.settingsPath }

func (m *ClaudeMonitor) Start(ctx context.Context) {
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
				m.translate(he)
			}
		}
	}()
	go func() {
		defer safe.Recover("claude-monitor-outbox")
		m.drain(ctx)
	}()
}

// enqueueLocked appends ev to the ordered outbox. Caller holds m.mu.
func (m *ClaudeMonitor) enqueueLocked(ev Event) {
	m.outbox = append(m.outbox, ev)
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// drain forwards the outbox to events, in order, until ctx is done.
func (m *ClaudeMonitor) drain(ctx context.Context) {
	for {
		m.mu.Lock()
		if len(m.outbox) == 0 {
			m.mu.Unlock()
			select {
			case <-m.wake:
				continue
			case <-ctx.Done():
				return
			}
		}
		ev := m.outbox[0]
		m.outbox[0] = Event{}
		m.outbox = m.outbox[1:]
		m.mu.Unlock()
		select {
		case m.events <- ev:
		case <-ctx.Done():
			return
		}
	}
}

func (m *ClaudeMonitor) translate(he hooklistener.HookEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// StateExited is terminal (bug F32). Once the AgentExit sentinel has
	// translated, drop any straggler hook, so it cannot clobber StateExited
	// back to done or running. Two near-simultaneous loopback POSTs (a
	// fire-and-forget Stop curl and the AgentExit curl) can land on the
	// listener out of order; this guard keeps AgentExit terminal regardless.
	if m.exited {
		if he.Type == hooklistener.EventPermissionRequest {
			m.listener.Decide(he.ReqID, hooklistener.Decision{Abstain: true})
		}
		return
	}
	var ev Event
	switch he.Type {
	case "SessionStart":
		// SessionStart fires at startup, resume, /clear and compaction, with
		// claude sitting at its prompt, so it is NOT "running" (AGT-7). A
		// compaction happens mid-turn, so it keeps the current state; it
		// still reports the session id.
		if he.Source == "compact" {
			ev = Event{Kind: "state", SessionID: he.SessionID}
		} else {
			ev = Event{Kind: "state", State: StateIdle, SessionID: he.SessionID}
		}
	case "UserPromptSubmit":
		// A new turn starts (AGT-7).
		ev = Event{Kind: "state", State: StateRunning, SessionID: he.SessionID}
	case "PostToolUse":
		// Installed only for the interactive tools (AskUserQuestion,
		// ExitPlanMode): the user answered, so the agent works again (AGT-7).
		clearable := m.state == StateAwaitingInput || (m.state == StateAwaitingApproval && len(m.pending) == 0)
		if !clearable {
			return
		}
		ev = Event{Kind: "state", State: StateRunning}
	case "Notification":
		// Installed only for idle_prompt: claude has sat at its prompt for
		// about a minute. Stop does not fire on a user interrupt (Esc, or
		// "No" in claude's own permission dialog), and PostToolUse does not
		// fire for a dismissed question, so this is what settles a turn the
		// user interrupted instead of leaving "running" or "awaiting-input"
		// lit until the next prompt. done and errored stay as they are.
		if he.NotificationType != "idle_prompt" || len(m.pending) > 0 {
			return
		}
		switch m.state {
		case StateRunning, StateAwaitingInput, StateAwaitingApproval:
		default:
			return
		}
		ev = Event{Kind: "state", State: StateIdle}
	case "Stop":
		// Stop means the agent finished its turn. Emit StateDone, and let
		// dispatchNotify fire the "Turn complete" ambient toast.
		ev = Event{Kind: "state", State: StateDone}
	case "StopFailure":
		ev = Event{Kind: "state", State: StateErrored, Err: he.ErrorType}
	case hookEventAgentExit:
		// The exit sentinel (see exit_sentinel.go) fired. The claude PROCESS
		// is gone, while the login shell survives, so no pty:exit event will
		// fire. Surface the terminal StateExited instead.
		ev = Event{Kind: "state", State: StateExited, Err: exitReason(he.ErrorType)}
		m.exited = true
	case "PreToolUse":
		// Installed only for the interactive tools. The agent is asking the
		// USER to choose (a question, or a plan to accept), which is an
		// attention SIGNAL, not an approval. The listener already answered
		// the hook with no decision, so claude renders its own dialog in the
		// pane through its normal flow (AGT-23).
		if !interactiveTools[he.ToolName] {
			return
		}
		ev = Event{Kind: "question", State: StateAwaitingInput}
	case hooklistener.EventPermissionRequest:
		if interactiveTools[he.ToolName] {
			// The question and plan-approval UIs are claude's own dialogs
			// (ExitPlanMode's carries mode choices and a feedback path that a
			// two-button card would lose). Never answer them: an explicit
			// allow can complete the tool without showing the dialog
			// (AGT-23). The PreToolUse signal already raised awaiting-input.
			m.listener.Decide(he.ReqID, hooklistener.Decision{Abstain: true})
			return
		}
		m.pending[he.ReqID] = struct{}{}
		ev = Event{Kind: "approval", State: StateAwaitingApproval,
			Approval: newApprovalReq(he.ReqID, he.ToolName, he.ToolInput)}
	case hooklistener.EventRequestCancelled:
		// The parked hook ended without perch's verdict: the user answered
		// claude's own dialog, pressed Esc, or the hook timed out. Retract the
		// card (ResolvedReqID) and, once nothing else is pending, clear the
		// amber state. The agent is unblocked either way, so it reads as
		// running until its next hook says otherwise (AGT-16, APP-16).
		if _, ok := m.pending[he.ReqID]; !ok {
			return
		}
		ev = m.resolveLocked(he.ReqID)
	default:
		// NOTE: there is no "Notification" case. perch does not install that
		// hook, and no UI surface consumes it.
		return
	}
	// Track state and lastTool BEFORE emitting, so a reader that observes the
	// event on the channel also observes the updated state.
	if ev.State != "" {
		m.state = ev.State
	}
	if ev.Approval != nil && ev.Approval.Tool != "" {
		m.lastTool = ev.Approval.Tool
	}
	m.enqueueLocked(ev)
}

// newApprovalReq builds the approval card payload for a PermissionRequest.
func newApprovalReq(reqID, tool string, raw json.RawMessage) *ApprovalReq {
	sum := tool
	if n := len(raw); n > 0 {
		if n < toolInputSummaryCutoff {
			sum += ": " + string(raw)
		} else {
			// The full input ships separately (Input, below, up to
			// MaxApprovalInputLen). So a long input is ELLIPSIZED into the
			// summary instead of dropped.
			sum += ": " + ellipsizeInput(string(raw), toolInputSummaryCutoff)
		}
	}
	input := string(raw)
	if len(input) > MaxApprovalInputLen {
		input = input[:MaxApprovalInputLen]
	}
	return &ApprovalReq{ReqID: reqID, Tool: tool, Summary: sum, Input: input,
		InputHash: approvalInputHash(tool, raw)}
}

// nonSemanticInputKeys lists, per built-in tool, tool_input keys that do
// not change what the call DOES and that the model rewrites freely between
// otherwise identical calls. They are left out of the always-rule key, so
// "always allow `npm test`" matches the next `npm test` even when the model
// words its description differently (AGT-9). Only listed built-in tools are
// stripped: for any other tool, MCP tools included, every key stays in the
// key, because there a field such as `description` can be the payload.
// Security-relevant flags such as Bash's dangerouslyDisableSandbox always
// stay.
var nonSemanticInputKeys = map[string][]string{
	"Bash":       {"description", "timeout", "run_in_background"},
	"PowerShell": {"description", "timeout", "run_in_background"},
	"WebFetch":   {"prompt"},
}

// approvalInputHash is the hex sha256 of the canonical always-rule key for
// a tool input: the FULL, untruncated input, decoded, with the
// non-semantic keys removed, and re-encoded with sorted keys. An input that
// is not a JSON object hashes verbatim.
func approvalInputHash(tool string, raw json.RawMessage) string {
	canon := []byte(raw)
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil && obj != nil {
		for _, k := range nonSemanticInputKeys[tool] {
			delete(obj, k)
		}
		// encoding/json sorts map keys, so the encoding is canonical at the
		// top level. Nested values keep their raw bytes.
		if b, err := json.Marshal(obj); err == nil {
			canon = b
		}
	}
	h := sha256.Sum256(canon)
	return hex.EncodeToString(h[:])
}

// Approve answers a parked PermissionRequest. It never blocks: the clearing
// event goes through the ordered outbox, so the app may call Approve from
// its event pump (maybeAutoApprove) without deadlocking (AGT-15, APP-15).
//
// An unknown or expired reqID (the hook was cancelled, already answered, or
// belongs to a displaced monitor) is a no-op that changes no state (AGT-16,
// APP-9). Otherwise awaiting-approval clears only when no other approval is
// still pending, and only if the state is still awaiting-approval, so a
// newer real state (done, errored) is never clobbered. Allow and deny both
// clear to StateRunning: a PermissionRequest deny is applied silently and
// the model continues its turn with the denial, until Stop reports done.
func (m *ClaudeMonitor) Approve(reqID string, d Decision) error {
	if m.listener == nil {
		return nil
	}
	m.listener.Decide(reqID, hooklistener.Decision{Allow: d.Allow, Always: d.Always})
	m.mu.Lock()
	defer m.mu.Unlock()
	// Resolve by this monitor's own record, not by Decide's result: a
	// request whose hook was cancelled a moment ago (its cancellation event
	// not yet translated) is still resolved here, exactly once.
	if _, known := m.pending[reqID]; !known {
		return nil
	}
	if m.exited {
		delete(m.pending, reqID)
		return nil
	}
	ev := m.resolveLocked(reqID)
	if ev.State != "" {
		m.state = ev.State
	}
	m.enqueueLocked(ev)
	return nil
}

// resolveLocked removes reqID from the pending set and returns the ONE
// event that reports its resolution. Every resolution (perch's verdict, or
// a cancelled hook) emits exactly one event carrying ResolvedReqID, so a
// consumer can always retract the card. When it was the last pending
// approval and the state is still awaiting-approval, the event is
// Kind "state" with StateRunning (the agent is unblocked); otherwise it is
// Kind "approval-resolved" with no state change. Caller holds m.mu and
// applies ev.State.
func (m *ClaudeMonitor) resolveLocked(reqID string) Event {
	delete(m.pending, reqID)
	if len(m.pending) == 0 && m.state == StateAwaitingApproval {
		return Event{Kind: "state", State: StateRunning, ResolvedReqID: reqID}
	}
	return Event{Kind: "approval-resolved", ResolvedReqID: reqID}
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
// ClaudeMonitor and OpencodeMonitor.
const monitorEventChanBuf = 64

// toolAskUserQuestion is the claude built-in tool the agent calls to ask
// the user a multiple-choice question. perch treats it as an attention
// SIGNAL (StateAwaitingInput) and never answers its permission.
const toolAskUserQuestion = "AskUserQuestion"

// toolExitPlanMode is the claude built-in tool that presents a plan for the
// user to accept. It is interactive like AskUserQuestion.
const toolExitPlanMode = "ExitPlanMode"

// interactiveTools are claude's tools whose permission dialog IS the user
// interaction. perch signals them (awaiting-input) and never answers them.
var interactiveTools = map[string]bool{toolAskUserQuestion: true, toolExitPlanMode: true}

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

// tokenFileMode is the file mode of the per-session settings file, which
// carries the listener's bearer token: owner read/write only.
const tokenFileMode = 0o600

// perchMonitorSentinel tags every perch hook command. Older perch versions
// wrote these hooks into the worktree's .claude/settings.json;
// cleanupLegacyWorktreeHooks uses the tag to find leftovers.
const perchMonitorSentinel = "perch-monitor-hook"

// permissionHookTimeoutSec is the PermissionRequest hook's timeout. claude's
// own dialog is shown while the hook runs, so the hook may wait as long as
// the user thinks; claude's 600 s default would retract the perch card
// under a user who stepped away (APP-16).
const permissionHookTimeoutSec = 86400

// claudeSettingsFile is the per-session settings file's base name.
const claudeSettingsFile = "settings.json"

// settingsRoots returns the candidate parents for the per-session
// settings directory, in preference order. $XDG_RUNTIME_DIR comes first: it
// is per-user, 0700, and never age-cleaned, whereas systemd-tmpfiles can
// expire a long-lived session's file under /tmp. It is a var so tests can
// redirect it.
var settingsRoots = func() []string {
	var roots []string
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" && filepath.IsAbs(d) {
		roots = append(roots, d)
	}
	return append(roots, os.TempDir(), "/tmp")
}

// settingsDirPrefix names the per-session settings directories:
// perch-claude-<pid>-<random>. The pid lets the sweep tell a crashed perch's
// leftovers from a live one's.
const settingsDirPrefix = "perch-claude-"

// staleSettingsAge is how old a settings directory without a parseable
// owner pid must be before the sweep removes it.
const staleSettingsAge = 24 * time.Hour

var sweepOnce sync.Once

// sweepStaleSettingsDirs removes per-session settings directories that a
// crashed or killed perch left behind (each holds a dead listener's token).
// It only touches directories named perch-claude-* that the current user
// owns, and only when their owning pid is dead, or, for a name without a
// pid, when they are older than staleSettingsAge. It never touches this
// process's own directories.
func sweepStaleSettingsDirs(roots []string) {
	self := os.Getpid()
	uid := os.Getuid()
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || !strings.HasPrefix(name, settingsDirPrefix) {
				continue
			}
			full := filepath.Join(root, name)
			fi, err := os.Lstat(full)
			if err != nil || !fi.IsDir() {
				continue
			}
			if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
				continue
			}
			pidStr, _, _ := strings.Cut(strings.TrimPrefix(name, settingsDirPrefix), "-")
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				if pid == self || syscall.Kill(pid, 0) != syscall.ESRCH {
					continue // ours, or its perch is still alive
				}
			} else if time.Since(fi.ModTime()) < staleSettingsAge {
				continue
			}
			_ = os.RemoveAll(full)
		}
	}
}

// buildHookSettings returns the per-session settings JSON: perch's hooks,
// pointing at this session's listener.
//
//   - PermissionRequest (all tools) blocks on perch's approval card. It fires
//     only when claude would itself ask, so claude's allow rules and
//     acceptEdits/bypassPermissions modes keep working (AGT-8).
//   - PreToolUse and PostToolUse, matched to AskUserQuestion|ExitPlanMode
//     only, raise and clear the "asking you" signal without gating any tool.
//   - Notification (idle_prompt) settles a turn the user interrupted, for
//     which Stop never fires.
//   - UserPromptSubmit, Stop, StopFailure, SessionStart drive the lifecycle
//     state (AGT-7).
func buildHookSettings(addr, token string) ([]byte, error) {
	url := "http://" + addr + "/hook"
	post := fmt.Sprintf(`curl -sf -X POST -H "Authorization: Bearer %s" -H "Content-Type: application/json" --data-binary @- %s`, token, url)
	// Non-blocking hooks discard stdout: for SessionStart and
	// UserPromptSubmit claude would add it to the model's context.
	signal := post + " >/dev/null 2>&1 || true # " + perchMonitorSentinel
	blocking := post + " || true # " + perchMonitorSentinel
	group := func(matcher *string, cmd string, timeout int) map[string]any {
		h := map[string]any{"type": "command", "command": cmd}
		if timeout > 0 {
			h["timeout"] = timeout
		}
		g := map[string]any{"hooks": []any{h}}
		if matcher != nil {
			g["matcher"] = *matcher
		}
		return g
	}
	all, ask, idle := "", toolAskUserQuestion+"|"+toolExitPlanMode, "idle_prompt"
	hooks := map[string]any{
		hooklistener.EventPermissionRequest: []any{group(&all, blocking, permissionHookTimeoutSec)},
		"PreToolUse":                        []any{group(&ask, signal, 0)},
		"PostToolUse":                       []any{group(&ask, signal, 0)},
		"Notification":                      []any{group(&idle, signal, 0)},
		"UserPromptSubmit":                  []any{group(nil, signal, 0)},
		"Stop":                              []any{group(nil, signal, 0)},
		"StopFailure":                       []any{group(nil, signal, 0)},
		"SessionStart":                      []any{group(&all, signal, 0)},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"hooks": hooks}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

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
	cleanupLegacyWorktreeHooks(cwd)
	if err := m.writeSettings(); err != nil {
		return "", fmt.Errorf("ClaudeMonitor.Prepare: settings: %w", err)
	}
	args := []string{launchBin(m.adapter, "claude"), "--settings", `"` + m.settingsPath + `"`}
	if m.adapter != nil {
		if resumeID != "" {
			args = append(args, m.adapter.ResumeArgs(resumeID)...)
		} else {
			args = append(args, m.adapter.NewArgs()...)
		}
	}
	// perch writes the returned command verbatim into the pane's pty (see
	// pty.Bridge.Write, a raw passthrough). exitSentinel is appended, so that
	// when claude exits, gracefully or via SIGKILL or OOM, the shell pings the
	// listener with the captured exit code. PaneEnv() supplies the token and
	// URL that exitSentinel references by name. The trailing newline (added
	// by wrapForLoginShell) is what submits the command to the shell.
	return wrapForLoginShell(strings.Join(args, " ") + exitSentinel), nil
}

// writeSettings creates the private per-session directory and settings
// file. A second call (a re-Prepare) replaces the previous one.
func (m *ClaudeMonitor) writeSettings() error {
	data, err := buildHookSettings(m.listener.Addr(), m.listener.Token())
	if err != nil {
		return err
	}
	m.removeSettings()
	roots := settingsRoots()
	sweepOnce.Do(func() { sweepStaleSettingsDirs(roots) })
	var dir string
	for _, root := range roots {
		d, err := os.MkdirTemp(root, settingsDirPrefix+strconv.Itoa(os.Getpid())+"-") // 0700
		if err != nil {
			continue
		}
		if shellSafeWord(d) {
			dir = d
			break
		}
		// The path would need escaping in the typed launch line; try the
		// next root instead.
		_ = os.RemoveAll(d)
	}
	if dir == "" {
		return errors.New("no usable private temp directory for the claude settings file")
	}
	path := filepath.Join(dir, claudeSettingsFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, tokenFileMode)
	if err == nil {
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	m.settingsDir, m.settingsPath = dir, path
	return nil
}

func (m *ClaudeMonitor) removeSettings() {
	if m.settingsDir != "" {
		_ = os.RemoveAll(m.settingsDir)
		m.settingsDir, m.settingsPath = "", ""
	}
}

// PaneEnv supplies the exit sentinel's token and URL through the pane
// shell's process environment, so the launch line can reference them by
// name instead of inlining the bearer token, which the interactive shell
// would echo on screen. Call PaneEnv after Prepare, which creates
// m.listener. claude reuses its existing hook listener.
func (m *ClaudeMonitor) PaneEnv() []string {
	return exitPaneEnv(m.listener)
}

// cleanupLegacyWorktreeHooks removes a `.claude/settings.json` that an
// older perch version left in the worktree, but ONLY when the file holds
// nothing except perch's own hook groups or nothing at all (`{}`, or
// `{"hooks": {}}`, which is what the old Teardown left behind), and ONLY
// when git does not track it: deleting a tracked file would itself dirty
// the tree. It then also removes `.claude/` if that is left empty. A file
// with any user content is never touched: rewriting it would reformat the
// user's file, and a stale perch group in it only makes a failing,
// non-blocking curl.
func cleanupLegacyWorktreeHooks(cwd string) {
	if cwd == "" {
		return
	}
	dir := filepath.Join(cwd, ".claude")
	path := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil || !onlyPerchHooks(data) {
		return
	}
	if gitTracksFile(cwd, filepath.Join(".claude", "settings.json")) {
		return
	}
	if os.Remove(path) == nil {
		_ = os.Remove(dir) // fails, harmlessly, unless the directory is empty
	}
}

// gitTracksFile reports whether git tracks rel inside cwd. It answers false
// only when git positively says the file is untracked (exit 1) or cwd is
// not a repository (exit 128); when git is missing or fails otherwise it
// answers true, so the caller leaves the file alone. It is a var so tests
// can stub it.
var gitTracksFile = func(cwd, rel string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "ls-files", "--error-unmatch", "--", rel)
	err := cmd.Run()
	if err == nil {
		return true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && (ee.ExitCode() == 1 || ee.ExitCode() == 128) {
		return false
	}
	return true
}

// onlyPerchHooks reports whether a settings document holds nothing but
// perch hook groups (or an empty hooks object).
func onlyPerchHooks(data []byte) bool {
	var s map[string]any
	if json.Unmarshal(data, &s) != nil {
		return false
	}
	for k, v := range s {
		if k != "hooks" {
			return false
		}
		hm, ok := v.(map[string]any)
		if !ok {
			return false
		}
		for _, groups := range hm {
			arr, ok := groups.([]any)
			if !ok {
				return false
			}
			for _, item := range arr {
				g, _ := item.(map[string]any)
				if !isPerchMonitorGroup(g) {
					return false
				}
			}
		}
	}
	return true
}

// isPerchMonitorGroup reports whether every hook in the group g is a perch
// hook.
func isPerchMonitorGroup(g map[string]any) bool {
	hs, _ := g["hooks"].([]any)
	if len(hs) == 0 {
		return false
	}
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		cmd, _ := hm["command"].(string)
		if !strings.Contains(cmd, perchMonitorSentinel) {
			return false
		}
	}
	return true
}

// Teardown removes the per-session settings directory and closes the
// listener this monitor owns.
func (m *ClaudeMonitor) Teardown() error {
	m.removeSettings()
	var closeErr error
	if m.ownedLn && m.listener != nil {
		closeErr = m.listener.Close()
	}
	return closeErr
}
