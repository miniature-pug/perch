package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// ── SwapPane ──────────────────────────────────────────────────────────────────

func TestSwapPane_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "swap-pane", "-s", "%3", "-t", "%7")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.SwapPane(context.Background(), "%3", "%7"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	want := []string{"swap-pane", "-s", "%3", "-t", "%7"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestSwapPane_EmptySrc_Rejected(t *testing.T) {
	r := proc.NewFakeRunner()
	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SwapPane(context.Background(), "", "%7")
	if err == nil {
		t.Fatal("expected error for empty src")
	}
	if len(r.Calls) != 0 {
		t.Errorf("runner must not be called on validation failure, got %d calls", len(r.Calls))
	}
}

func TestSwapPane_EmptyDst_Rejected(t *testing.T) {
	r := proc.NewFakeRunner()
	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SwapPane(context.Background(), "%3", "")
	if err == nil {
		t.Fatal("expected error for empty dst")
	}
	if len(r.Calls) != 0 {
		t.Errorf("runner must not be called on validation failure, got %d calls", len(r.Calls))
	}
}

func TestSwapPane_DashLeadingSrc_Rejected(t *testing.T) {
	r := proc.NewFakeRunner()
	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SwapPane(context.Background(), "-bad", "%7")
	if err == nil {
		t.Fatal("expected error for dash-leading src")
	}
	if len(r.Calls) != 0 {
		t.Errorf("runner must not be called on validation failure, got %d calls", len(r.Calls))
	}
}

func TestSwapPane_DashLeadingDst_Rejected(t *testing.T) {
	r := proc.NewFakeRunner()
	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SwapPane(context.Background(), "%3", "-bad")
	if err == nil {
		t.Fatal("expected error for dash-leading dst")
	}
	if len(r.Calls) != 0 {
		t.Errorf("runner must not be called on validation failure, got %d calls", len(r.Calls))
	}
}

func TestSwapPane_ErrorWrapped(t *testing.T) {
	underlying := errors.New("can't swap")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: underlying, Stderr: []byte("can't swap panes")},
		"tmux", "swap-pane", "-s", "%3", "-t", "%7")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.SwapPane(context.Background(), "%3", "%7")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "can't swap panes") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

// ── SplitWindow ───────────────────────────────────────────────────────────────

func TestSplitWindow_Horizontal_NoCmd(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("%7\n")},
		"tmux", "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "%3", "-c", "/work")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.SplitWindow(context.Background(), "%3", "/work", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%7" {
		t.Errorf("paneID = %q, want %%7", paneID)
	}

	want := []string{"split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "%3", "-c", "/work"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestSplitWindow_Vertical_WithCmd(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("%9\n")},
		"tmux", "split-window", "-d", "-v", "-P", "-F", "#{pane_id}", "-t", "%5", "-c", "/tmp", "sleep infinity")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.SplitWindow(context.Background(), "%5", "/tmp", false, "sleep infinity")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%9" {
		t.Errorf("paneID = %q, want %%9", paneID)
	}

	want := []string{"split-window", "-d", "-v", "-P", "-F", "#{pane_id}", "-t", "%5", "-c", "/tmp", "sleep infinity"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestSplitWindow_StdoutTrimmed(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("  %7  \n")},
		"tmux", "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "%3", "-c", "/work")

	o := Tmux{Runner: r, Bin: "tmux"}
	paneID, err := o.SplitWindow(context.Background(), "%3", "/work", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "%7" {
		t.Errorf("paneID = %q, want %%7 (whitespace trimmed)", paneID)
	}
}

func TestSplitWindow_ErrorWrapped(t *testing.T) {
	underlying := errors.New("no room")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: underlying, Stderr: []byte("no room to split")},
		"tmux", "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", "%3", "-c", "/work")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, err := o.SplitWindow(context.Background(), "%3", "/work", true, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "no room to split") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

// ── ResizeWindow ──────────────────────────────────────────────────────────────

func TestResizeWindow_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "resize-window", "-t", "=perch", "-x", "200", "-y", "50")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.ResizeWindow(context.Background(), "=perch", 200, 50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"resize-window", "-t", "=perch", "-x", "200", "-y", "50"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestResizeWindow_ErrorWrapped(t *testing.T) {
	underlying := errors.New("no such session")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: underlying, Stderr: []byte("no such session: badname")},
		"tmux", "resize-window", "-t", "=badname", "-x", "80", "-y", "24")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.ResizeWindow(context.Background(), "=badname", 80, 24)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "no such session") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

