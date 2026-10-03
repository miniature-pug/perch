// Package envsync hosts the app-owned loopback endpoint. The endpoint
// receives a perch session terminal's current environment, and it computes
// the delta the agent needs for its relaunch. It reuses the exact security
// posture of internal/hooklistener: loopback only on 127.0.0.1, a per-session
// Bearer token of 32 crypto-random bytes compared in constant time, and a
// 1 MiB body cap. Unlike the hook listener, envsync is a small dedicated
// handler, so the environment payload (which may hold secrets) never has to
// pass through agent.Event.
//
// The package holds the environment payload in memory only, and NEVER logs
// or writes it to disk. The handler logs nothing about the body. It hands the
// captured delta to the caller's SyncFunc, and the app stores the delta in an
// in-memory overlay.
package envsync

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miniature-pug/perch/internal/hooklistener"
	"github.com/miniature-pug/perch/internal/safe"
)

const (
	// tokenBytes is the number of crypto-random bytes in a per-session token
	// (hex-encoded, 64 chars). This matches the hook listener's token strength.
	tokenBytes = 32

	// maxSyncBodyBytes caps the sync request body at 1 MiB, so an over-large
	// POST cannot exhaust memory. A real environment payload is far smaller.
	// An over-limit body makes Decode return an error and fall into the 400
	// path, instead of being buffered whole.
	maxSyncBodyBytes = 1 << 20

	// syncPath is the single endpoint path.
	syncPath = "/sync"

	// perchPrefix marks perch-internal variables. The package always excludes
	// these from a captured delta (PERCH_ENVSYNC_*, PERCH_EXIT_*, and so on).
	// They are perch plumbing, never something the user meant to hand to the
	// agent.
	perchPrefix = "PERCH_"

	// These are the env-sync handle names, the single source of truth for the
	// three variables the app injects into each workspace's shell drawer and
	// `perch reload` reads back. EnvURL is the endpoint URL, EnvToken is the
	// per-session Bearer token, and EnvWS is the workspace id.
	EnvURL   = "PERCH_ENVSYNC_URL"
	EnvToken = "PERCH_ENVSYNC_TOKEN"
	EnvWS    = "PERCH_ENVSYNC_WS"

	// The HTTP server timeouts bound how long one connection can occupy the
	// listener, and this closes the slowloris risk of an unbounded server.
	// The hook listener omits WriteTimeout because its PermissionRequest handler
	// blocks on human decision time. The sync handler differs: it always
	// responds immediately, so a finite WriteTimeout is both safe and
	// appropriate here.
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 10 * time.Second
	serverWriteTimeout      = 10 * time.Second
	serverIdleTimeout       = 60 * time.Second
)

// SyncFunc receives the authenticated workspace id and the computed
// environment delta after a valid POST. The delta holds KEY=VALUE entries
// that are new or changed compared to the baseline. It excludes PERCH_*
// entries. SyncFunc must not block: the app's implementation stores the
// overlay and dispatches the relaunch on its own goroutine.
type SyncFunc func(workspaceID string, delta []string)

// Delta is the full environment change a `perch reload` carries: Set holds
// KEY=VALUE entries that are new or changed versus the baseline, and Unset
// holds the KEYs present in the baseline but absent from the terminal's
// environment (the user ran `unset KEY`). Both exclude PERCH_* plumbing and
// shell-volatile variables.
type Delta struct {
	Set   []string
	Unset []string
}

// DeltaFunc receives the authenticated workspace id and the full Delta. It
// must not block, like SyncFunc.
type DeltaFunc func(workspaceID string, d Delta)

// SyncRequest is the wire format `perch reload` POSTs to the endpoint.
// SyncRequest is the single source of truth for the request shape: cmd/perch
// encodes it, and this file decodes it.
type SyncRequest struct {
	// WorkspaceID is the session's workspace id (from PERCH_ENVSYNC_WS). The
	// handler cross-checks it against the token's workspace, so a token
	// minted for one session can never request a relaunch of another.
	WorkspaceID string `json:"workspace_id"`
	// Env is the full os.Environ() of the session terminal at reload time.
	Env []string `json:"env"`
}

// Listener is the loopback env-sync endpoint. Each app run starts one
// Listener. It holds a per-session token-to-workspace map, so many shell
// drawers share a single server.
type Listener struct {
	srv      *http.Server
	ln       net.Listener
	baseline map[string]string
	onSync   DeltaFunc

	mu      sync.Mutex
	tokens  map[string]string // token → workspaceID
	wsToken map[string]string // workspaceID → token (mint-or-lookup reverse index)
}

// New binds a loopback listener on an ephemeral port, and starts serving.
// baseline is the app's os.Environ(), captured at start. The listener
// computes every delta against this reference. New invokes onSync after each
// valid POST.
//
// New delivers only Delta.Set to onSync. Use NewWithDelta to also receive
// the variables the user unset.
func New(baseline []string, onSync SyncFunc) (*Listener, error) {
	var f DeltaFunc
	if onSync != nil {
		f = func(ws string, d Delta) { onSync(ws, d.Set) }
	}
	return NewWithDelta(baseline, f)
}

