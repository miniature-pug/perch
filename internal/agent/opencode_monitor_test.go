// internal/agent/opencode_monitor_test.go
package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
)

func TestOpencodeMonitorSSEParser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fixture, err := os.ReadFile(filepath.Join("testdata", "opencode", "events.sse"))
	if err != nil { t.Fatalf("fixture: %v", err) }

	var postedDecision string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(fixture)
		case "/permission":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			postedDecision = body["decision"]
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "test-pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.Start(ctx)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 4 {
		select {
		case ev := <-om.Events(): got = append(got, ev)
		case <-deadline: t.Fatalf("timeout after %d events", len(got))
		}
	}

	if got[0].Kind != "state" || got[0].State != agent.StateRunning {
		t.Errorf("ev[0]: want state=running, got %+v", got[0])
	}
	// session.next.step.started must carry the session id so the app can persist
	// it as LastSessionID and pass it back as resumeID on the next OpenWorkspace.
	if got[0].SessionID != "ses-1" {
		t.Errorf("ev[0].SessionID = %q, want ses-1", got[0].SessionID)
	}
	if got[1].Kind != "usage" || got[1].Tokens != 140 {
		t.Errorf("ev[1]: want usage tokens=140, got %+v", got[1])
	}
	if got[2].State != agent.StateAwaitingApproval || got[2].Approval == nil {
		t.Errorf("ev[2]: want awaiting-approval, got %+v", got[2])
	}
	if got[3].State != agent.StateErrored {
		t.Errorf("ev[3]: want errored, got %+v", got[3])
	}

	// State tracking: after the errored frame drained, CurrentState is errored
	// (set before the channel send → race-free once got[3] is received).
	if om.CurrentState() != agent.StateErrored {
		t.Errorf("CurrentState = %q, want %q", om.CurrentState(), agent.StateErrored)
	}
	// LastApprovalTool reflects the permission.v2.asked frame.
	if om.LastApprovalTool() != "Bash" {
		t.Errorf("LastApprovalTool = %q, want Bash", om.LastApprovalTool())
	}

	_ = om.Approve(got[2].Approval.ReqID, agent.Decision{Allow: true})
	if postedDecision != "once" { t.Errorf("want decision=once, got %q", postedDecision) }

	caps := om.Capabilities()
	if !caps.Approvals || !caps.Attention || !caps.Tokens { t.Errorf("caps: %+v", caps) }
}

// TestOpencodeMonitorPrepare_Resume verifies that Prepare appends --session
// <id> to the attach command when resumeID is non-empty, and omits it for a
// fresh-start call.
func TestOpencodeMonitorPrepare_Resume(t *testing.T) {
	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), "http://localhost:1234", "pw")
	ctx := context.Background()

	// Fresh start: no resumeID → no --session flag.
	fresh, err := om.Prepare(ctx, "ws-1", "/some/dir", "", "")
	if err != nil {
		t.Fatalf("Prepare fresh: %v", err)
	}
	const wantFresh = "OPENCODE_SERVER_PASSWORD=pw opencode serve & opencode attach $OPENCODE_URL"
	if fresh != wantFresh {
		t.Errorf("fresh:\n  got  %q\n  want %q", fresh, wantFresh)
	}

	// Resume: non-empty resumeID → --session appended to attach.
	const id = "ses-abc123"
	resume, err := om.Prepare(ctx, "ws-1", "/some/dir", id, "")
	if err != nil {
		t.Fatalf("Prepare resume: %v", err)
	}
	const wantResume = "OPENCODE_SERVER_PASSWORD=pw opencode serve & opencode attach $OPENCODE_URL --session ses-abc123"
	if resume != wantResume {
		t.Errorf("resume:\n  got  %q\n  want %q", resume, wantResume)
	}
}

// TestOpencodeMonitorSSE_SessionIDCapture verifies that the monitor emits an
// Event with SessionID set when the SSE stream carries a sessionId field on
// session.next.step.started. This mirrors the ClaudeMonitor's SessionStart
// path and is what allows app.go to persist LastSessionID for resume.
func TestOpencodeMonitorSSE_SessionIDCapture(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Minimal fixture: just the step.started event with a session id.
	const fixture = "data: {\"type\":\"session.next.step.started\",\"sessionId\":\"ses-xyz789\",\"step\":{\"type\":\"text\"}}\n\n"

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
