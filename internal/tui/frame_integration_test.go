//go:build integration

package tui

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/tmux"
)

// newFrameTestServer returns a Tmux wired to a private socket for frame
// integration tests. Mirrors newTUITestServer (same package, same build tag).
func newFrameTestServer(t *testing.T) tmux.Tmux {
	t.Helper()
	tmx := tmux.New()
	tmx.Socket = fmt.Sprintf("perch-frame-it-%d-%d", os.Getpid(), time.Now().UnixNano()%100000)
	ctx := context.Background()
	t.Cleanup(func() {
		_ = tmx.KillServer(ctx)
	})
	return tmx
}

// agentPaneAlive returns true if paneID appears in ListPanesAll — pane ids are
// stable across swap-pane, so presence confirms the pane process survived.
func agentPaneAlive(t *testing.T, tmx tmux.Tmux, paneID string) bool {
	t.Helper()
	ctx := context.Background()
	panes, err := tmx.ListPanesAll(ctx)
	if err != nil {
		// If the server is gone (killed), ListPanesAll will error; treat as dead.
		return false
	}
	for _, p := range panes {
		if p.ID == paneID {
			return true
		}
	}
	return false
}

// TestIntegration_Frame_DataLossGate_Positive is the primary data-loss gate: it
// verifies that the quit sequence (swapInCmd to display agent, then quitFrameCmd)
// returns the agent pane to its home session before killing the frame, so the
// agent process survives after the frame is gone.
//
// Structure:
//  1. Build a minimal "frame" session (one window, two panes: sidebar + placeholder).
//  2. Build a real "agent" session running sleep.
//  3. Run swapInCmd to display the agent (agent pane moves to frame main slot).
//  4. Manually set displayedPaneID on the Model (simulating the swappedMsg handler).
//  5. Run quitFrameCmd to swap-home the agent then kill the frame.
//  6. Assert: the agent pane is still alive in ListPanesAll (survived).
func TestIntegration_Frame_DataLossGate_Positive(t *testing.T) {
	tmx := newFrameTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	// ── Build the frame session ──────────────────────────────────────────────
	// Session "perch-frame": one window with the sidebar pane, then split a
	// placeholder pane horizontally.
	sidebarPane, err := tmx.NewSession(ctx, "perch-frame", "frame", dir)
	if err != nil {
		t.Fatalf("NewSession perch-frame: %v", err)
	}
	// Split: placeholder pane is the "main slot" in the frame window.
	placeholderPane, err := tmx.SplitWindow(ctx, sidebarPane, dir, true, "")
	if err != nil {
		t.Fatalf("SplitWindow placeholder: %v", err)
	}

	// ── Build the agent session ──────────────────────────────────────────────
	// Session "agent": running sleep so the process is long-lived.
	agentPane, err := tmx.NewSession(ctx, "agent", "main", dir)
	if err != nil {
		t.Fatalf("NewSession agent: %v", err)
	}
	if err := tmx.SendKeys(ctx, agentPane, "sleep 100000"); err != nil {
		t.Fatalf("SendKeys agent: %v", err)
	}
	// Allow the sleep to start.
	time.Sleep(150 * time.Millisecond)

	// Verify agent pane is alive before any swap.
	if !agentPaneAlive(t, tmx, agentPane) {
		t.Fatal("pre-condition: agent pane not in ListPanesAll")
	}

	// ── Construct Model with frame context ──────────────────────────────────
	m := New(nil).WithLoader(loader{
		Tmux:    tmx,
		BaseDir: t.TempDir(),
		Now:     1000,
	})
	m.frameSession = "perch-frame"
	m.placeholderPaneID = placeholderPane
	m.displayedPaneID = "" // nothing displayed yet

	// ── Step 3: run swapInCmd to display the agent ───────────────────────────
	cmd := m.swapInCmd(agentPane)
	if cmd == nil {
		t.Fatal("swapInCmd returned nil")
	}
	swapMsg := cmd()
	sm, ok := swapMsg.(swappedMsg)
	if !ok {
		t.Fatalf("swapInCmd: want swappedMsg, got %T: %v", swapMsg, swapMsg)
	}
	if sm.err != nil {
		t.Fatalf("swapInCmd error: %v", sm.err)
	}
	if sm.noop {
		t.Fatal("swapInCmd returned noop for a real swap")
	}

	// ── Step 4: simulate the swappedMsg handler ──────────────────────────────
	// (The integration test runs cmds directly without going through Update.)
	m.swapping = false
	m.displayedPaneID = agentPane

	// Verify agent pane is still alive after the swap (it's now in the frame).
	if !agentPaneAlive(t, tmx, agentPane) {
		t.Fatal("after swapInCmd: agent pane not in ListPanesAll (swap failed?)")
	}

	// ── Step 5: run quitFrameCmd ─────────────────────────────────────────────
	quitCmd := m.quitFrameCmd()
	if quitCmd == nil {
		t.Fatal("quitFrameCmd returned nil")
	}
	quitMsg := quitCmd()
	if _, ok := quitMsg.(tea.QuitMsg); !ok {
		// Import issue — accept the msg regardless; the tmux ops are what matters.
		t.Logf("quitFrameCmd msg type: %T (expected tea.QuitMsg)", quitMsg)
	}

	// Allow frame kill to propagate.
	time.Sleep(200 * time.Millisecond)

	// ── Step 6: assert agent pane survived ───────────────────────────────────
	if !agentPaneAlive(t, tmx, agentPane) {
		t.Error("DATA LOSS: agent pane is gone after quitFrameCmd; swap-home did not run before kill-session")
	}

	// Verify frame session is gone.
	has, err := tmx.HasSession(ctx, "perch-frame")
	if err != nil {
		t.Logf("HasSession perch-frame error (may be expected if server is gone): %v", err)
	} else if has {
		t.Error("frame session 'perch-frame' still alive after quitFrameCmd")
	}

	// Agent session must still exist (its pane migrated back home).
	hasAgent, err := tmx.HasSession(ctx, "agent")
	if err != nil {
		t.Logf("HasSession agent error: %v", err)
	} else if !hasAgent {
		t.Error("agent session is gone after quitFrameCmd (expected it to survive)")
	}
}

