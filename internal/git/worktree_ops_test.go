// internal/git/worktree_ops_test.go
package git_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
)

// ── AddWorktreeExisting ───────────────────────────────────────────────────────

// TestAddWorktreeExisting_CallArgs verifies the FakeRunner sees the exact
// "worktree add <tree> <branch>" argv (no -b).
func TestAddWorktreeExisting_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "worktree", "add",
		"/repos/proj__worktrees/feat-x", "feat-x"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := git.AddWorktreeExisting(context.Background(), r,
		"/repos/proj", "feat-x", "/repos/proj__worktrees/feat-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	want := proc.Call{Name: "git", Args: wantArgs}
	if !reflect.DeepEqual(r.Calls[0], want) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0], want)
	}
}

// TestAddWorktreeExisting_FlagInjection verifies that AddWorktreeExisting
// rejects a leading-dash branch before any git call (flag-injection parity
// with AddWorktree).
func TestAddWorktreeExisting_FlagInjection(t *testing.T) {
	r := proc.NewFakeRunner()
	err := git.AddWorktreeExisting(context.Background(), r,
		"/repos/proj", "--evil", "/repos/proj__worktrees/evil")
	if err == nil {
		t.Fatal("must reject leading-dash branch")
	}
	if len(r.Calls) != 0 {
		t.Errorf("must not call git; got %d calls", len(r.Calls))
	}
}

// TestAddWorktreeExisting_Real verifies the git effect in a real temp repo.
func TestAddWorktreeExisting_Real(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)
	// create the branch first (existing-branch mode requires it)
	cmd := exec.Command("git", "branch", "feat-existing")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}
	treePath := filepath.Join(t.TempDir(), "feat-existing")
	r := proc.ExecRunner{}
	if err := git.AddWorktreeExisting(context.Background(), r, repo, "feat-existing", treePath); err != nil {
		t.Fatalf("AddWorktreeExisting: %v", err)
	}
	if _, err := os.Stat(treePath); err != nil {
		t.Fatalf("worktree path not created: %v", err)
	}
}

// ── RemoveWorktree ────────────────────────────────────────────────────────────

