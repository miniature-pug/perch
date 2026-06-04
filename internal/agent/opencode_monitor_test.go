// internal/agent/opencode_monitor_test.go
package agent_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/pty"
)

// TestOpencodeMonitorSSEParser drives the real opencode v1.15.12 SSE contract:
// frames are `data: {"id","type","properties":{…}}`, the stream is behind HTTP
// Basic auth, and an approval is POST /permission/:id/reply with {"reply":…}.
// The httptest server asserts the auth header and the reply path/body, so a
// regression in any of those (wrong envelope, wrong endpoint, Bearer instead of
// Basic) fails the test rather than passing against a fabricated contract.
func TestOpencodeMonitorSSEParser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fixture, err := os.ReadFile(filepath.Join("testdata", "opencode", "events.sse"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	const pw = "test-pw"
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+pw))

	var gotReply, gotReplyAuth, gotReplyPath string
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
			gotReplyAuth = r.Header.Get("Authorization")
			gotReplyPath = r.URL.Path
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotReply = body["reply"]
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
	for len(got) < 4 {
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
	// session.next.step.started must carry sessionID (properties.sessionID) so the
	// app can persist it as LastSessionID and resume on the next OpenWorkspace.
	if got[0].SessionID != "ses-1" {
		t.Errorf("ev[0].SessionID = %q, want ses-1", got[0].SessionID)
	}
	if got[1].Kind != "usage" || got[1].Tokens != 140 {
		t.Errorf("ev[1]: want usage tokens=140 (input+output), got %+v", got[1])
	}
	if got[2].State != agent.StateAwaitingApproval || got[2].Approval == nil {
		t.Fatalf("ev[2]: want awaiting-approval, got %+v", got[2])
	}
	if got[2].Approval.ReqID != "perm-1" || got[2].Approval.Tool != "bash" {
		t.Errorf("ev[2].Approval: want ReqID=perm-1 Tool=bash, got %+v", got[2].Approval)
	}
	// patterns are present → Input is a specific match key (never empty/loose).
	if got[2].Approval.Input == "" || !strings.Contains(got[2].Approval.Input, "ls -la") {
		t.Errorf("ev[2].Approval.Input = %q, want a specific key containing the pattern", got[2].Approval.Input)
	}
	if got[3].State != agent.StateErrored || got[3].Err != "timeout" {
		t.Errorf("ev[3]: want errored err=timeout, got %+v", got[3])
	}

	if om.CurrentState() != agent.StateErrored {
		t.Errorf("CurrentState = %q, want %q", om.CurrentState(), agent.StateErrored)
	}
	if om.LastApprovalTool() != "bash" {
		t.Errorf("LastApprovalTool = %q, want bash", om.LastApprovalTool())
	}

	if err := om.Approve(got[2].Approval.ReqID, agent.Decision{Allow: true}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if gotReply != "once" {
		t.Errorf("reply = %q, want once", gotReply)
	}
	if gotReplyPath != "/permission/perm-1/reply" {
		t.Errorf("reply path = %q, want /permission/perm-1/reply", gotReplyPath)
	}
	if gotReplyAuth != expectedAuth {
		t.Errorf("reply auth = %q, want Basic opencode:%s", gotReplyAuth, pw)
	}

	caps := om.Capabilities()
	if !caps.Approvals || !caps.Attention || !caps.Tokens {
		t.Errorf("caps: %+v", caps)
	}
}

// TestOpencodeMonitorApprove_AlwaysAndReject covers the other two reply values so
// the once|always|reject enum mapping is fully guarded.
func TestOpencodeMonitorApprove_AlwaysAndReject(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = body["reply"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "pw")

	if err := om.Approve("p1", agent.Decision{Allow: true, Always: true}); err != nil {
		t.Fatalf("Approve always: %v", err)
	}
	if got != "always" {
		t.Errorf("reply = %q, want always", got)
	}
	if err := om.Approve("p1", agent.Decision{Allow: false}); err != nil {
		t.Fatalf("Approve reject: %v", err)
	}
	if got != "reject" {
		t.Errorf("reply = %q, want reject", got)
	}
}

// TestOpencodeMonitorPermissionAsked_NoPatternsFailsClosed verifies the security
// invariant: a permission.asked with no distinguishing patterns yields an EMPTY
// Input, which app.maybeAutoApprove can never match — so it can never be
// silently auto-approved by an always-rule. (Fail closed.)
func TestOpencodeMonitorPermissionAsked_NoPatternsFailsClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const fixture = `data: {"type":"permission.asked","properties":{"id":"perm-9","sessionID":"s","permission":"bash","patterns":[],"metadata":{}}}` + "\n\n"
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
		if ev.Approval == nil {
			t.Fatalf("want approval event, got %+v", ev)
		}
		if ev.Approval.Input != "" {
			t.Errorf("Input = %q, want empty (fail closed: no patterns ⇒ no auto-approve key)", ev.Approval.Input)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for permission.asked event")
	}
}

