package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestComposite_PreservesUncoveredLines(t *testing.T) {
	bg := "aaaa\nbbbb\ncccc\ndddd"
	fg := "XX"
	out := composite(bg, fg, 1, 1) // place "XX" at row 1, col 1
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("line count = %d, want 4", len(lines))
	}
	if lines[0] != "aaaa" || lines[2] != "cccc" || lines[3] != "dddd" {
		t.Errorf("uncovered lines changed: %q", lines)
	}
	if got := ansi.Strip(lines[1]); got != "bXXb" {
		t.Errorf("composited row = %q, want \"bXXb\"", got)
	}
}

func TestComposite_WideRuneOffsetIsCellBased(t *testing.T) {
	// Backdrop row has a width-2 emoji: cells → [🤖][🤖](0,1) [sp](2) [a](3) [b](4) [c](5)
	bg := "🤖 abc"
	fg := "|"
	out := composite(bg, fg, 4, 0) // overlay at CELL column 4 (the 'b')
	line := ansi.Strip(strings.Split(out, "\n")[0])
	idx := strings.Index(line, "|")
	if idx < 0 {
		t.Fatalf("overlay char not found in %q", line)
	}
	// The visible width BEFORE the overlay char must be exactly 4 cells.
	if w := ansi.StringWidth(line[:idx]); w != 4 {
		t.Errorf("overlay landed at cell width %d, want 4 (rune/byte slicing bug?)", w)
	}
}

func TestComposite_ClipsOverflowingFg(t *testing.T) {
	bg := "abcd\nefgh"
	fg := "WIDE" // 4 cells, placed at col 2 on a 4-cell bg → must clip to "WI"
	out := composite(bg, fg, 2, 0)
	first := strings.Split(out, "\n")[0]
	if w := ansi.StringWidth(first); w != 4 {
		t.Fatalf("composited width = %d, want 4 (fg must clip to bg width, not overflow)", w)
	}
	if got := ansi.Strip(first); got != "abWI" {
		t.Errorf("clipped row = %q, want \"abWI\"", got)
	}
}
