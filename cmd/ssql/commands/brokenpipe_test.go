//go:build !windows && !js

package commands

import (
	"os/exec"
	"testing"
)

// TestKilledByBrokenPipe: only SIGPIPE is the "consumer finished early"
// death a pipeline excuses; SIGKILL (the OOM killer), another signal, or
// a non-zero exit is a failure. Both wait loops excused any signal until
// 2026-10-11.
func TestKilledByBrokenPipe(t *testing.T) {
	cases := map[string]bool{
		"kill -PIPE $$": true,
		"kill -KILL $$": false,
		"kill -TERM $$": false,
		"exit 3":        false,
		"exit 0":        false,
	}
	for script, want := range cases {
		err := exec.Command("sh", "-c", script).Run()
		if got := killedByBrokenPipe(err); got != want {
			t.Errorf("%s: killedByBrokenPipe = %v, want %v (err %v)", script, got, want, err)
		}
	}
}
