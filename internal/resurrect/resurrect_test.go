package resurrect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// paneFormat is the -F format string used by ListPanesAll (must match tmux.go).
const paneFormat = "#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}"

// paneLine builds a single list-panes output line in paneFormat.
func paneLine(id, sess, win string, dead bool) string {
	deadField := "0"
	if dead {
		deadField = "1"
	}
	return id + "\x1f1234\x1fbash\x1f" + deadField + "\x1f/work\x1f" + sess + "\x1f" + win + "\x1f\n"
}

// worktreePorcelain builds a minimal `git worktree list --porcelain` output
// containing one main worktree and one linked worktree.
func worktreePorcelain(mainPath, linkedPath string) []byte {
	return []byte("worktree " + mainPath + "\nHEAD abc123\nbranch refs/heads/main\n\nworktree " + linkedPath + "\nHEAD def456\nbranch refs/heads/feat\n\n")
}

// worktreePorcelainMainOnly builds porcelain output with only the main worktree.
func worktreePorcelainMainOnly(mainPath string) []byte {
	return []byte("worktree " + mainPath + "\nHEAD abc123\nbranch refs/heads/main\n\n")
}

// newDeps builds a Deps with a FakeRunner-backed tmux.Tmux. The same *FakeRunner
// backs both deps.Tmux.Runner and deps.Runner per L1 (tmux vs git argv never
// collide in the key space).
func newDeps(t *testing.T, baseDir string, fake *proc.FakeRunner) Deps {
	t.Helper()
	return Deps{
		Tmux: tmux.Tmux{
			Runner: fake,
			Bin:    "tmux",
			Getenv: func(string) string { return "" },
		},
		Runner:  fake,
		BaseDir: baseDir,
		Now:     1000,
	}
}

// seedWindow writes a shadow record using state.SaveWindow.
func seedWindow(t *testing.T, baseDir string, w model.Window) {
	t.Helper()
	if err := state.SaveWindow(baseDir, w); err != nil {
		t.Fatalf("seedWindow: %v", err)
	}
}

// countLaunches returns how many new-session or new-window calls appear in
// fake.Calls. These represent actual window-creation events from Launch.
func countLaunches(fake *proc.FakeRunner) int {
	n := 0
	for _, c := range fake.Calls {
		if len(c.Args) > 0 && (c.Args[0] == "new-session" || c.Args[0] == "new-window") {
			n++
		}
	}
	return n
}

// ── KEEP ──────────────────────────────────────────────────────────────────────

