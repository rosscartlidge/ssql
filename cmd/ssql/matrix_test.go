package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/rosscartlidge/ssql/v4/internal/lint/typedomain"
)

// TestTypeOperationMatrix is DFC163 §2.1: every declarable type × every
// argument that takes a field × every lane. Every type ssql added after
// the first four found bugs of one shape late: one type meeting one
// operation in one lane, which no hand-written case enumerated. The
// cells are read from `ssql -spec-json` (the CLI's own description of
// itself, DFC115), so a new command or flag joins with no edit here; a
// new type adds a fixture column and a row to matrixTypes.
//
// The oracles need no hand-written answers:
//   - loud or working: a cell runs, or every lane refuses it with an
//     Error line; generated Go that does not compile, or a Go panic, is
//     a failure;
//   - the lanes agree (runEquivCase, every lane the environment has);
//   - the type survives: an input column still in the output keeps its
//     wire type, unless the stage names it as a result (the stringified
//     time or decimal the eye cannot see in printed text);
//   - refusals agree: exec refusing while another lane answers is a
//     failure.
//
// A cell whose arguments cannot be filled from the spec and the override
// table is "not covered" and counted in the report, never skipped
// silently. Opt-in (Ross, 2026-11-05: pre-tag only): SSQL_MATRIX=1.
// SSQL_MATRIX_BIN tests another binary (DFC163 §2.1's acceptance run
// against an older release); SSQL_MATRIX_REPORT names the report file;
// SSQL_MATRIX_ONLY runs only the cells whose name contains it.
func TestTypeOperationMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("builds programs per cell")
	}
	if os.Getenv("SSQL_MATRIX") == "" {
		t.Skip("opt-in: SSQL_MATRIX=1 (DFC163 §2.1, a pre-tag gate)")
	}
	bin := os.Getenv("SSQL_MATRIX_BIN")
	if bin == "" {
		bin = corpusBin(t)
	}
	data := corpusData(t)
	cells, notCovered := matrixCells(t, bin)
	only := os.Getenv("SSQL_MATRIX_ONLY")

	report := &matrixReport{outcomes: map[string]map[string]map[string]int{}}
	for _, nc := range notCovered {
		report.add(nc.cmd, nc.typ, "not covered", nc.cmd+" "+nc.flag+": "+nc.reason)
	}
	t.Cleanup(func() { report.write(t, len(cells)) })

	repl := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data)
	t.Run("cells", func(t *testing.T) {
		for _, c := range cells {
			if only != "" && !strings.Contains(c.name, only) {
				continue
			}
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				if why := matrixKnownGap(c.name); why != "" && os.Getenv("SSQL_MATRIX_KNOWN") == "" {
					report.add(c.cmd, c.typ.col, "known", c.name+": "+why)
					t.Skip("known gap: " + why)
				}
				pipeline := repl.Replace(c.pipeline)
				outcome := "fail"
				defer func() {
					if outcome == "fail" || t.Failed() {
						report.add(c.cmd, c.typ.col, "fail", c.pipeline)
					} else {
						report.add(c.cmd, c.typ.col, outcome, "")
					}
				}()
				outcome = runMatrixCell(t, bin, pipeline, c)
			})
		}
	})
}

