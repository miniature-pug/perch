// internal/git/branches_test.go
package git_test

import (
	"context"
	"os/exec"
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

// TestBranches_UnbornRepo_ReturnsNonNilEmptySlice verifies that Branches returns
// a non-nil empty slice (not nil) for a repo with no commits (unborn HEAD).
// A nil return marshals to JSON null and crashes the Svelte frontend's {#each branches}.
func TestBranches_UnbornRepo_ReturnsNonNilEmptySlice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	// git init but NO commit → unborn HEAD, no branches
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Also set user config so git doesn't complain
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	r := proc.ExecRunner{}
	bs, err := git.Branches(context.Background(), r, dir)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if bs == nil {
		t.Fatal("Branches must return non-nil slice for unborn repo (nil marshals to JSON null)")
	}
	if len(bs) != 0 {
		t.Errorf("expected empty slice for branchless repo, got %v", bs)
	}
}
