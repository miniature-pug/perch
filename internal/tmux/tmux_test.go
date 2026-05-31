package tmux

import (
	"context"
	"errors"
	"os"
	"reflect"
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
	if !containsStr(err.Error(), "no server running") {
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
	if !containsStr(err.Error(), "@bad") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