// matrixKnown are the gaps found and not yet fixed, each a pattern over
// cell names and the reason (and its TODO): such a cell is skipped and
// counted as known, so the gate is green and any NEW failure is red.
// SSQL_MATRIX_KNOWN=1 runs them anyway, to find the entries a fix has
// made stale; delete an entry with its fix.
var matrixKnown = []struct{ pattern, why string }{
	// SQL lanes: refusals the interpreter makes and DuckDB does not.
	{`^(group-by|window)_(sum|avg)_(b|t)(_empties)?$`, "SQL answers a sum or average of a bool or a time; every lane must refuse (Ross, DFC163 §7)"},
	{`^(group-by|window)_(median|percentile)_(b|ip|s|t)(_empties)?$`, "SQL answers a median or percentile of a non-number; exec refuses (DFC163 §7)"},
	// SQL lanes: a value's text or kind.
	{`^(window_(mode|arg-max|arg-min)|unpivot_value|update_set)_ip(_empties)?$`, "SQL: an inet result of -mode, -arg-*, unpivot or update -set prints DuckDB's INET struct (its kind is not recorded)"},
	{`^(group-by|window)_string-agg_t(_empties)?$`, "SQL: string-agg of a time uses the engine's text, not RFC 3339"},
	{`^group-by_collect_`, "SQL: -collect lists a time, an inet or a decimal in the engine's own form"},
	{`^describe_FIELDS_(w|d)(_empties)?$`, "describe: an int128's max is wrong in every lane; SQL calls a decimal float"},
	{`^update_set_f(_empties)?$`, "SQL: update -set of a literal into a float column writes text"},
	// Missing values.
	{`^(sort_FIELDS|window_order)_.*_empties$`, "a missing value orders first in exec and last in SQL (NULLS LAST)"},
	{`^window_min_.*_empties$`, "window -min over a running frame is missing at a row whose own value is missing"},
	{`^window_(lag|lead)-default_`, "lag/lead -default where the neighbour exists but is missing: exec fills the default; Ross chose SQL's (default only past the edge, DFC163 §7)"},
	{`^(group-by|window)_(first|last)_.*_empties$`, "first/last skip a missing value in exec; Ross chose that, and the SQL lanes take the last row's NULL (DFC163 §7)"},
	// Floats.
	{`^(group-by|window)_(variance|stddev|median|percentile)_(i|f|d|w)(_empties)?$`, "variance, stddev and percentile interpolation differ from DuckDB in the last digit; Ross chose to adopt the engines' formulas (DFC163 §7)"},
	// Commands with several gaps.
	{`^extract_field_`, "extract: exec's error has no Error: prefix; typed refuses a non-string field exec reads as text"},
	{`^resample_`, "resample: int128/decimal values and axes (Ross: support both), a missing timestamp exec refuses, typed -time i does not compile"},
	{`^update_set-bucket_`, "bucket over int128/decimal (Ross: support), numeric widths, SQL answers where exec refuses"},
	{`^update_set_(d|ip|j)(_empties)?$`, "update -set literal: record codegen ignores the column's declared type; typed refuses a decimal or inet column"},
	// The harness.
	{`^top_field_(b|s)(_empties)?$`, "top over ties: which tied rows win is arbitrary"},
}

// matrixKnownGap is the reason a cell is a known gap, or "".
func matrixKnownGap(name string) string {
	for _, k := range matrixKnown {
		if regexp.MustCompile(k.pattern).MatchString(name) {
			return k.why
		}
	}
	return ""
}

// matrixType is one declarable type: its fixture column, its -type word,
// its domain member (typedomain's name; "" for json, which is no scalar)
// and a literal whose text order differs from its value order.
type matrixType struct{ col, typ, member, lit string }

var matrixTypes = []matrixType{
	{"i", "int", "int64", "10"},
	{"w", "int128", "int128", "20000000000000000000"},
	{"d", "decimal:2", "decimal", "9.75"},
	{"f", "float", "float64", "9.75"},
	{"s", "string", "string", "b"},
	{"b", "bool", "bool", "true"},
	{"t", "time", "time", "2026-01-10"},
	{"ip", "inet", "inet", "10.0.0.0"},
	{"j", "json", "", "[1]"},
}

// matrixCSV: k a distinct key, g a two-valued group, a column per type.
// Shuffled; text order is not value order in every column (9 < 10 <
// 100, 9.0.0.1 < 10.0.0.0/8, times in three offsets whose text order is
// not their instant order); row 4 repeats row 3. No empty cell: the
// typed reader refuses one in a non-string field by design (DFC124 §3),
// so the empty row has its own fixture and pass (matrixEmptiesCSV).
const matrixCSV = "k,g,s,i,w,d,f,b,t,j,ip\n" +
	"3,x,b,9,9,9.50,9.5,true,2026-01-09T00:00:00Z,\"[1,2]\",9.0.0.1\n" +
	"1,y,a10,10,100000000000000000000,10.25,10.25,false,2026-01-10T23:00:00+11:00,\"{\"\"a\"\":1}\",10.0.0.9\n" +
	"6,x,B,-3,-5,-3.10,-3.1,true,2025-12-31T20:00:00-05:00,[],::1\n" +
	"2,y,a9,100,20000000000000000000,100.00,100.5,false,2026-02-01T00:00:00Z,[3],10.0.0.0/8\n" +
	"4,y,b,9,9,9.50,9.5,true,2026-01-09T00:00:00Z,\"[1,2]\",9.0.0.1\n"

