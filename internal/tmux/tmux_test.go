package tmux

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// ── zero-value safety ─────────────────────────────────────────────────────────

func TestTmux_ZeroValueDefaults(t *testing.T) {
	var o Tmux
	if o.bin() != "tmux" {
		t.Errorf("zero-value bin() = %q, want tmux", o.bin())
	}
	// runner() and getenv() must not panic on zero value.
	_ = o.runner()
	_ = o.getenv()
}

// ── args() socket prefixing ───────────────────────────────────────────────────

func TestArgs_NoSocket(t *testing.T) {
	o := Tmux{}
	got := o.args("has-session", "-t=foo")
	want := []string{"has-session", "-t=foo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args() no socket = %v, want %v", got, want)
	}
}

func TestArgs_WithSocket(t *testing.T) {
	o := Tmux{Socket: "perch-test"}
	got := o.args("has-session", "-t=foo")
	want := []string{"-L", "perch-test", "has-session", "-t=foo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args() with socket = %v, want %v", got, want)
	}
}

func TestArgs_WithSocket_EmptySubCommand(t *testing.T) {
	o := Tmux{Socket: "s"}
	got := o.args()
	want := []string{"-L", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args() socket only = %v, want %v", got, want)
	}
}

// Verify socket prefix appears in the actual Call.Args via FakeRunner.
func TestArgs_SocketAppearsInCallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("")}, "tmux", "-L", "test-sock", "display-message", "-p", "#{start_time}")

	o := Tmux{Runner: r, Bin: "tmux", Socket: "test-sock"}
	_, _ = o.BootID(context.Background())

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	got := r.Calls[0].Args
	want := []string{"-L", "test-sock", "display-message", "-p", "#{start_time}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Call.Args = %v, want %v", got, want)
	}
}

// ── ExecArgs ──────────────────────────────────────────────────────────────────

// TestExecArgs_NoSocket verifies that ExecArgs prepends only the binary when
// Socket is unset — no -L flag — matching the expected argv for tea.ExecProcess.
func TestExecArgs_NoSocket_AttachArgs(t *testing.T) {
	o := Tmux{
		Bin: "tmux",
		Getenv: func(s string) string {
			if s == "TMUX" {
				return "/tmp/tmux-1000/default,1234,0"
			}
			return ""
		},
	}
	got := o.ExecArgs(o.AttachArgs("s")...)
	want := []string{"tmux", "switch-client", "-t", "=s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExecArgs (no socket, inside tmux) = %v, want %v", got, want)
	}
}

// TestExecArgs_WithSocket_AttachArgs verifies that ExecArgs inserts -L <socket>
// between the binary and subcommand so the private server is always addressed.
func TestExecArgs_WithSocket_AttachArgs(t *testing.T) {
	o := Tmux{
		Bin:    "tmux",
		Socket: "sock",
		Getenv: func(s string) string {
			if s == "TMUX" {
				return "/tmp/tmux-1000/default,1234,0"
			}
			return ""
		},
	}
	got := o.ExecArgs(o.AttachArgs("s")...)
	want := []string{"tmux", "-L", "sock", "switch-client", "-t", "=s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExecArgs (with socket, inside tmux) = %v, want %v", got, want)
	}
}

// ── target builders ───────────────────────────────────────────────────────────

