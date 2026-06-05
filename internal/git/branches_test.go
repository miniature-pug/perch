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
