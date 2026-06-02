package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// composite overlays fg onto bg with fg's top-left corner at cell column x and
// row y. All horizontal math is in terminal cells (ANSI-aware) so wide runes
// (emoji) in bg do not shift the overlay. Rows of bg not covered by fg pass
// through unchanged; on covered rows, bg's left part (cells [0,x)) is kept, the
// fg line is spliced in, and bg's right part (cells [x+fgWidth, bgWidth)) is
// appended. fg is clipped to bg's width so the result never grows wider than bg
// — preserving View's height/width invariants on a short terminal.
func composite(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	for i, fgLine := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue // fg row outside bg vertical bounds → skip
		}
		bgLine := bgLines[row]
		bgWidth := ansi.StringWidth(bgLine)
		if x >= bgWidth {
			continue // overlay starts past the line's content → leave bg as-is
		}

		fgWidth := ansi.StringWidth(fgLine)

		// Left part: bg cells [0, x). Pad with spaces if bg is shorter than x.
		left := ansi.Cut(bgLine, 0, x)
		if lw := ansi.StringWidth(left); lw < x {
			left += strings.Repeat(" ", x-lw)
		}

		// Clip fg so left+fg never exceeds bg width.
		if x+fgWidth > bgWidth {
			fgLine = ansi.Truncate(fgLine, bgWidth-x, "")
			fgWidth = ansi.StringWidth(fgLine)
		}

		// Right part: bg cells [x+fgWidth, bgWidth). Empty when fg reaches the edge.
		right := ansi.Cut(bgLine, x+fgWidth, bgWidth)

		// Reset SGR around the fg splice so bg color codes don't bleed into fg
		// and vice versa. ansi.Cut re-opens active styles for `right` itself.
		bgLines[row] = left + ansi.ResetStyle + fgLine + ansi.ResetStyle + right
	}
	return strings.Join(bgLines, "\n")
}
