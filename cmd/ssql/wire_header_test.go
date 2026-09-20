package main

import (
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
		bin + " from " + f + " | " + bin + " update -set ts junk | " + bin + " cast -type ts time": `field "ts" value junk is not a time`,
		cast + " | " + bin + " where -if ts gt banana":                                             `the field is a time but "banana" is not`,
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
