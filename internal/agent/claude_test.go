package agent

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/model"
)

// Fixtures under testdata/claude are minimal, real-shaped JSONL/JSON records
// sanitized from live claude transcripts and pid trackers.

// ── decodeSlug ────────────────────────────────────────────────────────────────

// existsSet builds an exists probe backed by a fixed set of absolute paths.
func existsSet(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestDecodeSlug(t *testing.T) {
	tests := []struct {
		name   string
		slug   string
		exists func(string) bool
		want   string
	}{
		{
			name:   "clean fully resolved",
			slug:   "-home-user-myproject",
			exists: existsSet("/home", "/home/user", "/home/user/myproject"),
			want:   "/home/user/myproject",
		},
		{
			name:   "merged component wins (longest tried first)",
			slug:   "-home-user-foo-bar",
			exists: existsSet("/home", "/home/user", "/home/user/foo-bar"),
			want:   "/home/user/foo-bar",
		},
		{
			name:   "split when merged does not exist",
			slug:   "-home-user-foo-bar",
			exists: existsSet("/home", "/home/user", "/home/user/foo", "/home/user/foo/bar"),
			want:   "/home/user/foo/bar",
		},
		{
			name:   "stuck: longest existing prefix plus naive remainder",
			slug:   "-home-user-foo-bar",
			exists: existsSet("/home", "/home/user"),
			want:   "/home/user/foo/bar",
		},
		{
			name:   "no leading dash returned unchanged",
			slug:   "home-user-myproject",
			exists: existsSet("/home", "/home/user"),
			want:   "home-user-myproject",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeSlug(tt.slug, tt.exists); got != tt.want {
				t.Errorf("decodeSlug(%q) = %q, want %q", tt.slug, got, tt.want)
			}
		})
	}
}

// ── parseTranscript ───────────────────────────────────────────────────────────

