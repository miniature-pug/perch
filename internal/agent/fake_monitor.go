// internal/agent/fake_monitor.go
package agent

import (
	"context"
	"sync"
)

type FakeMonitor struct {
	sequence  []Event
	events    chan Event
	mu        sync.Mutex
	decisions []Decision
	state     State
	lastTool  string
	tornDown  bool
}

func NewFakeMonitor(seq []Event) *FakeMonitor {
	return &FakeMonitor{sequence: seq, events: make(chan Event, len(seq)+4)}
}
func (f *FakeMonitor) Prepare(_ context.Context, workspaceID, _, _ string) (string, error) {
	go func() {
		for _, ev := range f.sequence {
			ev.WorkspaceID = workspaceID
			f.mu.Lock()
			if ev.State != "" {
				f.state = ev.State
			}
			if ev.Approval != nil && ev.Approval.Tool != "" {
				f.lastTool = ev.Approval.Tool
			}
			f.mu.Unlock()
			f.events <- ev
		}
	}()
	return "claude --fake", nil
}
func (f *FakeMonitor) Events() <-chan Event { return f.events }
func (f *FakeMonitor) Approve(_ string, d Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions = append(f.decisions, d)
	return nil
}
func (f *FakeMonitor) Capabilities() Caps { return Caps{true, true, true} }
func (f *FakeMonitor) Teardown() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tornDown = true
	return nil
}

// TornDown reports whether Teardown has been called. Test-only accessor.
func (f *FakeMonitor) TornDown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tornDown
}
func (f *FakeMonitor) Decisions() []Decision {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.decisions
}
func (f *FakeMonitor) CurrentState() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return StateIdle
	}
	return f.state
}
func (f *FakeMonitor) LastApprovalTool() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTool
}