// TestReconcile_Keep verifies that a record whose pane is live and boot matches
// produces a KEEP entry with no I/O mutations.
func TestReconcile_Keep(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	const (
		boot    = "12345"
		paneKey = "%10"
		sess    = "proj"
		win     = "feat"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "sess-abc",
		Tree:        baseDir,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      boot,
	})

	// BootID → current boot matches record.
	fake.Respond(proc.FakeResult{Stdout: []byte(boot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	// ListPanesAll → the pane is live.
	fake.Respond(proc.FakeResult{Stdout: []byte(paneLine(paneKey, sess, win, false))},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Kept) != 1 || report.Kept[0] != paneKey {
		t.Errorf("Kept = %v, want [%s]", report.Kept, paneKey)
	}
	if len(report.Pruned)+len(report.Restored)+len(report.Skipped) != 0 {
		t.Errorf("unexpected mutations: pruned=%v restored=%v skipped=%v",
			report.Pruned, report.Restored, report.Skipped)
	}
	if countLaunches(fake) != 0 {
		t.Errorf("expected zero launches for KEEP")
	}
	// Record must still be present.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 {
		t.Errorf("record count = %d, want 1 (KEEP must not remove the record)", len(wins))
	}
}

// ── PRUNE ─────────────────────────────────────────────────────────────────────

// TestReconcile_Prune verifies that a record whose pane is absent but boot
// matches produces a PRUNE entry and the record file is removed.
func TestReconcile_Prune(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	const (
		boot    = "12345"
		paneKey = "%20"
		sess    = "proj"
		win     = "feat"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "sess-def",
		Tree:        baseDir,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      boot,
	})

	// Current boot matches the record's boot.
	fake.Respond(proc.FakeResult{Stdout: []byte(boot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	// ListPanesAll → empty (pane is gone).
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Pruned) != 1 || report.Pruned[0] != paneKey {
		t.Errorf("Pruned = %v, want [%s]", report.Pruned, paneKey)
	}
	if len(report.Kept)+len(report.Restored)+len(report.Skipped) != 0 {
		t.Errorf("unexpected mutations: kept=%v restored=%v skipped=%v",
			report.Kept, report.Restored, report.Skipped)
	}
	if countLaunches(fake) != 0 {
		t.Errorf("expected zero launches for PRUNE")
	}
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 0 {
		t.Errorf("record count = %d, want 0 after PRUNE", len(wins))
	}
}

// ── RESTORE happy path ────────────────────────────────────────────────────────

// TestReconcile_RestoreHappy verifies the full restore path: boot mismatch →
// launch + set-option + old record removed + new record written with new pane
// id and new boot.
func TestReconcile_RestoreHappy(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	// Use t.TempDir() as the tree so os.Stat succeeds.
	tree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "12345"
		oldPane = "%30"
		newPane = "%99"
		sess    = "myproj"
		win     = "feat"
		sid     = "claude-session-1"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     oldPane,
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        tree,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      oldBoot,
	})

	// BootID → current boot (different from record's oldBoot → restore branch).
	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	// ListPanesAll → no live panes (server restarted, all panes gone).
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	// git worktree list → main + linked worktree at tree.
	mainTree := t.TempDir()
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, tree)},
		"git", "-C", tree, "worktree", "list", "--porcelain")

	// Launch → Connect → has-session (absent) → new-session returns newPane.
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+sess)
	fake.Respond(proc.FakeResult{Stdout: []byte(newPane + "\n")},
		"tmux", "new-session", "-d", "-s", sess, "-n", win, "-c", tree, "-P", "-F", "#{pane_id}")
	// send-keys: agent name + ResumeArgs("claude-session-1") = ["claude", "--resume", "claude-session-1"]
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "-l", "'claude' '--resume' '"+sid+"'")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "Enter")
	// SetPaneOption @perch_session.
	fake.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", newPane, "@perch_session", sid)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Restored must include the old pane key.
	if len(report.Restored) != 1 || report.Restored[0] != oldPane {
		t.Errorf("Restored = %v, want [%s]", report.Restored, oldPane)
	}
	if len(report.Kept)+len(report.Pruned)+len(report.Skipped) != 0 {
		t.Errorf("unexpected entries: kept=%v pruned=%v skipped=%v",
			report.Kept, report.Pruned, report.Skipped)
	}

	// Exactly one Launch must have been issued.
	if countLaunches(fake) != 1 {
		t.Errorf("launch count = %d, want 1", countLaunches(fake))
	}

	// set-option @perch_session must have been called.
	foundSetOpt := false
	for _, c := range fake.Calls {
		if len(c.Args) >= 5 && c.Args[0] == "set-option" && c.Args[4] == "@perch_session" {
			foundSetOpt = true
		}
	}
	if !foundSetOpt {
		t.Errorf("set-option @perch_session not called; calls: %v", fake.Calls)
	}

	// Old record removed, new record present with new pane id and new boot.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 {
		t.Fatalf("record count = %d, want 1 after restore", len(wins))
	}
	w := wins[0]
	if w.PaneKey != newPane {
		t.Errorf("new record PaneKey = %q, want %s", w.PaneKey, newPane)
	}
	if w.BootID != newBoot {
		t.Errorf("new record BootID = %q, want %s", w.BootID, newBoot)
	}
	if w.SessionID != sid {
		t.Errorf("new record SessionID = %q, want %s", w.SessionID, sid)
	}
}

// ── cold server ───────────────────────────────────────────────────────────────

