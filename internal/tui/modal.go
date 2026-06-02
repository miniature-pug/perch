package tui

import (
	"fmt"
	"path/filepath"

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
	modalTrustConfirm            // ask user to approve .perch.toml hooks
)

// trustDecision is the resolved user choice for a pending hook-bearing action.
// nil means "not yet decided" — the Cmd must gate.
type trustDecision struct {
	allow        bool   // run hooks?
	approvedHash string // the hash the user approved (guards TOCTOU on re-check)
}

// removeResume carries the parameters needed to resume removeCmd after a trust decision.
type removeResume struct {
	spec     modalState
	force    bool
	skipPrep bool
}

// trustReq is carried on a trustNeededMsg when a hook-bearing action needs user approval.
type trustReq struct {
	configPath string
	hash       string
	phase      string        // "post_create" | "pre_remove"
	create     *modalState   // non-nil to resume worktreeCreateCmd
	remove     *removeResume // non-nil to resume removeCmd
}

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
	trust       *trustReq // non-nil when kind==modalTrustConfirm
}

// renderModal returns a lipgloss-rendered modal box appropriate for ms.kind.
// boxStyle is the accent-derived style used for the box border; it is supplied
// by the caller (Model.theme.modalBox) so each instance carries its own accent.
func renderModal(ms modalState, boxStyle lipgloss.Style) string {
	var content string
	switch ms.kind {
	case modalRemoveConfirm:
		content = "Remove worktree " + ms.branch + "?  (y) confirm  (n/esc) cancel"
	case modalForceConfirm:
		content = ms.branch + " has modified/untracked files. Force remove? (y) force  (n/esc) cancel"
	case modalKillConfirm:
		what := "this session"
		if ms.branch != "" {
			what = ms.branch
		}
		content = "Kill " + what + "? (y) confirm  (n/esc) cancel"
	case modalNewSession:
		labels := [3]string{"new worktree", "run here", "run in main"}
		content = fmt.Sprintf(
			"Worktree action for %s:\n%s\n%s\n%s",
			ms.branch,
			pickerLine(0, ms.action, labels[0]),
			pickerLine(1, ms.action, labels[1]),
			pickerLine(2, ms.action, labels[2]),
		)
	case modalTrustConfirm:
		if ms.trust != nil {
			dir := filepath.Dir(ms.trust.configPath)
			content = fmt.Sprintf(
				".perch.toml in %s defines shell hooks (%s). Run them?\n(a) trust always  (o) once  (d) deny",
				dir,
				ms.trust.phase,
			)
		}
	default:
		return ""
	}
	return boxStyle.Render(content)
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
	case modalTrustConfirm:
		return "a trust always · o once · d deny"
	default:
		return ""
	}
}
