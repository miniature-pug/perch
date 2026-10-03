//go:build integration

// reopen_bug_test.go: the "reopen after /exit" bug, exercised end-to-end against a
// REAL ClaudeMonitor and REAL hook listener (never the FakeMonitor), with REAL pty
// bridges. It reproduces two combining backend defects behind the stuck "session
// has ended" overlay and silent-zombie reopen. It locks in both fixes:
//
//	D1 (hooks lost on reopen): after Reopen, the new monitor's own hook settings
//	   file (claude --settings <file>) must carry its LIVE listener addr and
//	   token, not the old, dead one. This way, the reopened agent's hooks POST
//	   to a listener that perch actually watches. The test proves the listener
//	   is LIVE: it POSTs hooks to the addr and token read back from the file,
//	   and it observes StateRunning flow through.
//	D2 (stale pty:exit re-latches the overlay): closing the DISPLACED login-shell
//	   bridge must NOT emit on the shared "pty:exit:pane-<id>" name, or the
//	   remounted Terminal would catch the stale exit and re-latch the overlay.
//
// The agent is never launched. Its hook POSTs (SessionStart and AgentExit) go
// directly over HTTP, exactly as the real claude hooks and exit sentinel would.
package app

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// presentAdapter wraps a real adapter but always reports the agent CLI as
// installed, so these tests exercise the real monitor on hosts without the
// claude binary (OpenWorkspace skips the monitor when Detect is false).
type presentAdapter struct{ agent.Adapter }

func (presentAdapter) Detect() bool { return true }

// realAdapterPresent is a newAdapter seam that returns the real adapter for
// tool, wrapped in presentAdapter.
func realAdapterPresent(tool string) agent.Adapter {
	if ad := agentAdapter(tool); ad != nil {
		return presentAdapter{ad}
	}
	return nil
}

// hookSettingsPath returns the per-session hook settings file of the live
// claude monitor of workspace id (claude --settings <file>).
func hookSettingsPath(t *testing.T, a *App, id string) string {
	t.Helper()
	a.mu.Lock()
	m := a.monitors[id]
	a.mu.Unlock()
	cm, ok := m.(*agent.ClaudeMonitor)
	if !ok {
		t.Fatalf("workspace %s has no live ClaudeMonitor (got %T)", id, m)
	}
	return cm.HookSettingsPath()
}

// hookAddrToken extracts the loopback addr (host:port) and the hex Bearer token
// from a claude monitor's per-session hook settings file.
// hookAddrToken fails the test if either value is absent. An absent token is
// itself the D1 symptom: Teardown stripped every perch group, and nothing
// re-asserted the new one.
func hookAddrToken(t *testing.T, settingsPath string) (addr, token string) {
	t.Helper()
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read %s: %v", settingsPath, err)
	}
	tokMatch := regexp.MustCompile(`Bearer (\S+)`).FindSubmatch(raw)
	if tokMatch == nil {
		t.Fatalf("no Bearer token in settings.json (D1: reopened session has no live hooks):\n%s", raw)
	}
	token = regexp.MustCompile(`^[0-9a-fA-F]+`).FindString(string(tokMatch[1]))
	if token == "" {
		t.Fatalf("could not extract hex token from %q", tokMatch[1])
	}
	addrMatch := regexp.MustCompile(`http://([^/ "]+)/hook`).FindSubmatch(raw)
	if addrMatch == nil {
		t.Fatalf("no hook URL in settings.json:\n%s", raw)
	}
	return string(addrMatch[1]), token
}

// postHook POSTs a hook payload to the loopback listener with the Bearer token,
// exactly as the real claude hooks and exit sentinel do.
func postHook(t *testing.T, addr, token, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/hook", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST hook to %s: %v", addr, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST hook to %s: status %d", addr, resp.StatusCode)
	}
}