// TestReconcile_ColdServer verifies the cold-server path: BootID errors means
// currentBoot=="" so every record enters the RESTORE branch and no prune happens.
// When new-session succeeds (tmux spawns the server on first new-session), the
// restore completes and the rewritten record has BootID=="" (the L3 fallback
// path: both the snapshot and post-launch BootID reads fail, so restoredBoot
// falls back to currentBoot which is "").
func TestReconcile_ColdServer(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	tree := t.TempDir()
	mainTree := t.TempDir()

	const (
		oldBoot = "someboot"
		paneKey = "%40"
		newPane = "%41"
		sess    = "proj"
		win     = "feat"
		sid     = "claude-session-cold"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        tree,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      oldBoot,
	})

	// Both BootID reads (snapshot + L3 post-launch) share the same key and both
	// return error → currentBoot="" (cold signal) and restoredBoot falls back to "".
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "display-message", "-p", "#{start_time}")
	// ListPanesAll also errors (server not yet running) → livePanes = nil.
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	// git worktree list → main + linked worktree at tree.
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, tree)},
		"git", "-C", tree, "worktree", "list", "--porcelain")

	// Launch → has-session exits 1 (no session) → new-session SUCCEEDS (tmux
	// spawns the server on the first new-session call — this is the real mechanism).
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+sess)
	fake.Respond(proc.FakeResult{Stdout: []byte(newPane + "\n")},
		"tmux", "new-session", "-d", "-s", sess, "-n", win, "-c", tree, "-P", "-F", "#{pane_id}")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "-l", "'claude' '--resume' '"+sid+"'")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "Enter")
	fake.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", newPane, "@perch_session", sid)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No prune must occur (prune requires bootMatch, which requires currentBoot != "").
	if len(report.Pruned) != 0 {
		t.Errorf("Pruned = %v, want empty (cold server must not prune)", report.Pruned)
	}
	// Record must be restored.
	if len(report.Restored) != 1 {
		t.Errorf("Restored = %v, want 1 entry", report.Restored)
	}

	// The rewritten record must exist with the new pane id and BootID=="" (L3
	// fallback: both BootID reads return error so restoredBoot falls back to
	// currentBoot which is "").
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 {
		t.Fatalf("record count = %d, want 1 after cold-server restore", len(wins))
	}
	if wins[0].PaneKey != newPane {
		t.Errorf("restored record PaneKey = %q, want %s", wins[0].PaneKey, newPane)
	}
	if wins[0].BootID != "" {
		t.Errorf("restored record BootID = %q, want \"\" (L3 fallback)", wins[0].BootID)
	}
}

// ── skip: empty-sid ───────────────────────────────────────────────────────────

// TestReconcile_SkipEmptySID verifies that a record with an empty SessionID
// gets a definitive skip (empty-sid) and the record is deleted.
func TestReconcile_SkipEmptySID(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	const (
		oldBoot = "oldboot"
		newBoot = "99999"
		paneKey = "%50"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolOpencode,
		SessionID:   "", // empty → definitive skip
		Tree:        baseDir,
		TmuxSession: "proj",
		TmuxWindow:  "main",
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "empty-sid" {
		t.Errorf("Skipped = %v, want [{empty-sid}]", report.Skipped)
	}
	// Definitive skip: record must be deleted.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 0 {
		t.Errorf("record count = %d, want 0 (definitive skip deletes record)", len(wins))
	}
}

// ── skip: window-live ─────────────────────────────────────────────────────────

