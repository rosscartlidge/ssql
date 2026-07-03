package main

// N-way differential equivalence harness.
//
// The corpus (corpus_test.go) is a *smoke* test: it checks that generated
// programs run and their output contains/excludes some substrings. That let a
// real bug through — `top` on a string field ranked wrong in some modes but
// each mode's output still *contained* the expected names, and the fixture
// (employees, alphabetical) didn't discriminate "return first N" from "return
// sorted N".
//
// This harness closes both gaps. For each pipeline it runs EVERY
// result-producing lane —
//
//	exec      the interpreted CLI pipeline (the reference oracle)
//	go-record `generate go` under SSQLGO=record, compiled + run
//	go-typed  `generate go` under SSQLGO=typed
//	go-parallel `generate go` under SSQLGO=parallel
//	ssql-opt  `generate ssql` (the optimised pipeline), re-run
//
// — captures each as canonical JSONL (via `to jsonl`), NORMALISES away the
// legitimate cross-mode differences (column order: record sorts alphabetically,
// typed keeps struct order; number formatting), and asserts every lane is
// EXACTLY equal — as an ordered list when the pipeline defines an order
// (sort/top), else as a multiset (parallel output is unordered).
//
// Fixtures are SHUFFLED with distinct values so a wrong selection/ordering
// actually diverges. Where a `Golden` is given it's an implementation-
// independent oracle: it catches the case where every lane agrees but all are
// wrong (which is exactly how the `top` execution bug hid).

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// corpusShuffledCSV: distinct city + pop, in neither city- nor pop-sorted
// order, so "first N by input" differs from "N by value" — the property the
// alphabetical employees fixture lacked.
const corpusShuffledCSV = `id,city,pop
7,Mumbai,20
3,Cairo,10
9,Lima,7
1,Oslo,31
5,Tokyo,37
2,Delhi,29
8,Lagos,14
4,Paris,11
6,Nairobi,4
10,Quito,2
12,Hanoi,9
11,Bogota,25
`

// EquivCase is one pipeline that every lane must agree on. Pipeline ends
// BEFORE the sink — the harness appends `| <bin> to jsonl` for a canonical
// capture. Uses {{.bin}} / {{.data}} like the corpus.
type EquivCase struct {
	Name     string
	Pipeline string
	Ordered  bool              // output order is semantically defined
	Golden   []map[string]any  // optional implementation-independent oracle
	Skip     map[string]string // lane name -> skip reason
}

// equivLane is one result-producing path.
type equivLane struct {
	name string
	run  func(t *testing.T, bin, pipeline string) string // returns raw JSONL
}

func equivSentinel(mode string) string {
	if mode == "record" {
		return "1"
	}
	return mode
}

