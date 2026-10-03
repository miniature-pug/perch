// internal/git/worktree_ops.go
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/miniature-pug/perch/internal/proc"
)

// ErrWorktreeDirty is returned (or wrapped) when an operation needs a clean
// worktree but the working tree has uncommitted changes.
var ErrWorktreeDirty = errors.New("worktree has uncommitted changes")

// ErrBranchInUse is returned by CreateWorkspace when the requested branch is
// already checked out by a tracked perch session.
var ErrBranchInUse = errors.New("branch already checked out by a session")

// ErrNoCommits is returned (wrapped) when an operation needs a commit to exist
// but the repository has an unborn HEAD (freshly init'd, zero commits).
var ErrNoCommits = errors.New("git: repository has no commits yet")

// HasCommits reports whether repoRoot has at least one commit (a born HEAD).
// It runs `git -C <repoRoot> rev-parse --verify --quiet HEAD`. On an unborn
// HEAD, git exits non-zero with empty stdout AND empty stderr. That is the
// canonical unborn signal, and HasCommits yields (false, nil). A non-empty
// stderr (e.g. "fatal: not a git repository") is a real error. HasCommits
// wraps and returns it.
func HasCommits(ctx context.Context, r proc.Runner, repoRoot string) (bool, error) {
	stdout, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		if len(bytes.TrimSpace(stdout)) == 0 && len(bytes.TrimSpace(stderr)) == 0 {
			return false, nil // unborn HEAD
		}
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return false, fmt.Errorf("git: HasCommits: rev-parse %s: %w (stderr: %s)", repoRoot, err, msg)
		}
		return false, fmt.Errorf("git: HasCommits: rev-parse %s: %w", repoRoot, err)
	}
	return true, nil
}

// AddWorktreeExisting runs `git -C <repoRoot> worktree add -- <treePath> <branch>`.
// It checks out an existing branch into a new linked worktree (no -b; the branch
// must already exist). AddWorktreeExisting validates branch with ValidRef for
// flag-injection parity with AddWorktree.
func AddWorktreeExisting(ctx context.Context, r proc.Runner, repoRoot, branch, treePath string) error {
	if err := ValidRef(branch); err != nil {
		return fmt.Errorf("git: AddWorktreeExisting: invalid branch: %w: %w", ErrInvalidRef, err)
	}
	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "worktree", "add", "--", treePath, branch)
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return fmt.Errorf("git: worktree add existing %s: %w (stderr: %s)", repoRoot, err, msg)
		}
		return fmt.Errorf("git: worktree add existing %s: %w", repoRoot, err)
	}
	return nil
}

// RemoveWorktree runs `git -C <repoRoot> worktree remove [--force] <treePath>`.
// When force is false, git refuses if the tree has uncommitted changes.
// When force is true, removal proceeds regardless.
func RemoveWorktree(ctx context.Context, r proc.Runner, repoRoot, treePath string, force bool) error {
	args := []string{"-C", repoRoot, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, treePath)
	_, stderr, err := r.Run(ctx, "git", args...)
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return fmt.Errorf("git: worktree remove %s: %w (stderr: %s)", treePath, err, msg)
		}
		return fmt.Errorf("git: worktree remove %s: %w", treePath, err)
	}
	return nil
}

// PruneWorktrees runs `git -C <repoRoot> worktree prune`, which drops git's
// administrative records for linked worktrees whose directories no longer
// exist. Until they are pruned, git still treats the branch of a deleted
// worktree as checked out ("is already used by worktree at ..."), and
// refuses to add a new worktree at the old path ("is a missing but already
// registered worktree"). Locked worktrees are never pruned.
func PruneWorktrees(ctx context.Context, r proc.Runner, repoRoot string) error {
	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "worktree", "prune")
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return fmt.Errorf("git: worktree prune %s: %w (stderr: %s)", repoRoot, err, msg)
		}
		return fmt.Errorf("git: worktree prune %s: %w", repoRoot, err)
	}
	return nil
}

// WorktreeDirty reports whether the working tree at treePath has any uncommitted
// changes. It runs `git -C <treePath> status --porcelain`. Non-empty output means dirty.
func WorktreeDirty(ctx context.Context, r proc.Runner, treePath string) (bool, error) {
	stdout, stderr, err := r.Run(ctx, "git", "-C", treePath, "status", "--porcelain")
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return false, fmt.Errorf("git: status %s: %w (stderr: %s)", treePath, err, msg)
		}
		return false, fmt.Errorf("git: status %s: %w", treePath, err)
	}
	return len(bytes.TrimSpace(stdout)) > 0, nil
}