// TestReconcile_SkipWindowLive verifies the FD4 guard: a non-dead pane already
// in the target session/window → skip:window-live + record deleted.
func TestReconcile_SkipWindowLive(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	const (
		oldBoot = "oldboot"
		newBoot = "88888"
		paneKey = "%60"
		sess    = "proj"
		win     = "feat"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "some-session",
		Tree:        baseDir,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	// Live pane exists in the same session/window (FD4 guard fires).
	fake.Respond(proc.FakeResult{Stdout: []byte(paneLine("%61", sess, win, false))},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "window-live" {
		t.Errorf("Skipped = %v, want [{window-live}]", report.Skipped)
	}
	// Definitive skip: record deleted.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 0 {
		t.Errorf("record count = %d, want 0 (definitive skip deletes record)", len(wins))
	}
}

// ── skip: tree-gone ───────────────────────────────────────────────────────────

// TestReconcile_SkipTreeGone verifies that a record whose Tree path no longer
// exists gets a definitive skip (tree-gone) and the record is deleted.
func TestReconcile_SkipTreeGone(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	const (
		oldBoot = "oldboot"
		newBoot = "77777"
		paneKey = "%70"
	)

	nonExistentTree := filepath.Join(t.TempDir(), "deleted-worktree")

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "some-session",
		Tree:        nonExistentTree,
		TmuxSession: "proj",
		TmuxWindow:  "feat",
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "tree-gone" {
		t.Errorf("Skipped = %v, want [{tree-gone}]", report.Skipped)
	}
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 0 {
		t.Errorf("record count = %d, want 0 (definitive skip deletes record)", len(wins))
	}
}

// ── skip: main ────────────────────────────────────────────────────────────────

// TestReconcile_SkipMain verifies that when the tree belongs to the main
// worktree, the record gets a definitive skip (main) and is deleted.
func TestReconcile_SkipMain(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	mainTree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "66666"
		paneKey = "%80"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "some-session",
		Tree:        mainTree, // tree IS the main worktree
		TmuxSession: "proj",
		TmuxWindow:  "main",
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	// git worktree list → only the main worktree (tree == main checkout).
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelainMainOnly(mainTree)},
		"git", "-C", mainTree, "worktree", "list", "--porcelain")

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "main" {
		t.Errorf("Skipped = %v, want [{main}]", report.Skipped)
	}
	// Definitive skip: record deleted.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 0 {
		t.Errorf("record count = %d, want 0 (definitive skip deletes record)", len(wins))
	}
}

// ── skip: git-error (transient) ───────────────────────────────────────────────

// TestReconcile_SkipGitError verifies that a git subprocess error is treated as
// a transient skip (git-error), keeping the record for the next run.
func TestReconcile_SkipGitError(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	tree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "55555"
		paneKey = "%90"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "some-session",
		Tree:        tree,
		TmuxSession: "proj",
		TmuxWindow:  "feat",
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	// git errors → transient skip.
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 128}},
		"git", "-C", tree, "worktree", "list", "--porcelain")

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "git-error" {
		t.Errorf("Skipped = %v, want [{git-error}]", report.Skipped)
	}
	// Transient: record must be kept.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 {
		t.Errorf("record count = %d, want 1 (transient skip keeps record)", len(wins))
	}
}

// ── double-run idempotency ────────────────────────────────────────────────────

// TestReconcile_DoubleRunIdempotent verifies that after a successful restore,
// a second Reconcile call over the rewritten records issues zero launch calls.
// Two separate FakeRunner instances are used: run 1 sees an empty pane list
// (server restarted, no panes) and run 2 sees the restored pane as live so
// the second pass classifies it KEEP.
func TestReconcile_DoubleRunIdempotent(t *testing.T) {
	baseDir := t.TempDir()

	tree := t.TempDir()
	mainTree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "12345"
		oldPane = "%100"
		newPane = "%101"
		sess    = "proj"
		win     = "feat"
		sid     = "session-idem"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     oldPane,
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        tree,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      oldBoot,
	})

	// ── run 1: server restarted, pane list is empty ──

	fake1 := proc.NewFakeRunner()
	fake1.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	// Empty pane list: old pane is gone, no window-live conflict.
	fake1.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	fake1.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, tree)},
		"git", "-C", tree, "worktree", "list", "--porcelain")
	fake1.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+sess)
	fake1.Respond(proc.FakeResult{Stdout: []byte(newPane + "\n")},
		"tmux", "new-session", "-d", "-s", sess, "-n", win, "-c", tree, "-P", "-F", "#{pane_id}")
	fake1.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "-l", "'claude' '--resume' '"+sid+"'")
	fake1.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "Enter")
	fake1.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", newPane, "@perch_session", sid)

	deps1 := newDeps(t, baseDir, fake1)
	report1, err := Reconcile(context.Background(), deps1)
	if err != nil {
		t.Fatalf("run 1 error: %v", err)
	}
	if len(report1.Restored) != 1 {
		t.Errorf("run 1 Restored = %v, want 1 entry", report1.Restored)
	}
	launches1 := countLaunches(fake1)
	if launches1 != 1 {
		t.Errorf("run 1 launches = %d, want 1", launches1)
	}

	// ── run 2: new pane is live, boot matches the saved record ──
	// The rewritten record has PaneKey=newPane, BootID=newBoot.
	// ListPanesAll returns newPane as non-dead → paneByID=true, bootMatch=true → KEEP.

	fake2 := proc.NewFakeRunner()
	fake2.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake2.Respond(proc.FakeResult{Stdout: []byte(paneLine(newPane, sess, win, false))},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	deps2 := newDeps(t, baseDir, fake2)
	report2, err := Reconcile(context.Background(), deps2)
	if err != nil {
		t.Fatalf("run 2 error: %v", err)
	}
	if len(report2.Restored)+len(report2.Pruned)+len(report2.Skipped) != 0 {
		t.Errorf("run 2 should be no-op: restored=%v pruned=%v skipped=%v",
			report2.Restored, report2.Pruned, report2.Skipped)
	}
	if len(report2.Kept) != 1 || report2.Kept[0] != newPane {
		t.Errorf("run 2 Kept = %v, want [%s]", report2.Kept, newPane)
	}
	if countLaunches(fake2) != 0 {
		t.Errorf("run 2 issued %d launches, want 0", countLaunches(fake2))
	}
}

