//go:build integration

package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
)

// newTestServer returns a Tmux wired to a private socket so tests never touch
// the user's default tmux server. The server is killed and its socket file is
// removed on test cleanup (best-effort for the socket; kill-server leaves it).
func newTestServer(t *testing.T) Tmux {
	t.Helper()
	socket := fmt.Sprintf("perch-test-%d", os.Getpid())
	tmx := Tmux{
		Runner: proc.ExecRunner{},
		Bin:    "tmux",
		Socket: socket,
	}
	t.Cleanup(func() {
		_ = tmx.KillServer(context.Background())
		// kill-server leaves the socket file behind; remove it best-effort.
		// Respect $TMUX_TMPDIR when set, matching tmux's own socket-dir logic.
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
		}
		socketPath := filepath.Join(dir, socket)
		_ = os.Remove(socketPath)
	})
	return tmx
}

// TestIntegration_Connect_OptionRoundTrip verifies that Connect creates a pane,
// that SetPaneOption/GetPaneOption round-trip correctly, and that ListPanesAll
// returns the pane with the expected @perch_session value.
func TestIntegration_Connect_OptionRoundTrip(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	paneID, err := tmx.Connect(ctx, "perchitest", "win1", dir)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if paneID == "" {
		t.Fatal("Connect returned empty pane ID")
	}
	if paneID[0] != '%' {
		t.Errorf("pane ID should start with '%%', got %q", paneID)
	}

	target := WindowTarget("perchitest", "win1")
	if err := tmx.SetPaneOption(ctx, target, "@perch_session", "ses_xyz"); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	got, err := tmx.GetPaneOption(ctx, target, "@perch_session")
	if err != nil {
		t.Fatalf("GetPaneOption: %v", err)
	}
	if got != "ses_xyz" {
		t.Errorf("GetPaneOption = %q, want ses_xyz", got)
	}

	panes, err := tmx.ListPanesAll(ctx)
	if err != nil {
		t.Fatalf("ListPanesAll: %v", err)
	}

	found := false
	for _, p := range panes {
		if p.PerchSession == "ses_xyz" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListPanesAll: no pane with @perch_session=ses_xyz; panes: %+v", panes)
	}
}

// TestIntegration_NewWindow_SecondAgent verifies that two windows in the same
// session get distinct pane IDs and that exact window targeting works correctly
// when reading back distinct @perch_session values.
func TestIntegration_NewWindow_SecondAgent(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()

	pane1, err := tmx.NewSession(ctx, "p2", "w1", t.TempDir())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if pane1 == "" {
		t.Fatal("NewSession returned empty pane ID")
	}

	pane2, err := tmx.NewWindow(ctx, "p2", "w2", t.TempDir())
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if pane2 == "" {
		t.Fatal("NewWindow returned empty pane ID")
	}

	if pane1 == pane2 {
		t.Errorf("NewSession and NewWindow returned the same pane ID %q — must be distinct", pane1)
	}

	target1 := WindowTarget("p2", "w1")
	target2 := WindowTarget("p2", "w2")

	if err := tmx.SetPaneOption(ctx, target1, "@perch_session", "agent-w1"); err != nil {
		t.Fatalf("SetPaneOption w1: %v", err)
	}
	if err := tmx.SetPaneOption(ctx, target2, "@perch_session", "agent-w2"); err != nil {
		t.Fatalf("SetPaneOption w2: %v", err)
	}

	got1, err := tmx.GetPaneOption(ctx, target1, "@perch_session")
	if err != nil {
		t.Fatalf("GetPaneOption w1: %v", err)
	}
	got2, err := tmx.GetPaneOption(ctx, target2, "@perch_session")
	if err != nil {
		t.Fatalf("GetPaneOption w2: %v", err)
	}

	if got1 != "agent-w1" {
		t.Errorf("w1 @perch_session = %q, want agent-w1", got1)
	}
	if got2 != "agent-w2" {
		t.Errorf("w2 @perch_session = %q, want agent-w2", got2)
	}
}

