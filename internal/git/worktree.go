// Package git: worktree.go contains the functions that create and resolve
// paths for git linked worktrees. All git shell-outs go through proc.Runner,
// so callers can inject a FakeRunner in unit tests.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/miniature-pug/perch/internal/proc"
)

// worktreeDirSuffix is appended to the project name to form the sibling
// directory that holds all linked worktrees when no custom worktree_dir is set.
const worktreeDirSuffix = "__worktrees"

// ErrBranchExists is returned (wrapped) by AddWorktree when the requested
// branch already exists in the repository.
var ErrBranchExists = errors.New("git: worktree branch already exists")

// ErrWorktreePathExists is returned (wrapped) by AddWorktree when something
// already exists at the requested worktree path.
var ErrWorktreePathExists = errors.New("git: worktree path already exists")

// SlugifyBranch converts a git branch name into a filesystem-safe handle.
// Rules: keep [A-Za-z0-9._-], map '/' and any other rune to '-', collapse
// consecutive '-' runs to one, trim leading or trailing '-', and lowercase
// the result. An empty or fully-stripped result becomes "worktree".
//
// SlugifyBranch exists to satisfy filesystem naming constraints when it
// converts a git branch name. Do not conflate SlugifyBranch with any
// pane-title sanitizer; a pane-title sanitizer allows a different set of
// characters.
func SlugifyBranch(branch string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range branch {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			// '/' and every other rune become '-'. Collapse consecutive runs.
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "worktree"
	}
	return slug
}

// WorktreePath resolves the filesystem path for a new linked worktree.
//
//   - worktreeDir == ""       → sibling directory: <parent of projectRoot>/<base>__worktrees/<handle>
//   - worktreeDir is absolute → <worktreeDir>/<handle>
//   - worktreeDir is relative → <projectRoot>/<worktreeDir>/<handle> (cleaned)
//
// Returns an error only when projectRoot or handle is empty.
func WorktreePath(projectRoot, handle, worktreeDir string) (string, error) {
	if projectRoot == "" {
		return "", errors.New("git: WorktreePath: projectRoot must not be empty")
	}
	if handle == "" {
		return "", errors.New("git: WorktreePath: handle must not be empty")
	}

	switch {
	case worktreeDir == "":
		sibling := filepath.Base(projectRoot) + worktreeDirSuffix
		return filepath.Join(filepath.Dir(projectRoot), sibling, handle), nil
	case filepath.IsAbs(worktreeDir):
		return filepath.Join(worktreeDir, handle), nil
	default:
		return filepath.Clean(filepath.Join(projectRoot, worktreeDir, handle)), nil
	}
}

// AvailableWorktreePath returns WorktreePath(projectRoot, handle,
// worktreeDir) when nothing exists at that path yet. Otherwise it returns
// the first free path among handle-2, handle-3, and so on. SlugifyBranch is
// lossy ("feat/x", "feat-x" and "Feat-X" share one handle), so callers that
// derive the handle from a branch name should use this instead of
// WorktreePath, so that two branches never compete for one directory.
func AvailableWorktreePath(projectRoot, handle, worktreeDir string) (string, error) {
	for n := 1; ; n++ {
		h := handle
		if n > 1 {
			h = fmt.Sprintf("%s-%d", handle, n)
		}
		p, err := WorktreePath(projectRoot, h, worktreeDir)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Lstat(p); errors.Is(statErr, fs.ErrNotExist) {
			return p, nil
		}
		if n >= maxWorktreePathAttempts {
			return "", fmt.Errorf("git: no free worktree path for handle %q: %w", handle, ErrWorktreePathExists)
		}
	}
}

// maxWorktreePathAttempts bounds the AvailableWorktreePath search.
const maxWorktreePathAttempts = 100