// TestReconcile_DoubleRunIdempotent_SamePaneKey is a regression test for the
// case where the restarted tmux server assigns the same pane id as the old one
// (e.g. both are %0). Before the fix, RemoveWindow(oldKey) would delete the
// record just written by SaveWindow(newKey) when oldKey == newKey, making a
// second Reconcile see no records (idempotency broken). The fix skips the
// RemoveWindow call when paneID == w.PaneKey.
func TestReconcile_DoubleRunIdempotent_SamePaneKey(t *testing.T) {
	baseDir := t.TempDir()

	tree := t.TempDir()
	mainTree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "99999"
		paneKey = "%0" // same id on old and new server
		sess    = "proj"
		win     = "feat"
		sid     = "session-same-pane"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        tree,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      oldBoot,
	})

	// ── run 1: server restarted, new server reuses %0 ──

	fake1 := proc.NewFakeRunner()
	fake1.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake1.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	fake1.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, tree)},
		"git", "-C", tree, "worktree", "list", "--porcelain")
	fake1.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+sess)
	// New server reuses the same pane id.
	fake1.Respond(proc.FakeResult{Stdout: []byte(paneKey + "\n")},
		"tmux", "new-session", "-d", "-s", sess, "-n", win, "-c", tree, "-P", "-F", "#{pane_id}")
	fake1.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", paneKey, "-l", "'claude' '--resume' '"+sid+"'")
	fake1.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", paneKey, "Enter")
	fake1.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", paneKey, "@perch_session", sid)

	deps1 := newDeps(t, baseDir, fake1)
	report1, err := Reconcile(context.Background(), deps1)
	if err != nil {
		t.Fatalf("run 1 error: %v", err)
	}
	if len(report1.Restored) != 1 {
		t.Errorf("run 1 Restored = %v, want 1 entry", report1.Restored)
	}

	// The record must survive (SaveWindow wrote it; RemoveWindow must be skipped).
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows after run 1: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("LoadWindows after run 1: got %d records, want 1 (same-pane-key regression)", len(windows))
	}
	if windows[0].BootID != newBoot {
		t.Errorf("record BootID = %q, want %q", windows[0].BootID, newBoot)
	}

	// ── run 2: pane is live, boot matches ──

	fake2 := proc.NewFakeRunner()
	fake2.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake2.Respond(proc.FakeResult{Stdout: []byte(paneLine(paneKey, sess, win, false))},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	deps2 := newDeps(t, baseDir, fake2)
	report2, err := Reconcile(context.Background(), deps2)
	if err != nil {
		t.Fatalf("run 2 error: %v", err)
	}
	if len(report2.Restored)+len(report2.Pruned)+len(report2.Skipped) != 0 {
		t.Errorf("run 2 should be no-op: restored=%v pruned=%v skipped=%v",
			report2.Restored, report2.Pruned, report2.Skipped)
	}
	if len(report2.Kept) != 1 || report2.Kept[0] != paneKey {
		t.Errorf("run 2 Kept = %v, want [%s]", report2.Kept, paneKey)
	}
	if countLaunches(fake2) != 0 {
		t.Errorf("run 2 issued %d launches, want 0", countLaunches(fake2))
	}
}

