// internal/git/branches_test.go
package git_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
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

// TestBranches_PinsDefaultOrCurrentFirst is the FakeRunner ordering guard: the
// current/default branch must come FIRST (callers take branches[0]), with the
// rest alphabetical, regardless of the order `git branch` emitted them.
func TestBranches_PinsDefaultOrCurrentFirst(t *testing.T) {
	branchList := proc.FakeResult{Stdout: []byte("zeta\nmain\nalpha\n")}

	t.Run("current branch pinned first", func(t *testing.T) {
		r := proc.NewFakeRunner()
		r.Respond(branchList, "git", "-C", "/repo", "for-each-ref", "--format=%(refname:lstrip=2)", "refs/heads/")
		// HEAD is on "main" → it is pinned first.
		r.Respond(proc.FakeResult{Stdout: []byte("main\n")},
			"git", "-C", "/repo", "symbolic-ref", "--quiet", "--short", "HEAD")

		got, err := git.Branches(context.Background(), r, "/repo")
		if err != nil {
			t.Fatalf("Branches: %v", err)
		}
		want := []string{"main", "alpha", "zeta"}
		if !equalStrings(got, want) {
			t.Errorf("Branches = %v, want %v (current branch first, rest alphabetical)", got, want)
		}
	})

	t.Run("detached HEAD falls back to origin/HEAD default", func(t *testing.T) {
		r := proc.NewFakeRunner()
		r.Respond(branchList, "git", "-C", "/repo", "for-each-ref", "--format=%(refname:lstrip=2)", "refs/heads/")
		// Detached HEAD → symbolic-ref HEAD fails.
		r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
			"git", "-C", "/repo", "symbolic-ref", "--quiet", "--short", "HEAD")
		// Default resolves from origin/HEAD → origin/main.
		r.Respond(proc.FakeResult{Stdout: []byte("origin/main\n")},
			"git", "-C", "/repo", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")

		got, err := git.Branches(context.Background(), r, "/repo")
		if err != nil {
			t.Fatalf("Branches: %v", err)
		}
		if len(got) == 0 || got[0] != "main" {
			t.Errorf("Branches = %v, want default branch \"main\" first", got)
		}
	})

	t.Run("no HEAD/origin info falls back to main convention", func(t *testing.T) {
		r := proc.NewFakeRunner()
		r.Respond(branchList, "git", "-C", "/repo", "for-each-ref", "--format=%(refname:lstrip=2)", "refs/heads/")
		r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
			"git", "-C", "/repo", "symbolic-ref", "--quiet", "--short", "HEAD")
		r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
			"git", "-C", "/repo", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")

		got, err := git.Branches(context.Background(), r, "/repo")
		if err != nil {
			t.Fatalf("Branches: %v", err)
		}
		if len(got) == 0 || got[0] != "main" {
			t.Errorf("Branches = %v, want \"main\" first via convention fallback", got)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
