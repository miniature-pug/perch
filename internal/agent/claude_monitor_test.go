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

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/pty"
)

// newMonitorWithTestListener creates a ClaudeMonitor backed by a real
// in-process hook listener. The caller must defer cleanup(). TMPDIR is
// redirected, so the per-session settings directory lands in a test dir.
func newMonitorWithTestListener(t *testing.T) (*agent.ClaudeMonitor, *hooklistener.Listener, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", "")
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	m := agent.NewClaudeMonitorWithListener(agent.NewClaude(), l)
	return m, l, func() { _ = m.Teardown(); _ = l.Close() }
}

// readHookSettings parses the per-session settings file.
func readHookSettings(t *testing.T, m *agent.ClaudeMonitor) map[string]any {
	t.Helper()
	data, err := os.ReadFile(m.HookSettingsPath())
	if err != nil {
		t.Fatalf("read hook settings: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse hook settings: %v", err)
	}
	hooks, _ := s["hooks"].(map[string]any)
	return hooks
}

// TestClaudeMonitorPrepare_HooksLiveOutsideWorktree is the AGT-2/AGT-3/APP-2
// regression. Prepare must not write anything into the worktree (a
// pre-existing committed settings.json stays byte-identical, and no
// .claude/ appears where there was none). The hooks live in a private
// per-session file that the launch line loads with --settings, and
// Teardown removes it.
func TestClaudeMonitorPrepare_HooksLiveOutsideWorktree(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo")
	claudeDir := filepath.Join(worktree, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	// A user file with formatting and characters a re-marshal would change.
	foreign := "{\n    \"hooks\": {\"Stop\": [{\"matcher\": \"\", \"hooks\": [{\"type\": \"command\", \"command\": \"a && b > c\"}]}]},\n    \"model\": \"x\"\n}\n"
	userFile := filepath.Join(claudeDir, "settings.json")
	_ = os.WriteFile(userFile, []byte(foreign), 0o644)

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got, _ := os.ReadFile(userFile); string(got) != foreign {
		t.Errorf("Prepare modified the worktree settings.json:\n%s", got)
	}
	path := m.HookSettingsPath()
	if path == "" || strings.HasPrefix(path, worktree) {
		t.Fatalf("hook settings path %q must be outside the worktree", path)
	}
	if !strings.Contains(cmd, `--settings "`+path+`"`) {
		t.Errorf("launch line does not load the hook settings: %q", cmd)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings file mode = %o, want 600 (carries the bearer token)", perm)
	}
	di, _ := os.Stat(filepath.Dir(path))
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("settings dir mode = %o, want 700", perm)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), l.Token()) || !strings.Contains(string(raw), l.Addr()) {
		t.Errorf("settings file lacks this session's listener addr/token:\n%s", raw)
	}
	hooks := readHookSettings(t, m)
	for _, ev := range []string{"PermissionRequest", "PreToolUse", "PostToolUse", "Notification", "UserPromptSubmit", "Stop", "StopFailure", "SessionStart"} {
		if arr, _ := hooks[ev].([]any); len(arr) != 1 {
			t.Errorf("hooks[%q] = %v, want one group", ev, hooks[ev])
		}
	}
	// AGT-8: only PermissionRequest gates tools; PreToolUse/PostToolUse are
	// scoped to AskUserQuestion.
	for _, ev := range []string{"PreToolUse", "PostToolUse"} {
		g := hooks[ev].([]any)[0].(map[string]any)
		if g["matcher"] != "AskUserQuestion|ExitPlanMode" {
			t.Errorf("%s matcher = %v, want AskUserQuestion|ExitPlanMode", ev, g["matcher"])
		}
	}
	if g := hooks["Notification"].([]any)[0].(map[string]any); g["matcher"] != "idle_prompt" {
		t.Errorf("Notification matcher = %v, want idle_prompt", g["matcher"])
	}
	pr := hooks["PermissionRequest"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if pr["timeout"] == nil {
		t.Error("PermissionRequest hook must carry an explicit long timeout (APP-16)")
	}

	_ = m.Teardown()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("Teardown left the settings dir behind: %v", err)
	}
	if got, _ := os.ReadFile(userFile); string(got) != foreign {
		t.Errorf("Teardown modified the worktree settings.json:\n%s", got)
	}
}

