package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestToParquetTypesAllLanes: `to parquet` writes a time column as a
// timestamp in every lane (exec, record, typed, parallel) and the lanes
// read back identically; a column null in row 1 is typed from all of
// its values; an empty result writes a valid zero-row file; no input at
// all is an error that leaves no file behind. Before the fix the record
// lanes wrote times as VARCHAR holding Go's Time.String() and typed a
// column from the first record, the generated record and typed sinks did
// not compile (`ctx` undefined; the Stream form after a serial source),
// and an empty input left a 0-byte file.
func TestToParquetTypesAllLanes(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	csv := "id,ts,name\n" +
		"3,2023-05-17T08:15:00Z,c\n" +
		"1,2023-05-15T00:00:00Z,a\n" +
		"2,2023-05-16T10:30:00+11:00,b\n"
	if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	// late is null in row 1: only the header says it is a time.
	jsonl := `{"_schema":{"fields":["id","late"],"types":{"id":"int","late":"time"}}}
{"id":1,"late":null}
{"id":2,"late":"2023-05-16T11:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(dir, "n.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	shell := func(script string) (string, error) {
		c := exec.Command("bash", "-c", script)
		c.Dir = dir
		out, err := c.CombinedOutput()
		return string(out), err
	}
	source := bin + " from csv t.csv -type ts time"

	lanes := []string{"exec", "record", "typed", "parallel"}
	var first string
	for _, lane := range lanes {
		out := "out_" + lane + ".parquet"
		pipeline := source + " | " + bin + " to parquet " + out
		if lane == "exec" {
			if o, err := shell(pipeline); err != nil {
				t.Fatalf("exec: %v\n%s", err, o)
			}
		} else if o, err := runGeneratedPipeline(t, bin, dir, lane, pipeline); err != nil {
			t.Fatalf("%s: %v\n%s", lane, err, o)
		}

		schema, err := shell("SSQL_MODE=schema " + bin + " from " + out)
		if err != nil {
			t.Fatalf("%s schema: %v\n%s", lane, err, schema)
		}
		for _, want := range []string{`"id":"int"`, `"ts":"time"`, `"name":"string"`} {
			if !strings.Contains(schema, want) {
				t.Errorf("%s: schema lacks %s: %s", lane, want, schema)
			}
		}

		rows, err := shell(bin + " from parquet " + out + " | " + bin + " sort id | " + bin + " to csv")
		if err != nil {
			t.Fatalf("%s read back: %v\n%s", lane, err, rows)
		}
		if !strings.Contains(rows, "2,2023-05-15T23:30:00Z,b") {
			t.Errorf("%s: times did not round-trip as instants:\n%s", lane, rows)
		}
		if first == "" {
			first = rows
		} else if rows != first {
			t.Errorf("%s read back differs from exec:\n%s\nexec:\n%s", lane, rows, first)
		}

		if duck, err := exec.LookPath("duckdb"); err == nil {
			d, err := shell(duck + ` -csv -noheader -c "SELECT column_type FROM (DESCRIBE SELECT ts FROM '` + out + `')"`)
			if err != nil {
				t.Fatalf("%s duckdb: %v\n%s", lane, err, d)
			}
			if strings.TrimSpace(d) != "TIMESTAMP WITH TIME ZONE" {
				t.Errorf("%s: duckdb sees %q, want TIMESTAMP WITH TIME ZONE", lane, d)
			}
		}
	}

	// to arrow shares the writer, and its generated record sink had the
	// same undefined ctx.
	for _, lane := range []string{"exec", "record"} {
		out := "out_" + lane + ".arrow"
		pipeline := source + " | " + bin + " to arrow " + out
		if lane == "exec" {
			if o, err := shell(pipeline); err != nil {
				t.Fatalf("arrow exec: %v\n%s", err, o)
			}
		} else if o, err := runGeneratedPipeline(t, bin, dir, lane, pipeline); err != nil {
			t.Fatalf("arrow %s: %v\n%s", lane, err, o)
		}
		rows, err := shell(bin + " from arrow " + out + " | " + bin + " sort id | " + bin + " to csv")
		if err != nil || rows != first {
			t.Errorf("arrow %s read back %v:\n%s\nwant:\n%s", lane, err, rows, first)
		}
	}

	// A column null in row 1 is typed from the header and the later
	// rows, not as a string from the first record. The record lane's
	// JSONL reader ignored the header's time type until 2026-10-12 and
	// wrote VARCHAR; the typed lane read the null as year 1 and now
	// refuses it (a time.Time field cannot hold absence; DFC124).
	pipeline := bin + " from jsonl n.jsonl | " + bin + " to parquet null1_LANE.parquet"
	for _, lane := range []string{"exec", "record"} {
		out := "null1_" + lane + ".parquet"
		p := strings.ReplaceAll(pipeline, "LANE", lane)
		if lane == "exec" {
			if o, err := shell(p); err != nil {
				t.Fatalf("null in row 1 (exec): %v\n%s", err, o)
			}
		} else if o, err := runGeneratedPipeline(t, bin, dir, lane, p); err != nil {
			t.Fatalf("null in row 1 (%s): %v\n%s", lane, err, o)
		}
		if schema, err := shell("SSQL_MODE=schema " + bin + " from " + out); err != nil ||
			!strings.Contains(schema, `"late":"time"`) {
			t.Errorf("null in row 1 (%s): want late:time, got %v %s", lane, err, schema)
		}
		if rows, err := shell(bin + " from parquet " + out + " | " + bin + " to csv"); err != nil ||
			rows != "id,late\n1,\n2,2023-05-16T11:00:00Z\n" {
			t.Errorf("null in row 1 (%s): read back %v %q", lane, err, rows)
		}
	}
	o, err := runGeneratedPipeline(t, bin, dir, "typed", strings.ReplaceAll(pipeline, "LANE", "typed"))
	assertLoudFailure(t, "null in row 1 (typed)", o, err, `field "late": null is not time.Time`)

	// An empty result keeps its header's columns: a zero-row file.
	if o, err := shell(source + " | " + bin + " where -if id eq 99 | " + bin + " to parquet empty.parquet"); err != nil {
		t.Fatalf("empty result: %v\n%s", err, o)
	}
	if schema, err := shell("SSQL_MODE=schema " + bin + " from empty.parquet"); err != nil ||
		!strings.Contains(schema, `"ts":"time"`) {
		t.Errorf("empty result: want a zero-row file with ts:time, got %v %s", err, schema)
	}

	// No input at all: an error, and no file a later reader trips on.
	o, err = shell(": | " + bin + " to parquet none.parquet")
	if err == nil || !strings.Contains(o, "nothing to write") {
		t.Errorf("no input: want loud error, got %v %q", err, o)
	}
	if _, err := os.Stat(filepath.Join(dir, "none.parquet")); err == nil {
		t.Error("no input: left a file behind")
	}
}
