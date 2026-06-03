// Package pty provides a direct pseudo-terminal bridge per pane (no tmux).
package pty

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	creackpty "github.com/creack/pty"
)

type EmitFunc func(event string, data ...any)

type Bridge struct {
	mu      sync.Mutex
	ptyFile io.WriteCloser
	closer  func() error
	setsize func(cols, rows uint16) error
	writeFn func([]byte) (int, error)
}

func (b *Bridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writeFn != nil {
		return b.writeFn(p)
	}
	if b.ptyFile == nil {
		return 0, os.ErrClosed
	}
	return b.ptyFile.Write(p)
}

// OverrideWriteForTest replaces the pty write target with fn. Test-only; used by
// app tests that capture what OpenWorkspace writes to the shell without a real pty.
func (b *Bridge) OverrideWriteForTest(fn func([]byte) (int, error)) {
	b.writeFn = fn
}

func (b *Bridge) Resize(cols, rows uint16) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.setsize == nil {
		return nil
	}
	return b.setsize(cols, rows)
}

func (b *Bridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closer == nil {
		return nil
	}
	c := b.closer
	b.closer = nil
	b.ptyFile = nil
	return c()
}

const maxChunk = 16 * 1024

// LoginShellArgv returns [$SHELL, "-l"], falling back to ["/bin/bash", "-l"].
func LoginShellArgv() []string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/bash"
	}
	return []string{sh, "-l"}
}

// Spawn starts argv[0] argv[1:] inside a pty in working directory cwd,
// pumping output to emit on `event` as bounded []int chunks (≤ maxChunk).
// No tmux. Closing the returned Bridge kills the process group and reaps it.
func Spawn(ctx context.Context, cwd string, argv []string, event string, emit EmitFunc, cols, rows uint16) (*Bridge, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("pty Spawn: argv must not be empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // caller-controlled input
	cmd.Dir = cwd
	f, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("pty Spawn: start %q: %w", argv[0], err)
	}
	b := &Bridge{
		ptyFile: f,
		setsize: func(c, r uint16) error {
			return creackpty.Setsize(f, &creackpty.Winsize{Cols: c, Rows: r})
		},
		closer: func() error {
			ferr := f.Close()
			if cmd.Process != nil {
				// Kill the whole process group (the shell is a session/group leader via
				// creack/pty's Setsid), so children the shell forked die too. Negative
				// pid targets the group. Fall back to killing just the process.
				if perr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); perr != nil {
					_ = cmd.Process.Kill()
				}
				_, _ = cmd.Process.Wait()
			}
			return ferr
		},
	}
	go pumpReader(f, event, emit, maxChunk)
	return b, nil
}

// NewBridgeForTest returns a Bridge whose only behaviour is to call closer on
// Close. Used by app tests that need an observable Bridge without a real pty.
func NewBridgeForTest(closer func() error) *Bridge {
	return &Bridge{closer: closer}
}

func pumpReader(r io.Reader, event string, emit EmitFunc, maxChunk int) {
	buf := make([]byte, maxChunk)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out := make([]int, n)
			for i := 0; i < n; i++ {
				out[i] = int(buf[i])
			}
			emit(event, out)
		}
		if err != nil {
			return
		}
	}
}
