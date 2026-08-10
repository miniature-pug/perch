// internal/agent/fake_monitor.go
package agent

import (
	"context"
	"sync"
)

// ApproveCall records one Approve invocation for test assertions.
type ApproveCall struct {
	ReqID string
	D     Decision
}

type FakeMonitor struct {
	sequence     []Event
	events       chan Event
	mu           sync.Mutex
	decisions    []Decision
	approveCalls []ApproveCall
	state        State
	lastTool     string
	tornDown     bool
	launchCmd    string
	paneEnv      []string
	// capturedResumeID holds the last resumeID argument passed to Prepare.
	// Test-only. Read it with CapturedResumeID().
	capturedResumeID string
}

func NewFakeMonitor(seq []Event) *FakeMonitor {
	return &FakeMonitor{sequence: seq, events: make(chan Event, len(seq)+4), launchCmd: "claude --fake"}
}
func (f *FakeMonitor) Prepare(_ context.Context, workspaceID, _, resumeID string) (string, error) {
	f.mu.Lock()
	f.capturedResumeID = resumeID
	cmd := f.launchCmd
	f.mu.Unlock()
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
	return cmd, nil
}

// CapturedResumeID returns the resumeID argument last passed to Prepare.
// Test-only.
func (f *FakeMonitor) CapturedResumeID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capturedResumeID
}
func (f *FakeMonitor) Events() <-chan Event { return f.events }
func (f *FakeMonitor) Approve(reqID string, d Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions = append(f.decisions, d)
	f.approveCalls = append(f.approveCalls, ApproveCall{ReqID: reqID, D: d})
	return nil
}

// ApproveCalls returns the recorded Approve invocations. Test-only.
func (f *FakeMonitor) ApproveCalls() []ApproveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.approveCalls
}

func (f *FakeMonitor) Capabilities() Caps { return Caps{true, true} }
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

// Start is a no-op for the fake. Prepare's goroutine emits the events.
func (f *FakeMonitor) Start(_ context.Context) {}

// SetState overrides the state that CurrentState() reports. Test-only.
func (f *FakeMonitor) SetState(s State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = s
}

// SetLaunchCmd overrides the launch command that Prepare returns. Test-only.
func (f *FakeMonitor) SetLaunchCmd(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launchCmd = cmd
}

// PaneEnv returns the pane environment set by SetPaneEnv. It is nil by
// default.
func (f *FakeMonitor) PaneEnv() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paneEnv
}

// SetPaneEnv overrides the pane environment that PaneEnv returns. Test-only.
func (f *FakeMonitor) SetPaneEnv(env []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneEnv = env
}

// Replay pushes ev onto the events channel, so app-level pump tests can
// observe forwarding. Test-only.
func (f *FakeMonitor) Replay(ev Event) {
	f.events <- ev
}
