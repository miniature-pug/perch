package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Miniature-Pug/perch/internal/tmux"
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

	// loader is optional; when set, Init returns its load Cmd.
	loader *loader

	// loadErr holds the last whole-load failure message for display in the UI.
	// Empty string means no error. Cleared on successful reload.
	loadErr string

	// launchErr holds the last launch/attach failure message. Separate from
	// loadErr so the two categories can be displayed distinctly.
	launchErr string

	// previewContent holds the current text shown in the preview pane.
	// Stored separately from the viewport so tests can assert without rendering.
	previewContent string

	// capturing is true while a CapturePane call is in-flight.
	// It prevents overlapping capture commands.
	capturing bool
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

// WithLoader returns a copy of m with the given loader wired in.
// Init will then return the load Cmd automatically.
func (m Model) WithLoader(l loader) Model {
	m.loader = &l
	return m
}

// Init satisfies tea.Model. When a loader is configured it fires the initial
// data load; otherwise it does nothing (scaffold / test mode).
func (m Model) Init() tea.Cmd {
	if m.loader != nil {
		return m.loader.load()
	}
	return nil
}

// Update handles all incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case itemsLoadedMsg:
		if msg.err == nil {
			m.loadErr = ""
			m.list.SetItems(msg.items)
		} else {
			m.loadErr = msg.err.Error()
		}
		// Refresh the preview for the newly-selected item.
		return m, m.previewCmd()

	case launchedMsg:
		if msg.err != nil {
			m.launchErr = msg.err.Error()
			return m, nil
		}
		m.launchErr = ""
		target := tmux.WindowTarget(msg.session, msg.window)
		return m.attachTo(target)

	case switchedMsg:
		if msg.err != nil {
			m.launchErr = msg.err.Error()
		} else {
			m.launchErr = ""
		}
		return m, nil

	case attachFinishedMsg:
		if msg.err != nil {
			m.launchErr = msg.err.Error()
		} else {
			m.launchErr = ""
		}
		// Reloading the list after detach is deferred.
		return m, nil

	case previewMsg:
		m.capturing = false
		sel, ok := m.selectedItem()
		if ok && msg.target == sel.captureTarget {
			// Matching target: apply the content and stop.
			m.previewContent = msg.content
			m.preview.SetContent(msg.content)
			return m, nil
		}
		// Stale target: the user navigated during the in-flight capture.
		// Re-fire a capture for the now-current selection.
		return m, m.previewCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Reserve 2 cells per border side + footer row.
		listWidth := msg.Width * 30 / 100
		previewWidth := msg.Width - listWidth
		paneHeight := msg.Height - footerHeight - 2*borderSize

		// Clamp all derived dimensions to ≥ 0 so subcomponents never receive
		// negative sizes on very small terminals.
		m.list.SetWidth(max(0, listWidth-2*borderSize))
		m.list.SetHeight(max(0, paneHeight))
		m.preview.Width = max(0, previewWidth-2*borderSize)
		m.preview.Height = max(0, paneHeight)
		m.ready = true

		return m, m.previewCmd()

	case tea.KeyMsg:
		// When the list is in filter mode let it handle all keys first so the
		// text input receives characters and the filter can be accepted/cancelled.
		if m.list.SettingFilter() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			m.refreshStaticPreview()
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
			return m, m.previewCmd()

		case key.Matches(msg, m.keys.Enter):
			it, ok := m.selectedItem()
			if !ok || !it.isSession {
				return m, nil
			}
			if it.live && it.liveTarget != "" {
				// Switch to the existing window — NEVER relaunch a live session:
				// concurrent --resume can corrupt the shared transcript.
				return m.attachTo(it.liveTarget)
			}
			return m, m.launchCmd(launchSpec{
				tool:        it.tool,
				sessionID:   it.id,
				branch:      it.tree,
				treePath:    it.treePath,
				projectPath: it.projectPath,
				resume:      true,
			})

		case key.Matches(msg, m.keys.New):
			it, ok := m.selectedItem()
			if !ok {
				// Need a tree context; no-op without a selection
				// (tool/model picker is deferred to M9).
				return m, nil
			}
			return m, m.launchCmd(launchSpec{
				tool:        it.tool,
				branch:      it.tree,
				treePath:    it.treePath,
				projectPath: it.projectPath,
				resume:      false,
			})
		}
	}

	// Delegate all other messages (j/k navigation, pagination, filter ticking…)
	// to the list component, then refresh the preview for the new selection.
	var cmd tea.Cmd
	prevIdx := m.list.Index()
	m.list, cmd = m.list.Update(msg)

	// If the selection changed, update the preview.
	if m.list.Index() != prevIdx {
		return m, tea.Batch(cmd, m.previewCmd())
	}
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

	if m.launchErr != "" {
		errBar := styles.errorBar.Render("Launch failed: " + m.launchErr)
		return lipgloss.JoinVertical(lipgloss.Left, errBar, body, footer)
	}

	if m.loadErr != "" {
		errBar := styles.errorBar.Render("Error loading sessions: " + m.loadErr)
		return lipgloss.JoinVertical(lipgloss.Left, errBar, body, footer)
	}

	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

// selectedItem returns the currently selected list item as an item, or false.
func (m *Model) selectedItem() (item, bool) {
	sel := m.list.SelectedItem()
	if sel == nil {
		return item{}, false
	}
	it, ok := sel.(item)
	return it, ok
}

// previewCmd returns the appropriate tea.Cmd for the currently selected item:
//   - live item with a capture target: fires a CapturePane call (gated by capturing).
//   - idle item or no selection: refreshes the static detail view inline (no cmd).
func (m *Model) previewCmd() tea.Cmd {
	sel, ok := m.selectedItem()
	if !ok {
		m.refreshStaticPreview()
		return nil
	}

	if sel.live && sel.captureTarget != "" {
		if m.capturing {
			// An in-flight capture is already running; don't stack another.
			return nil
		}
		if m.loader == nil {
			// No loader (test / scaffold mode): show static detail.
			m.refreshStaticPreview()
			return nil
		}
		m.capturing = true
		target := sel.captureTarget
		ldr := m.loader
		return func() tea.Msg {
			content, err := ldr.Tmux.CapturePane(context.Background(), target, 0)
			if err != nil {
				// Degrade to empty on error; don't abort or panic.
				content = ""
			}
			return previewMsg{content: content, target: target}
		}
	}

	// Idle item: static detail, no async call.
	m.refreshStaticPreview()
	return nil
}

// refreshStaticPreview updates the preview viewport with static detail content
// for the current selection. Called for idle items and when no loader is set.
func (m *Model) refreshStaticPreview() {
	content := m.detailContent()
	m.previewContent = content
	m.preview.SetContent(content)
}

// detailContent builds the preview pane text for the currently selected item.
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
