package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
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

// ── helpers for frame tests ───────────────────────────────────────────────────

// frameModel builds a Model pre-loaded with frame fields and a FakeRunner.
// placeholderPaneID is set, so inFrame() == true.
// displayedPaneID may be "" (nothing shown) or non-empty.
func frameModel(r *proc.FakeRunner, displayed string) Model {
	tmx := tmux.Tmux{
		Runner: r,
		Bin:    "tmux",
		Getenv: func(key string) string {
			if key == "TMUX" {
				return "/tmp/tmux-1000/default,1234,0"
			}
			return ""
		},
	}
	m := New(nil).WithLoader(loader{
		Tmux:    tmx,
		BaseDir: "/tmp/frame-test",
		Now:     1000,
	})
	m.frameSession = "perch"
	m.placeholderPaneID = "%PL"
	m.displayedPaneID = displayed
	return m
}

// collectSwapPaneCalls returns the args slices for every swap-pane call recorded.
func collectSwapPaneCalls(r *proc.FakeRunner) [][]string {
	var out [][]string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "swap-pane" {
			out = append(out, c.Args)
		}
	}
	return out
}

// hasPaneSizeCall returns true if FakeRunner recorded a display-message PaneSize
// call for the given pane id.
// PaneSize calls: args = ["display-message", "-p", "-t", paneID, "#{pane_width}\x1f#{pane_height}"]
func hasPaneSizeCall(r *proc.FakeRunner, paneID string) bool {
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "display-message" &&
			c.Args[3] == paneID && c.Args[4] == "#{pane_width}\x1f#{pane_height}" {
			return true
		}
	}
	return false
}

// hasResizeWindowCall returns true if FakeRunner recorded a resize-window call
// with the given target.
func hasResizeWindowCall(r *proc.FakeRunner, target string) bool {
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 3 && c.Args[0] == "resize-window" &&
			c.Args[2] == target {
			return true
		}
	}
	return false
}

// hasKillSessionCall returns true if the expected kill-session call was recorded.
func hasKillSessionCall(r *proc.FakeRunner, session string) bool {
	target := tmux.SessionTarget(session)
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 3 && c.Args[0] == "kill-session" &&
			c.Args[2] == target {
			return true
		}
	}
	return false
}

// ── inFrame helper ────────────────────────────────────────────────────────────

func TestInFrame_TrueWhenPlaceholderSet(t *testing.T) {
	m := New(nil)
	if m.inFrame() {
		t.Error("inFrame() on plain Model: want false")
	}
	m.placeholderPaneID = "%PL"
	if !m.inFrame() {
		t.Error("inFrame() with placeholderPaneID set: want true")
	}
}

// ── swapInCmd unit tests ──────────────────────────────────────────────────────

// TestSwapIn_NothingDisplayed verifies that when nothing is displayed and we
// request %A, we get: PaneSize(%PL), ResizeWindow(%A), SwapPane -s %A -t %PL,
// RefreshClient. swappedMsg.target == %A.
func TestSwapIn_NothingDisplayed(t *testing.T) {
	r := proc.NewFakeRunner()
	// Register PaneSize response for the placeholder.
	r.Respond(proc.FakeResult{Stdout: []byte("80\x1f24\n")},
		"tmux", "display-message", "-p", "-t", "%PL", "#{pane_width}\x1f#{pane_height}")
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "") // nothing displayed

	cmd := m.swapInCmd("%A")
	if cmd == nil {
		t.Fatal("swapInCmd: want non-nil cmd")
	}
	msg := cmd()
	sm, ok2 := msg.(swappedMsg)
	if !ok2 {
		t.Fatalf("want swappedMsg, got %T: %v", msg, msg)
	}
	if sm.err != nil {
		t.Fatalf("swappedMsg error: %v", sm.err)
	}
	if sm.noop {
		t.Error("want noop=false for a real swap")
	}
	if sm.target != "%A" {
		t.Errorf("swappedMsg.target = %q, want %%A", sm.target)
	}

	// Must have PaneSize on the placeholder (op.src == %A → op.dst == %PL).
	if !hasPaneSizeCall(r, "%PL") {
		t.Error("want PaneSize call on %PL (the destination slot), got none")
	}
	// Must have ResizeWindow on %A.
	if !hasResizeWindowCall(r, "%A") {
		t.Error("want ResizeWindow call on %A, got none")
	}

	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 1 {
		t.Fatalf("want 1 swap-pane call, got %d: %v", len(swaps), swaps)
	}
	// swap-pane -s %A -t %PL
	if swaps[0][2] != "%A" || swaps[0][4] != "%PL" {
		t.Errorf("swap-pane args = %v, want -s %%A -t %%PL", swaps[0])
	}
}

