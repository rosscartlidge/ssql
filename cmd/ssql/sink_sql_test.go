package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sinkSQLFixture has what a file sink can get wrong: a delimiter and a quote
// in a cell, empty cells, a decimal's trailing zero, a time with an offset
// and one with a fraction.
const sinkSQLFixture = `k,s,f,d,t,b
1,"a,b",1.5,12.30,2024-03-05T01:02:03Z,true
2,,1e20,,2024-03-05T01:02:03.5+05:30,false
3,"q""x",0.1,3.00,,
`

const sinkSQLSource = "from csv t.csv -type d decimal:2 -type t time -type b bool"

func sinkShell(t *testing.T, dir, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TZ=Australia/Sydney", "PATH="+filepath.Join(os.Getenv("HOME"), ".local", "bin")+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// sinkRows reads a written file back through ssql, by its own format and
// with its declared types, into rows with an absent value left out (the
// engines write null where ssql writes an empty string or nothing) and a time
// as text in the pipeline zone (exec's file keeps a cell's own offset, an
// engine's has the instant).
func sinkRows(t *testing.T, dir, bin, file string) []map[string]any {
	t.Helper()
	kind := strings.TrimPrefix(filepath.Ext(file), ".")
	reader := bin + " from " + kind + " " + file
	if kind == "csv" || kind == "tsv" {
		reader += " -type d decimal:2 -type t time -type b bool"
	}
	script := reader + " | " + bin + " cast -type t time | " + bin + " cast -type t string | " + bin + " to jsonl"
	out, err := sinkShell(t, dir, script)
	if err != nil {
		t.Fatalf("reading %s back: %v\n%s", file, err, out)
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var row map[string]any
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&row); err != nil {
			t.Fatalf("%s: %v in %q", file, err, line)
		}
		if _, schema := row["_schema"]; schema {
			continue
		}
		for k, v := range row {
			if v == nil || v == "" {
				delete(row, k)
			}
			if n, ok := v.(json.Number); ok {
				f, _ := n.Float64()
				row[k] = f
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// TestGenerateSQLWritesTheSinkFile: `to csv FILE` in generate sql printed the
// rows and wrote nothing, exit 0, in every dialect (Ross, 2026-11-07). The
// file DuckDB writes reads back as the file exec writes.
func TestGenerateSQLWritesTheSinkFile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs duckdb")
	}
	if duckdbBinary() == "" {
		t.Skip("duckdb not found")
	}
	bin := corpusBin(t)
	for _, sink := range []struct{ kind, file string }{
		{"csv", "o.csv"}, {"tsv", "o.tsv"}, {"jsonl", "o.jsonl"}, {"json", "o.json"}, {"parquet", "o.parquet"},
	} {
		t.Run(sink.kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "exec"), 0o755); err != nil {
				t.Fatal(err)
			}
			src := bin + " " + sinkSQLSource + " | " + bin + " where -if k gt 0 | "
			if out, err := sinkShell(t, dir, src+bin+" to "+sink.kind+" exec/"+sink.file); err != nil {
				t.Fatalf("exec: %v\n%s", err, out)
			}
			pipeline := "ssql " + strings.ReplaceAll(sinkSQLSource+" | where -if k gt 0 | to "+sink.kind+" "+sink.file, " | ", " | ssql ")
			gen := "PATH=" + filepath.Dir(bin) + ":$PATH " + bin + " generate sql -run -pipeline '" + pipeline + "'"
			out, err := sinkShell(t, dir, gen)
			if err != nil {
				t.Fatalf("generate sql -run: %v\n%s", err, out)
			}
			if out != "" {
				t.Errorf("the rows belong in the file, stdout has:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(dir, sink.file)); err != nil {
				t.Fatalf("no file written: %v", err)
			}
			got, want := sinkRows(t, dir, bin, sink.file), sinkRows(t, dir, bin, "exec/"+sink.file)
			if len(want) != 3 || !reflect.DeepEqual(got, want) {
				t.Errorf("rows differ from exec's file\n got %v\nwant %v", got, want)
			}
		})
	}
}

