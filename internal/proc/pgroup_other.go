//go:build !unix

package proc

import "os/exec"

// setProcessGroup is a no-op where process groups are unavailable. There,
// ctx cancellation kills only the direct child.
func setProcessGroup(*exec.Cmd) {}
