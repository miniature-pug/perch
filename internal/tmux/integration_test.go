//go:build integration

package tmux

import (
	"context"
	"fmt"
	"os"
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
