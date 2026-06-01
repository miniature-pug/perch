//go:build integration

package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// newTUITestServer returns a Tmux wired to a private socket so tests never
// touch the user's default tmux server. This duplicates the unexported
// tmux.newTestServer harness because it lives in a different package.
func newTUITestServer(t *testing.T) tmux.Tmux {
	t.Helper()
	tmx := tmux.New()
	tmx.Socket = fmt.Sprintf("perch-tui-it-%d", os.Getpid())
	ctx := context.Background()
	t.Cleanup(func() {
		_ = tmx.KillServer(ctx)
	})
	return tmx
}

// TestIntegration_TUI_SentinelLaunch verifies that Launch runs a command in a
// real pane and that CapturePane reflects the output within a 2-second deadline.
// The arithmetic marker PERCH_$((6*7)) proves the shell actually executed the
// command (not just recorded keystrokes).
func TestIntegration_TUI_SentinelLaunch(t *testing.T) {
	tmx := newTUITestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	session := "tui-it-sentinel"
	window := "w1"

	paneID, err := tmx.Launch(ctx, session, window, dir, []string{"sh", "-c", "echo PERCH_$((6*7))"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if paneID == "" {
		t.Fatal("Launch returned empty pane ID")
	}

	target := tmux.WindowTarget(session, window)

	deadline := time.Now().Add(2 * time.Second)
	var lastOut string
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		out, err := tmx.CapturePane(ctx, target, 0)
		if err != nil {
			t.Fatalf("CapturePane: %v", err)
		}
		lastOut = out
		if strings.Contains(out, "PERCH_42") {
			return
		}
	}
	t.Fatalf("CapturePane never showed PERCH_42 within 2s; last output:\n%s", lastOut)
}

// TestIntegration_TUI_LiveIdleJoin verifies the D6 live/idle join end-to-end
// against a real tmux server: SetPaneOption stamps a pane with a fixture
// session ID, ListPanesAll returns it, buildLiveIndex indexes it, and
// buildItemFromSession produces a live item with the correct captureTarget and
// non-empty liveTarget.
func TestIntegration_TUI_LiveIdleJoin(t *testing.T) {
	tmx := newTUITestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	// Bootstrap a pane for the fixture session.
	session := "tui-it-join"
	window := "w1"
	_, err := tmx.Connect(ctx, session, window, dir)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	target := tmux.WindowTarget(session, window)
	fixtureID := "sess_it_fixture"

	if err := tmx.SetPaneOption(ctx, target, "@perch_session", fixtureID); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	panes, err := tmx.ListPanesAll(ctx)
	if err != nil {
		t.Fatalf("ListPanesAll: %v", err)
	}

	// Assert a returned pane carries the fixture session ID.
	found := false
	for _, p := range panes {
		if p.PerchSession == fixtureID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListPanesAll: no pane with @perch_session=%q; panes: %+v", fixtureID, panes)
	}

	// Build and inspect the live index.
	idx := buildLiveIndex(panes)
	livePane, ok := idx[fixtureID]
	if !ok {
		t.Fatalf("buildLiveIndex: fixture session %q not in index", fixtureID)
	}

	// Construct a model.Session matching the fixture and run the join.
	s := model.Session{
		ID:        fixtureID,
		Tool:      model.ToolClaude,
		Directory: dir,
		Title:     "IT fixture session",
		Updated:   time.Now().Unix(),
	}
	it := buildItemFromSession(s, "it-project", "main", dir, dir, time.Now().Unix(), idx, false).(item)

	if !it.live {
		t.Error("want live=true for fixture session with matching pane, got false")
	}
	if it.captureTarget != livePane.ID {
		t.Errorf("captureTarget = %q, want %q", it.captureTarget, livePane.ID)
	}
	if it.liveTarget == "" {
		t.Error("want non-empty liveTarget for live item, got empty")
	}
	wantLiveTarget := tmux.WindowTarget(livePane.Session, livePane.Window)
	if it.liveTarget != wantLiveTarget {
		t.Errorf("liveTarget = %q, want %q", it.liveTarget, wantLiveTarget)
	}
}
