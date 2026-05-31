// Package tui implements the keyboard-first two-pane TUI for perch.
package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap holds all key bindings used by the root model.
type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	Filter      key.Binding
	ClearFilter key.Binding
	Enter       key.Binding
	New         key.Binding
	Quit        key.Binding
}

// defaultKeys returns the standard key map for perch.
func defaultKeys() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("↓/j", "down"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		ClearFilter: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "clear filter"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("↵", "switch"),
		),
		New: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "new"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
	}
}
