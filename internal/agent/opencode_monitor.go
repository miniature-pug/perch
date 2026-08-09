// internal/agent/opencode_monitor.go
package agent

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/safe"
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
	// exitListener is a dedicated loopback listener the shell exit sentinel POSTs
	// to when `opencode attach` exits (opencode has no hook system, so unlike claude
	// it cannot reuse an existing listener). Created in Prepare, drained by a second
	// goroutine in Start, closed in Teardown.
	exitListener *hooklistener.Listener
	mu           sync.Mutex
	state        State
	// exited is set once the exit sentinel reports the agent is gone. It makes
	// StateExited terminal: the backgrounded `opencode serve` can outlive `attach`
	// and keep pushing session.status SSE frames that would otherwise clobber
	// StateExited back to running/idle, so translateSSE and emit early-return once
	// it is set. Guarded by mu.
	exited bool
}

const (
	// loopbackServerURLFmt is the format string used to build the opencode serve
	// URL from a free loopback port number. Uses hooklistener.LoopbackHost as the
	// single source of truth for the loopback address.
	loopbackServerURLFmt = "http://" + hooklistener.LoopbackHost + ":%d"

	// opencodeServePollInterval is the sleep between readiness-poll iterations in
	// the launch incantation. The poll's ITERATION COUNT is derived from
	// firstConnectDeadline / this interval (see opencodeServePollMaxIters) so the
	// shell poll's total budget equals the monitor's connect deadline.
	opencodeServePollInterval = 200 * time.Millisecond

	// randomTokenBytes is the number of cryptographically-random bytes used when
	// generating the Basic-auth password for opencode serve.
	randomTokenBytes = 16

	// opencodeBasicAuthUser is the fixed HTTP Basic-auth username opencode's
	// server expects (v1.15.12 server/auth.ts).
	opencodeBasicAuthUser = "opencode"

	// firstConnectDeadline bounds BOTH the monitor's initial connection to the
	// opencode server (before it reports StateErrored) AND — via the derived poll
	// count below — the readiness poll baked into the launch incantation. Deriving
	// both bounds from ONE budget closes the attach-timing gap: previously the poll
	// gave up at ~10s while this deadline was 30s, so a serve that bound between 10s
	// and 30s left the poll to `exec opencode attach` into a not-yet-listening
	// server. attach makes a single non-retrying connection and exits 1, killing the
	// pane with no error until this deadline finally fired StateErrored (~20s later).
	// With one budget the poll keeps probing until the connect deadline, so a serve
	// that binds any time before the deadline is caught and attach runs against a
	// live server; if it never binds, the pane dies and StateErrored fire together.
	firstConnectDeadline = 30 * time.Second

	// sseRetryBackoff is the sleep between SSE reconnect attempts.
	sseRetryBackoff = 500 * time.Millisecond

	// sseScannerInitBuf is the initial bufio.Scanner buffer capacity for the SSE
	// reader.
	sseScannerInitBuf = 64 * 1024

	// sseScannerMaxBuf is the maximum token size the SSE scanner will accept.
	sseScannerMaxBuf = 1024 * 1024

	// serveAndAttachFmt is the shell incantation Prepare returns. It backgrounds
	// `opencode serve`, polls until the port is listening, then exec's
	// `opencode attach`. Arguments (in order): password, portStr, pollMaxIters,
	// serverURL, pollIntervalSec, attach. The leading space keeps the password out
	// of history-ignoring shells. The poll count and interval are passed in (not
	// baked in) because they are DERIVED from firstConnectDeadline (see the var
	// block below) so the poll budget and the monitor's connect deadline stay in
	// lockstep — do not hard-code them back into this string.
	serveAndAttachFmt = " ( export OPENCODE_SERVER_PASSWORD=%s;" +
		" opencode serve --port %s --hostname " + hooklistener.LoopbackHost + " >/dev/null 2>&1 &" +
		" i=0; while [ $i -lt %s ]; do curl -s -o /dev/null %s && break; i=$((i+1)); sleep %s; done;" +
		" exec %s )" + exitSentinel + "\n"
)

