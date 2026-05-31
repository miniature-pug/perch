package discover

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
)

// porcelainForPath returns a minimal git worktree porcelain blob for a single
// non-bare checkout at path on the given branch.
func porcelainForPath(path, branch string) []byte {
	return []byte("worktree " + path + "\nHEAD 0000000000000000000000000000000000000001\nbranch refs/heads/" + branch + "\n")
}

// porcelainMainPlusLinked returns a porcelain blob with two entries: main at
// mainPath and a linked worktree at linkedPath on linkedBranch. The main
// checkout always appears as entry[0], regardless of which path was scanned.
func porcelainMainPlusLinked(mainPath, mainBranch, linkedPath, linkedBranch string) []byte {
	return []byte(
		"worktree " + mainPath + "\nHEAD 0000000000000000000000000000000000000001\nbranch refs/heads/" + mainBranch + "\n\n" +
			"worktree " + linkedPath + "\nHEAD 0000000000000000000000000000000000000002\nbranch refs/heads/" + linkedBranch + "\n",
	)
}

// makeFakeRepo creates a directory at path and adds a .git entry so Scan
// reports it as a candidate. dir=true makes a .git directory (normal repo);
// dir=false makes a .git file (linked worktree / submodule).
func makeFakeRepo(t *testing.T, path string, gitIsDir bool) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("makeFakeRepo mkdir %s: %v", path, err)
	}
	gitPath := filepath.Join(path, ".git")
	if gitIsDir {
		if err := os.Mkdir(gitPath, 0o755); err != nil {
			t.Fatalf("makeFakeRepo mkdir .git %s: %v", gitPath, err)
		}
	} else {
		if err := os.WriteFile(gitPath, []byte("gitdir: ../../../.git/worktrees/wt\n"), 0o644); err != nil {
			t.Fatalf("makeFakeRepo write .git file %s: %v", gitPath, err)
		}
	}
}

// ── Dedup test ────────────────────────────────────────────────────────────────

