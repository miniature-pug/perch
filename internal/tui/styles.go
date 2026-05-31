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
