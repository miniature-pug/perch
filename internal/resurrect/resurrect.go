// Package resurrect implements the boot-id reconcile engine for `perch resurrect`.
// After a tmux server restart the live panes are gone but the shadow records in
// windows/<paneKey>.json survive. Reconcile reads those records, compares them
// against the live server state, and rebuilds windows lost to a restart.
//
// All subprocess calls are routed through the injected seams in Deps so the
// engine is fully unit-testable without spawning real processes.
package resurrect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// Deps carries the external seams used by Reconcile. Production code wires real
// implementations; unit tests inject FakeRunner-backed stubs.
type Deps struct {
	// Tmux drives the tmux server.
	Tmux tmux.Tmux
	// Runner is the proc.Runner used for git subprocess calls.
	Runner proc.Runner
	// BaseDir is the perch state directory (parent of windows/).
	BaseDir string
	// Now is the unix timestamp written into restored Window.Updated fields.
	Now int64
	// Roots is the list of scan roots from the global config (cfg.Roots). When
	// non-empty, the RESTORE branch rejects any window whose Tree does not reside
	// under at least one root, preventing a compromised windows/*.json from
	// directing git operations into an attacker-controlled directory (V7c).
	//
	// An empty Roots slice disables the containment check (fail-open) so that
	// the production path continues to work before the caller is wired to pass
	// roots. Production callers (cmd/perch/main.go) must load the config and
	// populate this field to activate the guard.
	Roots []string
}

// SkipNote records why a window record was skipped during reconciliation.
// Reason values are one of: empty-sid, window-live, dup-window, tree-gone,
// main, git-error, no-worktree-match, unknown-tool, launch-failed,
// save-failed, prune-failed.
type SkipNote struct {
	// PaneKey is the shadow record's pane id (the filename key).
	PaneKey string
	// Tree is the record's working-directory path.
	Tree string
	// Reason is the machine-readable skip category.
	Reason string
}

// Report summarises what Reconcile did. Every slice element is a PaneKey.
type Report struct {
	// Kept lists records whose live pane was confirmed healthy.
	Kept []string
	// Pruned lists records deleted because the pane was closed intentionally.
	Pruned []string
	// Restored lists records that were rebuilt after a server restart.
	Restored []string
	// Skipped lists records that could not be processed, with reasons.
	Skipped []SkipNote
}

