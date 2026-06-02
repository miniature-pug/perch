package tui

import "github.com/charmbracelet/lipgloss"

// defaultAccent is the TUI fallback accent colour when no [theme].accent is
// configured. This is a TUI-local constant; the config package has its own
// identical constant (unexported, not shared — see M15 for centralisation).
const defaultAccent = "#EE6FF8"

// Palette uses AdaptiveColor struct literals (lipgloss v1: self-detecting
// light/dark, no renderer argument needed).
var (
	colorSubtle = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	colorNormal = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#DDDDDD"}
	colorError  = lipgloss.AdaptiveColor{Light: "#D70000", Dark: "#FF5F5F"}
	// colorMuted is a readable dim used for secondary TEXT (footer, empty-state).
	// Distinct from colorSubtle, which is intentionally near-background for BORDERS.
	colorMuted = lipgloss.AdaptiveColor{Light: "#6C6C6C", Dark: "#999999"}

	// Status indicator colours — single source of truth; referenced by statusStyle.
	colorStatusWorking = lipgloss.AdaptiveColor{Light: "#0077CC", Dark: "#5FD7FF"}
	colorStatusWaiting = lipgloss.AdaptiveColor{Light: "#CC8800", Dark: "#FFD75F"}
	colorStatusDone    = lipgloss.AdaptiveColor{Light: "#007700", Dark: "#5FFF5F"}
	colorStatusLive    = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#AAAAAA"}
)

// styles holds the pre-built lipgloss styles used throughout the TUI.
// Accent-dependent styles (selectedRow, helpOverlay, modal box) are NOT here —
// they live on the Model's theme field so each instance can carry a different
// configured accent without global mutation.
var styles = struct {
	// leftPane is the container style for the list pane.
	leftPane lipgloss.Style
	// rightPane is the container style for the preview pane.
	rightPane lipgloss.Style
	// footer is the hint bar rendered below both panes.
	footer lipgloss.Style
	// dimRow renders non-selected rows.
	dimRow lipgloss.Style
	// errorBar renders a whole-load error message above the main body.
	errorBar lipgloss.Style
	// toast renders a transient, non-blocking message bar above the body.
	toast lipgloss.Style
	// emptyState renders the "no repositories" hint in place of the body.
	emptyState lipgloss.Style
	// dimmedBody renders the stripped body behind a modal/help overlay.
	dimmedBody lipgloss.Style
}{
	leftPane: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorSubtle),
	rightPane: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorSubtle),
	footer: lipgloss.NewStyle().
		Foreground(colorMuted).
		MarginTop(0),
	dimRow: lipgloss.NewStyle().
		Foreground(colorNormal),
	errorBar: lipgloss.NewStyle().
		Foreground(colorError).
		Bold(true),
	toast: lipgloss.NewStyle().
		Foreground(colorError).
		Bold(true),
	emptyState: lipgloss.NewStyle().
		Foreground(colorMuted).
		Padding(1, 2),
	dimmedBody: lipgloss.NewStyle().Foreground(colorSubtle),
}

// theme holds the accent-derived instance-level styles for one Model.
// Built by newTheme from the configured accent colour; immutable after build.
type theme struct {
	// selectedRow highlights the active list row in the configured accent colour.
	selectedRow lipgloss.Style
	// helpOverlay renders the ? full-help popup box border in the accent colour.
	helpOverlay lipgloss.Style
	// modalBox renders the modal prompt box border in the accent colour.
	modalBox lipgloss.Style
}

// newTheme builds a theme from a hex accent string (e.g. "#EE6FF8").
// A single hex colour is used for both light and dark terminals — this is
// intentional for a user-chosen accent. The fallback is defaultAccent.
func newTheme(accent string) theme {
	if accent == "" {
		accent = defaultAccent
	}
	c := lipgloss.Color(accent)
	return theme{
		selectedRow: lipgloss.NewStyle().
			Foreground(c).
			Bold(true),
		helpOverlay: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c).
			Padding(1, 2),
		modalBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c).
			Padding(0, 1),
	}
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
		return lipgloss.NewStyle().Foreground(colorStatusWorking)
	case StatusWaiting:
		return lipgloss.NewStyle().Foreground(colorStatusWaiting)
	case StatusDone:
		return lipgloss.NewStyle().Foreground(colorStatusDone)
	case StatusLive:
		return lipgloss.NewStyle().Foreground(colorStatusLive)
	default: // StatusIdle
		return lipgloss.NewStyle().Foreground(colorSubtle)
	}
}
