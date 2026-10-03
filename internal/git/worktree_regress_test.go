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

// A failing post-checkout hook makes git exit non-zero after it created the
// worktree. AddWorktree removes that worktree and the branch, so a retry
// starts clean.
func TestAddWorktree_HookFailureLeavesNothingBehind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "wt")

	if err := git.AddWorktree(ctx, r, repo, "hooked", p, "main"); err == nil {
		t.Fatal("AddWorktree should report the hook failure")
	}
	if _, err := os.Lstat(p); err == nil {
		t.Error("worktree directory left behind")
	}
	if hasBranch(t, repo, "hooked") {
		t.Error("branch left behind")
	}
	if out := gitOut(t, repo, "worktree", "list", "--porcelain"); strings.Contains(out, p) {
		t.Errorf("worktree still registered:\n%s", out)
	}
}

// The cleanup after a failed add runs even when ctx has expired.
func TestAddWorktree_CleanupSurvivesExpiredContext(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"git", "-C", "/repos/proj", "rev-parse", "--verify", "--quiet", "refs/heads/feat-x")
	r.Respond(proc.FakeResult{Stderr: []byte("fatal: timed out"), Err: context.DeadlineExceeded},
		"git", "-C", "/repos/proj", "worktree", "add", "-b", "feat-x", "--", "/repos/proj__worktrees/feat-x", "HEAD")
	r.Respond(proc.FakeResult{}, "git", "-C", "/repos/proj", "branch", "-D", "feat-x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cr := &ctxRecordingRunner{inner: r}
	_ = git.AddWorktree(ctx, cr, "/repos/proj", "feat-x", "/repos/proj__worktrees/feat-x", "HEAD")
	if !cr.deleteSawLiveCtx {
		t.Error("branch -D ran with an expired context (or not at all)")
	}
}

// ctxRecordingRunner records whether `branch -D` ran under a live context.
type ctxRecordingRunner struct {
	inner            *proc.FakeRunner
	deleteSawLiveCtx bool
}

func (c *ctxRecordingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	if len(args) > 3 && args[2] == "branch" && args[3] == "-D" && ctx.Err() == nil {
		c.deleteSawLiveCtx = true
	}
	return c.inner.Run(ctx, name, args...)
}

func (c *ctxRecordingRunner) RunInDir(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	return c.inner.RunInDir(ctx, dir, name, args...)
}

func (c *ctxRecordingRunner) RunStdin(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	return c.inner.RunStdin(ctx, dir, stdin, name, args...)
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
// stays "in use" until its registration is dropped. ForgetStaleWorktrees
// drops exactly that registration, and leaves an unrelated worktree whose
// directory is only temporarily missing (an unmounted disk) registered.
func TestForgetStaleWorktrees_FreesOnlyTheMatchingEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t)
	p := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-q", "-b", "feat", p, "main")
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	// An unrelated worktree on a "disk" that is unmounted right now.
	usb := filepath.Join(t.TempDir(), "usb-wt")
	runGit(t, repo, "worktree", "add", "-q", "-b", "usb", usb, "main")
	away := usb + ".away"
	if err := os.Rename(usb, away); err != nil {
		t.Fatal(err)
	}

	p2 := filepath.Join(t.TempDir(), "wt2")
	if err := git.AddWorktreeExisting(ctx, r, repo, "feat", p2); err == nil {
		t.Fatal("test premise: git should still consider feat checked out")
	}
	if err := git.ForgetStaleWorktrees(ctx, r, repo, "", "feat"); err != nil {
		t.Fatalf("ForgetStaleWorktrees(branch): %v", err)
	}
	if err := git.AddWorktreeExisting(ctx, r, repo, "feat", p2); err != nil {
		t.Fatalf("AddWorktreeExisting after forgetting the stale entry: %v", err)
	}

	// The unrelated tree comes back and still works.
	if err := os.Rename(away, usb); err != nil {
		t.Fatal(err)
	}
	if out := gitOut(t, usb, "-C", usb, "status", "--porcelain"); out != "" {
		t.Errorf("status in the remounted tree = %q, want clean", out)
	}
	if err := git.AddWorktreeExisting(ctx, r, repo, "usb", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("the unrelated tree's branch was freed: its registration was dropped")
	}
}

// ForgetStaleWorktrees by path, when the directory exists but its .git file
// is gone: git refuses `worktree remove`, so only the admin dir is dropped
// and the directory's files stay.
func TestForgetStaleWorktrees_DirWithoutGitFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	repo := initRepo(t)
	p := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-q", "-b", "feat", p, "main")
	if err := os.Remove(filepath.Join(p, ".git")); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(p, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := git.ForgetStaleWorktrees(ctx, r, repo, p, ""); err != nil {
		t.Fatalf("ForgetStaleWorktrees(path): %v", err)
	}
	if out := gitOut(t, repo, "-C", repo, "worktree", "list", "--porcelain"); strings.Contains(out, p) {
		t.Errorf("registration for %s survived:\n%s", p, out)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a file in the directory was deleted: %v", err)
	}
}
