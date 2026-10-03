// internal/agent/opencode_monitor.go
package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/safe"
)

// OpencodeMonitor drives the opencode agent. opencode has no hook system.
// Instead, perch runs an `opencode serve` HTTP backend (an SSE event
// stream plus a permission-reply endpoint) that this monitor talks to,
// alongside an `opencode attach` TUI in the same pane for the user. Both
// point at the same self-assigned loopback server.
//
// The HTTP/SSE contract is pinned to opencode v1.15.12 source (the
// canonical repo is anomalyco/opencode, which sst/opencode redirects to).
// SSE GET /event emits `data: {"id","type","properties":{…}}` frames
// behind HTTP Basic auth. Approvals are POST /permission/:requestID/reply
// with body {"reply": once|always|reject}. Only manual smoke exercises
// the end-to-end loop against a real opencode binary, because perch's
// test constraints forbid launching a real agent. This is the same gate
// as the WebKit smoke. Everything below is verified against source, not
// a server.
type OpencodeMonitor struct {
	adapter    Adapter
	serverURL  string
	password   string
	events     chan Event
	httpClient *http.Client
	// exitListener is a dedicated loopback listener the shell exit
	// sentinel POSTs to when `opencode attach` exits. opencode has no
	// hook system, so unlike claude, this monitor cannot reuse an
	// existing listener. Prepare creates exitListener, a second goroutine
	// in Start drains it, and Teardown closes it.
	exitListener *hooklistener.Listener
	mu           sync.Mutex
	state        State
	// exited is set once the exit sentinel reports the agent is gone.
	// This field makes StateExited terminal. The backgrounded `opencode
	// serve` can outlive `attach` and keep pushing session.status SSE
	// frames that would otherwise clobber StateExited back to running or
	// idle. So translateSSE and emit both return early once exited is
	// set. Guarded by mu.
	exited bool
	// turnRunning records that the agent is in an active turn. perch sets
	// it on session.status busy and on question.replied. The terminating
	// idle reports StateDone, the turn-done ✓ and the ambient "Turn
	// complete" toast, only when turnRunning is set, INSTEAD of testing
	// the prior m.state, which a mid-turn permission.asked or
	// question.asked overwrites. Guarded by mu.
	turnRunning bool
	// children holds the ids of subagent (task tool) sessions, learned from
	// session.created/session.updated frames whose info.parentID is set.
	// /event streams every session of the instance; a child's status, idle
	// and error frames must not drive the workspace state or be persisted
	// as the resumable session id (AGT-6). Guarded by mu.
	children map[string]bool
	// stopSSE cancels the SSE pump. handleExit calls it, so an exited
	// session stops dialing a server that is gone (AGT-14). Guarded by mu.
	stopSSE context.CancelFunc
}

