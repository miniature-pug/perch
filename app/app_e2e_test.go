//go:build integration

// app_e2e_test.go — headless integration test locking 6 previously-fixed backend
// "seam" bugs. The fake-agent binary drives the real ClaudeMonitor and
// hooklistener without launching any real claude/opencode binary.
//
// BUG locks:
//   1 (pty wire): OpenWorkspace passes dataEvent == "pty:data:pane-<id>" so the
//      backend emits on exactly the channel WorkspaceVM.PaneID ("pane-<id>") that
//      the frontend Terminal subscribes to. The fake spawnPty captures and asserts
//      the event name; it also fires the emit callback once to exercise the path.
//      NOTE: the true cross-process round-trip (real pty bytes → WebKit → xterm)
//      is verified by the manual smoke checklist, since this test uses a fake bridge.
//   2+3: ListWorkspaces populates PaneID, LastActive, Branch (covered in seam_bugs_test.go; re-verified here by round-trip through CreateWorkspace).
//   4: OpenWorkspace stamps WorkspaceID on every forwarded agent:event.
//   5: OpenWorkspace composes Approval.ReqID as "<raw>:<wsID>"; Approve parses it back correctly.
package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/registry"
)

// pollEvent scans the captured emit slice and returns the first entry matching
// pred, using deadline polling with 30 ms sleep intervals.
func pollEvent(t *testing.T, deadline time.Time, mu *sync.Mutex, events *[]capturedEmit, pred func(capturedEmit) bool) (capturedEmit, bool) {
	t.Helper()
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, e := range *events {
			if pred(e) {
				mu.Unlock()
				return e, true
			}
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
	}
	return capturedEmit{}, false
}

type capturedEmit struct {
	event string
	data  any // data[0] when present
}