// TestOpencodeMonitorPrepare_Resume guards the launch incantation: a newline so
// the shell submits it; serve on the parsed port; a readiness poll before attach
// (attach does not retry); and --session appended only when resuming.
func TestOpencodeMonitorPrepare_Resume(t *testing.T) {
	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), "http://localhost:1234", "pw")
	ctx := context.Background()

	fresh, err := om.Prepare(ctx, "ws-1", "/some/dir", "", "")
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

	resume, err := om.Prepare(ctx, "ws-1", "/some/dir", "ses-abc123", "")
	if err != nil {
		t.Fatalf("Prepare resume: %v", err)
	}
	if !strings.Contains(resume, "opencode attach http://localhost:1234 --session ses-abc123") {
		t.Errorf("resume launch missing the --session attach form: %q", resume)
	}
}

// TestOpencodeMonitorPrepare_SelfAssignsPortAndPassword verifies the production
// path (no injected server) self-assigns a loopback URL + password rather than
// emitting the empty-server launch string the old code did.
func TestOpencodeMonitorPrepare_SelfAssignsPortAndPassword(t *testing.T) {
	mon, err := agent.NewMonitor("opencode", agent.NewOpencode())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	cmd, err := mon.Prepare(context.Background(), "ws", "/dir", "", "")
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
	// A 16-byte hex password is 32 chars; assert the export carries a non-empty token.
	if i := strings.Index(cmd, "OPENCODE_SERVER_PASSWORD="); i >= 0 {
		rest := cmd[i+len("OPENCODE_SERVER_PASSWORD="):]
		token := rest[:strings.IndexByte(rest, ';')]
		if len(token) < 16 {
			t.Errorf("generated password too short: %q", token)
		}
	}
}

// TestOpencodeMonitorPrepare_LaunchIncantationRunsInShell is the falsifying guard
// for the opencode launch half of the core loop. The serve+readiness+attach
// incantation is non-trivial shell — Contains assertions on the string cannot
// catch a syntax error in the `( … serve & while … done; exec attach )`
// composition. This runs Prepare's REAL output through a REAL /bin/sh with fake
// `opencode` and `curl` on PATH and asserts execution actually reaches `attach`
// with the self-assigned URL. It regresses if the shell composition breaks or the
// newline is dropped.
func TestOpencodeMonitorPrepare_LaunchIncantationRunsInShell(t *testing.T) {
	binDir := t.TempDir()
	// Fake `curl`: always succeeds, so the readiness poll breaks on the first try.
	if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	// Fake `opencode`: on `attach` print a sentinel with its args; `serve` is a no-op.
	op := "#!/bin/sh\nif [ \"$1\" = attach ]; then printf 'PERCH_ATTACH %s\\n' \"$*\"; fi\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(op), 0o755); err != nil {
		t.Fatalf("write fake opencode: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	mon, err := agent.NewMonitor("opencode", agent.NewOpencode())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	cwd := t.TempDir()
	cmd, err := mon.Prepare(context.Background(), "ws", cwd, "", "")
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
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, "data", "exit", emit, 80, 24)
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

// TestOpencodeMonitorSSE_SessionIDCapture verifies sessionID capture from the
// real envelope (properties.sessionID), which is what lets app.go persist
// LastSessionID for resume.
func TestOpencodeMonitorSSE_SessionIDCapture(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const fixture = `data: {"type":"session.next.step.started","properties":{"sessionID":"ses-xyz789"}}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(fixture))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "test-pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	deadline := time.After(3 * time.Second)
	var ev agent.Event
	select {
	case ev = <-om.Events():
	case <-deadline:
		t.Fatal("timeout waiting for session.next.step.started event")
	}

	if ev.Kind != "state" || ev.State != agent.StateRunning {
		t.Errorf("event: want kind=state state=running, got %+v", ev)
	}
	if ev.SessionID != "ses-xyz789" {
		t.Errorf("SessionID = %q, want ses-xyz789", ev.SessionID)
	}
}
