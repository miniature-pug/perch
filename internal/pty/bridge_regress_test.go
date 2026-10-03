package pty

import (
	"context"
	"encoding/base64"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func (b *Bridge) shellPIDForTest() int { return b.pid }

// outputCollector gathers a bridge's []int output for tests.
type outputCollector struct {
	mu  sync.Mutex
	buf []byte
}

func (o *outputCollector) emit(_ string, data ...any) {
	if len(data) != 1 {
		return
	}
	if chunk, ok := data[0].([]int); ok {
		o.mu.Lock()
		for _, v := range chunk {
			o.buf = append(o.buf, byte(v))
		}
		o.mu.Unlock()
	}
}

// waitPID waits until the output contains "<marker><pid>\n" and returns pid.
func (o *outputCollector) waitPID(t *testing.T, marker string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		out := string(o.buf)
		o.mu.Unlock()
		if i := strings.LastIndex(out, marker); i >= 0 {
			rest := out[i+len(marker):]
			if j := strings.IndexAny(rest, "\r\n"); j > 0 {
				if pid, err := strconv.Atoi(strings.TrimSpace(rest[:j])); err == nil {
					return pid
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never saw %q in output %q", marker, o.buf)
	return 0
}

func processGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		// A killed child of init lingers briefly as a zombie; treat that as gone.
		if st, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output(); err == nil &&
			strings.HasPrefix(strings.TrimSpace(string(st)), "Z") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// GFS-21: an interactive shell puts a background job in its own process
// group. Close must still kill it.
func TestSpawn_CloseKillsBackgroundJobInOwnGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var o outputCollector
	b, err := Spawn(context.Background(), t.TempDir(),
		[]string{"bash", "--norc", "--noprofile", "-i"}, nil, "d", "x", o.emit, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("sleep 31337 & echo BGPID=$!\n")); err != nil {
		t.Fatal(err)
	}
	pid := o.waitPID(t, "BGPID=")
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("background job %d not running: %v", pid, err)
	}
	if pgid == b.shellPIDForTest() {
		t.Fatalf("test premise: job control should give the job its own group")
	}

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if !processGone(pid, 3*time.Second) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("background job %d survived Close", pid)
	}
}

// The app cancels the pane ctx before it calls Close. By then the reaper may
// already have reaped the shell, so Close signals nothing; ctx cancellation
// itself must therefore kill the session, including a background job that
// does not hold the tty (review finding 1).
func TestSpawn_CancelThenCloseKillsBackgroundJobs(t *testing.T) {
	for _, cmdline := range []string{
		"sleep 31337 & echo BGPID=$!\n",
		"sleep 31337 </dev/null >/dev/null 2>&1 & echo BGPID=$!\n",
	} {
		t.Run(strings.TrimSpace(cmdline), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var o outputCollector
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, err := Spawn(ctx, t.TempDir(),
				[]string{"bash", "--norc", "--noprofile", "-i"}, nil, "d", "x", o.emit, 80, 24)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Write([]byte(cmdline)); err != nil {
				t.Fatal(err)
			}
			pid := o.waitPID(t, "BGPID=")
			cancel()
			time.Sleep(300 * time.Millisecond) // the app tears down the monitor here
			_ = b.Close()
			if !processGone(pid, 2*time.Second) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("background job %d survived cancel then Close", pid)
			}
		})
	}
}

// GFS-22: once the shell has exited and been reaped, the bridge is marked
// reaped, so a later Close does not signal a pid the kernel may reuse.
func TestSpawn_MarksReapedAfterNaturalExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exited := make(chan struct{}, 1)
	emit := func(ev string, _ ...any) {
		if ev == "x" {
			exited <- struct{}{}
		}
	}
	b, err := Spawn(context.Background(), t.TempDir(), []string{"sh", "-c", "exit 0"}, nil, "d", "x", emit, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit event")
	}
	b.mu.Lock()
	reaped := b.reaped
	b.mu.Unlock()
	if !reaped {
		t.Fatal("bridge not marked reaped after the shell exited")
	}
	if err := b.Close(); err != nil {
		t.Logf("Close after exit: %v", err)
	}
}

// GFS-23: a Write blocked on a full pty must not hold up Close.
func TestBridge_BlockedWriteDoesNotBlockClose(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	b, err := Spawn(context.Background(), t.TempDir(),
		[]string{"sh", "-c", "stty raw -echo; exec sleep 30"}, nil, "d", "x", func(string, ...any) {}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		big := make([]byte, 1<<20)
		for i := range big {
			big[i] = 'a'
		}
		_, _ = b.Write(big)
	}()
	time.Sleep(300 * time.Millisecond)

	done := make(chan struct{})
	go func() { _ = b.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-b.shellPIDForTest(), syscall.SIGKILL)
		t.Fatal("Close blocked behind a stalled Write")
	}
}

// GFS-24: SpawnBase64 emits each chunk as a base64 string.
func TestSpawnBase64_EmitsBase64Strings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var mu sync.Mutex
	var got []byte
	exited := make(chan struct{}, 1)
	emit := func(ev string, data ...any) {
		switch ev {
		case "d":
			s, ok := data[0].(string)
			if !ok {
				t.Errorf("payload type %T, want string", data[0])
				return
			}
			raw, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				t.Errorf("payload %q is not base64: %v", s, err)
				return
			}
			mu.Lock()
			got = append(got, raw...)
			mu.Unlock()
		case "x":
			exited <- struct{}{}
		}
	}
	b, err := SpawnBase64(context.Background(), t.TempDir(),
		[]string{"sh", "-c", "printf 'hi\\377'"}, nil, "d", "x", emit, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit event")
	}
	mu.Lock()
	defer mu.Unlock()
	if string(got) != "hi\xff" {
		t.Errorf("decoded output = %q, want %q", got, "hi\xff")
	}
}
