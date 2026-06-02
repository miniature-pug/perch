//go:build integration

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// newTestServer returns a Tmux struct targeting a PRIVATE tmux socket named
// perch-app-test-<pid>. A cleanup is registered to kill the server after the
// test completes so no socket leaks.
func newTestServer(t *testing.T) tmux.Tmux {
	t.Helper()
	socket := fmt.Sprintf("perch-app-test-%d", os.Getpid())
	tmx := tmux.Tmux{Runner: proc.ExecRunner{}, Bin: "tmux", Socket: socket}
	t.Cleanup(func() { _ = tmx.KillServer(context.Background()) })
	return tmx
}

// TestIntegration_App_CreateAgent exercises the full CreateAgent path using a
// real git repo, a real (private) tmux server, and no real claude/opencode
// binary — the binary is absent so the shell reports command-not-found, but
// the tmux window/pane and worktree are still created (which is what is
// asserted).
func TestIntegration_App_CreateAgent(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	// Isolate from real user config/state.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	run := proc.ExecRunner{}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-qm", "init"},
	} {
		if _, se, err := run.RunInDir(ctx, repo, "git", args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, se)
		}
	}

	a := &App{
		tmux:    tmx,
		run:     run,
		roots:   []string{root},
		emit:    func(string, ...any) {},
		bridges: map[string]*ptyEntry{},
	}

	sid, err := a.CreateAgent("claude", repo, "feat/x")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if sid == "" {
		t.Fatal("expected a non-empty claude session id")
	}

	// The default worktreeDir="" produces a sibling:
	//   <root>/proj__worktrees/feat-x
	// (git.WorktreePath: filepath.Join(filepath.Dir(repo), "proj__worktrees", "feat-x"))
	expectedWorktree := filepath.Join(root, "proj__worktrees", "feat-x")
	if _, err := os.Stat(expectedWorktree); err != nil {
		t.Errorf("expected worktree %q to exist after CreateAgent: %v", expectedWorktree, err)
	}

	// Confirm the tmux pane was created.
	panes, err := tmx.ListPanesAll(ctx)
	if err != nil {
		t.Fatalf("ListPanesAll: %v", err)
	}
	if len(panes) == 0 {
		t.Fatal("expected at least one tmux pane after CreateAgent")
	}
}
