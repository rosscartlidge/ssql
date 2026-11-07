package main

import (
	"os/exec"
	"strings"
	"testing"
)

// A time's text is the pipeline zone's where a stage converts it, and a
// writer's cell keeps its own offset (Ross, DFC164 §7): the harness reads a
// bare time back as an instant, so only raw output can tell them apart.
func TestTimeTextConvertsWritersKeep(t *testing.T) {
	bin, data := corpusBin(t), corpusData(t)
	from := bin + " from csv -type t time -tz America/New_York " + data + "/time_text.csv"
	for _, c := range []struct{ name, stages, want string }{
		{"to csv keeps the cell", "| " + bin + " to csv", "2026-01-01T09:00:00+11:00"},
		{"to jsonl keeps the cell", "| " + bin + " to jsonl", `"2026-01-01T09:00:00+11:00"`},
		{"cast converts", "| " + bin + " cast -type t string | " + bin + " to csv", "2025-12-31T17:00:00-05:00"},
		{"collect converts", "| " + bin + " group-by g -collect t c | " + bin + " to jsonl", `"2025-12-31T17:00:00-05:00"`},
		{"-set-field keeps the time", "| " + bin + " update -set-field x t | " + bin + " include x | " + bin + " to csv", "2026-01-01T09:00:00+11:00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := exec.Command("bash", "-c", from+" "+c.stages).CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Errorf("want %s in:\n%s", c.want, out)
			}
		})
	}
}
