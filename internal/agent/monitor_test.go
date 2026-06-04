// internal/agent/monitor_test.go
package agent_test

import (
	"github.com/Miniature-Pug/perch/internal/agent"
	"testing"
)

func TestNewMonitorDispatch(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"claude", "opencode"} {
		m, err := agent.NewMonitor(tool, agent.NewClaude())
		if err != nil || m == nil {
			t.Errorf("NewMonitor(%q): err=%v m=%v", tool, err, m)
		}
	}
	_, err := agent.NewMonitor("unknown", agent.NewClaude())
	if err == nil {
		t.Error("want error for unknown tool")
	}
}

func TestNewMonitorNilSafe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, err := agent.NewMonitor("claude", agent.NewClaude())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	if m.Events() == nil {
		t.Error("Events() channel must not be nil")
	}
	if err := m.Teardown(); err != nil {
		t.Errorf("Teardown: %v", err)
	}
}
