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
	fspkg "github.com/Miniature-Pug/perch/internal/fs"
	"github.com/Miniature-Pug/perch/internal/notify"
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
		spawnPty: func(ctx context.Context, cwd string, argv []string, dataEvent, exitEvent string,
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

	shellCwd := t.TempDir()
	spawnCalled := false
	a := &App{
		emit:     emit,
		roots:    []string{shellCwd},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		spawnPty: func(_ context.Context, cwd string, argv []string, dataEvent, exitEvent string,
			ef internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			spawnCalled = true
			if dataEvent != "pty:data:shell-1" {
				return nil, fmt.Errorf("wrong event %q", dataEvent)
			}
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
	}

	if err := a.OpenShell("shell-1", shellCwd); err != nil {
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

func TestApp_Settings_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}
	s := Settings{Theme: "tokyo-night", Density: "comfortable", DND: true}
	if err := a.SaveSettings(s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := a.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Theme != "tokyo-night" || got.Density != "comfortable" || !got.DND {
		t.Errorf("settings round-trip mismatch: %+v", got)
	}
}

// TestAtomicWriteApp_Mode0600 verifies N-5: atomicWriteApp (used by SaveSettings
// and SaveLayout) produces files with mode 0600 so a token-bearing settings
// payload is never readable by group/world.
func TestAtomicWriteApp_Mode0600(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}
	if err := a.SaveSettings(Settings{Theme: "gruvbox"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	info, err := os.Stat(a.settingsPath)
	if err != nil {
		t.Fatalf("Stat settings file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("settings file mode = %04o, want 0600", got)
	}
}

func TestApp_Layout_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:      store,
		emit:       func(string, ...any) {},
		bridges:    map[string]*internalpty.Bridge{},
		monitors:   map[string]agent.Monitor{},
		layoutPath: filepath.Join(cfgDir, "layout.json"),
	}
	blob := `{"sidebarW":240,"shellH":200}`
	if err := a.SaveLayout(blob); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}
	got, err := a.GetLayout()
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}
	if got != blob {
		t.Errorf("layout round-trip = %q, want %q", got, blob)
	}
}

func TestApp_GetSettings_ReturnsDefaultOnMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		settingsPath: filepath.Join(cfgDir, "no-such-settings.json"),
	}
	s, err := a.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings on missing file: %v", err)
	}
	if s.Theme != "gruvbox" {
		t.Errorf("default theme = %q, want gruvbox", s.Theme)
	}
}

func TestApp_ListDir_ReturnsDirEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{dir},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	nodes, err := a.ListDir(dir)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(nodes) == 0 {
		t.Error("ListDir must return at least the written file")
	}
}

func TestApp_Approve_RoutesToMonitor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-ap", WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor(nil)

	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{"ws-ap": fm},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}

	if err := a.Approve("req-001:ws-ap", "allow"); err != nil {
		t.Fatalf("Approve allow: %v", err)
	}
	calls := fm.ApproveCalls()
	if len(calls) != 1 || calls[0].ReqID != "req-001" {
		t.Errorf("Approve did not route to monitor; calls=%+v", calls)
	}
}

func TestApp_Approve_AlwaysPersistsRule(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-alw", WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor(nil)

	a := &App{
		store:    store,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{"ws-alw": fm},
		// Seed the pending approval the pump would have registered. tool+input
		// are resolved from here (backend-authoritative), not from the frontend.
		pending:      map[string]agent.ApprovalReq{"req-002:ws-alw": {ReqID: "req-002", Tool: "Bash", Input: "rm -rf /tmp/x"}},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}

	if err := a.Approve("req-002:ws-alw", "always"); err != nil {
		t.Fatalf("Approve always: %v", err)
	}

	s, err := a.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if len(s.AlwaysRules) == 0 {
		t.Fatal("AlwaysRule must be persisted on 'always' decision")
	}
	if s.AlwaysRules[0].Tool != "Bash" {
		t.Errorf("rule tool = %q, want Bash", s.AlwaysRules[0].Tool)
	}
	if s.AlwaysRules[0].Pattern != "rm -rf /tmp/x" {
		t.Errorf("rule pattern = %q, want the exact tool input", s.AlwaysRules[0].Pattern)
	}
}

