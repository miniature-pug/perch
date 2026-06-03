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
	ReqID          string          `json:"-"` // assigned by listener
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("hooklistener.New: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		_ = ln.Close()
		return nil, err
	}
	l := &Listener{
		ln:     ln,
		token:  hex.EncodeToString(raw),
		events: make(chan HookEvent, 64),
		reqs:   make(map[string]*pending),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", l.handleHook)
	l.srv = &http.Server{Handler: mux}
	go func() { _ = l.srv.Serve(ln) }()
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
	// body decoding and routing in Tasks 2.12/2.13
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (l *Listener) Decide(reqID string, d Decision) {
	l.mu.Lock()
	p := l.reqs[reqID]
	l.mu.Unlock()
	if p != nil {
		p.ch <- d
	}
}
