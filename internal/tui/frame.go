package tui

// frame.go holds the swap-pane state machine for the persistent-frame switcher
// (M11-0). The perch frame is one tmux window with two panes: the sidebar (this
// TUI) and a main slot that displays the selected agent live. Exactly one
// disposable "placeholder" pane exists for the frame's lifetime: it sits in the
// main slot when nothing is displayed, otherwise it parks in the home session of
// the agent currently shown. Switching agents never restarts them — it only moves
// panes with swap-pane. All panes are addressed by stable pane id (%N), never by
// session:window coordinate (which is momentarily stale mid-swap).

// swapOp is a single `tmux swap-pane -s <src> -t <dst>` exchange.
type swapOp struct {
	src string
	dst string
}

// planSwapIn returns the swap-pane ops to display the agent whose home pane is
// targetHome, given the pane currently displayed (displayed, "" if none) and the
// placeholder pane id.
//
//   - target already displayed            → no ops (no-op).
//   - nothing displayed                    → one swap: targetHome ↔ placeholder
//     (placeholder is in the frame main slot; after the swap targetHome occupies
//     the frame and the placeholder migrates to targetHome's home session).
//   - another agent displayed              → two swaps: first send the displayed
//     agent home (displayed ↔ placeholder, which is parked in the displayed
//     agent's home session), then bring the target in (targetHome ↔ placeholder,
//     now back in the frame).
//
// After applying the returned ops the caller sets displayed = targetHome; the
// placeholder pane id is unchanged (only its location moves).
func planSwapIn(displayed, placeholder, targetHome string) []swapOp {
	if targetHome == "" || targetHome == displayed {
		return nil
	}
	if displayed == "" {
		return []swapOp{{src: targetHome, dst: placeholder}}
	}
	return []swapOp{
		{src: displayed, dst: placeholder},
		{src: targetHome, dst: placeholder},
	}
}

// planSwapHome returns the swap-pane op that returns the currently displayed
// agent to its home session, leaving the placeholder back in the frame main slot.
// This MUST run before the frame session is killed on quit, or the displayed
// agent's process dies with the frame (data loss). No ops when nothing is shown.
// After applying it the caller sets displayed = "".
func planSwapHome(displayed, placeholder string) []swapOp {
	if displayed == "" {
		return nil
	}
	return []swapOp{{src: displayed, dst: placeholder}}
}
