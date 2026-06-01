package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// modalKind identifies which modal prompt is currently visible.
type modalKind int

const (
	modalNone          modalKind = iota
	modalNewSession              // w 3-action picker — routed in Pass 2, constant defined now
	modalRemoveConfirm           // confirm worktree removal
	modalForceConfirm            // confirm force removal of dirty worktree
	modalKillConfirm             // confirm kill-window
)

// modalState carries everything an action Cmd needs so the handler is self-contained.
type modalState struct {
	kind        modalKind
	action      int    // highlighted option in modalNewSession (0=new worktree, 1=run here, 2=run in main)
	target      string // live tmux window target (kill / switch-away)
	paneKey     string // shadow-record key (captureTarget)
	treePath    string
	branch      string
	projectPath string
	tool        string
	sessionID   string
}

// modalStyle is the lipgloss style used to render the modal box.
var modalStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorAccent).
	Padding(0, 1)

// renderModal returns a lipgloss-rendered modal box appropriate for ms.kind.
// width is the terminal width; the box is rendered at a fixed inner width.
func renderModal(ms modalState) string {
	var content string
	switch ms.kind {
	case modalRemoveConfirm:
		content = "Remove worktree " + ms.branch + "?  (y) confirm  (n/esc) cancel"
	case modalForceConfirm:
		content = ms.branch + " has modified/untracked files. Force remove? (y) force  (n/esc) cancel"
	case modalKillConfirm:
		content = "Kill window? (y) confirm  (n/esc) cancel"
	case modalNewSession:
		labels := [3]string{"new worktree", "run here", "run in main"}
		content = fmt.Sprintf(
			"Worktree action for %s:\n%s\n%s\n%s",
			ms.branch,
			pickerLine(0, ms.action, labels[0]),
			pickerLine(1, ms.action, labels[1]),
			pickerLine(2, ms.action, labels[2]),
		)
	default:
		return ""
	}
	return modalStyle.Render(content)
}

// pickerLine renders one row of the modalNewSession picker.
// The selected row is prefixed with "> "; others with "  ".
func pickerLine(idx, selected int, label string) string {
	if idx == selected {
		return fmt.Sprintf("> [%d] %s", idx+1, label)
	}
	return fmt.Sprintf("  [%d] %s", idx+1, label)
}

// modalFooterHint returns the key-hint string shown in the footer while ms.kind is active.
func modalFooterHint(kind modalKind) string {
	switch kind {
	case modalRemoveConfirm, modalForceConfirm, modalKillConfirm:
		return "y confirm · n/esc cancel"
	case modalNewSession:
		return "↑/↓ select · ↵ confirm · esc cancel"
	default:
		return ""
	}
}
