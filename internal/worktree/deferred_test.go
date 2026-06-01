package worktree

import (
	"context"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

func TestDeferredRemove_Success(t *testing.T) {
	baseDir := t.TempDir()

	// Seed a window record to be removed.
	w := model.Window{
		PaneKey:     "%5",
		Tool:        model.ToolClaude,
		SessionID:   "sess-abc",
		Tree:        "/work/myrepo",
		TmuxSession: "myproj",
		TmuxWindow:  "feat",
	}
	if err := state.SaveWindow(baseDir, w); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	opts := tmux.CleanupOpts{
		SourceWindowTarget: "=myproj:=feat",
		Tree:               "/work/myrepo",
		Branch:             "feat",
		RepoDir:            "/work/myrepo",
	}
	now := int64(1700000000)
	suffix := "abc123"
	wantScript := tmux.CleanupScript(opts, now, suffix)

	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "tmux", "run-shell", "-b", wantScript)

	tk := tmux.Tmux{Runner: r, Bin: "tmux"}
	if err := DeferredRemove(context.Background(), tk, baseDir, opts, "%5", now, suffix); err != nil {
		t.Fatalf("DeferredRemove: %v", err)
	}

	// Assert a run-shell -b call was recorded with the expected script.
	found := false
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 3 && c.Args[0] == "run-shell" && c.Args[1] == "-b" {
			if c.Args[2] == wantScript {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected run-shell -b <script> call; calls: %v", r.Calls)
	}

	// Assert the window record was removed.
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("want 0 window records after DeferredRemove, got %d", len(windows))
	}
}

func TestDeferredRemove_RunShellFails_RecordPreserved(t *testing.T) {
	baseDir := t.TempDir()

	w := model.Window{
		PaneKey:     "%5",
		Tool:        model.ToolClaude,
		SessionID:   "sess-abc",
		Tree:        "/work/myrepo",
		TmuxSession: "myproj",
		TmuxWindow:  "feat",
	}
	if err := state.SaveWindow(baseDir, w); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	opts := tmux.CleanupOpts{
		SourceWindowTarget: "=myproj:=feat",
		Tree:               "/work/myrepo",
		Branch:             "feat",
		RepoDir:            "/work/myrepo",
	}
	now := int64(1700000000)
	suffix := "abc123"
	wantScript := tmux.CleanupScript(opts, now, suffix)

	r := proc.NewFakeRunner()
	// run-shell exits 1 → RunShell treats any non-nil error as real (no exit≥1 silent-ignore), so the shadow record must be preserved.
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}, Stderr: []byte("server error")},
		"tmux", "run-shell", "-b", wantScript)

	tk := tmux.Tmux{Runner: r, Bin: "tmux"}
	err := DeferredRemove(context.Background(), tk, baseDir, opts, "%5", now, suffix)
	if err == nil {
		t.Fatal("expected error when run-shell fails")
	}
	if !strings.Contains(err.Error(), "worktree: deferred remove") {
		t.Errorf("error should be wrapped: %v", err)
	}

	// Window record must still exist (dispatch failed, nothing removed).
	windows, err2 := state.LoadWindows(baseDir)
	if err2 != nil {
		t.Fatalf("LoadWindows: %v", err2)
	}
	if len(windows) != 1 {
		t.Errorf("want 1 window record (preserved), got %d", len(windows))
	}
}
