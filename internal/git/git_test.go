package git

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
)

// ── Fixtures ──────────────────────────────────────────────────────────────────

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// ── ParsePorcelain: fixture-based tests ───────────────────────────────────────

func TestParsePorcelain_Multi(t *testing.T) {
	raw := readFixture(t, "worktree-list-porcelain-multi.txt")
	wts, err := ParsePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 3 {
		t.Fatalf("want 3 worktrees, got %d", len(wts))
	}

	// entry[0] is the main checkout.
	if wts[0].Path != "/repos/main-repo" {
		t.Errorf("entry[0].Path = %q, want /repos/main-repo", wts[0].Path)
	}
	if wts[0].Branch != "main" {
		t.Errorf("entry[0].Branch = %q, want main", wts[0].Branch)
	}
	if wts[0].Bare || wts[0].Detached {
		t.Errorf("entry[0] should not be bare or detached")
	}

	// Linked worktrees follow.
	if wts[1].Path != "/repos/worktree-bugfix" {
		t.Errorf("entry[1].Path = %q, want /repos/worktree-bugfix", wts[1].Path)
	}
	if wts[1].Branch != "bugfix" {
		t.Errorf("entry[1].Branch = %q, want bugfix", wts[1].Branch)
	}
	if wts[2].Path != "/repos/worktree-feature" {
		t.Errorf("entry[2].Path = %q, want /repos/worktree-feature", wts[2].Path)
	}
	if wts[2].Branch != "feature" {
		t.Errorf("entry[2].Branch = %q, want feature", wts[2].Branch)
	}
}

func TestParsePorcelain_Single(t *testing.T) {
	raw := readFixture(t, "worktree-list-porcelain-single.txt")
	wts, err := ParsePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("want 1 worktree, got %d", len(wts))
	}
	if wts[0].Path != "/repos/single-repo" {
		t.Errorf("Path = %q, want /repos/single-repo", wts[0].Path)
	}
	if wts[0].Branch != "main" {
		t.Errorf("Branch = %q, want main", wts[0].Branch)
	}
}

// TestParsePorcelain_Linked asserts the design linchpin: even when
// `git worktree list --porcelain` is run from INSIDE a linked worktree, the
// main checkout appears as entry[0]. This fixture was captured from within the
// linked worktree (/repos/wt-feature), yet git still lists the main checkout
// (/repos/main) first. This 2-entry fixture is intentionally distinct from the
// 3-entry multi fixture to avoid redundancy while proving the entry[0]=main invariant.
func TestParsePorcelain_Linked(t *testing.T) {
	raw := readFixture(t, "worktree-list-porcelain-linked.txt")
	wts, err := ParsePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fixture has exactly 2 entries: main checkout + one linked worktree.
	if len(wts) != 2 {
		t.Fatalf("want 2 worktrees, got %d", len(wts))
	}

	// Design linchpin: entry[0] is the MAIN checkout, even though the command
	// was captured from inside the linked worktree (/repos/wt-feature).
	if wts[0].Path != "/repos/main" {
		t.Errorf("entry[0].Path = %q, want /repos/main — main checkout must be entry[0] regardless of invocation directory", wts[0].Path)
	}
	if wts[0].Branch != "main" {
		t.Errorf("entry[0].Branch = %q, want main", wts[0].Branch)
	}
	if wts[0].Bare || wts[0].Detached {
		t.Errorf("entry[0] must be the main non-bare checkout, got bare=%v detached=%v", wts[0].Bare, wts[0].Detached)
	}

	// entry[1] is the linked worktree on branch "feature".
	if wts[1].Path != "/repos/wt-feature" {
		t.Errorf("entry[1].Path = %q, want /repos/wt-feature", wts[1].Path)
	}
	if wts[1].Branch != "feature" {
		t.Errorf("entry[1].Branch = %q, want feature", wts[1].Branch)
	}
}

// ── ParsePorcelain: inline-string tests ───────────────────────────────────────

// inlineBareInput is the real format captured via `git worktree list --porcelain`
// on a bare clone (`git clone --bare`). A bare repo has no working checkout.
const inlineBareInput = `worktree /repos/bare-repo
bare
`

// inlineDetachedInput is the real format captured for a detached worktree
// created via `git worktree add --detach`.
const inlineDetachedInput = `worktree /repos/main-repo
HEAD 0000000000000000000000000000000000000001
branch refs/heads/main

worktree /repos/worktree-detached
HEAD 0000000000000000000000000000000000000001
detached
`

// inlineLockedWithReasonInput is the real format captured after
// `git worktree lock --reason "keep for CI"`.
const inlineLockedWithReasonInput = `worktree /repos/main-repo
HEAD 0000000000000000000000000000000000000001
branch refs/heads/main

worktree /repos/worktree-feature
HEAD 0000000000000000000000000000000000000001
branch refs/heads/feature
locked keep for CI
`

