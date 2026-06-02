package tui

// recover_test.go covers M17-4: recovering the frame when a displayed agent's
// process exits (the displayed pane goes dead under remain-on-exit). The
// recovery swaps the live placeholder back into the frame main slot, kills the
// now-exiled dead pane (so the orphaned-alive placeholder is not leaked and the
// agent's resume stays clean), focuses the sidebar, and resets displayedPaneID.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// collectKillPaneCalls returns the args slices for every kill-pane call recorded.
func collectKillPaneCalls(r *proc.FakeRunner) [][]string {
	var out [][]string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "kill-pane" {
			out = append(out, c.Args)
		}
	}
	return out
}

// collectSelectPaneLeftCalls returns the args slices for every `select-pane -L`
// (sidebar focus) call recorded.
func collectSelectPaneLeftCalls(r *proc.FakeRunner) [][]string {
	var out [][]string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 2 && c.Args[0] == "select-pane" && c.Args[1] == "-L" {
			out = append(out, c.Args)
		}
	}
	return out
}

// respondPaneDead registers a FakeRunner response for PaneDead(target): "1" when
// dead, "0" when alive.
func respondPaneDead(r *proc.FakeRunner, target string, dead bool) {
	out := "0\n"
	if dead {
		out = "1\n"
	}
	r.Respond(proc.FakeResult{Stdout: []byte(out)},
		"tmux", "display-message", "-t", target, "-p", "#{pane_dead}")
}

// ── recoverDeadDisplayedCmd ───────────────────────────────────────────────────

// TestRecoverDeadDisplayed_SwapKillFocusReset verifies the full recovery
// sequence: swap-home (placeholder back into the frame), kill-pane of the
// now-exiled dead displayed pane, focus the sidebar (select-pane -L), and a
// returned message that resets displayedPaneID="" while leaving placeholderPaneID
// untouched.
func TestRecoverDeadDisplayed_SwapKillFocusReset(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A") // %A displayed (now dead), %PL placeholder

	cmd := m.recoverDeadDisplayedCmd()
	if cmd == nil {
		t.Fatal("recoverDeadDisplayedCmd: want non-nil cmd")
	}
	msg := cmd()

	// Applying the message must reset displayedPaneID and leave placeholder alone.
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q after recovery, want empty", m2.displayedPaneID)
	}
	if m2.placeholderPaneID != "%PL" {
		t.Errorf("placeholderPaneID = %q after recovery, want %%PL (unchanged)", m2.placeholderPaneID)
	}

	// Exactly one swap-pane: -s %A -t %PL (planSwapHome).
	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 1 {
		t.Fatalf("want 1 swap-pane call, got %d: %v", len(swaps), swaps)
	}
	if swaps[0][2] != "%A" || swaps[0][4] != "%PL" {
		t.Errorf("swap-home: want -s %%A -t %%PL, got %v", swaps[0])
	}

	// Exactly one kill-pane targeting the now-exiled dead displayed pane %A.
	kills := collectKillPaneCalls(r)
	if len(kills) != 1 {
		t.Fatalf("want 1 kill-pane call, got %d: %v", len(kills), kills)
	}
	if kills[0][2] != "%A" {
		t.Errorf("kill-pane: want -t %%A, got %v", kills[0])
	}

	// Sidebar focus via select-pane -L.
	if got := len(collectSelectPaneLeftCalls(r)); got != 1 {
		t.Errorf("want 1 select-pane -L (sidebar focus), got %d", got)
	}

	// Must NOT kill the frame session.
	if hasKillSessionCall(r, "perch") {
		t.Error("recoverDeadDisplayedCmd must NOT kill the frame session")
	}
}

// TestRecoverDeadDisplayed_OrderSwapBeforeKill verifies the swap-home runs
// BEFORE kill-pane: killing first would destroy the dead pane while it still
// occupies the frame main slot (placeholder still exiled). The swap must move
// the live placeholder back in first, exiling the dead pane, only then kill it.
func TestRecoverDeadDisplayed_OrderSwapBeforeKill(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A")
	cmd := m.recoverDeadDisplayedCmd()
	cmd()

	var swapIdx, killIdx = -1, -1
	for i, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 {
			if c.Args[0] == "swap-pane" && swapIdx == -1 {
				swapIdx = i
			}
			if c.Args[0] == "kill-pane" && killIdx == -1 {
				killIdx = i
			}
		}
	}
	if swapIdx == -1 || killIdx == -1 {
		t.Fatalf("missing swap (%d) or kill (%d) call", swapIdx, killIdx)
	}
	if swapIdx > killIdx {
		t.Errorf("swap-pane (idx %d) must precede kill-pane (idx %d)", swapIdx, killIdx)
	}
}

