package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ── statusFromOption ──────────────────────────────────────────────────────────

func TestStatusFromOption(t *testing.T) {
	tests := []struct {
		opt  string
		live bool
		want Status
	}{
		{"working", true, StatusWorking},
		{"waiting", true, StatusWaiting},
		{"done", true, StatusDone},
		{"", true, StatusLive},             // live + unset → honest "attached, unknown"
		{"unknown-val", true, StatusLive},  // live + unrecognised → StatusLive
		{"working", false, StatusWorking},  // !live + known → still maps correctly
		{"", false, StatusIdle},            // !live + unset → idle
		{"unknown-val", false, StatusIdle}, // !live + unrecognised → idle
	}
	for _, tc := range tests {
		got := statusFromOption(tc.opt, tc.live)
		if got != tc.want {
			t.Errorf("statusFromOption(%q, %v) = %v, want %v", tc.opt, tc.live, got, tc.want)
		}
	}
}

// ── statusPollMsg applies statuses and preserves selection ────────────────────

// TestModel_StatusPollMsgAppliesStatus verifies that a statusPollMsg sets the
// correct Status on the matching live item and that the list selection index
// is preserved.
func TestModel_StatusPollMsgAppliesStatus(t *testing.T) {
	liveItem := item{
		id:        "sess1",
		title:     "live session",
		tool:      "claude",
		status:    StatusLive,
		live:      true,
		isSession: true,
	}
	idleItem := item{
		id:        "sess2",
		title:     "idle session",
		tool:      "opencode",
		status:    StatusIdle,
		live:      false,
		isSession: true,
	}
	m := New([]list.Item{liveItem, idleItem})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)
	// Select the second item so we can verify selection is preserved.
	updated1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated1.(Model)
	wantIdx := m.list.Index()

	// Deliver a statusPollMsg that marks sess1 as working.
	updated, _ := m.Update(statusPollMsg{statuses: map[string]string{"sess1": "working"}})
	m = updated.(Model)

	if m.list.Index() != wantIdx {
		t.Errorf("selection index after statusPollMsg = %d, want %d", m.list.Index(), wantIdx)
	}

	items := m.list.Items()
	it0 := items[0].(item)
	if it0.status != StatusWorking {
		t.Errorf("live item status = %v, want StatusWorking", it0.status)
	}

	// Idle item must not be touched.
	it1 := items[1].(item)
	if it1.status != StatusIdle {
		t.Errorf("idle item status = %v, want StatusIdle (must not be modified)", it1.status)
	}

	// polling flag must be cleared.
	if m.polling {
		t.Error("polling should be false after statusPollMsg")
	}
}

