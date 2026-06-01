package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

const (
	testViewW = 80
	testViewH = 24
)

// liveSession returns a single populated live-session item so the body fills the
// pane region (an empty list would render the short empty-state instead).
func liveSession() []list.Item {
	return []list.Item{item{title: "s", isSession: true, live: true, liveTarget: "sess:1"}}
}

func TestView_PlainFillsHeightExactly(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	if h := lipgloss.Height(m.View()); h != testViewH {
		t.Fatalf("plain View height = %d, want exactly %d", h, testViewH)
	}
}

func TestView_ToastFitsHeightExactly(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	m, _ = m.withToast("something failed")
	if h := lipgloss.Height(m.View()); h != testViewH {
		t.Fatalf("View height with toast = %d, want exactly %d", h, testViewH)
	}
}

func TestView_ModalFitsHeightExactly(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	m = sendKey(m, 'x') // opens kill-confirm modal on a live session
	if m.modal.kind == modalNone {
		t.Fatal("expected a modal to open on x")
	}
	if h := lipgloss.Height(m.View()); h != testViewH {
		t.Fatalf("View height with modal = %d, want exactly %d", h, testViewH)
	}
}

func TestView_HelpFitsHeightExactly(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	m = sendKey(m, '?')
	if !m.showHelp {
		t.Fatal("expected help overlay open on ?")
	}
	if h := lipgloss.Height(m.View()); h != testViewH {
		t.Fatalf("View height with help = %d, want exactly %d", h, testViewH)
	}
}
