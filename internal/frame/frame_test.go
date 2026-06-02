package frame_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/frame"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newFakeSession returns a Tmux backed by a FakeRunner whose Default is a
// success result with empty stdout. Callers override individual commands via
// Respond.
func newFakeTmux(r *proc.FakeRunner) tmux.Tmux {
	return tmux.Tmux{Runner: r, Bin: "tmux"}
}

// ── Ensure CREATE path ────────────────────────────────────────────────────────

// TestEnsure_Create verifies that when the target session does not exist,
// Ensure runs: has-session (false), Launch (new-session + send-keys×2),
// set-option (@perch_frame=1), split-window, display-message (PaneSize),
// resize-pane.
func TestEnsure_Create(t *testing.T) {
	r := proc.NewFakeRunner()
	// has-session → exit 1 (session absent)
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=perch")
	// new-session → sidebar pane id
	r.Respond(proc.FakeResult{Stdout: []byte("%1\n")},
		"tmux", "new-session", "-d", "-s", "perch", "-n", "frame", "-c", "/root", "-P", "-F", "#{pane_id}")
	// send-keys literal (the quoted sidebar command)
	r.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", "%1", "-l", "'perch' '--sidebar'")
	// send-keys Enter
	r.Respond(proc.FakeResult{},
		"tmux", "send-keys", "-t", "%1", "Enter")
	// set-option @perch_frame=1 on sidebar pane
	r.Respond(proc.FakeResult{},
		"tmux", "set-option", "-p", "-t", "%1", frame.FrameMarker, "1")
	// split-window → main/placeholder pane id
	r.Respond(proc.FakeResult{Stdout: []byte("%2\n")},
		"tmux", "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "=perch:=frame", "-c", "/root", "sleep infinity")
	// display-message (PaneSize on sidebar) → "50\x1f24"
	r.Respond(proc.FakeResult{Stdout: []byte("50\x1f24\n")},
		"tmux", "display-message", "-p", "-t", "%1", "#{pane_width}\x1f#{pane_height}")
	// resize-pane sidebar to 50 cols
	r.Respond(proc.FakeResult{},
		"tmux", "resize-pane", "-t", "%1", "-x", "50", "-y", "24")

	tmx := newFakeTmux(r)
	info, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err != nil {
		t.Fatal("Ensure CREATE: unexpected error:", err)
	}
	if !info.Created {
		t.Error("Ensure CREATE: expected Created=true")
	}
	if info.Session != "perch" {
		t.Errorf("Ensure CREATE: Session=%q, want %q", info.Session, "perch")
	}
	if info.SidebarPane != "%1" {
		t.Errorf("Ensure CREATE: SidebarPane=%q, want %%1", info.SidebarPane)
	}
	if info.MainPane != "%2" {
		t.Errorf("Ensure CREATE: MainPane=%q, want %%2", info.MainPane)
	}
}

// TestEnsure_Create_HasExpectedCalls checks that Ensure emits the right tmux
// subcommands in the right order on the CREATE path, so a future refactor can't
// silently reorder the bootstrap sequence.
func TestEnsure_Create_HasExpectedCalls(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{} // succeed everything by default
	// has-session fails → session absent
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=perch")
	// new-session → sidebar pane
	r.Respond(proc.FakeResult{Stdout: []byte("%1\n")},
		"tmux", "new-session", "-d", "-s", "perch", "-n", "frame", "-c", "/root", "-P", "-F", "#{pane_id}")
	// split-window → main pane
	r.Respond(proc.FakeResult{Stdout: []byte("%2\n")},
		"tmux", "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "=perch:=frame", "-c", "/root", "sleep infinity")
	// PaneSize → give a real size so ResizePane is called with deterministic values
	r.Respond(proc.FakeResult{Stdout: []byte("120\x1f40\n")},
		"tmux", "display-message", "-p", "-t", "%1", "#{pane_width}\x1f#{pane_height}")

	tmx := newFakeTmux(r)
	_, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	// Build the ordered list of subcommands from Calls[i].Args[0].
	subCmds := make([]string, 0, len(r.Calls))
	for _, c := range r.Calls {
		if len(c.Args) > 0 {
			subCmds = append(subCmds, c.Args[0])
		}
	}
	// Launch calls Connect which calls HasSession internally — so has-session
	// appears twice: once from Ensure itself, once from Launch→Connect.
	want := []string{
		"has-session",     // Ensure: check session absent
		"has-session",     // Launch→Connect: same check before new-session
		"new-session",     // Launch→Connect→NewSession: create the session
		"send-keys",       // Launch: send literal command
		"send-keys",       // Launch: send Enter
		"set-option",      // Ensure: stamp @perch_frame=1
		"split-window",    // Ensure: add main/placeholder pane
		"set-option",      // Ensure: set remain-on-exit on frame window (M17-2)
		"display-message", // Ensure: PaneSize for resize
		"resize-pane",     // Ensure: resize sidebar to sidebarWidth
		"bind-key",        // Ensure: bind F12 in perchnav table
		"set-option",      // Ensure: set key-table perchnav
		"set-option",      // Ensure: mouse on
		"set-option",      // Ensure: status on
		"set-option",      // Ensure: status-left-length 200
		"set-option",      // Ensure: status-left statusLeft
		"set-option",      // Ensure: status-right ""
	}
	if !reflect.DeepEqual(subCmds, want) {
		t.Errorf("subcommand sequence mismatch:\ngot  %v\nwant %v", subCmds, want)
	}
}

// TestEnsure_Create_SessionOptions verifies that createFrame applies the
// session-level options (bind-key + set-option calls) after the sidebar resize,
// in the required order, and that failures are silently swallowed (best-effort).
func TestEnsure_Create_SessionOptions(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{} // succeed everything by default
	// has-session fails → session absent
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=perch")
	// new-session → sidebar pane
	r.Respond(proc.FakeResult{Stdout: []byte("%1\n")},
		"tmux", "new-session", "-d", "-s", "perch", "-n", "frame", "-c", "/root", "-P", "-F", "#{pane_id}")
	// split-window → main pane
	r.Respond(proc.FakeResult{Stdout: []byte("%2\n")},
		"tmux", "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "=perch:=frame", "-c", "/root", "sleep infinity")
	// PaneSize
	r.Respond(proc.FakeResult{Stdout: []byte("120\x1f40\n")},
		"tmux", "display-message", "-p", "-t", "%1", "#{pane_width}\x1f#{pane_height}")

	tmx := newFakeTmux(r)
	info, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if !info.Created {
		t.Error("expected Created=true")
	}

	// Collect calls after resize-pane (which is the last non-option call).
	// We find the resize-pane call index then check subsequent calls.
	resizeIdx := -1
	for i, c := range r.Calls {
		if len(c.Args) > 0 && c.Args[0] == "resize-pane" {
			resizeIdx = i
			break
		}
	}
	if resizeIdx < 0 {
		t.Fatal("resize-pane call not found")
	}

	// The calls after resize-pane must match the expected session-option sequence.
	afterResize := r.Calls[resizeIdx+1:]
	type wantCall struct {
		sub  string   // Args[0]
		rest []string // Args[1:]
	}
	wantCalls := []wantCall{
		{"bind-key", []string{"-T", "perchnav", "F12", "select-pane", "-L"}},
		{"set-option", []string{"-t", "perch", "key-table", "perchnav"}},
		{"set-option", []string{"-t", "perch", "mouse", "on"}},
		{"set-option", []string{"-t", "perch", "status", "on"}},
		{"set-option", []string{"-t", "perch", "status-left-length", "200"}},
		{"set-option", []string{"-t", "perch", "status-left", " perch │ F12/click ▸ list   ↵ ▸ open/resume   esc ▸ close window   q ▸ quit (agents live) "}},
		{"set-option", []string{"-t", "perch", "status-right", ""}},
	}
	if len(afterResize) != len(wantCalls) {
		t.Fatalf("after resize-pane: got %d calls, want %d:\n%v",
			len(afterResize), len(wantCalls), afterResize)
	}
	for i, w := range wantCalls {
		c := afterResize[i]
		if len(c.Args) == 0 {
			t.Errorf("call[%d]: empty args", i)
			continue
		}
		if c.Args[0] != w.sub {
			t.Errorf("call[%d]: subcommand=%q, want %q", i, c.Args[0], w.sub)
		}
		if !reflect.DeepEqual(c.Args[1:], w.rest) {
			t.Errorf("call[%d]: args[1:]=%v, want %v", i, c.Args[1:], w.rest)
		}
	}
}

// TestEnsure_Create_RemainOnExit verifies that createFrame issues
// set-option -w -t =perch:=frame remain-on-exit on after the split-window call.
func TestEnsure_Create_RemainOnExit(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{} // succeed everything by default
	// has-session fails → session absent
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=perch")
	// new-session → sidebar pane
	r.Respond(proc.FakeResult{Stdout: []byte("%1\n")},
		"tmux", "new-session", "-d", "-s", "perch", "-n", "frame", "-c", "/root", "-P", "-F", "#{pane_id}")
	// split-window → main pane
	r.Respond(proc.FakeResult{Stdout: []byte("%2\n")},
		"tmux", "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "=perch:=frame", "-c", "/root", "sleep infinity")

	tmx := newFakeTmux(r)
	_, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	// Find split-window call index; remain-on-exit must come after it.
	splitIdx := -1
	for i, c := range r.Calls {
		if len(c.Args) > 0 && c.Args[0] == "split-window" {
			splitIdx = i
			break
		}
	}
	if splitIdx < 0 {
		t.Fatal("split-window call not found")
	}

	// Search for the set-option -w remain-on-exit call after split-window.
	found := false
	for _, c := range r.Calls[splitIdx+1:] {
		if len(c.Args) >= 6 &&
			c.Args[0] == "set-option" &&
			c.Args[1] == "-w" &&
			c.Args[2] == "-t" &&
			c.Args[3] == "=perch:=frame" &&
			c.Args[4] == "remain-on-exit" &&
			c.Args[5] == "on" {
			found = true
			break
		}
	}
	if !found {
		t.Error("createFrame: missing set-option -w -t =perch:=frame remain-on-exit on after split-window")
	}
}

// ── Ensure REUSE path ─────────────────────────────────────────────────────────

// TestEnsure_Reuse verifies that when a perch-marked session already exists,
// Ensure returns Info{Created:false} with the sidebar+main pane ids and fires
// NO mutation calls (no new-session, split-window, set-option, resize-pane).
func TestEnsure_Reuse(t *testing.T) {
	r := proc.NewFakeRunner()
	// has-session → session present
	r.Respond(proc.FakeResult{},
		"tmux", "has-session", "-t", "=perch")
	// list-panes on the perch:frame window → two panes
	listPanesOut := "%1\x1f123\x1fzsh\x1f0\x1f/root\x1fperch\x1fframe\x1f\x1f\n" +
		"%2\x1f456\x1fsleep\x1f0\x1f/root\x1fperch\x1fframe\x1f\x1f\n"
	r.Respond(proc.FakeResult{Stdout: []byte(listPanesOut)},
		"tmux", "list-panes", "-t", "=perch:=frame", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")
	// GetPaneOption @perch_frame on %1 → "1" (marked)
	r.Respond(proc.FakeResult{Stdout: []byte("1\n")},
		"tmux", "show-options", "-p", "-t", "%1", "-v", frame.FrameMarker)
	// GetPaneOption @perch_frame on %2 → "" (not marked)
	r.Respond(proc.FakeResult{Stdout: []byte("\n")},
		"tmux", "show-options", "-p", "-t", "%2", "-v", frame.FrameMarker)

	tmx := newFakeTmux(r)
	info, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err != nil {
		t.Fatal("Ensure REUSE: unexpected error:", err)
	}
	if info.Created {
		t.Error("Ensure REUSE: expected Created=false")
	}
	if info.Session != "perch" {
		t.Errorf("Ensure REUSE: Session=%q, want perch", info.Session)
	}
	if info.SidebarPane != "%1" {
		t.Errorf("Ensure REUSE: SidebarPane=%q, want %%1", info.SidebarPane)
	}
	if info.MainPane != "%2" {
		t.Errorf("Ensure REUSE: MainPane=%q, want %%2", info.MainPane)
	}
	// No mutation should have been called.
	for _, c := range r.Calls {
		if len(c.Args) == 0 {
			continue
		}
		switch c.Args[0] {
		case "new-session", "split-window", "set-option", "resize-pane":
			t.Errorf("REUSE path must not call %q", c.Args[0])
		}
	}
}

// ── Ensure FD-03: non-perch session ──────────────────────────────────────────

// TestEnsure_FD03_NonPerchSession verifies that Ensure returns an error (and
// performs no mutation) when a session named "perch" exists but bears no
// @perch_frame marker on any pane.
func TestEnsure_FD03_NonPerchSession(t *testing.T) {
	r := proc.NewFakeRunner()
	// has-session → session present
	r.Respond(proc.FakeResult{},
		"tmux", "has-session", "-t", "=perch")
	// list-panes → two unmarked panes
	listPanesOut := "%10\x1f1\x1fzsh\x1f0\x1f/home\x1fperch\x1fwork\x1f\x1f\n" +
		"%11\x1f2\x1fvim\x1f0\x1f/home\x1fperch\x1fwork\x1f\x1f\n"
	r.Respond(proc.FakeResult{Stdout: []byte(listPanesOut)},
		"tmux", "list-panes", "-t", "=perch:=frame", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")
	// GetPaneOption returns "" for both panes — no marker.
	r.Default = &proc.FakeResult{Stdout: []byte("\n")}

	tmx := newFakeTmux(r)
	_, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err == nil {
		t.Fatal("Ensure FD-03: expected error for non-perch session, got nil")
	}
	if !strings.Contains(err.Error(), "not a perch frame") {
		t.Errorf("Ensure FD-03: error should mention 'not a perch frame'; got: %v", err)
	}
	// No mutations must have fired.
	for _, c := range r.Calls {
		if len(c.Args) == 0 {
			continue
		}
		switch c.Args[0] {
		case "new-session", "split-window", "set-option", "resize-pane":
			t.Errorf("FD-03 guard must not call %q", c.Args[0])
		}
	}
}

// TestEnsure_FD03_ListPanesFallback verifies FD-03 even when list-panes on the
// "frame" window fails (e.g. the existing session has a different window name):
// Ensure should still not clobber the session and return an error.
func TestEnsure_FD03_ListPanesFallback(t *testing.T) {
	r := proc.NewFakeRunner()
	// has-session → present
	r.Respond(proc.FakeResult{},
		"tmux", "has-session", "-t", "=perch")
	// list-panes on =perch:=frame fails (window doesn't exist)
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "list-panes", "-t", "=perch:=frame", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")

	tmx := newFakeTmux(r)
	_, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err == nil {
		t.Fatal("expected error when frame window absent, got nil")
	}
	if !strings.Contains(err.Error(), "not a perch frame") {
		t.Errorf("expected 'not a perch frame' error; got: %v", err)
	}
}

// ── SidebarContext ─────────────────────────────────────────────────────────────

// TestSidebarContext_PicksSiblingPane verifies that SidebarContext returns the
// pane that is NOT the sidebar (own TMUX_PANE) as the placeholder.
func TestSidebarContext_PicksSiblingPane(t *testing.T) {
	r := proc.NewFakeRunner()
	// display-message (CurrentClientWindow) → session "perch", window "frame"
	r.Respond(proc.FakeResult{Stdout: []byte("perch\x1fframe\n")},
		"tmux", "display-message", "-p", "-F", "#{session_name}\x1f#{window_name}")
	// list-panes on the window → sidebar (%1) and main/placeholder (%2)
	listPanesOut := "%1\x1f100\x1fperch\x1f0\x1f/root\x1fperch\x1fframe\x1f\x1f\n" +
		"%2\x1f101\x1fsleep\x1f0\x1f/root\x1fperch\x1fframe\x1f\x1f\n"
	r.Respond(proc.FakeResult{Stdout: []byte(listPanesOut)},
		"tmux", "list-panes", "-t", "=perch:=frame", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")

	tmx := newFakeTmux(r)
	getenv := func(k string) string {
		if k == "TMUX_PANE" {
			return "%1"
		}
		return ""
	}

	frameSession, placeholderPane, err := frame.SidebarContext(context.Background(), tmx, getenv)
	if err != nil {
		t.Fatal("SidebarContext: unexpected error:", err)
	}
	if frameSession != "perch" {
		t.Errorf("SidebarContext: frameSession=%q, want perch", frameSession)
	}
	if placeholderPane != "%2" {
		t.Errorf("SidebarContext: placeholderPane=%q, want %%2", placeholderPane)
	}
}

// TestSidebarContext_ErrorOnMissingTMUX_PANE verifies that SidebarContext
// returns an error when TMUX_PANE is empty (not running inside tmux).
func TestSidebarContext_ErrorOnMissingTMUX_PANE(t *testing.T) {
	r := proc.NewFakeRunner()
	tmx := newFakeTmux(r)
	getenv := func(string) string { return "" }
	_, _, err := frame.SidebarContext(context.Background(), tmx, getenv)
	if err == nil {
		t.Fatal("expected error when TMUX_PANE is empty")
	}
}

// TestSidebarContext_ErrorWhenNoSibling verifies SidebarContext returns an
// error when list-panes only returns one pane (degenerate / race).
func TestSidebarContext_ErrorWhenNoSibling(t *testing.T) {
	r := proc.NewFakeRunner()
	// CurrentClientWindow
	r.Respond(proc.FakeResult{Stdout: []byte("perch\x1fframe\n")},
		"tmux", "display-message", "-p", "-F", "#{session_name}\x1f#{window_name}")
	// only one pane in the window
	listPanesOut := "%1\x1f100\x1fperch\x1f0\x1f/root\x1fperch\x1fframe\x1f\x1f\n"
	r.Respond(proc.FakeResult{Stdout: []byte(listPanesOut)},
		"tmux", "list-panes", "-t", "=perch:=frame", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")

	tmx := newFakeTmux(r)
	getenv := func(k string) string {
		if k == "TMUX_PANE" {
			return "%1"
		}
		return ""
	}
	_, _, err := frame.SidebarContext(context.Background(), tmx, getenv)
	if err == nil {
		t.Fatal("expected error when no sibling pane found")
	}
}

// ── DefaultFrameSession constant ─────────────────────────────────────────────

func TestConstants(t *testing.T) {
	if frame.FrameMarker != "@perch_frame" {
		t.Errorf("FrameMarker=%q, want @perch_frame", frame.FrameMarker)
	}
	if frame.DefaultFrameSession != "perch" {
		t.Errorf("DefaultFrameSession=%q, want perch", frame.DefaultFrameSession)
	}
}

// ── Ensure returns error when ListPanes returns 0 panes (REUSE path) ─────────

func TestEnsure_Reuse_NoPanesError(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{},
		"tmux", "has-session", "-t", "=perch")
	// list-panes returns empty
	r.Respond(proc.FakeResult{Stdout: []byte("\n")},
		"tmux", "list-panes", "-t", "=perch:=frame", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")

	tmx := newFakeTmux(r)
	_, err := frame.Ensure(context.Background(), tmx, "perch", "/root", []string{"perch", "--sidebar"})
	if err == nil {
		t.Fatal("expected error when REUSE path finds no panes")
	}
	if !strings.Contains(err.Error(), "not a perch frame") {
		t.Errorf("expected 'not a perch frame' error; got: %v", err)
	}
}
