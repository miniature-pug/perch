// internal/fs/reveal_test.go
package fs_test

import (
	"testing"

	"github.com/miniature-pug/perch/internal/fs"
)

// fakeRevealRunner records the command passed to it without executing anything.
type fakeRevealRunner struct {
	calls [][]string
}

func (f *fakeRevealRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	return nil
}

func TestRevealInFiles_CallsXdgOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeRevealRunner{}
	fs.SetRevealRunner(fake)
	defer fs.SetRevealRunner(nil) // restore default

	if err := fs.RevealInFiles("/home/user/project/file.go"); err != nil {
		t.Fatalf("RevealInFiles: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	cmd := fake.calls[0]
	if cmd[0] != "xdg-open" {
		t.Errorf("expected xdg-open, got %s", cmd[0])
	}
	if cmd[1] != "/home/user/project" {
		t.Errorf("expected dir /home/user/project, got %s", cmd[1])
	}
}
