package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestJoinAsUnknownRightFieldIsLoud: `join … -as RIGHT NEW` naming a right
// field the right side lacks copied nothing, exit 0, in every lane; the
// stage after it refused NEW instead, which pointed nowhere near the
// mistake. Found by the DFC169 FAQ harness: a model reversed -as's
// arguments (`-as src_name name`). resolveJoin, the one check every lane
// shares, now refuses it.
func TestJoinAsUnknownRightFieldIsLoud(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("flights.csv", "src,dst,mins\nSYD,MEL,90\n")
	write("cities.csv", "city,name\nSYD,Sydney\nMEL,Melbourne\n")
	pipe := bin + " from flights.csv | " + bin + " join cities.csv -on src city -as src_name name | " + bin + " to csv"
	for _, lane := range []struct{ name, script string }{
		{"exec", pipe},
		{"schema", "export SSQL_MODE=schema; " + pipe},
		{"record", "export SSQL_MODE=record; " + pipe + " | " + bin + " generate go"},
		{"typed", "export SSQL_MODE=typed; " + pipe + " | " + bin + " generate go"},
		{"sql", "export SSQL_MODE=record; " + pipe + " | " + bin + " generate sql"},
	} {
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+lane.script)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), `join -as: right field "src_name" not found (available: city, name)`) {
			t.Errorf("%s: want a loud refusal of the unknown -as field, got err=%v\n%s", lane.name, err, out)
		}
	}
}
