package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// hasBinding reports whether bindings contains one whose help key matches keyHelp.
func hasBinding(bindings []key.Binding, keyHelp string) bool {
	for _, b := range bindings {
		if b.Help().Key == keyHelp {
			return true
		}
	}
	return false
}

func TestShortHelp_LiveSessionIncludesKill(t *testing.T) {
	m := New([]list.Item{item{title: "s", isSession: true, live: true, liveTarget: "sess:1"}})
	if !hasBinding(m.ShortHelp(), "x") {
		t.Fatal("live session: short help must include kill (x)")
	}
}

func TestShortHelp_IdleNonSessionExcludesKillAndRemove(t *testing.T) {
	m := New([]list.Item{item{title: "p", isSession: false}})
	sh := m.ShortHelp()
	if hasBinding(sh, "x") {
		t.Fatal("non-session: short help must NOT include kill (x)")
	}
	if hasBinding(sh, "d") {
		t.Fatal("non-session: short help must NOT include remove (d)")
	}
}

func TestShortHelp_MainCheckoutExcludesRemove(t *testing.T) {
	m := New([]list.Item{item{title: "main", isSession: true, isMain: true}})
	if hasBinding(m.ShortHelp(), "d") {
		t.Fatal("main checkout: short help must NOT include remove (d)")
	}
}

func TestFullHelp_IncludesAllCoreBindings(t *testing.T) {
	m := New(nil)
	var all []key.Binding
	for _, grp := range m.FullHelp() {
		all = append(all, grp...)
	}
	for _, k := range []string{"↵", "n", "w", "d", "x", "/", "z", "?", "q"} {
		if !hasBinding(all, k) {
			t.Fatalf("full help missing binding %q", k)
		}
	}
}

func TestHelpKey_TogglesOverlay(t *testing.T) {
	m := New(nil)
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = model.(Model)
	if !m.showHelp {
		t.Fatal("? must open the help overlay")
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if model.(Model).showHelp {
		t.Fatal("? again must close the help overlay")
	}
}

func TestKill_OnNonLive_Toasts(t *testing.T) {
	m := New([]list.Item{item{title: "idle", isSession: true, live: false}})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if model.(Model).toast == "" {
		t.Fatal("x on a non-live session must toast a reason, not silently no-op")
	}
}
