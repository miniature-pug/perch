package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/godbus/dbus/v5"
)

func main() {
	fmt.Println("Testing D-Bus org.freedesktop.Notifications ...")
	fmt.Println("Unfocus this terminal, then watch for a desktop notification.")
	time.Sleep(3 * time.Second) // give user time to unfocus

	if err := notifyDBus("perch spike 5", "D-Bus path: notification from org.freedesktop.Notifications"); err != nil {
		fmt.Println("D-Bus notify failed:", err)
		fmt.Println("Falling back to notify-send ...")
		if err2 := notifySend("perch spike 5", "notify-send fallback path"); err2 != nil {
			fmt.Println("notify-send also failed:", err2)
			os.Exit(1)
		}
		fmt.Println("notify-send succeeded.")
		return
	}
	fmt.Println("D-Bus notify succeeded.")
}

func notifyDBus(title, body string) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("dbus.SessionBus: %w", err)
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	// Notify(app_name, replaces_id, icon, summary, body, actions, hints, expire_timeout)
	call := obj.Call(
		"org.freedesktop.Notifications.Notify", 0,
		"perch",       // app_name
		uint32(0),     // replaces_id (0 = new)
		"dialog-info", // icon
		title,         // summary
		body,          // body
		[]string{},    // actions
		map[string]dbus.Variant{}, // hints
		int32(5000),   // expire_timeout ms
	)
	return call.Err
}

func notifySend(title, body string) error {
	cmd := exec.Command("notify-send", "-t", "5000", title, body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
