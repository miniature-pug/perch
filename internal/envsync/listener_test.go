package envsync

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// syncCapture records the (workspaceID, delta) pairs the listener delivers via
// its SyncFunc callback, guarded so the handler goroutine and the test goroutine
// never race.
type syncCapture struct {
	mu    sync.Mutex
	calls []captured
}

type captured struct {
	workspaceID string
	delta       []string
}

func (c *syncCapture) record(workspaceID string, delta []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, captured{workspaceID: workspaceID, delta: append([]string(nil), delta...)})
}

func (c *syncCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *syncCapture) last() (captured, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return captured{}, false
	}
	return c.calls[len(c.calls)-1], true
}

func newTestListener(t *testing.T, baseline []string) (*Listener, *syncCapture) {
	t.Helper()
	capt := &syncCapture{}
	l, err := New(baseline, capt.record)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, capt
}

// post sends a raw body with the given bearer token to the listener and returns
// the HTTP status code.
func post(t *testing.T, l *Listener, token string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, l.URL(), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

func mustJSON(t *testing.T, workspaceID string, env []string) []byte {
	t.Helper()
	b, err := json.Marshal(SyncRequest{WorkspaceID: workspaceID, Env: env})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ── computeDelta ─────────────────────────────────────────────────────────────

func TestComputeDelta(t *testing.T) {
	baseline := baselineMap([]string{"FOO=1", "BAR=2", "SAME=keep", "PERCH_EXIT_TOKEN=zzz"})
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{"new key captured", []string{"NEW=9"}, []string{"NEW=9"}},
		{"changed value captured", []string{"FOO=99"}, []string{"FOO=99"}},
		{"unchanged key ignored", []string{"SAME=keep"}, nil},
		{"perch prefixed excluded (new)", []string{"PERCH_ENVSYNC_TOKEN=abc"}, nil},
		{"perch prefixed excluded (changed)", []string{"PERCH_EXIT_TOKEN=changed"}, nil},
		{"malformed entry ignored", []string{"NOEQUALS", "=noKey", "OK=1"}, []string{"OK=1"}},
		{
			"mixed: new + changed captured, unchanged + perch dropped",
			[]string{"SAME=keep", "FOO=99", "NEW=9", "PERCH_ENVSYNC_WS=ws-a"},
			[]string{"FOO=99", "NEW=9"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeDelta(baseline, tc.env)
			if !equalStringSlices(got, tc.want) {
				t.Errorf("computeDelta = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── endpoint: valid token, delta delivery ────────────────────────────────────

func TestListener_ValidToken_DeliversDelta(t *testing.T) {
	l, capt := newTestListener(t, []string{"FOO=1", "BAR=2"})
	tok, err := l.TokenFor("ws-a")
	if err != nil {
		t.Fatalf("TokenFor: %v", err)
	}
	env := []string{"FOO=1", "BAR=3", "NEW=9", "PERCH_ENVSYNC_TOKEN=" + tok}
	code := post(t, l, tok, mustJSON(t, "ws-a", env))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if capt.count() != 1 {
		t.Fatalf("onSync called %d times, want 1", capt.count())
	}
	got, _ := capt.last()
	if got.workspaceID != "ws-a" {
		t.Errorf("workspaceID = %q, want ws-a", got.workspaceID)
	}
	// Only BAR=3 (changed) and NEW=9 (new). FOO unchanged; PERCH_* excluded.
	want := []string{"BAR=3", "NEW=9"}
	if !equalStringSlices(got.delta, want) {
		t.Errorf("delta = %v, want %v", got.delta, want)
	}
}

// ── endpoint: missing / wrong token ──────────────────────────────────────────

func TestListener_MissingToken_Rejected(t *testing.T) {
	l, capt := newTestListener(t, nil)
	if _, err := l.TokenFor("ws-a"); err != nil {
		t.Fatal(err)
	}
	code := post(t, l, "", mustJSON(t, "ws-a", []string{"NEW=1"}))
	if code != http.StatusUnauthorized {
		t.Errorf("missing token: status = %d, want 401", code)
	}
	if capt.count() != 0 {
		t.Errorf("onSync must NOT be called on missing token; got %d", capt.count())
	}
}

func TestListener_WrongToken_Rejected(t *testing.T) {
	l, capt := newTestListener(t, nil)
	if _, err := l.TokenFor("ws-a"); err != nil {
		t.Fatal(err)
	}
	code := post(t, l, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", mustJSON(t, "ws-a", []string{"NEW=1"}))
	if code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", code)
	}
	if capt.count() != 0 {
		t.Errorf("onSync must NOT be called on wrong token; got %d", capt.count())
	}
}

// ── endpoint: oversized body ─────────────────────────────────────────────────

func TestListener_OversizedBody_Rejected(t *testing.T) {
	l, capt := newTestListener(t, nil)
	tok, err := l.TokenFor("ws-a")
	if err != nil {
		t.Fatal(err)
	}
	// Build a body larger than the 1 MiB cap.
	huge := strings.Repeat("A", (1<<20)+1024)
	body := mustJSON(t, "ws-a", []string{"BIG=" + huge})
	code := post(t, l, tok, body)
	if code == http.StatusOK {
		t.Errorf("oversized body accepted (status 200); want rejection")
	}
	if capt.count() != 0 {
		t.Errorf("onSync must NOT be called on oversized body; got %d", capt.count())
	}
}

// ── endpoint: cross-workspace token ──────────────────────────────────────────

func TestListener_CrossWorkspaceToken_Rejected(t *testing.T) {
	l, capt := newTestListener(t, nil)
	tokA, err := l.TokenFor("ws-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.TokenFor("ws-b"); err != nil {
		t.Fatal(err)
	}
	// ws-a's token, but the body claims ws-b. Must be rejected; the delta must
	// never be applied to ws-b.
	code := post(t, l, tokA, mustJSON(t, "ws-b", []string{"NEW=1"}))
	if code != http.StatusForbidden {
		t.Errorf("cross-workspace token: status = %d, want 403", code)
	}
	if capt.count() != 0 {
		t.Errorf("onSync must NOT be called on cross-workspace token; got %d", capt.count())
	}
}

// ── endpoint: wrong method ───────────────────────────────────────────────────

func TestListener_WrongMethod_Rejected(t *testing.T) {
	l, _ := newTestListener(t, nil)
	tok, err := l.TokenFor("ws-a")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, l.URL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: status = %d, want 405", resp.StatusCode)
	}
}

// ── TokenFor: mint-or-lookup ─────────────────────────────────────────────────

func TestListener_TokenFor_StablePerWorkspace(t *testing.T) {
	l, _ := newTestListener(t, nil)
	a1, err := l.TokenFor("ws-a")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := l.TokenFor("ws-a")
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Errorf("TokenFor(ws-a) not stable: %q vs %q", a1, a2)
	}
	b, err := l.TokenFor("ws-b")
	if err != nil {
		t.Fatal(err)
	}
	if b == a1 {
		t.Errorf("TokenFor(ws-b) collided with ws-a token")
	}
	// 32 crypto-random bytes → 64 hex chars.
	if len(a1) != 64 {
		t.Errorf("token length = %d, want 64 hex chars", len(a1))
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