const (
	// loopbackServerURLFmt is the format string that builds the opencode
	// serve URL from a free loopback port number. It uses
	// hooklistener.LoopbackHost as the single source of truth for the
	// loopback address.
	loopbackServerURLFmt = "http://" + hooklistener.LoopbackHost + ":%d"

	// opencodeServePollInterval is the sleep between readiness-poll
	// iterations in the launch incantation. The poll's ITERATION COUNT
	// derives from firstConnectDeadline divided by this interval (see
	// opencodeServePollMaxIters), so the shell poll's total budget equals
	// the monitor's connect deadline.
	opencodeServePollInterval = 200 * time.Millisecond

	// randomTokenBytes is the number of cryptographically-random bytes
	// perch uses to generate the Basic-auth password for opencode serve.
	randomTokenBytes = 16

	// opencodeBasicAuthUser is the fixed HTTP Basic-auth username
	// opencode's server expects (v1.15.12 server/auth.ts).
	opencodeBasicAuthUser = "opencode"

	// firstConnectDeadline bounds BOTH the monitor's initial connection to
	// the opencode server, before it reports StateErrored, AND, through
	// the derived poll count below, the readiness poll baked into the
	// launch incantation. Deriving both bounds from ONE budget closes the
	// attach-timing gap. Before the fix, the poll gave up at about 10
	// seconds while this deadline was 30 seconds. So a serve that bound
	// between 10 and 30 seconds left the poll to `exec opencode attach`
	// into a not-yet-listening server. attach makes a single,
	// non-retrying connection and exits 1, which killed the pane with no
	// error, until this deadline finally fired StateErrored about 20
	// seconds later. With one budget, the poll keeps probing until the
	// connect deadline. So a serve that binds any time before the
	// deadline is caught, and attach runs against a live server. If the
	// serve never binds, the pane dies and StateErrored fires at the same
	// time.
	firstConnectDeadline = 30 * time.Second

	// sseRetryBackoffMin and sseRetryBackoffMax bound the sleep between SSE
	// (re)connect attempts. Before the first connection the monitor polls at
	// the minimum, so it notices the server coming up quickly; after a drop
	// the delay doubles up to the maximum (AGT-14).
	sseRetryBackoffMin = 500 * time.Millisecond
	sseRetryBackoffMax = 5 * time.Second

	// sseReaderBuf is the SSE line reader's buffer size. Longer lines are
	// accumulated up to sseMaxLineBytes.
	sseReaderBuf = 64 * 1024

	// sseMaxLineBytes caps one SSE line. A longer line (session.diff carries
	// whole patches) is SKIPPED instead of aborting the stream, which would
	// lose every frame published until the reconnect (AGT-13).
	sseMaxLineBytes = 1024 * 1024

	// sseIdleTimeout drops and reconnects a stream that delivered nothing,
	// not even opencode's 10-second server.heartbeat, for this long.
	sseIdleTimeout = 30 * time.Second

	// resyncTimeout bounds the GET /session/status resync after a reconnect.
	resyncTimeout = 2 * time.Second

	// serveAndAttachFmt is the shell incantation Prepare returns (before the
	// login-shell wrapper). It backgrounds `opencode serve`, polls until the
	// port is listening, runs `opencode attach`, and then kills the serve it
	// started. The arguments, in order, are: bin, portStr, pollMaxIters,
	// serverURL, pollIntervalSec, attach.
	//
	// attach is NOT exec'd: when it exits (/exit or a crash), the subshell
	// kills the backgrounded serve and exits with attach's status, which the
	// exit sentinel reports. The HUP/TERM trap covers the pane being closed
	// during the poll or while attach runs: it kills serve and exits, rather
	// than polling on and attaching to a hung-up tty. Before, serve was
	// orphaned to init and kept its port, memory and LSP children until
	// logout (AGT-4).
	//
	// The Basic-auth password is NOT in the line: it travels through PaneEnv
	// as PERCH_OPENCODE_PASSWORD, and the subshell exports it BY REFERENCE
	// as OPENCODE_SERVER_PASSWORD, together with the pinned username. So the
	// shell neither echoes the secret nor records it in history (AGT-12),
	// and the export runs AFTER the login rc files and is immune to a
	// `perch reload` overlay (which never touches PERCH_* keys), so a user's
	// own OPENCODE_SERVER_* values cannot make the monitor's requests fail
	// with 401 (AGT-11). The poll count and interval are DERIVED from
	// firstConnectDeadline (see the var block below), so the poll budget and
	// the monitor's connect deadline stay in lockstep. The line contains no
	// single quote and no backslash, so wrapForLoginShell can run it under
	// `sh -c` for non-POSIX login shells.
	serveAndAttachFmt = "( export " + envOpencodePassword + `="$` + envPerchOpencodePassword + `" ` +
		envOpencodeUsername + "=" + opencodeBasicAuthUser + ";" +
		" %s serve --port %s --hostname " + hooklistener.LoopbackHost + " >/dev/null 2>&1 & sp=$!;" +
		` trap "kill $sp 2>/dev/null; exit 129" HUP TERM;` +
		" i=0; while [ $i -lt %s ]; do curl -s -o /dev/null %s && break; i=$((i+1)); sleep %s; done;" +
		" %s; ec=$?; kill $sp 2>/dev/null; exit $ec )" + exitSentinel

	// Environment variables `opencode serve` and `opencode attach` read for
	// the server's Basic auth (v1.15.12 server/auth.ts).
	envOpencodePassword = "OPENCODE_SERVER_PASSWORD"
	envOpencodeUsername = "OPENCODE_SERVER_USERNAME"
	// envPerchOpencodePassword carries the generated password in the pane
	// env; the launch line exports it under envOpencodePassword.
	envPerchOpencodePassword = "PERCH_OPENCODE_PASSWORD"
)

// opencodeServePollMaxIters and opencodeServePollIntervalSec are the
// readiness-poll parameters interpolated into serveAndAttachFmt as shell
// tokens. Both are DERIVED from firstConnectDeadline and
// opencodeServePollInterval, so the shell poll's total budget (iterations
// times interval) equals the monitor's connect deadline: one budget, no
// attach-timing gap (see firstConnectDeadline). They are vars, not
// consts, only because they are computed. Treat them as read-only.
var (
	opencodeServePollMaxIters    = strconv.Itoa(int(firstConnectDeadline / opencodeServePollInterval))
	opencodeServePollIntervalSec = strconv.FormatFloat(opencodeServePollInterval.Seconds(), 'f', -1, 64)
)

