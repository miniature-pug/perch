// Package git — worktree.go contains the functions that create, remove, and
// inspect git linked worktrees. All git shell-outs go through proc.Runner so
// that callers can inject a FakeRunner in unit tests (§20.1).
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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

// ErrWorktreeDirty is returned (wrapped) by RemoveWorktree when the worktree
// cannot be safely removed because it contains modified or untracked files, or
// is locked.
var ErrWorktreeDirty = errors.New("git: worktree has modified/untracked files or is locked")

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

// dirtyPhrases are the git stderr substrings that indicate a worktree cannot
// be removed safely. Matched case-insensitively to be robust across git
// versions and locales.
//
// Verified against git 2.x:
//   - "contains modified or untracked files" — git worktree remove on a worktree with changes
//   - "locked"                               — git worktree remove on a locked worktree
//   - "is dirty"                             — older git phrasing
//   - "is a submodule"                       — submodule guard
var dirtyPhrases = []string{
	"contains modified or untracked files",
	"is dirty",
	"locked",
	"is a submodule",
}

// RemoveWorktree runs `git -C <repoRoot> worktree remove <path>`, appending
// --force when force is true.
//
// When force is false and stderr matches a known dirty/locked phrase, the
// returned error wraps ErrWorktreeDirty. Other failures wrap stderr verbatim.
func RemoveWorktree(ctx context.Context, r proc.Runner, repoRoot, path string, force bool) error {
	args := []string{"-C", repoRoot, "worktree", "remove", path}
	if force {
		args = append(args, "--force")
	}
	_, stderr, err := r.Run(ctx, "git", args...)
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		lower := strings.ToLower(msg)
		if !force {
			for _, phrase := range dirtyPhrases {
				if strings.Contains(lower, phrase) {
					return fmt.Errorf("git: worktree remove %s: %w (stderr: %s)", path, ErrWorktreeDirty, msg)
				}
			}
		}
		if len(msg) > 0 {
			return fmt.Errorf("git: worktree remove %s: %w (stderr: %s)", path, err, msg)
		}
		return fmt.Errorf("git: worktree remove %s: %w", path, err)
	}
	return nil
}

// PruneWorktrees runs `git -C <repoRoot> worktree prune`. Git prunes stale
// administrative records for worktrees whose paths no longer exist on disk.
func PruneWorktrees(ctx context.Context, r proc.Runner, repoRoot string) error {
	_, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "worktree", "prune")
	if err != nil {
		msg := string(bytes.TrimSpace(stderr))
		if len(msg) > 0 {
			return fmt.Errorf("git: worktree prune %s: %w (stderr: %s)", repoRoot, err, msg)
		}
		return fmt.Errorf("git: worktree prune %s: %w", repoRoot, err)
	}
	return nil
}

// RemoveLock removes the lock file at
// <repoRoot>/.git/worktrees/<internalName>/locked. It is a direct filesystem
// operation — the lock file is a local sentinel that does not require a git
// subprocess. os.ErrNotExist is silently ignored (already unlocked).
func RemoveLock(repoRoot, internalName string) error {
	lockPath := filepath.Join(repoRoot, ".git", "worktrees", internalName, "locked")
	err := os.Remove(lockPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("git: remove lock %s: %w", lockPath, err)
	}
	return nil
}

// InternalName resolves git's internal worktree directory name for treePath by
// reading the <treePath>/.git pointer file that git writes for linked
// worktrees. The file contains a line of the form:
//
//	gitdir: /abs/.git/worktrees/<name>
//
// The basename of that path is the internal name.
//
// No Runner is needed here: this reads a local pointer file, not a subprocess.
// The signature omits ctx and Runner intentionally.
//
// Falls back to filepath.Base(treePath) when the pointer file is absent,
// unreadable, or not in the expected format (best-effort for unusual setups).
func InternalName(treePath string) (string, error) {
	dotGit := filepath.Join(treePath, ".git")
	data, err := os.ReadFile(dotGit)
	if err != nil {
		// Unreadable or absent pointer file — best-effort fallback.
		return filepath.Base(treePath), nil //nolint:nilerr
	}

	line := strings.TrimSpace(string(data))
	// Expected: "gitdir: /abs/path/.git/worktrees/<name>"
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return filepath.Base(treePath), nil
	}
	gitdirPath := strings.TrimPrefix(line, prefix)
	// Strip any trailing "/.git" suffix (defensive; not observed in practice).
	gitdirPath = strings.TrimSuffix(gitdirPath, "/.git")
	name := filepath.Base(gitdirPath)
	if name == "" || name == "." {
		return filepath.Base(treePath), nil
	}
	return name, nil
}