// newAlwaysTestApp builds an App with a workspace + fake monitor + capturing
// emit seam, for the always-allow auto-approval tests.
func newAlwaysTestApp(t *testing.T, agentName string, notifies *[]map[string]any) (*App, *agent.FakeMonitor) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	_ = store.Upsert(registry.Workspace{ID: "ws", WorktreePath: t.TempDir(), Agent: agentName})
	fm := agent.NewFakeMonitor(nil)
	a := &App{
		store: store,
		emit: func(ev string, data ...any) {
			if notifies != nil && ev == "notify" && len(data) == 1 {
				if m, ok := data[0].(map[string]any); ok {
					*notifies = append(*notifies, m)
				}
			}
		},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{"ws": fm},
		pending:      map[string]agent.ApprovalReq{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}
	return a, fm
}

// TestApp_MaybeAutoApprove_ExactMatch asserts that a request exactly matching a
// persisted rule is allowed via the monitor, fires a routine transparency
// notification, and is suppressed (returns true).
func TestApp_MaybeAutoApprove_ExactMatch(t *testing.T) {
	var notifies []map[string]any
	a, fm := newAlwaysTestApp(t, "claude", &notifies)
	// M-13: the authoritative match key is the sha256 hash of the full input, not
	// the (truncated, display-only) Pattern. A rule must carry a Hash and the
	// incoming request must carry the matching InputHash.
	_ = a.SaveSettings(Settings{AlwaysRules: []AlwaysRule{{Agent: "claude", Tool: "Bash", Pattern: "ls -la", Hash: hashInput("ls -la")}}})

	req := agent.ApprovalReq{ReqID: "raw1", Tool: "Bash", Input: "ls -la", InputHash: hashInput("ls -la")}
	if !a.maybeAutoApprove("ws", "raw1", req, fm) {
		t.Fatal("exact-matching request must be auto-approved (suppressed)")
	}
	calls := fm.ApproveCalls()
	if len(calls) != 1 || calls[0].ReqID != "raw1" || !calls[0].D.Allow {
		t.Fatalf("expected one allow Approve(raw1); got %+v", calls)
	}
	if len(notifies) != 1 || notifies[0]["tier"] != "routine" {
		t.Errorf("expected one routine transparency notify; got %+v", notifies)
	}
}

// TestApp_MaybeAutoApprove_NoMatch asserts that a different input, a different
// tool, and an empty-pattern rule all FAIL to auto-approve (the card surfaces).
func TestApp_MaybeAutoApprove_NoMatch(t *testing.T) {
	cases := []struct {
		name string
		rule AlwaysRule
		req  agent.ApprovalReq
	}{
		{"different input → different hash", AlwaysRule{Agent: "claude", Tool: "Bash", Pattern: "ls -la", Hash: hashInput("ls -la")}, agent.ApprovalReq{ReqID: "r", Tool: "Bash", Input: "rm -rf /", InputHash: hashInput("rm -rf /")}},
		{"different tool (same content hash)", AlwaysRule{Agent: "claude", Tool: "Read", Pattern: "/x", Hash: hashInput("/x")}, agent.ApprovalReq{ReqID: "r", Tool: "Bash", Input: "/x", InputHash: hashInput("/x")}},
		{"rule with empty hash never matches (fail closed)", AlwaysRule{Agent: "claude", Tool: "Bash", Pattern: "anything", Hash: ""}, agent.ApprovalReq{ReqID: "r", Tool: "Bash", Input: "anything", InputHash: hashInput("anything")}},
		{"request with empty hash never matches (fail closed)", AlwaysRule{Agent: "claude", Tool: "Bash", Pattern: "anything", Hash: hashInput("anything")}, agent.ApprovalReq{ReqID: "r", Tool: "Bash", Input: "anything", InputHash: ""}},
		{"glob is NOT honored (hash is exact)", AlwaysRule{Agent: "claude", Tool: "Read", Pattern: "/tmp/**", Hash: hashInput("/tmp/**")}, agent.ApprovalReq{ReqID: "r", Tool: "Read", Input: "/tmp/secret", InputHash: hashInput("/tmp/secret")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, fm := newAlwaysTestApp(t, "claude", nil)
			_ = a.SaveSettings(Settings{AlwaysRules: []AlwaysRule{tc.rule}})
			if a.maybeAutoApprove("ws", tc.req.ReqID, tc.req, fm) {
				t.Errorf("%s: must NOT auto-approve", tc.name)
			}
			if len(fm.ApproveCalls()) != 0 {
				t.Errorf("%s: monitor.Approve must not be called on a non-match", tc.name)
			}
		})
	}
}

