// internal/git/branches.go
package git

import (
	"context"
	"fmt"
	"sort"
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

// Branches returns all local branch names in repo (bare names, no refs/heads/
// prefix), ordered so the branch a caller most likely wants comes FIRST: the
// currently checked-out branch, else the repo's default branch (origin/HEAD, then
// a main/master fallback). The remaining branches follow in alphabetical order.
//
// The pin-first ordering matters because callers that need a single sensible
// default (e.g. the New Session dialog's base-ref field) take branches[0]; without
// it they would pick whatever sorted alphabetically first ("aardvark" over "main").
func Branches(ctx context.Context, r proc.Runner, repo string) ([]string, error) {
	out, errOut, err := r.Run(ctx, "git", "-C", repo, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch: %w: %s", err, strings.TrimSpace(string(errOut)))
	}
	branches := make([]string, 0)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	if len(branches) < 2 {
		return branches, nil
	}
	pin := pinnedFirstBranch(ctx, r, repo, branches)
	sortBranchesPinnedFirst(branches, pin)
	return branches, nil
}

// pinnedFirstBranch picks the branch to sort first: the current branch when HEAD
// points at one of the listed branches, else the repo's default branch resolved
// from origin/HEAD, else a conventional "main"/"master" fallback. Returns "" when
// none of these is present in branches (leaving a pure alphabetical order).
//
// Each detection git call is best-effort: a failure (detached HEAD, no origin
// remote, missing origin/HEAD ref) simply falls through to the next strategy.
func pinnedFirstBranch(ctx context.Context, r proc.Runner, repo string, branches []string) string {
	present := func(name string) bool {
		for _, b := range branches {
			if b == name {
				return true
			}
		}
		return false
	}

	// 1. Current branch. `symbolic-ref --short HEAD` prints the short branch name
	//    and fails (non-zero) on a detached HEAD, so a clean empty/err → not on a branch.
	if out, _, err := r.Run(ctx, "git", "-C", repo, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		if cur := strings.TrimSpace(string(out)); cur != "" && present(cur) {
			return cur
		}
	}
	// 2. Default branch via origin/HEAD → "origin/<name>"; strip the remote prefix.
	if out, _, err := r.Run(ctx, "git", "-C", repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		d := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
		if d != "" && present(d) {
			return d
		}
	}
	// 3. Conventional default names, in order of preference.
	for _, name := range []string{"main", "master"} {
		if present(name) {
			return name
		}
	}
	return ""
}

// sortBranchesPinnedFirst sorts branches in place: pin (when non-empty and
// present) first, then every other branch alphabetically. Stable so the ordering
// is deterministic across calls.
func sortBranchesPinnedFirst(branches []string, pin string) {
	sort.SliceStable(branches, func(i, j int) bool {
		if branches[i] == pin {
			return branches[j] != pin
		}
		if branches[j] == pin {
			return false
		}
		return branches[i] < branches[j]
	})
}