func TestOpenWorkspace_ReopenAfterExit_RewritesHooksAndSilencesStaleExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// ── git repo under a configured root ──────────────────────────────────────
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-qm", "init"},
	} {
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	cfgDir := t.TempDir()
	store, err := registry.Load(cfgDir)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	a := NewApp(store, []string{root})
	a.settingsPath = filepath.Join(cfgDir, "settings.json")
	a.layoutPath = filepath.Join(cfgDir, "layout.json")

	// ── emit capture seam ─────────────────────────────────────────────────────
	var emitMu sync.Mutex
	var emitted []capturedEmit
	a.emit = func(event string, data ...any) {
		var d any
		if len(data) > 0 {
			d = data[0]
		}
		emitMu.Lock()
		emitted = append(emitted, capturedEmit{event: event, data: d})
		emitMu.Unlock()
	}

	// ── spawnPty seam: a REAL bridge (real reaper) wraps a benign, long-lived
	// process that stands in for the login shell. This shell SURVIVES the agent's
	// /exit. The test forwards the exitEvent name and emit callback verbatim. So
	// the displaced bridge's reaper would emit on exactly the shared
	// "pty:exit:pane-<id>" name that the bug involves, unless the fix disarms the
	// reaper.
	a.spawnPty = func(sctx context.Context, cwd string, _ []string, env []string, dataEvent, exitEvent string,
		emit internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error) {
		return internalpty.Spawn(sctx, cwd, []string{"sleep", "3600"}, env, dataEvent, exitEvent, emit, cols, rows)
	}
	// newMonitor stays at its real default (real ClaudeMonitor and real hook
	// listener); the adapter is real but always reports claude as installed.
	a.newAdapter = realAdapterPresent

	// ── create + open (monitor-1) ─────────────────────────────────────────────
	vm, err := a.CreateWorkspace("claude", repo, "main", "feat/reopen", "", true)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	wsID := vm.ID
	paneExitEvent := "pty:exit:" + paneIDFor(wsID)

	if err := a.OpenWorkspace(wsID); err != nil {
		t.Fatalf("OpenWorkspace (first): %v", err)
	}
	t.Cleanup(func() { _ = a.CloseWorkspace(wsID) })

	settings1 := hookSettingsPath(t, a, wsID)
	addr1, token1 := hookAddrToken(t, settings1)
	t.Logf("monitor-1 hook addr=%s token=%s…", addr1, token1[:8])

	// ── simulate a graceful agent /exit (shell survives): the exit sentinel POSTs
	// AgentExit to the same listener → StateExited (the overlay latches). ─────────
	postHook(t, addr1, token1, `{"hook_event_name":"AgentExit","error_type":"0"}`)
	if _, ok := pollEvent(t, time.Now().Add(10*time.Second), &emitMu, &emitted, func(e capturedEmit) bool {
		ev, ok := e.data.(agent.Event)
		return e.event == "agent:event" && ok && ev.State == agent.StateExited && ev.WorkspaceID == wsID
	}); !ok {
		t.Fatalf("timed out waiting for StateExited after the simulated agent /exit")
	}

	// baseline: nothing has emitted a pane pty:exit yet (the shell is still alive).
	assertNoPaneExit(t, &emitMu, &emitted, paneExitEvent, "before reopen")

	// ── Reopen (monitor-2) ────────────────────────────────────────────────────
	// Reopen displaces the surviving shell, using SuppressExit and Close. Reopen
	// also displaces monitor-1, using Teardown and a hook re-assertion onto
	// monitor-2.
	if err := a.OpenWorkspace(wsID); err != nil {
		t.Fatalf("OpenWorkspace (reopen): %v", err)
	}

	// ── D1 assertion: monitor-2's own settings file carries NEW, LIVE creds ─────
	settings2 := hookSettingsPath(t, a, wsID)
	addr2, token2 := hookAddrToken(t, settings2)
	t.Logf("monitor-2 hook addr=%s token=%s…", addr2, token2[:8])
	if token2 == token1 {
		t.Fatalf("D1: the reopened session still carries the OLD listener token — "+
			"the reopened agent would POST to a dead listener (token1=%s)", token1)
	}
	if settings2 == settings1 {
		t.Fatalf("D1: reopen reused monitor-1's settings file %s", settings1)
	}
	// Prove the credentials are LIVE. POST a SessionStart (idle) and then a
	// UserPromptSubmit (running) to the addr and token read back from the file.
	// The prompt must flow through monitor-2 as StateRunning. This shows that
	// the reopened agent CAN heal the overlay, and perch CAN observe the
	// session.
	postHook(t, addr2, token2, `{"hook_event_name":"SessionStart","session_id":"ses_reopen"}`)
	postHook(t, addr2, token2, `{"hook_event_name":"UserPromptSubmit","session_id":"ses_reopen"}`)
	if _, ok := pollEvent(t, time.Now().Add(10*time.Second), &emitMu, &emitted, func(e capturedEmit) bool {
		ev, ok := e.data.(agent.Event)
		return e.event == "agent:event" && ok && ev.State == agent.StateRunning && ev.WorkspaceID == wsID
	}); !ok {
		t.Fatalf("D1: reopened agent's hooks to %s produced no StateRunning — hooks are not live", addr2)
	}

	// ── D2 assertion: closing the displaced shell must NOT emit pty:exit on the
	// shared pane name. Reopen already reaped the displaced shell. The test waits a
	// generous window, since the buggy path would emit well before this window
	// ends. Then the test asserts silence. The replacement shell is still alive, so
	// nothing else emits that event. ────
	time.Sleep(2 * time.Second)
	assertNoPaneExit(t, &emitMu, &emitted, paneExitEvent, "after reopen (displaced shell must be silent)")
}

// assertNoPaneExit fails if any captured emit used the pane's pty:exit event name.
func assertNoPaneExit(t *testing.T, mu *sync.Mutex, events *[]capturedEmit, exitEvent, when string) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	for _, e := range *events {
		if e.event == exitEvent {
			t.Fatalf("D2: stale %q emitted (%s) — a displaced shell re-latches the overlay", exitEvent, when)
		}
	}
}
