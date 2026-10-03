// internal/agent/exit_sentinel.go
package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/miniature-pug/perch/internal/hooklistener"
)

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
// bearer token would show on screen. The sentinel captures `ec=$?` before it
// runs curl, because curl overwrites $?. The exit code travels in the query
// string (hooklistener.AgentExitParam), not in a JSON body, so the line
// carries no backslash and no single quote and can be wrapped verbatim in
// `sh -c '...'` for non-POSIX login shells (see wrapForLoginShell). The
// sentinel discards curl's output, so the marker never touches the pane's
// xterm.
const exitSentinel = `; ec=$?; curl -sf -X POST -H "Authorization: Bearer $PERCH_EXIT_TOKEN" "$PERCH_EXIT_URL?` +
	hooklistener.AgentExitParam + `=$ec" >/dev/null 2>&1`

// hookEventAgentExit is the hook_event_name the exit sentinel's report is
// translated to. For claude, this event rides the SAME loopback hook listener
// as the other lifecycle hooks. For opencode, perch sets up a dedicated exit
// listener, because opencode has no hook system. It is deliberately NOT one
// of the claude settings hooks; the sentinel goes into the typed launch line.
const hookEventAgentExit = hooklistener.EventAgentExit

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

// loginShell reports the pane's login shell ($SHELL, as pty.LoginShellArgv
// uses it). It is a var so tests can swap it.
var loginShell = func() string { return os.Getenv("SHELL") }

// nonPOSIXShells are login shells whose syntax rejects the POSIX-sh launch
// lines (`ec=$?`, `( … )`, `$((…))`, `while …; do`). For these, the line is
// run through `sh -c '…'` instead (AGT-17). POSIX-family shells (sh, bash,
// zsh, dash, ksh, …) keep running the line directly, so the user's aliases
// and shell functions for the agent binary still apply there.
var nonPOSIXShells = map[string]bool{
	"fish": true, "nu": true, "nushell": true, "elvish": true,
	"csh": true, "tcsh": true, "pwsh": true, "powershell": true,
}

// wrapForLoginShell turns a POSIX-sh launch line (without its trailing
// newline) into the line typed into the pane's login shell, newline
// included. For a non-POSIX login shell it wraps the line as `sh -c '…'`.
// That is valid in fish, nushell, elvish, csh, and PowerShell, because the
// launch lines contain no single quote and no backslash (enforced here: if
// one ever did, the line is typed unwrapped rather than mangled).
func wrapForLoginShell(line string) string {
	lead := ""
	if strings.HasPrefix(line, " ") {
		// Keep the leading space outside the wrapper, so history-ignoring
		// shells still skip the line.
		lead = " "
		line = strings.TrimLeft(line, " ")
	}
	sh := filepath.Base(loginShell())
	if nonPOSIXShells[sh] && !strings.ContainsAny(line, `'\`) {
		return lead + "sh -c '" + line + "'\n"
	}
	return lead + line + "\n"
}

// shellSafeWord reports whether s can be placed inside double quotes in a
// launch line with no escaping and no expansion, and also inside the
// `sh -c '…'` wrapper.
func shellSafeWord(s string) bool {
	return s != "" && !strings.ContainsAny(s, "'\"\\$`!\n\r")
}
