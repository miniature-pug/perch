package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/miniature-pug/perch/app"
	"github.com/miniature-pug/perch/internal/discover"
	"github.com/miniature-pug/perch/internal/envsync"
	"github.com/miniature-pug/perch/internal/model"
	"github.com/miniature-pug/perch/internal/registry"
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
	// This test does not fully control doctor's environment, but it can assert
	// that the verb "doctor" actually dispatches into doctor.Run (not a stub).
	// doctor.Run always emits a "\nperch <version>\n" header and then tool rows
	// (git, and so on), regardless of whether those tools are present.
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
	for _, hidden := range []string{"debug", "resurrect", "status", "setup"} {
		if strings.Contains(errOut, hidden) {
			t.Errorf("printUsage must not mention %q; stderr: %q", hidden, errOut)
		}
	}
	for _, visible := range []string{"doctor", "version", "attach"} {
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
	for _, must := range []string{"doctor", "version", "attach"} {
		if !strings.Contains(errOut, must) {
			t.Errorf("printUsage must mention %q; stderr: %q", must, errOut)
		}
	}
	for _, hidden := range []string{"resurrect", "status", "debug", "setup"} {
		if strings.Contains(errOut, hidden) {
			t.Errorf("printUsage must not mention %q; stderr: %q", hidden, errOut)
		}
	}
}

// ── reload command ────────────────────────────────────────────────────────────

// TestRun_Reload_IsARealCommand mirrors the not-a-command tests (setup,
// status, resurrect): `reload` must dispatch to its own handler, and must
// NOT fall through to the path-arg handler. Outside a perch session
// (PERCH_ENVSYNC_* absent), it prints a friendly error and exits non-zero,
// but it never prints Usage (which would prove a fall-through), and it
// never exits 2.
func TestRun_Reload_IsARealCommand(t *testing.T) {
	t.Setenv("PERCH_ENVSYNC_URL", "")
	t.Setenv("PERCH_ENVSYNC_TOKEN", "")
	t.Setenv("PERCH_ENVSYNC_WS", "")

	_, errOut, code := callRun([]string{"reload"})
	if code == 0 {
		t.Errorf("reload outside a session: want non-zero exit, got 0")
	}
	if code == 2 {
		t.Errorf("reload must not fall through to path handling (exit 2); got a real dispatch")
	}
	if strings.Contains(errOut, "Usage") {
		t.Errorf("reload is a real command; stderr must not contain Usage: %q", errOut)
	}
	if !strings.Contains(errOut, "session") {
		t.Errorf("reload outside a session should hint at the perch session terminal; got %q", errOut)
	}
}

