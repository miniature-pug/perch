package app

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/envsync"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// openLifecycle registers workspace id under a fresh temp worktree and opens
// it.
func openLifecycle(t *testing.T, l *lifecycleApp, id string) {
	t.Helper()
	wt := t.TempDir()
	l.roots = append(l.roots, wt)
	_ = l.store.Upsert(registry.Workspace{ID: id, WorktreePath: wt, Agent: "claude", Title: "t"})
	if err := l.OpenWorkspace(id); err != nil {
		t.Fatalf("OpenWorkspace(%s): %v", id, err)
	}
	t.Cleanup(func() { _ = l.CloseWorkspace(id) })
}

// TestApp_AgentMissing_SkipsMonitor is the AGT-22 regression guard: with the
// agent CLI missing, OpenWorkspace must not create, prepare or start a
// monitor (Prepare would stand up listeners for an agent that can never
// connect), but it still opens the shell and says why.
func TestApp_AgentMissing_SkipsMonitor(t *testing.T) {
	wt := t.TempDir()
	l := newLifecycleApp(t, []string{wt})
	var created atomic.Int32
	l.newMonitor = func(string, agent.Adapter) (agent.Monitor, error) {
		created.Add(1)
		return agent.NewFakeMonitor(nil), nil
	}
	l.newAdapter = fakeAdapterSeam(&fakeAdapter{name: "claude", detect: false})
	_ = l.store.Upsert(registry.Workspace{ID: "ws-miss", WorktreePath: wt, Agent: "claude"})
	if err := l.OpenWorkspace("ws-miss"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	defer func() { _ = l.CloseWorkspace("ws-miss") }()
	if n := created.Load(); n != 0 {
		t.Errorf("newMonitor called %d times for a missing agent, want 0", n)
	}
	l.mu.Lock()
	_, hasMon := l.monitors["ws-miss"]
	_, hasPane := l.bridges[paneIDFor("ws-miss")]
	l.mu.Unlock()
	if hasMon {
		t.Error("a monitor was registered for a missing agent")
	}
	if !hasPane {
		t.Error("the shell pane was not opened")
	}
	if _, ok := findNotify(l.snapshot(), "blocking", "Agent not found"); !ok {
		t.Error("no blocking 'Agent not found' notice")
	}
}

// TestApp_ResolvedReqID_DropsPendingAndComposesID covers wiring item 1: an
// event carrying ResolvedReqID removes the pending card and is forwarded
// with the composed "<raw>:<workspace>" id.
func TestApp_ResolvedReqID_DropsPendingAndComposesID(t *testing.T) {
	l := newLifecycleApp(t, nil)
	openLifecycle(t, l, "ws-r")
	fm := l.lastMonitor()
	fm.Replay(agent.Event{Kind: "approval", State: agent.StateAwaitingApproval, Approval: &agent.ApprovalReq{ReqID: "q1", Tool: "Bash", Input: "ls"}})
	waitFor(t, "pending q1", func() bool { return len(l.PendingApprovals()) == 1 })

	fm.Replay(agent.Event{Kind: "approval-resolved", ResolvedReqID: "q1"})
	waitFor(t, "pending cleared", func() bool { return len(l.PendingApprovals()) == 0 })
	var got string
	for _, r := range l.snapshot() {
		if ev, ok := r.data[0].(agent.Event); ok && r.event == "agent:event" && ev.Kind == "approval-resolved" {
			got = ev.ResolvedReqID
		}
	}
	if got != "q1:ws-r" {
		t.Errorf("forwarded resolvedReqId = %q, want q1:ws-r", got)
	}
	if _, ok := findNotify(l.snapshot(), "blocking", "Approval needed"); !ok {
		t.Error("the approval itself should still notify")
	}
	for _, r := range l.snapshot() {
		if r.event == "notify" {
			if m, _ := r.data[0].(map[string]any); m["state"] == "" {
				t.Errorf("approval-resolved produced a notify: %v", m)
			}
		}
	}
}

// TestApp_Reopen_PurgesAndDeniesStalePending is the APP-9 regression guard.
func TestApp_Reopen_PurgesAndDeniesStalePending(t *testing.T) {
	l := newLifecycleApp(t, nil)
	openLifecycle(t, l, "ws-p")
	oldMon := l.lastMonitor()
	oldMon.Replay(agent.Event{Kind: "approval", State: agent.StateAwaitingApproval, Approval: &agent.ApprovalReq{ReqID: "old", Tool: "Bash", Input: "ls"}})
	waitFor(t, "pending old", func() bool { return len(l.PendingApprovals()) == 1 })

	if err := l.OpenWorkspace("ws-p"); err != nil {
		t.Fatal(err)
	}
	if p := l.PendingApprovals(); len(p) != 0 {
		t.Errorf("PendingApprovals after reopen = %+v, want none", p)
	}
	calls := oldMon.ApproveCalls()
	if len(calls) != 1 || calls[0].ReqID != "old" || calls[0].D.Allow {
		t.Errorf("old monitor Approve calls = %+v, want one deny of 'old'", calls)
	}
}

// TestApp_DisplacedMonitorApproval_NotSurfaced guards the pump: an approval
// from a monitor that is no longer the workspace's current one is denied on
// that monitor and never becomes a pending card.
func TestApp_DisplacedMonitorApproval_NotSurfaced(t *testing.T) {
	l := newLifecycleApp(t, nil)
	openLifecycle(t, l, "ws-d")
	stale := agent.NewFakeMonitor(nil)
	l.forwardEvent("ws-d", stale, agent.Event{Kind: "approval", State: agent.StateAwaitingApproval, Approval: &agent.ApprovalReq{ReqID: "z", Tool: "Bash", Input: "x"}})
	if p := l.PendingApprovals(); len(p) != 0 {
		t.Errorf("displaced monitor's approval surfaced: %+v", p)
	}
	if c := stale.ApproveCalls(); len(c) != 1 || c[0].D.Allow {
		t.Errorf("displaced monitor Approve calls = %+v, want one deny", c)
	}
}

// TestApp_PendingApprovals_ArrivalOrder is the APP-22a regression guard.
func TestApp_PendingApprovals_ArrivalOrder(t *testing.T) {
	l := newLifecycleApp(t, nil)
	openLifecycle(t, l, "ws-o")
	fm := l.lastMonitor()
	ids := []string{"r9", "r1", "r5", "r3", "r7"}
	for _, id := range ids {
		fm.Replay(agent.Event{Kind: "approval", State: agent.StateAwaitingApproval, Approval: &agent.ApprovalReq{ReqID: id, Tool: "Bash", Input: id}})
	}
	waitFor(t, "five pending", func() bool { return len(l.PendingApprovals()) == len(ids) })
	for i := 0; i < 20; i++ {
		got := l.PendingApprovals()
		for j, p := range got {
			if p.Req.ReqID != ids[j]+":ws-o" {
				t.Fatalf("PendingApprovals order = %v, want arrival order %v", got, ids)
			}
		}
	}
}

// TestApp_EnvUnset_AppliedToPaneAndDrawer covers wiring item 5: variables a
// `perch reload` reports as unset are dropped from both the agent pane and
// the drawer environment, never touching PERCH_* keys.
func TestApp_EnvUnset_AppliedToPaneAndDrawer(t *testing.T) {
	t.Setenv("PERCH_UNSET_PROBE", "base")
	t.Setenv("ZZ_UNSET_ME", "base")
	wt := t.TempDir()
	l := newLifecycleApp(t, []string{wt})
	envs := map[string][]string{}
	l.spawnPty = func(_ context.Context, _ string, _ []string, env []string, dataEvent, _ string,
		_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
		l.mu.Lock()
		envs[dataEvent] = env
		l.mu.Unlock()
		return internalpty.NewBridgeForTest(func() error { return nil }), nil
	}
	_ = l.store.Upsert(registry.Workspace{ID: "ws-u", WorktreePath: wt, Agent: "claude"})
	l.onEnvSync("ws-u", envsync.Delta{Set: []string{"NEWVAR=1"}, Unset: []string{"ZZ_UNSET_ME", "PERCH_UNSET_PROBE"}})
	if err := l.OpenWorkspace("ws-u"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.CloseWorkspace("ws-u") }()
	if err := l.OpenShell("shell-ws-u", wt); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"pty:data:pane-ws-u", "pty:data:shell-ws-u"} {
		l.mu.Lock()
		env := envs[ev]
		l.mu.Unlock()
		if _, ok := envSliceGet(env, "ZZ_UNSET_ME"); ok {
			t.Errorf("%s: ZZ_UNSET_ME still present after a reload reported it unset", ev)
		}
		if _, ok := envSliceGet(env, "PERCH_UNSET_PROBE"); !ok {
			t.Errorf("%s: a PERCH_* key was dropped", ev)
		}
		if v, _ := envSliceGet(env, "NEWVAR"); v != "1" {
			t.Errorf("%s: overlay NEWVAR missing", ev)
		}
	}
}

