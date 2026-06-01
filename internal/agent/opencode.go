package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/resources"
)

// Opencode is the Adapter for the opencode CLI. Unlike claude, opencode exposes
// a documented listing command, so session enumeration shells out through the
// proc.Runner seam rather than parsing on-disk files. opencode's session list
// is scoped to the project that the working directory resolves to (there is no
// global/all-projects flag), so Dir selects which project's sessions to list;
// the caller sets it per tree.
type Opencode struct {
	// Runner runs the opencode CLI. Default proc.ExecRunner{}.
	Runner proc.Runner
	// Bin is the opencode binary name or path. Default "opencode".
	Bin string
	// Dir is the working directory the listing is scoped to (process cwd for the
	// CLI). Empty means inherit perch's own cwd.
	Dir string
	// LookPath resolves a binary on PATH; used by Detect. Default exec.LookPath.
	LookPath func(string) (string, error)
}

// Compile-time guarantee that Opencode satisfies the Adapter interface,
// including for the zero value (methods must not panic on nil seams).
var _ Adapter = Opencode{}

// NewOpencode returns an Opencode with production defaults filled in. Set Dir to
// scope the listing to a specific project directory.
func NewOpencode() Opencode {
	return Opencode{
		Runner:   proc.ExecRunner{},
		Bin:      "opencode",
		LookPath: exec.LookPath,
	}
}

func (o Opencode) runner() proc.Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return proc.ExecRunner{}
}

func (o Opencode) lookPath() func(string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath
	}
	return exec.LookPath
}

func (o Opencode) bin() string {
	if o.Bin != "" {
		return o.Bin
	}
	return "opencode"
}

// Name returns the canonical tool identifier.
func (o Opencode) Name() string { return string(model.ToolOpencode) }

// Detect reports whether the opencode binary resolves on PATH.
func (o Opencode) Detect() bool {
	_, err := o.lookPath()(o.bin())
	return err == nil
}

// ResumeArgs returns the args to resume sessionID in an interactive TUI. perch
// launches interactive panes, so this uses the top-level form (opencode
// --session <id>), not the one-shot "opencode run" subcommand.
func (o Opencode) ResumeArgs(sessionID string) []string {
	return []string{"--session", sessionID}
}

// ErrForkUnsupported is returned by Opencode.ForkInto. perch v1 deliberately
// starts a fresh session in the target worktree instead of forking, so callers
// can errors.Is against this sentinel to take the "start fresh" path. The CLI
// does expose a --fork flag (with --session/--continue), so enabling forking
// later is a small, intentional scope change rather than a technical limitation.
var ErrForkUnsupported = errors.New("opencode: fork into worktree is unsupported in v1 (start a fresh session instead)")

// ForkInto reports that opencode forking is unsupported (see ErrForkUnsupported).
func (o Opencode) ForkInto(sessionID, targetDir string) ([]string, error) {
	return nil, ErrForkUnsupported
}

// NewArgs builds the launch args for a fresh interactive session. opts.SessionID
// is ignored: opencode assigns its own session ids.
func (o Opencode) NewArgs(opts NewOpts) []string {
	var args []string
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Agent != "" {
		args = append(args, "--agent", opts.Agent)
	}
	if opts.Prompt != "" {
		args = append(args, "--prompt", opts.Prompt)
	}
	return args
}

// ── InstallStatusHook ─────────────────────────────────────────────────────────

// InstallStatusHook writes the embedded perch-status.ts plugin to
// ~/.config/opencode/plugins/perch-status.ts. opencode auto-discovers any *.ts
// file under that directory. Only perch-status.ts is written (overwrite-safe —
// it is our file); sibling plugins are never touched. The replace parameter is
// accepted for interface conformance — perch-status.ts is entirely perch-owned
// so both additive and replace mode unconditionally overwrite the file with the
// current embedded content (the file is always up-to-date after either call).
//
// The path uses $HOME/.config, not $XDG_CONFIG_HOME, so it matches the path
// that doctor's opencodePluginOk checks — both must agree or setup and doctor
// silently disagree. Keep in sync with doctor.go:opencodePluginOk.
func (o Opencode) InstallStatusHook(replace bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("opencode InstallStatusHook: home dir: %w", err)
	}
	dir := filepath.Join(home, ".config", "opencode", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("opencode InstallStatusHook: mkdir: %w", err)
	}

	path := filepath.Join(dir, "perch-status.ts")

	// Atomic write: temp file in the same directory so rename is one syscall.
	tmp, err := os.CreateTemp(dir, ".perch-status-*.ts")
	if err != nil {
		return fmt.Errorf("opencode InstallStatusHook: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(resources.PerchStatusTS); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("opencode InstallStatusHook: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("opencode InstallStatusHook: close temp: %w", err)
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("opencode InstallStatusHook: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("opencode InstallStatusHook: rename: %w", err)
	}
	return nil
}

// ── session enumeration ──────────────────────────────────────────────────────────

// sessionJSON mirrors one element of `opencode session list --format json`. The
// CLI emits flat camelCase fields with unix-millisecond timestamps (the DB's
// snake_case names are not used on the wire).
type sessionJSON struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
	// Created is decoded for wire fidelity only; model.Session carries no
	// created timestamp, so it is not mapped.
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
	ProjectID string `json:"projectId"`
}

// ListSessions lists the sessions opencode has for o.Dir's project. A runner or
// CLI failure is returned as an error (the tool degrades to "unavailable"); the
// caller decides whether to surface or ignore it.
func (o Opencode) ListSessions(ctx context.Context) ([]model.Session, error) {
	stdout, stderr, err := o.runner().RunInDir(ctx, o.Dir, o.bin(), "session", "list", "--format", "json")
	if err != nil {
		// Surface stderr: a bare "exit status N" is undebuggable in production.
		return nil, fmt.Errorf("opencode session list: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return parseSessionList(stdout)
}

// parseSessionList decodes `opencode session list --format json` output into
// sessions. An empty scope prints nothing (zero bytes) rather than "[]", so both
// empty/whitespace-only input and a literal "[]" map to zero sessions with no
// error. Malformed JSON returns an error and never panics. Millisecond
// timestamps are truncated to the unix seconds that model.Session stores.
func parseSessionList(raw []byte) ([]model.Session, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}

	var raws []sessionJSON
	if err := json.Unmarshal([]byte(trimmed), &raws); err != nil {
		return nil, err
	}

	sessions := make([]model.Session, 0, len(raws))
	for _, r := range raws {
		sessions = append(sessions, model.Session{
			ID:        r.ID,
			Tool:      model.ToolOpencode,
			Directory: r.Directory,
			Title:     r.Title,
			Updated:   r.Updated / 1000, // unix ms → unix seconds
		})
	}
	return sessions, nil
}

// GroupByDirectory buckets sessions by their bound directory. opencode's session
// list is project-scoped and a project can span sub-directories, so directory is
// the join key perch uses to attach a session to a working tree.
func GroupByDirectory(sessions []model.Session) map[string][]model.Session {
	out := make(map[string][]model.Session)
	for _, s := range sessions {
		out[s.Directory] = append(out[s.Directory], s)
	}
	return out
}
