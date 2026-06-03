// internal/fs/listdir_test.go
package fs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/fs"
)

func TestListDir_DirsFirstNameAsc(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	for _, name := range []string{"z.txt", "a.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "subdir", "file.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes, err := fs.ListDir(root, false)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(nodes) < 3 {
		t.Fatalf("expected ≥3 nodes, got %d", len(nodes))
	}
	if !nodes[0].IsDir || nodes[0].Name != "subdir" {
		t.Errorf("expected first node to be dir 'subdir', got %+v", nodes[0])
	}
	if nodes[1].Name != "a.txt" || nodes[2].Name != "z.txt" {
		t.Errorf("unexpected file order: %s %s", nodes[1].Name, nodes[2].Name)
	}
	for _, n := range nodes {
		if !filepath.IsAbs(n.Path) {
			t.Errorf("node %s has non-absolute path %s", n.Name, n.Path)
		}
	}
}

func TestListDir_GitignoreAware(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\nignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}

	nodes, err := fs.ListDir(root, true)
	if err != nil {
		t.Fatalf("ListDir gitignore-aware: %v", err)
	}
	for _, n := range nodes {
		if n.Name == "skip.log" {
			t.Errorf("skip.log should have been filtered by .gitignore")
		}
		if n.Name == "ignored" {
			t.Errorf("ignored/ dir should have been filtered by .gitignore")
		}
	}
	found := false
	for _, n := range nodes {
		if n.Name == "keep.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("keep.go should be present")
	}
}
