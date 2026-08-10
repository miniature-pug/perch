// internal/agent/exit_sentinel.go
package agent

import "github.com/miniature-pug/perch/internal/hooklistener"

// The agent runs INSIDE an interactive login shell. perch types the launch
// line into the shell's stdin (see app.OpenWorkspace, which calls br.Write).
// perch never execs the agent process directly.
//
// When the agent exits, gracefully via /exit, or violently via SIGKILL, OOM,
// or a segfault, control returns to the shell. The shell stays alive, so no
// pty:exit event ever fires, and the session would read "running" forever
// (bug F32).
//
// exitSentinel is a shell suffix appended to every agent launch line. After
// the foreground agent exits for any reason, the shell's wait call returns.
// The shell then captures $? and pings perch's loopback exit listener. The
// monitor translates that ping into StateExited, so a dead agent stops
// reading as running.
//
// The bearer token and URL are referenced BY NAME ($PERCH_EXIT_TOKEN and
// $PERCH_EXIT_URL). perch injects them into the pane shell's PROCESS
// ENVIRONMENT (see exitPaneEnv and app.OpenWorkspace). The sentinel never
// inlines them: the interactive shell ECHOES the typed line, so an inlined
// bearer token would show on screen. That would be a new secret exposure for
// claude, whose launch line carries no secret today. The sentinel captures
// `ec=$?` before it runs curl, because curl overwrites $?. The sentinel
// discards curl's output, so the marker never touches the pane's xterm.
const exitSentinel = `; ec=$?; curl -sf -X POST -H "Authorization: Bearer $PERCH_EXIT_TOKEN" -H "Content-Type: application/json" -d "{\"hook_event_name\":\"AgentExit\",\"error_type\":\"$ec\"}" "$PERCH_EXIT_URL" >/dev/null 2>&1`

// hookEventAgentExit is the hook_event_name value the exit sentinel POSTs.
// For claude, this event rides the SAME loopback hook listener as the other
// lifecycle hooks: handleHook forwards any non-PreToolUse type to Events().
// For opencode, perch sets up a dedicated exit listener, because opencode has
// no hook system. hookEventAgentExit is deliberately NOT one of
// perchMonitorEvents (the claude settings.json hooks). The sentinel goes into
// the typed launch line, not the settings file.
const hookEventAgentExit = "AgentExit"

// These are the pane-environment variable names exitSentinel reads. perch
// injects them at pty spawn, so the shell never echoes them, unlike the
// typed launch line.
const (
	envExitToken = "PERCH_EXIT_TOKEN"
	envExitURL   = "PERCH_EXIT_URL"
)

// exitPaneEnv returns the KEY=VALUE process-environment entries a pane shell
// needs, so exitSentinel can authenticate to l by name. exitPaneEnv returns
// nil when l is nil, so a monitor with no exit listener injects nothing.
func exitPaneEnv(l *hooklistener.Listener) []string {
	if l == nil {
		return nil
	}
	return []string{
		envExitToken + "=" + l.Token(),
		envExitURL + "=http://" + l.Addr() + "/hook",
	}
}

// exitReason turns the sentinel's captured $? code into a human-readable
// lifecycle reason. An empty code, or "0", means a clean exit ("exited").
// Any other code means a crash or a kill: bash reports 128 plus the signal
// number, for example 137 for SIGKILL or OOM. exitReason shows that code
// as is, so the user can tell a graceful /exit from an OOM. This state is
// distinct from StateErrored. A graceful /exit must NOT read as a red
// error. See StateExited.
func exitReason(ec string) string {
	if ec == "" || ec == "0" {
		return "exited"
	}
	return "exited (code " + ec + ")"
}
