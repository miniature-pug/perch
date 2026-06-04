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

// twoHunkFile creates a repo with a two-hunk file. "target.txt" has 20 lines
// committed, then lines 1 and 19 modified (unstaged) so git produces two hunks.
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

	// Stage only the first hunk (index 0).
	if err := git.StageHunk(ctx, r, repo, "target.txt", 0); err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	cachedOut, cachedErr, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "target.txt")
	if runErr != nil {
		t.Fatalf("git diff --cached: %v: %s", runErr, cachedErr)
	}
	cached := string(cachedOut)
	if !strings.Contains(cached, "TOP_CHANGE") {
		t.Errorf("cached diff should contain TOP_CHANGE, got:\n%s", cached)
	}
	if strings.Contains(cached, "BOTTOM_CHANGE") {
		t.Errorf("cached diff should NOT contain BOTTOM_CHANGE, got:\n%s", cached)
	}

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

	if err := git.DiscardHunk(ctx, r, repo, "target.txt", 0); err != nil {
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

// Staging a hunk of a file with NO trailing newline must not corrupt the index:
// after staging the sole hunk, the worktree and index agree → `git diff` is empty.
func TestStageHunk_NoTrailingNewline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)
	r := proc.ExecRunner{}
	ctx := context.Background()

	// Commit "a\nb" with NO trailing newline.
	if err := os.WriteFile(filepath.Join(repo, "nonl.txt"), []byte("a\nb"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "nonl"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Modify last line, still NO trailing newline.
	if err := os.WriteFile(filepath.Join(repo, "nonl.txt"), []byte("a\nB"), 0o644); err != nil {
		t.Fatal(err)
	}

	hunks, err := git.Hunks(ctx, r, repo, "nonl.txt")
	if err != nil || len(hunks) < 1 {
		t.Fatalf("Hunks: err=%v count=%d", err, len(hunks))
	}
	if err := git.StageHunk(ctx, r, repo, "nonl.txt", 0); err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	// Index must now byte-match the worktree → no remaining unstaged diff.
	unstaged, errOut, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--", "nonl.txt")
	if runErr != nil {
		t.Fatalf("git diff: %v: %s", runErr, errOut)
	}
	if strings.TrimSpace(string(unstaged)) != "" {
		t.Errorf("after staging the only hunk, unstaged diff must be empty (index==worktree); got:\n%s", unstaged)
	}
	// Worktree content must be unchanged (no spurious newline added).
	content, _ := os.ReadFile(filepath.Join(repo, "nonl.txt"))
	if string(content) != "a\nB" {
		t.Errorf("worktree content mutated: %q, want %q", content, "a\nB")
	}
}

// Staging a deletion hunk must stage a real deletion, not an empty blob.
func TestStageHunk_Deletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)
	r := proc.ExecRunner{}
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(repo, "del.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "add del"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Remove the tracked file (worktree deletion, unstaged).
	if err := os.Remove(filepath.Join(repo, "del.txt")); err != nil {
		t.Fatal(err)
	}

	hunks, err := git.Hunks(ctx, r, repo, "del.txt")
	if err != nil || len(hunks) < 1 {
		t.Fatalf("Hunks on deletion: err=%v count=%d", err, len(hunks))
	}
	if err := git.StageHunk(ctx, r, repo, "del.txt", 0); err != nil {
		t.Fatalf("StageHunk deletion: %v", err)
	}

	nameStatus, errOut, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--name-status")
	if runErr != nil {
		t.Fatalf("git diff --cached --name-status: %v: %s", runErr, errOut)
	}
	if !strings.Contains(string(nameStatus), "D\tdel.txt") {
		t.Errorf("expected staged deletion 'D\\tdel.txt', got:\n%s", nameStatus)
	}
}