// TestIntegration_BootID_Live verifies that BootID returns a non-empty string
// that parses as an integer (tmux #{start_time} is a unix timestamp).
func TestIntegration_BootID_Live(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()

	// Bootstrap a server first — BootID requires a live server.
	if _, err := tmx.NewSession(ctx, "boottest", "w", t.TempDir()); err != nil {
		t.Fatalf("NewSession (server bootstrap): %v", err)
	}

	id, err := tmx.BootID(ctx)
	if err != nil {
		t.Fatalf("BootID: %v", err)
	}
	if id == "" {
		t.Fatal("BootID returned empty string")
	}

	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		t.Errorf("BootID %q does not parse as int64: %v", id, err)
	}
}

// TestIntegration_ShadowRecord_RoundTrip verifies that SaveWindow followed by
// LoadWindows returns exactly one record with the expected field values.
// This test exercises state directly and requires no tmux server.
func TestIntegration_ShadowRecord_RoundTrip(t *testing.T) {
	baseDir := t.TempDir()

	w := model.Window{
		PaneKey:     "%42",
		Tool:        model.ToolClaude,
		SessionID:   "ses-roundtrip",
		Tree:        "/tmp/testproject",
		TmuxSession: "testproject",
		TmuxWindow:  "main",
		BootID:      "1717000000",
		Updated:     time.Now().Unix(),
	}

	if err := state.SaveWindow(baseDir, w); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}

	if len(windows) != 1 {
		t.Fatalf("LoadWindows returned %d records, want 1", len(windows))
	}

	got := windows[0]
	if got.PaneKey != w.PaneKey {
		t.Errorf("PaneKey = %q, want %q", got.PaneKey, w.PaneKey)
	}
	if got.Tool != w.Tool {
		t.Errorf("Tool = %q, want %q", got.Tool, w.Tool)
	}
	if got.SessionID != w.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, w.SessionID)
	}
	if got.Tree != w.Tree {
		t.Errorf("Tree = %q, want %q", got.Tree, w.Tree)
	}
	if got.TmuxSession != w.TmuxSession {
		t.Errorf("TmuxSession = %q, want %q", got.TmuxSession, w.TmuxSession)
	}
	if got.TmuxWindow != w.TmuxWindow {
		t.Errorf("TmuxWindow = %q, want %q", got.TmuxWindow, w.TmuxWindow)
	}
	if got.BootID != w.BootID {
		t.Errorf("BootID = %q, want %q", got.BootID, w.BootID)
	}
	if got.Updated != w.Updated {
		t.Errorf("Updated = %d, want %d", got.Updated, w.Updated)
	}
}

// TestIntegration_PerchPaneStatusRoundTrip verifies that @perch_pane_status
// round-trips through a real tmux server: the option is empty on a fresh pane
// and is readable through ListPanesAll after SetPaneOption writes it.
func TestIntegration_PerchPaneStatusRoundTrip(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()

	paneID, err := tmx.NewSession(ctx, "perchs", "w1", t.TempDir())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if paneID == "" {
		t.Fatal("NewSession returned empty pane ID")
	}

	// Before: @perch_pane_status must be empty on a fresh pane.
	panesBefore, err := tmx.ListPanesAll(ctx)
	if err != nil {
		t.Fatalf("ListPanesAll (before): %v", err)
	}
	foundBefore := false
	for _, p := range panesBefore {
		if p.ID == paneID {
			foundBefore = true
			if p.PerchStatus != "" {
				t.Errorf("before SetPaneOption: PerchStatus = %q, want \"\"", p.PerchStatus)
			}
			break
		}
	}
	if !foundBefore {
		t.Fatalf("ListPanesAll (before): pane %q not found; panes: %+v", paneID, panesBefore)
	}

	// Set the option via the production path.
	if err := tmx.SetPaneOption(ctx, paneID, "@perch_pane_status", "working"); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	// After: @perch_pane_status must equal "working".
	panesAfter, err := tmx.ListPanesAll(ctx)
	if err != nil {
		t.Fatalf("ListPanesAll (after): %v", err)
	}
	foundAfter := false
	for _, p := range panesAfter {
		if p.ID == paneID {
			foundAfter = true
			if p.PerchStatus != "working" {
				t.Errorf("after SetPaneOption: PerchStatus = %q, want \"working\"", p.PerchStatus)
			}
			break
		}
	}
	if !foundAfter {
		t.Fatalf("ListPanesAll (after): pane %q not found; panes: %+v", paneID, panesAfter)
	}
}

