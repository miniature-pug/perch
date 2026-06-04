package main

import (
	"io"
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

func TestRun_Setup_Replace_Exit0(t *testing.T) {
	// setup --replace must exit 0 and report "replaced" (or "not found") for each
	// tool — the old "not supported" stub must be gone.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	out, _, code := callRun([]string{"setup", "--replace"})
	if code != 0 {
		t.Errorf("expected exit 0 for setup --replace, got %d", code)
	}
	// The stub message must never appear.
	if strings.Contains(out, "--replace not supported") {
		t.Errorf("stub '--replace not supported' message still present; got: %q", out)
	}
	// Output must still mention "setup:" (at least one tool line or "no tools").
	if !strings.Contains(out, "setup:") {
		t.Errorf("expected 'setup:' prefix in output; got: %q", out)
	}
}

// TestSetupMessage tests the pure setupMessage helper that generates
// human-readable setup output. This directly verifies the "replaced" vs
// "installed" message divergence without PATH-dependent detection.
func TestSetupMessage_Claude(t *testing.T) {
	got := setupMessage("claude", false)
	if !strings.Contains(got, "installed") || strings.Contains(got, "replaced") {
		t.Errorf("additive claude message: want 'installed', got: %q", got)
	}
	got = setupMessage("claude", true)
	if !strings.Contains(got, "replaced") || strings.Contains(got, "installed") {
		t.Errorf("replace claude message: want 'replaced', got: %q", got)
	}
	if !strings.Contains(got, "~/.claude/settings.json") {
		t.Errorf("replace claude message missing path: %q", got)
	}
}

func TestSetupMessage_Opencode(t *testing.T) {
	got := setupMessage("opencode", false)
	if !strings.Contains(got, "installed") {
		t.Errorf("additive opencode message: want 'installed', got: %q", got)
	}
	got = setupMessage("opencode", true)
	if !strings.Contains(got, "replaced") {
		t.Errorf("replace opencode message: want 'replaced', got: %q", got)
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
	// (git, etc.) regardless of whether those tools are present.
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
	_, errOut, _ := callRun([]string{"doctr"})
	for _, hidden := range []string{"debug", "resurrect", "status"} {
		if strings.Contains(errOut, hidden) {
			t.Errorf("printUsage must not mention %q; stderr: %q", hidden, errOut)
		}
	}
	for _, visible := range []string{"setup", "doctor", "version", "attach"} {
		if !strings.Contains(errOut, visible) {
			t.Errorf("printUsage must mention surviving verb %q; stderr: %q", visible, errOut)
		}
	}
}

// ── removed verbs → exit 2 ────────────────────────────────────────────────────

func TestRun_RemovedVerbs_Exit2(t *testing.T) {
	for _, verb := range []string{"resurrect", "status"} {
		t.Run(verb, func(t *testing.T) {
			_, errOut, code := callRun([]string{verb})
			if code != 2 {
				t.Errorf("removed verb %q: want exit 2, got %d", verb, code)
			}
			if !strings.Contains(errOut, "Usage") {
				t.Errorf("removed verb %q: want Usage on stderr; got %q", verb, errOut)
			}
		})
	}
}

// ── attach command ────────────────────────────────────────────────────────────

func TestRun_NoArgs_CallsLaunchGUI(t *testing.T) {
	launched := false
	old := launchGUI
	launchGUI = func(_ []string) error { launched = true; return nil }
	defer func() { launchGUI = old }()

	code := run([]string{}, io.Discard, io.Discard)
	if code != 0 {
		t.Errorf("run() = %d, want 0", code)
	}
	if !launched {
		t.Error("launchGUI must be called with no args")
	}
}

func TestRun_Attach_FocusesWorkspace(t *testing.T) {
	// attach <query> must call launchGUI (which the SingleInstanceLock will
	// forward to a running instance, or start a fresh GUI when none is running).
	orig := launchGUI
	t.Cleanup(func() { launchGUI = orig })
	launched := false
	launchGUI = func(_ []string) error { launched = true; return nil }

	_, _, code := callRun([]string{"attach", "my-feature"})
	if code != 0 {
		t.Errorf("attach with query: want exit 0, got %d", code)
	}
	if !launched {
		t.Error("attach must call launchGUI to forward to a running instance or start a fresh GUI")
	}
}

func TestRun_Attach_NoArgs_Exit2(t *testing.T) {
	// attach with no query must print usage and return 2.
	_, stderr, code := callRun([]string{"attach"})
	if code != 2 {
		t.Errorf("attach with no args: want exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "Usage") {
		t.Errorf("attach with no args: want Usage on stderr; got %q", stderr)
	}
}

func TestRun_ResurrectRemoved_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"resurrect"})
	if code != 2 {
		t.Errorf("resurrect: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("resurrect: want Usage on stderr; got %q", errOut)
	}
}

func TestRun_StatusRemoved_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"status", "set", "working"})
	if code != 2 {
		t.Errorf("status: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("status: want Usage on stderr; got %q", errOut)
	}
}

func TestPrintUsage_ShowsAttach(t *testing.T) {
	_, errOut, _ := callRun([]string{"doctr"}) // unknown arg → usage
	for _, must := range []string{"setup", "doctor", "version", "attach"} {
		if !strings.Contains(errOut, must) {
			t.Errorf("printUsage must mention %q; stderr: %q", must, errOut)
		}
	}
	for _, hidden := range []string{"resurrect", "status", "debug"} {
		if strings.Contains(errOut, hidden) {
			t.Errorf("printUsage must not mention %q; stderr: %q", hidden, errOut)
		}
	}
}

// ── writeProjects formatting ──────────────────────────────────────────────────

func TestWriteProjects(t *testing.T) {
	proj := &model.Project{
		Path: "/repos/myrepo",
		Name: "myrepo",
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

// ── GUI launch seam ───────────────────────────────────────────────────────────

// TestRun_NoArgs_LaunchesGUI verifies the default invocation routes to the GUI
// seam (not the path-arg handler) and propagates its success.
func TestRun_NoArgs_LaunchesGUI(t *testing.T) {
	orig := launchGUI
	t.Cleanup(func() { launchGUI = orig })
	called := false
	launchGUI = func(roots []string) error { called = true; return nil }

	code := run(nil, io.Discard, io.Discard)
	if !called {
		t.Error("default invocation must call launchGUI")
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// TestRun_ValidPath_LaunchesGUI verifies a directory arg also launches the GUI.
func TestRun_ValidPath_LaunchesGUI(t *testing.T) {
	orig := launchGUI
	t.Cleanup(func() { launchGUI = orig })
	var gotRoots []string
	launchGUI = func(roots []string) error { gotRoots = roots; return nil }

	dir := t.TempDir()
	code := run([]string{dir}, io.Discard, io.Discard)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(gotRoots) == 0 {
		t.Error("expected guiRoots to supply at least one root")
	}
}
