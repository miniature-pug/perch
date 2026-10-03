package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/proc"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// hookRunner wraps a proc.Runner and calls before() ahead of every Run, so a
// test can observe App state at the moment a git command runs.
type hookRunner struct {
	proc.Runner
	before func(args []string)
}

func (h hookRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	if h.before != nil {
		h.before(args)
	}
	return h.Runner.Run(ctx, name, args...)
}

// lifecycleApp is a headless App with fake pty, monitor and adapter seams and
// an emit capture. spawn, when set, replaces the default fake spawnPty.
type lifecycleApp struct {
	*App
	recMu sync.Mutex // guards recs and mons; App.mu is l.mu
	recs  []emitRec
	mons  []*agent.FakeMonitor
}

func (l *lifecycleApp) snapshot() []emitRec {
	l.recMu.Lock()
	defer l.recMu.Unlock()
	return append([]emitRec(nil), l.recs...)
}

func (l *lifecycleApp) lastMonitor() *agent.FakeMonitor {
	l.recMu.Lock()
	defer l.recMu.Unlock()
	if len(l.mons) == 0 {
		return nil
	}
	return l.mons[len(l.mons)-1]
}

func newLifecycleApp(t *testing.T, roots []string) *lifecycleApp {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, err := registry.Load(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	l := &lifecycleApp{}
	l.App = &App{
		store:        store,
		roots:        roots,
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		pending:      map[string]agent.ApprovalReq{},
		cancels:      map[string]context.CancelFunc{},
		settingsPath: cfgDir + "/settings.json",
		emit: func(event string, data ...any) {
			l.recMu.Lock()
			l.recs = append(l.recs, emitRec{event, data})
			l.recMu.Unlock()
		},
		spawnPty: func(_ context.Context, _ string, _ []string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) {
			fm := agent.NewFakeMonitor(nil)
			l.recMu.Lock()
			l.mons = append(l.mons, fm)
			l.recMu.Unlock()
			return fm, nil
		},
		newAdapter: fakeAdapterSeam(&fakeAdapter{name: "claude", detect: true}),
	}
	return l
}

// TestApp_OpenRemoveSerialized is the APP-10 regression guard. A
// RemoveWorkspace that arrives while an OpenWorkspace is half done (for
// example the env-sync relaunch goroutine) must wait for it, and then tear
// the new session down. Before the per-workspace lock, Remove found nothing
// to close, Open then registered a pty and monitor for a removed workspace
// (leaked until shutdown), and Open's Upsert could recreate the record.
func TestApp_OpenRemoveSerialized(t *testing.T) {
	wt := t.TempDir()
	l := newLifecycleApp(t, []string{wt})
	_ = l.store.Upsert(registry.Workspace{ID: "ws-race", RepoPath: wt, WorktreePath: wt, Agent: "claude", Title: "t"})

	entered := make(chan struct{})
	release := make(chan struct{})
	l.spawnPty = func(_ context.Context, _ string, _ []string, _ []string, _, _ string,
		_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
		close(entered)
		<-release
		return internalpty.NewBridgeForTest(func() error { return nil }), nil
	}

	openErr := make(chan error, 1)
	go func() { openErr <- l.OpenWorkspace("ws-race") }()
	<-entered

	removeDone := make(chan error, 1)
	go func() { removeDone <- l.RemoveWorkspace("ws-race") }()
	select {
	case err := <-removeDone:
		t.Fatalf("RemoveWorkspace finished (%v) while OpenWorkspace was still running", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-openErr; err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if err := <-removeDone; err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}

	l.mu.Lock()
	nb, nm := len(l.bridges), len(l.monitors)
	l.mu.Unlock()
	if nb != 0 || nm != 0 {
		t.Errorf("after Open then Remove: %d bridges, %d monitors left, want 0/0 (leaked session)", nb, nm)
	}
	if _, ok := l.store.Get("ws-race"); ok {
		t.Error("record survived RemoveWorkspace")
	}
	if fm := l.lastMonitor(); fm == nil || !fm.TornDown() {
		t.Error("the opened monitor was never torn down")
	}
	l.opMu.Lock()
	n := len(l.opLocks)
	l.opMu.Unlock()
	if n != 0 {
		t.Errorf("opLocks holds %d entries after all operations finished, want 0", n)
	}
}

// TestApp_SetWorkspaceTitle_UnknownDoesNotResurrect guards the Update
// migration: renaming a removed workspace fails and never recreates it.
func TestApp_SetWorkspaceTitle_UnknownDoesNotResurrect(t *testing.T) {
	l := newLifecycleApp(t, []string{t.TempDir()})
	if err := l.SetWorkspaceTitle("ws-none", "x"); err == nil {
		t.Error("SetWorkspaceTitle on an unknown id = nil, want error")
	}
	if _, ok := l.store.Get("ws-none"); ok {
		t.Error("SetWorkspaceTitle created a record for an unknown id")
	}
}

// TestApp_SetWorkspaceTitle_KeepsConcurrentSessionID guards APP-10's lost
// update: the pump persisting LastSessionID and a rename must both land.
func TestApp_SetWorkspaceTitle_KeepsConcurrentSessionID(t *testing.T) {
	wt := t.TempDir()
	l := newLifecycleApp(t, []string{wt})
	_ = l.store.Upsert(registry.Workspace{ID: "ws-t", WorktreePath: wt, Agent: "claude", Title: "old"})
	if err := l.OpenWorkspace("ws-t"); err != nil {
		t.Fatal(err)
	}
	fm := l.lastMonitor()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		fm.Replay(agent.Event{Kind: "state", State: agent.StateIdle, SessionID: "sess-1"})
	}()
	if err := l.SetWorkspaceTitle("ws-t", "new"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w, _ := l.store.Get("ws-t"); w.LastSessionID == "sess-1" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	w, _ := l.store.Get("ws-t")
	if w.Title != "new" || w.LastSessionID != "sess-1" {
		t.Errorf("record = {Title:%q LastSessionID:%q}, want {new sess-1}", w.Title, w.LastSessionID)
	}
	_ = l.CloseWorkspace("ws-t")
}

// TestApp_RemoveWorkspace_StopsSessionBeforeDeletingTree is the APP-13
// regression guard: the agent, the shells and the watcher must be stopped
// before `git worktree remove` runs, for both Remove and ForceRemove.
func TestApp_RemoveWorkspace_StopsSessionBeforeDeletingTree(t *testing.T) {
	for _, force := range []bool{false, true} {
		repo := t.TempDir()
		tree := linkedWorktreeDir(t)
		l := newLifecycleApp(t, []string{repo, tree})
		_ = l.store.Upsert(registry.Workspace{ID: "ws-rm", RepoPath: repo, WorktreePath: tree, Worktree: true, Agent: "claude", Branch: "feat"})
		fr := proc.NewFakeRunner()
		fr.Respond(proc.FakeResult{}, "git", "-C", tree, "status", "--porcelain")
		if force {
			fr.Respond(proc.FakeResult{}, "git", "-C", repo, "worktree", "remove", "--force", tree)
		} else {
			fr.Respond(proc.FakeResult{}, "git", "-C", repo, "worktree", "remove", tree)
		}
		var liveAtRemove bool
		l.run = hookRunner{Runner: fr, before: func(args []string) {
			if len(args) >= 4 && args[2] == "worktree" && args[3] == "remove" {
				l.mu.Lock()
				_, liveAtRemove = l.monitors["ws-rm"]
				l.mu.Unlock()
			}
		}}
		if err := l.OpenWorkspace("ws-rm"); err != nil {
			t.Fatal(err)
		}
		var err error
		if force {
			err = l.ForceRemoveWorkspace("ws-rm")
		} else {
			err = l.RemoveWorkspace("ws-rm")
		}
		if err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		if liveAtRemove {
			t.Errorf("force=%v: the session was still live when git worktree remove ran", force)
		}
		if fm := l.lastMonitor(); !fm.TornDown() {
			t.Errorf("force=%v: monitor not torn down", force)
		}
	}
}

// TestApp_OpenShellDuringRemove_DoesNotLeak is the review #5 regression
// guard: a drawer the frontend respawns while RemoveWorkspace runs (after
// the shells were closed, before git deleted the tree) must wait for the
// removal and then fail, not leave a shell behind.
func TestApp_OpenShellDuringRemove_DoesNotLeak(t *testing.T) {
	repo := t.TempDir()
	tree := linkedWorktreeDir(t)
	l := newLifecycleApp(t, []string{repo, tree})
	_ = l.store.Upsert(registry.Workspace{ID: "ws-rm", RepoPath: repo, WorktreePath: tree, Worktree: true, Agent: "claude", Branch: "feat"})
	fr := proc.NewFakeRunner()
	fr.Respond(proc.FakeResult{}, "git", "-C", tree, "status", "--porcelain")
	fr.Respond(proc.FakeResult{}, "git", "-C", repo, "worktree", "remove", tree)
	shellErr := make(chan error, 1)
	l.run = hookRunner{Runner: fr, before: func(args []string) {
		if len(args) >= 4 && args[2] == "worktree" && args[3] == "remove" {
			go func() { shellErr <- l.OpenShell("shell-ws-rm_1", tree) }()
			time.Sleep(20 * time.Millisecond) // let it reach the lock
		}
	}}
	if err := l.OpenWorkspace("ws-rm"); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveWorkspace("ws-rm"); err != nil {
		t.Fatal(err)
	}
	if err := <-shellErr; err == nil {
		t.Error("OpenShell racing RemoveWorkspace succeeded")
	}
	l.mu.Lock()
	_, leaked := l.bridges["shell-ws-rm_1"]
	l.mu.Unlock()
	if leaked {
		t.Error("a drawer shell outlived RemoveWorkspace")
	}
}
