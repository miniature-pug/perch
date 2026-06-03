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
	om.StartSSE(ctx)

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
