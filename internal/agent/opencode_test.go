package agent

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
)

// Fixtures under testdata/opencode mirror real `opencode session list --format
// json` output. The single live session on the capture host seeded the shape;
// session-list.json extends it to three sessions across two directories so the
// grouping path is covered.

// ── parseSessionList ──────────────────────────────────────────────────────────

func TestParseSessionList_Valid(t *testing.T) {
	raw, err := os.ReadFile("testdata/opencode/session-list.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	sessions, err := parseSessionList(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(sessions))
	}

	first := sessions[0]
	if first.ID != "ses_18593fc84ffeg4oyInzAG2eLOL" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Directory != "/home/user/projA" {
		t.Errorf("Directory = %q", first.Directory)
	}
	if first.Title != "Greeting" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.Tool != model.ToolOpencode {
		t.Errorf("Tool = %q, want opencode", first.Tool)
	}
	// Millisecond timestamp truncated to unix seconds.
	if first.Updated != 1780170389 {
		t.Errorf("Updated = %d, want 1780170389 (1780170389857 ms → s)", first.Updated)
	}
}

func TestParseSessionList_MillisecondsToSeconds(t *testing.T) {
	raw := []byte(`[{"id":"ses_x","title":"t","directory":"/d","updated":1780190000000,"created":1}]`)
	sessions, err := parseSessionList(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sessions[0].Updated != 1780190000 {
		t.Errorf("Updated = %d, want 1780190000", sessions[0].Updated)
	}
}

func TestParseSessionList_Empty(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty bytes (real empty-scope output)", mustRead(t, "testdata/opencode/session-list-empty")},
		{"whitespace only", []byte("  \n\t ")},
		{"literal empty array", []byte("[]")},
		{"nil", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessions, err := parseSessionList(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(sessions) != 0 {
				t.Errorf("got %d sessions, want 0", len(sessions))
			}
		})
	}
}

func TestParseSessionList_Malformed(t *testing.T) {
	raw := mustRead(t, "testdata/opencode/session-list-malformed.json")

	defer func() {
		if p := recover(); p != nil {
			t.Errorf("parseSessionList panicked on malformed input: %v", p)
		}
	}()

	if _, err := parseSessionList(raw); err == nil {
		t.Error("expected an error for malformed JSON, got nil")
	}
}

// ── ListSessions ──────────────────────────────────────────────────────────────

func TestListSessions_PassesDirAndCommand(t *testing.T) {
	raw := mustRead(t, "testdata/opencode/session-list.json")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: raw}, "opencode", "session", "list", "--format", "json")

	o := NewOpencode()
	o.Runner = r
	o.Dir = "/home/user/projA"

	sessions, err := o.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(sessions))
	}

	if len(r.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(r.Calls))
	}
	call := r.Calls[0]
	// The whole point of RunInDir: the listing must be scoped to o.Dir.
	if call.Dir != "/home/user/projA" {
		t.Errorf("call.Dir = %q, want /home/user/projA", call.Dir)
	}
	wantArgs := []string{"session", "list", "--format", "json"}
	if call.Name != "opencode" || !reflect.DeepEqual(call.Args, wantArgs) {
		t.Errorf("call = %q %v, want opencode %v", call.Name, call.Args, wantArgs)
	}
}

func TestListSessions_RunnerError(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: errors.New("exec failed")}, "opencode", "session", "list", "--format", "json")

	o := NewOpencode()
	o.Runner = r

	if _, err := o.ListSessions(context.Background()); err == nil {
		t.Error("expected runner error to propagate, got nil")
	}
}

func TestListSessions_EmptyOutput(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: nil}, "opencode", "session", "list", "--format", "json")

	o := NewOpencode()
	o.Runner = r

	sessions, err := o.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("got %d sessions, want 0 for empty output", len(sessions))
	}
}

// ── GroupByDirectory ──────────────────────────────────────────────────────────

func TestGroupByDirectory(t *testing.T) {
	raw := mustRead(t, "testdata/opencode/session-list.json")
	sessions, err := parseSessionList(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	groups := GroupByDirectory(sessions)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if got := len(groups["/home/user/projA"]); got != 2 {
		t.Errorf("projA group = %d sessions, want 2", got)
	}
	if got := len(groups["/home/user/projB"]); got != 1 {
		t.Errorf("projB group = %d sessions, want 1", got)
	}
}

func TestGroupByDirectory_Empty(t *testing.T) {
	if got := GroupByDirectory(nil); len(got) != 0 {
		t.Errorf("GroupByDirectory(nil) = %v, want empty", got)
	}
}

// ── arg builders, Detect, Name ─────────────────────────────────────────────────

func TestOpencode_ResumeArgs(t *testing.T) {
	got := NewOpencode().ResumeArgs("ses_x")
	want := []string{"--session", "ses_x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResumeArgs = %v, want %v", got, want)
	}
}

func TestOpencode_ForkInto(t *testing.T) {
	got, err := NewOpencode().ForkInto("ses_x", "/target")
	if err == nil {
		t.Error("ForkInto must return an error (unsupported in v1)")
	}
	if got != nil {
		t.Errorf("ForkInto args = %v, want nil", got)
	}
}

func TestOpencode_NewArgs(t *testing.T) {
	c := NewOpencode()
	tests := []struct {
		name string
		opts NewOpts
		want []string
	}{
		{"none", NewOpts{}, nil},
		{"model", NewOpts{Model: "anthropic/claude"}, []string{"--model", "anthropic/claude"}},
		{"agent", NewOpts{Agent: "build"}, []string{"--agent", "build"}},
		{"prompt", NewOpts{Prompt: "do x"}, []string{"--prompt", "do x"}},
		{
			"all (SessionID ignored)",
			NewOpts{Model: "m", Agent: "a", Prompt: "p", SessionID: "ignored"},
			[]string{"--model", "m", "--agent", "a", "--prompt", "p"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.NewArgs(tt.opts); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewArgs(%+v) = %v, want %v", tt.opts, got, tt.want)
			}
		})
	}
}

func TestOpencode_Detect(t *testing.T) {
	ok := NewOpencode()
	ok.LookPath = func(string) (string, error) { return "/usr/bin/opencode", nil }
	if !ok.Detect() {
		t.Error("Detect() = false, want true when LookPath succeeds")
	}

	no := NewOpencode()
	no.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if no.Detect() {
		t.Error("Detect() = true, want false when LookPath fails")
	}
}

func TestOpencode_Name(t *testing.T) {
	if got := NewOpencode().Name(); got != "opencode" {
		t.Errorf("Name() = %q, want opencode", got)
	}
}

// TestOpencode_ZeroValueDefaults exercises the nil-seam fallbacks so a
// zero-value Opencode never panics on the pure paths.
func TestOpencode_ZeroValueDefaults(t *testing.T) {
	var o Opencode
	if o.bin() != "opencode" {
		t.Errorf("zero-value bin() = %q, want opencode", o.bin())
	}
	_ = o.Detect()           // default exec.LookPath; PATH-dependent, just no panic
	_ = o.NewArgs(NewOpts{}) // pure
}

// ── helpers ─────────────────────────────────────────────────────────────────

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
