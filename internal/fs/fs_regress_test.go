// internal/fs/fs_regress_test.go holds regressions for the fs audit
// findings GFS-8, GFS-9, GFS-12, GFS-13, GFS-28 and GFS-29.
package fs_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/fs"
)

// waitFor polls cond until it holds or the timeout passes.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// GFS-8: directories created in one burst (mkdir -p) are all watched, and
// files already inside them are reported.
func TestWatcher_NestedMkdirBurstIsWatched(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	seen := map[string]bool{}
	w, err := fs.Watch(root, func(p string) {
		mu.Lock()
		seen[p] = true
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// Give the watcher time to process the burst and add its watches.
	time.Sleep(300 * time.Millisecond)

	target := filepath.Join(deep, "x.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 3*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return seen[target] }) {
		t.Fatal("a write inside a directory created by mkdir -p was not observed")
	}
}

// GFS-8: files that already exist when their directory is first seen are
// reported (they were created before any watch could see them).
func TestWatcher_ReportsFilesInMovedInDirectory(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staging, "pkg", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "pkg", "sub", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	w, err := fs.Watch(root, func(p string) {
		mu.Lock()
		seen[p] = true
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if err := os.Rename(filepath.Join(staging, "pkg"), filepath.Join(root, "pkg")); err != nil {
		t.Skipf("rename across temp dirs unsupported here: %v", err)
	}
	want := filepath.Join(root, "pkg", "sub", "f.txt")
	if !waitFor(t, 3*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return seen[want] }) {
		t.Fatal("file inside a moved-in directory tree was not reported")
	}
}

// GFS-9: anchored, path and negated .gitignore patterns.
func TestShouldExclude_GitignoreSemantics(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		want     bool
	}{
		{"node_modules", []string{"/node_modules"}, true},
		{"build", []string{"/build/"}, true},
		{"src/gen", []string{"src/gen"}, true},
		{"lib/gen", []string{"src/gen"}, false},
		{"pkg/node_modules", []string{"/node_modules"}, false},
		{"pkg/node_modules", []string{"node_modules"}, true},
		{"keep", []string{"*", "!keep"}, false},
		{"other", []string{"*", "!keep"}, true},
		{"a/b/deep", []string{"**/deep"}, true},
	}
	for _, c := range cases {
		if got := fs.ShouldExclude(c.name, c.patterns); got != c.want {
			t.Errorf("ShouldExclude(%q, %q) = %v, want %v", c.name, c.patterns, got, c.want)
		}
	}
}

// GFS-9: the watcher does not descend into a directory excluded by an
// anchored pattern.
func TestWatcher_AnchoredGitignoreExcludesDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/node_modules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nm := filepath.Join(root, "node_modules", "dep")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []string
	w, err := fs.Watch(root, func(p string) {
		mu.Lock()
		seen = append(seen, p)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if err := os.WriteFile(filepath.Join(nm, "x.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, p := range seen {
		if strings.Contains(p, "node_modules") {
			t.Errorf("watcher reported %s inside an ignored directory", p)
		}
	}
}

// GFS-12: WriteFile through a symlink updates the target and keeps the link.
func TestWriteFile_ThroughSymlink(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "AGENTS.md")
	link := filepath.Join(d, "CLAUDE.md")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", link); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	li, err := os.Lstat(link)
	if err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("CLAUDE.md is no longer a symlink (err=%v)", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("AGENTS.md = %q, want %q", got, "new")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Errorf("target mode = %v, want 0600 preserved", fi.Mode().Perm())
	}
}

// GFS-12: a dangling symlink is refused rather than replaced.
func TestWriteFile_DanglingSymlinkRefused(t *testing.T) {
	d := t.TempDir()
	link := filepath.Join(d, "dangling")
	if err := os.Symlink("missing", link); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile(link, []byte("x")); err == nil {
		t.Fatal("WriteFile through a dangling symlink should fail")
	}
	if li, _ := os.Lstat(link); li.Mode()&os.ModeSymlink == 0 {
		t.Error("dangling symlink was replaced")
	}
}

// GFS-13: git status marks survive a symlinked directory path, names with
// spaces, and changes nested below a listed directory.
func TestListDir_GitStatusEdgeCases(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	realDir := t.TempDir()
	gitRun(t, realDir, "init", "-q")
	if err := os.MkdirAll(filepath.Join(realDir, "src", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/a b.txt", "src/deep/c.txt", "src/é.txt"} {
		if err := os.WriteFile(filepath.Join(realDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, realDir, "add", ".")
	gitRun(t, realDir, "commit", "-qm", "i")
	for _, f := range []string{"src/a b.txt", "src/deep/c.txt", "src/é.txt"} {
		if err := os.WriteFile(filepath.Join(realDir, f), []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(realDir, "untracked.txt"), []byte("u"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	mark := func(dir string) map[string]fs.Node {
		nodes, err := fs.ListDir(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]fs.Node{}
		for _, n := range nodes {
			m[n.Name] = n
		}
		return m
	}

	rootNodes := mark(link)
	if !rootNodes["untracked.txt"].Untracked {
		t.Error("untracked.txt not marked when listed through a symlinked root")
	}
	if !rootNodes["src"].Modified {
		t.Error("src not marked although files below it are modified")
	}
	srcNodes := mark(filepath.Join(realDir, "src"))
	for _, name := range []string{"a b.txt", "é.txt", "deep"} {
		if !srcNodes[name].Modified {
			t.Errorf("%q: Modified = false, want true", name)
		}
	}
}

// GFS-28: binary or non-UTF-8 content is refused instead of being mangled.
func TestReadFile_RejectsBinary(t *testing.T) {
	d := t.TempDir()
	for name, data := range map[string][]byte{
		"nul.bin":    {'a', 0, 'b'},
		"latin1.txt": {'c', 'a', 'f', 0xe9},
	} {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := fs.ReadFile(p); !errors.Is(err, fs.ErrBinaryFile) {
			t.Errorf("ReadFile(%s) err = %v, want ErrBinaryFile", name, err)
		}
	}
	p := filepath.Join(d, "utf8.txt")
	if err := os.WriteFile(p, []byte("café ✓\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(p); err != nil {
		t.Errorf("ReadFile(utf8.txt): %v", err)
	}
}

// GFS-29: a symlink to a directory is listed as a directory.
func TestListDir_SymlinkToDirIsDir(t *testing.T) {
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(d, "linked")); err != nil {
		t.Fatal(err)
	}
	nodes, err := fs.ListDir(d, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Name == "linked" && !n.IsDir {
			t.Error("symlink to a directory listed as a file")
		}
	}
}
