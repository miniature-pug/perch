package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// ── NewSession ────────────────────────────────────────────────────────────────

func TestNewSession_CallArgsAndPaneID(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("%3\n")},
		"tmux", "new-session", "-d", "-s", "myproj", "-n", "main", "-c", "/home/user/myproject", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.NewSession(context.Background(), "myproj", "main", "/home/user/myproject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%3" {
		t.Errorf("paneID = %q, want %%3", paneID)
	}

	wantArgs := []string{"new-session", "-d", "-s", "myproj", "-n", "main", "-c", "/home/user/myproject", "-P", "-F", "#{pane_id}"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestNewSession_StdoutTrimmed(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("  %0  \n")},
		"tmux", "new-session", "-d", "-s", "s", "-n", "w", "-c", "/tmp", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.NewSession(context.Background(), "s", "w", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%0" {
		t.Errorf("paneID = %q, want %%0 (whitespace trimmed)", paneID)
	}
}

func TestNewSession_ErrorWrapsSterr(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("boom")},
		"tmux", "new-session", "-d", "-s", "s", "-n", "w", "-c", "/tmp", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.NewSession(context.Background(), "s", "w", "/tmp")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

// ── NewWindow ─────────────────────────────────────────────────────────────────

func TestNewWindow_CallArgsAndPaneID(t *testing.T) {
	r := proc.NewFakeRunner()
	// target is SessionTarget("myproj") = "=myproj"
	r.Respond(proc.FakeResult{Stdout: []byte("%5\n")},
		"tmux", "new-window", "-t", "=myproj", "-n", "feat", "-c", "/src", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.NewWindow(context.Background(), "myproj", "feat", "/src")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%5" {
		t.Errorf("paneID = %q, want %%5", paneID)
	}

	wantArgs := []string{"new-window", "-t", "=myproj", "-n", "feat", "-c", "/src", "-P", "-F", "#{pane_id}"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestNewWindow_StdoutTrimmed(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("  %7  \n")},
		"tmux", "new-window", "-t", "=s", "-n", "w", "-c", "/d", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.NewWindow(context.Background(), "s", "w", "/d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%7" {
		t.Errorf("paneID = %q, want %%7", paneID)
	}
}

func TestNewWindow_ErrorWrapsSterr(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("no session")},
		"tmux", "new-window", "-t", "=s", "-n", "w", "-c", "/d", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.NewWindow(context.Background(), "s", "w", "/d")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

// ── SendKeys ──────────────────────────────────────────────────────────────────

func TestSendKeys_TwoCallsRecorded(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "send-keys", "-t", "%3", "-l", "echo hello")
	r.Respond(proc.FakeResult{}, "tmux", "send-keys", "-t", "%3", "Enter")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.SendKeys(context.Background(), "%3", "echo hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(r.Calls))
	}

	wantFirst := []string{"send-keys", "-t", "%3", "-l", "echo hello"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantFirst) {
		t.Errorf("Call[0].Args = %v, want %v", r.Calls[0].Args, wantFirst)
	}

	wantSecond := []string{"send-keys", "-t", "%3", "Enter"}
	if !reflect.DeepEqual(r.Calls[1].Args, wantSecond) {
		t.Errorf("Call[1].Args = %v, want %v", r.Calls[1].Args, wantSecond)
	}
}

func TestSendKeys_FirstCallError_NoEnter(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("bad pane")},
		"tmux", "send-keys", "-t", "%99", "-l", "cmd")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SendKeys(context.Background(), "%99", "cmd")
	if err == nil {
		t.Fatal("expected error from first send-keys call")
	}
	if !strings.Contains(err.Error(), "bad pane") {
		t.Errorf("error should contain stderr: %v", err)
	}

	// Enter must NOT have been sent.
	if len(r.Calls) != 1 {
		t.Errorf("got %d calls, want exactly 1 (Enter must not be sent after error)", len(r.Calls))
	}
}

// ── SetPaneOption ─────────────────────────────────────────────────────────────

func TestSetPaneOption_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", "%5", "@perch_session", "proj-abc")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.SetPaneOption(context.Background(), "%5", "@perch_session", "proj-abc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"set-option", "-p", "-t", "%5", "@perch_session", "proj-abc"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestSetPaneOption_ErrorWrapsSterr(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("unknown option")},
		"tmux", "set-option", "-p", "-t", "%5", "@bad", "val")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SetPaneOption(context.Background(), "%5", "@bad", "val")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// ── SetSessionOption ──────────────────────────────────────────────────────────

