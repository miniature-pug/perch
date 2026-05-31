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

// AttachArgs builds the tmux argv needed to attach to or switch to session.
// It returns switch-client args when TMUX is set in the environment (perch is
// running inside tmux), and attach-session args otherwise. The returned slice
// is executed via tea.ExecProcess in M5; this method only builds the args and
// never calls Run.
func (o Tmux) AttachArgs(session string) []string {
	if o.getenv()("TMUX") != "" {
		return []string{"switch-client", "-t", SessionTarget(session)}
	}
	return []string{"attach-session", "-t", SessionTarget(session)}
}

// Connect ensures a window exists for the given session/window/dir and returns
// its pane ID. The first agent for a project bootstraps the whole session via
// NewSession; subsequent agents each get a new window inside it via NewWindow.
// Window-reuse (attaching to an existing window) is deferred to M5/M6 — M4
// always creates a fresh window.
func (o Tmux) Connect(ctx context.Context, session, window, dir string) (string, error) {
	exists, err := o.HasSession(ctx, session)
	if err != nil {
		return "", err
	}
	if !exists {
		return o.NewSession(ctx, session, window, dir)
	}
	return o.NewWindow(ctx, session, window, dir)
}