// TestApp_Approve_Always_CapturesPendingByReqID is the discriminating test: with
// two approvals pending, clicking Always on the FIRST must persist a rule for
// the FIRST's tool+input — never the most-recently-seen approval. This fails
// under a racy "last approval" accessor and passes only with reqID resolution.
func TestApp_Approve_Always_CapturesPendingByReqID(t *testing.T) {
	a, _ := newAlwaysTestApp(t, "claude", nil)
	a.pending = map[string]agent.ApprovalReq{
		"r1:ws": {ReqID: "r1", Tool: "Read", Input: "/a.txt"},
		"r2:ws": {ReqID: "r2", Tool: "Bash", Input: "rm -rf /"}, // the "later" one
	}

	if err := a.Approve("r1:ws", "always"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	s, _ := a.GetSettings()
	if len(s.AlwaysRules) != 1 {
		t.Fatalf("expected exactly one persisted rule, got %d", len(s.AlwaysRules))
	}
	r := s.AlwaysRules[0]
	if r.Tool != "Read" || r.Pattern != "/a.txt" {
		t.Errorf("rule = {%s,%s}, want {Read,/a.txt} (the approved reqID, not the last-seen)", r.Tool, r.Pattern)
	}
	// Only r1 consumed; r2 stays pending.
	a.mu.Lock()
	_, r1Gone := a.pending["r1:ws"]
	_, r2Kept := a.pending["r2:ws"]
	a.mu.Unlock()
	if r1Gone {
		t.Error("r1 must be consumed from pending after decision")
	}
	if !r2Kept {
		t.Error("r2 must remain pending (only the decided reqID is consumed)")
	}
}

func TestApp_Approve_DenyNoSideEffects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-deny", WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor(nil)
	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{"ws-deny": fm},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}

	if err := a.Approve("req-003:ws-deny", "deny"); err != nil {
		t.Fatalf("Approve deny: %v", err)
	}
	s, _ := a.GetSettings()
	if len(s.AlwaysRules) != 0 {
		t.Error("deny must not persist an AlwaysRule")
	}
}

func initGitRepo(t *testing.T, root string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-qm", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestApp_DiffStat_ValidateAndDelegate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := initGitRepo(t, root)

	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := a.DiffStat(repo)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if len(files) == 0 {
		t.Error("DiffStat must return at least one FileDiff for an unstaged file")
	}
}

func TestApp_DiffStat_RejectsOutsideRoot(t *testing.T) {
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
	if _, err := a.DiffStat("/etc"); err == nil {
		t.Fatal("must reject path outside roots")
	}
}

func TestApp_Hunks_ReturnsHunks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := initGitRepo(t, root)

	p := filepath.Join(repo, "file.txt")
	if err := os.WriteFile(p, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "add file")
	if err := os.WriteFile(p, []byte("line1\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	hunks, err := a.Hunks(repo, "file.txt")
	if err != nil {
		t.Fatalf("Hunks: %v", err)
	}
	if len(hunks) == 0 {
		t.Error("Hunks must return at least one hunk for modified file")
	}
}

func TestApp_ReadWriteFile_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{dir},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.WriteFile(path, "hello world"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := a.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got != "hello world" {
		t.Errorf("ReadFile = %q, want 'hello world'", got)
	}
}

func TestApp_ReadFile_RejectsOutsideRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{roots: []string{t.TempDir()}, emit: func(string, ...any) {},
		bridges: map[string]*internalpty.Bridge{}, monitors: map[string]agent.Monitor{}}
	if _, err := a.ReadFile("/etc/passwd"); err == nil {
		t.Fatal("ReadFile must reject a path outside configured roots")
	}
}

