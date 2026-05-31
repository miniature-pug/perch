package tmux

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// ── Launch ────────────────────────────────────────────────────────────────────

// TestLaunch_HappyPath verifies that Launch:
//   - calls Connect (has-session + new-session), returning the pane ID, and
//   - calls SendKeys with the correctly quoted literal, then Enter.
//
// Total calls: [0] has-session, [1] new-session, [2] send-keys -l, [3] send-keys Enter.
func TestLaunch_HappyPath(t *testing.T) {
	r := proc.NewFakeRunner()
	// has-session: exit 1 → session absent → Connect will call new-session.
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=proj")
	// new-session returns pane %1.
	r.Respond(proc.FakeResult{Stdout: []byte("%1\n")},
		"tmux", "new-session", "-d", "-s", "proj", "-n", "feat", "-c", "/dir", "-P", "-F", "#{pane_id}")
	// SendKeys literal — pane ID must match what new-session returned.
	r.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", "%1", "-l", "'claude' '--resume' 'abc'")
	r.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", "%1", "Enter")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.Launch(context.Background(), "proj", "feat", "/dir", []string{"claude", "--resume", "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%1" {
		t.Errorf("paneID = %q, want %%1", paneID)
	}

	if len(r.Calls) != 4 {
		t.Fatalf("got %d calls, want 4", len(r.Calls))
	}

	// Call[0]: has-session with exact-match target.
	wantHasSession := []string{"has-session", "-t", "=proj"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantHasSession) {
		t.Errorf("Calls[0].Args = %v, want %v", r.Calls[0].Args, wantHasSession)
	}

	// Call[1]: new-session
	wantNewSession := []string{"new-session", "-d", "-s", "proj", "-n", "feat", "-c", "/dir", "-P", "-F", "#{pane_id}"}
	if !reflect.DeepEqual(r.Calls[1].Args, wantNewSession) {
		t.Errorf("Calls[1].Args = %v, want %v", r.Calls[1].Args, wantNewSession)
	}

	// Call[2]: send-keys -l with the POSIX-single-quoted literal as ONE arg.
	wantSendKeysLiteral := []string{"send-keys", "-t", "%1", "-l", "'claude' '--resume' 'abc'"}
	if !reflect.DeepEqual(r.Calls[2].Args, wantSendKeysLiteral) {
		t.Errorf("Calls[2].Args = %v, want %v", r.Calls[2].Args, wantSendKeysLiteral)
	}

	// Call[3]: send-keys Enter
	wantEnter := []string{"send-keys", "-t", "%1", "Enter"}
	if !reflect.DeepEqual(r.Calls[3].Args, wantEnter) {
		t.Errorf("Calls[3].Args = %v, want %v", r.Calls[3].Args, wantEnter)
	}
}

// TestLaunch_QuotingSafety verifies POSIX single-quote escaping across all
// hazard characters: space, embedded single-quote, dollar-sign, semicolon.
// The expected literal is a raw string to avoid backslash interpretation.
func TestLaunch_QuotingSafety(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=s")
	r.Respond(proc.FakeResult{Stdout: []byte("%2\n")},
		"tmux", "new-session", "-d", "-s", "s", "-n", "w", "-c", "/d", "-P", "-F", "#{pane_id}")

	// Expected: each token single-quoted; embedded ' escaped as '\''
	wantLiteral := `'a b' 'it'\''s' '$VAR' ';'`
	r.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", "%2", "-l", wantLiteral)
	r.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", "%2", "Enter")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.Launch(context.Background(), "s", "w", "/d", []string{"a b", "it's", "$VAR", ";"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 4 {
		t.Fatalf("got %d calls, want 4", len(r.Calls))
	}
	wantSendKeysQuoting := []string{"send-keys", "-t", "%2", "-l", wantLiteral}
	if !reflect.DeepEqual(r.Calls[2].Args, wantSendKeysQuoting) {
		t.Errorf("Calls[2].Args = %v, want %v", r.Calls[2].Args, wantSendKeysQuoting)
	}
}

// TestLaunch_EmptyArgv verifies that an empty/nil argv returns an error and
// that neither Connect nor SendKeys is ever called.
func TestLaunch_EmptyArgv(t *testing.T) {
	r := proc.NewFakeRunner()
	o := Tmux{Runner: r, Bin: "tmux"}

	_, err := o.Launch(context.Background(), "proj", "feat", "/dir", nil)
	if err == nil {
		t.Fatal("expected error for empty argv")
	}
	if len(r.Calls) != 0 {
		t.Errorf("expected zero calls for empty argv, got %d", len(r.Calls))
	}

	// Also test empty (non-nil) slice.
	_, err = o.Launch(context.Background(), "proj", "feat", "/dir", []string{})
	if err == nil {
		t.Fatal("expected error for empty (non-nil) argv")
	}
	if len(r.Calls) != 0 {
		t.Errorf("expected zero calls for empty argv, got %d", len(r.Calls))
	}
}

// TestLaunch_ConnectError verifies that a Connect failure (exec-layer error on
// has-session, ExitCode -1) propagates and SendKeys is never called.
func TestLaunch_ConnectError(t *testing.T) {
	execErr := errors.New("exec: tmux not found")
	r := proc.NewFakeRunner()
	// Exec-layer failure on has-session → HasSession returns (false, err) →
	// Connect returns that error.
	r.Respond(proc.FakeResult{Err: execErr},
		"tmux", "has-session", "-t", "=proj")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.Launch(context.Background(), "proj", "feat", "/dir", []string{"claude"})
	if err == nil {
		t.Fatal("expected error from Connect failure")
	}
	if !errors.Is(err, execErr) {
		t.Errorf("error should wrap execErr: %v", err)
	}

	// Only the has-session call should have been recorded; SendKeys must not fire.
	if len(r.Calls) != 1 {
		t.Errorf("expected 1 call (has-session only), got %d", len(r.Calls))
	}
	if r.Calls[0].Args[0] != "has-session" {
		t.Errorf("only call should be has-session, got %v", r.Calls[0].Args)
	}
}
