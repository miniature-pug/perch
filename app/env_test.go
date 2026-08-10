package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/envsync"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// ── mergeEnv ─────────────────────────────────────────────────────────────────

func TestMergeEnv(t *testing.T) {
	tests := []struct {
		name     string
		base     []string
		injected []string
		overlay  []string
		want     map[string]string // key → expected value in the result
		absent   []string          // keys that must NOT appear
	}{
		{
			name:     "overlay overrides base",
			base:     []string{"AWS_PROFILE=old", "HOME=/home/me"},
			injected: nil,
			overlay:  []string{"AWS_PROFILE=new"},
			want:     map[string]string{"AWS_PROFILE": "new", "HOME": "/home/me"},
		},
		{
			name:     "injected sentinel preserved and overrides base",
			base:     []string{"PERCH_EXIT_TOKEN=base", "HOME=/h"},
			injected: []string{"PERCH_EXIT_TOKEN=sentinel", "PERCH_EXIT_URL=http://x/hook"},
			overlay:  nil,
			want: map[string]string{
				"PERCH_EXIT_TOKEN": "sentinel",
				"PERCH_EXIT_URL":   "http://x/hook",
				"HOME":             "/h",
			},
		},
		{
			name:     "overlay never clobbers a PERCH_ (sentinel + envsync preserved)",
			base:     nil,
			injected: []string{"PERCH_EXIT_TOKEN=sentinel", "PERCH_ENVSYNC_TOKEN=synctok"},
			// A malformed overlay that tries to override plumbing must be ignored.
			overlay: []string{"PERCH_EXIT_TOKEN=evil", "PERCH_ENVSYNC_TOKEN=evil", "REAL=1"},
			want: map[string]string{
				"PERCH_EXIT_TOKEN":    "sentinel",
				"PERCH_ENVSYNC_TOKEN": "synctok",
				"REAL":                "1",
			},
		},
		{
			name:     "precedence overlay > injected > base",
			base:     []string{"K=base"},
			injected: []string{"K=injected"},
			overlay:  []string{"K=overlay"},
			want:     map[string]string{"K": "overlay"},
		},
		{
			name:     "dedup by key keeps last within base",
			base:     []string{"DUP=first", "DUP=second"},
			injected: nil,
			overlay:  nil,
			want:     map[string]string{"DUP": "second"},
		},
		{
			name:     "malformed entries skipped",
			base:     []string{"NOEQUALS", "=noKey", "OK=1"},
			injected: nil,
			overlay:  nil,
			want:     map[string]string{"OK": "1"},
			absent:   []string{"NOEQUALS", ""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeEnv(tc.base, tc.injected, tc.overlay)
			gotMap := map[string]string{}
			for _, e := range got {
				k, v, ok := strings.Cut(e, "=")
				if !ok {
					t.Errorf("mergeEnv produced malformed entry %q", e)
					continue
				}
				if _, dup := gotMap[k]; dup {
					t.Errorf("mergeEnv produced duplicate key %q", k)
				}
				gotMap[k] = v
			}
			for k, v := range tc.want {
				if gotMap[k] != v {
					t.Errorf("key %q = %q, want %q (full: %v)", k, gotMap[k], v, got)
				}
			}
			for _, k := range tc.absent {
				if _, ok := gotMap[k]; ok {
					t.Errorf("key %q must be absent (full: %v)", k, got)
				}
			}
		})
	}
}

// ── ReloadAgentEnv ───────────────────────────────────────────────────────────

func TestReloadAgentEnv_WritesReloadCommand(t *testing.T) {
	var mu sync.Mutex
	var written []byte
	br := internalpty.NewBridgeForTest(func() error { return nil })
	br.OverrideWriteForTest(func(p []byte) (int, error) {
		mu.Lock()
		written = append(written, p...)
		mu.Unlock()
		return len(p), nil
	})
	a := &App{bridges: map[string]*internalpty.Bridge{"shell-ws1": br}}

	if err := a.ReloadAgentEnv("shell-ws1"); err != nil {
		t.Fatalf("ReloadAgentEnv: %v", err)
	}
	mu.Lock()
	got := string(written)
	mu.Unlock()
	if got != "perch reload\n" {
		t.Errorf("wrote %q, want %q", got, "perch reload\n")
	}
}

