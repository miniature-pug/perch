// internal/agent/claude_monitor_test.go
package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "", "")
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
	if _, err := m.Prepare(context.Background(), "wsM", worktree, "", ""); err != nil {
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
	if _, err := m.Prepare(context.Background(), "wsMPE", worktree, "", ""); err != nil {
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
	cmd, err := m.Prepare(context.Background(), "ws-submit", cwd, "", "")
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
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, "data", "exit", emit, 80, 24)
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
