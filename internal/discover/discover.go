// Package discover finds git repository candidate directories under a root.
//
// This package is the first stage of the perch discovery pipeline. It walks
// the filesystem and returns paths of directories that contain a ".git"
// entry: a directory for normal repos, or a regular file for linked
// worktrees and submodules.
//
// Package discover does not build domain objects, resolve worktrees, or
// enumerate branches. The integration layer that reads this output handles
// those steps.
package discover

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMaxDepth is the maximum directory depth Scan descends when Options.MaxDepth <= 0.
// Root itself is depth 0; a direct child of root is depth 1.
const DefaultMaxDepth = 8

// DefaultPrune is the set of directory base names Scan skips.
// Scan excludes heavy build-artifact and dependency directories by default
// because they are never top-level git repos and can be huge.
var DefaultPrune = []string{"node_modules", "vendor", ".git"}

// Options controls a Scan.
type Options struct {
	// MaxDepth is the maximum directory depth below root that Scan descends.
	// Root itself is depth 0. A direct child of root is depth 1.
	// Scan considers a candidate (a directory that contains ".git") only when
	// the candidate's depth is <= MaxDepth. In other words, Scan requires
	// depth(parent of .git) <= MaxDepth.
	// Scan treats a value <= 0 as the default depth of 8.
	MaxDepth int

	// Prune lists directory base names that Scan never enters.
	// A nil slice uses DefaultPrune. A non-nil, empty slice turns off all pruning.
	Prune []string
}

// Scan walks the filesystem rooted at root. Scan returns the absolute paths
// of directories that contain a child named ".git": a directory for a normal
// repo, or a regular file for a linked worktree or submodule. Scan never
// enters hidden (dot-prefixed) directories, so it never returns tool or
// config caches such as ~/.pyenv or ~/.npm as candidates.
//
// Behavior summary:
//   - root must exist. If root does not exist, Scan returns an error.
//   - Scan never follows symlinks (filepath.WalkDir does not follow
//     directory symlinks, which also prevents infinite cycles).
//   - Finding a repo does NOT stop the descent. Scan still finds nested,
//     independent repos.
//   - A read error on one entry skips only that subtree. It does not abort
//     the whole scan.
//   - Scan returns results in the lexical order that filepath.WalkDir
//     produces. The caller must do any further sorting or deduplication.
func Scan(root string, opts Options) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("discover: resolve root %s: %w", root, err)
	}

	// Fail fast when root does not exist, so callers get a clear error
	// instead of a silent empty result.
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("discover: root %s: %w", abs, err)
	}

	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}

	prune := opts.Prune
	if prune == nil {
		prune = DefaultPrune
	}
	pruneSet := make(map[string]bool, len(prune))
	for _, p := range prune {
		pruneSet[p] = true
	}

	var results []string

	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, werr error) error {
		// Per-entry error: skip this entry or subtree, but continue the walk.
		if werr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		name := d.Name()

		// Detect a ".git" entry. The containing directory is the candidate, so
		// Scan measures depth from the parent directory. Scan checks this branch
		// before any prune logic, so ".git" in DefaultPrune never hides detection.
		// Scan never descends into .git, but always detects .git when it visits.
		if name == ".git" && path != abs {
			parent := filepath.Dir(path)
			rel, relErr := filepath.Rel(abs, parent)
			if relErr == nil {
				depth := depthOf(rel)
				if depth <= maxDepth {
					results = append(results, parent)
				}
			}
			// Skip the contents of a .git directory. For a .git file, just continue.
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		// Scan always enters root.
		if path == abs {
			return nil
		}

		// Only directories need more checks.
		if !d.IsDir() {
			return nil
		}

		// Depth gate: Scan stops descending into directories that are too deep.
		rel, relErr := filepath.Rel(abs, path)
		if relErr != nil {
			return fs.SkipDir
		}
		depth := depthOf(rel)
		if depth > maxDepth {
			return fs.SkipDir
		}

		// Hidden-directory gate: never descend into dot-prefixed directories.
		// Tool and config caches such as ~/.pyenv, ~/.npm, and ~/.cache often
		// contain their own .git entry, but they are never the user's project repos.
		// The scan root itself is exempt from this gate (see above), so Scan still
		// scans a root that is itself a hidden directory.
		if strings.HasPrefix(name, ".") {
			return fs.SkipDir
		}

		// Prune gate: never descend into directories on the prune list.
		if pruneSet[name] {
			return fs.SkipDir
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover: walk %s: %w", abs, err)
	}

	return results, nil
}

// depthOf returns the number of path segments in a relative path.
// "."  → 0  (rel path equal to root)
// "a"  → 1
// "a/b" → 2
func depthOf(rel string) int {
	if rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}
