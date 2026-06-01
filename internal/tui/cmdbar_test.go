package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  cmdSpec
	}{
		{"quit short", "q", cmdSpec{kind: cmdQuit}},
		{"quit long", "quit", cmdSpec{kind: cmdQuit}},
		{"quit case-insensitive", "Quit", cmdSpec{kind: cmdQuit}},
		{"quit rejects args", "q now", cmdSpec{parseErr: "quit takes no arguments"}},
		{"new", "new", cmdSpec{kind: cmdNew}},
		{"new rejects args", "new x", cmdSpec{parseErr: "new takes no arguments"}},
		{"attach with query", "attach my repo", cmdSpec{kind: cmdAttach, arg: "my repo"}},
		{"attach preserves inner spacing", "attach  foo  bar", cmdSpec{kind: cmdAttach, arg: "foo  bar"}},
		{"attach needs query", "attach", cmdSpec{parseErr: "attach needs a query: :attach <text>"}},
		{"proj", "proj alpha", cmdSpec{kind: cmdProj, arg: "alpha"}},
		{"project alias", "project alpha", cmdSpec{kind: cmdProj, arg: "alpha"}},
		{"proj needs name", "proj", cmdSpec{parseErr: "proj needs a name: :proj <text>"}},
		{"setup bare", "setup", cmdSpec{kind: cmdSetup}},
		{"setup replace", "setup --replace", cmdSpec{kind: cmdSetup, replace: true}},
		{"setup bad flag", "setup --force", cmdSpec{parseErr: "setup: unknown flag --force"}},
		{"doctor", "doctor", cmdSpec{kind: cmdDoctor}},
		{"resurrect", "resurrect", cmdSpec{kind: cmdResurrect}},
		{"help", "help", cmdSpec{kind: cmdHelp}},
		{"help question", "?", cmdSpec{kind: cmdHelp}},
		{"help rejects args", "help me", cmdSpec{parseErr: "help takes no arguments"}},
		{"empty is silent cancel", "", cmdSpec{kind: cmdUnknown}},
		{"whitespace is silent cancel", "   ", cmdSpec{kind: cmdUnknown}},
		{"unknown verb", "frobnicate", cmdSpec{parseErr: "unknown command: frobnicate"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCommand(tt.input)
			if got != tt.want {
				t.Fatalf("parseCommand(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveItem(t *testing.T) {
	items := []list.Item{
		item{project: "perch", tree: "main", title: "perch", tool: "claude", isSession: true},
		item{project: "kb", tree: "feat-x", title: "kb", tool: "opencode", isSession: true},
		item{project: "perch", tree: "feat-y", title: "perch", tool: "claude", isSession: false}, // not a session
	}
	m := New(items)

	if got := m.resolveItem("kb"); got != 1 {
		t.Fatalf("resolveItem(kb) = %d, want 1", got)
	}
	if got := m.resolveItem("feat-y"); got != -1 {
		t.Fatalf("resolveItem(feat-y) = %d, want -1 (non-session must not match)", got)
	}
	if got := m.resolveItem("zzzzz"); got != -1 {
		t.Fatalf("resolveItem(zzzzz) = %d, want -1", got)
	}
}

// typeCmd feeds ':' then the given runes then Enter, returning the resulting Model.
func typeCmd(t *testing.T, m Model, line string) Model {
	t.Helper()
	mdl, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	m = mdl.(Model)
	if !m.cmdActive {
		t.Fatalf("':' did not activate the command bar")
	}
	for _, r := range line {
		mdl, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mdl.(Model)
	}
	mdl, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return mdl.(Model)
}

func readyModel(items []list.Item) Model {
	m := New(items)
	m.ready = true
	m.width, m.height = 100, 40
	return m
}

func TestCmdBarActivateAndCancel(t *testing.T) {
	m := readyModel(nil)
	mdl, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	m = mdl.(Model)
	if !m.cmdActive {
		t.Fatal("expected cmdActive after ':'")
	}
	mdl, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mdl.(Model)
	if m.cmdActive {
		t.Fatal("esc must cancel the command bar")
	}
}

func TestCmdBarUnknownToasts(t *testing.T) {
	m := typeCmd(t, readyModel(nil), "frobnicate")
	if m.cmdActive {
		t.Fatal("bar should close after Enter")
	}
	if m.toast == "" {
		t.Fatal("unknown command should set a toast")
	}
}

func TestCmdBarProjJumps(t *testing.T) {
	items := []list.Item{
		item{project: "alpha", title: "alpha", isSession: true},
		item{project: "bravo", title: "bravo", isSession: true},
	}
	m := typeCmd(t, readyModel(items), "proj bravo")
	if m.list.Index() != 1 {
		t.Fatalf("proj bravo: list index = %d, want 1", m.list.Index())
	}
}

func TestCmdBarResurrectGatedInFrame(t *testing.T) {
	m := readyModel(nil)
	m.placeholderPaneID = "%9" // inFrame() == true
	m = typeCmd(t, m, "resurrect")
	if m.toast == "" {
		t.Fatal("resurrect inside the frame must refuse with a toast")
	}
}
