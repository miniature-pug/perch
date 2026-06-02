// Package discover finds git repository candidate directories under a root.
//
// It is the first stage of the perch discovery pipeline: it walks the
// filesystem and returns paths of directories that contain a ".git" entry
// (directory for normal repos, regular file for linked worktrees / submodules).
// It does not build domain objects, resolve worktrees, or enumerate branches;
// those steps are handled by the integration layer that reads this output.
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

// DefaultPrune is the set of directory base names skipped during a scan.
// Heavy build-artifact and dependency directories are excluded by default
// because they are never top-level git repos and can be enormous.
var DefaultPrune = []string{"node_modules", "vendor", ".git"}

// Options controls a Scan.
type Options struct {
	// MaxDepth is the maximum directory depth below root that Scan descends.
	// Root itself is depth 0; a direct child of root is depth 1.
	// Scan considers a candidate (directory containing ".git") only when the
	// candidate's depth is <= MaxDepth — that is, only when depth(parent of .git) <= MaxDepth.
	// A value <= 0 is treated as the default depth of 8.
	MaxDepth int

	// Prune lists directory base names never descended into during a scan.
	// A nil slice uses DefaultPrune. A non-nil but empty slice disables all pruning.
	Prune []string
}

// Scan walks the filesystem rooted at root and returns the absolute paths of
// every directory that contains a child named ".git" (either a directory for a
// normal repo or a regular file for a linked worktree / submodule).
//
// Behaviour summary:
//   - root must exist; if it does not Scan returns an error.
//   - Symlinks are never followed (filepath.WalkDir does not follow directory
//     symlinks, which also prevents infinite cycles).
//   - Finding a repo does NOT stop the descent; nested independent repos are
//     still discovered.
//   - Per-entry read errors cause that subtree to be skipped; they do not abort
//     the whole scan.
//   - Results are returned in the lexical order produced by filepath.WalkDir.
//     The caller is responsible for any further sorting or deduplication.
func Scan(root string, opts Options) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("discover: resolve root %s: %w", root, err)
	}

	// Fail fast if root does not exist so callers receive a meaningful error
	// rather than a silent empty result.
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
		// Per-entry error: skip this entry/subtree but continue the walk.
		if werr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		name := d.Name()

		// Detect ".git" entry. The CONTAINING directory is the candidate, so
		// depth is measured by the parent directory. This branch is checked before
		// any prune logic so that ".git" in DefaultPrune never hides the detection;
		// we never descend INTO .git, but we always detect it when visited.
		if name == ".git" && path != abs {
			parent := filepath.Dir(path)
			rel, relErr := filepath.Rel(abs, parent)
			if relErr == nil {
				depth := depthOf(rel)
				if depth <= maxDepth {
					results = append(results, parent)
				}
			}
			// Skip .git contents regardless (dir) or move on (file).
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		// Root is always entered.
		if path == abs {
			return nil
		}

		// Only directories need further consideration.
		if !d.IsDir() {
			return nil
		}

		// Depth gate: stop descending directories that are too deep.
		rel, relErr := filepath.Rel(abs, path)
		if relErr != nil {
			return fs.SkipDir
		}
		depth := depthOf(rel)
		if depth > maxDepth {
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
