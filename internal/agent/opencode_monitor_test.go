// internal/agent/opencode_monitor_test.go
package agent_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/pty"
)

// TestOpencodeMonitorSSEParser drives the real opencode v1.15.12 SSE
// contract: frames are `data: {"id","type","properties":{…}}`, and the
// stream is behind HTTP Basic auth. A regression in the envelope or auth,
// for example the wrong envelope, or Bearer instead of Basic, fails
// against the real contract instead of a fabricated one.
//
// permission.asked is a PASSIVE attention signal for opencode
// (Approvals: false). opencode's own attach TUI owns the permission
// prompt, so the monitor surfaces StateAwaitingApproval with NO Approval
// payload and NEVER POSTs a reply. The server FAILS the test if any
// /permission/:id/reply arrives from the event path.
func TestOpencodeMonitorSSEParser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fixture, err := os.ReadFile(filepath.Join("testdata", "opencode", "events.sse"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	const pw = "test-pw"
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+pw))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/event":
			if r.Header.Get("Authorization") != expectedAuth {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(fixture)
		case strings.HasPrefix(r.URL.Path, "/permission/") && strings.HasSuffix(r.URL.Path, "/reply"):
			t.Errorf("unexpected reply POST %s — opencode approvals are TUI-owned; perch must not reply", r.URL.Path)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, pw)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 3 {
		select {
		case ev := <-om.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d events", len(got))
		}
	}

	if got[0].Kind != "state" || got[0].State != agent.StateRunning {
		t.Errorf("ev[0]: want state=running, got %+v", got[0])
	}
	// session.status (busy) must carry sessionID (properties.sessionID),
	// so the app can persist it as LastSessionID and resume on the next
	// OpenWorkspace.
	if got[0].SessionID != "ses-1" {
		t.Errorf("ev[0].SessionID = %q, want ses-1", got[0].SessionID)
	}
	// permission.asked is a PASSIVE attention signal: state
	// awaiting-approval but NO Approval payload, because opencode's TUI
	// owns the reply. The frontend gates the card on
	// caps.approvals=false. A nil Approval also means the app event pump
	// registers no pending approval and never calls Approve().
	if got[1].State != agent.StateAwaitingApproval || got[1].Kind != "approval" {
		t.Fatalf("ev[1]: want kind=approval/awaiting-approval, got %+v", got[1])
	}
	if got[1].Approval != nil {
		t.Errorf("ev[1] must carry NO Approval payload (passive signal, not a card), got %+v", got[1].Approval)
	}
	if got[2].State != agent.StateErrored || got[2].Err != "timeout" {
		t.Errorf("ev[2]: want errored err=timeout, got %+v", got[2])
	}

	if om.CurrentState() != agent.StateErrored {
		t.Errorf("CurrentState = %q, want %q", om.CurrentState(), agent.StateErrored)
	}
	// opencode never owns an approval, so LastApprovalTool is always
	// empty.
	if om.LastApprovalTool() != "" {
		t.Errorf("LastApprovalTool = %q, want empty (opencode approvals are TUI-owned)", om.LastApprovalTool())
	}

	caps := om.Capabilities()
	if caps.Approvals {
		t.Errorf("caps.Approvals = true, want false (opencode TUI owns approvals)")
	}
	if !caps.Attention {
		t.Errorf("caps.Attention = false, want true (perch surfaces the passive signal)")
	}
}

// TestOpencodeMonitorPermissionAsked_PassiveSignalNoReply is the
// regression guard for the double-prompt and stale-card bug. A
// permission.asked frame must produce a PASSIVE attention signal
// (Kind=approval, StateAwaitingApproval, Approval=nil), and the monitor
// must NEVER POST /permission/:id/reply from the event path, because the
// user answers in opencode's own attach TUI. If perch owned this
// approval, it would double-prompt. Since opencode emits no
// permission-resolved frame, the perch card would also never clear.
func TestOpencodeMonitorPermissionAsked_PassiveSignalNoReply(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const fixture = `data: {"type":"permission.asked","properties":{"id":"perm-9","sessionID":"s","permission":"bash","patterns":["ls -la"],"metadata":{}}}` + "\n\n"
	var replyPosted int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(fixture))
		case strings.HasPrefix(r.URL.Path, "/permission/") && strings.HasSuffix(r.URL.Path, "/reply"):
			atomic.AddInt32(&replyPosted, 1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	ev := nextEvent(t, om)
	if ev.Kind != "approval" || ev.State != agent.StateAwaitingApproval {
		t.Fatalf("want kind=approval/awaiting-approval, got %+v", ev)
	}
	if ev.Approval != nil {
		t.Errorf("permission.asked must carry NO Approval payload (signal, not card), got %+v", ev.Approval)
	}
	// No pending approval is registered anywhere, and nothing replies.
	// Give any errant background reply a window to land, then check that
	// none did.
	time.Sleep(200 * time.Millisecond)
	if n := atomic.LoadInt32(&replyPosted); n != 0 {
		t.Errorf("monitor POSTed %d permission replies from the event path; want 0 (TUI owns the reply)", n)
	}
}

