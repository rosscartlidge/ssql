package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// orderLargeRows is past the size where the engines read a CSV in parallel. The
// 50,000-row fixture hid this bug on DuckDB for as long as the lane existed.
const orderLargeRows = 3_000_000

func orderLargeFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "big.csv")
	var b bytes.Buffer
	b.WriteString("n,g,s\n")
	for i := 0; i < orderLargeRows; i++ {
		fmt.Fprintf(&b, "%d,%d,s%d\n", i, i%5, i)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func orderGenerate(t *testing.T, dialect, stages string) (string, error) {
	t.Helper()
	bin := corpusBin(t)
	script := "PATH=" + filepath.Dir(bin) + ":$PATH " + bin + " generate sql -dialect " + dialect + " -pipeline '" + stages + " | ssql to jsonl'"
	cmd := exec.Command("bash", "-c", script)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		return errb.String(), err
	}
	return out.String(), nil
}

func orderCanon(t *testing.T, raw string) string {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("%v in %q", err, raw)
	}
	for _, r := range rows {
		for k, v := range r { // an engine may print a list as text
			if s, ok := v.(string); ok && strings.HasPrefix(s, "[") {
				var list any
				if json.Unmarshal([]byte(s), &list) == nil {
					r[k] = list
				}
			}
		}
	}
	out, _ := json.Marshal(rows)
	return string(out)
}

// TestOrderReadingOverALargeInput: where SQL translates a stage that reads its
// input's order, the answer is the one the file gives. Row n is in group n%5.
// Before the order keys, `-first` here answered 954250 where the answer is 0, and
// a different number on the next run.
func TestOrderReadingOverALargeInput(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("writes a 3 M row file")
	}
	file := orderLargeFile(t)
	dir := filepath.Dir(file)
	run := map[string]func(t *testing.T, sql string) string{}
	if bin := duckdbBinary(); bin != "" {
		run["duckdb"] = func(t *testing.T, sql string) string {
			cmd := exec.Command(bin, "-json", "-c", sql)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("duckdb: %v\n%s", err, out)
			}
			return string(out)
		}
	}
	if py := os.Getenv("SSQL_DATAFUSION_PYTHON"); py != "" {
		script := filepath.Join(dir, "run.py")
		if err := os.WriteFile(script, []byte(datafusionRunner), 0o644); err != nil {
			t.Fatal(err)
		}
		run["datafusion"] = func(t *testing.T, sql string) string {
			cmd := exec.Command(py, script)
			cmd.Dir = dir
			cmd.Stdin = strings.NewReader(sql)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("datafusion: %v\n%s", err, out)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			return "[" + strings.Join(lines, ",") + "]"
		}
	}
	if len(run) == 0 {
		t.Skip("no engine here")
	}
	firstRows := `[{"g":0,"v":0},{"g":1,"v":1},{"g":2,"v":2},{"g":3,"v":3},{"g":4,"v":4}]`
	lastRows := `[{"g":0,"v":2999995},{"g":1,"v":2999996},{"g":2,"v":2999997},{"g":3,"v":2999998},{"g":4,"v":2999999}]`
	few := `where -if-expr "n % 700000 == 0" | ssql `
	cases := []struct {
		name, stages, want string
		duckdbOnly         bool
	}{
		{name: "sort_first", stages: "sort n | ssql group-by g -first n v | ssql sort g", want: firstRows},
		{name: "sort_last", stages: "sort n | ssql group-by g -last n v | ssql sort g", want: lastRows},
		{name: "sort_desc_first", stages: "sort -desc n | ssql group-by g -first n v | ssql sort g", want: lastRows},
		{name: "sort_collect", stages: few + "sort n | ssql group-by g -collect n v",
			want: `[{"g":0,"v":[0,700000,1400000,2100000,2800000]}]`},
		{name: "sort_desc_collect", stages: few + "sort -desc n | ssql group-by g -collect n v",
			want: `[{"g":0,"v":[2800000,2100000,1400000,700000,0]}]`},
		{name: "sort_string_agg", stages: few + "sort n | ssql group-by g -string-agg s : v",
			want: `[{"g":0,"v":"s0:s700000:s1400000:s2100000:s2800000"}]`},
		{name: "sort_offset", stages: "sort n | ssql include n | ssql offset 2999998", want: `[{"n":2999998},{"n":2999999}]`},
		{name: "sort_limit", stages: "where -if n ge 1000 | ssql sort n | ssql include n | ssql limit 2", want: `[{"n":1000},{"n":1001}]`},

		// DuckDB documents this much: the reader's order through WHERE and SELECT,
		// LIMIT and OFFSET over them, and row_number() OVER () as a column.
		{name: "first", stages: "group-by g -first n v | ssql sort g", want: firstRows, duckdbOnly: true},
		{name: "where_first", stages: "where -if n ge 0 | ssql group-by g -first n v | ssql sort g", want: firstRows, duckdbOnly: true},
		{name: "last", stages: "group-by g -last n v | ssql sort g", want: lastRows, duckdbOnly: true},
		{name: "collect", stages: few + "group-by g -collect n v",
			want: `[{"g":0,"v":[0,700000,1400000,2100000,2800000]}]`, duckdbOnly: true},
		{name: "string_agg", stages: few + "group-by g -string-agg s : v",
			want: `[{"g":0,"v":"s0:s700000:s1400000:s2100000:s2800000"}]`, duckdbOnly: true},
		{name: "offset", stages: "where -if n ge 0 | ssql include n | ssql offset 2999998", want: `[{"n":2999998},{"n":2999999}]`, duckdbOnly: true},
		{name: "limit", stages: "where -if n ge 1000 | ssql include n | ssql limit 2", want: `[{"n":1000},{"n":1001}]`, duckdbOnly: true},
	}
	for dialect, exec := range run {
		for _, c := range cases {
			if c.duckdbOnly && dialect != "duckdb" {
				continue
			}
			t.Run(dialect+"/"+c.name, func(t *testing.T) {
				sql, err := orderGenerate(t, dialect, "ssql from csv "+file+" | ssql "+c.stages)
				if err != nil {
					t.Fatalf("generate: %v\n%s", err, sql)
				}
				want := orderCanon(t, c.want)
				for i := 0; i < 4; i++ { // a parallel plan's answer changes between runs
					if got := orderCanon(t, exec(t, sql)); got != want {
						t.Fatalf("run %d\n got %s\nwant %s\nsql:\n%s", i, got, want, sql)
					}
				}
			})
		}
	}
}

