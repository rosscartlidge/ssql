package main

// TestCodelabLoopHasOneSchema pins the shape the signal-processing
// tutorial uses to compare several one-shot pipelines in a shell loop
// (DFC140 §2.1). Every ssql pipeline's raw output starts with a
// `_schema` header line, so `for …; do ssql … ; done | ssql to table`
// hands the consumer one header per iteration after the first — each
// read as a phantom record. codelab-run.sh cannot see that (it asserts
// exit 0 and non-empty output), which is how the bug shipped; this test
// asserts the row count, and also asserts the tutorial still carries the
// fixed idiom so doc and gate cannot drift apart.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodelabLoopHasOneSchema(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	src, err := os.ReadFile("../../doc/codelab-data/signal.csv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "signal.csv"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	loop := func(tail string) string {
		return `for window in hann hamming blackman none; do
  ` + bin + ` from signal.csv | ` + bin + ` spectrogram -field amplitude -window-size 32 -window-type $window -rate 100 |
    ` + bin + ` where -if time_index eq 2 | ` + bin + ` sort -desc magnitude | ` + bin + ` limit 3 |
    ` + bin + ` update -set window $window` + tail + `
done`
	}
	count := func(script string) string {
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+script+" | "+bin+" count")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// The tutorial's shape: to jsonl per iteration, one from jsonl after.
	if got := count(loop(" | "+bin+" to jsonl") + " | " + bin + " from jsonl"); got != "12" {
		t.Fatalf("fixed loop: want 12 rows (4 windows x 3), got %s", got)
	}
	// The shape the tutorial used to have: the gate must be able to fail.
	if got := count(loop("")); got == "12" {
		t.Fatalf("bare loop: expected phantom rows from the extra _schema lines, got exactly 12")
	}
	doc, err := os.ReadFile("../../doc/cli-signal-processing.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "ssql to jsonl\ndone | ssql from jsonl | ssql to table") {
		t.Fatalf("doc/cli-signal-processing.md: the window-comparison loop no longer ends `… | ssql to jsonl; done | ssql from jsonl` (DFC140 §2.1)")
	}
}
