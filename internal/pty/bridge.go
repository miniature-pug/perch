// Package pty bridges a tmux attach session to the GUI: it spawns
// `tmux attach` inside a pseudo-terminal, batches the pty's output into
// bounded byte chunks delivered through an injected emit seam, and forwards
// keystrokes and resize requests back to the pty. The emit seam (rather than a
// hardcoded wails runtime call) is what makes the bridge testable headlessly.
package pty

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	creackpty "github.com/creack/pty"

	"github.com/Miniature-Pug/perch/internal/tmux"
)

// EmitFunc delivers a named event with optional payload to the frontend. In
// production it wraps wails runtime.EventsEmit; in tests it captures calls.
type EmitFunc func(event string, data ...any)

// Bridge owns one `tmux attach` pseudo-terminal and the goroutine pumping its
// output to the frontend. One Bridge per open GUI terminal tab.
type Bridge struct {
	mu      sync.Mutex
	ptyFile io.WriteCloser // the master side of the pty (an *os.File in prod)
	closer  func() error   // closes the pty + reaps the tmux attach client
	setsize func(cols, rows uint16) error
}

// Write forwards raw keystroke bytes from xterm.js to the pty's stdin. The
// bytes are intentionally arbitrary — this is a terminal into the user's own
// agent/shell and confers no privilege beyond what the user already holds.
func (b *Bridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ptyFile == nil {
		return 0, os.ErrClosed
	}
	return b.ptyFile.Write(p)
}

// Resize applies the terminal dimensions reported by addon-fit to the pty
// winsize; tmux propagates the SIGWINCH to the attached client.
func (b *Bridge) Resize(cols, rows uint16) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.setsize == nil {
		return nil
	}
	return b.setsize(cols, rows)
}

// Close stops the pump and tears down the pty + attach client. Safe to call
// more than once.
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

// maxChunk bounds a single emitted byte chunk. Sized for snappy interactive
// latency with bounded IPC volume.
const maxChunk = 16 * 1024

// Spawn starts `tmux -L <socket> attach-session -t <session>` inside a
// pseudo-terminal and begins pumping its output to emit on the given event.
// The attach argv is built explicitly using attach-session (not AttachArgs,
// which branches on $TMUX to switch-client) so the behaviour is
// environment-independent: a pty is always a fresh client and attach-session
// is always correct. The raw session string is passed through
// tmux.SessionTarget (prepends '=') and is never passed to a shell. Closing
// the returned Bridge kills only this attach client; the agent session itself
// survives.
func Spawn(ctx context.Context, t tmux.Tmux, session, event string, emit EmitFunc) (*Bridge, error) {
	argv := t.ExecArgs("attach-session", "-t", tmux.SessionTarget(session))
	if len(argv) == 0 {
		return nil, fmt.Errorf("pty Spawn: empty tmux argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	f, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		return nil, fmt.Errorf("pty Spawn: start: %w", err)
	}
	b := &Bridge{
		ptyFile: f,
		setsize: func(cols, rows uint16) error {
			return creackpty.Setsize(f, &creackpty.Winsize{Cols: cols, Rows: rows})
		},
		closer: func() error {
			ferr := f.Close()
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			return ferr
		},
	}
	go pumpReader(f, event, emit, maxChunk)
	return b, nil
}

// pumpReader reads r until EOF or error, emitting each read as a bounded
// (≤maxChunk) []int chunk on event. Each chunk is emitted as a []int so
// Wails' JSON encoding delivers it to the frontend as a number[] (a []byte
// would JSON-encode as a base64 string, breaking the frontend's
// Uint8Array.from(number[]) reconstruction). Batching is bounded by maxChunk
// and the OS pty read granularity — no timer is involved.
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
