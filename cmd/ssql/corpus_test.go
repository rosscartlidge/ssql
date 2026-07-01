package main

// Pipeline regression-test corpus.
//
// Every CLI pipeline must support code generation in three modes —
// record (default), typed (SSQLGO=typed), and parallel (SSQLGO=parallel).
// This file runs a fixed set of representative pipelines (pulled
// from the CLI tutorials) through all three modes: it pipes the
// pipeline into `generate go`, writes the output as main.go, builds
// it, runs the resulting binary, and verifies expected substrings
// in stdout.
//
// As we add more typed/parallel command coverage, this corpus is
// our safety net. Adding a new pipeline here is cheap; the cost of
// silently breaking a tutorial example is high.
//
// This is a SMOKE test (Contains/Excludes substrings). For the stronger
// N-way DIFFERENTIAL gate — every result-producing lane (exec, go-record,
// go-typed, go-parallel, generate-ssql) must produce byte-identical
// normalised output, with golden oracles on shuffled data — see
// equivalence_test.go (TestPipelineEquivalence). That harness catches the
// "works in mode X, silently wrong in mode Y" bugs this one can miss.
//
// To run only the corpus tests:
//   go test ./cmd/ssql -run TestPipelineCorpus -v
//
// To skip the slow build-and-run tests:
//   go test ./cmd/ssql -run TestPipelineCorpus -short

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// PipelineCase describes one pipeline to test across codegen modes.
//
// Pipeline is a function that returns a bash command starting with
// the ssql binary path and ending in `| <bin> generate go`. The
// helper substitutes the test binary path and data directory.
type PipelineCase struct {
	Name string
	// Pipeline returns the full shell pipeline ending in
	// `... | <bin> generate go`. Must reference the binary as
	// {{.bin}} and data files as {{.data}}/<name>.csv.
	Pipeline string
	Contains []string // substrings the program output must contain
	Excludes []string // substrings the program output must NOT contain
	// SkipTyped/SkipParallel: non-empty reason → skip that mode.
	SkipRecord   string
	SkipTyped    string
	SkipParallel string
}

// Shared corpus binary — built once across all corpus subtests to
// keep the test suite fast.
var (
	corpusBinOnce  sync.Once
	corpusBinPath  string
	corpusBinErr   error
	corpusDataOnce sync.Once
	corpusDataDir  string
	corpusDataErr  error
)

func corpusBin(t *testing.T) string {
	t.Helper()
	corpusBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ssql-corpus-bin-")
		if err != nil {
			corpusBinErr = err
			return
		}
		corpusBinPath = filepath.Join(dir, "ssql")
		cmd := exec.Command("go", "build", "-o", corpusBinPath, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			corpusBinErr = fmt.Errorf("build ssql: %v\n%s", err, out)
		}
	})
	if corpusBinErr != nil {
		t.Fatal(corpusBinErr)
	}
	return corpusBinPath
}

// corpusData writes the shared test data files (employees, customers,
// orders) into a temp dir and returns the path. The files are small
// (<60 rows) so each pipeline round-trip stays fast.
func corpusData(t *testing.T) string {
	t.Helper()
	corpusDataOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ssql-corpus-data-")
		if err != nil {
			corpusDataErr = err
			return
		}
		corpusDataDir = dir
		files := map[string]string{
			"employees.csv": corpusEmployeesCSV,
			"customers.csv": corpusCustomersCSV,
			"orders.csv":    corpusOrdersCSV,
			"sales.csv":     corpusSalesCSV,
			"employees.tsv": strings.ReplaceAll(corpusEmployeesCSV, ",", "\t"),
			"shuffled.csv":  corpusShuffledCSV,
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				corpusDataErr = err
				return
			}
		}
	})
	if corpusDataErr != nil {
		t.Fatal(corpusDataErr)
	}
	return corpusDataDir
}

// runCorpusPipeline runs a corpus pipeline through `generate go`
// in the given mode (env "" for record, "typed" or "parallel"),
// compiles the generated source, runs the resulting binary, and
// returns its stdout.
func runCorpusPipeline(t *testing.T, mode, pipeline string) string {
	t.Helper()
	bin := corpusBin(t)
	data := corpusData(t)
	cmdline := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(pipeline)

	// Record mode = SSQLGO=1; typed = SSQLGO=typed; parallel = SSQLGO=parallel.
	// All three need SSQLGO set so each command emits code fragments
	// instead of executing.
	sentinel := mode
	if sentinel == "" {
		sentinel = "1"
	}
	full := "export SSQLGO=" + sentinel + " && " + cmdline + " | " + bin + " generate go"

	cmd := exec.Command("bash", "-c", full)
	src, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate go failed (mode=%q):\n  pipeline: %s\n  err: %v\n  output:\n%s",
			mode, full, err, src)
	}

	return goRunGenerated(t, string(src))
}