// ── launch-failed (transient) ─────────────────────────────────────────────────

// TestReconcile_LaunchFailed verifies that when Launch fails the record is kept
// (transient), the reason is launch-failed, and other records in the same run
// are still processed (fault isolation).
func TestReconcile_LaunchFailed(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	treeA := t.TempDir()
	treeB := t.TempDir()
	mainTree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "44444"
		paneA   = "%110"
		paneB   = "%111"
		newPane = "%112"
		sess    = "proj"
		winA    = "fail"
		winB    = "ok"
		sidA    = "session-fail"
		sidB    = "session-ok"
	)

	// Record A: launch will fail.
	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneA,
		Tool:        model.ToolClaude,
		SessionID:   sidA,
		Tree:        treeA,
		TmuxSession: sess,
		TmuxWindow:  winA,
		BootID:      oldBoot,
	})
	// Record B: launch will succeed.
	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneB,
		Tool:        model.ToolClaude,
		SessionID:   sidB,
		Tree:        treeB,
		TmuxSession: sess,
		TmuxWindow:  winB,
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	// git worktree list for tree A and B.
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, treeA)},
		"git", "-C", treeA, "worktree", "list", "--porcelain")
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, treeB)},
		"git", "-C", treeB, "worktree", "list", "--porcelain")

	// Record A: has-session → absent, new-session FAILS.
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+sess)
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("server down")},
		"tmux", "new-session", "-d", "-s", sess, "-n", winA, "-c", treeA, "-P", "-F", "#{pane_id}")

	// Record B: session now exists (from attempted A), new-window succeeds.
	// Since has-session can only be keyed once, use Default for B's path.
	// Use fake.Default for incidental calls; B's path needs its own responses.
	// For record B: Connect will call has-session (sess still absent → same key
	// as A returns exit 1), then new-session with winB. Register that.
	fake.Respond(proc.FakeResult{Stdout: []byte(newPane + "\n")},
		"tmux", "new-session", "-d", "-s", sess, "-n", winB, "-c", treeB, "-P", "-F", "#{pane_id}")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "-l", "'claude' '--resume' '"+sidB+"'")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "Enter")
	fake.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", newPane, "@perch_session", sidB)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// One skip with reason launch-failed.
	foundLF := false
	for _, s := range report.Skipped {
		if s.Reason == "launch-failed" {
			foundLF = true
		}
	}
	if !foundLF {
		t.Errorf("expected a launch-failed skip; Skipped = %v", report.Skipped)
	}
	// One restore (record B).
	if len(report.Restored) != 1 {
		t.Errorf("Restored = %v, want 1 entry (fault isolation)", report.Restored)
	}
	// Record A kept (transient).
	wins, _ := state.LoadWindows(baseDir)
	foundA := false
	for _, w := range wins {
		if w.PaneKey == paneA {
			foundA = true
		}
	}
	if !foundA {
		t.Errorf("record A (pane %s) should be kept after launch-failed", paneA)
	}
}

// ── isDescendant ──────────────────────────────────────────────────────────────

// TestIsDescendant exercises the path helper directly.
func TestIsDescendant(t *testing.T) {
	tests := []struct {
		parent, child string
		want          bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/b/c/d", true},
		{"/a/b", "/a/bc", false}, // "bc" is not under "b"
		{"/a/b", "/a", false},
		{"/a/b", "/x/y", false},
		{"/a/b/", "/a/b/c", true}, // trailing slash in parent
	}
	for _, tt := range tests {
		got := isDescendant(tt.parent, tt.child)
		if got != tt.want {
			t.Errorf("isDescendant(%q, %q) = %v, want %v", tt.parent, tt.child, got, tt.want)
		}
	}
}

// ── empty state dir ───────────────────────────────────────────────────────────