// matrixEmptiesCSV is matrixCSV with a row empty past its key and group:
// the empties pass runs every cell over it in exec and the SQL lanes
// (an absent value meeting every operation).
// Its text columns hold values: an empty text cell is "" by design
// (DFC124 §2) where an SQL engine reads NULL, a known divergence, not
// this pass's subject. The json cell holds one too: outside a json cell
// the column is undeclared text.
const matrixEmptiesCSV = matrixCSV + "5,x,e,,,,,,,[0],\n"

// matrixNoGoLanes skips the Go lanes for the empties pass.
var matrixNoGoLanes = map[string]string{
	"go-record": "the empties pass runs exec and the SQL lanes", "go-typed": "the typed reader refuses an empty cell (DFC124 §3)",
	"go-parallel": "the typed reader refuses an empty cell (DFC124 §3)",
}

// matrixFrom is the source a cell of type col reads: the fixture with
// every column declared — except the json column outside a json cell,
// since a declared json column has no SQL translation yet and would
// skip the SQL lanes of every cell (the first run's silent gap).
func matrixFrom(file, col string) string {
	parts := []string{"{{.bin}} from csv -type k int -type g string"}
	for _, ty := range matrixTypes {
		if ty.typ == "json" && col != ty.col || matrixSkippedType(ty.col) {
			continue
		}
		parts = append(parts, "-type "+ty.col+" "+ty.typ)
	}
	return strings.Join(parts, " ") + " {{.data}}/" + file
}

// matrixSkippedType: SSQL_MATRIX_SKIP_TYPES names fixture columns (space
// separated) to neither declare nor test — for running the matrix against
// an older release (SSQL_MATRIX_BIN) that does not know a type: v4.137.2
// has no inet, so its every cell would refuse at the source.
func matrixSkippedType(col string) bool {
	return slices.Contains(strings.Fields(os.Getenv("SSQL_MATRIX_SKIP_TYPES")), col)
}

// matrixInputTypes is each fixture column's wire type after the source
// of a cell of type col.
func matrixInputTypes(col string) map[string]string {
	out := map[string]string{"k": "int", "g": "string"}
	for _, ty := range matrixTypes {
		if ty.typ == "json" && col != ty.col {
			out[ty.col] = "string"
			continue
		}
		out[ty.col] = ty.typ
	}
	return out
}

// TestMatrixTypesCoverTheDomain: the matrix has a row for every scalar
// the domain lint knows (and json), so a new type cannot join the lint
// without joining the matrix.
func TestMatrixTypesCoverTheDomain(t *testing.T) {
	var got []string
	for _, ty := range matrixTypes {
		if ty.member != "" {
			got = append(got, ty.member)
		}
	}
	want := typedomain.Members()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("matrixTypes members %v; the domain lint's are %v — add a fixture column and a row", got, want)
	}
	header := strings.Split(strings.SplitN(matrixCSV, "\n", 2)[0], ",")
	for _, ty := range matrixTypes {
		if !slices.Contains(header, ty.col) {
			t.Errorf("matrixCSV has no column %q", ty.col)
		}
	}
}

type matrixCell struct {
	name, cmd, flag string
	empties         bool // the empties pass: exec and the SQL lanes
	typ             matrixType
	pipeline        string
	ordered         bool
	results         []string // the result names the stage creates
	retypes         []string // input columns the stage may change the type of
}

type matrixGap struct{ cmd, flag, typ, reason string }

// matrixSpecFlag is one flag of -spec-json, as the matrix needs it.
type matrixSpecFlag struct {
	Names []string `json:"names"`
	Args  []struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		Completer string `json:"completer"`
		Hint      string `json:"hint"`
	} `json:"args"`
}

