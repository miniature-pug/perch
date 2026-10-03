package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gitpkg "github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
	"github.com/miniature-pug/perch/internal/registry"
)

// fsChangedPayloads returns the payloads of every captured fs:changed event.
func fsChangedPayloads(recs []emitRec) []map[string]any {
	var out []map[string]any
	for _, r := range recs {
		if r.event == "fs:changed" && len(r.data) > 0 {
			if m, ok := r.data[0].(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// openWatched opens a workspace whose watcher seam captures onChange.
func openWatched(t *testing.T) (*lifecycleApp, func(string), string) {
	t.Helper()
	wt := t.TempDir()
	l := newLifecycleApp(t, []string{wt})
	l.debounce = 20 * time.Millisecond
	var onChange func(string)
	l.newWatcher = makeWatcherSeam(t, &onChange)
	_ = l.store.Upsert(registry.Workspace{ID: "ws-fs", WorktreePath: wt, Agent: "claude"})
	if err := l.OpenWorkspace("ws-fs"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.CloseWorkspace("ws-fs") })
	return l, onChange, wt
}

// TestApp_FsChanged_CarriesPaths covers wiring item 3: one event per window
// listing each changed path once.
func TestApp_FsChanged_CarriesPaths(t *testing.T) {
	l, onChange, wt := openWatched(t)
	a, b := filepath.Join(wt, "a.go"), filepath.Join(wt, "b.go")
	onChange(b)
	onChange(a)
	onChange(b)
	waitFor(t, "fs:changed", func() bool { return len(fsChangedPayloads(l.snapshot())) == 1 })
	time.Sleep(50 * time.Millisecond)
	ps := fsChangedPayloads(l.snapshot())
	if len(ps) != 1 {
		t.Fatalf("got %d fs:changed events, want 1", len(ps))
	}
	paths, _ := ps[0]["paths"].([]string)
	if strings.Join(paths, ",") != a+","+b || ps[0]["truncated"] != false || ps[0]["path"] != wt {
		t.Errorf("payload = %v, want paths [%s %s], truncated false, path %s", ps[0], a, b, wt)
	}
}

// TestApp_FsChanged_TruncatesPastCap: more than fsChangedMaxPaths changes in
// one window produce one event with no paths and truncated=true.
func TestApp_FsChanged_TruncatesPastCap(t *testing.T) {
	l, onChange, wt := openWatched(t)
	for i := 0; i <= fsChangedMaxPaths; i++ {
		onChange(filepath.Join(wt, fmt.Sprintf("f%04d", i)))
	}
	waitFor(t, "fs:changed", func() bool { return len(fsChangedPayloads(l.snapshot())) == 1 })
	time.Sleep(50 * time.Millisecond)
	ps := fsChangedPayloads(l.snapshot())
	if len(ps) != 1 {
		t.Fatalf("got %d fs:changed events, want 1", len(ps))
	}
	paths, ok := ps[0]["paths"].([]string)
	if !ok || paths == nil || len(paths) != 0 || ps[0]["truncated"] != true {
		t.Errorf("payload = %v, want paths [] (non-nil) and truncated true", ps[0])
	}
	// The next window starts fresh.
	onChange(filepath.Join(wt, "next"))
	waitFor(t, "second fs:changed", func() bool { return len(fsChangedPayloads(l.snapshot())) == 2 })
	if p2 := fsChangedPayloads(l.snapshot())[1]; p2["truncated"] != false {
		t.Errorf("second window payload = %v, want truncated false", p2)
	}
}

// TestApp_DiscardHunk_StaleIDRefused covers wiring item 1: a discard of a
// hunk that changed since it was displayed is refused with ErrHunkChanged
// and touches nothing.
func TestApp_DiscardHunk_StaleIDRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := initGitRepo(t, root)
	p := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "f")
	if err := os.WriteFile(p, []byte("one\nTWO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := newLifecycleApp(t, []string{root})
	hs, err := l.Hunks(repo, "f.txt")
	if err != nil || len(hs) != 1 || hs[0].ID == "" {
		t.Fatalf("Hunks = %+v, %v", hs, err)
	}
	// The agent edits the same lines after the user saw the hunk.
	if err := os.WriteFile(p, []byte("one\nAGENT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = l.DiscardHunk(repo, "f.txt", hs[0].Index, hs[0].ID)
	if !errors.Is(err, gitpkg.ErrHunkChanged) {
		t.Fatalf("DiscardHunk with a stale id = %v, want ErrHunkChanged", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "one\nAGENT\n" {
		t.Errorf("file = %q, the agent's edit was discarded", got)
	}
	if err := l.StageHunk(repo, "f.txt", 0, ""); err == nil {
		t.Error("StageHunk with an empty id = nil, want error")
	}
}

// TestApp_CreateWorkspace_PrunesDeletedWorktree covers wiring item 7 (GFS-27,
// APP-7): a worktree directory deleted outside perch must not block a new
// session on the same path (new-branch mode) or the same branch
// (existing-branch mode).
func TestApp_CreateWorkspace_PrunesDeletedWorktree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	repo := makeTestRepo(t, root)
	wtRoot := filepath.Join(root, "proj__worktrees")
	l := newLifecycleApp(t, []string{root})

	// Existing-branch mode: branch "feat" was checked out in a worktree that
	// was then rm -rf'ed.
	runGit(t, repo, "branch", "feat")
	runGit(t, repo, "worktree", "add", filepath.Join(wtRoot, "elsewhere"), "feat")
	if err := os.RemoveAll(filepath.Join(wtRoot, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CreateWorkspace("claude", repo, "", "feat", "", true); err != nil {
		t.Fatalf("existing-branch CreateWorkspace after rm -rf: %v", err)
	}

	// New-branch mode: the slug path of "newb" was a worktree that was
	// rm -rf'ed.
	runGit(t, repo, "worktree", "add", "-b", "tmp", filepath.Join(wtRoot, "newb"), "main")
	if err := os.RemoveAll(filepath.Join(wtRoot, "newb")); err != nil {
		t.Fatal(err)
	}
	vm, err := l.CreateWorkspace("claude", repo, "main", "newb", "", true)
	if err != nil {
		t.Fatalf("new-branch CreateWorkspace on a pruned path: %v", err)
	}
	if vm.WorktreePath != filepath.Join(wtRoot, "newb") {
		t.Errorf("WorktreePath = %q, want the freed slug path", vm.WorktreePath)
	}
}

// TestApp_CreateWorkspace_SlugCollisionPicksFreePath covers wiring item 5
// (GFS-16): "feat/x" and "feat-x" share a slug; the second session gets the
// next free directory instead of failing.
func TestApp_CreateWorkspace_SlugCollisionPicksFreePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	repo := makeTestRepo(t, root)
	l := newLifecycleApp(t, []string{root})
	first, err := l.CreateWorkspace("claude", repo, "main", "feat/x", "", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.CreateWorkspace("claude", repo, "main", "feat-x", "", true)
	if err != nil {
		t.Fatalf("second CreateWorkspace with a colliding slug: %v", err)
	}
	if first.WorktreePath == second.WorktreePath || !strings.HasSuffix(second.WorktreePath, "feat-x-2") {
		t.Errorf("paths = %q and %q, want the second to be feat-x-2", first.WorktreePath, second.WorktreePath)
	}
}

// concurrencyRunner answers ChangedFiles and BranchMerged for any tree after
// a short delay and records the peak number of concurrent calls.
type concurrencyRunner struct {
	mu       sync.Mutex
	inFlight int
	peak     int
}

func (c *concurrencyRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.peak {
		c.peak = c.inFlight
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()
	select {
	case <-time.After(30 * time.Millisecond):
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	if len(args) > 2 && args[2] == "branch" {
		return []byte("main\n"), nil, nil
	}
	return nil, nil, nil
}

func (c *concurrencyRunner) RunInDir(ctx context.Context, _, name string, args ...string) ([]byte, []byte, error) {
	return c.Run(ctx, name, args...)
}

func (c *concurrencyRunner) RunStdin(ctx context.Context, _ string, _ []byte, name string, args ...string) ([]byte, []byte, error) {
	return c.Run(ctx, name, args...)
}

var _ proc.Runner = (*concurrencyRunner)(nil)

// TestApp_ListStaleSessions_ConcurrentBoundedAndSkipsOpen is the APP-19
// regression guard: rows are inspected concurrently, never more than
// staleWorkers at once, keep the registry order, and an open session is not
// listed as stale.
func TestApp_ListStaleSessions_ConcurrentBoundedAndSkipsOpen(t *testing.T) {
	l := newLifecycleApp(t, nil)
	cr := &concurrencyRunner{}
	l.run = cr
	old := time.Now().Add(-90 * 24 * time.Hour)
	var want []string
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("ws-%02d", i)
		tree := linkedWorktreeDir(t)
		l.roots = append(l.roots, tree)
		_ = l.store.Upsert(registry.Workspace{ID: id, RepoPath: tree, WorktreePath: tree, Worktree: true,
			Agent: "claude", Branch: "b" + id, BaseRef: "main", LastActive: old})
		want = append(want, id)
	}
	// ws-00 is open: in use, so not stale whatever its LastActive says.
	// Opening bumps LastActive, so put it back.
	if err := l.OpenWorkspace("ws-00"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.CloseWorkspace("ws-00") }()
	_, _ = l.store.Update("ws-00", func(w *registry.Workspace) error { w.LastActive = old; return nil })

	rows, err := l.ListStaleSessions()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.ID)
	}
	if strings.Join(got, ",") != strings.Join(want[1:], ",") {
		t.Errorf("rows = %v, want %v (registry order, open ws-00 excluded)", got, want[1:])
	}
	if cr.peak < 2 || cr.peak > staleWorkers {
		t.Errorf("peak concurrent git calls = %d, want 2..%d", cr.peak, staleWorkers)
	}
}