func TestParseTranscript_TitleFromLastAITitle(t *testing.T) {
	f := openFixture(t, "projects/-home-user-myproject/11111111-1111-1111-1111-111111111111.jsonl")
	defer func() { _ = f.Close() }()

	title, cwd, had, err := parseTranscript(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !had {
		t.Fatalf("hadRecords = false, want true")
	}
	if title != "Plan and implement features" {
		t.Errorf("title = %q, want last ai-title %q", title, "Plan and implement features")
	}
	if cwd != "/home/user/myproject" {
		t.Errorf("firstCwd = %q, want /home/user/myproject", cwd)
	}
}

func TestParseTranscript_TitleFallbackSkipsMeta(t *testing.T) {
	f := openFixture(t, "projects/-home-user-noai/22222222-2222-2222-2222-222222222222.jsonl")
	defer func() { _ = f.Close() }()

	title, cwd, had, err := parseTranscript(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !had {
		t.Fatalf("hadRecords = false, want true")
	}
	if title != "the real human prompt" {
		t.Errorf("title = %q, want first non-meta human message", title)
	}
	if cwd != "" {
		t.Errorf("firstCwd = %q, want empty (no cwd in fixture)", cwd)
	}
}

func TestParseTranscript_MalformedLineSkipped(t *testing.T) {
	f := openFixture(t, "projects/-home-user-myproject/33333333-3333-3333-3333-333333333333.jsonl")
	defer func() { _ = f.Close() }()

	title, cwd, had, err := parseTranscript(f)
	if err != nil {
		t.Fatalf("unexpected error (malformed line must not error): %v", err)
	}
	if !had {
		t.Fatalf("hadRecords = false, want true")
	}
	// Line 3 is invalid JSON; lines 1,2,4 still parse, so the last ai-title wins.
	if title != "Title after the broken line" {
		t.Errorf("title = %q, want %q", title, "Title after the broken line")
	}
	if cwd != "/home/user/myproject" {
		t.Errorf("firstCwd = %q, want /home/user/myproject", cwd)
	}
}

func TestParseTranscript_EmptyFile(t *testing.T) {
	f := openFixture(t, "projects/-home-user-myproject/44444444-4444-4444-4444-444444444444.jsonl")
	defer func() { _ = f.Close() }()

	title, _, had, err := parseTranscript(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if had {
		t.Errorf("hadRecords = true, want false for empty file")
	}
	if title != "" {
		t.Errorf("title = %q, want empty", title)
	}
}

func TestParseTranscript_ArrayContentFallback(t *testing.T) {
	// content as an array of blocks; first block with a text field is used.
	raw := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"block text here"}]}}`
	title, _, had, err := parseTranscript(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !had {
		t.Fatalf("hadRecords = false, want true")
	}
	if title != "block text here" {
		t.Errorf("title = %q, want %q", title, "block text here")
	}
}

// ── readPidTrackers ───────────────────────────────────────────────────────────

func TestReadPidTrackers(t *testing.T) {
	got := readPidTrackers("testdata/claude")
	want := map[string]string{
		"22222222-2222-2222-2222-222222222222": "/home/user/pid-tracked-dir",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readPidTrackers = %v, want %v (bad.json must be skipped)", got, want)
	}
}

func TestReadPidTrackers_MissingDir(t *testing.T) {
	got := readPidTrackers("testdata/claude/does-not-exist")
	if len(got) != 0 {
		t.Errorf("readPidTrackers(missing) = %v, want empty", got)
	}
}

// ── ListSessions ──────────────────────────────────────────────────────────────

func newTestClaude() Claude {
	c := NewClaude()
	c.Home = "testdata/claude"
	// Deterministic slug decode: only the slugonly path resolves cleanly.
	c.Exists = existsSet("/home", "/home/user", "/home/user/slugonly")
	return c
}

func TestListSessions(t *testing.T) {
	c := newTestClaude()
	sessions, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	byID := make(map[string]model.Session, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}

	// Empty file 44444444 yields no session; subagent decoy is never enumerated.
	if _, ok := byID["44444444-4444-4444-4444-444444444444"]; ok {
		t.Errorf("empty .jsonl must not produce a session")
	}
	if _, ok := byID["agent-decoy"]; ok {
		t.Errorf("subagent transcript must never be enumerated as a session")
	}

	wantIDs := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		"66666666-6666-6666-6666-666666666666",
	}
	if len(sessions) != len(wantIDs) {
		t.Fatalf("got %d sessions, want %d: %+v", len(sessions), len(wantIDs), sessions)
	}

	// Sorted by ID for determinism.
	gotIDs := make([]string, len(sessions))
	for i, s := range sessions {
		gotIDs[i] = s.ID
	}
	if !sort.StringsAreSorted(gotIDs) {
		t.Errorf("sessions not sorted by ID: %v", gotIDs)
	}

	// Tier 1: transcript cwd wins.
	s1 := byID["11111111-1111-1111-1111-111111111111"]
	if s1.Directory != "/home/user/myproject" {
		t.Errorf("11111111 Directory = %q, want transcript cwd /home/user/myproject", s1.Directory)
	}
	if s1.Title != "Plan and implement features" {
		t.Errorf("11111111 Title = %q", s1.Title)
	}
	if s1.Tool != model.ToolClaude {
		t.Errorf("11111111 Tool = %q, want claude", s1.Tool)
	}

	// Tier 2: no transcript cwd, pid entry wins.
	s2 := byID["22222222-2222-2222-2222-222222222222"]
	if s2.Directory != "/home/user/pid-tracked-dir" {
		t.Errorf("22222222 Directory = %q, want pid cwd /home/user/pid-tracked-dir", s2.Directory)
	}
	if s2.Title != "the real human prompt" {
		t.Errorf("22222222 Title = %q, want human-message fallback", s2.Title)
	}

	// Tier 3: no transcript cwd, no pid entry, slug-decode wins.
	s6 := byID["66666666-6666-6666-6666-666666666666"]
	if s6.Directory != "/home/user/slugonly" {
		t.Errorf("66666666 Directory = %q, want slug-decode /home/user/slugonly", s6.Directory)
	}

	// Updated == file mtime in unix seconds.
	fi, err := os.Stat("testdata/claude/projects/-home-user-myproject/11111111-1111-1111-1111-111111111111.jsonl")
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	if s1.Updated != fi.ModTime().Unix() {
		t.Errorf("11111111 Updated = %d, want mtime %d", s1.Updated, fi.ModTime().Unix())
	}
}

func TestListSessions_MissingHome(t *testing.T) {
	c := NewClaude()
	c.Home = "testdata/claude/nope"
	sessions, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("missing home must not error, got %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("missing home = %v, want empty", sessions)
	}
}

// ── arg builders, Detect, Name ─────────────────────────────────────────────────

func TestResumeArgs(t *testing.T) {
	got := NewClaude().ResumeArgs("abc")
	want := []string{"--resume", "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResumeArgs = %v, want %v", got, want)
	}
}

func TestForkInto(t *testing.T) {
	got, err := NewClaude().ForkInto("abc", "/some/target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--resume", "abc", "--fork-session"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ForkInto = %v, want %v", got, want)
	}
}

func TestNewArgs(t *testing.T) {
	c := NewClaude()
	tests := []struct {
		name string
		opts NewOpts
		want []string
	}{
		{"none", NewOpts{}, nil},
		{"model", NewOpts{Model: "opus"}, []string{"--model", "opus"}},
		{"sessionid", NewOpts{SessionID: "uuid"}, []string{"--session-id", "uuid"}},
		{"prompt", NewOpts{Prompt: "do x"}, []string{"do x"}},
		{
			"all",
			NewOpts{Model: "opus", SessionID: "uuid", Prompt: "do x", Agent: "ignored"},
			[]string{"--model", "opus", "--session-id", "uuid", "do x"},
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

func TestDetect(t *testing.T) {
	ok := NewClaude()
	ok.LookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	if !ok.Detect() {
		t.Errorf("Detect() = false, want true when LookPath succeeds")
	}

	no := NewClaude()
	no.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if no.Detect() {
		t.Errorf("Detect() = true, want false when LookPath fails")
	}
}

func TestName(t *testing.T) {
	if got := NewClaude().Name(); got != "claude" {
		t.Errorf("Name() = %q, want claude", got)
	}
}

// TestZeroValueDefaults exercises the nil-seam fallback helpers so a
// zero-value Claude (the var _ Adapter = Claude{} guarantee) never panics.
func TestZeroValueDefaults(t *testing.T) {
	var c Claude // all seams nil, Bin empty

	// bin() falls back to "claude"; Detect uses default exec.LookPath. We do
	// not assert the boolean (PATH-dependent) — only that no panic occurs.
	_ = c.Detect()

	// exists() falls back to defaultExists, which probes the real FS.
	if !c.exists()("/") {
		t.Errorf("default exists() should report / as an existing directory")
	}
	if c.exists()("/no/such/path/perch-test") {
		t.Errorf("default exists() should report a missing path as absent")
	}

	// A regular file is not a directory.
	tmp, err := os.CreateTemp(t.TempDir(), "f")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	_ = tmp.Close()
	if c.exists()(tmp.Name()) {
		t.Errorf("default exists() should reject a regular file")
	}
}

func TestHumanText_Empty(t *testing.T) {
	if got := humanText(nil); got != "" {
		t.Errorf("humanText(nil) = %q, want empty", got)
	}
	if got := humanText(&messageField{}); got != "" {
		t.Errorf("humanText(empty content) = %q, want empty", got)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/claude/" + name)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	return f
}
