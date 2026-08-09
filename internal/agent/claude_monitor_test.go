// internal/agent/claude_monitor_test.go
package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
	"github.com/Miniature-Pug/perch/internal/pty"
)

// newMonitorWithTestListener creates a ClaudeMonitor backed by a real in-process
// hooklistener. Caller defers cleanup().
func newMonitorWithTestListener(t *testing.T) (*agent.ClaudeMonitor, *hooklistener.Listener, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	m := agent.NewClaudeMonitorWithListener(agent.NewClaude(), l)
	return m, l, func() { _ = l.Close() }
}

func TestClaudeMonitorPrepare(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo")
	_ = os.MkdirAll(worktree, 0o755)

	// Pre-seed a foreign Stop hook so we can assert it survives.
	claudeDir := filepath.Join(worktree, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	foreign := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"foreign-tool notify"}]}]}}`
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(foreign), 0o644)

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasPrefix(cmd, "claude") {
		t.Errorf("unexpected cmd: %q", cmd)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var s map[string]any
	_ = json.Unmarshal(data, &s)
	hooks, _ := s["hooks"].(map[string]any)
	for _, ev := range []string{"PreToolUse", "Stop", "StopFailure", "SessionStart"} {
		arr, _ := hooks[ev].([]any)
		if len(arr) == 0 {
			t.Errorf("hooks[%q] missing after Prepare", ev)
		}
	}
	// Foreign Stop hook must still be present.
	stopArr, _ := hooks["Stop"].([]any)
	found := false
	for _, item := range stopArr {
		g, _ := item.(map[string]any)
		hs, _ := g["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if strings.Contains(fmt.Sprint(hm["command"]), "foreign-tool") {
				found = true
			}
		}
	}
	if !found {
		t.Error("foreign Stop hook was removed by Prepare")
	}

	// Teardown must remove perch hooks but leave foreign ones.
	_ = m.Teardown()
	data2, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var s2 map[string]any
	_ = json.Unmarshal(data2, &s2)
	hooks2, _ := s2["hooks"].(map[string]any)
	stopArr2, _ := hooks2["Stop"].([]any)
	foreignStillThere := false
	for _, item := range stopArr2 {
		g, _ := item.(map[string]any)
		hs, _ := g["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if strings.Contains(fmt.Sprint(hm["command"]), "foreign-tool") {
				foreignStillThere = true
			}
			if strings.Contains(fmt.Sprint(hm["command"]), "perch-monitor-hook") {
				t.Error("perch hook survived Teardown")
			}
		}
	}
	if !foreignStillThere {
		t.Error("foreign Stop hook missing after Teardown")
	}
}

func TestClaudeMonitorSettingsFileMode(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo-mode")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	// No pre-existing .claude/settings.json → Prepare creates it fresh.
	if _, err := m.Prepare(context.Background(), "wsM", worktree, ""); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	fi, err := os.Stat(filepath.Join(worktree, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings.json mode = %o, want 600 (carries bearer token)", perm)
	}
}

// TestClaudeMonitorSettingsFileModePreExisting asserts that Prepare forces the
// settings.json to 0600 even when a pre-existing file is world-readable (0644).
// The Bearer token is the sole defence against other local users; it must not
// be leaked via a permissive file mode, regardless of what mode the file had
// before Prepare ran.
func TestClaudeMonitorSettingsFileModePreExisting(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo-mode-preexisting")
	claudeDir := filepath.Join(worktree, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	// Create a settings.json at 0644, then chmod explicitly to defeat umask.
	if err := os.WriteFile(settingsPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(settingsPath, 0o644); err != nil {
		t.Fatal(err)
	}
	// Verify the pre-condition: file really is 0644 before Prepare.
	fi, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("pre-condition stat: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("pre-condition: expected 0644, got %o", fi.Mode().Perm())
	}

	// Run Prepare — must force the file down to 0600.
	if _, err := m.Prepare(context.Background(), "wsMPE", worktree, ""); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	fi2, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("post-Prepare stat: %v", err)
	}
	if perm := fi2.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings.json mode = %o, want 600 (pre-existing 0644 must be forced down; carries bearer token)", perm)
	}
}

func TestClaudeMonitorEventTranslation(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	post := func(payload string) {
		req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+l.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
	}
	post(`{"hook_event_name":"SessionStart","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)
	post(`{"hook_event_name":"Stop","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 2 {
		select {
		case ev := <-m.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d events", len(got))
		}
	}
	if got[0].Kind != "state" || got[0].State != agent.StateRunning {
		t.Errorf("ev[0]: %+v", got[0])
	}
	if got[1].Kind != "state" || got[1].State != agent.StateDone {
		t.Errorf("ev[1]: %+v", got[1])
	}

	// State tracking: after the Stop event drained, CurrentState reflects done.
	// (translateAndEmit sets m.state BEFORE the channel send, so this is race-free.)
	if m.CurrentState() != agent.StateDone {
		t.Errorf("CurrentState after Stop = %q, want %q", m.CurrentState(), agent.StateDone)
	}
}

// TestClaudeMonitorStopFailureErrored asserts the StopFailure hook event
// translates to Event{Kind:"state", State:StateErrored, Err:<error_type>} — the
// blocking "Agent error" path dispatchNotify keys off. The error_type payload
// field must surface verbatim in Event.Err.
func TestClaudeMonitorStopFailureErrored(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook",
		strings.NewReader(`{"hook_event_name":"StopFailure","session_id":"sid-E","error_type":"context_limit"}`))
	req.Header.Set("Authorization", "Bearer "+l.Token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case ev := <-m.Events():
		if ev.Kind != "state" || ev.State != agent.StateErrored {
			t.Fatalf("StopFailure must emit state/StateErrored; got %+v", ev)
		}
		if ev.Err != "context_limit" {
			t.Errorf("Err = %q, want %q (the error_type)", ev.Err, "context_limit")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for StopFailure event")
	}
}

// postHook POSTs a hook payload to the listener's /hook and returns the response
// body string. PreToolUse blocks in the handler until Decide() is called, so
// callers that POST a PreToolUse normally run this in a goroutine.
func postHook(t *testing.T, l *hooklistener.Listener, payload string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
	if err != nil {
		t.Errorf("new request: %v", err)
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+l.Token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("post hook: %v", err)
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestClaudeMonitorAskUserQuestion_AutoAllow is the CRITICAL guard for the
// question signal: a PreToolUse for AskUserQuestion must NOT raise an approval
// card and must NOT block the agent. The monitor itself auto-allows the hook
// (calls Decide internally) BEFORE the test ever touches Approve/Decide, so:
//   - the emitted Event is Kind=="question"/StateAwaitingInput with a NIL Approval
//   - the /hook POST returns "permissionDecision":"allow" on its own
//
// The POST runs in a goroutine guarded by a result channel + timeout: if the
// source ever stops auto-allowing, the handler hangs on <-p.ch forever and this
// test FAILS (timeout) rather than passing against a mock. Mocks cannot prove
// the real binary emits AskUserQuestion's PreToolUse — that is the manual smoke
// step — but this proves the auto-allow translation contract end to end.
func TestClaudeMonitorAskUserQuestion_AutoAllow(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"question":"pick one"},"session_id":"s","cwd":"/p"}`)
	}()

	// The monitor must emit the question signal.
	var ev agent.Event
	select {
	case ev = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for question event")
	}
	if ev.Kind != "question" {
		t.Errorf("Kind = %q, want question", ev.Kind)
	}
	if ev.State != agent.StateAwaitingInput {
		t.Errorf("State = %q, want awaiting-input", ev.State)
	}
	if ev.Approval != nil {
		t.Errorf("question must carry NO approval (signal, not card), got %+v", ev.Approval)
	}

	// The POST must complete on its OWN — the test never calls Approve/Decide.
	// If the source did not auto-allow, the handler is still blocked on <-p.ch and
	// this select times out.
	select {
	case body := <-respCh:
		if !strings.Contains(body, `"permissionDecision":"allow"`) {
			t.Errorf("hook response = %q, want it to contain permissionDecision allow", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AskUserQuestion PreToolUse did not auto-allow: /hook POST never returned " +
			"(monitor failed to Decide internally — the agent would be blocked)")
	}
}

