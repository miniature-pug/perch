// internal/agent/opencode_monitor.go
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type OpencodeMonitor struct {
	adapter    Adapter
	serverURL  string
	password   string
	events     chan Event
	httpClient *http.Client
	mu         sync.Mutex
	state      State
	lastTool   string
}

func newOpencodeMonitor(a Adapter) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, events: make(chan Event, 64), httpClient: &http.Client{}}
}
func NewOpencodeMonitorWithServer(a Adapter, serverURL, pw string) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, serverURL: serverURL, password: pw,
		events: make(chan Event, 64), httpClient: &http.Client{}}
}
func (m *OpencodeMonitor) Events() <-chan Event { return m.events }
func (m *OpencodeMonitor) Capabilities() Caps  { return Caps{Approvals: true, Attention: true, Tokens: true} }

func (m *OpencodeMonitor) Prepare(_ context.Context, _, cwd, _ string) (string, error) {
	return fmt.Sprintf(
		"OPENCODE_SERVER_PASSWORD=%s opencode serve & opencode attach $OPENCODE_URL",
		m.password), nil
}

func (m *OpencodeMonitor) Teardown() error { return nil }

func (m *OpencodeMonitor) StartSSE(ctx context.Context) {
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.serverURL+"/event", nil)
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+m.password)
		resp, err := m.httpClient.Do(req)
		if err != nil {
			return
		}
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			m.translateSSE([]byte(strings.TrimPrefix(line, "data: ")))
		}
	}()
}

type sseFrame struct {
	Type         string `json:"type"`
	PermissionID string `json:"permissionId"`
	Tool         string `json:"tool"`
	Error        string `json:"error"`
	Step         *struct {
		Cost   float64 `json:"cost"`
		Tokens *struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
	} `json:"step"`
	Input json.RawMessage `json:"input"`
}

func (m *OpencodeMonitor) translateSSE(data []byte) {
	var f sseFrame
	if json.Unmarshal(data, &f) != nil {
		return
	}
	var ev Event
	switch f.Type {
	case "session.next.step.started":
		ev = Event{Kind: "state", State: StateRunning}
	case "session.next.step.ended":
		if f.Step == nil || f.Step.Tokens == nil {
			return
		}
		ev = Event{Kind: "usage", Tokens: f.Step.Tokens.Input + f.Step.Tokens.Output, Cost: f.Step.Cost}
	case "session.next.step.failed":
		ev = Event{Kind: "state", State: StateErrored, Err: f.Error}
	case "permission.v2.asked":
		sum := f.Tool
		if len(f.Input) > 0 && len(f.Input) < 120 {
			sum += ": " + string(f.Input)
		}
		ev = Event{Kind: "approval", State: StateAwaitingApproval,
			Approval: &ApprovalReq{ReqID: f.PermissionID, Tool: f.Tool, Summary: sum}}
	default:
		return
	}
	m.mu.Lock()
	if ev.State != "" {
		m.state = ev.State
	}
	if ev.Approval != nil && ev.Approval.Tool != "" {
		m.lastTool = ev.Approval.Tool
	}
	m.mu.Unlock()
	m.events <- ev
}

func (m *OpencodeMonitor) Approve(reqID string, d Decision) error {
	decision := "reject"
	if d.Allow && d.Always {
		decision = "always"
	} else if d.Allow {
		decision = "once"
	}
	body, _ := json.Marshal(map[string]string{"permissionId": reqID, "decision": decision})
	req, _ := http.NewRequest(http.MethodPost, m.serverURL+"/permission", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.password)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (m *OpencodeMonitor) CurrentState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == "" {
		return StateIdle
	}
	return m.state
}

func (m *OpencodeMonitor) LastApprovalTool() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastTool
}