func TestApp_WriteFile_RejectsOutsideRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{roots: []string{t.TempDir()}, emit: func(string, ...any) {},
		bridges: map[string]*internalpty.Bridge{}, monitors: map[string]agent.Monitor{}}
	if err := a.WriteFile("/tmp/perch-evil-test.txt", "x"); err == nil {
		t.Fatal("WriteFile must reject a path whose parent is outside configured roots")
	}
}

func TestApp_ListDir_RejectsOutsideRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{roots: []string{t.TempDir()}, emit: func(string, ...any) {},
		bridges: map[string]*internalpty.Bridge{}, monitors: map[string]agent.Monitor{}}
	if _, err := a.ListDir("/etc"); err == nil {
		t.Fatal("ListDir must reject a directory outside configured roots")
	}
}

func TestApp_Approve_AlwaysUsesWorkspaceAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-oc", WorktreePath: wt, Agent: "opencode"})
	fm := agent.NewFakeMonitor(nil)
	a := &App{store: store, emit: func(string, ...any) {},
		bridges: map[string]*internalpty.Bridge{}, monitors: map[string]agent.Monitor{"ws-oc": fm},
		pending:      map[string]agent.ApprovalReq{"r1:ws-oc": {ReqID: "r1", Tool: "bash", Input: "ls"}},
		settingsPath: filepath.Join(cfgDir, "settings.json")}
	if err := a.Approve("r1:ws-oc", "always"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	s, _ := a.GetSettings()
	if len(s.AlwaysRules) == 0 || s.AlwaysRules[0].Agent != "opencode" {
		t.Errorf("AlwaysRule agent = %v, want opencode", s.AlwaysRules)
	}
}

// makeWatcherSeam returns a newWatcherFunc that captures the registered onChange
// closure into *capturedOnChange and returns an inert real watcher on a temp dir.
func makeWatcherSeam(t *testing.T, capturedOnChange *func(string)) newWatcherFunc {
	t.Helper()
	return func(absRoot string, onChange func(string)) (*fspkg.Watcher, error) {
		*capturedOnChange = onChange
		// Return a real watcher on a temp dir so Close() works correctly.
		return fspkg.Watch(t.TempDir(), func(string) {})
	}
}

// newWatcherTestApp builds a minimal App wired for watcher tests: fake spawnPty,
// fake newMonitor, fake newWatcher, tiny debounce, and an emit capture seam.
func newWatcherTestApp(t *testing.T, wt string, capturedOnChange *func(string)) (*App, func() []struct {
	event string
	data  []any
}) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	_ = store.Upsert(registry.Workspace{ID: "ws-watch", WorktreePath: wt, Agent: "claude", Title: "t"})

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

	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     emit,
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
		debounce: time.Millisecond,
		spawnPty: func(_ context.Context, _ string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) {
			return agent.NewFakeMonitor(nil), nil
		},
		newWatcher: makeWatcherSeam(t, capturedOnChange),
	}

	snapshot := func() []struct {
		event string
		data  []any
	} {
		mu.Lock()
		defer mu.Unlock()
		out := make([]struct {
			event string
			data  []any
		}, len(emitted))
		copy(out, emitted)
		return out
	}
	return a, snapshot
}

// countFsChanged counts emitted events with event == "fs:changed".
func countFsChanged(events []struct {
	event string
	data  []any
}) int {
	n := 0
	for _, e := range events {
		if e.event == "fs:changed" {
			n++
		}
	}
	return n
}

// TestApp_Watcher_RegisteredOnOpen asserts that opening a workspace registers
// an onChange closure (Test A).
func TestApp_Watcher_RegisteredOnOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := t.TempDir()
	var capturedOnChange func(string)
	a, _ := newWatcherTestApp(t, wt, &capturedOnChange)

	if err := a.OpenWorkspace("ws-watch"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if capturedOnChange == nil {
		t.Fatal("newWatcher was not called with an onChange closure")
	}
}