// TestClaudeMonitorPreToolUse_NonQuestionBlocksUntilDecide is the regression
// counterpart: a NON-question PreToolUse (Write) must take the approval path —
// Kind=="approval"/StateAwaitingApproval — and BLOCK until Decide supplies a
// verdict. It must NOT auto-allow. The POST goroutine stays pending until the
// test calls Approve; we assert it is still pending before, then completes after.
func TestClaudeMonitorPreToolUse_NonQuestionBlocksUntilDecide(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
	}()

	var ev agent.Event
	select {
	case ev = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for approval event")
	}
	if ev.Kind != "approval" {
		t.Fatalf("Kind = %q, want approval", ev.Kind)
	}
	if ev.State != agent.StateAwaitingApproval {
		t.Errorf("State = %q, want awaiting-approval", ev.State)
	}
	if ev.Approval == nil || ev.Approval.Tool != "Write" {
		t.Fatalf("Approval: want Tool=Write, got %+v", ev.Approval)
	}

	// The handler must STILL be blocked — no auto-allow for a non-question tool.
	select {
	case body := <-respCh:
		t.Fatalf("Write PreToolUse must block until Decide, but the POST returned early: %q", body)
	case <-time.After(150 * time.Millisecond):
		// expected: still pending
	}

	// Now supply the verdict; the POST must complete with allow.
	if err := m.Approve(ev.Approval.ReqID, agent.Decision{Allow: true}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	select {
	case body := <-respCh:
		if !strings.Contains(body, `"permissionDecision":"allow"`) {
			t.Errorf("after Approve, hook response = %q, want permissionDecision allow", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("POST never completed after Approve")
	}
}

// TestClaudeMonitorApprovalSummary_EllipsizesLongInput is the regression guard for
// the all-or-nothing summary: a tool input at/above the summary cutoff must be
// ELLIPSIZED into the approval Summary (tool name + ": " + first bytes + "…"), not
// dropped, so the card conveys what the agent wants to run rather than showing the
// bare tool name. The full untruncated input must still ship separately in Input.
func TestClaudeMonitorApprovalSummary_EllipsizesLongInput(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// A tool_input whose raw JSON is well over the 120-byte summary cutoff.
	bigVal := strings.Repeat("x", 200)
	payload := fmt.Sprintf(
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":%q},"session_id":"s","cwd":"/p"}`,
		bigVal)

	respCh := make(chan string, 1)
	go func() { respCh <- postHook(t, l, payload) }()

	var ev agent.Event
	select {
	case ev = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for approval event")
	}
	if ev.Approval == nil {
		t.Fatalf("want approval event with payload, got %+v", ev)
	}
	sum := ev.Approval.Summary

	// Must NOT be the bare tool name (the old drop behavior) and must carry the
	// "<tool>: " prefix with ellipsized content.
	if sum == "Bash" {
		t.Fatalf("summary was dropped to the bare tool name; want an ellipsized input summary")
	}
	if !strings.HasPrefix(sum, "Bash: ") {
		t.Errorf("summary missing the \"Bash: \" prefix; got %q", sum)
	}
	if !strings.HasSuffix(sum, "…") {
		t.Errorf("long input must be ellipsized (end with …); got %q", sum)
	}
	// Bounded: the 200-byte input must be truncated, not shipped whole in the summary.
	if len(sum) >= len(bigVal) {
		t.Errorf("summary not truncated (%d bytes); want it bounded near the cutoff: %q", len(sum), sum)
	}
	// The full, untruncated input must still be available on Input for the card.
	if !strings.Contains(ev.Approval.Input, bigVal) {
		t.Errorf("full input must still ship in Approval.Input; got %q", ev.Approval.Input)
	}

	// Unblock the hook POST goroutine so it does not leak.
	if err := m.Approve(ev.Approval.ReqID, agent.Decision{Allow: true}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	select {
	case <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatal("hook POST never returned after Approve")
	}
}

// TestClaudeMonitorApprove_ClearsAttention is the regression guard for the stuck
// sidebar attention signal: after a non-question PreToolUse raises an approval
// (StateAwaitingApproval) and the user ALLOWS it via Approve, the monitor MUST
// emit a Kind=="state"/StateRunning event (the tool proceeds) so the frontend's
// last-event-wins per-workspace state clears the amber awaiting-approval
// indicator, AND CurrentState() must report StateRunning. Before the fix, Approve
// only unblocked the hook handler and emitted nothing, so the indicator stayed
// stuck forever. (Deny → StateIdle is covered by TestClaudeMonitorApprove_Deny_ClearsToIdle.)
func TestClaudeMonitorApprove_ClearsAttention(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
	}()

	var ev agent.Event
	select {
	case ev = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for approval event")
	}
	if ev.Kind != "approval" || ev.State != agent.StateAwaitingApproval || ev.Approval == nil {
		t.Fatalf("want awaiting-approval event, got %+v", ev)
	}
	if m.CurrentState() != agent.StateAwaitingApproval {
		t.Fatalf("pre-condition: CurrentState = %q, want awaiting-approval", m.CurrentState())
	}

	if err := m.Approve(ev.Approval.ReqID, agent.Decision{Allow: true}); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Approve must emit a StateRunning event to clear the attention indicator.
	select {
	case resolved := <-m.Events():
		if resolved.Kind != "state" || resolved.State != agent.StateRunning {
			t.Errorf("after Approve, want Kind=state/StateRunning, got %+v", resolved)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Approve emitted no event: the sidebar attention indicator would stay stuck")
	}

	if m.CurrentState() != agent.StateRunning {
		t.Errorf("CurrentState after Approve = %q, want %q", m.CurrentState(), agent.StateRunning)
	}

	// Drain the hook POST so the goroutine does not leak.
	select {
	case <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatal("hook POST never returned after Approve")
	}
}

// TestClaudeMonitorApprove_Deny_ClearsToIdle asserts that a DENY decision clears
// the awaiting-approval attention signal to StateIdle (the agent may stop) — not
// StateRunning. Denying a tool does not resume work, so surfacing "running" would
// be wrong.
func TestClaudeMonitorApprove_Deny_ClearsToIdle(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
	}()

	var ev agent.Event
	select {
	case ev = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for approval event")
	}
	if ev.Kind != "approval" || ev.Approval == nil {
		t.Fatalf("want approval event, got %+v", ev)
	}

	if err := m.Approve(ev.Approval.ReqID, agent.Decision{Allow: false}); err != nil {
		t.Fatalf("Approve(deny): %v", err)
	}

	select {
	case resolved := <-m.Events():
		if resolved.Kind != "state" || resolved.State != agent.StateIdle {
			t.Errorf("after deny, want Kind=state/StateIdle, got %+v", resolved)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deny emitted no clearing event")
	}
	if m.CurrentState() != agent.StateIdle {
		t.Errorf("CurrentState after deny = %q, want %q", m.CurrentState(), agent.StateIdle)
	}

	select {
	case <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatal("hook POST never returned after deny")
	}
}

// TestClaudeMonitorApprove_DoesNotClobberNewerState guards the race where a newer
// real state (StateDone: the agent's turn ended) arrives on the hook stream BEFORE
// the user's decision lands. Approve must NOT clobber Done with running/idle and
// must NOT emit a clearing event — the amber indicator is already gone (state
// advanced past awaiting-approval), and forcing "running" would show a stale feel.
func TestClaudeMonitorApprove_DoesNotClobberNewerState(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// Raise an approval (blocks in a goroutine until we decide).
	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
	}()

	var appr agent.Event
	select {
	case appr = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for approval event")
	}
	if appr.Kind != "approval" || appr.Approval == nil {
		t.Fatalf("want approval event, got %+v", appr)
	}

	// A Stop arrives FIRST: the agent's turn ended while the approval card sat open.
	// This advances m.state to StateDone (Stop is a non-blocking hook event).
	postHook(t, l, `{"hook_event_name":"Stop","session_id":"s","transcript_path":"/t","cwd":"/p"}`)
	select {
	case doneEv := <-m.Events():
		if doneEv.State != agent.StateDone {
			t.Fatalf("expected StateDone from Stop, got %+v", doneEv)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Stop→Done event")
	}
	if m.CurrentState() != agent.StateDone {
		t.Fatalf("pre-condition: CurrentState = %q, want done", m.CurrentState())
	}

	// Now the user's decision lands. It must NOT emit and must NOT clobber Done.
	if err := m.Approve(appr.Approval.ReqID, agent.Decision{Allow: true}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	select {
	case ev := <-m.Events():
		t.Fatalf("Approve emitted an event after state advanced to Done — clobbered newer state: %+v", ev)
	case <-time.After(300 * time.Millisecond):
		// no event — correct
	}
	if m.CurrentState() != agent.StateDone {
		t.Errorf("CurrentState after Approve = %q, want %q (Done must not be clobbered)", m.CurrentState(), agent.StateDone)
	}

	select {
	case <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatal("hook POST never returned after Approve")
	}
}

// TestClaudeMonitorApprove_NotDroppedUnderBackpressure is the regression guard for
// the stuck awaiting-approval signal: when the monitor's events channel is
// momentarily full, Approve must NOT silently drop the state-clearing event. The
// old code emitted the clearing frame with `select { case ...: default: }`, so a
// full channel meant the frontend stayed stuck on awaiting-approval forever. The
// fix blocks (cancellable) until a slot frees, so the clearing event is always
// delivered. This test fills the channel to capacity, calls Approve on an
// awaiting-approval monitor, then drains the channel and asserts the StateRunning
// clearing event eventually arrives. It goes RED against the `default:` version
// (the clearing frame is lost) and GREEN after.
func TestClaudeMonitorApprove_NotDroppedUnderBackpressure(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// Raise an approval so m.state == StateAwaitingApproval.
	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
	}()

	var appr agent.Event
	select {
	case appr = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for approval event")
	}
	if appr.Kind != "approval" || appr.Approval == nil {
		t.Fatalf("want approval event, got %+v", appr)
	}
	if m.CurrentState() != agent.StateAwaitingApproval {
		t.Fatalf("pre-condition: CurrentState = %q, want awaiting-approval", m.CurrentState())
	}

	// Saturate the events channel so any non-blocking send would be dropped.
	m.FillEvents(m.EventsCap())

	// Approve blocks on the full channel until a slot frees — run it in a goroutine
	// and record when it returns.
	approveDone := make(chan error, 1)
	go func() {
		approveDone <- m.Approve(appr.Approval.ReqID, agent.Decision{Allow: true})
	}()

	// While the channel is full, Approve must not have returned (it is blocked on
	// the reliable send, not dropping the frame).
	select {
	case <-approveDone:
		t.Fatal("Approve returned while channel was full — it dropped the clearing event instead of blocking")
	case <-time.After(150 * time.Millisecond):
		// expected: still blocked on the send.
	}

	// Drain the channel. The clearing StateRunning frame must appear among the
	// drained events — it was not silently lost.
	deadline := time.After(3 * time.Second)
	sawClearing := false
	for !sawClearing {
		select {
		case ev := <-m.Events():
			if ev.Kind == "state" && ev.State == agent.StateRunning {
				sawClearing = true
			}
		case <-deadline:
			t.Fatal("clearing StateRunning event never delivered — Approve dropped it under backpressure")
		}
	}

	select {
	case err := <-approveDone:
		if err != nil {
			t.Fatalf("Approve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Approve never returned after the clearing event was delivered")
	}
	if m.CurrentState() != agent.StateRunning {
		t.Errorf("CurrentState after Approve = %q, want %q", m.CurrentState(), agent.StateRunning)
	}

	// Drain the hook POST so the goroutine does not leak.
	select {
	case <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatal("hook POST never returned after Approve")
	}
}

// TestClaudeMonitorPrepare_LaunchCommandSubmitsToShell is the falsifying guard
// for the core agent-launch loop. The string Prepare() returns is written
// VERBATIM into the pane's pty (app.OpenWorkspace → pty.Bridge.Write, a raw
// passthrough), and a shell only runs a line once it is terminated by a
// newline. Earlier code returned the launch command without a trailing "\n",
// so the agent never started — a bug invisible to every mock-bounded test
// because they assert the returned string, not that a shell executes it.
//
// This test exercises Prepare()'s REAL output through a REAL /bin/sh: a fake
// `claude` on PATH prints a sentinel, and we assert the command actually runs.
// It regresses the instant the submitting newline is dropped from Prepare().
func TestClaudeMonitorPrepare_LaunchCommandSubmitsToShell(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	// Fake `claude` on PATH that prints a sentinel. Prepare()'s command name is
	// the literal "claude" (Adapter.Name()), so a real shell resolving and
	// running it via PATH is exactly the production path minus the real binary.
	binDir := t.TempDir()
	const sentinel = "PERCH_SUBMIT_OK"
	script := "#!/bin/sh\nprintf '" + sentinel + "\\n'\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cwd := t.TempDir()
	cmd, err := m.Prepare(context.Background(), "ws-submit", cwd, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasSuffix(cmd, "\n") {
		t.Fatalf("Prepare() command %q lacks the trailing newline that submits it to the shell", cmd)
	}

	var mu sync.Mutex
	var out []byte
	emit := func(event string, data ...any) {
		if event != "data" || len(data) != 1 {
			return
		}
		if chunk, ok := data[0].([]int); ok {
			mu.Lock()
			for _, v := range chunk {
				out = append(out, byte(v))
			}
			mu.Unlock()
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, nil, "data", "exit", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = br.Close() }()

	if _, err := br.Write([]byte(cmd)); err != nil {
		t.Fatalf("Write launch command: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := strings.Contains(string(out), sentinel)
		mu.Unlock()
		if seen {
			return // command executed: the launch loop's real contract holds
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := string(out)
	mu.Unlock()
	t.Errorf("launch command never executed in the shell: sentinel %q absent from pty output %q "+
		"(command written but not submitted — missing trailing newline?)", sentinel, got)
}

// TestClaudeMonitorAgentExit_EmitsStateExited is the F32 core for claude: the shell
// exit sentinel POSTs an AgentExit (carrying the captured $?) to the SAME loopback
// listener as the other lifecycle hooks; the monitor must translate it to a terminal
// StateExited (distinct from StateErrored) so a dead agent stops reading "running".
// CurrentState must also flip so the state survives a webview reload.
func TestClaudeMonitorAgentExit_EmitsStateExited(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// 137 = 128+SIGKILL, what bash reports for an OOM-killed foreground child.
	_ = postHook(t, l, `{"hook_event_name":"AgentExit","error_type":"137"}`)

	select {
	case ev := <-m.Events():
		if ev.Kind != "state" || ev.State != agent.StateExited {
			t.Fatalf("AgentExit must emit state/StateExited; got %+v", ev)
		}
		if ev.Err != "exited (code 137)" {
			t.Errorf("Err = %q, want %q", ev.Err, "exited (code 137)")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for AgentExit event")
	}
	if m.CurrentState() != agent.StateExited {
		t.Errorf("CurrentState after AgentExit = %q, want exited", m.CurrentState())
	}
}

// TestClaudeMonitorAgentExit_StateExitedIsTerminal is the symmetric F32 guard for
// claude: once AgentExit has been translated to StateExited, a straggler hook must
// NOT clobber it. claude fires no hook after its process is dead, so AgentExit is
// normally the last event — but the exit sentinel's AgentExit curl and a
// fire-and-forget Stop curl are two independent loopback POSTs that can be
// serialized onto the listener out of order. This posts AgentExit, drains the
// terminal StateExited, THEN posts a Stop and asserts (a) no StateDone/running event
// follows and (b) CurrentState stays StateExited. Against the un-fixed monitor (no
// exited field/guard) the Stop translates to StateDone and this FAILS.
func TestClaudeMonitorAgentExit_StateExitedIsTerminal(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// AgentExit → terminal StateExited (drain it so exited is armed before the Stop).
	_ = postHook(t, l, `{"hook_event_name":"AgentExit","error_type":"0"}`)
	select {
	case ev := <-m.Events():
		if ev.Kind != "state" || ev.State != agent.StateExited {
			t.Fatalf("AgentExit must emit state/StateExited; got %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for AgentExit event")
	}
	if m.CurrentState() != agent.StateExited {
		t.Fatalf("pre-condition: CurrentState = %q, want exited", m.CurrentState())
	}

	// A straggler Stop (a fire-and-forget curl serialized after AgentExit) MUST be
	// dropped: StateExited is terminal, so no StateDone event may follow.
	_ = postHook(t, l, `{"hook_event_name":"Stop","session_id":"s","transcript_path":"/t","cwd":"/p"}`)
	select {
	case ev := <-m.Events():
		t.Fatalf("exited guard failed: a post-exit Stop was emitted: %+v", ev)
	case <-time.After(300 * time.Millisecond):
		// good — no event
	}
	if m.CurrentState() != agent.StateExited {
		t.Errorf("StateExited was clobbered by a straggler Stop: %q", m.CurrentState())
	}
}

// TestClaudeMonitorPrepare_ExitSentinelUsesEnvNotLiteralToken proves the launch line
// carries the exit sentinel referencing PERCH_EXIT_TOKEN/PERCH_EXIT_URL BY NAME —
// and NOT the literal bearer token, which the interactive shell would echo on-screen
// (a new secret exposure for claude, whose launch line carries no secret today). The
// token/URL travel via PaneEnv (the process environment), which is never echoed.
func TestClaudeMonitorPrepare_ExitSentinelUsesEnvNotLiteralToken(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo")
	_ = os.MkdirAll(worktree, 0o755)

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, want := range []string{"; ec=$?", "curl", "$PERCH_EXIT_TOKEN", "$PERCH_EXIT_URL", "AgentExit"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("launch cmd missing %q; got %q", want, cmd)
		}
	}
	if strings.Contains(cmd, l.Token()) {
		t.Errorf("launch cmd LEAKS the literal bearer token (would be echoed on-screen): %q", cmd)
	}

	env := m.PaneEnv()
	if !envSliceHas(env, "PERCH_EXIT_TOKEN="+l.Token()) {
		t.Errorf("PaneEnv missing PERCH_EXIT_TOKEN=<token>; got %v", env)
	}
	if !envSliceHas(env, "PERCH_EXIT_URL=http://"+l.Addr()+"/hook") {
		t.Errorf("PaneEnv missing PERCH_EXIT_URL=<addr>; got %v", env)
	}
}

// envSliceHas reports whether env contains an exact KEY=VALUE entry.
func envSliceHas(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

// TestClaudeMonitor_ExitSentinelFiresEndToEnd is the falsifying guard for the WHOLE
// F32 mechanism, not just the translate step: it runs Prepare's REAL launch line
// through a REAL shell with a fake `claude` that exits 42, injects PaneEnv exactly
// as app.OpenWorkspace does, and asserts the shell's exit sentinel captures $? and
// curls the listener → StateExited("exited (code 42)"). This proves the crux the bug
// hinges on — the shell OUTLIVES the agent and the sentinel fires on the agent's exit
// (no pty:exit needed) — with the token arriving via the process ENV (never the typed
// line). curl-gated so it skips gracefully where curl is absent.
func TestClaudeMonitor_ExitSentinelFiresEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not on PATH; the exit sentinel needs it")
	}
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	binDir := t.TempDir()
	// Fake `claude` that exits 42 (a crash-like nonzero code the shell reports as $?).
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cwd := t.TempDir()
	launch, err := m.Prepare(context.Background(), "ws", cwd, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// The launch line must NOT carry the literal token (env-injected, not inlined).
	if strings.Contains(launch, l.Token()) {
		t.Fatalf("launch line leaked the bearer token: %q", launch)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// Inject the exit env exactly as app.OpenWorkspace does: PaneEnv merged onto
	// os.Environ() (so PERCH_EXIT_TOKEN/PERCH_EXIT_URL are in the shell's process env).
	env := append(os.Environ(), m.PaneEnv()...)
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, env, "data", "exit", func(string, ...any) {}, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = br.Close() }()

	if _, err := br.Write([]byte(launch)); err != nil {
		t.Fatalf("write launch: %v", err)
	}

	select {
	case ev := <-m.Events():
		if ev.Kind != "state" || ev.State != agent.StateExited {
			t.Fatalf("shell exit sentinel must yield StateExited, got %+v", ev)
		}
		if ev.Err != "exited (code 42)" {
			t.Errorf("Err = %q, want 'exited (code 42)' (the shell's captured $?)", ev.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exit sentinel never reached the listener — the shell did not curl on agent exit")
	}
}

// TestClaudeNewArgs_NoModel verifies NewArgs returns an empty slice (no --model ever).
func TestClaudeNewArgs_NoModel(t *testing.T) {
	c := agent.NewClaude()
	args := c.NewArgs()
	if len(args) != 0 {
		t.Errorf("NewArgs() = %v, want []", args)
	}
}

// TestOpencodeNewArgs_NoModel verifies NewArgs returns an empty slice.
func TestOpencodeNewArgs_NoModel(t *testing.T) {
	o := agent.NewOpencode()
	args := o.NewArgs()
	if len(args) != 0 {
		t.Errorf("NewArgs() = %v, want []", args)
	}
}
