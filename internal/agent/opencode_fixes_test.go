package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/pty"
)

// sse joins frames into an SSE body.
func sse(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: " + f + "\n\n")
	}
	return b.String()
}

// expectStates reads len(want) events and checks their states in order,
// then checks that nothing else arrives for a short while.
func expectStates(t *testing.T, om *agent.OpencodeMonitor, want ...agent.State) []agent.Event {
	t.Helper()
	var got []agent.Event
	for range want {
		got = append(got, nextEvent(t, om))
	}
	for i, w := range want {
		if got[i].State != w {
			t.Errorf("event %d: state %q, want %q (all: %+v)", i, got[i].State, w, got)
		}
	}
	return got
}

// TestOpencodeMonitorSSE_ErrorStaysStickyAcrossIdle is the AGT-5
// regression: opencode publishes session.error and immediately
// session.status{idle}; the errored state must survive that idle, and the
// message must come from data.message, not the class name.
func TestOpencodeMonitorSSE_ErrorStaysStickyAcrossIdle(t *testing.T) {
	om, done := serveSSE(t, sse(
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		`{"type":"session.error","properties":{"sessionID":"s","error":{"name":"APIError","data":{"message":"rate limited"}}}}`,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`,
	))
	defer done()
	got := expectStates(t, om, agent.StateRunning, agent.StateErrored)
	if got[1].Err != "rate limited" {
		t.Errorf("Err = %q, want data.message", got[1].Err)
	}
	time.Sleep(200 * time.Millisecond)
	if st := om.CurrentState(); st != agent.StateErrored {
		t.Errorf("idle after error clobbered the errored state: %q", st)
	}
}

// TestOpencodeMonitorSSE_AbortAndOverflowAreNotErrors checks AGT-5's
// special cases: an Esc interrupt (MessageAbortedError) and an
// auto-compacting ContextOverflowError raise no errored state.
func TestOpencodeMonitorSSE_AbortAndOverflowAreNotErrors(t *testing.T) {
	om, done := serveSSE(t, sse(
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		`{"type":"session.error","properties":{"sessionID":"s","error":{"name":"ContextOverflowError","data":{"message":"too long"}}}}`,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		`{"type":"session.error","properties":{"sessionID":"s","error":{"name":"MessageAbortedError","data":{"message":"aborted"}}}}`,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`,
	))
	defer done()
	// busy, busy, then the post-abort idle reads as a steady idle (no ✓).
	expectStates(t, om, agent.StateRunning, agent.StateRunning, agent.StateIdle)
}

// TestOpencodeMonitorSSE_ChildSessionsIgnored is the AGT-6 regression: a
// subagent's busy/idle must not complete the parent's turn or report the
// child id as the resumable session.
func TestOpencodeMonitorSSE_ChildSessionsIgnored(t *testing.T) {
	om, done := serveSSE(t, sse(
		`{"type":"session.status","properties":{"sessionID":"ses_parent","status":{"type":"busy"}}}`,
		`{"type":"session.created","properties":{"sessionID":"ses_child","info":{"id":"ses_child","parentID":"ses_parent"}}}`,
		`{"type":"session.status","properties":{"sessionID":"ses_child","status":{"type":"busy"}}}`,
		`{"type":"session.status","properties":{"sessionID":"ses_child","status":{"type":"idle"}}}`,
		`{"type":"session.error","properties":{"sessionID":"ses_child","error":{"name":"X","message":"child failed"}}}`,
		`{"type":"session.status","properties":{"sessionID":"ses_parent","status":{"type":"idle"}}}`,
	))
	defer done()
	got := expectStates(t, om, agent.StateRunning, agent.StateDone)
	for _, ev := range got {
		if ev.SessionID != "ses_parent" {
			t.Errorf("event reports session %q, want ses_parent: %+v", ev.SessionID, ev)
		}
	}
}

