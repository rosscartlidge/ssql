package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSinkTimeFormatLanes (DFC147 §7.3): `-time-format` on to csv renders
// the same text from exec, a record program and a typed program — on the
// pipeline zone's wall clock (the source says Sydney, the test runs under
// UTC) — and the other sinks honour it in exec. The sinks have no SQL
// lane, so this stands in for an equivalence case.
func TestSinkTimeFormatLanes(t *testing.T) {
	bin := corpusBin(t)
	data := corpusData(t)
	root := mustRepoRoot(t)
	src := bin + " from csv " + filepath.Join(data, "times.csv") + " -type ts time -tz Australia/Sydney | " + bin + " sort id | " + bin + " include id ts"
	run := func(t *testing.T, mode, pipeline string) string {
		t.Helper()
		script := pipeline
		if mode != "exec" {
			script = "(export SSQL_MODE=" + mode + "; " + pipeline + ") | " + bin + " generate go -run"
		}
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "TZ=UTC", "SSQL_MODULE_DIR="+root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		return string(out)
	}
	want := "id,ts\n1,2026-01-05 10:30\n2,2026-01-05 10:30\n3,2026-01-31 23:00\n4,2026-04-05 02:30\n5,2024-03-01 12:15\n6,2026-01-12 10:59\n"
	for _, mode := range []string{"exec", "record", "typed"} {
		t.Run("csv_"+mode, func(t *testing.T) {
			got := run(t, mode, src+" | "+bin+" to csv -time-format '%Y-%m-%d %H:%M'")
			if got != want {
				t.Errorf("mode %s:\n got %q\nwant %q", mode, got, want)
			}
		})
	}
	t.Run("csv_go_layout", func(t *testing.T) {
		got := run(t, "exec", src+" | "+bin+" limit 1 | "+bin+" to csv -time-format 2006-01-02")
		if !strings.Contains(got, "1,2026-01-05\n") {
			t.Errorf("go layout: %q", got)
		}
	})
	t.Run("tsv_table_markdown", func(t *testing.T) {
		if got := run(t, "exec", src+" | "+bin+" limit 1 | "+bin+" to tsv -time-format '%d/%m/%Y'"); !strings.Contains(got, "1\t05/01/2026") {
			t.Errorf("tsv: %q", got)
		}
		if got := run(t, "exec", src+" | "+bin+" limit 1 | "+bin+" to table -time-format '%b %-d %H:%M'"); !strings.Contains(got, "Jan 5 10:30") {
			t.Errorf("table: %q", got)
		}
		if got := run(t, "typed", src+" | "+bin+" limit 1 | "+bin+" to table -time-format '%b %-d %H:%M'"); !strings.Contains(got, "Jan 5 10:30") {
			t.Errorf("typed table: %q", got)
		}
		if got := run(t, "exec", src+" | "+bin+" limit 1 | "+bin+" to markdown -time-format '%Y-%m-%d'"); !strings.Contains(got, "| 2026-01-05 |") {
			t.Errorf("markdown: %q", got)
		}
		if got := run(t, "record", src+" | "+bin+" limit 1 | "+bin+" to markdown -time-format '%Y-%m-%d'"); !strings.Contains(got, "| 2026-01-05 |") {
			t.Errorf("record markdown: %q", got)
		}
	})
	t.Run("bad_pattern_refused", func(t *testing.T) {
		cmd := exec.Command("bash", "-c", src+" | "+bin+" to csv -time-format '%Q'")
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "%Q") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})
}