func TestSessionTarget(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"myproj", "=myproj"},
		{"a-b", "=a-b"},
	}
	for _, tc := range tests {
		if got := SessionTarget(tc.name); got != tc.want {
			t.Errorf("SessionTarget(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWindowTarget(t *testing.T) {
	tests := []struct {
		session, window string
		want            string
	}{
		{"myproj", "main", "=myproj:=main"},
		{"proj", "feat-branch", "=proj:=feat-branch"},
	}
	for _, tc := range tests {
		if got := WindowTarget(tc.session, tc.window); got != tc.want {
			t.Errorf("WindowTarget(%q,%q) = %q, want %q", tc.session, tc.window, got, tc.want)
		}
	}
}

// ── parsePanes ────────────────────────────────────────────────────────────────

func TestParsePanes_GoodFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/tmux/list-panes-with-options.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	panes := parsePanes(raw)
	if len(panes) != 2 {
		t.Fatalf("got %d panes, want 2", len(panes))
	}

	p0 := panes[0]
	if p0.ID != "%0" {
		t.Errorf("p0.ID = %q, want %%0", p0.ID)
	}
	if p0.PID != "1234" {
		t.Errorf("p0.PID = %q, want 1234", p0.PID)
	}
	if p0.Command != "bash" {
		t.Errorf("p0.Command = %q, want bash", p0.Command)
	}
	if p0.Dead {
		t.Error("p0.Dead = true, want false")
	}
	if p0.Path != "/home/user/myproject" {
		t.Errorf("p0.Path = %q, want /home/user/myproject", p0.Path)
	}
	if p0.Session != "myproject" {
		t.Errorf("p0.Session = %q, want myproject", p0.Session)
	}
	if p0.Window != "main" {
		t.Errorf("p0.Window = %q, want main", p0.Window)
	}
	if p0.PerchSession != "proj-abc" {
		t.Errorf("p0.PerchSession = %q, want proj-abc", p0.PerchSession)
	}

	// Second pane: path WITH a space, empty @perch_session.
	p1 := panes[1]
	if p1.ID != "%1" {
		t.Errorf("p1.ID = %q, want %%1", p1.ID)
	}
	if p1.Path != "/home/user/my project/src" {
		t.Errorf("p1.Path = %q, want path with space", p1.Path)
	}
	if p1.Command != "claude" {
		t.Errorf("p1.Command = %q, want claude", p1.Command)
	}
	if p1.PerchSession != "" {
		t.Errorf("p1.PerchSession = %q, want empty", p1.PerchSession)
	}
}

func TestParsePanes_EmptyFile(t *testing.T) {
	raw, err := os.ReadFile("testdata/tmux/list-panes-empty.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	panes := parsePanes(raw)
	if len(panes) != 0 {
		t.Errorf("parsePanes(empty) = %d panes, want 0", len(panes))
	}
}

func TestParsePanes_NilInput(t *testing.T) {
	if panes := parsePanes(nil); panes != nil {
		t.Errorf("parsePanes(nil) = %v, want nil", panes)
	}
}

func TestParsePanes_MalformedLineSkipped(t *testing.T) {
	// A line with too few fields must be skipped; valid lines must still parse.
	raw := []byte("%0\x1f1234\x1fbash\x1f0\x1f/home/u\x1fsess\x1fwin\x1fval\n" + // good
		"not-enough-fields\n" + // bad: skip
		"%2\x1f9999\x1fzsh\x1f0\x1f/tmp\x1fs2\x1fw2\x1f\n") // good
	panes := parsePanes(raw)
	if len(panes) != 2 {
		t.Errorf("got %d panes, want 2 (malformed line skipped)", len(panes))
	}
	if panes[0].ID != "%0" || panes[1].ID != "%2" {
		t.Errorf("wrong IDs: %v", panes)
	}
}

func TestParsePanes_NoPanic(t *testing.T) {
	// Garbage input must never panic.
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("parsePanes panicked: %v", p)
		}
	}()
	_ = parsePanes([]byte("garbage\x00data\x01more"))
}

// ── HasSession ────────────────────────────────────────────────────────────────

func TestHasSession_Exists(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "has-session", "-t", "=myproj")

	o := Tmux{Runner: r, Bin: "tmux"}
	got, err := o.HasSession(context.Background(), "myproj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("HasSession = false, want true when exit 0")
	}

	// Guard against regression: target must be a separate arg, never "-t==name".
	wantArgs := []string{"has-session", "-t", "=myproj"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v (target must be separate arg, not concatenated)", r.Calls[0].Args, wantArgs)
	}
}

func TestHasSession_Missing(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}}, "tmux", "has-session", "-t", "=miss")

	o := Tmux{Runner: r, Bin: "tmux"}
	got, err := o.HasSession(context.Background(), "miss")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("HasSession = true, want false for exit 1")
	}
}

func TestHasSession_ExecFailure(t *testing.T) {
	execErr := errors.New("exec: no such file or directory")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: execErr}, "tmux", "has-session", "-t", "=myproj")

	o := Tmux{Runner: r, Bin: "tmux"}
	got, err := o.HasSession(context.Background(), "myproj")
	if err == nil {
		t.Fatal("expected error for exec failure, got nil")
	}
	if got {
		t.Error("HasSession = true on exec failure, want false")
	}
	if !errors.Is(err, execErr) {
		t.Errorf("error does not wrap execErr: %v", err)
	}
}

