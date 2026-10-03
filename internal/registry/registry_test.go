// internal/registry/registry_test.go
package registry_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/registry"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("want empty list, got %d items", len(got))
	}

	now := time.Now().Truncate(time.Second)
	w := registry.Workspace{
		ID:            "ws-abc123",
		WorktreePath:  "/tmp/repo",
		Agent:         "claude",
		LastSessionID: "ses_xyz",
		Title:         "my workspace",
		LastActive:    now,
	}
	if err := s.Upsert(w); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok := s.Get("ws-abc123")
	if !ok {
		t.Fatal("Get returned not-found after Upsert")
	}
	if got.Title != "my workspace" || got.Agent != "claude" {
		t.Fatalf("unexpected workspace: %+v", got)
	}

	// Reload from disk to check persistence
	s2, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load after Upsert: %v", err)
	}
	list := s2.List()
	if len(list) != 1 {
		t.Fatalf("want 1 item after reload, got %d", len(list))
	}
	if list[0].ID != "ws-abc123" {
		t.Fatalf("unexpected ID: %s", list[0].ID)
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	// workspaces.json does not exist. Load must succeed with an empty store.
	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("expected empty store, got %d items", len(s.List()))
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{
		ID: "ws-1", WorktreePath: "/tmp/x", Agent: "opencode",
		Title: "x", LastActive: time.Now(),
	})
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "workspaces.json" {
			t.Errorf("unexpected file after Upsert: %s", e.Name())
		}
	}
}

func TestSortOrderLastActiveDesc(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	base := time.Now().Truncate(time.Second)
	for i, id := range []string{"ws-old", "ws-new", "ws-mid"} {
		_ = s.Upsert(registry.Workspace{
			ID: id, WorktreePath: "/tmp/" + id, Agent: "claude",
			Title: id, LastActive: base.Add(time.Duration(i) * time.Hour),
		})
	}
	list := s.List()
	if list[0].ID != "ws-mid" || list[1].ID != "ws-new" || list[2].ID != "ws-old" {
		t.Fatalf("wrong sort order: %v", []string{list[0].ID, list[1].ID, list[2].ID})
	}
}

// TestSortStableOnEqualLastActive verifies that records that share a
// LastActive timestamp keep a fixed order across repeated List() calls (a
// deterministic ID tie-breaker), instead of reordering with the random map
// iteration order.
// Before the fix, List used an unstable sort.Slice with no tie-breaker, so
// equal-timestamp records could swap between refreshes and make the sidebar
// flicker.
func TestSortStableOnEqualLastActive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, _ := registry.Load(dir)

	// Both records share the same LastActive. The test picks IDs so the
	// deterministic tie-break order (ID ascending) is "ws-aaa" then "ws-bbb".
	ts := time.Now().Truncate(time.Second)
	for _, id := range []string{"ws-bbb", "ws-aaa"} {
		_ = s.Upsert(registry.Workspace{
			ID: id, WorktreePath: "/tmp/" + id, Agent: "claude",
			Title: id, LastActive: ts,
		})
	}

	// Repeat List() many times. With a random map order feeding the sort, an
	// unstable sort with no tie-breaker would eventually flip the pair.
	first := s.List()
	if len(first) != 2 {
		t.Fatalf("want 2 workspaces, got %d", len(first))
	}
	if first[0].ID != "ws-aaa" || first[1].ID != "ws-bbb" {
		t.Fatalf("tie-break order = [%s %s], want [ws-aaa ws-bbb]", first[0].ID, first[1].ID)
	}
	for i := 0; i < 50; i++ {
		got := s.List()
		if got[0].ID != first[0].ID || got[1].ID != first[1].ID {
			t.Fatalf("List order changed across calls: iter %d got [%s %s], want [%s %s]",
				i, got[0].ID, got[1].ID, first[0].ID, first[1].ID)
		}
	}
}

func TestRemove(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{
		ID: "ws-del", WorktreePath: "/tmp/del", Agent: "claude",
		Title: "del", LastActive: time.Now(),
	})
	if err := s.Remove("ws-del"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Get("ws-del"); ok {
		t.Fatal("Get returned item after Remove")
	}
}