// matrixCells enumerates the cells: every flag argument with the fields
// completer, crossed with every type.
func matrixCells(t *testing.T, bin string) ([]matrixCell, []matrixGap) {
	out, err := exec.Command(bin, "-spec-json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Subcommands map[string]struct {
			Flags []matrixSpecFlag `json:"flags"`
		} `json:"subcommands"`
	}
	if err := json.Unmarshal(out, &spec); err != nil {
		t.Fatal(err)
	}
	var cells []matrixCell
	var gaps []matrixGap
	names := make([]string, 0, len(spec.Subcommands))
	for n := range spec.Subcommands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, cmd := range names {
		for _, f := range spec.Subcommands[cmd].Flags {
			takesField := false
			for _, a := range f.Args {
				takesField = takesField || a.Completer == "fields"
			}
			if !takesField {
				continue
			}
			flag := f.Names[0]
			for _, ty := range matrixTypes {
				if matrixSkippedType(ty.col) {
					continue
				}
				variants, reason := matrixStages(cmd, flag, f, ty)
				if reason != "" {
					gaps = append(gaps, matrixGap{cmd, flag, ty.col, reason})
					continue
				}
				for i, v := range variants {
					name := cmd + "_" + strings.TrimPrefix(flag, "-") + "_" + ty.col
					if len(variants) > 1 {
						name += fmt.Sprintf("_%d", i+1)
					}
					v.name, v.cmd, v.flag, v.typ = name, cmd, flag, ty
					stage := v.pipeline
					v.pipeline = matrixFrom("matrix.csv", ty.col) + " | {{.bin}} " + stage
					cells = append(cells, v)
					e := v
					e.name, e.empties = name+"_empties", true
					e.pipeline = matrixFrom("matrix_empties.csv", ty.col) + " | {{.bin}} " + stage
					cells = append(cells, e)
				}
			}
		}
	}
	return cells, gaps
}

// matrixOverrides are the stages the spec alone cannot fill: a command
// whose grammar needs more than the flag under test. {col} is the type's
// column, {lit} its literal, {right} a typed one-column right side.
var matrixOverrides = map[string][]string{
	"top -field":         {"top 2 -field {col}"},
	"sort FIELDS":        {"sort {col} k"},
	"group-by FIELDS":    {"group-by {col} -count c"},
	"describe FIELDS":    {"describe {col}"},
	"include FIELDS":     {"include k {col}"},
	"exclude FIELDS":     {"exclude {col}"},
	"explode FIELD":      {"explode {col}"},
	"flatten FIELD":      {"flatten {col}"},
	"cast -type":         {"cast -type {col} string"},
	"rename -as":         {"rename -as {col} r1"},
	"fill -down":         {"fill -down {col}"},
	"fill -default":      {"fill -default {col} {lit}"},
	"where -if":          {"where -if {col} eq {lit}", "where -if {col} lt {lit}"},
	"where -if-field":    {"where -if-field {col} eq {col}"},
	"update -if":         {"update -if {col} lt {lit} -set g z"},
	"update -if-field":   {"update -if-field {col} eq {col} -set g z"},
	"update -set":        {"update -set {col} {lit}"},
	"update -set-expr":   {"update -set-expr {col} '{col}'"},
	"update -set-bucket": {"update -set-bucket r1 {col} 24h"},
	"update -set-field":  {"update -set-field r1 {col}"},
	"pivot -row":         {"pivot -row {col} -col g -val k"},
	"pivot -col":         {"pivot -row g -col {col} -val k"},
	"pivot -val":         {"pivot -row g -col s -val {col}"},
	"unpivot -id":        {"unpivot -id k -id {col} -value g"},
	"unpivot -value":     {"unpivot -id k -value {col}"},
	"join -using":        {"join {right} -using {col}"},
	"join -on":           {"join {right} -on {col} {col}"},
	"except -using":      {"except -file {right} -using {col}"},
	"except -on":         {"except -file {right} -on {col} {col}"},
	"intersect -using":   {"intersect -file {right} -using {col}"},
	"intersect -on":      {"intersect -file {right} -on {col} {col}"},
	"window -order":      {"window -order {col} -row-number r1"},
	"window -partition":  {"window -partition {col} -order k -row-number r1"},
	"resample -time":     {"resample -time {col} -every 24h -value i"},
	"resample -value":    {"resample -time t -every 24h -value {col}"},
	"correlate -with":    {"correlate -field i -with {col}"},
	"correlate -field":   {"correlate -field {col} -auto"},
	"convolve -field":    {"convolve -field {col} -auto"},
	"ifft -magnitude":    {"ifft -magnitude {col} -phase f"},
	"ifft -phase":        {"ifft -magnitude f -phase {col}"},
	"extract -field":     {"extract -field {col} -re '(?P<r1>[0-9])'"},
	"group-by -arg-max":  {"group-by g -arg-max {col} {col} r1"},
	"group-by -arg-min":  {"group-by g -arg-min {col} {col} r1"},
	"window -arg-max":    {"window -order k -arg-max {col} {col} r1"},
	"window -arg-min":    {"window -order k -arg-min {col} {col} r1"},
}

