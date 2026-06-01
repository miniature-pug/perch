//go:build integration

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegration_Fork_ClaudeForkedSessionLands is a headless demo that:
//  1. Seeds a real claude session with a known UUID.
//  2. Forks it into a worktree dir via tmux, pinning a second UUID.
//  3. Verifies the fork landed (capture-pane non-empty, session file exists).
//
// Skips cleanly when claude, tmux, or git are absent. Gated behind
// //go:build integration — never run by `make test`.
func TestIntegration_Fork_ClaudeForkedSessionLands(t *testing.T) {
	// Skip if any required binary is absent.
	for _, bin := range []string{"claude", "tmux", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("skipping: %q not on PATH: %v", bin, err)
		}
	}

	// ── Setup: temp git repo ─────────────────────────────────────────────────────
	repoDir, err := os.MkdirTemp("", "perch-fork-it-repo-")
	if err != nil {
		t.Fatalf("MkdirTemp repo: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(repoDir) })

	for _, args := range [][]string{
		{"git", "-C", repoDir, "init"},
		{"git", "-C", repoDir, "commit", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// ── Setup: worktree dir (sibling temp dir) ───────────────────────────────────
	worktreeDir, err := os.MkdirTemp("", "perch-fork-it-wt-")
	if err != nil {
		t.Fatalf("MkdirTemp worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(worktreeDir) })

	// ── Seed session ─────────────────────────────────────────────────────────────
	uuidSeed, err := newSessionID()
	if err != nil {
		t.Fatalf("newSessionID seed: %v", err)
	}

	seedCtx, seedCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer seedCancel()
	seedCmd := exec.CommandContext(seedCtx, "claude", "--session-id", uuidSeed, "-p", "reply with the single word ok")
	seedCmd.Dir = repoDir
	seedOut, seedErr := seedCmd.Output()
	if seedErr != nil {
		// If claude is present but the session seed fails (no auth, rate-limit, etc.),
		// skip rather than fail so CI stays green without credentials.
		t.Skipf("claude seed session failed (no auth?): %v\noutput: %s", seedErr, seedOut)
	}

	// ── Mint fork UUID and launch fork in detached tmux ─────────────────────────
	uuidFork, err := newSessionID()
	if err != nil {
		t.Fatalf("newSessionID fork: %v", err)
	}

	tmuxSession := "perchm6fork"
	forkCmd := fmt.Sprintf("claude --resume %s --fork-session --session-id %s", uuidSeed, uuidFork)
	launchArgs := []string{
		"tmux", "new-session", "-d",
		"-s", tmuxSession,
		"-c", worktreeDir,
		forkCmd,
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", tmuxSession).Run()
	})

	if out, err := exec.Command(launchArgs[0], launchArgs[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session fork: %v\n%s", err, out)
	}

	// Give the fork a few seconds to start.
	time.Sleep(10 * time.Second)

	// ── Assert: capture-pane is non-empty ────────────────────────────────────────
	captureOut, err := exec.Command("tmux", "capture-pane", "-t", tmuxSession, "-p").Output()
	if err != nil {
		t.Fatalf("tmux capture-pane: %v", err)
	}
	if strings.TrimSpace(string(captureOut)) == "" {
		t.Error("tmux capture-pane returned empty output; fork pane appears dead")
	}

	// ── Assert: session file for uuidFork exists under ~/.claude/projects/ ───────
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		claudeHome = filepath.Join(home, ".claude")
	}

	// slug: abs path with '/' replaced by '-' (leading '/' becomes leading '-').
	slug := strings.ReplaceAll(worktreeDir, "/", "-")
	slugDir := filepath.Join(claudeHome, "projects", slug)

	// Guard: only touch paths that embed the temp prefix.
	if !strings.HasPrefix(worktreeDir, os.TempDir()) {
		t.Fatalf("worktree dir %q is not under temp prefix %q — refusing to inspect slug dir", worktreeDir, os.TempDir())
	}

	sessionFile := filepath.Join(slugDir, uuidFork+".jsonl")
	if _, err := os.Stat(sessionFile); err != nil {
		// The file may not exist yet if the fork is still initialising; wait a
		// bit more and retry once rather than failing immediately.
		time.Sleep(5 * time.Second)
		if _, err := os.Stat(sessionFile); err != nil {
			t.Logf("capture-pane output:\n%s", captureOut)
			t.Errorf("session file %q not found after fork: %v", sessionFile, err)
		}
	}

	// ── Cleanup: remove only the temp slug dir ───────────────────────────────────
	t.Cleanup(func() {
		if !strings.HasPrefix(worktreeDir, os.TempDir()) {
			return // safety guard: never touch real project dirs
		}
		_ = os.RemoveAll(slugDir)
	})
}