// When perchBin is set (the app knows its own absolute path), the reload button
// must type the shell-quoted absolute path so it resolves even if a login profile
// clobbers PATH — the binary lives at bin/perch and is not on PATH.
func TestReloadAgentEnv_WritesAbsoluteBinaryPathWhenSet(t *testing.T) {
	var mu sync.Mutex
	var written []byte
	br := internalpty.NewBridgeForTest(func() error { return nil })
	br.OverrideWriteForTest(func(p []byte) (int, error) {
		mu.Lock()
		written = append(written, p...)
		mu.Unlock()
		return len(p), nil
	})
	const bin = "/home/me/repo/bin/perch"
	a := &App{perchBin: bin, bridges: map[string]*internalpty.Bridge{"shell-ws1": br}}

	if err := a.ReloadAgentEnv("shell-ws1"); err != nil {
		t.Fatalf("ReloadAgentEnv: %v", err)
	}
	mu.Lock()
	got := string(written)
	mu.Unlock()
	want := shellQuote(bin) + " reload\n"
	if got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/usr/bin/perch", "'/usr/bin/perch'"},
		{"spaces", "/opt/my apps/perch", "'/opt/my apps/perch'"},
		{"embedded single quote", "/home/o'brien/perch", `'/home/o'\''brien/perch'`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuote(tc.in); got != tc.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReloadAgentEnv_RejectsHomeShell(t *testing.T) {
	br := internalpty.NewBridgeForTest(func() error { return nil })
	wrote := false
	br.OverrideWriteForTest(func(p []byte) (int, error) { wrote = true; return len(p), nil })
	a := &App{bridges: map[string]*internalpty.Bridge{homeShellPaneID: br}}

	if err := a.ReloadAgentEnv(homeShellPaneID); err == nil {
		t.Error("ReloadAgentEnv(shell-home) = nil, want error (home drawer has no workspace)")
	}
	if wrote {
		t.Error("ReloadAgentEnv must not write to the home drawer")
	}
}

func TestReloadAgentEnv_UnknownPane(t *testing.T) {
	a := &App{bridges: map[string]*internalpty.Bridge{}}
	if err := a.ReloadAgentEnv("shell-nope"); err == nil {
		t.Error("ReloadAgentEnv(unknown pane) = nil, want error")
	}
}

func TestReloadAgentEnv_InvalidPaneID(t *testing.T) {
	a := &App{bridges: map[string]*internalpty.Bridge{}}
	if err := a.ReloadAgentEnv("shell-ws:evil"); err == nil {
		t.Error("ReloadAgentEnv(invalid pane id) = nil, want error")
	}
}

// ── OpenShell env injection ──────────────────────────────────────────────────

// openShellCapturesEnv builds an App whose spawnPty records the env slice, opens
// the given drawer pane, and returns the captured env. perchBin seeds a.perchBin
// so the PATH-prepend / PERCH_BIN injection can be exercised (pass "" for none).
func openShellCapturesEnv(t *testing.T, paneID, cwd string, ls *envsync.Listener, overlay map[string][]string, perchBin string) []string {
	t.Helper()
	var mu sync.Mutex
	var captured []string
	a := &App{
		roots:      []string{cwd},
		emit:       func(string, ...any) {},
		bridges:    map[string]*internalpty.Bridge{},
		monitors:   map[string]agent.Monitor{},
		envsync:    ls,
		envOverlay: overlay,
		perchBin:   perchBin,
		spawnPty: func(_ context.Context, _ string, _ []string, env []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			mu.Lock()
			captured = env
			mu.Unlock()
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
	}
	if err := a.OpenShell(paneID, cwd); err != nil {
		t.Fatalf("OpenShell(%q): %v", paneID, err)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), captured...)
}

func TestOpenShell_InjectsEnvsyncVars_WorkspaceDrawer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	ls, err := envsync.New(nil, func(string, []string) {})
	if err != nil {
		t.Fatalf("envsync.New: %v", err)
	}
	t.Cleanup(func() { _ = ls.Close() })

	cwd := t.TempDir()
	env := openShellCapturesEnv(t, "shell-ws1", cwd, ls, nil, "")

	tok, _ := ls.TokenFor("ws1")
	if !envSliceHasApp(env, "PERCH_ENVSYNC_URL="+ls.URL()) {
		t.Errorf("drawer env missing PERCH_ENVSYNC_URL; got %v", env)
	}
	if !envSliceHasApp(env, "PERCH_ENVSYNC_TOKEN="+tok) {
		t.Errorf("drawer env missing PERCH_ENVSYNC_TOKEN=<token>")
	}
	if !envSliceHasApp(env, "PERCH_ENVSYNC_WS=ws1") {
		t.Errorf("drawer env missing PERCH_ENVSYNC_WS=ws1")
	}
	if !envSliceHasPrefix(env, "HOME=") {
		t.Errorf("drawer env clobbered os.Environ() — no HOME present")
	}
}

func TestOpenShell_HomeDrawer_NoEnvsyncVars(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	ls, err := envsync.New(nil, func(string, []string) {})
	if err != nil {
		t.Fatalf("envsync.New: %v", err)
	}
	t.Cleanup(func() { _ = ls.Close() })

	env := openShellCapturesEnv(t, homeShellPaneID, t.TempDir(), ls, nil, "")
	if envSliceHasPrefix(env, "PERCH_ENVSYNC_") {
		t.Errorf("home drawer must NOT receive PERCH_ENVSYNC_* vars; got %v", env)
	}
}

func TestOpenShell_AppliesOverlay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	cwd := t.TempDir()
	env := openShellCapturesEnv(t, "shell-ws1", cwd, nil, map[string][]string{"ws1": {"MYVAR=hello"}}, "")
	if !envSliceHasApp(env, "MYVAR=hello") {
		t.Errorf("drawer env missing overlay var MYVAR=hello; got %v", env)
	}
}