// TestOpencodeMonitorSSE_PermissionRepliedClears is the AGT-10 regression:
// permission.replied clears the amber signal at once, and the turn still
// completes with done.
func TestOpencodeMonitorSSE_PermissionRepliedClears(t *testing.T) {
	om, done := serveSSE(t, sse(
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		`{"type":"permission.asked","properties":{"id":"per_1","sessionID":"s"}}`,
		`{"type":"permission.replied","properties":{"sessionID":"s","requestID":"per_1","reply":"once"}}`,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`,
	))
	defer done()
	expectStates(t, om, agent.StateRunning, agent.StateAwaitingApproval, agent.StateRunning, agent.StateDone)
}

// TestOpencodeMonitorSSE_OverlongLineSkipped is the AGT-13 regression: a
// frame longer than the line cap must be skipped, not abort the stream
// and lose the frames after it.
func TestOpencodeMonitorSSE_OverlongLineSkipped(t *testing.T) {
	huge := `{"type":"session.diff","properties":{"diff":"` + strings.Repeat("x", 2<<20) + `"}}`
	om, done := serveSSE(t, sse(
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		huge,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"idle"}}}`,
	))
	defer done()
	expectStates(t, om, agent.StateRunning, agent.StateDone)
}

// TestOpencodeMonitorSSE_ResyncAfterLostIdle checks the reconnect resync:
// the stream drops after busy (the idle is lost), and GET /session/status
// on reconnect shows no busy session, so the turn completes.
func TestOpencodeMonitorSSE_ResyncAfterLostIdle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var conns int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			if atomic.AddInt32(&conns, 1) == 1 {
				_, _ = w.Write([]byte(sse(`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`)))
				return // drop the stream: the idle is lost
			}
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done() // a quiet, live stream
		case "/session/status":
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)
	expectStates(t, om, agent.StateRunning, agent.StateDone)
}

// TestOpencodeMonitor_StopsDialingAfterExit is the AGT-14 regression: once
// the exit sentinel reports the agent gone, the SSE pump stops instead of
// redialing a dead server twice a second forever.
func TestOpencodeMonitor_StopsDialingAfterExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var dials int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			atomic.AddInt32(&dials, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			return // every stream ends at once, forcing reconnects
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	ln, err := hooklistener.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	om := agent.NewOpencodeMonitorWithServerAndExitListener(agent.NewOpencode(), srv.URL, "pw", ln)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr()+"/hook?agent_exit=0", nil)
	req.Header.Set("Authorization", "Bearer "+ln.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if ev := nextEvent(t, om); ev.State != agent.StateExited {
		t.Fatalf("want exited, got %+v", ev)
	}
	time.Sleep(200 * time.Millisecond)
	before := atomic.LoadInt32(&dials)
	time.Sleep(1500 * time.Millisecond)
	if after := atomic.LoadInt32(&dials); after != before {
		t.Errorf("monitor kept dialing after exit: %d -> %d", before, after)
	}
}

// TestOpencodeMonitorPrepare_ServeDiesWithAttach is the AGT-4 regression.
// It runs the REAL launch line in a REAL shell with a fake `opencode`
// whose `serve` sleeps (recording its pid) and whose `attach` exits at
// once. The backgrounded serve must be killed when attach exits, not
// orphaned to init.
func TestOpencodeMonitorPrepare_ServeDiesWithAttach(t *testing.T) {
	binDir := t.TempDir()
	pidFile := filepath.Join(binDir, "serve.pid")
	op := "#!/bin/sh\nif [ \"$1\" = serve ]; then echo $$ > " + pidFile + "; exec sleep 30; fi\n" +
		"if [ \"$1\" = attach ]; then sleep 0.3; echo ATTACH_DONE; exit 3; fi\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(op), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", "/bin/sh")

	mon, err := agent.NewMonitor("opencode", agent.NewOpencode())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mon.Teardown() }()
	cwd := t.TempDir()
	cmd, err := mon.Prepare(context.Background(), "ws", cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, append(os.Environ(), mon.PaneEnv()...), "data", "exit", func(string, ...any) {}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	if _, err := br.Write([]byte(cmd)); err != nil {
		t.Fatal(err)
	}

	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && pid == 0 {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("fake serve never started")
	}
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // serve is gone: reaped with attach
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatal("opencode serve survived attach's exit (orphaned)")
}