// TestSwapIn_SwitchAgents verifies switching from %A (currently displayed) to %B:
// two swaps in order: (-s %A -t %PL) then (-s %B -t %PL), with pre-size before
// the second swap. displayedPaneID becomes %B on swappedMsg.
func TestSwapIn_SwitchAgents(t *testing.T) {
	r := proc.NewFakeRunner()
	// PaneSize on %PL for the bring-in op (op.src == %B → op.dst == %PL).
	r.Respond(proc.FakeResult{Stdout: []byte("120\x1f40\n")},
		"tmux", "display-message", "-p", "-t", "%PL", "#{pane_width}\x1f#{pane_height}")
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A") // %A currently displayed

	cmd := m.swapInCmd("%B")
	if cmd == nil {
		t.Fatal("swapInCmd: want non-nil cmd")
	}
	msg := cmd()
	sm, ok2 := msg.(swappedMsg)
	if !ok2 {
		t.Fatalf("want swappedMsg, got %T", msg)
	}
	if sm.err != nil {
		t.Fatalf("swappedMsg error: %v", sm.err)
	}
	if sm.target != "%B" {
		t.Errorf("swappedMsg.target = %q, want %%B", sm.target)
	}

	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 2 {
		t.Fatalf("want 2 swap-pane calls, got %d: %v", len(swaps), swaps)
	}
	// First swap: send %A home.
	if swaps[0][2] != "%A" || swaps[0][4] != "%PL" {
		t.Errorf("first swap: want -s %%A -t %%PL, got %v", swaps[0])
	}
	// Second swap: bring %B in.
	if swaps[1][2] != "%B" || swaps[1][4] != "%PL" {
		t.Errorf("second swap: want -s %%B -t %%PL, got %v", swaps[1])
	}
	// PaneSize must have been called for the bring-in op.
	if !hasPaneSizeCall(r, "%PL") {
		t.Error("want PaneSize call on %PL for bring-in op, got none")
	}
	// ResizeWindow on %B.
	if !hasResizeWindowCall(r, "%B") {
		t.Error("want ResizeWindow call on %B, got none")
	}
}

// TestSwapIn_AlreadyDisplayed verifies that requesting the already-displayed
// pane produces a noop swappedMsg with no swap-pane calls.
func TestSwapIn_AlreadyDisplayed(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A") // %A already displayed

	cmd := m.swapInCmd("%A")
	if cmd == nil {
		t.Fatal("swapInCmd on already-displayed: want non-nil cmd (noop msg)")
	}
	msg := cmd()
	sm, ok2 := msg.(swappedMsg)
	if !ok2 {
		t.Fatalf("want swappedMsg, got %T", msg)
	}
	if !sm.noop {
		t.Error("want noop=true when target already displayed")
	}

	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 0 {
		t.Errorf("want 0 swap-pane calls for noop, got %d: %v", len(swaps), swaps)
	}
}

// TestSwapIn_NotInFrame verifies that Enter on a live session without frame
// context still calls the old switch-client (attach) path, not swap-pane.
func TestSwapIn_NotInFrame_UsesAttach(t *testing.T) {
	r := proc.NewFakeRunner()
	wantTarget := "=sess:=win"
	r.Respond(proc.FakeResult{}, "tmux", "switch-client", "-t", wantTarget)

	liveIt := item{
		tool:          "claude",
		tree:          "feat",
		id:            "live-sess-id",
		projectPath:   "/proj/myrepo",
		treePath:      "/proj/myrepo",
		isSession:     true,
		live:          true,
		liveTarget:    wantTarget,
		captureTarget: "%99",
	}
	m := New([]list.Item{liveIt}).WithLoader(loader{
		Tmux: tmux.Tmux{
			Runner: r,
			Bin:    "tmux",
			Getenv: func(key string) string {
				if key == "TMUX" {
					return "/tmp/tmux-1000/default,1234,0"
				}
				return ""
			},
		},
		BaseDir: t.TempDir(),
		Now:     1000,
	})
	// No frame fields set → inFrame() == false.
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on live item: want cmd")
	}
	msg := cmd()
	// Must be a switchedMsg (not swappedMsg) — attach path.
	sm, ok := msg.(switchedMsg)
	if !ok {
		t.Fatalf("want switchedMsg (attach path), got %T: %v", msg, msg)
	}
	if sm.err != nil {
		t.Fatalf("switchedMsg error: %v", sm.err)
	}
	// Must not have called swap-pane.
	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 0 {
		t.Errorf("want 0 swap-pane calls in non-frame mode, got %d", len(swaps))
	}
}

// ── swappedMsg handler ────────────────────────────────────────────────────────