// TestRecoverDeadDisplayed_NothingDisplayed_Noop verifies recoverDeadDisplayedCmd
// is a no-op (no tmux calls) when displayedPaneID=="".
func TestRecoverDeadDisplayed_NothingDisplayed_Noop(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "") // nothing displayed

	cmd := m.recoverDeadDisplayedCmd()
	if cmd == nil {
		t.Fatal("recoverDeadDisplayedCmd: want non-nil cmd even for noop")
	}
	msg := cmd()
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q, want empty", m2.displayedPaneID)
	}
	if len(r.Calls) != 0 {
		t.Errorf("want 0 tmux calls for noop recovery, got %d: %v", len(r.Calls), r.Calls)
	}
}

// TestRecoverDeadDisplayed_SwapErrorStillKillsAndResets verifies best-effort
// handling: even when swap-home errors, the kill, focus, and reset still happen.
func TestRecoverDeadDisplayed_SwapErrorStillKillsAndResets(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok
	// swap-pane errors.
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "swap-pane", "-s", "%A", "-t", "%PL")

	m := frameModel(r, "%A")
	cmd := m.recoverDeadDisplayedCmd()
	msg := cmd()
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q after swap-error recovery, want empty", m2.displayedPaneID)
	}
	if len(collectKillPaneCalls(r)) != 1 {
		t.Error("want kill-pane even when swap-home errored")
	}
}

// ── tick detection ────────────────────────────────────────────────────────────

// TestTick_DeadDisplayedPane_TriggersRecovery verifies the self-heal path: a
// statusTickMsg while in-frame with a displayed pane fires a deadness probe; when
// the probe reports dead, the resulting message triggers the recovery (swap+kill).
func TestTick_DeadDisplayedPane_TriggersRecovery(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok
	respondPaneDead(r, "%A", true) // displayed pane is dead

	m := frameModel(r, "%A")
	// A loaded model is needed so statusPollCmd doesn't nil-panic; frameModel's
	// loader is set, so the tick batch is well-formed.

	_, cmd := m.Update(statusTickMsg{})
	if cmd == nil {
		t.Fatal("tick: want non-nil cmd batch")
	}
	// Drain the batch: one of the cmds is the deadness probe → its msg feeds back.
	msgs := drainBatch(t, cmd)
	var checked *displayedPaneCheckedMsg
	for _, msg := range msgs {
		if dm, ok := msg.(displayedPaneCheckedMsg); ok {
			checked = &dm
		}
	}
	if checked == nil {
		t.Fatal("tick with displayed pane: want a displayedPaneCheckedMsg in the batch")
	}
	if !checked.dead {
		t.Fatal("probe reported alive, want dead")
	}

	// Feeding the dead-checked msg back must dispatch the recovery (swap + kill).
	r.Calls = nil
	_, recCmd := m.Update(*checked)
	if recCmd == nil {
		t.Fatal("dead displayedPaneCheckedMsg: want a recovery cmd")
	}
	recCmd()
	if len(collectSwapPaneCalls(r)) != 1 {
		t.Errorf("recovery: want 1 swap-pane, got %d", len(collectSwapPaneCalls(r)))
	}
	if len(collectKillPaneCalls(r)) != 1 {
		t.Errorf("recovery: want 1 kill-pane, got %d", len(collectKillPaneCalls(r)))
	}
}

// TestTick_LiveDisplayedPane_NoRecovery verifies that a tick with a LIVE
// displayed pane does NOT trigger recovery: the deadness probe reports alive and
// no swap/kill is issued.
func TestTick_LiveDisplayedPane_NoRecovery(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok
	respondPaneDead(r, "%A", false) // displayed pane alive

	m := frameModel(r, "%A")

	_, cmd := m.Update(statusTickMsg{})
	msgs := drainBatch(t, cmd)
	var checked *displayedPaneCheckedMsg
	for _, msg := range msgs {
		if dm, ok := msg.(displayedPaneCheckedMsg); ok {
			checked = &dm
		}
	}
	if checked == nil {
		t.Fatal("want a displayedPaneCheckedMsg")
	}
	if checked.dead {
		t.Fatal("probe reported dead, want alive")
	}

	r.Calls = nil
	_, recCmd := m.Update(*checked)
	if recCmd != nil {
		// If a cmd is returned it must NOT issue any swap/kill.
		recCmd()
	}
	if n := len(collectKillPaneCalls(r)); n != 0 {
		t.Errorf("live displayed pane: want 0 kill-pane, got %d", n)
	}
	if n := len(collectSwapPaneCalls(r)); n != 0 {
		t.Errorf("live displayed pane: want 0 swap-pane, got %d", n)
	}
}

