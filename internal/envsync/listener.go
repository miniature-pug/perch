// Package envsync hosts the app-owned loopback endpoint that receives a perch
// session terminal's current environment and computes the delta the agent must
// be relaunched with. It reuses the exact security posture of
// internal/hooklistener — loopback only on 127.0.0.1, a per-session Bearer token
// of 32 crypto-random bytes compared in constant time, and a 1 MiB body cap — but
// it is a small dedicated handler so the environment payload (which may contain
// secrets) never has to be threaded through agent.Event.
//
// The environment payload is held in memory only and is NEVER logged or written
// to disk: the handler logs nothing about the body, and the captured delta is
// handed to the caller's SyncFunc, which the app stores in an in-memory overlay.
package envsync

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/safe"
)

const (
	// tokenBytes is the number of crypto-random bytes in a per-session token
	// (hex-encoded → 64 chars). Matches the hook listener's token strength.
	tokenBytes = 32

	// maxSyncBodyBytes caps the sync request body (1 MiB) so an over-large POST
	// cannot exhaust memory. A real environment payload is orders of magnitude
	// smaller; an over-limit body makes Decode return an error and falls into the
	// 400 path rather than being buffered whole.
	maxSyncBodyBytes = 1 << 20

	// syncPath is the single endpoint path.
	syncPath = "/sync"

	// perchPrefix marks perch-internal variables, which are always excluded from a
	// captured delta (PERCH_ENVSYNC_*, PERCH_EXIT_*, …). They are perch plumbing,
	// never something the user meant to hand to the agent.
	perchPrefix = "PERCH_"

	// Env-sync handle names — the single source of truth for the three variables
	// the app injects into a per-workspace drawer and `perch reload` reads back.
	// EnvURL is the endpoint URL, EnvToken the per-session Bearer token, EnvWS the
	// workspace id.
	EnvURL   = "PERCH_ENVSYNC_URL"
	EnvToken = "PERCH_ENVSYNC_TOKEN"
	EnvWS    = "PERCH_ENVSYNC_WS"

	// HTTP server timeouts bound how long a single connection may occupy the
	// listener, closing the slowloris exposure of an unbounded server. Unlike the
	// hook listener (whose PreToolUse handler blocks on human decision time and so
	// omits WriteTimeout), the sync handler always responds immediately, so a
	// finite WriteTimeout is both safe and appropriate here.
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 10 * time.Second
	serverWriteTimeout      = 10 * time.Second
	serverIdleTimeout       = 60 * time.Second
)

// SyncFunc is invoked with the authenticated workspace id and the computed
// environment delta (KEY=VALUE entries that are new or changed versus the
// baseline, PERCH_* excluded) after a valid POST. It must not block: the app's
// implementation stores the overlay and dispatches the relaunch on its own
// goroutine.
type SyncFunc func(workspaceID string, delta []string)

// SyncRequest is the wire format `perch reload` POSTs to the endpoint. It is the
// single source of truth for the request shape, encoded by cmd/perch and decoded
// here.
type SyncRequest struct {
	// WorkspaceID is the session's workspace id (from PERCH_ENVSYNC_WS). It is
	// cross-checked against the token's workspace so a token minted for one session
	// can never request a relaunch of another.
	WorkspaceID string `json:"workspace_id"`
	// Env is the full os.Environ() of the session terminal at reload time.
	Env []string `json:"env"`
}

// Listener is the loopback env-sync endpoint. One is stood up per app run; it
// holds a per-session token→workspace map so many workspace drawers share a
// single server.
type Listener struct {
	srv      *http.Server
	ln       net.Listener
	baseline map[string]string
	onSync   SyncFunc

	mu      sync.Mutex
	tokens  map[string]string // token → workspaceID
	wsToken map[string]string // workspaceID → token (mint-or-lookup reverse index)
}

