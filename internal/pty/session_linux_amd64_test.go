//go:build linux && amd64

package pty

import (
	"syscall"
	"testing"
)

func TestLegacyWaitSyscalls(t *testing.T) {
	t.Parallel()
	args := func(a ...uint64) (out [6]uint64) { copy(out[:], a); return }
	for _, c := range []struct {
		name        string
		nr          int
		a           [6]uint64
		wait, timed bool
	}{
		// fish at its prompt: poll(fds, 1, -1)
		{"poll forever", syscall.SYS_POLL, args(0x1000, 1, 0xffffffff), true, false},
		{"poll forever, sign-extended", syscall.SYS_POLL, args(0x1000, 1, ^uint64(0)), true, false},
		{"poll timeout", syscall.SYS_POLL, args(0x1000, 1, 2000), true, true},
		{"select forever", syscall.SYS_SELECT, args(1, 0x1000, 0, 0, 0), true, false},
		{"select timeout", syscall.SYS_SELECT, args(1, 0x1000, 0, 0, 0x2000), true, true},
		{"other", syscall.SYS_WAIT4, args(), false, false},
	} {
		wait, timed := legacyInputWait(c.nr, c.a)
		if wait != c.wait || timed != c.timed {
			t.Errorf("%s: wait=%v timed=%v, want %v %v", c.name, wait, timed, c.wait, c.timed)
		}
		if got := inputWaitSyscall(c.nr, c.a); got != c.wait {
			t.Errorf("%s: inputWait = %v, want %v", c.name, got, c.wait)
		}
		if got := rcInputSyscall(c.nr, c.a, false); got != c.timed {
			t.Errorf("%s: rcInput = %v, want %v", c.name, got, c.timed)
		}
	}
}
