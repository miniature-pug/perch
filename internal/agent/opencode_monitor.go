// internal/agent/opencode_monitor.go
package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OpencodeMonitor drives the opencode agent. opencode has no hook system; instead
// perch runs an `opencode serve` HTTP backend (SSE event stream + permission-reply
// endpoint) that this monitor talks to, alongside an `opencode attach` TUI in the
// same pane for the user. Both point at the same self-assigned loopback server.
//
// The HTTP/SSE contract is pinned to opencode v1.15.12 source (canonical repo
// anomalyco/opencode, which sst/opencode redirects to): SSE GET /event emits
// `data: {"id","type","properties":{…}}` frames behind HTTP Basic auth; approvals
// are POST /permission/:requestID/reply with body {"reply": once|always|reject}.
// The end-to-end loop against a real opencode binary is exercised only by manual
// smoke (perch's test constraints forbid launching a real agent) — the same gate
// as the WebKit smoke. Everything below is verified against source, not a server.
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
	// serverURL/password stay empty here and are self-assigned in Prepare so the
	// free port is grabbed as late as possible (smallest bind→serve race window).
	return &OpencodeMonitor{adapter: a, events: make(chan Event, 64), httpClient: &http.Client{}}
}

// NewOpencodeMonitorWithServer injects a server URL + password instead of
// self-assigning them. Test-only: it lets tests point the monitor at an
// httptest server. Production goes through newOpencodeMonitor + Prepare.
func NewOpencodeMonitorWithServer(a Adapter, serverURL, pw string) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, serverURL: serverURL, password: pw,
		events: make(chan Event, 64), httpClient: &http.Client{}}
}

func (m *OpencodeMonitor) Events() <-chan Event { return m.events }
func (m *OpencodeMonitor) Capabilities() Caps {
	return Caps{Approvals: true, Attention: true, Tokens: true}
}

// Prepare self-assigns a free loopback port + a random Basic-auth password (unless
// a server was injected for tests) and returns the shell incantation the pane runs.
//
// The incantation backgrounds an `opencode serve` on that port, polls until the
// port is listening, then exec's `opencode attach <url>` (the user's TUI). The
// poll is required because `opencode attach` makes a single, non-retrying
// connection and exits 1 if the server is not yet up (verified in opencode
// v1.15.12 attach.ts → validateSession). serve's output is discarded so it never
// corrupts the TUI; a server that never binds surfaces as StateErrored from the
// monitor's bounded connect in Start, not from pane noise.
//
// The password is passed via the environment (never argv, so it does not appear
// in `ps`), and the whole line leads with a space so a history-ignoring shell
// keeps the secret out of shell history. It is an ephemeral random secret gating
// a loopback-only server that dies with the pane, so on-screen exposure is low risk.
//
// resumeID, when set, becomes `--session <id>` on attach (opencode resumes that
// session). The model param is intentionally NOT wired: `opencode attach` accepts
// no --model/--agent (those live only on the standalone TUI command, which cannot
// drive the serve+attach HTTP backend perch needs), so model selection is deferred
// to opencode's in-TUI picker. This is a documented deviation, not a cut corner.
func (m *OpencodeMonitor) Prepare(_ context.Context, _, _, resumeID, _ string) (string, error) {
	if m.serverURL == "" {
		port, err := freeLoopbackPort()
		if err != nil {
			return "", fmt.Errorf("OpencodeMonitor.Prepare: pick port: %w", err)
		}
		pw, err := randomToken()
		if err != nil {
			return "", fmt.Errorf("OpencodeMonitor.Prepare: gen password: %w", err)
		}
		m.serverURL = fmt.Sprintf("http://127.0.0.1:%d", port)
		m.password = pw
	}

	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(m.serverURL, "http://"))
	if err != nil {
		return "", fmt.Errorf("OpencodeMonitor.Prepare: parse server URL %q: %w", m.serverURL, err)
	}

	attach := "opencode attach " + m.serverURL
	if resumeID != "" {
		// resumeID charset is [A-Za-z0-9_-], validated by app.validateSessionID
		// in the event pump (L-10 fix) before any session id is persisted to the
		// registry, so plain concatenation is safe as a single shell token.
		attach += " --session " + resumeID
	}

	// Leading space keeps the password out of history-ignoring shells. The poll
	// caps at ~10s (50 × 0.2s) then falls through to attach, which will surface
	// its own error if serve never came up.
	cmd := fmt.Sprintf(
		" ( export OPENCODE_SERVER_PASSWORD=%s;"+
			" opencode serve --port %s --hostname 127.0.0.1 >/dev/null 2>&1 &"+
			" i=0; while [ $i -lt 50 ]; do curl -s -o /dev/null %s && break; i=$((i+1)); sleep 0.2; done;"+
			" exec %s )\n",
		m.password, portStr, m.serverURL, attach)
	return cmd, nil
}

