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

// numModes is the count of screen modes; next/prev wrap modulo this.
const numModes screenMode = 3

// next returns the next mode, wrapping after modeFullPreview.
func (s screenMode) next() screenMode { return (s + 1) % numModes }

// prev returns the previous mode, wrapping before modeNormal.
func (s screenMode) prev() screenMode { return (s + numModes - 1) % numModes }
