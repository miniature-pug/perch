package tui

// close_window_test.go contains all tests for the non-destructive close-window
// feature (M16-3): esc with a displayed pane swaps it home (keeps agent alive)
// and resets displayedPaneID to "".

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// ── closeWindowCmd unit tests ─────────────────────────────────────────────────

// TestCloseWindow_SwapsHomeAndResetsDisplayed verifies that closeWindowCmd with
// a displayed pane issues exactly one swap-pane (planSwapHome) and the returned
// message, when applied to the model, sets displayedPaneID == "".
func TestCloseWindow_SwapsHomeAndResetsDisplayed(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A") // %A currently displayed

	cmd := m.closeWindowCmd()
	if cmd == nil {
		t.Fatal("closeWindowCmd: want non-nil cmd")
	}
	msg := cmd()

	// The returned message must reset displayedPaneID when applied.
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q after closeWindowCmd, want empty", m2.displayedPaneID)
	}

	// Exactly one swap-pane: -s %A -t %PL (planSwapHome).
	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 1 {
		t.Fatalf("want 1 swap-pane call, got %d: %v", len(swaps), swaps)
	}
	if swaps[0][2] != "%A" || swaps[0][4] != "%PL" {
		t.Errorf("swap-home: want -s %%A -t %%PL, got %v", swaps[0])
	}

	// Must NOT kill the session.
	if hasKillSessionCall(r, "perch") {
		t.Error("closeWindowCmd must NOT kill the frame session")
	}
}

// TestCloseWindow_NothingDisplayed_Noop verifies that closeWindowCmd with
// displayedPaneID=="" issues ZERO tmux calls and returns a no-op message.
func TestCloseWindow_NothingDisplayed_Noop(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "") // nothing displayed

	cmd := m.closeWindowCmd()
	if cmd == nil {
		t.Fatal("closeWindowCmd: want non-nil cmd even for noop")
	}
	msg := cmd()

	updated, _ := m.Update(msg)
	m2 := updated.(Model)

	// displayedPaneID stays "" (was already "").
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q after noop closeWindowCmd, want empty", m2.displayedPaneID)
	}

	// ZERO tmux calls: no swap, no kill.
	if len(r.Calls) != 0 {
		t.Errorf("want 0 tmux calls for noop closeWindowCmd, got %d: %v", len(r.Calls), r.Calls)
	}
}

// TestCloseWindow_DeadPane_StillResetsDisplayed verifies graceful dead-pane
// handling: if the swap-home errors (pane no longer exists), closeWindowCmd
// must still reset displayedPaneID="" and must not panic or treat it as fatal.
func TestCloseWindow_DeadPane_StillResetsDisplayed(t *testing.T) {
	r := proc.NewFakeRunner()
	// Make swap-pane return an error (simulate dead pane).
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "swap-pane", "-d", "-s", "%A", "-t", "%PL")

	m := frameModel(r, "%A")

	cmd := m.closeWindowCmd()
	if cmd == nil {
		t.Fatal("closeWindowCmd: want non-nil cmd")
	}
	// Must not panic.
	msg := cmd()

	// Even with swap error, model should reset displayedPaneID.
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("dead-pane: displayedPaneID = %q after closeWindowCmd, want empty", m2.displayedPaneID)
	}
}

// ── quitFrameCmd dead-pane regression ────────────────────────────────────────

// TestQuitFrame_DeadPane_StillKillsAndQuits verifies that quitFrameCmd survives
// a dead displayed pane: the swap-home may error, but kill-session and QuitMsg
// still happen.
func TestQuitFrame_DeadPane_StillKillsAndQuits(t *testing.T) {
	r := proc.NewFakeRunner()
	// swap-pane errors (dead pane), kill-session and everything else succeed.
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "swap-pane", "-d", "-s", "%A", "-t", "%PL")
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A")

	cmd := m.quitFrameCmd()
	if cmd == nil {
		t.Fatal("quitFrameCmd: want non-nil cmd")
	}
	msg := cmd()

	// Must still return QuitMsg.
	if _, ok2 := msg.(tea.QuitMsg); !ok2 {
		t.Fatalf("want tea.QuitMsg even with dead pane, got %T: %v", msg, msg)
	}
	// kill-session must still have run.
	if !hasKillSessionCall(r, "perch") {
		t.Error("want kill-session even when swap-home failed (dead pane)")
	}
}

// ── esc key handler tests ─────────────────────────────────────────────────────

// TestEsc_NoFilter_DisplayedPane_TriggersCloseWindow verifies that esc with no
// active filter and a displayed pane triggers closeWindowCmd (issues swap-pane).
func TestEsc_NoFilter_DisplayedPane_TriggersCloseWindow(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A") // %A displayed, no filter

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc with displayed pane: want non-nil cmd")
	}

	// Execute the cmd and check a swap happened.
	msg := cmd()
	updated, _ := m.Update(msg)
	m2 := updated.(Model)

	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q after esc close-window, want empty", m2.displayedPaneID)
	}

	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 1 {
		t.Fatalf("esc close-window: want 1 swap-pane, got %d: %v", len(swaps), swaps)
	}
}

// TestEsc_FilterApplied_DisplayedPane_ClearsFilterNotSwap verifies that esc
// while a filter is applied (FilterApplied state) clears the filter and does
// NOT issue any swap-pane, even when a pane is displayed.
func TestEsc_FilterApplied_DisplayedPane_ClearsFilterNotSwap(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	items := fixtures()
	m := New(items).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: "/tmp/close-test",
		Now:     1000,
	})
	m.frameSession = "perch"
	m.placeholderPaneID = "%PL"
	m.displayedPaneID = "%A"

	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// Enter filter mode.
	updated1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated1.(Model)
	if !m.list.SettingFilter() {
		t.Fatal("precondition: list must be in filtering mode after '/'")
	}

	// Type a character to narrow the filter.
	for _, ch := range "foo" {
		var cmd tea.Cmd
		updated1, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = updated1.(Model)
		if cmd != nil {
			feedFilterMatchMsg(t, &m, cmd)
		}
	}

	// Accept filter (press Enter to leave Filtering → FilterApplied state).
	updated2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated2.(Model)
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("precondition: want FilterApplied after accepting filter, got %v", m.list.FilterState())
	}

	// Now esc: should clear filter, NOT close window.
	r.Calls = nil // reset recorded calls before esc
	updated3, cmd3 := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated3.(Model)

	if cmd3 != nil {
		// Execute any cmd returned; it should be previewCmd, not swap.
		cmd3()
	}

	// No swap-pane must have been issued.
	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 0 {
		t.Errorf("esc with filter applied: want 0 swap-pane calls, got %d: %v", len(swaps), swaps)
	}

	// displayedPaneID must still be %A (filter clear, not window close).
	if m.displayedPaneID != "%A" {
		t.Errorf("displayedPaneID = %q after filter-clear esc, want %%A", m.displayedPaneID)
	}

	// Filter should be reset.
	if m.list.FilterState() != list.Unfiltered {
		t.Errorf("filter state = %v after esc, want Unfiltered", m.list.FilterState())
	}
}

// TestEsc_NoFilter_NothingDisplayed_Noop verifies that esc with no filter and
// no displayed pane is a no-op (no swap, no error).
func TestEsc_NoFilter_NothingDisplayed_Noop(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "") // nothing displayed

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if cmd != nil {
		// Execute to check no swap happens.
		cmd()
	}

	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 0 {
		t.Errorf("esc noop: want 0 swap-pane calls, got %d: %v", len(swaps), swaps)
	}
}
