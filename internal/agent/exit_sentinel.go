// internal/agent/exit_sentinel.go
package agent

import "github.com/Miniature-Pug/perch/internal/hooklistener"

// The agent runs INSIDE an interactive login shell — perch types the launch line
// into the shell's stdin (see app.OpenWorkspace → br.Write); it never execs the
// agent. So when the agent exits — gracefully via /exit, or violently via
// SIGKILL/OOM/segfault — control returns to the still-alive shell and no pty:exit
// ever fires, leaving the session reading "running" forever (F32). exitSentinel is
// the shell suffix appended to every agent launch line: after the foreground agent
// exits for ANY reason the shell's wait returns, it captures $? and pings perch's
// loopback exit listener, which the monitor translates into StateExited so a dead
// agent stops reading as running.
//
// The bearer token and URL are referenced BY NAME ($PERCH_EXIT_TOKEN /
// $PERCH_EXIT_URL), injected into the pane shell's PROCESS ENVIRONMENT (see
// exitPaneEnv + app.OpenWorkspace), never inlined — the interactive shell ECHOES
// the typed line, so an inlined bearer token would be rendered on-screen (a new
// secret exposure for claude, whose launch line carries no secret today). `ec=$?`
// is captured BEFORE curl because curl overwrites $?. Output is discarded so the
// marker never touches the pane's xterm.
const exitSentinel = `; ec=$?; curl -sf -X POST -H "Authorization: Bearer $PERCH_EXIT_TOKEN" -H "Content-Type: application/json" -d "{\"hook_event_name\":\"AgentExit\",\"error_type\":\"$ec\"}" "$PERCH_EXIT_URL" >/dev/null 2>&1`

// hookEventAgentExit is the hook_event_name the exit sentinel POSTs. For claude it
// rides the SAME loopback hooklistener as the other lifecycle hooks (handleHook
// forwards any non-PreToolUse type to Events()); for opencode a dedicated exit
// listener is stood up because opencode has no hook system. It is deliberately NOT
// one of perchMonitorEvents (the claude settings.json hooks): the sentinel is
// injected into the typed launch line, not the settings file.
const hookEventAgentExit = "AgentExit"

// Pane-environment variable names read by exitSentinel. Injected at pty spawn so
// they are never echoed (unlike the typed launch line).
const (
	envExitToken = "PERCH_EXIT_TOKEN"
	envExitURL   = "PERCH_EXIT_URL"
)

// exitPaneEnv returns the KEY=VALUE process-environment entries a pane shell needs
// so exitSentinel can authenticate to l by name. Returns nil when l is nil so a
// monitor without an exit listener injects nothing.
func exitPaneEnv(l *hooklistener.Listener) []string {
	if l == nil {
		return nil
	}
	return []string{
		envExitToken + "=" + l.Token(),
		envExitURL + "=http://" + l.Addr() + "/hook",
	}
}

// exitReason renders a human lifecycle reason from the sentinel's captured $?.
// An empty or "0" code is a clean exit ("exited"); any other code is a crash/kill
// (bash reports 128+signal, e.g. 137 = SIGKILL/OOM) and is surfaced verbatim so
// the user can tell a graceful /exit from an OOM. Distinct from StateErrored (a
// graceful /exit must NOT read as a red error) — see StateExited.
func exitReason(ec string) string {
	if ec == "" || ec == "0" {
		return "exited"
	}
	return "exited (code " + ec + ")"
}
