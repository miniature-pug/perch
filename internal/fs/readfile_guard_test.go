// internal/fs/readfile_guard_test.go
package fs_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/fs"
)

func mkfifo(t *testing.T, path string) error {
	t.Helper()
	return syscall.Mkfifo(path, 0o600)
}

func TestReadFile_RegularFileOK(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestReadFile_OversizedRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	// One byte over the cap (sparse; no real allocation).
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(fs.MaxReadFileBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	_, err = fs.ReadFile(path)
	if err == nil {
		t.Fatal("ReadFile must reject a file larger than the cap")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v, want a 'too large' message", err)
	}
}

func TestReadFile_AtCapOK(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "atcap.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(fs.MaxReadFileBytes); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	got, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile at exactly the cap must succeed: %v", err)
	}
	if int64(len(got)) != fs.MaxReadFileBytes {
		t.Errorf("read %d bytes, want %d", len(got), fs.MaxReadFileBytes)
	}
}

// TestReadFile_FIFORejectedWithoutHanging proves ReadFile refuses a named pipe
// instead of blocking forever on the open/read. The whole test is guarded by a
// timeout goroutine so a regression (removal of the special-file guard) fails
// CI rather than wedging it: reading a FIFO with no writer blocks indefinitely.
func TestReadFile_FIFORejectedWithoutHanging(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := mkfifo(t, fifo); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := fs.ReadFile(fifo)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ReadFile must reject a FIFO")
		}
		if !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("error = %v, want 'not a regular file'", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFile hung on a FIFO — special-file guard missing")
	}
}