func equivLanes() []equivLane {
	goLane := func(name, mode string) equivLane {
		return equivLane{name, func(t *testing.T, bin, pipeline string) string {
			src := equivShell(t, name, "export SSQLGO="+equivSentinel(mode)+" && "+
				pipeline+" | "+bin+" to jsonl | "+bin+" generate go")
			return goRunGenerated(t, src)
		}}
	}
	lanes := []equivLane{
		{"exec", func(t *testing.T, bin, pipeline string) string {
			return equivShell(t, "exec", pipeline+" | "+bin+" to jsonl")
		}},
		goLane("go-record", "record"),
		goLane("go-typed", "typed"),
		goLane("go-parallel", "parallel"),
		{"ssql-opt", func(t *testing.T, bin, pipeline string) string {
			gen := equivShell(t, "ssql-opt-gen", "export SSQLGO=1 && "+
				pipeline+" | "+bin+" to jsonl | "+bin+" generate ssql")
			// generate ssql emits a pipeline that invokes bare `ssql`; run it
			// with the test binary. Strip any comment lines first.
			var parts []string
			for _, ln := range strings.Split(strings.TrimSpace(gen), "\n") {
				ln = strings.TrimSpace(ln)
				if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "--") {
					continue
				}
				parts = append(parts, ln)
			}
			opt := strings.ReplaceAll(strings.Join(parts, " "), "ssql ", bin+" ")
			return equivShell(t, "ssql-opt", opt)
		}},
	}
	// The DuckDB lane is the independent second-engine oracle: the pipeline is
	// translated by `generate sql` and executed by DuckDB, whose implementation
	// shares nothing with ssql — a unanimous-but-wrong answer across the Go
	// lanes can't fool it. Only present when a duckdb binary is available.
	if duckdb := duckdbBinary(); duckdb != "" {
		lanes = append(lanes, equivLane{"duckdb", func(t *testing.T, bin, pipeline string) string {
			sql := equivShell(t, "duckdb-gen", "export SSQL_MODE=record && "+
				pipeline+" | "+bin+" to jsonl | "+bin+" generate sql")
			cmd := exec.Command(duckdb, "-json", "-c", sql)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("lane %q: duckdb failed: %v\n  sql:\n%s\n  stderr:\n%s",
					"duckdb", err, sql, stderr.String())
			}
			// duckdb -json prints one JSON array; re-emit as JSONL for equivParse.
			raw := strings.TrimSpace(stdout.String())
			if raw == "" {
				return ""
			}
			var rows []map[string]any
			if err := json.Unmarshal([]byte(raw), &rows); err != nil {
				t.Fatalf("lane %q: bad duckdb -json output: %v\n%s", "duckdb", err, raw)
			}
			// duckdb -json renders HUGEINT (e.g. SUM over BIGINT) as a JSON
			// string. ssql's CSV reader parses canonical integer strings as
			// numbers anyway, so converting them back is normalising a
			// representation difference, not masking a value difference.
			for _, r := range rows {
				for k, v := range r {
					if s, ok := v.(string); ok && canonicalIntRe.MatchString(s) {
						if f, err := strconv.ParseFloat(s, 64); err == nil {
							r[k] = f
						}
					}
				}
			}
			var sb strings.Builder
			for _, r := range rows {
				b, _ := json.Marshal(r)
				sb.Write(b)
				sb.WriteByte('\n')
			}
			return sb.String()
		}})
	}
	return lanes
}

var canonicalIntRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// duckdbBinary locates duckdb (PATH, then ~/.local/bin); empty when absent so
// the DuckDB lane degrades to skipped rather than failing the suite.
func duckdbBinary() string {
	if p, err := exec.LookPath("duckdb"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "duckdb")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// equivShell runs a bash pipeline and returns stdout, failing with stderr on
// error (stderr is kept separate so it never contaminates the captured data).
func equivShell(t *testing.T, lane, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("lane %q failed:\n  script: %s\n  err: %v\n  stderr:\n%s",
			lane, script, err, stderr.String())
	}
	return stdout.String()
}

// equivParse turns raw JSONL into records, skipping blanks and any _schema
// header. json.Unmarshal makes every number a float64, normalising int-vs-float
// representation across lanes.
func equivParse(t *testing.T, lane, raw string) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("lane %q: bad JSONL line %q: %v", lane, line, err)
		}
		if _, isSchema := m["_schema"]; isSchema {
			continue
		}
		recs = append(recs, m)
	}
	return recs
}

// equivCanon renders records as canonical strings: json.Marshal sorts map keys,
// so column order is normalised. For unordered output the rows are sorted so
// two lanes with different row order still compare equal.
func equivCanon(recs []map[string]any, ordered bool) []string {
	out := make([]string, len(recs))
	for i, m := range recs {
		b, _ := json.Marshal(m)
		out[i] = string(b)
	}
	if !ordered {
		sort.Strings(out)
	}
	return out
}

