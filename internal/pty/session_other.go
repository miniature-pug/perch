//go:build !linux

package pty

// killSession is a no-op where /proc is unavailable: Close then kills only
// the shell's own process group.
func killSession(int) {}

// waitExited returns at once where waitid(WNOWAIT) is unavailable. The
// reaper then marks the shell reaped just before cmd.Wait reaps it.
func waitExited(int) {}

// ttyState is unsupported here: WaitShellReady then relies on the
// bracketed-paste marker alone.
func ttyState(uintptr) (int, bool, error) { return 0, false, errTTYUnsupported }

// childInGroup cannot tell here; it reports false.
func childInGroup(int) bool { return false }

// awaitsRcInput cannot tell here; it reports false.
func awaitsRcInput(int, bool) bool { return false }

// shellInInputWait cannot tell here; it reports false.
func shellInInputWait(int) bool { return false }

// awaitsPromptRead cannot tell here; it reports false.
func awaitsPromptRead(int) bool { return false }