// TestOpencodeMonitorPrepare_Resume guards the launch incantation. It
// checks for: a newline, so the shell submits the command; serve on the
// parsed port; a readiness poll before attach, because attach does not
// retry; and --session appended only when resuming.
func TestOpencodeMonitorPrepare_Resume(t *testing.T) {
	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), "http://localhost:1234", "pw")
	defer func() { _ = om.Teardown() }() // Prepare now stands up an exit listener; close it.
	ctx := context.Background()

	fresh, err := om.Prepare(ctx, "ws-1", "/some/dir", "")
	if err != nil {
		t.Fatalf("Prepare fresh: %v", err)
	}
	if !strings.HasSuffix(fresh, "\n") {
		t.Errorf("launch command must end with a newline to submit to the shell: %q", fresh)
	}
	for _, want := range []string{
		"export OPENCODE_SERVER_PASSWORD=pw",
		"opencode serve --port 1234 --hostname 127.0.0.1",
		"curl -s -o /dev/null http://localhost:1234", // readiness poll before attach
		"opencode attach http://localhost:1234",
	} {
		if !strings.Contains(fresh, want) {
			t.Errorf("fresh launch missing %q\n  got: %q", want, fresh)
		}
	}
	if strings.Contains(fresh, "--session") {
		t.Errorf("fresh launch must not carry --session: %q", fresh)
	}

	resume, err := om.Prepare(ctx, "ws-1", "/some/dir", "ses-abc123")
	if err != nil {
		t.Fatalf("Prepare resume: %v", err)
	}
	if !strings.Contains(resume, "opencode attach http://localhost:1234 --session ses-abc123") {
		t.Errorf("resume launch missing the --session attach form: %q", resume)
	}
}

// TestOpencodeMonitorPrepare_SelfAssignsPortAndPassword verifies the
// production path (no injected server) self-assigns a loopback URL and
// password, instead of emitting the empty-server launch string the old
// code did.
func TestOpencodeMonitorPrepare_SelfAssignsPortAndPassword(t *testing.T) {
	mon, err := agent.NewMonitor("opencode", agent.NewOpencode())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	defer func() { _ = mon.Teardown() }() // Prepare now stands up an exit listener; close it.
	cmd, err := mon.Prepare(context.Background(), "ws", "/dir", "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasSuffix(cmd, "\n") {
		t.Errorf("launch command must end with a newline: %q", cmd)
	}
	if !strings.Contains(cmd, "http://127.0.0.1:") {
		t.Errorf("expected a self-assigned loopback URL, got %q", cmd)
	}
	if strings.Contains(cmd, "OPENCODE_SERVER_PASSWORD=;") || strings.Contains(cmd, "OPENCODE_SERVER_PASSWORD= ") {
		t.Errorf("password was not generated (empty): %q", cmd)
	}
	// A 16-byte hex password is 32 chars. Check that the export carries
	// a non-empty token.
	if i := strings.Index(cmd, "OPENCODE_SERVER_PASSWORD="); i >= 0 {
		rest := cmd[i+len("OPENCODE_SERVER_PASSWORD="):]
		token := rest[:strings.IndexByte(rest, ';')]
		if len(token) < 16 {
			t.Errorf("generated password too short: %q", token)
		}
	}
}

