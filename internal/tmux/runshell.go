package tmux

import (
	"context"
	"fmt"
	"strings"
)

// RunShell dispatches script via `tmux run-shell -b` (backgrounded) so the
// caller's pane can self-close: run-shell returns immediately and the script
// runs detached on the server. The pure RunShellArgs builder is intentionally
// NOT reused here (it omits -b by design); do not modify it.
//
// This is called from a live pane, so the server is up; any error is a real
// failure and is returned verbatim (unlike KillSession/KillWindow, exit ≥1 is
// NOT silently ignored).
func (o Tmux) RunShell(ctx context.Context, script string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(), o.args("run-shell", "-b", script)...)
	if err != nil {
		return fmt.Errorf("tmux run-shell: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}
