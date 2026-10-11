//go:build windows || js

package commands

import "os/exec"

// exitedBySIGPIPE: Windows and js/wasm have no SIGPIPE (a write to a
// closed pipe is an error the writer reports), so no stage is excused.
func exitedBySIGPIPE(*exec.ExitError) bool { return false }