// TestRun_Reload_PostsEnvWhenInSession verifies that with the PERCH_ENVSYNC_*
// handles present, `reload` POSTs its environment and workspace id to the endpoint
// with the Bearer token, and exits 0.
func TestRun_Reload_PostsEnvWhenInSession(t *testing.T) {
	const token = "test-token-abc"
	var mu sync.Mutex
	var (
		gotAuth   string
		gotMethod string
		gotReq    envsync.SyncRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("PERCH_ENVSYNC_URL", srv.URL)
	t.Setenv("PERCH_ENVSYNC_TOKEN", token)
	t.Setenv("PERCH_ENVSYNC_WS", "ws-x")
	t.Setenv("PERCH_RELOAD_TEST_VAR", "captured")

	out, errOut, code := callRun([]string{"reload"})
	if code != 0 {
		t.Fatalf("reload in session: want exit 0, got %d (stderr: %q)", code, errOut)
	}
	if out == "" {
		t.Errorf("reload should print a confirmation on success")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer "+token)
	}
	if gotReq.WorkspaceID != "ws-x" {
		t.Errorf("body workspace_id = %q, want ws-x", gotReq.WorkspaceID)
	}
	found := false
	for _, e := range gotReq.Env {
		if e == "PERCH_RELOAD_TEST_VAR=captured" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("posted env must include the caller's own os.Environ(); missing PERCH_RELOAD_TEST_VAR")
	}
}

// TestRun_Reload_NeverLaunchesGUI proves the reload path never touches the GUI
// seam, in or out of a session.
func TestRun_Reload_NeverLaunchesGUI(t *testing.T) {
	t.Setenv("PERCH_ENVSYNC_URL", "")
	t.Setenv("PERCH_ENVSYNC_TOKEN", "")
	t.Setenv("PERCH_ENVSYNC_WS", "")

	orig := launchGUI
	t.Cleanup(func() { launchGUI = orig })
	launched := false
	launchGUI = func([]string) error { launched = true; return nil }

	_, _, _ = callRun([]string{"reload"})
	if launched {
		t.Error("reload must never launch the GUI")
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

// ── guiRoots ──────────────────────────────────────────────────────────────────

// TestGuiRoots_ConfigErrorReported is the MSC-10 regression guard: a broken
// config.toml is reported on stderr, not silently replaced by the cwd.
func TestGuiRoots_ConfigErrorReported(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	if err := os.MkdirAll(filepath.Join(cfgHome, "perch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgHome, "perch", "config.toml"), []byte("roots = [unterminated"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var stderr bytes.Buffer
	roots := guiRoots(dir, false, &stderr)
	if !strings.Contains(stderr.String(), "config") {
		t.Errorf("stderr = %q, want a config error", stderr.String())
	}
	if len(roots) != 1 || roots[0] != dir {
		t.Errorf("roots = %v, want [%s]", roots, dir)
	}
}

// TestGuiRoots_RepoAddsSiblingWorktreeDir is the APP-3 regression guard: a
// launch inside a repo with no configured roots also covers the sibling
// <repo>__worktrees directory, where worktree sessions are created.
func TestGuiRoots_RepoAddsSiblingWorktreeDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "myrepo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := guiRoots(repo, false, io.Discard)
	want := []string{repo, repo + "__worktrees"}
	if strings.Join(roots, "|") != strings.Join(want, "|") {
		t.Errorf("roots = %v, want %v", roots, want)
	}
	plain := t.TempDir()
	if roots := guiRoots(plain, false, io.Discard); len(roots) != 1 || roots[0] != plain {
		t.Errorf("non-repo roots = %v, want [%s]", roots, plain)
	}
}

// TestGuiRoots_ExplicitPathJoinsConfiguredRoots is the APP-22c regression
// guard: `perch <path>` covers <path> even when config.toml sets roots.
func TestGuiRoots_ExplicitPathJoinsConfiguredRoots(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	configured := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgHome, "perch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgHome, "perch", "config.toml"), []byte(fmt.Sprintf("roots = [%q]\n", configured)), 0o600); err != nil {
		t.Fatal(err)
	}
	named := t.TempDir()
	if roots := guiRoots(named, true, io.Discard); strings.Join(roots, "|") != named+"|"+configured {
		t.Errorf("explicit roots = %v, want [%s %s]", roots, named, configured)
	}
	if roots := guiRoots(named, false, io.Discard); strings.Join(roots, "|") != configured {
		t.Errorf("implicit roots = %v, want [%s]", roots, configured)
	}
}

// TestRun_RelativePath_PassesAbsoluteRoot is the APP-4 regression guard.
func TestRun_RelativePath_PassesAbsoluteRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	orig := launchGUI
	t.Cleanup(func() { launchGUI = orig })
	var gotRoots []string
	launchGUI = func(roots []string) error { gotRoots = roots; return nil }
	dir := t.TempDir()
	t.Chdir(dir)
	if code := run([]string{"."}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if len(gotRoots) == 0 || !filepath.IsAbs(gotRoots[0]) {
		t.Errorf("roots = %v, want an absolute first root", gotRoots)
	}
}

// initCommittedRepo creates a git repo with one commit at dir.
func initCommittedRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "i"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// TestGuiRoots_SymlinkedLaunchDirCoversWorktrees is the review #3
// regression guard: launched in a repo reached through a symlinked
// directory, discovery reports the resolved repo path, so the worktree path
// is resolved too, and the roots must cover it.
func TestGuiRoots_SymlinkedLaunchDirCoversWorktrees(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	real := t.TempDir()
	initCommittedRepo(t, filepath.Join(real, "r"))
	link := filepath.Join(t.TempDir(), "code")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	roots := guiRoots(filepath.Join(link, "r"), false, io.Discard)
	store, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := app.NewApp(store, roots)
	repos, err := a.DiscoverRepos()
	if err != nil || len(repos) == 0 {
		t.Fatalf("DiscoverRepos = %v, %v", repos, err)
	}
	if _, err := a.CreateWorkspace("claude", repos[0].Path, "HEAD", "feat-x", "", true); err != nil {
		t.Errorf("CreateWorkspace(worktree) from a symlinked launch dir (roots %v): %v", roots, err)
	}
}

// TestGuiRoots_ExplicitRepoWithConfigCoversWorktrees is the review #4
// regression guard: `perch <repo>` with configured roots also covers the
// repo's sibling worktree directory.
func TestGuiRoots_ExplicitRepoWithConfigCoversWorktrees(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	configured := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgHome, "perch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgHome, "perch", "config.toml"), []byte(fmt.Sprintf("roots = [%q]\n", configured)), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "r")
	initCommittedRepo(t, repo)
	roots := guiRoots(repo, true, io.Discard)
	want := []string{repo, repo + "__worktrees", configured}
	if strings.Join(roots, "|") != strings.Join(want, "|") {
		t.Errorf("roots = %v, want %v", roots, want)
	}
}
