package commands

import (
	"strings"
	"testing"
)

// A fold is a recursive CTE (DFC156): INIT at index 0 cast to the state's
// type, EVERY over the state columns and the next list row, FINAL per row
// for a window; a range source orders it; a bounded frame, a source with
// no order and a dialect without the list form refuse.
func TestWindowFoldSQL(t *testing.T) {
	q := &sqlQuery{}
	if err := translateFrom(q, []string{"range", "5"}); err != nil {
		t.Fatal(err)
	}
	if err := translateWindow(q, []string{"-stream-expr", "{a:int128(0), b:int128(1)}", "{a:b, b:a+b}", "b", "fib", "-stream-expr", "{s:0}", "{s: s + n}", "s", "run"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"WITH RECURSIVE __src AS (SELECT * FROM",
		"list(s ORDER BY s.n) AS __xs FROM __src s",
		"__fold(__i, __xs, __st0_a, __st0_b, __st1_s) AS (",
		"SELECT 0, __xs, CAST(0 AS HUGEINT), CAST(1 AS HUGEINT), CAST(0 AS BIGINT) FROM __parts",
		"SELECT __i + 1, __xs, __st0_b, (__st0_a + __st0_b), (__st1_s + __xs[__i + 1].n) FROM __fold WHERE __i < len(__xs)",
		"SELECT unnest(__xs[__i]), __st0_b AS fib, __st1_s AS run FROM __fold WHERE __i >= 1",
	} {
		if !strings.Contains(q.fromClause, want) {
			t.Errorf("missing %q in:\n%s", want, q.fromClause)
		}
	}
	if sqlColumnKinds["fib"] != "int128" || sqlColumnKinds["run"] != "int" {
		t.Errorf("result kinds: fib=%q run=%q", sqlColumnKinds["fib"], sqlColumnKinds["run"])
	}

	// A partitioned, ordered fold over a CSV-shaped source.
	q = &sqlQuery{fromClause: `read_csv('e.csv')`, columns: []string{"name", "dept", "salary"}}
	sqlColumnKinds = map[string]string{"name": "string", "dept": "string", "salary": "int"}
	if err := translateWindow(q, []string{"-partition", "dept", "-order", "salary", "-desc", "-stream-expr", "{s:0}", "{s: s + salary}", "s", "run"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`SELECT s.dept AS __p0, list(s ORDER BY s.salary DESC) AS __xs FROM __src s GROUP BY s.dept`,
		`__fold(__p0, __i, __xs, __st0_s)`,
		`(__st0_s + __xs[__i + 1].salary)`,
	} {
		if !strings.Contains(q.fromClause, want) {
			t.Errorf("missing %q in:\n%s", want, q.fromClause)
		}
	}

	// Refusals name their reason.
	q = &sqlQuery{fromClause: `read_csv('e.csv')`, columns: []string{"salary"}}
	err := translateWindow(q, []string{"-stream-expr", "{s:0}", "{s: s + salary}", "s", "run"})
	if err == nil || !strings.Contains(err.Error(), "without -order") {
		t.Errorf("no order: %v", err)
	}
	q = &sqlQuery{fromClause: `read_csv('e.csv')`, columns: []string{"salary"}}
	err = translateWindow(q, []string{"-order", "salary", "-preceding", "1", "-stream-expr", "{s:0}", "{s: s + salary}", "s", "run"})
	if err == nil || !strings.Contains(err.Error(), "bounded frame") {
		t.Errorf("sliding: %v", err)
	}
	sqlColumnKinds = map[string]string{}
}

// group-by's fold: the keys are the partition, the registry aggregates
// ride beside the list, FINAL at the last index; an earlier sort is the
// order, a range source too; otherwise the input order is invisible.
func TestGroupByFoldSQL(t *testing.T) {
	q := &sqlQuery{fromClause: `read_csv('e.csv')`, columns: []string{"name", "dept", "salary"}, orderBy: []string{`"name"`}}
	sqlColumnKinds = map[string]string{"name": "string", "dept": "string", "salary": "int"}
	if err := translateGroupBy(q, []string{"dept", "-count", "c", "-stream-expr", "{s:0, n:0}", "{s: s + salary, n: n + 1}", "s / n", "avg"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`list(s ORDER BY s."name") AS __xs, COUNT(*) AS __a0 FROM __src s GROUP BY s.dept`,
		`__fold(__p0, __i, __xs, __a0, __st0_s, __st0_n)`,
		`SELECT __p0 AS dept, __a0 AS c, (CASE WHEN isfinite((__st0_s / __st0_n)) THEN (__st0_s / __st0_n) END) AS "avg" FROM __fold WHERE __i = len(__xs)`, // the finite rule on `/`, as everywhere
	} {
		if !strings.Contains(q.fromClause, want) {
			t.Errorf("missing %q in:\n%s", want, q.fromClause)
		}
	}
	if sqlColumnKinds["avg"] != "float" {
		t.Errorf("avg kind %q", sqlColumnKinds["avg"])
	}
	q = &sqlQuery{fromClause: `read_csv('e.csv')`, columns: []string{"dept", "salary"}}
	err := translateGroupBy(q, []string{"dept", "-stream-expr", "{s:0}", "{s: s + salary}", "s", "t"})
	if err == nil || !strings.Contains(err.Error(), "input order") {
		t.Errorf("unsorted: %v", err)
	}
	sqlColumnKinds = map[string]string{}
}
