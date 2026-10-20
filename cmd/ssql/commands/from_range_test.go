package commands

import (
	"strings"
	"testing"
)

func TestParseRangeArgs(t *testing.T) {
	cases := []struct {
		args []string
		want rangeOpts
		err  string
	}{
		{[]string{"10"}, rangeOpts{count: 10, start: 1, step: 1, field: "n"}, ""},
		{[]string{"-start", "0", "4", "-step", "-3", "-field", "i", "-tz", "UTC"}, rangeOpts{count: 4, start: 0, step: -3, field: "i"}, ""},
		{[]string{"0"}, rangeOpts{count: 0, start: 1, step: 1, field: "n"}, ""},
		{[]string{}, rangeOpts{}, "COUNT is required"},
		{[]string{"-1"}, rangeOpts{}, "COUNT must be 0 or more"},
		{[]string{"3", "-step", "0"}, rangeOpts{}, "-step must not be 0"},
		{[]string{"3", "-field", "a b"}, rangeOpts{}, "plain identifier"},
		{[]string{"3", "-field", "1st"}, rangeOpts{}, "plain identifier"},
		{[]string{"3", "-start", "x"}, rangeOpts{}, "-start wants an integer"},
		{[]string{"3", "4"}, rangeOpts{}, "unexpected argument"},
	}
	for _, c := range cases {
		got, err := parseRangeArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%v: want error %q, got %v", c.args, c.err, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if got != c.want {
			t.Errorf("%v = %+v, want %+v", c.args, got, c.want)
		}
	}
}

// from range renders each engine's generator: half-open range() on
// DuckDB and DataFusion (stop = start + N*step), inclusive
// generate_series on Postgres (end = start + (N-1)*step, WHERE false
// for N = 0), and seeds the one column as an int.
func TestRangeSourceSQL(t *testing.T) {
	q := &sqlQuery{}
	if err := translateFrom(q, []string{"range", "3", "-start", "0", "-step", "2"}); err != nil {
		t.Fatal(err)
	}
	if q.fromClause != "(SELECT n FROM range(0, 6, 2) AS t(n))" || len(q.columns) != 1 || q.columns[0] != "n" {
		t.Errorf("duckdb: %s / %v", q.fromClause, q.columns)
	}
	if sqlColumnKinds["n"] != "int" {
		t.Errorf("n kind = %q, want int", sqlColumnKinds["n"])
	}
	q = &sqlQuery{}
	if err := translateFrom(q, []string{"range", "4", "-start", "10", "-step", "-3", "-field", "i"}); err != nil {
		t.Fatal(err)
	}
	if q.fromClause != "(SELECT i FROM range(10, -2, -3) AS t(i))" {
		t.Errorf("duckdb negative step: %s", q.fromClause)
	}
	if err := translateFrom(&sqlQuery{}, []string{"range", "3", "-step", "0"}); err == nil || !strings.Contains(err.Error(), "-step must not be 0") {
		t.Errorf("step 0: %v", err)
	}

	pg := mustDialect(t, dialectPostgres, "ssql from range 3 -start 0 -step 2", "ssql where -if n gt 1")
	wantAll(t, pg, `generate_series(0, 4, 2) AS t("n")`, `AS __q`)
	pg0 := mustDialect(t, dialectPostgres, "ssql from range 0")
	wantAll(t, pg0, `generate_series(1, 0, 1) AS t("n") WHERE false`)
	df := mustDialect(t, dialectDataFusion, "ssql from range 3", "ssql where -if n gt 1")
	wantAll(t, df, `(SELECT value AS "n" FROM range(1, 4, 1))`)
}

// generate ssql's source rules see a generator, not a file.
func TestGetFromFileAndFormatRange(t *testing.T) {
	file, format := getFromFileAndFormat(&pipelineCmd{RawArgs: []string{"range", "10"}})
	if file != "" || format != "range" {
		t.Errorf("got (%q, %q), want (\"\", \"range\")", file, format)
	}
}
