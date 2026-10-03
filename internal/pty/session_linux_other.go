//go:build linux && !amd64

package pty

// legacyInputWait: poll(2) and select(2) are not separate system calls on
// this architecture (they are ppoll and pselect6).
func legacyInputWait(int, [6]uint64) (wait, timed bool) { return false, false }
