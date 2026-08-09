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
			runEquivCase(t, bin, repl.Replace(c.Pipeline), c)
		})
	}
}

// runEquivCase runs one pipeline through every lane and asserts agreement
// (and the golden, when supplied). Shared by the hand-written case list and
// the permutation generator.
func runEquivCase(t *testing.T, bin, pipeline string, c EquivCase) {
	t.Helper()
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
}

// TestPipelinePermutations enumerates every ordered PAIR of a small stage set
// and runs each 2-stage pipeline through all lanes. Rationale: each v4.56
// stage-order bug (limit|group-by flattened wrong, update|group-by invalid,
// projection pairs colliding) was a two-stage ORDERING that no hand-written
// case exercised — orderings are cheap to enumerate mechanically, so
// enumerate them all instead of waiting for a user to hit each shape.
//
// Every pipeline is prefixed with `sort id` so "first N" semantics are
// deterministic in every lane (including the DuckDB one).
func TestPipelinePermutations(t *testing.T) {
	if testing.Short() {
		t.Skip("permutation equivalence tests are slow (each lane compiles + runs)")
	}
	bin := corpusBin(t)
	data := corpusData(t)
	repl := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data)

	stages := []struct{ key, cmd string }{
		{"where", `{{.bin}} where -if pop gt 5`},
		// Typed lanes run this NATIVE as of expr-transpiler Phase 1. Only pop
		// is referenced — the one field every other stage's output retains
		// (group-by pop -count drops city).
		{"whereexpr", `{{.bin}} where -if-expr 'pop > 5 && pop != 9'`},
		{"sort", `{{.bin}} sort pop -desc`},
		{"limit", `{{.bin}} limit 5`},
		{"group", `{{.bin}} group-by pop -count cnt`},
		{"distinct", `{{.bin}} distinct`},
	}
	// group|limit is skipped because the PIPELINE itself is nondeterministic:
	// group-by emission order is unspecified, so "first 5 groups" legitimately
	// differs between lanes. Not a translation bug — there is nothing to agree on.
	nondeterministic := map[string]bool{"group|limit": true}

	for i, a := range stages {
		for j, b := range stages {
			if i == j || nondeterministic[a.key+"|"+b.key] {
				continue
			}
			c := EquivCase{
				Name: "perm_" + a.key + "_then_" + b.key,
				Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} sort id | ` +
					a.cmd + ` | ` + b.cmd,
				Ordered: false,
			}
			t.Run(c.Name, func(t *testing.T) {
				t.Parallel()
				runEquivCase(t, bin, repl.Replace(c.Pipeline), c)
			})
		}
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
		// +if negation was silently DROPPED by record and typed codegen
		// (the condition was applied UN-negated), while exec and the SQL
		// translator honoured it.
		Name:     "where_negated_if",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where +if city eq Oslo`,
		Ordered:  false,
	},
	{
		// +if-expr entries arrive as {"expression":…, "_negated":true} maps;
		// record codegen read them as plain strings and dropped the whole
		// condition (returning every row).
		Name:     "where_negated_expr",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where +if-expr 'pop > 15'`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 3, "city": "Cairo", "pop": 10},
			{"id": 9, "city": "Lima", "pop": 7},
			{"id": 8, "city": "Lagos", "pop": 14},
			{"id": 4, "city": "Paris", "pop": 11},
			{"id": 6, "city": "Nairobi", "pop": 4},
			{"id": 10, "city": "Quito", "pop": 2},
			{"id": 12, "city": "Hanoi", "pop": 9},
		},
	},
	{
		// update codegen dropped +if negation the same way.
		Name:     "update_negated_if",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update +if pop gt 15 -set city Low`,
		Ordered:  false,
	},
	{
		// update record codegen only parsed -if-expr when a -if flag was ALSO
		// present — `update -if-expr … -set …` (the shipped help example
		// shape) generated an UNCONDITIONAL update.
		Name:     "update_if_expr_only",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update -if-expr 'pop > 25' -set city Big`,
		Ordered:  false,
		// go-typed/go-parallel unskipped in Phase 1 of the expr transpiler:
		// the predicate now transpiles to native Go in typed mode.
	},
	{
		// generate ssql's optimiser: parseWhereArgs didn't recognise +if, so
		// any rule that REBUILT the where args dropped it. Here range
		// tightening (gt 5 + ge 8 → ge 8) triggers the rebuild and the
		// ssql-opt lane silently lost the +if — returning pop >= 8 instead of
		// pop >= 8 AND NOT(pop < 12). The +if must also stay OPAQUE to the
		// tightening itself (its bounds are inverted). Golden = pop >= 12.
		// (Distinct operators on purpose: duplicate field+op conditions hit a
		// separate record-codegen flag-naming bug, tracked in TODO.md.)
		Name:     "where_negated_survives_simplify",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 5 -if pop ge 8 +if pop lt 12`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 7, "city": "Mumbai", "pop": 20},
			{"id": 1, "city": "Oslo", "pop": 31},
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 2, "city": "Delhi", "pop": 29},
			{"id": 8, "city": "Lagos", "pop": 14},
			{"id": 11, "city": "Bogota", "pop": 25},
		},
	},
	{
		// Same optimiser class for +if-expr: predicate reorder (ne is cheaper
		// than gt, so the conditions swap) rebuilds the where args and the
		// unrecognised +if-expr token vanished from the optimised pipeline.
		Name:     "where_negated_expr_survives_reorder",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 8 -if city ne Oslo +if-expr 'pop > 12'`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 3, "city": "Cairo", "pop": 10},
			{"id": 4, "city": "Paris", "pop": 11},
			{"id": 12, "city": "Hanoi", "pop": 9},
		},
	},
	{
		// Duplicate field+op conditions: record codegen derived flag var
		// names from field+op only, and collectParams' rename rewrote BOTH
		// identical references to the last name — `pop gt 5` silently became
		// `pop gt 8`. Invisible for ANDed same-direction bounds; the +if mix
		// makes it wrong (pop>8 && !(pop>8) = empty). Golden = 5 < pop <= 8.
		Name:     "where_dup_fieldop_negated",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 5 +if pop gt 8`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 9, "city": "Lima", "pop": 7},
		},
	},
	{
		// THREE duplicates of the same field+op: the sequential ReplaceAll
		// rename corrupted by prefix (*flagPopGt inside *flagPopGt2 →
		// *flagPopGt32, undeclared) — the generated record code didn't even
		// compile. Golden = 5 < pop <= 10.
		Name:     "where_dup_fieldop_three",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 2 -if pop gt 5 +if pop gt 10`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 3, "city": "Cairo", "pop": 10},
			{"id": 9, "city": "Lima", "pop": 7},
			{"id": 12, "city": "Hanoi", "pop": 9},
		},
	},
	{
		// Cross-fragment rename guard: the first where now emits pop-gt AND
		// pop-gt2 itself, so the second where's pop-gt must rename PAST the
		// taken suffix (pop-gt3) — a naive count-based rename would register
		// the same flag name twice and panic at flag.Parse. Golden = pop > 10.
		Name:     "where_dup_fieldop_two_stages",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 2 -if pop gt 5 | {{.bin}} where -if pop gt 10`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 7, "city": "Mumbai", "pop": 20},
			{"id": 1, "city": "Oslo", "pop": 31},
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 2, "city": "Delhi", "pop": 29},
			{"id": 8, "city": "Lagos", "pop": 14},
			{"id": 4, "city": "Paris", "pop": 11},
			{"id": 11, "city": "Bogota", "pop": 25},
		},
	},
	{
		// -stream-expr as a typed accumulator (expr-transpiler Phase 2):
		// single group over all rows, classic avg fold. Golden =
		// sum(pop)/12 = 199/12. duckdb skipped: generate sql rejects
		// -stream-expr loudly (v4.56.0 behaviour, by design).
		Name:     "groupby_stream_avg",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by -stream-expr '{s:0, n:0}' '{s:s+pop, n:n+1}' 's/n' avg_pop`,
		Ordered:  false,
		Golden: []map[string]any{
			{"avg_pop": 16.583333333333332},
		},
		Skip: map[string]string{
			"duckdb": "-stream-expr has no SQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// Widening fixpoint: init declares s as int, every adds pop/2
		// (float division!) — the state must widen to float64 and the
		// division must NOT become Go integer division. Golden = 199/2.
		Name:     "groupby_stream_widening",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by -stream-expr '{s:0}' '{s: s + pop/2}' 's' half_sum`,
		Ordered:  false,
		Golden: []map[string]any{
			{"half_sum": 99.5},
		},
		Skip: map[string]string{
			"duckdb": "-stream-expr has no SQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// SIMULTANEITY gate: every's object is computed from the OLD state
		// then replaces it — {a: b, b: a} must SWAP each row. Sequential
		// assignment (a=b then b=a-already-overwritten) converges to (1,1)
		// and returns 1; the correct fold alternates and returns 0 after 12
		// rows. The golden catches unanimous-but-wrong.
		Name:     "groupby_stream_swap",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by -stream-expr '{a:0, b:1}' '{a: b, b: a}' 'a' flip`,
		Ordered:  false,
		Golden: []map[string]any{
			{"flip": 0},
		},
		Skip: map[string]string{
			"duckdb": "-stream-expr has no SQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// Per-group state reset + mixing a built-in aggregation with a
		// stream fold in one aggregator struct.
		Name:     "groupby_stream_grouped",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by city -count c -stream-expr '{s:0, n:0}' '{s:s+pop, n:n+1}' 's/n' v`,
		Ordered:  false,
		Skip: map[string]string{
			"duckdb": "-stream-expr has no SQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// Tier V (expr-transpiler Phase 1.5): sha256 is outside exprToGo's
		// native subset, so typed lanes evaluate it with the VM against a
		// generated static env — WITHOUT ejecting the stage to record mode.
		// Golden = cities whose sha256 hex digest sorts above "8"
		// (precomputed; sha256 is deterministic).
		Name:     "where_expr_tier_v",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if-expr 'sha256(city) > "8"'`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 7, "city": "Mumbai", "pop": 20},
			{"id": 9, "city": "Lima", "pop": 7},
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 8, "city": "Lagos", "pop": 14},
			{"id": 10, "city": "Quito", "pop": 2},
		},
		Skip: map[string]string{
			"duckdb": "sha256 has no exprToSQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// Tier V -set-expr on an EXISTING string column: VM eval + runtime
		// MustCoerceString typing, still inside the typed StreamSelect.
		Name:     "update_set_expr_tier_v",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update -if pop gt 25 -set-expr city 'upper(sha256(city))'`,
		Ordered:  false,
		Skip: map[string]string{
			"duckdb": "sha256 has no exprToSQL translation (generate sql fails loudly by design)",
		},
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
		// go-typed/go-parallel unskipped in Phase 1 of the expr transpiler:
		// -set-expr now transpiles to a native typed assignment.
	},
	{
		// The transpiler's headline semantic in a full pipeline: expr-lang
		// division is ALWAYS float64 (pop/2 of 31 is 15.5, not 15), and the
		// NEW field's type comes from the expression's inferred type. Runs
		// native in typed/parallel lanes as of Phase 1.
		Name:     "update_set_expr_division",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update -set-expr half 'pop / 2'`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 7, "city": "Mumbai", "pop": 20, "half": 10},
			{"id": 3, "city": "Cairo", "pop": 10, "half": 5},
			{"id": 9, "city": "Lima", "pop": 7, "half": 3.5},
			{"id": 1, "city": "Oslo", "pop": 31, "half": 15.5},
			{"id": 5, "city": "Tokyo", "pop": 37, "half": 18.5},
			{"id": 2, "city": "Delhi", "pop": 29, "half": 14.5},
			{"id": 8, "city": "Lagos", "pop": 14, "half": 7},
			{"id": 4, "city": "Paris", "pop": 11, "half": 5.5},
			{"id": 6, "city": "Nairobi", "pop": 4, "half": 2},
			{"id": 10, "city": "Quito", "pop": 2, "half": 1},
			{"id": 12, "city": "Hanoi", "pop": 9, "half": 4.5},
			{"id": 11, "city": "Bogota", "pop": 25, "half": 12.5},
		},
	},
	{
		// Ternary with same-type branches transpiles natively; exercises the
		// generated func(){}() closure through every lane.
		Name:     "update_set_expr_ternary",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update -set-expr size 'pop > 15 ? "big" : "small"'`,
		Ordered:  false,
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
	{
		// A stage arriving "out of SQL clause order": limit BEFORE group-by.
		// The pre-v4.56 assembler flattened everything into one SELECT, so
		// the SQL grouped ALL rows and limited the GROUPS — a silently
		// different pipeline. The sort makes "first 5" deterministic in
		// every lane.
		Name:     "limit_then_group",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} sort city | {{.bin}} limit 5 | {{.bin}} group-by city -count cnt`,
		Ordered:  false,
		Golden: []map[string]any{
			{"city": "Bogota", "cnt": 1},
			{"city": "Cairo", "cnt": 1},
			{"city": "Delhi", "cnt": 1},
			{"city": "Hanoi", "cnt": 1},
			{"city": "Lagos", "cnt": 1},
		},
	},
	{
		// Unconditional -set emitted `CASE ELSE 3 END` (no WHEN — a SQL
		// syntax error), and update-then-group needs a subquery wrap.
		Name:     "update_unconditional_then_group",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update -set pop 3 | {{.bin}} group-by pop -count cnt`,
		Ordered:  false,
		Golden: []map[string]any{
			{"pop": 3, "cnt": 12},
		},
	},
	{
		// Self-union without -all must dedup back to the original rows.
		// Broken before v4.56: the exec dedup key was fmt.Sprintf("%v", r),
		// which embeds the schema POINTER — records from different sources
		// never matched, so `union` returned everything (24 rows, not 12).
		Name:     "union_dedup",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} union -file <({{.bin}} from csv {{.data}}/shuffled.csv)`,
		Ordered:  false,
	},
	{
		// update -set on a NEW column: exec creates the field; SQL needs
		// `SELECT *, 3 AS x` (REPLACE would be a binder error). Decided via
		// header-seeded column tracking in the assembler.
		Name:     "update_new_column",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} update -set x 3 | {{.bin}} where -if pop gt 25`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 5, "city": "Tokyo", "pop": 37, "x": 3},
			{"id": 1, "city": "Oslo", "pop": 31, "x": 3},
			{"id": 2, "city": "Delhi", "pop": 29, "x": 3},
		},
	},
	{
		// distinct rendered as a fake select column (`SELECT DISTINCT, col`
		// / bare `SELECT DISTINCT`) before v4.56.
		Name:     "include_distinct",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} include dept | {{.bin}} distinct`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering"},
			{"dept": "Marketing"},
			{"dept": "Sales"},
		},
	},
}
