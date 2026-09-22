package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `ssql run` executes a pipeline document with no shell (DFC134 §5.1).
// The oracle is the shell form of the same document (`run -print`, run by
// bash): byte-identical output, and the same behaviour on failure, early
// exit, and nested pipelines.
func TestRunDocument(t *testing.T) {
	bin := corpusBin(t)
	data := corpusData(t)
	dir := t.TempDir()

	write := func(name, doc string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	run := func(t *testing.T, stdin string, args ...string) (string, string, error) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = data
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		return out.String(), errb.String(), err
	}
	// The oracle: the document's shell rendering, run by bash with the
	// corpus binary substituted for "ssql".
	viaShell := func(t *testing.T, doc string, stdin string) string {
		t.Helper()
		text, _, err := run(t, "", "run", "-print", doc)
		if err != nil {
			t.Fatalf("run -print: %v", err)
		}
		script := strings.ReplaceAll(strings.TrimSpace(text), "ssql ", bin+" ")
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
		cmd.Dir = data
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("shell form failed: %v\n%s", err, script)
		}
		return string(out)
	}

	t.Run("hostile values and a nested join agree with the shell form", func(t *testing.T) {
		doc := write("join.json", `[
			["from", "csv", "-arg", "hostile.csv"],
			["join", ["from", "csv", "-arg", "shuffled.csv"], "-on", "name", "city"],
			["where", "-if", "name", "ne", "-generate"],
			["where", "-if-expr", "name != who", "-param", "who", "string", "x\" || true || \""],
			["include", "-arg", "name", "-arg", "-desc", "-arg", "pop"],
			["sort", "-arg", "-desc", "-desc"],
			["to", "csv"]
		]`)
		got, stderr, err := run(t, "", "run", doc)
		if err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		if want := viaShell(t, doc, ""); got != want {
			t.Errorf("document:\n%s\nshell form:\n%s", got, want)
		}
		if !strings.HasPrefix(got, "name,-desc,pop\n") {
			t.Errorf("unexpected output:\n%s", got)
		}
	})

	t.Run("the first stage reads the runner's stdin", func(t *testing.T) {
		doc := write("stdin.json", `[["from", "csv"], ["limit", "-arg", "2"], ["to", "csv"]]`)
		got, _, err := run(t, "a,b\n1,2\n3,4\n5,6\n", "run", doc)
		if err != nil || got != "a,b\n1,2\n3,4\n" {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("-stdin reads the document and the first stage gets no stdin", func(t *testing.T) {
		got, _, err := run(t, `[["from","csv","-arg","shuffled.csv"],["limit","-arg","1"],["include","-arg","city"],["to","csv"]]`, "run", "-stdin")
		if err != nil || got != "city\nMumbai\n" {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("-check refuses before anything runs", func(t *testing.T) {
		out := filepath.Join(dir, "never.csv")
		doc := write("bad.json", `[["from","csv","-arg","shuffled.csv"],["where","-bogus","1"],["to","csv","`+out+`"]]`)
		_, stderr, err := run(t, "", "run", doc)
		if err == nil || !strings.Contains(stderr, "stage 2 (where)") || !strings.Contains(stderr, "-bogus") {
			t.Errorf("want the failing stage named: %v\n%s", err, stderr)
		}
		if _, statErr := os.Stat(out); statErr == nil {
			t.Error("the sink ran although an earlier stage was invalid")
		}
		if _, _, err := run(t, "", "run", "-check", doc); err == nil {
			t.Error("-check accepted an invalid document")
		}
		good := write("good.json", `[["from","csv","-arg","shuffled.csv"],["to","csv","`+out+`"]]`)
		if _, _, err := run(t, "", "run", "-check", good); err != nil {
			t.Errorf("-check: %v", err)
		}
		if _, statErr := os.Stat(out); statErr == nil {
			t.Error("-check ran the pipeline")
		}
	})

	t.Run("a failing stage is named, exit 1", func(t *testing.T) {
		doc := write("fail.json", `[["from","csv","-arg","nope.csv"],["to","csv"]]`)
		_, stderr, err := run(t, "", "run", doc)
		if err == nil || !strings.Contains(stderr, "stage 1 (from csv)") || !strings.Contains(stderr, "nope.csv") {
			t.Errorf("got %v\n%s", err, stderr)
		}
	})

	t.Run("early exit downstream is not a failure", func(t *testing.T) {
		var big strings.Builder
		big.WriteString("n\n")
		for i := 0; i < 300000; i++ {
			big.WriteString("1234567890\n")
		}
		doc := write("early.json", `[["from","csv"],["limit","-arg","1"],["to","csv"]]`)
		got, stderr, err := run(t, big.String(), "run", doc)
		if err != nil || got != "n\n1234567890\n" {
			t.Errorf("got %q, %v\n%s", got, err, stderr)
		}
	})

	t.Run("generation mode passes through to the stages", func(t *testing.T) {
		doc := write("gen.json", `[["from","csv","-arg","shuffled.csv"],["where","-if","pop","gt","10"],["to","csv"]]`)
		cmd := exec.Command(bin, "run", doc)
		cmd.Dir = data
		cmd.Env = append(os.Environ(), "SSQL_MODE=record")
		out, err := cmd.Output()
		if err != nil || !strings.Contains(string(out), `"type":"init"`) || !strings.Contains(string(out), `"kind":"where"`) {
			t.Errorf("fragments expected: %v\n%.300s", err, out)
		}
	})
}
