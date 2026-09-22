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

// TestSignalCommandsEmitSchemaHeader (DFC128 D4): fft, ifft, convolve,
// correlate and spectrogram build fresh records with known fields, so
// they write the `_schema` line every other command writes. Without it
// the next stage read headerless JSONL and — before D2 — grew a leading
// `_line_number` column in `to table`.
func TestSignalCommandsEmitSchemaHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("t,value\n")
	for i := 0; i < 32; i++ {
		// a small square-ish wave: distinct enough for every command
		v := []string{"0", "1", "0", "-1"}[i%4]
		sb.WriteString(strings.Join([]string{string(rune('0' + i%10)), v}, ",") + "\n")
	}
	sig := filepath.Join(dir, "sig.csv")
	if err := os.WriteFile(sig, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	from := bin + " from " + sig + " | " + bin + " "
	for name, stage := range map[string]string{
		"fft":         from + "fft -field value -rate 100",
		"ifft":        from + "fft -field value -rate 100 -phase | " + bin + " ifft -magnitude magnitude -phase phase",
		"convolve":    from + "convolve -field value -kernel avg -size 3",
		"correlate":   from + "correlate -field value -auto",
		"spectrogram": from + "spectrogram -field value -window-size 8 -rate 100",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := exec.Command("bash", "-c", stage).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", stage, err, out)
			}
			first, _, _ := strings.Cut(string(out), "\n")
			if !strings.HasPrefix(first, `{"_schema":`) {
				t.Fatalf("first output line is not a schema header: %s", first)
			}
			table, err := exec.Command("bash", "-c", stage+" | "+bin+" to table").CombinedOutput()
			if err != nil {
				t.Fatalf("to table: %v\n%s", err, table)
			}
			if strings.Contains(string(table), "_line_number") {
				t.Errorf("to table shows _line_number:\n%s", table)
			}
		})
	}
}

// TestHeaderlessJSONLHasNoLineNumber (DFC128 D2): another tool's NDJSON
// (DuckDB COPY, psql row_to_json) has no `_schema` line; reading it must
// yield exactly its own fields — the library's synthetic `_line_number`
// never reaches a CLI stream, on stdin or from a file.
func TestHeaderlessJSONLHasNoLineNumber(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	f := filepath.Join(t.TempDir(), "export.jsonl")
	if err := os.WriteFile(f, []byte("{\"a\":1,\"b\":\"x\"}\n{\"a\":2,\"b\":\"y\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{
		bin + " from jsonl " + f + " | " + bin + " to csv",
		bin + " from " + f + " | " + bin + " to csv",
		"cat " + f + " | " + bin + " from jsonl - | " + bin + " to csv",
		"cat " + f + " | " + bin + " where -if a gt 0 | " + bin + " to csv",
	} {
		out, err := exec.Command("bash", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		if got, want := string(out), "a,b\n1,x\n2,y\n"; got != want {
			t.Errorf("%s\n got: %q\nwant: %q", script, got, want)
		}
	}
}

// TestTimeIntoStringFieldIsRFC3339 (DFC128 F3): writing a time into an
// existing string field used Go's Time.String() form, which nothing
// parses back — not ssql's own convertToTime, not DuckDB, not Postgres.
func TestTimeIntoStringFieldIsRFC3339(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	f := filepath.Join(t.TempDir(), "d.csv")
	if err := os.WriteFile(f, []byte("id,d\n1,2026-01-02 10:30:00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := bin + " from " + f + " | " + bin + " update -set-expr d 'date(d)' 2>/dev/null | " + bin + " to csv"
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := string(out), "id,d\n1,2026-01-02T10:30:00Z\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestNullInFirstRecordSurvives (DFC128 F1/D3): `from` infers the header
// from a bounded sample, not the first record, so a nullable column
// reaches every sink; a field that first appears after the sample is a
// loud, non-zero failure naming the field, the record and the remedy.
func TestNullInFirstRecordSurvives(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	jsonl := filepath.Join(dir, "export.jsonl")
	os.WriteFile(jsonl, []byte("{\"id\":1,\"note\":null}\n{\"id\":2,\"note\":\"hello\"}\n{\"id\":3,\"note\":\"x\",\"late\":7}\n"), 0o644)
	array := filepath.Join(dir, "export.json")
	os.WriteFile(array, []byte(`[{"id":1,"note":null},{"id":2,"note":"hello"},{"id":3,"note":"x","late":7}]`), 0o644)

	for _, src := range []string{"from jsonl " + jsonl, "from " + jsonl, "from json " + array, "from " + array} {
		out, err := exec.Command("bash", "-c", bin+" "+src+" | "+bin+" to csv").CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", src, err, out)
		}
		if got, want := string(out), "id,note,late\n1,,\n2,hello,\n3,x,7\n"; got != want {
			t.Errorf("%s\n got: %q\nwant: %q", src, got, want)
		}
	}
	for _, sink := range []string{"to table", "to markdown", "to json"} {
		out, err := exec.Command("bash", "-c", bin+" from jsonl "+jsonl+" | "+bin+" "+sink).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", sink, err, out)
		}
		for _, want := range []string{"note", "hello", "late", "7"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("%s lost %q:\n%s", sink, want, out)
			}
		}
	}

	// A field past the sample: loud, non-zero, actionable — never dropped.
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+bin+" from jsonl "+jsonl+" | "+bin+" to csv")
	cmd.Env = append(os.Environ(), "SSQL_SCHEMA_SAMPLE=2")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a field after the sample must fail the pipeline:\n%s", out)
	}
	for _, want := range []string{`field "late" first appears at record 3`, "2-record sample", "SSQL_SCHEMA_SAMPLE"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("error lacks %q:\n%s", want, out)
		}
	}
}

