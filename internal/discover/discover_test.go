package discover

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// makeDir creates a directory (and any parents) inside base.
func makeDir(t *testing.T, base string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{base}, parts...)...)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("makeDir %s: %v", path, err)
	}
	return path
}

// makeGitDir places a ".git" directory inside dir, marking it as a normal repo.
func makeGitDir(t *testing.T, dir string) {
	t.Helper()
	makeDir(t, dir, ".git")
}

// makeGitFile places a ".git" regular file inside dir, simulating a linked
// worktree or submodule.
func makeGitFile(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, ".git")
	if err := os.WriteFile(p, []byte("gitdir: /somewhere\n"), 0o644); err != nil {
		t.Fatalf("makeGitFile %s: %v", p, err)
	}
}

// sortedScan runs Scan and returns a sorted slice of results.
func sortedScan(t *testing.T, root string, opts Options) ([]string, error) {
	t.Helper()
	got, err := Scan(root, opts)
	if got != nil {
		sort.Strings(got)
	}
	return got, err
}

// mustScan runs Scan and fails the test on error.
func mustScan(t *testing.T, root string, opts Options) []string {
	t.Helper()
	got, err := sortedScan(t, root, opts)
	if err != nil {
		t.Fatalf("Scan(%q): unexpected error: %v", root, err)
	}
	return got
}

// assertPaths fails the test if got and want do not contain the same paths.
// Both slices are sorted before comparison so ordering is irrelevant.
func assertPaths(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)

	// Treat nil and empty as equivalent.
	if len(got) == 0 && len(want) == 0 {
		return
	}

	if len(got) != len(want) {
		t.Errorf("path count: got %d, want %d\n  got:  %v\n  want: %v", len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("path[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestMaxDepthBoundary verifies the exact off-by-one pin from the project's
// discover test matrix:
//
//	A repo whose ".git" sits 3 levels below root (root/a/b/c/.git):
//	  - MaxDepth:2 → NOT found   (depth 3 > 2)
//	  - MaxDepth:3 → found       (depth 3 <= 3)
func TestMaxDepthBoundary(t *testing.T) {
	root := t.TempDir()
	// root/a/b/c is at depth 3 relative to root.
	deep := makeDir(t, root, "a", "b", "c")
	makeGitDir(t, deep)

	t.Run("below_limit_not_found", func(t *testing.T) {
		got := mustScan(t, root, Options{MaxDepth: 2})
		assertPaths(t, got, nil)
	})

	t.Run("at_limit_found", func(t *testing.T) {
		got := mustScan(t, root, Options{MaxDepth: 3})
		assertPaths(t, got, []string{deep})
	})
}

// TestPruneNodeModules verifies that a repo nested inside node_modules is never
// returned because node_modules is in DefaultPrune.
func TestPruneNodeModules(t *testing.T) {
	root := t.TempDir()
	nm := makeDir(t, root, "node_modules", "some-pkg")
	makeGitDir(t, nm)

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, nil)
}

// TestPruneVendor verifies that a repo nested inside vendor is never returned.
func TestPruneVendor(t *testing.T) {
	root := t.TempDir()
	v := makeDir(t, root, "vendor", "sub")
	makeGitDir(t, v)

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, nil)
}

// TestGitAsFile verifies that a directory containing a ".git" regular file
// (linked-worktree / submodule shape) is treated as a repo candidate.
func TestGitAsFile(t *testing.T) {
	root := t.TempDir()
	wt := makeDir(t, root, "worktree")
	makeGitFile(t, wt)

	got := mustScan(t, root, Options{MaxDepth: 3})
	assertPaths(t, got, []string{wt})
}

// TestNonGitTree verifies that a directory tree with no ".git" anywhere
// produces an empty result without error.
func TestNonGitTree(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "proj", "src")
	makeDir(t, root, "docs")

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, nil)
}

// TestNestedRepos verifies that both an outer repo and an inner repo nested
// inside its working tree are both returned.
func TestNestedRepos(t *testing.T) {
	root := t.TempDir()
	outer := makeDir(t, root, "outer")
	makeGitDir(t, outer)
	inner := makeDir(t, root, "outer", "sub", "inner")
	makeGitDir(t, inner)

	// Default MaxDepth (0→8) covers depth 4 easily.
	got := mustScan(t, root, Options{})
	assertPaths(t, got, []string{outer, inner})
}

// TestUnreadableSubdir verifies that a permission error on one subtree does
// not abort the whole scan and that a findable repo elsewhere is still returned.
// Skipped when running as root because root bypasses filesystem permissions.
func TestUnreadableSubdir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod-0 does not restrict access, skipping")
	}

	root := t.TempDir()

	// A directory we will make unreadable.
	blocked := makeDir(t, root, "blocked")
	// Put something inside so there would be entries to find if we could read it.
	makeDir(t, blocked, "inner")

	// A findable repo in a sibling directory.
	good := makeDir(t, root, "good")
	makeGitDir(t, good)

	// Make blocked unreadable. Restore permissions so t.TempDir cleanup succeeds.
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatalf("chmod blocked: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blocked, 0o755)
	})

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, []string{good})
}

// TestHiddenDirSkip verifies that hidden (dot-prefixed) directories are never
// descended into during a scan, so tool/config caches like ~/.pyenv or ~/.npm
// are never returned as repo candidates.
//
//   - A normal repo (proj/.git) IS returned.
//   - A top-level hidden dir containing a .git (.pyenv/.git) is NOT returned.
//   - A repo nested inside a hidden dir (.cache/inner/.git) is NOT returned
//     because the walk never descends into the hidden directory.
func TestHiddenDirSkip(t *testing.T) {
	root := t.TempDir()

	// Normal visible repo — must be found.
	proj := makeDir(t, root, "proj")
	makeGitDir(t, proj)

	// Hidden dir that looks like a pyenv install — must NOT be found.
	pyenv := makeDir(t, root, ".pyenv")
	makeGitDir(t, pyenv)

	// Repo nested inside a hidden cache dir — must NOT be found.
	cacheInner := makeDir(t, root, ".cache", "inner")
	makeGitDir(t, cacheInner)

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, []string{proj})
}

// TestRootNotExist verifies that Scan returns an error (not a silent empty
// result) when the root directory does not exist.
func TestRootNotExist(t *testing.T) {
	_, err := Scan("/nonexistent/path/that/will/not/exist/perch_test", Options{MaxDepth: 2})
	if err == nil {
		t.Fatal("expected error for nonexistent root, got nil")
	}
}
