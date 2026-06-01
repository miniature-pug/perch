package tui

import "testing"

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