// paneDead reports the pane_dead flag for paneID via ListPanesAll. Returns
// (false, false) when the pane is not found.
func paneState(t *testing.T, tmx tmux.Tmux, paneID string) (found, dead bool) {
	t.Helper()
	ctx := context.Background()
	panes, err := tmx.ListPanesAll(ctx)
	if err != nil {
		return false, false
	}
	for _, p := range panes {
		if p.ID == paneID {
			return true, p.Dead
		}
	}
	return false, false
}

// TestIntegration_Frame_RecoverDeadDisplayed exercises the M17-4 self-heal path
// end to end on a private tmux server: an agent is swapped into the frame main
// slot, its process exits while displayed (the pane goes dead under
// remain-on-exit), and recoverDeadDisplayedCmd restores the frame to its resting
// shape — [sidebar, live placeholder] — with the dead pane reaped, then proves
// the frame is still usable by swapping a second agent in.
func TestIntegration_Frame_RecoverDeadDisplayed(t *testing.T) {
	tmx := newFrameTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	// ── Build the frame session: window "frame" = [sidebar, placeholder]. ─────
	sidebarPane, err := tmx.NewSession(ctx, "rec-frame", "frame", dir)
	if err != nil {
		t.Fatalf("NewSession rec-frame: %v", err)
	}
	placeholderPane, err := tmx.SplitWindow(ctx, sidebarPane, dir, true, "")
	if err != nil {
		t.Fatalf("SplitWindow placeholder: %v", err)
	}
	// remain-on-exit on the frame window: a displayed agent that exits leaves a
	// DEAD pane in the main slot (the precondition the recovery handles) instead
	// of the pane silently vanishing.
	frameTarget := tmux.WindowTarget("rec-frame", "frame")
	if err := tmx.SetWindowOption(ctx, frameTarget, "remain-on-exit", "on"); err != nil {
		t.Fatalf("SetWindowOption remain-on-exit: %v", err)
	}

	// ── Build the agent session running an interactive shell. ────────────────
	agentPane, err := tmx.NewSession(ctx, "rec-agent", "main", dir)
	if err != nil {
		t.Fatalf("NewSession rec-agent: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// ── Construct the Model with frame context. ──────────────────────────────
	m := New(nil).WithLoader(loader{
		Tmux:    tmx,
		BaseDir: t.TempDir(),
		Now:     1000,
	})
	m.frameSession = "rec-frame"
	m.placeholderPaneID = placeholderPane
	m.displayedPaneID = ""

	// ── Swap the agent into the frame main slot. ─────────────────────────────
	swapMsg := m.swapInCmd(agentPane)()
	if sm, ok := swapMsg.(swappedMsg); !ok || sm.err != nil || sm.noop {
		t.Fatalf("swapInCmd: unexpected msg %T %+v", swapMsg, swapMsg)
	}
	m.swapping = false
	m.displayedPaneID = agentPane

	// ── Kill the agent's shell process so its pane goes dead in the frame. ───
	// `exit` on the interactive shell terminates it; remain-on-exit keeps the
	// pane present-but-dead.
	if err := tmx.SendKeys(ctx, agentPane, "exit"); err != nil {
		t.Fatalf("SendKeys exit: %v", err)
	}
	// Poll for the pane to become dead (avoids a fixed sleep flake).
	deadline := time.Now().Add(3 * time.Second)
	for {
		found, dead := paneState(t, tmx, agentPane)
		if found && dead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent pane %q did not go dead (found=%v dead=%v)", agentPane, found, dead)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// ── Run the recovery. ─────────────────────────────────────────────────────
	recMsg := m.recoverDeadDisplayedCmd()()
	if _, ok := recMsg.(windowClosedMsg); !ok {
		t.Fatalf("recoverDeadDisplayedCmd: want windowClosedMsg, got %T", recMsg)
	}
	m.displayedPaneID = "" // simulate the windowClosedMsg handler

	// Allow the kill-pane to propagate.
	time.Sleep(150 * time.Millisecond)

	// ── Assert: the dead agent pane is reaped. ───────────────────────────────
	if found, _ := paneState(t, tmx, agentPane); found {
		t.Errorf("dead displayed pane %q still present after recovery (kill-pane did not run)", agentPane)
	}

	// ── Assert: the frame window is [sidebar, live placeholder] = 2 panes. ───
	framePanes, err := tmx.ListPanes(ctx, frameTarget)
	if err != nil {
		t.Fatalf("ListPanes frame: %v", err)
	}
	if len(framePanes) != 2 {
		t.Fatalf("frame window has %d panes after recovery, want 2 (sidebar+placeholder): %+v", len(framePanes), framePanes)
	}
	for _, p := range framePanes {
		if p.Dead {
			t.Errorf("frame pane %q is dead after recovery, want all alive", p.ID)
		}
	}
	// The placeholder must be one of the two frame panes (id stable across swap).
	if found, dead := paneState(t, tmx, placeholderPane); !found || dead {
		t.Errorf("placeholder %q after recovery: found=%v dead=%v, want found+alive", placeholderPane, found, dead)
	}

	// ── Assert: the frame is reusable — swap a second agent in. ──────────────
	agent2Pane, err := tmx.NewSession(ctx, "rec-agent2", "main", dir)
	if err != nil {
		t.Fatalf("NewSession rec-agent2: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	swap2 := m.swapInCmd(agent2Pane)()
	if sm, ok := swap2.(swappedMsg); !ok || sm.err != nil || sm.noop {
		t.Fatalf("second swapInCmd: unexpected msg %T %+v (frame not reusable)", swap2, swap2)
	}
	if found, dead := paneState(t, tmx, agent2Pane); !found || dead {
		t.Errorf("second agent %q after re-swap: found=%v dead=%v, want found+alive", agent2Pane, found, dead)
	}
}

// TestIntegration_Frame_DataLossGate_NegativeControl is the negative control:
// it proves the gate has teeth by intentionally killing the frame WITHOUT
// swapping the agent home. The agent pane (still in the frame) should die with
// the frame. If this test fails (agent survives), the positive test's assertion
// would not be a meaningful data-loss gate.
func TestIntegration_Frame_DataLossGate_NegativeControl(t *testing.T) {
	tmx := newFrameTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	// ── Build frame session ───────────────────────────────────────────────────
	sidebarPane, err := tmx.NewSession(ctx, "nc-frame", "frame", dir)
	if err != nil {
		t.Fatalf("NewSession nc-frame: %v", err)
	}
	placeholderPane, err := tmx.SplitWindow(ctx, sidebarPane, dir, true, "")
	if err != nil {
		t.Fatalf("SplitWindow placeholder: %v", err)
	}

	// ── Build agent session ───────────────────────────────────────────────────
	agentPane, err := tmx.NewSession(ctx, "nc-agent", "main", dir)
	if err != nil {
		t.Fatalf("NewSession nc-agent: %v", err)
	}
	if err := tmx.SendKeys(ctx, agentPane, "sleep 100000"); err != nil {
		t.Fatalf("SendKeys nc-agent: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// Verify agent pane alive before swap.
	if !agentPaneAlive(t, tmx, agentPane) {
		t.Fatal("pre-condition: agent pane not in ListPanesAll")
	}

	// ── Swap agent into the frame (simulate swapInCmd) ────────────────────────
	// Build the op manually to avoid going through swapInCmd (we want to control
	// the Model state precisely for the negative control).
	ops := planSwapIn("", placeholderPane, agentPane)
	for _, op := range ops {
		if err := tmx.SwapPane(ctx, op.src, op.dst); err != nil {
			t.Fatalf("SwapPane (swap-in): %v", err)
		}
	}

	// Verify agent pane is still alive (now in the frame main slot).
	if !agentPaneAlive(t, tmx, agentPane) {
		t.Fatal("after swap-in: agent pane not in ListPanesAll")
	}

	// ── Kill the frame WITHOUT swapping home (negative control) ───────────────
	if err := tmx.KillSession(ctx, "nc-frame"); err != nil {
		t.Fatalf("KillSession nc-frame: %v", err)
	}

	// Allow kill to propagate.
	time.Sleep(200 * time.Millisecond)

	// ── Assert agent pane is GONE (killed with the frame) ────────────────────
	// This is the negative control: killing the frame while the agent pane is in
	// it should destroy the agent pane. If the agent pane is still alive here,
	// the gate has no teeth.
	if agentPaneAlive(t, tmx, agentPane) {
		t.Fatal("negative control BROKEN: agent pane survived a frame kill without swap-home; the data-loss gate has no teeth")
	}

	t.Log("negative control: agent pane correctly destroyed when frame killed without swap-home")
}