func (m *OpencodeMonitor) Teardown() error { return nil }

// freeLoopbackPort asks the kernel for a free TCP port on 127.0.0.1 and releases
// it. There is a small race between release and `opencode serve` binding it; if
// another process steals the port, serve fails and Start reports StateErrored.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// randomToken returns 16 cryptographically-random bytes as hex (shell-safe,
// needs no quoting).
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// authHeader returns the HTTP Basic auth header value opencode expects: the
// username defaults to the literal "opencode" (v1.15.12 server/auth.ts), the
// password is the one we generated and exported to `opencode serve`.
func (m *OpencodeMonitor) authHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+m.password))
}

// Start consumes the opencode SSE event stream until ctx is cancelled. The first
// connection is bounded: if the server never comes up within the deadline the
// monitor emits StateErrored once and stops. Once connected at least once, a
// dropped stream is reconnected silently (so a normal mid-session reconnect never
// flaps the UI to errored). The goroutine exits on ctx cancellation — every wait
// selects on ctx.Done() and every HTTP request is ctx-scoped — so closing a
// workspace (which cancels ctx) reaps it with no leak.
func (m *OpencodeMonitor) Start(ctx context.Context) {
	go func() {
		const firstConnectDeadline = 30 * time.Second
		const retryBackoff = 500 * time.Millisecond
		deadline := time.Now().Add(firstConnectDeadline)
		connectedOnce := false

		for {
			if ctx.Err() != nil {
				return
			}
			if m.streamOnce(ctx) {
				connectedOnce = true
			}
			if ctx.Err() != nil {
				return
			}
			if !connectedOnce && time.Now().After(deadline) {
				m.emit(ctx, Event{Kind: "state", State: StateErrored,
					Err: "opencode server did not start"})
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryBackoff):
			}
		}
	}()
}

// streamOnce opens GET /event and consumes frames until the stream ends or
// errors. It returns true if it successfully connected (HTTP 200 + began
// reading), false on dial failure or a non-200 response, so the caller can tell
// "never connected" (→ eventually errored) from "stream dropped" (→ reconnect).
func (m *OpencodeMonitor) streamOnce(ctx context.Context) (connected bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.serverURL+"/event", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", m.authHeader())
	req.Header.Set("Accept", "text/event-stream")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// SSE data lines are `data:<json>` (an optional single space after the
		// colon is stripped per the SSE spec). Other lines (event:, id:, the
		// heartbeat comment) are ignored.
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" {
			continue
		}
		m.translateSSE(ctx, []byte(payload))
	}
	return true
}

// sseEnvelope is the opencode bus-event wire shape: every SSE frame is
// {"id","type","properties":{…}} with the typed payload nested under properties
// (v1.15.12 server .../handlers/event.ts).
type sseEnvelope struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