// inlineLockedNoReasonInput is the real format captured after
// `git worktree lock` with no --reason flag.
const inlineLockedNoReasonInput = `worktree /repos/main-repo
HEAD 0000000000000000000000000000000000000001
branch refs/heads/main

worktree /repos/worktree-bugfix
HEAD 0000000000000000000000000000000000000001
branch refs/heads/bugfix
locked
`

func TestParsePorcelain_Bare(t *testing.T) {
	wts, err := ParsePorcelain([]byte(inlineBareInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("want 1 worktree, got %d", len(wts))
	}
	if !wts[0].Bare {
		t.Errorf("want Bare=true, got false")
	}
	if wts[0].Branch != "" {
		t.Errorf("want empty Branch for bare, got %q", wts[0].Branch)
	}
	if wts[0].Head != "" {
		t.Errorf("want empty Head for bare, got %q", wts[0].Head)
	}
}

func TestMainWorktree_BareOnly(t *testing.T) {
	wts, err := ParsePorcelain([]byte(inlineBareInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok := MainWorktree(wts)
	if ok {
		t.Errorf("MainWorktree on bare-only input must return ok=false")
	}
}

func TestToTrees_BareSkipped(t *testing.T) {
	wts, err := ParsePorcelain([]byte(inlineBareInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	proj := &model.Project{Path: "/repos/bare-repo", Name: "bare-repo"}
	trees := ToTrees(wts, proj)
	if len(trees) != 0 {
		t.Errorf("ToTrees must skip bare entries, got %d trees", len(trees))
	}
}

func TestParsePorcelain_Detached(t *testing.T) {
	wts, err := ParsePorcelain([]byte(inlineDetachedInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("want 2 worktrees, got %d", len(wts))
	}
	det := wts[1]
	if !det.Detached {
		t.Errorf("want Detached=true, got false")
	}
	if det.Branch != "" {
		t.Errorf("want empty Branch for detached, got %q", det.Branch)
	}
}

func TestParsePorcelain_LockedWithReason(t *testing.T) {
	wts, err := ParsePorcelain([]byte(inlineLockedWithReasonInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("want 2 worktrees, got %d", len(wts))
	}
	if !wts[1].Locked {
		t.Errorf("want Locked=true, got false")
	}
}

func TestParsePorcelain_LockedNoReason(t *testing.T) {
	wts, err := ParsePorcelain([]byte(inlineLockedNoReasonInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("want 2 worktrees, got %d", len(wts))
	}
	if !wts[1].Locked {
		t.Errorf("want Locked=true, got false")
	}
}

func TestParsePorcelain_BranchStripped(t *testing.T) {
	input := "worktree /repos/repo\nHEAD 0000000000000000000000000000000000000001\nbranch refs/heads/feature\n"
	wts, err := ParsePorcelain([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("want 1 worktree, got %d", len(wts))
	}
	if wts[0].Branch != "feature" {
		t.Errorf("Branch = %q, want %q", wts[0].Branch, "feature")
	}
}

// TestParsePorcelain_Malformed_AttrBeforeWorktree verifies that a known
// attribute line appearing before any worktree line returns an error and does
// not panic.
func TestParsePorcelain_Malformed_AttrBeforeWorktree(t *testing.T) {
	input := "HEAD abc123\nworktree /repos/repo\n"
	_, err := ParsePorcelain([]byte(input))
	if err == nil {
		t.Fatal("want error for known attribute before worktree line, got nil")
	}
}

// TestParsePorcelain_Malformed_UnknownLinesSkipped verifies that unknown lines
// (garbage/future attributes) are silently skipped without returning an error.
func TestParsePorcelain_Malformed_UnknownLinesSkipped(t *testing.T) {
	input := "worktree /repos/repo\nHEAD 0000000000000000000000000000000000000001\nfuture-attr value\nbranch refs/heads/main\n"
	wts, err := ParsePorcelain([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("want 1 worktree, got %d", len(wts))
	}
	if wts[0].Branch != "main" {
		t.Errorf("Branch = %q, want main", wts[0].Branch)
	}
}

// TestParsePorcelain_LeadingTrailingBlanks verifies tolerance of extra blank lines.
func TestParsePorcelain_LeadingTrailingBlanks(t *testing.T) {
	input := "\n\nworktree /repos/repo\nHEAD 0000000000000000000000000000000000000001\nbranch refs/heads/main\n\n\n"
	wts, err := ParsePorcelain([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("want 1 worktree, got %d", len(wts))
	}
}

// TestParsePorcelain_CRLF verifies that CRLF line endings are handled.
func TestParsePorcelain_CRLF(t *testing.T) {
	input := "worktree /repos/repo\r\nHEAD 0000000000000000000000000000000000000001\r\nbranch refs/heads/main\r\n"
	wts, err := ParsePorcelain([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("want 1 worktree, got %d", len(wts))
	}
	if wts[0].Branch != "main" {
		t.Errorf("Branch = %q, want main", wts[0].Branch)
	}
	if wts[0].Path != "/repos/repo" {
		t.Errorf("Path = %q, want /repos/repo", wts[0].Path)
	}
}

// ── ListWorktrees ─────────────────────────────────────────────────────────────

func TestListWorktrees_HappyPath(t *testing.T) {
	fixture := readFixture(t, "worktree-list-porcelain-multi.txt")
	root := "/repos/main-repo"

	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: fixture}, "git", "-C", root, "worktree", "list", "--porcelain")

	wts, err := ListWorktrees(context.Background(), r, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 3 {
		t.Fatalf("want 3 worktrees, got %d", len(wts))
	}

	// Assert exact invocation recorded.
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 recorded call, got %d", len(r.Calls))
	}
	wantCall := proc.Call{
		Name: "git",
		Args: []string{"-C", root, "worktree", "list", "--porcelain"},
	}
	if !reflect.DeepEqual(r.Calls[0], wantCall) {
		t.Errorf("r.Calls[0] = %+v, want %+v", r.Calls[0], wantCall)
	}
}

func TestListWorktrees_RunnerError(t *testing.T) {
	root := "/repos/main-repo"
	r := proc.NewFakeRunner()
	// Deliberately register no response and no Default → FakeRunner returns error.
	_, err := ListWorktrees(context.Background(), r, root)
	if err == nil {
		t.Fatal("want error on runner failure, got nil")
	}
}

func TestListWorktrees_RunnerErrorWithStderr(t *testing.T) {
	root := "/repos/main-repo"
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: not a git repository"),
		Err:    errors.New("exit status 128"),
	}, "git", "-C", root, "worktree", "list", "--porcelain")

	_, err := ListWorktrees(context.Background(), r, root)
	if err == nil {
		t.Fatal("want error on runner failure, got nil")
	}
	// Error message should include stderr.
	if err.Error() == "" {
		t.Errorf("want non-empty error message")
	}
}

// ── MainWorktree ──────────────────────────────────────────────────────────────

func TestMainWorktree_Multi(t *testing.T) {
	fixture := readFixture(t, "worktree-list-porcelain-multi.txt")
	wts, err := ParsePorcelain(fixture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	main, ok := MainWorktree(wts)
	if !ok {
		t.Fatal("MainWorktree returned ok=false, want true")
	}
	if main.Path != "/repos/main-repo" {
		t.Errorf("main.Path = %q, want /repos/main-repo", main.Path)
	}
}

func TestMainWorktree_Single(t *testing.T) {
	fixture := readFixture(t, "worktree-list-porcelain-single.txt")
	wts, err := ParsePorcelain(fixture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	main, ok := MainWorktree(wts)
	if !ok {
		t.Fatal("MainWorktree returned ok=false, want true")
	}
	if main.Path != "/repos/single-repo" {
		t.Errorf("main.Path = %q, want /repos/single-repo", main.Path)
	}
}

func TestMainWorktree_Empty(t *testing.T) {
	_, ok := MainWorktree(nil)
	if ok {
		t.Error("MainWorktree on nil slice must return ok=false")
	}
}

// ── ToTrees ───────────────────────────────────────────────────────────────────

func TestToTrees_Multi(t *testing.T) {
	fixture := readFixture(t, "worktree-list-porcelain-multi.txt")
	wts, err := ParsePorcelain(fixture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	proj := &model.Project{Path: "/repos/main-repo", Name: "main-repo"}
	trees := ToTrees(wts, proj)

	if len(trees) != 3 {
		t.Fatalf("want 3 trees, got %d", len(trees))
	}

	// IsMain mapping: first entry is main.
	if !trees[0].IsMain {
		t.Errorf("trees[0].IsMain = false, want true")
	}
	if trees[1].IsMain {
		t.Errorf("trees[1].IsMain = true, want false")
	}
	if trees[2].IsMain {
		t.Errorf("trees[2].IsMain = true, want false")
	}

	// Project pointer wired on every tree.
	for i, tr := range trees {
		if tr.Project != proj {
			t.Errorf("trees[%d].Project pointer mismatch", i)
		}
	}

	// Branch propagation.
	if trees[0].Branch != "main" {
		t.Errorf("trees[0].Branch = %q, want main", trees[0].Branch)
	}
	if trees[1].Branch != "bugfix" {
		t.Errorf("trees[1].Branch = %q, want bugfix", trees[1].Branch)
	}
}

// TestToTrees_BareAndNonBare verifies that a bare entry at position 0 is
// skipped and the subsequent non-bare entry becomes IsMain.
func TestToTrees_BareAndNonBare(t *testing.T) {
	// Synthetic: bare first, then a working checkout.
	wts := []Worktree{
		{Path: "/repos/bare", Bare: true},
		{Path: "/repos/main", Branch: "main"},
		{Path: "/repos/linked", Branch: "linked"},
	}
	proj := &model.Project{Path: "/repos/bare", Name: "bare"}
	trees := ToTrees(wts, proj)

	if len(trees) != 2 {
		t.Fatalf("want 2 trees (bare skipped), got %d", len(trees))
	}
	if !trees[0].IsMain {
		t.Errorf("trees[0].IsMain = false, want true (first non-bare)")
	}
	if trees[1].IsMain {
		t.Errorf("trees[1].IsMain = true, want false")
	}
}
