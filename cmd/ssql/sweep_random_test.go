package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRandomDifferential is DFC133 instrument 3: random pipelines over
// random adversarial tables, the interpreted lane against DuckDB running
// `generate sql` — an engine that shares nothing with ssql. The cases in
// TestPipelineEquivalence are hand-written and see only what someone
// thought to try; this explores the same space without imagination, then
// SHRINKS a disagreement (drop stages, then rows) to the smallest pipeline
// and table that still disagree, and prints them with the seed. Every
// real finding is promoted to a permanent equivalence case.
//
//	SSQL_FUZZ=500 [SSQL_FUZZ_SEED=133] go test ./cmd/ssql -run TestRandomDifferential -v
//
// Known engine differences are constraints on the GENERATOR (§5), never
// exceptions in the oracle: no arrival-order aggregates, positional limits
// only after a sort on the unique id, floats compared to 12 digits, no
// string↔number casts, no zoned time strings.
func TestRandomDifferential(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("SSQL_FUZZ"))
	if n <= 0 {
		t.Skip("opt-in: SSQL_FUZZ=<number of pipelines> (DFC133)")
	}
	duckdb := duckdbBinary()
	if duckdb == "" {
		t.Skip("duckdb not found — it is this test's oracle")
	}
	seed := int64(133)
	if s, err := strconv.ParseInt(os.Getenv("SSQL_FUZZ_SEED"), 10, 64); err == nil {
		seed = s
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	rd := &randDiff{bin: bin, duckdb: duckdb, dir: dir}

	type job struct {
		i    int
		seed int64
	}
	jobs := make(chan job)
	var mu sync.Mutex
	var findings []string
	seen := map[string]bool{}
	var agreed, refused, bothFailed int
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				rng := rand.New(rand.NewSource(j.seed))
				tbl := genTable(rng)
				stages := genPipeline(rng, tbl)
				verdict, detail := rd.compare(j.i, tbl, stages)
				mu.Lock()
				switch verdict {
				case "agree":
					agreed++
				case "refused":
					refused++
				case "both-fail":
					bothFailed++
				default:
					mu.Unlock()
					sTbl, sStages := rd.shrink(j.i, tbl, stages, verdict)
					_, sDetail := rd.compare(j.i, sTbl, sStages)
					if sDetail == "" {
						sDetail = detail
					}
					sig := verdict + "|" + stageKinds(sStages)
					mu.Lock()
					if !seen[sig] {
						seen[sig] = true
						findings = append(findings, fmt.Sprintf("%s (seed %d, case %d)\n  pipeline: %s\n  table:\n%s\n%s",
							verdict, j.seed, j.i, strings.Join(sStages, " | "), indent(sTbl.csv(), "    "), sDetail))
					}
				}
				mu.Unlock()
			}
		}()
	}
	master := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		jobs <- job{i, master.Int63()}
	}
	close(jobs)
	wg.Wait()

	slices.Sort(findings)
	for _, f := range findings {
		t.Errorf("DISAGREE: %s", f)
	}
	t.Logf("random differential: %d pipelines (seed %d): %d agree, %d refused by generate sql, %d failed in both lanes, %d distinct findings",
		n, seed, agreed, refused, bothFailed, len(findings))
}

// ---- tables ----

type randTable struct {
	cols []string
	kind map[string]string // int | float | string | bool
	rows [][]string        // "" = NULL
}

func (tb *randTable) csv() string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Write(tb.cols)
	w.WriteAll(tb.rows)
	return b.String()
}

func (tb *randTable) clone() *randTable {
	c := &randTable{cols: slices.Clone(tb.cols), kind: tb.kind}
	for _, r := range tb.rows {
		c.rows = append(c.rows, slices.Clone(r))
	}
	return c
}