// matrixUncovered names the flags with no fill yet, and why: each is a
// counted gap in the report.
var matrixUncovered = map[string]string{
	"from -format":         "a source flag (the source is fixed)",
	"cast -format":         "a time pattern for one type",
	"merge -by":            "merges several sorted files",
	"merge -if":            "merges several sorted files",
	"traverse -from":       "needs an edge graph",
	"traverse -to":         "needs an edge graph",
	"traverse -sum":        "needs an edge graph",
	"traverse -lowest-sum": "needs an edge graph",
	"join -asof":           "an as-of axis is a time or a number by design",
	"join -asof-on":        "an as-of axis is a time or a number by design",
	"update -param-field":  "a parameter needs an expression that uses it",
	"where -param-field":   "a parameter needs an expression that uses it",
	"update -if-macro":     "needs a macro file",
	"update -set-macro":    "needs a macro file",
	"where -if-macro":      "needs a macro file",
}

// matrixStages builds a cell's stage text (one or more variants), or the
// reason it cannot.
func matrixStages(cmd, flag string, f matrixSpecFlag, ty matrixType) ([]matrixCell, string) {
	key := cmd + " " + flag
	if why, ok := matrixUncovered[key]; ok {
		return nil, why
	}
	fill := func(s string) string {
		right := "<(" + matrixFrom("matrix.csv", ty.col) + " | {{.bin}} include " + ty.col + " k | {{.bin}} rename -as k rk)"
		return strings.NewReplacer("{col}", ty.col, "{lit}", ty.lit, "{right}", right).Replace(s)
	}
	var stages []string
	if o, ok := matrixOverrides[key]; ok {
		stages = o
	} else {
		// From the spec: the field under test, a result name, a small
		// number, a separator, the type's literal.
		var args []string
		res := 0
		for _, a := range f.Args {
			switch {
			case a.Completer == "fields":
				args = append(args, "{col}")
			case strings.Contains(a.Name, "result") || a.Name == "new-name":
				res++
				args = append(args, fmt.Sprintf("r%d", res))
			case a.Name == "n":
				args = append(args, "1")
			case a.Name == "p":
				args = append(args, "0.5")
			case a.Name == "sep":
				args = append(args, "';'")
			case a.Name == "default" || a.Name == "value":
				args = append(args, "{lit}")
			default:
				return nil, "argument " + a.Name + " has no fill"
			}
		}
		prefix := cmd
		switch cmd {
		case "group-by":
			prefix = "group-by g"
		case "window":
			prefix = "window -order k"
		}
		stage := prefix
		if strings.HasPrefix(flag, "-") {
			stage += " " + flag
		}
		stages = []string{stage + " " + strings.Join(args, " ")}
	}
	var cells []matrixCell
	for _, s := range stages {
		c := matrixCell{pipeline: fill(s), ordered: cmd == "sort" || cmd == "top"}
		for _, w := range strings.Fields(c.pipeline) {
			if len(w) >= 2 && w[0] == 'r' && w[1] >= '1' && w[1] <= '9' {
				c.results = append(c.results, w)
			}
		}
		// The stage may retype the column it names: cast and update by
		// design, rename moves it. Resample averages its value columns
		// into floats; pivot names its new columns after values, which
		// may be an input column's name (s holds "b").
		switch cmd {
		case "cast", "update", "rename":
			c.retypes = []string{ty.col}
		case "resample":
			c.retypes = []string{ty.col, "i"}
		case "pivot":
			for col := range matrixInputTypes(ty.col) {
				c.retypes = append(c.retypes, col)
			}
		}
		cells = append(cells, c)
	}
	return cells, ""
}

