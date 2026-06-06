// internal/agent/fake_monitor_test.go
package agent_test

import (
	"context"
	"github.com/Miniature-Pug/perch/internal/agent"
	"testing"
	"time"
)

func TestFakeMonitorEventSequence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seq := []agent.Event{
		{Kind: "state", State: agent.StateRunning},
		{Kind: "approval", State: agent.StateAwaitingApproval,
			Approval: &agent.ApprovalReq{ReqID: "r1", Tool: "Bash", Summary: "ls"}},
		{Kind: "state", State: agent.StateDone},
	}
	f := agent.NewFakeMonitor(seq)

	cmd, err := f.Prepare(context.Background(), "ws1", "/repo", "")
	if err != nil || cmd == "" {
		t.Fatalf("Prepare: err=%v cmd=%q", err, cmd)
	}
	caps := f.Capabilities()
	if !caps.Approvals || !caps.Attention {
		t.Errorf("want all-true caps, got %+v", caps)
	}

	var got []agent.Event
	deadline := time.After(3 * time.Second)
	for len(got) < len(seq) {
		select {
		case ev := <-f.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d events", len(got))
		}
	}
	if got[0].State != agent.StateRunning {
		t.Errorf("ev[0]: %+v", got[0])
	}
	if got[1].Approval == nil || got[1].Approval.ReqID != "r1" {
		t.Errorf("ev[1]: %+v", got[1])
	}

	_ = f.Approve("r1", agent.Decision{Allow: true})
	if len(f.Decisions()) == 0 || !f.Decisions()[0].Allow {
		t.Error("want recorded allow")
	}
	_ = f.Teardown()
}
