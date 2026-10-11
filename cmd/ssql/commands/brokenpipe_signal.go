//go:build !windows && !js

package commands

import (
	"os/exec"
	"syscall"
)

// exitedBySIGPIPE reports whether the process was killed by SIGPIPE.
func exitedBySIGPIPE(ee *exec.ExitError) bool {
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGPIPE
}