// runMatrixCell runs one cell and returns its outcome: "pass" (every
// lane agrees, the types survive) or "refused" (every lane refused).
// A failure is reported through t.
func runMatrixCell(t *testing.T, bin, pipeline string, c matrixCell) string {
	env := []string{"TZ=UTC"}
	stdout, stderr, err := matrixShell(pipeline, env)
	if sweepRuntimeError(stderr) {
		t.Fatalf("exec: a Go runtime error reached the user:\n%s\n  %s", stderr, pipeline)
	}
	if err != nil {
		if !strings.Contains(stderr, "Error") {
			t.Fatalf("exec failed without an Error line (%v):\n%s\n  %s", err, stderr, pipeline)
		}
		matrixRefusalsAgree(t, bin, pipeline, env, c.empties)
		return "refused"
	}
	matrixTypesSurvive(t, stdout, c)
	ec := EquivCase{Name: c.name, Pipeline: pipeline, Ordered: c.ordered, Skip: map[string]string{}}
	if c.empties {
		for lane, why := range matrixNoGoLanes {
			ec.Skip[lane] = why
		}
	}
	// The decimal column passes through every cell (unless the stage
	// retypes it); in a decimal cell, the results that keep a value's type
	// and an unpivot's value column are decimals too.
	if !(c.cmd == "cast" && c.typ.col == "d") { // cast -type d string makes it text
		ec.Decimals = []string{"d"}
	}
	if c.typ.col == "d" && matrixKeepsType[strings.TrimPrefix(c.flag, "-")] {
		ec.Decimals = append(ec.Decimals, append([]string{"value"}, c.results...)...)
	}
	// A stage with no SQL translation skips the SQL lanes, as a case's
	// Skip["duckdb"] does: that is a known gap, not this oracle's.
	if _, se, err := matrixShell("export SSQL_MODE=record && "+pipeline+" | "+bin+" to jsonl | "+bin+" generate sql", env); err != nil {
		if sweepRuntimeError(se) {
			t.Fatalf("generate sql: a Go runtime error:\n%s", se)
		}
		ec.Skip["duckdb"] = strings.TrimSpace(se)
		runEquivCase(t, bin, pipeline, ec)
		return "pass, no SQL"
	}
	runEquivCase(t, bin, pipeline, ec)
	return "pass"
}

// matrixKeepsType: the flags whose result is one of the field's values,
// or a sum of them (a decimal's sum is a decimal).
var matrixKeepsType = map[string]bool{
	"sum": true, "min": true, "max": true, "first": true, "last": true, "any": true, "mode": true,
	"lag": true, "lead": true, "lag-default": true, "lead-default": true, "nth-value": true,
	"arg-max": true, "arg-min": true, "value": true, "set-field": true, "as": true,
}

// matrixTypesSurvive: every input column still in the stage's output,
// other than its results and the column it may retype, has the input's
// wire type.
func matrixTypesSurvive(t *testing.T, out string, c matrixCell) {
	line, _, _ := strings.Cut(out, "\n")
	var hdr struct {
		Schema struct {
			Types map[string]string `json:"types"`
		} `json:"_schema"`
	}
	if json.Unmarshal([]byte(line), &hdr) != nil || hdr.Schema.Types == nil {
		return // not a record stream (describe's summary, a signal command's table)
	}
	for col, in := range matrixInputTypes(c.typ.col) {
		if matrixSkippedType(col) {
			continue
		}
		got, kept := hdr.Schema.Types[col]
		if !kept || slices.Contains(c.results, col) || slices.Contains(c.retypes, col) {
			continue
		}
		if got != in {
			t.Errorf("type survives: column %q was %s and is %s after the stage", col, in, got)
		}
	}
}

