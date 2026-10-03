//go:build unix

package proc_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/proc"
)

// A grandchild that inherits the output pipes must not keep Run blocked
// past the ctx deadline (GFS-25).
func TestExecRunner_CtxBoundsGrandchildHoldingPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _ = proc.ExecRunner{}.Run(ctx, "sh", "-c", "sleep 30 & sleep 30")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Run returned after %v; want it bounded by ctx (300ms) plus the pipe drain delay", d)
	}
}

// ctx cancellation kills the whole process group, not just the direct child.
func TestExecRunner_CtxKillsProcessGroup(t *testing.T) {
	marker := "perch-proc-pgkill-" + strings.ReplaceAll(t.Name(), "/", "-")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// exec -a renames the background sleep so pgrep can find it.
	_, _, _ = proc.ExecRunner{}.Run(ctx, "bash", "-c", "(exec -a "+marker+" sleep 30 >/dev/null 2>&1) & wait")
	time.Sleep(200 * time.Millisecond)
	if out, err := exec.Command("pgrep", "-f", marker).Output(); err == nil && len(out) > 0 {
		_ = exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("grandchild survived ctx cancellation: pids %s", out)
	}
}

// A command that succeeds while a detached grandchild holds its pipes still
// reports success, with its own output.
func TestExecRunner_SuccessWithLingeringGrandchild(t *testing.T) {
	start := time.Now()
	out, _, err := proc.ExecRunner{}.Run(context.Background(), "sh", "-c", "sleep 30 & echo hi")
	if err != nil {
		t.Fatalf("Run err = %v, want nil", err)
	}
	if strings.TrimSpace(string(out)) != "hi" {
		t.Errorf("stdout = %q, want hi", out)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Run took %v; want it bounded by the pipe drain delay", d)
	}
	_ = exec.Command("pkill", "-f", "^sleep 30$").Run()
}

// git runs with optional locks off, no terminal prompt, and untranslated
// messages (GFS-7, GFS-26).
func TestExecRunner_GitEnvironment(t *testing.T) {
	dir := t.TempDir()
	// A shell alias makes git print the environment it passes to children.
	out, errOut, err := proc.ExecRunner{}.Run(context.Background(), "git", "-C", dir,
		"-c", "alias.envdump=!env", "envdump")
	if err != nil {
		t.Fatalf("git envdump: %v: %s", err, errOut)
	}
	for _, want := range []string{"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LANGUAGE=C", "LC_MESSAGES=C"} {
		if !strings.Contains(string(out), "\n"+want+"\n") && !strings.HasPrefix(string(out), want+"\n") {
			t.Errorf("git environment lacks %s", want)
		}
	}
}
