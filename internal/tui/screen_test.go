package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func TestEmptyStateText_NoItemsWithRoot(t *testing.T) {
	m := New(nil)
	m.root = "/home/u/code"
	got := m.emptyStateText()
	want := "No git repositories found under /home/u/code"
	if got != want {
		t.Fatalf("emptyStateText = %q, want %q", got, want)
	}
}

func TestEmptyStateText_NoItemsNoRoot(t *testing.T) {
	m := New(nil)
	got := m.emptyStateText()
	if got != "No git repositories found" {
		t.Fatalf("emptyStateText = %q, want generic message", got)
	}
}

func TestEmptyStateText_WithItemsReturnsEmpty(t *testing.T) {
	m := New([]list.Item{item{title: "x", isSession: true}})
	if got := m.emptyStateText(); got != "" {
		t.Fatalf("emptyStateText = %q, want empty when items present", got)
	}
}

func TestEmptyStateText_SuppressedDuringLoadError(t *testing.T) {
	m := New(nil)
	m.loadErr = "boom"
	if got := m.emptyStateText(); got != "" {
		t.Fatalf("emptyStateText = %q, want empty while loadErr is set", got)
	}
}

// loadErr precedence must beat the root-qualified message path too.
func TestEmptyStateText_LoadErrBeatsRoot(t *testing.T) {
	m := New(nil)
	m.root = "/home/u/code"
	m.loadErr = "boom"
	if got := m.emptyStateText(); got != "" {
		t.Fatalf("emptyStateText = %q, want empty (loadErr precedence) with root set", got)
	}
}

func sendKey(m Model, r rune) Model {
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return model.(Model)
}

// sizeModel sends a WindowSizeMsg and returns the resulting model.
func sizeModel(m Model, w, h int) (Model, tea.Cmd) {
	model, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return model.(Model), cmd
}

func TestScreenMode_ZCyclesForward(t *testing.T) {
	m := New(nil)
	m, _ = sizeModel(m, 120, 40)
	if m.mode != modeNormal {
		t.Fatalf("initial mode = %v, want modeNormal", m.mode)
	}
	m = sendKey(m, 'z')
	if m.mode != modeFullList {
		t.Fatalf("after z = %v, want modeFullList", m.mode)
	}
	m = sendKey(m, 'z')
	if m.mode != modeFullPreview {
		t.Fatalf("after zz = %v, want modeFullPreview", m.mode)
	}
	m = sendKey(m, 'z')
	if m.mode != modeNormal {
		t.Fatalf("after zzz = %v, want modeNormal (wrap)", m.mode)
	}
}

func TestScreenMode_ZShiftCyclesBackward(t *testing.T) {
	m := New(nil)
	m, _ = sizeModel(m, 120, 40)
	m = sendKey(m, 'Z')
	if m.mode != modeFullPreview {
		t.Fatalf("after Z = %v, want modeFullPreview (wrap back)", m.mode)
	}
}

func TestRelayout_NarrowForcesVerticalStack(t *testing.T) {
	m := New(nil)
	m, _ = sizeModel(m, 50, 40)
	if m.list.Width() < 40 {
		t.Fatalf("narrow list width = %d, want near-full (vertical stack)", m.list.Width())
	}
	if m.preview.Width < 40 {
		t.Fatalf("narrow preview width = %d, want near-full (vertical stack)", m.preview.Width)
	}
}

func TestRelayout_WideNormalSplits(t *testing.T) {
	m := New(nil)
	m, _ = sizeModel(m, 120, 40)
	if m.list.Width() >= m.preview.Width {
		t.Fatalf("wide normal: list width %d should be < preview width %d", m.list.Width(), m.preview.Width)
	}
}
