package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// Status describes the agent activity state of a session row.
type Status int

const (
	StatusWorking Status = iota // agent is actively running
	StatusWaiting               // agent is awaiting user input
	StatusDone                  // session completed
	StatusIdle                  // no session running (placeholder row)
)

// glyph returns the display glyph for the status.
func (s Status) glyph() string {
	switch s {
	case StatusWorking:
		return glyphWorking
	case StatusWaiting:
		return glyphWaiting
	case StatusDone:
		return glyphDone
	default:
		return glyphIdle
	}
}

// item is a single row in the selector list.
// It implements list.Item.
type item struct {
	// project is the repo/project name this session belongs to.
	// Populated by live data (M5-3+); empty for scaffold fixtures.
	project string
	// tree is the worktree or branch identifier for this session.
	// Populated by live data (M5-3+); empty for scaffold fixtures.
	tree string
	// title is the session/project title shown in the list.
	title string
	// tool identifies which agent (claude/opencode) runs this session.
	tool string
	// status is the agent activity state.
	status Status
	// relTime is the human-readable relative timestamp (e.g. "2m ago").
	relTime string
	// isSession is true when the row represents a live or completed session.
	// false indicates a "start new" placeholder row.
	isSession bool
	// id is the session identifier (agent-assigned).
	id string
	// live is true when a live tmux pane is attached to this session.
	live bool
	// captureTarget is the tmux pane ID to capture for preview (empty when idle).
	captureTarget string
}

// FilterValue returns the fuzzy-search key: project + tree + title + tool.
// Concatenating all four lets the user filter by any combination, e.g.
// "myrepo main foo claude" or simply "foo".
func (i item) FilterValue() string {
	return i.project + " " + i.tree + " " + i.title + " " + i.tool
}

// itemDelegate renders each list row with a cursor marker, status glyph, tool
// label, title, and relative time.
type itemDelegate struct{}

// Height returns how many terminal rows a single item occupies.
func (d itemDelegate) Height() int { return 1 }

// Spacing returns the number of blank rows between items.
func (d itemDelegate) Spacing() int { return 0 }

// Update handles item-level messages; nothing needed for scaffold.
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

// Render writes the item row to w.
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}

	cursor := glyphBlank + " "
	if index == m.Index() {
		cursor = glyphCursor + " "
	}

	row := fmt.Sprintf("%s%s %-9s %-40s %s",
		cursor,
		it.status.glyph(),
		it.tool,
		it.title,
		it.relTime,
	)

	if index == m.Index() {
		_, _ = fmt.Fprint(w, styles.selectedRow.Render(row))
	} else {
		_, _ = fmt.Fprint(w, styles.dimRow.Render(row))
	}
}