// Reconcile reads all shadow records, snapshots the live server state once, then
// classifies each record as KEEP, PRUNE, or RESTORE and executes the
// appropriate action. The only hard-error return is an unreadable state
// directory; every per-record fault is captured in Report.Skipped.
func Reconcile(ctx context.Context, deps Deps) (Report, error) {
	records, err := state.LoadWindows(deps.BaseDir)
	if err != nil {
		return Report{}, fmt.Errorf("resurrect: load windows: %w", err)
	}
	// Nothing to reconcile — return before touching tmux. This keeps the common
	// "no live windows recorded" case from requiring a running tmux server.
	if len(records) == 0 {
		return Report{}, nil
	}

	// FD1: single snapshot, then classify, then mutate.
	currentBoot, _ := deps.Tmux.BootID(ctx)     // "" when server is down — that is the signal
	livePanes, _ := deps.Tmux.ListPanesAll(ctx) // nil when server is down

	var report Report

	// restoredWindows tracks session\x1fwindow keys for windows that were
	// (re)created earlier in this run. The FD4 live-window guard reads the
	// frozen livePanes snapshot and cannot see panes created after the snapshot
	// was taken, so a second record sharing the same session+window would pass
	// the guard and Launch a duplicate. This in-run set closes that gap.
	restoredWindows := map[string]bool{}

	// restoredBoot caches the boot id read after the first successful Launch
	// (L3: server is guaranteed up by then). A zero value means not yet resolved.
	var restoredBoot string
	bootResolved := false

	for _, w := range records {
		// FD2: match the live pane by pane_id, not @perch_session.
		paneByID := hasLivePaneByID(livePanes, w.PaneKey)
		// FD3: bootMatch requires a non-empty currentBoot (server is up) and
		// an exact match with the record's stored boot id.
		bootMatch := currentBoot != "" && w.BootID == currentBoot

		switch {
		case paneByID && bootMatch:
			// KEEP: the pane is alive and boot ids match — no I/O needed.
			report.Kept = append(report.Kept, w.PaneKey)

		case !paneByID && bootMatch && hasLiveSession(livePanes, w.TmuxSession):
			// PRUNE: same server, home session alive, pane intentionally closed.
			// If the home session is also gone (!hasLiveSession), the record falls
			// through to the default (RESTORE) branch: the agent was stranded by a
			// perch crash and must be re-launched, not silently dropped.
			if rerr := state.RemoveWindow(deps.BaseDir, w.PaneKey); rerr != nil {
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "prune-failed",
				})
			} else {
				report.Pruned = append(report.Pruned, w.PaneKey)
			}

		default:
			// RESTORE branch: boot mismatch or server is cold.
			// FD5 guards in order — cheap to expensive.

			// Guard 1: opencode-new records have no session id and cannot be resumed.
			if w.SessionID == "" {
				_ = state.RemoveWindow(deps.BaseDir, w.PaneKey) // definitive skip: delete cruft
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "empty-sid",
				})
				continue
			}

			// Guard 2 (FD4): live-window guard — reuse the snapshot, no extra call.
			// If a live non-dead pane already exists in the target session/window,
			// Launch would send-keys into it instead of creating a new agent pane.
			// Also skip when an earlier restore in this same run already (re)created
			// a pane in that session+window (the snapshot cannot reflect that pane).
			windowKey := w.TmuxSession + "\x1f" + w.TmuxWindow
			if hasLiveWindowPane(livePanes, w.TmuxSession, w.TmuxWindow) {
				_ = state.RemoveWindow(deps.BaseDir, w.PaneKey) // definitive skip
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "window-live",
				})
				continue
			} else if restoredWindows[windowKey] {
				_ = state.RemoveWindow(deps.BaseDir, w.PaneKey) // definitive skip
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "dup-window",
				})
				continue
			}

			// Guard 3a (V7c): reject trees outside the configured scan roots.
			// This prevents a compromised windows/*.json from directing git
			// operations into an attacker-controlled directory. Fail-open when
			// deps.Roots is empty (not yet configured in the production caller).
			if !treeUnderRoots(deps.Roots, w.Tree) {
				// Transient skip: a roots misconfiguration should not permanently
				// delete legitimate records; the record is re-evaluated on the
				// next run (with correct roots or after the check passes).
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "tree-out-of-root",
				})
				continue
			}

			// Guard 3: the working directory must still exist.
			if _, serr := os.Stat(w.Tree); errors.Is(serr, os.ErrNotExist) {
				_ = state.RemoveWindow(deps.BaseDir, w.PaneKey) // definitive skip
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "tree-gone",
				})
				continue
			}

			// Guard 4: verify the tree is part of a non-main linked worktree.
			wts, gerr := git.ListWorktrees(ctx, deps.Runner, w.Tree)
			if gerr != nil {
				// Transient: keep the record for the next run.
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "git-error",
				})
				continue
			}
			matched, ok := findAncestorWorktree(wts, w.Tree)
			if !ok {
				// No worktree matched — transient: keep the record.
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "no-worktree-match",
				})
				continue
			}
			mainWt, hasMain := git.MainWorktree(wts)
			if hasMain && matched.Path == mainWt.Path {
				// Main-checkout sessions are not auto-resurrected (too intrusive).
				_ = state.RemoveWindow(deps.BaseDir, w.PaneKey) // definitive skip
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "main",
				})
				continue
			}

			// All guards passed — RESTORE.
			adapter, ok := adapterFor(w.Tool)
			if !ok {
				// Defensive: unknown tool type. Transient: keep the record.
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "unknown-tool",
				})
				continue
			}

			argv := append([]string{adapter.Name()}, adapter.ResumeArgs(w.SessionID)...)
			paneID, lerr := deps.Tmux.Launch(ctx, w.TmuxSession, w.TmuxWindow, w.Tree, argv)
			if lerr != nil {
				// Transient: keep the record for the next run.
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "launch-failed",
				})
				continue
			}

			// Re-stamp @perch_session so the TUI can discover this pane.
			_ = deps.Tmux.SetPaneOption(ctx, paneID, "@perch_session", w.SessionID)

			// L3: read the restored server's boot id once and cache it.
			if !bootResolved {
				if b, berr := deps.Tmux.BootID(ctx); berr == nil {
					restoredBoot = b
				} else {
					restoredBoot = currentBoot // fallback; may be ""
				}
				bootResolved = true
			}

			// Replace the dead-boot record with one stamped with the new boot.
			// Save first so that a save failure leaves the old record intact
			// (the next run can self-heal via the live pane and FD4 guard).
			if serr := state.SaveWindow(deps.BaseDir, model.Window{
				PaneKey:     paneID,
				Tool:        w.Tool,
				SessionID:   w.SessionID,
				Tree:        w.Tree,
				TmuxSession: w.TmuxSession,
				TmuxWindow:  w.TmuxWindow,
				BootID:      restoredBoot,
				Updated:     deps.Now,
			}); serr != nil {
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "save-failed",
				})
				continue
			}
			// Only remove the old record when the new pane id differs.
			// When the tmux server restarts it resets its pane counter, so
			// the fresh server may assign the same id (e.g. %0) as the old
			// one. In that case SaveWindow already overwrote the file in
			// place — removing it here would delete the just-written record
			// and break idempotency (second run sees no record to KEEP).
			if paneID != w.PaneKey {
				_ = state.RemoveWindow(deps.BaseDir, w.PaneKey)
			}

			report.Restored = append(report.Restored, w.PaneKey)
			restoredWindows[windowKey] = true
		}
	}

	return report, nil
}

