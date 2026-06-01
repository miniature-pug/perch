package tui

import (
	"strings"
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

// View must never emit a line wider than the terminal — a wrapped over-wide line
// would re-introduce vertical overflow that lipgloss.Height cannot detect.

func TestView_LongToastStaysWithinWidthAndHeight(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	m, _ = m.withToast(strings.Repeat("x", 200))
	v := m.View()
	if w := lipgloss.Width(v); w > testViewW {
		t.Fatalf("View width with long toast = %d, want <= %d", w, testViewW)
	}
	if h := lipgloss.Height(v); h != testViewH {
		t.Fatalf("View height with long toast = %d, want %d", h, testViewH)
	}
}

func TestView_LongLoadErrStaysWithinWidthAndHeight(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	m.loadErr = strings.Repeat("e", 200)
	v := m.View()
	if w := lipgloss.Width(v); w > testViewW {
		t.Fatalf("View width with long loadErr = %d, want <= %d", w, testViewW)
	}
	if h := lipgloss.Height(v); h != testViewH {
		t.Fatalf("View height with long loadErr = %d, want %d", h, testViewH)
	}
}

func TestView_LongModalStaysWithinWidth(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	m.modal = modalState{kind: modalRemoveConfirm, branch: strings.Repeat("b", 200)}
	v := m.View()
	if w := lipgloss.Width(v); w > testViewW {
		t.Fatalf("View width with long modal branch = %d, want <= %d", w, testViewW)
	}
	if h := lipgloss.Height(v); h > testViewH {
		t.Fatalf("View height with long modal = %d, want <= %d", h, testViewH)
	}
}

func TestView_HelpOverlayStaysWithinWidth(t *testing.T) {
	m := New(liveSession())
	m, _ = sizeModel(m, testViewW, testViewH)
	m = sendKey(m, '?')
	v := m.View()
	if w := lipgloss.Width(v); w > testViewW {
		t.Fatalf("View width with help overlay = %d, want <= %d", w, testViewW)
	}
	if h := lipgloss.Height(v); h != testViewH {
		t.Fatalf("View height with help overlay = %d, want %d", h, testViewH)
	}
}

// At a wide terminal the list pane must respect its ~30% allocation so the
// preview pane is not squeezed — i.e. the joined body fills exactly the width.
func TestView_TwoPaneBodyFillsWidth(t *testing.T) {
	// Several items with long titles that would each render ~50 cols if untruncated.
	items := []list.Item{
		item{title: strings.Repeat("t", 60), tool: "claude", isSession: true, live: true, liveTarget: "s:1"},
		item{title: strings.Repeat("u", 60), tool: "opencode", isSession: true},
	}
	m := New(items)
	m, _ = sizeModel(m, testViewW, testViewH)
	if w := lipgloss.Width(m.View()); w != testViewW {
		t.Fatalf("two-pane View width = %d, want exactly %d (list pane must respect its allocation)", w, testViewW)
	}
	// The list pane itself must be bounded to its allocated width, not ~50 cols.
	if lw := m.list.Width(); lw <= 0 || lw >= testViewW/2 {
		t.Fatalf("list width = %d, want a narrow left-pane allocation (< half of %d)", lw, testViewW)
	}
}