// TestApp_Watcher_EmitsFsChanged asserts that invoking onChange once results
// in exactly one fs:changed event (after the debounce window) with the correct
// payload (Test B).
func TestApp_Watcher_EmitsFsChanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := t.TempDir()
	var capturedOnChange func(string)
	a, snapshot := newWatcherTestApp(t, wt, &capturedOnChange)

	if err := a.OpenWorkspace("ws-watch"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if capturedOnChange == nil {
		t.Fatal("onChange not captured")
	}

	// Fire one raw change.
	capturedOnChange("somefile.go")

	// Wait long enough for the debounce timer (1ms) to fire; 50ms is ample.
	time.Sleep(50 * time.Millisecond)

	events := snapshot()
	if n := countFsChanged(events); n != 1 {
		t.Errorf("fs:changed emit count = %d, want 1", n)
	}
	// Verify payload.
	for _, e := range events {
		if e.event != "fs:changed" {
			continue
		}
		if len(e.data) == 0 {
			t.Fatal("fs:changed emitted with no data")
		}
		payload, ok := e.data[0].(map[string]any)
		if !ok {
			t.Fatalf("fs:changed data[0] type = %T, want map[string]any", e.data[0])
		}
		if payload["workspaceId"] != "ws-watch" {
			t.Errorf("workspaceId = %v, want ws-watch", payload["workspaceId"])
		}
		if payload["path"] != wt {
			t.Errorf("path = %v, want %s", payload["path"], wt)
		}
	}
}

// ── Feature 1: model plumbing ─────────────────────────────────────────────────

// TestApp_CreateWorkspace_PersistsModel verifies that the model arg is stored in
// the registry record when a workspace is created.
func TestApp_CreateWorkspace_PersistsModel(t *testing.T) {
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

	vm, err := a.CreateWorkspace("claude", repo, "feat/model-test", "claude-opus-4-5")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	w, ok := store.Get(vm.ID)
	if !ok {
		t.Fatal("workspace not found in registry")
	}
	if w.Model != "claude-opus-4-5" {
		t.Errorf("Model = %q, want claude-opus-4-5", w.Model)
	}
}

// TestApp_OpenWorkspace_ModelReachesAgent verifies that the Model stored in the
// registry workspace is passed through to Monitor.Prepare (and thus the agent
// launch args) on a fresh-start open (no LastSessionID).
func TestApp_OpenWorkspace_ModelReachesAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-model",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "t",
		Model:        "claude-sonnet-4-5",
	})

	var capturedFM *agent.FakeMonitor
	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
		spawnPty: func(_ context.Context, _ string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) {
			fm := agent.NewFakeMonitor(nil)
			capturedFM = fm
			return fm, nil
		},
	}

	if err := a.OpenWorkspace("ws-model"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if capturedFM == nil {
		t.Fatal("FakeMonitor was not created")
	}
	if got := capturedFM.CapturedModel(); got != "claude-sonnet-4-5" {
		t.Errorf("Prepare received model=%q, want claude-sonnet-4-5", got)
	}
	// No prior session: resumeID must be empty (fresh start path).
	if got := capturedFM.CapturedResumeID(); got != "" {
		t.Errorf("Prepare received resumeID=%q, want empty (fresh start)", got)
	}
}

// ── Feature 2: session resume ─────────────────────────────────────────────────

// TestApp_OpenWorkspace_SessionIDPersistedOnSessionStart verifies that when the
// monitor emits a SessionStart event carrying a session id, the app persists it
// to LastSessionID in the registry.
func TestApp_OpenWorkspace_SessionIDPersistedOnSessionStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-ses",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "t",
	})

	fm := agent.NewFakeMonitor(nil)
	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
		spawnPty: func(_ context.Context, _ string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) {
			return fm, nil
		},
	}

	if err := a.OpenWorkspace("ws-ses"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	// Simulate a SessionStart event carrying a session id.
	fm.Replay(agent.Event{Kind: "state", State: agent.StateRunning, SessionID: "ses_abc123"})

	// Poll until the registry is updated (the pump goroutine is async).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w, ok := store.Get("ws-ses"); ok && w.LastSessionID == "ses_abc123" {
			return // pass
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, _ := store.Get("ws-ses")
	t.Errorf("LastSessionID = %q, want ses_abc123", w.LastSessionID)
}

