package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/registry"
)

func TestValidateSessionID_AllowlistCharset(t *testing.T) {
	good := []string{"ses_18593fc84ffeg4oyInzAG2eLOL", "2b96f5bc-43ef-454d-a12d-791ad68da8dd", "win-1"}
	for _, s := range good {
		if err := validateSessionID(s); err != nil {
			t.Errorf("validateSessionID(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{"", "a b", "a;b", "../x", "a\x1fb", "a\nb", "a/b"}
	for _, s := range bad {
		if err := validateSessionID(s); err == nil {
			t.Errorf("validateSessionID(%q) = nil, want error", s)
		}
	}
}

func TestValidateSessionID_AdversarialCases(t *testing.T) {
	rejected := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"overlong", strings.Repeat("a", 129)},
		{"control NUL", "ses\x00id"},
		{"control LF", "ses\nid"},
		{"shell semicolon", "ses;id"},
		{"shell dollar-paren", "ses$(id)"},
		{"shell backtick", "ses`id`"},
		{"shell pipe", "ses|id"},
		{"path separator slash", "ses/id"},
		{"path traversal dotdot", "../etc/passwd"},
		{"unicode lookalike", "ses‐id"}, // U+2010 HYPHEN, not ASCII '-'
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSessionID(tc.input); err == nil {
				t.Errorf("validateSessionID(%q) = nil, want error", tc.input)
			}
		})
	}
	t.Run("normal allowlisted id", func(t *testing.T) {
		if err := validateSessionID("ses_abc-123"); err != nil {
			t.Errorf("validateSessionID(\"ses_abc-123\") = %v, want nil", err)
		}
	})
}

func TestValidateWorktreeUnderRoots(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "perch", "wt")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := []string{root}
	// ACCEPT: real path under root, and the root itself.
	if err := validateWorktreeUnderRoots(sub, roots); err != nil {
		t.Errorf("path under root rejected: %v", err)
	}
	if err := validateWorktreeUnderRoots(root, roots); err != nil {
		t.Errorf("path at root rejected: %v", err)
	}
	// REJECT.
	outside := t.TempDir() // a real directory NOT under root → tests containment
	for _, p := range []string{
		"/etc/passwd",                         // real, outside root (containment)
		outside,                               // real, outside root (containment)
		root + "/../secret",                   // non-clean (rejected before resolve)
		"relative/path",                       // not absolute
		"",                                    // empty
		filepath.Join(root, "does-not-exist"), // in-root but missing → resolve error
	} {
		if err := validateWorktreeUnderRoots(p, roots); err == nil {
			t.Errorf("validateWorktreeUnderRoots(%q) = nil, want error", p)
		}
	}
}

// TestValidateWorktreeUnderRoots_SymlinkEscape uses REAL on-disk symlinks so the
// EvalSymlinks containment check is actually exercised (not skipped). The escape
// link points at a real directory OUTSIDE the root, so EvalSymlinks resolves
// successfully and it is the prefix check — not a resolve error — that rejects it.
func TestValidateWorktreeUnderRoots_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	roots := []string{root}

	// ESCAPE → must be REJECTED by containment (target resolves successfully).
	outsideTarget := t.TempDir() // real dir outside root
	escapeLink := filepath.Join(root, "evil-link")
	if err := os.Symlink(outsideTarget, escapeLink); err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeUnderRoots(escapeLink, roots); err == nil {
		t.Error("symlink whose target escapes root must be rejected by containment")
	}

	// INSIDE → must be ACCEPTED.
	realInside := filepath.Join(root, "real")
	if err := os.MkdirAll(realInside, 0o755); err != nil {
		t.Fatal(err)
	}
	goodLink := filepath.Join(root, "good-link")
	if err := os.Symlink(realInside, goodLink); err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeUnderRoots(goodLink, roots); err != nil {
		t.Errorf("symlink resolving inside root must be accepted: %v", err)
	}
}

