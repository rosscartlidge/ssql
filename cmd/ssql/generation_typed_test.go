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
	// 'update' is still Tier 2 / Tier 3 in typed-mode, so this
	// pipeline should error out.
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=typed && "+bin+" from "+emp+" | "+bin+" update -set tier gold | "+bin+" to csv | "+bin+" generate go")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected typed pipeline to fail, got output:\n%s", out)
	}
	if !strings.Contains(string(out), "does not yet support typed mode") {
		t.Errorf("error message should mention typed-mode unsupported, got:\n%s", out)
	}
	if !strings.Contains(string(out), "update") {
		t.Errorf("error should name the offending command, got:\n%s", out)
	}
}

func TestTypedLimitSkip(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,age\n1,Alice,30\n2,Bob,25\n3,Carol,42\n4,Dave,28\n5,Eve,35\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" offset 1 | "+bin+" limit 2 | "+bin+" to csv")

	for _, want := range []string{
		"typed.Skip[PeopleRow](*flagOffset)",
		"typed.Limit[PeopleRow](*flagLimit)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q\nsource:\n%s", want, src)
		}
	}

	out := goRunGenerated(t, src)
	// Bob and Carol (offset 1, limit 2) should appear.
	for _, want := range []string{"Bob", "Carol"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	for _, dontWant := range []string{"Alice", "Dave", "Eve"} {
		if strings.Contains(out, dontWant) {
			t.Errorf("did not expect %q in output, got:\n%s", dontWant, out)
		}
	}
}

func TestTypedIncludeProjection(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,dept_id,age\n1,Alice,D01,30\n2,Bob,D02,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" include name age | "+bin+" to csv")

	if !strings.Contains(src, "PeopleRowSubset") {
		t.Errorf("expected projected struct PeopleRowSubset in source\n%s", src)
	}
	if !strings.Contains(src, "typed.Select(") {
		t.Errorf("expected typed.Select call\n%s", src)
	}

	out := goRunGenerated(t, src)
	// First line is the header; ssql tags use the original CSV column
	// names (lowercase), so we expect "name,age".
	firstLine := strings.SplitN(out, "\n", 2)[0]
	if firstLine != "name,age" {
		t.Errorf("expected header 'name,age', got %q", firstLine)
	}
	// Body should not mention dept_id values.
	if strings.Contains(out, "D01") || strings.Contains(out, "D02") {
		t.Errorf("excluded fields leaked into output:\n%s", out)
	}
}

func TestTypedExcludeProjection(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,dept_id,age\n1,Alice,D01,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" exclude id dept_id | "+bin+" to csv")
	out := goRunGenerated(t, src)
	if !strings.Contains(out, "name,age") {
		t.Errorf("expected header 'name,age' after exclude, got:\n%s", out)
	}
	if strings.Contains(out, "D01") {
		t.Errorf("excluded dept_id leaked:\n%s", out)
	}
}

func TestTypedRenameProjection(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,age\n1,Alice,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" rename -as name full_name -as age years | "+bin+" to csv")
	out := goRunGenerated(t, src)
	if !strings.Contains(out, "id,full_name,years") {
		t.Errorf("expected renamed header 'id,full_name,years', got:\n%s", out)
	}
	if !strings.Contains(out, "Alice") {
		t.Errorf("data should still flow through rename, got:\n%s", out)
	}
}

func TestTypedGroupByCountAvg(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "employees.csv")
	if err := os.WriteFile(emp, []byte("id,name,dept_id,salary\n1,Alice,D01,100000\n2,Bob,D02,80000\n3,Carol,D01,120000\n4,Dave,D02,60000\n5,Eve,D01,80000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" group-by dept_id -count headcount -avg salary avg_salary | "+bin+" to csv")

	for _, want := range []string{
		"typed.GroupBy(",
		"EmployeesRowAggregator",
		"EmployeesRowAggregatorResult",
		"EmployeesRowGroup",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q\n%s", want, src)
		}
	}

	out := goRunGenerated(t, src)
	// D01: 3 employees at 100/120/80 = avg 100000
	// D02: 2 employees at 80/60 = avg 70000
	// Output is unordered (map iteration), so check substrings.
	if !strings.Contains(out, "D01,3,100000") {
		t.Errorf("expected D01,3,100000 row\n%s", out)
	}
	if !strings.Contains(out, "D02,2,70000") {
		t.Errorf("expected D02,2,70000 row\n%s", out)
	}
}

func TestTypedGroupByMultipleKeys(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "sales.csv")
	if err := os.WriteFile(emp, []byte("region,product,amount\nN,A,100\nN,B,200\nN,A,150\nS,A,300\nS,B,400\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" group-by region product -sum amount total | "+bin+" to csv")

	if !strings.Contains(src, "SalesRowGroupKey") {
		t.Errorf("expected composite key struct SalesRowGroupKey\n%s", src)
	}

	out := goRunGenerated(t, src)
	for _, want := range []string{"N,A,250", "N,B,200", "S,A,300", "S,B,400"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
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
