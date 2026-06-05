// internal/notify/notify.go
package notify

import (
	"github.com/godbus/dbus/v5"
	"os/exec"
)

const (
	dbusNotifyService    = "org.freedesktop.Notifications"
	dbusNotifyObjectPath = "/org/freedesktop/Notifications"
	dbusNotifyTimeoutMS  = int32(5000)
	dbusNoReplaceID      = uint32(0) // 0 = new notification
)

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

// RunFunc is the injectable seam for notify-send (mirrors claude.go func-field idiom).
type RunFunc func(name string, args ...string) error

type dbusNotifier struct{}

func (d dbusNotifier) Notify(title, body string) error {
	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.Auth(nil); err != nil {
		return err
	}
	obj := conn.Object(dbusNotifyService, dbus.ObjectPath(dbusNotifyObjectPath))
	return obj.Call(dbusNotifyService+".Notify", 0,
		"perch", dbusNoReplaceID, "", title, body,
		[]string{}, map[string]dbus.Variant{}, dbusNotifyTimeoutMS).Err
}

type runnerNotifier struct{ run RunFunc }

func (r runnerNotifier) Notify(title, body string) error { return r.run("notify-send", title, body) }

// New returns a dbus Notifier; falls back to notify-send if dbus is unavailable.
func New() Notifier {
	// Probe dbus availability with a throwaway connection; close it immediately
	// so the probe never leaks a session-bus connection. dbusNotifier opens its
	// own short-lived connection per Notify call.
	if conn, err := dbus.SessionBusPrivate(); err == nil {
		_ = conn.Close()
		return dbusNotifier{}
	}
	return NewWithRunner(func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	})
}

// NewWithRunner returns a Notifier backed by the injected runner (test seam).
func NewWithRunner(run RunFunc) Notifier { return runnerNotifier{run: run} }
