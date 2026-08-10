package discover

import (
	"context"
	"path/filepath"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/model"
	"github.com/miniature-pug/perch/internal/proc"
)

// ProjectTrees bundles a discovered git project with its worktrees.
type ProjectTrees struct {
	Project model.Project
	Trees   []model.Tree
}

// Projects discovers git projects under root and returns them with their
// worktrees. Projects orders the result by frecency; with no stats (cold
// start), it orders the result alphabetically.
//
// Projects propagates hard errors from Scan. Projects silently skips
// per-repository errors from ListWorktrees, so one bad repository does not
// abort the whole listing. When duplicate candidates share the same
// canonical main-worktree path, Projects keeps only the first one.
//
// Projects never mutates stats. Pass an empty map for cold-start
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
	// The value is a pointer, so git.ToTrees gets a stable address for Project.
	// This avoids loop-variable aliasing, even under pre-Go-1.22 semantics.
	byPath := make(map[string]*ProjectTrees, len(paths))

	for _, candidate := range paths {
		wts, err := git.ListWorktrees(ctx, r, candidate)
		if err != nil {
			// Skip this candidate. One bad repo must not abort the whole listing.
			continue
		}

		main, ok := git.MainWorktree(wts)
		if !ok {
			// This is a bare-only repository. It has no working checkout to open.
			continue
		}

		// Dedup: keep only the first candidate whose main path maps here.
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