// TestDelimitedColumnOrderFromPipe: `… | ssql from csv -` kept its
// columns in ALPHABETICAL order (the reader's schema is name-sorted and
// only the file path re-read the header for the order); TSV lost the
// order for files too. The header row is now peeked without consuming
// the stream. Found verifying the Postgres `\copy … TO STDOUT` pipe for
// the codelab (DFC128 D5).
func TestDelimitedColumnOrderFromPipe(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	csvFile := filepath.Join(dir, "t.csv")
	tsvFile := filepath.Join(dir, "t.tsv")
	os.WriteFile(csvFile, []byte("zeta,alpha,\"mid,dle\"\n1,2,3\n"), 0o644)
	os.WriteFile(tsvFile, []byte("zeta\talpha\tmiddle\n1\t2\t3\n"), 0o644)
	for script, want := range map[string]string{
		"cat " + csvFile + " | " + bin + " from csv - | " + bin + " to csv": "zeta,alpha,\"mid,dle\"\n1,2,3\n",
		bin + " from " + csvFile + " | " + bin + " to csv":                  "zeta,alpha,\"mid,dle\"\n1,2,3\n",
		"cat " + tsvFile + " | " + bin + " from tsv - | " + bin + " to csv": "zeta,alpha,middle\n1,2,3\n",
		bin + " from " + tsvFile + " | " + bin + " to csv":                  "zeta,alpha,middle\n1,2,3\n",
		// a header that arrives before the data does (live stream)
		"(printf 'zeta,alpha\\n'; sleep 0.3; printf '1,2\\n') | " + bin + " from csv - | " + bin + " to csv": "zeta,alpha\n1,2\n",
	} {
		out, err := exec.Command("bash", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		if string(out) != want {
			t.Errorf("%s\n got: %q\nwant: %q", script, out, want)
		}
	}
}

// TestTimeWireType (DFC128 D1): `cast -type F time` makes a time column,
// the `_schema` header says so, the NEXT process reads it back as a time
// (methods work, where compares instants, sort is chronological), every
// sink renders RFC 3339, and a value or operand that is not a time stops
// the pipeline.
func TestTimeWireType(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	f := filepath.Join(t.TempDir(), "t.csv")
	os.WriteFile(f, []byte("id,ts\n1,2026-03-01 00:00:00\n2,2026-01-01T05:00:00\n3,2026-02-01\n4,2026-01-31 23:30:00+00\n"), 0o644)
	cast := bin + " from " + f + " | " + bin + " cast -type ts time"
	run := func(script string) (string, error) {
		out, err := exec.Command("bash", "-c", "set -o pipefail; "+script).CombinedOutput()
		return string(out), err
	}

	out, err := run(cast + " | head -1")
	if err != nil || !strings.Contains(out, `"ts":"time"`) {
		t.Fatalf("header must carry the time type: %v\n%s", err, out)
	}
	for script, want := range map[string]string{
		cast + " | " + bin + " sort ts | " + bin + " include id | " + bin + " to csv":                                          "id\n2\n4\n3\n1\n",
		cast + " | " + bin + " where -if ts ge 2026-02-01 | " + bin + " sort ts | " + bin + " include id | " + bin + " to csv": "id\n3\n1\n",
		cast + " | " + bin + " update -set-expr h 'ts.Hour()' | " + bin + " where -if id eq 4 | " + bin + " to csv":            "id,ts,h\n4,2026-01-31T23:30:00Z,23\n",
		cast + " | " + bin + " where -if id eq 2 | " + bin + " to tsv":                                                         "id\tts\n2\t2026-01-01T05:00:00Z\n",
		bin + " from csv " + f + " -type ts time | " + bin + " where -if id eq 3 | " + bin + " to csv":                         "id,ts\n3,2026-02-01T00:00:00Z\n",
	} {
		if out, err := run(script); err != nil || out != want {
			t.Errorf("%s\n got: %q (%v)\nwant: %q", script, out, err, want)
		}
	}
	for _, sink := range []string{"to table", "to markdown", "to json", "to jsonl"} {
		out, err := run(cast + " | " + bin + " where -if id eq 1 | " + bin + " " + sink)
		if err != nil || !strings.Contains(out, "2026-03-01T00:00:00Z") || strings.Contains(out, "+0000 UTC") {
			t.Errorf("%s must render RFC 3339: %v\n%s", sink, err, out)
		}
	}
	for script, want := range map[string]string{
		bin + " from " + f + " | " + bin + " update -set ts junk | " + bin + " cast -type ts time": `field "ts" value "junk" is not a time`,
		cast + " | " + bin + " where -if ts gt banana":                                             `field "ts" is a time but "banana" is not`,
	} {
		out, err := run(script)
		if err == nil || !strings.Contains(out, want) {
			t.Errorf("%s must fail with %q, got %v\n%s", script, want, err, out)
		}
	}
}

// TestNullColumnsKeepTheirNames (DFC128 §6g): a column that is NULL in
// EVERY record still has a name and a place — in the header and in every
// sink — through all four JSON entry points; a NULL in a later row of an
// int column is not 0; `where` on a column whose first row is NULL works,
// and a field that really is unknown is still an error.
func TestNullColumnsKeepTheirNames(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	jl := filepath.Join(dir, "x.jsonl")
	arr := filepath.Join(dir, "x.json")
	os.WriteFile(jl, []byte("{\"id\":1,\"n\":5,\"gone\":null}\n{\"id\":2,\"n\":null,\"gone\":null}\n{\"id\":3,\"n\":7,\"gone\":null}\n"), 0o644)
	os.WriteFile(arr, []byte(`[{"id":1,"n":5,"gone":null},{"id":2,"n":null,"gone":null},{"id":3,"n":7,"gone":null}]`), 0o644)
	run := func(script string) (string, error) {
		out, err := exec.Command("bash", "-c", "set -o pipefail; "+script).CombinedOutput()
		return string(out), err
	}
	for _, src := range []string{bin + " from " + jl, bin + " from " + arr, "cat " + jl + " | " + bin + " from jsonl -", "cat " + arr + " | " + bin + " from json -"} {
		out, err := run(src + " | " + bin + " include id n gone | " + bin + " to csv")
		if want := "id,n,gone\n1,5,\n2,,\n3,7,\n"; err != nil || out != want {
			t.Errorf("%s\n got: %q (%v)\nwant: %q", src, out, err, want)
		}
		if out, err := run(src + " | " + bin + " where -if n ge 0 | " + bin + " include id | " + bin + " to csv"); err != nil || out != "id\n1\n3\n" {
			t.Errorf("%s | where n ge 0: %q (%v) — a NULL must not match as 0", src, out, err)
		}
		if out, err := run(src + " | " + bin + " group-by gone -sum n total | " + bin + " to csv"); err != nil || !strings.Contains(out, "12") {
			t.Errorf("%s | sum: %q (%v)", src, out, err)
		}
	}
	hdr, _ := run(bin + " from " + jl + " | head -1")
	if !strings.Contains(hdr, `"gone":"string"`) || !strings.Contains(hdr, `"n":"int"`) {
		t.Errorf("header types: %s", hdr)
	}
	out, err := run(bin + " from " + jl + " | " + bin + " where -if nope gt 1")
	if err == nil || !strings.Contains(out, "unknown field(s): nope") {
		t.Errorf("an unknown field must still fail: %v\n%s", err, out)
	}
}

// TestHeaderDoesNotDependOnRowOrder (DFC133 row-order sweep): the `_schema`
// header — field set AND types — is a property of the data, not of which
// row happens to be first. A CSV whose first row had an empty cell wrote
// that column's type as "string" although the reader had typed the column
// float; a JSON array coerced a whole column to the type of its first
// value. Promoted from the sweep so the class stays closed.
func TestHeaderDoesNotDependOnRowOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	header := func(file string) string {
		out, err := exec.Command("bash", "-c", bin+" from "+file+" | head -1").CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", file, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pairs := [][2]string{
		{write("a1.csv", "id,pop,city\n1,,\n2,3.5,Oslo\n3,7,Lima\n"), write("a2.csv", "id,pop,city\n2,3.5,Oslo\n1,,\n3,7,Lima\n")},
		{write("b1.tsv", "id\tpop\n1\t\n2\t3.5\n"), write("b2.tsv", "id\tpop\n2\t3.5\n1\t\n")},
		{write("c1.jsonl", "{\"id\":1,\"pop\":null}\n{\"id\":2,\"pop\":3.5}\n"), write("c2.jsonl", "{\"id\":2,\"pop\":3.5}\n{\"id\":1,\"pop\":null}\n")},
		{write("d1.json", `[{"id":1,"pop":null},{"id":2,"pop":3.5}]`), write("d2.json", `[{"id":2,"pop":3.5},{"id":1,"pop":null}]`)},
	}
	for _, p := range pairs {
		h1, h2 := header(p[0]), header(p[1])
		if h1 != h2 {
			t.Errorf("header depends on row order:\n  %s: %s\n  %s: %s", filepath.Base(p[0]), h1, filepath.Base(p[1]), h2)
		}
		if !strings.Contains(h1, `"pop":"float"`) {
			t.Errorf("%s: pop must be typed float, got %s", filepath.Base(p[0]), h1)
		}
	}

	// A column that mixes 12 and "12": each value keeps its own type, so the
	// answer is the same in either order and in both JSON shapes. (It is 2:
	// the number 12, and the string "12", which is lexically above "10".)
	// The array reader used to coerce the column to its first value's type,
	// so the count depended on which element came first.
	var counts []string
	for _, f := range []string{
		write("m1.json", `[{"k":12},{"k":"12"},{"k":7}]`), write("m2.json", `[{"k":"12"},{"k":12},{"k":7}]`),
		write("m1.jsonl", "{\"k\":12}\n{\"k\":\"12\"}\n{\"k\":7}\n"), write("m2.jsonl", "{\"k\":\"12\"}\n{\"k\":12}\n{\"k\":7}\n"),
	} {
		out, err := exec.Command("bash", "-c", bin+" from "+f+" | "+bin+" where -if k gt 10 | "+bin+" count").CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", filepath.Base(f), err, out)
		}
		counts = append(counts, strings.TrimSpace(string(out)))
	}
	for _, c := range counts {
		if c != counts[0] || c != "2" {
			t.Errorf("a mixed column must give the same answer in every order and shape, got %q", counts)
			break
		}
	}
}

