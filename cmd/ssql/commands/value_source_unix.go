//go:build unix

package commands

import (
	"os/exec"
	"syscall"
)

// inOwnProcessGroup puts the sampled pipeline in its own process group
// and, on timeout, kills the group: killing bash alone would leave its
// ssql stages reading a large file after Ctrl-O has given up.
func inOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
