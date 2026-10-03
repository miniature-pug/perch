package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// TestApp_WorkspaceForBranch_JSONShape is the APP-1 regression guard. Wails
// resolves a (string, bool) method to its first value only, so the binding
// must return one value that marshals to {id, found}.
func TestApp_WorkspaceForBranch_JSONShape(t *testing.T) {
	l := newLifecycleApp(t, nil)
	_ = l.store.Upsert(registry.Workspace{ID: "ws-b", RepoPath: "/r", WorktreePath: "/r__worktrees/x", Worktree: true, Branch: "x"})
	b, err := json.Marshal(l.WorkspaceForBranch("/r", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"id":"ws-b","found":true}` {
		t.Errorf("WorkspaceForBranch JSON = %s, want {\"id\":\"ws-b\",\"found\":true}", b)
	}
	b, _ = json.Marshal(l.WorkspaceForBranch("/r", "y"))
	if string(b) != `{"id":"","found":false}` {
		t.Errorf("miss JSON = %s", b)
	}
}

// TestApp_GetSettings_AlwaysRulesNeverNull is the APP-5 regression guard.
func TestApp_GetSettings_AlwaysRulesNeverNull(t *testing.T) {
	l := newLifecycleApp(t, nil)
	check := func(when string) {
		t.Helper()
		s, err := l.GetSettings()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(s)
		if !strings.Contains(string(b), `"alwaysRules":[]`) {
			t.Errorf("%s: settings JSON = %s, want alwaysRules []", when, b)
		}
		if s.Theme == "" || s.Density == "" || s.Font == "" {
			t.Errorf("%s: blank appearance fields: %+v", when, s)
		}
	}
	check("no file")
	if err := os.WriteFile(l.settingsPath, []byte(`{"theme":"","alwaysRules":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	check("null rules on disk")
	if err := os.WriteFile(l.settingsPath, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	check("corrupt file")
}

// TestApp_OpenWorkspace_MissingTreeRefused is the APP-6 regression guard: a
// session whose tree was deleted outside perch is not reopened (the agent
// would run in an empty non-git directory), and nothing is prepared.
func TestApp_OpenWorkspace_MissingTreeRefused(t *testing.T) {
	repo := t.TempDir()
	l := newLifecycleApp(t, []string{repo})
	gone := filepath.Join(repo, "deleted")
	noGit := t.TempDir() // exists, but has no .git: a recreated directory
	_ = l.store.Upsert(registry.Workspace{ID: "ws-gone", RepoPath: repo, WorktreePath: gone, Worktree: true, Agent: "claude"})
	_ = l.store.Upsert(registry.Workspace{ID: "ws-nogit", RepoPath: repo, WorktreePath: noGit, Worktree: true, Agent: "claude"})
	for _, id := range []string{"ws-gone", "ws-nogit"} {
		if err := l.OpenWorkspace(id); !errors.Is(err, ErrWorktreeMissing) {
			t.Errorf("OpenWorkspace(%s) = %v, want ErrWorktreeMissing", id, err)
		}
	}
	if l.lastMonitor() != nil {
		t.Error("a monitor was created for a missing tree")
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Error("OpenWorkspace recreated the deleted tree")
	}
}

// TestApp_OpenShell_DisplaceDoesNotEmitExit is the APP-11 regression guard:
// re-opening a live shell pane must not let the displaced shell emit the
// shared pty:exit event, which the frontend would read as the new shell
// exiting.
func TestApp_OpenShell_DisplaceDoesNotEmitExit(t *testing.T) {
	wt := t.TempDir()
	l := newLifecycleApp(t, []string{wt})
	_ = l.store.Upsert(registry.Workspace{ID: "ws-sh", WorktreePath: wt, Agent: "claude"})
	l.spawnPty = func(ctx context.Context, cwd string, _ []string, env []string, dataEvent, exitEvent string,
		emit internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error) {
		return internalpty.SpawnBase64(ctx, cwd, []string{"sleep", "3600"}, env, dataEvent, exitEvent, emit, cols, rows)
	}
	for i := 0; i < 2; i++ {
		if err := l.OpenShell("shell-ws-sh", wt); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = l.CloseShell("shell-ws-sh") }()
	time.Sleep(500 * time.Millisecond)
	for _, r := range l.snapshot() {
		if r.event == "pty:exit:shell-ws-sh" {
			t.Fatal("the displaced shell emitted pty:exit on the shared event name")
		}
	}
}

// TestApp_WriteFile_NewFileUnderSymlinkedRoot is the APP-12 regression
// guard: creating a file must work when the root (or an ancestor) is a
// symlink, and must land in the real directory.
func TestApp_WriteFile_NewFileUnderSymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	l := newLifecycleApp(t, []string{link})
	if err := l.WriteFile(filepath.Join(link, "new.txt"), "hello"); err != nil {
		t.Fatalf("WriteFile under a symlinked root: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(real, "new.txt")); err != nil || string(got) != "hello" {
		t.Errorf("real/new.txt = %q, %v", got, err)
	}
	// Escapes are still refused.
	outside := t.TempDir()
	if err := l.WriteFile(filepath.Join(outside, "x.txt"), "x"); err == nil {
		t.Error("WriteFile outside the roots = nil, want error")
	}
	if err := os.Symlink(outside, filepath.Join(real, "esc")); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteFile(filepath.Join(link, "esc", "y.txt"), "y"); err == nil {
		t.Error("WriteFile through a symlinked dir that leaves the root = nil, want error")
	}
}

// TestApp_CreateWorkspace_InRepoRefusesSharedCheckoutSwitch is the APP-18
// regression guard: an in-repo session must not switch the branch under
// another open session in the same checkout.
func TestApp_CreateWorkspace_InRepoRefusesSharedCheckoutSwitch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := makeTestRepo(t, root)
	runGit(t, repo, "branch", "feat")
	l := newLifecycleApp(t, []string{root})
	first, err := l.CreateWorkspace("claude", repo, "", "main", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.OpenWorkspace(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CreateWorkspace("claude", repo, "", "feat", "", false); !errors.Is(err, ErrCheckoutInUse) {
		t.Fatalf("CreateWorkspace switching an open session's checkout = %v, want ErrCheckoutInUse", err)
	}
	// Attaching to the current branch needs no switch and stays allowed.
	if _, err := l.CreateWorkspace("claude", repo, "", "main", "", false); err != nil {
		t.Errorf("CreateWorkspace on the current branch: %v", err)
	}
	// Once the other session is closed, the switch is allowed.
	_ = l.CloseWorkspace(first.ID)
	if _, err := l.CreateWorkspace("claude", repo, "", "feat", "", false); err != nil {
		t.Errorf("CreateWorkspace after closing the other session: %v", err)
	}
}

// TestApp_OpenShell_ValidatesPaneAndCwd is the APP-21 regression guard.
func TestApp_OpenShell_ValidatesPaneAndCwd(t *testing.T) {
	wt := t.TempDir()
	other := t.TempDir()
	l := newLifecycleApp(t, []string{wt, other})
	_ = l.store.Upsert(registry.Workspace{ID: "ws-v", WorktreePath: wt, Agent: "claude"})
	var spawnedCwd []string
	l.spawnPty = func(_ context.Context, cwd string, _ []string, _ []string, _, _ string,
		_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
		spawnedCwd = append(spawnedCwd, cwd)
		return internalpty.NewBridgeForTest(func() error { return nil }), nil
	}
	for _, tc := range []struct{ pane, cwd string }{
		{"pane-ws-v", wt},       // an agent pane id
		{"shell-", wt},          // no workspace id
		{"shell-nope", wt},      // unknown workspace
		{"shell-ws-v", other},   // under a root, but not the workspace's tree
		{"random", wt},          // not a shell pane
		{"shell-ws-v_1", "/"},   // outside every root
		{"shell-ws-v_2", "rel"}, // relative
	} {
		if err := l.OpenShell(tc.pane, tc.cwd); err == nil {
			t.Errorf("OpenShell(%q, %q) = nil, want error", tc.pane, tc.cwd)
		}
	}
	if len(spawnedCwd) != 0 {
		t.Fatalf("rejected calls spawned shells: %v", spawnedCwd)
	}
	sub := filepath.Join(wt, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.OpenShell("shell-ws-v_3", sub); err != nil {
		t.Errorf("OpenShell in a subdirectory of the tree: %v", err)
	}
	if err := l.OpenShell(homeShellPaneID, "/etc"); err != nil {
		t.Fatalf("OpenShell(home): %v", err)
	}
	if got := spawnedCwd[len(spawnedCwd)-1]; got != l.HomeShellCwd() {
		t.Errorf("home shell cwd = %q, want HomeShellCwd %q (the IPC cwd must be ignored)", got, l.HomeShellCwd())
	}
}

// TestApp_ScheduleTitleUpdate_NoopAfterShutdown is the APP-22d regression
// guard: a pump event that lands after shutdown must not re-arm the title
// timer.
func TestApp_ScheduleTitleUpdate_NoopAfterShutdown(t *testing.T) {
	a := &App{bridges: map[string]*internalpty.Bridge{}, monitors: map[string]agent.Monitor{}}
	a.shutdown(context.Background())
	a.scheduleTitleUpdate()
	a.titleMu.Lock()
	defer a.titleMu.Unlock()
	if a.titleTimer != nil {
		t.Error("scheduleTitleUpdate armed a timer after shutdown")
	}
}

// TestNewApp_NormalizesRelativeRoots is the APP-4 regression guard.
func TestNewApp_NormalizesRelativeRoots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	a := NewApp(store, []string{".", "", dir})
	if len(a.roots) != 1 || !filepath.IsAbs(a.roots[0]) {
		t.Fatalf("roots = %v, want one absolute root", a.roots)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ListDir(sub); err != nil {
		t.Errorf("ListDir under a root given as \".\": %v", err)
	}
}

// TestApp_DiscoverRepos_SortedAcrossRoots guards the APP-23 DiscoverRepos
// fix: repositories from several roots come back as one sorted list.
func TestApp_DiscoverRepos_SortedAcrossRoots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rootA, rootB := t.TempDir(), t.TempDir()
	for _, p := range []string{filepath.Join(rootA, "zeta"), filepath.Join(rootB, "alpha")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, p, "init", "-q")
	}
	l := newLifecycleApp(t, []string{rootA, rootB})
	repos, err := l.DiscoverRepos()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "alpha,zeta" {
		t.Errorf("DiscoverRepos names = %v, want [alpha zeta]", names)
	}
}
