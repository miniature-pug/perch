package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
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