// ── ResizePane ────────────────────────────────────────────────────────────────

func TestResizePane_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "resize-pane", "-t", "%5", "-x", "120", "-y", "40")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.ResizePane(context.Background(), "%5", 120, 40); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"resize-pane", "-t", "%5", "-x", "120", "-y", "40"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestResizePane_ErrorWrapped(t *testing.T) {
	underlying := errors.New("no such pane")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: underlying, Stderr: []byte("no such pane: %99")},
		"tmux", "resize-pane", "-t", "%99", "-x", "80", "-y", "24")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.ResizePane(context.Background(), "%99", 80, 24)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "no such pane") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

// ── RefreshClient ─────────────────────────────────────────────────────────────

func TestRefreshClient_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "refresh-client")

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.RefreshClient(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	want := []string{"refresh-client"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestRefreshClient_ErrorWrapped(t *testing.T) {
	underlying := errors.New("no client")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: underlying, Stderr: []byte("no client found")},
		"tmux", "refresh-client")

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.RefreshClient(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "no client found") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

// ── PaneSize ──────────────────────────────────────────────────────────────────

func TestPaneSize_CallArgs(t *testing.T) {
	r := proc.NewFakeRunner()
	// PaneSize uses display-message -p -t <paneID> '#{pane_width}\x1f#{pane_height}'
	r.Respond(proc.FakeResult{Stdout: []byte("120\x1f40\n")},
		"tmux", "display-message", "-p", "-t", "%5", "#{pane_width}\x1f#{pane_height}")

	o := Tmux{Runner: r, Bin: "tmux"}
	w, h, err := o.PaneSize(context.Background(), "%5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 120 {
		t.Errorf("width = %d, want 120", w)
	}
	if h != 40 {
		t.Errorf("height = %d, want 40", h)
	}

	want := []string{"display-message", "-p", "-t", "%5", "#{pane_width}\x1f#{pane_height}"}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Call.Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestPaneSize_ParsesCorrectly(t *testing.T) {
	cases := []struct {
		stdout string
		wantW  int
		wantH  int
	}{
		{"80\x1f24\n", 80, 24},
		{"200\x1f50\n", 200, 50},
		{"  160 \x1f 48 \n", 160, 48}, // trimmed whitespace
	}
	for _, tc := range cases {
		r := proc.NewFakeRunner()
		r.Default = &proc.FakeResult{Stdout: []byte(tc.stdout)}
		o := Tmux{Runner: r, Bin: "tmux"}
		w, h, err := o.PaneSize(context.Background(), "%1")
		if err != nil {
			t.Fatalf("stdout=%q: unexpected error: %v", tc.stdout, err)
		}
		if w != tc.wantW || h != tc.wantH {
			t.Errorf("stdout=%q: got %d×%d, want %d×%d", tc.stdout, w, h, tc.wantW, tc.wantH)
		}
	}
}

func TestPaneSize_MalformedOutput_ReturnsError(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{Stdout: []byte("noseparator\n")}
	o := Tmux{Runner: r, Bin: "tmux"}
	_, _, err := o.PaneSize(context.Background(), "%1")
	if err == nil {
		t.Fatal("expected error for malformed output")
	}
	if !strings.Contains(err.Error(), "unexpected output") {
		t.Errorf("error should mention 'unexpected output': %v", err)
	}
}

func TestPaneSize_NonIntWidth_ReturnsError(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{Stdout: []byte("notanint\x1f24\n")}
	o := Tmux{Runner: r, Bin: "tmux"}
	_, _, err := o.PaneSize(context.Background(), "%1")
	if err == nil {
		t.Fatal("expected error for non-integer width")
	}
}

func TestPaneSize_NonIntHeight_ReturnsError(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{Stdout: []byte("80\x1fnotanint\n")}
	o := Tmux{Runner: r, Bin: "tmux"}
	_, _, err := o.PaneSize(context.Background(), "%1")
	if err == nil {
		t.Fatal("expected error for non-integer height")
	}
}

func TestPaneSize_ErrorWrapped(t *testing.T) {
	underlying := errors.New("no such pane")
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: underlying, Stderr: []byte("can't find pane %99")},
		"tmux", "display-message", "-p", "-t", "%99", "#{pane_width}\x1f#{pane_height}")

	o := Tmux{Runner: r, Bin: "tmux"}
	_, _, err := o.PaneSize(context.Background(), "%99")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("error should wrap underlying: %v", err)
	}
	if !strings.Contains(err.Error(), "can't find pane") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}