// TestTick_NothingDisplayed_NoProbe verifies that a tick with no displayed pane
// does not fire a deadness probe (no display-message #{pane_dead} call).
func TestTick_NothingDisplayed_NoProbe(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "") // nothing displayed

	_, cmd := m.Update(statusTickMsg{})
	msgs := drainBatch(t, cmd)
	for _, msg := range msgs {
		if _, ok := msg.(displayedPaneCheckedMsg); ok {
			t.Error("nothing displayed: must not emit displayedPaneCheckedMsg")
		}
	}
}

// drainBatch runs cmd, unpacking a tea.BatchMsg into its constituent messages by
// invoking each child cmd. Non-batch cmds yield a single message.
func drainBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		if c == nil {
			continue
		}
		out = append(out, drainBatch(t, c)...)
	}
	return out
}

// ── closeWindowCmd alive vs dead ──────────────────────────────────────────────

// TestCloseWindow_LiveDisplayed_SwapNoKill verifies that closing the window with
// a LIVE displayed pane swaps it home (agent survives) and does NOT kill-pane.
func TestCloseWindow_LiveDisplayed_SwapNoKill(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok
	respondPaneDead(r, "%A", false) // alive

	m := frameModel(r, "%A")
	cmd := m.closeWindowCmd()
	if cmd == nil {
		t.Fatal("closeWindowCmd: want non-nil cmd")
	}
	msg := cmd()
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q after close, want empty", m2.displayedPaneID)
	}

	if len(collectSwapPaneCalls(r)) != 1 {
		t.Errorf("live close: want 1 swap-pane, got %d", len(collectSwapPaneCalls(r)))
	}
	if n := len(collectKillPaneCalls(r)); n != 0 {
		t.Errorf("live close: want 0 kill-pane (agent survives), got %d", n)
	}
}

// TestCloseWindow_DeadDisplayed_SwapAndKill verifies that closing the window with
// a DEAD displayed pane performs full recovery: swap home + kill-pane the exiled
// dead pane + focus sidebar + reset.
func TestCloseWindow_DeadDisplayed_SwapAndKill(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok
	respondPaneDead(r, "%A", true) // dead

	m := frameModel(r, "%A")
	cmd := m.closeWindowCmd()
	if cmd == nil {
		t.Fatal("closeWindowCmd: want non-nil cmd")
	}
	msg := cmd()
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q after dead close, want empty", m2.displayedPaneID)
	}

	if len(collectSwapPaneCalls(r)) != 1 {
		t.Errorf("dead close: want 1 swap-pane, got %d", len(collectSwapPaneCalls(r)))
	}
	kills := collectKillPaneCalls(r)
	if len(kills) != 1 {
		t.Fatalf("dead close: want 1 kill-pane, got %d: %v", len(kills), kills)
	}
	if kills[0][2] != "%A" {
		t.Errorf("dead close kill-pane: want -t %%A, got %v", kills[0])
	}
	if len(collectSelectPaneLeftCalls(r)) != 1 {
		t.Error("dead close: want select-pane -L (sidebar focus)")
	}
}

// TestCloseWindow_NothingDisplayed_NoopNoProbe verifies the no-op guard still
// holds: with displayedPaneID=="" no tmux calls (including the deadness probe)
// are issued.
func TestCloseWindow_NothingDisplayed_NoopNoProbe(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "")
	cmd := m.closeWindowCmd()
	if cmd == nil {
		t.Fatal("closeWindowCmd: want non-nil cmd even for noop")
	}
	msg := cmd()
	updated, _ := m.Update(msg)
	m2 := updated.(Model)
	if m2.displayedPaneID != "" {
		t.Errorf("displayedPaneID = %q, want empty", m2.displayedPaneID)
	}
	if len(r.Calls) != 0 {
		t.Errorf("noop close: want 0 tmux calls, got %d: %v", len(r.Calls), r.Calls)
	}
}
