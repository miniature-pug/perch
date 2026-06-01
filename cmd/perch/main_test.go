package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
	"github.com/Miniature-Pug/perch/internal/tui"
)

// helper executes run and returns stdout, stderr, and the exit code.
func callRun(args []string) (stdout, stderr string, code int) {
	var out, errBuf strings.Builder
	code = run(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

// ── Non-existent path → exit 2 ────────────────────────────────────────────────

func TestRun_NonExistentPath_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"/does/not/exist/perch-test"})
	if code != 2 {
		t.Errorf("expected exit 2 for non-existent path, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

// ── File path (not a directory) → exit 2 ─────────────────────────────────────

func TestRun_FilePath_Exit2(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "somefile.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := callRun([]string{f})
	if code != 2 {
		t.Errorf("expected exit 2 for file path, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

// ── setup ─────────────────────────────────────────────────────────────────────

func TestRun_Setup_Exit0(t *testing.T) {
	// Redirect HOME so InstallStatusHook writes to a temp dir, not the real home.
	// Detection succeeds/fails based on PATH; either way the handler exits 0.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	out, _, code := callRun([]string{"setup"})
	if code != 0 {
		t.Errorf("expected exit 0 for setup, got %d", code)
	}
	if !strings.Contains(out, "setup") {
		t.Errorf("expected output mentioning 'setup'; got: %q", out)
	}
}

func TestRun_Setup_ContainsSetupPrefix(t *testing.T) {
	// PATH-independent: regardless of whether claude/opencode are installed,
	// the handler always exits 0 and always emits at least one "setup:" line.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	out, _, code := callRun([]string{"setup"})
	if code != 0 {
		t.Errorf("expected exit 0 for setup, got %d", code)
	}
	if !strings.Contains(out, "setup:") {
		t.Errorf("expected 'setup:' prefix in output; got: %q", out)
	}
}

func TestRun_Setup_Replace_NotSupportedNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	out, _, code := callRun([]string{"setup", "--replace"})
	if code != 0 {
		t.Errorf("expected exit 0 for setup --replace (additive), got %d", code)
	}
	if !strings.Contains(out, "--replace not supported") {
		t.Errorf("expected --replace note in output; got: %q", out)
	}
}

// ── resurrect (empty state dir → nothing to reconcile) ───────────────────────

func TestRun_Resurrect_Exit0(t *testing.T) {
	// Point state.StateDir() at a temp dir with no windows/ records so
	// Reconcile early-returns without touching tmux. The test is fully hermetic.
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	out, _, code := callRun([]string{"resurrect"})
	if code != 0 {
		t.Errorf("expected exit 0 for resurrect with empty state dir, got %d", code)
	}
	if !strings.Contains(out, "nothing to reconcile") {
		t.Errorf("expected 'nothing to reconcile' message; got: %q", out)
	}
}

// ── status set (FD4: empty $TMUX_PANE → silent exit 0) ───────────────────────

func TestRun_StatusSetWorking_Exit0(t *testing.T) {
	// Pin TMUX_PANE to empty so handleStatus takes the FD4 no-op path and never
	// reaches the real tmux server — keeps the test hermetic even when run from
	// inside a tmux pane.
	t.Setenv("TMUX_PANE", "")
	_, _, code := callRun([]string{"status", "set", "working"})
	if code != 0 {
		t.Errorf("expected exit 0 for status set working, got %d", code)
	}
}

func TestRun_StatusSetWaiting_Exit0(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	_, _, code := callRun([]string{"status", "set", "waiting"})
	if code != 0 {
		t.Errorf("expected exit 0 for status set waiting, got %d", code)
	}
}

func TestRun_StatusSetDone_Exit0(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	_, _, code := callRun([]string{"status", "set", "done"})
	if code != 0 {
		t.Errorf("expected exit 0 for status set done, got %d", code)
	}
}

func TestRun_StatusSetInvalid_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"status", "set", "invalid"})
	if code != 2 {
		t.Errorf("expected exit 2 for invalid status value, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

func TestRun_StatusNoArgs_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"status"})
	if code != 2 {
		t.Errorf("expected exit 2 for bare status, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

// ── version ───────────────────────────────────────────────────────────────────

func TestRun_Version_Exit0(t *testing.T) {
	out, _, code := callRun([]string{"version"})
	if code != 0 {
		t.Errorf("expected exit 0 for version, got %d", code)
	}
	// version var is "dev" in tests.
	if !strings.Contains(out, "dev") {
		t.Errorf("expected version string in output; got: %q", out)
	}
}

func TestRun_Version_ContainsPlatformInfo(t *testing.T) {
	out, _, _ := callRun([]string{"version"})
	if !strings.Contains(out, "go") {
		t.Errorf("expected Go version in output; got: %q", out)
	}
}

// ── doctor verb routes to doctor.Run ─────────────────────────────────────────

func TestRun_Doctor_Routes(t *testing.T) {
	// We don't fully control doctor's environment in this test, but we can assert
	// that the verb "doctor" actually dispatches into doctor.Run (not a stub).
	// doctor.Run always emits a "\nperch <version>\n" header and then tool rows
	// (tmux, git, etc.) regardless of whether those tools are present.
	// A routing regression to an unimplemented stub would print none of these.
	var out strings.Builder
	var errBuf strings.Builder
	code := run([]string{"doctor"}, &out, &errBuf)
	if code != 0 && code != 1 {
		t.Errorf("doctor returned unexpected code %d", code)
	}
	output := out.String()
	// The doctor report header always contains the version string.
	if !strings.Contains(output, "perch") {
		t.Errorf("expected doctor report header ('perch ...') in output; got:\n%s", output)
	}
	// doctor.Run always emits rows for every tool in the descriptor table.
	if !strings.Contains(output, "tmux") {
		t.Errorf("expected 'tmux' row in doctor output (proves real routing); got:\n%s", output)
	}
	if !strings.Contains(output, "git") {
		t.Errorf("expected 'git' row in doctor output (proves real routing); got:\n%s", output)
	}
}

// ── unknown verb (typo) → exit 2 ──────────────────────────────────────────────

func TestRun_UnknownVerb_Exit2(t *testing.T) {
	// "doctr" is not a known verb and not an existing directory.
	_, errOut, code := callRun([]string{"doctr"})
	if code != 2 {
		t.Errorf("expected exit 2 for unknown verb, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

// ── debug discover: empty dir → exit 0, "no projects found" ─────────────────

func TestRun_DebugDiscover_EmptyDir_Exit0(t *testing.T) {
	dir := t.TempDir() // no .git entries → Scan returns nothing
	out, _, code := callRun([]string{"debug", "discover", dir})
	if code != 0 {
		t.Errorf("expected exit 0 for empty dir, got %d", code)
	}
	if !strings.Contains(out, "no") || !strings.Contains(out, "projects found") {
		t.Errorf("expected 'no ... projects found' message; got: %q", out)
	}
}

// ── debug discover: nonexistent path → exit 2 ────────────────────────────────

func TestRun_DebugDiscover_NonexistentPath_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"debug", "discover", "/does/not/exist/perch-test-debug"})
	if code != 2 {
		t.Errorf("expected exit 2 for nonexistent path, got %d", code)
	}
	if errOut == "" {
		t.Errorf("expected error message on stderr; got empty")
	}
}

// ── debug: unknown subcommand → exit 2 ───────────────────────────────────────

func TestRun_DebugUnknownSubcommand_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"debug", "bogus"})
	if code != 2 {
		t.Errorf("expected exit 2 for unknown debug subcommand, got %d", code)
	}
	if errOut == "" {
		t.Errorf("expected usage message on stderr; got empty")
	}
}

// ── debug: no subcommand → exit 2 ────────────────────────────────────────────

func TestRun_DebugNoSubcommand_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"debug"})
	if code != 2 {
		t.Errorf("expected exit 2 for bare debug, got %d", code)
	}
	if errOut == "" {
		t.Errorf("expected usage message on stderr; got empty")
	}
}