// TestContainedUnderRoots_SymlinkRoot verifies that containedUnderRoots accepts
// a treePath expressed via a symlinked root (e.g. the configured root is a
// symlink to a real directory) while still rejecting paths that escape all
// roots via an absolute path or a dotdot traversal.
func TestContainedUnderRoots_SymlinkRoot(t *testing.T) {
	realDir := t.TempDir()
	linkParent := t.TempDir()
	linkRoot := filepath.Join(linkParent, "link-root")
	if err := os.Symlink(realDir, linkRoot); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	// treePath is built from the LINK path (lexical only — it does not exist on disk yet).
	treePathViaLink := filepath.Join(linkRoot, "proj__worktrees", "feat-x")

	// PRIMARY ASSERTION: a treePath under a symlinked root MUST be accepted.
	if !containedUnderRoots(treePathViaLink, []string{linkRoot}) {
		t.Errorf("containedUnderRoots(%q, [%q]) = false, want true (symlinked root must not false-reject)", treePathViaLink, linkRoot)
	}

	// ESCAPE assertions: escapes must still be rejected under the symlinked root.
	if containedUnderRoots("/etc/feat-x", []string{linkRoot}) {
		t.Error("containedUnderRoots(\"/etc/feat-x\", symlinked root) = true, want false (absolute escape must be rejected)")
	}
	dotdotEscape := filepath.Clean(filepath.Join(linkRoot, "..", "..", "escape", "feat-x"))
	if containedUnderRoots(dotdotEscape, []string{linkRoot}) {
		t.Errorf("containedUnderRoots(%q, symlinked root) = true, want false (dotdot escape must be rejected)", dotdotEscape)
	}
}

// TestApp_CreateAgent_ContainmentGuard proves that a config-supplied
// worktreeDir with an adversarial value (absolute or dotdot-relative) causes
// containedUnderRoots to return false.
func TestApp_CreateAgent_ContainmentGuard(t *testing.T) {
	// Test the containedUnderRoots helper directly with adversarial treePaths.
	root := t.TempDir()
	roots := []string{root}

	// treePath inside root → accepted.
	insidePath := filepath.Join(root, "proj__worktrees", "feat-x")
	if !containedUnderRoots(insidePath, roots) {
		t.Error("treePath inside root must be accepted by containedUnderRoots")
	}

	// Adversarial: absolute path outside root (e.g. worktreeDir="/etc").
	if containedUnderRoots("/etc/feat-x", roots) {
		t.Error("treePath /etc/feat-x must be rejected (outside all roots)")
	}

	// Adversarial: dotdot escape that resolves outside root
	// (e.g. projectPath=root/proj, worktreeDir="../../escape" → root/../escape/feat-x).
	escapePath := filepath.Clean(filepath.Join(root, "proj", "..", "..", "escape", "feat-x"))
	if containedUnderRoots(escapePath, roots) {
		t.Errorf("treePath %q must be rejected (dotdot escape outside root)", escapePath)
	}

	// Edge case: treePath exactly equals root → accepted.
	if !containedUnderRoots(root, roots) {
		t.Error("treePath equal to root must be accepted")
	}
}

// fakeBridge is an observable closer for makeBridgeWithCloser.
type fakeBridge struct {
	closed int
	mu     sync.Mutex
}

func (f *fakeBridge) Close() error { f.mu.Lock(); defer f.mu.Unlock(); f.closed++; return nil }

func makeBridgeWithCloser(f *fakeBridge) *internalpty.Bridge {
	return internalpty.NewBridgeForTest(f.Close)
}

func TestNewApp_Fields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, err := registry.Load(cfgDir)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	a := NewApp(store, []string{"/tmp/root"})
	if a == nil {
		t.Fatal("NewApp returned nil")
	}
	if a.bridges == nil {
		t.Fatal("bridges map not initialised")
	}
	if a.monitors == nil {
		t.Fatal("monitors map not initialised")
	}
	if a.emit == nil {
		t.Fatal("emit seam must be non-nil before startup")
	}
}

func TestApp_Shutdown_ClosesBridgesAndTearsDownMonitors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	fb := &fakeBridge{}
	fm := agent.NewFakeMonitor(nil)

	a := &App{
		store:    store,
		roots:    []string{"/tmp"},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	a.bridges["pane-1"] = makeBridgeWithCloser(fb)
	a.monitors["ws1"] = fm

	a.shutdown(context.Background())

	if fb.closed == 0 {
		t.Fatal("shutdown must close all Bridges")
	}
	if !fm.TornDown() {
		t.Fatal("shutdown must call Monitor.Teardown on all monitors")
	}
}