func newOpencodeMonitor(a Adapter) *OpencodeMonitor {
	// serverURL and password stay empty here. Prepare self-assigns them,
	// so the free port is grabbed as late as possible, keeping the
	// bind-to-serve race window as small as possible.
	return &OpencodeMonitor{adapter: a, events: make(chan Event, monitorEventChanBuf), httpClient: &http.Client{},
		children: map[string]bool{}}
}

// NewOpencodeMonitorWithServer injects a server URL and password instead
// of self-assigning them. Test-only: it lets a test point the monitor at
// an httptest server. Production code goes through newOpencodeMonitor,
// then Prepare.
func NewOpencodeMonitorWithServer(a Adapter, serverURL, pw string) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, serverURL: serverURL, password: pw,
		events: make(chan Event, monitorEventChanBuf), httpClient: &http.Client{},
		children: map[string]bool{}}
}

// NewOpencodeMonitorWithServerAndExitListener injects both the SSE server
// credentials and the exit-sentinel listener. Test-only: it lets a test
// POST an AgentExit to the same loopback listener the shell sentinel
// would hit, then feed a later SSE frame to prove the exited guard drops
// it, all without a real pane or pty. Production code goes through
// newOpencodeMonitor, then Prepare, which self-assigns both.
func NewOpencodeMonitorWithServerAndExitListener(a Adapter, serverURL, pw string, exitLn *hooklistener.Listener) *OpencodeMonitor {
	m := NewOpencodeMonitorWithServer(a, serverURL, pw)
	m.exitListener = exitLn
	return m
}

func (m *OpencodeMonitor) Events() <-chan Event { return m.events }
func (m *OpencodeMonitor) Capabilities() Caps {
	// Approvals is false, because opencode's own `attach` TUI owns the
	// permission prompt (Allow once / Allow always / Reject), and perch
	// cannot suppress it. So perch does NOT render its own ApprovalCard;
	// the frontend gates the card on caps.approvals. perch surfaces
	// permission.asked only as a PASSIVE attention signal
	// (StateAwaitingApproval plus the blocking notification), cleared by
	// permission.replied, mirroring question.asked: the user answers in the
	// TUI, and perch never replies, which avoids a double prompt. claude is
	// DIFFERENT: its PermissionRequest hook BLOCKS, and perch's reply
	// answers claude's prompt, so claude keeps Approvals: true.
	return Caps{Approvals: false, Attention: true}
}

// Prepare self-assigns a free loopback port and a random Basic-auth
// password, unless a test injected a server, and returns the shell
// incantation the pane runs.
//
// The incantation backgrounds an `opencode serve` on that port, polls
// until the port is listening, then exec's `opencode attach <url>`, the
// user's TUI. The poll is required because `opencode attach` makes a
// single, non-retrying connection and exits 1 if the server is not yet
// up. This is verified in opencode v1.15.12 attach.ts, function
// validateSession. serve's output is discarded, so it never corrupts the
// TUI. A server that never binds surfaces as StateErrored from the
// monitor's bounded connect in Start, not from pane noise.
//
// The password travels through the pane's process environment (PaneEnv),
// never argv and never the typed line, so it appears neither in `ps`, nor
// on screen, nor in shell history.
//
// resumeID, when set, becomes `--session <id>` on attach, so opencode
// resumes that session. perch does not pass a model flag. `opencode
// attach` accepts no --model or --agent flag; those flags live only on
// the standalone TUI command, which cannot drive the serve-plus-attach
// HTTP backend perch needs. So model selection is the harness's job.
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

	// Stand up the exit listener the shell sentinel POSTs to when attach
	// exits. Unlike claude, opencode has no hook system to reuse, so this
	// is a dedicated listener. This step is skipped when a test injected
	// one.
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

	bin := launchBin(m.adapter, "opencode")
	attach := bin + " attach " + m.serverURL
	if resumeID != "" && m.adapter != nil {
		// resumeID uses the charset [A-Za-z0-9_-]. app.validateSessionID
		// validates it in the event pump, before perch persists any session
		// id to the registry. So plain concatenation is safe as a single
		// shell token. opencode also accepts --continue to resume the most
		// recent session; perch does not wire it, because an empty resumeID
		// means a fresh workspace, not "continue".
		attach += " " + strings.Join(m.adapter.ResumeArgs(resumeID), " ")
	}

	// The leading space keeps the line out of history-ignoring shells. The
	// poll budget (iterations times interval) derives from
	// firstConnectDeadline, so it keeps probing until the same deadline the
	// monitor's connect uses, before it falls through to attach.
	line := " " + fmt.Sprintf(serveAndAttachFmt,
		bin, portStr, opencodeServePollMaxIters, m.serverURL, opencodeServePollIntervalSec, attach)
	return wrapForLoginShell(line), nil
}