// An out-of-range index must return an error, not panic or apply the wrong hunk.
func TestStageHunk_IndexOutOfRange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := proc.ExecRunner{}
	ctx := context.Background()
	if err := git.StageHunk(ctx, r, repo, "target.txt", 99); err == nil {
		t.Fatal("expected error for out-of-range hunk index, got nil")
	}
}

// A file path containing a newline must be rejected before invoking git.
func TestStageHunk_RejectsNewlineInPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)
	r := proc.ExecRunner{}
	ctx := context.Background()
	if err := git.StageHunk(ctx, r, repo, "evil\nname.txt", 0); err == nil {
		t.Fatal("expected error for path containing newline")
	}
}

// TestStageHunk_GoesToRunnerSeam verifies that the `git apply` invocation
// is routed through the proc.Runner seam (via RunStdin) so that FakeRunner
// intercepts it. Before the fix, gitApplyPatch called exec.CommandContext
// directly and FakeRunner never saw the apply call.
func TestStageHunk_GoesToRunnerSeam(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	// Minimal fake diff output for a single hunk.
	diffOut := "diff --git a/f.txt b/f.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/f.txt\n" +
		"+++ b/f.txt\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"+new\n"

	r := proc.NewFakeRunner()
	// git diff call returns the canned diff.
	r.Respond(proc.FakeResult{Stdout: []byte(diffOut)},
		"git", "-C", "/repo", "diff", "--unified=3", "--no-color", "--", "f.txt")
	// git apply call succeeds (empty stdout/stderr, no error).
	r.Respond(proc.FakeResult{}, "git", "apply", "--cached", "-")

	err := git.StageHunk(ctx, r, "/repo", "f.txt", 0)
	if err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	// Find the git-apply call in Calls. Before the fix this call was never
	// recorded because gitApplyPatch used exec.CommandContext directly.
	var applyCall *proc.Call
	for i := range r.Calls {
		if r.Calls[i].Name == "git" && len(r.Calls[i].Args) >= 1 && r.Calls[i].Args[0] == "apply" {
			applyCall = &r.Calls[i]
			break
		}
	}
	if applyCall == nil {
		t.Fatal("git apply was never invoked through the runner — runner seam bypassed")
	}
	if len(applyCall.Stdin) == 0 {
		t.Error("git apply was called but with empty stdin — patch not piped through runner")
	}
	if !strings.Contains(string(applyCall.Stdin), "@@ -1,1 +1,1 @@") {
		t.Errorf("git apply stdin does not contain hunk header; got:\n%s", applyCall.Stdin)
	}
}

// TestStageHunk_ExecRunnerStdinWired verifies that ExecRunner.RunStdin actually
// wires the patch to git's stdin. This is the real-git counterpart that ensures
// the production apply path (not just the seam) works end-to-end.
func TestStageHunk_ExecRunnerStdinWired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := proc.ExecRunner{}
	ctx := context.Background()

	hunks, err := git.Hunks(ctx, r, repo, "target.txt")
	if err != nil || len(hunks) < 2 {
		t.Fatalf("Hunks: err=%v count=%d", err, len(hunks))
	}

	// Stage only the first hunk (TOP_CHANGE) via the runner-backed path.
	if err := git.StageHunk(ctx, r, repo, "target.txt", 0); err != nil {
		t.Fatalf("StageHunk (ExecRunner.RunStdin): %v", err)
	}

	// Confirm the staged index contains TOP_CHANGE but not BOTTOM_CHANGE.
	cachedOut, cachedErr, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "target.txt")
	if runErr != nil {
		t.Fatalf("git diff --cached: %v: %s", runErr, cachedErr)
	}
	if !strings.Contains(string(cachedOut), "TOP_CHANGE") {
		t.Errorf("staged diff should contain TOP_CHANGE:\n%s", cachedOut)
	}
	if strings.Contains(string(cachedOut), "BOTTOM_CHANGE") {
		t.Errorf("staged diff should NOT contain BOTTOM_CHANGE:\n%s", cachedOut)
	}
}