// TestCastIsStrict: a value that is not of the target type STOPS the
// pipeline — it used to become 0 or false, the behaviour DFC124 removed
// from the CSV reader — with one Error line naming the field, the value
// and the way out, in the interpreter and in generated programs alike (a
// generated program used to print a Go stack trace for a bad time, because
// the panic was a string). -invalid missing keeps the row and says how
// many values it could not convert.
func TestCastIsStrict(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "c.csv")
	os.WriteFile(f, []byte("id,score,flag,when\n1,10,yes,2026-01-02\n2,N/A,maybe,soon\n3,,no,\n"), 0o644)
	run := func(script string) (string, error) {
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
		cmd.Env = append(os.Environ(), "SSQL_MODULE_DIR="+repo)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for target, want := range map[string]string{
		"-type score int":   `field "score" value "N/A" is not an int`,
		"-type score float": `field "score" value "N/A" is not a float`,
		"-type flag bool":   `field "flag" value "maybe" is not a bool`,
		"-type when time":   `field "when" value "soon" is not a time`,
	} {
		for _, script := range []string{
			bin + " from " + f + " | " + bin + " cast " + target + " | " + bin + " to csv",
			bin + " generate go -run -mode record -pipeline '" + bin + " from " + f + " | " + bin + " cast " + target + " | " + bin + " to csv'",
			bin + " generate go -run -mode typed -pipeline '" + bin + " from " + f + " | " + bin + " cast " + target + " | " + bin + " to csv'",
		} {
			out, err := run(script)
			if err == nil || !strings.Contains(out, want) || !strings.Contains(out, "-invalid missing") {
				t.Errorf("%s\n must fail with %q and name the way out; got err=%v\n%s", script, want, err, out)
			}
			if strings.Contains(out, "goroutine ") || strings.Contains(out, "panic:") {
				t.Errorf("%s printed a Go stack trace instead of one Error line:\n%s", script, out)
			}
		}
	}
	out, err := run(bin + " from " + f + " | " + bin + " cast -type score int -type flag bool -invalid missing | " + bin + " to csv")
	if err != nil || !strings.Contains(out, "2 values could not be converted") || !strings.Contains(out, "\n2,,,soon\n") || !strings.Contains(out, "\n3,,false,\n") {
		t.Errorf("-invalid missing must keep the rows, leave bad values empty and count them: %v\n%s", err, out)
	}
	if out, err := run(bin + " from " + f + " | " + bin + " cast -type score int -invalid zero"); err == nil || !strings.Contains(out, "unknown -invalid") {
		t.Errorf("an unknown -invalid mode must be refused: %v\n%s", err, out)
	}
}