// runCorpusCase exercises a single PipelineCase across all modes
// (record, typed, parallel). Each mode runs as a t.Run subtest so a
// failure in one mode doesn't mask failures in the others.
func runCorpusCase(t *testing.T, c PipelineCase) {
	modes := []struct {
		name string
		env  string
		skip string
	}{
		{"record", "", c.SkipRecord},
		{"typed", "typed", c.SkipTyped},
		{"parallel", "parallel", c.SkipParallel},
	}
	for _, m := range modes {
		m := m
		t.Run(m.name, func(t *testing.T) {
			if m.skip != "" {
				t.Skip(m.skip)
			}
			t.Parallel()
			out := runCorpusPipeline(t, m.env, c.Pipeline)
			for _, want := range c.Contains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\n--- pipeline:\n%s\n--- output:\n%s",
						want, c.Pipeline, out)
				}
			}
			for _, dontWant := range c.Excludes {
				if strings.Contains(out, dontWant) {
					t.Errorf("output unexpectedly contains %q\n--- pipeline:\n%s\n--- output:\n%s",
						dontWant, c.Pipeline, out)
				}
			}
		})
	}
}

// TestPipelineCorpus walks the corpus and exercises each pipeline
// across the three codegen modes.
func TestPipelineCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus tests are slow (each compiles + runs generated Go)")
	}

	cases := []PipelineCase{
		// --- Sources / sinks ----------------------------------
		{
			Name:     "from_csv_to_table",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} to table`,
			Contains: []string{"Alice", "Bob", "Engineering"},
		},
		{
			Name:     "to_table_maxwidth",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} to table -max-width 6`,
			// -max-width must truncate identically in record and typed
			// modes: "Engineering" (11) → "Eng..." (6). Before, typed
			// codegen ignored -max-width and showed the full value.
			Contains: []string{"Eng..."},
			Excludes: []string{"Engineering"},
		},
		{
			Name:     "from_csv_to_csv",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} to csv`,
			// Record mode reorders columns alphabetically (no schema
			// preservation); typed/parallel preserve struct order.
			// So just assert each cell value appears.
			Contains: []string{"Alice", "Bob", "Engineering"},
		},
		{
			Name:     "from_csv_to_jsonl",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} to jsonl`,
			Contains: []string{`"name":"Alice"`, `"age":35`},
			// to jsonl is not yet wired up for typed mode (Tier 2).
			SkipTyped:    "to jsonl: typed-mode sink not implemented",
			SkipParallel: "to jsonl: typed-mode sink not implemented",
		},
		{
			Name:     "from_tsv_to_table",
			Pipeline: `{{.bin}} from {{.data}}/employees.tsv | {{.bin}} to table`,
			Contains: []string{"Alice", "Engineering"},
		},

		// --- Filtering / projection ---------------------------
		{
			Name:     "where_simple",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} where -if age gt 30 | {{.bin}} to csv`,
			Contains: []string{"Alice", "Carol"},
			Excludes: []string{"Bob,28"}, // Bob (age 28) filtered out
		},
		{
			Name:     "where_string_eq",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} where -if dept eq Engineering | {{.bin}} to csv`,
			Contains: []string{"Alice", "Carol"},
			Excludes: []string{"Bob,28,Sales"},
		},
		{
			Name:     "include_projection",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} include name salary | {{.bin}} to csv`,
			Contains: []string{"name,salary", "Alice,95000"},
			Excludes: []string{"dept", "Engineering"}, // not projected
		},
		{
			Name:     "exclude_projection",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} exclude hire_date status | {{.bin}} to csv`,
			Contains: []string{"name", "age", "dept", "Alice"},
			// "hire_date" and "status" should not appear in headers.
			// We can't assert "active" not in body since some other
			// data might mention it, but the field name is
			// distinctive.
			Excludes: []string{"hire_date"},
		},
		{
			Name:     "rename",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} rename -as name full_name -as salary pay | {{.bin}} to csv`,
			Contains: []string{"full_name", "pay", "Alice"},
		},
		{
			// Regression: include then no-agg group-by both project to a
			// derived struct. In typed mode the two projections used to be
			// named "included" → "no new variables on left side of :=" /
			// type mismatch (uniqueVarName now disambiguates). group-by with
			// no aggregations == project-to-keys + distinct.
			Name:     "include_then_groupby",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} include name dept | {{.bin}} group-by dept | {{.bin}} to csv`,
			Contains: []string{"Engineering", "Sales", "Marketing"},
			Excludes: []string{"name", "Alice"}, // group-by dept drops name
		},

		// --- Ordering / dedup ---------------------------------
		{
			Name:     "sort_desc",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} sort -desc salary | {{.bin}} to csv`,
			// All rows still present. Sort-order can't be asserted via
			// substring-equality because record mode reorders columns
			// alphabetically; typed mode keeps struct order. Both
			// modes produce all rows. (Order is checked in the typed
			// integration tests separately.)
			Contains: []string{"Carol", "Alice", "105000", "95000"},
		},
		{
			Name:     "distinct",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} include dept | {{.bin}} distinct | {{.bin}} to csv`,
			Contains: []string{"Engineering", "Sales", "Marketing"},
		},
		{
			Name:     "limit",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} sort name | {{.bin}} limit 2 | {{.bin}} to csv`,
			// After sort by name, first 2 are Alice, Bob.
			Contains: []string{"Alice", "Bob"},
			Excludes: []string{"Carol"},
		},
		{
			Name:     "offset_limit",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} sort name | {{.bin}} offset 1 | {{.bin}} limit 2 | {{.bin}} to csv`,
			Contains: []string{"Bob", "Carol"},
			Excludes: []string{"Alice,35"},
		},
		{
			Name:     "top",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} top 2 -field salary | {{.bin}} to csv`,
			// Top 2 by salary (default desc): Carol (105000), Alice (95000).
			// Heap-based: typed/parallel emit typed.TopBy / typed.TopByParallel.
			Contains: []string{"Carol", "Alice", "105000", "95000"},
			Excludes: []string{"Bob"},
		},
		{
			Name:     "top_asc",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} top 2 -field salary -asc | {{.bin}} to csv`,
			// Bottom 2 by salary (-asc): Bob (65000), David (72000).
			// Exercises ssql.BottomBy / typed.BottomBy / typed.BottomByParallel.
			Contains: []string{"Bob", "David", "65000", "72000"},
			Excludes: []string{"Carol"},
		},
		{
			Name:     "top_string_asc",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} top 2 -field name -asc | {{.bin}} to csv`,
			// Bottom 2 by NAME (-asc), lexicographic: Alice, Bob. Guards that
			// record-mode `top` orders strings the same as the typed codegen
			// (the old numeric-only key collapsed all strings to 0 and
			// returned arbitrary rows).
			Contains: []string{"Alice", "Bob"},
			Excludes: []string{"Grace", "Frank"},
		},
		{
			Name:     "top_string_desc",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} top 2 -field name | {{.bin}} to csv`,
			// Top 2 by NAME (desc), lexicographic: Grace, Frank. This case is
			// DISCRIMINATING — employees are in alphabetical input order, so a
			// buggy "return the first 2 rows" (the old float64-key-coerces-to-0
			// behaviour) would yield Alice/Bob, which this catches. Covers the
			// record-mode codegen path the earlier -asc case couldn't.
			Contains: []string{"Grace", "Frank"},
			Excludes: []string{"Alice", "Bob"},
		},

		// --- Aggregation --------------------------------------
		{
			Name:     "group_by_count",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} group-by dept -count n | {{.bin}} to csv`,
			Contains: []string{"Engineering", "Sales", "Marketing"},
		},
		{
			// No aggregations — DISTINCT on the grouped field.
			// Three rows expected: Engineering, Sales, Marketing.
			Name:     "group_by_no_aggs",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} group-by dept | {{.bin}} to csv`,
			Contains: []string{"Engineering", "Sales", "Marketing"},
			Excludes: []string{"Alice", "salary"}, // projected away
		},
		{
			Name:     "group_by_sum_avg",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} group-by dept -sum salary total -avg salary avg_pay | {{.bin}} to csv`,
			Contains: []string{"Engineering", "total", "avg_pay"},
		},

		// --- Mutations / casts --------------------------------
		{
			Name:     "update_set_const",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} update -set tag staff | {{.bin}} to csv`,
			Contains: []string{"tag", "staff", "Alice"},
		},
		{
			Name:     "cast_to_string",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} cast -type age string | {{.bin}} include name age | {{.bin}} to csv`,
			Contains: []string{"Alice", "Bob", "35", "28"},
		},

		// --- Joins / unions / merges --------------------------
		{
			Name: "join_using",
			Pipeline: `{{.bin}} from {{.data}}/orders.csv | ` +
				`{{.bin}} join <({{.bin}} from {{.data}}/customers.csv) -using customer_id | ` +
				`{{.bin}} to csv`,
			// Record mode: order_id (snake), typed mode: OrderID (Camel).
			// Just check the values are joined correctly.
			Contains: []string{"Widget", "customer_1"},
		},
		{
			Name: "union_distinct",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | ` +
				`{{.bin}} include dept | ` +
				`{{.bin}} union -file <({{.bin}} from {{.data}}/employees.csv | {{.bin}} include dept) | ` +
				`{{.bin}} distinct | ` +
				`{{.bin}} to csv`,
			Contains: []string{"Engineering", "Sales", "Marketing"},
		},

		// --- Count sink --------------------------------------
		{
			Name:     "count_simple",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} count`,
			// 7 rows in corpusEmployeesCSV
			Contains: []string{"7"},
		},
		{
			Name:     "count_after_where",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} where -if age gt 30 | {{.bin}} count`,
			// Alice(35), Carol(42), David(31), Frank(45), Grace(33) → 5
			Contains: []string{"5"},
		},
		{
			Name:     "count_after_sort",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | {{.bin}} sort name | {{.bin}} count`,
			// All 7 rows after sort
			Contains: []string{"7"},
		},

		// --- Phase B mixed-mode (typed → Record adapter) ------
		{
			// Pivot is Tier 3 (Record-only). Under typed mode the
			// planner inserts a typed→Record boundary upstream and
			// the rest of the pipeline runs on Records.
			Name:       "mixed_pivot",
			Pipeline:   `{{.bin}} from {{.data}}/sales.csv | {{.bin}} pivot -row product -col region -val amount | {{.bin}} to csv`,
			Contains:   []string{"product", "Widget", "Gadget"},
			SkipRecord: "pivot is record-mode by default; this case targets typed→Record boundary",
		},

		// --- Compound pipelines (the realistic shape) ---------
		{
			Name: "where_groupby_sort_limit",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | ` +
				`{{.bin}} where -if status eq active | ` +
				`{{.bin}} group-by dept -count n -sum salary total | ` +
				`{{.bin}} sort -desc total | ` +
				`{{.bin}} limit 5 | ` +
				`{{.bin}} to csv`,
			Contains: []string{"dept", "n", "total"},
		},
		{
			Name: "where_include_sort",
			Pipeline: `{{.bin}} from {{.data}}/employees.csv | ` +
				`{{.bin}} where -if salary gt 70000 | ` +
				`{{.bin}} include name salary | ` +
				`{{.bin}} sort -desc salary | ` +
				`{{.bin}} to csv`,
			Contains: []string{"name", "salary", "Carol", "105000", "Alice", "95000"},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			runCorpusCase(t, c)
		})
	}
}

