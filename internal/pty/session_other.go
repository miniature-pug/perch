//go:build !linux

package pty

// killSession is a no-op where /proc is unavailable: Close then kills only
// the shell's own process group.
func killSession(int) {}

// waitExited returns at once where waitid(WNOWAIT) is unavailable. The
// reaper then marks the shell reaped just before cmd.Wait reaps it.
func waitExited(int) {}
