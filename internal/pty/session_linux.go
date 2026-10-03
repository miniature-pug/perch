//go:build linux

package pty

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// killSessionPasses bounds how often killSession rescans /proc for members
// that a not-yet-killed process forked during the previous pass.
const killSessionPasses = 3

// killSession sends SIGKILL to every live process whose session id is sid,
// and to each such process's group, so that children forked into a group
// after the scan die too. It reads /proc/<pid>/stat; zombies are skipped,
// since they are already dead.
//
// The caller must guarantee that sid still names its session: the session
// leader must not have been reaped yet.
func killSession(sid int) {
	self := os.Getpid()
	for pass := 0; pass < killSessionPasses; pass++ {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return
		}
		found := false
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid == self {
				continue
			}
			data, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if err != nil {
				continue // exited meanwhile
			}
			// Field 2 (comm) is parenthesised and may contain spaces or ')',
			// so the fixed fields start after the LAST ')'. After it come
			// state, ppid, pgrp, session.
			i := bytes.LastIndexByte(data, ')')
			if i < 0 {
				continue
			}
			f := strings.Fields(string(data[i+1:]))
			if len(f) < 4 || f[0] == "Z" {
				continue
			}
			if s, _ := strconv.Atoi(f[3]); s != sid {
				continue
			}
			found = true
			if pgrp, _ := strconv.Atoi(f[2]); pgrp > 0 {
				_ = syscall.Kill(-pgrp, syscall.SIGKILL)
			}
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if !found {
			return
		}
	}
}

// waitExited blocks until the child pid has exited, WITHOUT reaping it
// (waitid with WNOWAIT): the pid stays reserved until cmd.Wait reaps it.
func waitExited(pid int) {
	const pPID = 1     // P_PID idtype for waitid(2)
	var info [128]byte // siginfo_t
	for {
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, pPID, uintptr(pid),
			uintptr(unsafe.Pointer(&info[0])), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
		if errno != syscall.EINTR {
			return
		}
	}
}

// ttyState reads, through the pty master fd, the terminal's foreground
// process group (TIOCGPGRP) and whether its line discipline is in canonical
// mode (ICANON in the termios TCGETS returns, which on Linux is the slave's).
func ttyState(fd uintptr) (pgrp int, canonical bool, err error) {
	var pg int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGPGRP,
		uintptr(unsafe.Pointer(&pg))); errno != 0 {
		return 0, false, errno
	}
	var t syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS,
		uintptr(unsafe.Pointer(&t))); errno != 0 {
		return 0, false, errno
	}
	return int(pg), t.Lflag&syscall.ICANON != 0, nil
}

// childInGroup reports whether a child of pid runs in pid's own process
// group: the shell is then waiting for a foreground command (one that job
// control did not move to a group of its own), not reading the tty itself.
// It reads /proc/<pid>/task/<pid>/children, and reports false when that is
// unavailable.
func childInGroup(pid int) bool {
	p := strconv.Itoa(pid)
	data, err := os.ReadFile("/proc/" + p + "/task/" + p + "/children")
	if err != nil {
		return false
	}
	for _, c := range strings.Fields(string(data)) {
		stat, err := os.ReadFile("/proc/" + c + "/stat")
		if err != nil {
			continue
		}
		i := bytes.LastIndexByte(stat, ')')
		if i < 0 {
			continue
		}
		// After comm: state, ppid, pgrp.
		if f := strings.Fields(string(stat[i+1:])); len(f) >= 3 && f[0] != "Z" && f[2] == p {
			return true
		}
	}
	return false
}

// awaitsRcInput reports whether the shell pid, blocked as /proc/<pid>/syscall
// shows, is waiting for input in a way its prompt never does, so it must be
// running a builtin read from an rc file:
//   - pselect6 or ppoll with a timeout: `read -t N` in bash or zsh (a
//     prompt waits without one, unless TMOUT is set);
//   - in canonical mode, a 1-byte read: dash's `read` builtin (dash, and
//     bash --noediting, read their prompt line in large blocks).
//
// It reports false when the file is unreadable or the shell is running.
func awaitsRcInput(pid int, canonical bool) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/syscall")
	if err != nil {
		return false
	}
	f := strings.Fields(string(data))
	if len(f) < 6 {
		return false // "running", or a kernel without the arguments
	}
	nr, err := strconv.Atoi(f[0])
	if err != nil {
		return false
	}
	arg := func(i int) uint64 {
		v, _ := strconv.ParseUint(strings.TrimPrefix(f[1+i], "0x"), 16, 64)
		return v
	}
	switch nr {
	case syscall.SYS_PSELECT6:
		return arg(4) != 0
	case syscall.SYS_PPOLL:
		return arg(2) != 0
	case syscall.SYS_READ:
		return canonical && arg(2) == 1
	}
	return false
}