// TestOrderTranslatedOnlyWithAGuarantee: a stage that reads its input's order
// translates only where the engine promises the answer; everything else refuses
// and says how to get one. A stage that reads no order is never refused for it.
func TestOrderTranslatedOnlyWithAGuarantee(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := write("a.csv", "n,g\n1,1\n"), write("b.csv", "n,g\n2,2\n")
	side := write("side.csv", "g,label\n1,x\n")
	from := "ssql from csv " + a + " | ssql "
	join := "join " + side + " -using g | ssql "
	const yes, no = "translates", "refuses"
	for _, c := range []struct{ name, stages, duckdb, others string }{
		// No sort: only DuckDB documents an order.
		{"first", from + "group-by g -first n v", yes, no},
		{"last", from + "group-by g -last n v", yes, no},
		{"collect", from + "group-by g -collect n v", yes, no},
		{"string_agg", from + "group-by g -string-agg n : v", yes, no},
		{"rollup_first", from + "group-by g -first n v -rollup", yes, no},
		{"limit", from + "limit 1", yes, no},
		{"offset", from + "offset 1", yes, no},
		{"where_limit", from + "where -if n gt 0 | ssql limit 1", yes, no},
		{"multi_file_first", "ssql from csv " + a + " " + b + " | ssql group-by g -first n v", yes, no},
		{"multi_file_offset", "ssql from csv " + a + " " + b + " | ssql offset 1", yes, no},

		// A stage whose output order no engine promises: not even DuckDB.
		{"join_limit", from + join + "limit 1", no, no},
		{"join_first", from + join + "group-by g -first n v", no, no},
		{"group_limit", from + "group-by g -sum n v | ssql limit 1", no, no},
		{"distinct_limit", from + "include g | ssql distinct | ssql limit 1", no, no},

		// A sort says which order, on every engine.
		{"sort_first", from + "sort n | ssql group-by g -first n v", yes, yes},
		{"sort_last", from + "sort n | ssql group-by g -last n v", yes, yes},
		{"sort_collect", from + "sort n | ssql group-by g -collect n v", yes, yes},
		{"sort_string_agg", from + "sort n | ssql group-by g -string-agg n : v", yes, yes},
		{"sort_limit", from + "sort n | ssql limit 1", yes, yes},
		{"sort_offset", from + "sort n | ssql offset 1", yes, yes},
		{"sort_where_limit", from + "sort n | ssql where -if n gt 0 | ssql limit 1", yes, yes},
		{"sort_include_limit", from + "sort n | ssql include g | ssql limit 1", yes, yes},
		{"sort_update_limit", from + "sort n | ssql update -set-expr m \"n + 1\" | ssql limit 1", yes, yes},
		{"join_sort_limit", from + join + "sort n | ssql limit 1", yes, yes},
		{"join_sort_first", from + join + "sort n | ssql group-by g -first n v", yes, yes},
		{"group_sort_limit", from + "group-by g -sum n v | ssql sort v | ssql limit 1", yes, yes},
		{"top", from + "top 1 -field n", yes, yes},
		{"range_first", "ssql from range 5 | ssql group-by -first n v", yes, yes},
		{"range_limit", "ssql from range 5 | ssql limit 2", yes, yes},

		// A sort below the stage that broke the order says nothing.
		{"sort_join_limit", from + "sort n | ssql " + join + "limit 1", no, no},
		{"sort_group_limit", from + "sort n | ssql group-by g -sum n v | ssql limit 1", no, no},

		// Reads no order: arg's answer is defined but for a tie, and -any promises nothing.
		{"sum", from + "group-by g -sum n v", yes, yes},
		{"count", from + "group-by g -count v", yes, yes},
		{"any", from + "group-by g -any n v", yes, yes},
		{"arg_max", from + "group-by g -arg-max n g v", yes, yes},
		{"where", from + "where -if n gt 0", yes, yes},
		{"join_sum", from + join + "group-by g -sum n v", yes, yes},
		{"window_ordered", from + "window -partition g -order n -sum n r", yes, yes},
	} {
		for _, dialect := range []string{"duckdb", "postgres", "datafusion"} {
			want := c.others
			if dialect == "duckdb" {
				want = c.duckdb
			}
			t.Run(c.name+"/"+dialect, func(t *testing.T) {
				out, err := orderGenerate(t, dialect, c.stages)
				switch {
				case want == yes && err != nil:
					// Another stage's refusal is that stage's business.
					if strings.Contains(out, "reads its input's order") || strings.Contains(out, "reads the order of a sort") {
						t.Errorf("should translate:\n%s", out)
					} else if !strings.Contains(out, "translation") {
						t.Errorf("failed: %v\n%s", err, out)
					}
				case want == no && err == nil:
					t.Errorf("should refuse:\n%s", out)
				case want == no && !strings.Contains(out, "reads its input's order"):
					t.Errorf("the refusal should give the reason:\n%s", out)
				case want == no && !strings.Contains(out, "add a sort") && !strings.Contains(out, "generate go"):
					t.Errorf("the refusal should give the way out:\n%s", out)
				}
			})
		}
	}
	// The answer must rest on the guarantee. Row numbering alone makes DuckDB's plan
	// serial, so the large-input test passes without this ORDER BY, by behaviour.
	for name, stages := range map[string]string{
		"first":      "group-by g -first n v",
		"last":       "group-by g -last n v",
		"collect":    "group-by g -collect n v",
		"string_agg": "group-by g -string-agg n : v",
	} {
		sql, err := orderGenerate(t, "duckdb", from+stages)
		if err != nil || !strings.Contains(sql, "ORDER BY __ssql_row") || !strings.Contains(sql, "row_number() OVER ()") {
			t.Errorf("%s: should read the numbered rows: %v\n%s", name, err, sql)
		}
	}
	// The sort's key is gone by the stage that needs it: say so, not "add a sort".
	for _, stages := range []string{"sort n | ssql include g | ssql group-by g -first g v"} {
		out, err := orderGenerate(t, "duckdb", from+stages)
		if err == nil || !strings.Contains(out, "whose key was dropped") {
			t.Errorf("%s: err %v\n%s", stages, err, out)
		}
	}
}

