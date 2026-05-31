// Package worktree handles file seeding and hook execution for freshly-created
// git linked worktrees. All shell-outs go through proc.Runner (§20.1); all FS
// operations use the os package directly.
package worktree

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Miniature-Pug/perch/internal/config"
)

// Seed copies and symlinks the files described by files into the freshly-created
// worktree at treePath, using repoRoot as the source base.
//
// Security gate (plan §7.1): before any filesystem mutation, every post-glob
// match is validated to reside strictly inside repoRoot. A match that resolves
// outside the repo (e.g. via a symlink in the repo, or ".." in the glob) is a
// hard error — Seed returns immediately and performs NO copy or symlink. The
// entire match set is validated first so that a late bad match cannot leave a
// half-seeded tree.
func Seed(repoRoot, treePath string, files config.Files) error {
	// Canonicalize repoRoot so EvalSymlinks-resolved match paths can be compared
	// against the same reference frame. This prevents false-rejects when the
	// repo lives under a symlinked ancestor (e.g. ~/repos -> /data/repos).
	canonRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return fmt.Errorf("worktree: seed: resolve repoRoot %q: %w", repoRoot, err)
	}

	// Expand all globs first, before any mutation, so we can validate the full
	// match set in one pass. Two separate slices preserve Copy vs Symlink semantics.
	copyMatches, err := expandGlobs(repoRoot, files.Copy)
	if err != nil {
		return fmt.Errorf("worktree: seed: expand copy globs: %w", err)
	}
	symlinkMatches, err := expandGlobs(repoRoot, files.Symlink)
	if err != nil {
		return fmt.Errorf("worktree: seed: expand symlink globs: %w", err)
	}

	// Pass 1: validate the full match set before any mutation.
	// EvalSymlinks is resolved ONCE here and the result is carried forward into
	// pass 2; reusing the resolved path closes the TOCTOU window where a
	// symlink could be repointed between validation and the copy/symlink call.
	// Order matters: verify ALL matches first, THEN mutate — a bad entry at the
	// end must not leave a partially-seeded worktree.
	resolvedCopy := make([]string, len(copyMatches))
	for i, m := range copyMatches {
		r, err := validateInside(canonRoot, m)
		if err != nil {
			return err
		}
		resolvedCopy[i] = r
	}
	resolvedSymlink := make([]string, len(symlinkMatches))
	for i, m := range symlinkMatches {
		r, err := validateInside(canonRoot, m)
		if err != nil {
			return err
		}
		resolvedSymlink[i] = r
	}

	// Pass 2: mutate. Copy first, then symlinks.
	// Source paths use the validated resolved paths from pass 1; destination
	// subpaths are computed from the original match so placement/naming is unchanged.
	for i, m := range copyMatches {
		rel, err := filepath.Rel(repoRoot, m)
		if err != nil {
			return fmt.Errorf("worktree: seed: rel path for %q: %w", m, err)
		}
		dst := filepath.Join(treePath, rel)
		if err := copyPath(resolvedCopy[i], dst); err != nil {
			return fmt.Errorf("worktree: seed: copy %q: %w", rel, err)
		}
	}
	for i, m := range symlinkMatches {
		rel, err := filepath.Rel(repoRoot, m)
		if err != nil {
			return fmt.Errorf("worktree: seed: rel path for %q: %w", m, err)
		}
		linkPath := filepath.Join(treePath, rel)
		if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
			return fmt.Errorf("worktree: seed: mkdirall for symlink %q: %w", rel, err)
		}
		target, err := relSymlinkTarget(linkPath, resolvedSymlink[i])
		if err != nil {
			return fmt.Errorf("worktree: seed: symlink target for %q: %w", rel, err)
		}
		if err := os.Symlink(target, linkPath); err != nil {
			return fmt.Errorf("worktree: seed: symlink %q: %w", rel, err)
		}
	}
	return nil
}

// expandGlobs expands a slice of glob patterns relative to base, returning the
// flattened list of matching absolute paths. A pattern with zero matches is
// silently skipped — config may list optional files.
func expandGlobs(base string, patterns []string) ([]string, error) {
	var matches []string
	for _, pat := range patterns {
		abs := filepath.Join(base, pat)
		ms, err := filepath.Glob(abs)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", pat, err)
		}
		matches = append(matches, ms...)
	}
	return matches, nil
}

// validateInside checks that match (an absolute post-glob path) resides inside
// canonRoot (already EvalSymlinks-resolved). It resolves symlinks in match so
// that a symlink pointing outside the repo is caught before any mutation.
// It returns the resolved absolute path so pass 2 can operate on it directly,
// closing the TOCTOU window between validation and mutation.
func validateInside(canonRoot, match string) (string, error) {
	resolved, err := filepath.EvalSymlinks(match)
	if err != nil {
		return "", fmt.Errorf("worktree: seed: resolve match %q: %w", match, err)
	}
	ok, err := insideRepo(canonRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("worktree: seed: check match %q: %w", match, err)
	}
	if !ok {
		return "", fmt.Errorf("worktree: seed: match %q resolves outside repo root %q", match, canonRoot)
	}
	return resolved, nil
}

// insideRepo reports whether abs is strictly inside (or equal to) repoRoot.
// Both paths must already be clean and absolute. Uses filepath.Rel: if the
// relative path starts with ".." the match escapes the root.
func insideRepo(repoRoot, abs string) (bool, error) {
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// copyPath copies src into dst. If src is a directory, it walks the tree and
// copies only regular files — v1 deliberately skips symlinks and irregular
// files inside matched directories to avoid nested escape paths without
// per-file re-validation.
func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Only copy regular files; skip symlinks and other irregular types
			// to avoid unvalidated escape paths inside a matched directory.
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			return copyFile(path, filepath.Join(dst, rel))
		})
	}
	return copyFile(src, dst)
}

// copyFile copies src to dst, preserving the file mode. It creates parent
// directories as needed.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	// in is read-only; its Close error carries no write-flush risk, so we log
	// it only implicitly via the deferred close. Out is closed explicitly below
	// so its flush error is captured.
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Explicit Chmod after write because O_CREATE is subject to umask; this
	// ensures the destination mode matches the source exactly.
	return os.Chmod(dst, info.Mode().Perm())
}

// relSymlinkTarget computes a relative symlink target from linkPath to target
// so the symlink survives the worktree being moved (plan §7.1).
func relSymlinkTarget(linkPath, target string) (string, error) {
	rel, err := filepath.Rel(filepath.Dir(linkPath), target)
	if err != nil {
		return "", err
	}
	return rel, nil
}