// opencodeServePollMaxIters and opencodeServePollIntervalSec are the readiness-poll
// parameters interpolated into serveAndAttachFmt as shell tokens. Both are DERIVED
// from firstConnectDeadline and opencodeServePollInterval so the shell poll's total
// budget (iters × interval) equals the monitor's connect deadline — one budget, no
// attach-timing gap (see firstConnectDeadline). They are vars, not consts, only
// because they are computed; treat them as read-only.
var (
	opencodeServePollMaxIters    = strconv.Itoa(int(firstConnectDeadline / opencodeServePollInterval))
	opencodeServePollIntervalSec = strconv.FormatFloat(opencodeServePollInterval.Seconds(), 'f', -1, 64)
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

// NewOpencodeMonitorWithServerAndExitListener injects both the SSE server creds and
// the exit-sentinel listener. Test-only: it lets a test POST an AgentExit to the
// same loopback listener the shell sentinel would hit (and then feed a later SSE
// frame to prove the exited guard drops it) without a real pane/pty. Production
// goes through newOpencodeMonitor + Prepare, which self-assigns both.
func NewOpencodeMonitorWithServerAndExitListener(a Adapter, serverURL, pw string, exitLn *hooklistener.Listener) *OpencodeMonitor {
	m := NewOpencodeMonitorWithServer(a, serverURL, pw)
	m.exitListener = exitLn
	return m
}

func (m *OpencodeMonitor) Events() <-chan Event { return m.events }
func (m *OpencodeMonitor) Capabilities() Caps {
	// Approvals: false — opencode's own `attach` TUI owns the permission prompt
	// (Allow once / Allow always / Reject) and perch cannot suppress it, so perch
	// does NOT render its own ApprovalCard (the frontend gates the card on
	// caps.approvals). perch surfaces permission.asked only as a PASSIVE attention
	// signal (StateAwaitingApproval + the blocking notification), mirroring how
	// question.asked is handled: the user answers in the TUI and perch never
	// replies. This avoids a double prompt and the stale-card bug (opencode emits
	// no permission-resolved SSE frame, so a perch-owned card would never clear).
	// claude is DIFFERENT: its PreToolUse hook BLOCKS and perch's reply suppresses
	// claude's native prompt, so claude keeps Approvals: true.
	return Caps{Approvals: false, Attention: true}
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
// session). perch does not pass a model flag; `opencode attach` accepts no
// --model/--agent (those live only on the standalone TUI command, which cannot
// drive the serve+attach HTTP backend perch needs), so model selection is the
// harness's concern.
func (m *OpencodeMonitor) Prepare(_ context.Context, _, _, resumeID string) (string, error) {
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

	// Stand up the exit listener the shell sentinel POSTs to on attach exit. Unlike
	// claude, opencode has no hook system to reuse, so this is a dedicated listener.
	// Skipped when one was injected for tests.
	if m.exitListener == nil {
		l, err := hooklistener.New()
		if err != nil {
			return "", fmt.Errorf("OpencodeMonitor.Prepare: exit listener: %w", err)
		}
		m.exitListener = l
	}

	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(m.serverURL, "http://"))
	if err != nil {
		return "", fmt.Errorf("OpencodeMonitor.Prepare: parse server URL %q: %w", m.serverURL, err)
	}

	attach := "opencode attach " + m.serverURL
	if resumeID != "" {
		// resumeID charset is [A-Za-z0-9_-], validated by app.validateSessionID
		// in the event pump before any session id is persisted to the registry,
		// so plain concatenation is safe as a single shell token.
		attach += " --session " + resumeID
	}
	// opencode also accepts --continue/-c to resume the most-recent session
	// without knowing its ID. We deliberately do NOT wire that flag here because
	// there is no mechanism in the call path for a caller to express "continue
	// intent" as distinct from "I have no session id": app.OpenWorkspace always
	// passes w.LastSessionID (a concrete ID or ""). An empty resumeID means the
	// workspace is fresh, not that --continue should be used. Adding --continue
	// support would require a new signal in the Monitor.Prepare signature, which
	// is a shared interface (ClaudeMonitor, FakeMonitor also implement it): a
	// multi-file interface change. If a "resume most recent" UX is later desired,
	// extend app.OpenWorkspace / the registry to pass a "continueLatest bool"
	// through to Prepare, then add the flag here.

	// Leading space keeps the password out of history-ignoring shells. The poll
	// budget (iters × interval) is derived from firstConnectDeadline, so it keeps
	// probing until the same deadline the monitor's connect uses before falling
	// through to attach — a serve that binds before the deadline is caught here
	// rather than abandoned early into a dead attach.
	cmd := fmt.Sprintf(serveAndAttachFmt,
		m.password, portStr, opencodeServePollMaxIters, m.serverURL, opencodeServePollIntervalSec, attach)
	return cmd, nil
}

// PaneEnv supplies the exit sentinel's token/URL via the pane shell's process
// environment (the sentinel appended to serveAndAttachFmt references them by name
// so no secret is echoed). Called after Prepare, which creates m.exitListener.
func (m *OpencodeMonitor) PaneEnv() []string {
	return exitPaneEnv(m.exitListener)
}

// Teardown closes the exit listener (the SSE stream is reaped by ctx cancellation,
// not here). Closing before the pane bridge is SIGKILLed — the order OpenWorkspace/
// CloseWorkspace/shutdown enforce — leaves a late exit sentinel nowhere to land.
func (m *OpencodeMonitor) Teardown() error {
	if m.exitListener != nil {
		return m.exitListener.Close()
	}
	return nil
}

// freeLoopbackPort asks the kernel for a free TCP port on 127.0.0.1 and releases
// it. There is a small race between release and `opencode serve` binding it; if
// another process steals the port, serve fails and Start reports StateErrored.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", hooklistener.LoopbackHost+":0")
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
		defer safe.Recover("opencode-monitor")
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

	// Second pump: the exit sentinel. When `opencode attach` exits, the shell POSTs
	// an AgentExit to the exit listener; translate it to a terminal StateExited.
	if m.exitListener == nil {
		return
	}
	go func() {
		defer safe.Recover("opencode-exit-monitor")
		for {
			select {
			case <-ctx.Done():
				return
			case he, ok := <-m.exitListener.Events():
				if !ok {
					return
				}
				if he.Type == hookEventAgentExit {
					m.handleExit(ctx, he.ErrorType)
				}
			}
		}
	}()
}

