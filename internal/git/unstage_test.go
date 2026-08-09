// internal/git/unstage_test.go
package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
)

// TestUnstageHunk_GoesToRunnerSeam is the FakeRunner argv guard: unstaging must
// resolve the merged Hunks() index (which reads both the working-tree and cached
// diffs) and pipe the reverse patch to `git apply --reverse --cached -` through
// the runner seam (RunStdin), so the working tree is never touched and the call is
// interceptable in unit tests. Here the working-tree diff is empty, so the sole
// staged hunk sits at merged index 0.
func TestUnstageHunk_GoesToRunnerSeam(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	// Minimal fake cached (staged) diff output for a single hunk.
	cachedDiff := "diff --git a/f.txt b/f.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/f.txt\n" +
		"+++ b/f.txt\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"+new\n"

	r := proc.NewFakeRunner()
	// Resolving the merged index reads the working-tree diff too; it is empty here
	// so the only hunk is the staged one at merged index 0.
	r.Respond(proc.FakeResult{Stdout: []byte("")},
		"git", "-C", "/repo", "diff", "--unified=3", "--no-color", "--", "f.txt")
	// The staged hunk is located in `git diff --cached ...` output by header match.
	r.Respond(proc.FakeResult{Stdout: []byte(cachedDiff)},
		"git", "-C", "/repo", "diff", "--cached", "--unified=3", "--no-color", "--", "f.txt")
	// The apply must be `git apply --reverse --cached -`.
	r.Respond(proc.FakeResult{}, "git", "apply", "--reverse", "--cached", "-")

	if err := git.UnstageHunk(ctx, r, "/repo", "f.txt", 0); err != nil {
		t.Fatalf("UnstageHunk: %v", err)
	}

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
	wantArgs := []string{"apply", "--reverse", "--cached", "-"}
	if strings.Join(applyCall.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("apply argv = %v, want %v", applyCall.Args, wantArgs)
	}
	if len(applyCall.Stdin) == 0 {
		t.Error("git apply called with empty stdin — patch not piped through runner")
	}
	if !strings.Contains(string(applyCall.Stdin), "@@ -1,1 +1,1 @@") {
		t.Errorf("git apply stdin missing hunk header; got:\n%s", applyCall.Stdin)
	}
}

// TestUnstageHunk_RejectsNewlineInPath rejects a file path with a newline before
// invoking git (parity with StageHunk/DiscardHunk).
func TestUnstageHunk_RejectsNewlineInPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := proc.ExecRunner{}
	if err := git.UnstageHunk(context.Background(), r, t.TempDir(), "evil\nname.txt", 0); err == nil {
		t.Fatal("expected error for path containing newline")
	}
}