// TestOpencodeMonitorPrepare_LaunchIncantationRunsInShell is the
// falsifying guard for the opencode launch half of the core loop. The
// serve-readiness-attach incantation is non-trivial shell. Contains
// assertions on the string cannot catch a syntax error in the `( …
// serve & while … done; exec attach )` composition. This test runs
// Prepare's REAL output through a REAL /bin/sh with fake `opencode` and
// `curl` on PATH, and checks that execution actually reaches `attach`
// with the self-assigned URL. This test fails if the shell composition
// breaks or the newline is dropped.
func TestOpencodeMonitorPrepare_LaunchIncantationRunsInShell(t *testing.T) {
	binDir := t.TempDir()
	// A fake `curl` always succeeds, so the readiness poll breaks on the
	// first try.
	if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	// A fake `opencode` prints a sentinel with its args on `attach`;
	// `serve` is a no-op.
	op := "#!/bin/sh\nif [ \"$1\" = attach ]; then printf 'PERCH_ATTACH %s\\n' \"$*\"; fi\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(op), 0o755); err != nil {
		t.Fatalf("write fake opencode: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	mon, err := agent.NewMonitor("opencode", agent.NewOpencode())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	defer func() { _ = mon.Teardown() }() // Prepare now stands up an exit listener; close it.
	cwd := t.TempDir()
	cmd, err := mon.Prepare(context.Background(), "ws", cwd, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
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
		t.Fatalf("Write launch incantation: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := strings.Contains(string(out), "PERCH_ATTACH attach http://127.0.0.1:")
		mu.Unlock()
		if seen {
			return // incantation parsed, ran, and reached attach with the server URL
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := string(out)
	mu.Unlock()
	t.Errorf("launch incantation never reached `opencode attach` with the server URL; pty output: %q", got)
}

// TestOpencodeMonitorSSE_SessionStatusDrivesIdle verifies the
// session-level status event drives running and idle. step.ended must
// NOT drive them, because a turn has many steps. status.type busy maps
// to running, and idle maps to idle (v1.15.12 session/status.ts).
func TestOpencodeMonitorSSE_SessionStatusDrivesIdle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := `data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}` + "\n\n" +
		`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(fixture))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 2 {
		select {
		case ev := <-om.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d events", len(got))
		}
	}
	if got[0].State != agent.StateRunning {
		t.Errorf("busy → want running, got %+v", got[0])
	}
	if got[1].State != agent.StateDone {
		t.Errorf("idle → want done (turn complete), got %+v", got[1])
	}
	if om.CurrentState() != agent.StateDone {
		t.Errorf("CurrentState = %q, want done", om.CurrentState())
	}
}

// TestOpencodeMonitorSSE_PermissionMidTurnStillEmitsDone is the
// regression guard for the "opencode never shows the turn-done ✓" bug. A
// real turn calls a tool, so a permission.asked frame lands mid-turn.
// opencode emits NO permission-resolved frame, so m.state stays
// StateAwaitingApproval for the rest of the turn. The terminating idle
// must STILL report StateDone, driven by the turn-in-progress flag, set
// on busy and preserved across permission.asked, not by the clobbered
// prior state. Before the fix, this degraded to StateIdle, with no ✓ and
// no toast, for every turn that used a tool, which is every real turn.
func TestOpencodeMonitorSSE_PermissionMidTurnStillEmitsDone(t *testing.T) {
	om, done := serveSSE(t,
		`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`+"\n\n"+
			`data: {"type":"permission.asked","properties":{"id":"perm-1","sessionID":"s"}}`+"\n\n"+
			`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`+"\n\n")
	defer done()

	if ev := nextEvent(t, om); ev.State != agent.StateRunning {
		t.Fatalf("ev[0]: busy → want running, got %+v", ev)
	}
	if ev := nextEvent(t, om); ev.Kind != "approval" || ev.State != agent.StateAwaitingApproval {
		t.Fatalf("ev[1]: permission.asked → want approval/awaiting-approval, got %+v", ev)
	}
	ev := nextEvent(t, om)
	if ev.State != agent.StateDone {
		t.Errorf("ev[2]: busy→permission.asked→idle → want StateDone (turn complete ✓), got %+v", ev)
	}
	if om.CurrentState() != agent.StateDone {
		t.Errorf("CurrentState = %q, want done", om.CurrentState())
	}
}

// TestOpencodeMonitorSSE_SessionIdleEmitsDone verifies the deprecated
// session.idle alias: a busy-to-idle transition (running, then
// session.idle) is a completed turn, and it must produce
// State==StateDone.
func TestOpencodeMonitorSSE_SessionIdleEmitsDone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := `data: {"type":"session.status","properties":{"status":{"type":"busy"}}}` + "\n\n" +
		`data: {"type":"session.idle","properties":{}}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(fixture))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	var got []agent.Event
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-om.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d events", len(got))
		}
	}
	if got[1].State != agent.StateDone {
		t.Errorf("busy→session.idle → want StateDone, got %+v", got[1])
	}
}

