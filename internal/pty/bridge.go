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
	// suppressExit, when set, DISARMS the reaper goroutine's exitEvent emit.
	// The process still gets killed and reaped, but the reaper never emits
	// pty:exit. SuppressExit sets this flag under mu. The reaper reads the
	// flag under mu before it emits. This applies only to a DISPLACED pane,
	// whose bridge shares its exitEvent name with the pane's replacement (see
	// app.OpenWorkspace). A genuine exit must still emit, so suppressExit
	// stays false on every normally-closed bridge.
	suppressExit bool
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

// OverrideWriteForTest replaces the pty write target with fn. This function
// is test-only. App tests use it to capture what OpenWorkspace writes to the
// shell, without a real pty.
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

// SuppressExit disarms the reaper's exitEvent emit for this bridge. After
// this call, Close (or a ctx cancellation) still kills and reaps the
// process, but the reaper never emits a pty:exit event. SuppressExit is
// idempotent, and safe to call on a bridge with no reaper (a test bridge).
//
// SuppressExit exists for the pane-displacement path in app.OpenWorkspace. On
// Reopen, the displaced login shell SURVIVES the agent's /exit, and its
// bridge shares the exitEvent name ("pty:exit:pane-<id>") with the freshly
// remounted Terminal. Without SuppressExit, a stray reaper emit would
// re-latch the "session has ended" overlay. The caller MUST call
// SuppressExit BEFORE the pane context is cancelled: that cancellation reaps
// the shell through exec.CommandContext and can wake the reaper, so a
// combined "close-and-suppress" call performed later would race the emit.
// Suppression therefore stays separable from Close. A genuine agent or shell
// exit never calls SuppressExit, so it still emits.
func (b *Bridge) SuppressExit() {
	b.mu.Lock()
	b.suppressExit = true
	b.mu.Unlock()
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

// Spawn starts argv[0] with argv[1:] inside a pty, in working directory cwd.
// Spawn pumps output to emit on dataEvent, as bounded []int chunks (at most
// maxChunk each). When the process exits, whether naturally or through
// Close, emit fires exitEvent with a map payload {"code": <int>}. code holds
// the process exit code, or -1 on a signal death or a forced close. Spawn
// uses no tmux. Closing the returned Bridge kills the process group. The
// reaper goroutine owns the single cmd.Wait call.
//
// When env is non-nil, it becomes the child process environment verbatim.
// Callers that want to ADD variables must pass
// append(os.Environ(), extra...), to preserve the inherited environment. A
// nil env leaves cmd.Env unset, so Go inherits the current process
// environment unchanged (the plain-shell case).
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
			// master fd. Closing first would unblock pumpReader → the reaper
			// goroutine calls cmd.Wait() → the kernel can reap the pid and could
			// reuse it before the Kill reaches the (now stale) pgid. Killing
			// first guarantees the signal targets the correct group.
			// Fall back to killing just the process on any Kill error.
			if cmd.Process != nil {
				if perr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); perr != nil {
					_ = cmd.Process.Kill()
				}
			}
			// Close the pty master fd after the kill, so pumpReader unblocks and
			// the reaper goroutine can proceed with cmd.Wait().
			// NOTE: cmd.Wait() is NOT called here. The reaper goroutine below is
			// the single Wait site. Calling Wait in two places yields an
			// incorrect ProcessState on the second call. This function must
			// never call Wait.
			return f.Close()
		},
	}
	go func() {
		defer safe.Recover("pty-reaper")
		// pumpReader blocks until the pty fd returns EOF. This happens when
		// closer calls f.Close(), or when the process closes its own side.
		// This nested func recovers a pumpReader panic, so the child is still
		// reaped below. A panicking reader must never leak the process.
		func() {
			defer safe.Recover("pty-pump")
			pumpReader(f, dataEvent, emit, maxChunk)
		}()
		// Pump returned ⇒ pty EOF ⇒ the process is ending. This is the single
		// Wait site (no race).
		_ = cmd.Wait()
		code := -1 // signal death (forced Close / ctx kill) reports -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		// This re-checks the disarm flag under the same mutex that
		// SuppressExit writes. A displaced pane suppresses its exit emit
		// before the process is reaped, so the reaper observes the flag here
		// and stays silent (see SuppressExit). The reaper releases the lock
		// before it calls emit, so a full events consumer can never block a
		// mutex.
		b.mu.Lock()
		suppressed := b.suppressExit
		b.mu.Unlock()
		if suppressed {
			return
		}
		emit(exitEvent, map[string]any{"code": code})
	}()
	return b, nil
}

// NewBridgeForTest returns a Bridge whose only behaviour is to call closer on
// Close. App tests use it when they need an observable Bridge without a real
// pty.
func NewBridgeForTest(closer func() error) *Bridge {
	return &Bridge{closer: closer}
}

// NewBridgeForTestWithResize returns a Bridge whose closer and resize are the
// supplied funcs. App tests use it to assert Resize routing without a real
// pty.
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