// TestClaudeMonitorPrepare_NoWorktreeFiles checks that a session in a
// directory without .claude/ leaves the directory untouched (APP-2: git
// status must stay clean).
func TestClaudeMonitorPrepare_NoWorktreeFiles(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := t.TempDir()
	if _, err := m.Prepare(context.Background(), "ws", worktree, ""); err != nil {
		t.Fatal(err)
	}
	_ = m.Teardown()
	entries, _ := os.ReadDir(worktree)
	if len(entries) != 0 {
		t.Errorf("worktree gained files: %v", entries)
	}
}

// TestClaudeMonitorPrepare_SharedCwdSeparateHooks is the APP-8 regression:
// two sessions on the same cwd each get their own hook file pointing at
// their own listener, and one session's Teardown does not touch the other.
func TestClaudeMonitorPrepare_SharedCwdSeparateHooks(t *testing.T) {
	a, la, ca := newMonitorWithTestListener(t)
	defer ca()
	lb, err := hooklistener.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lb.Close() }()
	b := agent.NewClaudeMonitorWithListener(agent.NewClaude(), lb)
	defer func() { _ = b.Teardown() }()
	cwd := t.TempDir()
	if _, err := a.Prepare(context.Background(), "A", cwd, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Prepare(context.Background(), "B", cwd, ""); err != nil {
		t.Fatal(err)
	}
	if a.HookSettingsPath() == b.HookSettingsPath() {
		t.Fatal("two sessions share one hook settings file")
	}
	rb, _ := os.ReadFile(b.HookSettingsPath())
	if !strings.Contains(string(rb), lb.Token()) || strings.Contains(string(rb), la.Token()) {
		t.Error("session B's hooks do not point at B's own listener")
	}
	_ = a.Teardown()
	if _, err := os.Stat(b.HookSettingsPath()); err != nil {
		t.Errorf("A's Teardown removed B's hooks: %v", err)
	}
}

