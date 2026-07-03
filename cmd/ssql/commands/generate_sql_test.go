package commands

import (
	"slices"
	"strings"
	"testing"
)

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
	want = "* REPLACE (CASE ELSE 99 END AS pop)"
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
