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
// corpusEmptiesCSV: one all-empty row (DFC124). An empty numeric or
// boolean cell is ABSENT in the record lanes and NULL in DuckDB; an
// empty text cell stays "" (commands treat it as missing).
const corpusEmptiesCSV = `id,n,f,s,b
1,10,1.5,x,true
2,,,,
3,30,3.5,z,false
4,40,4.5,w,true
`

// corpusAppLog: a text log with one line that does not match the
// timestamp/level/message shape (exercises extract -skip).
const corpusAppLog = `2026-01-01T00:00:01Z INFO started
2026-01-01T00:00:02Z WARN disk 91%
garbage
2026-01-01T00:00:03Z INFO done
2026-01-01T00:00:04Z ERROR failed: timeout
`

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
		// ssql's record model does not distinguish "absent" from
		// "null" (GetOr yields the default for both), and SQL can only
		// express absence as NULL. Drop null-valued keys so a lane that
		// omits a field and a lane that emits null compare equal —
		// a representation difference, not a value difference.
		for k, v := range m {
			if v == nil {
				delete(m, k)
			}
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

// TestFlagExprMetamorphic is convergence Phase A
// (doc/research/flag-expr-convergence.md): for every flag operator, the flag
// form and its expression equivalent must produce IDENTICAL output — in every
// lane. `FIELD OP VALUE` is lowered independently in five places (exec
// applyOperator, record generateCondition, update's generateConditionCode,
// typed typedWhereCondition, SQL translateWhere) while the expression form
// goes through the transpiler; this gate pins the two surfaces to one
// semantics before any lowering is converged.
//
// Mechanics: each pipeline must be internally lane-consistent
// (runEquivCase), and the two exec outputs must agree (the metamorphic
// assertion) — together that pins all lanes of both pipelines to each other.
func TestFlagExprMetamorphic(t *testing.T) {
	if testing.Short() {
		t.Skip("equivalence tests are slow (each lane compiles + runs)")
	}
	bin := corpusBin(t)
	data := corpusData(t)

	pairs := []struct {
		name     string
		flag     string            // stage in flag syntax
		expr     string            // the same stage in expression syntax
		skipFlag map[string]string // lanes the FLAG form cannot run (known capability gaps)
	}{
		{name: "eq_int", flag: `where -if pop eq 20`, expr: `where -if-expr 'pop == 20'`},
		{name: "eq_string", flag: `where -if city eq Oslo`, expr: `where -if-expr 'city == "Oslo"'`},
		{name: "ne_string", flag: `where -if city ne Oslo`, expr: `where -if-expr 'city != "Oslo"'`},
		{name: "gt_int", flag: `where -if pop gt 15`, expr: `where -if-expr 'pop > 15'`},
		{name: "ge_int", flag: `where -if pop ge 14`, expr: `where -if-expr 'pop >= 14'`},
		{name: "lt_int", flag: `where -if pop lt 10`, expr: `where -if-expr 'pop < 10'`},
		{name: "le_int", flag: `where -if pop le 10`, expr: `where -if-expr 'pop <= 10'`},
		{
			// Lexicographic string ordering — exec's compareGreater does it;
			// every codegen backend must agree.
			name: "gt_string", flag: `where -if city gt Lima`, expr: `where -if-expr 'city > "Lima"'`,
		},
		{name: "contains", flag: `where -if city contains an`, expr: `where -if-expr 'city contains "an"'`},
		{name: "startswith", flag: `where -if city startswith L`, expr: `where -if-expr 'city startsWith "L"'`},
		{name: "endswith", flag: `where -if city endswith o`, expr: `where -if-expr 'city endsWith "o"'`},
		{
			// Phase B unlock (C.7): `-if … regex` was a Tier-3 error in typed
			// codegen; the shared lowering gives it the hoisted-pattern
			// emission the expression form always had — all lanes run.
			name: "regex", flag: `where -if city regex ^[A-M]`, expr: `where -if-expr 'city matches "^[A-M]"'`,
		},
		{name: "negated_if", flag: `where +if pop gt 15`, expr: `where +if-expr 'pop > 15'`},
		{name: "negated_string_op", flag: `where +if city contains an`, expr: `where +if-expr 'city contains "an"'`},
		{name: "and_conditions", flag: `where -if pop gt 5 -if city ne Oslo`, expr: `where -if-expr 'pop > 5 && city != "Oslo"'`},
		{name: "or_clauses", flag: `where -if pop gt 25 + -if city eq Lima`, expr: `where -if-expr 'pop > 25 || city == "Lima"'`},
		// Update pairs -set an EXISTING field: a conditional -set on a NEW
		// field has no SQL translation (loud by design), which would knock
		// out the duckdb lane for both forms.
		{name: "update_if", flag: `update -if pop gt 15 -set city big`, expr: `update -if-expr 'pop > 15' -set city big`},
		{name: "update_negated", flag: `update +if pop gt 15 -set city small`, expr: `update +if-expr 'pop > 15' -set city small`},
		{
			// String ordering through update's OWN condition emission
			// (generateConditionCode had the same unconditional-numeric bug
			// as where's — worse: float64(0) > "Lima" didn't compile).
			name: "update_string_gt", flag: `update -if city gt Lima -set pop 0`, expr: `update -if-expr 'city > "Lima"' -set pop 0`,
		},
	}

	for _, p := range pairs {
		p := p
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			pipeFlag := bin + " from csv " + data + "/shuffled.csv | " + bin + " " + p.flag
			pipeExpr := bin + " from csv " + data + "/shuffled.csv | " + bin + " " + p.expr

			// Each pipeline internally lane-consistent.
			runEquivCase(t, bin, pipeFlag, EquivCase{Name: p.name + "_flag", Skip: p.skipFlag})
			runEquivCase(t, bin, pipeExpr, EquivCase{Name: p.name + "_expr"})

			// The metamorphic assertion: exec(flag) == exec(expr).
			flagOut := equivCanon(equivParse(t, "exec-flag",
				equivShell(t, "exec-flag", pipeFlag+" | "+bin+" to jsonl")), false)
			exprOut := equivCanon(equivParse(t, "exec-expr",
				equivShell(t, "exec-expr", pipeExpr+" | "+bin+" to jsonl")), false)
			if !slices.Equal(flagOut, exprOut) {
				t.Errorf("flag form and expression form disagree in exec:\n  %s → %s\n  %s → %s",
					p.flag, strings.Join(flagOut, " "),
					p.expr, strings.Join(exprOut, " "))
			}
		})
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

	stages := permStages()
	for i, a := range stages {
		for j, b := range stages {
			if i == j || permOrderHazard([]string{a.key, b.key}) {
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

// permStages is the stage set shared by the pair and triple permutation
// gates. Every stage references only `pop` — the one field every other
// stage's output retains (group-by pop -count drops city).
func permStages() []struct{ key, cmd string } {
	return []struct{ key, cmd string }{
		{"where", `{{.bin}} where -if pop gt 5`},
		// Typed lanes run this NATIVE as of expr-transpiler Phase 1.
		{"whereexpr", `{{.bin}} where -if-expr 'pop > 5 && pop != 9'`},
		{"sort", `{{.bin}} sort pop -desc`},
		{"limit", `{{.bin}} limit 5`},
		{"group", `{{.bin}} group-by pop -count cnt`},
		{"distinct", `{{.bin}} distinct`},
		// top SORTS its output, so it stays deterministic even downstream
		// of group-by's unspecified emission order.
		{"top", `{{.bin}} top 3 -field pop`},
		// update with a -set-expr derived UNIQUELY from pop: no ties for a
		// downstream sort/limit/top, and it puts the expr transpiler's
		// assignment path (native in typed lanes, advisory-native or VM in
		// record) into every combination.
		{"update", `{{.bin}} update -set-expr popx 'pop * 2 + 1'`},
	}
}

// permOrderHazard reports whether a stage sequence is INHERENTLY
// nondeterministic — not a translation bug, so the lanes have nothing to
// agree on. The one hazard: group-by's emission order is unspecified, so a
// positional `limit` downstream of `group` is "first N of an unspecified
// order" — unless an order-restoring stage (sort, or top, which sorts by
// value) intervenes. Every other stage is either order-insensitive under
// the multiset comparison or selects by VALUE rather than position.
func permOrderHazard(keys []string) bool {
	unordered := false
	for _, k := range keys {
		switch k {
		case "group":
			unordered = true
		case "sort", "top":
			unordered = false
		case "limit":
			if unordered {
				return true
			}
		}
	}
	return false
}

// TestPermOrderHazard pins the exclusion rule — excluding too much would
// silently shrink coverage; too little makes the gate flaky.
func TestPermOrderHazard(t *testing.T) {
	cases := []struct {
		seq  []string
		want bool
	}{
		{[]string{"group", "limit"}, true},
		{[]string{"limit", "group"}, false},
		{[]string{"group", "where", "limit"}, true},   // where preserves the unspecified order
		{[]string{"group", "sort", "limit"}, false},   // sort restores order
		{[]string{"group", "top", "limit"}, false},    // top sorts by value
		{[]string{"group", "update", "limit"}, true},  // update preserves order
		{[]string{"where", "group", "distinct"}, false},
		{[]string{"sort", "group", "limit"}, true}, // sort BEFORE group doesn't help
	}
	for _, c := range cases {
		if got := permOrderHazard(c.seq); got != c.want {
			t.Errorf("permOrderHazard(%v) = %v, want %v", c.seq, got, c.want)
		}
	}
}

// TestPipelinePermutationTriples is the opt-in SLOW gate: every ordered
// TRIPLE of the permutation stage set (8·7·6 = 336 pipelines minus order
// hazards), each through every lane. Wall-clock is several minutes even
// parallelized, so it runs only with SSQL_PERM_TRIPLES=1 — intended for
// pre-release checklists and after codegen-touching changes, not every
// test invocation. Rationale: each historical stage-ordering bug was found
// exactly when this family was widened; triples cover wrap-of-wrap
// interactions pairs cannot.
func TestPipelinePermutationTriples(t *testing.T) {
	if testing.Short() {
		t.Skip("permutation triples are slow")
	}
	if os.Getenv("SSQL_PERM_TRIPLES") == "" {
		t.Skip("set SSQL_PERM_TRIPLES=1 to run the 3-stage permutation gate (several minutes)")
	}
	bin := corpusBin(t)
	data := corpusData(t)
	repl := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data)

	stages := permStages()
	for i, a := range stages {
		for j, b := range stages {
			for k, c := range stages {
				if i == j || j == k || i == k {
					continue
				}
				if permOrderHazard([]string{a.key, b.key, c.key}) {
					continue
				}
				ec := EquivCase{
					Name: "perm3_" + a.key + "_" + b.key + "_" + c.key,
					Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} sort id | ` +
						a.cmd + ` | ` + b.cmd + ` | ` + c.cmd,
					Ordered: false,
				}
				t.Run(ec.Name, func(t *testing.T) {
					t.Parallel()
					runEquivCase(t, bin, repl.Replace(ec.Pipeline), ec)
				})
			}
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
		// DFC110: a SEEDED sample must select the identical row set in
		// every Go lane — selection is a pure function of (seed, row
		// index) via the spec-stable RNG in the ssql package. The
		// duckdb lane is skipped by design: DuckDB's RNG cannot
		// reproduce ssql's seeded selection (generate sql refuses
		// -seed loudly); an unseeded statistical translation is
		// covered by TestTranslateSampleSQL.
		Name: "sample_seeded",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} sample 7 -seed 42 | {{.bin}} sort city`,
		Ordered: true,
		Skip:    map[string]string{"duckdb": "seeded sampling has no cross-engine deterministic equivalent (DFC110)"},
	},
	{
		// sample 0 = pass-through dial (the limit-0 convention): the
		// stage must vanish identically everywhere, duckdb included.
		Name: "sample_zero_passthrough",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} sample 0 | {{.bin}} sort city`,
		Ordered: true,
	},
	{
		// from -sample (byte-offset, DFC110 amendment): seeded selection
		// must be byte-identical across the Go lanes — exec and codegen
		// share ssql.SampleCSVFile. duckdb: seeded refusal by design.
		Name: "from_sample_seeded",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv -sample 5 -sample-seed 7 | ` +
			`{{.bin}} sort city`,
		Ordered: true,
		Skip:    map[string]string{"duckdb": "seeded sampling has no cross-engine deterministic equivalent (DFC110)"},
	},
	{
		// Agg-less group-by (DISTINCT semantics) — the parallel lane
		// now runs typed.DistinctParallel instead of Serial()+Distinct
		// (6.7s → 1.5s on 14.6M parquet rows); the distinct SET must be
		// identical in every lane.
		Name:     "groupby_no_aggs_distinct",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by pop | {{.bin}} sort pop`,
		Ordered:  true,
	},
	{
		// resample (DFC121): every Go lane calls the ONE
		// ssql.ResampleRecords (typed shims records at the barrier),
		// so agreement is by construction — this gate proves it STAYS
		// that way. pop stands in as epoch seconds; the fixture has
		// duplicate pops, exercising the order-independent dup rule.
		Name: "resample_previous",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} resample -time pop -every 5s -value id`,
		Ordered: true,
	},
	{
		Name: "resample_linear",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} resample -time pop -every 5s -value id -fill linear -time-unit s`,
		Ordered: true,
	},
	{
		// Dead-sort elimination (DFC123 §7): the generate-ssql lane
		// optimises the first sort away; every lane must still produce
		// the identical ordered output (ids are unique → the second
		// sort is a total order, so removal is exact).
		Name: "dead_sort_across_where",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} sort -desc pop | {{.bin}} where -if id gt 2 | {{.bin}} sort id`,
		Ordered: true,
	},
	{
		// The LIVENESS pin — the miscompile shape. limit consumes the
		// first sort's order (it selects WHICH three rows), so the
		// optimiser must keep it; if the rule ever fires here, the
		// generate-ssql lane selects different rows and this case
		// fails. (Optimises to `top 3 -field pop | sort id` — same
		// selection, pops are unique.)
		Name: "live_sort_limit_sort_desc",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} sort -desc pop | {{.bin}} limit 3 | {{.bin}} sort id`,
		Ordered: true,
	},
	{
		// Same liveness shape, ASCENDING — this variant is the one
		// that actually reaches ruleSortElimination's classification:
		// the desc case above is rewritten to `top` by
		// ruleSortLimitToTop before the dead-sort rule runs, so it
		// pins rule COMPOSITION but not the limit-consumes-order
		// fact. (Found by sabotage: adding limit to orderReset passed
		// the desc case and fails this one.)
		Name: "live_sort_limit_sort_asc",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} sort pop | {{.bin}} limit 3 | {{.bin}} sort id`,
		Ordered: true,
	},
	{
		// limit -last (DFC122 Tier 1, kept under the SQL verb): the last
		// N in arrival order. SQL has no arrival order → the duckdb lane
		// refuses loudly (Skip records the contract); every Go lane must
		// agree byte-for-byte.
		Name:     "limit_last_unsorted",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} limit -last 3`,
		Ordered:  true,
		Skip:     map[string]string{"duckdb": "limit -last without a preceding sort has no SQL translation (arrival order undefined) — refuses loudly by design"},
	},
	{
		// With a sort in front, SQL translates: take N under the
		// REVERSED order, restore the original order outside. All
		// lanes incl. duckdb.
		Name:     "limit_last_sorted",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} sort pop | {{.bin}} limit -last 3`,
		Ordered:  true,
	},
	{
		// The optimiser pin: `sort -desc x | limit -last N` is the N
		// SMALLEST in descending order — NOT `top N -field x`. The
		// ssql-opt lane diverges if sort-limit-to-top ever fires on
		// -last; the duckdb lane pins the ASC/DESC reversal.
		Name:     "limit_last_desc_is_not_top",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} sort -desc pop | {{.bin}} limit -last 3`,
		Ordered:  true,
	},
	{
		// describe (DFC122 Tier 1): one row per field, exact stats,
		// numeric stats absent on string fields. Every lane incl. the
		// DuckDB translation (type names mapped to ssql's vocabulary,
		// median = quantile_cont). Ordered: rows follow field order.
		Name:     "describe_all",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} describe`,
		Ordered:  true,
	},
	{
		Name:     "describe_fields_after_where",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 8 | {{.bin}} describe pop city`,
		Ordered:  true,
	},
	{
		// unpivot (DFC122 Tier 1): wide→long, homogeneous int value
		// columns so every lane incl. DuckDB's native UNPIVOT agrees.
		// Unordered: row-local expansion, parallel lanes reorder.
		Name:     "unpivot_ints",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} unpivot -id city -value id -value pop -col k -val v`,
		Ordered:  false,
	},
	{
		// Default value list (all non-id columns, sorted) + custom
		// column names, after a filter.
		Name:     "unpivot_default_values",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 20 | {{.bin}} exclude city | {{.bin}} unpivot -id id`,
		Ordered:  false,
	},
	{
		// DFC124: empties are absent → describe's missing/mean agree with
		// the DuckDB oracle. Typed lanes skipped: the typed reader still
		// writes zero values (DFC124 §3 — the recorded next step).
		Name:     "empties_describe",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} describe`,
		Ordered:  true,
		Skip:     map[string]string{"go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		Name:     "empties_unpivot",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} unpivot -id id -value n -value f`,
		Ordered:  false,
		Skip:     map[string]string{"go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		Name:     "empties_where_numeric",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} where -if n gt 5 | {{.bin}} include id n`,
		Ordered:  false,
		Skip:     map[string]string{"go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		// fill -default over the empties fixture: 99 is discriminating
		// (the typed reader's zero-for-empty would NOT be defaulted, so
		// typed lanes are skipped by name — DFC124 §3).
		Name:     "fill_default",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} fill -default n 99 -default s unknown | {{.bin}} include id n s`,
		Ordered:  false,
		Skip:     map[string]string{"go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		// fill -down needs an order: sorted, so the DuckDB LAST_VALUE
		// window has its ORDER BY; leading gap on row 1 gets the default.
		Name:     "fill_down_sorted",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} sort id | {{.bin}} fill -down n -down f -default f 0 | {{.bin}} include id n f`,
		Ordered:  true,
		Skip:     map[string]string{"go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		// Unsorted -down: the SQL lane refuses loudly (Skip records the
		// contract); the Go lanes agree on arrival order.
		Name:     "fill_down_unsorted",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} fill -down n | {{.bin}} include id n`,
		Ordered:  true,
		Skip:     map[string]string{"duckdb": "fill -down without a preceding sort has no SQL translation (carry order undefined) — refuses loudly by design", "go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		// from lines: 1-based line_number + line, in file order.
		Name:     "lines_identity",
		Pipeline: `{{.bin}} from lines {{.data}}/app.log`,
		Ordered:  true,
	},
	{
		// extract -skip: named groups → string fields, source field
		// dropped, non-matching line gone — every lane incl. DuckDB's
		// regexp_extract + regexp_matches.
		Name:     "extract_log_skip",
		Pipeline: `{{.bin}} from lines {{.data}}/app.log | {{.bin}} extract -field line -re '^(?P<ts>\S+) (?P<lvl>\w+) (?P<msg>.*)$' -skip`,
		Ordered:  true,
	},
	{
		Name:     "extract_keep_then_groupby",
		Pipeline: `{{.bin}} from lines {{.data}}/app.log | {{.bin}} extract -field line -re '^(?P<ts>\S+) (?P<lvl>\w+) ' -skip -keep | {{.bin}} group-by lvl -count n`,
		Ordered:  false,
	},
	{
		// extract WITHOUT -skip has no SQL translation (SQL cannot fail
		// per row); the Skip records the contract. Only matching lines
		// here so the Go lanes don't fail loudly.
		Name:     "extract_no_skip_all_match",
		Pipeline: `{{.bin}} from lines {{.data}}/app.log | {{.bin}} where -if line ne 'garbage' | {{.bin}} extract -field line -re '^(?P<ts>\S+) (?P<lvl>\w+) (?P<msg>.*)$'`,
		Ordered:  true,
		Skip:     map[string]string{"duckdb": "extract without -skip refuses SQL translation by design (SQL cannot fail on a non-matching row)"},
	},
	{
		// from -last N: the seek-based tail at the source. Same rows as
		// `| limit -last N`; every lane incl. DuckDB (ordered full read
		// + reversed LIMIT — correct, not fast).
		Name:     "from_last",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv -last 3`,
		Ordered:  true,
	},
	{
		Name:     "from_last_then_where",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv -last 5 | {{.bin}} where -if pop gt 5`,
		Ordered:  true,
	},
	{
		// cast in EVERY lane — exec included. cast had only ever been
		// exercised through codegen (the corpus), so its exec path read
		// stdin with the schema-unaware reader, saw `_schema` as a
		// record, and errored "unknown field" while passing rows through
		// unchanged. Found by the codelab runner (DFC125).
		Name:     "cast_float_then_filter",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} cast -type pop float | {{.bin}} where -if pop gt 10 | {{.bin}} include id pop`,
		Ordered:  false,
	},
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
		// -cube (and -rollup) were REJECTED outright by typed-mode codegen.
		// They now eject to the record ssql.Rollup path via the Phase B
		// typed→Record boundary — typed/parallel lanes must produce the
		// exact enrichment exec does. Golden hand-checked: Widget/US
		// appears twice (100+50), so a wrong grouping diverges.
		Name: "groupby_cube_typed_eject",
		Pipeline: `{{.bin}} from csv {{.data}}/sales.csv | ` +
			`{{.bin}} group-by product region -count c -sum amount total -cube`,
		Ordered: false,
		Golden: []map[string]any{
			{"product": "Widget", "region": "US", "c": 5, "total": 580, "product_c": 3, "product_total": 300, "region_c": 3, "region_total": 350, "product_region_c": 2, "product_region_total": 150},
			{"product": "Widget", "region": "EU", "c": 5, "total": 580, "product_c": 3, "product_total": 300, "region_c": 2, "region_total": 230, "product_region_c": 1, "product_region_total": 150},
			{"product": "Gadget", "region": "US", "c": 5, "total": 580, "product_c": 2, "product_total": 280, "region_c": 3, "region_total": 350, "product_region_c": 1, "product_region_total": 200},
			{"product": "Gadget", "region": "EU", "c": 5, "total": 580, "product_c": 2, "product_total": 280, "region_c": 2, "region_total": 230, "product_region_c": 1, "product_region_total": 80},
		},
		Skip: map[string]string{
			"duckdb": "generate sql refuses -cube (GROUP BY CUBE translation not implemented; loud error)",
		},
	},
	{
		// Direct-file join (`join FILE.csv`, extension-inferred like
		// `from FILE`) must agree with the procsub form in every lane —
		// the shared Golden on BOTH cases pins direct ≡ procsub.
		Name: "join_direct_csv",
		Pipeline: `{{.bin}} from csv {{.data}}/orders.csv | ` +
			`{{.bin}} join {{.data}}/customers.csv -using customer_id | ` +
			`{{.bin}} include order_id product country tier`,
		Ordered: false,
		Golden: []map[string]any{
			{"order_id": 1, "product": "Widget", "country": "US", "tier": "gold"},
			{"order_id": 2, "product": "Gadget", "country": "US", "tier": "bronze"},
			{"order_id": 3, "product": "Doohickey", "country": "FR", "tier": "silver"},
			{"order_id": 4, "product": "Widget", "country": "UK", "tier": "silver"},
		},
	},
	{
		Name: "join_procsub_csv",
		Pipeline: `{{.bin}} from csv {{.data}}/orders.csv | ` +
			`{{.bin}} join <({{.bin}} from csv {{.data}}/customers.csv) -using customer_id | ` +
			`{{.bin}} include order_id product country tier`,
		Ordered: false,
		Golden: []map[string]any{
			{"order_id": 1, "product": "Widget", "country": "US", "tier": "gold"},
			{"order_id": 2, "product": "Gadget", "country": "US", "tier": "bronze"},
			{"order_id": 3, "product": "Doohickey", "country": "FR", "tier": "silver"},
			{"order_id": 4, "product": "Widget", "country": "UK", "tier": "silver"},
		},
	},
	{
		// `limit 0` / `offset 0` are pass-throughs (a limit stage you can
		// dial to 0 for full runs) and MUST vanish from generated
		// go/sql/ssql — every lane must return exactly the where result.
		Name:     "limit_zero_passthrough",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 15 | {{.bin}} offset 0 | {{.bin}} limit 0`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 7, "city": "Mumbai", "pop": 20},
			{"id": 1, "city": "Oslo", "pop": 31},
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 2, "city": "Delhi", "pop": 29},
			{"id": 11, "city": "Bogota", "pop": 25},
		},
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
		// -expr aggregation as a native MERGEABLE accumulator
		// (expr-transpiler Phase 3): avg desugars via the patcher normal
		// form to sum/len. Golden = 199/12. duckdb skipped: generate sql
		// rejects -expr loudly (v4.56.0 behaviour, by design).
		Name:     "groupby_expr_avg",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by -expr 'avg(pop)' ap`,
		Ordered:  false,
		Golden: []map[string]any{
			{"ap": 16.583333333333332},
		},
		Skip: map[string]string{
			"duckdb": "-expr has no SQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// Arithmetic over aggregation terms: sum(pop*2)/count() — the
		// int/int division must be float64 in the OUTER expression too.
		// Golden = 398/12.
		Name:     "groupby_expr_arith",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by -expr 'sum(pop * 2) / count()' v`,
		Ordered:  false,
		Golden: []map[string]any{
			{"v": 33.166666666666664},
		},
		Skip: map[string]string{
			"duckdb": "-expr has no SQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// Int-accumulator fidelity: sum of ints is an INT in the VM, so
		// `% 5` is integer modulo (199 % 5 = 4). A blanket float64
		// accumulator would refuse % and silently fall back — this case
		// keeps the native path honest about integer semantics.
		Name:     "groupby_expr_int_mod",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by -expr 'sum(pop) % 5' m`,
		Ordered:  false,
		Golden: []map[string]any{
			{"m": 4},
		},
		Skip: map[string]string{
			"duckdb": "-expr has no SQL translation (generate sql fails loudly by design)",
		},
	},
	{
		// Grouped + mixed with a built-in aggregation in one aggregator;
		// exercises the Merge path in the go-parallel lane.
		Name:     "groupby_expr_grouped",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} group-by city -count c -expr 'sum(pop * pop)' sq`,
		Ordered:  false,
		Skip: map[string]string{
			"duckdb": "-expr has no SQL translation (generate sql fails loudly by design)",
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
		// Expression canonicalization (convergence Phase C): the ssql-opt
		// lane rewrites this -if-expr into structured -if conditions and
		// then range-tightens them (`pop > 9 && pop > 5` → `-if pop gt 9`)
		// — the optimized pipeline must still agree with every other lane.
		// The binding term sits ON a fixture boundary (Hanoi pop=9) so a
		// wrong operator mapping (gt↔ge) actually diverges; the negated
		// single-term (+if-expr → +if) canonicalizes too. Golden = pop > 9
		// AND NOT city == "Oslo".
		Name:     "where_expr_canonicalized",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if-expr 'pop > 9 && pop > 5' +if-expr 'city == "Oslo"'`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 7, "city": "Mumbai", "pop": 20},
			{"id": 3, "city": "Cairo", "pop": 10},
			{"id": 5, "city": "Tokyo", "pop": 37},
			{"id": 2, "city": "Delhi", "pop": 29},
			{"id": 8, "city": "Lagos", "pop": 14},
			{"id": 4, "city": "Paris", "pop": 11},
			{"id": 11, "city": "Bogota", "pop": 25},
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