// PaneEnv supplies, through the pane shell's process environment, the exit
// sentinel's token and URL and the opencode server's Basic-auth password
// (as PERCH_OPENCODE_PASSWORD, which the launch line exports by reference;
// see serveAndAttachFmt). The shell echoes no secret. Call PaneEnv after
// Prepare.
func (m *OpencodeMonitor) PaneEnv() []string {
	env := exitPaneEnv(m.exitListener)
	if m.password != "" {
		env = append(env, envPerchOpencodePassword+"="+m.password)
	}
	return env
}

// Teardown closes the exit listener. ctx cancellation reaps the SSE stream
// elsewhere, not here. app.OpenWorkspace (on reopen), CloseWorkspace and
// shutdown call Teardown BEFORE they close the pane bridge: closing the
// exit listener first means a late exit sentinel from the dying shell has
// nowhere to land, so it cannot raise a spurious "Agent exited" on a
// reopen or close (AGT-20).
func (m *OpencodeMonitor) Teardown() error {
	if m.exitListener != nil {
		return m.exitListener.Close()
	}
	return nil
}

// freeLoopbackPort asks the kernel for a free TCP port on 127.0.0.1 and
// releases it. There is a small race between the release and `opencode
// serve` binding the port. If another process steals the port, serve
// fails, and Start reports StateErrored.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", hooklistener.LoopbackHost+":0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// randomToken returns randomTokenBytes cryptographically-random bytes as
// hex. Hex is shell-safe and needs no quoting.
func randomToken() (string, error) {
	b := make([]byte, randomTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// authHeader returns the HTTP Basic auth header value opencode expects.
// The username is opencodeBasicAuthUser (v1.15.12 server/auth.ts). The
// password is the one this monitor generated and exported to `opencode
// serve`.
func (m *OpencodeMonitor) authHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(opencodeBasicAuthUser+":"+m.password))
}

// Start consumes the opencode SSE event stream until ctx is cancelled.
// The first connection is bounded: if the server never comes up within
// the deadline, the monitor emits StateErrored once and stops. Once
// connected at least once, a dropped stream reconnects silently, so a
// normal mid-session reconnect never flaps the UI to errored. The
// goroutine exits on ctx cancellation: every wait selects on ctx.Done(),
// and every HTTP request is ctx-scoped. So closing a workspace, which
// cancels ctx, reaps the goroutine with no leak.
func (m *OpencodeMonitor) Start(ctx context.Context) {
	sseCtx, stop := context.WithCancel(ctx)
	m.mu.Lock()
	m.stopSSE = stop
	exited := m.exited
	m.mu.Unlock()
	if exited {
		stop()
	}
	go func() {
		defer safe.Recover("opencode-monitor")
		defer stop()
		ctx := sseCtx
		deadline := time.Now().Add(firstConnectDeadline)
		connectedOnce := false
		backoff := sseRetryBackoffMin

		for {
			if ctx.Err() != nil {
				return
			}
			if m.streamOnce(ctx, connectedOnce) {
				connectedOnce = true
				backoff = sseRetryBackoffMin
			} else if connectedOnce {
				backoff = min(backoff*2, sseRetryBackoffMax)
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
			case <-time.After(backoff):
			}
		}
	}()

	// Second pump: the exit sentinel. When `opencode attach` exits, the
	// shell POSTs an AgentExit event to the exit listener. Translate that
	// event into a terminal StateExited.
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

// handleExit records the terminal StateExited and marks the monitor
// exited, so translateSSE, commitSSE, and emit drop any later SSE frame,
// for example one from the backgrounded `opencode serve` that outlives
// `attach`. handleExit fires once; it ignores a duplicate AgentExit.
func (m *OpencodeMonitor) handleExit(ctx context.Context, ec string) {
	if !m.markExited() {
		return // duplicate AgentExit, already terminal
	}
	// Stop the SSE pump: the session is over, and the server is going away
	// with attach (AGT-14).
	m.mu.Lock()
	stop := m.stopSSE
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	// Send directly, not through emit, so this terminal frame bypasses
	// the exited guard it just armed. markExited already set state to
	// StateExited.
	m.send(ctx, Event{Kind: "state", State: StateExited, Err: exitReason(ec)})
}

// markExited atomically arms the terminal exited guard. Under the lock it
// sets exited and StateExited, and reports whether THIS call armed it:
// false if the guard was already set, so handleExit can ignore a
// duplicate AgentExit. Keeping the arm in one helper means handleExit and
// the commitSSE and emit re-checks all observe exactly the same atomic
// transition.
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

// streamOnce opens GET /event and consumes frames until the stream ends,
// errors, or stays silent for sseIdleTimeout. It returns true if it
// connected successfully (HTTP 200), and false on a dial failure or a
// non-200 response. This lets the caller tell "never connected", which
// eventually becomes errored, from "stream dropped", which triggers a
// reconnect. On a reconnect (resync), it first re-reads the session
// statuses, because opencode replays nothing on connect, so a frame lost
// in the gap (typically the final idle) would otherwise leave the state
// stuck.
func (m *OpencodeMonitor) streamOnce(ctx context.Context, resync bool) (connected bool) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, m.serverURL+"/event", nil)
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
	// Liveness: opencode sends server.heartbeat every 10 s. A stream that
	// delivers nothing for sseIdleTimeout is dead; cancel it to reconnect.
	idle := time.AfterFunc(sseIdleTimeout, cancel)
	defer idle.Stop()
	if resync {
		m.resync(ctx)
	}
	br := bufio.NewReaderSize(resp.Body, sseReaderBuf)
	var long []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		idle.Reset(sseIdleTimeout)
		if err == bufio.ErrBufferFull {
			// A line longer than the buffer: accumulate it, or skip it once
			// it exceeds sseMaxLineBytes, instead of aborting the stream.
			if !skipping {
				if len(long)+len(chunk) > sseMaxLineBytes {
					skipping, long = true, long[:0]
				} else {
					long = append(long, chunk...)
				}
			}
			continue
		}
		if err != nil {
			return true
		}
		if skipping {
			skipping = false
			continue
		}
		line := chunk
		if len(long) > 0 {
			long = append(long, chunk...)
			line = long
		}
		m.handleSSELine(ctx, line)
		long = long[:0]
	}
}