// matrixRefusalsAgree: exec refused, so every Go lane and DuckDB must
// refuse too (at generation, build is NOT a refusal: generated code that
// does not compile is a failure), and none may panic.
func matrixRefusalsAgree(t *testing.T, bin, pipeline string, env []string, empties bool) {
	modes := []string{"record", "typed", "parallel"}
	if empties {
		modes = nil
	}
	for _, mode := range modes {
		src, se, err := matrixShell("export SSQL_MODE="+mode+" && "+pipeline+" | "+bin+" to jsonl | "+bin+" generate go", env)
		if sweepRuntimeError(se) {
			t.Errorf("go-%s: generate go panicked:\n%s", mode, se)
			continue
		}
		if err != nil {
			continue // refused at generation, loudly
		}
		out, buildErr, runErr := goBuildRunGenerated(t, "matrix", map[string]string{"main.go": src}, env)
		switch {
		case buildErr != nil:
			t.Errorf("go-%s: exec refused, and the generated program does not compile:\n%s", mode, out)
		case sweepRuntimeError(out):
			t.Errorf("go-%s: the generated program panicked:\n%s", mode, out)
		case runErr == nil:
			t.Errorf("go-%s: exec refused, but the generated program answered:\n%s", mode, matrixHead(out))
		}
	}
	if duckdb := duckdbBinary(); duckdb != "" {
		sql, se, err := matrixShell("export SSQL_MODE=record && "+pipeline+" | "+bin+" to jsonl | "+bin+" generate sql", env)
		if sweepRuntimeError(se) {
			t.Errorf("duckdb: generate sql panicked:\n%s", se)
			return
		}
		if err != nil {
			return
		}
		cmd := exec.Command(duckdb, "-json", "-c", sql)
		var o bytes.Buffer
		cmd.Stdout = &o
		if cmd.Run() == nil && strings.TrimSpace(o.String()) != "" && strings.TrimSpace(o.String()) != "[]" {
			t.Errorf("duckdb: exec refused, but DuckDB answered:\n%s\n  sql:\n%s", matrixHead(o.String()), sql)
		}
	}
}

func matrixShell(script string, env []string) (string, string, error) {
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func matrixHead(s string) string {
	if lines := strings.SplitN(s, "\n", 6); len(lines) > 5 {
		return strings.Join(lines[:5], "\n") + "\n…"
	}
	return s
}

// matrixReport is the grid: per command, per type, the count of each
// outcome; and every failing or uncovered cell, named.
type matrixReport struct {
	mu       sync.Mutex
	outcomes map[string]map[string]map[string]int // cmd → type → outcome → n
	notes    []string
}

func (r *matrixReport) add(cmd, typ, outcome, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outcomes[cmd] == nil {
		r.outcomes[cmd] = map[string]map[string]int{}
	}
	if r.outcomes[cmd][typ] == nil {
		r.outcomes[cmd][typ] = map[string]int{}
	}
	r.outcomes[cmd][typ][outcome]++
	if note != "" {
		r.notes = append(r.notes, outcome+": "+note)
	}
}

func (r *matrixReport) write(t *testing.T, cells int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := os.Getenv("SSQL_MATRIX_REPORT")
	if path == "" {
		path = filepath.Join(os.TempDir(), "ssql-matrix-report.md")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Type × operation matrix (DFC163 §2.1)\n\n%d cells. Each entry: pass / pass with no SQL translation / refused by every lane / known gap / not covered / fail.\n\n", cells)
	b.WriteString("| command |")
	for _, ty := range matrixTypes {
		b.WriteString(" " + ty.col + " (" + ty.typ + ") |")
	}
	b.WriteString("\n|---|" + strings.Repeat("---|", len(matrixTypes)) + "\n")
	cmds := make([]string, 0, len(r.outcomes))
	for c := range r.outcomes {
		cmds = append(cmds, c)
	}
	sort.Strings(cmds)
	totals := map[string]int{}
	for _, c := range cmds {
		b.WriteString("| " + c + " |")
		for _, ty := range matrixTypes {
			o := r.outcomes[c][ty.col]
			fmt.Fprintf(&b, " %d/%d/%d/%d/%d/%d |", o["pass"], o["pass, no SQL"], o["refused"], o["known"], o["not covered"], o["fail"])
			for k, n := range o {
				totals[k] += n
			}
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nTotals: %d pass, %d pass with no SQL, %d refused, %d known gaps, %d not covered, %d fail.\n\n", totals["pass"], totals["pass, no SQL"], totals["refused"], totals["known"], totals["not covered"], totals["fail"])
	notes := slices.Clone(r.notes)
	sort.Strings(notes)
	for _, n := range notes {
		b.WriteString("- " + n + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Logf("matrix report: %v", err)
		return
	}
	t.Logf("matrix: %d pass (%d of them with no SQL translation), %d refused, %d known gaps, %d not covered, %d fail — report %s", totals["pass"]+totals["pass, no SQL"], totals["pass, no SQL"], totals["refused"], totals["known"], totals["not covered"], totals["fail"], path)
}
