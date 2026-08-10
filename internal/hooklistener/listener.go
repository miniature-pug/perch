// internal/hooklistener/listener.go
package hooklistener

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

type Decision struct {
	Allow  bool `json:"allow"`
	Always bool `json:"always"`
}

type HookEvent struct {
	Type           string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ErrorType      string          `json:"error_type"`
	ReqID          string          `json:"-"` // the listener sets this field
}

type pending struct{ ch chan Decision }

type Listener struct {
	srv    *http.Server
	ln     net.Listener
	token  string
	events chan HookEvent
	mu     sync.Mutex
	reqs   map[string]*pending
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
func (l *Listener) Close() error             { return l.srv.Close() }

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
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if ev.Type != "PreToolUse" {
		// Lifecycle events (Stop, StopFailure, SessionStart, Notification) must
		// never drop silently. The sidebar state depends on them. The handler
		// uses a blocking send here, but it stays cancellable, so a client
		// disconnect or a server shutdown cannot leak this handler goroutine.
		select {
		case l.events <- ev:
		case <-r.Context().Done():
			http.Error(w, "client gone", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	// PreToolUse blocks until Decide() supplies a verdict.
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
		http.Error(w, "client gone", http.StatusServiceUnavailable)
		return
	}
	perm := "deny"
	if d.Allow {
		perm = "allow"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": perm,
		},
	})
}

func (l *Listener) Decide(reqID string, d Decision) {
	l.mu.Lock()
	p := l.reqs[reqID]
	l.mu.Unlock()
	if p == nil {
		return
	}
	// This is a non-blocking send into the size-1 buffered channel. The handler
	// consumes exactly one verdict. The first Decide call for a reqID fills the
	// buffer and wins. A second Decide call for the same reqID (a double click
	// or a retry) finds the buffer full and falls through the default case,
	// instead of blocking the caller (a Wails IPC goroutine) forever. The first
	// verdict is the one the handler delivers.
	select {
	case p.ch <- d:
	default:
	}
}
