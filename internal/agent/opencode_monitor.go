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
	serverURL  string
	password   string
	events     chan Event
	httpClient *http.Client
	mu         sync.Mutex
	state      State
	lastTool   string
}

const (
	// loopbackHost is the single source of truth for the loopback address used by
	// both the server URL and the `--hostname` flag below. Centralizing it keeps the
	// two references in lockstep.
	loopbackHost = "127.0.0.1"

	// loopbackServerURLFmt is the format string used to build the opencode serve
	// URL from a free loopback port number.
	loopbackServerURLFmt = "http://" + loopbackHost + ":%d"

	// opencodeServePollMaxIters and opencodeServePollIntervalSec bound the readiness
	// poll baked into serveAndAttachFmt (budget: 50×0.2s≈10s). They are string consts
	// because they are interpolated directly into the emitted shell command.
	opencodeServePollMaxIters    = "50"
	opencodeServePollIntervalSec = "0.2"

	// randomTokenBytes is the number of cryptographically-random bytes used when
	// generating the Basic-auth password for opencode serve.
	randomTokenBytes = 16

	// opencodeBasicAuthUser is the fixed HTTP Basic-auth username opencode's
	// server expects (v1.15.12 server/auth.ts).
	opencodeBasicAuthUser = "opencode"

	// firstConnectDeadline bounds the initial connection attempt to the opencode
	// server before the monitor reports StateErrored.
	firstConnectDeadline = 30 * time.Second

	// sseRetryBackoff is the sleep between SSE reconnect attempts.
	sseRetryBackoff = 500 * time.Millisecond

	// sseScannerInitBuf is the initial bufio.Scanner buffer capacity for the SSE
	// reader.
	sseScannerInitBuf = 64 * 1024

	// sseScannerMaxBuf is the maximum token size the SSE scanner will accept.
	sseScannerMaxBuf = 1024 * 1024

	// approveTimeout bounds the HTTP POST used to reply to a permission request.
	approveTimeout = 10 * time.Second

	// serveAndAttachFmt is the shell incantation Prepare returns. It backgrounds
	// `opencode serve`, polls until the port is listening (budget: 50×0.2s≈10s),
	// then exec's `opencode attach`. Arguments (in order): password, portStr,
	// serverURL, attach. The leading space keeps the password out of
	// history-ignoring shells. The 50-iteration / 0.2 s values are baked into
	// the shell command and must not be changed without updating the comment above
	// that documents the ~10 s budget.
	serveAndAttachFmt = " ( export OPENCODE_SERVER_PASSWORD=%s;" +
		" opencode serve --port %s --hostname " + loopbackHost + " >/dev/null 2>&1 &" +
		" i=0; while [ $i -lt " + opencodeServePollMaxIters + " ]; do curl -s -o /dev/null %s && break; i=$((i+1)); sleep " + opencodeServePollIntervalSec + "; done;" +
		" exec %s )\n"
)

func newOpencodeMonitor(a Adapter) *OpencodeMonitor {
	// serverURL/password stay empty here and are self-assigned in Prepare so the
	// free port is grabbed as late as possible (smallest bind→serve race window).
	return &OpencodeMonitor{events: make(chan Event, monitorEventChanBuf), httpClient: &http.Client{}}
}

// NewOpencodeMonitorWithServer injects a server URL + password instead of
// self-assigning them. Test-only: it lets tests point the monitor at an
// httptest server. Production goes through newOpencodeMonitor + Prepare.
func NewOpencodeMonitorWithServer(a Adapter, serverURL, pw string) *OpencodeMonitor {
	return &OpencodeMonitor{serverURL: serverURL, password: pw,
		events: make(chan Event, monitorEventChanBuf), httpClient: &http.Client{}}
}

func (m *OpencodeMonitor) Events() <-chan Event { return m.events }
func (m *OpencodeMonitor) Capabilities() Caps {
	return Caps{Approvals: true, Attention: true}
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
		m.serverURL = fmt.Sprintf(loopbackServerURLFmt, port)
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
	// L-18: opencode also accepts --continue/-c to resume the most-recent session
	// without knowing its ID. We deliberately do NOT wire that flag here because
	// there is no mechanism in the call path for a caller to express "continue
	// intent" as distinct from "I have no session id": app.OpenWorkspace always
	// passes w.LastSessionID (a concrete ID or ""). An empty resumeID means the
	// workspace is fresh, not that --continue should be used. Adding --continue
	// support would require a new signal in the Monitor.Prepare signature, which
	// is a shared interface (ClaudeMonitor, FakeMonitor also implement it) — a
	// multi-file interface change outside the scope of this fix. If a "resume
	// most recent" UX is later desired, extend app.OpenWorkspace / the registry
	// to pass a "continueLatest bool" through to Prepare, then add the flag here.

	// Leading space keeps the password out of history-ignoring shells. The poll
	// caps at ~10s (50 × 0.2s) then falls through to attach, which will surface
	// its own error if serve never came up.
	cmd := fmt.Sprintf(serveAndAttachFmt,
		m.password, portStr, m.serverURL, attach)
	return cmd, nil
}