// TestSwappedMsg_SetsDisplayedPaneID verifies the Update handler for swappedMsg
// sets displayedPaneID = msg.target and clears swapping.
func TestSwappedMsg_SetsDisplayedPaneID(t *testing.T) {
	m := New(nil)
	m.placeholderPaneID = "%PL"
	m.displayedPaneID = ""
	m.swapping = true

	updated, cmd := m.Update(swappedMsg{target: "%A"})
	m2 := updated.(Model)
	if m2.swapping {
		t.Error("swapping not cleared after swappedMsg")
	}
	if m2.displayedPaneID != "%A" {
		t.Errorf("displayedPaneID = %q, want %%A", m2.displayedPaneID)
	}
	_ = cmd
}

// TestSwappedMsg_Noop does not change displayedPaneID.
func TestSwappedMsg_Noop(t *testing.T) {
	m := New(nil)
	m.placeholderPaneID = "%PL"
	m.displayedPaneID = "%A"
	m.swapping = true

	updated, _ := m.Update(swappedMsg{target: "%A", noop: true})
	m2 := updated.(Model)
	if m2.swapping {
		t.Error("swapping not cleared after noop swappedMsg")
	}
	if m2.displayedPaneID != "%A" {
		t.Errorf("displayedPaneID changed on noop: got %q, want %%A", m2.displayedPaneID)
	}
}

// ── Enter on live session in-frame ────────────────────────────────────────────

// TestEnter_InFrame_LivePane verifies that Enter on a live pane when inFrame()
// dispatches swapInCmd (returns a cmd that produces swappedMsg), not switchedMsg.
func TestEnter_InFrame_LivePane(t *testing.T) {
	r := proc.NewFakeRunner()
	// PaneSize on %PL for bring-in.
	r.Respond(proc.FakeResult{Stdout: []byte("80\x1f24\n")},
		"tmux", "display-message", "-p", "-t", "%PL", "#{pane_width}\x1f#{pane_height}")
	ok := proc.FakeResult{}
	r.Default = &ok

	liveIt := item{
		tool:          "claude",
		tree:          "feat",
		id:            "live-sess-id",
		projectPath:   "/proj/myrepo",
		treePath:      "/proj/myrepo",
		isSession:     true,
		live:          true,
		liveTarget:    "=sess:=win",
		captureTarget: "%A",
	}
	m := New([]list.Item{liveIt}).WithLoader(loader{
		Tmux: tmux.Tmux{
			Runner: r,
			Bin:    "tmux",
			Getenv: func(key string) string {
				if key == "TMUX" {
					return "/tmp/tmux-1000/default,1234,0"
				}
				return ""
			},
		},
		BaseDir: t.TempDir(),
		Now:     1000,
	})
	m.frameSession = "perch"
	m.placeholderPaneID = "%PL"
	m.displayedPaneID = ""

	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	updated1, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated1.(Model)
	if !m.swapping {
		t.Error("swapping should be true after dispatching swapInCmd")
	}
	if cmd == nil {
		t.Fatal("Enter in-frame: want non-nil cmd")
	}

	msg := cmd()
	sm, ok2 := msg.(swappedMsg)
	if !ok2 {
		t.Fatalf("want swappedMsg, got %T: %v", msg, msg)
	}
	if sm.err != nil {
		t.Fatalf("swappedMsg error: %v", sm.err)
	}
	if sm.target != "%A" {
		t.Errorf("swappedMsg.target = %q, want %%A", sm.target)
	}

	// Must have swap-pane.
	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 1 {
		t.Fatalf("want 1 swap-pane call, got %d", len(swaps))
	}
}

// ── quitFrameCmd ─────────────────────────────────────────────────────────────

// TestQuitFrame_SwapsHomeThenKills verifies that quitFrameCmd emits swap-pane
// (send %A home) BEFORE kill-session for the frame session.
func TestQuitFrame_SwapsHomeThenKills(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "%A") // %A currently displayed

	cmd := m.quitFrameCmd()
	if cmd == nil {
		t.Fatal("quitFrameCmd: want non-nil cmd")
	}
	msg := cmd()
	// The returned msg must be tea.QuitMsg.
	if _, ok2 := msg.(tea.QuitMsg); !ok2 {
		t.Fatalf("want tea.QuitMsg, got %T: %v", msg, msg)
	}

	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 1 {
		t.Fatalf("want 1 swap-pane (swap home), got %d: %v", len(swaps), swaps)
	}
	if swaps[0][2] != "%A" || swaps[0][4] != "%PL" {
		t.Errorf("swap-home: want -s %%A -t %%PL, got %v", swaps[0])
	}

	// kill-session must appear AFTER the swap.
	if !hasKillSessionCall(r, "perch") {
		t.Error("want kill-session for frame session 'perch', got none")
	}

	// Verify ordering: swap before kill.
	swapIdx, killIdx := -1, -1
	for i, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "swap-pane" {
			swapIdx = i
		}
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "kill-session" {
			killIdx = i
		}
	}
	if swapIdx < 0 {
		t.Fatal("swap-pane not found in Calls")
	}
	if killIdx < 0 {
		t.Fatal("kill-session not found in Calls")
	}
	if swapIdx >= killIdx {
		t.Errorf("ordering violation: swap-pane (idx %d) must precede kill-session (idx %d)", swapIdx, killIdx)
	}
}

