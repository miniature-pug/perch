//go:build integration

package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// newTestServer returns a Tmux wired to a private socket so tests never touch
// the user's default tmux server. The server is killed and its socket file is
// removed on test cleanup (best-effort).
func newTestServer(t *testing.T) tmux.Tmux {
	t.Helper()
	socket := fmt.Sprintf("perch-test-%d", os.Getpid())
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

// newGitRepo initialises a bare-minimum real git repository in a temp dir and
// returns its path. It creates one committed file so the repo has a HEAD.
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
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
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("init\n"), 0o644); err != nil {
		t.Fatalf("write readme.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "init")
	return dir
}

func skipIfMissing(t *testing.T, bins ...string) {
	t.Helper()
	for _, b := range bins {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not found in PATH: %v", b, err)
		}
	}
}

// TestIntegration_Worktree_CreateReflectsInPorcelain verifies that AddWorktree
// creates a linked worktree that git's own porcelain reports correctly.
func TestIntegration_Worktree_CreateReflectsInPorcelain(t *testing.T) {
	skipIfMissing(t, "git")
	ctx := context.Background()
	repo := newGitRepo(t)
	r := proc.ExecRunner{}

	// Pass a non-existent subdir so git creates it; placing it inside a separate
	// TempDir ensures t.Cleanup removes it.
	wtBase := t.TempDir()
	wtPath := filepath.Join(wtBase, "wt")

	if err := git.AddWorktree(ctx, r, repo, "feat-x", wtPath, "HEAD"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	list := string(out)
	if !strings.Contains(list, wtPath) {
		t.Errorf("worktree list does not contain %q\nfull output:\n%s", wtPath, list)
	}
	if !strings.Contains(list, "feat-x") {
		t.Errorf("worktree list does not contain branch feat-x\nfull output:\n%s", list)
	}
}

// TestIntegration_Worktree_SeedCopiesFile verifies that Seed copies a file
// declared in config.Files.Copy from the repo into the new worktree.
func TestIntegration_Worktree_SeedCopiesFile(t *testing.T) {
	skipIfMissing(t, "git")
	ctx := context.Background()
	repo := newGitRepo(t)
	r := proc.ExecRunner{}

	// Write and commit seed.txt so it exists in the repo.
	seedContent := "hello from seed\n"
	seedSrc := filepath.Join(repo, "seed.txt")
	if err := os.WriteFile(seedSrc, []byte(seedContent), 0o644); err != nil {
		t.Fatalf("write seed.txt: %v", err)
	}
	runGit := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("add", "seed.txt")
	runGit("commit", "-m", "add seed")

	wtBase := t.TempDir()
	wtPath := filepath.Join(wtBase, "wt-seed")
	if err := git.AddWorktree(ctx, r, repo, "feat-seed", wtPath, "HEAD"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	if err := Seed(repo, wtPath, config.Files{Copy: []string{"seed.txt"}}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	dst := filepath.Join(wtPath, "seed.txt")
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read %s: %v", dst, err)
	}
	if string(data) != seedContent {
		t.Errorf("seed.txt content = %q, want %q", string(data), seedContent)
	}
}

// TestIntegration_Worktree_RemoveClean verifies that RemoveWorktree(force=false)
// succeeds on a clean worktree, removes the directory, and clears it from
// git's worktree list.
func TestIntegration_Worktree_RemoveClean(t *testing.T) {
	skipIfMissing(t, "git")
	ctx := context.Background()
	repo := newGitRepo(t)
	r := proc.ExecRunner{}

	wtBase := t.TempDir()
	wtPath := filepath.Join(wtBase, "wt-clean")
	if err := git.AddWorktree(ctx, r, repo, "feat-clean", wtPath, "HEAD"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	if err := git.RemoveWorktree(ctx, r, repo, wtPath, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree dir still exists after remove: %v", err)
	}

	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	if strings.Contains(string(out), wtPath) {
		t.Errorf("git worktree list still shows %q after remove\nfull output:\n%s", wtPath, out)
	}
}

// TestIntegration_Worktree_RemoveDirtyThenForce verifies ErrWorktreeDirty is
// returned for a non-force remove of a dirty worktree, and that a forced remove
// then succeeds. Reports which dirty condition triggered the sentinel.
func TestIntegration_Worktree_RemoveDirtyThenForce(t *testing.T) {
	skipIfMissing(t, "git")
	ctx := context.Background()
	repo := newGitRepo(t)
	r := proc.ExecRunner{}

	wtBase := t.TempDir()
	wtPath := filepath.Join(wtBase, "wt-dirty")
	if err := git.AddWorktree(ctx, r, repo, "feat-dirty", wtPath, "HEAD"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// First try untracked file — most git versions flag this as dirty.
	untrackedPath := filepath.Join(wtPath, "untracked.txt")
	if err := os.WriteFile(untrackedPath, []byte("untracked\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	err := git.RemoveWorktree(ctx, r, repo, wtPath, false)
	dirtyCondition := "untracked file"
	if err == nil || !errors.Is(err, git.ErrWorktreeDirty) {
		// Some git versions don't treat untracked-only as dirty; fall back to a
		// modified tracked file.
		t.Logf("untracked-only did NOT trigger ErrWorktreeDirty (err=%v); trying modified tracked file", err)
		_ = os.Remove(untrackedPath)

		// readme.txt was committed in newGitRepo; modify it in-place.
		modPath := filepath.Join(wtPath, "readme.txt")
		if err2 := os.WriteFile(modPath, []byte("modified\n"), 0o644); err2 != nil {
			t.Fatalf("write modified tracked file: %v", err2)
		}
		dirtyCondition = "modified tracked file"
		err = git.RemoveWorktree(ctx, r, repo, wtPath, false)
	}

	if err == nil {
		t.Fatal("RemoveWorktree(force=false) on dirty worktree returned nil, want ErrWorktreeDirty")
	}
	if !errors.Is(err, git.ErrWorktreeDirty) {
		t.Fatalf("RemoveWorktree(force=false) error = %v, want errors.Is ErrWorktreeDirty", err)
	}
	t.Logf("ErrWorktreeDirty triggered by: %s", dirtyCondition)

	// Force remove must succeed.
	if err := git.RemoveWorktree(ctx, r, repo, wtPath, true); err != nil {
		t.Fatalf("RemoveWorktree(force=true): %v", err)
	}
	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree dir still exists after forced remove: %v", err)
	}
}

// TestIntegration_Worktree_DeferredSelfClose exercises the full §7.2 teardown:
// DeferredRemove dispatches a backgrounded CleanupScript that kills the window,
// renames the tree to trash, prunes the git worktree reference, and removes the
// trash dir. It also probes whether the bare `tmux kill-window` inside the
// script hits the private socket or the default server.
func TestIntegration_Worktree_DeferredSelfClose(t *testing.T) {
	skipIfMissing(t, "git", "tmux")
	ctx := context.Background()
	repo := newGitRepo(t)
	r := proc.ExecRunner{}

	// Create the worktree that will be torn down.
	wtBase := t.TempDir()
	wtPath := filepath.Join(wtBase, "wt-defer")
	if err := git.AddWorktree(ctx, r, repo, "defer-x", wtPath, "HEAD"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	// Write a committed file inside the tree so it is non-empty at teardown time.
	if err := os.WriteFile(filepath.Join(wtPath, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write work.txt: %v", err)
	}

	tmx := newTestServer(t)

	// Bootstrap a keepalive session BEFORE the target session. This ensures the
	// server stays alive when kill-window removes the last window of defersess —
	// a server with only one session self-exits on last-window-kill, which would
	// SIGHUP the in-flight run-shell and abort mv/prune/rm.
	if _, err := tmx.NewSession(ctx, "keepalive", "kw", t.TempDir()); err != nil {
		t.Fatalf("NewSession keepalive: %v", err)
	}

	paneID, err := tmx.Connect(ctx, "defersess", "deferwin", wtPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Verify the session/window exists before we tear it down.
	exists, err := tmx.HasSession(ctx, "defersess")
	if err != nil {
		t.Fatalf("HasSession pre-check: %v", err)
	}
	if !exists {
		t.Fatal("defersess should exist before DeferredRemove")
	}

	baseDir := t.TempDir()
	w := model.Window{
		PaneKey:     paneID,
		Tool:        model.ToolClaude,
		Tree:        wtPath,
		TmuxSession: "defersess",
		TmuxWindow:  "deferwin",
	}
	if err := state.SaveWindow(baseDir, w); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	opts := tmux.CleanupOpts{
		SourceWindowTarget: tmux.WindowTarget("defersess", "deferwin"),
		SwitchToTarget:     "",
		Tree:               wtPath,
		Branch:             "defer-x",
		RepoDir:            repo,
	}

	now := time.Now().Unix()
	if err := DeferredRemove(ctx, tmx, baseDir, opts, paneID, now, "test"); err != nil {
		t.Fatalf("DeferredRemove: %v", err)
	}

	// (c) DeferredRemove removes the state record synchronously — check immediately.
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows after DeferredRemove: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("LoadWindows: got %d records after DeferredRemove, want 0", len(windows))
	}

	// The backgrounded CleanupScript runs: sleep 0.3 → kill-window → mv → prune
	// → branch -d → rm. We poll until the trash dir is gone (rm = terminal step)
	// which guarantees the entire chain completed. Then read the tmux state once.
	trashGlob := filepath.Join(wtBase, ".perch_trash_test_*")
	var treeGone, trashGone bool
	const maxPoll = 40
	for i := 0; i < maxPoll; i++ {
		time.Sleep(100 * time.Millisecond)

		if _, err := os.Stat(wtPath); errors.Is(err, os.ErrNotExist) {
			treeGone = true
		}
		matches, _ := filepath.Glob(trashGlob)
		if len(matches) == 0 && treeGone {
			trashGone = true
			break
		}
	}

	// Hard assertions (a): tree dir removed.
	if !treeGone {
		t.Fatalf("worktree dir %q still exists after %dms", wtPath, maxPoll*100)
	}
	// Hard assertion (d): trash dir removed.
	if !trashGone {
		matches, _ := filepath.Glob(trashGlob)
		t.Fatalf("trash dir(s) still exist after %dms: %v", maxPoll*100, matches)
	}

	// Hard assertion (b): git worktree prune ran — repo no longer lists wtPath.
	out, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(out), wtPath) {
		t.Errorf("git worktree list still shows %q after teardown\nfull output:\n%s", wtPath, out)
	}

	// Probe (non-fatal): did the bare `tmux kill-window` in the script hit the
	// private socket? If so, the session should be gone (it was the only window
	// in defersess).
	sessExists, err := tmx.HasSession(ctx, "defersess")
	if err != nil {
		t.Logf("HasSession(defersess) after teardown: err=%v", err)
	} else if !sessExists {
		t.Logf("FINDING: defersess session is GONE after script — bare tmux kill-window DID target the private socket (full e2e window kill worked)")
	} else {
		// Session still alive; check whether the window's pane is gone.
		panes, _ := tmx.ListPanes(ctx, tmux.WindowTarget("defersess", "deferwin"))
		if len(panes) == 0 {
			t.Logf("FINDING: defersess session still present but deferwin window is GONE — kill-window targeted private socket")
		} else {
			t.Logf("FINDING: defersess session and deferwin window still PRESENT after script — bare tmux kill-window did NOT hit the private socket (missed default-server gap)")
		}
	}

	// Cleanup: kill the target session if still alive (keepalive is handled by
	// newTestServer's server-level cleanup).
	_ = tmx.KillSession(ctx, "defersess")
}

// TestIntegration_Mapping_SurvivesRestart verifies that a Mapping written to
// state.json is correctly recovered by a fresh LoadState call, simulating a
// process restart.
func TestIntegration_Mapping_SurvivesRestart(t *testing.T) {
	baseDir := t.TempDir()

	st, err := state.LoadState(baseDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	state.SetMapping(&st, "sess-1", state.Mapping{
		Tool:   model.Tool("claude"),
		Tree:   "/some/tree",
		Choice: state.ChoiceWorktree,
	})
	if err := state.SaveState(baseDir, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Simulate a restart by loading from disk into a fresh variable.
	st2, err := state.LoadState(baseDir)
	if err != nil {
		t.Fatalf("LoadState (fresh): %v", err)
	}

	mp, ok := state.LookupMapping(st2, "sess-1")
	if !ok {
		t.Fatal("LookupMapping: key sess-1 not found after restart")
	}
	if string(mp.Tool) != "claude" {
		t.Errorf("Tool = %q, want claude", mp.Tool)
	}
	if mp.Tree != "/some/tree" {
		t.Errorf("Tree = %q, want /some/tree", mp.Tree)
	}
	if mp.Choice != state.ChoiceWorktree {
		t.Errorf("Choice = %q, want %q", mp.Choice, state.ChoiceWorktree)
	}
}
