// internal/fs/watcher_test.go
package fs_test

import (
	"os"
	"path/filepath"
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
	defer w.Close()

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