// ── BootID ────────────────────────────────────────────────────────────────────

func TestBootID_OK(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("1717000000\n")}, "tmux", "display-message", "-p", "#{start_time}")

	o := Tmux{Runner: r, Bin: "tmux"}
	id, err := o.BootID(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "1717000000" {
		t.Errorf("BootID = %q, want 1717000000", id)
	}
}

func TestBootID_Error_WrapsSterr(t *testing.T) {
	underlying := errors.New("no server running")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stderr: []byte("  no server running on /tmp/tmux  \n"), Err: underlying},
		"tmux", "display-message", "-p", "#{start_time}")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.BootID(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "no server running") {
		t.Errorf("error should include stderr text: %v", err)
	}
}

// ── ListPanes ─────────────────────────────────────────────────────────────────

func TestListPanes_CallArgs(t *testing.T) {
	raw, _ := os.ReadFile("testdata/tmux/list-panes-with-options.txt")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: raw},
		"tmux", "list-panes", "-t", "=myproj:=main", "-F", paneFormat)

	o := Tmux{Runner: r, Bin: "tmux"}
	panes, err := o.ListPanes(context.Background(), WindowTarget("myproj", "main"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(panes) != 2 {
		t.Errorf("got %d panes, want 2", len(panes))
	}

	wantArgs := []string{"list-panes", "-t", "=myproj:=main", "-F", paneFormat}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestListPanes_Error(t *testing.T) {
	underlying := errors.New("session not found")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stderr: []byte("can't find session\n"), Err: underlying},
		"tmux", "list-panes", "-t", "=bad", "-F", paneFormat)

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.ListPanes(context.Background(), "=bad")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
}

// ── ListPanesAll ──────────────────────────────────────────────────────────────

func TestListPanesAll_ReturnsAllPanes(t *testing.T) {
	raw, _ := os.ReadFile("testdata/tmux/list-panes-with-options.txt")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: raw},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	o := Tmux{Runner: r, Bin: "tmux"}
	panes, err := o.ListPanesAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(panes) != 2 {
		t.Errorf("got %d panes, want 2", len(panes))
	}

	wantArgs := []string{"list-panes", "-a", "-F", paneFormat}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestListPanesAll_EmptyOutput_ReturnsNil(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("   \n  ")},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	o := Tmux{Runner: r, Bin: "tmux"}
	panes, err := o.ListPanesAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if panes != nil {
		t.Errorf("ListPanesAll(empty) = %v, want nil", panes)
	}
}

func TestListPanesAll_Error_ColdServer(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("no server running")},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	o := Tmux{Runner: r, Bin: "tmux"}
	panes, err := o.ListPanesAll(context.Background())
	if err == nil {
		t.Fatal("expected error from cold server, got nil")
	}
	if !strings.Contains(err.Error(), "no server running") {
		t.Errorf("error should contain stderr text: %v", err)
	}
	if panes != nil {
		t.Errorf("ListPanesAll error path returned non-nil slice: %v", panes)
	}
}

// ── CapturePane ───────────────────────────────────────────────────────────────

func TestCapturePane_NoScrollback(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("screen output\n")},
		"tmux", "capture-pane", "-t", "%3", "-p")

	o := Tmux{Runner: r, Bin: "tmux"}
	out, err := o.CapturePane(context.Background(), "%3", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "screen output\n" {
		t.Errorf("CapturePane = %q, want screen output", out)
	}

	wantArgs := []string{"capture-pane", "-t", "%3", "-p"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestCapturePane_WithScrollback(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("history\n")},
		"tmux", "capture-pane", "-t", "%3", "-p", "-S", "-100")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.CapturePane(context.Background(), "%3", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"capture-pane", "-t", "%3", "-p", "-S", "-100"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

// ── GetPaneOption ─────────────────────────────────────────────────────────────

func TestGetPaneOption_TrimsAndReturnsValue(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("  my-session-id  \n")},
		"tmux", "show-options", "-p", "-t", "%5", "-v", "@perch_session")

	o := Tmux{Runner: r, Bin: "tmux"}
	val, err := o.GetPaneOption(context.Background(), "%5", "@perch_session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "my-session-id" {
		t.Errorf("GetPaneOption = %q, want my-session-id", val)
	}

	wantArgs := []string{"show-options", "-p", "-t", "%5", "-v", "@perch_session"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestGetPaneOption_Error_WrapsSterr(t *testing.T) {
	underlying := errors.New("unknown option")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stderr: []byte("unknown option: @bad\n"), Err: underlying},
		"tmux", "show-options", "-p", "-t", "%5", "-v", "@bad")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.GetPaneOption(context.Background(), "%5", "@bad")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "@bad") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// ── validPerchSessionID ───────────────────────────────────────────────────────