// TestApp_OpenWorkspace_ResumeUsesLastSessionID verifies that on a second
// OpenWorkspace call after a session id has been persisted, the stored
// LastSessionID is passed as resumeID to Monitor.Prepare.
func TestApp_OpenWorkspace_ResumeUsesLastSessionID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	// Pre-seed a workspace that already has a LastSessionID (simulating the
	// state after a previous open + SessionStart).
	_ = store.Upsert(registry.Workspace{
		ID:            "ws-resume",
		WorktreePath:  wt,
		Agent:         "claude",
		Title:         "t",
		LastSessionID: "ses_resume42",
	})

	var lastFM *agent.FakeMonitor
	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
		spawnPty: func(_ context.Context, _ string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) {
			fm := agent.NewFakeMonitor(nil)
			lastFM = fm
			return fm, nil
		},
	}

	if err := a.OpenWorkspace("ws-resume"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if lastFM == nil {
		t.Fatal("FakeMonitor was not created")
	}
	if got := lastFM.CapturedResumeID(); got != "ses_resume42" {
		t.Errorf("Prepare received resumeID=%q, want ses_resume42", got)
	}
}

// ── Feature 3: gitignore-aware file tree ──────────────────────────────────────

// TestApp_ListDir_HonorsGitignore verifies that App.ListDir excludes entries
// matching .gitignore patterns (e.g. node_modules/) from its output.
func TestApp_ListDir_HonorsGitignore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Create a .gitignore that excludes node_modules/ and *.log.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create items that should be included.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create items that should be excluded.
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{dir},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	nodes, err := a.ListDir(dir)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}

	for _, n := range nodes {
		if n.Name == "node_modules" {
			t.Error("node_modules should be excluded by .gitignore")
		}
		if n.Name == "debug.log" {
			t.Error("debug.log should be excluded by .gitignore (*.log pattern)")
		}
	}
	found := false
	for _, n := range nodes {
		if n.Name == "main.go" {
			found = true
		}
	}
	if !found {
		t.Error("main.go should be present (not gitignored)")
	}
}

// ── OS notification tests ─────────────────────────────────────────────────────

// newNotifyTestApp builds a minimal App wired for OS-notification tests.
// It injects a FakeNotifier and a settingsPath in a temp dir so GetSettings works.
func newNotifyTestApp(t *testing.T, focused bool, dnd bool) (*App, *notify.FakeNotifier) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	fn := &notify.FakeNotifier{}
	a := &App{
		store:        store,
		roots:        []string{t.TempDir()},
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		notifier:     fn,
		focused:      focused,
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}
	// Persist DND setting.
	_ = a.SaveSettings(Settings{DND: dnd})
	return a, fn
}

// TestApp_DispatchNotify_OSFires_WhenUnfocusedAndDNDOff asserts that an OS
// notification is sent for a blocking-tier event when the window is unfocused
// and DND is disabled.
func TestApp_DispatchNotify_OSFires_WhenUnfocusedAndDNDOff(t *testing.T) {
	a, fn := newNotifyTestApp(t, false /*focused*/, false /*dnd*/)

	a.dispatchNotify(agent.Event{
		Kind:        "state",
		State:       agent.StateAwaitingApproval,
		WorkspaceID: "ws-1",
	})

	if len(fn.Calls) != 1 {
		t.Fatalf("expected 1 OS notify call, got %d", len(fn.Calls))
	}
	if fn.Calls[0].Title != "Approval needed" {
		t.Errorf("title = %q, want %q", fn.Calls[0].Title, "Approval needed")
	}
}