// TestIntegration_SendKeys_RunsCommand verifies that SendKeys fires both the
// literal keystroke and the trailing Enter, and that CapturePane reflects the
// resulting shell output. The arithmetic marker PERCH_$((6*7)) ensures the
// shell actually executed the command (output is PERCH_42) rather than just
// recording the keystrokes.
func TestIntegration_SendKeys_RunsCommand(t *testing.T) {
	tm := newTestServer(t)
	ctx := context.Background()

	if _, err := tm.Connect(ctx, "sktest", "skw1", t.TempDir()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	tgt := WindowTarget("sktest", "skw1")

	if err := tm.SendKeys(ctx, tgt, "echo PERCH_$((6*7))"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	var lastOut string
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		out, err := tm.CapturePane(ctx, tgt, 0)
		if err != nil {
			t.Fatalf("CapturePane: %v", err)
		}
		lastOut = out
		if strings.Contains(out, "PERCH_42") {
			return
		}
	}
	t.Fatalf("CapturePane never showed PERCH_42 after 2s; last output:\n%s", lastOut)
}

// ── frame/swap integration tests ──────────────────────────────────────────────

// lookupPath returns the absolute path of a binary on PATH, or "" if missing.
func lookupPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

// waitForContent polls CapturePane up to maxTries*delay for any of the wanted
// substrings to appear in the output. Returns the last captured output.
func waitForContent(t *testing.T, tm Tmux, target string, wanted []string, maxTries int, delay time.Duration) string {
	t.Helper()
	var last string
	for i := 0; i < maxTries; i++ {
		time.Sleep(delay)
		out, err := tm.CapturePane(context.Background(), target, 0)
		if err != nil {
			// May fail transiently while pane is being set up; keep trying.
			continue
		}
		last = out
		for _, w := range wanted {
			if strings.Contains(out, w) {
				return out
			}
		}
	}
	return last
}

// TestIntegration_SwapPane_BothSessionsAlive verifies that SwapPane across two
// sessions keeps both sessions alive and both original pids still alive after
// the swap. Mirrors spike Q3.
func TestIntegration_SwapPane_BothSessionsAlive(t *testing.T) {
	tm := newTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	// Create two detached sessions, each running sleep.
	paneA, err := tm.NewSession(ctx, "swapA", "main", dir)
	if err != nil {
		t.Fatalf("NewSession swapA: %v", err)
	}
	paneB, err := tm.NewSession(ctx, "swapB", "main", dir)
	if err != nil {
		t.Fatalf("NewSession swapB: %v", err)
	}

	if err := tm.SendKeys(ctx, paneA, "sleep 100000"); err != nil {
		t.Fatalf("SendKeys paneA: %v", err)
	}
	if err := tm.SendKeys(ctx, paneB, "sleep 100000"); err != nil {
		t.Fatalf("SendKeys paneB: %v", err)
	}

	// Allow the sleeps to start.
	time.Sleep(200 * time.Millisecond)

	// Record the pane_pid for each pane before the swap.
	panesAll, err := tm.ListPanesAll(ctx)
	if err != nil {
		t.Fatalf("ListPanesAll (pre-swap): %v", err)
	}
	pidFor := make(map[string]string) // pane ID → pid
	for _, p := range panesAll {
		pidFor[p.ID] = p.PID
	}
	pidA, okA := pidFor[paneA]
	pidB, okB := pidFor[paneB]
	if !okA || !okB {
		t.Fatalf("pre-swap pids not found: paneA=%q pid=%q, paneB=%q pid=%q; all: %+v",
			paneA, pidA, paneB, pidB, panesAll)
	}

	// Perform the swap.
	if err := tm.SwapPane(ctx, paneA, paneB); err != nil {
		t.Fatalf("SwapPane: %v", err)
	}

	// Both sessions must still exist.
	hasA, err := tm.HasSession(ctx, "swapA")
	if err != nil {
		t.Fatalf("HasSession swapA: %v", err)
	}
	hasB, err := tm.HasSession(ctx, "swapB")
	if err != nil {
		t.Fatalf("HasSession swapB: %v", err)
	}
	if !hasA {
		t.Error("session swapA is gone after swap, want alive")
	}
	if !hasB {
		t.Error("session swapB is gone after swap, want alive")
	}

	// Re-list all panes. After swap, pane IDs are stable but they live in the
	// other session. Assert both original pids are still alive anywhere.
	panesPost, err := tm.ListPanesAll(ctx)
	if err != nil {
		t.Fatalf("ListPanesAll (post-swap): %v", err)
	}
	postPids := make(map[string]bool)
	for _, p := range panesPost {
		postPids[p.PID] = true
	}
	if !postPids[pidA] {
		t.Errorf("pid of paneA (%s) no longer alive post-swap; post panes: %+v", pidA, panesPost)
	}
	if !postPids[pidB] {
		t.Errorf("pid of paneB (%s) no longer alive post-swap; post panes: %+v", pidB, panesPost)
	}
}

// TestIntegration_ReflowGate verifies that pre-resizing a parked agent session
// to frame dimensions and then swapping it into a wide frame pane causes the
// app to reflow: at least one captured line after the swap is wider than the
// original 80-col session width.
//
// It requires vim or vi to be on PATH; if neither is available the test is
// skipped (t.Skip). The test uses a deterministic wide content file so that
// at ≥120 cols the file renders as one long line (>80 cols) rather than
// relying on the app's own UI chrome which varies.
func TestIntegration_ReflowGate(t *testing.T) {
	// Prefer vim for display stability; fall back to vi.
	editorPath := lookupPath("vim")
	if editorPath == "" {
		editorPath = lookupPath("vi")
	}
	if editorPath == "" {
		t.Skip("neither vim nor vi found on PATH; skipping reflow gate")
	}

	tm := newTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	// Write a file with one line that is ~100 chars wide. At 80 cols vim wraps
	// it to ≤80-char screen rows; at ≥120 cols it renders as one ~100-char row.
	// The content is deterministic: 100 '-' characters plus a unique marker.
	wideLine := strings.Repeat("-", 98) + "END\n"
	contentFile := filepath.Join(dir, "wide.txt")
	if err := os.WriteFile(contentFile, []byte(wideLine), 0o600); err != nil {
		t.Fatalf("write content file: %v", err)
	}

	// Create the "agent" session at 80×24 (tmux default) and open the editor.
	agentPane, err := tm.NewSession(ctx, "reflowagent", "main", dir)
	if err != nil {
		t.Fatalf("NewSession reflowagent: %v", err)
	}
	if err := tm.ResizeWindow(ctx, SessionTarget("reflowagent"), 80, 24); err != nil {
		// resize-window may need window-size manual on clientless server (tmux 3.6).
		// Set the option and retry.
		_ = tm.SetPaneOption(ctx, agentPane, "window-size", "manual")
		if err2 := tm.ResizeWindow(ctx, SessionTarget("reflowagent"), 80, 24); err2 != nil {
			t.Fatalf("ResizeWindow 80x24: %v (original: %v)", err2, err)
		}
	}

	// Launch editor in the agent pane. Use -u NONE to suppress vi's startup
	// message ("Press ENTER or type command to continue") which would otherwise
	// block rendering the file content.
	editorCmd := editorPath + " -u NONE " + contentFile
	if err := tm.SendKeys(ctx, agentPane, editorCmd); err != nil {
		t.Fatalf("SendKeys editor: %v", err)
	}
	// Wait for the file content to appear. Accept either the wide content line
	// or the "ENTER" startup prompt so we can dismiss it.
	paintOut := waitForContent(t, tm, agentPane, []string{"END", "ENTER", strings.Repeat("-", 10)}, 40, 100*time.Millisecond)
	// If vi still shows its startup prompt, dismiss it by sending Enter.
	if strings.Contains(paintOut, "ENTER") || strings.Contains(paintOut, "Press") {
		_, _, _ = tm.runner().Run(ctx, tm.bin(), tm.args("send-keys", "-t", agentPane, "Enter")...)
		time.Sleep(200 * time.Millisecond)
	}

	// Create the "frame" session/pane at 200×50 — wide enough to confirm reflow.
	framePane, err := tm.NewSession(ctx, "reflowframe", "frame", dir)
	if err != nil {
		t.Fatalf("NewSession reflowframe: %v", err)
	}
	if err := tm.ResizeWindow(ctx, SessionTarget("reflowframe"), 200, 50); err != nil {
		_ = tm.SetPaneOption(ctx, framePane, "window-size", "manual")
		if err2 := tm.ResizeWindow(ctx, SessionTarget("reflowframe"), 200, 50); err2 != nil {
			t.Fatalf("ResizeWindow 200x50: %v (original: %v)", err2, err)
		}
	}

	// Read back the actual frame width (may differ from what we requested on a
	// headless server) and use it as the reflow target.
	frameW, frameH, err := tm.PaneSize(ctx, framePane)
	if err != nil {
		t.Fatalf("PaneSize (frame): %v", err)
	}
	if frameW < 120 {
		t.Skipf("frame pane width %d < 120 after resize (headless server constraint); skipping reflow gate", frameW)
	}
	t.Logf("frame pane: %d×%d", frameW, frameH)

	// Pre-size the agent session to the frame dimensions before swapping.
	if err := tm.ResizeWindow(ctx, SessionTarget("reflowagent"), frameW, frameH); err != nil {
		_ = tm.SetPaneOption(ctx, agentPane, "window-size", "manual")
		if err2 := tm.ResizeWindow(ctx, SessionTarget("reflowagent"), frameW, frameH); err2 != nil {
			t.Fatalf("ResizeWindow agent to frame dims: %v", err2)
		}
	}

	// Swap the agent pane into the frame pane slot.
	if err := tm.SwapPane(ctx, agentPane, framePane); err != nil {
		t.Fatalf("SwapPane: %v", err)
	}

	// Nudge clients to repaint (best-effort on a headless server).
	_ = tm.RefreshClient(ctx)

	// Wait for the editor to repaint at the new width.
	time.Sleep(400 * time.Millisecond)

	// Capture the frame pane — agent pane is now in the frame slot; after the
	// swap the original framePane ID lives in the agent session but agentPane ID
	// is now in the frame. Capture agentPane (it is the content pane wherever it
	// is).
	var captured string
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		out, err := tm.CapturePane(ctx, agentPane, 0)
		if err != nil {
			continue
		}
		captured = out
		// Check if any line is wider than 80 chars.
		for _, line := range strings.Split(out, "\n") {
			if len(line) > 80 {
				t.Logf("reflow confirmed: found line of len %d (>80) after swap to %d-wide frame", len(line), frameW)
				return
			}
		}
	}

	// Show details for diagnosis if the test fails.
	t.Logf("frame width: %d", frameW)
	t.Logf("editor: %s", editorPath)
	t.Logf("captured pane output:\n%s", captured)
	t.Errorf("reflow gate: no captured line wider than 80 cols after swap to %d-wide frame", frameW)
}