// --- shared corpus data ---------------------------------------

const corpusEmployeesCSV = `name,age,dept,salary,city,level,hire_date,status
Alice,35,Engineering,95000,SF,7,2018-03-15,active
Bob,28,Sales,65000,NYC,4,2021-06-01,active
Carol,42,Engineering,105000,SF,9,2015-01-10,active
David,31,Marketing,72000,Chicago,5,2020-04-22,active
Eve,29,Engineering,88000,SF,6,2019-08-30,active
Frank,45,Sales,82000,NYC,8,2014-02-18,active
Grace,33,Marketing,78000,Chicago,6,2017-11-05,inactive
`

const corpusCustomersCSV = `customer_id,name,country,tier
1,customer_1,US,bronze
2,customer_2,UK,silver
3,customer_3,US,gold
4,customer_4,DE,bronze
5,customer_5,FR,silver
`

const corpusOrdersCSV = `order_id,customer_id,product,amount,order_date
1,3,Widget,245.50,2024-01-15
2,1,Gadget,89.99,2024-01-18
3,5,Doohickey,512.00,2024-02-01
4,2,Widget,178.25,2024-02-10
`

const corpusSalesCSV = `product,region,amount
Widget,US,100
Widget,EU,150
Gadget,US,200
Gadget,EU,80
Widget,US,50
`
