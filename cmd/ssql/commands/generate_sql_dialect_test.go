package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// assembleDialect runs a synthetic fragment stream through the assembler
// for one dialect (assembleFromCommands is the DuckDB form).
func assembleDialect(t *testing.T, d sqlDialect, cmds ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, c := range cmds {
		if err := enc.Encode(lib.CodeFragment{Type: "stmt", Command: c}); err != nil {
			t.Fatal(err)
		}
	}
	return assembleSQLDialect(&buf, d)
}

func mustDialect(t *testing.T, d sqlDialect, cmds ...string) string {
	t.Helper()
	sql, err := assembleDialect(t, d, cmds...)
	if err != nil {
		t.Fatalf("%s: %v\n  %v", d, err, cmds)
	}
	return sql
}

func wantAll(t *testing.T, sql string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(sql, w) {
			t.Errorf("missing %q in:\n%s", w, sql)
		}
	}
}

func wantNone(t *testing.T, sql string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(sql, n) {
			t.Errorf("unexpected %q in:\n%s", n, sql)
		}
	}
}

// dialectFixture writes a small CSV so the translator knows the source's
// columns (Postgres needs them for every `SELECT *` rewrite) and returns
// its path.
func dialectFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Emp-Data.csv")
	csv := "Name,age,score,flag,note,hire_date\nAlice,30,1.5,true,,2020-01-05\nBob,25,2,false,x,2021-03-02\nCarol,,3.25,true,y,2019-07-09\n"
	if err := os.WriteFile(p, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseSQLDialect(t *testing.T) {
	for in, want := range map[string]sqlDialect{"": dialectDuckDB, "duckdb": dialectDuckDB, "Postgres": dialectPostgres,
		"postgresql": dialectPostgres, "pg": dialectPostgres, "datafusion": dialectDataFusion, "df": dialectDataFusion} {
		got, err := parseSQLDialect(in)
		if err != nil || got != want {
			t.Errorf("parseSQLDialect(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseSQLDialect("sqlite"); err == nil || !strings.Contains(err.Error(), "duckdb, postgres, datafusion") {
		t.Errorf("unknown dialect must name the choices, got %v", err)
	}
}

func TestPGTableName(t *testing.T) {
	for in, want := range map[string]string{
		"employees.csv": `"employees"`, "data/My-File.CSV": `"my_file"`, "2024.csv": `"t_2024"`,
		"/abs/path/a b.c.tsv": `"a_b_c"`, "___.csv": `"t_"`,
	} {
		if got := pgTableName(in); got != want {
			t.Errorf("pgTableName(%q) = %s, want %s", in, got, want)
		}
	}
}

// The Postgres prologue types columns from a sample the way ssql's reader
// does: ints, floats (int+float mix), booleans, text, and an all-empty
// column is TEXT; header names are quoted verbatim (case kept).
func TestPGInferColumns(t *testing.T) {
	cols, types := pgInferColumns(dialectFixture(t), ',')
	wantCols := []string{"Name", "age", "score", "flag", "note", "hire_date"}
	wantTypes := []string{"TEXT", "BIGINT", "DOUBLE PRECISION", "BOOLEAN", "TEXT", "DATE"}
	if strings.Join(cols, ",") != strings.Join(wantCols, ",") || strings.Join(types, ",") != strings.Join(wantTypes, ",") {
		t.Errorf("got %v %v\nwant %v %v", cols, types, wantCols, wantTypes)
	}
	if c, ty := pgInferColumns("/nonexistent/x.csv", ','); c != nil || ty != nil {
		t.Errorf("unreadable file must yield nil, got %v %v", c, ty)
	}
}

// The default dialect renders exactly what it did before -dialect existed.
func TestDialectDuckDBUnchanged(t *testing.T) {
	sql := assembleFromCommands(t,
		"ssql from csv data.csv -sample 5",
		"ssql where -if name regex ^A",
		"ssql group-by dept -median salary med -first name f -collect level lv -arg-max name salary top -mode level m -string-agg name , names",
		"ssql exclude m",
	)
	wantAll(t, sql, "(dialect: duckdb)", "FROM 'data.csv' USING SAMPLE 5 ROWS (reservoir)", "regexp_matches(name, '^A')",
		"median(salary)", "first(name)", "LIST(level)", "arg_max(name, salary)", "mode(level)", "string_agg(name, ',')", "* EXCLUDE (m)")
	wantNone(t, sql, `"dept"`, "AS __q", "\\copy")
}

func TestDialectPostgres(t *testing.T) {
	file := dialectFixture(t)

	t.Run("source_prologue_and_spellings", func(t *testing.T) {
		sql := mustDialect(t, dialectPostgres,
			"ssql from csv "+file,
			"ssql where -if Name regex ^A",
			"ssql group-by flag -median score med -percentile score 0.9 p90 -first Name f -last Name l -any Name a -collect age ages -arg-max Name score top -arg-min Name score bottom -mode note m -string-agg age , agelist",
		)
		wantAll(t, sql,
			"(dialect: postgres)",
			`--   CREATE TABLE IF NOT EXISTS "emp_data" ("Name" TEXT, "age" BIGINT, "score" DOUBLE PRECISION, "flag" BOOLEAN, "note" TEXT, "hire_date" DATE);`,
			`--   \copy "emp_data" FROM '`+file+`' CSV HEADER`,
			`FROM "emp_data"`,
			`("Name" ~ '^A')`,
			`percentile_cont(0.5) WITHIN GROUP (ORDER BY "score") AS "med"`,
			`percentile_cont(0.9) WITHIN GROUP (ORDER BY "score") AS "p90"`,
			`(array_agg("Name"))[1] AS "f"`,
			`(array_agg("Name"))[cardinality(array_agg("Name"))] AS "l"`,
			`any_value("Name") AS "a"`,
			`array_agg("age") AS "ages"`,
			`(array_agg("Name" ORDER BY "score" DESC))[1] AS "top"`,
			`(array_agg("Name" ORDER BY "score" ASC))[1] AS "bottom"`,
			`mode() WITHIN GROUP (ORDER BY "note") AS "m"`,
			`string_agg(CAST("age" AS TEXT), ',') AS "agelist"`,
			`GROUP BY "flag"`,
		)
		wantNone(t, sql, "regexp_matches", "LIST(", "quantile_cont", "arg_max", "median(")
	})

	t.Run("star_rewrites_become_column_lists", func(t *testing.T) {
		sql := mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql exclude note hire_date")
		wantAll(t, sql, `SELECT "Name", "age", "score", "flag"`)
		wantNone(t, sql, "EXCLUDE")

		sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql rename -as Name who")
		wantAll(t, sql, `SELECT "Name" AS "who", "age", "score", "flag", "note", "hire_date"`)
		wantNone(t, sql, "RENAME")

		sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql cast -type age float -type Name string")
		wantAll(t, sql, `SELECT CAST("Name" AS TEXT) AS "Name", CAST("age" AS DOUBLE PRECISION) AS "age", "score"`)
		wantNone(t, sql, "REPLACE", "VARCHAR", "AS DOUBLE)")

		sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql update -if age gt 26 -set flag false")
		wantAll(t, sql, `CASE WHEN "age" > 26 THEN FALSE ELSE "flag" END AS "flag", "note"`)
		wantNone(t, sql, "REPLACE")

		sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql fill -default note none")
		wantAll(t, sql, `COALESCE("note", 'none') AS "note", "hire_date"`)
	})

	t.Run("subqueries_are_aliased", func(t *testing.T) {
		sql := mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql limit 2", "ssql group-by flag -count n")
		wantAll(t, sql, ") AS __q")
		sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql group-by flag age -count n -cube")
		wantAll(t, sql, ") AS __q", `(__d."flag" IS NOT DISTINCT FROM __s1."flag")`)
		sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql unpivot -id Name -value age -value score")
		wantAll(t, sql, `SELECT "Name" AS "Name", 'age' AS "name", "age" AS "value" FROM "emp_data" WHERE "age" IS NOT NULL`, "UNION ALL", `'score' AS "name", "score" AS "value"`, ") AS __q")
		wantNone(t, sql, "UNPIVOT")
	})

	t.Run("sampling", func(t *testing.T) {
		sql := mustDialect(t, dialectPostgres, "ssql from csv "+file+" -sample 2")
		wantAll(t, sql, `(SELECT * FROM "emp_data" ORDER BY random() LIMIT 2) AS __q`)
		sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, "ssql sample -percent 10")
		wantAll(t, sql, `WHERE random() < 10 / 100.0) AS __q`)
		wantNone(t, sql, "USING SAMPLE")
	})

	t.Run("multi_file_union", func(t *testing.T) {
		other := filepath.Join(filepath.Dir(file), "more.csv")
		os.WriteFile(other, []byte("Name,age,score,flag,note,hire_date\nDan,41,0.5,false,,2018-01-01\n"), 0o644)
		sql := mustDialect(t, dialectPostgres, "ssql from csv "+file+" "+other)
		wantAll(t, sql, `(SELECT * FROM "emp_data" UNION ALL SELECT * FROM "more") AS __q`, `\copy "more" FROM`)
	})

	t.Run("expression_predicates", func(t *testing.T) {
		sql := mustDialect(t, dialectPostgres, "ssql from csv "+file, `ssql where -if-expr 'Name contains "li" && Name endsWith "e" && Name matches "^A"'`)
		wantAll(t, sql, `(strpos("Name", 'li') > 0)`, `(right("Name", length('e')) = 'e')`, `("Name" ~ '^A')`)
	})

	t.Run("refusals", func(t *testing.T) {
		for _, tc := range []struct {
			cmds []string
			want string
		}{
			{[]string{"ssql from parquet x.parquet"}, "has no postgres translation"},
			{[]string{"ssql from lines x.txt"}, "from lines has no postgres translation"},
			{[]string{"ssql from csv " + file + " -last 2"}, "from -last has no postgres translation"},
			{[]string{"ssql from csv " + file, "ssql window -order age -median score m"}, "window -median has no postgres translation"},
			{[]string{"ssql from csv " + file, "ssql window -order age -count-distinct note m"}, "window -count-distinct has no postgres translation"},
			{[]string{"ssql from csv " + file, "ssql sort age", "ssql fill -down note"}, "fill -down has no postgres translation"},
			{[]string{"ssql from csv " + file, "ssql describe"}, "describe has no postgres translation"},
			{[]string{"ssql from csv " + file, "ssql extract -field Name -re '(?P<x>.)' -skip"}, "extract has no postgres translation"},
			{[]string{"ssql from csv /nonexistent/gone.csv", "ssql exclude a"}, "exclude has no postgres translation"},
		} {
			_, err := assembleDialect(t, dialectPostgres, tc.cmds...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v: want error containing %q, got %v", tc.cmds, tc.want, err)
			}
		}
	})
}

func TestDialectDataFusion(t *testing.T) {
	file := dialectFixture(t)
	sql := mustDialect(t, dialectDataFusion,
		"ssql from csv "+file,
		"ssql where -if Name regex ^A",
		"ssql group-by flag -median score med -percentile score 0.9 p90 -first Name f -last Name l -any Name a -collect age ages -arg-max Name score top -string-agg age , agelist",
		"ssql exclude f",
	)
	wantAll(t, sql,
		"(dialect: datafusion)",
		"FROM '"+file+"'",
		`regexp_like("Name", '^A')`,
		`median("score") AS "med"`,
		`percentile_cont(0.9) WITHIN GROUP (ORDER BY "score") AS "p90"`,
		`first_value("Name") AS "f"`, `last_value("Name") AS "l"`, `first_value("Name") AS "a"`,
		`array_agg("age") AS "ages"`,
		`first_value("Name" ORDER BY "score" DESC) AS "top"`,
		`string_agg("age", ',') AS "agelist"`,
		`* EXCLUDE ("f")`,
	)
	wantNone(t, sql, "\\copy", "AS __q", "CAST(\"age\" AS TEXT)")

	// RENAME is the one `SELECT *` modifier DataFusion lacks; REPLACE it has.
	sql = mustDialect(t, dialectDataFusion, "ssql from csv "+file, "ssql rename -as Name who")
	wantAll(t, sql, `SELECT "Name" AS "who", "age"`)
	wantNone(t, sql, "RENAME")
	sql = mustDialect(t, dialectDataFusion, "ssql from csv "+file, "ssql cast -type age float")
	wantAll(t, sql, `* REPLACE (CAST("age" AS DOUBLE) AS "age")`)

	// Sampling and UNPIVOT take the portable forms; no alias needed.
	sql = mustDialect(t, dialectDataFusion, "ssql from csv "+file+" -sample 2", "ssql unpivot -id Name -value age -value score")
	wantAll(t, sql, "ORDER BY random() LIMIT 2)", "UNION ALL", `'age' AS "name", "age" AS "value"`)
	wantNone(t, sql, "USING SAMPLE", "UNPIVOT", "AS __q")

	// Integer division is float division in expr-lang and DuckDB, not in
	// DataFusion or Postgres.
	sql = mustDialect(t, dialectDataFusion, "ssql from csv "+file, `ssql update -set-expr half 'age / 2'`)
	wantAll(t, sql, `(CAST("age" AS DOUBLE) / 2) AS "half"`)
	sql = mustDialect(t, dialectPostgres, "ssql from csv "+file, `ssql update -set-expr half 'age / 2'`)
	wantAll(t, sql, `(CAST("age" AS DOUBLE PRECISION) / 2) AS "half"`)
	sql = assembleFromCommands(t, "ssql from csv "+file, `ssql update -set-expr half 'age / 2'`)
	wantAll(t, sql, `(age / 2) AS half`)

	// Windowed registry aggregates: bounded-frame string_agg cannot slide
	// in DataFusion, the unbounded form can; median over a frame is fine.
	sql = mustDialect(t, dialectDataFusion, "ssql from csv "+file, "ssql window -order age -string-agg Name , who -median score m")
	wantAll(t, sql, `string_agg("Name", ',') OVER`, `median("score") OVER`)

	for _, tc := range []struct {
		cmds []string
		want string
	}{
		{[]string{"ssql from csv " + file, "ssql group-by flag -mode note m"}, "-mode has no datafusion translation"},
		{[]string{"ssql from lines x.txt"}, "from lines has no datafusion translation"},
		{[]string{"ssql from tsv x.tsv"}, "from x.tsv has no datafusion translation"},
		{[]string{"ssql from csv " + file, "ssql resample -every 1m -time hire_date"}, "resample has no datafusion translation"},
		{[]string{"ssql from csv " + file, "ssql window -order age -arg-max Name score top"}, "window -arg-max has no datafusion translation"},
		{[]string{"ssql from csv " + file, "ssql window -order age -percentile score 0.9 p"}, "window -percentile has no datafusion translation"},
		{[]string{"ssql from csv " + file, "ssql window -order age -preceding 1 -string-agg Name , who"}, "window -string-agg with a bounded frame has no datafusion translation"},
		{[]string{"ssql from csv " + file, "ssql window -order age -range-preceding 5 -string-agg Name , who"}, "window -string-agg with a bounded frame has no datafusion translation"},
	} {
		_, err := assembleDialect(t, dialectDataFusion, tc.cmds...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want error containing %q, got %v", tc.cmds, tc.want, err)
		}
	}
	// The dialect is restored after each assembly.
	if sqlDialectCur != dialectDuckDB || pgLoads != nil {
		t.Errorf("dialect state leaked: %q %v", sqlDialectCur, pgLoads)
	}
}