// ── debug absent from printUsage ─────────────────────────────────────────────

func TestPrintUsage_NoDebug(t *testing.T) {
	// Trigger a bad verb to capture the usage output that printUsage emits.
	_, errOut, _ := callRun([]string{"doctr"})
	if strings.Contains(errOut, "debug") {
		t.Errorf("printUsage must not mention 'debug' (hidden command); stderr: %q", errOut)
	}
}

// ── writeProjects formatting ──────────────────────────────────────────────────

func TestWriteProjects(t *testing.T) {
	proj := &model.Project{
		Path:  "/repos/myrepo",
		Name:  "myrepo",
		IsGit: true,
	}

	tests := []struct {
		name     string
		projects []*discover.ProjectTrees
		want     string
	}{
		{
			// Header: "Name  Path\n"
			// Main tree (IsMain=true):  "  * <branch>  <path>\n"
			// Linked tree (IsMain=false): "    <branch>  <path>\n"  (marker=" " gives 3 spaces total)
			name: "one project two trees main marked with star",
			projects: []*discover.ProjectTrees{
				{
					Project: *proj,
					Trees: []model.Tree{
						{Path: "/repos/myrepo", Branch: "main", IsMain: true, Project: proj},
						{Path: "/repos/myrepo-feat", Branch: "feat/foo", IsMain: false, Project: proj},
					},
				},
			},
			want: "myrepo  /repos/myrepo\n" +
				"  * main  /repos/myrepo\n" +
				"    feat/foo  /repos/myrepo-feat\n",
		},
		{
			// Empty branch: "  * <empty>  <path>\n" → "  *   <path>\n" (branch="" → two spaces between * and path's two-space prefix)
			name: "tree with empty branch degrades cleanly",
			projects: []*discover.ProjectTrees{
				{
					Project: *proj,
					Trees: []model.Tree{
						{Path: "/repos/myrepo", Branch: "", IsMain: true, Project: proj},
					},
				},
			},
			want: "myrepo  /repos/myrepo\n" +
				"  *   /repos/myrepo\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			writeProjects(&buf, tc.projects)
			got := buf.String()
			if got != tc.want {
				t.Errorf("writeProjects output mismatch\ngot:  %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// ── bootstrap: fallback to direct TUI when frame.Ensure fails ────────────────

// TestBootstrap_FallbackOnEnsureError verifies that when frame.Ensure fails
// (e.g. tmux unavailable), bootstrap calls the fallback function instead of
// attaching, and exits 0 (the fallback exit code).
func TestBootstrap_FallbackOnEnsureError(t *testing.T) {
	dir := t.TempDir()

	fallbackCalled := false
	attachCalled := false

	// Wire a FakeRunner that returns an exec-layer error on has-session so
	// frame.Ensure propagates it back to bootstrap.
	r := proc.NewFakeRunner()
	// has-session error: exec-layer failure (ExitCode == -1)
	r.Respond(proc.FakeResult{Err: &execLayerError{}},
		"tmux", "has-session", "-t", "=perch")

	deps := bootstrapDeps{
		tmuxClient: tmux.Tmux{Runner: r, Bin: "tmux"},
		executable: func() (string, error) { return "/usr/local/bin/perch", nil },
		fallback: func(root string, stdout, stderr io.Writer) int {
			fallbackCalled = true
			return 0
		},
		attach: func(_ context.Context, _ tmux.Tmux, _ string) int {
			attachCalled = true
			return 0
		},
	}

	var out, errBuf strings.Builder
	code := bootstrap(deps, dir, &out, &errBuf)
	if code != 0 {
		t.Errorf("expected exit 0 from fallback, got %d", code)
	}
	if !fallbackCalled {
		t.Error("expected fallback to be called when frame.Ensure fails")
	}
	if attachCalled {
		t.Error("attach must not be called when falling back")
	}
	// stderr should contain a "frame setup" warning
	if !strings.Contains(errBuf.String(), "frame setup") {
		t.Errorf("expected 'frame setup' warning on stderr; got: %q", errBuf.String())
	}
}

// TestBootstrap_AttachOnSuccess verifies that when frame.Ensure succeeds,
// bootstrap calls attach (not fallback) and returns attach's exit code.
func TestBootstrap_AttachOnSuccess(t *testing.T) {
	dir := t.TempDir()

	attachCalled := false
	fallbackCalled := false

	r := proc.NewFakeRunner()
	// has-session → session absent (exit 1 from tmux means "not found")
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=perch")
	// Use Default to succeed everything else (new-session, send-keys×2,
	// set-option, split-window, display-message, resize-pane).
	r.Default = &proc.FakeResult{Stdout: []byte("%1\n")}

	deps := bootstrapDeps{
		tmuxClient: tmux.Tmux{Runner: r, Bin: "tmux"},
		executable: func() (string, error) { return "/usr/local/bin/perch", nil },
		fallback: func(root string, stdout, stderr io.Writer) int {
			fallbackCalled = true
			return 0
		},
		attach: func(_ context.Context, _ tmux.Tmux, frameSession string) int {
			attachCalled = true
			if frameSession != "perch" {
				t.Errorf("attach: frameSession=%q, want perch", frameSession)
			}
			return 0
		},
	}

	var out, errBuf strings.Builder
	code := bootstrap(deps, dir, &out, &errBuf)
	if code != 0 {
		t.Errorf("expected exit 0 from attach, got %d (stderr: %q)", code, errBuf.String())
	}
	if !attachCalled {
		t.Error("expected attach to be called on success")
	}
	if fallbackCalled {
		t.Error("fallback must not be called on success")
	}
}

// ── --sidebar routes to sidebar() with frame fields wired ────────────────────

// TestSidebar_FrameContextWiredThrough verifies that sidebar() calls
// frame.SidebarContext, discovers the sibling pane, and passes FrameSession +
// PlaceholderPane through to runTUI — without starting a real Bubble Tea
// program.
func TestSidebar_FrameContextWiredThrough(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	r := proc.NewFakeRunner()
	// CurrentClientWindow → session="perch", window="frame"
	r.Respond(proc.FakeResult{Stdout: []byte("perch\x1fframe\n")},
		"tmux", "display-message", "-p", "-F", "#{session_name}\x1f#{window_name}")
	// list-panes on the frame window → sidebar (%1) + placeholder (%2)
	listPanesOut := "%1\x1f100\x1fperch\x1f0\x1f/root\x1fperch\x1fframe\x1f\x1f\n" +
		"%2\x1f101\x1fsleep\x1f0\x1f/root\x1fperch\x1fframe\x1f\x1f\n"
	r.Respond(proc.FakeResult{Stdout: []byte(listPanesOut)},
		"tmux", "list-panes", "-t", "=perch:=frame", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")

	var capturedCfg tui.Config
	tuiCalled := false

	deps := sidebarDeps{
		tmuxClient: tmux.Tmux{Runner: r, Bin: "tmux"},
		getenv: func(k string) string {
			if k == "TMUX_PANE" {
				return "%1"
			}
			return ""
		},
		runTUI: func(_ context.Context, cfg tui.Config) error {
			tuiCalled = true
			capturedCfg = cfg
			return nil
		},
	}

	var out, errBuf strings.Builder
	code := sidebar(deps, nil, &out, &errBuf)
	if code != 0 {
		t.Errorf("sidebar: expected exit 0, got %d (stderr: %q)", code, errBuf.String())
	}
	if !tuiCalled {
		t.Fatal("runTUI was not called")
	}
	if capturedCfg.FrameSession != "perch" {
		t.Errorf("cfg.FrameSession=%q, want perch", capturedCfg.FrameSession)
	}
	if capturedCfg.PlaceholderPane != "%2" {
		t.Errorf("cfg.PlaceholderPane=%q, want %%2", capturedCfg.PlaceholderPane)
	}
}

// TestSidebar_DirectModeWhenNoFrameContext verifies that when SidebarContext
// fails (TMUX_PANE empty), sidebar() still calls runTUI with empty frame fields
// (direct-TUI mode).
func TestSidebar_DirectModeWhenNoFrameContext(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	r := proc.NewFakeRunner()
	tuiCalled := false

	deps := sidebarDeps{
		tmuxClient: tmux.Tmux{Runner: r, Bin: "tmux"},
		getenv:     func(string) string { return "" }, // TMUX_PANE=""
		runTUI: func(_ context.Context, cfg tui.Config) error {
			tuiCalled = true
			if cfg.FrameSession != "" || cfg.PlaceholderPane != "" {
				return fmt.Errorf("expected empty frame fields in direct mode; got session=%q placeholder=%q",
					cfg.FrameSession, cfg.PlaceholderPane)
			}
			return nil
		},
	}

	var out, errBuf strings.Builder
	code := sidebar(deps, nil, &out, &errBuf)
	if code != 0 {
		t.Errorf("sidebar direct-mode: expected exit 0, got %d (stderr: %q)", code, errBuf.String())
	}
	if !tuiCalled {
		t.Fatal("runTUI was not called in direct-mode path")
	}
}

// TestRun_Sidebar_Routes verifies that run() dispatches "--sidebar" to the
// sidebar handler (not the path-arg handler). We call run() and accept any
// non-2 exit code, because exit 2 is the path-arg "not a directory" failure —
// proves routing is correct without launching a real TUI (the sidebar() core
// is tested above via the injected seam).
func TestRun_Sidebar_Routes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, _, code := callRun([]string{"--sidebar"})
	// exit 2 means the arg was treated as a bad path — routing failed.
	if code == 2 {
		t.Error("--sidebar must not route to the path-arg handler (exit 2 = bad routing)")
	}
}

// ── exec-layer error helper ───────────────────────────────────────────────────

// execLayerError simulates an exec-layer failure (binary missing / PATH issue).
// proc.ExitCode returns -1 for errors that don't implement ExitCode().
type execLayerError struct{}

func (e *execLayerError) Error() string { return "exec: no such file or directory" }
