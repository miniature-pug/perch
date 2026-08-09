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

	"github.com/miniature-pug/perch/internal/safe"
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

const (
	maxChunk     = 16 * 1024
	defaultShell = "/bin/bash"
)

// LoginShellArgv returns [$SHELL, "-l"], falling back to ["/bin/bash", "-l"].
func LoginShellArgv() []string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = defaultShell
	}
	return []string{sh, "-l"}
}

// Spawn starts argv[0] argv[1:] inside a pty in working directory cwd,
// pumping output to emit on dataEvent as bounded []int chunks (≤ maxChunk).
// When the process exits (naturally or via Close), emit fires exitEvent with
// a map payload {"code": <int>} where code is the process exit code or -1 on
// signal death / forced close. No tmux. Closing the returned Bridge kills the
// process group; the reaper goroutine owns the single cmd.Wait call.
//
// env, when non-nil, becomes the child process environment verbatim; callers that
// want to ADD variables must pass append(os.Environ(), extra...) so the inherited
// environment is preserved. A nil env leaves cmd.Env unset, so Go inherits the
// current process environment unchanged (the plain-shell case).
func Spawn(ctx context.Context, cwd string, argv []string, env []string, dataEvent, exitEvent string, emit EmitFunc, cols, rows uint16) (*Bridge, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("pty Spawn: argv must not be empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // caller-controlled input
	cmd.Dir = cwd
	if env != nil {
		cmd.Env = env
	}
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
			// Send SIGKILL to the whole process group BEFORE closing the pty
			// master fd. Closing first unblocks pumpReader → the reaper goroutine
			// calls cmd.Wait() → the kernel can reap the pid and potentially
			// reuse it before the Kill reaches the (now stale) pgid. By killing
			// first we guarantee the signal targets the correct group.
			// Fall back to killing just the process on any Kill error.
			if cmd.Process != nil {
				if perr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); perr != nil {
					_ = cmd.Process.Kill()
				}
			}
			// Close the pty master fd after the kill so pumpReader unblocks and
			// the reaper goroutine can proceed with cmd.Wait().
			// NOTE: cmd.Wait() is NOT called here. The reaper goroutine below is
			// the single Wait site. Calling Wait in two places yields an incorrect
			// ProcessState on the second call; we must not do it.
			return f.Close()
		},
	}
	go func() {
		defer safe.Recover("pty-reaper")
		// pumpReader blocks until the pty fd returns EOF (which happens when
		// f.Close() is called by closer, or when the process closes its side).
		// Recover a pumpReader panic in a nested func so the child is still
		// reaped below — a panicking reader must never leak the process.
		func() {
			defer safe.Recover("pty-pump")
			pumpReader(f, dataEvent, emit, maxChunk)
		}()
		// Pump returned ⇒ pty EOF ⇒ process is ending. Single Wait site (no race).
		_ = cmd.Wait()
		code := -1 // signal death (forced Close / ctx kill) reports -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		emit(exitEvent, map[string]any{"code": code})
	}()
	return b, nil
}

// NewBridgeForTest returns a Bridge whose only behaviour is to call closer on
// Close. Used by app tests that need an observable Bridge without a real pty.
func NewBridgeForTest(closer func() error) *Bridge {
	return &Bridge{closer: closer}
}

// NewBridgeForTestWithResize returns a Bridge whose closer and resize are the
// supplied funcs. For app tests that assert Resize routing without a real pty.
func NewBridgeForTestWithResize(closer func() error, resize func(cols, rows uint16) error) *Bridge {
	return &Bridge{closer: closer, setsize: resize}
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