// TestApp_DispatchNotify_OSFires_ErroredTier asserts that an errored-state event
// (also blocking tier) fires an OS notification when unfocused + DND off.
func TestApp_DispatchNotify_OSFires_ErroredTier(t *testing.T) {
	a, fn := newNotifyTestApp(t, false /*focused*/, false /*dnd*/)

	a.dispatchNotify(agent.Event{
		Kind:        "state",
		State:       agent.StateErrored,
		Err:         "something broke",
		WorkspaceID: "ws-1",
	})

	if len(fn.Calls) != 1 {
		t.Fatalf("expected 1 OS notify call, got %d", len(fn.Calls))
	}
	if fn.Calls[0].Title != "Agent error" {
		t.Errorf("title = %q, want 'Agent error'", fn.Calls[0].Title)
	}
}

// TestApp_DispatchNotify_OSSuppressed_WhenFocused asserts that no OS notification
// fires when the window is focused, even for a blocking-tier event.
func TestApp_DispatchNotify_OSSuppressed_WhenFocused(t *testing.T) {
	a, fn := newNotifyTestApp(t, true /*focused*/, false /*dnd*/)

	a.dispatchNotify(agent.Event{
		Kind:        "state",
		State:       agent.StateAwaitingApproval,
		WorkspaceID: "ws-1",
	})

	if len(fn.Calls) != 0 {
		t.Errorf("expected 0 OS notify calls when focused, got %d", len(fn.Calls))
	}
}

// TestApp_DispatchNotify_OSFires_BlockingDespiteDND asserts that a blocking-tier
// OS notification STILL fires when DND is enabled (unfocused). Per SPEC §8, DND
// mutes only tiers 2–3 (ambient + routine) and never tier 1 (blocking); since
// only blocking events fire an OS notification, DND must not suppress them.
func TestApp_DispatchNotify_OSFires_BlockingDespiteDND(t *testing.T) {
	a, fn := newNotifyTestApp(t, false /*focused*/, true /*dnd*/)

	a.dispatchNotify(agent.Event{
		Kind:        "state",
		State:       agent.StateAwaitingApproval,
		WorkspaceID: "ws-1",
	})

	if len(fn.Calls) != 1 {
		t.Fatalf("expected 1 OS notify call (DND must not mute blocking), got %d", len(fn.Calls))
	}
	if fn.Calls[0].Title != "Approval needed" {
		t.Errorf("title = %q, want %q", fn.Calls[0].Title, "Approval needed")
	}
}

// TestApp_DispatchNotify_OSSuppressed_AmbientTier asserts that ambient-tier events
// (e.g. StateDone) never fire an OS notification.
func TestApp_DispatchNotify_OSSuppressed_AmbientTier(t *testing.T) {
	a, fn := newNotifyTestApp(t, false /*focused*/, false /*dnd*/)

	a.dispatchNotify(agent.Event{
		Kind:        "state",
		State:       agent.StateDone,
		WorkspaceID: "ws-1",
	})

	if len(fn.Calls) != 0 {
		t.Errorf("expected 0 OS notify calls for ambient tier, got %d", len(fn.Calls))
	}
}

// TestApp_DispatchNotify_NilNotifier_NoPanic proves that a nil notifier (the
// no-op fallback path when OS notifications are unavailable) never panics.
func TestApp_DispatchNotify_NilNotifier_NoPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:        store,
		roots:        []string{t.TempDir()},
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		notifier:     nil, // explicit no-op / unavailable
		focused:      false,
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}
	_ = a.SaveSettings(Settings{DND: false})

	// Must not panic.
	a.dispatchNotify(agent.Event{
		Kind:        "state",
		State:       agent.StateAwaitingApproval,
		WorkspaceID: "ws-nil",
	})
}

// TestApp_SetWindowFocus_UpdatesState asserts that SetWindowFocus correctly
// stores the focused state under the mutex.
func TestApp_SetWindowFocus_UpdatesState(t *testing.T) {
	a := &App{focused: true}

	a.SetWindowFocus(false)
	a.mu.Lock()
	got := a.focused
	a.mu.Unlock()
	if got {
		t.Error("SetWindowFocus(false) did not update focused to false")
	}

	a.SetWindowFocus(true)
	a.mu.Lock()
	got = a.focused
	a.mu.Unlock()
	if !got {
		t.Error("SetWindowFocus(true) did not update focused to true")
	}
}

