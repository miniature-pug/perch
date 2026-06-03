// internal/git/branches_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

func TestBranches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	cmd := exec.Command("git", "branch", "feature-x")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	r := proc.ExecRunner{}
	branches, err := git.Branches(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	found := make(map[string]bool)
	for _, b := range branches {
		found[b] = true
	}
	if !found["main"] {
		t.Errorf("branches should include main, got %v", branches)
	}
	if !found["feature-x"] {
		t.Errorf("branches should include feature-x, got %v", branches)
	}
}

func TestWorktrees_WithLinked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	wtDir := filepath.Join(t.TempDir(), "linked-wt")
	cmd := exec.Command("git", "worktree", "add", "-b", "wt-branch", wtDir, "HEAD")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	r := proc.ExecRunner{}
	wts, err := git.Worktrees(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(wts) < 2 {
		t.Fatalf("expected ≥2 worktrees, got %d", len(wts))
	}

	found := make(map[string]bool)
	for _, wt := range wts {
		if wt.Path == "" {
			t.Errorf("worktree has empty path: %+v", wt)
		}
		found[wt.Branch] = true
		if wt.Head == "" {
			t.Errorf("worktree has empty Head: %+v", wt)
		}
		if _, err := os.Stat(wt.Path); err != nil {
			t.Errorf("worktree path %s not stat-able: %v", wt.Path, err)
		}
	}
	if !found["wt-branch"] {
		t.Errorf("linked worktree branch wt-branch not found in %v", wts)
	}
}
