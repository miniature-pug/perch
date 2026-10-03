package agent_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/pty"
)

// TestClaudeMonitor_IdlePromptSettlesInterruptedTurn: Stop never fires on a
// user interrupt, so after a cancelled approval (the user rejected claude's
// own dialog) or a dismissed question the state would stay "running" or
// "awaiting-input". The Notification idle_prompt hook settles it to idle,
// but never overrides done.
func TestClaudeMonitor_IdlePromptSettlesInterruptedTurn(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// Interrupted approval: cancelled hook -> running, then idle_prompt -> idle.
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
	<-m.Events()
	reqCancel()
	<-done
	if ev := <-m.Events(); ev.State != agent.StateRunning {
		t.Fatalf("after cancel want running, got %+v", ev)
	}
	_ = postHook(t, l, `{"hook_event_name":"Notification","notification_type":"idle_prompt","message":"waiting"}`)
	if ev := <-m.Events(); ev.State != agent.StateIdle {
		t.Errorf("idle_prompt after an interrupt: want idle, got %+v", ev)
	}

	// Dismissed question: awaiting-input -> idle_prompt -> idle.
	_ = postHook(t, l, `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{}}`)
	<-m.Events()
	_ = postHook(t, l, `{"hook_event_name":"Notification","notification_type":"idle_prompt"}`)
	if ev := <-m.Events(); ev.State != agent.StateIdle {
		t.Errorf("idle_prompt after a dismissed question: want idle, got %+v", ev)
	}

	// done is never downgraded; other notification types are ignored.
	_ = postHook(t, l, `{"hook_event_name":"Stop"}`)
	<-m.Events()
	_ = postHook(t, l, `{"hook_event_name":"Notification","notification_type":"idle_prompt"}`)
	_ = postHook(t, l, `{"hook_event_name":"Notification","notification_type":"permission_prompt"}`)
	select {
	case ev := <-m.Events():
		t.Errorf("idle_prompt after done must not emit, got %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
	if m.CurrentState() != agent.StateDone {
		t.Errorf("state = %q, want done", m.CurrentState())
	}
}

// TestClaudeMonitor_ExitPlanModeIsNotACard: ExitPlanMode's permission dialog
// is the plan-approval UI (mode choice, feedback), so perch must abstain on
// it, like AskUserQuestion, and only raise the awaiting-input signal.
func TestClaudeMonitor_ExitPlanModeIsNotACard(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	_ = postHook(t, l, `{"hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"p"}}`)
	if ev := <-m.Events(); ev.Kind != "question" || ev.State != agent.StateAwaitingInput {
		t.Errorf("ExitPlanMode PreToolUse: want question signal, got %+v", ev)
	}
	resp := make(chan string, 1)
	go func() {
		resp <- postHook(t, l, `{"hook_event_name":"PermissionRequest","tool_name":"ExitPlanMode","tool_input":{"plan":"p"}}`)
	}()
	select {
	case body := <-resp:
		if body != "" {
			t.Errorf("ExitPlanMode PermissionRequest must get no decision, got %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ExitPlanMode PermissionRequest was parked as a perch approval")
	}
	select {
	case ev := <-m.Events():
		t.Errorf("ExitPlanMode raised an event: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestApprovalInputHash_KeepsDescriptionForMCPTools: for MCP tools the
// description can be the payload, so it must stay in the always-rule key.
func TestApprovalInputHash_KeepsDescriptionForMCPTools(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	hashOf := func(input string) string {
		resp := make(chan string, 1)
		go func() {
			resp <- postHook(t, l, `{"hook_event_name":"PermissionRequest","tool_name":"mcp__jira__create_issue","tool_input":`+input+`}`)
		}()
		ev := <-m.Events()
		_ = m.Approve(ev.Approval.ReqID, agent.Decision{Allow: true})
		<-resp
		<-m.Events()
		return ev.Approval.InputHash
	}
	if hashOf(`{"title":"t","description":"a"}`) == hashOf(`{"title":"t","description":"b"}`) {
		t.Error("an MCP tool's description was stripped from the always-rule key")
	}
}

// TestClaudeMonitorPrepare_KeepsTrackedLegacyFile: a perch-only leftover that
// git TRACKS is left alone, because deleting it would dirty the tree.
func TestClaudeMonitorPrepare_KeepsTrackedLegacyFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	repo := t.TempDir()
	f := filepath.Join(repo, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	_ = os.WriteFile(f, []byte(`{"hooks": {}}`), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if _, err := m.Prepare(context.Background(), "ws", repo, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("tracked legacy settings.json was deleted: %v", err)
	}
}

// TestOpencodeLaunch_CredentialsSurviveUserEnv is the regression for the
// rc/overlay override: with the user's own OPENCODE_SERVER_PASSWORD and
// USERNAME in the pane env, `opencode serve` must still get perch's
// generated password and the pinned username.
func TestOpencodeLaunch_CredentialsSurviveUserEnv(t *testing.T) {
	binDir := t.TempDir()
	out := filepath.Join(binDir, "creds")
	op := "#!/bin/sh\nif [ \"$1\" = serve ]; then echo \"$OPENCODE_SERVER_USERNAME:$OPENCODE_SERVER_PASSWORD\" > " + out + "; fi\n"
	_ = os.WriteFile(filepath.Join(binDir, "opencode"), []byte(op), 0o755)
	_ = os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", "/bin/sh")

	mon, _ := agent.NewMonitor("opencode", agent.NewOpencode())
	defer func() { _ = mon.Teardown() }()
	cwd := t.TempDir()
	cmd, err := mon.Prepare(context.Background(), "ws", cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	var pw string
	for _, e := range mon.PaneEnv() {
		if v, ok := strings.CutPrefix(e, "PERCH_OPENCODE_PASSWORD="); ok {
			pw = v
		}
	}
	env := append(os.Environ(), "OPENCODE_SERVER_PASSWORD=users-own", "OPENCODE_SERVER_USERNAME=someone")
	env = append(env, mon.PaneEnv()...)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, env, "data", "exit", func(string, ...any) {}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	_, _ = br.Write([]byte(cmd))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			if got := strings.TrimSpace(string(b)); got != "opencode:"+pw {
				t.Errorf("serve got credentials %q, want opencode:<generated>", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fake serve never ran")
}

// TestOpencodeLaunch_HangupDuringPollExits: a HUP during the readiness poll
// must kill serve AND end the subshell, not poll on and attach to a dead tty.
func TestOpencodeLaunch_HangupDuringPollExits(t *testing.T) {
	binDir := t.TempDir()
	ppidFile := filepath.Join(binDir, "ppid")
	attached := filepath.Join(binDir, "attached")
	op := "#!/bin/sh\nif [ \"$1\" = serve ]; then echo $PPID > " + ppidFile + "; exec sleep 30; fi\n" +
		"if [ \"$1\" = attach ]; then touch " + attached + "; fi\n"
	_ = os.WriteFile(filepath.Join(binDir, "opencode"), []byte(op), 0o755)
	_ = os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/bin/sh\nexit 7\n"), 0o755) // never ready
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", "/bin/sh")

	mon, _ := agent.NewMonitor("opencode", agent.NewOpencode())
	defer func() { _ = mon.Teardown() }()
	cwd := t.TempDir()
	cmd, _ := mon.Prepare(context.Background(), "ws", cwd, "")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, append(os.Environ(), mon.PaneEnv()...), "data", "exit", func(string, ...any) {}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	_, _ = br.Write([]byte(cmd))
	var sub int
	for i := 0; i < 250 && sub == 0; i++ {
		if b, err := os.ReadFile(ppidFile); err == nil {
			sub, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sub == 0 {
		t.Fatal("fake serve never started")
	}
	_ = syscall.Kill(sub, syscall.SIGHUP)
	for i := 0; i < 100; i++ {
		if syscall.Kill(sub, 0) != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if syscall.Kill(sub, 0) == nil {
		_ = syscall.Kill(sub, syscall.SIGKILL)
		t.Fatal("subshell kept polling after SIGHUP")
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(attached); err == nil {
		t.Error("attach ran after the pane hung up")
	}
}

// TestOpencodeMonitorSSE_PermissionRejectIsNotTurnComplete: a plain reject
// stops opencode's loop; the idle that follows must not report done.
func TestOpencodeMonitorSSE_PermissionRejectIsNotTurnComplete(t *testing.T) {
	om, done := serveSSE(t, sse(
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		`{"type":"permission.asked","properties":{"id":"per_1","sessionID":"s"}}`,
		`{"type":"permission.replied","properties":{"sessionID":"s","requestID":"per_1","reply":"reject"}}`,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`,
	))
	defer done()
	expectStates(t, om, agent.StateRunning, agent.StateAwaitingApproval, agent.StateIdle)
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case ev := <-om.Events():
			if ev.State == agent.StateDone {
				t.Fatalf("rejected turn reported done: %+v", ev)
			}
		case <-deadline:
			return
		}
	}
}

// TestOpencodeMonitorSSE_ChildQuestionKeepsParentSession: a subagent's
// question still raises the signal but must not report the child's id.
func TestOpencodeMonitorSSE_ChildQuestionKeepsParentSession(t *testing.T) {
	om, done := serveSSE(t, sse(
		`{"type":"session.created","properties":{"sessionID":"ses_child","info":{"id":"ses_child","parentID":"ses_parent"}}}`,
		`{"type":"question.asked","properties":{"sessionID":"ses_child"}}`,
	))
	defer done()
	ev := nextEvent(t, om)
	if ev.Kind != "question" || ev.SessionID != "" {
		t.Errorf("child question: want a signal with no session id, got %+v", ev)
	}
}
