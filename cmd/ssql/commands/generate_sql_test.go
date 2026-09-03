package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// assembleFromCommands runs a synthetic fragment stream (one Command per
// pipeline stage) through assembleSQL, mirroring what `generate sql` receives.
func assembleFromCommands(t *testing.T, cmds ...string) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, c := range cmds {
		if err := enc.Encode(lib.CodeFragment{Type: "stmt", Command: c}); err != nil {
			t.Fatal(err)
		}
	}
	sql, err := assembleSQL(&buf)
	if err != nil {
		t.Fatalf("assembleSQL(%v): %v", cmds, err)
	}
	return sql
}

// TestSubqueryWrapping guards pipeline-order correctness. A single SELECT
// evaluates clauses in a FIXED order (WHERE→GROUP BY→ORDER BY→LIMIT), so
// stages arriving "out of order" must wrap the accumulated query as a
// subquery. The original flattening silently computed a different pipeline —
// `update -set x 3 | limit 10 | group-by r | join …` became one SELECT that
// grouped everything, limited the GROUPS, and had an invalid CASE to boot.
func TestSubqueryWrapping(t *testing.T) {
	mustBefore := func(t *testing.T, sql, first, second string) {
		t.Helper()
		i, j := strings.Index(sql, first), strings.Index(sql, second)
		if i < 0 || j < 0 {
			t.Fatalf("missing %q or %q in:\n%s", first, second, sql)
		}
		if i > j {
			t.Errorf("%q must appear before %q (inner subquery first):\n%s", first, second, sql)
		}
	}

	t.Run("limit_before_group", func(t *testing.T) {
		sql := assembleFromCommands(t,
			"ssql from csv data.csv",
			"ssql limit 10",
			"ssql group-by dept -count cnt",
		)
		mustBefore(t, sql, "LIMIT 10", "GROUP BY")
		if !strings.Contains(sql, "FROM (") {
			t.Errorf("expected a subquery wrap:\n%s", sql)
		}
	})

	t.Run("projection_before_group", func(t *testing.T) {
		sql := assembleFromCommands(t,
			"ssql from csv data.csv",
			"ssql update -set x 3",
			"ssql group-by dept -count cnt",
		)
		mustBefore(t, sql, "* REPLACE", "GROUP BY")
	})

	t.Run("join_after_group", func(t *testing.T) {
		sql := assembleFromCommands(t,
			"ssql from csv data.csv",
			"ssql group-by dept -count cnt",
			"ssql join lookup.csv -using dept",
		)
		mustBefore(t, sql, "GROUP BY", "JOIN")
	})

	t.Run("sort_after_limit", func(t *testing.T) {
		sql := assembleFromCommands(t,
			"ssql from csv data.csv",
			"ssql limit 5",
			"ssql sort name",
		)
		mustBefore(t, sql, "LIMIT 5", "ORDER BY")
	})

	t.Run("two_projections", func(t *testing.T) {
		sql := assembleFromCommands(t,
			"ssql from csv data.csv",
			"ssql update -set x 3",
			"ssql rename -as old new",
		)
		// The LATER projection is the outer SELECT (textually first); the
		// earlier one nests inside FROM (...).
		mustBefore(t, sql, "* RENAME", "* REPLACE")
		mustBefore(t, sql, "FROM (", "* REPLACE")
	})

	t.Run("where_after_group", func(t *testing.T) {
		sql := assembleFromCommands(t,
			"ssql from csv data.csv",
			"ssql group-by dept -count cnt",
			"ssql where -if cnt gt 2",
		)
		mustBefore(t, sql, "GROUP BY", "WHERE")
	})

	// In-order pipelines must stay a single flat SELECT — wrapping
	// everything would be correct but unreadable.
	t.Run("in_order_stays_flat", func(t *testing.T) {
		sql := assembleFromCommands(t,
			"ssql from csv data.csv",
			"ssql where -if age gt 25",
			"ssql group-by dept -count cnt",
			"ssql sort dept",
			"ssql limit 5",
		)
		if strings.Contains(sql, "FROM (") {
			t.Errorf("in-order pipeline should not wrap:\n%s", sql)
		}
	})
}

// TestTranslateUnionSQL: union becomes a SQL set operation over the
// accumulated query and each <(…)> source (previously "unsupported").
func TestTranslateUnionSQL(t *testing.T) {
	assemble := func(t *testing.T, unionCmd string) string {
		t.Helper()
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, frag := range []lib.CodeFragment{
			{Type: "stmt", Command: "ssql from csv base.csv"},
			{Type: "func", FuncName: "unionSource1", Command: "ssql from csv extra.csv",
				FuncBody: []*lib.CodeFragment{{Type: "stmt", Command: "ssql from csv extra.csv"}}},
			{Type: "stmt", Command: unionCmd},
		} {
			if err := enc.Encode(frag); err != nil {
				t.Fatal(err)
			}
		}
		sql, err := assembleSQL(&buf)
		if err != nil {
			t.Fatalf("assembleSQL: %v", err)
		}
		return sql
	}

	sql := assemble(t, "ssql union -file /dev/fd/63")
	for _, want := range []string{"'base.csv'", "UNION", "'extra.csv'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "UNION ALL") {
		t.Errorf("bare union must dedup (UNION, not UNION ALL):\n%s", sql)
	}

	if sql := assemble(t, "ssql union -all -file /dev/fd/63"); !strings.Contains(sql, "UNION ALL") {
		t.Errorf("-all must emit UNION ALL:\n%s", sql)
	}
}

