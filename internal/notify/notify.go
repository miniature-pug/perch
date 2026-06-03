// internal/notify/notify.go
package notify

import (
	"os/exec"
	"github.com/godbus/dbus/v5"
)

type Notifier interface{ Notify(title, body string) error }

type FakeNotifier struct{ Calls []struct{ Title, Body string } }

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
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	return obj.Call("org.freedesktop.Notifications.Notify", 0,
		"perch", uint32(0), "", title, body,
		[]string{}, map[string]dbus.Variant{}, int32(5000)).Err
}

type runnerNotifier struct{ run RunFunc }

func (r runnerNotifier) Notify(title, body string) error { return r.run("notify-send", title, body) }

// New returns a dbus Notifier; falls back to notify-send if dbus is unavailable.
func New() Notifier {
	if _, err := dbus.SessionBusPrivate(); err == nil {
		return dbusNotifier{}
	}
	return NewWithRunner(func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	})
}

// NewWithRunner returns a Notifier backed by the injected runner (test seam).
func NewWithRunner(run RunFunc) Notifier { return runnerNotifier{run: run} }