func TestApp_Shutdown_Idempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{"/tmp"},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	a.shutdown(context.Background())
	a.shutdown(context.Background()) // must not panic
}

func TestApp_ListWorkspaces_FromRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-abc",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "my-feature",
	})

	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	vms := a.ListWorkspaces()
	if len(vms) != 1 {
		t.Fatalf("ListWorkspaces = %d items, want 1", len(vms))
	}
	if vms[0].ID != "ws-abc" {
		t.Errorf("ID = %q, want ws-abc", vms[0].ID)
	}
	if vms[0].Agent != "claude" {
		t.Errorf("Agent = %q, want claude", vms[0].Agent)
	}
	if vms[0].State != agent.StateIdle {
		t.Errorf("State = %q, want idle", vms[0].State)
	}
}

func TestApp_ListWorkspaces_LiveMonitorStatePropagated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-1", WorktreePath: wt, Agent: "claude", Title: "t"})

	fm := agent.NewFakeMonitor(nil)
	fm.SetState(agent.StateRunning)

	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{"ws-1": fm},
	}

	vms := a.ListWorkspaces()
	if len(vms) != 1 || vms[0].State != agent.StateRunning {
		t.Errorf("live monitor state not reflected; vms=%+v", vms)
	}
	if vms[0].Caps != fm.Capabilities() {
		t.Errorf("Caps not propagated from monitor")
	}
}

func TestApp_CreateWorkspace_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t",
			"commit", "--allow-empty", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	vm, err := a.CreateWorkspace("claude", repo, "feat/hello", "claude-opus-4-5")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if vm.ID == "" {
		t.Fatal("ID must be non-empty")
	}
	if vm.Agent != "claude" {
		t.Errorf("Agent = %q, want claude", vm.Agent)
	}
	if _, ok := store.Get(vm.ID); !ok {
		t.Fatal("workspace must be persisted to registry")
	}
}

func TestApp_CreateWorkspace_RejectsOutsideRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:    store,
		roots:    []string{t.TempDir()},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	if _, err := a.CreateWorkspace("claude", "/etc", "feat/x", ""); err == nil {
		t.Fatal("must reject path outside roots")
	}
}

func TestApp_CreateWorkspace_RejectsInvalidAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	sub := filepath.Join(root, "proj")
	_ = os.MkdirAll(sub, 0o755)
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if _, err := a.CreateWorkspace("ghost", sub, "feat/x", ""); err == nil {
		t.Fatal("must reject unknown agent")
	}
}

func TestApp_OpenWorkspace_WritesLaunchCmdAndEmitsEvents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-open",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "t",
	})

	fm := agent.NewFakeMonitor(nil)
	fm.SetLaunchCmd("claude --resume abc\n")

	var mu sync.Mutex
	var emitted []struct {
		event string
		data  []any
	}
	emit := func(event string, data ...any) {
		mu.Lock()
		emitted = append(emitted, struct {
			event string
			data  []any
		}{event, data})
		mu.Unlock()
	}

	var written []byte
	var writeMu sync.Mutex

	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     emit,
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
		spawnPty: func(ctx context.Context, cwd string, argv []string, event string,
			ef internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error) {
			b := internalpty.NewBridgeForTest(func() error { return nil })
			b.OverrideWriteForTest(func(p []byte) (int, error) {
				writeMu.Lock()
				written = append(written, p...)
				writeMu.Unlock()
				return len(p), nil
			})
			return b, nil
		},
		newMonitor: func(toolName string, _ agent.Adapter) (agent.Monitor, error) {
			return fm, nil
		},
	}

	if err := a.OpenWorkspace("ws-open"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		writeMu.Lock()
		got := string(written)
		writeMu.Unlock()
		if strings.Contains(got, "claude") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	writeMu.Lock()
	got := string(written)
	writeMu.Unlock()
	if !strings.Contains(got, "claude") {
		t.Errorf("launchCmd not written to pty; wrote: %q", got)
	}

	// Replay a fake event and assert it is re-emitted on "agent:event".
	fm.Replay(agent.Event{WorkspaceID: "ws-open", Kind: "state", State: agent.StateRunning})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range emitted {
		if e.event == "agent:event" {
			found = true
		}
	}
	if !found {
		t.Error("agent:event was not emitted after Monitor.Events() replay")
	}
}