func (m *OpencodeMonitor) translateSSE(ctx context.Context, data []byte) {
	var env sseEnvelope
	if json.Unmarshal(data, &env) != nil {
		return
	}
	var ev Event
	switch env.Type {
	case "session.next.step.started":
		// Carry sessionID so the app can persist it as LastSessionID and pass it
		// back as resumeID on the next OpenWorkspace (mirrors ClaudeMonitor's
		// SessionStart). The field is `sessionID` (v1.15.12 session-event.ts Base).
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(env.Properties, &p)
		ev = Event{Kind: "state", State: StateRunning, SessionID: p.SessionID}
	case "session.status":
		// The session-level status is the authoritative idle/running signal.
		// step.ended fires per-step (a turn has many steps) so it must NOT drive
		// idle; session.status does. status.type ∈ {idle, busy, retry}
		// (v1.15.12 session/status.ts). retry is transient → no transition.
		var p struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		switch p.Status.Type {
		case "idle":
			ev = Event{Kind: "state", State: StateIdle}
		case "busy":
			ev = Event{Kind: "state", State: StateRunning}
		default:
			return
		}
	case "session.idle":
		// Deprecated alias of session.status{type:idle}; handle both for safety.
		ev = Event{Kind: "state", State: StateIdle}
	case "session.next.step.ended":
		var p struct {
			Cost   float64 `json:"cost"`
			Tokens struct {
				Input  int `json:"input"`
				Output int `json:"output"`
			} `json:"tokens"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		ev = Event{Kind: "usage", Tokens: p.Tokens.Input + p.Tokens.Output, Cost: p.Cost}
	case "session.next.step.failed":
		var p struct {
			Error json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(env.Properties, &p)
		ev = Event{Kind: "state", State: StateErrored, Err: errMessage(p.Error)}
	case "permission.asked":
		var p struct {
			ID         string   `json:"id"`
			Permission string   `json:"permission"`
			Patterns   []string `json:"patterns"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		summary := p.Permission
		if len(p.Patterns) > 0 {
			summary += ": " + strings.Join(p.Patterns, ", ")
		}
		// SECURITY: ApprovalReq.InputHash is the authoritative always-allow match key
		// (app.maybeAutoApprove). An empty InputHash can never match a rule, so we
		// FAIL CLOSED — compute hash only when the request carries distinguishing
		// patterns; otherwise leave InputHash empty and force the user to approve
		// every time. Never collapse distinct operations to one key.
		// M-13: hash is computed from the FULL (untruncated) input before truncation.
		input := ""
		inputHash := ""
		if len(p.Patterns) > 0 {
			key, _ := json.Marshal(struct {
				Permission string   `json:"permission"`
				Patterns   []string `json:"patterns"`
			}{p.Permission, p.Patterns})
			fullInput := string(key)
			h := sha256.Sum256([]byte(fullInput))
			inputHash = hex.EncodeToString(h[:])
			input = fullInput
			if len(input) > MaxApprovalInputLen {
				input = input[:MaxApprovalInputLen]
			}
		}
		ev = Event{Kind: "approval", State: StateAwaitingApproval,
			Approval: &ApprovalReq{ReqID: p.ID, Tool: p.Permission, Summary: summary, Input: input, InputHash: inputHash}}
	default:
		return
	}
	// Track state/lastTool (mutex-guarded) BEFORE emitting, so a reader that
	// observes the event on the channel also observes the updated state.
	m.mu.Lock()
	if ev.State != "" {
		m.state = ev.State
	}
	if ev.Approval != nil && ev.Approval.Tool != "" {
		m.lastTool = ev.Approval.Tool
	}
	m.mu.Unlock()
	m.send(ctx, ev)
}

// errMessage extracts a human string from a session.next.step.failed error,
// which may be a bare string or an object with a message/name field.
func errMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Message string `json:"message"`
		Name    string `json:"name"`
	}
	if json.Unmarshal(raw, &o) == nil {
		if o.Message != "" {
			return o.Message
		}
		if o.Name != "" {
			return o.Name
		}
	}
	return string(raw)
}

// emit tracks state then sends; used for monitor-originated lifecycle events
// (e.g. the connect-failure StateErrored) that do not come from an SSE frame.
func (m *OpencodeMonitor) emit(ctx context.Context, ev Event) {
	m.mu.Lock()
	if ev.State != "" {
		m.state = ev.State
	}
	m.mu.Unlock()
	m.send(ctx, ev)
}

func (m *OpencodeMonitor) send(ctx context.Context, ev Event) {
	select {
	case m.events <- ev:
	case <-ctx.Done():
	}
}

// Approve replies to an opencode permission request: POST /permission/:id/reply
// with {"reply": once|always|reject} (v1.15.12). reqID is the permission's id
// from the permission.asked frame.
func (m *OpencodeMonitor) Approve(reqID string, d Decision) error {
	reply := "reject"
	if d.Allow && d.Always {
		reply = "always"
	} else if d.Allow {
		reply = "once"
	}
	body, _ := json.Marshal(map[string]string{"reply": reply})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := m.serverURL + "/permission/" + url.PathEscape(reqID) + "/reply"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", m.authHeader())
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opencode permission reply: status %d", resp.StatusCode)
	}
	return nil
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
