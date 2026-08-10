// export_test.go exposes internal monitor state for white-box tests only.
// The `go test` command compiles this file only during a test build.
package agent

import "context"

// FillEvents writes n filler Event values into the monitor's events channel,
// filling it to capacity. FillEvents panics if n exceeds the channel
// capacity. The backpressure regression test uses this method to check that
// Approve does not silently drop the awaiting-approval clearing event when
// the channel is momentarily full.
func (m *ClaudeMonitor) FillEvents(n int) {
	for i := 0; i < n; i++ {
		m.events <- Event{Kind: "filler"}
	}
}

// EventsCap returns the capacity of the monitor's events channel.
func (m *ClaudeMonitor) EventsCap() int { return cap(m.events) }

// MarkExitedForTest arms the opencode monitor's terminal exited guard exactly
// as handleExit does: it sets exited and StateExited under the lock, without
// emitting the terminal event. A test can use it to place the monitor in the
// post-exit state, then drive commitSSE directly to prove section B's
// re-check. MarkExitedForTest returns markExited's arm result: false if the
// monitor was already exited.
func (m *OpencodeMonitor) MarkExitedForTest() bool { return m.markExited() }

// CommitSSEForTest drives the post-check write half of translateSSE (section
// B: commitSSE) directly. It reproduces the check-then-act (TOCTOU) window
// that the single section-A check misses: handleExit lands after section A
// sees exited==false (so section A already lets the frame through), but
// before section B writes m.state. A test can feed a pre-computed running ev
// here with exited already armed. This proves section B RE-CHECKS exited
// and drops the frame, a guarantee section A alone cannot give.
func (m *OpencodeMonitor) CommitSSEForTest(ctx context.Context, ev Event) { m.commitSSE(ctx, ev) }
