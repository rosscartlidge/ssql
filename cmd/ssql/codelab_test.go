package main

// TestCodelabRuns is the L2 gate for doc/cli-codelab.md (DFC125): every
// bash block executes in doc/codelab-data against a freshly built
// binary; failures name the block and its line. Blocks that cannot run
// carry an explicit `# codelab: skip — reason` first line.

import (
	"os/exec"
	"testing"
)

func TestCodelabRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("codelab runner skipped in -short")
	}
	out, err := exec.Command("bash", "../../scripts/codelab-run.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("codelab examples failed:\n%s", out)
	}
	t.Logf("%s", out)
}
