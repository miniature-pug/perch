package pty

import (
	"context"
	"io"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestBridge_WriteForwardsToPty(t *testing.T) {
	pr, pw := io.Pipe()
	b := &Bridge{ptyFile: pw}

	go func() { _, _ = b.Write([]byte("ls\r")) }()

	buf := make([]byte, 3)
	if _, err := io.ReadFull(pr, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != "ls\r" {
		t.Fatalf("pty received %q, want %q", buf, "ls\r")
	}
}

func TestBridge_ResizeCallsSetter(t *testing.T) {
	var gotCols, gotRows uint16
	b := &Bridge{
		setsize: func(cols, rows uint16) error {
			gotCols, gotRows = cols, rows
			return nil
		},
	}
	if err := b.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if gotCols != 120 || gotRows != 40 {
		t.Fatalf("setter got cols=%d rows=%d, want 120/40", gotCols, gotRows)
	}
}

func TestPumpReader_BatchesChunksAsIntSlices(t *testing.T) {
	pr, pw := io.Pipe()

	var mu sync.Mutex
	var got [][]int
	emit := func(_ string, data ...any) {
		mu.Lock()
		defer mu.Unlock()
		if len(data) == 1 {
			if b, ok := data[0].([]int); ok {
				cp := make([]int, len(b))
				copy(cp, b)
				got = append(got, cp)
			}
		}
	}

	done := make(chan struct{})
	go func() {
		pumpReader(pr, "pty-data:t1", emit, 4096)
		close(done)
	}()

	_, _ = pw.Write([]byte("hello "))
	_, _ = pw.Write([]byte("world"))
	_ = pw.Close()
	<-done

	mu.Lock()
	defer mu.Unlock()
	var total []byte
	for _, c := range got {
		for _, v := range c {
			total = append(total, byte(v))
		}
	}
	if string(total) != "hello world" {
		t.Fatalf("reassembled bytes = %q, want %q", total, "hello world")
	}
	for i, c := range got {
		if len(c) > 4096 {
			t.Fatalf("chunk %d exceeded max size: %d", i, len(c))
		}
	}
}

func TestLoginShellArgv_UsesShellEnv(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	argv := LoginShellArgv()
	if len(argv) != 2 || argv[0] != "/bin/sh" || argv[1] != "-l" {
		t.Fatalf("LoginShellArgv = %v, want [/bin/sh -l]", argv)
	}
}

func TestLoginShellArgv_FallsBackToBash(t *testing.T) {
	t.Setenv("SHELL", "")
	argv := LoginShellArgv()
	if len(argv) != 2 || argv[0] != "/bin/bash" || argv[1] != "-l" {
		t.Fatalf("LoginShellArgv = %v, want [/bin/bash -l]", argv)
	}
}

func TestBridge_CloseIdempotent(t *testing.T) {
	closed := 0
	b := &Bridge{closer: func() error { closed++; return nil }}
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if closed != 1 {
		t.Errorf("closer called %d times, want exactly 1", closed)
	}
}

// TestSpawn_RoundTrip spawns `sh -c 'printf hi'` and asserts that the
// emitted []int bytes contain "hi". The test polls with a deadline before
// Close, so bytes are not lost.
func TestSpawn_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var mu sync.Mutex
	var collected []byte

	emit := func(event string, data ...any) {
		if event != "test-event" {
			return
		}
		if len(data) == 1 {
			if chunk, ok := data[0].([]int); ok {
				mu.Lock()
				for _, v := range chunk {
					collected = append(collected, byte(v))
				}
				mu.Unlock()
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	br, err := Spawn(ctx, t.TempDir(), []string{"sh", "-c", "printf hi"}, nil, "test-event", "pty:exit:t1", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := string(collected)
		mu.Unlock()
		if strings.Contains(got, "hi") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	_ = br.Close()

	mu.Lock()
	got := string(collected)
	mu.Unlock()
	if !strings.Contains(got, "hi") {
		t.Errorf("round-trip bytes = %q, want to contain %q", got, "hi")
	}
}

func TestSpawn_EmitsExitEvent(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // hermetic: no profile read
	type ev struct {
		name string
		data []any
	}
	got := make(chan ev, 8)
	emit := func(name string, data ...any) { got <- ev{name, data} }
	// `sh -c 'exit 7'` exits fast and reads no -l profile.
	b, err := Spawn(context.Background(), t.TempDir(),
		[]string{"/bin/sh", "-c", "exit 7"}, nil, "pty:data:t1", "pty:exit:t1", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-got:
			if e.name != "pty:exit:t1" {
				continue // skip any trailing pty:data
			}
			m, ok := e.data[0].(map[string]any)
			if !ok {
				t.Fatalf("exit payload not a map: %#v", e.data[0])
			}
			if m["code"] != 7 {
				t.Fatalf("exit code = %v, want 7", m["code"])
			}
			return
		case <-deadline:
			t.Fatal("no pty:exit emitted within 5s")
		}
	}
}

// TestBridge_SuppressExit_DisarmsExitEmit proves the displaced-pane fix.
// After SuppressExit, the reaper reaps the (killed) process, but emits NO
// exitEvent. This means a remounted Terminal that shares the exitEvent name
// cannot re-latch its overlay from a stale exit. The test spawns a
// long-lived process, so it never exits on its own within the window. Only
// SuppressExit plus Close reaps it.
func TestBridge_SuppressExit_DisarmsExitEmit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fired := make(chan struct{}, 4)
	emit := func(name string, _ ...any) {
		if name == "pty:exit:silent" {
			fired <- struct{}{}
		}
	}
	b, err := Spawn(context.Background(), t.TempDir(),
		[]string{"sleep", "30"}, nil, "pty:data:silent", "pty:exit:silent", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Disarm BEFORE closing (the app disarms before the ctx cancel that reaps the
	// shell), then force the close.
	b.SuppressExit()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-fired:
		t.Fatal("pty:exit emitted after SuppressExit — the reaper must stay silent for a displaced pane")
	case <-time.After(2 * time.Second):
		// No emit occurs within a generous window (the buggy path emits well
		// under this). Suppression held.
	}
}

// TestBridge_Close_WithoutSuppress_StillEmits is the positive control for
// the suppression test. A normal (unsuppressed) forced Close MUST still emit
// the exit event. CloseWorkspace relies on exactly this: a genuine agent or
// shell exit.
func TestBridge_Close_WithoutSuppress_StillEmits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fired := make(chan int, 4)
	emit := func(name string, data ...any) {
		if name != "pty:exit:loud" {
			return
		}
		if m, ok := data[0].(map[string]any); ok {
			if code, ok := m["code"].(int); ok {
				fired <- code
				return
			}
		}
		fired <- -999
	}
	b, err := Spawn(context.Background(), t.TempDir(),
		[]string{"sleep", "30"}, nil, "pty:data:loud", "pty:exit:loud", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case code := <-fired:
		if code != -1 {
			t.Fatalf("forced-close exit code = %d, want -1 (signal death)", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no pty:exit emitted after a normal Close — a genuine exit MUST still emit")
	}
}

// TestSpawn_CloseKillsProcessGroup proves that Close reaps the children the
// login shell forks, not just the shell itself. The test spawns a shell that
// backgrounds a long sleep and prints the child PID. After Close, that PID
// must be gone.
func TestSpawn_CloseKillsProcessGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var mu sync.Mutex
	var buf []byte
	emit := func(event string, data ...any) {
		if event != "pg" || len(data) != 1 {
			return
		}
		if chunk, ok := data[0].([]int); ok {
			mu.Lock()
			for _, v := range chunk {
				buf = append(buf, byte(v))
			}
			mu.Unlock()
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Background a SIGHUP-ignoring sleep so that killing the session leader
	// (which sends SIGHUP to the foreground group) is NOT enough to kill it.
	// Only a SIGKILL to the whole process group will work, which is what
	// Close sends via syscall.Kill(-pgid, SIGKILL).
	script := "nohup sleep 30 >/dev/null 2>&1 & echo PGTESTPID=$!; sleep 5"
	br, err := Spawn(ctx, t.TempDir(), []string{"sh", "-c", script}, nil, "pg", "pty:exit:pg", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Parse the child PID from emitted output.
	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && childPID == 0 {
		mu.Lock()
		out := string(buf)
		mu.Unlock()
		if i := strings.Index(out, "PGTESTPID="); i >= 0 {
			rest := out[i+len("PGTESTPID="):]
			j := strings.IndexAny(rest, "\r\n")
			if j > 0 {
				if pid, perr := strconv.Atoi(strings.TrimSpace(rest[:j])); perr == nil {
					childPID = pid
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatalf("never captured child PID; output=%q", string(buf))
	}

	// Sanity: child is alive now.
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("child %d should be alive before Close: %v", childPID, err)
	}

	if err := br.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close, the backgrounded child must be dead (signal 0 -> ESRCH).
	gone := false
	d2 := time.Now().Add(3 * time.Second)
	for time.Now().Before(d2) {
		if err := syscall.Kill(childPID, 0); err != nil {
			gone = true // ESRCH or EPERM => not ours/alive
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Errorf("child %d survived Close — process group not killed", childPID)
		_ = syscall.Kill(childPID, syscall.SIGKILL) // cleanup
	}
}

// TestSpawn_ReaderPanicDoesNotCrashAndStillReapsChild proves two things: a
// panic inside pumpReader (through a panicking emit on data output) does not
// crash the process, and cmd.Wait still runs. The exit event must still
// fire, and that only happens after Wait. Without the reaper's recover, this
// test would abort the whole test binary.
func TestSpawn_ReaderPanicDoesNotCrashAndStillReapsChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exitFired := make(chan int, 1)
	emit := func(name string, data ...any) {
		if name == "panic-exit" {
			if m, ok := data[0].(map[string]any); ok {
				if code, ok := m["code"].(int); ok {
					exitFired <- code
					return
				}
			}
			exitFired <- -999
			return
		}
		// Any data output panics, unwinding pumpReader.
		panic("emit exploded")
	}
	// `printf hi` writes output (triggering the panicking data emit) then exits 0.
	b, err := Spawn(context.Background(), t.TempDir(),
		[]string{"/bin/sh", "-c", "printf hi"}, nil, "panic-data", "panic-exit", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	select {
	case code := <-exitFired:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exit event never fired — reaper did not survive the reader panic")
	}
}
