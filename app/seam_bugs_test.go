// seam_bugs_test.go: tests pinning backend-contract behavior at the agent/app seam.
package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/notify"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/registry"
)

// ---------------------------------------------------------------------------
// ListWorkspaces must populate PaneID, LastActive, and Branch.
// ---------------------------------------------------------------------------

func TestListWorkspaces_PopulatesPaneIDLastActiveBranch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	wt := t.TempDir()
	ts := time.Now().Truncate(time.Millisecond)
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-seam-1",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "t",
		Branch:       "feat/hello",
		LastActive:   ts,
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
	vm := vms[0]

	// PaneID
	want := "pane-ws-seam-1"
	if vm.PaneID != want {
		t.Errorf("PaneID = %q, want %q", vm.PaneID, want)
	}

	// LastActive
	if vm.LastActive.IsZero() {
		t.Error("LastActive is zero, want non-zero")
	}
	if !vm.LastActive.Equal(ts) {
		t.Errorf("LastActive = %v, want %v", vm.LastActive, ts)
	}

	// Branch
	if vm.Branch != "feat/hello" {
		t.Errorf("Branch = %q, want feat/hello", vm.Branch)
	}
}

// ---------------------------------------------------------------------------
// Event forwarding must stamp WorkspaceID onto the event.
// We use Replay (not Prepare's sequence) so the event is NOT pre-stamped.
// ---------------------------------------------------------------------------

func TestOpenWorkspace_EventForwarding_StampsWorkspaceID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-stamp",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "t",
	})

	fm := agent.NewFakeMonitor(nil)

	var mu sync.Mutex
	var agentEvents []agent.Event
	emit := func(event string, data ...any) {
		if event == "agent:event" && len(data) == 1 {
			if ev, ok := data[0].(agent.Event); ok {
				mu.Lock()
				agentEvents = append(agentEvents, ev)
				mu.Unlock()
			}
		}
	}

	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     emit,
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
		newAdapter: fakeAdapterSeam(&fakeAdapter{name: "claude", detect: true}),
	}

	if err := a.OpenWorkspace("ws-stamp"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	// Replay a raw event with WorkspaceID deliberately empty (not pre-stamped).
	fm.Replay(agent.Event{Kind: "state", State: agent.StateRunning})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(agentEvents)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	evs := agentEvents
	mu.Unlock()

	if len(evs) == 0 {
		t.Fatal("no agent:event emitted after Replay")
	}
	if evs[0].WorkspaceID != "ws-stamp" {
		t.Errorf("forwarded event WorkspaceID = %q, want ws-stamp", evs[0].WorkspaceID)
	}
}

// ---------------------------------------------------------------------------
// Approval composition: ReqID must be <raw>:<workspaceID> so that Approve() can
// parse and route it. Also verifies dispatchNotify emits with correct workspaceId.
// ---------------------------------------------------------------------------

