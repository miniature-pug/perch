//go:build integration

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// newTestServer returns a Tmux struct targeting a PRIVATE tmux socket named
// perch-app-test-<pid>. A cleanup is registered to kill the server after the
// test completes so no socket leaks.
func newTestServer(tb testing.TB) tmux.Tmux {
	tb.Helper()
	socket := fmt.Sprintf("perch-app-test-%d", os.Getpid())
	tmx := tmux.Tmux{Runner: proc.ExecRunner{}, Bin: "tmux", Socket: socket}
	tb.Cleanup(func() {
		_ = tmx.KillServer(context.Background())
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
		}
		_ = os.Remove(filepath.Join(dir, socket))
	})
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

// fakeAgentCmd stamps a sentinel then idles. A FAKE agent — no real
// claude/opencode binary or $HOME/auth is touched.
const fakeAgentCmd = "printf 'PERCH_FAKE_READY\\n'; while :; do sleep 1; done"

func newHeadlessApp(tb testing.TB, tmx tmux.Tmux, emit internalpty.EmitFunc) *App {
	return &App{
		tmux:    tmx,
		run:     proc.ExecRunner{},
		roots:   []string{tb.TempDir()},
		emit:    emit,
		bridges: map[string]*ptyEntry{},
	}
}

func TestIntegration_App_ListThenOpenTerminal(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	pane, err := tmx.Launch(ctx, "perch", "feat-x", dir, []string{"sh", "-c", fakeAgentCmd})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := tmx.SetPaneOption(ctx, pane, tmux.OptionPerchSession, "ses_fake01"); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	var mu sync.Mutex
	var sb strings.Builder
	emit := func(_ string, data ...any) {
		if len(data) == 1 {
			if b, ok := data[0].([]int); ok {
				mu.Lock()
				for _, v := range b {
					sb.WriteByte(byte(v))
				}
				mu.Unlock()
			}
		}
	}
	a := newHeadlessApp(t, tmx, emit)

	sessions, err := a.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.ID == "ses_fake01" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListSessions missing ses_fake01: %+v", sessions)
	}

	if err := a.OpenTerminal("tab1", "ses_fake01"); err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	defer a.CloseTerminal("tab1")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := strings.Contains(sb.String(), "PERCH_FAKE_READY")
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(sb.String(), "PERCH_FAKE_READY") {
		t.Fatalf("no pty bytes emitted via OpenTerminal; got %q", sb.String())
	}
}

// TestIntegration_OpenTerminal_ReopenSameTab_NoLeak verifies the
// close-and-replace invariant: reopening a tab with the same tabID closes the
// prior bridge and registers exactly one new entry. After CloseTerminal the
// registry is empty and no tmux attach client lingers.
func TestIntegration_OpenTerminal_ReopenSameTab_NoLeak(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	pane, err := tmx.Launch(ctx, "perch", "feat-reopen", dir, []string{"sh", "-c", fakeAgentCmd})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := tmx.SetPaneOption(ctx, pane, tmux.OptionPerchSession, "ses_reopen01"); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	a := newHeadlessApp(t, tmx, func(_ string, _ ...any) {})

	// First open.
	if err := a.OpenTerminal("tab1", "ses_reopen01"); err != nil {
		t.Fatalf("first OpenTerminal: %v", err)
	}
	// Second open on the SAME tab: must close-and-replace without error.
	if err := a.OpenTerminal("tab1", "ses_reopen01"); err != nil {
		t.Fatalf("second OpenTerminal (reopen): %v", err)
	}

	// Only one entry must exist in the registry after the reopen.
	a.mu.Lock()
	n := len(a.bridges)
	a.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 bridge after reopen, got %d", n)
	}

	// Close and verify the registry is empty.
	if err := a.CloseTerminal("tab1"); err != nil {
		t.Fatalf("CloseTerminal: %v", err)
	}
	a.mu.Lock()
	n = len(a.bridges)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 bridges after CloseTerminal, got %d", n)
	}
}

// BenchmarkPtyThroughput floods the pty with output and asserts the emit path
// stays bounded: each emitted chunk never exceeds maxChunk (16 KiB), so memory
// per IPC crossing is capped regardless of flood volume.
func BenchmarkPtyThroughput(b *testing.B) {
	tmx := newTestServer(b)
	ctx := context.Background()
	dir := b.TempDir()

	pane, err := tmx.Launch(ctx, "flood", "win", dir, []string{"sh", "-c", "yes PERCH_FLOOD"})
	if err != nil {
		b.Fatalf("Launch: %v", err)
	}
	_ = pane

	var maxSeen int
	var mu sync.Mutex
	emit := func(_ string, data ...any) {
		if len(data) == 1 {
			if by, ok := data[0].([]int); ok {
				mu.Lock()
				if len(by) > maxSeen {
					maxSeen = len(by)
				}
				mu.Unlock()
			}
		}
	}
	a := newHeadlessApp(b, tmx, emit)
	if err := tmx.SetPaneOption(ctx, pane, tmux.OptionPerchSession, "ses_flood1"); err != nil {
		b.Fatalf("SetPaneOption: %v", err)
	}

	b.ResetTimer()
	if err := a.OpenTerminal("flood", "ses_flood1"); err != nil {
		b.Fatalf("OpenTerminal: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	a.CloseTerminal("flood")
	b.StopTimer()

	mu.Lock()
	defer mu.Unlock()
	if maxSeen > 16*1024 {
		b.Fatalf("emitted chunk exceeded 16 KiB bound under flood: %d", maxSeen)
	}
}
