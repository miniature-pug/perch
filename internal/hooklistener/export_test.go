// export_test.go exposes internal Listener state for white-box tests only.
// This file is compiled only during `go test`.
package hooklistener

import "time"

// FillEventsBuffer directly writes n filler HookEvent values into the
// events channel, saturating it. Panics if n exceeds the channel capacity.
// For use by TestStopEventNotDroppedUnderBackpressure only.
func (l *Listener) FillEventsBuffer(n int) {
	for i := 0; i < n; i++ {
		l.events <- HookEvent{Type: "SessionStart", SessionID: "filler"}
	}
}

// EventsCap returns the capacity of the events channel.
func (l *Listener) EventsCap() int { return cap(l.events) }

// ServerTimeouts exposes the http.Server's configured timeouts so a test can
// assert the slowloris-hardening deadlines are set. WriteTimeout is included so
// a test can verify it stays 0 (unbounded) — a finite WriteTimeout would abort a
// blocking PreToolUse approval.
func (l *Listener) ServerTimeouts() (readHeader, read, write, idle time.Duration) {
	return l.srv.ReadHeaderTimeout, l.srv.ReadTimeout, l.srv.WriteTimeout, l.srv.IdleTimeout
}
