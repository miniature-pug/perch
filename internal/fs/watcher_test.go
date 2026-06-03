// internal/fs/watcher_test.go
package fs_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/fs"
)

func TestWatcher_FileCreateFiresOnChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	fired := make(chan string, 4)
	w, err := fs.Watch(root, func(absPath string) {
		fired <- absPath
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()

	newFile := filepath.Join(root, "created.txt")
	if err := os.WriteFile(newFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-fired:
		if filepath.Dir(got) != root && got != newFile {
			t.Logf("onChange fired with path: %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: onChange not fired after file create")
	}
}

func TestWatcher_Close(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	w, err := fs.Watch(root, func(string) {})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	_ = w.Close()
}

func TestShouldExclude(t *testing.T) {
	if !fs.ShouldExclude(".git", nil) {
		t.Error(".git should always be excluded")
	}
	if !fs.ShouldExclude("node_modules", []string{"node_modules"}) {
		t.Error("node_modules should be excluded when in gitignore patterns")
	}
	if fs.ShouldExclude("src", []string{"node_modules"}) {
		t.Error("src should not be excluded")
	}
}

func TestWatcher_NestedFileFiresOnChange(t *testing.T) {
	root := t.TempDir()

	ch := make(chan string, 16)
	w, err := fs.Watch(root, func(absPath string) {
		ch <- absPath
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()

	subDir := filepath.Join(root, "sub")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Allow time for the Create event to trigger fw.Add on the new subdir.
	time.Sleep(80 * time.Millisecond)

	nestedFile := filepath.Join(subDir, "f.txt")
	if err := os.WriteFile(nestedFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case p := <-ch:
			if filepath.Base(p) == "f.txt" && strings.Contains(p, "sub") {
				return // success
			}
		case <-deadline:
			t.Fatal("timeout: onChange not fired for nested file sub/f.txt")
		}
	}
}

func TestWatcher_GitDirExcluded(t *testing.T) {
	root := t.TempDir()

	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var delivered []string
	var mu sync.Mutex
	w, err := fs.Watch(root, func(absPath string) {
		mu.Lock()
		delivered = append(delivered, absPath)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Write into .git — should not be watched.
	if err := os.WriteFile(filepath.Join(gitDir, "x"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for _, p := range delivered {
		if strings.Contains(p, "/.git/") || strings.HasSuffix(p, "/.git") {
			t.Errorf("onChange fired for .git path: %s", p)
		}
	}
}