// TestGenerateSQLCSVBytesWithoutTime: without a time column the file is exec's,
// byte for byte (quoting, empties, a decimal's scale).
func TestGenerateSQLCSVBytesWithoutTime(t *testing.T) {
	if testing.Short() {
		t.Skip("runs duckdb")
	}
	if duckdbBinary() == "" {
		t.Skip("duckdb not found")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := sinkShell(t, dir, bin+" "+sinkSQLSource+" | "+bin+" exclude t | "+bin+" to csv exec.csv")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, out)
	}
	out, err = sinkShell(t, dir, "PATH="+filepath.Dir(bin)+":$PATH "+bin+" generate sql -run -pipeline 'ssql "+sinkSQLSource+" | ssql exclude t | ssql to csv duck.csv'")
	if err != nil {
		t.Fatalf("generate sql -run: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "duck.csv"))
	want, _ := os.ReadFile(filepath.Join(dir, "exec.csv"))
	if string(got) != string(want) || len(want) == 0 {
		t.Errorf("bytes differ\n got %q\nwant %q", got, want)
	}
}

// TestGenerateSQLUnwritableSinkFails: the engine's error reaches the exit code.
func TestGenerateSQLUnwritableSinkFails(t *testing.T) {
	if testing.Short() {
		t.Skip("runs duckdb")
	}
	if duckdbBinary() == "" {
		t.Skip("duckdb not found")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := sinkShell(t, dir, "PATH="+filepath.Dir(bin)+":$PATH "+bin+" generate sql -run -pipeline 'ssql from csv t.csv | ssql to csv /nonexistent/x.csv'"); err == nil {
		t.Errorf("exit 0 with nothing written:\n%s", out)
	}
}

// TestSinkFileIsWrittenOrRefused: a backend that cannot write the file says
// so; it never answers with rows that go somewhere else.
func TestSinkFileIsWrittenOrRefused(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, generate, sink string }{
		{"postgres_csv", "sql -dialect postgres", "to csv o.csv"},
		{"postgres_parquet", "sql -dialect postgres", "to parquet o.parquet"},
		{"datafusion_csv", "sql -dialect datafusion", "to csv o.csv"},
		{"datafusion_jsonl", "sql -dialect datafusion", "to jsonl o.jsonl"},
		{"rust_jsonl", "rust", "to jsonl o.jsonl"},
		{"rust_time_format", "rust", "to csv o.csv -time-format %Y"},
		{"rust_time_format_stdout", "rust", "to csv -time-format %Y"},
		{"rust_sidecar", "rust", "to csv o.csv -sidecar"},
		{"tsv_separator_two_chars", "go", "to tsv o.tsv -separator ab"},
		{"rust_separator_multibyte", "rust", "to tsv o.tsv -separator →"},
		{"duckdb_sidecar", "sql", "to csv o.csv -sidecar"},
		{"duckdb_time_format", "sql", "to csv o.csv -time-format %Y"},
		{"duckdb_xlsx", "sql", "to xlsx o.xlsx"},
		{"duckdb_lines", "sql", "to lines s o.txt"},
		{"duckdb_markdown", "sql", "to markdown -output o.md"},
		{"duckdb_chart", "sql", "to chart -x k -y f"},
	} {
		t.Run(c.name, func(t *testing.T) {
			script := "PATH=" + filepath.Dir(bin) + ":$PATH " + bin + " generate " + c.generate + " -pipeline 'ssql from csv t.csv | ssql " + c.sink + "'"
			out, err := sinkShell(t, dir, script)
			if err == nil {
				t.Fatalf("not refused:\n%s", out)
			}
			if strings.Contains(out, "panic:") || !strings.Contains(out, c.sink[:strings.Index(c.sink[3:], " ")+3]) {
				t.Errorf("the refusal should name the sink:\n%s", out)
			}
		})
	}
	// Stdout sinks and the files rust writes are written.
	for _, c := range []struct{ generate, sink, want string }{
		{"sql", "to table", "SELECT"},
		{"sql", "to csv", "SELECT"},
		{"sql -dialect postgres", "to csv", "SELECT"},
		{"rust", "to parquet o.parquet", "write_parquet"},
		{"rust", "to csv o.csv", `File::create(path)`},
		{"rust", "to tsv o.tsv -separator ^", `with_delimiter(b'^')`},
		{"rust", "to tsv", `QuoteStyle::Never`},
	} {
		script := "PATH=" + filepath.Dir(bin) + ":$PATH " + bin + " generate " + c.generate + " -pipeline 'ssql from csv t.csv | ssql " + c.sink + "'"
		out, err := sinkShell(t, dir, script)
		if err != nil || !strings.Contains(out, c.want) || strings.Contains(out, "COPY") {
			t.Errorf("%s | %s: err %v\n%s", c.generate, c.sink, err, out)
		}
	}
}

// sinkUncommented drops comment lines: every generator echoes the pipeline in
// a header, so the sink's file is in the text whether it is written or not.
func sinkUncommented(out string) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "--") && !strings.HasPrefix(l, "//") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// TestEverySinkFileIsWrittenOrRefused asks the binary which sinks take a file
// and holds every generating backend to it: the program names that file, or
// the backend refuses and names it. A new `to` command with a file is covered
// the day it registers; one that forgets stampSinkFile fails here, where a
// hand-kept list would not have heard of it. The lanes compare stdout, so
// nothing else in the suite would see a file go missing.
func TestEverySinkFileIsWrittenOrRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("generates a program per sink and backend")
	}
	bin := corpusBin(t)
	raw, err := exec.Command(bin, "-spec-json").Output()
	if err != nil {
		t.Fatalf("-spec-json: %v", err)
	}
	var spec struct {
		Subcommands map[string]struct {
			Subcommands map[string]struct {
				Flags []struct {
					Names    []string `json:"names"`
					Required bool     `json:"required"`
					Args     []struct {
						Completer string `json:"completer"`
					} `json:"args"`
				} `json:"flags"`
			} `json:"subcommands"`
		} `json:"subcommands"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	duckdb := duckdbBinary() != ""
	backends := []string{"sql", "sql -dialect postgres", "sql -dialect datafusion", "rust", "go"}
	sinks := map[string]bool{}
	wrote := 0
	for kind, cmd := range spec.Subcommands["to"].Subcommands {
		file := "sink_out_" + kind + ".out"
		var opts, fields []string
		positional, named := false, false
		for _, f := range cmd.Flags {
			name := f.Names[0]
			switch {
			case name == "FILE":
				positional = true
			case name == "-output":
				named = true
			case name == "-x" || name == "-y" || (f.Required && strings.HasPrefix(name, "-")):
				opts = append(opts, name, "k")
			case !strings.HasPrefix(name, "-") && len(f.Args) > 0 && f.Args[0].Completer == "fields":
				fields = []string{"s"} // a positional before FILE
			}
		}
		if !positional && !named {
			continue // this sink has no file to lose
		}
		sinks[kind] = true
		sink := "to " + kind + " " + strings.Join(opts, " ")
		if positional {
			sink += " " + strings.Join(fields, " ") + " " + file
		} else {
			sink += " -output " + file
		}
		for _, backend := range backends {
			t.Run(kind+"/"+strings.ReplaceAll(backend, " ", "_"), func(t *testing.T) {
				gen := "PATH=" + filepath.Dir(bin) + ":$PATH " + bin + " generate " + backend + " -pipeline 'ssql from csv t.csv | ssql " + sink + "'"
				out, err := sinkShell(t, dir, gen)
				if strings.Contains(out, "panic:") || strings.Contains(out, "goroutine ") {
					t.Fatalf("crashed:\n%s", out)
				}
				if !strings.Contains(sinkUncommented(out), file) {
					// A failure for any other reason (a flag this test
					// filled badly) names no file, so it cannot pass.
					t.Fatalf("err %v; the output neither writes %s nor refuses it:\n%s", err, file, out)
				}
				if err != nil || backend != "sql" || !duckdb {
					return
				}
				// The statement names the file: DuckDB must then write it.
				if out, err := sinkShell(t, dir, strings.Replace(gen, "generate sql", "generate sql -run", 1)); err != nil {
					t.Fatalf("-run: %v\n%s", err, out)
				}
				if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
					t.Fatalf("named in the SQL, not on disk: %v", err)
				}
				wrote++
			})
		}
	}
	// Guard the enumeration: an empty walk would pass everything above.
	for _, kind := range []string{"csv", "tsv", "json", "jsonl", "parquet", "xlsx", "arrow", "lines", "markdown", "chart", "animate", "explore", "wav"} {
		if !sinks[kind] {
			t.Errorf("the spec no longer shows a file on `to %s`", kind)
		}
	}
	if duckdb && wrote != 5 {
		t.Errorf("DuckDB wrote %d sinks' files, want 5 (csv, tsv, json, jsonl, parquet)", wrote)
	}
}
