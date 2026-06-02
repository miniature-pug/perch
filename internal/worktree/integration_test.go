//go:build integration

package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

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
