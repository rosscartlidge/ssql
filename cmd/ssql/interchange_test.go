package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The interchange gates (DFC128 D6). The codelab claims ssql's JSON and
// CSV go to and from DuckDB and PostgreSQL; the paragraph that first said
// so was written from a check that never ran, and running it for real
// found a dropped nullable column, a synthetic `_line_number`, scrambled
// CSV column order and an unparseable timestamp form. These tests are the
// claim, executed: each engine, both directions, with the data that broke
// things — a NULL in the FIRST row, a zoneless and a zoned timestamp, a
// whole-number float — asserting row counts, FIELD SETS and column order,
// not just "it ran". DuckDB is gated on its binary like the equivalence
// lane; Postgres on SSQL_TEST_PG_HOST (psql over ssh, SQL on stdin — see
// doc/research/ssh-test-environment.md).

func ixRun(t *testing.T, stdin, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\nstderr:\n%s\nstdout:\n%s", script, err, stderr.String(), stdout.String())
	}
	return stdout.String()
}

func ixWant(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s\n got: %q\nwant: %q", what, got, want)
	}
}

func TestDuckDBInterchange(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	duckdb := duckdbBinary()
	if duckdb == "" {
		t.Skip("duckdb not found (PATH or ~/.local/bin)")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	sql := func(stmt string) string {
		t.Helper()
		f := filepath.Join(dir, "q.sql")
		os.WriteFile(f, []byte(stmt), 0o644)
		// SQL from a file on stdin: an empty stdin opens DuckDB's shell.
		return ixRun(t, "", "cd "+dir+" && timeout 60 "+duckdb+" -csv < "+f)
	}

	// --- DuckDB → ssql: NULL in the first row, a timestamp, a whole float.
	sql(`CREATE TABLE t AS SELECT * FROM (VALUES
	       (1, NULL,    TIMESTAMP '2026-01-02 10:30:00', 2.0),
	       (2, 'hello', TIMESTAMP '2026-02-03 11:00:00', 3.5)) v(id, note, ts, amount);
	     COPY t TO 'nd.json';
	     COPY t TO 'arr.json' (ARRAY true);`)
	const rows = "id,note,amount\n1,,2\n2,hello,3.5\n"
	for _, src := range []string{"from nd.json", "from jsonl nd.json", "from arr.json", "from json arr.json"} {
		got := ixRun(t, "", "cd "+dir+" && "+bin+" "+src+" | "+bin+" include id note amount | "+bin+" to csv")
		ixWant(t, "DuckDB COPY → ssql "+src+" (the nullable column must survive, no _line_number)", got, rows)
	}
	// The field SET, exactly: nothing synthetic, nothing dropped.
	hdr := ixRun(t, "", "cd "+dir+" && "+bin+" from nd.json | head -1")
	for _, f := range []string{`"id"`, `"note"`, `"ts"`, `"amount"`} {
		if !strings.Contains(hdr, f) {
			t.Errorf("header lacks %s: %s", f, hdr)
		}
	}
	if strings.Contains(hdr, "_line_number") {
		t.Errorf("header has a synthetic field: %s", hdr)
	}
	// duckdb -json on a pipe.
	os.WriteFile(filepath.Join(dir, "q2.sql"), []byte("SELECT id, note FROM 'nd.json' ORDER BY id"), 0o644)
	got := ixRun(t, "", "cd "+dir+" && timeout 60 "+duckdb+" -json < q2.sql | "+bin+" from json - | "+bin+" include id note | "+bin+" to csv")
	ixWant(t, "duckdb -json | ssql from json -", got, "id,note\n1,\n2,hello\n")
	// DuckDB's timestamp form becomes a time, and computes.
	got = ixRun(t, "", "cd "+dir+" && "+bin+" from nd.json | "+bin+" cast -type ts time | "+bin+" update -set-expr h 'ts.Hour()' | "+bin+" include id ts h | "+bin+" to csv")
	ixWant(t, "DuckDB timestamp → cast time", got, "id,ts,h\n1,2026-01-02T10:30:00Z,10\n2,2026-02-03T11:00:00Z,11\n")

	// --- ssql → DuckDB: to jsonl (no _schema line) and to json (one array);
	// a `time` column must arrive as a TIMESTAMP, the nullable one as NULL.
	ixRun(t, "", "cd "+dir+" && "+bin+" from nd.json | "+bin+" cast -type ts time | "+bin+" to jsonl back.jsonl")
	ixRun(t, "", "cd "+dir+" && "+bin+" from nd.json | "+bin+" cast -type ts time | "+bin+" to json back.json")
	for _, f := range []string{"back.jsonl", "back.json"} {
		got := sql("SELECT count(*) AS n, count(note) AS notes, sum(amount) AS total, typeof(min(ts)) AS ts_type, min(ts) AS first FROM read_json_auto('" + f + "')")
		ixWant(t, "ssql → DuckDB read_json_auto("+f+")", got, "n,notes,total,ts_type,first\n2,1,5.5,TIMESTAMP,2026-01-02 10:30:00\n")
	}
	// With the header left in (a plain redirect, not `to jsonl`) DuckDB sees a
	// phantom row — the codelab's warning, pinned so the advice stays true.
	ixRun(t, "", "cd "+dir+" && "+bin+" from nd.json > withheader.jsonl")
	got = sql("SELECT count(*) AS n FROM read_json_auto('withheader.jsonl')")
	ixWant(t, "a _schema line is one phantom row to DuckDB", got, "n\n3\n")

	// CSV both ways keeps the header's column order.
	sql("COPY (SELECT id, note, amount FROM 'nd.json' ORDER BY id) TO 'out.csv' (HEADER)")
	got = ixRun(t, "", "cd "+dir+" && cat out.csv | "+bin+" from csv - | "+bin+" to csv")
	ixWant(t, "DuckDB CSV | ssql from csv - (column order)", got, "id,note,amount\n1,,2\n2,hello,3.5\n")
}

func TestPostgresInterchange(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	host := os.Getenv("SSQL_TEST_PG_HOST")
	if host == "" {
		t.Skip("SSQL_TEST_PG_HOST not set (the LXD rig: ssql-node1)")
	}
	bin := corpusBin(t)
	psql := func(script string) string {
		t.Helper()
		cmd := exec.Command("ssh", host, "sudo", "-u", "postgres", "psql", "-X", "-At", "-q", "-v", "ON_ERROR_STOP=1", "-d", "ssql")
		cmd.Stdin = strings.NewReader(script)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("psql: %v\n%s\nscript:\n%s", err, stderr.String(), script)
		}
		return stdout.String()
	}
	tbl := fmt.Sprintf("ssql_ix_%d", os.Getpid())
	back := tbl + "_back"
	t.Cleanup(func() { psql("DROP TABLE IF EXISTS " + tbl + "; DROP TABLE IF EXISTS " + back + ";") })
	psql(fmt.Sprintf(`DROP TABLE IF EXISTS %[1]s;
CREATE TABLE %[1]s (id bigint, note text, ts timestamp, tz timestamptz, amount double precision);
INSERT INTO %[1]s VALUES (1, NULL, '2026-01-02 10:30:00', '2026-01-02 10:30:00+00', 2),
                         (2, 'hello', '2026-02-03 11:00:00', '2026-02-03 11:00:00+00', 3.5);`, tbl))

	// --- Postgres → ssql.
	const rows = "id,note,amount\n1,,2\n2,hello,3.5\n"
	nd := psql("SELECT row_to_json(t) FROM " + tbl + " t ORDER BY id;")
	ixWant(t, "row_to_json | ssql from jsonl -", ixRun(t, nd, bin+" from jsonl - | "+bin+" include id note amount | "+bin+" to csv"), rows)
	arr := psql("SELECT json_agg(t ORDER BY id) FROM " + tbl + " t;")
	ixWant(t, "json_agg | ssql from json -", ixRun(t, arr, bin+" from json - | "+bin+" include id note amount | "+bin+" to csv"), rows)
	csv := psql("\\copy (SELECT id, note, amount FROM " + tbl + " ORDER BY id) TO STDOUT CSV HEADER")
	ixWant(t, "\\copy TO STDOUT | ssql from csv - (column order, NULL as empty)", ixRun(t, csv, bin+" from csv - | "+bin+" to csv"), rows)

	// Both Postgres timestamp types, in both export shapes, become times.
	// JSON: 2026-01-02T10:30:00 and …+00:00; CSV: 2026-01-02 10:30:00 and …+00.
	const hours = "id,a,b\n1,10,10\n2,11,11\n"
	castHours := " | " + bin + " cast -type ts time -type tz time | " + bin + " update -set-expr a 'ts.Hour()' -set-expr b 'tz.UTC().Hour()' | " + bin + " include id a b | " + bin + " to csv"
	ixWant(t, "Postgres JSON timestamps → cast time", ixRun(t, nd, bin+" from jsonl -"+castHours), hours)
	tsCSV := psql("\\copy (SELECT id, ts, tz FROM " + tbl + " ORDER BY id) TO STDOUT CSV HEADER")
	ixWant(t, "Postgres CSV timestamps → cast time", ixRun(t, tsCSV, bin+" from csv -"+castHours), hours)
	ixWant(t, "date() reads the zoneless JSON form", ixRun(t, nd, bin+" from jsonl - | "+bin+" update -set-expr y 'date(ts).Year()' | "+bin+" include id y | "+bin+" to csv"), "id,y\n1,2026\n2,2026\n")

	// --- ssql → Postgres: CSV through \copy FROM STDIN; the empty cell is
	// NULL, a `time` column loads into timestamptz.
	out := ixRun(t, nd, bin+" from jsonl - | "+bin+" cast -type ts time | "+bin+" include id note ts amount | "+bin+" to csv")
	got := psql(fmt.Sprintf("CREATE TABLE %[1]s (id bigint, note text, ts timestamptz, amount double precision);\n\\copy %[1]s FROM STDIN CSV HEADER\n%[2]s\\.\nSELECT id, note IS NULL, ts AT TIME ZONE 'UTC', amount FROM %[1]s ORDER BY id;", back, out))
	ixWant(t, "ssql to csv | \\copy FROM STDIN", got, "1|t|2026-01-02 10:30:00|2\n2|f|2026-02-03 11:00:00|3.5\n")
	// …and as JSON Lines through a jsonb staging column.
	jl := ixRun(t, nd, bin+" from jsonl - | "+bin+" include id note | "+bin+" to jsonl")
	got = psql(fmt.Sprintf("CREATE TEMP TABLE stage (doc jsonb);\n\\copy stage FROM STDIN\n%s\\.\nSELECT doc->>'id', doc ? 'note' FROM stage ORDER BY 1;", jl))
	ixWant(t, "ssql to jsonl → jsonb (absent field, not null)", got, "1|f\n2|t\n")
}