// ssePrefilter lists byte patterns of the only frame types the monitor acts
// on. Frames without any of them, chiefly the high-rate message.part.*
// streaming deltas, are skipped without a JSON decode (AGT-13).
var ssePrefilter = [][]byte{[]byte(`"session.`), []byte(`"question.`), []byte(`"permission.`)}

// handleSSELine processes one raw SSE line. SSE data lines are
// `data:<json>`; the spec allows one optional space after the colon. Other
// lines (event:, id:, comments, blank separators) are ignored. line is only
// valid during the call.
func (m *OpencodeMonitor) handleSSELine(ctx context.Context, line []byte) {
	line = bytes.TrimSpace(line)
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return
	}
	for _, p := range ssePrefilter {
		if bytes.Contains(payload, p) {
			m.translateSSE(ctx, payload)
			return
		}
	}
}

// resync reconciles the state with GET /session/status after a reconnect.
// The endpoint lists only non-idle sessions ({id: {type: busy|retry}}).
// If no root session is busy but perch still thinks a turn is running, the
// final idle was lost: apply it. If a root session is busy while perch
// reads idle or done, the busy was lost: apply that.
func (m *OpencodeMonitor) resync(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, resyncTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, m.serverURL+"/session/status", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", m.authHeader())
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, sseMaxLineBytes)).Decode(&statuses) != nil {
		return
	}
	busy := false
	for id, st := range statuses {
		if (st.Type == "busy" || st.Type == "retry") && !m.isChild(id) {
			busy = true
		}
	}
	m.mu.Lock()
	cur, running := m.state, m.turnRunning
	m.mu.Unlock()
	switch {
	case !busy && (running || cur == StateRunning):
		if st := m.idleState(); st != "" {
			m.commitSSE(ctx, Event{Kind: "state", State: st})
		}
	case busy && (cur == StateIdle || cur == StateDone || cur == ""):
		m.markTurnRunning()
		m.commitSSE(ctx, Event{Kind: "state", State: StateRunning})
	}
}

// isChild reports whether id is a known subagent session.
func (m *OpencodeMonitor) isChild(id string) bool {
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.children[id]
}

// sseEnvelope is the opencode bus-event wire shape. Every SSE frame is
// {"id","type","properties":{…}}, with the typed payload nested under
// properties (v1.15.12 server .../handlers/event.ts).
type sseEnvelope struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// markTurnRunning records that a turn is in progress, so a later idle
// reports StateDone. perch calls it on the busy status and on
// question.replied, when the turn resumes. perch deliberately does NOT
// call it for the passive attention signals (permission.asked and
// question.asked), so a mid-turn approval or question does not erase the
// fact that a turn is running.
func (m *OpencodeMonitor) markTurnRunning() {
	m.mu.Lock()
	m.turnRunning = true
	m.mu.Unlock()
}

