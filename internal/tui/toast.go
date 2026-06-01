package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// toastDuration is how long a transient toast stays visible before auto-clear.
const toastDuration = 3 * time.Second

// clearToastMsg requests clearing the toast. It only clears when seq matches the
// model's current toastSeq, so an older clear tick can't wipe a newer toast.
type clearToastMsg struct{ seq int }

// withToast returns a copy of m showing msg as a transient toast plus the Cmd
// that clears it after toastDuration. toastSeq increments per toast so a stale
// clear tick is ignored.
func (m Model) withToast(msg string) (Model, tea.Cmd) {
	m.toast = msg
	m.toastSeq++
	seq := m.toastSeq
	return m, tea.Tick(toastDuration, func(time.Time) tea.Msg {
		return clearToastMsg{seq: seq}
	})
}