// adapterFor resolves the agent adapter for a known tool. Returns false for
// unknown tools so the engine can treat that as a transient skip rather than
// panicking.
func adapterFor(tool model.Tool) (agent.Adapter, bool) {
	switch tool {
	case model.ToolClaude:
		return agent.NewClaude(), true
	case model.ToolOpencode:
		return agent.NewOpencode(), true
	default:
		return nil, false
	}
}

// isDescendant reports whether child is equal to parent or lives inside it.
// Both paths are cleaned before comparison so symlink-free paths compare
// correctly regardless of trailing slashes or doubled separators.
func isDescendant(parent, child string) bool {
	p := filepath.Clean(parent)
	c := filepath.Clean(child)
	return c == p || strings.HasPrefix(c, p+string(os.PathSeparator))
}

// treeUnderRoots reports whether tree is contained within at least one of the
// given scan roots. The containment check uses filepath.Rel to avoid prefix
// false-positives (e.g. /root/foo is not under /root/fo). Both paths are
// cleaned before comparison. When roots is empty the function returns true
// (fail-open: no roots configured means no containment restriction).
func treeUnderRoots(roots []string, tree string) bool {
	if len(roots) == 0 {
		return true // fail-open: guard is disabled when roots are not configured
	}
	cleanTree := filepath.Clean(tree)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		// Accept tree == root exactly.
		if cleanTree == cleanRoot {
			return true
		}
		rel, err := filepath.Rel(cleanRoot, cleanTree)
		if err != nil {
			continue
		}
		// A path outside root starts with ".." or is "..".
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// hasLivePaneByID reports whether any live (non-dead) pane in panes has the
// given pane id.
func hasLivePaneByID(panes []tmux.Pane, paneKey string) bool {
	for _, p := range panes {
		if p.ID == paneKey && !p.Dead {
			return true
		}
	}
	return false
}

// hasLiveWindowPane reports whether any live non-dead pane in panes belongs to
// the given session and window. This is the FD4 live-window guard.
func hasLiveWindowPane(panes []tmux.Pane, session, window string) bool {
	for _, p := range panes {
		if p.Session == session && p.Window == window && !p.Dead {
			return true
		}
	}
	return false
}

// hasLiveSession reports whether any live (non-dead) pane in panes belongs to
// the given session. Used by the PRUNE discriminator: a record whose pane is
// gone but whose home session is still alive means the user intentionally closed
// the window; if the session itself is also gone, the agent may have been
// stranded by a perch crash and should be re-launched (RESTORE path).
func hasLiveSession(panes []tmux.Pane, session string) bool {
	for _, p := range panes {
		if p.Session == session && !p.Dead {
			return true
		}
	}
	return false
}

// findAncestorWorktree finds the worktree whose Path is an ancestor-or-equal of
// tree (the agent may have cd'd into a subdir of the worktree root). When
// multiple worktrees satisfy the ancestor-or-equal condition (e.g. a linked
// worktree nested under the main checkout path), the one with the LONGEST
// cleaned Path is returned — i.e. the most-specific (deepest) match. Returns
// the matching Worktree and true, or zero value and false if no worktree matched.
func findAncestorWorktree(wts []git.Worktree, tree string) (git.Worktree, bool) {
	var best git.Worktree
	bestLen := -1
	for _, wt := range wts {
		if isDescendant(wt.Path, tree) {
			if n := len(filepath.Clean(wt.Path)); n > bestLen {
				bestLen = n
				best = wt
			}
		}
	}
	if bestLen < 0 {
		return git.Worktree{}, false
	}
	return best, true
}