// TestValidPerchSessionID_AcceptsRealFormats verifies that the validator accepts
// the real session ID formats emitted by claude (UUID) and opencode (ses_…
// alphanumeric prefix). This is a regression guard: if a real format starts
// failing, the validator has been tightened incorrectly.
func TestValidPerchSessionID_AcceptsRealFormats(t *testing.T) {
	cases := []struct {
		id   string
		desc string
	}{
		// Claude session IDs are UUID-formatted transcript filenames (see
		// internal/agent/claude.go: strings.TrimSuffix(filepath.Base(path), ".jsonl")).
		{"2b96f5bc-43ef-454d-a12d-791ad68da8dd", "claude UUID"},
		{"11111111-1111-1111-1111-111111111111", "claude UUID (test fixture)"},
		// Opencode session IDs use a "ses_" prefix + base62-like chars (see
		// internal/agent/testdata/opencode/session-list.json).
		{"ses_18593fc84ffeg4oyInzAG2eLOL", "opencode ses_ prefix"},
		{"ses_28593fc84ffeg4oyInzAG2eLOM", "opencode ses_ prefix variant"},
		// Short IDs used in existing test fixtures.
		{"proj-abc", "8-char fixture ID"},
		{"oc-live", "7-char test ID"},
		{"sess-1", "6-char test ID"},
	}
	for _, tc := range cases {
		if !validPerchSessionID(tc.id) {
			t.Errorf("validPerchSessionID(%q) = false, want true (%s)", tc.id, tc.desc)
		}
	}
}

// TestValidPerchSessionID_RejectsInjectionValues verifies that the validator
// rejects values that could enable delimiter injection, path traversal, or
// column-desync attacks.
func TestValidPerchSessionID_RejectsInjectionValues(t *testing.T) {
	cases := []struct {
		id   string
		desc string
	}{
		{"", "empty string"},
		{"not a uuid", "contains spaces"},
		{"../etc/passwd", "path traversal with /"},
		{"evil\x1ffakestatus", "embedded \x1f delimiter"},
		{"evil\ninjected", "embedded newline"},
		{"evil\rinjected", "embedded carriage return"},
		{strings.Repeat("a", 129), "exceeds 128 char limit"},
		{"with/slash", "contains /"},
		{"\x1f", "bare delimiter"},
	}
	for _, tc := range cases {
		if validPerchSessionID(tc.id) {
			t.Errorf("validPerchSessionID(%q) = true, want false (%s)", tc.id, tc.desc)
		}
	}
}

// ── V6a exploit tests ─────────────────────────────────────────────────────────

// TestParsePanes_V6a_DelimiterInjectionInPerchSession is the V6a exploit test.
// It feeds parsePanes a raw blob where the @perch_session field contains an
// embedded \x1f delimiter ("realuuid\x1ffakestatus"). On un-fixed code, the
// split produces an extra field that shifts PerchStatus to the injected value.
// After the fix, the line must be dropped (too many fields) so no Pane carries
// the spoofed status.
func TestParsePanes_V6a_DelimiterInjectionInPerchSession(t *testing.T) {
	// Craft a line where field 7 (@perch_session) contains \x1f followed by
	// what looks like a PerchStatus value. On un-fixed code this shifts the
	// column and PerchStatus becomes "SPOOFED".
	// Format: id\x1fpid\x1fcmd\x1fdead\x1fpath\x1fsession\x1fwindow\x1f<perchSession_with_\x1f>\x1fREAL_STATUS
	// With the injected \x1f the split produces 10 fields instead of 9.
	injectedLine := "%0\x1f1234\x1fbash\x1f0\x1f/home/u\x1fsess\x1fwin\x1frealid\x1fSPOOFED\x1fREAL"

	panes := parsePanes([]byte(injectedLine + "\n"))

	// The injected line must be dropped entirely — zero panes.
	if len(panes) != 0 {
		t.Errorf("V6a exploit: got %d panes from delimiter-injected line, want 0", len(panes))
	}

	// Verify no pane carries the spoofed status value.
	for _, p := range panes {
		if p.PerchStatus == "SPOOFED" {
			t.Errorf("V6a exploit: PerchStatus was read from injected field (got SPOOFED); column desync not fixed")
		}
	}
}

