// Package tmux controls a tmux server through the proc.Runner seam. All
// commands are routed via the Tmux struct's runner/bin/socket so that unit
// tests inject a FakeRunner and never spawn real processes.
package tmux

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// paneFormat is the -F format string for list-panes. Fields are delimited by
// 0x1f (ASCII unit separator) so spaces in paths cannot split a field. One
// pane per line. Field order must match parsePanes.
const paneFormat = "#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}"

// Tmux drives a tmux server. The zero value is ready to use (production
// defaults are filled in by the seam helpers).
type Tmux struct {
	// Runner runs tmux commands. Default proc.ExecRunner{}.
	Runner proc.Runner
	// Bin is the tmux binary name or path. Default "tmux".
	Bin string
	// Socket routes every command to a private server when non-empty: prepends
	// "-L <socket>" to every invocation. The integration harness sets this so
	// tests never touch the user's default server.
	Socket string
	// Getenv resolves environment variables. Default os.Getenv. Used by
	// AttachArgs (M4-B) to read TMUX and display variables.
	Getenv func(string) string
}

// New returns a Tmux with production defaults.
func New() Tmux {
	return Tmux{
		Runner: proc.ExecRunner{},
		Bin:    "tmux",
		Getenv: os.Getenv,
	}
}

// ── seam helpers ──────────────────────────────────────────────────────────────

func (o Tmux) runner() proc.Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return proc.ExecRunner{}
}

func (o Tmux) bin() string {
	if o.Bin != "" {
		return o.Bin
	}
	return "tmux"
}

func (o Tmux) getenv() func(string) string {
	if o.Getenv != nil {
		return o.Getenv
	}
	return os.Getenv
}

// args builds the full argv for a tmux sub-command. When Socket is set it
// prepends "-L <socket>" so every call targets the private server.
func (o Tmux) args(sub ...string) []string {
	if o.Socket == "" {
		return sub
	}
	out := make([]string, 0, 2+len(sub))
	out = append(out, "-L", o.Socket)
	out = append(out, sub...)
	return out
}

// ExecArgs returns the complete argv — binary, the -L socket flag (when set),
// then sub — suitable for exec.Command(argv[0], argv[1:]...). M5 builds an
// attach command as ExecArgs(AttachArgs(session)...) so the socket flag and
// binary are never dropped.
func (o Tmux) ExecArgs(sub ...string) []string {
	return append([]string{o.bin()}, o.args(sub...)...)
}

// ── target builders ───────────────────────────────────────────────────────────

// SessionTarget returns the exact-match target token for a session. The leading
// '=' anchors the name so tmux does not apply prefix or fnmatch matching.
func SessionTarget(session string) string {
	return "=" + session
}

// WindowTarget returns the exact-match target token for a window within a
// session. Both parts are anchored with '=' to address the window
// deterministically; prefix/fnmatch fallback is non-deterministic.
func WindowTarget(session, window string) string {
	return "=" + session + ":=" + window
}

// ── Pane ─────────────────────────────────────────────────────────────────────

// Pane is one tmux pane as read from list-panes.
type Pane struct {
	ID           string
	PID          string
	Command      string
	Dead         bool
	Path         string
	Session      string
	Window       string
	PerchSession string // @perch_session pane option
}

// parsePanes decodes raw list-panes output (one line per pane, fields separated
// by 0x1f). Lines with fewer than the expected number of fields are silently
// skipped so a malformed line never stops the parse or panics.
func parsePanes(raw []byte) []Pane {
	const fieldCount = 8
	lines := strings.Split(string(raw), "\n")
	var panes []Pane
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\x1f")
		if len(fields) < fieldCount {
			// Defensive: skip malformed lines rather than panic or return garbage.
			continue
		}
		panes = append(panes, Pane{
			ID:           fields[0],
			PID:          fields[1],
			Command:      fields[2],
			Dead:         fields[3] == "1",
			Path:         fields[4],
			Session:      fields[5],
			Window:       fields[6],
			PerchSession: fields[7],
		})
	}
	return panes
}

// ── read primitives ───────────────────────────────────────────────────────────

// HasSession reports whether a tmux session named name exists on the server.
// Exit code 0 → (true, nil); exit code ≥1 → (false, nil) (absent session or
// cold server both return exit 1); exec-layer failure (binary missing etc.) →
// (false, err).
func (o Tmux) HasSession(ctx context.Context, name string) (bool, error) {
	_, _, err := o.runner().Run(ctx, o.bin(), o.args("has-session", "-t", SessionTarget(name))...)
	if err == nil {
		return true, nil
	}
	if proc.ExitCode(err) >= 1 {
		// Session absent or server not running — not an error for the caller.
		return false, nil
	}
	// ExitCode == -1: exec-layer failure (e.g. binary not on PATH).
	return false, err
}

// BootID returns the tmux server's #{start_time}, which perch uses as a crash
// sentinel. This call requires a live server; a cold-server call returns an
// error.
func (o Tmux) BootID(ctx context.Context) (string, error) {
	stdout, stderr, err := o.runner().Run(ctx, o.bin(), o.args("display-message", "-p", "#{start_time}")...)
	if err != nil {
		return "", fmt.Errorf("tmux display-message: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(string(stdout)), nil
}

// ListPanes lists the panes attached to target (a session or window target).
func (o Tmux) ListPanes(ctx context.Context, target string) ([]Pane, error) {
	stdout, stderr, err := o.runner().Run(ctx, o.bin(), o.args("list-panes", "-t", target, "-F", paneFormat)...)
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return parsePanes(stdout), nil
}

// ListPanesAll lists every pane across all sessions. Empty/whitespace output
// (no sessions) returns nil, nil rather than an empty slice. This is the
// batched read used by resurrect and admin operations.
func (o Tmux) ListPanesAll(ctx context.Context) ([]Pane, error) {
	stdout, stderr, err := o.runner().Run(ctx, o.bin(), o.args("list-panes", "-a", "-F", paneFormat)...)
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	if strings.TrimSpace(string(stdout)) == "" {
		return nil, nil
	}
	return parsePanes(stdout), nil
}

// CapturePane returns the visible content of target's pane. When scrollback > 0
// it appends -S -<n> to include that many lines of history.
func (o Tmux) CapturePane(ctx context.Context, target string, scrollback int) (string, error) {
	sub := []string{"capture-pane", "-t", target, "-p"}
	if scrollback > 0 {
		sub = append(sub, "-S", fmt.Sprintf("-%d", scrollback))
	}
	stdout, stderr, err := o.runner().Run(ctx, o.bin(), o.args(sub...)...)
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return string(stdout), nil
}

// GetPaneOption returns the value of a pane option (e.g. @perch_session) for
// target. The result is trimmed of leading/trailing whitespace.
func (o Tmux) GetPaneOption(ctx context.Context, target, key string) (string, error) {
	stdout, stderr, err := o.runner().Run(ctx, o.bin(), o.args("show-options", "-p", "-t", target, "-v", key)...)
	if err != nil {
		return "", fmt.Errorf("tmux show-options: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(string(stdout)), nil
}
