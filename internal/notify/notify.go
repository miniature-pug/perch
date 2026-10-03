// internal/notify/notify.go
package notify

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/miniature-pug/perch/internal/safe"
)

const (
	dbusNotifyService    = "org.freedesktop.Notifications"
	dbusNotifyObjectPath = "/org/freedesktop/Notifications"
	dbusNotifyTimeoutMS  = int32(5000)
	dbusNoReplaceID      = uint32(0) // 0 = new notification

	// callTimeout bounds one delivery attempt: the D-Bus Notify call, or the
	// notify-send fallback. A hung notification daemon must never wedge the
	// delivery goroutine for longer than this.
	callTimeout = 2 * time.Second

	// queueSize bounds the number of notifications waiting for delivery. The
	// queue only fills when the daemon is slow; a notification that arrives
	// while it is full is dropped (ErrDropped), because a desktop toast is
	// best-effort and must never apply backpressure to the caller.
	queueSize = 16
)

// ErrDropped is returned by an asynchronous Notifier when its delivery
// queue is full and the notification was discarded.
var ErrDropped = errors.New("notify: delivery queue full, notification dropped")

type Notifier interface {
	Notify(title, body string) error
}

type FakeNotifier struct {
	Calls []struct{ Title, Body string }
}

func (f *FakeNotifier) Notify(title, body string) error {
	f.Calls = append(f.Calls, struct{ Title, Body string }{title, body})
	return nil
}

// RunFunc is the injectable seam for notify-send (it mirrors the claude.go
// func-field idiom).
type RunFunc func(name string, args ...string) error

// dbusNotifier delivers through org.freedesktop.Notifications on the session
// bus. It keeps ONE connection for its lifetime and reconnects lazily after
// an error. The connection comes from dbus.ConnectSessionBus, which performs
// Auth AND Hello. A bare SessionBusPrivate+Auth connection skips Hello, and
// dbus-daemon disconnects any client whose first message is not Hello, so
// every notification used to fail with EOF (AGT-1). When the bus call fails
// for any reason (no daemon, no notification service, timeout), it falls back
// to notify-send through the injected runner.
type dbusNotifier struct {
	mu       sync.Mutex
	conn     *dbus.Conn
	connect  func() (*dbus.Conn, error)
	fallback RunFunc
}

func (d *dbusNotifier) Notify(title, body string) error {
	err := d.notifyDBus(title, body)
	if err == nil {
		return nil
	}
	if d.fallback != nil {
		if ferr := d.fallback("notify-send", title, body); ferr == nil {
			return nil
		}
	}
	return err
}

func (d *dbusNotifier) notifyDBus(title, body string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil || !d.conn.Connected() {
		conn, err := d.connect()
		if err != nil {
			return err
		}
		d.conn = conn
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	obj := d.conn.Object(dbusNotifyService, dbus.ObjectPath(dbusNotifyObjectPath))
	err := obj.CallWithContext(ctx, dbusNotifyService+".Notify", 0,
		"perch", dbusNoReplaceID, "", title, body,
		[]string{}, map[string]dbus.Variant{}, dbusNotifyTimeoutMS).Err
	if err != nil {
		// Drop the connection, so the next notification reconnects instead of
		// reusing a possibly broken one.
		_ = d.conn.Close()
		d.conn = nil
	}
	return err
}

type runnerNotifier struct{ run RunFunc }

func (r runnerNotifier) Notify(title, body string) error { return r.run("notify-send", title, body) }

// asyncNotifier decouples the caller from delivery. Notify only enqueues, so
// the app's per-workspace event pump never blocks on a slow or hung
// notification daemon (AGT-18). One goroutine drains the queue for the life
// of the process.
type asyncNotifier struct {
	ch chan [2]string
}

func newAsync(inner Notifier) *asyncNotifier {
	a := &asyncNotifier{ch: make(chan [2]string, queueSize)}
	go func() {
		defer safe.Recover("notify-delivery")
		for n := range a.ch {
			_ = inner.Notify(n[0], n[1])
		}
	}()
	return a
}

func (a *asyncNotifier) Notify(title, body string) error {
	select {
	case a.ch <- [2]string{title, body}:
		return nil
	default:
		return ErrDropped
	}
}

// runNotifySend runs notify-send with a bounded timeout.
func runNotifySend(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, notifySendArgs(args)...).Run()
}

// notifySendArgs puts "--" before the positional title and body, so agent
// or provider text that starts with "-" is never parsed as a notify-send
// option. The RunFunc seam keeps receiving (title, body); only the real
// exec adds the separator.
func notifySendArgs(args []string) []string {
	return append([]string{"--"}, args...)
}

// New returns the production Notifier. Delivery is asynchronous: Notify
// enqueues and returns at once. On a desktop with a session bus it delivers
// over D-Bus and falls back to notify-send when the bus call fails. Without
// a session bus it uses notify-send directly.
func New() Notifier {
	// Probe dbus availability with a throwaway connection, and close it
	// immediately. The probe never autolaunches a bus.
	if conn, err := dbus.SessionBusPrivateNoAutoStartup(); err == nil {
		_ = conn.Close()
		return newAsync(newDBusNotifier(runNotifySend))
	}
	return newAsync(NewWithRunner(runNotifySend))
}

func newDBusNotifier(fallback RunFunc) *dbusNotifier {
	return &dbusNotifier{
		connect: func() (*dbus.Conn, error) {
			return dbus.ConnectSessionBus()
		},
		fallback: fallback,
	}
}

// NewWithRunner returns a Notifier backed by the injected runner (test seam).
// It is synchronous.
func NewWithRunner(run RunFunc) Notifier { return runnerNotifier{run: run} }