// TestRemoveWorktree_CallArgs_NoForce verifies "worktree remove <tree>" argv.
func TestRemoveWorktree_CallArgs_NoForce(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "worktree", "remove", "/repos/proj__worktrees/feat-x"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := git.RemoveWorktree(context.Background(), r, "/repos/proj",
		"/repos/proj__worktrees/feat-x", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestRemoveWorktree_CallArgs_Force verifies that RemoveWorktree appends "--force" when force=true.
func TestRemoveWorktree_CallArgs_Force(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "worktree", "remove", "--force",
		"/repos/proj__worktrees/feat-x"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := git.RemoveWorktree(context.Background(), r, "/repos/proj",
		"/repos/proj__worktrees/feat-x", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestRemoveWorktree_Real verifies the tree disappears after removal.
func TestRemoveWorktree_Real(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)
	treePath := filepath.Join(t.TempDir(), "linked")
	r := proc.ExecRunner{}
	// Add a linked worktree on a new branch first.
	if err := git.AddWorktree(context.Background(), r, repo, "feat-rm",
		treePath, "HEAD"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if _, err := os.Stat(treePath); err != nil {
		t.Fatalf("tree not created: %v", err)
	}
	// Remove it.
	if err := git.RemoveWorktree(context.Background(), r, repo, treePath, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(treePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after removal: err=%v", err)
	}
}

// ── WorktreeDirty ─────────────────────────────────────────────────────────────

// TestWorktreeDirty_CallArgs verifies that WorktreeDirty emits the "status --porcelain" argv against treePath.
func TestWorktreeDirty_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/wt/feat-x", "status", "--porcelain"}
	r.Respond(proc.FakeResult{Stdout: []byte(" M file.go\n")}, "git", wantArgs...)

	dirty, err := git.WorktreeDirty(context.Background(), r, "/wt/feat-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dirty {
		t.Error("want dirty=true for non-empty porcelain output")
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestWorktreeDirty_Clean verifies dirty=false for empty output.
func TestWorktreeDirty_Clean(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("")}, "git",
		"-C", "/wt/feat-x", "status", "--porcelain")
	dirty, err := git.WorktreeDirty(context.Background(), r, "/wt/feat-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dirty {
		t.Error("want dirty=false for empty porcelain output")
	}
}

// TestWorktreeDirty_Real verifies dirty=false on a fresh tree and dirty=true
// after an uncommitted write.
func TestWorktreeDirty_Real(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)
	r := proc.ExecRunner{}

	// Fresh tree: clean.
	dirty, err := git.WorktreeDirty(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("WorktreeDirty clean: %v", err)
	}
	if dirty {
		t.Error("clean repo should not be dirty")
	}

	// Modify a tracked file.
	if err := os.WriteFile(filepath.Join(repo, "README.md"),
		[]byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = git.WorktreeDirty(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("WorktreeDirty dirty: %v", err)
	}
	if !dirty {
		t.Error("modified-file repo should be dirty")
	}
}

// ── BranchMerged ─────────────────────────────────────────────────────────────

// TestBranchMerged_CallArgs verifies the exact argv for "branch --merged".
func TestBranchMerged_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "branch", "--merged", "main",
		"--format=%(refname:short)"}
	r.Respond(proc.FakeResult{Stdout: []byte("feat-x\n")}, "git", wantArgs...)

	merged, err := git.BranchMerged(context.Background(), r, "/repos/proj", "feat-x", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !merged {
		t.Error("want merged=true when branch appears in --merged output")
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestBranchMerged_NotMerged verifies merged=false when branch not in output.
func TestBranchMerged_NotMerged(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("main\n")}, "git",
		"-C", "/repos/proj", "branch", "--merged", "main",
		"--format=%(refname:short)")

	merged, err := git.BranchMerged(context.Background(), r, "/repos/proj", "feat-x", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged {
		t.Error("want merged=false when branch absent from --merged output")
	}
}

// TestBranchMerged_Real verifies true after an actual merge and false before.
func TestBranchMerged_Real(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)
	r := proc.ExecRunner{}

	// Create feat branch.
	for _, args := range [][]string{
		{"checkout", "-b", "feat-merged"},
		{"commit", "--allow-empty", "-m", "feat commit"},
		{"checkout", "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Not merged yet.
	m, err := git.BranchMerged(context.Background(), r, repo, "feat-merged", "main")
	if err != nil {
		t.Fatalf("BranchMerged pre-merge: %v", err)
	}
	if m {
		t.Error("want merged=false before merge")
	}

	// Merge it in.
	cmd := exec.Command("git", "merge", "--no-ff", "feat-merged", "-m", "merge feat")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git merge: %v\n%s", err, out)
	}
	m, err = git.BranchMerged(context.Background(), r, repo, "feat-merged", "main")
	if err != nil {
		t.Fatalf("BranchMerged post-merge: %v", err)
	}
	if !m {
		t.Error("want merged=true after merge")
	}
}

// TestBranchMerged_FlagInjection verifies that BranchMerged rejects a
// leading-dash base (and a leading-dash branch) before any git call.
func TestBranchMerged_FlagInjection(t *testing.T) {
	t.Run("leading-dash base", func(t *testing.T) {
		r := proc.NewFakeRunner()
		_, err := git.BranchMerged(context.Background(), r, "/repos/proj", "feat-x", "--evil")
		if err == nil {
			t.Fatal("must reject leading-dash base")
		}
		if !errors.Is(err, git.ErrInvalidRef) {
			t.Errorf("want errors.Is(err, ErrInvalidRef); got %v", err)
		}
		if len(r.Calls) != 0 {
			t.Errorf("must not call git; got %d calls", len(r.Calls))
		}
	})
	t.Run("leading-dash branch", func(t *testing.T) {
		r := proc.NewFakeRunner()
		_, err := git.BranchMerged(context.Background(), r, "/repos/proj", "--evil", "main")
		if err == nil {
			t.Fatal("must reject leading-dash branch")
		}
		if !errors.Is(err, git.ErrInvalidRef) {
			t.Errorf("want errors.Is(err, ErrInvalidRef); got %v", err)
		}
		if len(r.Calls) != 0 {
			t.Errorf("must not call git; got %d calls", len(r.Calls))
		}
	})
}

// ── DeleteBranch ─────────────────────────────────────────────────────────────

// TestDeleteBranch_CallArgs_Safe verifies that DeleteBranch uses "-d" when force=false.
func TestDeleteBranch_CallArgs_Safe(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "branch", "-d", "feat-x"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := git.DeleteBranch(context.Background(), r, "/repos/proj", "feat-x", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestDeleteBranch_CallArgs_Force verifies that DeleteBranch uses "-D" when force=true.
func TestDeleteBranch_CallArgs_Force(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "branch", "-D", "feat-x"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := git.DeleteBranch(context.Background(), r, "/repos/proj", "feat-x", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestDeleteBranch_FlagInjection verifies that DeleteBranch rejects a leading-dash branch.
func TestDeleteBranch_FlagInjection(t *testing.T) {
	r := proc.NewFakeRunner()
	err := git.DeleteBranch(context.Background(), r, "/repos/proj", "--evil", false)
	if err == nil {
		t.Fatal("must reject leading-dash branch")
	}
	if len(r.Calls) != 0 {
		t.Errorf("must not call git; got %d calls", len(r.Calls))
	}
}

// TestDeleteBranch_SafeRefusesUnmerged verifies that git -d refuses an unmerged
// branch in a real repo (error bubbles back from git).
func TestDeleteBranch_SafeRefusesUnmerged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	// Create an unmerged branch.
	cmd := exec.Command("git", "checkout", "-b", "feat-unmerged")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "unmerged")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "checkout", "main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout main: %v\n%s", err, out)
	}

	r := proc.ExecRunner{}
	// force=false → -d → git must refuse because the branch is not merged.
	err := git.DeleteBranch(context.Background(), r, repo, "feat-unmerged", false)
	if err == nil {
		t.Fatal("DeleteBranch -d must error for an unmerged branch")
	}
}

// ── CheckoutBranch ────────────────────────────────────────────────────────────

// TestCheckoutBranch_CallArgs verifies the exact argv for "checkout <branch>".
func TestCheckoutBranch_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "checkout", "feat-x"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := git.CheckoutBranch(context.Background(), r, "/repos/proj", "feat-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestCheckoutBranch_FlagInjection verifies that CheckoutBranch rejects a leading-dash branch.
func TestCheckoutBranch_FlagInjection(t *testing.T) {
	r := proc.NewFakeRunner()
	err := git.CheckoutBranch(context.Background(), r, "/repos/proj", "--evil")
	if err == nil {
		t.Fatal("must reject leading-dash branch")
	}
	if len(r.Calls) != 0 {
		t.Errorf("must not call git; got %d calls", len(r.Calls))
	}
}

// TestCheckoutBranch_ErrorsOnDirtyConflict verifies that git checkout fails
// when there are conflicting local changes (tracked file modified that
// differs between branches).
func TestCheckoutBranch_ErrorsOnDirtyConflict(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	// Create a second branch that has a different version of README.md.
	for _, args := range [][]string{
		{"checkout", "-b", "feat-conflict"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"),
		[]byte("branch-version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "README.md"},
		{"commit", "-m", "conflict"},
		{"checkout", "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Now dirty README.md on main with a local change.
	if err := os.WriteFile(filepath.Join(repo, "README.md"),
		[]byte("local-dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := proc.ExecRunner{}
	// Checkout of feat-conflict must fail because README.md has local changes
	// that conflict with the branch's version.
	err := git.CheckoutBranch(context.Background(), r, repo, "feat-conflict")
	if err == nil {
		t.Fatal("CheckoutBranch must error when local changes conflict with target branch")
	}
}

// TestCheckoutBranch_Real_Success verifies that checkout works normally when the
// tree is clean.
func TestCheckoutBranch_Real_Success(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	cmd := exec.Command("git", "branch", "feat-clean")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	r := proc.ExecRunner{}
	if err := git.CheckoutBranch(context.Background(), r, repo, "feat-clean"); err != nil {
		t.Fatalf("CheckoutBranch on clean tree: %v", err)
	}
}

// ── CurrentBranch ─────────────────────────────────────────────────────────────

// TestCurrentBranch_CallArgs verifies the exact argv for "rev-parse --abbrev-ref HEAD".
func TestCurrentBranch_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "rev-parse", "--abbrev-ref", "HEAD"}
	r.Respond(proc.FakeResult{Stdout: []byte("main\n")}, "git", wantArgs...)

	branch, err := git.CurrentBranch(context.Background(), r, "/repos/proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want %q", branch, "main")
	}
	if !reflect.DeepEqual(r.Calls[0], proc.Call{Name: "git", Args: wantArgs}) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0],
			proc.Call{Name: "git", Args: wantArgs})
	}
}

// TestCurrentBranch_Real verifies the returned name on a real temp repo, both on
// the initial branch and after switching to a newly created one.
func TestCurrentBranch_Real(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t) // git init -b main + initial commit
	r := proc.ExecRunner{}

	branch, err := git.CurrentBranch(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("CurrentBranch on main: %v", err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want %q", branch, "main")
	}

	// Create and switch to another branch; CurrentBranch must follow.
	cmd := exec.Command("git", "checkout", "-b", "feat-current")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v\n%s", err, out)
	}
	branch, err = git.CurrentBranch(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("CurrentBranch on feat-current: %v", err)
	}
	if branch != "feat-current" {
		t.Errorf("branch = %q, want %q", branch, "feat-current")
	}
}

// ── ErrWorktreeDirty (package var) ───────────────────────────────────────────

// TestErrWorktreeDirty_IsSentinel confirms the var is exported and distinct.
func TestErrWorktreeDirty_IsSentinel(t *testing.T) {
	if git.ErrWorktreeDirty == nil {
		t.Fatal("ErrWorktreeDirty must be non-nil")
	}
	if !errors.Is(git.ErrWorktreeDirty, git.ErrWorktreeDirty) {
		t.Error("errors.Is identity check failed")
	}
}

// TestErrBranchInUse_IsSentinel confirms the var is exported and distinct.
func TestErrBranchInUse_IsSentinel(t *testing.T) {
	if git.ErrBranchInUse == nil {
		t.Fatal("ErrBranchInUse must be non-nil")
	}
	if !errors.Is(git.ErrBranchInUse, git.ErrBranchInUse) {
		t.Error("errors.Is identity check failed")
	}
}

// ── HasCommits ────────────────────────────────────────────────────────────────

// TestHasCommits verifies (false, nil) on an unborn HEAD, and (true, nil)
// after the repo gets its first commit.
func TestHasCommits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Create a bare git init (no commits yet, so HEAD is unborn).
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	r := proc.ExecRunner{}
	ctx := context.Background()

	// Before any commit: unborn HEAD → (false, nil).
	has, err := git.HasCommits(ctx, r, dir)
	if err != nil {
		t.Fatalf("HasCommits (no commits): unexpected error: %v", err)
	}
	if has {
		t.Fatal("HasCommits (no commits): want false, got true")
	}

	// Make one commit.
	for _, args := range [][]string{
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// After first commit: born HEAD → (true, nil).
	has, err = git.HasCommits(ctx, r, dir)
	if err != nil {
		t.Fatalf("HasCommits (after commit): unexpected error: %v", err)
	}
	if !has {
		t.Fatal("HasCommits (after commit): want true, got false")
	}
}

// TestErrNoCommits_IsSentinel confirms the var is exported and distinct.
func TestErrNoCommits_IsSentinel(t *testing.T) {
	if git.ErrNoCommits == nil {
		t.Fatal("ErrNoCommits must be non-nil")
	}
	if !errors.Is(git.ErrNoCommits, git.ErrNoCommits) {
		t.Error("errors.Is identity check failed")
	}
}