// TestMalformedJSONLIsAnError (DFC133 §7.6): at the CLI, a line that is
// not JSON stops the pipeline — from a file, on a stage's stdin, in a JSON
// array — with the line number and the way out. `-skip-invalid` reads a
// dirty file and says how many lines it skipped. It used to skip them
// silently; and a stage whose FIRST stdin line was not JSON returned
// nothing at all, exit 0.
func TestMalformedJSONLIsAnError(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	dirty := filepath.Join(dir, "dirty.jsonl")
	os.WriteFile(dirty, []byte("{\"id\":1}\nnot json at all\n{\"id\":2}\n"), 0o644)
	run := func(script string) (string, error) {
		out, err := exec.Command("bash", "-c", "set -o pipefail; "+script).CombinedOutput()
		return string(out), err
	}
	for _, script := range []string{
		bin + " from jsonl " + dirty + " | " + bin + " to csv",
		bin + " from " + dirty + " | " + bin + " to csv",
		"cat " + dirty + " | " + bin + " where -if id gt 0 | " + bin + " to csv",
		"cat " + dirty + " | " + bin + " to csv",
	} {
		out, err := run(script)
		if err == nil || !strings.Contains(out, "line 2 is not JSON") {
			t.Errorf("%s\n must fail naming line 2; got err=%v\n%s", script, err, out)
		}
	}
	out, err := run(bin + " from jsonl " + dirty + " -skip-invalid | " + bin + " to csv")
	if err != nil || !strings.Contains(out, "skipped 1 line that were not JSON") || !strings.Contains(out, "id\n1\n2\n") {
		t.Errorf("-skip-invalid must read the good lines and report the skip: %v\n%s", err, out)
	}
	out, err = run("printf '[\\n{\"id\":1}\\n]\\n' | " + bin + " where -if id gt 0")
	if err == nil || !strings.Contains(out, "JSON ARRAY") || !strings.Contains(out, "from json") {
		t.Errorf("an array on a stage's stdin must be named as one (it used to give an empty result, exit 0): %v\n%s", err, out)
	}
	out, err = run("printf '[{\"id\":1}, oops]' | " + bin + " from json - | " + bin + " to csv")
	if err == nil || !strings.Contains(out, "element 2 is not a JSON object") {
		t.Errorf("a bad array element must be an error: %v\n%s", err, out)
	}
	out, err = run("export SSQL_MODE=record; " + bin + " from jsonl " + dirty + " -skip-invalid | " + bin + " generate go")
	if err == nil || !strings.Contains(out, "no generated form yet") {
		t.Errorf("-skip-invalid in generation mode must refuse, not emit a strict program: %v\n%s", err, out)
	}
}

