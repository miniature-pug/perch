// internal/agent/opencode_monitor.go
package agent

import (
	"context"
	"net/http"
)

type OpencodeMonitor struct {
	adapter    Adapter
	serverURL  string
	password   string
	events     chan Event
	httpClient *http.Client
}

func newOpencodeMonitor(a Adapter) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, events: make(chan Event, 64), httpClient: &http.Client{}}
}
func NewOpencodeMonitorWithServer(a Adapter, serverURL, pw string) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, serverURL: serverURL, password: pw,
		events: make(chan Event, 64), httpClient: &http.Client{}}
}
func (m *OpencodeMonitor) Events() <-chan Event               { return m.events }
func (m *OpencodeMonitor) Approve(_ string, _ Decision) error { return nil }
func (m *OpencodeMonitor) Capabilities() Caps                 { return Caps{Approvals: true, Attention: true, Tokens: true} }
func (m *OpencodeMonitor) Prepare(_ context.Context, _, _, _ string) (string, error) {
	return "opencode", nil
}
func (m *OpencodeMonitor) Teardown() error { return nil }
func (m *OpencodeMonitor) CurrentState() State      { return StateIdle }
func (m *OpencodeMonitor) LastApprovalTool() string { return "" }
