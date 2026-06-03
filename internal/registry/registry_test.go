// internal/registry/registry_test.go
package registry_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/registry"
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
		ID:           "ws-abc123",
		WorktreePath: "/tmp/repo",
		Agent:        "claude",
		LastSessionID: "ses_xyz",
		Title:        "my workspace",
		LastActive:   now,
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

	// Reload from disk — persistence check
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
	// workspaces.json does not exist — Load must succeed with empty store
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