func TestSetSessionOption_MouseOn(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{},
		"tmux", "set-option", "-t", "perch", "mouse", "on")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.SetSessionOption(context.Background(), "perch", "mouse", "on"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"set-option", "-t", "perch", "mouse", "on"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestSetSessionOption_StatusOn(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{},
		"tmux", "set-option", "-t", "perch", "status", "on")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.SetSessionOption(context.Background(), "perch", "status", "on"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"set-option", "-t", "perch", "status", "on"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestSetSessionOption_ErrorWrapsSterr(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("unknown option")},
		"tmux", "set-option", "-t", "perch", "mouse", "on")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SetSessionOption(context.Background(), "perch", "mouse", "on")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// ── BindKey ───────────────────────────────────────────────────────────────────

func TestBindKey_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{},
		"tmux", "bind-key", "-T", "perchnav", "F12", "select-pane", "-L")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.BindKey(context.Background(), "perchnav", "F12", "select-pane", "-L"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"bind-key", "-T", "perchnav", "F12", "select-pane", "-L"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestBindKey_ErrorWrapsSterr(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("invalid table")},
		"tmux", "bind-key", "-T", "perchnav", "F12", "select-pane", "-L")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.BindKey(context.Background(), "perchnav", "F12", "select-pane", "-L")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid table") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// ── KillSession ───────────────────────────────────────────────────────────────

func TestKillSession_Success(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "kill-session", "-t", "=myproj")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.KillSession(context.Background(), "myproj"); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestKillSession_ExitOne_Tolerated(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "kill-session", "-t", "=myproj")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.KillSession(context.Background(), "myproj"); err != nil {
		t.Errorf("exit 1 should be tolerated, got: %v", err)
	}
}

func TestKillSession_ExecFailure_Returned(t *testing.T) {
	execErr := errors.New("exec: tmux not found")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: execErr, Stderr: []byte("tmux not found")},
		"tmux", "kill-session", "-t", "=myproj")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.KillSession(context.Background(), "myproj")
	if err == nil {
		t.Fatal("expected error for exec failure")
	}
	if !errors.Is(err, execErr) {
		t.Errorf("error should wrap execErr: %v", err)
	}
}

// ── KillServer ────────────────────────────────────────────────────────────────

func TestKillServer_Success(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "kill-server")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.KillServer(context.Background()); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestKillServer_ExitOne_Tolerated(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}}, "tmux", "kill-server")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.KillServer(context.Background()); err != nil {
		t.Errorf("exit 1 should be tolerated, got: %v", err)
	}
}

func TestKillServer_ExecFailure_Returned(t *testing.T) {
	execErr := errors.New("exec: no such file")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: execErr, Stderr: []byte("no such file")}, "tmux", "kill-server")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.KillServer(context.Background())
	if err == nil {
		t.Fatal("expected error for exec failure")
	}
	if !errors.Is(err, execErr) {
		t.Errorf("error should wrap execErr: %v", err)
	}
}

// ── AttachTargetArgs ──────────────────────────────────────────────────────────

func TestAttachTargetArgs_InsideTmux_SwitchClient(t *testing.T) {
	o := Tmux{
		Getenv: func(s string) string {
			if s == "TMUX" {
				return "/tmp/tmux-1000/default,1234,0"
			}
			return ""
		},
	}
	got := o.AttachTargetArgs("=s:=w")
	want := []string{"switch-client", "-t", "=s:=w"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AttachTargetArgs (inside tmux) = %v, want %v", got, want)
	}
}

func TestAttachTargetArgs_OutsideTmux_AttachSession(t *testing.T) {
	o := Tmux{
		Getenv: func(s string) string { return "" },
	}
	got := o.AttachTargetArgs("=s:=w")
	want := []string{"attach-session", "-t", "=s:=w"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AttachTargetArgs (outside tmux) = %v, want %v", got, want)
	}
}

// ── AttachArgs ────────────────────────────────────────────────────────────────

func TestAttachArgs_InsideTmux_SwitchClient(t *testing.T) {
	o := Tmux{
		Getenv: func(s string) string {
			if s == "TMUX" {
				return "/tmp/tmux-1000/default,1234,0"
			}
			return ""
		},
	}
	got := o.AttachArgs("myproj")
	want := []string{"switch-client", "-t", "=myproj"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AttachArgs (inside tmux) = %v, want %v", got, want)
	}
}

