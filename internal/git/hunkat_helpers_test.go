package git_test

import (
	"context"
	"fmt"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
)

// hunkAt reads Hunks for file and returns the hunk at merged index idx, the
// way the UI picks the hunk the user clicked.
func hunkAt(ctx context.Context, r proc.Runner, wt, file string, idx int) (git.Hunk, error) {
	hs, err := git.Hunks(ctx, r, wt, file)
	if err != nil {
		return git.Hunk{}, err
	}
	if idx < 0 || idx >= len(hs) {
		return git.Hunk{}, fmt.Errorf("hunk index %d out of range (%d hunks)", idx, len(hs))
	}
	return hs[idx], nil
}

// stageAt stages the hunk at merged index idx by its content id.
func stageAt(ctx context.Context, r proc.Runner, wt, file string, idx int) error {
	h, err := hunkAt(ctx, r, wt, file, idx)
	if err != nil {
		return err
	}
	return git.StageHunkChecked(ctx, r, wt, file, h.Index, h.ID)
}

// discardAt discards the hunk at merged index idx by its content id.
func discardAt(ctx context.Context, r proc.Runner, wt, file string, idx int) error {
	h, err := hunkAt(ctx, r, wt, file, idx)
	if err != nil {
		return err
	}
	return git.DiscardHunkChecked(ctx, r, wt, file, h.Index, h.ID)
}

// unstageAt unstages the staged hunk at merged index idx by its content id.
func unstageAt(ctx context.Context, r proc.Runner, wt, file string, idx int) error {
	h, err := hunkAt(ctx, r, wt, file, idx)
	if err != nil {
		return err
	}
	if !h.Staged {
		return fmt.Errorf("hunk %d is not staged", idx)
	}
	return git.UnstageHunkChecked(ctx, r, wt, file, h.Index, h.ID)
}