func (m *OpencodeMonitor) Teardown() error { return nil }

// freeLoopbackPort asks the kernel for a free TCP port on 127.0.0.1 and releases
// it. There is a small race between release and `opencode serve` binding it; if
// another process steals the port, serve fails and Start reports StateErrored.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", loopbackHost+":0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// randomToken returns randomTokenBytes cryptographically-random bytes as hex
// (shell-safe, needs no quoting).
func randomToken() (string, error) {
	b := make([]byte, randomTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// authHeader returns the HTTP Basic auth header value opencode expects: the
// username is opencodeBasicAuthUser (v1.15.12 server/auth.ts), the password is
// the one we generated and exported to `opencode serve`.
func (m *OpencodeMonitor) authHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(opencodeBasicAuthUser+":"+m.password))
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
			case <-time.After(sseRetryBackoff):
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
	sc.Buffer(make([]byte, 0, sseScannerInitBuf), sseScannerMaxBuf)
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

// idleTransition maps an opencode idle signal to a lifecycle state given the
// prior state: a busy→idle transition is a completed turn (StateDone, drives the
// §8 ambient toast); any other idle is a steady idle (StateIdle, no toast).
func idleTransition(prev State) Event {
	if prev == StateRunning {
		return Event{Kind: "state", State: StateDone}
	}
	return Event{Kind: "state", State: StateIdle}
}

func (m *OpencodeMonitor) translateSSE(ctx context.Context, data []byte) {
	var env sseEnvelope
	if json.Unmarshal(data, &env) != nil {
		return
	}
	// translateSSE is called serially from the single SSE-reader goroutine, so
	// reading the prior state here is race-free w.r.t. the write at the end.
	m.mu.Lock()
	prev := m.state
	m.mu.Unlock()

	var ev Event
	switch env.Type {
	case "session.status":
		// The session-level status is the authoritative idle/running signal and,
		// crucially, the only DEFAULT-emitted event that carries the sessionID
		// (the session.next.step.* events that also carry it are gated behind
		// OPENCODE_EXPERIMENTAL_EVENT_SYSTEM). Capturing sessionID here is what
		// makes resume work without the experimental flag: the app persists it as
		// LastSessionID and passes it back as `attach --session <id>`.
		// status.type ∈ {idle, busy, retry} (v1.15.12 session/status.ts).
		// retry is transient → no transition.
		var p struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		switch p.Status.Type {
		case "idle":
			// H-6: a busy→idle transition means the agent finished a turn → StateDone
			// so dispatchNotify fires the §8 ambient toast. An idle that does NOT
			// follow a running state (e.g. the session reporting idle at connect, or
			// a duplicate idle / the deprecated session.idle alias firing too) is a
			// steady idle → StateIdle, no spurious "Turn complete" toast.
			ev = idleTransition(prev)
			ev.SessionID = p.SessionID
		case "busy":
			ev = Event{Kind: "state", State: StateRunning, SessionID: p.SessionID}
		default:
			return
		}
	case "session.idle":
		// Deprecated alias of session.status{type:idle}; same transition rule.
		ev = idleTransition(prev)
	case "session.error":
		// opencode's default-emitted error event. (session.next.step.failed also
		// carries errors but is gated behind OPENCODE_EXPERIMENTAL_EVENT_SYSTEM,
		// which perch never sets — so session.error is the only error signal perch
		// can rely on.) Properties: {sessionID?, error} where error is a bare string
		// or {message,name} (v1.15.12 session-event.ts Error).
		var p struct {
			SessionID string          `json:"sessionID"`
			Error     json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(env.Properties, &p)
		ev = Event{Kind: "state", State: StateErrored, Err: errMessage(p.Error), SessionID: p.SessionID}
	case "question.asked":
		// The agent asks the USER a free-form choice — distinct from permission.asked
		// (a tool-run approval). Like claude's AskUserQuestion this is an attention
		// SIGNAL: the user answers in opencode's own attach TUI in the same pane, so
		// perch only surfaces StateAwaitingInput and does NOT reply on
		// POST /question/:id/reply (the TUI client owns the reply). The feel is
		// cleared by question.replied / question.rejected below. (v1.15.12
		// question/index.ts: question.asked is emitted unconditionally, no flag.)
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(env.Properties, &p)
		ev = Event{Kind: "question", State: StateAwaitingInput, SessionID: p.SessionID}
	case "question.replied", "question.rejected":
		// The question was resolved in the TUI — clear the awaiting-input feel.
		// replied → the agent resumes working (StateRunning); rejected → no active
		// turn, so fall back to a steady idle (idleTransition from awaiting-input is
		// never StateDone, so this fires no spurious "Turn complete" toast).
		if env.Type == "question.replied" {
			ev = Event{Kind: "state", State: StateRunning}
		} else {
			ev = idleTransition(prev)
		}
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

// errMessage extracts a human string from a session.error error payload,
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
	ctx, cancel := context.WithTimeout(context.Background(), approveTimeout)
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