func TestAttachArgs_OutsideTmux_AttachSession(t *testing.T) {
	o := Tmux{
		Getenv: func(s string) string { return "" },
	}
	got := o.AttachArgs("myproj")
	want := []string{"attach-session", "-t", "=myproj"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AttachArgs (outside tmux) = %v, want %v", got, want)
	}
}

// ── SwitchClient ──────────────────────────────────────────────────────────────

func TestSwitchClient_HappyPath(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "switch-client", "-t", "=proj:=feat")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.SwitchClient(context.Background(), "=proj:=feat"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	wantArgs := []string{"switch-client", "-t", "=proj:=feat"}
	if !reflect.DeepEqual(r.Calls[0].Args, wantArgs) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, wantArgs)
	}
}

func TestSwitchClient_ErrorWrapped(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("no client")},
		"tmux", "switch-client", "-t", "=proj:=feat")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SwitchClient(context.Background(), "=proj:=feat")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no client") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// ── SelectPane ────────────────────────────────────────────────────────────────

func TestSelectPane_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "select-pane", "-t", "%3")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.SelectPane(context.Background(), "%3"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	want := []string{"select-pane", "-t", "%3"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestSelectPane_ErrorWrapped(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("no such pane")},
		"tmux", "select-pane", "-t", "%3")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SelectPane(context.Background(), "%3")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such pane") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// ── Connect ───────────────────────────────────────────────────────────────────

func TestConnect_SessionAbsent_CreatesSession(t *testing.T) {
	r := proc.NewFakeRunner()
	// has-session exits 1 → session absent
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=myproj")
	r.Respond(proc.FakeResult{Stdout: []byte("%0\n")},
		"tmux", "new-session", "-d", "-s", "myproj", "-n", "main", "-c", "/work", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.Connect(context.Background(), "myproj", "main", "/work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%0" {
		t.Errorf("paneID = %q, want %%0", paneID)
	}

	// Verify new-session was called (second call after has-session).
	if len(r.Calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(r.Calls))
	}
	if r.Calls[1].Args[0] != "new-session" {
		t.Errorf("second call should be new-session, got: %v", r.Calls[1].Args)
	}
}

// TestConnect_SessionPresent_WindowAbsent_CreatesWindow verifies that when the
// session exists but the target window is absent (list-panes exits ≥1),
// Connect falls through to NewWindow.
func TestConnect_SessionPresent_WindowAbsent_CreatesWindow(t *testing.T) {
	r := proc.NewFakeRunner()
	// has-session exits 0 → session present
	r.Respond(proc.FakeResult{}, "tmux", "has-session", "-t", "=myproj")
	// list-panes for the window exits 1 → window absent
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "list-panes", "-t", "=myproj:=feat", "-F", paneFormat)
	r.Respond(proc.FakeResult{Stdout: []byte("%4\n")},
		"tmux", "new-window", "-t", "=myproj", "-n", "feat", "-c", "/work", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.Connect(context.Background(), "myproj", "feat", "/work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%4" {
		t.Errorf("paneID = %q, want %%4", paneID)
	}

	if len(r.Calls) != 3 {
		t.Fatalf("want 3 tmux calls (has-session, list-panes, new-window), got %d: %v", len(r.Calls), r.Calls)
	}
	// Verify new-window was called (scan, not index, since call count changed).
	found := false
	for _, c := range r.Calls {
		if c.Args[0] == "new-window" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a new-window call; calls: %v", r.Calls)
	}
}

func TestConnect_HasSessionError_ReturnsError(t *testing.T) {
	execErr := errors.New("exec: tmux not found")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: execErr},
		"tmux", "has-session", "-t", "=myproj")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.Connect(context.Background(), "myproj", "main", "/work")
	if err == nil {
		t.Fatal("expected error from HasSession exec failure")
	}
}

// livePaneLine builds a valid 8-field list-panes output line for one pane.
// dead=false → field[3]="0"; dead=true → field[3]="1".
func livePaneLine(id string, dead bool) []byte {
	deadField := "0"
	if dead {
		deadField = "1"
	}
	line := id + "\x1f1234\x1fbash\x1f" + deadField + "\x1f/work\x1fmyproj\x1ffeat\x1f\n"
	return []byte(line)
}

// TestConnect_SessionPresent_LivePane_Reuses verifies that when the session
// and window both exist with a live pane, Connect returns that pane's ID
// without issuing a new-window call.
func TestConnect_SessionPresent_LivePane_Reuses(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "has-session", "-t", "=myproj")
	r.Respond(proc.FakeResult{Stdout: livePaneLine("%7", false)},
		"tmux", "list-panes", "-t", "=myproj:=feat", "-F", paneFormat)

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.Connect(context.Background(), "myproj", "feat", "/work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%7" {
		t.Errorf("paneID = %q, want %%7 (reused)", paneID)
	}

	// No new-window call must have been made.
	for _, c := range r.Calls {
		if c.Args[0] == "new-window" {
			t.Errorf("unexpected new-window call; calls: %v", r.Calls)
		}
	}
}

// TestConnect_SessionPresent_AllDeadPanes_CreatesWindow verifies that when the
// window exists but all its panes are dead, Connect falls through to NewWindow.
func TestConnect_SessionPresent_AllDeadPanes_CreatesWindow(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "has-session", "-t", "=myproj")
	r.Respond(proc.FakeResult{Stdout: livePaneLine("%8", true)},
		"tmux", "list-panes", "-t", "=myproj:=feat", "-F", paneFormat)
	r.Respond(proc.FakeResult{Stdout: []byte("%9\n")},
		"tmux", "new-window", "-t", "=myproj", "-n", "feat", "-c", "/work", "-P", "-F", "#{pane_id}")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.Connect(context.Background(), "myproj", "feat", "/work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%9" {
		t.Errorf("paneID = %q, want %%9 (new window)", paneID)
	}

	found := false
	for _, c := range r.Calls {
		if c.Args[0] == "new-window" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a new-window call; calls: %v", r.Calls)
	}
}

// ── KillWindow ────────────────────────────────────────────────────────────────

func TestKillWindow_Success(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "kill-window", "-t", "=s:=w")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.KillWindow(context.Background(), "=s:=w"); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
	want := []string{"kill-window", "-t", "=s:=w"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestKillWindow_ExitOne_Tolerated(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "kill-window", "-t", "=s:=w")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.KillWindow(context.Background(), "=s:=w"); err != nil {
		t.Errorf("exit 1 should be tolerated, got: %v", err)
	}
}

func TestKillWindow_ExecFailure_Returned(t *testing.T) {
	execErr := errors.New("exec: tmux not found")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: execErr, Stderr: []byte("tmux not found")},
		"tmux", "kill-window", "-t", "=s:=w")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.KillWindow(context.Background(), "=s:=w")
	if err == nil {
		t.Fatal("expected error for exec failure")
	}
	if !errors.Is(err, execErr) {
		t.Errorf("error should wrap execErr: %v", err)
	}
}

