// internal/hooklistener/listener.go
package hooklistener

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miniature-pug/perch/internal/safe"
)

const (
	// LoopbackHost is the single source of truth for the loopback address. The
	// hook listener, and any package that binds or connects to the same
	// loopback interface, use this address: 127.0.0.1 with an ephemeral port.
	// It is never a public interface.
	LoopbackHost = "127.0.0.1"
	// listenerTokenBytes is the number of random bytes in the auth token.
	listenerTokenBytes = 32
	// hookEventChanBuf is the buffer size of the hook event channel.
	hookEventChanBuf = 64
	// reqIDBytes is the number of random bytes in each per-request ID. 16 bytes
	// (128 bits) make a birthday collision between request IDs practically
	// impossible.
	reqIDBytes = 16
	// maxHookBodyBytes caps the hook request body at 1 MiB, so an over-large
	// POST cannot exhaust memory. A real hook payload is far smaller.
	maxHookBodyBytes = 1 << 20
	// The HTTP server timeouts limit how long one connection can occupy the
	// listener. The limits close the slowloris risk of an unbounded server. The
	// values are generous for real hook traffic (small, local POSTs), but they
	// stay finite.
	//
	// WriteTimeout stays unset on purpose. Go arms the write deadline at the end
	// of the request-header read, and the deadline then covers the whole
	// ServeHTTP lifetime. A finite WriteTimeout would abort a PreToolUse
	// approval while it waits for the user's decision. This wait is a human
	// "think time", and it can rightly exceed any fixed bound.
	// ReadHeaderTimeout and ReadTimeout still close the slowloris risk from slow
	// header or body reads. The handler's own r.Context() cancellation bounds
	// the blocking-response phase instead, on a per-request basis.
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 10 * time.Second
	serverIdleTimeout       = 60 * time.Second
)

// Hook event names the listener treats specially.
const (
	// EventPermissionRequest is the one BLOCKING hook: claude fires it only
	// when it would show its own permission dialog, and waits for the hook's
	// decision. The handler parks the request until Decide supplies a
	// verdict, or until the client goes away.
	EventPermissionRequest = "PermissionRequest"
	// EventRequestCancelled is synthesized by the listener (claude never sends
	// it). It is delivered on Events() when a parked PermissionRequest ends
	// WITHOUT a verdict: the hook's curl was killed because the user answered
	// claude's own dialog, pressed Esc, or the hook timed out. ReqID names the
	// request, so the consumer can retract its approval card.
	EventRequestCancelled = "PermissionRequestCancelled"
	// EventAgentExit is the hook_event_name of the shell exit sentinel's
	// report that the agent process exited. The sentinel sends it as a POST
	// to the hook URL with the exit code in the AgentExitParam query
	// parameter (a JSON body with this event name is also accepted).
	EventAgentExit = "AgentExit"
	// AgentExitParam is the query parameter carrying the agent's exit code.
	AgentExitParam = "agent_exit"
)

// sanitizeExitCode keeps only the leading decimal digits of a shell exit
// code, capped at 8 characters, so a malformed query value never reaches
// the UI verbatim.
func sanitizeExitCode(s string) string {
	n := 0
	for n < len(s) && n < 8 && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return s[:n]
}

// Decision is the verdict for a parked PermissionRequest.
type Decision struct {
	Allow  bool `json:"allow"`
	Always bool `json:"always"`
	// Abstain answers the hook with NO decision (an empty 200 body), so
	// claude continues its normal permission flow and shows its own dialog.
	// Allow and Always are ignored when Abstain is set.
	Abstain bool `json:"abstain"`
}

type HookEvent struct {
	Type           string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ErrorType      string          `json:"error_type"`
	// Source is SessionStart's trigger: startup, resume, clear, compact, or
	// fork.
	Source string `json:"source"`
	// NotificationType is the Notification hook's type, e.g. idle_prompt.
	NotificationType string `json:"notification_type"`
	ReqID            string `json:"-"` // the listener sets this field
}

type pending struct{ ch chan Decision }

type Listener struct {
	srv    *http.Server
	ln     net.Listener
	token  string
	events chan HookEvent
	mu     sync.Mutex
	reqs   map[string]*pending
	// done is closed by Close. A handler that must deliver an event after
	// its own request context ended (the cancellation event) selects on it,
	// so it can never leak past the listener's lifetime.
	done      chan struct{}
	closeOnce sync.Once
}

func New() (*Listener, error) {
	ln, err := net.Listen("tcp", LoopbackHost+":0")
	if err != nil {
		return nil, fmt.Errorf("hooklistener.New: %w", err)
	}
	raw := make([]byte, listenerTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		_ = ln.Close()
		return nil, err
	}
	l := &Listener{
		ln:     ln,
		token:  hex.EncodeToString(raw),
		events: make(chan HookEvent, hookEventChanBuf),
		reqs:   make(map[string]*pending),
		done:   make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", l.handleHook)
	l.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	go func() {
		defer safe.Recover("hook-listener")
		_ = l.srv.Serve(ln)
	}()
	return l, nil
}

func (l *Listener) Addr() string             { return l.ln.Addr().String() }
func (l *Listener) Token() string            { return l.token }
func (l *Listener) Events() <-chan HookEvent { return l.events }

// Close shuts the server down. It is idempotent.
func (l *Listener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.done)
		err = l.srv.Close()
	})
	return err
}