// TestOrderRefusalInsideASideGivesTheReason: a side that cannot be built was
// "too complex", and a join went on to read the literal /dev/fd/63 as a table.
func TestOrderRefusalInsideASideGivesTheReason(t *testing.T) {
	dir := t.TempDir()
	a, k := filepath.Join(dir, "a.csv"), filepath.Join(dir, "k.csv")
	if err := os.WriteFile(a, []byte("n,g\n1,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k, []byte("g,label\n1,x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	side := "<(ssql from csv " + k + " | ssql limit 1)"
	for name, stage := range map[string]string{
		"join":   "join " + side + " -using g",
		"lookup": "join " + side + " -on g g -as label l",
		"union":  "include g | ssql union -file " + side,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := orderGenerate(t, "postgres", "ssql from csv "+a+" | ssql "+stage)
			if err == nil {
				t.Fatalf("should refuse:\n%s", out)
			}
			if !strings.Contains(out, "reads its input's order") || strings.Contains(out, "/dev/fd") {
				t.Errorf("the refusal should give the side's reason:\n%s", out)
			}
		})
	}
	// The side's own stages say nothing about the main pipeline's order.
	out, err := orderGenerate(t, "duckdb", "ssql from csv "+a+" | ssql union -file <(ssql from csv "+a+" | ssql group-by n g -count c | ssql include n g) | ssql sort n | ssql limit 1")
	if err != nil {
		t.Errorf("a sorted limit after a union should translate: %v\n%s", err, out)
	}
}

// TestSortBeforeCollectSurvivesTheOptimiser: `group-by -collect` lists its group in
// its input's order, so a sort before it means something. The optimiser deleted it
// as a sort before a stage that resets order: exec listed a,b,c, and every
// generated lane listed the file's order (found 2026-11-07).
func TestSortBeforeCollectSurvivesTheOptimiser(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte("n,g,s\n3,1,c\n1,1,a\n2,1,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", "export SSQL_MODE=record; "+bin+" from csv t.csv | "+bin+" sort n | "+bin+" group-by g -collect s v | "+bin+" to jsonl | "+bin+" generate ssql")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ssql sort n") {
		t.Errorf("the sort was dropped:\n%s", out)
	}
}