// TestApp_OnEnvSync_ClosedSessionNotReopened is the APP-14(b) regression
// guard: a reload for a closed session stores the overlay but never reopens
// the session in the background.
func TestApp_OnEnvSync_ClosedSessionNotReopened(t *testing.T) {
	wt := t.TempDir()
	l := newLifecycleApp(t, []string{wt})
	var spawns atomic.Int32
	l.spawnPty = func(_ context.Context, _ string, _ []string, _ []string, _, _ string,
		_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
		spawns.Add(1)
		return internalpty.NewBridgeForTest(func() error { return nil }), nil
	}
	_ = l.store.Upsert(registry.Workspace{ID: "ws-c", WorktreePath: wt, Agent: "claude"})
	l.onEnvSync("ws-c", envsync.Delta{Set: []string{"A=1"}})
	time.Sleep(50 * time.Millisecond)
	if n := spawns.Load(); n != 0 {
		t.Errorf("a reload for a closed session spawned %d ptys, want 0", n)
	}
	for _, r := range l.snapshot() {
		if r.event == evtWorkspaceRelaunch {
			t.Error("workspace:relaunch emitted for a closed session")
		}
	}
	if ov := l.overlayFor("ws-c"); len(ov) != 1 {
		t.Errorf("overlay not stored: %v", ov)
	}
}

