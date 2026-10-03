package envsync

import (
	"net/http"
	"sync"
	"testing"
)

// TestComputeDelta_IgnoresShellVolatile is the AGT-21 regression: PWD,
// OLDPWD, SHLVL, _, COLUMNS and LINES differ in every drawer shell and must
// not be carried into a relaunch.
func TestComputeDelta_IgnoresShellVolatile(t *testing.T) {
	baseline := baselineMap([]string{"PWD=/a", "SHLVL=1"})
	got := computeDelta(baseline, []string{"PWD=/b", "OLDPWD=/a", "SHLVL=3", "_=/usr/bin/env", "COLUMNS=80", "LINES=24", "AWS_PROFILE=x"})
	if !equalStringSlices(got, []string{"AWS_PROFILE=x"}) {
		t.Errorf("computeDelta = %v, want only AWS_PROFILE=x", got)
	}
}

// TestListener_DeliversUnset is the AGT-21 regression for `unset`: a key in
// the baseline but absent from the terminal env is reported in Unset
// (excluding PERCH_* and volatile keys).
func TestListener_DeliversUnset(t *testing.T) {
	var mu sync.Mutex
	var got Delta
	l, err := NewWithDelta([]string{"AWS_PROFILE=prod", "KEEP=1", "PWD=/x", "PERCH_EXIT_URL=u"}, func(_ string, d Delta) {
		mu.Lock()
		got = d
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	tok, _ := l.TokenFor("ws")
	if code := post(t, l, tok, mustJSON(t, "ws", []string{"KEEP=1", "NEW=2"})); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if !equalStringSlices(got.Set, []string{"NEW=2"}) || !equalStringSlices(got.Unset, []string{"AWS_PROFILE"}) {
		t.Errorf("delta = %+v, want Set [NEW=2] Unset [AWS_PROFILE]", got)
	}
}

// TestListener_Revoke is the AGT-21 regression for token lifetime: a
// revoked workspace's token no longer authenticates.
func TestListener_Revoke(t *testing.T) {
	l, _ := newTestListener(t, nil)
	tok, _ := l.TokenFor("ws")
	l.Revoke("ws")
	if code := post(t, l, tok, mustJSON(t, "ws", nil)); code != http.StatusUnauthorized {
		t.Errorf("revoked token: status %d, want 401", code)
	}
	if tok2, _ := l.TokenFor("ws"); tok2 == tok {
		t.Error("TokenFor after Revoke returned the revoked token")
	}
}
