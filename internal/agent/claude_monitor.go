// internal/agent/claude_monitor.go
package agent

import (
	"context"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
)

type ClaudeMonitor struct {
	adapter  Adapter
	listener *hooklistener.Listener
	ownedLn  bool // true when we created listener; Teardown closes it
	events   chan Event
	cwd      string
}

func newClaudeMonitor(a Adapter) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, events: make(chan Event, 64)}
}
func NewClaudeMonitorWithListener(a Adapter, l *hooklistener.Listener) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, listener: l, events: make(chan Event, 64)}
}
func (m *ClaudeMonitor) Events() <-chan Event               { return m.events }
func (m *ClaudeMonitor) Approve(_ string, _ Decision) error { return nil }
func (m *ClaudeMonitor) Capabilities() Caps                 { return Caps{Approvals: true, Attention: true, Tokens: true} }
func (m *ClaudeMonitor) Prepare(_ context.Context, _, _, _ string) (string, error) {
	return "claude", nil
}
func (m *ClaudeMonitor) Teardown() error {
	if m.ownedLn && m.listener != nil {
		return m.listener.Close()
	}
	return nil
}
func (m *ClaudeMonitor) CurrentState() State      { return StateIdle }
func (m *ClaudeMonitor) LastApprovalTool() string { return "" }