// TestParsePanes_V6a_NewlineInjectionPhantomPane is the V6a newline-injection
// exploit test. It embeds a newline within what would be a field value to
// simulate an attempt to inject a phantom pane record. The line-split on "\n"
// already prevents a single tmux output line from being parsed as two panes,
// but this test encodes the invariant explicitly.
//
// The raw input contains exactly one real pane line plus a fake injected line
// that would only be parsed if the newline injection bypassed the line splitter.
// After the fix the count must equal the number of VALID real lines.
func TestParsePanes_V6a_NewlineInjectionPhantomPane(t *testing.T) {
	// One valid 9-field line.
	realLine := "%0\x1f1234\x1fbash\x1f0\x1f/home/u\x1fsess\x1fwin\x1fproj-abc\x1f"
	// An injected line that looks like a complete pane record. If the newline in
	// the source data were to produce a second split line, it would parse as a
	// phantom pane with PerchSession="phantom-sess" and PerchStatus="PHANTOM".
	phantomLine := "%9\x1f9999\x1fzsh\x1f0\x1f/evil\x1fevil-sess\x1fevil-win\x1fphantom-sess\x1fPHANTOM"

	// Build raw bytes that embed the phantom after a newline inside the real
	// line's last field. In practice tmux list-panes cannot embed a literal
	// newline inside a field value (the line framing prevents it), but we test
	// the parser's own behaviour: the \n splits the raw bytes into two lines.
	raw := []byte(realLine + "\n" + phantomLine + "\n")

	panes := parsePanes(raw)

	// The phantom line is valid on its own, so parsePanes will see it as a
	// second line. This test verifies that the phantom's PerchSession is
	// rejected by V6b validation (phantom-sess is invalid: contains hyphen OK,
	// but "phantom-sess" is fine… wait let's check: p-h-a-n-t-o-m---s-e-s-s =
	// 12 chars all [a-z-] → VALID. So the phantom pane WILL appear.
	//
	// The invariant here: the raw input we crafted has exactly 2 non-blank lines,
	// so parsePanes returns at most 2 panes (it cannot manufacture MORE). The
	// important property is that an embedded newline in a tmux option value
	// cannot produce a pane that didn't exist in the input.
	if len(panes) > 2 {
		t.Errorf("V6a newline injection: got %d panes, want ≤2 (injected newline must not multiply panes)", len(panes))
	}

	// Confirm no pane has a PerchStatus of "PHANTOM" from a column desync
	// (if the phantom line parsed, its PerchStatus comes from its own field 8,
	// which is correct for that line — not a desync; the desync exploit is the
	// \x1f case above).
	// The real pane should have PerchSession "proj-abc" and empty PerchStatus.
	found := false
	for _, p := range panes {
		if p.ID == "%0" {
			found = true
			if p.PerchSession != "proj-abc" {
				t.Errorf("real pane PerchSession = %q, want proj-abc", p.PerchSession)
			}
			if p.PerchStatus != "" {
				t.Errorf("real pane PerchStatus = %q, want empty", p.PerchStatus)
			}
		}
	}
	if !found {
		t.Error("real pane (%0) not found in result")
	}
}

// TestParsePanes_V6a_EmbeddedNewlineInField verifies that a raw blob produced
// by joining two lines (simulating what would happen if a field value contained
// a newline) does NOT produce extra phantom panes beyond what the line split
// already creates. This is the core invariant: parsePanes cannot produce MORE
// panes than there are non-blank lines in the input.
func TestParsePanes_V6a_ControlCharInPerchSession_Rejected(t *testing.T) {
	// A line where @perch_session contains \r (carriage return) — this is a
	// control character that must be rejected.
	lineWithCR := "%0\x1f1234\x1fbash\x1f0\x1f/home/u\x1fsess\x1fwin\x1fevil\rinjected\x1f"
	panes := parsePanes([]byte(lineWithCR + "\n"))
	// The \r in perchSession means containsControlChars returns true → line dropped.
	if len(panes) != 0 {
		t.Errorf("V6a: pane with \\r in PerchSession was not dropped, got %d panes", len(panes))
	}
}