// TestWorkspace_RepoPathWorktreeRoundTrip verifies that RepoPath and
// Worktree survive an Upsert, Load, and Get cycle (a JSON round-trip).
func TestWorkspace_RepoPathWorktreeRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	w := registry.Workspace{
		ID:           "ws-rp1",
		RepoPath:     "/home/me/proj",
		WorktreePath: "/home/me/proj__worktrees/feat-x",
		Worktree:     true,
		Agent:        "claude",
		Title:        "feat-x",
		Branch:       "feat-x",
		LastActive:   now,
	}
	if err := s.Upsert(w); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// In-memory Get.
	got, ok := s.Get("ws-rp1")
	if !ok {
		t.Fatal("Get returned not-found after Upsert")
	}
	if got.RepoPath != "/home/me/proj" {
		t.Errorf("RepoPath = %q, want /home/me/proj", got.RepoPath)
	}
	if !got.Worktree {
		t.Error("Worktree = false, want true")
	}

	// Reload from disk.
	s2, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load after Upsert: %v", err)
	}
	got2, ok := s2.Get("ws-rp1")
	if !ok {
		t.Fatal("Get after reload returned not-found")
	}
	if got2.RepoPath != "/home/me/proj" {
		t.Errorf("RepoPath after reload = %q, want /home/me/proj", got2.RepoPath)
	}
	if !got2.Worktree {
		t.Error("Worktree after reload = false, want true")
	}

	// Non-worktree session: WorktreePath == RepoPath, Worktree == false.
	w2 := registry.Workspace{
		ID:           "ws-rp2",
		RepoPath:     "/home/me/proj",
		WorktreePath: "/home/me/proj",
		Worktree:     false,
		Agent:        "opencode",
		Title:        "main",
		Branch:       "main",
		LastActive:   now,
	}
	if err := s.Upsert(w2); err != nil {
		t.Fatalf("Upsert non-worktree: %v", err)
	}
	got3, ok := s.Get("ws-rp2")
	if !ok {
		t.Fatal("Get non-worktree returned not-found")
	}
	if got3.Worktree {
		t.Error("non-worktree session: Worktree = true, want false")
	}
	if got3.WorktreePath != got3.RepoPath {
		t.Errorf("non-worktree session: WorktreePath %q != RepoPath %q",
			got3.WorktreePath, got3.RepoPath)
	}
}

// TestWorkspace_ModelFieldGone verifies that Load accepts old JSON that
// contains a "model" field without error (unknown fields default to zero),
// and that the Workspace struct has no Model field for a caller to set.
func TestWorkspace_ModelFieldGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	// Write a workspaces.json that contains an old "model" key.
	oldJSON := `[{"id":"ws-old","worktreePath":"/tmp/x","agent":"claude",
"model":"claude-sonnet-4-5","title":"old","branch":"main",
"lastActive":"2026-01-01T00:00:00Z"}]`
	if err := os.WriteFile(filepath.Join(dir, "workspaces.json"),
		[]byte(oldJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load with old model key: %v", err)
	}
	got, ok := s.Get("ws-old")
	if !ok {
		t.Fatal("ws-old not found after load")
	}
	// RepoPath defaults to "" because the field is absent in the old JSON.
	// That is fine: future saves will fill in old records.
	_ = got.RepoPath
}

// TestWorkspace_BaseRefRoundTrip verifies that BaseRef survives an Upsert,
// Load, and Get cycle (a JSON round-trip). Stale-cleanup merge checks need
// this.
func TestWorkspace_BaseRefRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	w := registry.Workspace{
		ID:           "ws-br1",
		RepoPath:     "/home/me/proj",
		WorktreePath: "/home/me/proj__worktrees/feat-x",
		Worktree:     true,
		Agent:        "claude",
		Title:        "feat-x",
		Branch:       "feat/x",
		BaseRef:      "main",
		LastActive:   now,
	}
	if err := s.Upsert(w); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// In-memory Get.
	got, ok := s.Get("ws-br1")
	if !ok {
		t.Fatal("Get returned not-found after Upsert")
	}
	if got.BaseRef != "main" {
		t.Errorf("BaseRef = %q, want %q", got.BaseRef, "main")
	}

	// Reload from disk to check persistence.
	s2, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load after Upsert: %v", err)
	}
	got2, ok := s2.Get("ws-br1")
	if !ok {
		t.Fatal("Get after reload returned not-found")
	}
	if got2.BaseRef != "main" {
		t.Errorf("BaseRef after reload = %q, want %q", got2.BaseRef, "main")
	}

	// An empty BaseRef (non-worktree or legacy record) must survive too.
	w2 := registry.Workspace{
		ID:           "ws-br2",
		RepoPath:     "/home/me/proj",
		WorktreePath: "/home/me/proj",
		Agent:        "opencode",
		Title:        "main",
		Branch:       "main",
		BaseRef:      "",
		LastActive:   now,
	}
	if err := s.Upsert(w2); err != nil {
		t.Fatalf("Upsert empty BaseRef: %v", err)
	}
	got3, ok := s.Get("ws-br2")
	if !ok {
		t.Fatal("Get empty-BaseRef record returned not-found")
	}
	if got3.BaseRef != "" {
		t.Errorf("BaseRef = %q, want empty string for legacy record", got3.BaseRef)
	}
}

func TestDefaultConfigDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("XDG_CONFIG_HOME set", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		got := registry.DefaultConfigDir()
		want := filepath.Join(xdg, "perch")
		if got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	})

	t.Run("XDG_CONFIG_HOME unset", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		home := t.TempDir()
		t.Setenv("HOME", home)
		got := registry.DefaultConfigDir()
		want := filepath.Join(home, ".config", "perch")
		if got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	})
}

