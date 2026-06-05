// Package git — worktree.go contains the functions that create and resolve
// paths for git linked worktrees. All git shell-outs go through proc.Runner so
// that callers can inject a FakeRunner in unit tests (§20.1).
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// worktreeDirSuffix is appended to the project name to form the sibling
// directory that holds all linked worktrees when no custom worktree_dir is set.
const worktreeDirSuffix = "__worktrees"

// ErrBranchExists is returned (wrapped) by AddWorktree when the requested
// branch already exists in the repository.
var ErrBranchExists = errors.New("git: worktree branch already exists")

// SlugifyBranch converts a git branch name into a filesystem-safe handle.
// Rules: keep [A-Za-z0-9._-], map '/' and any other rune to '-', collapse
// consecutive '-' runs to one, trim leading/trailing '-', lowercase the result.
// An empty or fully-stripped result becomes "worktree".
//
// This slugifier targets git-branch → filesystem constraints. Do not conflate
// it with any pane-title sanitizer, which has different allowed character sets.
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
			// '/' and every other rune become '-'; collapse consecutive runs.
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

// AddWorktree runs `git -C <repoRoot> worktree add -b <branch> <path> <base>`.
// The caller supplies base already resolved (e.g. "HEAD" or a branch name) —
// this function does not default it.
//
// Security (V3-A): branch and base are validated with ValidRef before any argv
// is built. git worktree add does not support a trailing "--" before the
// committish positional (unlike "git checkout -- <path>"), so strict validation
// is the correct mitigation: both values are rejected if they begin with '-',
// contain "..", or contain other git check-ref-format-forbidden characters that
// could cause flag injection. path is always absolute (callers use WorktreePath).
//
// If the command fails and stderr indicates the branch already exists, the
// returned error wraps ErrBranchExists so callers can use errors.Is. Other
// failures wrap stderr verbatim.
func AddWorktree(ctx context.Context, r proc.Runner, repoRoot, branch, path, base string) error {
	// Validate branch and base before constructing any git argv.
	if err := ValidRef(branch); err != nil {
		return fmt.Errorf("git: AddWorktree: invalid branch: %w: %w", ErrInvalidRef, err)
	}
	if err := ValidRef(base); err != nil {
		return fmt.Errorf("git: AddWorktree: invalid base: %w: %w", ErrInvalidRef, err)
	}

	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "worktree", "add", "-b", branch, path, base)
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if strings.Contains(msg, "already exists") {
			return fmt.Errorf("git: worktree add %s: %w (stderr: %s)", repoRoot, ErrBranchExists, msg)
		}
		if len(msg) > 0 {
			return fmt.Errorf("git: worktree add %s: %w (stderr: %s)", repoRoot, err, msg)
		}
		return fmt.Errorf("git: worktree add %s: %w", repoRoot, err)
	}
	return nil
}
