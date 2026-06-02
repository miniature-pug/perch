package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// Stat is a compact summary of a worktree's uncommitted diff.
type Stat struct {
	Files   int
	Added   int
	Removed int
}

// Diff returns the uncommitted unified diff for the worktree at repoRoot. The
// path is passed via `git -C <root>` argv (never a shell); --no-color keeps the
// payload renderable by the frontend.
func Diff(ctx context.Context, r proc.Runner, repoRoot string) (string, error) {
	stdout, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "diff", "--no-color")
	if err != nil {
		return "", fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return string(stdout), nil
}

// DiffStat returns the added/removed line counts and changed-file count for the
// worktree's uncommitted diff, parsed from `git diff --numstat` lines of the
// form "<added>\t<removed>\t<path>". Binary files (added/removed == "-") count
// toward Files but not the line totals.
func DiffStat(ctx context.Context, r proc.Runner, repoRoot string) (Stat, error) {
	stdout, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "diff", "--numstat")
	if err != nil {
		return Stat{}, fmt.Errorf("git diff --numstat: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	var s Stat
	for _, line := range strings.Split(strings.TrimRight(string(stdout), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		s.Files++
		if a, e := strconv.Atoi(parts[0]); e == nil {
			s.Added += a
		}
		if d, e := strconv.Atoi(parts[1]); e == nil {
			s.Removed += d
		}
	}
	return s, nil
}