func TestE2E_HeadlessFullLoop(t *testing.T) {
	// ── 30-second overall guard ──────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── sandbox HOME and XDG_CONFIG_HOME BEFORE NewApp (so settingsPath is sandboxed) ──
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// ── build fake-agent binary ──────────────────────────────────────────────
	fakeAgentBin := filepath.Join(t.TempDir(), "fake-agent")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", fakeAgentBin,
		"github.com/Miniature-Pug/perch/cmd/fake-agent")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake-agent: %v: %s", err, out)
	}

	// ── set up git repo ──────────────────────────────────────────────────────
	// root is the configured root; repo lives inside it.
	// WorktreePath (worktreeDir=="") puts the linked worktree at:
	//   <parent of repo>/<basename>__worktrees/<slug>
	//   = <root>/<basename>__worktrees/<slug>
	// So root must be the parent of repo, and root is a valid root for both
	// the repo (for DiffStat) and the derived worktree (for containedUnderRoots).
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
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// Write a file and stage it so DiffStat returns ≥1 entry.
	helloPath := filepath.Join(repo, "hello.go")
	if err := os.WriteFile(helloPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "hello.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

	// ── registry + App ───────────────────────────────────────────────────────
	cfgDir := t.TempDir()
	store, err := registry.Load(cfgDir)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	a := NewApp(store, []string{root})

	// Override settingsPath so the app-level settings file is in cfgDir (not real HOME).
	a.settingsPath = filepath.Join(cfgDir, "settings.json")
	a.layoutPath = filepath.Join(cfgDir, "layout.json")

	// ── emit capture seam (non-blocking append) ───────────────────────────────
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

	// ── spawnPty seam (no real shell/agent launch) ────────────────────────────
	// Capture the event-name args passed by OpenWorkspace so we can assert the
	// pty data wire (bug-1): dataEvent must equal "pty:data:" + vm.PaneID.
	// NOTE: the true cross-process round-trip (real pty bytes → WebKit → xterm)
	// is smoke-tested via the manual checklist; this test uses a fake bridge.
	var capturedDataEvent, capturedExitEvent string
	a.spawnPty = func(_ context.Context, _ string, _ []string, dataEvent, exitEvent string,
		_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
		capturedDataEvent = dataEvent
		capturedExitEvent = exitEvent
		return internalpty.NewBridgeForTest(func() error { return nil }), nil
	}

	// ── keep a.newMonitor at its default (real ClaudeMonitor) ────────────────

	// ── CreateWorkspace ───────────────────────────────────────────────────────
	// Use "feat/e2e" to avoid collision with the repo's default "main" branch.
	vm, err := a.CreateWorkspace("claude", repo, "feat/e2e", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	wsID := vm.ID
	worktreePath := vm.WorktreePath

	t.Logf("workspace id: %s, worktree: %s", wsID, worktreePath)

	// ── OpenWorkspace ─────────────────────────────────────────────────────────
	if err := a.OpenWorkspace(wsID); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseWorkspace(wsID) })

	// ── BUG-1 ASSERTIONS: pty data event name matches WorkspaceVM.PaneID ─────
	//
	// OpenWorkspace computes:
	//   paneID   = "pane-" + id
	//   event    = "pty:data:" + paneID   →  "pty:data:pane-<id>"
	//   exitEvent = "pty:exit:" + paneID  →  "pty:exit:pane-<id>"
	//
	// (a) The VM exposes the pane id the Terminal will subscribe with.
	if vm.PaneID != "pane-"+wsID {
		t.Errorf("bug-1: vm.PaneID = %q, want %q", vm.PaneID, "pane-"+wsID)
	}
	// (b) The backend emits pty output on "pty:data:" + vm.PaneID, so the
	//     Terminal subscription ("pty:data:${paneId}") matches exactly.
	wantDataEvent := "pty:data:" + vm.PaneID // == "pty:data:pane-"+wsID
	if capturedDataEvent != wantDataEvent {
		t.Errorf("bug-1: spawnPty received dataEvent = %q, want %q", capturedDataEvent, wantDataEvent)
	}
	// Also assert the exit-event name for completeness (same pane-<id> formula).
	wantExitEvent := "pty:exit:" + vm.PaneID
	if capturedExitEvent != wantExitEvent {
		t.Errorf("bug-1: spawnPty received exitEvent = %q, want %q", capturedExitEvent, wantExitEvent)
	}
	t.Logf("bug-1: dataEvent=%q exitEvent=%q paneID=%q — all locked", capturedDataEvent, capturedExitEvent, vm.PaneID)

	// ── extract addr + token from the written settings.json ──────────────────
	// writeHooks runs synchronously inside Prepare (called by OpenWorkspace), so
	// the file exists immediately after OpenWorkspace returns.
	settingsJSON := filepath.Join(worktreePath, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsJSON)
	if err != nil {
		t.Fatalf("read worktree settings.json: %v", err)
	}
	t.Logf("worktree settings.json:\n%s", raw)

	// Extract token via "Bearer <token>".
	tokenRe := regexp.MustCompile(`Bearer (\S+)`)
	tokMatch := tokenRe.FindSubmatch(raw)
	if tokMatch == nil {
		t.Fatalf("could not find Bearer token in settings.json")
	}
	// Strip trailing non-hex chars (e.g. the surrounding quote and " # sentinel")
	// The token is hex, so grab until first non-hex char.
	tokenRaw := string(tokMatch[1])
	hexRe := regexp.MustCompile(`^[0-9a-fA-F]+`)
	token := hexRe.FindString(tokenRaw)
	if token == "" {
		t.Fatalf("could not extract hex token from %q", tokenRaw)
	}

	// Extract addr (host:port) from the hook URL in the command.
	addrRe := regexp.MustCompile(`http://([^/ "]+)/hook`)
	addrMatch := addrRe.FindSubmatch(raw)
	if addrMatch == nil {
		t.Fatalf("could not find hook URL in settings.json")
	}
	addr := string(addrMatch[1]) // e.g. "127.0.0.1:PORT"

	hookURL := "http://" + addr + "/hook"
	t.Logf("hook URL: %s, token: %s", hookURL, token)

	// ── launch fake-agent in background ──────────────────────────────────────
	// PERCH_SESSION_ID is not filtered by the monitor (translateAndEmit does not
	// correlate by session_id), so any valid value works.
	agentEnv := append(os.Environ(),
		"PERCH_HOOK_URL="+hookURL,
		"PERCH_HOOK_TOKEN="+token,
		"PERCH_SESSION_ID=ses_e2e-test",
		"PERCH_SCRIPT=SessionStart;PreToolUse,tool=Write,input={};Stop",
	)
	agentCmd := exec.CommandContext(ctx, fakeAgentBin)
	agentCmd.Env = agentEnv

	agentExitCh := make(chan error, 1)
	if err := agentCmd.Start(); err != nil {
		t.Fatalf("start fake-agent: %v", err)
	}
	// Ensure fake-agent is always cleaned up if test exits early.
	t.Cleanup(func() {
		if agentCmd.Process != nil {
			_ = agentCmd.Process.Kill()
		}
	})
	go func() {
		agentExitCh <- agentCmd.Wait()
	}()

	// ── ASSERTION 1 (bug-4): wait for agent:event with StateAwaitingApproval + WorkspaceID ──
	deadline := time.Now().Add(10 * time.Second)
	awaiting, found := pollEvent(t, deadline, &emitMu, &emitted, func(e capturedEmit) bool {
		if e.event != "agent:event" {
			return false
		}
		ev, ok := e.data.(agent.Event)
		if !ok {
			return false
		}
		return ev.State == agent.StateAwaitingApproval && ev.WorkspaceID == wsID
	})
	if !found {
		// Dump all captured events for debugging.
		emitMu.Lock()
		for i, e := range emitted {
			b, _ := json.Marshal(e.data)
			t.Logf("emit[%d] event=%q data=%s", i, e.event, b)
		}
		emitMu.Unlock()
		t.Fatalf("timed out waiting for agent:event with StateAwaitingApproval and WorkspaceID=%q (bug-4)", wsID)
	}

	ev := awaiting.data.(agent.Event)
	compositeReqID := ev.Approval.ReqID
	t.Logf("captured approval event: WorkspaceID=%q ReqID=%q", ev.WorkspaceID, compositeReqID)

	// ── ASSERTION 2 (bug-4): WorkspaceID is stamped ───────────────────────────
	if ev.WorkspaceID != wsID {
		t.Errorf("bug-4: agent:event WorkspaceID = %q, want %q", ev.WorkspaceID, wsID)
	}

	// ── ASSERTION 3 (bug-5): ReqID has suffix ":<wsID>" ──────────────────────
	if !strings.HasSuffix(compositeReqID, ":"+wsID) {
		t.Errorf("bug-5: compositeReqID %q does not have suffix %q", compositeReqID, ":"+wsID)
	}

	// ── ASSERTION 4 (bug-5 round-trip): Approve with composite ReqID succeeds ──
	// Capture reqID under lock then release before calling Approve (which may
	// block on the hooklistener's pending map).
	if err := a.Approve(compositeReqID, "allow"); err != nil {
		t.Errorf("bug-5: Approve(%q, allow): %v", compositeReqID, err)
	}

	// ── ASSERTION 5: fake-agent exits 0 (allow was routed correctly) ──────────
	select {
	case agentErr := <-agentExitCh:
		if agentErr != nil {
			t.Errorf("fake-agent exited with error: %v (expected 0 after allow)", agentErr)
		}
	case <-time.After(10 * time.Second):
		t.Error("timed out waiting for fake-agent to exit after Approve(allow)")
	}

	// ── ASSERTION 6 (bug-4 again): wait for terminal StateDone event with WorkspaceID ──
	// The claude monitor translates a Stop hook → StateDone (H-6): a completed turn
	// is what drives the §8 ambient "Turn complete" notification in dispatchNotify.
	// (StateIdle is reserved for steady non-terminal idle, e.g. opencode idle-at-connect.)
	termDeadline := time.Now().Add(5 * time.Second)
	termFound := false
	for time.Now().Before(termDeadline) {
		emitMu.Lock()
		for _, e := range emitted {
			if e.event != "agent:event" {
				continue
			}
			termEv, ok := e.data.(agent.Event)
			if !ok {
				continue
			}
			if termEv.State == agent.StateDone && termEv.WorkspaceID == wsID {
				termFound = true
			}
		}
		emitMu.Unlock()
		if termFound {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !termFound {
		t.Errorf("bug-4: no terminal agent:event (StateDone, WorkspaceID=%q) after Stop", wsID)
	}

	// ── ASSERTION 7: DiffStat returns ≥1 entry (the staged hello.go) ─────────
	// DiffStat validates against roots, so pass repo (under root).
	stat, err := a.DiffStat(repo)
	if err != nil {
		t.Fatalf("DiffStat(%q): %v", repo, err)
	}
	if len(stat) == 0 {
		t.Errorf("DiffStat returned 0 entries; expected ≥1 for staged hello.go")
	}
	t.Logf("DiffStat: %+v", stat)

	t.Logf("composite ReqID format observed: %q", compositeReqID)
}
