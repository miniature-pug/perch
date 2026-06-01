package tmux

import (
	"path/filepath"
	"strconv"
	"strings"
)

// CleanupOpts describes a deferred worktree teardown.
type CleanupOpts struct {
	SourceWindowTarget string // tmux target of the window to kill, e.g. "=proj:=feat"
	SwitchToTarget     string // session/window to switch the client to before killing; empty → skip
	Tree               string // absolute worktree path to remove
	Branch             string // git branch to delete; "" → skip `git branch -d`
	// RepoDir is the main repo/worktree root — REQUIRED. git commands run via
	// `git -C <RepoDir>` because the tree path is renamed before prune/branch-delete,
	// so git cannot infer the repo from a cwd that points inside the (now-moved)
	// tree. An empty RepoDir makes the git bookkeeping steps no-ops or failures;
	// thanks to the best-effort guards (|| true) on those steps, the trash
	// directory is still removed.
	RepoDir string
}

// shellQuote wraps s in POSIX single quotes. Any embedded single quotes in s are
// escaped by ending the quoted token, inserting a literal ', and re-opening it
// ('\”) — the standard POSIX sh single-quote escape sequence.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CleanupScript returns the /bin/sh command string for the §7.2 deferred
// self-close. The mandatory order (switch away → kill window → mv-to-trash →
// worktree prune → branch delete → rm) is enforced by joining steps with " && "
// so a gate failure stops the chain. The mv step is the gate for the git and rm
// steps: it frees the tree path immediately so a shell still cd'd into it cannot
// block removal.
//
// Best-effort steps (switch-client, kill-window, git worktree prune,
// git branch -d) are chained as `<cmd> || true` so that failure of one step
// does not abort the mandatory downstream teardown. In particular:
//   - switch-client: a failed switch must not abort the kill.
//   - kill-window: if already gone, teardown continues.
//   - git worktree prune: bookkeeping only; an empty/wrong RepoDir or an odd
//     repo state must not strand the already-moved trash directory.
//   - git branch -d: an unmerged or already-deleted branch must not leave trash
//     behind.
//
// now and trashSuffix are injected so the result is deterministic and
// unit-testable; the caller supplies a short stable suffix (e.g. an fnv hash of
// the tree path or the pane key) and a unix timestamp. This builder is NOT
// executed in M4 — M6 dispatches it via tmux run-shell.
func CleanupScript(o CleanupOpts, now int64, trashSuffix string) string {
	trashDir := filepath.Join(filepath.Dir(o.Tree), ".perch_trash_"+trashSuffix+"_"+strconv.FormatInt(now, 10))

	var steps []string

	// 1. Brief delay so the current pane's process exits cleanly before we kill
	// the window.
	steps = append(steps, "sleep 0.3")

	// 2. Switch the client away first; must be best-effort (|| true) so a missing
	// target or no-client-attached error does not abort the kill-window step.
	if o.SwitchToTarget != "" {
		steps = append(steps, "tmux switch-client -t "+shellQuote(o.SwitchToTarget)+" || true")
	}

	// 3. Kill the source window. Best-effort: if already gone, continue.
	steps = append(steps, "tmux kill-window -t "+shellQuote(o.SourceWindowTarget)+" || true")

	// 4. Rename the tree to a sibling trash directory. This step MUST succeed
	// (no || true) — it gates prune, branch-delete, and rm. The rename is atomic
	// on same-filesystem mounts, so no intermediate state is observable.
	steps = append(steps, "mv "+shellQuote(o.Tree)+" "+shellQuote(trashDir))

	// 5. Prune the now-dangling worktree reference from the repo. Best-effort
	// (|| true): bookkeeping only — an empty/wrong RepoDir or odd repo state
	// must not strand the already-moved trash directory.
	steps = append(steps, "git -C "+shellQuote(o.RepoDir)+" worktree prune || true")

	// 6. Delete the branch if requested. Best-effort (|| true): an unmerged branch
	// should not leave trash on disk. The "--" separator (defense-in-depth) prevents
	// a branch name beginning with "-" from being parsed as a flag by git.
	if o.Branch != "" {
		steps = append(steps, "git -C "+shellQuote(o.RepoDir)+" branch -d -- "+shellQuote(o.Branch)+" || true")
	}

	// 7. Remove the trash directory.
	steps = append(steps, "rm -rf "+shellQuote(trashDir))

	return strings.Join(steps, " && ")
}

// RunShellArgs wraps a shell command string as the tmux argv for run-shell:
// []string{"run-shell", script}. Dispatch is M6; this only builds the argv.
func RunShellArgs(script string) []string {
	return []string{"run-shell", script}
}