// New binds a loopback listener on an ephemeral port and starts serving. baseline
// is the app's os.Environ() captured at start, the reference against which every
// delta is computed. onSync is invoked after each valid POST.
func New(baseline []string, onSync SyncFunc) (*Listener, error) {
	ln, err := net.Listen("tcp", hooklistener.LoopbackHost+":0")
	if err != nil {
		return nil, err
	}
	l := &Listener{
		ln:       ln,
		baseline: baselineMap(baseline),
		onSync:   onSync,
		tokens:   map[string]string{},
		wsToken:  map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(syncPath, l.handleSync)
	l.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	go func() {
		defer safe.Recover("envsync-listener")
		_ = l.srv.Serve(ln)
	}()
	return l, nil
}

// Addr returns the loopback host:port the endpoint is bound to.
func (l *Listener) Addr() string { return l.ln.Addr().String() }

// URL returns the full sync endpoint URL injected into a drawer as
// PERCH_ENVSYNC_URL.
func (l *Listener) URL() string { return "http://" + l.ln.Addr().String() + syncPath }

// Close shuts the server down.
func (l *Listener) Close() error { return l.srv.Close() }

// TokenFor returns the env-sync token for workspaceID, minting a fresh 32-byte
// crypto-random token on first request and returning the same token on every
// later call for that workspace.
func (l *Listener) TokenFor(workspaceID string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if tok, ok := l.wsToken[workspaceID]; ok {
		return tok, nil
	}
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	l.tokens[tok] = workspaceID
	l.wsToken[workspaceID] = tok
	return tok, nil
}

// resolve returns the workspace id a presented bearer token authenticates for.
// It compares against every registered token in constant time (no early exit on
// match) so response timing never reveals which token — or how much of one —
// matched. Tokens are unique random values, so at most one can match.
func (l *Listener) resolve(presented string) (workspaceID string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	matched := 0
	for tok, ws := range l.tokens {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(presented)) == 1 {
			workspaceID = ws
			matched = 1
		}
	}
	return workspaceID, matched == 1
}

// bearer extracts the token from an Authorization: Bearer <token> header.
func bearer(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

func (l *Listener) handleSync(w http.ResponseWriter, r *http.Request) {
	// Authenticate first: an unauthenticated caller learns nothing about the
	// endpoint beyond "unauthorized", never even the accepted method.
	tokenWS, ok := l.resolve(bearer(r))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Cap the body so a malicious or malfunctioning client cannot exhaust memory;
	// an over-limit body makes Decode return an error and falls into the 400 path.
	r.Body = http.MaxBytesReader(w, r.Body, maxSyncBodyBytes)
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Deliberately generic: never echo any part of the (secret-bearing) body.
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Cross-workspace guard: the delta is applied to the token's workspace, and the
	// body must agree. A token minted for session A can never move session B.
	if req.WorkspaceID != tokenWS {
		http.Error(w, "workspace mismatch", http.StatusForbidden)
		return
	}
	delta := computeDelta(l.baseline, req.Env)
	if l.onSync != nil {
		l.onSync(tokenWS, delta)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// baselineMap indexes a KEY=VALUE environment slice by key for O(1) delta lookup.
// On duplicate keys the last entry wins, matching how a process resolves its
// environment.
func baselineMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		key, val, ok := strings.Cut(e, "=")
		if !ok || key == "" {
			continue
		}
		m[key] = val
	}
	return m
}

// computeDelta returns the KEY=VALUE entries in env whose key is new or whose
// value differs from baseline. Keys prefixed PERCH_ are always excluded (perch
// plumbing, never the user's intent), and malformed entries (no '=' or empty key)
// are skipped. The result preserves env's order.
func computeDelta(baseline map[string]string, env []string) []string {
	var out []string
	for _, e := range env {
		key, val, ok := strings.Cut(e, "=")
		if !ok || key == "" {
			continue
		}
		if strings.HasPrefix(key, perchPrefix) {
			continue
		}
		if base, exists := baseline[key]; exists && base == val {
			continue // unchanged versus baseline
		}
		out = append(out, e)
	}
	return out
}
