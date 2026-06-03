// internal/git/hunks_test.go
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestHunks_MultiHunk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t) // defined in hunk_test.go

	// Write a file with 20 lines and commit it
	orig := ""
	for i := 0; i < 20; i++ {
		orig += "line\n"
	}
	target := filepath.Join(repo, "multi.txt")
	if err := os.WriteFile(target, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add multi")

	// Modify line 1 and line 19 to force two non-adjacent hunks
	var lines [20]string
	for i := range lines {
		lines[i] = "line\n"
	}
	lines[0] = "CHANGED_TOP\n"
	lines[18] = "CHANGED_BOTTOM\n"
	modified := ""
	for _, l := range lines {
		modified += l
	}
	if err := os.WriteFile(target, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	r := proc.ExecRunner{}
	hunks, err := git.Hunks(context.Background(), r, repo, "multi.txt")
	if err != nil {
		t.Fatalf("Hunks: %v", err)
	}
	if len(hunks) < 2 {
		t.Fatalf("expected ≥2 hunks, got %d", len(hunks))
	}
	for i, h := range hunks {
		if h.Index != i {
			t.Errorf("hunk[%d].Index = %d, want %d", i, h.Index, i)
		}
		if h.File != "multi.txt" {
			t.Errorf("hunk[%d].File = %q, want multi.txt", i, h.File)
		}
		if h.Header == "" {
			t.Errorf("hunk[%d].Header is empty", i)
		}
		for j, l := range h.Lines {
			switch l.Kind {
			case "ctx", "add", "del":
			default:
				t.Errorf("hunk[%d].Lines[%d].Kind = %q, want ctx/add/del", i, j, l.Kind)
			}
		}
	}
}