// takeTurnRunning reports whether a turn was in progress, and clears the
// flag, so the busy-to-idle edge yields StateDone exactly once. A
// duplicate or steady idle, or an idle at connect, reads false and stays
// StateIdle, firing no spurious toast.
func (m *OpencodeMonitor) takeTurnRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.turnRunning
	m.turnRunning = false
	return r
}

// idleState decides the lifecycle state an opencode idle signal should
// emit, or "" for a no-op. It reads and clears the turn-in-progress flag
// AND the current state under one lock, so the decision is atomic
// against a concurrent handleExit.
//
// opencode's SessionStatus.set() is LEVEL-triggered with no dedup, and a
// single idle transition publishes BOTH session.status{idle} AND the
// deprecated session.idle alias (v1.15.12 status.ts). So redundant idle
// frames are all protocol-legal: the alias sibling, a repeated
// status{idle}, or a status snapshot replayed on a silent SSE reconnect.
// NONE of them may revert a completed turn:
//   - turn flag set: the busy-to-idle edge completed a turn, so this returns StateDone and clears the flag.
//   - flag clear, already StateDone: this returns "" (no-op). A redundant idle keeps the ✓ and the "Turn complete" notification, instead of clobbering them to a steady StateIdle. The frontend's done-to-idle edge would additionally treat a steady StateIdle as "resolved" and drop the notification. This makes opencode's done sticky until the next turn, matching claude, whose Stop hook holds StateDone the same way.
//   - flag clear, not already done, for example idle at connect or after a reject: this returns a steady StateIdle.
func (m *OpencodeMonitor) idleState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.turnRunning {
		m.turnRunning = false
		return StateDone
	}
	// done and errored are sticky until the next turn. opencode publishes
	// session.error and IMMEDIATELY session.status{idle} (processor.ts
	// halt()), so an idle after an error must not erase the red errored
	// state within milliseconds (AGT-5).
	if m.state == StateDone || m.state == StateErrored {
		return ""
	}
	return StateIdle
}

