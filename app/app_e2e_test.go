//go:build integration

// app_e2e_test.go: a headless integration test that locks backend "seam"
// contracts. The fake-agent binary drives the real ClaudeMonitor and hook
// listener. It does not launch any real claude or opencode binary.
//
// Contracts locked:
//
//	pty wire: OpenWorkspace passes dataEvent == "pty:data:pane-<id>". This makes
//	   the backend emit on exactly the channel WorkspaceVM.PaneID ("pane-<id>")
//	   that the frontend Terminal subscribes to. The fake spawnPty captures the
//	   event name and asserts it. The fake spawnPty also fires the emit callback
//	   once to exercise the path.
//	   NOTE: this test uses a fake bridge. The manual smoke checklist verifies the
//	   true cross-process round-trip (real pty bytes -> WebKit -> xterm).
//	ListWorkspaces populates PaneID, LastActive, and Branch. seam_bugs_test.go
//	   covers this. This test re-verifies the same behavior by round-trip through
//	   CreateWorkspace.
//	OpenWorkspace stamps WorkspaceID on every forwarded agent:event.
//	OpenWorkspace composes Approval.ReqID as "<raw>:<wsID>". Approve parses it
//	   back correctly.
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

	"github.com/miniature-pug/perch/internal/agent"
	git "github.com/miniature-pug/perch/internal/git"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// pollEvent scans the captured emit slice. It returns the first entry that
// matches pred. It polls until the deadline and sleeps 30 ms between checks.
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
	// ── build fake-agent binary ──────────────────────────────────────────────
	// Build before the HOME sandbox below. The go build cache (GOCACHE)
	// defaults to $HOME/.cache/go-build. A sandboxed HOME points GOCACHE at an
	// empty directory and forces a cold recompile on every run. A separate
	// build deadline also stops a cold CI compile from spending the loop
	// guard's 30-second budget.
	fakeAgentBin := filepath.Join(t.TempDir(), "fake-agent")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", fakeAgentBin,
		"github.com/miniature-pug/perch/cmd/fake-agent")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake-agent: %v: %s", err, out)
	}

	// ── sandbox HOME and XDG_CONFIG_HOME BEFORE NewApp (so settingsPath is sandboxed) ──
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// ── 30-second guard for the loop ─────────────────────────────────────────
	// This deadline bounds the fake-agent runtime and the event polling. The
	// build above has its own deadline, so a slow build never spends this
	// budget.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── set up git repo ──────────────────────────────────────────────────────
	// root is the configured root. The repo lives inside root.
	// WorktreePath (worktreeDir=="") puts the linked worktree at:
	//   <parent of repo>/<basename>__worktrees/<slug>
	//   = <root>/<basename>__worktrees/<slug>
	// So root must be the parent of repo. root is also a valid root for both the
	// repo (for DiffStat) and the derived worktree (for containedUnderRoots).
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
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
	// Capture the event-name args that OpenWorkspace passes. This lets the test
	// assert the pty data wire: dataEvent must equal "pty:data:" + vm.PaneID.
	// NOTE: this test uses a fake bridge. The manual checklist smoke-tests the
	// true cross-process round-trip (real pty bytes → WebKit → xterm).
	var capturedDataEvent, capturedExitEvent string
	a.spawnPty = func(_ context.Context, _ string, _ []string, _ []string, dataEvent, exitEvent string,
		_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
		capturedDataEvent = dataEvent
		capturedExitEvent = exitEvent
		return internalpty.NewBridgeForTest(func() error { return nil }), nil
	}

	// ── keep a.newMonitor at its default (real ClaudeMonitor) ────────────────

	// ── CreateWorkspace ───────────────────────────────────────────────────────
	// Use "feat/e2e" to avoid collision with the repo's default "main" branch.
	vm, err := a.CreateWorkspace("claude", repo, "main", "feat/e2e", "", true)
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

	// ── pty wire: data event name matches WorkspaceVM.PaneID ─────────────────
	//
	// OpenWorkspace computes:
	//   paneID   = "pane-" + id
	//   event    = "pty:data:" + paneID   →  "pty:data:pane-<id>"
	//   exitEvent = "pty:exit:" + paneID  →  "pty:exit:pane-<id>"
	//
	// (a) The VM exposes the pane ID that the Terminal subscribes with.
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
	// Strip trailing non-hex chars (for example the surrounding quote and " # sentinel").
	// The token is hex. Grab characters up to the first non-hex character.
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
	addr := string(addrMatch[1]) // for example "127.0.0.1:PORT"

	hookURL := "http://" + addr + "/hook"
	t.Logf("hook URL: %s, token: %s", hookURL, token)

	// ── launch fake-agent in background ──────────────────────────────────────
	// The monitor does not filter PERCH_SESSION_ID (translateAndEmit does not
	// correlate by session_id). So any valid value works.
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
	// Always clean up fake-agent if the test exits early.
	t.Cleanup(func() {
		if agentCmd.Process != nil {
			_ = agentCmd.Process.Kill()
		}
	})
	go func() {
		agentExitCh <- agentCmd.Wait()
	}()

	// ── ASSERTION 1: wait for agent:event with StateAwaitingApproval + WorkspaceID ──
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

	// ── ASSERTION 2: WorkspaceID is stamped ───────────────────────────────────
	if ev.WorkspaceID != wsID {
		t.Errorf("bug-4: agent:event WorkspaceID = %q, want %q", ev.WorkspaceID, wsID)
	}

	// ── ASSERTION 3: ReqID has suffix ":<wsID>" ──────────────────────────────
	if !strings.HasSuffix(compositeReqID, ":"+wsID) {
		t.Errorf("bug-5: compositeReqID %q does not have suffix %q", compositeReqID, ":"+wsID)
	}

	// ── ASSERTION 4 (round-trip): Approve with composite ReqID succeeds ───────
	// Capture reqID under lock then release before calling Approve (which may
	// block on the hook listener's pending map).
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

	// ── ASSERTION 6: wait for terminal StateDone event with WorkspaceID ──────
	// The claude monitor translates a Stop hook into StateDone. A completed turn
	// drives the ambient "Turn complete" notification in dispatchNotify.
	// (The code reserves StateIdle for steady, non-terminal idle, for example
	// opencode idle-at-connect.)
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

	// ── ASSERTION 7: DiffStat returns the staged hello.go with correct counts ──
	// hello.go contains "package main\n" (1 line), staged as a new file (A).
	// git diff --cached --numstat reports 1 added and 0 removed for hello.go.
	// DiffStat validates against roots, so pass repo (under root).
	stat, err := a.DiffStat(repo)
	if err != nil {
		t.Fatalf("DiffStat(%q): %v", repo, err)
	}
	t.Logf("DiffStat: %+v", stat)
	var helloStat *git.FileDiff
	for i := range stat {
		if filepath.Base(stat[i].Path) == "hello.go" {
			helloStat = &stat[i]
			break
		}
	}
	if helloStat == nil {
		t.Errorf("DiffStat: hello.go not found in result; got %+v", stat)
	} else {
		if helloStat.Added != 1 {
			t.Errorf("DiffStat hello.go Added = %d, want 1 (staged new file with 1 line)", helloStat.Added)
		}
		if helloStat.Removed != 0 {
			t.Errorf("DiffStat hello.go Removed = %d, want 0", helloStat.Removed)
		}
		if helloStat.Status != "A" {
			t.Errorf("DiffStat hello.go Status = %q, want \"A\"", helloStat.Status)
		}
	}

	t.Logf("composite ReqID format observed: %q", compositeReqID)
}
