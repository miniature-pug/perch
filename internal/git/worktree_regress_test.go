// internal/git/worktree_regress_test.go holds real-git regressions for the
// worktree and branch audit findings GFS-16, GFS-17, GFS-18 and GFS-27.
package git_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func hasBranch(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// GFS-16: two branch names that slugify to the same handle collide on the
// worktree path. That is reported as a path collision, not as an existing
// branch, and no orphan branch is left behind.
func TestAddWorktree_SlugCollisionIsPathError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t)

	p1, _ := git.WorktreePath(repo, git.SlugifyBranch("feat/x"), "")
	if err := git.AddWorktree(ctx, r, repo, "feat/x", p1, "main"); err != nil {
		t.Fatal(err)
	}
	p2, _ := git.WorktreePath(repo, git.SlugifyBranch("feat-x"), "")
	err := git.AddWorktree(ctx, r, repo, "feat-x", p2, "main")
	if !errors.Is(err, git.ErrWorktreePathExists) || errors.Is(err, git.ErrBranchExists) {
		t.Fatalf("err = %v; want ErrWorktreePathExists, not ErrBranchExists", err)
	}
	if hasBranch(t, repo, "feat-x") {
		t.Error("failed AddWorktree left branch feat-x behind")
	}

	// AvailableWorktreePath steers the second branch to a free directory.
	p3, err := git.AvailableWorktreePath(repo, git.SlugifyBranch("feat-x"), "")
	if err != nil || p3 == p2 || !strings.HasSuffix(p3, "feat-x-2") {
		t.Fatalf("AvailableWorktreePath = %q, %v; want a free *-2 path", p3, err)
	}
	if err := git.AddWorktree(ctx, r, repo, "feat-x", p3, "main"); err != nil {
		t.Fatalf("AddWorktree at the free path: %v", err)
	}
}

// GFS-16: when git fails after creating the branch, AddWorktree deletes the
// branch again.
func TestAddWorktree_FailureDeletesCreatedBranch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t)
	// A registered worktree whose directory was deleted: the path is free on
	// disk, but git refuses it only after creating the new branch.
	p := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-q", "-b", "old", p, "main")
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}

	err := git.AddWorktree(ctx, r, repo, "fresh", p, "main")
	if err == nil {
		t.Fatal("AddWorktree onto a registered path should fail")
	}
	if errors.Is(err, git.ErrBranchExists) {
		t.Errorf("err = %v; must not claim the branch exists", err)
	}
	if hasBranch(t, repo, "fresh") {
		t.Error("failed AddWorktree left branch fresh behind")
	}
}

// GFS-17: a detached HEAD must not add a "(HEAD detached ...)" entry, and a
// branch that shares its name with a tag is listed by its plain name.
func TestBranches_DetachedHeadAndAmbiguousName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t)
	runGit(t, repo, "branch", "zz")
	runGit(t, repo, "branch", "foo")
	runGit(t, repo, "tag", "foo")
	runGit(t, repo, "checkout", "-q", "--detach")

	bs, err := git.Branches(ctx, r, repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(bs, ","); got != "main,foo,zz" {
		t.Errorf("Branches = %q, want main,foo,zz", got)
	}

	merged, err := git.BranchMerged(ctx, r, repo, "foo", "main")
	if err != nil || !merged {
		t.Errorf("BranchMerged(foo) = %v, %v; want true (refname:short prints heads/foo)", merged, err)
	}
}

// GFS-18: CheckoutBranch on a name that is a file, not a branch, must fail
// instead of restoring the file and reporting success.
func TestCheckoutBranch_RejectsFileAndTag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t) // has README.md
	runGit(t, repo, "tag", "v1")

	if err := git.CheckoutBranch(ctx, r, repo, "README.md"); err == nil {
		t.Error("CheckoutBranch(README.md) succeeded; want an error for a non-branch")
	}
	if err := git.CheckoutBranch(ctx, r, repo, "v1"); err == nil {
		t.Error("CheckoutBranch(v1) succeeded; a tag must not detach HEAD silently")
	}
	if cur, _ := git.CurrentBranch(ctx, r, repo); cur != "main" {
		t.Errorf("current branch = %q, want main", cur)
	}

	runGit(t, repo, "branch", "feat")
	if err := git.CheckoutBranch(ctx, r, repo, "feat"); err != nil {
		t.Fatalf("CheckoutBranch(feat): %v", err)
	}
	if cur, _ := git.CurrentBranch(ctx, r, repo); cur != "feat" {
		t.Errorf("current branch = %q, want feat", cur)
	}
}

// GFS-27: after a worktree directory is deleted outside perch, its branch
// stays "in use" until PruneWorktrees runs.
func TestPruneWorktrees_FreesBranchOfDeletedWorktree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t)
	p := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-q", "-b", "feat", p, "main")
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	p2 := filepath.Join(t.TempDir(), "wt2")
	if err := git.AddWorktreeExisting(ctx, r, repo, "feat", p2); err == nil {
		t.Fatal("test premise: git should still consider feat checked out")
	}
	if err := git.PruneWorktrees(ctx, r, repo); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	if err := git.AddWorktreeExisting(ctx, r, repo, "feat", p2); err != nil {
		t.Fatalf("AddWorktreeExisting after prune: %v", err)
	}
	if out := gitOut(t, repo, "worktree", "list"); !strings.Contains(out, p2) {
		t.Errorf("worktree list lacks %s:\n%s", p2, out)
	}
}