// TestModel_StatusPollMsgWaitingAndDone checks all three named states map correctly.
func TestModel_StatusPollMsgWaitingAndDone(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Status
	}{
		{"waiting", StatusWaiting},
		{"done", StatusDone},
		{"", StatusLive}, // empty → live fallback
	} {
		liveItem := item{id: "s", title: "t", tool: "claude", status: StatusLive, live: true, isSession: true}
		m := New([]list.Item{liveItem})
		updated0, _ := m.Update(windowMsg)
		m = updated0.(Model)

		updated, _ := m.Update(statusPollMsg{statuses: map[string]string{"s": tc.raw}})
		m = updated.(Model)

		got := m.list.Items()[0].(item).status
		if got != tc.want {
			t.Errorf("statusPollMsg(%q): got %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// ── statusPollCmd drop-guard direct tests ────────────────────────────────────

// TestStatusPollCmd_FiresWhenIdle verifies that statusPollCmd returns a non-nil
// cmd and sets polling=true when polling is false, a loader is set, and the list
// is not in filter mode.
func TestStatusPollCmd_FiresWhenIdle(t *testing.T) {
	ldr := loader{Tmux: tmux.Tmux{Runner: proc.NewFakeRunner()}}
	m := New(nil).WithLoader(ldr)
	m.polling = false

	cmd := m.statusPollCmd()
	if cmd == nil {
		t.Fatal("statusPollCmd with polling=false: expected non-nil cmd")
	}
	if !m.polling {
		t.Error("statusPollCmd must set polling=true when it fires")
	}
}

// TestStatusPollCmd_DropsWhenPolling verifies that statusPollCmd returns nil
// (dropped) when polling is already true. The loader is set so that polling is
// the sole cause of the nil return — the test would fail if the polling guard
// were removed even with a loader present.
func TestStatusPollCmd_DropsWhenPolling(t *testing.T) {
	ldr := loader{Tmux: tmux.Tmux{Runner: proc.NewFakeRunner()}}
	m := New(nil).WithLoader(ldr)
	m.polling = true

	cmd := m.statusPollCmd()
	if cmd != nil {
		t.Fatal("statusPollCmd with polling=true: expected nil (drop guard), got non-nil")
	}
}

// ── statusTickMsg always re-arms ─────────────────────────────────────────────

// TestModel_StatusTickMsgAlwaysRearms verifies that statusTickMsg always returns
// a non-nil cmd (the tick is re-armed) regardless of the polling state. The
// assertion is made on the returned model, not the discarded input.
func TestModel_StatusTickMsgAlwaysRearms(t *testing.T) {
	ldr := loader{Tmux: tmux.Tmux{Runner: proc.NewFakeRunner()}}
	m := New(nil).WithLoader(ldr)
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// polling=false: tick should re-arm AND fire a poll.
	m.polling = false
	newModel, cmd := m.Update(statusTickMsg{})
	returned := newModel.(Model)
	if cmd == nil {
		t.Fatal("statusTickMsg with polling=false: expected non-nil batch cmd")
	}
	// On the returned model, polling should be true (statusPollCmd set it).
	if !returned.polling {
		t.Error("polling should be true on the returned model after tick fires the poll cmd")
	}

	// polling=true: tick must re-arm but must NOT fire another poll.
	returned.polling = true
	newModel2, cmd2 := returned.Update(statusTickMsg{})
	returned2 := newModel2.(Model)
	if cmd2 == nil {
		t.Fatal("statusTickMsg with polling=true: expected non-nil batch (re-arm only)")
	}
	// polling stays true — no second poll was fired to reset it.
	if !returned2.polling {
		t.Error("polling changed unexpectedly on returned model during drop-guard tick")
	}
}

// ── glyph colour render doesn't panic ────────────────────────────────────────

// TestGlyphColourRenderNoPanic exercises all statuses through the delegate Render
// path to confirm no panic and that the glyph string appears in the output.
// Each status runs in its own subtest so a panic in one iteration is isolated
// and correctly fails only that subtest.
func TestGlyphColourRenderNoPanic(t *testing.T) {
	statuses := []Status{StatusWorking, StatusWaiting, StatusDone, StatusLive, StatusIdle}
	for _, st := range statuses {
		st := st // capture loop variable
		t.Run(st.glyph(), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Render panicked for status %v: %v", st, r)
				}
			}()

			it := item{id: "x", title: "test", tool: "claude", status: st, isSession: true}
			l := list.New([]list.Item{it}, itemDelegate{}, 80, 5)

			rendered := l.View()
			glyph := st.glyph()
			if rendered == "" {
				t.Errorf("status %v: rendered empty string", st)
			}
			// The glyph must appear somewhere in the rendered output.
			if len(glyph) > 0 && !containsRune(rendered, []rune(glyph)[0]) {
				t.Errorf("status %v: glyph %q not found in rendered output", st, glyph)
			}
		})
	}
}

// containsRune reports whether s contains r.
func containsRune(s string, r rune) bool {
	for _, ch := range s {
		if ch == r {
			return true
		}
	}
	return false
}

// ── statusPoll (loader) builds correct map ────────────────────────────────────

// TestLoaderStatusPoll verifies that statusPoll builds the sessionID→status map
// correctly: dead panes and empty-PerchSession panes are excluded; first-write-wins
// for duplicates; the status value from PerchStatus is preserved as-is.
func TestLoaderStatusPoll(t *testing.T) {
	// Build a 9-field stdout line: include PerchStatus as the 9th field.
	paneLine9 := func(paneID, sess, status string, dead bool) string {
		deadField := "0"
		if dead {
			deadField = "1"
		}
		fields := []string{paneID, "1234", "bash", deadField, "/work", "tmuxsess", "win", sess, status}
		out := ""
		for i, f := range fields {
			if i > 0 {
				out += "\x1f"
			}
			out += f
		}
		return out
	}

	lines := paneLine9("%1", "sess-a", "working", false) + "\n" +
		paneLine9("%2", "sess-b", "waiting", false) + "\n" +
		paneLine9("%3", "sess-a", "done", false) + "\n" + // duplicate: first-write-wins
		paneLine9("%4", "sess-c", "done", true) + "\n" + // dead: excluded
		paneLine9("%5", "", "working", false) + "\n" // empty PerchSession: excluded

	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte(lines)},
		"tmux", "list-panes", "-a", "-F", paneFormatFlag)

	l := loader{Tmux: tmux.Tmux{Runner: r}}
	msg := l.statusPoll()().(statusPollMsg)

	if msg.statuses["sess-a"] != "working" {
		t.Errorf("sess-a: got %q, want working (first-write-wins)", msg.statuses["sess-a"])
	}
	if msg.statuses["sess-b"] != "waiting" {
		t.Errorf("sess-b: got %q, want waiting", msg.statuses["sess-b"])
	}
	if _, ok := msg.statuses["sess-c"]; ok {
		t.Error("sess-c: dead pane must not appear in statuses")
	}
	if _, ok := msg.statuses[""]; ok {
		t.Error("empty PerchSession: must not appear in statuses")
	}
}
