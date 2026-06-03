// internal/fs/readwrite_test.go
package fs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/fs"
)

func TestReadWriteRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")

	if err := fs.WriteFile(path, []byte("hello world")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestWriteFile_ModePreservedOnOverwrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")

	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := fs.WriteFile(path, []byte("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatalf("WriteFile overwrite: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "#!/bin/sh\necho hi\n" {
		t.Errorf("content mismatch: %q", data)
	}
}

func TestWriteFile_NewFileDefaultMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	if err := fs.WriteFile(path, []byte("new")); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() == 0 {
		t.Errorf("new file has mode 0")
	}
}
