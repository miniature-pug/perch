// shell_multi_test.go: tests for multiple shell terminals per session — the
// paneID→workspace recovery helper, per-tab CloseShell, and CloseWorkspace reaping
// every one of a session's shells (default "shell-<id>" plus "shell-<id>_<n>" tabs).
package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/miniature-pug/perch/internal/agent"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

func TestWorkspaceIDForShellPane(t *testing.T) {
	tests := []struct{ pane, want string }{
		{"shell-abc", "abc"},                   // default shell
		{"shell-abc-def-1a2b-3c4d", "abc-def-1a2b-3c4d"}, // UUID (hyphens), default
		{"shell-abc-def-1a2b-3c4d_1", "abc-def-1a2b-3c4d"}, // additional tab
		{"shell-abc-def-1a2b-3c4d_42", "abc-def-1a2b-3c4d"},
		{"shell-home", "home"}, // home shell — callers guard homeShellPaneID separately
		{"pane-abc", ""},       // agent pane, not a shell
		{"shell-", ""},         // empty remainder
		{"", ""},
		{"nonsense", ""},
	}
	for _, tt := range tests {
		if got := workspaceIDForShellPane(tt.pane); got != tt.want {
			t.Errorf("workspaceIDForShellPane(%q) = %q, want %q", tt.pane, got, tt.want)
		}
	}
}

func TestCloseShell_ClosesOnePaneAndIsIdempotent(t *testing.T) {
	var mu sync.Mutex
	closed := map[string]int{}
	mk := func(name string) *internalpty.Bridge {
		return internalpty.NewBridgeForTest(func() error {
			mu.Lock()
			closed[name]++
			mu.Unlock()
			return nil
		})
	}
	a := &App{bridges: map[string]*internalpty.Bridge{
		"shell-ws1":   mk("default"),
		"shell-ws1_1": mk("tab1"),
	}}

	if err := a.CloseShell("shell-ws1_1"); err != nil {
		t.Fatalf("CloseShell: %v", err)
	}

	a.mu.Lock()
	_, hasDefault := a.bridges["shell-ws1"]
	_, hasTab1 := a.bridges["shell-ws1_1"]
	a.mu.Unlock()
	if !hasDefault {
		t.Error("CloseShell closed the wrong pane — the default shell is gone")
	}
	if hasTab1 {
		t.Error("CloseShell did not drop shell-ws1_1 from the registry")
	}

	mu.Lock()
	if closed["tab1"] != 1 {
		t.Errorf("shell-ws1_1 bridge Close called %d times, want 1", closed["tab1"])
	}
	if closed["default"] != 0 {
		t.Errorf("the default shell must not be closed, got %d", closed["default"])
	}
	mu.Unlock()

	// Idempotent: closing an already-gone pane is a no-op, not an error.
	if err := a.CloseShell("shell-ws1_1"); err != nil {
		t.Errorf("second CloseShell must be idempotent, got %v", err)
	}
	mu.Lock()
	if closed["tab1"] != 1 {
		t.Errorf("idempotent CloseShell must not re-close, got %d", closed["tab1"])
	}
	mu.Unlock()

	// A malformed pane id is rejected (same guard as every pty method).
	if err := a.CloseShell("bad;id"); err == nil {
		t.Error("CloseShell must reject a malformed pane id")
	}
}

func TestCloseWorkspace_ReapsAllSessionShells(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	const wsID = "ws-multi"
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: wsID, WorktreePath: wt, Agent: "claude"})

	var mu sync.Mutex
	closed := map[string]int{}
	mk := func(name string) *internalpty.Bridge {
		return internalpty.NewBridgeForTest(func() error {
			mu.Lock()
			closed[name]++
			mu.Unlock()
			return nil
		})
	}

	a := &App{
		store:        store,
		roots:        []string{wt},
		emit:         func(string, ...any) {},
		monitors:     map[string]agent.Monitor{},
		cancels:      map[string]context.CancelFunc{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
		bridges: map[string]*internalpty.Bridge{
			paneIDFor(wsID):        mk("agent"),
			"shell-" + wsID:        mk("shell-default"),
			"shell-" + wsID + "_1": mk("shell-1"),
			"shell-" + wsID + "_2": mk("shell-2"),
			// A different workspace's shells must survive untouched.
			"shell-other":   mk("other-default"),
			"shell-other_1": mk("other-1"),
		},
	}

	if err := a.CloseWorkspace(wsID); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, k := range []string{paneIDFor(wsID), "shell-" + wsID, "shell-" + wsID + "_1", "shell-" + wsID + "_2"} {
		if _, found := a.bridges[k]; found {
			t.Errorf("CloseWorkspace left %q in the registry (pty leak)", k)
		}
	}
	if _, found := a.bridges["shell-other"]; !found {
		t.Error("CloseWorkspace reaped ANOTHER workspace's default shell")
	}
	if _, found := a.bridges["shell-other_1"]; !found {
		t.Error("CloseWorkspace reaped ANOTHER workspace's shell tab")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"agent", "shell-default", "shell-1", "shell-2"} {
		if closed[name] != 1 {
			t.Errorf("bridge %q Close called %d times, want 1", name, closed[name])
		}
	}
	if closed["other-default"] != 0 || closed["other-1"] != 0 {
		t.Errorf("CloseWorkspace closed another workspace's shells: %v", closed)
	}
}