// BranchMerged reports whether branch has been merged into base by running
// `git -C <repoRoot> branch --merged <base> --format=%(refname:short)` and
// checking whether branch appears in the output. Using --format avoids the
// leading "* " marker on the current branch that `git branch --merged` emits
// in default format. BranchMerged validates both branch and base with
// ValidRef for flag-injection parity.
//
// %(refname:short) prints "heads/<branch>" instead of "<branch>" when a tag
// or remote-tracking ref shares the branch's name, so BranchMerged accepts
// both spellings.
func BranchMerged(ctx context.Context, r proc.Runner, repoRoot, branch, base string) (bool, error) {
	if err := ValidRef(branch); err != nil {
		return false, fmt.Errorf("git: BranchMerged: invalid branch: %w: %w", ErrInvalidRef, err)
	}
	if err := ValidRef(base); err != nil {
		return false, fmt.Errorf("git: BranchMerged: invalid base: %w: %w", ErrInvalidRef, err)
	}
	stdout, stderr, err := r.Run(ctx, "git", "-C", repoRoot,
		"branch", "--merged", base, "--format=%(refname:short)")
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return false, fmt.Errorf("git: branch --merged %s %s: %w (stderr: %s)", repoRoot, base, err, msg)
		}
		return false, fmt.Errorf("git: branch --merged %s %s: %w", repoRoot, base, err)
	}
	for _, line := range strings.Split(string(stdout), "\n") {
		if l := strings.TrimSpace(line); l == branch || l == "heads/"+branch {
			return true, nil
		}
	}
	return false, nil
}

// DeleteBranch runs `git -C <repoRoot> branch -d|-D <branch>`. When force is
// false, DeleteBranch uses git -d (git refuses to delete an unmerged
// branch). When force is true, DeleteBranch uses git -D. DeleteBranch
// validates branch with ValidRef for flag-injection parity.
func DeleteBranch(ctx context.Context, r proc.Runner, repoRoot, branch string, force bool) error {
	if err := ValidRef(branch); err != nil {
		return fmt.Errorf("git: DeleteBranch: invalid branch: %w: %w", ErrInvalidRef, err)
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "branch", flag, branch)
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return fmt.Errorf("git: branch %s %s: %w (stderr: %s)", flag, branch, err, msg)
		}
		return fmt.Errorf("git: branch %s %s: %w", flag, branch, err)
	}
	return nil
}

// CurrentBranch returns the short name of the branch checked out now in the
// repo at repoRoot. Returns the literal "HEAD" if detached. It runs
// `git -C <repoRoot> rev-parse --abbrev-ref HEAD`.
func CurrentBranch(ctx context.Context, r proc.Runner, repoRoot string) (string, error) {
	stdout, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return "", fmt.Errorf("git: CurrentBranch: rev-parse %s: %w (stderr: %s)", repoRoot, err, msg)
		}
		return "", fmt.Errorf("git: CurrentBranch: rev-parse %s: %w", repoRoot, err)
	}
	return string(bytes.TrimSpace(stdout)), nil
}

// CheckoutBranch runs `git -C <repoRoot> switch --no-guess <branch>`. git
// fails (and returns a non-zero exit) if the current working tree has
// changes that conflict with the target branch. CheckoutBranch validates
// branch with ValidRef for flag-injection parity.
//
// `git switch` only ever switches to a local branch. `git checkout <name>`
// would instead restore a FILE called <name> when no such branch exists, or
// detach HEAD at a tag or commit, and still exit 0, so the caller would
// record a branch that is not checked out. --no-guess also stops git from
// silently creating a local branch from a same-named remote branch.
func CheckoutBranch(ctx context.Context, r proc.Runner, repoRoot, branch string) error {
	if err := ValidRef(branch); err != nil {
		return fmt.Errorf("git: CheckoutBranch: invalid branch: %w: %w", ErrInvalidRef, err)
	}
	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "switch", "--no-guess", branch)
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if msg != "" {
			return fmt.Errorf("git: checkout %s: %w (stderr: %s)", branch, err, msg)
		}
		return fmt.Errorf("git: checkout %s: %w", branch, err)
	}
	return nil
}