func TestOpenWorkspace_ApprovalReqIDComposition(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-appr",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "t",
	})

	fm := agent.NewFakeMonitor(nil)

	var mu sync.Mutex
	var agentEvents []agent.Event
	var notifyEvents []map[string]any
	var osNotifyCalls [][]string // records OS desktop notifications fired
	osNotify := func(_ string, args ...string) error {
		mu.Lock()
		osNotifyCalls = append(osNotifyCalls, args)
		mu.Unlock()
		return nil
	}
	emit := func(event string, data ...any) {
		if len(data) != 1 {
			return
		}
		switch event {
		case "agent:event":
			if ev, ok := data[0].(agent.Event); ok {
				mu.Lock()
				agentEvents = append(agentEvents, ev)
				mu.Unlock()
			}
		case "notify":
			if m, ok := data[0].(map[string]any); ok {
				mu.Lock()
				notifyEvents = append(notifyEvents, m)
				mu.Unlock()
			}
		}
	}

	a := &App{
		store:        store,
		roots:        []string{wt},
		emit:         emit,
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		cancels:      map[string]context.CancelFunc{},
		settingsPath: cfgDir + "/settings.json",
		// focused defaults to false here (unfocused), so a blocking-tier event
		// must fire the OS notification through this injected runner.
		notifier: notify.NewWithRunner(osNotify),
		spawnPty: func(_ context.Context, _ string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) {
			return fm, nil
		},
		newAdapter: fakeAdapterSeam(&fakeAdapter{name: "claude", detect: true}),
	}

	if err := a.OpenWorkspace("ws-appr"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	// Replay a raw approval event with WorkspaceID empty, ReqID bare. The Kind is
	// "approval", matching what ClaudeMonitor/OpencodeMonitor actually emit. A prior
	// version injected Kind:"state", masking the dispatchNotify approval case which
	// keyed on the wrong Kind and never fired.
	fm.Replay(agent.Event{
		Kind:  "approval",
		State: agent.StateAwaitingApproval,
		Approval: &agent.ApprovalReq{
			ReqID:   "raw123",
			Tool:    "Bash",
			Summary: "rm -rf /",
		},
	})

	// Wait for the OS notification — the LAST step dispatchNotify performs for a
	// blocking event — so observing it under mu establishes happens-before for
	// the agent:event and notify writes that precede it. If the fix regresses,
	// this times out and the OS-notify assertion below fails with precise blame.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		fired := len(osNotifyCalls) > 0
		mu.Unlock()
		if fired {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	evs := agentEvents
	notEvs := notifyEvents
	osCalls := osNotifyCalls
	mu.Unlock()

	if len(evs) == 0 {
		t.Fatal("no agent:event emitted after Replay")
	}

	// approval ReqID must be composed as <raw>:<workspaceID>
	ev := evs[0]
	if ev.Approval == nil {
		t.Fatal("forwarded event Approval is nil")
	}
	wantReqID := "raw123:ws-appr"
	if ev.Approval.ReqID != wantReqID {
		t.Errorf("forwarded Approval.ReqID = %q, want %q", ev.Approval.ReqID, wantReqID)
	}

	// WorkspaceID stamp verified here too.
	if ev.WorkspaceID != "ws-appr" {
		t.Errorf("forwarded event WorkspaceID = %q, want ws-appr", ev.WorkspaceID)
	}

	// Approve must route using the composed reqID.
	if err := a.Approve("raw123:ws-appr", "allow"); err != nil {
		t.Fatalf("Approve with composed reqID: %v", err)
	}
	calls := fm.ApproveCalls()
	if len(calls) == 0 {
		t.Fatal("Approve did not route to monitor")
	}
	if calls[0].ReqID != "raw123" {
		t.Errorf("monitor received ReqID = %q, want raw123", calls[0].ReqID)
	}

	// dispatchNotify must emit notify with correct workspaceId (not "").
	if len(notEvs) == 0 {
		t.Fatal("no notify event emitted after approval event")
	}
	if wid := notEvs[0]["workspaceId"]; wid != "ws-appr" {
		t.Errorf("notify workspaceId = %v, want ws-appr", wid)
	}
	// an approval event must dispatch at the BLOCKING tier, not fall through
	// dispatchNotify's default.
	if tier := notEvs[0]["tier"]; tier != "blocking" {
		t.Errorf("notify tier = %v, want blocking", tier)
	}
	// and, while unfocused, must fire an OS desktop notification. Pre-fix the
	// approval case keyed on Kind:"state" while monitors emit Kind:"approval", so
	// this never fired.
	if len(osCalls) == 0 {
		t.Error("approval event fired no OS desktop notification")
	} else if osCalls[0][0] != "Approval needed" {
		t.Errorf("OS notification title = %q, want \"Approval needed\"", osCalls[0][0])
	}
}

// PendingApprovals must expose every still-undecided approval, tagged with the
// workspace it belongs to (derived from the composed pending-map key), so the
// frontend can rebuild its queue after a reload or a late open (the agent:event
// carrying an approval is a one-shot). An always-empty result must be [] not nil.
func TestApp_PendingApprovals(t *testing.T) {
	a := &App{pending: map[string]agent.ApprovalReq{}}

	// Empty: a fresh app has no pending approvals; must be non-nil.
	got := a.PendingApprovals()
	if got == nil {
		t.Fatal("PendingApprovals returned nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("PendingApprovals on empty = %d entries, want 0", len(got))
	}

	// Seed the pending map exactly as the event pump does: key + stored ApprovalReq
	// both carry the composed "<raw>:<workspaceID>" ReqID.
	a.pending["raw1:ws-a"] = agent.ApprovalReq{ReqID: "raw1:ws-a", Tool: "Bash", Summary: "ls"}
	a.pending["raw2:ws-b"] = agent.ApprovalReq{ReqID: "raw2:ws-b", Tool: "Edit", Summary: "x"}

	got = a.PendingApprovals()
	if len(got) != 2 {
		t.Fatalf("PendingApprovals = %d entries, want 2", len(got))
	}
	byWs := map[string]PendingApprovalVM{}
	for _, p := range got {
		byWs[p.WorkspaceID] = p
	}
	if p, ok := byWs["ws-a"]; !ok {
		t.Error("missing ws-a entry")
	} else if p.Req.ReqID != "raw1:ws-a" || p.Req.Tool != "Bash" {
		t.Errorf("ws-a Req = %+v, want composed ReqID raw1:ws-a tool Bash", p.Req)
	}
	if p, ok := byWs["ws-b"]; !ok {
		t.Error("missing ws-b entry")
	} else if p.Req.ReqID != "raw2:ws-b" || p.Req.Tool != "Edit" {
		t.Errorf("ws-b Req = %+v, want composed ReqID raw2:ws-b tool Edit", p.Req)
	}
}

// ---------------------------------------------------------------------------
// Registry: Branch field must round-trip through save/load.
// ---------------------------------------------------------------------------

func TestRegistry_WorkspaceBranchRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	w := registry.Workspace{
		ID:           "ws-branch",
		WorktreePath: "/tmp/x",
		Agent:        "claude",
		Title:        "t",
		Branch:       "feat/my-feature",
		LastActive:   time.Now().Truncate(time.Second),
	}
	if err := s.Upsert(w); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Reload from disk to verify persistence.
	s2, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load after Upsert: %v", err)
	}
	got, ok := s2.Get("ws-branch")
	if !ok {
		t.Fatal("workspace not found after reload")
	}
	if got.Branch != "feat/my-feature" {
		t.Errorf("Branch after reload = %q, want feat/my-feature", got.Branch)
	}
}
