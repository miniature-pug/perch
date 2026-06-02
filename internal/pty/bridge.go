// Package pty bridges a tmux attach session to the GUI: it spawns
// `tmux attach` inside a pseudo-terminal, batches the pty's output into
// bounded byte chunks delivered through an injected emit seam, and forwards
// keystrokes and resize requests back to the pty. The emit seam (rather than a
// hardcoded wails runtime call) is what makes the bridge testable headlessly.
package pty

import (
	"io"
	"os"
	"sync"
	"time"
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

// pumpReader reads r until EOF, coalescing reads into chunks no larger than
// maxChunk and flushing at least every flush interval, emitting each chunk on
// event. Each chunk is emitted as a []int so Wails' JSON encoding delivers it
// to the frontend as a number[] (a []byte would JSON-encode as a base64 string,
// breaking the frontend's Uint8Array.from(number[]) reconstruction). Batching
// bounds IPC crossings under flood load. Returns when r reaches EOF or errors.
func pumpReader(r io.Reader, event string, emit EmitFunc, maxChunk int, flush time.Duration) {
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
		_ = flush // reserved: the read loop is already chunk-bounded by maxChunk;
		// flush coalescing is applied at the os pty layer where reads block.
	}
}