// TestPipelineEquivalence is the N-way differential gate.
func TestPipelineEquivalence(t *testing.T) {
	if testing.Short() {
		t.Skip("equivalence tests are slow (each lane compiles + runs)")
	}
	bin := corpusBin(t)
	data := corpusData(t)
	repl := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data)
	if duckdbBinary() == "" {
		t.Log("duckdb not found (PATH or ~/.local/bin) — the second-engine SQL lane is skipped")
	}

	for _, c := range equivCases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			pipeline := repl.Replace(c.Pipeline)
			lanes := equivLanes()

			results := make(map[string][]map[string]any)
			for _, ln := range lanes {
				if reason := c.Skip[ln.name]; reason != "" {
					continue
				}
				results[ln.name] = equivParse(t, ln.name, ln.run(t, bin, pipeline))
			}

			ref, ok := results["exec"]
			if !ok {
				t.Fatal("exec lane is the reference oracle and must not be skipped")
			}

			// Ground truth: exec must match the implementation-independent
			// golden when supplied (catches "all lanes agree but all wrong").
			if c.Golden != nil {
				if wantC, gotC := equivCanon(c.Golden, c.Ordered), equivCanon(ref, c.Ordered); !slices.Equal(gotC, wantC) {
					t.Errorf("exec lane disagrees with golden:\n  golden: %s\n  exec:   %s",
						strings.Join(wantC, " "), strings.Join(gotC, " "))
				}
			}

			// Every lane must match the reference.
			refC := equivCanon(ref, c.Ordered)
			for _, ln := range lanes {
				got, ok := results[ln.name]
				if !ok || ln.name == "exec" {
					continue
				}
				if gotC := equivCanon(got, c.Ordered); !slices.Equal(gotC, refC) {
					t.Errorf("lane %q disagrees with exec (%s):\n  exec: %s\n  %s: %s",
						ln.name, orderedLabel(c.Ordered),
						strings.Join(refC, " "), ln.name, strings.Join(gotC, " "))
				}
			}
		})
	}
}

func orderedLabel(ordered bool) string {
	if ordered {
		return "ordered"
	}
	return "as multiset"
}

var equivCases = []EquivCase{
	{
		Name:     "identity",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv`,
		Ordered:  false, // parallel output is unordered
	},
	{
		Name:     "where",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 15`,
		Ordered:  false,
	},
	{
		// -if-expr exercises the expr→SQL translation: `&&` and "double
		// quotes" mean something different in SQL, so verbatim passthrough
		// (the pre-v4.56 behaviour) is a DuckDB parse/binder error.
		Name:     "where_expr",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if-expr 'pop > 15 && city != "Oslo"'`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 7, "city": "Mumbai", "pop": 20},
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 2, "city": "Delhi", "pop": 29},
			{"id": 11, "city": "Bogota", "pop": 25},
		},
	},
	{
		// -set-expr was silently DROPPED by the SQL translator before v4.56
		// (the update emitted no REPLACE), so DuckDB returned unmodified rows.
		Name:     "update_set_expr",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update -if pop gt 25 -set-expr city 'upper(city)'`,
		Ordered:  false,
		Skip: map[string]string{
			"go-typed":    "typed codegen rejects update -set-expr (loud Tier-3 error)",
			"go-parallel": "typed codegen rejects update -set-expr (loud Tier-3 error)",
		},
	},
	{
		// The discriminating case: top by a STRING field on shuffled data,
		// with a golden oracle. This is the exact bug shape that slipped
		// through the substring corpus.
		Name:     "top_string_desc",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} top 3 -field city`,
		Ordered:  true,
		Golden: []map[string]any{
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 10, "city": "Quito", "pop": 2},
			{"id": 4, "city": "Paris", "pop": 11},
		},
	},
	{
		Name:     "top_string_asc",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} top 3 -field city -asc`,
		Ordered:  true,
		Golden: []map[string]any{
			{"id": 11, "city": "Bogota", "pop": 25},
			{"id": 3, "city": "Cairo", "pop": 10},
			{"id": 2, "city": "Delhi", "pop": 29},
		},
	},
	{
		Name:     "top_numeric",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} top 3 -field pop`,
		Ordered:  true,
		Golden: []map[string]any{
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 1, "city": "Oslo", "pop": 31},
			{"id": 2, "city": "Delhi", "pop": 29},
		},
	},
	{
		Name:     "sort_string",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} sort city`,
		Ordered:  true,
	},
	{
		Name:     "group_by",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -count cnt -sum salary total`,
		Ordered:  false, // group emission order differs across modes
	},
}
