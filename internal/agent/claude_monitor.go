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

	"github.com/Miniature-Pug/perch/internal/hooklistener"
	"github.com/Miniature-Pug/perch/internal/safe"
)

type ClaudeMonitor struct {
	adapter  Adapter
	listener *hooklistener.Listener
	ownedLn  bool // true when we created listener; Teardown closes it
	events   chan Event
	cwd      string

	mu       sync.Mutex
	state    State
	lastTool string
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
		// Stop means the agent finished its turn, so emit StateDone and let
		// dispatchNotify fire the "Turn complete" ambient toast.
		ev = Event{Kind: "state", State: StateDone}
	case "StopFailure":
		ev = Event{Kind: "state", State: StateErrored, Err: he.ErrorType}
	// NOTE: "Notification" is intentionally absent. perch hooks exactly four
	// events: PreToolUse, Stop, StopFailure, and SessionStart. "Notification"
	// is not among them and is NOT installed in perchMonitorEvents, so the hook
	// never fires and the case would be unreachable. There is no UI consumer for
	// a Claude attention-notification signal. Omitting the dead case keeps the
	// code honest.
	case "PreToolUse":
		if he.ToolName == toolAskUserQuestion {
			// AskUserQuestion is the agent asking the USER to choose, not a request
			// to act on the system — so it is an attention SIGNAL, not an approval.
			// Surface StateAwaitingInput (the "question" sidebar feel + a blocking
			// notify) and auto-allow the hook immediately so claude proceeds to
			// render the question in its own pane TUI, where the user answers.
			// Gating it behind perch's approval card would double-prompt and need-
			// lessly block the agent; auto-allow is safe because the tool has no
			// system side effect to approve. ExitPlanMode is deliberately NOT
			// treated this way — auto-allowing it would skip the user's plan review,
			// so it stays on the normal approval path below.
			m.listener.Decide(he.ReqID, hooklistener.Decision{Allow: true})
			ev = Event{Kind: "question", State: StateAwaitingInput}
		} else {
			sum := he.ToolName
			if len(he.ToolInput) > 0 && len(he.ToolInput) < toolInputSummaryCutoff {
				sum += ": " + string(he.ToolInput)
			}
			fullInput := string(he.ToolInput)
			// Compute hash of the FULL (untruncated) input before truncation so
			// two inputs sharing a 4096-byte prefix produce distinct hashes.
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
	// Track state/lastTool (mutex-guarded) BEFORE emitting, so a reader that
	// observes the event on the channel also observes the updated state.
	m.mu.Lock()
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
	// The decision unblocks the hook handler and the agent resumes. Clear the amber
	// awaiting-approval indicator by advancing the cached state and emitting a
	// clearing event — but ONLY if we are still awaiting approval. A newer real
	// state (StateDone/StateErrored) can arrive on the SSE/hook stream before the
	// user's decision lands (e.g. the agent's turn ended while the card sat open);
	// clobbering it with StateRunning would show a stale "running" feel. The chosen
	// cleared state depends on the decision: allow/always → StateRunning (the tool
	// proceeds), deny → StateIdle (the agent may stop). The mutex is the same one
	// translateAndEmit uses, so m.state stays consistent; we update it regardless of
	// whether the buffered channel accepts the frame, so CurrentState() is always
	// correct even if the send is dropped.
	cleared := StateRunning
	if !d.Allow {
		cleared = StateIdle
	}
	m.mu.Lock()
	if m.state != StateAwaitingApproval {
		// A newer real state already landed — do not clobber it or emit.
		m.mu.Unlock()
		return nil
	}
	m.state = cleared
	m.mu.Unlock()
	select {
	case m.events <- Event{Kind: "state", State: cleared}:
	default:
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

// monitorEventChanBuf is the shared event-channel buffer size used by both
// ClaudeMonitor and OpencodeMonitor. Sized to absorb bursts without blocking
// the emitter goroutine.
const monitorEventChanBuf = 64

// toolAskUserQuestion is the claude built-in tool the agent calls to ask the
// user a multiple-choice question. perch treats its PreToolUse as an attention
// SIGNAL (StateAwaitingInput) and auto-allows it so claude renders the question
// in its own pane TUI — see the PreToolUse case in translateAndEmit.
const toolAskUserQuestion = "AskUserQuestion"

// toolInputSummaryCutoff is the maximum raw ToolInput byte length that is
// included verbatim in the approval-event Summary. Inputs at or above this
// threshold are omitted from the summary (the full input is still hashed).
const toolInputSummaryCutoff = 120

// claudeMonitorDirMode is the directory mode used when creating .claude/.
const claudeMonitorDirMode = 0o755

// tokenFileMode is the file mode enforced on any temp file that carries a
// Bearer token. 0600 = owner read/write only; no group/world exposure.
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
	// The returned command is written verbatim into the pane's pty (see
	// pty.Bridge.Write — raw passthrough, no transformation). The trailing
	// newline is what actually submits it to the shell; without it the launch
	// command sits on the prompt unexecuted and the agent never starts.
	return strings.Join(append([]string{m.adapter.Name()}, args...), " ") + "\n", nil
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

// mergeMonitorHooks is pure (no I/O). It uses the same additive-append+sentinel
// idiom as mergeClaudeHooks in claude.go: each perch group has a command
// containing perchMonitorSentinel so it can be identified on Teardown.
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

// atomicWrite writes data to path atomically via a temp file.
// The file is always created with mode 0600 (owner read/write only) because
// settings.json embeds a Bearer token; preserving a pre-existing looser mode
// would expose the token to other local users.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// Set 0600 on the temp file before writing so there is no window where the
	// token-bearing content is readable by group/world. os.CreateTemp already
	// uses 0600, but we set it explicitly to document the invariant.
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
