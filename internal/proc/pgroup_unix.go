//go:build unix

package proc

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts cmd in a new process group and makes ctx
// cancellation kill that whole group. exec.CommandContext alone kills only
// the direct child, which leaves hook processes and helpers running.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// The child leads its own group, so -pid names the group. Cancel runs
		// before Wait reaps the child, so the pid cannot have been reused.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