func TestApp_OpenWorkspace_UnknownID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{t.TempDir()},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
	}
	if err := a.OpenWorkspace("no-such-id"); err == nil {
		t.Fatal("must error on unknown workspace ID")
	}
}

func TestApp_WriteToPty_RoutesToBridge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var written []byte
	var mu sync.Mutex
	br := internalpty.NewBridgeForTest(func() error { return nil })
	br.OverrideWriteForTest(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		written = append(written, p...)
		return len(p), nil
	})

	a := &App{
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{"pane-ws1": br},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.WriteToPty("pane-ws1", []int{104, 101, 108, 108, 111}); err != nil {
		t.Fatalf("WriteToPty: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if string(written) != "hello" {
		t.Errorf("pty received %q, want hello", written)
	}
}

func TestApp_WriteToPty_UnknownPane(t *testing.T) {
	a := &App{
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.WriteToPty("no-pane", []int{65}); err == nil {
		t.Fatal("must error for unknown pane")
	}
}

func TestApp_ResizePty_Succeeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var resized bool
	br := internalpty.NewBridgeForTestWithResize(func() error { return nil },
		func(_, _ uint16) error { resized = true; return nil })
	a := &App{
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{"pane-ws2": br},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.ResizePty("pane-ws2", 120, 40); err != nil {
		t.Fatalf("ResizePty: %v", err)
	}
	if !resized {
		t.Error("Resize was not called on the bridge")
	}
}

func TestApp_CloseWorkspace_ClosesAndKeepsInRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-close", WorktreePath: wt, Agent: "claude", Title: "t"})

	closed := false
	br := internalpty.NewBridgeForTest(func() error { closed = true; return nil })
	fm := agent.NewFakeMonitor(nil)

	a := &App{
		store:    store,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{"pane-ws-close": br},
		monitors: map[string]agent.Monitor{"ws-close": fm},
	}

	if err := a.CloseWorkspace("ws-close"); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}
	if !closed {
		t.Error("Bridge must be closed")
	}
	if !fm.TornDown() {
		t.Error("Monitor must be torn down")
	}
	if _, ok := store.Get("ws-close"); !ok {
		t.Error("CloseWorkspace must NOT remove workspace from registry")
	}
}

func TestApp_RemoveWorkspace_RemovesFromRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-rm", WorktreePath: wt, Agent: "claude", Title: "t"})

	fm := agent.NewFakeMonitor(nil)
	a := &App{
		store:    store,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{"ws-rm": fm},
	}

	if err := a.RemoveWorkspace("ws-rm"); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}
	if _, ok := store.Get("ws-rm"); ok {
		t.Error("RemoveWorkspace must remove workspace from registry")
	}
}

func TestApp_OpenShell_SpawnsAndEmits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")

	var mu sync.Mutex
	var emitted []string
	emit := func(event string, data ...any) {
		if strings.HasPrefix(event, "pty:data:") {
			mu.Lock()
			emitted = append(emitted, event)
			mu.Unlock()
		}
	}

	spawnCalled := false
	a := &App{
		emit:     emit,
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		spawnPty: func(_ context.Context, cwd string, argv []string, event string,
			ef internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			spawnCalled = true
			if event != "pty:data:shell-1" {
				return nil, fmt.Errorf("wrong event %q", event)
			}
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
	}

	if err := a.OpenShell("shell-1", t.TempDir()); err != nil {
		t.Fatalf("OpenShell: %v", err)
	}
	if !spawnCalled {
		t.Error("spawnPty must be called by OpenShell")
	}
	a.mu.Lock()
	_, ok := a.bridges["shell-1"]
	a.mu.Unlock()
	if !ok {
		t.Error("bridge for shell-1 not registered")
	}
}
