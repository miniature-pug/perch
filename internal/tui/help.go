package tui

import "github.com/charmbracelet/bubbles/key"

// ShortHelp returns the contextual footer bindings for the current selection,
// satisfying the bubbles/help KeyMap interface. Selection-dependent keys
// (open/worktree/remove/kill) are included only when the action is valid for the
// highlighted row, so the footer never advertises a key its handler rejects.
func (m Model) ShortHelp() []key.Binding {
	var b []key.Binding
	it, ok := m.selectedItem()
	if ok && it.isSession {
		b = append(b, m.keys.Enter)
	}
	b = append(b, m.keys.New)
	if ok && it.isSession {
		b = append(b, m.keys.Worktree)
		if !it.isMain {
			b = append(b, m.keys.Remove)
		}
	}
	if ok && it.live && it.liveTarget != "" {
		b = append(b, m.keys.Kill)
	}
	b = append(b, m.keys.Filter, m.keys.ScreenFwd, m.keys.Help, m.keys.Quit)
	return b
}

// FullHelp returns the complete keybinding reference shown in the ? overlay,
// grouped into columns, satisfying the bubbles/help KeyMap interface.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.keys.Enter, m.keys.New, m.keys.Worktree},
		{m.keys.Remove, m.keys.Kill},
		{m.keys.Filter, m.keys.ClearFilter},
		{m.keys.ScreenFwd, m.keys.ScreenBack, m.keys.Help, m.keys.Quit},
	}
}