// TestProjects_Dedup verifies that when both a main repo dir and an in-tree
// linked-worktree dir are candidates (both FakeRunner responses report the same
// entry[0] main path), the result contains ONE ProjectTrees for that main path.
//
// Layout under TempDir:
//
//	projA/          <- normal repo (.git dir) → Scan candidate
//	projA/wt/       <- linked worktree (.git file) → Scan candidate
//
// Both porcelain blobs list projA as entry[0]; projA/wt appears as a linked
// worktree in both. First-writer wins so we get projA's result.
func TestProjects_Dedup(t *testing.T) {
	root := t.TempDir()
	projA := filepath.Join(root, "projA")
	wtDir := filepath.Join(projA, "wt")

	makeFakeRepo(t, projA, true)  // .git dir
	makeFakeRepo(t, wtDir, false) // .git file

	mainBlob := porcelainMainPlusLinked(projA, "main", wtDir, "feat")

	r := proc.NewFakeRunner()
	// Both candidates report projA as main.
	r.Respond(proc.FakeResult{Stdout: mainBlob}, "git", "-C", projA, "worktree", "list", "--porcelain")
	r.Respond(proc.FakeResult{Stdout: mainBlob}, "git", "-C", wtDir, "worktree", "list", "--porcelain")

	results, err := Projects(context.Background(), r, root, Options{}, map[string]state.ProjectStat{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 ProjectTrees (dedup), got %d", len(results))
	}
	if results[0].Project.Path != projA {
		t.Errorf("Project.Path = %q, want %q", results[0].Project.Path, projA)
	}
	// Should have 2 trees: main + linked worktree (bare skipped).
	if len(results[0].Trees) != 2 {
		t.Errorf("want 2 trees, got %d", len(results[0].Trees))
	}
}

// ── Cold-start alphabetical ordering ─────────────────────────────────────────

// TestProjects_ColdStartAlphabetical verifies that with an empty stats map,
// results are returned in alphabetical order by project path.
func TestProjects_ColdStartAlphabetical(t *testing.T) {
	root := t.TempDir()

	// Create siblings with names that have a clear alphabetical order.
	alpha := filepath.Join(root, "alpha")
	bravo := filepath.Join(root, "bravo")
	charlie := filepath.Join(root, "charlie")

	for _, d := range []string{alpha, bravo, charlie} {
		makeFakeRepo(t, d, true)
	}

	r := proc.NewFakeRunner()
	for _, d := range []string{alpha, bravo, charlie} {
		r.Respond(proc.FakeResult{Stdout: porcelainForPath(d, "main")},
			"git", "-C", d, "worktree", "list", "--porcelain")
	}

	results, err := Projects(context.Background(), r, root, Options{}, map[string]state.ProjectStat{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	want := []string{alpha, bravo, charlie}
	for i, w := range want {
		if results[i].Project.Path != w {
			t.Errorf("results[%d].Project.Path = %q, want %q", i, results[i].Project.Path, w)
		}
	}
}

// ── Frecency ordering ─────────────────────────────────────────────────────────

// TestProjects_FrecencyOrdering verifies that a repo with high rank + recent
// LastAccessed sorts first, with the others in alphabetical order after.
func TestProjects_FrecencyOrdering(t *testing.T) {
	root := t.TempDir()
	now := int64(1_000_000)

	alpha := filepath.Join(root, "alpha")
	bravo := filepath.Join(root, "bravo")
	charlie := filepath.Join(root, "charlie")

	for _, d := range []string{alpha, bravo, charlie} {
		makeFakeRepo(t, d, true)
	}

	r := proc.NewFakeRunner()
	for _, d := range []string{alpha, bravo, charlie} {
		r.Respond(proc.FakeResult{Stdout: porcelainForPath(d, "main")},
			"git", "-C", d, "worktree", "list", "--porcelain")
	}

	// Give bravo a high rank accessed right now → it scores highest.
	stats := map[string]state.ProjectStat{
		bravo: {Rank: 100, LastAccessed: now},
	}

	results, err := Projects(context.Background(), r, root, Options{}, stats, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	// bravo first, then alpha, charlie (alphabetical for zero-score entries).
	wantOrder := []string{bravo, alpha, charlie}
	for i, w := range wantOrder {
		if results[i].Project.Path != w {
			t.Errorf("results[%d].Project.Path = %q, want %q", i, results[i].Project.Path, w)
		}
	}
}

// ── Trees attached correctly ──────────────────────────────────────────────────

// TestProjects_TreesAttached verifies that:
//   - Tree.IsMain is set on the main checkout only.
//   - Tree.Project points to the right Project (matched by path).
//   - A bare entry produces no Tree.
func TestProjects_TreesAttached(t *testing.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "repo")
	linkedPath := filepath.Join(root, "linked-wt")

	makeFakeRepo(t, mainPath, true)
	makeFakeRepo(t, linkedPath, false)

	blob := porcelainMainPlusLinked(mainPath, "main", linkedPath, "feat")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: blob}, "git", "-C", mainPath, "worktree", "list", "--porcelain")
	r.Respond(proc.FakeResult{Stdout: blob}, "git", "-C", linkedPath, "worktree", "list", "--porcelain")

	results, err := Projects(context.Background(), r, root, Options{}, map[string]state.ProjectStat{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 project, got %d", len(results))
	}
	pt := results[0]

	if len(pt.Trees) != 2 {
		t.Fatalf("want 2 trees (main + linked, bare skipped), got %d", len(pt.Trees))
	}

	// Find main and linked trees by IsMain flag.
	var mainTree, linkedTree *model.Tree
	for i := range pt.Trees {
		tr := &pt.Trees[i]
		if tr.IsMain {
			mainTree = tr
		} else {
			linkedTree = tr
		}
	}
	if mainTree == nil {
		t.Fatal("no tree has IsMain=true")
	}
	if linkedTree == nil {
		t.Fatal("no tree has IsMain=false")
	}
	if mainTree.Path != mainPath {
		t.Errorf("mainTree.Path = %q, want %q", mainTree.Path, mainPath)
	}
	if linkedTree.Path != linkedPath {
		t.Errorf("linkedTree.Path = %q, want %q", linkedTree.Path, linkedPath)
	}
	// Project pointer must point to the stored project (same path).
	for i, tr := range pt.Trees {
		if tr.Project == nil {
			t.Errorf("trees[%d].Project is nil", i)
			continue
		}
		if tr.Project.Path != mainPath {
			t.Errorf("trees[%d].Project.Path = %q, want %q", i, tr.Project.Path, mainPath)
		}
	}

	// Pointer-identity invariant: every Tree.Project must point at the
	// element's own Project field — not a stale copy from the build loop.
	if pt.Trees[0].Project != &pt.Project {
		t.Errorf("Tree.Project must point at the element's own Project (shared identity)")
	}
}

// ── Skip-and-continue on runner error ────────────────────────────────────────

// TestProjects_SkipOnRunnerError verifies that when one candidate's
// ListWorktrees call fails (runner error), that candidate is silently skipped
// and the function returns the remaining projects without error.
func TestProjects_SkipOnRunnerError(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good")
	bad := filepath.Join(root, "bad")

	makeFakeRepo(t, good, true)
	makeFakeRepo(t, bad, true)

	r := proc.NewFakeRunner()
	// "good" has a valid response.
	r.Respond(proc.FakeResult{Stdout: porcelainForPath(good, "main")},
		"git", "-C", good, "worktree", "list", "--porcelain")
	// "bad" has an error response.
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: not a git repository"),
		Err:    errors.New("exit status 128"),
	}, "git", "-C", bad, "worktree", "list", "--porcelain")

	results, err := Projects(context.Background(), r, root, Options{}, map[string]state.ProjectStat{}, 0)
	if err != nil {
		t.Fatalf("Projects returned error %v; want nil (skip-and-continue)", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result (bad skipped), got %d", len(results))
	}
	if results[0].Project.Path != good {
		t.Errorf("Project.Path = %q, want %q", results[0].Project.Path, good)
	}
}
