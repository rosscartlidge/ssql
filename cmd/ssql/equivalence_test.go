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
	"time"

	"github.com/rosscartlidge/ssql/v4"
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

// corpusIntFirstCSV: the first data row is all ints, later rows are
// floats (DFC124 §3). First-row inference typed v as int and truncated
// every later value to 0; the sample must type it float in every lane.
// Shuffled so a wrong selection diverges.
const corpusIntFirstCSV = `t,v
0,0
3,2
1,.5
4,.25
2,1.5
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

	// ColumnsUnordered opts a case out of the column-order comparison, for
	// the shapes where a lane's order is legitimately its own (say why).
	ColumnsUnordered bool
}

// equivCommon is a filtered to the names also in b, keeping a's order.
func equivCommon(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, n := range b {
		in[n] = true
	}
	var out []string
	for _, n := range a {
		if in[n] {
			out = append(out, n)
		}
	}
	return out
}

// equivColumnOrder is the positional union of a lane's JSONL key order
// (the writers' rule: a key first seen in a later row goes after the key
// that precedes it there), for comparing column order across lanes.
func equivColumnOrder(raw string) []string {
	var fields []string
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") || strings.HasPrefix(line, `{"_schema"`) {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		depth, prev := 0, ""
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch v := tok.(type) {
			case json.Delim:
				switch v {
				case '{', '[':
					depth++
				case '}', ']':
					depth--
				}
			case string:
				if depth != 1 {
					continue
				}
				// A key at depth 1 is followed by its value; skip the value.
				key := v
				var val any
				if err := dec.Decode(&val); err != nil {
					break
				}
				if !seen[key] {
					seen[key] = true
					placed := false
					if prev != "" {
						for i, f := range fields {
							if f == prev {
								fields = slices.Insert(fields, i+1, key)
								placed = true
								break
							}
						}
					}
					if !placed {
						fields = append(fields, key)
					}
				}
				prev = key
			}
		}
	}
	return fields
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
			// DuckDB 1.5.0 prints the malformed `[{]` for a result with no
			// rows (LIMIT 0, USING SAMPLE 0 ROWS): zero records.
			if raw == "" || raw == "[]" || raw == "[{]" {
				return ""
			}
			// One JSONL line per row, VERBATIM: DuckDB's key order is its
			// SELECT order, which the column-order comparison needs (a
			// round trip through Go maps sorted it). The HUGEINT-as-string
			// normalisation is applied to the parsed rows in runEquivCase.
			var rows []json.RawMessage
			if err := json.Unmarshal([]byte(raw), &rows); err != nil {
				t.Fatalf("lane %q: bad duckdb -json output: %v\n%s", "duckdb", err, raw)
			}
			var sb strings.Builder
			for _, r := range rows {
				sb.Write(r)
				sb.WriteByte('\n')
			}
			return sb.String()
		}})
	}
	// Dialect oracle lanes (DFC132 §4): the same generated SQL rendered
	// for another engine and executed by it. Opt-in like the SSH rig —
	// DataFusion needs a Python with the `datafusion` package
	// (SSQL_DATAFUSION_PYTHON=/path/to/python), Postgres the LXD rig
	// (SSQL_TEST_PG_HOST=ssql-node1, psql reached over ssh as the postgres
	// superuser; see doc/research/ssh-test-environment.md §PostgreSQL).
	// A stage the dialect refuses by design ("has no X translation") skips
	// the lane for that case with a log line rather than failing it.
	if py := os.Getenv("SSQL_DATAFUSION_PYTHON"); py != "" {
		lanes = append(lanes, equivLane{"datafusion", func(t *testing.T, bin, pipeline string) string {
			sql, skip := equivDialectSQL(t, "datafusion", bin, pipeline)
			if skip != "" {
				return skip
			}
			script := filepath.Join(t.TempDir(), "run.py")
			if err := os.WriteFile(script, []byte(datafusionRunner), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(py, script)
			cmd.Stdin = strings.NewReader(sql)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("lane %q: datafusion failed: %v\n  sql:\n%s\n  stderr:\n%s",
					"datafusion", err, sql, stderr.String())
			}
			return stdout.String()
		}})
	}
	if host := os.Getenv("SSQL_TEST_PG_HOST"); host != "" {
		lanes = append(lanes, equivLane{"postgres", func(t *testing.T, bin, pipeline string) string {
			sql, skip := equivDialectSQL(t, "postgres", bin, pipeline)
			if skip != "" {
				return skip
			}
			script := pgOracleScript(t, sql)
			cmd := exec.Command("ssh", host, "sudo", "-u", "postgres", "psql", "-X", "-At", "-v", "ON_ERROR_STOP=1", "-d", "ssql")
			cmd.Stdin = strings.NewReader(script)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("lane %q: psql failed: %v\n  sql:\n%s\n  stderr:\n%s",
					"postgres", err, sql, stderr.String())
			}
			return stdout.String()
		}})
	}
	return lanes
}

// equivSkipPrefix marks a lane result that means "this dialect refused the
// pipeline by design"; runEquivCase logs and drops the lane for the case.
const equivSkipPrefix = "\x00skip: "

// equivSQLLanes are the lanes that run `generate sql` output; a case's
// duckdb skip reason (a stage with no SQL translation at all) applies to
// every one of them.
var equivSQLLanes = map[string]bool{"duckdb": true, "datafusion": true, "postgres": true}

// equivDialectSQL translates the pipeline for a dialect. A by-design
// refusal (dialectRefuse's "has no <dialect> translation") returns a skip
// marker; any other failure is the lane's failure.
func equivDialectSQL(t *testing.T, dialect, bin, pipeline string) (sql, skip string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "export SSQL_MODE=record && "+pipeline+" | "+bin+" to jsonl | "+bin+" generate sql -dialect "+dialect)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "has no "+dialect+" translation") {
			return "", equivSkipPrefix + strings.TrimSpace(stderr.String())
		}
		t.Fatalf("lane %q failed to generate:\n  pipeline: %s\n  err: %v\n  stderr:\n%s", dialect, pipeline, err, stderr.String())
	}
	return stdout.String(), ""
}

// datafusionRunner executes the SQL on stdin with the DataFusion Python
// bindings and prints one JSON object per row (dates and other non-JSON
// values as strings, the way ssql reads them from CSV).
const datafusionRunner = `import json, sys
from datafusion import SessionContext, SessionConfig
ctx = SessionContext(SessionConfig().set("datafusion.catalog.has_header", "true")).enable_url_table()
sql = sys.stdin.read().strip().rstrip(";")
for row in ctx.sql(sql).to_pylist():
    print(json.dumps(row, default=str))
`

// pgOracleScript turns generate sql's Postgres output into one psql
// session: the prologue's CREATE TABLE as TEMP tables (session-scoped, so
// parallel cases cannot collide and nothing is left behind), each \copy
// fed from STDIN with the local file's bytes (the rig has no copy of the
// fixtures), then the statement wrapped in row_to_json so the result is
// JSONL like every other lane.
func pgOracleScript(t *testing.T, sql string) string {
	t.Helper()
	var script, stmt strings.Builder
	for _, ln := range strings.Split(sql, "\n") {
		switch {
		case strings.HasPrefix(ln, "--   CREATE TABLE IF NOT EXISTS "):
			script.WriteString("CREATE TEMP TABLE " + strings.TrimPrefix(ln, "--   CREATE TABLE IF NOT EXISTS ") + "\n")
		case strings.HasPrefix(ln, "--   \\copy "):
			m := pgCopyRe.FindStringSubmatch(ln)
			if m == nil {
				t.Fatalf("lane %q: unparsable \\copy line %q", "postgres", ln)
			}
			data, err := os.ReadFile(strings.ReplaceAll(m[2], "''", "'"))
			if err != nil {
				t.Fatalf("lane %q: reading fixture: %v", "postgres", err)
			}
			script.WriteString("\\copy " + m[1] + " FROM STDIN CSV HEADER" + m[3] + "\n")
			script.Write(data)
			if !strings.HasSuffix(string(data), "\n") {
				script.WriteString("\n")
			}
			script.WriteString("\\.\n")
		case strings.HasPrefix(ln, "--"), strings.TrimSpace(ln) == "":
		default:
			stmt.WriteString(ln + "\n")
		}
	}
	body := strings.TrimSpace(stmt.String())
	body = strings.TrimSuffix(body, ";")
	script.WriteString("SELECT row_to_json(__r) FROM (\n" + body + "\n) __r;\n")
	return script.String()
}

var pgCopyRe = regexp.MustCompile(`^--   \\copy ("[^"]+") FROM '((?:[^']|'')*)' CSV HEADER(.*)$`)

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

// equivNormaliseTimes renders every string that IS a time (in any form
// ssql.ParseTime reads) as RFC 3339 UTC. A `time` column is an instant;
// ssql writes it as 2026-01-20T00:00:00Z, DuckDB's JSON as "2026-01-20
// 00:00:00", a DATE as "2026-01-20" — three spellings of one value. Applied
// to every lane and to the goldens alike, so it can only hide a
// representation difference, never a different instant (DFC128 D1).
func equivNormaliseTimes(m map[string]any) map[string]any {
	var out map[string]any
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if t, ok := ssql.ParseTime(s); ok {
			if out == nil {
				out = make(map[string]any, len(m))
				for k2, v2 := range m {
					out[k2] = v2
				}
			}
			out[k] = t.UTC().Format(time.RFC3339Nano)
		}
	}
	if out == nil {
		return m
	}
	return out
}

// equivCanon renders records as canonical strings: json.Marshal sorts map keys,
// so column order is normalised. For unordered output the rows are sorted so
// two lanes with different row order still compare equal.
func equivCanon(recs []map[string]any, ordered bool) []string {
	out := make([]string, len(recs))
	for i, m := range recs {
		b, _ := json.Marshal(equivNormaliseTimes(m))
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
	columns := make(map[string][]string)
	for _, ln := range lanes {
		reason := c.Skip[ln.name]
		if reason == "" && equivSQLLanes[ln.name] {
			reason = c.Skip["duckdb"] // no SQL translation at all → no dialect either
		}
		if reason != "" {
			continue
		}
		raw := ln.run(t, bin, pipeline)
		if strings.HasPrefix(raw, equivSkipPrefix) {
			t.Logf("lane %q skipped (dialect refusal by design): %s", ln.name, strings.TrimPrefix(raw, equivSkipPrefix))
			continue
		}
		results[ln.name] = equivParse(t, ln.name, raw)
		columns[ln.name] = equivColumnOrder(raw)
	}

	ref, ok := results["exec"]
	if !ok {
		t.Fatal("exec lane is the reference oracle and must not be skipped")
	}

	// Column ORDER is part of the result too (since 2026-09-23: the record
	// lane's writers and readers keep it, `include` names it, so a
	// pipeline's columns come out the same in every lane). Compared as the
	// positional union of each lane's JSONL key order against exec's.
	if !c.ColumnsUnordered {
		for name, cols := range columns {
			if name == "exec" || len(cols) == 0 || len(columns["exec"]) == 0 {
				continue
			}
			// Over the columns both lanes have: a column one lane never
			// emits (every value absent in exec, NULL in SQL) is a
			// presence difference the value comparison judges, not an
			// order difference.
			a, b := equivCommon(columns["exec"], cols), equivCommon(cols, columns["exec"])
			if !slices.Equal(a, b) {
				t.Errorf("lane %q column order differs from exec:\n  exec: %v\n  %s: %v", name, columns["exec"], name, cols)
			}
		}
	}

	// Ground truth: exec must match the implementation-independent
	// golden when supplied (catches "all lanes agree but all wrong").
	if c.Golden != nil {
		if wantC, gotC := equivCanon(c.Golden, c.Ordered), equivCanon(ref, c.Ordered); !slices.Equal(gotC, wantC) {
			t.Errorf("exec lane disagrees with golden:\n  golden: %s\n  exec:   %s",
				strings.Join(wantC, " "), strings.Join(gotC, " "))
		}
	}

	// DuckDB's -json prints a BOOLEAN as the STRING "true"/"false" (1.5.0).
	// In a column where exec has booleans, that is a spelling, not a value.
	// Per column, so a text column that happens to say "true" stays text.
	boolCols := map[string]bool{}
	for _, r := range ref {
		for k, v := range r {
			if _, isBool := v.(bool); isBool {
				boolCols[k] = true
			}
		}
	}
	for name, rows := range results {
		if !equivSQLLanes[name] {
			continue
		}
		for _, r := range rows {
			for k, v := range r {
				if s, isStr := v.(string); isStr && boolCols[k] && (s == "true" || s == "false") {
					r[k] = s == "true"
				}
				// duckdb -json renders HUGEINT (e.g. SUM over BIGINT) as a
				// JSON string. ssql's CSV reader parses canonical integer
				// strings as numbers anyway, so converting them back is
				// normalising a representation difference.
				if s, isStr := v.(string); isStr && canonicalIntRe.MatchString(s) {
					if f, err := strconv.ParseFloat(s, 64); err == nil {
						r[k] = f
					}
				}
			}
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
		// -not negates a whole clause; -invert/-v negates the whole where.
		{name: "not_clause", flag: `where -not -if pop gt 15 -if city eq Oslo`, expr: `where -if-expr '!(pop > 15 && city == "Oslo")'`},
		{name: "not_clause_or", flag: `where -not -if pop gt 15 -if city eq Oslo + -if city eq Lima`, expr: `where -if-expr '!(pop > 15 && city == "Oslo") || city == "Lima"'`},
		{name: "invert_or", flag: `where -invert -if pop gt 25 + -if city eq Lima`, expr: `where -if-expr '!(pop > 25 || city == "Lima")'`},
		{name: "invert_short", flag: `where -invert -if pop gt 25`, expr: `where -if-expr '!(pop > 25)'`},
		{name: "invert_with_not", flag: `where -invert -not -if pop gt 15 -if city eq Oslo`, expr: `where -if-expr 'pop > 15 && city == "Oslo"'`},
		{name: "update_if", flag: `update -if pop gt 15 -set city big`, expr: `update -if-expr 'pop > 15' -set city big`},
		{name: "update_negated", flag: `update +if pop gt 15 -set city small`, expr: `update +if-expr 'pop > 15' -set city small`},
		{name: "update_not_clause", flag: `update -not -if pop gt 15 -if city eq Oslo -set city other`, expr: `update -if-expr '!(pop > 15 && city == "Oslo")' -set city other`},
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
		{[]string{"group", "where", "limit"}, true},  // where preserves the unspecified order
		{[]string{"group", "sort", "limit"}, false},  // sort restores order
		{[]string{"group", "top", "limit"}, false},   // top sorts by value
		{[]string{"group", "update", "limit"}, true}, // update preserves order
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
		// bare `sample` = pass-through dial (the bare-limit convention):
		// the stage must vanish identically everywhere, duckdb included.
		Name: "sample_bare_passthrough",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} sample | {{.bin}} sort city`,
		Ordered: true,
	},
	{
		// `sample 0` keeps no rows in every lane (USING SAMPLE 0 ROWS in
		// SQL); until v4.92.0 it was the pass-through dial.
		Name: "sample_zero_is_empty",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | ` +
			`{{.bin}} sample 0 | {{.bin}} sort city`,
		Ordered: true,
		Golden:  []map[string]any{},
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
		// resample over a `time` column (DFC128 D1, second unit): time in,
		// time out, on the same epoch grid as the numeric and string
		// families. The golden is what the STRING-date form of the same
		// pipeline produced before the column could be a time.
		Name: "resample_time_previous",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} cast -type date time | ` +
			`{{.bin}} resample -time date -every 336h -value amount`,
		Ordered: true,
		Golden: []map[string]any{
			{"date": "2026-01-01T00:00:00Z", "amount": 100}, {"date": "2026-01-15T00:00:00Z", "amount": 100},
			{"date": "2026-01-29T00:00:00Z", "amount": 200}, {"date": "2026-02-12T00:00:00Z", "amount": 400},
			{"date": "2026-02-26T00:00:00Z", "amount": 400},
		},
	},
	{
		Name: "resample_time_next",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} cast -type date time | ` +
			`{{.bin}} resample -time date -every 336h -value amount -fill next`,
		Ordered: true,
	},
	{
		Name: "resample_time_linear",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} cast -type date time | ` +
			`{{.bin}} resample -time date -every 168h -value amount -fill linear`,
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
		// `from jsonl` has a typed form (roadmap item 9, 2026-09-06): the
		// struct is inferred from a sample of lines and the pipeline stays
		// typed instead of falling back to record mode wholesale. Every
		// lane must agree on the same rows; the DuckDB lane reads the
		// plain file with read_json.
		Name:     "jsonl_typed_where_groupby",
		Pipeline: `{{.bin}} from jsonl {{.data}}/employees.jsonl | {{.bin}} where -if age gt 30 | {{.bin}} group-by dept -count n -sum salary total`,
		Ordered:  false,
	},
	{
		// cast is ONE conversion in every lane (ssql.CastValue). Text → int
		// truncates a fraction (2.9 → 2, -7 stays), as Go and pandas do —
		// SQL's CAST rounds, so the SQL lane truncates explicitly; booleans
		// read yes/off/1/TRUE.
		Name:     "cast_text_to_int_and_bool",
		Pipeline: `{{.bin}} from csv {{.data}}/castable.csv -type score string | {{.bin}} cast -type score int -type flag bool`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 1, "score": 10, "flag": true}, {"id": 2, "score": 2, "flag": false},
			{"id": 3, "score": -7, "flag": true}, {"id": 4, "score": 0, "flag": true},
		},
		Skip: map[string]string{"duckdb": "generate sql does not translate from-stage -type overrides"},
	},
	{
		// -invalid missing: a value that is not of the type (N/A, maybe)
		// becomes a field without a value — never 0 or false. Default is to
		// stop the pipeline (TestCastIsStrict); typed mode's missing is the
		// zero value (DFC124 §3).
		Name:     "cast_invalid_missing",
		Pipeline: `{{.bin}} from csv {{.data}}/uncastable.csv | {{.bin}} cast -type score int -type flag bool -invalid missing`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 1, "score": 10, "flag": true}, {"id": 2}, {"id": 3, "score": 2, "flag": false},
		},
		Skip: map[string]string{"go-typed": "typed mode has no missing: an unconvertible value is the zero value (DFC124 §3)", "go-parallel": "typed mode has no missing: an unconvertible value is the zero value (DFC124 §3)"},
	},
	{
		// DFC133 random differential: a CSV column holding 02134 or 007 was
		// typed int and read as 2134 and 7 — the zeros gone for good. A
		// zero-padded number is an identifier: the column is text in every
		// lane, it filters and sorts as text, and DuckDB's sniffer agrees.
		Name:     "zero_padded_codes_stay_text",
		Pipeline: `{{.bin}} from csv {{.data}}/zero_padded.csv | {{.bin}} where -if zip startswith 0 | {{.bin}} sort part | {{.bin}} include id zip part`,
		Ordered:  true,
		Golden:   []map[string]any{{"id": 1, "zip": "02134", "part": "007"}, {"id": 3, "zip": "00501", "part": "045"}},
		Skip:     map[string]string{"datafusion": "DataFusion's CSV reader infers 02134 as Int64 — the defect ssql had until DFC133; DuckDB and Postgres agree with ssql"},
	},
	{
		// `from csv -type COL time` in RECORD codegen emitted FieldTypeAuto
		// (its own copy of the type-name table lacked "time"), so the column
		// stayed a string and a time comparison matched nothing in that lane
		// only (found 2026-09-22). Golden by hand from dated.csv.
		Name:     "from_csv_type_time_all_lanes",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv -type date time | {{.bin}} where -if-expr 'date >= since' -param since time 2026-01-15 | {{.bin}} group-by -count n`,
		Ordered:  false,
	},
	{
		// DFC134 §5.3: -param NAME TYPE VALUE binds a VARIABLE of the clause's
		// expressions. Typed float and int, two parameters shared by one
		// expression, native transpile in both Go lanes, literal in SQL.
		// Golden by hand: pop*1.5 > 15 → pop > 10.
		Name:     "param_float_int_where",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if-expr 'pop * rate > lo' -param rate float 1.5 -param lo int 15 | {{.bin}} sort id | {{.bin}} include id pop`,
		Ordered:  true,
		Golden: []map[string]any{{"id": 1, "pop": 31}, {"id": 2, "pop": 29}, {"id": 4, "pop": 11}, {"id": 5, "pop": 37},
			{"id": 7, "pop": 20}, {"id": 8, "pop": 14}, {"id": 11, "pop": 25}},
	},
	{
		// §3.3's attack string as a PARAMETER: it is compared, not parsed,
		// in every lane including the SQL one (where it must also survive
		// SQL quoting). Golden: nothing is called that, so only Oslo.
		Name:     "param_string_attack_is_data",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if-expr 'city == who' -param who string "x' || 1=1 || '" + -if-expr 'city == who' -param who string Oslo | {{.bin}} include id city`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1, "city": "Oslo"}},
	},
	{
		// Clause scope: the same name bound differently in two update
		// clauses, shared by -if-expr and -set-expr within a clause; a bool
		// parameter; first-match-wins (the SQL translator used to render a
		// later unconditional clause as unconditional, found 2026-09-22).
		// Golden by hand.
		Name:     "param_clause_scope_update",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if-expr 'id <= 4' | {{.bin}} update -if-expr 'pop > lo && big' -param lo int 20 -param big bool true -set-expr pop 'pop * k' -param k int 2 - -set-expr city 'tag' -param tag string small | {{.bin}} sort id`,
		Ordered:  true,
		Golden: []map[string]any{{"id": 1, "city": "Oslo", "pop": 62}, {"id": 2, "city": "Delhi", "pop": 58},
			{"id": 3, "city": "small", "pop": 10}, {"id": 4, "city": "small", "pop": 11}},
	},
	{
		// A time parameter: outside the Go transpiler's lattice, so the Go
		// lanes take the VM tier with a parsed time; SQL renders a TIMESTAMP
		// literal. Golden by hand from dated.csv.
		Name:     "param_time_where",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} cast -type date time | {{.bin}} where -if-expr 'date >= since' -param since time 2026-01-15 | {{.bin}} group-by -count n`,
		Ordered:  false,
	},
	{
		// DFC135 -if-field: numeric field against field, int/int and the
		// absent side (row 5 has no b) false; negation true. Golden by hand.
		Name:     "if_field_numeric",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs.csv | {{.bin}} where -if-field a lt b | {{.bin}} include id`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1}, {"id": 4}},
	},
	{
		Name:     "if_field_numeric_negated",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs.csv | {{.bin}} where +if-field a lt b | {{.bin}} include id`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 2}, {"id": 3}, {"id": 5}},
	},
	{
		// The absent side: false, and its negation true (row 5 has no b).
		Name:     "if_field_absent_side",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs_absent.csv | {{.bin}} where +if-field a lt b | {{.bin}} include id`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 2}, {"id": 5}},
		Skip:     map[string]string{"go-typed": "a typed struct cannot hold an absent int (DFC124 §3)", "go-parallel": "a typed struct cannot hold an absent int (DFC124 §3)"},
	},
	{
		// -set-field from an absent source leaves the target absent.
		Name:     "set_field_absent_source",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs_absent.csv | {{.bin}} update -set-field a b | {{.bin}} include id a`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1, "a": 7}, {"id": 2, "a": 3}, {"id": 5}},
		Skip:     map[string]string{"go-typed": "a typed struct cannot hold an absent int (DFC124 §3)", "go-parallel": "a typed struct cannot hold an absent int (DFC124 §3)"},
	},
	{
		// The string operators with a FIELD as the pattern: contains,
		// startswith, endswith, regex.
		Name:     "if_field_text_ops",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs.csv | {{.bin}} update -set-expr c 's contains p ? 1 : 0' -set-expr st 's startsWith p ? 1 : 0' -set-expr en 's endsWith p ? 1 : 0' | {{.bin}} where -if-field s regex q | {{.bin}} include id c st en`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1, "c": 1, "st": 0, "en": 0}, {"id": 3, "c": 1, "st": 1, "en": 1}, {"id": 5, "c": 1, "st": 0, "en": 1}},
	},
	{
		// -if-field with the string operators directly (the flag form, not
		// the expression): same rows as above.
		Name:     "if_field_text_ops_flags",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs.csv | {{.bin}} where -if-field s contains p -if-field s endswith p + -if-field s startswith p | {{.bin}} include id`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 2}, {"id": 3}, {"id": 5}},
	},
	{
		// -set-field: copy a column (type kept), conditionally; an absent
		// source leaves the target absent (row 5's b). Golden by hand.
		Name:     "set_field_copy",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs.csv | {{.bin}} update -if-field a gt b -set-field a b | {{.bin}} include id a`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1, "a": 5}, {"id": 2, "a": 3}, {"id": 3, "a": 4}, {"id": 4, "a": 2}, {"id": 5, "a": 1}},
	},
	{
		// -param-field: the same expression text with a literal and with a
		// column bound to the same name; typed views (int column as float,
		// text column as string). Golden by hand: a*1.5 > b, or a*a > b on
		// the one row where s == p.
		Name:     "param_field_literal_vs_column",
		Pipeline: `{{.bin}} from csv {{.data}}/pairs.csv | {{.bin}} where -if-expr 'a * rate > b' -param rate float 1.5 + -if-expr 'a * rate > b && s == tag' -param-field rate float a -param-field tag string p | {{.bin}} include id`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}, {"id": 5}},
	},
	{
		// -param-field over a column whose name is not an expression
		// identifier, and the SQL CAST when the declared type differs from
		// the column's kind.
		Name:     "param_field_cast_view",
		Pipeline: `{{.bin}} from csv {{.data}}/inject.csv | {{.bin}} where -if-expr 'k >= "b"' -param-field k string 1st | {{.bin}} update -set-expr n2 'n * 2' -param-field n float id | {{.bin}} include id n2`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 2, "n2": 4}, {"id": 3, "n2": 6}, {"id": 4, "n2": 8}, {"id": 5, "n2": 10}},
	},
	{
		// DFC134 §4/§6: injection strings in every value slot (-if, -param,
		// -set) and a digit-led column name, through every lane including
		// generate sql executed by DuckDB. The row whose note IS the attack
		// string is selected, and only it; nothing is dropped, nothing is
		// commented out. Golden by hand.
		Name:     "sql_injection_strings_are_data",
		Pipeline: `{{.bin}} from csv {{.data}}/inject.csv | {{.bin}} where -if note eq "'; DROP TABLE t; --" + -if-expr 'note == who' -param who string "x' OR '1'='1" + -if note contains "%_" | {{.bin}} update -set 1st "it's -- fine */" | {{.bin}} sort id`,
		Ordered:  true,
		Golden: []map[string]any{
			{"id": 2, "note": "'; DROP TABLE t; --", "1st": "it's -- fine */"},
			{"id": 3, "note": "x' OR '1'='1", "1st": "it's -- fine */"},
			{"id": 4, "note": "50%_off", "1st": "it's -- fine */"}},
	},
	{
		// DFC134 §5.2: -arg VALUE is a positional whatever VALUE looks like.
		// Sort DESCENDING by a column called "-desc", keep columns called
		// "-desc" and "-generate" (bare, the latter flips include into code
		// generation). Golden is hand-written from the fixture.
		Name:     "hostile_names_include_sort",
		Pipeline: `{{.bin}} from csv {{.data}}/hostile.csv | {{.bin}} sort -arg -desc -desc | {{.bin}} include -arg name -arg -desc -arg -generate`,
		Ordered:  true,
		Golden: []map[string]any{
			{"name": "amy", "-desc": 9, "-generate": 1}, {"name": "cal", "-desc": 7, "-generate": 3},
			{"name": "bob", "-desc": 5, "-generate": 2}, {"name": "dee", "-desc": 1, "-generate": 4}},
		Skip: map[string]string{"duckdb": "generate sql refuses a positional beginning with - or + loudly (its translators re-read argv by leading dash; TODO.md, DFC134 §5.2)"},
	},
	{
		// The generate-ssql optimiser read the KEY "-desc" as the descending
		// flag and fused this ASCENDING two-key sort + limit into
		// `top 2 -field name` (dee, cal). Golden = the two smallest names.
		Name:     "hostile_names_two_key_sort_is_not_top",
		Pipeline: `{{.bin}} from csv {{.data}}/hostile.csv | {{.bin}} sort -arg name -arg -desc | {{.bin}} limit 2 | {{.bin}} include -arg name`,
		Ordered:  true,
		Golden:   []map[string]any{{"name": "amy"}, {"name": "bob"}},
		Skip:     map[string]string{"duckdb": "generate sql refuses a positional beginning with - or + loudly (its translators re-read argv by leading dash; TODO.md, DFC134 §5.2)"},
	},
	{
		// Separators and a plus-prefixed name as data, through exclude; and
		// a flag slot (-field) holding a hostile name after the optimiser
		// DOES fuse (single key, descending): top 2 by "-desc".
		Name:     "hostile_names_exclude_then_top",
		Pipeline: `{{.bin}} from csv {{.data}}/hostile.csv | {{.bin}} exclude -arg - -arg +x -arg -generate | {{.bin}} sort -arg -desc -desc | {{.bin}} limit 2`,
		Ordered:  true,
		Golden:   []map[string]any{{"name": "amy", "-desc": 9}, {"name": "cal", "-desc": 7}},
		Skip:     map[string]string{"duckdb": "generate sql refuses a positional beginning with - or + loudly (its translators re-read argv by leading dash; TODO.md, DFC134 §5.2)"},
	},
	{
		// -arg with ordinary values is the bare form exactly, in EVERY lane
		// including the SQL ones (which collapse it): the form a program
		// emits must not cost it a backend.
		Name:     "arg_form_equals_bare_form",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} sort -arg pop -desc | {{.bin}} include -arg city -arg pop | {{.bin}} limit -arg 3`,
		Ordered:  true,
	},
	{
		// DFC133 random differential. An aggregate over NO values has no
		// value — not 0 (Avg did) and not "" (Min/Max/Median did: a present
		// string the next `where` compared, so group b matched `m le 2`).
		// The empty SUM is 0 and the SQL lane says COALESCE(SUM, 0). An
		// empty text cell is missing (DFC124): MIN skips it, COUNT(DISTINCT)
		// does not count it.
		Name:     "groupby_aggregates_over_no_values",
		Pipeline: `{{.bin}} from csv {{.data}}/missing_groups.csv | {{.bin}} group-by g -count c -sum v total -avg v mean -max v m -min t first_t -count-distinct t nt`,
		Ordered:  false,
		Golden: []map[string]any{
			{"g": "a", "c": 2, "total": 4, "mean": 2, "m": 3, "first_t": "Oslo", "nt": 1},
			{"g": "b", "c": 2, "total": 0, "nt": 0},
		},
		Skip: map[string]string{"go-typed": "typed reader: absent field → zero value (DFC124 §3)", "go-parallel": "typed reader: absent field → zero value (DFC124 §3)"},
	},
	{
		// …and a condition on that valueless result is false, its negation true.
		Name:     "groupby_no_value_then_where",
		Pipeline: `{{.bin}} from csv {{.data}}/missing_groups.csv | {{.bin}} group-by g -max v m | {{.bin}} where -if m le 2 + +if m gt 0 | {{.bin}} include g`,
		Ordered:  false,
		Golden:   []map[string]any{{"g": "b"}},
		Skip:     map[string]string{"go-typed": "typed reader: absent field → zero value (DFC124 §3)", "go-parallel": "typed reader: absent field → zero value (DFC124 §3)"},
	},
	{
		// A literal is typed by the COLUMN it meets, not by its spelling:
		// `code eq 12` rendered `code = 12` and DuckDB refused to cast the
		// text column; `-set code 99` mixed VARCHAR and INTEGER in a CASE.
		Name:     "literal_typed_by_text_column",
		Pipeline: `{{.bin}} from csv {{.data}}/missing_groups.csv | {{.bin}} update -if id ge 3 -set code 0099 | {{.bin}} where -if code eq 12 + -if code eq 0099 | {{.bin}} include id`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1}, {"id": 3}, {"id": 4}},
		Skip:     map[string]string{"go-typed": "typed reader refuses the fixture's empty numeric cells (DFC124 §3)", "go-parallel": "typed reader refuses the fixture's empty numeric cells (DFC124 §3)"},
	},
	{
		// …and the literal written INTO a text column is the token as typed.
		// `update -set zip 02134` gave three answers in four lanes: exec
		// parsed it and coerced back ("2134"), record codegen stored the
		// NUMBER 2134, typed and SQL kept "02134".
		Name:     "update_literal_into_text_column_keeps_its_spelling",
		Pipeline: `{{.bin}} from csv {{.data}}/missing_groups.csv | {{.bin}} update -if id ge 3 -set code 0099 | {{.bin}} where -if id ge 3 | {{.bin}} include id code`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 3, "code": "0099"}, {"id": 4, "code": "0099"}},
		Skip:     map[string]string{"go-typed": "typed reader refuses the fixture's empty numeric cells (DFC124 §3)", "go-parallel": "typed reader refuses the fixture's empty numeric cells (DFC124 §3)"},
	},

	{
		// update's SQL negation is ssql's: +if on a row with no value is
		// TRUE (rows 3 and 4 have no v), where SQL's bare NOT said NULL.
		Name:     "update_negated_condition_on_missing",
		Pipeline: `{{.bin}} from csv {{.data}}/missing_groups.csv | {{.bin}} update +if v ge 2 -set g z | {{.bin}} include id g`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1, "g": "z"}, {"id": 2, "g": "a"}, {"id": 3, "g": "z"}, {"id": 4, "g": "z"}},
		Skip:     map[string]string{"go-typed": "typed reader: absent field → zero value (DFC124 §3)", "go-parallel": "typed reader: absent field → zero value (DFC124 §3)"},
	},
	{
		// cast of an empty text cell stays missing — it became 0.
		Name:     "cast_empty_text_stays_missing",
		Pipeline: `{{.bin}} from csv {{.data}}/missing_groups.csv | {{.bin}} cast -type e int | {{.bin}} where -if id le 2 | {{.bin}} include id e`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1}, {"id": 2}},
		Skip:     map[string]string{"go-typed": "typed reader: absent field → zero value (DFC124 §3)", "go-parallel": "typed reader: absent field → zero value (DFC124 §3)"},
	},
	{
		// DFC133 (row-order sweep): an int key joined to a float key. The
		// library's hash key printed both as "3", then the confirming
		// Match compared the raw interfaces — int64(3) != float64(3) — and
		// EVERY row of the join vanished, silently. Typed mode refused
		// with "join key types differ". Numbers compare as numbers.
		Name:     "join_int_key_to_float_key",
		Pipeline: `{{.bin}} from csv {{.data}}/orders_floatkey.csv | {{.bin}} join {{.data}}/customers.csv -using customer_id | {{.bin}} include order_id name`,
		Ordered:  false,
		Golden: []map[string]any{
			{"order_id": 1, "name": "customer_3"}, {"order_id": 2, "name": "customer_1"}, {"order_id": 4, "name": "customer_5"},
		},
	},
	{
		// DFC128 §6g: filtering on a column whose FIRST row is NULL. exec
		// validated `where`'s fields against the first record's values and
		// failed with "unknown field(s): score (available: …, score)".
		Name:     "jsonl_where_on_nullable_column",
		Pipeline: `{{.bin}} from jsonl {{.data}}/null_first.jsonl | {{.bin}} where -if score gt 3 | {{.bin}} include id score`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 2, "score": 4}},
		Skip:     map[string]string{"go-typed": "typed reader: absent field → zero value (DFC124 §3)", "go-parallel": "typed reader: absent field → zero value (DFC124 §3)"},
	},
	{
		// …and a NULL in a LATER element of a JSON array's int column: exec
		// turned it into 0 (the typed setter's fall-through), so `n ge 0`
		// matched a row that has no n, and sums were silently wrong.
		Name:     "json_array_null_is_not_zero",
		Pipeline: `{{.bin}} from json {{.data}}/null_mixed.json | {{.bin}} where -if n ge 0 | {{.bin}} include id n`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1, "n": 5}, {"id": 3, "n": 7}},
		Skip:     map[string]string{"go-typed": "typed reader: absent field → zero value (DFC124 §3)", "go-parallel": "typed reader: absent field → zero value (DFC124 §3)"},
	},
	{
		// DFC128 D1: `time` is a wire type. cast makes the column a time in
		// every lane; where compares it as a time (the operand is a DATE
		// form, the column a timestamp) and sort orders it chronologically.
		Name:     "cast_time_where_sort",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} cast -type date time | {{.bin}} where -if date gt 2026-01-10 | {{.bin}} sort date | {{.bin}} include id date`,
		Ordered:  true,
		Golden: []map[string]any{
			{"id": 2, "date": "2026-01-20T00:00:00Z"},
			{"id": 3, "date": "2026-02-02T00:00:00Z"},
			{"id": 4, "date": "2026-02-10T00:00:00Z"},
			{"id": 5, "date": "2026-02-28T00:00:00Z"},
		},
	},
	{
		// The source forms DuckDB, Postgres and APIs write, in one column:
		// as strings they sort lexically (the space before the T, the date
		// alone first); as times, chronologically.
		Name:     "cast_time_mixed_forms_sort",
		Skip:     map[string]string{"duckdb": "row 4 carries a UTC offset (…23:30:00+00): DuckDB's CAST(VARCHAR AS TIMESTAMP) shifts it into the session time zone, so its order depends on where the test runs; ssql's time is an instant"},
		Pipeline: `{{.bin}} from csv {{.data}}/mixed_times.csv | {{.bin}} cast -type ts time | {{.bin}} sort ts | {{.bin}} include id`,
		Ordered:  true,
		Golden:   []map[string]any{{"id": 2}, {"id": 4}, {"id": 3}, {"id": 1}},
	},
	{
		// bucket() over a time column: time in, time out, grouped.
		Name:     "cast_time_bucket_groupby",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} cast -type date time | {{.bin}} update -set-expr fortnight 'bucket(date, "336h")' | {{.bin}} group-by fortnight -count n -sum amount total`,
		Ordered:  false,
	},
	{
		// ssql's own date(): the forms GetOr[time.Time] reads, including
		// Postgres's zoneless JSON timestamp that expr-lang's date() refused.
		Name:     "update_date_function_forms",
		Skip:     map[string]string{"duckdb": "method calls on a time (date(ts).Day()) have no SQL translation — refused loudly by design"},
		Pipeline: `{{.bin}} from csv {{.data}}/mixed_times.csv | {{.bin}} update -set-expr day 'date(ts).Day()' -set-expr hour 'date(ts).UTC().Hour()' | {{.bin}} include id day hour`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 1, "day": 1, "hour": 0}, {"id": 2, "day": 1, "hour": 5},
			{"id": 3, "day": 1, "hour": 0}, {"id": 4, "day": 31, "hour": 23},
		},
	},
	{
		// DFC128 F1/D3: a field that is NULL (= absent) in the first
		// record. exec's `from` inferred the header from that one record,
		// the header is authoritative, and every sink dropped `note` and
		// `score` without a word — exec was the only lane that lost them.
		// The header is now the union over a bounded sample. Golden pins
		// the rows; typed lanes render an absent cell as the zero value.
		Name:     "jsonl_null_in_first_record",
		Pipeline: `{{.bin}} from jsonl {{.data}}/null_first.jsonl | {{.bin}} where -if id gt 0`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 1},
			{"id": 2, "note": "b", "score": 4},
			{"id": 3, "note": "c", "score": 2.5},
		},
		Skip: map[string]string{"go-typed": "typed reader: absent field → zero value (DFC124 §3)", "go-parallel": "typed reader: absent field → zero value (DFC124 §3)"},
	},
	{
		// The same rows behind a `_schema` header (a tee'd file): the
		// typed reader takes the struct from the header and skips the
		// line; the exec reader coerces by it. DuckDB has no notion of
		// the header and would read it as a row — skipped by name.
		Name:     "jsonl_schema_header_where",
		Pipeline: `{{.bin}} from jsonl {{.data}}/employees_schema.jsonl | {{.bin}} where -if age gt 30 | {{.bin}} include name age`,
		Ordered:  false,
		Skip:     map[string]string{"duckdb": "read_json has no notion of ssql's _schema header line"},
	},
	{
		// DFC124 §3: column types come from a SAMPLE of leading rows, not
		// the first row. v opens with `0`; the rows that must survive are
		// the fractional ones the old reader turned into 0. DuckDB's
		// sniffer samples too, so the duckdb lane is the independent
		// oracle; the typed lane samples via SampleCSVSchema.
		Name:     "int_first_row_floats_survive",
		Pipeline: `{{.bin}} from csv {{.data}}/int_first.csv | {{.bin}} where -if v gt 0.4 | {{.bin}} include t v`,
		Ordered:  false,
		Golden: []map[string]any{
			{"t": 3, "v": 2}, {"t": 1, "v": 0.5}, {"t": 2, "v": 1.5},
		},
	},
	{
		// The same fixture as TSV: the TSV reader typed each value on its
		// own until v4.91.0 (no column typing at all in exec, so a wrong
		// answer needed no late row); now it shares readRows with CSV.
		Name:     "int_first_tsv_floats_survive",
		Pipeline: `{{.bin}} from tsv {{.data}}/int_first.tsv | {{.bin}} where -if v gt 0.4 | {{.bin}} include t v`,
		Ordered:  false,
		Golden: []map[string]any{
			{"t": 3, "v": 2}, {"t": 1, "v": 0.5}, {"t": 2, "v": 1.5},
		},
	},
	{
		// `-type` on from jsonl (new in v4.91.0): exec coerces per record,
		// record codegen emits ssql.CoerceFieldTypes, typed codegen fixes
		// the struct field — one answer, five lanes.
		Name:     "jsonl_type_override_agrees",
		Pipeline: `{{.bin}} from jsonl {{.data}}/employees.jsonl -type age float -type name string | {{.bin}} where -if age gt 29.5 | {{.bin}} include name age`,
		Ordered:  false,
		Skip:     map[string]string{"duckdb": "generate sql does not translate from-stage -type overrides"},
	},
	{
		// exec `where -if` compared an int field against a fractional
		// operand by ParseInt → error → silent false, dropping every row;
		// the other lanes compare numerically. Found by the int_first case
		// (a float column whose whole numbers travel as JSON `2`).
		Name:     "where_int_field_float_operand",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} where -if age gt 29.5 | {{.bin}} include name age`,
		Ordered:  false,
	},
	{
		// Same fixture through describe: min/max/mean over v must see
		// the fractions (a truncating reader gives mean 0.6, not 0.85).
		Name:     "int_first_row_describe",
		Pipeline: `{{.bin}} from csv {{.data}}/int_first.csv | {{.bin}} describe | {{.bin}} where -if field eq v | {{.bin}} include field min max mean`,
		Ordered:  false,
		Skip:     map[string]string{"go-typed": "typed reader: describe FIELDS typed lane N/A here (see empties_*)", "go-parallel": "typed reader: describe FIELDS typed lane N/A here (see empties_*)"},
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
		// A condition on an ABSENT value is false for every operator —
		// exec's `exists && op`, SQL's NULL comparison. `gt 5` (below) cannot
		// tell: an absent n read as 0 fails it too. `ge 0` and `ne` can, and
		// record codegen failed both until it got the HasValue guard
		// (DFC128 §6g). +if negates outside the guard, so it keeps the row.
		Name:     "empties_where_absent_never_matches",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} where -if n ge 0 -if n ne 99999 | {{.bin}} include id n`,
		Ordered:  false,
		Skip:     map[string]string{"go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		// The same rule in update: row 2 has no n, so `-if n ge 0` must not
		// touch it. `tag` exists beforehand (SQL cannot add a column
		// conditionally), and the where keeps the case about update.
		Name:     "empties_update_absent_never_matches",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} update -set tag no | {{.bin}} update -if n ge 0 -set tag hit | {{.bin}} include id tag`,
		Ordered:  false,
		Golden:   []map[string]any{{"id": 1, "tag": "hit"}, {"id": 2, "tag": "no"}, {"id": 3, "tag": "hit"}, {"id": 4, "tag": "hit"}},
		Skip:     map[string]string{"go-typed": "typed reader: empty cell → zero value (DFC124 §3)", "go-parallel": "typed reader: empty cell → zero value (DFC124 §3)"},
	},
	{
		Name:     "empties_where_negated_keeps_absent",
		Pipeline: `{{.bin}} from csv {{.data}}/empties.csv | {{.bin}} where +if n ge 0 | {{.bin}} include id`,
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
		// -cube (and -rollup) were REJECTED outright by typed-mode codegen,
		// then ejected to the record ssql.Rollup path (36 s on 14.6M rows),
		// and since 2026-09-05 run natively: a parallel detail group-by
		// carrying mergeable state + typed.RollupEnrich (1.7 s). The
		// typed/parallel lanes must produce the exact enrichment exec
		// does; the duckdb lane runs translateGroupByRollup. Golden
		// hand-checked: Widget/US appears twice (100+50), so a wrong
		// grouping diverges.
		Name: "groupby_cube",
		Pipeline: `{{.bin}} from csv {{.data}}/sales.csv | ` +
			`{{.bin}} group-by product region -count c -sum amount total -cube`,
		Ordered: false,
		Golden: []map[string]any{
			{"product": "Widget", "region": "US", "c": 5, "total": 580, "product_c": 3, "product_total": 300, "region_c": 3, "region_total": 350, "product_region_c": 2, "product_region_total": 150},
			{"product": "Widget", "region": "EU", "c": 5, "total": 580, "product_c": 3, "product_total": 300, "region_c": 2, "region_total": 230, "product_region_c": 1, "product_region_total": 150},
			{"product": "Gadget", "region": "US", "c": 5, "total": 580, "product_c": 2, "product_total": 280, "region_c": 3, "region_total": 350, "product_region_c": 1, "product_region_total": 200},
			{"product": "Gadget", "region": "EU", "c": 5, "total": 580, "product_c": 2, "product_total": 280, "region_c": 2, "region_total": 230, "product_region_c": 1, "product_region_total": 80},
		},
	},
	{
		// -rollup over three fields with every aggregate the translator
		// knows: the DuckDB lane aggregates each grouping set over the raw
		// rows through one subquery per set (translateGroupByRollup) and
		// must match exec's enrichment column for column — including
		// AVG, which a window-function rewrite could not reproduce.
		Name: "groupby_rollup_three_fields",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | ` +
			`{{.bin}} group-by dept city status -count n -sum salary total -avg age mean_age -min level lo -max level hi -rollup`,
		Ordered: false,
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
		// A bare `limit` (no N) and `offset 0` are pass-throughs (a limit
		// stage you dial off by deleting the number) and MUST vanish from
		// generated go/sql/ssql — every lane must return exactly the
		// where result.
		Name:     "limit_bare_passthrough",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 15 | {{.bin}} offset 0 | {{.bin}} limit`,
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
		// Two consecutive wheres, the first with two OR clauses: (A OR B)
		// AND C. The optimiser's where-merge concatenated the argument
		// lists — A OR (B AND C) — so every generated-Go lane filtered
		// wrong until 2026-09-10; it now merges single-clause stages only.
		Name:     "where_or_then_where_is_and",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 15 + -if city eq Lima | {{.bin}} where -if pop lt 30 | {{.bin}} include id city pop`,
		Ordered:  false,
	},
	{
		// `update -set-bucket FIELD SOURCE WIDTH` is the flag spelling of
		// -set-expr FIELD 'bucket(SOURCE, "WIDTH")' (one implementation,
		// exprfn.SnapNanos); both must agree with each other and with
		// DuckDB, where bucket() translates to a magnitude-detecting CASE.
		Name:     "set_bucket_flag_seconds",
		Pipeline: `{{.bin}} from {{.data}}/epochs.csv | {{.bin}} update -set-bucket m ts 1m | {{.bin}} group-by m -sum v total -count n`,
		Ordered:  false,
		Golden: []map[string]any{
			{"m": 1699999980, "total": 30, "n": 2},
			{"m": 1700000040, "total": 120, "n": 3},
		},
	},
	{
		Name:     "set_bucket_expr_seconds",
		Pipeline: `{{.bin}} from {{.data}}/epochs.csv | {{.bin}} update -set-expr m 'bucket(ts, "1m")' | {{.bin}} group-by m -sum v total -count n`,
		Ordered:  false,
		Golden: []map[string]any{
			{"m": 1699999980, "total": 30, "n": 2},
			{"m": 1700000040, "total": 120, "n": 3},
		},
	},
	{
		// Milliseconds: the unit is read from the value's magnitude.
		Name:     "set_bucket_flag_millis",
		Pipeline: `{{.bin}} from {{.data}}/epochs_ms.csv | {{.bin}} update -set-bucket m ts 1m | {{.bin}} include id m`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 3, "m": 1700000040000},
			{"id": 1, "m": 1699999980000},
			{"id": 5, "m": 1700000040000},
			{"id": 2, "m": 1699999980000},
			{"id": 4, "m": 1700000040000},
		},
	},
	{
		// String aggregates: max/min over a text column in -expr, in every
		// Go lane (typed lowering has no shape for max, so it falls back to
		// record codegen — the result must still agree). SQL has no -expr.
		Name:     "expr_agg_string_max_min",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -expr 'max(hire_date)' latest -expr 'min(hire_date)' earliest -expr 'first(name)' first_seen -expr 'last(name)' last_seen -expr 'max(salary * 2)' top2 -count n`,
		Ordered:  false,
		Skip:     map[string]string{"duckdb": "group-by -expr has no SQL translation (expression aggregations are ssql-specific)"},
	},
	{
		// -min/-max FLAGS on a string field, in every lane. Until v4.99.0
		// exec and record codegen used Min[float64], which reported 0 for
		// every group while typed refused and DuckDB answered — the
		// top-by-string shape again (DFC129 §2). MinOf/MaxOf keep the
		// field's type; the Golden is the independent oracle (hand-checked
		// against the fixture: per dept the alphabetically first and last
		// name, and the salary range).
		Name:     "groupby_min_max_string",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -min name first_name -max name last_name -min salary lo -max salary hi`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "first_name": "Alice", "last_name": "Eve", "lo": 88000, "hi": 105000},
			{"dept": "Sales", "first_name": "Bob", "last_name": "Frank", "lo": 65000, "hi": 82000},
			{"dept": "Marketing", "first_name": "David", "last_name": "Grace", "lo": 72000, "hi": 78000},
		},
	},
	{
		// DFC129 phase 1: -first/-last/-any are ARRIVAL order — file order
		// on a file source in every lane (exec walks the group's rows,
		// record codegen the same, the typed parallel merge is in shard
		// order, DuckDB's first/last on a single-threaded small scan).
		// Golden from the fixture: per dept the first and last name in
		// file order.
		Name:     "groupby_first_last_any",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -first name first_name -last name last_name -any city a_city -count n`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "first_name": "Alice", "last_name": "Eve", "a_city": "SF", "n": 3},
			{"dept": "Sales", "first_name": "Bob", "last_name": "Frank", "a_city": "NYC", "n": 2},
			{"dept": "Marketing", "first_name": "David", "last_name": "Grace", "a_city": "Chicago", "n": 2},
		},
	},
	{
		// The same first/last under -presorted, which forces the SERIAL
		// typed group-by (GroupByOrdered): on the sharded parallel path the
		// 7-row fixture is one row per shard, so Merge alone decides the
		// answer and a wrong Add is invisible — this case is the one that
		// exercises Add's arrival order (watched it fail on a planted
		// last-as-first Add). Sorted by dept then name, so the Golden is
		// defined in every lane.
		Name:     "groupby_first_last_presorted_serial",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} sort dept name | {{.bin}} group-by dept -presorted -first name first_name -last name last_name -string-agg name "," names`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "first_name": "Alice", "last_name": "Eve", "names": "Alice,Carol,Eve"},
			{"dept": "Sales", "first_name": "Bob", "last_name": "Frank", "names": "Bob,Frank"},
			{"dept": "Marketing", "first_name": "David", "last_name": "Grace", "names": "David,Grace"},
		},
	},
	{
		// DFC129 phase 1: -count-distinct (a set per group, merged by
		// union) and -string-agg (arrival order, shared value formatting —
		// the salary column is an int, joined as digits in every lane
		// including DuckDB's string_agg). level is distinct per dept as
		// {7,9,6} {4,8} {5,6}: 3, 2, 2.
		Name:     "groupby_count_distinct_string_agg",
		Skip:     map[string]string{"postgres": "string_agg in arrival order: Postgres feeds the aggregate through an unstable sort by the group key"},
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -count-distinct level levels -count-distinct city cities -string-agg name ", " members -string-agg salary ";" pays`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "levels": 3, "cities": 1, "members": "Alice, Carol, Eve", "pays": "95000;105000;88000"},
			{"dept": "Sales", "levels": 2, "cities": 1, "members": "Bob, Frank", "pays": "65000;82000"},
			{"dept": "Marketing", "levels": 2, "cities": 1, "members": "David, Grace", "pays": "72000;78000"},
		},
	},
	{
		// Order-sensitive aggregates under -cube: the typed rollup merges
		// parent levels from detail-group STATE, which would join strings
		// in group order; it therefore ejects first/last/any/string-agg to
		// record codegen, and every lane must still agree with exec's
		// row-walking Rollup. count-distinct stays typed (set union).
		Name:     "groupby_cube_order_sensitive_aggs",
		Skip:     map[string]string{"postgres": "first/string_agg in arrival order: Postgres's aggregate input order is undefined (unstable sort by the group key)"},
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept status -first name f -string-agg name "," m -count-distinct city c -count n -cube`,
		Ordered:  false,
	},
	{
		// DFC129 phase 2: median and a percentile — the continuous quantile
		// (DuckDB quantile_cont / Postgres percentile_cont), one shared
		// interpolation (ssql.QuantileCont) in every Go lane. Golden from
		// DuckDB and Python on the fixture.
		Name:     "groupby_stats_quantiles",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -median salary med -percentile salary 0.9 p90 -count n`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "med": 95000, "p90": 103000, "n": 3},
			{"dept": "Sales", "med": 73500, "p90": 80300, "n": 2},
			{"dept": "Marketing", "med": 75000, "p90": 77400, "n": 2},
		},
	},
	{
		// DFC129 phase 2: sample stddev and variance (Welford; the typed
		// parallel lane merges shard states). Floating-point summation is
		// order-dependent, so a parallel merge can differ from the serial
		// pass in the LAST digit on arbitrary data (as -avg and -sum can);
		// this fixture's variances are exact (73e6, 144.5e6, 18e6), so the
		// Golden — DuckDB's stddev_samp/var_samp — must match bit for bit.
		Name:     "groupby_stats_spread",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -stddev salary sd -variance salary v`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "sd": 8544.003745317532, "v": 73000000},
			{"dept": "Sales", "sd": 12020.815280171308, "v": 144500000},
			{"dept": "Marketing", "sd": 4242.640687119285, "v": 18000000},
		},
	},
	{
		// DFC129 phase 2: -mode keeps the field's type and breaks ties by
		// FIRST arrival — level is all-distinct per dept, so the answer is
		// the first row's level (7, 4, 5), which is also what DuckDB's mode
		// returns on a single-threaded scan; city has a clear winner.
		Name:     "groupby_mode",
		Skip:     map[string]string{"postgres": "mode ties: Postgres's mode() returns the smallest tied value, ssql the first seen"},
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -mode city top_city -mode level top_level`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "top_city": "SF", "top_level": 7},
			{"dept": "Sales", "top_city": "NYC", "top_level": 4},
			{"dept": "Marketing", "top_city": "Chicago", "top_level": 5},
		},
	},
	{
		// Statistics under -cube stay on the typed rollup path (quantile
		// lists concatenate, mode counts add with offset first-seen
		// indices, count-distinct sets union) and must agree with exec's
		// row-walking Rollup. stddev is left out here: parent levels merge
		// Welford states, which can differ from the serial pass in the last
		// digit (see groupby_stats_spread).
		Name:     "groupby_cube_stats",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept status -median salary med -mode city mc -count-distinct level lv -count n -cube`,
		Ordered:  false,
	},
	{
		// -sum / -avg are compensated (Neumaier) in every Go lane: group a
		// sums to exactly 1 where plain float64 addition — and DuckDB's
		// SUM(DOUBLE) and kahan_sum — return 0. The typed parallel lane
		// merges compensated shard states and keeps the 1 too.
		Name:     "groupby_sum_compensated",
		Pipeline: `{{.bin}} from csv {{.data}}/precision.csv | {{.bin}} group-by k -sum x total -avg x mean -count n`,
		Ordered:  false,
		Golden: []map[string]any{
			{"k": "a", "total": 1, "mean": 1.0 / 3, "n": 3},
			{"k": "b", "total": 0.6, "mean": 0.19999999999999998, "n": 3}, // 0.6/3 in float64
		},
		Skip: map[string]string{"duckdb": "DuckDB's SUM(DOUBLE) (and kahan_sum) lose the unit in 1e16 + 1 - 1e16; ssql's Neumaier sum keeps it"},
	},
	{
		// DFC129 phase 3: -arg-max FIELD BY R / -arg-min — FIELD at the
		// extreme BY, ties to first arrival, BY ordered like -min (numbers,
		// strings, times), the carried FIELD keeps its type. Golden from
		// DuckDB's arg_max/arg_min on the fixture.
		Name:     "groupby_arg_max_min",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -arg-max name salary top -arg-min name salary low -arg-max name level senior -arg-min hire_date salary low_hired -max salary top_salary`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "top": "Carol", "low": "Eve", "senior": "Carol", "low_hired": "2019-08-30", "top_salary": 105000},
			{"dept": "Sales", "top": "Frank", "low": "Bob", "senior": "Frank", "low_hired": "2021-06-01", "top_salary": 82000},
			{"dept": "Marketing", "top": "Grace", "low": "David", "senior": "Grace", "low_hired": "2020-04-22", "top_salary": 78000},
		},
	},
	{
		// The same under -presorted, which forces the serial typed
		// group-by so the accumulator's Add (not only Merge) decides — the
		// fixture is one row per shard on the parallel path.
		Name:     "groupby_arg_max_presorted_serial",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} sort dept name | {{.bin}} group-by dept -presorted -arg-max name salary top -arg-min name salary low`,
		Ordered:  false,
		Golden: []map[string]any{
			{"dept": "Engineering", "top": "Carol", "low": "Eve"},
			{"dept": "Sales", "top": "Frank", "low": "Bob"},
			{"dept": "Marketing", "top": "Grace", "low": "David"},
		},
	},
	{
		// -arg-max under -cube: order-sensitive on ties, so the typed
		// rollup ejects it to record codegen (like first/last); every lane
		// must still match exec's row-walking Rollup.
		Name:     "groupby_cube_arg_max",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept status -arg-max name salary top -count n -cube`,
		Ordered:  false,
	},
	// ---- window (DFC130 unit 0): the command had no differential coverage
	// before 2026-09-15. Fixture: employees.csv — within each dept the
	// salaries are in neither file nor alphabetical order, so a wrong
	// ordering diverges. Goldens from DuckDB.
	{
		Name:     "window_ranking_by_salary_desc",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -desc -row-number rn -rank rk -dense-rank dr -ntile 2 nt -percent-rank pr | {{.bin}} include name rn rk dr nt pr`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Carol", "rn": 1, "rk": 1, "dr": 1, "nt": 1, "pr": 0},
			{"name": "Alice", "rn": 2, "rk": 2, "dr": 2, "nt": 1, "pr": 0.5},
			{"name": "Eve", "rn": 3, "rk": 3, "dr": 3, "nt": 2, "pr": 1},
			{"name": "Frank", "rn": 1, "rk": 1, "dr": 1, "nt": 1, "pr": 0},
			{"name": "Bob", "rn": 2, "rk": 2, "dr": 2, "nt": 2, "pr": 1},
			{"name": "Grace", "rn": 1, "rk": 1, "dr": 1, "nt": 1, "pr": 0},
			{"name": "David", "rn": 2, "rk": 2, "dr": 2, "nt": 2, "pr": 1},
		},
	},
	{
		// LAG/LEAD at the partition edge have no value: exec omits the
		// field, SQL yields NULL — the canonical form must treat them alike.
		Name:     "window_offset_by_salary",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -lag salary 1 prev -lead salary 1 next -first salary lo -last salary hi | {{.bin}} include name prev next lo hi`,
		Ordered:  false,
	},
	{
		// Running aggregates over ssql's default frame (ROWS UNBOUNDED
		// PRECEDING → CURRENT ROW), ordered by salary within dept.
		Name:     "window_running_aggregates",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -sum salary run -avg salary ravg -count n -min salary mn -max salary mx | {{.bin}} include name run ravg n mn mx`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "run": 88000, "ravg": 88000, "n": 1, "mn": 88000, "mx": 88000},
			{"name": "Alice", "run": 183000, "ravg": 91500, "n": 2, "mn": 88000, "mx": 95000},
			{"name": "Carol", "run": 288000, "ravg": 96000, "n": 3, "mn": 88000, "mx": 105000},
			{"name": "Bob", "run": 65000, "ravg": 65000, "n": 1, "mn": 65000, "mx": 65000},
			{"name": "Frank", "run": 147000, "ravg": 73500, "n": 2, "mn": 65000, "mx": 82000},
			{"name": "David", "run": 72000, "ravg": 72000, "n": 1, "mn": 72000, "mx": 72000},
			{"name": "Grace", "run": 150000, "ravg": 75000, "n": 2, "mn": 72000, "mx": 78000},
		},
	},
	{
		// The default frame with TIED order values. ssql's default is ROWS
		// (cumulative by row); SQL's default with ORDER BY is RANGE, which
		// includes the current row's peers — the translator used to leave
		// the frame implicit and DuckDB summed whole peer groups. Found by
		// this case; the frame is now always rendered explicitly.
		Name:     "window_default_frame_with_ties",
		Skip:     map[string]string{"postgres": "ROWS frame over tied order keys: Postgres's sort is not stable, so which peer comes first is undefined"},
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order status -sum salary run -last name lastn -count cnt | {{.bin}} include name run lastn cnt`,
		Ordered:  false,
	},
	{
		Name:     "window_rows_frame_moving",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -preceding 1 -following 1 -avg salary mavg -sum salary msum -count mn | {{.bin}} include name mavg msum mn`,
		Ordered:  false,
	},
	{
		Name:     "window_unbounded_frame",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -preceding -1 -following -1 -sum salary total -max salary top -first name lowest_paid | {{.bin}} include name total top lowest_paid`,
		Ordered:  false,
	},
	{
		// Two clauses: a per-dept ascending numbering and a global descending
		// one. The SQL translator recognised only a bare "-" as the clause
		// separator (autocli's is "+"), so both clauses collapsed into one and
		// the second clause's -desc re-sorted the first. Found by this case.
		Name:     "window_two_clauses",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -row-number rn_in_dept + -order salary -desc -row-number rn_overall | {{.bin}} include name rn_in_dept rn_overall`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "rn_in_dept": 1, "rn_overall": 3},
			{"name": "Alice", "rn_in_dept": 2, "rn_overall": 2},
			{"name": "Carol", "rn_in_dept": 3, "rn_overall": 1},
			{"name": "Bob", "rn_in_dept": 1, "rn_overall": 7},
			{"name": "Frank", "rn_in_dept": 2, "rn_overall": 4},
			{"name": "David", "rn_in_dept": 1, "rn_overall": 6},
			{"name": "Grace", "rn_in_dept": 2, "rn_overall": 5},
		},
	},
	{
		// Global ranking with a tie (level 6 twice): RANK skips, DENSE_RANK
		// does not, PERCENT_RANK uses RANK − 1.
		Name:     "window_global_ranking_with_ties",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -order level -rank rk -dense-rank dr -percent-rank pr -row-number rn | {{.bin}} include name level rk dr pr`,
		Ordered:  false,
	},
	{
		// -presorted: the streaming O(1)-memory path, fed by an explicit sort.
		Name:     "window_presorted_streaming",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} sort dept salary | {{.bin}} window -presorted -partition dept -order salary -row-number rn -sum salary run -lag salary 1 prev | {{.bin}} include name rn run prev`,
		Ordered:  false,
	},
	// ---- window, DFC130 unit 1: cume_dist, nth_value, lag/lead defaults,
	// count(field). Goldens from DuckDB.
	{
		Name:     "window_cume_dist_nth_value_count_field",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -cume-dist cd -nth-value name 2 second -count-field name n | {{.bin}} include name cd second n`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "cd": 1.0 / 3, "n": 1},
			{"name": "Alice", "cd": 2.0 / 3, "second": "Alice", "n": 2},
			{"name": "Carol", "cd": 1, "second": "Alice", "n": 3},
			{"name": "Bob", "cd": 0.5, "n": 1},
			{"name": "Frank", "cd": 1, "second": "Frank", "n": 2},
			{"name": "David", "cd": 0.5, "n": 1},
			{"name": "Grace", "cd": 1, "second": "Grace", "n": 2},
		},
	},
	{
		// LAG/LEAD with a default: a typed literal (0 → int, "none" → string)
		// stands in at the partition edge in every lane, including the SQL
		// third argument.
		Name:     "window_lag_lead_default",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -lag-default salary 1 0 prev -lead-default name 1 none next | {{.bin}} include name prev next`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "prev": 0, "next": "Alice"},
			{"name": "Alice", "prev": 88000, "next": "Carol"},
			{"name": "Carol", "prev": 95000, "next": "none"},
			{"name": "Bob", "prev": 0, "next": "Frank"},
			{"name": "Frank", "prev": 65000, "next": "none"},
			{"name": "David", "prev": 0, "next": "Grace"},
			{"name": "Grace", "prev": 72000, "next": "none"},
		},
	},
	{
		// NTH_VALUE and COUNT(field) over a bounded ROWS frame (1 preceding).
		Name:     "window_bounded_nth_value_count_field",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -preceding 1 -following 0 -nth-value salary 2 nth2 -count-field salary c2 | {{.bin}} include name nth2 c2`,
		Ordered:  false,
	},
	{
		// COUNT(field) must skip missing values: prev is absent on each
		// partition's first row (LAG with no earlier row), NULL in SQL.
		Name:     "window_count_field_skips_missing",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -lag salary 1 prev | {{.bin}} window -partition dept -order salary -count-field prev n_prev -count n_all | {{.bin}} include name n_prev n_all`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "n_prev": 0, "n_all": 1},
			{"name": "Alice", "n_prev": 1, "n_all": 2},
			{"name": "Carol", "n_prev": 2, "n_all": 3},
			{"name": "Bob", "n_prev": 0, "n_all": 1},
			{"name": "Frank", "n_prev": 1, "n_all": 2},
			{"name": "David", "n_prev": 0, "n_all": 1},
			{"name": "Grace", "n_prev": 1, "n_all": 2},
		},
	},
	{
		// The same four on the -presorted streaming path (ring buffers and
		// delayed emission with a default) must equal the materialised path.
		Name:     "window_presorted_unit1_functions",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} sort dept salary | {{.bin}} window -presorted -partition dept -order salary -nth-value name 2 second -count-field name n -lag-default salary 1 0 prev -lead-default name 1 none next | {{.bin}} include name second n prev next`,
		Ordered:  false,
	},
	// ---- window, DFC130 unit 2: registry aggregates over the frame.
	{
		// Rolling sample stddev/variance are ABSENT for a one-row frame (SQL's
		// NULL — the library's Welford says 0 for group-by, the frame path
		// says nothing below two rows); rolling median and quantile.
		Name:     "window_rolling_stats",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -stddev salary sd -variance salary v -median salary med -percentile salary 0.9 p90 | {{.bin}} include name sd v med p90`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "med": 88000, "p90": 88000},
			{"name": "Alice", "sd": 4949.747468305833, "v": 24500000, "med": 91500, "p90": 94300},
			{"name": "Carol", "sd": 8544.003745317532, "v": 73000000, "med": 95000, "p90": 103000},
			{"name": "Bob", "med": 65000, "p90": 65000},
			{"name": "Frank", "sd": 12020.815280171308, "v": 144500000, "med": 73500, "p90": 80300},
			{"name": "David", "med": 72000, "p90": 72000},
			{"name": "Grace", "sd": 4242.640687119285, "v": 18000000, "med": 75000, "p90": 77400},
		},
	},
	{
		// Rolling count-distinct, string-agg (frame order) and mode.
		Name:     "window_rolling_set_list_mode",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -count-distinct status st -string-agg name "," names -mode city mc | {{.bin}} include name st names mc`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "st": 1, "names": "Eve", "mc": "SF"},
			{"name": "Alice", "st": 1, "names": "Eve,Alice", "mc": "SF"},
			{"name": "Carol", "st": 1, "names": "Eve,Alice,Carol", "mc": "SF"},
			{"name": "Bob", "st": 1, "names": "Bob", "mc": "NYC"},
			{"name": "Frank", "st": 1, "names": "Bob,Frank", "mc": "NYC"},
			{"name": "David", "st": 1, "names": "David", "mc": "Chicago"},
			{"name": "Grace", "st": 2, "names": "David,Grace", "mc": "Chicago"},
		},
	},
	{
		// arg-max / arg-min over a two-row moving frame: the BY field is a
		// second read column (projection pruning must keep it).
		Name:     "window_rolling_arg_max_min",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order name -preceding 1 -arg-max name salary richer -arg-min name salary poorer | {{.bin}} include name richer poorer`,
		Ordered:  false,
	},
	// ---- window, DFC130 unit 3: RANGE frames by value and by time.
	{
		// Rows whose salary is within 10000 below this row's (peers included):
		// SQL's RANGE BETWEEN 10000 PRECEDING AND CURRENT ROW.
		Name:     "window_range_numeric",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -range-preceding 10000 -count n -sum salary s -string-agg name "," who | {{.bin}} include name n s who`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Eve", "n": 1, "s": 88000, "who": "Eve"},
			{"name": "Alice", "n": 2, "s": 183000, "who": "Eve,Alice"},
			{"name": "Carol", "n": 2, "s": 200000, "who": "Alice,Carol"},
			{"name": "Bob", "n": 1, "s": 65000, "who": "Bob"},
			{"name": "Frank", "n": 1, "s": 82000, "who": "Frank"},
			{"name": "David", "n": 1, "s": 72000, "who": "David"},
			{"name": "Grace", "n": 2, "s": 150000, "who": "David,Grace"},
		},
	},
	{
		// A duration bound over a DATE order column: the last 14 days per
		// row. DuckDB reads the CSV dates as DATE and takes RANGE BETWEEN
		// INTERVAL '1209600 seconds' PRECEDING; ssql parses the plain date.
		Name:     "window_range_time_14d",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} window -order date -range-preceding 14d -sum amount s -count n | {{.bin}} include id s n`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 1, "s": 100, "n": 1},
			{"id": 2, "s": 200, "n": 1},
			{"id": 3, "s": 500, "n": 2},
			{"id": 4, "s": 700, "n": 2},
			{"id": 5, "s": 500, "n": 1},
		},
	},
	{
		// Both bounds, on an epoch-seconds column read as a number.
		Name:     "window_range_epoch_both_bounds",
		Pipeline: `{{.bin}} from csv {{.data}}/epochs.csv | {{.bin}} window -order ts -range-preceding 20 -range-following 10 -sum v s -count n | {{.bin}} include id s n`,
		Ordered:  false,
	},
	{
		// RANGE 0 PRECEDING is the current row AND its peers (level 6 appears
		// twice); UNBOUNDED FOLLOWING counts everything at or above.
		Name:     "window_range_peers_unbounded",
		Pipeline: `{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -order level -range-preceding 0 -range-following unbounded -count peers_and_above | {{.bin}} include name level peers_and_above`,
		Ordered:  false,
		Golden: []map[string]any{
			{"name": "Bob", "level": 4, "peers_and_above": 7},
			{"name": "David", "level": 5, "peers_and_above": 6},
			{"name": "Eve", "level": 6, "peers_and_above": 5},
			{"name": "Grace", "level": 6, "peers_and_above": 5},
			{"name": "Alice", "level": 7, "peers_and_above": 3},
			{"name": "Frank", "level": 8, "peers_and_above": 2},
			{"name": "Carol", "level": 9, "peers_and_above": 1},
		},
	},
	{
		// A field named like an expr builtin (`date`) is the FIELD when used
		// bare in an expression, in every lane: exec's VM patches it to
		// $env["date"], the transpiled lanes always read it as a field, SQL
		// sees a column. Until v4.94.0 exec refused to compile it.
		Name:     "expr_field_named_like_builtin",
		Pipeline: `{{.bin}} from csv {{.data}}/dated.csv | {{.bin}} where -if-expr 'date >= "2026-02-01"' | {{.bin}} include id amount`,
		Ordered:  false,
		Golden: []map[string]any{
			{"id": 3, "amount": 300},
			{"id": 5, "amount": 500},
			{"id": 4, "amount": 400},
		},
	},
	{
		// `limit 0` is SQL's LIMIT 0: no records in every lane (it was
		// the pass-through dial until v4.92.0).
		Name:     "limit_zero_is_empty",
		Pipeline: `{{.bin}} from csv {{.data}}/shuffled.csv | {{.bin}} where -if pop gt 15 | {{.bin}} limit 0`,
		Ordered:  false,
		Golden:   []map[string]any{},
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
