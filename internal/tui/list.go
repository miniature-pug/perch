package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Miniature-Pug/perch/internal/status"
)

// Status describes the agent activity state of a session row.
type Status int

const (
	StatusWorking Status = iota // agent is actively running
	StatusWaiting               // agent is awaiting user input
	StatusDone                  // session completed
	StatusIdle                  // no session running (placeholder row)
	StatusLive                  // tmux pane attached/live; real working/waiting/done needs @perch_status (later milestone)
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
	case StatusLive:
		return glyphLive
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
	// tree is the branch name for this session's working tree.
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
	// projectPath is the absolute path to the repository root — used for SessionName + frecency.
	projectPath string
	// treePath is the absolute working directory — used as Launch dir and SaveWindow.Tree.
	treePath string
	// liveTarget is the pre-built WindowTarget of the live pane's window; empty when idle.
	liveTarget string
	// isMain is true for the project's main checkout (never removable, §7.2).
	isMain bool
}

// FilterValue returns the fuzzy-search key: project + tree + title + tool.
// Concatenating all four lets the user filter by any combination, e.g.
// "myrepo main foo claude" or simply "foo".
func (i item) FilterValue() string {
	return i.project + " " + i.tree + " " + i.title + " " + i.tool
}

// rowTruncStyle truncates a rendered list row to the pane width. MaxWidth returns
// a copy, so this shared base style is never mutated.
var rowTruncStyle = lipgloss.NewStyle()

// itemDelegate renders each list row with a cursor marker, status glyph, tool
// label, title, and relative time.
// selectedRow is the accent-derived style for the highlighted row; it is set
// from the Model's theme in New (default accent) and updated in WithLoader when
// the configured accent is resolved. Zero-value renders without accent styling.
type itemDelegate struct {
	selectedRow lipgloss.Style
}

// Height returns how many terminal rows a single item occupies.
func (d itemDelegate) Height() int { return 1 }

// Spacing returns the number of blank rows between items.
func (d itemDelegate) Spacing() int { return 0 }

// Update handles item-level messages; nothing needed for scaffold.
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

// Render writes the item row to w.
//
// The status glyph is rendered in its own colour segment so that
// selectedRow/dimRow (which set Foreground) do not override the glyph colour.
// Column widths are preserved: %-9s tool, %-40s title, %s relTime.
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}

	cursor := glyphBlank + " "
	if index == m.Index() {
		cursor = glyphCursor + " "
	}

	glyph := statusStyle(it.status).Render(it.status.glyph())
	rest := fmt.Sprintf("%-9s %-40s %s", it.tool, it.title, it.relTime)

	var rowStyle lipgloss.Style
	if index == m.Index() {
		rowStyle = d.selectedRow
	} else {
		rowStyle = styles.dimRow
	}

	row := cursor + glyph + " " + rowStyle.Render(rest)
	// Truncate to the list's allocated width so a long title can't overflow the
	// pane (the %-40s padding would otherwise make every row ~50 cols regardless
	// of layout). Guard width==0 (unsized list in tests) to avoid blanking rows.
	if width := m.Width(); width > 0 {
		row = rowTruncStyle.MaxWidth(width).Render(row)
	}
	_, _ = fmt.Fprint(w, row)
}

// statusFromOption maps the raw @perch_pane_status option value to a Status.
// live controls what is returned for unset/unknown values: when true the pane is
// known-live but hasn't reported yet (StatusLive); when false there is no live
// pane (StatusIdle).
func statusFromOption(opt string, live bool) Status {
	switch opt {
	case status.StateWorking:
		return StatusWorking
	case status.StateWaiting:
		return StatusWaiting
	case status.StateDone:
		return StatusDone
	}
	if live {
		return StatusLive
	}
	return StatusIdle
}
