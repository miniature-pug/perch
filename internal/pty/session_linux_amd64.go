//go:build linux && amd64

package pty

import "syscall"

// legacyInputWait classifies the wait syscalls that exist only on amd64
// (arm64 has just ppoll and pselect6): glibc's poll(2) issues SYS_POLL, as
// fish's prompt does, and older glibc's select(2) issues SYS_SELECT. wait
// reports whether nr is one of them, timed whether the call has a timeout.
func legacyInputWait(nr int, a [6]uint64) (wait, timed bool) {
	switch nr {
	case syscall.SYS_POLL:
		// poll(fds, nfds, int timeout): negative waits forever.
		return true, int32(uint32(a[2])) >= 0 //nolint:gosec // truncation to the int argument is the point
	case syscall.SYS_SELECT:
		// select(nfds, r, w, e, *timeout): NULL waits forever.
		return true, a[4] != 0
	}
	return false, false
}