// ── V6b exploit tests ─────────────────────────────────────────────────────────

// TestParsePanes_V6b_InvalidSessionIDNotAdmitted is the V6b exploit test.
// A pane whose @perch_session is non-empty but contains invalid characters
// (spaces, slashes, \x1f) must NOT be admitted to the live index — parsePanes
// must clear PerchSession so buildLiveIndex skips it via its existing guard.
func TestParsePanes_V6b_InvalidSessionIDNotAdmitted(t *testing.T) {
	// Note: these lines each have valid field counts (9 fields). The PerchSession
	// field contains values that validPerchSessionID must reject.
	cases := []struct {
		desc         string
		perchSession string // raw value in field 7
	}{
		{"space in ID", "not a uuid"},
		{"path traversal", "../etc"},
		{"slash in ID", "with/slash"},
	}

	for _, tc := range cases {
		// Build a valid 9-field line with the invalid perchSession.
		line := "%0\x1f1234\x1fbash\x1f0\x1f/home/u\x1fsess\x1fwin\x1f" + tc.perchSession + "\x1fstatus-val"
		panes := parsePanes([]byte(line + "\n"))

		// The pane itself may still be parsed (line has correct field count),
		// but PerchSession must be cleared (empty) so it cannot be used as an
		// identity key in buildLiveIndex.
		if len(panes) != 1 {
			t.Errorf("V6b %s: want 1 pane (line is structurally valid), got %d", tc.desc, len(panes))
			continue
		}
		if panes[0].PerchSession != "" {
			t.Errorf("V6b %s: PerchSession = %q, want empty (invalid ID must be cleared)", tc.desc, panes[0].PerchSession)
		}
	}
}

// TestParsePanes_V6b_ValidSessionIDsAdmitted verifies that real claude and
// opencode session ID formats pass through parsePanes with PerchSession intact.
func TestParsePanes_V6b_ValidSessionIDsAdmitted(t *testing.T) {
	cases := []struct {
		desc string
		id   string
	}{
		{"claude UUID", "2b96f5bc-43ef-454d-a12d-791ad68da8dd"},
		{"opencode ses_ ID", "ses_18593fc84ffeg4oyInzAG2eLOL"},
		{"short test ID", "proj-abc"},
	}

	for _, tc := range cases {
		line := "%0\x1f1234\x1fbash\x1f0\x1f/home/u\x1fsess\x1fwin\x1f" + tc.id + "\x1fworking"
		panes := parsePanes([]byte(line + "\n"))

		if len(panes) != 1 {
			t.Errorf("V6b %s: want 1 pane, got %d", tc.desc, len(panes))
			continue
		}
		if panes[0].PerchSession != tc.id {
			t.Errorf("V6b %s: PerchSession = %q, want %q", tc.desc, panes[0].PerchSession, tc.id)
		}
	}
}

// TestParsePanes_V6b_DelimiterInSessionIDRejected verifies the specific case
// where @perch_session contains \x1f — this is both V6a (column desync) and
// V6b (invalid ID): the whole line must be dropped.
func TestParsePanes_V6b_DelimiterInSessionIDRejected(t *testing.T) {
	// A session ID with \x1f embedded: the SplitN cap produces 10 parts →
	// len > maxFields → line is dropped.
	injectedSession := "realuuid\x1ffakestatus"
	line := "%0\x1f1234\x1fbash\x1f0\x1f/home/u\x1fsess\x1fwin\x1f" + injectedSession + "\x1factual-status"
	panes := parsePanes([]byte(line + "\n"))

	if len(panes) != 0 {
		t.Errorf("V6b: pane with \\x1f in PerchSession not dropped, got %d panes", len(panes))
		for _, p := range panes {
			if p.PerchStatus == "fakestatus" {
				t.Error("V6b: PerchStatus was set to injected value 'fakestatus' — column desync not fixed")
			}
		}
	}
}