// TestOpencodeMonitorSSE_SessionIdleAliasDoesNotClobberDone is the
// FAIL-ON-REVERT guard for the "opencode turn-done ✓ never persists"
// bug. opencode's SessionStatus.set() publishes TWO SSE frames for a
// single idle transition (v1.15.12 status.ts: Event.Status{idle}, then
// the deprecated Event.Idle alias). The REAL sequence for a finished
// turn is therefore:
//
//	session.status{busy} → session.status{idle} → session.idle
//
// The session.status{idle} frame is the turn-completing edge (StateDone,
// the ✓, and the "Turn complete" toast). The trailing deprecated
// session.idle alias is pure redundancy. It must emit NOTHING, because
// emitting StateIdle would apply in arrival order in the frontend and
// CLOBBER the StateDone: the sidebar ✓ would revert to idle. Before the
// fix, the alias fell through idleState() to commitSSE and emitted
// StateIdle. So this test's "no trailing event" assertion FAILS on the
// un-fixed code.
func TestOpencodeMonitorSSE_SessionIdleAliasDoesNotClobberDone(t *testing.T) {
	om, done := serveSSE(t,
		`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`+"\n\n"+
			`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`+"\n\n"+
			`data: {"type":"session.idle","properties":{}}`+"\n\n")
	defer done()

	if ev := nextEvent(t, om); ev.State != agent.StateRunning {
		t.Fatalf("ev[0]: busy → want running, got %+v", ev)
	}
	if ev := nextEvent(t, om); ev.State != agent.StateDone {
		t.Fatalf("ev[1]: session.status{idle} → want done (turn complete ✓), got %+v", ev)
	}
	// The trailing deprecated session.idle alias is redundant after
	// session.status{idle}. It must produce NO further state event;
	// emitting StateIdle would clobber StateDone.
	select {
	case ev := <-om.Events():
		t.Fatalf("trailing session.idle emitted a clobbering event: %+v", ev)
	case <-time.After(300 * time.Millisecond):
		// good, the redundant alias was suppressed, StateDone persists
	}
	if om.CurrentState() != agent.StateDone {
		t.Errorf("final state clobbered by redundant session.idle: CurrentState = %q, want done", om.CurrentState())
	}
}

// TestOpencodeMonitorSSE_RedundantStatusIdleDoesNotClobberDone guards the
// GENERAL form of the turn-done clobber. opencode's SessionStatus.set()
// is level-triggered with no dedup, so a redundant PRIMARY
// session.status{idle} frame, for example a repeated idle or a status
// snapshot replayed on a silent SSE reconnect, is protocol-legal. Like
// the deprecated session.idle alias, it must NOT revert a completed
// turn. Sequence:
//
//	session.status{busy} → session.status{idle} → session.status{idle}
//
// The first idle completes the turn (StateDone). The second, with the
// turn flag already cleared and the state already StateDone, must emit
// NOTHING (idleState returns ""). Before the general fix, the second
// frame emitted StateIdle and clobbered the done marker, and the
// frontend's done-to-idle edge would additionally drop the "Turn
// complete" notification. So the "no trailing event" assertion FAILS on
// the un-generalized code.
func TestOpencodeMonitorSSE_RedundantStatusIdleDoesNotClobberDone(t *testing.T) {
	om, done := serveSSE(t,
		`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`+"\n\n"+
			`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`+"\n\n"+
			`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`+"\n\n")
	defer done()

	if ev := nextEvent(t, om); ev.State != agent.StateRunning {
		t.Fatalf("ev[0]: busy → want running, got %+v", ev)
	}
	if ev := nextEvent(t, om); ev.State != agent.StateDone {
		t.Fatalf("ev[1]: session.status{idle} → want done (turn complete ✓), got %+v", ev)
	}
	// The second session.status{idle} is redundant after the turn
	// already completed. It must produce NO further state event;
	// emitting StateIdle would clobber done.
	select {
	case ev := <-om.Events():
		t.Fatalf("redundant session.status{idle} emitted a clobbering event: %+v", ev)
	case <-time.After(300 * time.Millisecond):
		// good, the redundant idle was suppressed, StateDone persists
	}
	if om.CurrentState() != agent.StateDone {
		t.Errorf("final state clobbered by redundant session.status{idle}: CurrentState = %q, want done", om.CurrentState())
	}
}

