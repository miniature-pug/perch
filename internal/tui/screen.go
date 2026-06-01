package tui

// screenMode selects the pane layout. z cycles forward, Z backward.
type screenMode int

const (
	modeNormal      screenMode = iota // two panes (or vertical stack when narrow)
	modeFullList                      // selector fills the width
	modeFullPreview                   // preview fills the width
)

// minWideWidth is the terminal width below which modeNormal stacks the panes
// vertically instead of side-by-side.
const minWideWidth = 80

// next returns the next mode, wrapping after modeFullPreview.
func (s screenMode) next() screenMode { return (s + 1) % 3 }

// prev returns the previous mode, wrapping before modeNormal.
func (s screenMode) prev() screenMode { return (s + 2) % 3 }
