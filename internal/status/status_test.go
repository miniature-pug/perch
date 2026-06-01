package status_test

import (
	"context"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/status"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ── Machine tests (§20.3 table) ───────────────────────────────────────────────

// fired collects the states returned by Machine methods and returns the
// non-empty ones in order. This mirrors the "expected calls to perch status
// set" column in the §20.3 table.
func fired(pairs ...struct {
	state string
	fire  bool
}) []string {
	var out []string
	for _, p := range pairs {
		if p.fire {
			out = append(out, p.state)
		}
	}
	return out
}

func pair(state string, fire bool) struct {
	state string
	fire  bool
} {
	return struct {
		state string
		fire  bool
	}{state, fire}
}

func TestMachine_Table(t *testing.T) {
	t.Run("dedup same state twice", func(t *testing.T) {
		m := status.NewMachine()
		sid := "sessA"
		got := fired(
			pair(m.OnSessionStatus(sid, "busy")),
			pair(m.OnSessionStatus(sid, "busy")),
		)
		want := []string{"working"}
		assertFired(t, want, got)
	})

	t.Run("stale busy rejected after done", func(t *testing.T) {
		m := status.NewMachine()
		sid := "sessA"
		got := fired(
			pair(m.OnSessionStatus(sid, "busy")),
			pair(m.OnSessionStatus(sid, "idle")),
			pair(m.OnSessionStatus(sid, "busy")), // stale — dropped
		)
		want := []string{"working", "done"}
		assertFired(t, want, got)
	})

	t.Run("re-arm on user message", func(t *testing.T) {
		m := status.NewMachine()
		sid := "sessA"
		got := fired(
			pair(m.OnSessionStatus(sid, "busy")),
			pair(m.OnSessionStatus(sid, "idle")),
			pair(m.OnUserMessage(sid)),           // re-arms acceptWorking
			pair(m.OnSessionStatus(sid, "busy")), // fires again
		)
		want := []string{"working", "done", "working"}
		assertFired(t, want, got)
	})

	t.Run("waiting to working via permission", func(t *testing.T) {
		m := status.NewMachine()
		sid := "sessA"
		got := fired(
			pair(m.OnPermissionAsked(sid)),
			pair(m.OnPermissionReplied(sid)),
		)
		want := []string{"waiting", "working"}
		assertFired(t, want, got)
	})

	t.Run("independent sessions", func(t *testing.T) {
		m := status.NewMachine()
		stA1, fA1 := m.OnSessionStatus("sessA", "busy")
		stB1, fB1 := m.OnSessionStatus("sessB", "idle")

		if !fA1 || stA1 != "working" {
			t.Errorf("sessA: want (working, true), got (%q, %v)", stA1, fA1)
		}
		if !fB1 || stB1 != "done" {
			t.Errorf("sessB: want (done, true), got (%q, %v)", stB1, fB1)
		}

		// Verify no cross-contamination: A should still be able to fire done,
		// B should not fire working (acceptWorking disarmed after done).
		stA2, fA2 := m.OnSessionStatus("sessA", "idle")
		stB2, fB2 := m.OnSessionStatus("sessB", "busy") // disarmed
		if !fA2 || stA2 != "done" {
			t.Errorf("sessA follow-up: want (done, true), got (%q, %v)", stA2, fA2)
		}
		if fB2 {
			t.Errorf("sessB follow-up: expected no fire, got (%q, %v)", stB2, fB2)
		}
	})
}

func assertFired(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("fired states: want %v, got %v", want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("fired[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

// ── Machine alias / edge-case coverage ────────────────────────────────────────

func TestMachine_Aliases(t *testing.T) {
	t.Run("OnSessionIdle alias", func(t *testing.T) {
		m := status.NewMachine()
		state, fire := m.OnSessionIdle("s1")
		if !fire || state != "done" {
			t.Errorf("OnSessionIdle: want (done,true), got (%q,%v)", state, fire)
		}
	})

	t.Run("OnUserMessage returns no-op", func(t *testing.T) {
		m := status.NewMachine()
		state, fire := m.OnUserMessage("s3")
		if fire || state != "" {
			t.Errorf("OnUserMessage: want (\"\",false), got (%q,%v)", state, fire)
		}
	})

	t.Run("retry event fires working", func(t *testing.T) {
		m := status.NewMachine()
		state, fire := m.OnSessionStatus("s4", "retry")
		if !fire || state != "working" {
			t.Errorf("retry event: want (working,true), got (%q,%v)", state, fire)
		}
	})

	t.Run("unknown status type is no-op", func(t *testing.T) {
		m := status.NewMachine()
		state, fire := m.OnSessionStatus("s5", "paused")
		if fire || state != "" {
			t.Errorf("unknown statusType: want (\"\",false), got (%q,%v)", state, fire)
		}
	})

	t.Run("permission dedup", func(t *testing.T) {
		m := status.NewMachine()
		sid := "s6"
		_, _ = m.OnPermissionAsked(sid)
		state, fire := m.OnPermissionAsked(sid) // dedup
		if fire || state != "" {
			t.Errorf("permission dedup: want (\"\",false), got (%q,%v)", state, fire)
		}
	})

	t.Run("done dedup", func(t *testing.T) {
		m := status.NewMachine()
		sid := "s7"
		_, _ = m.OnSessionStatus(sid, "idle")
		state, fire := m.OnSessionStatus(sid, "idle") // dedup
		if fire || state != "" {
			t.Errorf("done dedup: want (\"\",false), got (%q,%v)", state, fire)
		}
	})
}

// ── Set tests via FakeRunner ───────────────────────────────────────────────────

// makeTestDeps returns a status.Deps with a FakeRunner injected. The fake has
// a permissive Default so any set-option call succeeds without explicit canning.
func makeTestDeps() (status.Deps, *proc.FakeRunner) {
	fake := proc.NewFakeRunner()
	fake.Default = &proc.FakeResult{}
	deps := status.Deps{
		Tmux: tmux.Tmux{Runner: fake},
	}
	return deps, fake
}

func TestSet_Valid(t *testing.T) {
	for _, state := range []string{"working", "waiting", "done"} {
		state := state
		t.Run(state, func(t *testing.T) {
			deps, fake := makeTestDeps()
			err := status.Set(context.Background(), deps, "%3", state)
			if err != nil {
				t.Fatalf("Set(%q): unexpected error: %v", state, err)
			}
			if len(fake.Calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(fake.Calls))
			}
			call := fake.Calls[0]
			wantArgs := []string{"set-option", "-p", "-t", "%3", "@perch_pane_status", state}
			assertArgs(t, wantArgs, call.Args)
		})
	}
}

func TestSet_InvalidState(t *testing.T) {
	deps, fake := makeTestDeps()
	err := status.Set(context.Background(), deps, "%3", "bogus")
	if err == nil {
		t.Fatal("expected error for invalid state, got nil")
	}
	// Validation must happen before any subprocess call.
	if len(fake.Calls) != 0 {
		t.Errorf("invalid state should not reach runner; got %d calls", len(fake.Calls))
	}
}

func assertArgs(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("args: want %v, got %v", want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("args[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}