var (
	randInts    = []string{"0", "1", "-1", "2", "3", "7", "10", "100", "-5", "9007199254740993"}
	randFloats  = []string{"0.5", "1.5", "2.0", "-0.25", "3.75", "10.0", "0.1", "100.5"}
	randStrings = []string{"a", "b", "ab", "abc", "Oslo", "lima", "x y", "12", "007", "é", "A", "zz", "a_b", "50%"}
	// randHostile are values that are syntax in some grammar between the
	// document and the engine: SQL quoting and comments, shell quoting, CSV
	// quoting, LIKE metacharacters, expr-lang operators. DFC134's claim is
	// that none of them can be anything but a value; this is where it is
	// tested at volume, in every literal slot and every cell.
	randHostile = []string{`'`, `''`, `'; DROP TABLE t; --`, `x' OR '1'='1`, `--`, `/* c */`, `"`, `a"b`, `\`, `%`, `_`, `a,b`, `$1`, `${x}`, `x" || true || "`, `;`, `NULL`, `null`, `1e3`, `0x1`}
	// randHostileNames are column names that need quoting in SQL and are
	// still plain words to a shell: reserved words, a dash, a dot, a digit
	// first. (A name beginning with - or + is left to the -arg cases: the
	// SQL translators refuse it loudly, DFC134 §5.2a.3.)
	randHostileNames = []string{"select", "from", "where", "order", "group", "x-y", "a.b", "1st", "Mixed", "ünï", "s"}
	randBools        = []string{"true", "false"}
)

// genTable: 3–10 rows over id (unique, never null) plus one column of each
// kind, with NULLs weighted in — including, sometimes, the whole first row
// and a whole column — and duplicate keys so group-by has groups.
func genTable(rng *rand.Rand) *randTable {
	sName := "s"
	if rng.Intn(3) == 0 {
		sName = pick(rng, randHostileNames)
	}
	tb := &randTable{cols: []string{"id", "k", "n", "f", sName, "b"},
		kind: map[string]string{"id": "int", "k": "int", "n": "int", "f": "float", sName: "string", "b": "bool"}}
	pool := randStrings
	if rng.Intn(3) == 0 {
		pool = append(append([]string{}, randStrings...), randHostile...)
	}
	nrows := 3 + rng.Intn(8)
	nullCol := -1
	if rng.Intn(6) == 0 {
		nullCol = 2 + rng.Intn(4) // n, f, s or b entirely NULL
	}
	for i := 0; i < nrows; i++ {
		row := []string{strconv.Itoa(i + 1), strconv.Itoa(1 + rng.Intn(3)), pick(rng, randInts), pick(rng, randFloats), pick(rng, pool), pick(rng, randBools)}
		for c := 1; c < len(row); c++ {
			if c == nullCol || rng.Intn(5) == 0 || (i == 0 && rng.Intn(3) == 0) {
				row[c] = ""
			}
		}
		tb.rows = append(tb.rows, row)
	}
	// A typed column needs at least one value, or the engines are free to
	// disagree about the type of nothing; an all-NULL column is kept only
	// when chosen on purpose above.
	for c := 1; c < len(tb.cols); c++ {
		if c == nullCol {
			// A column with no values at all has no type to agree on: ssql
			// and DuckDB both read it as text. The generator treats it as
			// text too (so: no numeric aggregates over it, no conditions).
			tb.kind[tb.cols[c]] = "string"
			continue
		}
		has := false
		for _, r := range tb.rows {
			has = has || r[c] != ""
		}
		if !has {
			tb.rows[len(tb.rows)-1][c] = map[string]string{"int": "4", "float": "4.5", "string": "q", "bool": "true"}[tb.kind[tb.cols[c]]]
		}
	}
	// The string column must LOOK like text to both engines: a column whose
	// only values are "12" and "1e3" is an int/float column to ssql's
	// inference and to DuckDB's, and the generator's later string
	// assignment to it is then a type error, not a finding.
	sc := 4
	textual := false
	for _, r := range tb.rows {
		if v := r[sc]; v != "" {
			if _, err := strconv.ParseFloat(strings.TrimPrefix(v, "0x"), 64); err != nil && strings.ToLower(v) != "true" && strings.ToLower(v) != "false" {
				textual = true
			}
		}
	}
	if !textual {
		tb.rows[len(tb.rows)-1][sc] = "q"
	}
	return tb
}

func pick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

// pos writes a positional either bare or in its flag form (-arg VALUE,
// DFC134 §5.2), half and half: the form a program emits must mean the same
// as the one a person types in every lane, the SQL one included.
func pos(rng *rand.Rand, v string) string {
	if rng.Intn(2) == 0 {
		return v
	}
	return "-arg " + v
}

func posAll(rng *rand.Rand, vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = pos(rng, v)
	}
	return strings.Join(out, " ")
}

// ---- pipelines ----

// genPipeline draws 1–4 stages, tracking the current columns and their
// kinds the way the schema flows, so stages reference fields that exist.
func genPipeline(rng *rand.Rand, tb *randTable) []string {
	cols := slices.Clone(tb.cols)
	kind := map[string]string{}
	for k, v := range tb.kind {
		kind[k] = v
	}
	var stages []string
	grouped := false
	// DFC124, a DECISION not a defect: an empty text cell stays "" in ssql
	// (so `where -if s eq ""` is expressible) and is NULL in DuckDB. A
	// condition on such a column legitimately differs, so the generator
	// does not write one; aggregates over it are fair game — commands
	// treat "" as missing, which is what the engines must agree on.
	condOK := func(f string) bool {
		if kind[f] != "string" {
			return true
		}
		c := slices.Index(tb.cols, f)
		if c < 0 {
			return false // a derived text column: its emptiness is not known here
		}
		for _, r := range tb.rows {
			if r[c] == "" {
				return false
			}
		}
		return true
	}
	target := 1 + rng.Intn(4)
	for tries := 0; len(stages) < target && tries < 40; tries++ {
		var st string
		switch rng.Intn(9) {
		case 0, 1, 2: // where
			f := pick(rng, cols)
			if !condOK(f) {
				continue
			}
			st = "where " + genCond(rng, f, kind[f])
		case 3: // update an existing field conditionally
			f := pick(rng, cols)
			tgt := pick(rng, cols)
			if tgt == "id" || !condOK(f) {
				continue
			}
			st = "update " + genCond(rng, f, kind[f]) + " -set " + tgt + " " + randQuote(genLiteral(rng, kind[tgt]))
		case 4: // group-by
			if grouped || len(cols) < 3 {
				continue
			}
			key := pick(rng, cols)
			if kind[key] == "float" || key == "id" {
				continue
			}
			aggs := []string{"-count c"}
			newCols, newKind := []string{key, "c"}, map[string]string{key: kind[key], "c": "int"}
			for _, f := range cols {
				if f == key || rng.Intn(2) == 0 {
					continue
				}
				switch kind[f] {
				case "int", "float":
					fn := pick(rng, []string{"sum", "min", "max", "avg", "median"})
					aggs = append(aggs, fmt.Sprintf("-%s %s %s_%s", fn, f, fn, f))
					newCols = append(newCols, fn+"_"+f)
					newKind[fn+"_"+f] = "float"
					if fn == "min" || fn == "max" {
						// an extreme keeps its field's type; `update -set` on an
						// int column coerces a float literal to int by design
						newKind[fn+"_"+f] = kind[f]
					}
				case "string":
					fn := pick(rng, []string{"min", "max", "count-distinct"})
					name := strings.ReplaceAll(fn, "-", "_") + "_" + f
					aggs = append(aggs, fmt.Sprintf("-%s %s %s", fn, f, name))
					newCols = append(newCols, name)
					newKind[name] = map[bool]string{true: "int", false: "string"}[fn == "count-distinct"]
				}
			}
			st = "group-by " + pos(rng, key) + " " + strings.Join(aggs, " ")
			cols, kind, grouped = newCols, newKind, true
		case 5: // include a subset
			if len(cols) < 3 {
				continue
			}
			keep := []string{cols[0]}
			for _, f := range cols[1:] {
				if rng.Intn(2) == 0 {
					keep = append(keep, f)
				}
			}
			st = "include " + posAll(rng, keep)
			cols = keep
		case 6: // exclude one
			if len(cols) < 3 {
				continue
			}
			i := 1 + rng.Intn(len(cols)-1)
			st = "exclude " + pos(rng, cols[i])
			cols = slices.Delete(slices.Clone(cols), i, i+1)
		case 7: // positional, only on the unique id so ties cannot matter
			if grouped || !slices.Contains(cols, "id") {
				continue
			}
			nrow := 1 + rng.Intn(4)
			if rng.Intn(2) == 0 {
				st = fmt.Sprintf("sort %s%s | ssql limit %s", pick(rng, []string{"", "-desc "}), pos(rng, "id"), pos(rng, fmt.Sprint(nrow)))
			} else {
				st = fmt.Sprintf("top %s%s -field id", pick(rng, []string{"", "-asc "}), pos(rng, fmt.Sprint(nrow)))
			}
		case 8: // cast int → float (the one cast every engine agrees on)
			var ints []string
			for _, f := range cols {
				if kind[f] == "int" && f != "id" {
					ints = append(ints, f)
				}
			}
			if len(ints) == 0 {
				continue
			}
			f := pick(rng, ints)
			st = "cast -type " + f + " float"
			kind[f] = "float"
		}
		stages = append(stages, st)
	}
	return stages
}

func genCond(rng *rand.Rand, f, k string) string {
	flag := pick(rng, []string{"-if", "-if", "-if", "+if"})
	switch k {
	case "int", "float":
		return fmt.Sprintf("%s %s %s %s", flag, f, pick(rng, []string{"eq", "ne", "gt", "ge", "lt", "le"}), genLiteral(rng, k))
	case "bool":
		return fmt.Sprintf("%s %s %s %s", flag, f, pick(rng, []string{"eq", "ne"}), pick(rng, randBools))
	default:
		if exprParamName.MatchString(f) && !exprKeyword[f] && rng.Intn(3) == 0 {
			// The same comparison as an expression with the value bound by
			// -param (DFC134 §5.3): the value must be data in every lane.
			op := pick(rng, []string{"==", "!="})
			return fmt.Sprintf("%s %s -param who string %s", strings.Replace(flag, "if", "if-expr", 1), randQuote(f+" "+op+" who"), randQuote(genLiteral(rng, k)))
		}
		return fmt.Sprintf("%s %s %s %s", flag, f, pick(rng, []string{"eq", "ne", "contains", "startswith", "endswith"}), randQuote(genLiteral(rng, k)))
	}
}

var exprParamName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var exprKeyword = map[string]bool{"select": false, "from": false, "and": true, "or": true, "not": true, "in": true, "nil": true, "true": true, "false": true, "let": true, "if": true, "else": true}

func genLiteral(rng *rand.Rand, k string) string {
	switch k {
	case "int":
		return pick(rng, []string{"0", "1", "2", "3", "-1", "10"})
	case "float":
		return pick(rng, []string{"0", "0.5", "1.5", "2", "10"})
	case "bool":
		return pick(rng, randBools)
	}
	if rng.Intn(3) == 0 {
		return pick(rng, randHostile)
	}
	return pick(rng, []string{"a", "b", "ab", "Oslo", "x y", "12", "A", "z"})
}

func randQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func stageKinds(stages []string) string {
	var ks []string
	for _, s := range stages {
		k := strings.Fields(s)[0]
		if strings.Contains(s, "+if") {
			k += "+if"
		}
		ks = append(ks, k)
	}
	return strings.Join(ks, ",")
}

func indent(s, p string) string {
	return p + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+p)
}

// ---- running and comparing ----

type randDiff struct{ bin, duckdb, dir string }

func (rd *randDiff) script(file string, stages []string) string {
	p := rd.bin + " from csv " + file
	for _, s := range stages {
		p += " | " + rd.bin + " " + s
	}
	return p
}

func runBash(script string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", "set -o pipefail; "+script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), strings.TrimSpace(stderr.String()), err
}

// compare returns a verdict — agree, refused (generate sql declined the
// pipeline by design), both-fail, or the kind of disagreement — and, for a
// disagreement, the two outputs.
func (rd *randDiff) compare(i int, tb *randTable, stages []string) (string, string) {
	file := filepath.Join(rd.dir, fmt.Sprintf("t%d_%d.csv", i, len(tb.rows)*1000+len(stages)*10+len(tb.cols)))
	if err := os.WriteFile(file, []byte(tb.csv()), 0o644); err != nil {
		return "both-fail", err.Error()
	}
	defer os.Remove(file)
	pipeline := rd.script(file, stages)

	execOut, execErrText, execErr := runBash(pipeline + " | " + rd.bin + " to jsonl")
	sql, genErrText, genErr := runBash("export SSQL_MODE=record && " + pipeline + " | " + rd.bin + " to jsonl | " + rd.bin + " generate sql")
	if genErr != nil {
		if execErr != nil {
			return "both-fail", ""
		}
		if strings.Contains(genErrText, "no SQL") || strings.Contains(genErrText, "has no SQL") || strings.Contains(genErrText, "translation") {
			return "refused", ""
		}
		return "generate-sql-fails", "  generate sql: " + firstLine(genErrText)
	}
	sqlFile := file + ".sql"
	os.WriteFile(sqlFile, []byte(sql), 0o644)
	defer os.Remove(sqlFile)
	duckOut, duckErrText, duckErr := runBash("timeout 60 " + rd.duckdb + " -json < " + sqlFile)
	switch {
	case execErr != nil && duckErr != nil:
		return "both-fail", ""
	case execErr != nil:
		return "exec-fails-duckdb-answers", "  exec: " + firstLine(execErrText)
	case duckErr != nil:
		return "duckdb-rejects-generated-sql", "  duckdb: " + firstLine(duckErrText) + "\n  sql: " + strings.ReplaceAll(strings.TrimSpace(stripComments(sql)), "\n", " ")
	}
	execRows := jsonlRows(execOut)
	// DuckDB's -json prints a HUGEINT — any SUM over BIGINT — and a DECIMAL
	// as a string. A column that is numeric on the exec side is numeric.
	numericCols := map[string]bool{}
	for _, r := range execRows {
		for k, v := range r {
			if _, isNum := v.(float64); isNum {
				numericCols[k] = true
			}
		}
	}
	dRows := duckRows(duckOut)
	for _, r := range dRows {
		for k, v := range r {
			// (…and a DECIMAL, e.g. "-1.0" from a CASE mixing a sum with 0.5)
			if str, isStr := v.(string); isStr && numericCols[k] && str != "" && !strings.ContainsAny(str, "xXpP_ ") {
				if f, err := strconv.ParseFloat(str, 64); err == nil {
					r[k] = f
				}
			}
		}
	}
	a, b := canonRows(execRows), canonRows(dRows)
	if slices.Equal(a, b) {
		return "agree", ""
	}
	return "rows-differ", "  exec:   " + strings.Join(a, " ") + "\n  duckdb: " + strings.Join(b, " ")
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

func stripComments(sql string) string {
	var keep []string
	for _, ln := range strings.Split(sql, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(ln), "--") {
			keep = append(keep, ln)
		}
	}
	return strings.Join(keep, "\n")
}

func jsonlRows(out string) []map[string]any {
	var rows []map[string]any
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "{") {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(ln), &m) != nil {
			continue
		}
		if _, isSchema := m["_schema"]; isSchema {
			continue
		}
		rows = append(rows, m)
	}
	return rows
}

func duckRows(out string) []map[string]any {
	raw := strings.TrimSpace(out)
	if raw == "" || raw == "[]" || raw == "[{]" {
		return nil
	}
	var rows []map[string]any
	json.Unmarshal([]byte(raw), &rows)
	return rows
}

// canonRows applies the representation rules the equivalence harness has
// already agreed (DFC124, DFC102) and nothing more: null ≡ absent; an
// empty text cell is "" in ssql and NULL in DuckDB — one missing value;
// numbers to 12 significant digits (int ≡ float; last-place float effects
// are documented); DuckDB's -json prints booleans and HUGEINT sums as
// strings. Both lanes pass through the same function, so it can only hide
// a spelling, never a different value.
func canonRows(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{}
		for k, v := range r {
			switch x := v.(type) {
			case nil:
				continue
			case float64:
				m[k] = roundSig(x)
			case string:
				switch {
				case x == "":
					continue
				case x == "true" || x == "false":
					m[k] = x == "true"
				default:
					// Text stays text: "99" and 99 are DIFFERENT answers. A
					// looser rule here hid record codegen storing a number in
					// a text column (DFC133).
					m[k] = x
				}
			default:
				m[k] = v
			}
		}
		b, _ := json.Marshal(m)
		out = append(out, string(b))
	}
	slices.Sort(out)
	return out
}

func roundSig(f float64) float64 {
	g, err := strconv.ParseFloat(strconv.FormatFloat(f, 'g', 12, 64), 64)
	if err != nil {
		return f
	}
	return g
}

// shrink: drop stages (last first), then rows, keeping any change after
// which the SAME kind of disagreement remains.
func (rd *randDiff) shrink(i int, tb *randTable, stages []string, verdict string) (*randTable, []string) {
	// A column that HAS values must keep at least one: emptying a column
	// changes how both engines type it (text), which is a different
	// question from the one being shrunk.
	hadValue := make([]bool, len(tb.cols))
	for _, r := range tb.rows {
		for c, v := range r {
			hadValue[c] = hadValue[c] || v != ""
		}
	}
	still := func(t *randTable, s []string) bool {
		if len(s) == 0 || len(t.rows) == 0 {
			return false
		}
		for c := range t.cols {
			has := false
			for _, r := range t.rows {
				has = has || r[c] != ""
			}
			if hadValue[c] && !has {
				return false
			}
		}
		v, _ := rd.compare(i, t, s)
		return v == verdict
	}
	for changed := true; changed; {
		changed = false
		for k := len(stages) - 1; k >= 0; k-- {
			cand := slices.Delete(slices.Clone(stages), k, k+1)
			if still(tb, cand) {
				stages, changed = cand, true
				break
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for k := len(tb.rows) - 1; k >= 0; k-- {
			cand := tb.clone()
			cand.rows = slices.Delete(cand.rows, k, k+1)
			if still(cand, stages) {
				tb, changed = cand, true
				break
			}
		}
	}
	return tb, stages
}