// TestOpencodeMonitorSSE_IdleAtConnectIsSteady verifies the
// no-spurious-toast rule: an idle that does NOT follow a running state,
// for example the session reporting idle at connect, maps to StateIdle,
// not StateDone. So dispatchNotify does not fire a "Turn complete" toast
// on workspace open.
func TestOpencodeMonitorSSE_IdleAtConnectIsSteady(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := `data: {"type":"session.status","properties":{"status":{"type":"idle"}}}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(fixture))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	select {
	case ev := <-om.Events():
		if ev.State != agent.StateIdle {
			t.Errorf("idle-at-connect → want StateIdle (no toast), got %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for idle event")
	}
}

// TestOpencodeMonitorSSE_SessionStatusCapturesSessionID verifies
// sessionID capture from the DEFAULT session.status event. session.status
// is the only DEFAULT-emitted event that carries the sessionID; the
// session.next.step.* events that also carry it are gated behind
// OPENCODE_EXPERIMENTAL_EVENT_SYSTEM. Without this capture, resume breaks
// on default opencode. Both the busy and idle frames carry sessionID as a
// TOP-LEVEL property of properties, a sibling of status, NOT nested
// inside it, per v1.15.12 session/status.ts. The app persists
// Event.SessionID as LastSessionID and passes it to attach --session.
func TestOpencodeMonitorSSE_SessionStatusCapturesSessionID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := `data: {"type":"session.status","properties":{"sessionID":"ses_abc123","status":{"type":"busy"}}}` + "\n\n" +
		`data: {"type":"session.status","properties":{"sessionID":"ses_abc123","status":{"type":"idle"}}}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(fixture))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 2 {
		select {
		case ev := <-om.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d events", len(got))
		}
	}
	// The busy frame maps to running, and it must carry the sessionID
	// for resume.
	if got[0].State != agent.StateRunning {
		t.Errorf("busy → want running, got %+v", got[0])
	}
	if got[0].SessionID != "ses_abc123" {
		t.Errorf("busy SessionID = %q, want ses_abc123", got[0].SessionID)
	}
	// The idle frame (busy to idle: a completed turn) must ALSO carry
	// the sessionID.
	if got[1].SessionID != "ses_abc123" {
		t.Errorf("idle SessionID = %q, want ses_abc123", got[1].SessionID)
	}
}

// serveSSE is a small helper: it stands up an httptest server that
// streams the given SSE fixture on GET /event, and returns 404 for
// everything else, wires an OpencodeMonitor to it, and starts it. The
// caller drains om.Events() and should defer the returned cleanup func.
func serveSSE(t *testing.T, fixture string) (*agent.OpencodeMonitor, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(fixture))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")
	ctx, cancel := context.WithCancel(context.Background())
	om.Start(ctx)
	return om, func() { cancel(); srv.Close() }
}

// nextEvent reads one event from the monitor or fails on timeout.
func nextEvent(t *testing.T, om *agent.OpencodeMonitor) agent.Event {
	t.Helper()
	select {
	case ev := <-om.Events():
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
		return agent.Event{}
	}
}

// TestOpencodeMonitorSSE_QuestionAsked verifies that question.asked
// translates to the attention SIGNAL (Kind=="question",
// StateAwaitingInput) carrying the sessionID, NOT an approval. The user
// answers in opencode's own attach TUI, so perch never replies on the
// question endpoint. perch only surfaces the signal.
func TestOpencodeMonitorSSE_QuestionAsked(t *testing.T) {
	om, done := serveSSE(t, `data: {"type":"question.asked","properties":{"sessionID":"ses-1"}}`+"\n\n")
	defer done()

	ev := nextEvent(t, om)
	if ev.Kind != "question" {
		t.Errorf("Kind = %q, want question", ev.Kind)
	}
	if ev.State != agent.StateAwaitingInput {
		t.Errorf("State = %q, want awaiting-input", ev.State)
	}
	if ev.SessionID != "ses-1" {
		t.Errorf("SessionID = %q, want ses-1", ev.SessionID)
	}
	if ev.Approval != nil {
		t.Errorf("question must carry NO approval (signal, not card), got %+v", ev.Approval)
	}
}

