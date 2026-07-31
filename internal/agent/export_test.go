// export_test.go exposes internal monitor state for white-box tests only.
// This file is compiled only during `go test`.
package agent

// FillEvents writes n filler Event values into the monitor's events channel,
// saturating it. Panics if n exceeds the channel capacity. For use by the
// backpressure regression test that verifies Approve does not silently drop the
// awaiting-approval clearing event when the channel is momentarily full.
func (m *ClaudeMonitor) FillEvents(n int) {
	for i := 0; i < n; i++ {
		m.events <- Event{Kind: "filler"}
	}
}

// EventsCap returns the capacity of the monitor's events channel.
func (m *ClaudeMonitor) EventsCap() int { return cap(m.events) }