// TestUpdateNewColumnSQL: with header-seeded column tracking, `update -set`
// on a field NOT in the source becomes an added select expression
// (`SELECT *, 3 AS x`) — `* REPLACE` on a missing column is a binder error.
func TestUpdateNewColumnSQL(t *testing.T) {
	dir := t.TempDir()
	csvPath := dir + "/data.csv"
	if err := os.WriteFile(csvPath, []byte("id,city,pop\n1,Oslo,31\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sql := assembleFromCommands(t, "ssql from csv "+csvPath, "ssql update -set x 3")
	if !strings.Contains(sql, "SELECT *, 3 AS x") {
		t.Errorf("new column: want `SELECT *, 3 AS x`, got:\n%s", sql)
	}
	if strings.Contains(sql, "REPLACE") {
		t.Errorf("new column must not use REPLACE:\n%s", sql)
	}

	// Existing column keeps the REPLACE form.
	sql = assembleFromCommands(t, "ssql from csv "+csvPath, "ssql update -set pop 3")
	if !strings.Contains(sql, "* REPLACE (3 AS pop)") {
		t.Errorf("existing column: want REPLACE, got:\n%s", sql)
	}

	// Conditional set on a NEW column has no faithful translation (exec
	// leaves the field absent on unmatched rows) — must fail loudly.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, c := range []string{"ssql from csv " + csvPath, "ssql update -if pop gt 5 -set x 3"} {
		if err := enc.Encode(lib.CodeFragment{Type: "stmt", Command: c}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := assembleSQL(&buf); err == nil {
		t.Error("conditional -set on new column: expected error, got none")
	}

	// Unknown schema (unreadable source) assumes columns exist — REPLACE.
	sql = assembleFromCommands(t, "ssql from csv missing-file.csv", "ssql update -set x 3")
	if !strings.Contains(sql, "* REPLACE (3 AS x)") {
		t.Errorf("unknown schema: want REPLACE fallback, got:\n%s", sql)
	}
}

// TestUpdateUnconditionalSet: `update -set x 3` with no -if must emit the
// plain value — `CASE ELSE 3 END` (no WHEN arm) is a SQL syntax error.
func TestUpdateUnconditionalSet(t *testing.T) {
	q := &sqlQuery{}
	if err := translateUpdate(q, []string{"-set", "x", "3"}); err != nil {
		t.Fatalf("translateUpdate: %v", err)
	}
	want := "* REPLACE (3 AS x)"
	if len(q.selectExprs) != 1 || q.selectExprs[0] != want {
		t.Errorf("selectExprs = %v, want [%s]", q.selectExprs, want)
	}

	// Mixed: conditional arms become WHEN, the unconditional becomes ELSE.
	q = &sqlQuery{}
	err := translateUpdate(q, []string{"-if", "a", "gt", "1", "-set", "x", "2", "-", "-set", "x", "9"})
	if err != nil {
		t.Fatalf("translateUpdate mixed: %v", err)
	}
	want = "* REPLACE (CASE WHEN a > 1 THEN 2 ELSE 9 END AS x)"
	if len(q.selectExprs) != 1 || q.selectExprs[0] != want {
		t.Errorf("selectExprs = %v, want [%s]", q.selectExprs, want)
	}
}

// TestDistinctSQL: distinct must render as SELECT DISTINCT (the old
// implementation prepended "DISTINCT" as a fake select column, producing
// `SELECT DISTINCT` with no columns, or `SELECT DISTINCT, col`).
func TestDistinctSQL(t *testing.T) {
	sql := assembleFromCommands(t, "ssql from csv data.csv", "ssql distinct")
	if !strings.Contains(sql, "SELECT DISTINCT *") {
		t.Errorf("bare distinct: want SELECT DISTINCT *, got:\n%s", sql)
	}

	sql = assembleFromCommands(t, "ssql from csv data.csv", "ssql include city", "ssql distinct")
	if !strings.Contains(sql, "SELECT DISTINCT city") {
		t.Errorf("include+distinct: want SELECT DISTINCT city, got:\n%s", sql)
	}
}

// TestResortPrepends: a second `sort` re-sorts stably — its keys are primary,
// the earlier order is the tie-break, so new entries must be PREPENDED.
func TestResortPrepends(t *testing.T) {
	q := &sqlQuery{}
	if err := translateSort(q, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := translateSort(q, []string{"y", "-desc"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"y DESC", "x"}
	if !slices.Equal(q.orderBy, want) {
		t.Errorf("orderBy = %v, want %v", q.orderBy, want)
	}
}

// TestSQLLiteral guards value-token rendering: numerics and booleans must be
// bare, not string-quoted. `n > '15'` happens to work in DuckDB (it coerces by
// column type) but is semantically fragile — '9' > '15' is true as strings,
// false as numbers — and breaks stricter engines. Inf/NaN tokens stay quoted
// (no bare SQL spelling).
func TestSQLLiteral(t *testing.T) {
	cases := []struct{ in, want string }{
		{"15", "15"},
		{"-3", "-3"},
		{"2.5", "2.5"},
		{"1e5", "1e5"},
		{"true", "TRUE"},
		{"False", "FALSE"},
		{"Oslo", "'Oslo'"},
		{"", "''"},
		{"O'Brien", "'O''Brien'"},
		{"inf", "'inf'"},
		{"NaN", "'NaN'"},
	}
	for _, c := range cases {
		if got := sqlLiteral(c.in); got != c.want {
			t.Errorf("sqlLiteral(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestTranslateConditionLiterals(t *testing.T) {
	if got, want := translateCondition("pop", "gt", "15"), "pop > 15"; got != want {
		t.Errorf("numeric: got %q, want %q", got, want)
	}
	if got, want := translateCondition("city", "eq", "Oslo"), "city = 'Oslo'"; got != want {
		t.Errorf("string: got %q, want %q", got, want)
	}
	// Pattern operators keep their quoted-string form regardless of the token.
	if got, want := translateCondition("code", "contains", "15"), "code LIKE '%15%'"; got != want {
		t.Errorf("contains: got %q, want %q", got, want)
	}
}

// TestExprToSQL guards the expr-lang → SQL translation. Verbatim passthrough
// (the pre-v4.56 behaviour) was broken: `&&` is a SQL parse error, `||` means
// string concat, and "double quotes" quote identifiers, not strings.
func TestExprToSQL(t *testing.T) {
	cases := []struct{ in, want string }{
		{`pop > 15 && city != "Oslo"`, `((pop > 15) AND (city <> 'Oslo'))`},
		{`age >= 18 and status == "active"`, `((age >= 18) AND (status = 'active'))`},
		{`a == 1 || b == 2`, `((a = 1) OR (b = 2))`},
		{`price * qty > 1000`, `((price * qty) > 1000)`},
		{`upper(city) == "OSLO"`, `(upper(city) = 'OSLO')`},
		{`len(name) > 3`, `(length(name) > 3)`},
		{`hasPrefix(name, "A")`, `starts_with(name, 'A')`},
		{`name startsWith "A"`, `starts_with(name, 'A')`},
		{`name matches "^A"`, `regexp_matches(name, '^A')`},
		// NB expr-lang only accepts contains as an OPERATOR (`a contains b`);
		// the call form `contains(a, b)` is a parse error even in execution.
		{`email contains "@"`, `contains(email, '@')`},
		{`x ?? 0`, `COALESCE(x, 0)`},
		{`pop in [1, 2, 3]`, `(pop IN (1, 2, 3))`},
		{`has("email")`, `(email IS NOT NULL)`},
		{`getOr("score", 0) > 5`, `(COALESCE(score, 0) > 5)`},
		{`int(x) > 5`, `(CAST(x AS BIGINT) > 5)`},
		{`not active`, `(NOT active)`},
		{`active ? "yes" : "no"`, `(CASE WHEN active THEN 'yes' ELSE 'no' END)`},
		{`min(a, b) < 10`, `(least(a, b) < 10)`},
		{`abs(actual - expected) < 0.01`, `(abs((actual - expected)) < 0.01)`},
	}
	for _, c := range cases {
		got, err := exprToSQL(c.in)
		if err != nil {
			t.Errorf("exprToSQL(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("exprToSQL(%q) = %s, want %s", c.in, got, c.want)
		}
	}

	// Untranslatable constructs must fail loudly, naming the offender —
	// silently emitting broken SQL is how this bug class hides.
	failures := []struct{ in, wantSub string }{
		{`split(name, ",")`, "split"},
		{`all(items, # > 0)`, "all"},
		{`foo.bar > 1`, "member access"},
		{`trimPrefix(name, "x")`, "trimPrefix"},
		{`x in ids`, "list literal"},
	}
	for _, f := range failures {
		_, err := exprToSQL(f.in)
		if err == nil {
			t.Errorf("exprToSQL(%q): expected an error, got none", f.in)
			continue
		}
		if !strings.Contains(err.Error(), f.wantSub) {
			t.Errorf("exprToSQL(%q) error %q, want it to mention %q", f.in, err, f.wantSub)
		}
	}
}

// TestTranslateWhereExpr covers -if-expr translation and +if/+if-expr
// negation (previously negated conditions were silently DROPPED, so the SQL
// returned extra rows).
func TestTranslateWhereExpr(t *testing.T) {
	q := &sqlQuery{}
	if err := translateWhere(q, []string{"-if-expr", `pop > 15 && city != "Oslo"`}); err != nil {
		t.Fatalf("translateWhere: %v", err)
	}
	want := `((pop > 15) AND (city <> 'Oslo'))`
	if len(q.whereClauses) != 1 || q.whereClauses[0] != want {
		t.Errorf("whereClauses = %v, want [%s]", q.whereClauses, want)
	}

	q = &sqlQuery{}
	if err := translateWhere(q, []string{"+if", "city", "eq", "Oslo"}); err != nil {
		t.Fatalf("translateWhere +if: %v", err)
	}
	want = `NOT (city = 'Oslo')`
	if len(q.whereClauses) != 1 || q.whereClauses[0] != want {
		t.Errorf("negated whereClauses = %v, want [%s]", q.whereClauses, want)
	}

	q = &sqlQuery{}
	if err := translateWhere(q, []string{"-if-expr", `x && y ||`}); err == nil {
		t.Error("translateWhere: expected error for unparsable expression")
	}
}

// TestTranslateUpdateExpr covers -set-expr and -if-expr in update (both were
// silently ignored before v4.56 — the CASE simply omitted the assignment).
func TestTranslateUpdateExpr(t *testing.T) {
	q := &sqlQuery{}
	err := translateUpdate(q, []string{"-if", "pop", "gt", "25", "-set-expr", "city", "upper(city)"})
	if err != nil {
		t.Fatalf("translateUpdate: %v", err)
	}
	want := "* REPLACE (CASE WHEN pop > 25 THEN upper(city) ELSE city END AS city)"
	if len(q.selectExprs) != 1 || q.selectExprs[0] != want {
		t.Errorf("selectExprs = %v, want [%s]", q.selectExprs, want)
	}

	// -set literals are typed now: numeric stays bare.
	q = &sqlQuery{}
	if err := translateUpdate(q, []string{"-set", "pop", "99"}); err != nil {
		t.Fatalf("translateUpdate -set: %v", err)
	}
	want = "* REPLACE (99 AS pop)"
	if len(q.selectExprs) != 1 || q.selectExprs[0] != want {
		t.Errorf("selectExprs = %v, want [%s]", q.selectExprs, want)
	}

	q = &sqlQuery{}
	if err := translateUpdate(q, []string{"-set-expr", "x", `split(y, ",")`}); err == nil {
		t.Error("translateUpdate: expected error for untranslatable -set-expr")
	}
}

// TestTranslateGroupByFailsLoudly: aggregation forms with no SQL translation
// must error, not silently drop the aggregation.
func TestTranslateGroupByFailsLoudly(t *testing.T) {
	for _, flag := range [][]string{
		{"dept", "-expr", "total", "sum(values)"},
		{"dept", "-stream-expr", "0", "state + x", "state", "total"},
		{"dept", "-rollup", "-count", "cnt"},
		{"dept", "-cube", "-count", "cnt"},
	} {
		q := &sqlQuery{}
		if err := translateGroupBy(q, flag); err == nil {
			t.Errorf("translateGroupBy(%v): expected error, got none", flag)
		}
	}
}

// TestTranslateTopSQL guards the `top` → SQL translation. It regressed
// silently once already: it looked for the long-removed -by flag (so no
// ORDER BY was ever emitted) and treated args[0] as N (so `-asc` became the
// LIMIT). N is the first bare positional; the field comes from -field/-f;
// -asc flips DESC→ASC.
func TestTranslateTopSQL(t *testing.T) {
	cases := []struct {
		name      string
		args      []string // args after "ssql top"
		wantOrder string
		wantLimit string
	}{
		// quoteIdent leaves simple identifiers unquoted.
		{"desc", []string{"3", "-field", "name"}, "name DESC", "3"},
		{"asc", []string{"-asc", "3", "-field", "name"}, "name ASC", "3"},
		{"asc-after-n", []string{"3", "-asc", "-field", "name"}, "name ASC", "3"},
		{"short-field", []string{"5", "-f", "salary"}, "salary DESC", "5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &sqlQuery{}
			if err := translateTop(q, c.args); err != nil {
				t.Fatalf("translateTop: %v", err)
			}
			if q.limit != c.wantLimit {
				t.Errorf("limit = %q, want %q", q.limit, c.wantLimit)
			}
			if !slices.Contains(q.orderBy, c.wantOrder) {
				t.Errorf("orderBy = %v, want to contain %q", q.orderBy, c.wantOrder)
			}
		})
	}
}

func TestTranslateSampleSQL(t *testing.T) {
	// Unseeded forms translate to DuckDB USING SAMPLE.
	q := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateSample(q, []string{"-percent", "5"}); err != nil {
		t.Fatal(err)
	}
	if q.fromClause != "'x.csv' USING SAMPLE 5% (bernoulli)" || !q.sampled {
		t.Errorf("percent form: %q sampled=%v", q.fromClause, q.sampled)
	}

	q2 := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateSample(q2, []string{"1000"}); err != nil {
		t.Fatal(err)
	}
	if q2.fromClause != "'x.csv' USING SAMPLE 1000 ROWS (reservoir)" {
		t.Errorf("N form: %q", q2.fromClause)
	}

	// Seeded sampling REFUSES loudly (DFC110): no cross-engine
	// deterministic equivalent.
	q3 := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateSample(q3, []string{"7", "-seed", "42"}); err == nil ||
		!strings.Contains(err.Error(), "no SQL equivalent") {
		t.Errorf("seeded: want loud refusal, got %v", err)
	}

	// sample 0 = pass-through dial: stage vanishes.
	q4 := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateSample(q4, []string{"0"}); err != nil || q4.sampled || q4.fromClause != "'x.csv'" {
		t.Errorf("zero dial: %v %q", err, q4.fromClause)
	}

	// USING SAMPLE binds to FROM — anything accumulated must wrap first.
	q5 := &sqlQuery{fromClause: "'x.csv'", whereClauses: []string{"a > 1"}}
	if !needsWrap(q5, "sample") {
		t.Error("sample after where must wrap")
	}
	q6 := &sqlQuery{fromClause: "'x.csv'"}
	if needsWrap(q6, "sample") {
		t.Error("sample on bare from must not wrap")
	}
}

func TestTranslateFromSampleSQL(t *testing.T) {
	// from -sample N → USING SAMPLE on the FROM clause; the N value
	// must NOT be mistaken for a file (regression: read_csv_auto
	// (['kind.csv','5'])).
	q := &sqlQuery{}
	if err := translateFrom(q, []string{"csv", "kind.csv", "-sample", "5"}); err != nil {
		t.Fatal(err)
	}
	if q.fromClause != "'kind.csv' USING SAMPLE 5 ROWS (reservoir)" || !q.sampled {
		t.Errorf("got %q sampled=%v", q.fromClause, q.sampled)
	}
	// Seeded refusal.
	q2 := &sqlQuery{}
	if err := translateFrom(q2, []string{"csv", "kind.csv", "-sample", "5", "-sample-seed", "42"}); err == nil ||
		!strings.Contains(err.Error(), "no SQL equivalent") {
		t.Errorf("seeded: want refusal, got %v", err)
	}
}

func TestTranslateResampleSQL(t *testing.T) {
	// The happy path builds the DFC121 construction: dedup'd per-field
	// series (max per ts = highest-wins), epoch-aligned generate_series
	// grid, ASOF join, edge clamp, ORDER BY.
	q := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(q, nil, []string{"-time", "t", "-every", "10s", "-value", "v"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"generate_series(lo, hi, s)",
		"ASOF LEFT JOIN __s0 p0 ON __grid.__g >= p0.__ts",
		"max(CAST(v AS DOUBLE))", // duplicate ts keeps highest value
		"GROUP BY __ts",          // one row per timestamp
		"ORDER BY __ts LIMIT 1",  // leading-edge clamp
		"ORDER BY t",             // grid order defines output order
		"10000000000",            // -every 10s in ns, awaiting unit division
		"1e17",                   // magnitude-based unit detection (Go thresholds)
	} {
		if !strings.Contains(q.fromClause, want) {
			t.Errorf("SQL missing %q:\n%s", want, q.fromClause)
		}
	}
	if len(q.columns) != 2 || q.columns[0] != "t" || q.columns[1] != "v" {
		t.Errorf("columns = %v, want [t v]", q.columns)
	}

	// -fill next flips the ASOF direction; linear needs BOTH sides.
	qn := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(qn, nil, []string{"-time", "t", "-every", "1s", "-value", "v", "-fill", "next"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(qn.fromClause, "ASOF LEFT JOIN __s0 n0 ON __grid.__g <= n0.__ts") {
		t.Errorf("next fill missing forward ASOF:\n%s", qn.fromClause)
	}
	ql := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(ql, nil, []string{"-time", "t", "-every", "1s", "-value", "v", "-fill", "linear"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ql.fromClause, "p0 ON __grid.__g >= p0.__ts") ||
		!strings.Contains(ql.fromClause, "n0 ON __grid.__g <= n0.__ts") ||
		!strings.Contains(ql.fromClause, "CAST(n0.__ts - p0.__ts AS DOUBLE)") {
		t.Errorf("linear fill missing dual ASOF + interpolation:\n%s", ql.fromClause)
	}

	// -time-unit pins the unit — no detection CASE in the SQL.
	qu := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(qu, nil, []string{"-time", "t", "-every", "5s", "-value", "v", "-time-unit", "s"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(qu.fromClause, "1e17") {
		t.Errorf("pinned unit should skip magnitude detection:\n%s", qu.fromClause)
	}

	// v1 refusals are LOUD: string timestamps and bounds have no SQL
	// translation yet — silence here would mean a silently different
	// pipeline (the cardinal sin).
	for _, c := range [][]string{
		{"-time", "t", "-every", "10s", "-value", "v", "-time-format", "2006-01-02"},
		{"-time", "t", "-every", "10s", "-value", "v", "-from", "0"},
		{"-time", "t", "-every", "10s", "-value", "v", "-to", "100"},
	} {
		qr := &sqlQuery{fromClause: "'x.csv'"}
		if err := translateResample(qr, nil, c); err == nil || !strings.Contains(err.Error(), "generate go") {
			t.Errorf("args %v: want loud refusal pointing at generate go, got %v", c, err)
		}
	}

	// Missing requireds refuse too.
	qm := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(qm, nil, []string{"-time", "t"}); err == nil {
		t.Error("missing -every/-value: want error")
	}

	// resample rebuilds the whole query — accumulated state must wrap.
	qw := &sqlQuery{fromClause: "'x.csv'", whereClauses: []string{"a > 1"}}
	if !needsWrap(qw, "resample") {
		t.Error("resample after where must wrap")
	}
	qb := &sqlQuery{fromClause: "'x.csv'"}
	if needsWrap(qb, "resample") {
		t.Error("resample on bare from must not wrap")
	}
}

func TestTranslateResampleSQLStructuredOp(t *testing.T) {
	// The structured-Op path (DFC123 slice 3) must produce
	// byte-identical SQL to the argv fallback — one lowering
	// (buildResampleSQL), two front doors.
	argvQ := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(argvQ, nil, []string{"-time", "t", "-every", "10s", "-value", "v", "-fill", "linear", "-time-unit", "s"}); err != nil {
		t.Fatal(err)
	}
	op := &lib.Op{
		Kind: "resample",
		Args: map[string]any{
			"time":      "t",
			"every":     float64(10_000_000_000), // JSON round-trip: numbers decode as float64
			"values":    []any{"v"},              // JSON round-trip: lists decode as []any
			"fill":      "linear",
			"time_unit": "s",
		},
	}
	opQ := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(opQ, op, nil); err != nil {
		t.Fatal(err)
	}
	if opQ.fromClause != argvQ.fromClause {
		t.Errorf("structured and argv paths diverge:\n--- op:\n%s\n--- argv:\n%s", opQ.fromClause, argvQ.fromClause)
	}

	// Structured refusals: the command recorded -time-format / -from —
	// the translator must refuse just as loudly as with argv.
	for _, extra := range []map[string]any{
		{"time_format": "2006-01-02"},
		{"from": "0"},
		{"to": "100"},
	} {
		args := map[string]any{"time": "t", "every": float64(1e9), "values": []any{"v"}}
		for k, v := range extra {
			args[k] = v
		}
		q := &sqlQuery{fromClause: "'x.csv'"}
		err := translateResample(q, &lib.Op{Kind: "resample", Args: args}, nil)
		if err == nil || !strings.Contains(err.Error(), "generate go") {
			t.Errorf("structured %v: want loud refusal pointing at generate go, got %v", extra, err)
		}
	}

	// An Op WITHOUT structured Args (older command emitting only
	// Kind/Argv) must fall through to argv parsing, not error.
	q := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateResample(q, &lib.Op{Kind: "resample"}, []string{"-time", "t", "-every", "5s", "-value", "v"}); err != nil {
		t.Fatal("Kind-only Op must fall back to argv:", err)
	}
}

func TestTranslateLimitLast(t *testing.T) {
	// No ORDER BY → loud refusal (arrival order is undefined in SQL).
	q := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateLimit(q, []string{"-last", "3"}); err == nil || !strings.Contains(err.Error(), "preceding sort") {
		t.Errorf("unsorted -last: want loud refusal, got %v", err)
	}
	// Flag order shouldn't matter.
	q = &sqlQuery{fromClause: "'x.csv'"}
	if err := translateLimit(q, []string{"3", "-last"}); err == nil {
		t.Error("N before -last must still be recognised as -last")
	}

	// With ORDER BY: inner query takes N under the REVERSED order,
	// outer restores the original.
	q = &sqlQuery{fromClause: "'x.csv'", orderBy: []string{`"pop" DESC`, `"id" ASC`}}
	if err := translateLimit(q, []string{"-last", "3"}); err != nil {
		t.Fatal(err)
	}
	if q.limit != "" {
		t.Errorf("outer query must not carry the LIMIT (it belongs to the inner): %q", q.limit)
	}
	if len(q.orderBy) != 2 || q.orderBy[0] != `"pop" DESC` || q.orderBy[1] != `"id" ASC` {
		t.Errorf("outer ORDER BY must be the original: %v", q.orderBy)
	}
	inner := q.fromClause
	for _, want := range []string{`"pop" ASC`, `"id" DESC`, "LIMIT 3"} {
		if !strings.Contains(inner, want) {
			t.Errorf("inner subquery missing %q:\n%s", want, inner)
		}
	}

	// Plain limit unchanged.
	q = &sqlQuery{fromClause: "'x.csv'"}
	if err := translateLimit(q, []string{"7"}); err != nil || q.limit != "7" {
		t.Errorf("plain limit: err=%v limit=%q", err, q.limit)
	}
}

func TestTranslateDescribeSQL(t *testing.T) {
	// Unknown source columns → loud refusal (the translator can't
	// enumerate fields it doesn't know).
	q := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateDescribe(q, nil, nil); err == nil || !strings.Contains(err.Error(), "generate go") {
		t.Errorf("nil columns: want loud refusal, got %v", err)
	}
	// Unknown requested field → loud, listing the available ones.
	q = &sqlQuery{fromClause: "'x.csv'", columns: []string{"id", "city"}}
	if err := translateDescribe(q, nil, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "available: id, city") {
		t.Errorf("unknown field: want loud refusal with available list, got %v", err)
	}
	// Structured Op path: one UNION ALL branch per field, ssql type
	// vocabulary, exact distinct, median, ordered by field position.
	q = &sqlQuery{fromClause: "'x.csv'", columns: []string{"id", "city", "pop"}}
	op := &lib.Op{Kind: "describe", Args: map[string]any{"fields": []any{"pop", "id"}}}
	if err := translateDescribe(q, op, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`'pop' AS "field"`, `'id' AS "field"`, "UNION ALL", "ORDER BY __ord",
		`THEN 'int' WHEN`, `THEN 'float' WHEN`, `= 'BOOLEAN' THEN 'bool' ELSE 'string'`,
		`count(DISTINCT pop) FILTER`, `median(TRY_CAST(pop AS DOUBLE))`,
	} {
		if !strings.Contains(q.fromClause, want) {
			t.Errorf("SQL missing %q:\n%s", want, q.fromClause)
		}
	}
	if strings.Contains(q.fromClause, `'city' AS "field"`) {
		t.Error("unrequested field must not be described")
	}
	if len(q.columns) != len(describeColumns) || q.columns[0] != "field" {
		t.Errorf("output columns = %v", q.columns)
	}
	// Argv fallback (Op-less fragment) selects the same fields.
	q2 := &sqlQuery{fromClause: "'x.csv'", columns: []string{"id", "city", "pop"}}
	if err := translateDescribe(q2, nil, []string{"pop", "id"}); err != nil {
		t.Fatal(err)
	}
	if q2.fromClause != q.fromClause {
		t.Error("argv fallback must produce the same SQL as the structured path")
	}
}

func TestTranslateUnpivotSQL(t *testing.T) {
	// Explicit values need no column list; ids + col + val projected.
	q := &sqlQuery{fromClause: "'x.csv'"}
	op := &lib.Op{Kind: "unpivot", Args: map[string]any{"ids": []any{"name"}, "values": []any{"jan", "feb"}, "col": "month", "val": "revenue"}}
	if err := translateUnpivot(q, op, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"UNPIVOT", "ON jan, feb", "INTO NAME month VALUE revenue", "SELECT name, month, revenue FROM"} {
		if !strings.Contains(q.fromClause, want) {
			t.Errorf("SQL missing %q:\n%s", want, q.fromClause)
		}
	}
	if len(q.columns) != 3 || q.columns[2] != "revenue" {
		t.Errorf("columns = %v", q.columns)
	}
	// Default values: all non-id columns sorted; needs the column list.
	q = &sqlQuery{fromClause: "'x.csv'", columns: []string{"id", "z", "a"}}
	if err := translateUnpivot(q, nil, []string{"-id", "id"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.fromClause, "ON a, z INTO NAME name VALUE value") {
		t.Errorf("default values must be sorted non-id columns:\n%s", q.fromClause)
	}
	q = &sqlQuery{fromClause: "'x.csv'"}
	if err := translateUnpivot(q, nil, []string{"-id", "id"}); err == nil || !strings.Contains(err.Error(), "name the -value fields") {
		t.Errorf("default values with unknown columns: want loud refusal, got %v", err)
	}
	// Structured and argv paths agree.
	a := &sqlQuery{fromClause: "'x.csv'"}
	b := &sqlQuery{fromClause: "'x.csv'"}
	_ = translateUnpivot(a, op, nil)
	_ = translateUnpivot(b, nil, []string{"-id", "name", "-value", "jan", "-value", "feb", "-col", "month", "-val", "revenue"})
	if a.fromClause != b.fromClause {
		t.Error("structured and argv paths diverge")
	}
}

func TestTranslateFillSQL(t *testing.T) {
	// -down without ORDER BY → loud refusal.
	q := &sqlQuery{fromClause: "'x.csv'"}
	if err := translateFill(q, nil, []string{"-down", "n"}); err == nil || !strings.Contains(err.Error(), "preceding sort") {
		t.Errorf("unsorted -down: want loud refusal, got %v", err)
	}
	// Nothing to do → error.
	if err := translateFill(&sqlQuery{fromClause: "'x.csv'"}, nil, nil); err == nil {
		t.Error("fill with no flags must error")
	}
	// -default only: COALESCE via * REPLACE; numeric literal bare, text quoted.
	q = &sqlQuery{fromClause: "'x.csv'"}
	if err := translateFill(q, nil, []string{"-default", "n", "99", "-default", "s", "unknown"}); err != nil {
		t.Fatal(err)
	}
	sel := q.selectExprs[0]
	for _, want := range []string{"* REPLACE (", "COALESCE(n, 99) AS n", "COALESCE(s, 'unknown') AS s"} {
		if !strings.Contains(sel, want) {
			t.Errorf("missing %q in %s", want, sel)
		}
	}
	// -down with ORDER BY: LAST_VALUE IGNORE NULLS over the query's order,
	// outer ORDER BY preserved; a field both carried and defaulted nests.
	q = &sqlQuery{fromClause: "'x.csv'", orderBy: []string{`"id" ASC`}}
	if err := translateFill(q, nil, []string{"-down", "f", "-default", "f", "0"}); err != nil {
		t.Fatal(err)
	}
	sel = q.selectExprs[0]
	if !strings.Contains(sel, `COALESCE(LAST_VALUE(f IGNORE NULLS) OVER (ORDER BY "id" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW), 0) AS f`) {
		t.Errorf("down+default should nest: %s", sel)
	}
	if len(q.orderBy) != 1 || q.orderBy[0] != `"id" ASC` {
		t.Errorf("outer order lost: %v", q.orderBy)
	}
}

func TestTranslateExtractSQL(t *testing.T) {
	re := `^(?P<ts>\S+) (?P<lvl>\w+) (?P<msg>.*)$`
	// Without -skip: loud refusal.
	q := &sqlQuery{fromClause: "src"}
	if err := translateExtract(q, nil, []string{"-field", "line", "-re", re}); err == nil || !strings.Contains(err.Error(), "-skip") {
		t.Errorf("no -skip: want loud refusal, got %v", err)
	}
	// With -skip: regexp_extract with the named list, WHERE regexp_matches,
	// source field excluded, captures projected; column tracking follows.
	q = &sqlQuery{fromClause: "src", columns: []string{"line_number", "line"}}
	if err := translateExtract(q, nil, []string{"-field", "line", "-re", re, "-skip"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"regexp_extract(line, '^(?P<ts>", "['ts', 'lvl', 'msg']", "WHERE regexp_matches(line,", "EXCLUDE (line, __m)", "__m.ts AS ts", "__m.msg AS msg"} {
		if !strings.Contains(q.fromClause, want) {
			t.Errorf("missing %q:\n%s", want, q.fromClause)
		}
	}
	if len(q.columns) != 4 || q.columns[0] != "line_number" || q.columns[3] != "msg" {
		t.Errorf("columns = %v", q.columns)
	}
	// -keep keeps the source column.
	q = &sqlQuery{fromClause: "src"}
	if err := translateExtract(q, nil, []string{"-field", "line", "-re", re, "-skip", "-keep"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.fromClause, "EXCLUDE (__m)") || strings.Contains(q.fromClause, "EXCLUDE (line") {
		t.Errorf("-keep must not exclude the source: %s", q.fromClause)
	}
	// from lines seeds the two columns.
	q = &sqlQuery{}
	if err := translateFrom(q, []string{"lines", "app.log"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.fromClause, "row_number() OVER () AS line_number") || len(q.columns) != 2 {
		t.Errorf("from lines: %s / %v", q.fromClause, q.columns)
	}
}

func TestNamedOnlyPattern(t *testing.T) {
	cases := map[string]string{
		`(?P<a>\d+)-(\d+)`:      `(?P<a>\d+)-(?:\d+)`,
		`[(](?P<x>.)`:           `[(](?P<x>.)`,
		`\((?P<y>\w+)\)`:        `\((?P<y>\w+)\)`,
		`(?:pre)(?P<z>.)(q)`:    `(?:pre)(?P<z>.)(?:q)`,
	}
	for in, want := range cases {
		if got := namedOnlyPattern(in); got != want {
			t.Errorf("namedOnlyPattern(%q) = %q, want %q", in, got, want)
		}
	}
}
