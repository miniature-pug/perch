package tmux

import (
	"context"
	"fmt"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// NewSession creates a new detached tmux session named session, whose first
// window is named window and opened in dir. Returns the pane ID of the first
// pane (tmux -P -F '#{pane_id}' output).
func (o Tmux) NewSession(ctx context.Context, session, window, dir string) (string, error) {
	stdout, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("new-session", "-d", "-s", session, "-n", window, "-c", dir, "-P", "-F", "#{pane_id}")...)
	if err != nil {
		return "", fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(string(stdout)), nil
}

// NewWindow opens a new window named window in the existing session, starting
// in dir. Returns the pane ID of the new window's pane.
func (o Tmux) NewWindow(ctx context.Context, session, window, dir string) (string, error) {
	stdout, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("new-window", "-t", SessionTarget(session), "-n", window, "-c", dir, "-P", "-F", "#{pane_id}")...)
	if err != nil {
		return "", fmt.Errorf("tmux new-window: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(string(stdout)), nil
}

// SendKeys sends literal to the pane identified by target, followed by a
// separate Enter keystroke. Two Run calls are used deliberately: the -l flag
// defeats tmux key-name and ';' parsing (a bare ';' arg or trailing ';'
// causes tmux to interpret the rest as a tmux command chain). Enter is sent
// as a second call so it is processed as a key-name, not literal text.
// Callers are expected to pass already-shell-quoted command text; argv
// shell-quoting of the literal is handled in M5.
func (o Tmux) SendKeys(ctx context.Context, target, literal string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("send-keys", "-t", target, "-l", literal)...)
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	_, stderr, err = o.runner().Run(ctx, o.bin(),
		o.args("send-keys", "-t", target, "Enter")...)
	if err != nil {
		return fmt.Errorf("tmux send-keys Enter: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// SetPaneOption sets a pane option (e.g. @perch_session) on the pane
// identified by target, using set-option -p.
func (o Tmux) SetPaneOption(ctx context.Context, target, key, val string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("set-option", "-p", "-t", target, key, val)...)
	if err != nil {
		return fmt.Errorf("tmux set-option: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// SetSessionOption sets a session-scoped tmux option (set-option -t <session> <key> <val>).
func (o Tmux) SetSessionOption(ctx context.Context, session, key, val string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("set-option", "-t", session, key, val)...)
	if err != nil {
		return fmt.Errorf("tmux set-option: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// SelectPane focuses a pane (select-pane -t <target>).
func (o Tmux) SelectPane(ctx context.Context, target string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("select-pane", "-t", target)...)
	if err != nil {
		return fmt.Errorf("tmux select-pane: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// BindKey binds a key in a named key-table (bind-key -T <table> <key> <cmd...>).
func (o Tmux) BindKey(ctx context.Context, table, keyName string, cmd ...string) error {
	argv := append([]string{"bind-key", "-T", table, keyName}, cmd...)
	_, stderr, err := o.runner().Run(ctx, o.bin(), o.args(argv...)...)
	if err != nil {
		return fmt.Errorf("tmux bind-key: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// KillSession terminates the session named session. Killing the last session
// causes the tmux server to exit, so a subsequent kill returns exit 1 (no
// server running). Exit codes ≥1 are treated as "already gone" and silently
// ignored; only exec-layer failures (ExitCode == -1) are returned as errors.
func (o Tmux) KillSession(ctx context.Context, session string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("kill-session", "-t", SessionTarget(session))...)
	if err == nil {
		return nil
	}
	if proc.ExitCode(err) >= 1 {
		// Session absent or server already exited — not an error.
		return nil
	}
	return fmt.Errorf("tmux kill-session: %w: %s", err, strings.TrimSpace(string(stderr)))
}

// KillWindow kills the window addressed by target (e.g. WindowTarget(s,w)). A
// missing window exits ≥1; like KillSession that is treated as already-gone.
// Only exec-layer failures (ExitCode == -1) are returned as errors.
func (o Tmux) KillWindow(ctx context.Context, target string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(), o.args("kill-window", "-t", target)...)
	if err == nil {
		return nil
	}
	if proc.ExitCode(err) >= 1 {
		// Window absent or server already exited — not an error.
		return nil
	}
	return fmt.Errorf("tmux kill-window: %w: %s", err, strings.TrimSpace(string(stderr)))
}

// KillServer terminates the tmux server. If the server is already down the
// command exits with code ≥1; that is treated as success. Only exec-layer
// failures are returned as errors.
func (o Tmux) KillServer(ctx context.Context) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(), o.args("kill-server")...)
	if err == nil {
		return nil
	}
	if proc.ExitCode(err) >= 1 {
		return nil
	}
	return fmt.Errorf("tmux kill-server: %w: %s", err, strings.TrimSpace(string(stderr)))
}

// CurrentClientWindow returns the session and window the attached client is
// currently focused on. It is used as a focused-in-tree guard.
func (o Tmux) CurrentClientWindow(ctx context.Context) (session, window string, err error) {
	stdout, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("display-message", "-p", "-F", "#{session_name}\x1f#{window_name}")...)
	if err != nil {
		return "", "", fmt.Errorf("tmux display-message: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	out := strings.TrimSpace(string(stdout))
	parts := strings.SplitN(out, FieldDelim, 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("tmux display-message: unexpected output %q", out)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

// AttachTargetArgs returns the tmux SUBCOMMAND args (switch-client or
// attach-session) needed to reach a pre-built target token. It is the
// primitive underlying AttachArgs; callers that already hold a WindowTarget
// (e.g. launchCmd after a new session) use this directly to avoid
// re-building a SessionTarget from parts they don't have.
//
// Same environment-driven branching as AttachArgs: switch-client when TMUX
// is set (perch runs inside tmux), attach-session otherwise.
func (o Tmux) AttachTargetArgs(target string) []string {
	if o.getenv()("TMUX") != "" {
		return []string{"switch-client", "-t", target}
	}
	return []string{"attach-session", "-t", target}
}

// AttachArgs returns the tmux SUBCOMMAND args (switch-client or attach-session)
// needed to reach session. It returns switch-client args when TMUX is set in
// the environment (perch is running inside tmux), and attach-session args
// otherwise.
//
// The returned slice contains only the subcommand and its flags — it does NOT
// include the binary name or the -L socket flag. To obtain the full argv
// suitable for tea.ExecProcess in M5, combine with ExecArgs:
//
//	argv := t.ExecArgs(t.AttachArgs(session)...)
//	// exec.Command(argv[0], argv[1:]...)
//
// Do NOT exec the slice returned by AttachArgs directly; that would run
// "switch-client" as a binary and silently drop socket isolation.
func (o Tmux) AttachArgs(session string) []string {
	return o.AttachTargetArgs(SessionTarget(session))
}

// SwitchClient runs switch-client -t <target> via the runner. target is a
// pre-built target token (e.g. WindowTarget(session, window)). When perch runs
// inside tmux ($TMUX set) the client handover is instant and requires no
// terminal takeover, so the TUI dispatches this as a plain tea.Cmd rather than
// tea.ExecProcess.
func (o Tmux) SwitchClient(ctx context.Context, target string) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(), o.args("switch-client", "-t", target)...)
	if err != nil {
		return fmt.Errorf("tmux switch-client: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// Connect ensures a window exists for the given session/window/dir and returns
// the pane ID to use. The first agent for a project bootstraps the whole
// session via NewSession. When the session already exists, Connect tries to
// reuse an existing live pane in the named window: if list-panes succeeds and
// finds at least one non-dead pane, its ID is returned without creating a new
// window. A missing window (list-panes exits ≥1) or a window whose every pane
// is dead falls through to NewWindow so the caller always gets a usable pane.
func (o Tmux) Connect(ctx context.Context, session, window, dir string) (string, error) {
	exists, err := o.HasSession(ctx, session)
	if err != nil {
		return "", err
	}
	if !exists {
		return o.NewSession(ctx, session, window, dir)
	}
	// Session exists — try to reuse a live pane in the target window.
	panes, perr := o.ListPanes(ctx, WindowTarget(session, window))
	if perr == nil {
		for _, p := range panes {
			if !p.Dead {
				return p.ID, nil
			}
		}
	}
	// Window absent (list-panes error) or all panes dead — create a new window.
	return o.NewWindow(ctx, session, window, dir)
}
