// internal/git/hunk_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

// initRepo creates a temp git repo with an initial commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// initial commit
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestDiffStat_ModifiedAddedDeleted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	// Create initial state: a.txt, b.txt, c.txt all committed
	for name, content := range map[string]string{
		"a.txt": "line1\nline2\n",
		"b.txt": "orig\n",
		"c.txt": "will delete\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "add files"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Modify a.txt, add new.txt, delete c.txt
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("line1\nmodified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "c.txt")); err != nil {
		t.Fatal(err)
	}

	r := proc.ExecRunner{}
	ctx := context.Background()
	diffs, err := git.DiffStat(ctx, r, repo)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}

	byPath := make(map[string]git.FileDiff)
	for _, d := range diffs {
		byPath[d.Path] = d
	}

	// a.txt: modified (unstaged) — should show as "M"
	a, ok := byPath["a.txt"]
	if !ok {
		t.Fatal("a.txt missing from DiffStat")
	}
	if a.Status != "M" {
		t.Errorf("a.txt status = %q, want M", a.Status)
	}
	if a.Added < 1 || a.Removed < 1 {
		t.Errorf("a.txt added=%d removed=%d, want both ≥1", a.Added, a.Removed)
	}

	// c.txt: deleted (unstaged)
	c, ok := byPath["c.txt"]
	if !ok {
		t.Fatal("c.txt missing from DiffStat")
	}
	if c.Status != "D" {
		t.Errorf("c.txt status = %q, want D", c.Status)
	}
}
