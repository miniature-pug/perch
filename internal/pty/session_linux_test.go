//go:build linux

package pty

import (
	"syscall"
	"testing"
)

func TestSyscallClassification(t *testing.T) {
	t.Parallel()
	args := func(a ...uint64) (out [6]uint64) { copy(out[:], a); return }
	cases := []struct {
		name                          string
		nr                            int
		a                             [6]uint64
		canonical                     bool
		rcInput, rcInputCanon, inWait bool
		promptRead                    bool
	}{
		// readline's prompt: pselect6(n, &fds, NULL, NULL, NULL, &sigmask)
		{name: "pselect6 no timeout", nr: syscall.SYS_PSELECT6, a: args(1, 0x1000, 0, 0, 0, 0x2000),
			inWait: true, promptRead: true},
		// rc `read -t N`
		{name: "pselect6 timeout", nr: syscall.SYS_PSELECT6, a: args(1, 0x1000, 0, 0, 0x3000, 0x2000),
			rcInput: true, rcInputCanon: true, inWait: true},
		{name: "ppoll timeout", nr: syscall.SYS_PPOLL, a: args(0x1000, 1, 0x3000, 0, 8),
			rcInput: true, rcInputCanon: true, inWait: true},
		{name: "ppoll no timeout", nr: syscall.SYS_PPOLL, a: args(0x1000, 1, 0, 0, 8), inWait: true},
		// rc `read -n 1`, dash `read`
		{name: "read 1 byte", nr: syscall.SYS_READ, a: args(0, 0x1000, 1), rcInputCanon: true, inWait: true},
		{name: "read block", nr: syscall.SYS_READ, a: args(0, 0x1000, 4096), inWait: true},
		// waiting for a foreground child
		{name: "wait4", nr: syscall.SYS_WAIT4, a: args(0xffffffff, 0x1000, 0, 0)},
	}
	for _, c := range cases {
		if got := rcInputSyscall(c.nr, c.a, false); got != c.rcInput {
			t.Errorf("%s: rcInput(raw) = %v, want %v", c.name, got, c.rcInput)
		}
		if got := rcInputSyscall(c.nr, c.a, true); got != c.rcInputCanon {
			t.Errorf("%s: rcInput(canonical) = %v, want %v", c.name, got, c.rcInputCanon)
		}
		if got := inputWaitSyscall(c.nr, c.a); got != c.inWait {
			t.Errorf("%s: inputWait = %v, want %v", c.name, got, c.inWait)
		}
		if got := promptReadSyscall(c.nr, c.a); got != c.promptRead {
			t.Errorf("%s: promptRead = %v, want %v", c.name, got, c.promptRead)
		}
	}
}

func TestProcSyscallOfRunningProcess(t *testing.T) {
	t.Parallel()
	// This test process is running, not blocked in a syscall of its own
	// choosing: the parser must neither crash nor claim a wait it cannot see.
	if _, _, ok := procSyscall(-7); ok {
		t.Error("procSyscall of a missing process reported ok")
	}
	if shellInInputWait(-7) || awaitsPromptRead(-7) || awaitsRcInput(-7, true) {
		t.Error("a missing process was reported as waiting for input")
	}
}
