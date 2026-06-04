// export_test.go exposes internal Listener state for white-box tests only.
// This file is compiled only during `go test`.
package hooklistener

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