// TestReconcile_EmptyStateDir verifies that an empty state dir (no windows/)
// returns a zero report and no error.
func TestReconcile_EmptyStateDir(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()
	fake.Default = &proc.FakeResult{}

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Kept)+len(report.Pruned)+len(report.Restored)+len(report.Skipped) != 0 {
		t.Errorf("expected empty report for empty state dir, got: %+v", report)
	}
}

// ── opencode restore (adapter path) ──────────────────────────────────────────

// TestReconcile_RestoreOpencode verifies that opencode records use the opencode
// adapter's ResumeArgs (--session <id>).
func TestReconcile_RestoreOpencode(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	tree := t.TempDir()
	mainTree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "33333"
		oldPane = "%120"
		newPane = "%121"
		sess    = "oc-proj"
		win     = "oc-feat"
		sid     = "oc-session-1"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     oldPane,
		Tool:        model.ToolOpencode,
		SessionID:   sid,
		Tree:        tree,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, tree)},
		"git", "-C", tree, "worktree", "list", "--porcelain")
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+sess)
	fake.Respond(proc.FakeResult{Stdout: []byte(newPane + "\n")},
		"tmux", "new-session", "-d", "-s", sess, "-n", win, "-c", tree, "-P", "-F", "#{pane_id}")
	// opencode ResumeArgs → ["--session", sid]; adapter.Name() = "opencode"
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "-l", "'opencode' '--session' '"+sid+"'")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "Enter")
	fake.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", newPane, "@perch_session", sid)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Restored) != 1 {
		t.Errorf("Restored = %v, want 1 entry", report.Restored)
	}
	// Verify send-keys literal contains opencode adapter args.
	foundSendKeys := false
	for _, c := range fake.Calls {
		if len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[1] == "-t" && c.Args[2] == newPane && c.Args[3] == "-l" {
			if c.Args[4] == "'opencode' '--session' '"+sid+"'" {
				foundSendKeys = true
			}
		}
	}
	if !foundSendKeys {
		t.Errorf("send-keys with opencode --session not found; calls: %v", fake.Calls)
	}

	// Verify new record uses opencode tool.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 || wins[0].Tool != model.ToolOpencode {
		t.Errorf("restored record tool = %v, want opencode", wins)
	}
}

// ── LoadWindows hard error ────────────────────────────────────────────────────

// TestReconcile_LoadWindowsError verifies that an unreadable state dir returns
// a hard error (not a skip).
func TestReconcile_LoadWindowsError(t *testing.T) {
	// Create a file at the windows/ path so ReadDir fails.
	baseDir := t.TempDir()
	windowsPath := filepath.Join(baseDir, "windows")
	if err := os.WriteFile(windowsPath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	fake := proc.NewFakeRunner()
	fake.Default = &proc.FakeResult{}

	deps := newDeps(t, baseDir, fake)
	_, err := Reconcile(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error for unreadable windows dir, got nil")
	}
}

// ── Fix 1: nested linked worktree gets longest-match ─────────────────────────

// TestReconcile_RestoreNestedLinkedWorktree proves Fix 1: when the git worktree
// list returns a main checkout whose path is an ancestor of a linked worktree
// (e.g. main=/repo, linked=/repo/worktrees/feat), findAncestorWorktree must
// select the linked worktree (longest match), not the main checkout. Before Fix 1
// the first-match logic selected the main and wrongly produced skip:main; after
// Fix 1 the record is RESTORED.
func TestReconcile_RestoreNestedLinkedWorktree(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	// Build a real directory layout so os.Stat passes.
	repo := t.TempDir()
	tree := filepath.Join(repo, "worktrees", "feat")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatalf("setup: MkdirAll: %v", err)
	}

	const (
		oldBoot = "oldboot"
		newBoot = "12345"
		oldPane = "%200"
		newPane = "%201"
		sess    = "nested-proj"
		win     = "feat"
		sid     = "claude-nested-1"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     oldPane,
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        tree,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      oldBoot,
	})

	// Boot mismatch → RESTORE branch.
	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	// No live panes.
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	// git worktree list: main=repo (ancestor of tree), linked=tree (exact).
	// First-match logic wrongly returns repo→skip:main.
	// Longest-match (Fix 1) returns tree→RESTORE.
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelain(repo, tree)},
		"git", "-C", tree, "worktree", "list", "--porcelain")

	// Launch → no session yet → new-session succeeds.
	fake.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+sess)
	fake.Respond(proc.FakeResult{Stdout: []byte(newPane + "\n")},
		"tmux", "new-session", "-d", "-s", sess, "-n", win, "-c", tree, "-P", "-F", "#{pane_id}")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "-l", "'claude' '--resume' '"+sid+"'")
	fake.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", newPane, "Enter")
	fake.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", newPane, "@perch_session", sid)

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must be RESTORED (not skipped with reason "main").
	if len(report.Restored) != 1 {
		t.Errorf("Restored = %v, want [%s]; Skipped = %v", report.Restored, oldPane, report.Skipped)
	}
	for _, s := range report.Skipped {
		if s.Reason == "main" {
			t.Errorf("got skip:main — Fix 1 longest-match not applied (first-match bug)")
		}
	}
	if countLaunches(fake) != 1 {
		t.Errorf("launch count = %d, want 1", countLaunches(fake))
	}
}