// handleExit records the terminal StateExited and marks the monitor exited so any
// later SSE frame (the backgrounded `opencode serve` outliving `attach`) is dropped
// by translateSSE/commitSSE/emit. Fires once — a duplicate AgentExit is ignored.
func (m *OpencodeMonitor) handleExit(ctx context.Context, ec string) {
	if !m.markExited() {
		return // duplicate AgentExit — already terminal
	}
	// send directly (not emit) so this terminal frame bypasses the exited guard it
	// just armed; state was already set to StateExited by markExited.
	m.send(ctx, Event{Kind: "state", State: StateExited, Err: exitReason(ec)})
}

// markExited atomically arms the terminal exited guard: under the lock it sets
// exited and StateExited, and reports whether THIS call armed it (false if it was
// already set, so handleExit can ignore a duplicate AgentExit). Keeping the arm in
// one helper means handleExit and the commitSSE/emit re-checks all observe exactly
// the same atomic transition.
func (m *OpencodeMonitor) markExited() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exited {
		return false
	}
	m.exited = true
	m.state = StateExited
	return true
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
// ambient toast); any other idle is a steady idle (StateIdle, no toast).
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
	// reading the prior state here is race-free w.r.t. the write in commitSSE.
	m.mu.Lock()
	// exited is terminal: once the exit sentinel has fired, a straggling
	// session.status frame from the backgrounded `opencode serve` must NOT clobber
	// StateExited back to running/idle. This is a FAST-PATH check only — the switch
	// below runs with NO lock held, so handleExit can still land between here and the
	// state write. commitSSE RE-CHECKS exited under the same lock that writes m.state
	// to close that check-then-act (TOCTOU) window; this early check merely skips the
	// switch work in the common already-exited case.
	if m.exited {
		m.mu.Unlock()
		return
	}
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
			// A busy→idle transition means the agent finished a turn → StateDone
			// so dispatchNotify fires the ambient toast. An idle that does NOT
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
		// A tool-run permission request. Unlike claude (whose PreToolUse hook BLOCKS
		// so perch's reply is the sole answer, gating the ApprovalCard), opencode's
		// `attach` TUI shows its OWN native permission prompt in the same pane and
		// perch cannot suppress it. So perch does NOT own this approval: it emits a
		// PASSIVE attention signal only — StateAwaitingApproval with NO Approval
		// payload. This mirrors question.asked (the user answers in the TUI, perch
		// never replies). Because Approval is nil, the app event pump registers no
		// pending approval and never calls Approve(); dispatchNotify still fires the
		// blocking "Approval needed" notification and the sidebar keeps its distinct
		// amber awaiting-approval glance. Cleared when the agent's next real state
		// (running/idle/done) arrives on the SSE stream. (opencode emits no
		// permission-resolved frame, which is exactly why a perch-owned card would
		// go stale — hence the passive signal.)
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		ev = Event{Kind: "approval", State: StateAwaitingApproval}
	default:
		return
	}
	m.commitSSE(ctx, ev)
}