// branchExists reports whether refs/heads/<branch> exists in repoRoot. It
// relies on the exit status of `rev-parse --verify --quiet`, not on git's
// (translatable) messages: a missing ref exits non-zero with empty stderr.
func branchExists(ctx context.Context, r proc.Runner, repoRoot, branch string) (bool, error) {
	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	if msg := string(bytes.TrimSpace(stderr)); msg != "" {
		return false, fmt.Errorf("git: rev-parse %s: %w (stderr: %s)", repoRoot, err, msg)
	}
	return false, nil
}

// addCleanupTimeout bounds the best-effort cleanup after a failed AddWorktree.
const addCleanupTimeout = 15 * time.Second

// cleanupFailedAdd undoes what a failed `worktree add -b` may have left:
// the worktree itself (a failing post-checkout hook makes git exit non-zero
// AFTER it created and checked out the worktree) and the new branch.
// AddWorktree checked that neither path nor branch existed beforehand, so
// both are ours. The cleanup runs even when ctx has already expired (a
// timeout during a slow checkout is one way to get here), under its own
// short deadline. Every step is best-effort.
func cleanupFailedAdd(ctx context.Context, r proc.Runner, repoRoot, branch, path string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), addCleanupTimeout)
	defer cancel()
	if _, err := os.Lstat(path); err == nil {
		_, _, _ = r.Run(cctx, "git", "-C", repoRoot, "worktree", "remove", "--force", path)
	}
	// When git failed before creating the branch, this fails harmlessly.
	_, _, _ = r.Run(cctx, "git", "-C", repoRoot, "branch", "-D", branch)
}

// AddWorktree runs `git -C <repoRoot> worktree add -b <branch> -- <path> <base>`.
// The caller supplies base already resolved (e.g. "HEAD" or a branch name).
// AddWorktree does not default it.
//
// Security: AddWorktree validates branch and base with ValidRef before it
// builds any argv. ValidRef rejects both values if they begin with '-',
// contain "..", or contain another character that git check-ref-format
// forbids. The "--" before the positionals is a second guard against flag
// injection. path is always absolute (callers use WorktreePath).
//
// Before running git, AddWorktree checks that nothing exists at path
// (otherwise it returns an error wrapping ErrWorktreePathExists) and that
// the branch does not exist yet (otherwise ErrBranchExists). Both checks
// are exact, so a path collision is never reported as an existing branch.
// git creates the branch before the worktree, and a failing post-checkout
// hook makes it fail after the worktree exists too. So when worktree add
// fails, AddWorktree removes any worktree it created at path and deletes
// the branch (see cleanupFailedAdd), even if ctx has expired. Other
// failures wrap stderr verbatim.
func AddWorktree(ctx context.Context, r proc.Runner, repoRoot, branch, path, base string) error {
	// Validate branch and base before constructing any git argv.
	if err := ValidRef(branch); err != nil {
		return fmt.Errorf("git: AddWorktree: invalid branch: %w: %w", ErrInvalidRef, err)
	}
	if err := ValidRef(base); err != nil {
		return fmt.Errorf("git: AddWorktree: invalid base: %w: %w", ErrInvalidRef, err)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("git: worktree add %s: %w: %s", repoRoot, ErrWorktreePathExists, path)
	}
	if exists, err := branchExists(ctx, r, repoRoot, branch); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("git: worktree add %s: %w: %s", repoRoot, ErrBranchExists, branch)
	}

	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "worktree", "add", "-b", branch, "--", path, base)
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if strings.Contains(msg, "a branch named '"+branch+"' already exists") {
			// Another process created the branch after the check above.
			return fmt.Errorf("git: worktree add %s: %w (stderr: %s)", repoRoot, ErrBranchExists, msg)
		}
		cleanupFailedAdd(ctx, r, repoRoot, branch, path)
		if len(msg) > 0 {
			return fmt.Errorf("git: worktree add %s: %w (stderr: %s)", repoRoot, err, msg)
		}
		return fmt.Errorf("git: worktree add %s: %w", repoRoot, err)
	}
	return nil
}
