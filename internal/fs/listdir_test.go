// internal/fs/listdir_test.go
package fs_test

import (
	"os"
	"os/exec"
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

// gitRun is a helper that runs a git command inside dir and fails the test if it errors.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestListDir_GitStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	// Initialise a real git repo.
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@test.com")
	gitRun(t, root, "config", "user.name", "Test")

	// Create and commit a file.
	committedPath := filepath.Join(root, "committed.txt")
	if err := os.WriteFile(committedPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create an untracked directory (git emits "?? dir/" in porcelain).
	untrackedDir := filepath.Join(root, "newdir")
	if err := os.MkdirAll(untrackedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Put something inside so the dir is emitted.
	if err := os.WriteFile(filepath.Join(untrackedDir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	gitRun(t, root, "add", "committed.txt")
	gitRun(t, root, "commit", "-q", "-m", "init")

	// Modify the committed file (shows as " M").
	if err := os.WriteFile(committedPath, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an untracked file.
	untrackedFile := filepath.Join(root, "untracked.txt")
	if err := os.WriteFile(untrackedFile, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes, err := fs.ListDir(root, false)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}

	byName := make(map[string]fs.Node)
	for _, n := range nodes {
		byName[n.Name] = n
	}

	// committed.txt: should be Modified=true, Untracked=false.
	if n, ok := byName["committed.txt"]; !ok {
		t.Error("committed.txt missing from listing")
	} else {
		if !n.Modified {
			t.Errorf("committed.txt: want Modified=true, got false")
		}
		if n.Untracked {
			t.Errorf("committed.txt: want Untracked=false, got true")
		}
	}

	// untracked.txt: should be Untracked=true, Modified=false.
	if n, ok := byName["untracked.txt"]; !ok {
		t.Error("untracked.txt missing from listing")
	} else {
		if !n.Untracked {
			t.Errorf("untracked.txt: want Untracked=true, got false")
		}
		if n.Modified {
			t.Errorf("untracked.txt: want Modified=false, got true")
		}
	}

	// newdir: untracked directory — porcelain emits "?? newdir/".
	if n, ok := byName["newdir"]; !ok {
		t.Error("newdir missing from listing")
	} else {
		if !n.Untracked {
			t.Errorf("newdir: want Untracked=true, got false")
		}
		if n.Modified {
			t.Errorf("newdir: want Modified=false, got true")
		}
	}
}
