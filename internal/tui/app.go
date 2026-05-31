package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	// borderSize is the number of cells consumed by a rounded border on one side.
	borderSize = 1
	// footerHeight is the number of terminal rows reserved for the footer hint bar.
	footerHeight = 1
)

// Model is the root Bubble Tea model for the perch TUI.
// It composes a list (left pane) and a viewport (right pane).
type Model struct {
	list    list.Model
	preview viewport.Model
	keys    keyMap
	width   int
	height  int
	ready   bool
}

// New returns a Model with the given items pre-loaded.
// Width and height start at zero; they are updated by the first tea.WindowSizeMsg.
func New(items []list.Item) Model {
	l := list.New(items, itemDelegate{}, 0, 0)
	// Disable the built-in quit binding so our own Quit key is the only exit.
	l.KeyMap.Quit.SetEnabled(false)
	l.KeyMap.ForceQuit.SetEnabled(false)
	// Use a plain title so the height calculation stays simple.
	l.Title = "Sessions"

	return Model{
		list:    l,
		preview: viewport.New(0, 0),
		keys:    defaultKeys(),
	}
}

// Init satisfies tea.Model. No initial command needed for scaffold.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles all incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Reserve 2 cells per border side + footer row.
		listWidth := msg.Width * 30 / 100
		previewWidth := msg.Width - listWidth
		paneHeight := msg.Height - footerHeight - 2*borderSize

		m.list.SetWidth(listWidth - 2*borderSize)
		m.list.SetHeight(paneHeight)
		m.preview.Width = previewWidth - 2*borderSize
		m.preview.Height = paneHeight
		m.ready = true

		m.preview.SetContent(m.detailContent())
		return m, nil

	case tea.KeyMsg:
		// When the list is in filter mode let it handle all keys first so the
		// text input receives characters and the filter can be accepted/cancelled.
		if m.list.SettingFilter() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			m.preview.SetContent(m.detailContent())
			return m, cmd
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit

		case key.Matches(msg, m.keys.Filter):
			// Delegate '/' to the list so it enters filtering mode.
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd

		case key.Matches(msg, m.keys.ClearFilter):
			// Esc resets the filter when not in active filtering mode.
			m.list.ResetFilter()
			m.preview.SetContent(m.detailContent())
			return m, nil

		case key.Matches(msg, m.keys.Enter):
			// TODO(M5-4): launch or attach to the selected session.
			return m, nil

		case key.Matches(msg, m.keys.New):
			// TODO(M5-4): launch a new session in the selected tree.
			return m, nil
		}
	}

	// Delegate all other messages (j/k navigation, pagination, filter ticking…)
	// to the list component.
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.preview.SetContent(m.detailContent())
	return m, cmd
}

// View renders the full TUI as a single string.
func (m Model) View() string {
	if !m.ready {
		return "Initialising…"
	}

	leftPane := styles.leftPane.Render(m.list.View())
	rightPane := styles.rightPane.Render(m.preview.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	footer := styles.footer.Render(
		"↵ switch · n new · / filter · q quit",
	)

	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

// detailContent builds the preview pane text for the currently selected item.
// It only uses item fields — no tmux capture yet (that is M5-3).
func (m *Model) detailContent() string {
	sel := m.list.SelectedItem()
	if sel == nil {
		return "(no selection)"
	}
	it, ok := sel.(item)
	if !ok {
		return "(unknown item type)"
	}
	return fmt.Sprintf(
		"Title:   %s\nTool:    %s\nStatus:  %s\nUpdated: %s\n",
		it.title,
		it.tool,
		it.status.glyph(),
		it.relTime,
	)
}
