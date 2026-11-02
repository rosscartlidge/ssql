//go:build !unix

package commands

import "os/exec"

// inOwnProcessGroup is a no-op where there are no process groups (the
// wasm builds cannot run the pipeline at all; the source file is sampled).
func inOwnProcessGroup(cmd *exec.Cmd) {}
