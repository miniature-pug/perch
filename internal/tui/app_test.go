package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// windowMsg is a fixed size used across tests so the list has valid dimensions.
var windowMsg = tea.WindowSizeMsg{Width: 120, Height: 40}

// fixtures returns a set of items for testing.
// Items 0 and 1 contain "foo" in their title; items 2 and 3 do not.
// "foo" as a fuzzy query should match only items 0 and 1 but not "bazqux" or "xyzzy".
func fixtures() []list.Item {
	return []list.Item{
		item{title: "foobar", tool: "claude", status: StatusWorking, relTime: "1m ago", isSession: true},
		item{title: "foolish", tool: "opencode", status: StatusWaiting, relTime: "5m ago", isSession: true},
		item{title: "bazqux", tool: "claude", status: StatusDone, relTime: "1h ago", isSession: true},
		item{title: "xyzzy", tool: "opencode", status: StatusIdle, relTime: "2h ago", isSession: false},
	}
}

// sendAndSettle sends a message and waits a brief moment for the event loop to
// process it.  Used only for teatest-based sub-tests.
func sendAndSettle(tm *teatest.TestModel, msg tea.Msg) {
	tm.Send(msg)
	time.Sleep(20 * time.Millisecond)
}

// --- Test 1: j/k navigation moves selection index ---

func TestNavigationMovesIndex(t *testing.T) {
	m := New(fixtures())

	// Give the model a valid size before sending keys.
	updated, _ := m.Update(windowMsg)
	m = updated.(Model)

	// Initial index should be 0.
	if m.list.Index() != 0 {
		t.Fatalf("want initial index 0, got %d", m.list.Index())
	}

	// Press 'j' (down).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	if m.list.Index() != 1 {
		t.Fatalf("after j: want index 1, got %d", m.list.Index())
	}

	// Press 'k' (up) — should go back to 0.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = updated.(Model)
	if m.list.Index() != 0 {
		t.Fatalf("after k: want index 0, got %d", m.list.Index())
	}

	// Press down arrow.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.list.Index() != 1 {
		t.Fatalf("after down: want index 1, got %d", m.list.Index())
	}

	// Press up arrow.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.list.Index() != 0 {
		t.Fatalf("after up: want index 0, got %d", m.list.Index())
	}
}

// --- Test 2: typing /foo reduces VisibleItems to matching subset ---
//
// The bubbles list filter is asynchronous: each character update dispatches a
// tea.Cmd (filterItems) that computes matches and returns a FilterMatchesMsg.
// We manually execute each cmd and feed the resulting message back through
// Update so this test has no goroutine or timing dependency.

func TestFilterReducesVisibleItems(t *testing.T) {
	m := New(fixtures())

	// Give the model a valid size first.
	updated, _ := m.Update(windowMsg)
	m = updated.(Model)

	// Press '/' to enter filter mode.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(Model)

	if !m.list.SettingFilter() {
		t.Fatal("expected filter mode after '/'")
	}

	// Type 'f', 'o', 'o' one character at a time, executing the returned cmd
	// after each character so the FilterMatchesMsg is processed synchronously.
	for _, ch := range "foo" {
		var cmd tea.Cmd
		updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = updated.(Model)
		if cmd != nil {
			// Execute the cmd; it returns a FilterMatchesMsg (or a Batch of cmds).
			feedFilterMatchMsg(t, &m, cmd)
		}
	}

	visible := m.list.VisibleItems()
	if len(visible) == 0 || len(visible) >= 4 {
		t.Fatalf("expected 1–3 items matching 'foo', got %d", len(visible))
	}
	// Verify non-matching items are absent.
	for _, v := range visible {
		it := v.(item)
		// "bazqux" and "xyzzy" have no f…o…o subsequence.
		if it.title == "bazqux" || it.title == "xyzzy" {
			t.Errorf("non-matching item %q appeared in filtered results", it.title)
		}
	}
}

// feedFilterMatchMsg executes cmd (or unpacks a Batch) looking for a
// list.FilterMatchesMsg and feeds it back into the model.  It intentionally
// stops after one level to avoid following infinite blink/tick cmds.
func feedFilterMatchMsg(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	switch v := msg.(type) {
	case list.FilterMatchesMsg:
		// This is what we need: feed it back so filteredItems is updated.
		updated, _ := m.Update(v)
		*m = updated.(Model)
	case tea.BatchMsg:
		// Batch from the textinput: may contain the filterItems cmd and a blink
		// cmd.  Execute each sub-cmd but only process FilterMatchesMsg results.
		for _, c := range v {
			if c == nil {
				continue
			}
			sub := c()
			if fm, ok := sub.(list.FilterMatchesMsg); ok {
				updated, _ := m.Update(fm)
				*m = updated.(Model)
			}
			// Ignore blink/tick/nil results — they cause infinite loops.
		}
	}
}

// --- Test 3: Esc after filtering clears the filter and restores all items ---
//
// This test first narrows the list with a filter so there is something to
// restore, then verifies Esc brings all items back.

func TestEscClearsFilter(t *testing.T) {
	m := New(fixtures())
	updated, _ := m.Update(windowMsg)
	m = updated.(Model)

	// Enter filter mode with '/'.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(Model)
	if !m.list.SettingFilter() {
		t.Fatal("expected list to be in filtering mode after '/'")
	}

	// Type 'foo' to narrow the list, feeding the FilterMatchesMsg back
	// synchronously so the reduction is observable before we send Esc.
	for _, ch := range "foo" {
		var cmd tea.Cmd
		updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = updated.(Model)
		if cmd != nil {
			feedFilterMatchMsg(t, &m, cmd)
		}
	}

	// Confirm the list narrowed — without this we cannot verify "restore".
	if len(m.list.VisibleItems()) >= len(fixtures()) {
		t.Fatalf("filter did not narrow: still %d items", len(m.list.VisibleItems()))
	}

	// Esc while in Filtering state: list's CancelWhileFiltering clears the
	// filter and returns to Unfiltered, restoring all items.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if len(m.list.VisibleItems()) != len(fixtures()) {
		t.Fatalf("after esc: want %d visible items, got %d",
			len(fixtures()), len(m.list.VisibleItems()))
	}
}

// --- Test 4: q and ctrl+c both issue tea.Quit ---

func TestQuitIssuesTeatQuit(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{
			name: "q key",
			msg:  tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")},
		},
		{
			name: "ctrl+c",
			msg:  tea.KeyMsg{Type: tea.KeyCtrlC},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm := teatest.NewTestModel(
				t,
				New(fixtures()),
				teatest.WithInitialTermSize(120, 40),
			)
			time.Sleep(30 * time.Millisecond)

			sendAndSettle(tm, tc.msg)

			tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
			// If WaitFinished returns without error, the program exited cleanly.
		})
	}
}
