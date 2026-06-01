package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/model"
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