// envSliceGet returns the value of the last KEY=... entry, and whether present.
func envSliceGet(env []string, key string) (string, bool) {
	val, ok := "", false
	for _, e := range env {
		if v, found := strings.CutPrefix(e, key+"="); found {
			val, ok = v, true
		}
	}
	return val, ok
}

// When perchBin is an absolute path, a per-workspace drawer must get PATH
// prepended with the binary's dir (so a MANUAL `perch reload` resolves) and a
// PERCH_BIN escape hatch, without dropping the rest of the existing PATH.
func TestOpenShell_InjectsPerchBinPath_WhenAbsolute(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	oldPath := "/usr/bin" + string(os.PathListSeparator) + "/bin"
	t.Setenv("PATH", oldPath)
	ls, err := envsync.New(nil, func(string, []string) {})
	if err != nil {
		t.Fatalf("envsync.New: %v", err)
	}
	t.Cleanup(func() { _ = ls.Close() })

	cwd := t.TempDir()
	const bin = "/opt/perch/bin/perch"
	env := openShellCapturesEnv(t, "shell-ws1", cwd, ls, nil, bin)

	gotPath, ok := envSliceGet(env, "PATH")
	if !ok {
		t.Fatalf("drawer env has no PATH entry; got %v", env)
	}
	first := strings.SplitN(gotPath, string(os.PathListSeparator), 2)[0]
	if first != filepath.Dir(bin) {
		t.Errorf("PATH first element = %q, want %q (full PATH=%q)", first, filepath.Dir(bin), gotPath)
	}
	wantPath := filepath.Dir(bin) + string(os.PathListSeparator) + oldPath
	if gotPath != wantPath {
		t.Errorf("PATH = %q, want %q (old tail must be preserved)", gotPath, wantPath)
	}
	if !envSliceHasApp(env, "PERCH_BIN="+bin) {
		t.Errorf("drawer env missing PERCH_BIN=%s; got %v", bin, env)
	}
}