// TestClaudeMonitorPrepare_CleansLegacyPerchOnlyFile checks the one-time
// cleanup of a worktree settings.json left by an older perch: a file with
// ONLY perch hook groups is deleted (with its then-empty .claude/), and a
// file with any user content is left byte-identical.
func TestClaudeMonitorPrepare_CleansLegacyPerchOnlyFile(t *testing.T) {
	legacyCmd := `curl -sf -X POST -H \"Authorization: Bearer abc\" -d @- http://127.0.0.1:1/hook # perch-monitor-hook`
	perchOnly := `{"hooks":{"PreToolUse":[{"matcher":"","hooks":[{"type":"command","command":"` + legacyCmd + `"}]}],"Stop":[{"matcher":"","hooks":[{"type":"command","command":"` + legacyCmd + `"}]}]}}`
	mixed := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"` + legacyCmd + `"}]},{"hooks":[{"type":"command","command":"mine"}]}]}}`
	for _, tc := range []struct {
		name, content string
		wantGone      bool
	}{
		{"perch-only", perchOnly, true},
		{"old-teardown-leftover", "{\n  \"hooks\": {}\n}\n", true},
		{"empty-object", "{}", true},
		{"mixed", mixed, false},
		{"other-keys", `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + legacyCmd + `"}]}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, cleanup := newMonitorWithTestListener(t)
			defer cleanup()
			cwd := t.TempDir()
			dir := filepath.Join(cwd, ".claude")
			_ = os.MkdirAll(dir, 0o755)
			f := filepath.Join(dir, "settings.json")
			_ = os.WriteFile(f, []byte(tc.content), 0o600)
			if _, err := m.Prepare(context.Background(), "ws", cwd, ""); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(f)
			if tc.wantGone {
				if err == nil {
					t.Fatalf("perch-only legacy file kept: %s", got)
				}
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Errorf("empty .claude/ kept")
				}
				return
			}
			if string(got) != tc.content {
				t.Errorf("file with user content was rewritten:\n%s", got)
			}
		})
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
	// AGT-7: SessionStart means claude sits at its prompt (idle);
	// UserPromptSubmit starts a turn (running); Stop ends it (done); a
	// second UserPromptSubmit re-enters running; a compaction SessionStart
	// keeps the state and only reports the session id.
	post(`{"hook_event_name":"SessionStart","source":"startup","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)
	post(`{"hook_event_name":"UserPromptSubmit","session_id":"sid-A"}`)
	post(`{"hook_event_name":"Stop","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)
	post(`{"hook_event_name":"UserPromptSubmit","session_id":"sid-A"}`)
	post(`{"hook_event_name":"SessionStart","source":"compact","session_id":"sid-A"}`)

	want := []agent.State{agent.StateIdle, agent.StateRunning, agent.StateDone, agent.StateRunning, ""}
	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < len(want) {
		select {
		case ev := <-m.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d events", len(got))
		}
	}
	for i, w := range want {
		if got[i].Kind != "state" || got[i].State != w {
			t.Errorf("ev[%d] = %+v, want state %q", i, got[i], w)
		}
	}
	if got[0].SessionID != "sid-A" || got[4].SessionID != "sid-A" {
		t.Errorf("SessionStart must carry the session id: %+v %+v", got[0], got[4])
	}
	if m.CurrentState() != agent.StateRunning {
		t.Errorf("compaction changed the state: %q", m.CurrentState())
	}
}

// TestClaudeMonitorStopFailureErrored checks that the StopFailure hook
// event translates to Event{Kind:"state", State:StateErrored,
// Err:<error_type>}. dispatchNotify keys its blocking "Agent error" path
// off this event. The error_type payload field must surface verbatim in
// Event.Err.
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

// postHook POSTs a hook payload to the listener's /hook and returns the
// response body string. PreToolUse blocks in the handler until the test
// calls Decide(). So callers that POST a PreToolUse normally run postHook
// in a goroutine.
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

// TestClaudeMonitorAskUserQuestion_SignalOnly is the guard for the question
// signal (AGT-23). The AskUserQuestion PreToolUse must raise
// Kind=="question"/StateAwaitingInput with NO approval, and the hook must
// return at once with NO decision (an empty body), so claude shows the
// question through its normal flow. A PermissionRequest for AskUserQuestion
// must also be answered with no decision, and raise no approval card.
// PostToolUse (the user answered) returns the state to running (AGT-7).
func TestClaudeMonitorAskUserQuestion_SignalOnly(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	body := postHook(t, l,
		`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"question":"pick one"},"session_id":"s","cwd":"/p"}`)
	if body != "" {
		t.Errorf("PreToolUse response = %q, want empty (no decision)", body)
	}
	var ev agent.Event
	select {
	case ev = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for question event")
	}
	if ev.Kind != "question" || ev.State != agent.StateAwaitingInput || ev.Approval != nil {
		t.Errorf("want question/awaiting-input with no approval, got %+v", ev)
	}

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"question":"pick one"}}`)
	}()
	select {
	case body := <-respCh:
		if body != "" {
			t.Errorf("PermissionRequest(AskUserQuestion) response = %q, want empty (no decision)", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PermissionRequest for AskUserQuestion was not answered by the monitor")
	}

	_ = postHook(t, l, `{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{}}`)
	select {
	case ev = <-m.Events():
		if ev.Kind != "state" || ev.State != agent.StateRunning {
			t.Errorf("after PostToolUse want state/running, got %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PostToolUse did not clear awaiting-input")
	}
}

// TestClaudeMonitorPreToolUse_NonQuestionBlocksUntilDecide is the
// regression counterpart. A NON-question PreToolUse (Write) must take the
// approval path, Kind=="approval"/StateAwaitingApproval, and BLOCK until
// Decide supplies a verdict. It must NOT auto-allow. The POST goroutine
// stays pending until the test calls Approve. The test checks it is
// still pending before, then checks it completes after.
func TestClaudeMonitorPreToolUse_NonQuestionBlocksUntilDecide(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
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

	// The handler must STILL be blocked. There is no auto-allow for a
	// non-question tool.
	select {
	case body := <-respCh:
		t.Fatalf("Write PreToolUse must block until Decide, but the POST returned early: %q", body)
	case <-time.After(150 * time.Millisecond):
		// expected: still pending
	}

	// Now supply the verdict. The POST must complete with allow.
	if err := m.Approve(ev.Approval.ReqID, agent.Decision{Allow: true}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	select {
	case body := <-respCh:
		if !strings.Contains(body, `"behavior":"allow"`) {
			t.Errorf("after Approve, hook response = %q, want permissionDecision allow", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("POST never completed after Approve")
	}
}

// TestClaudeMonitorApprovalSummary_EllipsizesLongInput is the regression
// guard for the all-or-nothing summary. A tool input at or above the
// summary cutoff must be ELLIPSIZED into the approval Summary (tool name,
// then ": ", then the first bytes, then "…"). It must not be dropped, so
// the card conveys what the agent wants to run instead of showing only
// the bare tool name. The full, untruncated input must still ship
// separately in Input.
func TestClaudeMonitorApprovalSummary_EllipsizesLongInput(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// A tool_input whose raw JSON is well over the 120-byte summary cutoff.
	bigVal := strings.Repeat("x", 200)
	payload := fmt.Sprintf(
		`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":%q},"session_id":"s","cwd":"/p"}`,
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

	// The summary must NOT be the bare tool name (the old drop behavior).
	// It must carry the "<tool>: " prefix with ellipsized content.
	if sum == "Bash" {
		t.Fatalf("summary was dropped to the bare tool name; want an ellipsized input summary")
	}
	if !strings.HasPrefix(sum, "Bash: ") {
		t.Errorf("summary missing the \"Bash: \" prefix; got %q", sum)
	}
	if !strings.HasSuffix(sum, "…") {
		t.Errorf("long input must be ellipsized (end with …); got %q", sum)
	}
	// Bounded: the summary must truncate the 200-byte input, not ship it whole.
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

// TestClaudeMonitorApprove_ClearsAttention is the regression guard for the
// stuck sidebar attention signal. After a non-question PreToolUse raises
// an approval (StateAwaitingApproval), and the user ALLOWS it through
// Approve, the monitor MUST emit a Kind=="state"/StateRunning event (the
// tool proceeds). This lets the frontend's last-event-wins per-workspace
// state clear the amber awaiting-approval indicator, and CurrentState()
// must report StateRunning. Before the fix, Approve only unblocked the
// hook handler and emitted nothing, so the indicator stayed stuck
// forever. TestClaudeMonitorApprove_Deny_ClearsToIdle covers the deny
// path, which clears to StateIdle.
func TestClaudeMonitorApprove_ClearsAttention(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
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

// TestClaudeMonitorApprove_Deny_ClearsToRunning checks that a DENY decision
// also clears the awaiting-approval attention signal. A PermissionRequest
// deny is applied silently and the model continues its turn with the
// denial, so the agent reads as running until Stop reports done.
func TestClaudeMonitorApprove_Deny_ClearsToRunning(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
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
		if resolved.Kind != "state" || resolved.State != agent.StateRunning {
			t.Errorf("after deny, want Kind=state/StateRunning, got %+v", resolved)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deny emitted no clearing event")
	}

	select {
	case body := <-respCh:
		if !strings.Contains(body, `"behavior":"deny"`) {
			t.Errorf("deny response = %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("hook POST never returned after deny")
	}
}

// TestClaudeMonitorApprove_UnknownReqIDIsNoop is the AGT-16/APP-9
// regression: answering an unknown or expired reqID must not clear a real,
// still-blocked approval.
func TestClaudeMonitorApprove_UnknownReqIDIsNoop(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l, `{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{}}`)
	}()
	ev := <-m.Events()
	if err := m.Approve("stale-req", agent.Decision{Allow: true}); err != nil {
		t.Fatal(err)
	}
	if m.CurrentState() != agent.StateAwaitingApproval {
		t.Errorf("unknown reqID cleared the state to %q", m.CurrentState())
	}
	select {
	case e := <-m.Events():
		t.Fatalf("unknown reqID emitted %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
	_ = m.Approve(ev.Approval.ReqID, agent.Decision{Allow: true})
	<-respCh
}

// TestClaudeMonitor_ParallelApprovalsClearOnLast is the AGT-16 regression:
// with two pending approvals, answering one must keep awaiting-approval.
func TestClaudeMonitor_ParallelApprovalsClearOnLast(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	resp := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp <- postHook(t, l, `{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{}}`)
		}()
	}
	a, b := <-m.Events(), <-m.Events()
	_ = m.Approve(a.Approval.ReqID, agent.Decision{Allow: true})
	if m.CurrentState() != agent.StateAwaitingApproval {
		t.Fatalf("one of two approvals answered: state = %q, want awaiting-approval", m.CurrentState())
	}
	// Every resolution is reported, so the frontend can always retract the
	// card; the first one changes no state.
	if e := <-m.Events(); e.Kind != "approval-resolved" || e.ResolvedReqID != a.Approval.ReqID || e.State != "" {
		t.Errorf("first resolution: want approval-resolved for %s with no state, got %+v", a.Approval.ReqID, e)
	}
	_ = m.Approve(b.Approval.ReqID, agent.Decision{Allow: true})
	select {
	case e := <-m.Events():
		if e.State != agent.StateRunning || e.ResolvedReqID != b.Approval.ReqID {
			t.Errorf("after the last approval want running carrying its reqID, got %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no clearing event after the last approval")
	}
	<-resp
	<-resp
}

// TestClaudeMonitor_CancelledApprovalIsRetracted is the AGT-16/APP-16
// regression: when the parked PermissionRequest is cancelled (the user
// answered claude's own dialog, or the hook timed out), the monitor emits
// an approval-resolved event naming the raw reqID and clears the state.
func TestClaudeMonitor_CancelledApprovalIsRetracted(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	reqCtx, reqCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, "http://"+l.Addr()+"/hook",
			strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"}}`))
		req.Header.Set("Authorization", "Bearer "+l.Token())
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	appr := <-m.Events()
	reqCancel()
	<-done
	select {
	case ev := <-m.Events():
		if ev.ResolvedReqID != appr.Approval.ReqID || ev.State != agent.StateRunning {
			t.Errorf("want a running event retracting %s, got %+v", appr.Approval.ReqID, ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled approval was never retracted")
	}
	if m.CurrentState() != agent.StateRunning {
		t.Errorf("state after retraction = %q", m.CurrentState())
	}
}

// TestApprovalInputHash_IgnoresNonSemanticKeys is the AGT-9 regression:
// the always-rule key for Bash ignores the model-written description and
// timeout, but not the command or the sandbox flag.
func TestApprovalInputHash_IgnoresNonSemanticKeys(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	hashOf := func(input string) string {
		resp := make(chan string, 1)
		go func() {
			resp <- postHook(t, l, `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":`+input+`}`)
		}()
		ev := <-m.Events()
		_ = m.Approve(ev.Approval.ReqID, agent.Decision{Allow: true})
		<-resp
		<-m.Events() // the clearing event
		return ev.Approval.InputHash
	}
	a := hashOf(`{"command":"npm test","description":"Run tests","timeout":1000}`)
	b := hashOf(`{"description":"Run the test suite","command":"npm test"}`)
	c := hashOf(`{"command":"npm test","dangerouslyDisableSandbox":true}`)
	d := hashOf(`{"command":"npm run build"}`)
	if a != b {
		t.Error("re-worded description changed the always-rule key")
	}
	if a == c || a == d {
		t.Error("semantic input change did not change the always-rule key")
	}
}

// TestClaudeMonitorApprove_DoesNotClobberNewerState guards the race where
// a newer real state (StateDone: the agent's turn ended) arrives on the
// hook stream BEFORE the user's decision lands. Approve must NOT clobber
// Done with running or idle, and must NOT emit a clearing event. The
// amber indicator is already gone, because the state has moved past
// awaiting-approval, and forcing "running" would show a stale feel.
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
			`{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
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

	// A Stop arrives FIRST: the agent's turn ended while the approval card
	// sat open. This advances m.state to StateDone. Stop is a
	// non-blocking hook event.
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

	// Now the user's decision lands. It must NOT clobber Done: it only
	// reports the resolution (so the card can be retracted), with no state.
	if err := m.Approve(appr.Approval.ReqID, agent.Decision{Allow: true}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	select {
	case ev := <-m.Events():
		if ev.State != "" || ev.Kind != "approval-resolved" || ev.ResolvedReqID != appr.Approval.ReqID {
			t.Fatalf("Approve after Done must only retract the card, got %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Approve did not report the resolution")
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

// TestClaudeMonitorApprove_NeverBlocksAndNeverDrops is the AGT-15/APP-15
// regression. The app calls Approve from its event pump, the only reader of
// the events channel. With the channel full, a blocking Approve deadlocked
// that pump. Approve must return at once, and the clearing event must
// still be delivered, in order, once the channel drains.
func TestClaudeMonitorApprove_NeverBlocksAndNeverDrops(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	respCh := make(chan string, 1)
	go func() {
		respCh <- postHook(t, l,
			`{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{"path":"/x"},"session_id":"s","cwd":"/p"}`)
	}()

	var appr agent.Event
	select {
	case appr = <-m.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for approval event")
	}

	// Saturate the events channel, exactly as a stalled pump would.
	m.FillEvents(m.EventsCap())

	approveDone := make(chan error, 1)
	go func() { approveDone <- m.Approve(appr.Approval.ReqID, agent.Decision{Allow: true}) }()
	select {
	case err := <-approveDone:
		if err != nil {
			t.Fatalf("Approve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Approve blocked on a full events channel — the app's pump would deadlock")
	}
	if m.CurrentState() != agent.StateRunning {
		t.Errorf("CurrentState after Approve = %q, want running", m.CurrentState())
	}

	deadline := time.After(3 * time.Second)
	for sawClearing := false; !sawClearing; {
		select {
		case ev := <-m.Events():
			if ev.Kind == "state" && ev.State == agent.StateRunning {
				sawClearing = true
			}
		case <-deadline:
			t.Fatal("clearing StateRunning event never delivered")
		}
	}
	select {
	case <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatal("hook POST never returned after Approve")
	}
}

// TestClaudeMonitorPrepare_LaunchCommandSubmitsToShell is the falsifying
// guard for the core agent-launch loop. The string Prepare() returns is
// written VERBATIM into the pane's pty (app.OpenWorkspace calls
// pty.Bridge.Write, a raw passthrough). A shell only runs a line once a
// newline terminates it. Earlier code returned the launch command without
// a trailing "\n", so the agent never started. This bug was invisible to
// every mock-bounded test, because those tests check the returned string,
// not that a shell executes it.
//
// This test exercises Prepare()'s REAL output through a REAL /bin/sh: a
// fake `claude` on PATH prints a sentinel, and the test checks that the
// command actually runs. This test fails the instant Prepare() drops the
// submitting newline.
func TestClaudeMonitorPrepare_LaunchCommandSubmitsToShell(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	// A fake `claude` on PATH prints a sentinel. Prepare()'s command name
	// is the literal "claude" (Adapter.Name()). So a real shell that
	// resolves and runs it through PATH follows exactly the production
	// path, minus the real binary.
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

// TestClaudeMonitorAgentExit_EmitsStateExited is the F32 core for claude.
// The shell exit sentinel POSTs an AgentExit event, carrying the captured
// $?, to the SAME loopback listener as the other lifecycle hooks. The
// monitor must translate that event to a terminal StateExited, distinct
// from StateErrored, so a dead agent stops reading as "running".
// CurrentState must also flip, so the state survives a webview reload.
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

// TestClaudeMonitorAgentExit_StateExitedIsTerminal is the symmetric F32
// guard for claude. Once AgentExit has translated to StateExited, a
// straggler hook must NOT clobber it. claude fires no hook after its
// process is dead, so AgentExit is normally the last event. But the exit
// sentinel's AgentExit curl and a fire-and-forget Stop curl are two
// independent loopback POSTs that can land on the listener out of order.
// This test posts AgentExit, drains the terminal StateExited, THEN posts
// a Stop, and checks that (a) no StateDone or running event follows and
// (b) CurrentState stays StateExited. Against the un-fixed monitor, with
// no exited field or guard, the Stop translates to StateDone, and this
// test FAILS.
func TestClaudeMonitorAgentExit_StateExitedIsTerminal(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// AgentExit leads to terminal StateExited. Drain it, so exited is
	// armed before the Stop.
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

	// A straggler Stop, a fire-and-forget curl that lands after AgentExit,
	// MUST be dropped. StateExited is terminal, so no StateDone event may
	// follow.
	_ = postHook(t, l, `{"hook_event_name":"Stop","session_id":"s","transcript_path":"/t","cwd":"/p"}`)
	select {
	case ev := <-m.Events():
		t.Fatalf("exited guard failed: a post-exit Stop was emitted: %+v", ev)
	case <-time.After(300 * time.Millisecond):
		// good, no event
	}
	if m.CurrentState() != agent.StateExited {
		t.Errorf("StateExited was clobbered by a straggler Stop: %q", m.CurrentState())
	}
}

// TestClaudeMonitorPrepare_ExitSentinelUsesEnvNotLiteralToken proves the
// launch line carries the exit sentinel that references PERCH_EXIT_TOKEN
// and PERCH_EXIT_URL BY NAME, and NOT the literal bearer token, which the
// interactive shell would echo on screen. Echoing it would be a new secret
// exposure for claude, whose launch line carries no secret today. The
// token and URL travel through PaneEnv (the process environment), which
// the shell never echoes.
func TestClaudeMonitorPrepare_ExitSentinelUsesEnvNotLiteralToken(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo")
	_ = os.MkdirAll(worktree, 0o755)

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, want := range []string{"; ec=$?", "curl", "$PERCH_EXIT_TOKEN", "$PERCH_EXIT_URL", "agent_exit=$ec"} {
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

// TestClaudeMonitor_ExitSentinelFiresEndToEnd is the falsifying guard for
// the WHOLE F32 mechanism, not just the translate step. It runs Prepare's
// REAL launch line through a REAL shell with a fake `claude` that exits
// 42, injects PaneEnv exactly as app.OpenWorkspace does, and checks that
// the shell's exit sentinel captures $? and curls the listener, producing
// StateExited("exited (code 42)"). This proves the crux the bug hinges
// on: the shell OUTLIVES the agent, and the sentinel fires on the agent's
// exit with no pty:exit needed, while the token arrives through the
// process ENV and never through the typed line. This test is gated on
// curl, so it skips gracefully where curl is absent.
func TestClaudeMonitor_ExitSentinelFiresEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not on PATH; the exit sentinel needs it")
	}
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	binDir := t.TempDir()
	// A fake `claude` that exits 42, a crash-like nonzero code the shell
	// reports as $?.
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cwd := t.TempDir()
	launch, err := m.Prepare(context.Background(), "ws", cwd, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// The launch line must NOT carry the literal token. perch injects it
	// through the environment instead of inlining it.
	if strings.Contains(launch, l.Token()) {
		t.Fatalf("launch line leaked the bearer token: %q", launch)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// Inject the exit env exactly as app.OpenWorkspace does: merge PaneEnv
	// onto os.Environ(), so PERCH_EXIT_TOKEN and PERCH_EXIT_URL land in
	// the shell's process env.
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

// TestClaudeNewArgs_NoModel verifies that NewArgs returns an empty slice;
// perch never passes --model.
func TestClaudeNewArgs_NoModel(t *testing.T) {
	c := agent.NewClaude()
	args := c.NewArgs()
	if len(args) != 0 {
		t.Errorf("NewArgs() = %v, want []", args)
	}
}

// TestOpencodeNewArgs_NoModel verifies that NewArgs returns an empty slice.
func TestOpencodeNewArgs_NoModel(t *testing.T) {
	o := agent.NewOpencode()
	args := o.NewArgs()
	if len(args) != 0 {
		t.Errorf("NewArgs() = %v, want []", args)
	}
}