// ── CurrentClientWindow ───────────────────────────────────────────────────────

func TestCurrentClientWindow_HappyPath(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("sess\x1fwin\n")},
		"tmux", "display-message", "-p", "-F", "#{session_name}\x1f#{window_name}")

	o := Tmux{Runner: r, Bin: "tmux"}
	sess, win, err := o.CurrentClientWindow(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != "sess" {
		t.Errorf("session = %q, want sess", sess)
	}
	if win != "win" {
		t.Errorf("window = %q, want win", win)
	}

	want := []string{"display-message", "-p", "-F", "#{session_name}\x1f#{window_name}"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestCurrentClientWindow_MalformedOutput_ReturnsError(t *testing.T) {
	r := proc.NewFakeRunner()
	// No \x1f separator in output.
	r.Respond(proc.FakeResult{Stdout: []byte("noseparator\n")},
		"tmux", "display-message", "-p", "-F", "#{session_name}\x1f#{window_name}")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, _, err := o.CurrentClientWindow(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed output")
	}
	if !strings.Contains(err.Error(), "unexpected output") {
		t.Errorf("error should mention 'unexpected output': %v", err)
	}
}

func TestCurrentClientWindow_ExecError_Wrapped(t *testing.T) {
	execErr := errors.New("exec: tmux not found")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: execErr, Stderr: []byte("not found")},
		"tmux", "display-message", "-p", "-F", "#{session_name}\x1f#{window_name}")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, _, err := o.CurrentClientWindow(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, execErr) {
		t.Errorf("error should wrap execErr: %v", err)
	}
}
