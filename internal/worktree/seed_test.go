package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/config"
)

// makeRepo builds a minimal temporary repo directory containing the specified
// files with the given content and mode. Returns the repo root.
func makeRepo(t *testing.T, entries map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range entries {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdirall: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func TestSeed_CopyFile(t *testing.T) {
	repo := makeRepo(t, map[string]string{".env": "SECRET=1\n"})
	tree := t.TempDir()

	err := Seed(repo, tree, config.Files{Copy: []string{".env"}})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(tree, ".env"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != "SECRET=1\n" {
		t.Errorf("content mismatch: got %q", string(got))
	}
}

func TestSeed_CopyGlobMultiple(t *testing.T) {
	repo := makeRepo(t, map[string]string{
		".env":       "A=1\n",
		".env.local": "A=local\n",
	})
	tree := t.TempDir()

	err := Seed(repo, tree, config.Files{Copy: []string{".env*"}})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	for _, name := range []string{".env", ".env.local"} {
		if _, err := os.Stat(filepath.Join(tree, name)); err != nil {
			t.Errorf("expected %s to be copied: %v", name, err)
		}
	}
}

func TestSeed_CopyPreservesMode(t *testing.T) {
	repo := t.TempDir()
	src := filepath.Join(repo, "run.sh")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()

	if err := Seed(repo, tree, config.Files{Copy: []string{"run.sh"}}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	info, err := os.Stat(filepath.Join(tree, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode: got %o, want 0755", info.Mode().Perm())
	}
}

func TestSeed_CopyDirectory(t *testing.T) {
	// Verify that a glob matching a directory walks and copies regular files.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "configs", "a.yaml"), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "configs", "b.yaml"), []byte("b: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()

	if err := Seed(repo, tree, config.Files{Copy: []string{"configs"}}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	for _, name := range []string{"configs/a.yaml", "configs/b.yaml"} {
		if _, err := os.Stat(filepath.Join(tree, name)); err != nil {
			t.Errorf("expected %s copied: %v", name, err)
		}
	}
}

func TestSeed_Symlink(t *testing.T) {
	repo := t.TempDir()
	// Create a node_modules directory in the repo.
	if err := os.MkdirAll(filepath.Join(repo, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "node_modules", "pkg.js"), []byte("// pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()

	if err := Seed(repo, tree, config.Files{Symlink: []string{"node_modules"}}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	linkPath := filepath.Join(tree, "node_modules")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	// The target must be relative (not absolute) so the symlink survives a move.
	if filepath.IsAbs(target) {
		t.Errorf("symlink target is absolute: %q; want relative", target)
	}

	// Resolving the symlink must reach the real node_modules.
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	want := filepath.Join(repo, "node_modules")
	wantResolved, _ := filepath.EvalSymlinks(want)
	if resolved != wantResolved {
		t.Errorf("resolved symlink = %q; want %q", resolved, wantResolved)
	}
}

func TestSeed_Security_EscapingSymlinkMatch(t *testing.T) {
	// Build a repo containing a symlink "evil" -> /tmp (outside the repo).
	// Seed with Symlink:[evil] must return a non-nil error and perform NO
	// filesystem mutation in treePath.
	repo := t.TempDir()
	evilLink := filepath.Join(repo, "evil")
	// Point to /tmp — guaranteed to exist and be outside repo.
	if err := os.Symlink("/tmp", evilLink); err != nil {
		t.Fatalf("create evil symlink: %v", err)
	}

	tree := t.TempDir()

	err := Seed(repo, tree, config.Files{Symlink: []string{"evil"}})
	if err == nil {
		t.Fatal("Seed: expected error for escaping symlink match, got nil")
	}

	// treePath must be completely empty — no mutation was performed.
	entries, readErr := os.ReadDir(tree)
	if readErr != nil {
		t.Fatalf("ReadDir treePath: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("treePath is not empty after security rejection: %v", entries)
	}
}

func TestSeed_Security_EscapingCopyMatchAbortsMutation(t *testing.T) {
	// Build a repo with one safe file and one evil symlink in Copy list.
	// The evil entry must abort BEFORE any copy, leaving treePath empty.
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "safe.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	evilLink := filepath.Join(repo, "evil")
	if err := os.Symlink("/tmp", evilLink); err != nil {
		t.Fatalf("create evil symlink: %v", err)
	}

	tree := t.TempDir()

	// safe.txt appears first but evil also resolves outside — both are in Copy.
	err := Seed(repo, tree, config.Files{Copy: []string{"safe.txt", "evil"}})
	if err == nil {
		t.Fatal("Seed: expected error, got nil")
	}

	entries, _ := os.ReadDir(tree)
	if len(entries) != 0 {
		t.Errorf("treePath is not empty after security rejection: %v", entries)
	}
}

func TestSeed_EmptyGlobSkipped(t *testing.T) {
	// A glob with no matches is silently skipped — not an error.
	repo := makeRepo(t, map[string]string{"real.txt": "x"})
	tree := t.TempDir()

	err := Seed(repo, tree, config.Files{Copy: []string{"no-such-file-*.xyz"}})
	if err != nil {
		t.Fatalf("Seed: expected nil for empty glob, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FIX 1 — TOCTOU: copy via validated resolved path
// ---------------------------------------------------------------------------

// TestSeed_CopyInRepoSymlinkFollowsResolvedPath verifies that when a files.copy
// glob matches a symlink that points to a real in-repo file, Seed copies the
// real file's CONTENT into treePath. This proves that pass 2 operates on the
// validated resolved path (not the symlink itself), and the destination is
// named after the original match.
func TestSeed_CopyInRepoSymlinkFollowsResolvedPath(t *testing.T) {
	repo := t.TempDir()

	// Create the real file inside the repo.
	realFile := filepath.Join(repo, "real.txt")
	if err := os.WriteFile(realFile, []byte("real-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an in-repo symlink that points to the real file.
	linkFile := filepath.Join(repo, "link.txt")
	if err := os.Symlink(realFile, linkFile); err != nil {
		t.Fatalf("create in-repo symlink: %v", err)
	}

	tree := t.TempDir()

	// Seed with the symlink name in files.copy.
	if err := Seed(repo, tree, config.Files{Copy: []string{"link.txt"}}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// The destination must be named after the match ("link.txt"), not "real.txt".
	dst := filepath.Join(tree, "link.txt")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read copied destination: %v", err)
	}
	// Content must be the real file's content, proving we followed the resolved path.
	if string(got) != "real-content\n" {
		t.Errorf("content = %q; want %q", string(got), "real-content\n")
	}
	// The destination itself must be a regular file, not a symlink.
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("destination is a symlink; Seed should have copied the real file")
	}
}

// ---------------------------------------------------------------------------
// FIX 2 — coverage: error-injection tests
// ---------------------------------------------------------------------------

// TestExpandGlobs_MalformedPattern verifies that a syntactically invalid glob
// pattern causes expandGlobs (and thus Seed) to return an error.
func TestExpandGlobs_MalformedPattern(t *testing.T) {
	repo := makeRepo(t, map[string]string{"a.txt": "a"})
	tree := t.TempDir()

	// "[" is an unclosed bracket — filepath.Glob returns ErrBadPattern.
	err := Seed(repo, tree, config.Files{Copy: []string{"["}})
	if err == nil {
		t.Fatal("Seed: expected error for malformed glob, got nil")
	}
}

// TestSeed_BrokenSymlinkMatch verifies that a files.copy or files.symlink
// entry that is a dangling symlink (target missing) causes Seed to return an
// error and perform no mutation.
func TestSeed_BrokenSymlinkMatch(t *testing.T) {
	repo := t.TempDir()

	// Create a dangling symlink: target does not exist.
	danglingLink := filepath.Join(repo, "dangling")
	if err := os.Symlink(filepath.Join(repo, "nonexistent"), danglingLink); err != nil {
		t.Fatalf("create dangling symlink: %v", err)
	}

	tree := t.TempDir()

	err := Seed(repo, tree, config.Files{Copy: []string{"dangling"}})
	if err == nil {
		t.Fatal("Seed: expected error for dangling symlink, got nil")
	}

	// No mutation must have occurred.
	entries, _ := os.ReadDir(tree)
	if len(entries) != 0 {
		t.Errorf("treePath is not empty after dangling symlink error: %v", entries)
	}
}

// TestCopyFile_UnreadableSource verifies that copyFile returns an error when
// the source file has mode 0o000 (unreadable).
func TestCopyFile_UnreadableSource(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: 0o000 permission check is bypassed")
	}
	src := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o644) })

	dst := filepath.Join(t.TempDir(), "out")
	err := copyFile(src, dst)
	if err == nil {
		t.Fatal("copyFile: expected error for unreadable source, got nil")
	}
}

// TestCopyFile_UnwritableDestParent verifies that copyFile returns an error
// when it cannot create the destination's parent directory (parent is 0o000).
func TestCopyFile_UnwritableDestParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: 0o000 permission check is bypassed")
	}
	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Make the destination parent directory unwritable.
	readOnlyDir := t.TempDir()
	if err := os.Chmod(readOnlyDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o755) })

	dst := filepath.Join(readOnlyDir, "subdir", "out.txt")
	err := copyFile(src, dst)
	if err == nil {
		t.Fatal("copyFile: expected error for unwritable dest parent, got nil")
	}
}

// TestInsideRepo_Boundaries exercises the exact-root, exact-parent, and
// prefix-sibling cases of insideRepo that guard the rel != ".." branch.
func TestInsideRepo_Boundaries(t *testing.T) {
	repoRoot := "/data/myrepo"

	cases := []struct {
		name string
		abs  string
		want bool
	}{
		{"exact root", repoRoot, true},
		{"child", repoRoot + "/subdir/file.txt", true},
		{"exact parent (rel=..)", filepath.Dir(repoRoot), false},
		{"prefix sibling (rel=../myreopfoo or similar)", repoRoot + "foo", false},
		{"grandparent", filepath.Dir(filepath.Dir(repoRoot)), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := insideRepo(repoRoot, tc.abs)
			if err != nil {
				t.Fatalf("insideRepo(%q, %q): unexpected error: %v", repoRoot, tc.abs, err)
			}
			if got != tc.want {
				t.Errorf("insideRepo(%q, %q) = %v; want %v", repoRoot, tc.abs, got, tc.want)
			}
		})
	}
}

// TestInsideRepo_PrefixSiblingIsRejected proves specifically that a path that
// shares the repo root as a string prefix but is not inside it is rejected.
// This is the load-bearing case: without the rel != ".." guard, a sibling like
// "/data/myrepofoo" might slip past HasPrefix("../") on some platforms.
func TestInsideRepo_PrefixSiblingIsRejected(t *testing.T) {
	repoRoot := "/data/myrepo"
	sibling := repoRoot + "sibling" // /data/myreposibling — a directory that shares prefix

	ok, err := insideRepo(repoRoot, sibling)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("insideRepo accepted prefix-sibling %q as inside %q", sibling, repoRoot)
	}
}

// TestSeed_BadRepoRoot verifies that an invalid/nonexistent repoRoot causes
// Seed to return an error on EvalSymlinks.
func TestSeed_BadRepoRoot(t *testing.T) {
	err := Seed("/nonexistent/repo/root", t.TempDir(), config.Files{})
	if err == nil {
		t.Fatal("Seed: expected error for nonexistent repoRoot, got nil")
	}
	if !strings.Contains(err.Error(), "resolve repoRoot") {
		t.Errorf("error should mention resolve repoRoot; got: %v", err)
	}
}
