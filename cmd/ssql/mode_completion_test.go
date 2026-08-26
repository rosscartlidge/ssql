package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestGenerateModeCompletion guards the `generate go -mode` value completer.
// It went unshipped originally (no Completer at all), so Tab offered nothing.
// This drives the real `-complete` protocol the shell uses.
func TestGenerateModeCompletion(t *testing.T) {
	bin := buildSSQLForTypedTest(t)

	// Empty word: offer the canonical modes.
	out, err := exec.Command(bin, "-complete", "4", "generate", "go", "-mode", "").CombinedOutput()
	if err != nil {
		t.Fatalf("-complete: %v\n%s", err, out)
	}
	got := strings.Fields(string(out))
	for _, want := range []string{"record", "typed"} {
		if !contains(got, want) {
			t.Errorf("-mode completion missing %q; got %v", want, got)
		}
	}
	// parallel is a deprecated alias — we deliberately do NOT advertise it.
	if contains(got, "parallel") {
		t.Errorf("-mode completion should not offer the deprecated alias 'parallel'; got %v", got)
	}

	// Prefix "t" narrows to typed.
	out, err = exec.Command(bin, "-complete", "4", "generate", "go", "-mode", "t").CombinedOutput()
	if err != nil {
		t.Fatalf("-complete prefix: %v\n%s", err, out)
	}
	if got := strings.Fields(string(out)); !contains(got, "typed") || contains(got, "record") {
		t.Errorf("-mode 't' completion = %v, want [typed]", got)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// TestFieldCompletionSources guards the autocli host hooks (autocli
// v4.14.x): Tab field names on `from parquet FILE -columns` answer
// from the footer via exec-self schema mode, value sampling answers
// from the column via ReadParquetColumns, and the plain same-command
// CSV path (catalog -if) keeps working — that one shipped silently
// broken (dangling-flag parse discarded the positional; autocli
// v4.14.1) and must never regress again.
func TestFieldCompletionSources(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()

	// Fixture: csv → parquet.
	csv := dir + "/d.csv"
	if err := os.WriteFile(csv, []byte("dept,salary\nsales,10\neng,20\nsales,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pq := dir + "/d.parquet"
	if out, err := exec.Command("bash", "-c",
		bin+" from csv "+csv+" | "+bin+" to parquet "+pq).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v\n%s", err, out)
	}

	complete := func(pos string, args ...string) []string {
		out, err := exec.Command(bin, append([]string{"-complete", pos}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("-complete %v: %v\n%s", args, err, out)
		}
		return strings.Fields(string(out))
	}

	// Parquet field names from the footer (was: Use-Ctrl-O hint).
	got := complete("5", "from", "parquet", pq, "-columns", "")
	for _, want := range []string{"dept", "salary"} {
		if !contains(got, want) {
			t.Errorf("parquet -columns completion missing %q; got %v", want, got)
		}
	}
	if contains(got, "Use-Ctrl-O") {
		t.Errorf("parquet -columns completion degraded to hint: %v", got)
	}

	// Same-command CSV field completion (the dangling-flag regression).
	got = complete("5", "from", "catalog", csv, "-if", "")
	if !contains(got, "dept") || !contains(got, "salary") {
		t.Errorf("catalog -if csv completion = %v, want dept+salary", got)
	}

	// Parquet VALUE sampling via the per-invocation cache-file param
	// (the Ctrl-O value phase's exact call shape).
	cmd := exec.Command(bin, "-complete", "5", "where", "-if", "dept", "eq", "")
	cmd.Env = append(os.Environ(), "AUTOCLI_CACHE_FILE="+pq)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("value complete: %v\n%s", err, out)
	}
	vals := strings.Fields(string(out))
	if !contains(vals, "sales") || !contains(vals, "eng") {
		t.Errorf("parquet value completion = %v, want sales+eng", vals)
	}
}
