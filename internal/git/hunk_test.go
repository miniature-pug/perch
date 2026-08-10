// internal/git/hunk_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
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

	// a.txt: modified (unstaged). Should show as "M".
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

func TestHunks_StagedFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// file_staged.txt: create, commit, modify, then stage the modification.
	// No further working-tree modification: this file has staged-only changes.
	if err := os.WriteFile(filepath.Join(repo, "file_staged.txt"), []byte("original content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "file_staged.txt")
	gitRun("commit", "-m", "add file_staged")

	if err := os.WriteFile(filepath.Join(repo, "file_staged.txt"), []byte("staged content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Do NOT commit yet. file_staged.txt has a staged-but-uncommitted change.

	// file_working.txt: create and commit in its own commit BEFORE staging
	// file_staged.txt, so that the subsequent commit of file_working.txt does
	// not accidentally sweep up the staged file_staged.txt change.
	if err := os.WriteFile(filepath.Join(repo, "file_working.txt"), []byte("original content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "file_working.txt")
	gitRun("commit", "-m", "add file_working")

	// Now stage the modification to file_staged.txt (after file_working is committed).
	if err := os.WriteFile(filepath.Join(repo, "file_staged.txt"), []byte("staged content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "file_staged.txt") // stage the modification, NOT committed

	if err := os.WriteFile(filepath.Join(repo, "file_working.txt"), []byte("working content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// do NOT stage file_working.txt

	r := proc.ExecRunner{}
	ctx := context.Background()

	// Staged file: all returned hunks should have Staged=true.
	stagedHunks, err := git.Hunks(ctx, r, repo, "file_staged.txt")
	if err != nil {
		t.Fatalf("Hunks(file_staged.txt): %v", err)
	}
	if len(stagedHunks) == 0 {
		t.Fatal("expected at least one hunk for file_staged.txt, got none")
	}
	for i, h := range stagedHunks {
		if !h.Staged {
			t.Errorf("file_staged.txt hunk[%d]: Staged=false, want true", i)
		}
	}

	// Working-tree file: all returned hunks should have Staged=false.
	workingHunks, err := git.Hunks(ctx, r, repo, "file_working.txt")
	if err != nil {
		t.Fatalf("Hunks(file_working.txt): %v", err)
	}
	if len(workingHunks) == 0 {
		t.Fatal("expected at least one hunk for file_working.txt, got none")
	}
	for i, h := range workingHunks {
		if h.Staged {
			t.Errorf("file_working.txt hunk[%d]: Staged=true, want false", i)
		}
	}
}
