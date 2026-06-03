// internal/git/branches.go
package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// WorktreeInfo is a summary of one git worktree from `git worktree list --porcelain`.
// JSON tags are frozen — do not rename.
type WorktreeInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

// Branches returns all local branch names in repo (bare names, no refs/heads/ prefix).
func Branches(ctx context.Context, r proc.Runner, repo string) ([]string, error) {
	out, errOut, err := r.Run(ctx, "git", "-C", repo, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch: %w: %s", err, strings.TrimSpace(string(errOut)))
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// Worktrees returns all worktrees for repo via `git worktree list --porcelain`,
// skipping bare entries. Reuses the existing ListWorktrees logic.
func Worktrees(ctx context.Context, r proc.Runner, repo string) ([]WorktreeInfo, error) {
	wts, err := ListWorktrees(ctx, r, repo)
	if err != nil {
		return nil, err
	}
	var out []WorktreeInfo
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		out = append(out, WorktreeInfo{
			Path:   wt.Path,
			Branch: wt.Branch,
			Head:   wt.Head,
		})
	}
	return out, nil
}
