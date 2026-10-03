// Package pty provides a direct pseudo-terminal bridge per pane (no tmux).
package pty

import (
	"context"
	"encoding/base64"
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
	// reaped is set under mu by the reaper once the shell has exited and is
	// about to be reaped. After that its pid may be reused, so closer must
	// not signal it, its process group, or its session any more.
	reaped bool
	// pid is the shell's pid (0 for a test bridge without a process).
	pid int
	// gate tracks shell readiness for WaitShellReady (nil for a test bridge
	// without a process). It is set before the pump starts and never changes.
	gate *readyGate
}

// Write sends p to the pty. Write does not hold mu while it writes: a
// foreground program in raw mode that is not reading lets a large paste
// block in the kernel, and Close, Resize and the reaper must not wait behind
// it. Close closes the pty after killing the session, which ends a blocked
// write.
func (b *Bridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	writeFn, f := b.writeFn, b.ptyFile
	b.mu.Unlock()
	if writeFn != nil {
		return writeFn(p)
	}
	if f == nil {
		return 0, os.ErrClosed
	}
	return f.Write(p)
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
	if b.gate != nil {
		b.gate.markClosed()
	}
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

// killSessionLocked SIGKILLs the shell's process group and every other
// process in its session. The caller holds b.mu. The reaper sets b.reaped
// under b.mu before it reaps the shell, so while b.reaped is false the
// shell is alive or an unreaped zombie: its pid, process group id and
// session id cannot belong to anyone else. Once reaped, it does nothing.
func (b *Bridge) killSessionLocked(cmd *exec.Cmd) {
	if cmd.Process == nil || b.reaped {
		return
	}
	pid := cmd.Process.Pid
	if perr := syscall.Kill(-pid, syscall.SIGKILL); perr != nil {
		_ = cmd.Process.Kill()
	}
	// creack/pty makes the shell a session leader, so the session id is its
	// pid. Background jobs live in other process groups of that session and
	// survive the group kill above.
	killSession(pid)
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
// uses no tmux. The reaper goroutine owns the single cmd.Wait call.
//
// Closing the returned Bridge kills every process in the shell's session:
// the shell's own process group, and also the background jobs, which an
// interactive shell puts in process groups of their own (`npm run dev &`).
// Only processes that started a new session of their own (daemons) escape.
// Once the shell has exited and been reaped, Close no longer signals
// anything, because the kernel may have reused its pid.
//
// When env is non-nil, it becomes the child process environment verbatim.
// Callers that want to ADD variables must pass
// append(os.Environ(), extra...), to preserve the inherited environment. A
// nil env leaves cmd.Env unset, so Go inherits the current process
// environment unchanged (the plain-shell case).
//
// A caller that types a line into the shell right after spawning it (the
// agent launch line) should first call WaitShellReady, or an rc file that
// drains or flushes stdin can swallow the line after the tty has echoed it.
func Spawn(ctx context.Context, cwd string, argv []string, env []string, dataEvent, exitEvent string, emit EmitFunc, cols, rows uint16) (*Bridge, error) {
	return spawn(ctx, cwd, argv, env, dataEvent, exitEvent, emit, cols, rows, intsPayload)
}

// SpawnBase64 is Spawn, except that each dataEvent payload is the chunk as a
// standard base64 string instead of a []int. A []int costs about four JSON
// bytes per output byte and a large allocation per chunk; base64 costs 4/3.
// The frontend decodes it with
// Uint8Array.from(atob(s), (c) => c.charCodeAt(0)).
func SpawnBase64(ctx context.Context, cwd string, argv []string, env []string, dataEvent, exitEvent string, emit EmitFunc, cols, rows uint16) (*Bridge, error) {
	return spawn(ctx, cwd, argv, env, dataEvent, exitEvent, emit, cols, rows, base64Payload)
}

func spawn(ctx context.Context, cwd string, argv []string, env []string, dataEvent, exitEvent string, emit EmitFunc, cols, rows uint16, encode func([]byte) any) (*Bridge, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("pty Spawn: argv must not be empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // caller-controlled input
	cmd.Dir = cwd
	if env != nil {
		cmd.Env = env
	}
	b := &Bridge{}
	// ctx cancellation kills exactly what Close kills. os/exec's default would
	// SIGKILL only the shell; the reaper could then reap it before Close runs,
	// and Close would (correctly) no longer signal anything, leaving the
	// shell's background jobs alive. Cancel must be set before Start; it
	// can fire as soon as Start returns, so it locks b.mu, which guards the
	// fields set below.
	cmd.Cancel = func() error {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.killSessionLocked(cmd)
		return nil
	}
	f, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("pty Spawn: start %q: %w", argv[0], err)
	}
	// The readiness probe reads the tty state through RawControl, which
	// (unlike f.Fd) leaves the fd non-blocking and fails cleanly once f is
	// closed.
	rawConn, err := f.SyscallConn()
	if err != nil {
		b.mu.Lock()
		b.killSessionLocked(cmd)
		b.mu.Unlock()
		_ = f.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("pty Spawn: %w", err)
	}
	gate := newReadyGate(cmd.Process.Pid, func() (pgrp int, canonical bool, err error) {
		if cerr := rawConn.Control(func(fd uintptr) {
			pgrp, canonical, err = ttyState(fd)
		}); cerr != nil {
			return 0, false, cerr
		}
		return pgrp, canonical, err
	})
	b.mu.Lock()
	b.ptyFile = f
	b.pid = cmd.Process.Pid
	b.gate = gate
	b.setsize = func(c, r uint16) error {
		return creackpty.Setsize(f, &creackpty.Winsize{Cols: c, Rows: r})
	}
	// closer runs with b.mu held (see Close).
	b.closer = func() error {
		// Kill BEFORE closing the pty master fd. Closing first would unblock
		// pumpReader, and the reaper could reap the shell, freeing its pid
		// for reuse before the signals land.
		b.killSessionLocked(cmd)
		// Close the pty master fd after the kill, so pumpReader unblocks and
		// the reaper goroutine can proceed with cmd.Wait().
		// NOTE: cmd.Wait() is NOT called here. The reaper goroutine below is
		// the single Wait site. Calling Wait in two places yields an
		// incorrect ProcessState on the second call. This function must
		// never call Wait.
		return f.Close()
	}
	b.mu.Unlock()
	go func() {
		defer safe.Recover("pty-reaper")
		// pumpReader blocks until the pty fd returns EOF. This happens when
		// closer calls f.Close(), or when the process closes its own side.
		// This nested func recovers a pumpReader panic, so the child is still
		// reaped below. A panicking reader must never leak the process.
		func() {
			defer safe.Recover("pty-pump")
			pump(f, dataEvent, emit, maxChunk, encode, gate.observe)
		}()
		gate.markClosed()
		// Pump returned ⇒ pty EOF ⇒ the process is ending. Wait for it to
		// exit WITHOUT reaping it, then mark it reaped under b.mu, and only
		// then reap it. A concurrent Close therefore either finishes its
		// kills while the pid is still reserved, or sees b.reaped and skips
		// them. This is the single Wait site (no race).
		waitExited(cmd.Process.Pid)
		b.mu.Lock()
		b.reaped = true
		b.mu.Unlock()
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

// intsPayload encodes a chunk as one int per byte (Spawn's format).
func intsPayload(chunk []byte) any {
	out := make([]int, len(chunk))
	for i, c := range chunk {
		out[i] = int(c)
	}
	return out
}

// base64Payload encodes a chunk as a standard base64 string (SpawnBase64's format).
func base64Payload(chunk []byte) any {
	return base64.StdEncoding.EncodeToString(chunk)
}

func pumpReader(r io.Reader, event string, emit EmitFunc, maxChunk int) {
	pump(r, event, emit, maxChunk, intsPayload, nil)
}

// pump reads r until an error, and emits each chunk read (at most maxChunk
// bytes) on event, encoded by encode. A non-nil observe sees each chunk
// first; it must not modify or retain it.
func pump(r io.Reader, event string, emit EmitFunc, maxChunk int, encode func([]byte) any, observe func([]byte)) {
	buf := make([]byte, maxChunk)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if observe != nil {
				observe(buf[:n])
			}
			emit(event, encode(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}