// TestUnstageHunk_MovesStagedHunkBackToWorktree is the real-git round-trip: stage
// a hunk, then unstage it, and assert the index no longer carries it while the
// working-tree content is byte-for-byte unchanged. This exercises the production
// apply path (ExecRunner.RunStdin) against a throwaway repo. It matches the git
// package's existing real-git test style (see stage_test.go); running it under
// `-tags=integration` as well is a superset, not a conflict.
func TestUnstageHunk_MovesStagedHunkBackToWorktree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t) // target.txt: TOP_CHANGE + BOTTOM_CHANGE, both unstaged
	r := proc.ExecRunner{}
	ctx := context.Background()

	// Capture the working-tree content up front; it must be identical after the
	// whole stage→unstage cycle (unstage touches the index only).
	wtPath := filepath.Join(repo, "target.txt")
	before, err := os.ReadFile(wtPath)
	if err != nil {
		t.Fatal(err)
	}

	// Stage only the first working-tree hunk (TOP_CHANGE).
	if err := git.StageHunk(ctx, r, repo, "target.txt", 0); err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	// Pre-condition: TOP_CHANGE is now staged, BOTTOM_CHANGE is still unstaged.
	cachedOut, cErr, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "target.txt")
	if runErr != nil {
		t.Fatalf("git diff --cached: %v: %s", runErr, cErr)
	}
	if !strings.Contains(string(cachedOut), "TOP_CHANGE") {
		t.Fatalf("pre-condition: staged index should contain TOP_CHANGE, got:\n%s", cachedOut)
	}

	// Unstage the staged hunk by its MERGED Hunks() index. Because BOTTOM_CHANGE
	// remains an unstaged hunk, the staged TOP_CHANGE hunk sits at merged index 1,
	// not 0 — passing a hardcoded 0 would target the wrong (unstaged) hunk.
	hunks, err := git.Hunks(ctx, r, repo, "target.txt")
	if err != nil {
		t.Fatalf("Hunks: %v", err)
	}
	stagedIdx := -1
	for _, h := range hunks {
		if h.Staged {
			stagedIdx = h.Index
			break
		}
	}
	if stagedIdx < 0 {
		t.Fatalf("expected a staged hunk after StageHunk; hunks=%+v", hunks)
	}
	if err := git.UnstageHunk(ctx, r, repo, "target.txt", stagedIdx); err != nil {
		t.Fatalf("UnstageHunk(index=%d): %v", stagedIdx, err)
	}

	// The index must no longer carry TOP_CHANGE (nothing staged).
	cachedAfter, cErr2, runErr2 := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "target.txt")
	if runErr2 != nil {
		t.Fatalf("git diff --cached (after): %v: %s", runErr2, cErr2)
	}
	if strings.TrimSpace(string(cachedAfter)) != "" {
		t.Errorf("after unstage the index must be empty for target.txt; got staged diff:\n%s", cachedAfter)
	}

	// The change must be back in the working tree (both hunks unstaged again).
	unstagedAfter, _, _ := r.Run(ctx, "git", "-C", repo, "diff", "--", "target.txt")
	if !strings.Contains(string(unstagedAfter), "TOP_CHANGE") {
		t.Errorf("after unstage, TOP_CHANGE must reappear as an unstaged change; got:\n%s", unstagedAfter)
	}
	if !strings.Contains(string(unstagedAfter), "BOTTOM_CHANGE") {
		t.Errorf("after unstage, BOTTOM_CHANGE must still be an unstaged change; got:\n%s", unstagedAfter)
	}

	// The working-tree FILE content must be byte-identical to before (unstage
	// reverts the INDEX only, never the working tree).
	after, err := os.ReadFile(wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("working-tree content mutated by unstage:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// mixedStagedFile builds a repo whose "mixed.txt" has three well-separated hunks,
// then stages the top two (TOP_HUNK, MID_HUNK) and leaves the third (BOT_HUNK)
// unstaged. This produces the mixed staged/unstaged state in which a staged
// hunk's MERGED Hunks() index differs from its position in `git diff --cached`:
// the unstaged BOT_HUNK occupies merged index 0, pushing the two staged hunks to
// merged indices 1 and 2 even though they sit at cached positions 0 and 1.
func mixedStagedFile(t *testing.T, r proc.Runner) string {
	t.Helper()
	ctx := context.Background()
	repo := initRepo(t)

	// 30 committed lines.
	orig := ""
	for i := 0; i < 30; i++ {
		orig += "line\n"
	}
	target := filepath.Join(repo, "mixed.txt")
	if err := os.WriteFile(target, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base mixed")

	// Change lines 1, 15, 29 → three non-adjacent hunks under unified=3 context.
	var lines [30]string
	for i := range lines {
		lines[i] = "line\n"
	}
	lines[0] = "TOP_HUNK\n"
	lines[14] = "MID_HUNK\n"
	lines[28] = "BOT_HUNK\n"
	content := ""
	for _, l := range lines {
		content += l
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stage the top two hunks. Staging shifts the working-tree indices, so after
	// staging the current topmost hunk (index 0) the next one becomes index 0.
	if err := git.StageHunk(ctx, r, repo, "mixed.txt", 0); err != nil { // TOP_HUNK
		t.Fatalf("StageHunk TOP: %v", err)
	}
	if err := git.StageHunk(ctx, r, repo, "mixed.txt", 0); err != nil { // MID_HUNK
		t.Fatalf("StageHunk MID: %v", err)
	}
	return repo
}

// hunkTouches reports whether any line of h contains substr.
func hunkTouches(h git.Hunk, substr string) bool {
	for _, l := range h.Lines {
		if strings.Contains(l.Text, substr) {
			return true
		}
	}
	return false
}

// TestUnstageHunk_MixedDiff_UnstageFirstStaged is the regression guard for the F2
// merged-index bug. For a file with BOTH an unstaged hunk and multiple staged
// hunks, UnstageHunk must honor the MERGED Hunks() index — the same contract
// StageHunk/DiscardHunk use — not a `git diff --cached` position. Here mixed.txt
// has one unstaged hunk (BOT_HUNK) and two staged hunks (TOP_HUNK, MID_HUNK), so
// the FIRST staged hunk's merged index is 1 while its cached-diff position is 0.
// The buggy cached-local implementation sliced cached position 1 (MID_HUNK) and
// unstaged the WRONG hunk; the fix locates the target by header and unstages
// TOP_HUNK. This test fails against the buggy implementation and passes after it.
func TestUnstageHunk_MixedDiff_UnstageFirstStaged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := proc.ExecRunner{}
	ctx := context.Background()
	repo := mixedStagedFile(t, r)

	wtPath := filepath.Join(repo, "mixed.txt")
	before, err := os.ReadFile(wtPath)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve the merged index of the FIRST staged hunk — the index a frontend
	// would pass. It must be nonzero here because an unstaged hunk (BOT_HUNK)
	// precedes the staged hunks in the merged list, the exact condition the buggy
	// cached-local interpretation mishandled.
	hunks, err := git.Hunks(ctx, r, repo, "mixed.txt")
	if err != nil {
		t.Fatalf("Hunks: %v", err)
	}
	firstStaged := -1
	for _, h := range hunks {
		if h.Staged {
			firstStaged = h.Index
			break
		}
	}
	if firstStaged < 0 {
		t.Fatalf("setup: expected at least one staged hunk; hunks=%+v", hunks)
	}
	if firstStaged == 0 {
		t.Fatalf("setup: the first staged hunk should have a nonzero merged index "+
			"(an unstaged hunk should precede it); got 0, hunks=%+v", hunks)
	}
	// Sanity: the first staged hunk is TOP_HUNK (topmost by file order).
	if !hunkTouches(hunks[firstStaged], "TOP_HUNK") {
		t.Fatalf("setup: first staged hunk should touch TOP_HUNK; got %+v", hunks[firstStaged])
	}

	// Unstage the FIRST staged hunk by its merged index.
	if err := git.UnstageHunk(ctx, r, repo, "mixed.txt", firstStaged); err != nil {
		t.Fatalf("UnstageHunk(index=%d): %v", firstStaged, err)
	}

	// The correct hunk (TOP_HUNK) must have left the index; the OTHER staged hunk
	// (MID_HUNK) must STILL be staged.
	cachedOut, cErr, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "mixed.txt")
	if runErr != nil {
		t.Fatalf("git diff --cached: %v: %s", runErr, cErr)
	}
	cached := string(cachedOut)
	if strings.Contains(cached, "TOP_HUNK") {
		t.Errorf("wrong hunk unstaged: TOP_HUNK must no longer be staged; cached diff:\n%s", cached)
	}
	if !strings.Contains(cached, "MID_HUNK") {
		t.Errorf("wrong hunk unstaged: MID_HUNK must remain staged; cached diff:\n%s", cached)
	}

	// TOP_HUNK must reappear as an unstaged change; BOT_HUNK must still be one.
	unstagedOut, _, _ := r.Run(ctx, "git", "-C", repo, "diff", "--", "mixed.txt")
	unstaged := string(unstagedOut)
	if !strings.Contains(unstaged, "TOP_HUNK") {
		t.Errorf("TOP_HUNK must reappear as an unstaged change; unstaged diff:\n%s", unstaged)
	}
	if !strings.Contains(unstaged, "BOT_HUNK") {
		t.Errorf("BOT_HUNK must still be an unstaged change; unstaged diff:\n%s", unstaged)
	}

	// Unstage reverts the INDEX only: the working-tree file must be byte-identical.
	after, err := os.ReadFile(wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("working-tree content mutated by unstage:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
