// export_test.go exposes internal monitor state for white-box tests only.
// This file is compiled only during `go test`.
package agent

import "context"

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

// MarkExitedForTest arms the opencode monitor's terminal exited guard exactly as
// handleExit does (sets exited + StateExited under the lock, WITHOUT emitting the
// terminal event). It lets a test place the monitor in the post-exit state and then
// drive commitSSE directly to prove section B's re-check. Returns markExited's arm
// result (false if already exited).
func (m *OpencodeMonitor) MarkExitedForTest() bool { return m.markExited() }

// CommitSSEForTest drives translateSSE's post-check write half (section B: commitSSE)
// directly. It reproduces the check-then-act (TOCTOU) window the single section-A
// check missed — handleExit landing AFTER section A saw exited==false (so the frame
// was already let through) but BEFORE section B writes m.state. Feeding a pre-computed
// running ev here with exited already armed proves section B RE-CHECKS exited and
// drops the frame, a guarantee section A alone cannot give.
func (m *OpencodeMonitor) CommitSSEForTest(ctx context.Context, ev Event) { m.commitSSE(ctx, ev) }
