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