// TestApp_Watcher_NoEmitAfterClose asserts that after CloseWorkspace the debounce
// goroutine has exited: additional onChange calls must not produce new fs:changed
// events. This proves the goroutine exits on wctx cancellation (Test C — no leak).
func TestApp_Watcher_NoEmitAfterClose(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := t.TempDir()
	var capturedOnChange func(string)
	a, snapshot := newWatcherTestApp(t, wt, &capturedOnChange)

	if err := a.OpenWorkspace("ws-watch"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if capturedOnChange == nil {
		t.Fatal("onChange not captured")
	}

	// Close the workspace — this cancels wctx and closes the watcher.
	if err := a.CloseWorkspace("ws-watch"); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}

	// Give the goroutine time to observe the cancellation.
	time.Sleep(20 * time.Millisecond)

	// Record the fs:changed count before any additional onChange calls.
	before := countFsChanged(snapshot())

	// Fire additional raw changes — the goroutine must be dead, so no new emits.
	capturedOnChange("after-close.go")
	capturedOnChange("after-close2.go")

	// Wait long enough that any spurious timer would have fired.
	time.Sleep(50 * time.Millisecond)

	after := countFsChanged(snapshot())
	if after != before {
		t.Errorf("fs:changed emitted after CloseWorkspace: before=%d after=%d — goroutine leak", before, after)
	}
}

// TestApp_DiscoverRepos_FindsReposUnderRoots verifies that DiscoverRepos
// returns RepoInfo entries for real git repositories placed under App.roots.
// The existing initGitRepo helper creates a "repo" subdirectory inside the
// supplied root, so we use two separate temp dirs as parent containers.
func TestApp_DiscoverRepos_FindsReposUnderRoots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Each call to initGitRepo creates <parent>/repo; use two parents so
	// we get two distinct repos under a single scan root.
	root := t.TempDir()
	parent1 := filepath.Join(root, "alpha")
	parent2 := filepath.Join(root, "beta")
	if err := os.MkdirAll(parent1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(parent2, 0o755); err != nil {
		t.Fatal(err)
	}
	repo1 := initGitRepo(t, parent1) // returns parent1/repo
	repo2 := initGitRepo(t, parent2) // returns parent2/repo

	a := &App{roots: []string{root}}

	repos, err := a.DiscoverRepos()
	if err != nil {
		t.Fatalf("DiscoverRepos: unexpected error: %v", err)
	}

	// Build a set of discovered paths (EvalSymlinks-normalised to handle /tmp
	// symlinks on some systems).
	found := make(map[string]RepoInfo, len(repos))
	for _, ri := range repos {
		norm, nerr := filepath.EvalSymlinks(ri.Path)
		if nerr != nil {
			norm = ri.Path
		}
		found[norm] = ri
	}

	for _, want := range []string{repo1, repo2} {
		norm, nerr := filepath.EvalSymlinks(want)
		if nerr != nil {
			norm = want
		}
		ri, ok := found[norm]
		if !ok {
			t.Errorf("repo %q not found in DiscoverRepos result; got %v", want, repos)
			continue
		}
		if ri.Name == "" {
			t.Errorf("repo %q: Name is empty", want)
		}
		if ri.Branch == "" {
			t.Errorf("repo %q: Branch is empty (expected a branch after initial commit)", want)
		}
		if len(ri.Worktrees) == 0 {
			t.Errorf("repo %q: Worktrees is empty", want)
		}
	}
}

// TestApp_DiscoverRepos_EmptyWhenNoRepos verifies that when App.roots point
// at a directory that contains no git repositories, DiscoverRepos returns a
// non-nil empty slice and no error.
func TestApp_DiscoverRepos_EmptyWhenNoRepos(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir() // empty directory — no git repos inside

	a := &App{roots: []string{root}}

	repos, err := a.DiscoverRepos()
	if err != nil {
		t.Fatalf("DiscoverRepos: unexpected error: %v", err)
	}
	if repos == nil {
		t.Fatal("DiscoverRepos returned nil slice; want non-nil empty slice")
	}
	if len(repos) != 0 {
		t.Errorf("DiscoverRepos returned %d repos; want 0 (empty dir)", len(repos))
	}
}
