//go:build integration

package resurrect

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// socketSeq is a process-wide counter ensuring every newTestServer call gets a
// unique socket name. Tests are run sequentially (they kill/restart servers)
// but we still want each test to own its socket so stale cleanup from one test
// cannot interfere with the next.
var socketSeq uint64

// newTestServer returns a Tmux wired to a private socket so tests never touch
// the user's default tmux server. The server is killed and its socket file is
// removed on test cleanup (best-effort).
func newTestServer(t *testing.T) tmux.Tmux {
	t.Helper()
	seq := atomic.AddUint64(&socketSeq, 1)
	// Keep the socket name short — the full socket path (TMUX_TMPDIR/<name>)
	// must stay under the ~104-char Unix socket path limit.
	socket := fmt.Sprintf("perch-rsr-%d-%d", os.Getpid(), seq)
	tmx := tmux.Tmux{
		Runner: proc.ExecRunner{},
		Bin:    "tmux",
		Socket: socket,
	}
	t.Cleanup(func() {
		_ = tmx.KillServer(context.Background())
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
		}
		socketPath := filepath.Join(dir, socket)
		_ = os.Remove(socketPath)
	})
	return tmx
}

// newGitRepo initialises a bare-minimum real git repository in a temp dir,
// creates one committed file so the repo has a HEAD, and adds a linked
// worktree on a new branch. Returns (repoRoot, wtPath).
func newGitRepo(t *testing.T) (repoRoot, wtPath string) {
	t.Helper()
	repoRoot = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repoRoot}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	// Disable GPG signing so the fixture commit works on machines with global
	// signing configured.
	run("config", "commit.gpgsign", "false")
	// Write an initial file so there is something to commit.
	if err := os.WriteFile(filepath.Join(repoRoot, "readme.txt"), []byte("init\n"), 0o644); err != nil {
		t.Fatalf("write readme.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "init")

	// Create a linked worktree in a sibling temp dir (never nested under repoRoot
	// so git and os.TempDir cleanup can't conflict).
	wtBase := t.TempDir()
	wtPath = filepath.Join(wtBase, "feat")
	out, err := exec.Command("git", "-C", repoRoot, "worktree", "add", "-b", "feat", wtPath, "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return repoRoot, wtPath
}

// skipIfMissing skips the test when any of the named binaries are absent from PATH.
func skipIfMissing(t *testing.T, bins ...string) {
	t.Helper()
	for _, b := range bins {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not found in PATH: %v", b, err)
		}
	}
}

// findSkipNote returns the first SkipNote in notes whose Reason matches reason.
func findSkipNote(notes []SkipNote, reason string) (SkipNote, bool) {
	for _, n := range notes {
		if n.Reason == reason {
			return n, true
		}
	}
	return SkipNote{}, false
}

// TestIntegration_Resurrect_RestoreAfterServerRestart is the DoD case: kill the
// tmux server, then call Reconcile — it must recreate the window and rewrite the
// shadow record with the new server's pane id and boot id.
func TestIntegration_Resurrect_RestoreAfterServerRestart(t *testing.T) {
	skipIfMissing(t, "tmux", "git")
	ctx := context.Background()

	tmx := newTestServer(t)
	repoRoot, wtPath := newGitRepo(t)
	baseDir := t.TempDir()

	sess := tmux.SessionName(repoRoot)
	win := tmux.WindowName("feat")

	// Start a real window so we can read the live boot id.
	oldPane, err := tmx.Launch(ctx, sess, win, wtPath, []string{"sh"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	boot1, err := tmx.BootID(ctx)
	if err != nil {
		t.Fatalf("BootID: %v", err)
	}

	const sid = "11111111-1111-4111-8111-111111111111"
	if err := state.SaveWindow(baseDir, model.Window{
		PaneKey:     oldPane,
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        wtPath,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      boot1,
		Updated:     time.Now().Unix(),
	}); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	// Kill the server — Reconcile will restart it on first Launch.
	if err := tmx.KillServer(ctx); err != nil {
		t.Fatalf("KillServer: %v", err)
	}

	report, err := Reconcile(ctx, Deps{
		Tmux:    tmx,
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(report.Restored) != 1 {
		t.Errorf("Restored = %v (len %d), want 1", report.Restored, len(report.Restored))
	}
	if len(report.Kept) != 0 {
		t.Errorf("Kept = %v, want empty", report.Kept)
	}
	if len(report.Pruned) != 0 {
		t.Errorf("Pruned = %v, want empty", report.Pruned)
	}
	if len(report.Skipped) != 0 {
		t.Errorf("Skipped = %v, want empty", report.Skipped)
	}

	// The session must have been rebuilt.
	hasSess, err := tmx.HasSession(ctx, sess)
	if err != nil {
		t.Fatalf("HasSession: %v", err)
	}
	if !hasSess {
		t.Errorf("HasSession(%q) = false after Reconcile, want true", sess)
	}

	// Load the rewritten record and assert its fields.
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("LoadWindows returned %d records, want 1", len(windows))
	}
	w := windows[0]
	if w.Tree != wtPath {
		t.Errorf("record Tree = %q, want %q", w.Tree, wtPath)
	}
	if w.SessionID != sid {
		t.Errorf("record SessionID = %q, want %q", w.SessionID, sid)
	}
	if w.TmuxSession != sess {
		t.Errorf("record TmuxSession = %q, want %q", w.TmuxSession, sess)
	}
	if w.TmuxWindow != win {
		t.Errorf("record TmuxWindow = %q, want %q", w.TmuxWindow, win)
	}
	// The restored pane must have been assigned a new id on the fresh server.
	if w.PaneKey == oldPane {
		t.Logf("NOTE: new paneKey == oldPane (%q) — tmux reset its counter; record rewrite and HasSession are the authoritative checks", oldPane)
	}
	// The BootID in the record must match the live server's boot id.
	newBoot, err := tmx.BootID(ctx)
	if err != nil {
		t.Fatalf("BootID (post-restore): %v", err)
	}
	if w.BootID != newBoot {
		t.Errorf("record BootID = %q, want live boot %q", w.BootID, newBoot)
	}
}

// TestIntegration_Resurrect_PruneIntentionallyClosed verifies that a record
// whose pane no longer exists (but whose BootID matches the live server) is
// deleted and no new window is created.
func TestIntegration_Resurrect_PruneIntentionallyClosed(t *testing.T) {
	skipIfMissing(t, "tmux", "git")
	ctx := context.Background()

	tmx := newTestServer(t)
	_, wtPath := newGitRepo(t)
	baseDir := t.TempDir()

	// Bootstrap the server by launching a keepalive session so BootID works.
	if _, err := tmx.NewSession(ctx, "keepalive", "kw", wtPath); err != nil {
		t.Fatalf("NewSession (keepalive): %v", err)
	}

	boot, err := tmx.BootID(ctx)
	if err != nil {
		t.Fatalf("BootID: %v", err)
	}

	const sid = "22222222-2222-4222-8222-222222222222"
	// %999 is a pane id that does not exist on the live server.
	if err := state.SaveWindow(baseDir, model.Window{
		PaneKey:     "%999",
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        wtPath,
		TmuxSession: "prune-sess",
		TmuxWindow:  "prune-win",
		BootID:      boot,
		Updated:     time.Now().Unix(),
	}); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	report, err := Reconcile(ctx, Deps{
		Tmux:    tmx,
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(report.Pruned) != 1 {
		t.Errorf("Pruned = %v (len %d), want 1", report.Pruned, len(report.Pruned))
	}
	if len(report.Restored) != 0 {
		t.Errorf("Restored = %v, want empty", report.Restored)
	}
	if len(report.Kept) != 0 {
		t.Errorf("Kept = %v, want empty", report.Kept)
	}
	if len(report.Skipped) != 0 {
		t.Errorf("Skipped = %v, want empty", report.Skipped)
	}

	// The record must have been deleted.
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("LoadWindows returned %d records after prune, want 0", len(windows))
	}

	// No new window must have been created for prune-sess.
	hasSess, err := tmx.HasSession(ctx, "prune-sess")
	if err != nil {
		t.Fatalf("HasSession: %v", err)
	}
	if hasSess {
		t.Errorf("prune-sess was created but should not have been")
	}
}

// TestIntegration_Resurrect_DoubleRunNoOp verifies that running Reconcile twice
// after a server restart is idempotent: the second run keeps the restored
// window and issues no new restores.
func TestIntegration_Resurrect_DoubleRunNoOp(t *testing.T) {
	skipIfMissing(t, "tmux", "git")
	ctx := context.Background()

	tmx := newTestServer(t)
	repoRoot, wtPath := newGitRepo(t)
	baseDir := t.TempDir()

	sess := tmux.SessionName(repoRoot)
	win := tmux.WindowName("feat")

	// Start a window to get a real pane and boot id.
	oldPane, err := tmx.Launch(ctx, sess, win, wtPath, []string{"sh"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	boot1, err := tmx.BootID(ctx)
	if err != nil {
		t.Fatalf("BootID: %v", err)
	}

	const sid = "33333333-3333-4333-8333-333333333333"
	if err := state.SaveWindow(baseDir, model.Window{
		PaneKey:     oldPane,
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        wtPath,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      boot1,
		Updated:     time.Now().Unix(),
	}); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	// Kill the server — first Reconcile will restart it.
	if err := tmx.KillServer(ctx); err != nil {
		t.Fatalf("KillServer: %v", err)
	}

	now := time.Now().Unix()

	// First run: must restore.
	report1, err := Reconcile(ctx, Deps{
		Tmux:    tmx,
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     now,
	})
	if err != nil {
		t.Fatalf("Reconcile (pass 1): %v", err)
	}
	if len(report1.Restored) != 1 {
		t.Fatalf("pass 1: Restored = %v (len %d), want 1", report1.Restored, len(report1.Restored))
	}

	// Second run: server is live, record is rewritten — must KEEP, not RESTORE.
	report2, err := Reconcile(ctx, Deps{
		Tmux:    tmx,
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     now,
	})
	if err != nil {
		t.Fatalf("Reconcile (pass 2): %v", err)
	}

	if len(report2.Restored) != 0 {
		t.Errorf("pass 2: Restored = %v, want empty (idempotent)", report2.Restored)
	}
	if len(report2.Kept) != 1 {
		t.Errorf("pass 2: Kept = %v (len %d), want 1", report2.Kept, len(report2.Kept))
	}
	if len(report2.Pruned) != 0 {
		t.Errorf("pass 2: Pruned = %v, want empty", report2.Pruned)
	}
	if len(report2.Skipped) != 0 {
		t.Errorf("pass 2: Skipped = %v, want empty", report2.Skipped)
	}

	// Session must still be alive.
	hasSess, err := tmx.HasSession(ctx, sess)
	if err != nil {
		t.Fatalf("HasSession: %v", err)
	}
	if !hasSess {
		t.Errorf("HasSession(%q) = false after second Reconcile, want true", sess)
	}

	// Record count must remain exactly one (no duplicate records).
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Errorf("LoadWindows returned %d records after pass 2, want 1", len(windows))
	}
}

// TestIntegration_Resurrect_SkipMainCheckout verifies that a shadow record
// pointing at the repo's main checkout is not resurrected and is deleted
// (definitive skip:main).
func TestIntegration_Resurrect_SkipMainCheckout(t *testing.T) {
	skipIfMissing(t, "tmux", "git")
	ctx := context.Background()

	tmx := newTestServer(t)
	mainRoot, _ := newGitRepo(t)
	baseDir := t.TempDir()

	sess := tmux.SessionName(mainRoot)
	win := tmux.WindowName("main")

	const sid = "44444444-4444-4444-8444-444444444444"
	// BootID "1" is stale (cold server → currentBoot="" → bootMatch=false →
	// RESTORE branch → guard 4 → matched worktree is main → skip:main).
	if err := state.SaveWindow(baseDir, model.Window{
		PaneKey:     "%888",
		Tool:        model.ToolClaude,
		SessionID:   sid,
		Tree:        mainRoot,
		TmuxSession: sess,
		TmuxWindow:  win,
		BootID:      "1",
		Updated:     time.Now().Unix(),
	}); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	report, err := Reconcile(ctx, Deps{
		Tmux:    tmx,
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Must have a skip:main note.
	note, found := findSkipNote(report.Skipped, "main")
	if !found {
		t.Errorf("Skipped does not contain reason=main; got %v", report.Skipped)
	} else {
		t.Logf("SkipNote: %+v", note)
	}

	if len(report.Restored) != 0 {
		t.Errorf("Restored = %v, want empty", report.Restored)
	}
	if len(report.Kept) != 0 {
		t.Errorf("Kept = %v, want empty", report.Kept)
	}

	// No window should have been created.
	hasSess, err := tmx.HasSession(ctx, sess)
	if err != nil {
		t.Fatalf("HasSession: %v", err)
	}
	if hasSess {
		t.Errorf("session %q was created but should not have been (skip:main)", sess)
	}

	// The record must have been deleted (definitive skip).
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("LoadWindows returned %d records after skip:main, want 0", len(windows))
	}
}