// TestApp_OnEnvSync_RelaunchFailureNotifies is the APP-14(a) regression
// guard: a failed relaunch is reported, not swallowed.
func TestApp_OnEnvSync_RelaunchFailureNotifies(t *testing.T) {
	l := newLifecycleApp(t, nil)
	openLifecycle(t, l, "ws-f")
	l.newMonitor = func(string, agent.Adapter) (agent.Monitor, error) {
		return nil, os.ErrPermission
	}
	l.onEnvSync("ws-f", envsync.Delta{Set: []string{"A=1"}})
	waitFor(t, "relaunch failure notice", func() bool {
		_, ok := findNotify(l.snapshot(), "blocking", "Reload failed")
		return ok
	})
}

// TestApp_Remove_RevokesEnvsyncAndDropsOverlay covers wiring item 5 and
// APP-14(b): removing a session revokes its env-sync token and forgets its
// overlay and unset list; closing revokes the token but keeps the overlay.
func TestApp_Remove_RevokesEnvsyncAndDropsOverlay(t *testing.T) {
	ls, err := envsync.NewWithDelta(nil, func(string, envsync.Delta) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ls.Close() }()
	l := newLifecycleApp(t, nil)
	l.envsync = ls
	openLifecycle(t, l, "ws-v")
	tok1, _ := ls.TokenFor("ws-v")
	l.onEnvSync("ws-v", envsync.Delta{Set: []string{"A=1"}, Unset: []string{"B"}})
	waitFor(t, "relaunch", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.mons) >= 2
	})

	if err := l.CloseWorkspace("ws-v"); err != nil {
		t.Fatal(err)
	}
	tok2, _ := ls.TokenFor("ws-v")
	if tok2 == tok1 {
		t.Error("CloseWorkspace kept the env-sync token alive")
	}
	if ov, un := l.envFor("ws-v"); len(ov) != 1 || len(un) != 1 {
		t.Errorf("CloseWorkspace dropped the overlay (%v / %v); it must survive for the next open", ov, un)
	}
	if err := l.RemoveWorkspace("ws-v"); err != nil {
		t.Fatal(err)
	}
	if tok3, _ := ls.TokenFor("ws-v"); tok3 == tok2 {
		t.Error("RemoveWorkspace kept the env-sync token alive")
	}
	if ov, un := l.envFor("ws-v"); ov != nil || un != nil {
		t.Errorf("RemoveWorkspace kept env state: %v / %v", ov, un)
	}
}

// TestApp_ApproveAlways_SaveErrorReturned is the APP-17 regression guard.
func TestApp_ApproveAlways_SaveErrorReturned(t *testing.T) {
	l := newLifecycleApp(t, nil)
	openLifecycle(t, l, "ws-s")
	// /proc/self accepts no new files, even for root, while reading a missing
	// file there is a plain ENOENT, so GetSettings succeeds and the write
	// fails.
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("needs /proc")
	}
	l.settingsPath = "/proc/self/perch-settings-test.json"
	l.mu.Lock()
	l.pending["q:ws-s"] = agent.ApprovalReq{ReqID: "q:ws-s", Tool: "Bash", Input: "ls", InputHash: "h"}
	l.mu.Unlock()
	err := l.Approve("q:ws-s", "always")
	if err == nil || !strings.Contains(err.Error(), "could not save") {
		t.Errorf("Approve(always) with an unwritable settings file = %v, want a save error", err)
	}
	if c := l.lastMonitor().ApproveCalls(); len(c) != 1 || !c[0].D.Allow {
		t.Errorf("the request itself must still be allowed; calls = %+v", c)
	}
}
