package main

// TestCodelabRuns is the L2 gate for the codelabs on the learning path
// (DFC125): every code block of every codelab executes against the
// current checkout, and a failure names the doc, the block, and its
// line. Blocks that cannot run carry an explicit
// `# codelab: skip — reason` first line.
//
//   - CLI codelabs (```bash blocks) run through scripts/codelab-run.sh in
//     a throwaway copy of doc/codelab-data against a freshly built binary.
//   - Go codelabs (```go programs, fragments, and ```bash blocks) run
//     through scripts/codelab-go-run.sh in a throwaway module that
//     `replace`s ssql/v4 with this checkout.
//
// Add a new codelab here the day it is written — a codelab nobody runs
// rots (74 of the old CLI codelab's 106 blocks failed when first run).

import (
	"os/exec"
	"testing"
)

func TestCodelabRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("codelab runners skipped in -short")
	}
	cases := []struct {
		doc    string
		runner string
	}{
		{"doc/cli-codelab.md", "codelab-run.sh"},
		{"doc/cli-signal-processing.md", "codelab-run.sh"},
		{"doc/codelab-intro.md", "codelab-go-run.sh"},
		{"doc/typed-codelab.md", "codelab-go-run.sh"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.doc, func(t *testing.T) {
			t.Parallel()
			out, err := exec.Command("bash", "../../scripts/"+c.runner, "../../"+c.doc).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: codelab blocks failed:\n%s", c.doc, out)
			}
			t.Logf("%s", out)
		})
	}
}