// When perchBin is empty or non-absolute, neither a PATH-prepend nor PERCH_BIN
// may be injected (prepending "." or a bare name would poison PATH).
func TestOpenShell_NoPerchBinInjection_WhenEmptyOrRelative(t *testing.T) {
	for _, bin := range []string{"", "perch"} {
		name := "empty"
		if bin != "" {
			name = "relative"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("SHELL", "/bin/sh")
			oldPath := "/usr/bin" + string(os.PathListSeparator) + "/bin"
			t.Setenv("PATH", oldPath)
			ls, err := envsync.New(nil, func(string, []string) {})
			if err != nil {
				t.Fatalf("envsync.New: %v", err)
			}
			t.Cleanup(func() { _ = ls.Close() })

			cwd := t.TempDir()
			env := openShellCapturesEnv(t, "shell-ws1", cwd, ls, nil, bin)

			if envSliceHasPrefix(env, "PERCH_BIN=") {
				t.Errorf("drawer env must NOT have PERCH_BIN for perchBin=%q; got %v", bin, env)
			}
			gotPath, ok := envSliceGet(env, "PATH")
			if !ok {
				t.Fatalf("drawer env has no PATH entry; got %v", env)
			}
			if gotPath != oldPath {
				t.Errorf("PATH = %q, want unchanged %q (no prepend for perchBin=%q)", gotPath, oldPath, bin)
			}
		})
	}
}

// ── OpenWorkspace applies overlay at the agent pane ──────────────────────────

func TestOpenWorkspace_AppliesOverlayAtAgentPane(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, _ := registry.Load(t.TempDir())
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-ov", WorktreePath: wt, Agent: "claude", Title: "t"})

	fm := agent.NewFakeMonitor(nil)
	fm.SetLaunchCmd("claude --resume abc\n")

	var mu sync.Mutex
	var capturedEnv []string
	a := &App{
		store:      store,
		roots:      []string{wt},
		emit:       func(string, ...any) {},
		bridges:    map[string]*internalpty.Bridge{},
		monitors:   map[string]agent.Monitor{},
		cancels:    map[string]context.CancelFunc{},
		envOverlay: map[string][]string{"ws-ov": {"AWS_PROFILE=synced"}},
		spawnPty: func(_ context.Context, _ string, _ []string, env []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			mu.Lock()
			capturedEnv = env
			mu.Unlock()
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) { return fm, nil },
		newAdapter: fakeAdapterSeam(&fakeAdapter{name: "claude", detect: true}),
	}
	if err := a.OpenWorkspace("ws-ov"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseWorkspace("ws-ov") })

	mu.Lock()
	got := append([]string(nil), capturedEnv...)
	mu.Unlock()
	if !envSliceHasApp(got, "AWS_PROFILE=synced") {
		t.Errorf("agent pane env missing overlay var AWS_PROFILE=synced; got %v", got)
	}
	if !envSliceHasPrefix(got, "HOME=") {
		t.Errorf("agent pane env clobbered os.Environ() — no HOME present")
	}
}

// ── onEnvSync stores overlay and dispatches an async relaunch ─────────────────

