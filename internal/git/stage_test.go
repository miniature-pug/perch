// internal/git/stage_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

// twoHunkFile creates a repo with a two-hunk file ready to test staging.
// Returns repo path. The file "target.txt" has 20 lines committed, then lines
// 1 and 19 modified (unstaged) so git produces two hunks.
func twoHunkFile(t *testing.T) string {
	t.Helper()
	repo := initRepo(t)
	orig := ""
	for i := 0; i < 20; i++ {
		orig += "line\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "target.txt"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	var lines [20]string
	for i := range lines {
		lines[i] = "line\n"
	}
	lines[0] = "TOP_CHANGE\n"
	lines[18] = "BOTTOM_CHANGE\n"
	content := ""
	for _, l := range lines {
		content += l
	}
	if err := os.WriteFile(filepath.Join(repo, "target.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestStageHunk_OneOfTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := proc.ExecRunner{}
	ctx := context.Background()

	hunks, err := git.Hunks(ctx, r, repo, "target.txt")
	if err != nil || len(hunks) < 2 {
		t.Fatalf("Hunks setup: err=%v count=%d", err, len(hunks))
	}

	// Stage only the first hunk
	if err := git.StageHunk(ctx, r, repo, hunks[0]); err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	// git diff --cached should show the staged hunk (first change)
	cachedOut, cachedErr, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "target.txt")
	if runErr != nil {
		t.Fatalf("git diff --cached: %v: %s", runErr, cachedErr)
	}
	cached := string(cachedOut)
	if !strings.Contains(cached, "TOP_CHANGE") {
		t.Errorf("cached diff should contain TOP_CHANGE, got:\n%s", cached)
	}
	// The second hunk should NOT be staged
	if strings.Contains(cached, "BOTTOM_CHANGE") {
		t.Errorf("cached diff should NOT contain BOTTOM_CHANGE, got:\n%s", cached)
	}

	// git diff (unstaged) should still contain BOTTOM_CHANGE
	unstagedOut, _, _ := r.Run(ctx, "git", "-C", repo, "diff", "--", "target.txt")
	if !strings.Contains(string(unstagedOut), "BOTTOM_CHANGE") {
		t.Errorf("unstaged diff should still contain BOTTOM_CHANGE")
	}
}

func TestDiscardHunk_RevertsWorktreeLines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := proc.ExecRunner{}
	ctx := context.Background()

	hunks, err := git.Hunks(ctx, r, repo, "target.txt")
	if err != nil || len(hunks) < 2 {
		t.Fatalf("Hunks setup: err=%v count=%d", err, len(hunks))
	}

	// Discard the first hunk (TOP_CHANGE → reverts to "line\n")
	if err := git.DiscardHunk(ctx, r, repo, hunks[0]); err != nil {
		t.Fatalf("DiscardHunk: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(repo, "target.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "TOP_CHANGE") {
		t.Errorf("TOP_CHANGE should have been discarded, file:\n%s", content)
	}
	if !strings.Contains(string(content), "BOTTOM_CHANGE") {
		t.Errorf("BOTTOM_CHANGE should still be present, file:\n%s", content)
	}
}
