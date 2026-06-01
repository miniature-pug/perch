package tui

import "github.com/charmbracelet/lipgloss"

// Palette uses AdaptiveColor struct literals (lipgloss v1: self-detecting
// light/dark, no renderer argument needed).
var (
	colorSubtle = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	colorAccent = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	colorNormal = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#DDDDDD"}
	colorError  = lipgloss.AdaptiveColor{Light: "#D70000", Dark: "#FF5F5F"}
)

// styles holds the pre-built lipgloss styles used throughout the TUI.
var styles = struct {
	// leftPane is the container style for the list pane.
	leftPane lipgloss.Style
	// rightPane is the container style for the preview pane.
	rightPane lipgloss.Style
	// footer is the hint bar rendered below both panes.
	footer lipgloss.Style
	// selectedRow highlights the active list row.
	selectedRow lipgloss.Style
	// dimRow renders non-selected rows.
	dimRow lipgloss.Style
	// errorBar renders a whole-load error message above the main body.
	errorBar lipgloss.Style
	// toast renders a transient, non-blocking message bar above the body.
	toast lipgloss.Style
	// emptyState renders the "no repositories" hint in place of the body.
	emptyState lipgloss.Style
	// helpOverlay renders the ? full-help popup box.
	helpOverlay lipgloss.Style
}{
	leftPane: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorSubtle),
	rightPane: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorSubtle),
	footer: lipgloss.NewStyle().
		Foreground(colorSubtle).
		MarginTop(0),
	selectedRow: lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true),
	dimRow: lipgloss.NewStyle().
		Foreground(colorNormal),
	errorBar: lipgloss.NewStyle().
		Foreground(colorError).
		Bold(true),
	toast: lipgloss.NewStyle().
		Foreground(colorError).
		Bold(true),
	emptyState: lipgloss.NewStyle().
		Foreground(colorSubtle).
		Padding(1, 2),
	helpOverlay: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2),
}

// Status glyphs — kept here so delegate and item are in the same file space.
const (
	glyphWorking = "🤖"
	glyphWaiting = "💬"
	glyphDone    = "✓"
	glyphIdle    = "○"
	glyphLive    = "●" // neutral "attached/live" glyph; distinct from Working (🤖)
	glyphCursor  = "▸"
	glyphBlank   = " "
)

// statusStyle returns a lipgloss.Style whose foreground reflects the given
// Status. working=cyan/blue, waiting=amber/yellow, done=green, live=neutral
// grey, idle=subtle. Applied only to the glyph segment — never to the rest of
// the row — so selectedRow/dimRow can colour text independently.
func statusStyle(s Status) lipgloss.Style {
	switch s {
	case StatusWorking:
		return lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#0077CC", Dark: "#5FD7FF"})
	case StatusWaiting:
		return lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#CC8800", Dark: "#FFD75F"})
	case StatusDone:
		return lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#007700", Dark: "#5FFF5F"})
	case StatusLive:
		return lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#AAAAAA"})
	default: // StatusIdle
		return lipgloss.NewStyle().Foreground(colorSubtle)
	}
}