// commitSSE is the post-switch write half of translateSSE (section B), split out so
// the exited RE-CHECK it performs is directly testable. It records ev.State
// (mutex-guarded, BEFORE emitting, so a reader that observes the event on the
// channel also observes the updated state) and sends ev — but ONLY after
// RE-CHECKING m.exited under the SAME lock that writes m.state.
//
// That re-check closes a check-then-act (TOCTOU) race the single section-A check
// missed: the whole switch runs with no lock held, so handleExit can run ENTIRELY
// between section A's check (which saw exited==false and let the frame through) and
// this write. If it does, it has already set exited, m.state=StateExited, and
// emitted the terminal frame; without this re-check a straggler session.status{busy}
// would then clobber m.state back to StateRunning and emit a running event AFTER the
// exited event — the exact stale-"running" symptom F32 exists to eliminate, and
// permanently stuck (every later frame is dropped once exited is set). Re-checking
// here keeps StateExited terminal: once exited is set, no SSE frame changes m.state
// away from StateExited or emits a non-exited state event. The send happens after
// the lock is released so a full events channel can never block a critical section
// (mirroring emit). opencode emits no Approval payload (approvals are owned by its
// own TUI), so there is no lastTool to track here — unlike claude.
func (m *OpencodeMonitor) commitSSE(ctx context.Context, ev Event) {
	m.mu.Lock()
	if m.exited {
		m.mu.Unlock()
		return
	}
	if ev.State != "" {
		m.state = ev.State
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
	// Once exited is set, StateExited is terminal — a late connect-failure
	// StateErrored (or any other monitor-originated event) must not override it.
	if m.exited {
		m.mu.Unlock()
		return
	}
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

// Approve is a deliberate no-op for opencode. It exists only to satisfy the
// Monitor interface; it is NEVER reached in production for opencode.
//
// opencode advertises Capabilities().Approvals = false, so perch renders no
// ApprovalCard and registers no pending approval for a permission.asked frame
// (app's event pump only records a pending — and only ever calls mon.Approve —
// when evt.Approval != nil, which opencode never emits). The user answers the
// permission in opencode's own `attach` TUI, which owns the reply; perch must
// not POST /permission/:id/reply as well (that would race the TUI and, since
// opencode emits no permission-resolved SSE frame, could leave a stale feel).
// With no pending registered, neither maybeAutoApprove, decideOne, nor
// CloseWorkspace's deny-pending loop can reach this method for an opencode
// workspace. Returning nil keeps the interface honest without touching the wire.
func (m *OpencodeMonitor) Approve(reqID string, d Decision) error {
	_ = reqID
	_ = d
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

// LastApprovalTool always returns "" for opencode: it exists only to satisfy the
// Monitor interface. app.Approve reads it to name a card-answered tool, but perch
// never answers an opencode approval (its TUI owns them), so there is no tool to
// report. claude, which does own its approvals, tracks and returns a real value.
func (m *OpencodeMonitor) LastApprovalTool() string { return "" }