func (m *OpencodeMonitor) translateSSE(ctx context.Context, data []byte) {
	var env sseEnvelope
	if json.Unmarshal(data, &env) != nil {
		return
	}
	// translateSSE runs serially from the single SSE-reader goroutine, so
	// the turn-in-progress flag, read and cleared under mu in
	// takeTurnRunning, is race-free against commitSSE's state write.
	m.mu.Lock()
	// exited is terminal. Once the exit sentinel has fired, a straggling
	// session.status frame from the backgrounded `opencode serve` must
	// NOT clobber StateExited back to running or idle. This is a
	// FAST-PATH check only. The switch below runs with NO lock held, so
	// handleExit can still land between here and the state write.
	// commitSSE RE-CHECKS exited under the same lock that writes
	// m.state, to close that check-then-act (TOCTOU) window. This early
	// check merely skips the switch work in the common already-exited
	// case.
	if m.exited {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	var ev Event
	switch env.Type {
	case "session.created", "session.updated":
		// Learn subagent (task tool) sessions: their info carries parentID
		// (opencode's own TUI tells them apart the same way).
		var p struct {
			SessionID string `json:"sessionID"`
			Info      struct {
				ID       string `json:"id"`
				ParentID string `json:"parentID"`
			} `json:"info"`
		}
		if json.Unmarshal(env.Properties, &p) != nil || p.Info.ParentID == "" {
			return
		}
		id := p.Info.ID
		if id == "" {
			id = p.SessionID
		}
		if id != "" {
			m.mu.Lock()
			m.children[id] = true
			m.mu.Unlock()
		}
		return
	case "session.status":
		// The session-level status is the authoritative idle/running
		// signal. Crucially, it is also the only DEFAULT-emitted event
		// that carries the sessionID; the session.next.step.* events
		// that also carry it are gated behind
		// OPENCODE_EXPERIMENTAL_EVENT_SYSTEM. Capturing sessionID here
		// is what makes resume work without the experimental flag: the
		// app persists it as LastSessionID and passes it back as
		// `attach --session <id>`. status.type is one of idle, busy, or
		// retry (v1.15.12 session/status.ts). retry is transient, so it
		// causes no transition.
		var p struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		if m.isChild(p.SessionID) {
			// A subagent's busy/idle must not complete the parent's turn,
			// nor become the persisted resume id (AGT-6).
			return
		}
		switch p.Status.Type {
		case "idle":
			// A busy-to-idle transition means the agent finished a turn.
			// This becomes StateDone, so dispatchNotify fires the
			// ambient toast and the sidebar shows the ✓. idleState
			// reads the turn-in-progress flag, set on busy and
			// preserved across a mid-turn permission.asked or
			// question.asked, instead of the prior m.state, which a
			// passive attention signal overwrites. Without that, any
			// turn that used a tool would degrade to a steady
			// StateIdle, with no ✓ and no toast: the "opencode never
			// shows done" bug. A redundant idle after the turn already
			// completed, for example the deprecated alias, a repeated
			// status{idle}, or a reconnect snapshot, with the flag
			// clear and the state already StateDone, is a NO-OP.
			// idleState returns "", so it never reverts the ✓.
			st := m.idleState()
			if st == "" {
				return
			}
			ev = Event{Kind: "state", State: st, SessionID: p.SessionID}
		case "busy":
			m.markTurnRunning()
			ev = Event{Kind: "state", State: StateRunning, SessionID: p.SessionID}
		default:
			return
		}
	case "session.idle":
		// session.idle is a deprecated alias of
		// session.status{type:idle}. opencode's SessionStatus.set()
		// publishes BOTH for one idle transition (v1.15.12 status.ts: it
		// calls Bus.publish(Event.Status,...), then
		// Bus.publish(Event.Idle,...)). The same idle rule applies
		// through idleState(). If this alias is ITSELF the
		// turn-completing edge, for example an alias-only build with the
		// flag still set, idleState reports StateDone. If it is the
		// redundant sibling after session.status{idle}, with the flag
		// clear and the state already StateDone, idleState returns "",
		// and this code suppresses the alias so it cannot clobber the
		// done marker.
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(env.Properties, &p)
		if m.isChild(p.SessionID) {
			return
		}
		st := m.idleState()
		if st == "" {
			return
		}
		ev = Event{Kind: "state", State: st}
	case "session.error":
		// session.error is opencode's default-emitted error event.
		// session.next.step.failed also carries errors, but it is gated
		// behind OPENCODE_EXPERIMENTAL_EVENT_SYSTEM, which perch never
		// sets. So session.error is the only error signal perch can
		// rely on. Its properties are {sessionID?, error}, where error
		// is a bare string or {message,name} (v1.15.12
		// session-event.ts Error).
		var p struct {
			SessionID string          `json:"sessionID"`
			Error     json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(env.Properties, &p)
		if m.isChild(p.SessionID) {
			return
		}
		switch errName(p.Error) {
		case "MessageAbortedError":
			// The user interrupted (Esc). Not an error: drop the turn, so
			// the idle that follows reads as a steady idle, with no "Turn
			// complete" and no blocking "Agent error" (AGT-5).
			_ = m.takeTurnRunning()
			return
		case "ContextOverflowError":
			// opencode auto-compacts and continues the same turn
			// (processor.ts); keep the turn running (AGT-5).
			return
		}
		_ = m.takeTurnRunning() // an errored turn must not phantom-complete on a later idle
		// The session id is not reported here: an error frame must not
		// redirect the persisted resume id.
		ev = Event{Kind: "state", State: StateErrored, Err: errMessage(p.Error)}
	case "question.asked":
		// question.asked means the agent asks the USER a free-form
		// choice. This is distinct from permission.asked, a tool-run
		// approval. Like claude's AskUserQuestion, this is an attention
		// SIGNAL: the user answers in opencode's own attach TUI in the
		// same pane, so perch only surfaces StateAwaitingInput and does
		// NOT reply on POST /question/:id/reply. The TUI client owns
		// the reply. question.replied and question.rejected below
		// clear the signal. (v1.15.12 question/index.ts: opencode
		// emits question.asked unconditionally, with no flag.)
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(env.Properties, &p)
		sid := p.SessionID
		if m.isChild(sid) {
			// A subagent's question still needs the user, so the signal
			// stays, but the child id must not become the persisted resume
			// id (AGT-6).
			sid = ""
		}
		ev = Event{Kind: "question", State: StateAwaitingInput, SessionID: sid}
	case "question.replied", "question.rejected":
		// The question was resolved in the TUI. Clear the
		// awaiting-input signal. replied means the agent resumes
		// working (StateRunning), so the turn is still in progress.
		// rejected means the turn is abandoned: clear the
		// turn-in-progress flag and fall back to a steady StateIdle,
		// never StateDone, so no spurious "Turn complete" toast fires,
		// and a later idle does not phantom-complete the turn.
		if env.Type == "question.replied" {
			m.markTurnRunning()
			ev = Event{Kind: "state", State: StateRunning}
		} else {
			_ = m.takeTurnRunning()
			ev = Event{Kind: "state", State: StateIdle}
		}
	case "permission.asked":
		// permission.asked is a tool-run permission request. opencode's
		// `attach` TUI shows its OWN native permission prompt in the same
		// pane, and perch cannot suppress it. So perch emits a PASSIVE
		// attention signal only: StateAwaitingApproval with NO Approval
		// payload, so the app registers no pending approval and never calls
		// Approve(). permission.replied below clears it.
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		ev = Event{Kind: "approval", State: StateAwaitingApproval}
	case "permission.replied":
		// The user answered the permission in the TUI (v1.15.12
		// permission/index.ts: {sessionID, requestID, reply}). Clear the
		// amber signal now rather than at the next step's busy, which can be
		// minutes away for a long tool run (AGT-10). On once/always the turn
		// flag is kept, so the turn's final idle still reports done. A
		// reject stops opencode's loop, so it ends the turn: the idle that
		// follows is a steady idle, not a "Turn complete". A reject with
		// feedback continues, and its next busy re-arms the turn.
		var p struct {
			Reply string `json:"reply"`
		}
		if json.Unmarshal(env.Properties, &p) != nil {
			return
		}
		if p.Reply == "reject" {
			_ = m.takeTurnRunning()
		}
		m.mu.Lock()
		awaiting := m.state == StateAwaitingApproval
		m.mu.Unlock()
		if !awaiting {
			return
		}
		if p.Reply == "reject" {
			ev = Event{Kind: "state", State: StateIdle}
		} else {
			ev = Event{Kind: "state", State: StateRunning}
		}
	default:
		return
	}
	m.commitSSE(ctx, ev)
}

// commitSSE is the post-switch write half of translateSSE (section B). It
// is split out, so the exited RE-CHECK it performs is directly testable.
// It records ev.State, mutex-guarded, BEFORE emitting, so a reader that
// observes the event on the channel also observes the updated state, and
// sends ev, but ONLY after RE-CHECKING m.exited under the SAME lock that
// writes m.state.
//
// That re-check closes a check-then-act (TOCTOU) race the single
// section-A check misses. The whole switch runs with no lock held, so
// handleExit can run ENTIRELY between section A's check, which saw
// exited==false and let the frame through, and this write. If handleExit
// does run there, it has already set exited, set m.state to StateExited,
// and emitted the terminal frame. Without this re-check, a straggler
// session.status{busy} would then clobber m.state back to StateRunning
// and emit a running event AFTER the exited event: the exact
// stale-"running" symptom F32 exists to eliminate, and permanently stuck,
// because every later frame is dropped once exited is set. Re-checking
// here keeps StateExited terminal: once exited is set, no SSE frame
// changes m.state away from StateExited or emits a non-exited state
// event. The send happens after the lock is released, so a full events
// channel can never block a critical section, mirroring emit. opencode
// emits no Approval payload, because its own TUI owns approvals, so
// there is no lastTool to track here, unlike claude.
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

// errMessage extracts a human-readable string from a session.error error
// payload. opencode sends a NamedError toObject(), {name, data:{message}}
// (core/util/error.ts), and older builds a bare string or {message,name}.
// The message wins over the class name, so the user reads "rate limited"
// rather than "APIError" (AGT-5).
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
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &o) == nil {
		for _, v := range []string{o.Data.Message, o.Message, o.Name} {
			if v != "" {
				return v
			}
		}
	}
	return string(raw)
}

// errName returns a session.error payload's NamedError class name, or "".
func errName(raw json.RawMessage) string {
	var o struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return ""
	}
	return o.Name
}

// emit tracks state, then sends. perch uses it for monitor-originated
// lifecycle events, for example the connect-failure StateErrored, that
// do not come from an SSE frame.
func (m *OpencodeMonitor) emit(ctx context.Context, ev Event) {
	m.mu.Lock()
	// Once exited is set, StateExited is terminal. A late connect-failure
	// StateErrored, or any other monitor-originated event, must not
	// override it.
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

// Approve is a deliberate no-op for opencode. It exists only to satisfy
// the Monitor interface, and is never reached in production: opencode
// advertises Capabilities().Approvals = false and never emits an Approval
// payload, so the app registers no pending approval for it. The user
// answers the permission in opencode's own `attach` TUI, which owns the
// reply; a second reply from perch would race it.
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

// LastApprovalTool always returns "" for opencode. It exists only to
// satisfy the Monitor interface. app.Approve reads it to name a
// card-answered tool, but perch never answers an opencode approval,
// because its TUI owns them, so there is no tool to report. claude, which
// does own its approvals, tracks and returns a real value.
func (m *OpencodeMonitor) LastApprovalTool() string { return "" }
