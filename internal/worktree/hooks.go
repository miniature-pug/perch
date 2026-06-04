package worktree

import (
	"context"
	"fmt"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// HookEnv carries the environment variables injected into each hook invocation.
type HookEnv struct {
	Handle       string
	WorktreePath string
	ProjectRoot  string
	Branch       string
}

// RunHooks runs each command in cmds in order inside treePath. An empty cmds
// list is a no-op. A failing hook aborts the remaining hooks and returns the error.
//
// Commands are invoked via `sh -c <script>` where script prepends POSIX export
// statements for the PERCH_* environment variables. Injecting via sh -c exports
// avoids widening the proc.Runner interface with an env parameter — Runner has
// no env param and adding one would require changes across all callers and test
// doubles. The sh -c seam is the deliberate abstraction boundary.
//
// phase is used only for error context (e.g. "post_create").
func RunHooks(ctx context.Context, r proc.Runner, treePath, phase string, cmds []string, env HookEnv) error {
	for _, cmd := range cmds {
		script := buildScript(cmd, env)
		_, _, err := r.RunInDir(ctx, treePath, "sh", "-c", script)
		if err != nil {
			return fmt.Errorf("worktree: %s hook %q failed: %w", phase, cmd, err)
		}
	}
	return nil
}

// buildScript wraps cmd with POSIX export statements for the PERCH_* variables.
func buildScript(cmd string, env HookEnv) string {
	return strings.Join([]string{
		"export PERCH_HANDLE=" + shellQuote(env.Handle),
		"export PERCH_WORKTREE_PATH=" + shellQuote(env.WorktreePath),
		"export PERCH_PROJECT_ROOT=" + shellQuote(env.ProjectRoot),
		"export PERCH_BRANCH=" + shellQuote(env.Branch),
		cmd,
	}, "; ")
}

// shellQuote wraps s in POSIX single quotes. Embedded single quotes are escaped
// using the standard POSIX sequence (end quote, literal ', reopen quote).
// Duplicated here to keep the packages independent (do not import across
// packages for an unexported helper).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
