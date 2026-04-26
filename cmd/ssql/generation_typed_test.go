package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildSSQLForTypedTest compiles a fresh ssql binary into the test
// temp dir and returns its path. Each typed-mode test rebuilds because
// they exercise the full SSQLGO=typed -> generate go -> go build -> run
// loop, and stale binaries make failures hard to diagnose.
func buildSSQLForTypedTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ssql_typed_test")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ssql: %v\n%s", err, out)
	}
	return bin
}

// runTypedPipeline runs an SSQLGO=typed pipeline ending in `generate go`
// and returns the produced Go source.
func runTypedPipeline(t *testing.T, bin, pipeline string) string {
	t.Helper()
	full := "export SSQLGO=typed && " + pipeline + " | " + bin + " generate go"
	cmd := exec.Command("bash", "-c", full)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("typed pipeline failed: %v\n%s", err, out)
	}
	return string(out)
}

// goRunGenerated writes src to a temp module, builds it, and returns
// the program's stdout.
func goRunGenerated(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainGo, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module typedtest\n\ngo 1.24\n\nrequire github.com/rosscartlidge/ssql/v4 v4.0.0\n\nreplace github.com/rosscartlidge/ssql/v4 => " + repo + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	build := exec.Command("go", "build", "-o", "prog", ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build generated:\n%s\n--- source:\n%s", out, src)
	}
	run := exec.Command(filepath.Join(dir, "prog"))
	run.Dir = dir
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated: %v\n%s", err, out)
	}
	return string(out)
}

func TestTypedFromToCSV(t *testing.T) {
	csvData := "id,name,age\n1,Alice,30\n2,Bob,25\n"
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte(csvData), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin, bin+" from "+emp+" | "+bin+" to csv")

	// Generated source must reference the typed package and an inferred type.
	for _, want := range []string{
		"github.com/rosscartlidge/ssql/v4/typed",
		"PeopleRow struct",
		`Name`, `string`, `Age`, `int64`,
		"typed.ReadCSV[PeopleRow]",
		"typed.WriteCSVToWriter",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source missing %q\nsource:\n%s", want, src)
		}
	}

	out := goRunGenerated(t, src)
	for _, want := range []string{"Alice", "30", "Bob", "25"} {
		if !strings.Contains(out, want) {
			t.Errorf("program output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestTypedFromWhereToCSV(t *testing.T) {
	csvData := "id,name,age\n1,Alice,30\n2,Bob,25\n3,Carol,42\n"
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte(csvData), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" where -if age ge 30 | "+bin+" to csv")

	for _, want := range []string{
		"typed.Where(func(r PeopleRow) bool",
		"r.Age >= 30",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source missing %q\nsource:\n%s", want, src)
		}
	}

	out := goRunGenerated(t, src)
	if !strings.Contains(out, "Alice") || !strings.Contains(out, "Carol") {
		t.Errorf("expected Alice and Carol in output, got:\n%s", out)
	}
	if strings.Contains(out, "Bob") {
		t.Errorf("Bob (age 25) should have been filtered out, got:\n%s", out)
	}
}

func TestTypedFromWhereJoinToCSV(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "employees.csv")
	dept := filepath.Join(dir, "depts.csv")
	if err := os.WriteFile(emp, []byte("id,name,dept_id,years\n1,Alice,D01,8\n2,Bob,D02,3\n3,Carol,D01,12\n4,Dave,DZZ,5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dept, []byte("dept_id,dept_name,location\nD01,Engineering,SF\nD02,Sales,NYC\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" where -if years ge 5 | "+bin+" join "+dept+" -using dept_id | "+bin+" to csv")

	for _, want := range []string{
		"EmployeesRow",
		"DeptsRow",
		"EmployeesRow_DeptsRow",
		"typed.HashJoin(filtered, rightSource1()",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source missing %q\nsource:\n%s", want, src)
		}
	}

	out := goRunGenerated(t, src)
	// Alice and Carol should be present (years >= 5, dept matches).
	for _, want := range []string{"Alice", "Engineering", "Carol"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	// Bob is filtered (years < 5); Dave's dept doesn't exist (no inner-join match).
	for _, dontWant := range []string{"Bob", "Dave"} {
		if strings.Contains(out, dontWant) {
			t.Errorf("did not expect %q in output, got:\n%s", dontWant, out)
		}
	}
}

func TestTypedUnsupportedCommandErrors(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,age\n1,Alice,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=typed && "+bin+" from "+emp+" | "+bin+" group-by name -count n | "+bin+" to csv | "+bin+" generate go")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected typed pipeline to fail, got output:\n%s", out)
	}
	if !strings.Contains(string(out), "does not yet support typed mode") {
		t.Errorf("error message should mention typed-mode unsupported, got:\n%s", out)
	}
	if !strings.Contains(string(out), "group-by") {
		t.Errorf("error should name the offending command, got:\n%s", out)
	}
}

func TestTypedRecordModeUnaffected(t *testing.T) {
	// Regression: SSQLGO=1 (Record mode) must still produce the same
	// shape of output it did before Phase 2.
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,age\n1,Alice,30\n2,Bob,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=1 && "+bin+" from "+emp+" | "+bin+" where -if age gt 25 | "+bin+" to csv | "+bin+" generate go")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("record-mode pipeline failed: %v\n%s", err, out)
	}
	src := string(out)
	for _, want := range []string{
		"github.com/rosscartlidge/ssql/v4",
		"ssql.ReadCSV(",
		"ssql.Where(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("record-mode regression: missing %q\nsource:\n%s", want, src)
		}
	}
	// Must NOT mention typed package.
	if strings.Contains(src, "ssql/v4/typed") {
		t.Errorf("record-mode regression: typed package leaked into output\n%s", src)
	}
}