// TestOpencodeMonitorSSE_QuestionReplied verifies that a question.replied
// event (the user answered in the TUI) clears the awaiting-input signal
// by resuming the agent to StateRunning.
func TestOpencodeMonitorSSE_QuestionReplied(t *testing.T) {
	om, done := serveSSE(t, `data: {"type":"question.replied","properties":{"sessionID":"ses-1"}}`+"\n\n")
	defer done()

	ev := nextEvent(t, om)
	if ev.State != agent.StateRunning {
		t.Errorf("question.replied → want StateRunning, got %+v", ev)
	}
}

// TestOpencodeMonitorSSE_QuestionRejected verifies that a
// question.rejected event from a non-running prior state (asked, then
// rejected: AwaitingInput, never Running) maps to a steady StateIdle. No
// spurious "Turn complete" toast fires, because a rejected question
// clears the turn-in-progress flag, so it never yields StateDone.
func TestOpencodeMonitorSSE_QuestionRejected(t *testing.T) {
	om, done := serveSSE(t,
		`data: {"type":"question.asked","properties":{"sessionID":"ses-1"}}`+"\n\n"+
			`data: {"type":"question.rejected","properties":{"sessionID":"ses-1"}}`+"\n\n")
	defer done()

	asked := nextEvent(t, om)
	if asked.State != agent.StateAwaitingInput {
		t.Fatalf("first event: want awaiting-input, got %+v", asked)
	}
	rejected := nextEvent(t, om)
	if rejected.State != agent.StateIdle {
		t.Errorf("question.rejected (from awaiting-input) → want StateIdle, got %+v", rejected)
	}
}

// TestOpencodeMonitorSSE_SessionError verifies that the default-emitted
// session.error event maps to StateErrored, with the human-readable
// message extracted from the {name,message} error object.
func TestOpencodeMonitorSSE_SessionError(t *testing.T) {
	om, done := serveSSE(t,
		`data: {"type":"session.error","properties":{"sessionID":"ses-1","error":{"name":"X","message":"boom"}}}`+"\n\n")
	defer done()

	ev := nextEvent(t, om)
	if ev.State != agent.StateErrored {
		t.Errorf("State = %q, want errored", ev.State)
	}
	if ev.Err != "boom" {
		t.Errorf("Err = %q, want boom", ev.Err)
	}
}

// TestOpencodeMonitorPrepare_ExitSentinelUsesEnvNotLiteralToken proves
// opencode's launch line carries the exit sentinel that references
// PERCH_EXIT_TOKEN and PERCH_EXIT_URL by name, and NOT the literal
// exit-listener token, because the interactive shell echoes the typed
// line. The token and URL travel through PaneEnv (the process
// environment).
func TestOpencodeMonitorPrepare_ExitSentinelUsesEnvNotLiteralToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mon := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), "http://127.0.0.1:4599", "pw")
	defer func() { _ = mon.Teardown() }()

	cmd, err := mon.Prepare(context.Background(), "ws", t.TempDir(), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, want := range []string{"; ec=$?", "$PERCH_EXIT_TOKEN", "$PERCH_EXIT_URL", "agent_exit=$ec"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("launch cmd missing %q; got %q", want, cmd)
		}
	}
	// The sentinel must run in the LOGIN shell, after the ( … attach )
	// subshell.
	if !strings.Contains(cmd, ")") || strings.Index(cmd, "$PERCH_EXIT_TOKEN") < strings.LastIndex(cmd, ")") {
		t.Errorf("exit sentinel must follow the attach subshell's closing ) : %q", cmd)
	}
	var token string
	for _, e := range mon.PaneEnv() {
		if strings.HasPrefix(e, "PERCH_EXIT_TOKEN=") {
			token = strings.TrimPrefix(e, "PERCH_EXIT_TOKEN=")
		}
	}
	if token == "" {
		t.Fatalf("PaneEnv missing PERCH_EXIT_TOKEN; got %v", mon.PaneEnv())
	}
	if strings.Contains(cmd, token) {
		t.Errorf("launch cmd LEAKS the literal exit-listener token: %q", cmd)
	}
}

