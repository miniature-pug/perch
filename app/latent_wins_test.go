package app

import (
	"testing"

	"github.com/miniature-pug/perch/internal/agent"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// TestApp_ListWorkspaces_WillResumeAndBaseRef covers WIN #2 (WillResume) and
// WIN #3 (BaseRef): both are additive projections of already-persisted registry
// fields (Workspace.LastSessionID and Workspace.BaseRef) that ListWorkspaces
// previously dropped. A resume-capable record (LastSessionID set) must project
// WillResume=true; a record forked off a base ref must project that BaseRef; a
// fresh/old record (both empty) must project the zero values.
func TestApp_ListWorkspaces_WillResumeAndBaseRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	// Deterministic ordering: List() sorts by LastActive desc then ID asc. Both
	// records share the zero LastActive, so they order by ID ascending: the
	// "resume-*" id sorts before the "fresh-*" id.
	_ = store.Upsert(registry.Workspace{
		ID:            "resume-ws",
		WorktreePath:  t.TempDir(),
		Agent:         "claude",
		Title:         "resumable",
		Branch:        "feat/x",
		LastSessionID: "sess-abc123",
		BaseRef:       "main",
	})
	_ = store.Upsert(registry.Workspace{
		ID:           "zfresh-ws",
		WorktreePath: t.TempDir(),
		Agent:        "claude",
		Title:        "fresh",
		Branch:       "feat/y",
		// LastSessionID empty → WillResume false; BaseRef empty (old/in-repo record).
	})

	a := &App{
		store:    store,
		roots:    []string{t.TempDir()},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	vms := a.ListWorkspaces()
	if len(vms) != 2 {
		t.Fatalf("ListWorkspaces = %d items, want 2", len(vms))
	}
	byID := map[string]WorkspaceVM{}
	for _, vm := range vms {
		byID[vm.ID] = vm
	}

	cases := []struct {
		id             string
		wantWillResume bool
		wantBaseRef    string
	}{
		{"resume-ws", true, "main"},
		{"zfresh-ws", false, ""},
	}
	for _, tc := range cases {
		vm, ok := byID[tc.id]
		if !ok {
			t.Fatalf("workspace %q missing from ListWorkspaces", tc.id)
		}
		if vm.WillResume != tc.wantWillResume {
			t.Errorf("%s: WillResume = %v, want %v", tc.id, vm.WillResume, tc.wantWillResume)
		}
		if vm.BaseRef != tc.wantBaseRef {
			t.Errorf("%s: BaseRef = %q, want %q", tc.id, vm.BaseRef, tc.wantBaseRef)
		}
	}
}

// TestWindowTitle covers WIN #6's pure title function: "perch" when nothing needs
// the user, "perch (N need you)" otherwise. A non-positive count is defensive and
// must also yield the bare "perch".
func TestWindowTitle(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{-1, "perch"},
		{0, "perch"},
		{1, "perch (1 need you)"},
		{2, "perch (2 need you)"},
		{42, "perch (42 need you)"},
	}
	for _, tc := range cases {
		if got := windowTitle(tc.count); got != tc.want {
			t.Errorf("windowTitle(%d) = %q, want %q", tc.count, got, tc.want)
		}
	}
}

// TestCountNeedsAttention covers the pure need-count helper: only
// StateAwaitingApproval and StateAwaitingInput count toward the "need you" total;
// every other lifecycle state (running/idle/done/errored/exited) does not.
func TestCountNeedsAttention(t *testing.T) {
	cases := []struct {
		name   string
		states []agent.State
		want   int
	}{
		{"empty", nil, 0},
		{"none-need", []agent.State{agent.StateIdle, agent.StateRunning, agent.StateDone, agent.StateErrored, agent.StateExited}, 0},
		{"one-approval", []agent.State{agent.StateAwaitingApproval}, 1},
		{"one-input", []agent.State{agent.StateAwaitingInput}, 1},
		{"mixed", []agent.State{agent.StateAwaitingApproval, agent.StateRunning, agent.StateAwaitingInput, agent.StateIdle, agent.StateAwaitingApproval}, 3},
	}
	for _, tc := range cases {
		if got := countNeedsAttention(tc.states); got != tc.want {
			t.Errorf("%s: countNeedsAttention = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestApp_AttentionCount_FromLiveMonitors proves the monitor→count wiring: the
// count is read from the live monitors' CurrentState(), so only the workspaces
// whose monitor is in an attention state contribute. Registry records without a
// live monitor (State defaults to idle) never count.
func TestApp_AttentionCount_FromLiveMonitors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	_ = store.Upsert(registry.Workspace{ID: "ws-approval", WorktreePath: t.TempDir(), Agent: "claude", Title: "a"})
	_ = store.Upsert(registry.Workspace{ID: "ws-input", WorktreePath: t.TempDir(), Agent: "claude", Title: "b"})
	_ = store.Upsert(registry.Workspace{ID: "ws-running", WorktreePath: t.TempDir(), Agent: "claude", Title: "c"})
	_ = store.Upsert(registry.Workspace{ID: "ws-nomonitor", WorktreePath: t.TempDir(), Agent: "claude", Title: "d"})

	approval := agent.NewFakeMonitor(nil)
	approval.SetState(agent.StateAwaitingApproval)
	input := agent.NewFakeMonitor(nil)
	input.SetState(agent.StateAwaitingInput)
	running := agent.NewFakeMonitor(nil)
	running.SetState(agent.StateRunning)

	a := &App{
		store:   store,
		roots:   []string{t.TempDir()},
		emit:    func(string, ...any) {},
		bridges: map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{
			"ws-approval": approval,
			"ws-input":    input,
			"ws-running":  running,
			// ws-nomonitor intentionally absent → idle → does not count.
		},
	}

	if got := a.attentionCount(); got != 2 {
		t.Errorf("attentionCount = %d, want 2 (one awaiting-approval + one awaiting-input)", got)
	}
}
