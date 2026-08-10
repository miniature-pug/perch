// Package safe holds small helpers that keep long-lived goroutines from taking
// the whole process down. A panic in a background goroutine (an event pump, a
// filesystem watcher, a pty reaper) is otherwise fatal: Go has no way to catch
// it from outside the goroutine, so the runtime crashes the app.
package safe

import (
	"log"
	"runtime/debug"
)

// Recover recovers a panicking goroutine, logging the panic value and stack
// trace prefixed with label so the goroutine exits cleanly instead of crashing
// the process. Use it as the first deferred call at the top of every long-lived
// background goroutine:
//
//	go func() {
//		defer safe.Recover("event-pump")
//		// ...
//	}()
func Recover(label string) {
	if r := recover(); r != nil {
		log.Printf("safe: goroutine %q recovered from panic: %v\n%s", label, r, debug.Stack())
	}
}