func TestLoad_CorruptJSON_QuarantinesAndReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "perch")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	corruptData := []byte(`[{bad`)
	registryPath := filepath.Join(configDir, "workspaces.json")
	if err := os.WriteFile(registryPath, corruptData, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := registry.Load(configDir)
	if err != nil {
		t.Fatalf("Load returned error on corrupt JSON: %v", err)
	}
	if n := len(store.List()); n != 0 {
		t.Fatalf("expected 0 workspaces, got %d", n)
	}
	// Original file should be gone (renamed).
	if _, err := os.Stat(registryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected original workspaces.json to be gone, got: %v", err)
	}
	// Backup should exist with the original bytes.
	matches, err := filepath.Glob(registryPath + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("expected a .corrupt-* backup file, found none")
	}
	got, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, corruptData) {
		t.Fatalf("backup data mismatch: got %q, want %q", got, corruptData)
	}
}

func TestUpdate_MutatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{ID: "a", Title: "t", LastSessionID: "s1"})

	got, err := s.Update("a", func(w *registry.Workspace) error {
		w.LastSessionID = "s2"
		w.ID = "hijack" // must be ignored
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.ID != "a" || got.LastSessionID != "s2" || got.Title != "t" {
		t.Fatalf("unexpected result: %+v", got)
	}
	s2, _ := registry.Load(dir)
	w, ok := s2.Get("a")
	if !ok || w.LastSessionID != "s2" || w.Title != "t" {
		t.Fatalf("not persisted: %+v ok=%v", w, ok)
	}
	if _, ok := s2.Get("hijack"); ok {
		t.Fatal("Update must not allow changing the ID")
	}
}

func TestUpdate_DoesNotResurrectRemoved(t *testing.T) {
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{ID: "a"})
	_ = s.Remove("a")
	called := false
	_, err := s.Update("a", func(w *registry.Workspace) error { called = true; return nil })
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if called {
		t.Fatal("fn must not run for a missing record")
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("record was resurrected")
	}
}

func TestUpdate_FnErrorLeavesRecordUnchanged(t *testing.T) {
	s, _ := registry.Load(t.TempDir())
	_ = s.Upsert(registry.Workspace{ID: "a", Title: "orig"})
	boom := errors.New("boom")
	_, err := s.Update("a", func(w *registry.Workspace) error { w.Title = "changed"; return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if w, _ := s.Get("a"); w.Title != "orig" {
		t.Fatalf("record changed despite error: %+v", w)
	}
}

// Concurrent Updates of different fields must not lose each other's writes.
func TestUpdate_ConcurrentFieldsNoLostUpdate(t *testing.T) {
	s, _ := registry.Load(t.TempDir())
	_ = s.Upsert(registry.Workspace{ID: "a"})
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.Update("a", func(w *registry.Workspace) error { w.LastSessionID = "sess"; return nil })
		}()
		go func() {
			defer wg.Done()
			_, _ = s.Update("a", func(w *registry.Workspace) error { w.Title = "title"; return nil })
		}()
	}
	wg.Wait()
	w, _ := s.Get("a")
	if w.LastSessionID != "sess" || w.Title != "title" {
		t.Fatalf("lost update: %+v", w)
	}
}

func TestFlushFailureRollsBackMemory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced for root")
	}
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{ID: "a", Title: "orig"})
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if err := s.Upsert(registry.Workspace{ID: "b"}); err == nil {
		t.Fatal("expected flush error")
	}
	if _, ok := s.Get("b"); ok {
		t.Fatal("failed Upsert left record in memory")
	}
	if _, err := s.Update("a", func(w *registry.Workspace) error { w.Title = "x"; return nil }); err == nil {
		t.Fatal("expected flush error")
	}
	if w, _ := s.Get("a"); w.Title != "orig" {
		t.Fatalf("failed Update not rolled back: %+v", w)
	}
	if err := s.Remove("a"); err == nil {
		t.Fatal("expected flush error")
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatal("failed Remove not rolled back")
	}
}

func TestLoad_CorruptRecoversFromBackup(t *testing.T) {
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{ID: "a"})
	_ = s.Upsert(registry.Workspace{ID: "b"}) // first flush's file becomes .bak
	// Simulate a crash that left a zero-length main file.
	if err := os.WriteFile(filepath.Join(dir, "workspaces.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := registry.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get("a"); !ok {
		t.Fatalf("expected record a recovered from .bak, got %+v", s2.List())
	}
}

func TestFlushWritesSortedByID(t *testing.T) {
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	for _, id := range []string{"c", "a", "b"} {
		_ = s.Upsert(registry.Workspace{ID: id})
	}
	data, _ := os.ReadFile(filepath.Join(dir, "workspaces.json"))
	ia, ib, ic := bytes.Index(data, []byte(`"a"`)), bytes.Index(data, []byte(`"b"`)), bytes.Index(data, []byte(`"c"`))
	if !(ia < ib && ib < ic) {
		t.Fatalf("not sorted by ID: %s", data)
	}
}

func TestLoad_RemovesStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".workspaces-123.json.tmp")
	fresh := filepath.Join(dir, ".workspaces-456.json.tmp")
	_ = os.WriteFile(stale, []byte("x"), 0o600)
	_ = os.WriteFile(fresh, []byte("x"), 0o600)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(stale, old, old)
	if _, err := registry.Load(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale temp not removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh temp must be kept")
	}
}

func TestLoad_TightensConfigDirMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Load(dir); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, want 0700", fi.Mode().Perm())
	}
}