// NewWithDelta is New with a callback that receives the full Delta,
// including Unset.
func NewWithDelta(baseline []string, onSync DeltaFunc) (*Listener, error) {
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

// Addr returns the loopback host:port where the endpoint listens.
func (l *Listener) Addr() string { return l.ln.Addr().String() }

// URL returns the full sync endpoint URL. The app injects this URL into a
// shell drawer as PERCH_ENVSYNC_URL.
func (l *Listener) URL() string { return "http://" + l.ln.Addr().String() + syncPath }

// Close shuts the server down.
func (l *Listener) Close() error { return l.srv.Close() }

// TokenFor returns the env-sync token for workspaceID. On the first request
// for a workspace, TokenFor mints a fresh 32-byte crypto-random token.
// TokenFor returns that same token on every later call for the workspace.
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

// Revoke invalidates workspaceID's token, so a removed workspace's shell
// drawer can no longer trigger a relaunch. A later TokenFor mints a fresh
// token.
func (l *Listener) Revoke(workspaceID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if tok, ok := l.wsToken[workspaceID]; ok {
		delete(l.tokens, tok)
		delete(l.wsToken, workspaceID)
	}
}

// resolve returns the workspace id that a presented bearer token
// authenticates. resolve compares the token against every registered token in
// constant time, with no early exit on a match, so response timing never
// reveals which token matched, or how much of one matched. Tokens are unique
// random values, so at most one token can match.
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
	// Cap the body so a malicious or malfunctioning client cannot exhaust
	// memory. An over-limit body makes Decode return an error and fall into
	// the 400 path.
	r.Body = http.MaxBytesReader(w, r.Body, maxSyncBodyBytes)
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// This error is deliberately generic: never echo any part of the
		// (secret-bearing) body.
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Cross-workspace guard: the handler applies the delta to the token's
	// workspace, so the body must agree. A token minted for session A can
	// never move session B.
	if req.WorkspaceID != tokenWS {
		http.Error(w, "workspace mismatch", http.StatusForbidden)
		return
	}
	d := Delta{Set: computeDelta(l.baseline, req.Env), Unset: computeUnset(l.baseline, req.Env)}
	if l.onSync != nil {
		l.onSync(tokenWS, d)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// baselineMap indexes a KEY=VALUE environment slice by key, for an O(1) delta
// lookup. On duplicate keys, the last entry wins. This matches how a process
// resolves its own environment.
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

// volatileKeys are variables every shell rewrites for itself (working
// directory, nesting depth, last command, terminal size). They differ between
// the drawer shell and perch's launch environment without any user intent,
// and carrying them into a relaunch would give the agent shell a stale PWD
// or an inflated SHLVL (AGT-21).
var volatileKeys = map[string]bool{
	"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true, "COLUMNS": true, "LINES": true,
}

// ignoredKey reports whether key never belongs in a Delta.
func ignoredKey(key string) bool {
	return strings.HasPrefix(key, perchPrefix) || volatileKeys[key]
}

// computeDelta returns the KEY=VALUE entries in env whose key is new or whose
// value differs from baseline. computeDelta always excludes keys prefixed
// PERCH_ (perch plumbing, never the user's intent) and shell-volatile keys.
// It skips malformed entries too (no '=', or an empty key). The result keeps
// env's order.
func computeDelta(baseline map[string]string, env []string) []string {
	var out []string
	for _, e := range env {
		key, val, ok := strings.Cut(e, "=")
		if !ok || key == "" {
			continue
		}
		if ignoredKey(key) {
			continue
		}
		if isPathList(key) {
			val = dedupPathList(val)
			e = key + "=" + val
		}
		if base, exists := baseline[key]; exists && base == val {
			continue // unchanged versus baseline
		}
		out = append(out, e)
	}
	return out
}

// isPathList reports whether key holds a colon-separated search path
// (PATH, MANPATH, LD_LIBRARY_PATH, PYTHONPATH, ...).
func isPathList(key string) bool { return strings.HasSuffix(key, "PATH") }

// dedupPathList drops repeated entries from a colon-separated list, keeping
// the first occurrence (which is the one that wins a lookup). The drawer's
// login rc prepends to a PATH that already carries the previous reload's
// prepends; without this, every `perch reload` would add one more copy.
// Deduplicating keeps the captured value bounded.
func dedupPathList(v string) string {
	parts := strings.Split(v, ":")
	seen := make(map[string]bool, len(parts))
	out := parts[:0]
	for _, p := range parts {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, ":")
}

// computeUnset returns the baseline keys absent from env, sorted, excluding
// PERCH_* and shell-volatile keys: the variables the user unset in the
// terminal, which a relaunch must drop too (AGT-21).
func computeUnset(baseline map[string]string, env []string) []string {
	present := baselineMap(env)
	var out []string
	for key := range baseline {
		if ignoredKey(key) {
			continue
		}
		if _, ok := present[key]; !ok {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
