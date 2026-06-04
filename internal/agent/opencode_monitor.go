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
	"time"
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

// Prepare returns the opencode serve+attach launch command. When resumeID is
// non-empty the attach subcommand is extended with --session <resumeID> so
// opencode resumes the identified session (opencode attach --session is
// supported since v1.x and a v1.1.1 regression was fixed in issue #7149).
// The model param is accepted for interface conformance but is not yet wired —
// opencode's default model selection handles that path for now.
func (m *OpencodeMonitor) Prepare(_ context.Context, _, cwd, resumeID, _ string) (string, error) {
	attach := "opencode attach $OPENCODE_URL"
	if resumeID != "" {
		// resumeID charset is [A-Za-z0-9_-] (validated upstream), so plain
		// concatenation is safe as a single shell token; no quoting needed.
		attach += " --session " + resumeID
	}
	return fmt.Sprintf("OPENCODE_SERVER_PASSWORD=%s opencode serve & %s", m.password, attach), nil
}

func (m *OpencodeMonitor) Teardown() error { return nil }

func (m *OpencodeMonitor) Start(ctx context.Context) {
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
			m.translateSSE(ctx, []byte(strings.TrimPrefix(line, "data: ")))
		}
	}()
}

type sseFrame struct {
	Type         string `json:"type"`
	SessionID    string `json:"sessionId"`
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

func (m *OpencodeMonitor) translateSSE(ctx context.Context, data []byte) {
	var f sseFrame
	if json.Unmarshal(data, &f) != nil {
		return
	}
	var ev Event
	switch f.Type {
	case "session.next.step.started":
		// Mirror ClaudeMonitor's SessionStart: carry SessionID so the app layer
		// can persist it as LastSessionID and pass it back as resumeID on the
		// next OpenWorkspace call (app.go:527–530).
		ev = Event{Kind: "state", State: StateRunning, SessionID: f.SessionID}
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
	select {
	case m.events <- ev:
	case <-ctx.Done():
	}
}

func (m *OpencodeMonitor) Approve(reqID string, d Decision) error {
	decision := "reject"
	if d.Allow && d.Always {
		decision = "always"
	} else if d.Allow {
		decision = "once"
	}
	body, _ := json.Marshal(map[string]string{"permissionId": reqID, "decision": decision})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.serverURL+"/permission", bytes.NewReader(body))
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