func TestOnEnvSync_StoresOverlayAndRelaunchesAsync(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, _ := registry.Load(t.TempDir())
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-sync", WorktreePath: wt, Agent: "claude", Title: "t"})

	fm := agent.NewFakeMonitor(nil)
	fm.SetLaunchCmd("claude --resume abc\n")

	var mu sync.Mutex
	var capturedEnv []string
	spawned := make(chan struct{}, 1)
	a := &App{
		store:      store,
		roots:      []string{wt},
		emit:       func(string, ...any) {},
		bridges:    map[string]*internalpty.Bridge{},
		monitors:   map[string]agent.Monitor{},
		cancels:    map[string]context.CancelFunc{},
		envOverlay: map[string][]string{},
		spawnPty: func(_ context.Context, _ string, _ []string, env []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			mu.Lock()
			capturedEnv = env
			mu.Unlock()
			select {
			case spawned <- struct{}{}:
			default:
			}
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) { return fm, nil },
		newAdapter: fakeAdapterSeam(&fakeAdapter{name: "claude", detect: true}),
	}

	a.onEnvSync("ws-sync", []string{"API_TOKEN=fresh"})

	// The overlay must be stored synchronously (before the async relaunch reads it).
	if ov := a.overlayFor("ws-sync"); len(ov) != 1 || ov[0] != "API_TOKEN=fresh" {
		t.Fatalf("overlay not stored synchronously; got %v", ov)
	}

	select {
	case <-spawned:
	case <-time.After(2 * time.Second):
		t.Fatal("onEnvSync did not dispatch an async OpenWorkspace relaunch")
	}
	t.Cleanup(func() { _ = a.CloseWorkspace("ws-sync") })

	mu.Lock()
	got := append([]string(nil), capturedEnv...)
	mu.Unlock()
	if !envSliceHasApp(got, "API_TOKEN=fresh") {
		t.Errorf("relaunched agent pane missing synced var API_TOKEN=fresh; got %v", got)
	}
}

// A reload respawns the agent pty under the same paneID; the frontend must remount
// a fresh xterm so the new `claude --resume` does not redraw over the stale buffer
// (the garble seen after a reload). onEnvSync signals that remount by emitting
// workspace:relaunch with the workspace id, and it must fire SYNCHRONOUSLY — before
// the async respawn — so the fresh, correctly-sized pane is ready as the agent draws.
func TestOnEnvSync_EmitsWorkspaceRelaunchSynchronously(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, _ := registry.Load(t.TempDir())
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-relaunch", WorktreePath: wt, Agent: "claude", Title: "t"})

	fm := agent.NewFakeMonitor(nil)
	fm.SetLaunchCmd("claude --resume abc\n")

	var mu sync.Mutex
	var relaunchIDs []string
	spawned := make(chan struct{}, 1)
	a := &App{
		store: store,
		roots: []string{wt},
		emit: func(event string, data ...any) {
			if event != evtWorkspaceRelaunch {
				return // ignore the agent:event/etc. the async relaunch also emits
			}
			mu.Lock()
			defer mu.Unlock()
			if len(data) > 0 {
				if m, ok := data[0].(map[string]any); ok {
					if id, ok := m["workspaceId"].(string); ok {
						relaunchIDs = append(relaunchIDs, id)
					}
				}
			}
		},
		bridges:    map[string]*internalpty.Bridge{},
		monitors:   map[string]agent.Monitor{},
		cancels:    map[string]context.CancelFunc{},
		envOverlay: map[string][]string{},
		spawnPty: func(_ context.Context, _ string, _ []string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			select {
			case spawned <- struct{}{}:
			default:
			}
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) { return fm, nil },
		newAdapter: fakeAdapterSeam(&fakeAdapter{name: "claude", detect: true}),
	}

	a.onEnvSync("ws-relaunch", []string{"API_TOKEN=fresh"})

	// The remount signal must already be recorded the instant onEnvSync returns —
	// it is emitted before the goroutine, so no waiting is needed for THIS check.
	mu.Lock()
	gotSync := append([]string(nil), relaunchIDs...)
	mu.Unlock()
	if len(gotSync) != 1 || gotSync[0] != "ws-relaunch" {
		t.Fatalf("workspace:relaunch not emitted synchronously with the workspace id; got %v", gotSync)
	}

	// Drain the async relaunch so CloseWorkspace has a live pane to tear down cleanly.
	select {
	case <-spawned:
	case <-time.After(2 * time.Second):
		t.Fatal("onEnvSync did not dispatch the async relaunch")
	}
	t.Cleanup(func() { _ = a.CloseWorkspace("ws-relaunch") })
}
