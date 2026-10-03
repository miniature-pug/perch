package discover

import (
	"context"
	"errors"
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

// assertPaths fails the test if got and want do not hold the same paths.
// assertPaths sorts both slices before comparing them, so order does not matter.
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

// TestMaxDepthBoundary verifies the exact off-by-one depth boundary:
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

// TestPruneNodeModules verifies that Scan never returns a repo nested inside
// node_modules, because node_modules is in DefaultPrune.
func TestPruneNodeModules(t *testing.T) {
	root := t.TempDir()
	nm := makeDir(t, root, "node_modules", "some-pkg")
	makeGitDir(t, nm)

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, nil)
}

// TestPruneVendor verifies that Scan never returns a repo nested inside vendor.
func TestPruneVendor(t *testing.T) {
	root := t.TempDir()
	v := makeDir(t, root, "vendor", "sub")
	makeGitDir(t, v)

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, nil)
}

// TestGitAsFile verifies that Scan treats a directory that contains a ".git"
// regular file (a linked-worktree or submodule shape) as a repo candidate.
func TestGitAsFile(t *testing.T) {
	root := t.TempDir()
	wt := makeDir(t, root, "worktree")
	makeGitFile(t, wt)

	got := mustScan(t, root, Options{MaxDepth: 3})
	assertPaths(t, got, []string{wt})
}

// TestNonGitTree verifies that Scan returns an empty result without error
// for a directory tree with no ".git" anywhere.
func TestNonGitTree(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "proj", "src")
	makeDir(t, root, "docs")

	got := mustScan(t, root, Options{MaxDepth: 5})
	assertPaths(t, got, nil)
}

// TestNestedRepos verifies that Scan returns both an outer repo and an
// inner repo nested inside the outer repo's worktree.
func TestNestedRepos(t *testing.T) {
	root := t.TempDir()
	outer := makeDir(t, root, "outer")
	makeGitDir(t, outer)
	inner := makeDir(t, root, "outer", "sub", "inner")
	makeGitDir(t, inner)

	// MaxDepth 0 defaults to 8, which easily covers depth 4.
	got := mustScan(t, root, Options{})
	assertPaths(t, got, []string{outer, inner})
}

// TestUnreadableSubdir verifies that a permission error on one subtree does
// not abort the whole scan, and that Scan still returns a findable repo
// elsewhere. The test skips when running as root, because root bypasses
// filesystem permissions.
func TestUnreadableSubdir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod-0 does not restrict access, skipping")
	}

	root := t.TempDir()

	// A directory that the test makes unreadable.
	blocked := makeDir(t, root, "blocked")
	// Put something inside, so there would be entries to find if the test
	// could read the directory.
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

// TestHiddenDirSkip verifies that Scan never descends into hidden
// (dot-prefixed) directories, so it never returns tool and config caches
// like ~/.pyenv or ~/.npm as repo candidates.
//
//   - Scan returns a normal repo (proj/.git).
//   - Scan does NOT return a top-level hidden dir containing a .git
//     (.pyenv/.git).
//   - Scan does NOT return a repo nested inside a hidden dir
//     (.cache/inner/.git), because the walk never enters the hidden
//     directory.
func TestHiddenDirSkip(t *testing.T) {
	root := t.TempDir()

	// Normal visible repo. Scan must find it.
	proj := makeDir(t, root, "proj")
	makeGitDir(t, proj)

	// Hidden dir that looks like a pyenv install. Scan must not find it.
	pyenv := makeDir(t, root, ".pyenv")
	makeGitDir(t, pyenv)

	// Repo nested inside a hidden cache dir. Scan must not find it.
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

// TestSymlinkedRoot verifies that a root that is itself a symlink is scanned.
func TestSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := makeDir(t, base, "real")
	proj := makeDir(t, real, "proj")
	makeGitDir(t, proj)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	for _, root := range []string{link, link + string(filepath.Separator)} {
		got, err := Scan(root, Options{})
		if err != nil {
			t.Fatalf("Scan(%q): %v", root, err)
		}
		want, _ := filepath.EvalSymlinks(proj)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("Scan(%q) = %v, want [%s]", root, got, want)
		}
	}
}

// TestPruneBuildArtifacts verifies that common build-output directories are skipped.
func TestPruneBuildArtifacts(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"target", "build", "dist", "venv", "__pycache__", "site-packages"} {
		makeGitDir(t, makeDir(t, root, d, "inner"))
	}
	got, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want no repos under pruned dirs, got %v", got)
	}
}

// TestScanContextCancelled verifies that a cancelled context aborts the walk.
func TestScanContextCancelled(t *testing.T) {
	root := t.TempDir()
	makeGitDir(t, makeDir(t, root, "a"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanContext(ctx, root, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