// TestOpencodeMonitorAgentExit_EmitsStateExitedAndGuardsLaterSSE is the
// F32 core for opencode: (1) the shell exit sentinel POSTs AgentExit to
// the monitor's dedicated exit listener, producing a terminal
// StateExited; (2) the backgrounded `opencode serve` can outlive `attach`
// and keep pushing session.status frames, which the exited guard MUST
// drop, so StateExited never flips back to running or idle.
func TestOpencodeMonitorAgentExit_EmitsStateExitedAndGuardsLaterSSE(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// The SSE server holds a busy frame until the test releases it, so
	// AgentExit is guaranteed to process (exited set) before the frame
	// is delivered. This proves the guard drops the frame, instead of
	// racing the SSE goroutine.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		if fl != nil {
			fl.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`data: {"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}` + "\n\n"))
		if fl != nil {
			fl.Flush()
		}
		<-r.Context().Done() // hold the connection so it is not reconnected
	}))
	defer srv.Close()

	exitLn, err := hooklistener.New()
	if err != nil {
		t.Fatalf("exit listener: %v", err)
	}
	defer func() { _ = exitLn.Close() }()

	om := agent.NewOpencodeMonitorWithServerAndExitListener(agent.NewOpencode(), srv.URL, "pw", exitLn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	// (1) AgentExit through the exit listener produces StateExited.
	_ = postHook(t, exitLn, `{"hook_event_name":"AgentExit","error_type":"137"}`)
	ev := nextEvent(t, om)
	if ev.Kind != "state" || ev.State != agent.StateExited {
		t.Fatalf("AgentExit must emit state/StateExited; got %+v", ev)
	}
	if ev.Err != "exited (code 137)" {
		t.Errorf("Err = %q, want %q", ev.Err, "exited (code 137)")
	}
	if om.CurrentState() != agent.StateExited {
		t.Fatalf("CurrentState = %q, want exited", om.CurrentState())
	}

	// (2) Release the straggler busy SSE frame. The exited guard must
	// DROP it.
	close(release)
	select {
	case ev := <-om.Events():
		t.Fatalf("exited guard failed: a post-exit SSE frame was emitted: %+v", ev)
	case <-time.After(300 * time.Millisecond):
		// good, no event
	}
	if om.CurrentState() != agent.StateExited {
		t.Errorf("StateExited was clobbered by a straggler SSE frame: %q", om.CurrentState())
	}
}

// TestOpencodeMonitorExitedGuard_SectionBReChecksTOCTOU is the
// discriminator for the check-then-act (TOCTOU) fix. It proves
// translateSSE's WRITE half (section B, commitSSE) RE-CHECKS m.exited
// under the same lock that writes m.state, a guarantee the single
// section-A check cannot give.
//
// The whole event switch runs with no lock held, so handleExit can run
// ENTIRELY between section A's check, which saw exited==false and let a
// session.status{busy} frame through, computing StateRunning, and
// section B's state write. This test reproduces exactly that
// interleaving deterministically: arm exited as handleExit does
// (MarkExitedForTest), THEN drive the post-check write path with the
// running ev section A already let through (CommitSSEForTest). commitSSE
// must drop it: no running event on the channel, and CurrentState stays
// StateExited.
//
// Against the un-fixed guard, with section B's `if m.exited { return }`
// reverted, this test FAILS: commitSSE writes StateRunning and sends the
// running event, reproducing the stale-"running" symptom (the sidebar
// flips back to running under the Reopen overlay).
func TestOpencodeMonitorExitedGuard_SectionBReChecksTOCTOU(t *testing.T) {
	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), "http://127.0.0.1:1", "pw")
	ctx := context.Background()

	// Arm the terminal exited guard exactly as handleExit does. This
	// stands in for the exit sentinel firing in the TOCTOU window, after
	// section A's check passed.
	if !om.MarkExitedForTest() {
		t.Fatal("MarkExitedForTest: monitor was already exited")
	}
	if om.CurrentState() != agent.StateExited {
		t.Fatalf("pre-condition: CurrentState = %q, want exited", om.CurrentState())
	}

	// Drive section B directly with the running ev section A let
	// through pre-exit.
	om.CommitSSEForTest(ctx, agent.Event{Kind: "state", State: agent.StateRunning, SessionID: "s"})

	// The re-check must have dropped it: no event emitted, state still
	// terminal.
	select {
	case ev := <-om.Events():
		t.Fatalf("section B failed to re-check exited: emitted a post-exit event: %+v", ev)
	default:
		// good, commitSSE dropped the frame, nothing was sent
	}
	if om.CurrentState() != agent.StateExited {
		t.Errorf("section B clobbered StateExited: CurrentState = %q, want exited", om.CurrentState())
	}
}
