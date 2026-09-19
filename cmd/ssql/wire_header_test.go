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