// TestQuitFrame_NothingDisplayed_JustKills verifies quitFrameCmd with nothing
// displayed: no swap-pane, just kill-session + tea.Quit.
func TestQuitFrame_NothingDisplayed_JustKills(t *testing.T) {
	r := proc.NewFakeRunner()
	ok := proc.FakeResult{}
	r.Default = &ok

	m := frameModel(r, "") // nothing displayed

	cmd := m.quitFrameCmd()
	msg := cmd()
	if _, ok2 := msg.(tea.QuitMsg); !ok2 {
		t.Fatalf("want tea.QuitMsg, got %T", msg)
	}
	swaps := collectSwapPaneCalls(r)
	if len(swaps) != 0 {
		t.Errorf("want 0 swaps when nothing displayed, got %d", len(swaps))
	}
	if !hasKillSessionCall(r, "perch") {
		t.Error("want kill-session even when nothing displayed")
	}
}

// ── launchedMsg pane field ────────────────────────────────────────────────────

// TestLaunchedMsg_InFrame_DispatchesSwapIn verifies that a launchedMsg in frame
// mode dispatches swapInCmd (returns swappedMsg), not attachTo (switchedMsg).
func TestLaunchedMsg_InFrame_DispatchesSwapIn(t *testing.T) {
	r := proc.NewFakeRunner()
	// PaneSize on placeholder for bring-in.
	r.Respond(proc.FakeResult{Stdout: []byte("80\x1f24\n")},
		"tmux", "display-message", "-p", "-t", "%PL", "#{pane_width}\x1f#{pane_height}")
	ok := proc.FakeResult{}
	r.Default = &ok

	m := New(nil).WithLoader(loader{
		Tmux: tmux.Tmux{
			Runner: r,
			Bin:    "tmux",
			Getenv: func(key string) string {
				if key == "TMUX" {
					return "/tmp/tmux-1000/default,1234,0"
				}
				return ""
			},
		},
		BaseDir: t.TempDir(),
		Now:     1000,
	})
	m.frameSession = "perch"
	m.placeholderPaneID = "%PL"
	m.displayedPaneID = ""

	// Simulate launchedMsg with a pane id.
	updated, cmd := m.Update(launchedMsg{session: "s", window: "w", pane: "%NEW"})
	m = updated.(Model)
	if !m.swapping {
		t.Error("swapping should be true after in-frame launchedMsg")
	}
	if cmd == nil {
		t.Fatal("in-frame launchedMsg: want non-nil cmd")
	}

	msg := cmd()
	sm, ok2 := msg.(swappedMsg)
	if !ok2 {
		t.Fatalf("want swappedMsg, got %T: %v", msg, msg)
	}
	if sm.err != nil {
		t.Fatalf("swappedMsg error: %v", sm.err)
	}
}

// TestLaunchedMsg_NotInFrame_UsesAttach verifies the non-frame launchedMsg path
// still calls switch-client (attachTo).
func TestLaunchedMsg_NotInFrame_UsesAttach(t *testing.T) {
	r := proc.NewFakeRunner()
	sess := "=perch-test"
	win := "=w1"
	target := tmux.WindowTarget("perch-test", "w1")
	r.Respond(proc.FakeResult{}, "tmux", "switch-client", "-t", target)

	m := New(nil).WithLoader(loader{
		Tmux: tmux.Tmux{
			Runner: r,
			Bin:    "tmux",
			Getenv: func(key string) string {
				if key == "TMUX" {
					return "/tmp/tmux-1000/default,1234,0"
				}
				return ""
			},
		},
		BaseDir: t.TempDir(),
		Now:     1000,
	})
	// No frame fields — inFrame() == false.

	_ = sess
	_ = win
	updated, cmd := m.Update(launchedMsg{session: "perch-test", window: "w1", pane: "%X"})
	_ = updated
	if cmd == nil {
		t.Fatal("non-frame launchedMsg: want cmd")
	}

	msg := cmd()
	sm, ok := msg.(switchedMsg)
	if !ok {
		t.Fatalf("want switchedMsg (attach path), got %T: %v", msg, msg)
	}
	if sm.err != nil {
		t.Fatalf("switchedMsg error: %v", sm.err)
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