// auth validates the Bearer token in constant time to avoid leaking it via
// response timing. Returns true only on an exact token match.
func (l *Listener) auth(r *http.Request) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := strings.TrimPrefix(h, prefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(l.token)) == 1
}

func (l *Listener) handleHook(w http.ResponseWriter, r *http.Request) {
	if !l.auth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Hooks POST their payload. Reject any other method before touching the body.
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Cap the request body so a malicious or malfunctioning hook cannot exhaust
	// memory with an unbounded POST. 1 MiB is far larger than any real hook
	// payload (tool name plus tool input). An over-limit body makes Decode
	// return an error. The handler then falls into the 400 path below, instead
	// of buffering the whole body.
	r.Body = http.MaxBytesReader(w, r.Body, maxHookBodyBytes)
	var ev HookEvent
	if q := r.URL.Query(); q.Has(AgentExitParam) {
		// The shell exit sentinel reports the agent's exit code in the query
		// string, so the typed launch line needs no JSON quoting (it must
		// stay free of backslashes and single quotes to survive a
		// `sh -c '...'` wrapper typed into fish or nushell).
		ev = HookEvent{Type: EventAgentExit, ErrorType: sanitizeExitCode(q.Get(AgentExitParam))}
	} else if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Read the body to EOF (MaxBytesReader still caps it). net/http only
	// starts watching for the client closing the connection, which is what
	// cancels r.Context(), once the body is fully consumed. Without this a
	// body with bytes past the JSON value (curl --data-binary keeps a
	// trailing newline) may never report the hook being killed, and a parked
	// PermissionRequest would never be retracted.
	_, _ = io.Copy(io.Discard, r.Body)
	if ev.Type != EventPermissionRequest {
		// Every other event is non-blocking: lifecycle hooks (Stop,
		// StopFailure, SessionStart, UserPromptSubmit, PostToolUse), the
		// AskUserQuestion PreToolUse signal, and the exit sentinel's
		// AgentExit. They must never drop silently, because the sidebar state
		// depends on them. The handler uses a blocking send here, but it
		// stays cancellable, so a client disconnect or a server shutdown
		// cannot leak this handler goroutine. The 200 carries an EMPTY body,
		// which claude reads as "no decision", so a PreToolUse signal never
		// overrides claude's own permission evaluation.
		select {
		case l.events <- ev:
		case <-r.Context().Done():
			http.Error(w, "client gone", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	// PermissionRequest blocks until Decide() supplies a verdict.
	reqBytes := make([]byte, reqIDBytes)
	_, _ = rand.Read(reqBytes)
	ev.ReqID = hex.EncodeToString(reqBytes)
	p := &pending{ch: make(chan Decision, 1)}
	l.mu.Lock()
	l.reqs[ev.ReqID] = p
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.reqs, ev.ReqID)
		l.mu.Unlock()
	}()
	// Deliver the approval event. A blocking approval must never drop.
	select {
	case l.events <- ev:
	case <-r.Context().Done():
		http.Error(w, "client gone", http.StatusServiceUnavailable)
		return
	}
	// Wait for the verdict. Stay cancellable, so a client disconnect or a
	// server shutdown never leaks this handler goroutine.
	var d Decision
	select {
	case d = <-p.ch:
	case <-r.Context().Done():
		// The request ended without a verdict: claude killed the hook
		// because the user answered its own dialog, pressed Esc, or the hook
		// timed out. Retract it, so the consumer can drop its approval card
		// instead of leaving a dead one up (AGT-16). The send outlives the
		// request context, so it selects on the listener's own lifetime.
		l.mu.Lock()
		delete(l.reqs, ev.ReqID)
		l.mu.Unlock()
		select {
		case l.events <- HookEvent{Type: EventRequestCancelled, SessionID: ev.SessionID, ToolName: ev.ToolName, ReqID: ev.ReqID}:
		case <-l.done:
		}
		http.Error(w, "client gone", http.StatusServiceUnavailable)
		return
	}
	if d.Abstain {
		w.WriteHeader(http.StatusOK)
		return
	}
	behavior := "deny"
	if d.Allow {
		behavior = "allow"
	}
	decision := map[string]any{"behavior": behavior}
	if !d.Allow {
		decision["message"] = "Denied by the user in perch."
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": EventPermissionRequest,
			"decision":      decision,
		},
	})
}

// Decide delivers a verdict for a parked PermissionRequest. It reports
// whether the request was still pending and this verdict was accepted. It
// returns false for an unknown or already-finished reqID (the hook was
// cancelled, or answered before), and for a second verdict on the same
// reqID. Decide never blocks.
func (l *Listener) Decide(reqID string, d Decision) bool {
	l.mu.Lock()
	p := l.reqs[reqID]
	l.mu.Unlock()
	if p == nil {
		return false
	}
	// This is a non-blocking send into the size-1 buffered channel. The handler
	// consumes exactly one verdict. The first Decide call for a reqID fills the
	// buffer and wins. A second Decide call for the same reqID (a double click
	// or a retry) finds the buffer full and falls through the default case,
	// instead of blocking the caller (a Wails IPC goroutine) forever. The first
	// verdict is the one the handler delivers.
	select {
	case p.ch <- d:
		return true
	default:
		return false
	}
}
