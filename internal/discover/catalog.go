package discover

import (
	"context"
	"path/filepath"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
)

// ProjectTrees bundles a discovered git project with its working trees.
type ProjectTrees struct {
	Project model.Project
	Trees   []model.Tree
}

// Projects discovers git projects under root and returns them with their
// worktrees, ordered by frecency (cold start / no stats → alphabetical).
//
// Hard errors from Scan are propagated. Per-repository errors from
// ListWorktrees are silently skipped (one bad repo must not abort the listing).
// Duplicate candidates that share the same canonical main-worktree path are
// deduplicated; first writer wins.
//
// stats is read-only; it is never mutated. Pass an empty map for cold-start
// (alphabetical) ordering.
func Projects(
	ctx context.Context,
	r proc.Runner,
	root string,
	opts Options,
	stats map[string]ProjectStat,
	now int64,
) ([]*ProjectTrees, error) {
	paths, err := Scan(root, opts)
	if err != nil {
		return nil, err
	}

	// byPath deduplicates candidates by canonical main-worktree path.
	// The value is a pointer so that git.ToTrees receives a stable address for
	// Project — no loop-variable aliasing even with pre-Go-1.22 semantics.
	byPath := make(map[string]*ProjectTrees, len(paths))

	for _, candidate := range paths {
		wts, err := git.ListWorktrees(ctx, r, candidate)
		if err != nil {
			// Skip this candidate; one bad repo must not abort the whole listing.
			continue
		}

		main, ok := git.MainWorktree(wts)
		if !ok {
			// Bare-only repository — no working checkout to open.
			continue
		}

		// Dedup: only the first candidate whose main path maps here is kept.
		if _, seen := byPath[main.Path]; seen {
			continue
		}

		pt := &ProjectTrees{
			Project: model.Project{
				Path: main.Path,
				Name: filepath.Base(main.Path),
			},
		}
		// Pass &pt.Project so every Tree.Project pointer is stable (points into
		// the heap-allocated ProjectTrees, not a stack copy).
		pt.Trees = git.ToTrees(wts, &pt.Project)
		byPath[main.Path] = pt
	}

	// Build a stats copy restricted to discovered paths so that SortedPaths
	// returns exactly the right set in frecency order.
	discovered := make(map[string]ProjectStat, len(byPath))
	for p := range byPath {
		if s, ok := stats[p]; ok {
			discovered[p] = s
		} else {
			discovered[p] = ProjectStat{} // zero → alphabetical tiebreak
		}
	}

	ordered := SortedPaths(discovered, now)

	result := make([]*ProjectTrees, 0, len(ordered))
	for _, p := range ordered {
		result = append(result, byPath[p])
	}
	return result, nil
}
