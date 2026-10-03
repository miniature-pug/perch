package notify

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// startPrivateBus runs a throwaway dbus-daemon and returns its address. The
// test skips when dbus-daemon is not installed.
func startPrivateBus(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "--session", "--nofork", "--print-address=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Skipf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("read bus address: %v", err)
	}
	return strings.TrimSpace(line)
}

type fakeNotifyService struct {
	mu    sync.Mutex
	calls [][2]string
}

func (f *fakeNotifyService) Notify(app string, id uint32, icon, summary, body string,
	actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [2]string{summary, body})
	return 1, nil
}

// TestDBusNotifier_DeliversOverRealBus is the AGT-1 regression. The old code
// opened SessionBusPrivate, called Auth, and never Hello, so dbus-daemon
// dropped the connection and every Notify failed with EOF. This test runs a
// real dbus-daemon, registers a fake org.freedesktop.Notifications service,
// and checks the notification arrives. The fallback must not run.
func TestDBusNotifier_DeliversOverRealBus(t *testing.T) {
	addr := startPrivateBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)

	svcConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("service connect: %v", err)
	}
	defer func() { _ = svcConn.Close() }()
	svc := &fakeNotifyService{}
	if err := svcConn.Export(svc, dbusNotifyObjectPath, dbusNotifyService); err != nil {
		t.Fatal(err)
	}
	if reply, err := svcConn.RequestName(dbusNotifyService, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName: %v %v", reply, err)
	}

	fellBack := false
	n := newDBusNotifier(func(string, ...string) error { fellBack = true; return nil })
	for i := 0; i < 2; i++ { // the second call reuses the cached connection
		if err := n.Notify("Approval needed", "body"); err != nil {
			t.Fatalf("Notify #%d: %v", i, err)
		}
	}
	if fellBack {
		t.Error("fallback ran although the bus delivered")
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.calls) != 2 || svc.calls[0][0] != "Approval needed" {
		t.Errorf("service calls = %v, want 2 deliveries", svc.calls)
	}
}

// TestDBusNotifier_FallsBackToNotifySend checks that a bus with no
// notification service falls back to the runner.
func TestDBusNotifier_FallsBackToNotifySend(t *testing.T) {
	addr := startPrivateBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	var got []string
	n := newDBusNotifier(func(name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	})
	if err := n.Notify("t", "b"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(got) != 3 || got[0] != "notify-send" || got[1] != "t" {
		t.Errorf("fallback argv = %v", got)
	}
}

type blockingNotifier struct{ release chan struct{} }

func (b blockingNotifier) Notify(string, string) error { <-b.release; return nil }

// TestAsyncNotifier_NeverBlocksCaller is the AGT-18 regression: a hung
// delivery must not block Notify; once the queue is full, Notify drops.
func TestAsyncNotifier_NeverBlocksCaller(t *testing.T) {
	inner := blockingNotifier{release: make(chan struct{})}
	defer close(inner.release)
	a := newAsync(inner)
	done := make(chan struct{})
	var dropped bool
	go func() {
		defer close(done)
		for i := 0; i < queueSize+5; i++ {
			if err := a.Notify("t", "b"); errors.Is(err, ErrDropped) {
				dropped = true
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked on a hung delivery")
	}
	if !dropped {
		t.Error("expected drops once the queue was full")
	}
}