// TestGeneratedJoinReadsAWholeSideFile: generated record-mode `join FILE.jsonl`
// opened the side file and DEFERRED ITS CLOSE inside the function that
// returned the lazy reader — closed before anyone read it. A small file was
// already in the 64 KB buffer and appeared to work; a 450 KB side file
// joined 214 of 20,000 rows, exit 0, because the read error was ignored
// (v4.102.0 and earlier). Found when the JSON Lines readers became strict
// (DFC133 §7.6); ssql.CloseWhenDone closes when the reader is finished.
func TestGeneratedJoinReadsAWholeSideFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary and a generated program")
	}
	bin := corpusBin(t)
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var left, right strings.Builder
	left.WriteString("id,v\n")
	right.WriteString("id,w\n")
	const n = 20000 // ~450 KB of JSON Lines: several buffers' worth
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&left, "%d,%d\n", i, i*10)
		fmt.Fprintf(&right, "%d,%d\n", i, i*7)
	}
	os.WriteFile(filepath.Join(dir, "left.csv"), []byte(left.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "right.csv"), []byte(right.String()), 0o644)
	run := func(script string) string {
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "SSQL_MODULE_DIR="+repo)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, stderr.String())
		}
		return strings.TrimSpace(stdout.String())
	}
	run(bin + " from csv right.csv | " + bin + " tee side.jsonl > /dev/null")
	pipeline := bin + " from csv left.csv | " + bin + " join side.jsonl -using id | " + bin + " count"
	if got := run(pipeline); got != fmt.Sprint(n) {
		t.Fatalf("interpreter joined %s rows, want %d", got, n)
	}
	if got := run(bin + " generate go -run -mode record -pipeline '" + pipeline + "'"); got != fmt.Sprint(n) {
		t.Errorf("generated record-mode join read %s of %d side-file rows", got, n)
	}
}
