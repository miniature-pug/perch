// internal/git/worktree_ops.go
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// ForgetStaleWorktrees drops git's registration of the linked worktrees of
// repoRoot that are stale (git lists them as "prunable": the directory, or
// its .git file, is gone) AND are the one in the caller's way: the entry at
// path, or the entry that has branch checked out. An empty path or branch
// matches nothing. Locked entries are never touched.
//
// Until such a registration is dropped, git still treats its branch as
// checked out ("is already used by worktree at ...") and refuses to add a
// new worktree at its path ("is a missing but already registered
// worktree"). Unlike `git worktree prune`, this never drops the
// registration of an unrelated worktree whose directory is only
// temporarily missing (an unmounted disk, a moved tree that `git worktree
// repair` would fix).
//
// Each match is removed with `git worktree remove --force <path>`, which
// git accepts for a missing directory. When the directory exists but lacks
// its .git file, git refuses that ("validation failed"), and only that
// entry's administrative directory under $GIT_COMMON_DIR/worktrees is
// deleted; the directory and its files are left alone.
func ForgetStaleWorktrees(ctx context.Context, r proc.Runner, repoRoot, path, branch string) error {
	wts, err := ListWorktrees(ctx, r, repoRoot)
	if err != nil {
		return err
	}
	var errs []error
	for _, wt := range wts {
		if !wt.Prunable || wt.Locked {
			continue
		}
		byPath := path != "" && samePath(wt.Path, path)
		byBranch := branch != "" && wt.Branch == branch
		if !byPath && !byBranch {
			continue
		}
		if rmErr := RemoveWorktree(ctx, r, repoRoot, wt.Path, true); rmErr == nil {
			continue
		} else if admErr := removeWorktreeAdminDir(ctx, r, repoRoot, wt.Path); admErr != nil {
			errs = append(errs, fmt.Errorf("%w; %w", rmErr, admErr))
		}
	}
	return errors.Join(errs...)
}

// removeWorktreeAdminDir deletes the $GIT_COMMON_DIR/worktrees/<name>
// directory whose gitdir file points at <treePath>/.git, and nothing else.
func removeWorktreeAdminDir(ctx context.Context, r proc.Runner, repoRoot, treePath string) error {
	out, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("git: rev-parse --git-common-dir %s: %w (stderr: %s)", repoRoot, err, bytes.TrimSpace(stderr))
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoRoot, common)
	}
	admin := filepath.Join(common, "worktrees")
	entries, err := os.ReadDir(admin)
	if err != nil {
		return fmt.Errorf("git: read %s: %w", admin, err)
	}
	want := filepath.Join(treePath, ".git")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		gitdir, rerr := os.ReadFile(filepath.Join(admin, e.Name(), "gitdir"))
		if rerr != nil || !samePath(strings.TrimSpace(string(gitdir)), want) {
			continue
		}
		return os.RemoveAll(filepath.Join(admin, e.Name()))
	}
	return fmt.Errorf("git: no worktree registration for %s", treePath)
}

// samePath reports whether a and b name the same location, resolving
// symlinks in the longest existing prefix of each (the paths themselves are
// usually gone).
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	return resolveExisting(a) == resolveExisting(b)
}

// resolveExisting resolves symlinks in the deepest existing ancestor of p
// and re-appends the missing tail.
func resolveExisting(p string) string {
	p = filepath.Clean(p)
	var tail []string
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{r}, tail...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
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
