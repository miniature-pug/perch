package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	"github.com/miniature-pug/perch/internal/pty"
)

// TestLaunchLine_NonPOSIXShellWrapsInSh is the AGT-17 regression. Under a
// fish/nushell login shell the POSIX launch line is a parse error, so it is
// typed as `sh -c '…'`. The wrapped line must contain no inner single
// quote or backslash (fish and nushell would mangle them), and it must
// still run: executed by a real shell with a fake claude that exits 42,
// the exit sentinel must report code 42.
func TestLaunchLine_NonPOSIXShellWrapsInSh(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not on PATH; the exit sentinel needs it")
	}
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", "/usr/bin/fish")

	cwd := t.TempDir()
	line, err := m.Prepare(context.Background(), "ws", cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "sh -c '") || !strings.HasSuffix(line, "'\n") {
		t.Fatalf("fish login shell: want an sh -c '…' line, got %q", line)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "sh -c '"), "'\n")
	if strings.ContainsAny(inner, `'\`) {
		t.Fatalf("wrapped line contains a quote or backslash fish/nushell would mangle: %q", inner)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, append(os.Environ(), m.PaneEnv()...), "data", "exit", func(string, ...any) {}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	if _, err := br.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-m.Events():
		if ev.State != agent.StateExited || ev.Err != "exited (code 42)" {
			t.Fatalf("want exited (code 42), got %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wrapped launch line never ran its exit sentinel")
	}
}

// TestLaunchLine_POSIXShellUnwrapped checks bash/zsh users keep the direct
// line (so their aliases and functions for the agent still apply).
func TestLaunchLine_POSIXShellUnwrapped(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	t.Setenv("SHELL", "/bin/zsh")
	line, err := m.Prepare(context.Background(), "ws", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "claude --settings ") {
		t.Errorf("zsh: want the direct claude line, got %q", line)
	}
}

// TestLaunchLine_HonoursAdapterBin is the AGT-19 regression: a custom Bin
// that passes Detect must also be the binary the launch line runs.
func TestLaunchLine_HonoursAdapterBin(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("TMPDIR", t.TempDir())
	cm, err := agent.NewMonitor("claude", agent.Claude{Bin: "/opt/claude/bin/claude-dev"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cm.Teardown() }()
	line, err := cm.Prepare(context.Background(), "ws", t.TempDir(), "ses_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "/opt/claude/bin/claude-dev --settings ") || !strings.Contains(line, "--resume ses_1") {
		t.Errorf("claude launch ignores Bin or resume: %q", line)
	}

	om, err := agent.NewMonitor("opencode", agent.Opencode{Bin: "oc-dev"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = om.Teardown() }()
	line, err = om.Prepare(context.Background(), "ws", t.TempDir(), "ses_2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "oc-dev serve ") || !strings.Contains(line, "oc-dev attach ") || !strings.Contains(line, "--session ses_2") {
		t.Errorf("opencode launch ignores Bin or resume: %q", line)
	}

	// An unsafe Bin falls back to the canonical name rather than inject.
	bad, _ := agent.NewMonitor("claude", agent.Claude{Bin: "claude; rm -rf ~"})
	defer func() { _ = bad.Teardown() }()
	line, _ = bad.Prepare(context.Background(), "ws", t.TempDir(), "")
	if !strings.HasPrefix(line, "claude --settings ") {
		t.Errorf("unsafe Bin was not rejected: %q", line)
	}
}