// ── Fix 2 / missing guard: no-worktree-match keeps record (transient) ────────

// TestReconcile_SkipNoWorktreeMatch verifies that when git worktree list
// succeeds but none of the returned worktree paths are ancestors of the
// record's Tree, the record gets a transient skip:no-worktree-match and the
// record FILE is kept (not deleted).
func TestReconcile_SkipNoWorktreeMatch(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	// tree exists on disk — passes Guard 3.
	tree := t.TempDir()
	// Unrelated worktree path (not an ancestor of tree).
	unrelated := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "22222"
		paneKey = "%210"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "some-session",
		Tree:        tree,
		TmuxSession: "proj",
		TmuxWindow:  "feat",
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	// git worktree list: only an unrelated worktree — no ancestor of tree.
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelainMainOnly(unrelated)},
		"git", "-C", tree, "worktree", "list", "--porcelain")

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "no-worktree-match" {
		t.Errorf("Skipped = %v, want [{no-worktree-match}]", report.Skipped)
	}
	// Transient: record must be kept.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 {
		t.Errorf("record count = %d, want 1 (transient skip keeps record)", len(wins))
	}
}

// ── unknown-tool: transient skip, record kept ─────────────────────────────────

// TestReconcile_SkipUnknownTool verifies that a record with an unrecognized
// Tool value gets a transient skip:unknown-tool, no Launch is issued, and the
// record FILE is kept for the next run.
func TestReconcile_SkipUnknownTool(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()

	// tree and mainTree both exist so Guard 3 and the git response align.
	tree := t.TempDir()
	mainTree := t.TempDir()

	const (
		oldBoot = "oldboot"
		newBoot = "33333"
		paneKey = "%220"
	)

	seedWindow(t, baseDir, model.Window{
		PaneKey:     paneKey,
		Tool:        model.Tool("ghost"), // unknown tool
		SessionID:   "some-session",
		Tree:        tree,
		TmuxSession: "proj",
		TmuxWindow:  "feat",
		BootID:      oldBoot,
	})

	fake.Respond(proc.FakeResult{Stdout: []byte(newBoot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)
	// git worktree list: main=mainTree (disjoint from tree), linked=tree.
	// Execution passes Guard 3 (tree exists) and Guard 4 (linked match, not main)
	// and reaches the adapterFor guard.
	fake.Respond(proc.FakeResult{Stdout: worktreePorcelain(mainTree, tree)},
		"git", "-C", tree, "worktree", "list", "--porcelain")

	deps := newDeps(t, baseDir, fake)
	report, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "unknown-tool" {
		t.Errorf("Skipped = %v, want [{unknown-tool}]", report.Skipped)
	}
	// No Launch must be issued.
	if countLaunches(fake) != 0 {
		t.Errorf("expected zero launches for unknown-tool skip, got %d", countLaunches(fake))
	}
	// Transient: record must be kept.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 {
		t.Errorf("record count = %d, want 1 (transient skip keeps record)", len(wins))
	}
}
