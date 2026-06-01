package worktree

import (
	"context"
	"fmt"

	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// DeferredRemove dispatches the §7.2 self-close teardown (kill window, move
// tree to trash, prune, delete branch, rm) as a backgrounded tmux run-shell,
// then removes the shadow window record. The script is dispatched FIRST; only
// on successful dispatch is the record removed, so a failed dispatch never
// orphans a worktree from its tracking record.
func DeferredRemove(ctx context.Context, t tmux.Tmux, baseDir string, opts tmux.CleanupOpts, paneKey string, now int64, suffix string) error {
	script := tmux.CleanupScript(opts, now, suffix)
	if err := t.RunShell(ctx, script); err != nil {
		return fmt.Errorf("worktree: deferred remove: %w", err)
	}
	return state.RemoveWindow(baseDir, paneKey)
}
