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
	full := "export SSQLGO=typed && " + pipeline + " | " + bin + " generate go +O"
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
	return goRunGeneratedEnv(t, src, nil)
}

// goRunGeneratedEnv is goRunGenerated with extra environment for the run
// (the equivalence harness's TZ pin, DFC147).
func goRunGeneratedEnv(t *testing.T, src string, env []string) string {
	t.Helper()
	out, err := goRunGeneratedModuleEnv(t, "typedtest", map[string]string{"main.go": src}, env)
	if err != nil {
		t.Fatalf("run generated: %v\n%s", err, out)
	}
	return out
}

// goRunGeneratedResult builds the generated source (a build failure is
// fatal) and runs it, returning the combined output and the run error
// so a caller can assert that a program FAILS (DFC142 step 0: the
// record-mode to csv/tsv sinks exited 0 on an unwritable output).
func goRunGeneratedResult(t *testing.T, src string) (string, error) {
	t.Helper()
	return goRunGeneratedModule(t, "typedtest", map[string]string{"main.go": src})
}

// goRunGeneratedModule writes files (path → source, subdirectories
// allowed) into a temp module that replaces ssql with this checkout,
// builds the main package at its root and runs it, returning the
// combined output and the run error. A build failure is fatal and
// prints every file. Library mode (generate go -package) needs the
// multi-file form: the generated package plus a driver main.
func goRunGeneratedModule(t *testing.T, module string, files map[string]string) (string, error) {
	return goRunGeneratedModuleEnv(t, module, files, nil)
}

// goRunGeneratedModuleEnv is goRunGeneratedModule with extra environment
// for the program's run (not its build).
func goRunGeneratedModuleEnv(t *testing.T, module string, files map[string]string, env []string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module " + module + "\n\ngo 1.24\n\nrequire github.com/rosscartlidge/ssql/v4 v4.0.0\n\nreplace github.com/rosscartlidge/ssql/v4 => " + repo + "\n"
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
		var all strings.Builder
		for name, src := range files {
			fmt.Fprintf(&all, "--- %s:\n%s\n", name, src)
		}
		t.Fatalf("go build generated:\n%s\n%s", out, all.String())
	}
	run := exec.Command(filepath.Join(dir, "prog"))
	run.Dir = dir
	run.Env = append(os.Environ(), env...)
	out, err := run.CombinedOutput()
	return string(out), err
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
	// As of v4.40 SSQLGO=typed defaults to dual templates, so the source
	// can be either typed.ReadCSV (serial) or typed.ReadCSVParallel — and
	// the writer can be either typed.WriteCSVToWriter (serial) or the
	// per-shard buffer-dump form `records.WriteCSVToWriter`. Accept either.
	for _, want := range []string{
		"github.com/rosscartlidge/ssql/v4/typed",
		"PeopleRow struct",
		`Name`, `string`, `Age`, `int64`,
		"typed.ReadCSV", // matches both ReadCSV and ReadCSVParallel
		"WriteCSVToWriter",
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

	// SSQLGO=typed now defaults to dual templates — the planner
	// picks Stream.Where (parallel form) or typed.Where (serial)
	// per pipeline. Accept either form; the predicate body is
	// identical across modes.
	if !strings.Contains(src, ".Where(func(r PeopleRow) bool") {
		t.Errorf("generated source missing `.Where(func(r PeopleRow) bool`\nsource:\n%s", src)
	}
	if !strings.Contains(src, "r.Age >= 30") {
		t.Errorf("generated source missing %q\nsource:\n%s", "r.Age >= 30", src)
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

	// As of v4.40 SSQLGO=typed picks HashJoinParallel by default
	// when a Stream consumer is reachable; HashJoin (serial) is the
	// alternative the planner picks otherwise. Accept either; the
	// joined-fragment shape is the same.
	for _, want := range []string{
		"EmployeesRow",
		"DeptsRow",
		"EmployeesRow_DeptsRow",
		"typed.HashJoin", // matches both HashJoin and HashJoinParallel
		"rightSource1()",
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
	if err := os.WriteFile(emp, []byte("id,name,age\n1,Alice,30\n2,Bob,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	// 'pivot' under typed mode used to error; as of v4.40 (Phase B
	// mixed mode) the planner inserts a typed→Record adapter
	// upstream, so the pipeline now compiles and runs. Verify that.
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=typed && "+bin+" from "+emp+" | "+bin+" pivot -row name -col id -val age | "+bin+" to csv | "+bin+" generate go +O")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Phase B should let pivot work under typed mode, got error:\n%s\n--- output:\n%s", err, out)
	}
	src := string(out)
	// The toRecord boundary should be present.
	if !strings.Contains(src, "ssql.NewRecordFromSchema") {
		t.Errorf("expected typed→Record adapter (ssql.NewRecordFromSchema) in source\n%s", src)
	}
	if !strings.Contains(src, "ssql.Pivot") {
		t.Errorf("expected ssql.Pivot call in source\n%s", src)
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
	// Either typed.Select (serial) or typed.StreamSelect (parallel)
	// — the planner picks per pipeline as of v4.40.
	if !strings.Contains(src, "typed.Select(") && !strings.Contains(src, "typed.StreamSelect(") {
		t.Errorf("expected typed.Select or typed.StreamSelect call\n%s", src)
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

	// As of v4.40 SSQLGO=typed picks GroupByParallel by default
	// when the upstream is a Stream; the planner picks GroupBy
	// (serial) otherwise. Accept either.
	for _, want := range []string{
		"typed.GroupBy", // matches both GroupBy and GroupByParallel
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

func TestTypedSortDesc(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,salary\n1,Alice,95000\n2,Bob,65000\n3,Carol,120000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort salary -desc | "+bin+" limit 2 | "+bin+" to csv")

	if !strings.Contains(src, ", true)(") || !strings.Contains(src, "typed.SortParallelBy(") {
		t.Errorf("expected a descending typed.SortParallelBy call\n%s", src)
	}

	out := goRunGenerated(t, src)
	// Highest salaries: Carol (120k), Alice (95k). Bob (65k) is excluded by limit.
	if !strings.Contains(out, "Carol") || !strings.Contains(out, "Alice") {
		t.Errorf("expected Carol and Alice in output, got:\n%s", out)
	}
	if strings.Contains(out, "Bob") {
		t.Errorf("Bob should be excluded by limit 2 after sort, got:\n%s", out)
	}
	// First data line should be Carol (highest salary).
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "3,Carol") {
		t.Errorf("expected first row to be Carol (sorted desc), got:\n%s", out)
	}
}

func TestTypedSortAsc(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,age\nCarol,42\nAlice,30\nBob,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort age | "+bin+" to csv")
	if !strings.Contains(src, "typed.SortParallelBy(") || !strings.Contains(src, ", false)(") {
		t.Errorf("expected an ascending typed.SortParallelBy\n%s", src)
	}
	out := goRunGenerated(t, src)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected 4 lines (header + 3), got:\n%s", out)
	}
	if !strings.HasPrefix(lines[1], "Bob") || !strings.HasPrefix(lines[3], "Carol") {
		t.Errorf("expected Bob first, Carol last by age asc; got:\n%s", out)
	}
}

func TestTypedDistinct(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "dups.csv")
	if err := os.WriteFile(emp, []byte("name,age\nAlice,30\nBob,25\nAlice,30\nCarol,42\nBob,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" distinct | "+bin+" to csv")
	// Parallel plan emits DistinctParallel; a serial downgrade emits
	// Distinct — either is the typed dedupe.
	if !strings.Contains(src, "typed.DistinctParallel(") && !strings.Contains(src, "typed.Distinct(") {
		t.Errorf("expected a typed.Distinct/DistinctParallel call\n%s", src)
	}
	out := goRunGenerated(t, src)
	// Should be 3 unique rows + header.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 lines (header + 3 unique), got %d:\n%s", len(lines), out)
	}
	for _, want := range []string{"Alice", "Bob", "Carol"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in distinct output, got:\n%s", want, out)
		}
	}
}

func TestTypedUnionDedup(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.csv")
	b := filepath.Join(dir, "b.csv")
	if err := os.WriteFile(a, []byte("name,age\nAlice,30\nBob,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("name,age\nBob,25\nCarol,42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+a+" | "+bin+" union -file "+b+" | "+bin+" to csv")
	if !strings.Contains(src, "typed.Union(") {
		t.Errorf("expected typed.Union call (dedup)\n%s", src)
	}
	out := goRunGenerated(t, src)
	// 3 unique rows: Alice, Bob, Carol.
	bobCount := strings.Count(out, "Bob,25")
	if bobCount != 1 {
		t.Errorf("expected exactly one Bob row after union dedup, got %d:\n%s", bobCount, out)
	}
	for _, want := range []string{"Alice", "Bob", "Carol"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in union output:\n%s", want, out)
		}
	}
}

func TestTypedUnionAll(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.csv")
	b := filepath.Join(dir, "b.csv")
	if err := os.WriteFile(a, []byte("name,age\nAlice,30\nBob,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("name,age\nBob,25\nCarol,42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+a+" | "+bin+" union -file "+b+" -all | "+bin+" to csv")
	if !strings.Contains(src, "typed.Concat(") {
		t.Errorf("expected typed.Concat call (union -all)\n%s", src)
	}
	out := goRunGenerated(t, src)
	// 4 rows total: Alice, Bob, Bob, Carol.
	bobCount := strings.Count(out, "Bob,25")
	if bobCount != 2 {
		t.Errorf("expected two Bob rows after union -all, got %d:\n%s", bobCount, out)
	}
}

func TestTypedUnionSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.csv")
	b := filepath.Join(dir, "b.csv")
	if err := os.WriteFile(a, []byte("name,age\nAlice,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("name,score\nBob,99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=typed && "+bin+" from "+a+" | "+bin+" union -file "+b+" | "+bin+" to csv | "+bin+" generate go +O")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected union with mismatched schemas to fail, got:\n%s", out)
	}
	if !strings.Contains(string(out), "union") || !strings.Contains(string(out), "schema") && !strings.Contains(string(out), "field") {
		t.Errorf("error should explain the schema mismatch, got:\n%s", out)
	}
}

func TestTypedTopBy(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,salary\n1,Alice,95000\n2,Bob,65000\n3,Carol,120000\n4,Dave,55000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" top 2 -field salary | "+bin+" to csv")
	// `from | top` is parallel-reachable, so the planner keeps the
	// heap-based parallel form — NOT a full SortByDesc + Limit.
	if !strings.Contains(src, "typed.TopByParallel") {
		t.Errorf("expected typed.TopByParallel (bounded-heap top-k)\n%s", src)
	}
	if strings.Contains(src, "typed.SortByDesc") || strings.Contains(src, "typed.Limit") {
		t.Errorf("top must not desugar to SortByDesc + Limit anymore\n%s", src)
	}
	out := goRunGenerated(t, src)
	if !strings.Contains(out, "Carol") || !strings.Contains(out, "Alice") {
		t.Errorf("expected top-2 by salary (Carol, Alice), got:\n%s", out)
	}
	if strings.Contains(out, "Bob") || strings.Contains(out, "Dave") {
		t.Errorf("Bob and Dave should be excluded, got:\n%s", out)
	}
}

// TestTypedTopBySerialForm forces the serial iter.Seq form (a sort
// upstream downgrades the running shape) and checks the planner swaps to
// typed.TopBy rather than the parallel TopByParallel.
// TestTypedTopAfterSortStaysParallel: the sort produces an ordered
// Stream (2026-10-29), so a top after it keeps its parallel form; until
// then the sort was SerialOnly and this test pinned the serial TopBy
// after the downgrade.
func TestTypedTopAfterSortStaysParallel(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,salary\n1,Alice,95000\n2,Bob,65000\n3,Carol,120000\n4,Dave,55000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort name | "+bin+" top 2 -field salary | "+bin+" to csv")
	if !strings.Contains(src, "typed.TopByParallel") {
		t.Errorf("expected typed.TopByParallel after a parallel sort\n%s", src)
	}
	if strings.Contains(src, ".Serial()") {
		t.Errorf("no Serial() boundary belongs between a sort and a top\n%s", src)
	}
	out := goRunGenerated(t, src)
	if !strings.Contains(out, "Carol") || !strings.Contains(out, "Alice") {
		t.Errorf("expected top-2 by salary (Carol, Alice), got:\n%s", out)
	}
	if strings.Contains(out, "Bob") || strings.Contains(out, "Dave") {
		t.Errorf("Bob and Dave should be excluded, got:\n%s", out)
	}
}

func TestTypedSortMultiField(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "sales.csv")
	if err := os.WriteFile(emp, []byte("region,product,amount\nN,A,100\nN,B,200\nS,A,300\nN,C,150\nS,B,250\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort region product | "+bin+" to csv")
	if !strings.Contains(src, "typed.SortParallelFunc") {
		t.Errorf("expected typed.SortParallelFunc for multi-field sort\n%s", src)
	}
	out := goRunGenerated(t, src)
	// Lex-asc by region, then product: N/A, N/B, N/C, S/A, S/B
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 6 {
		t.Fatalf("expected 6 lines, got:\n%s", out)
	}
	want := []string{"N,A,100", "N,B,200", "N,C,150", "S,A,300", "S,B,250"}
	for i, w := range want {
		if !strings.Contains(lines[i+1], w) {
			t.Errorf("line %d: expected %q, got %q", i+1, w, lines[i+1])
		}
	}
}

func TestTypedCastFloat(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,years\nAlice,8\nBob,3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" cast -type years float | "+bin+" to csv")
	if !strings.Contains(src, "Years  float64") && !strings.Contains(src, "Years float64") {
		t.Errorf("expected Years to become float64 in cast struct\n%s", src)
	}
	if !strings.Contains(src, "float64(r.Years)") {
		t.Errorf("expected float64(r.Years) conversion\n%s", src)
	}
	out := goRunGenerated(t, src)
	for _, want := range []string{"Alice", "Bob"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in cast output\n%s", want, out)
		}
	}
}

// TestTypedCastKeepsPipelineParallel pins the planner's choice around a
// cast: a cast is a per-row map, so the stage speaks Stream[T] and the
// source read and the group-by after it keep their parallel forms. Until
// 2026-10-28 the cast template was SerialOnly and the planner downgraded
// BOTH neighbours: `from parquet | cast -type x decimal:2 | group-by` ran
// 1.29 s on 14.6 M rows where the parallel group-by alone ran 0.15 s
// (DFC158 §2.3). The serial alternative (typed.Select) must not appear.
func TestTypedCastKeepsPipelineParallel(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,years\nAlice,8\nBob,3\nAlice,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" cast -type years float | "+bin+" group-by name -sum years total | "+bin+" to csv")
	for _, want := range []string{"typed.ReadCSVParallel", "typed.StreamSelect(", "typed.GroupByParallel"} {
		if !strings.Contains(src, want) {
			t.Errorf("cast serialised the pipeline: generated source lacks %q\n%s", want, src)
		}
	}
	if strings.Contains(src, "typed.Select(") {
		t.Errorf("cast took its serial alternative\n%s", src)
	}
	out := goRunGenerated(t, src)
	for _, want := range []string{"Alice,10", "Bob,3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\n%s", want, out)
		}
	}
}

// TestTypedWindowKeepsSourceParallel: the window is a barrier whose
// output is an iter.Seq, but its INPUT is the source's Stream
// (typed.WindowParallel materialises the shards concurrently, in shard
// order); until 2026-10-28 the template was SerialOnly and the planner
// read the source serially — 2.4 s of the 14.6 M-row benchmark window
// (DFC158 §4.3). The serial typed.Window must not appear.
func TestTypedWindowKeepsSourceParallel(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,dept,years\nAlice,eng,8\nBob,ops,3\nCarol,eng,2\nDan,ops,5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" window -partition dept -order years -sum years total | "+bin+" to csv")
	for _, want := range []string{"typed.ReadCSVParallel", "typed.WindowParallel("} {
		if !strings.Contains(src, want) {
			t.Errorf("window serialised its source: generated source lacks %q\n%s", want, src)
		}
	}
	if strings.Contains(src, "typed.Window(") {
		t.Errorf("window took its serial alternative\n%s", src)
	}
	out := goRunGenerated(t, src)
	// Partitions in first-seen order (eng, then ops), each by years.
	for _, want := range []string{"name,dept,years,total\nCarol,eng,2,2\nAlice,eng,8,10\nBob,ops,3,3\nDan,ops,5,8\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\n%s", want, out)
		}
	}
}

// TestTypedWindowFeedsParallelGroupBy: the window's output is an ordered
// Stream whose shards are ranges of its output order, so the group-by
// after it runs its parallel form (and a serial consumer still sees the
// window's order through Serial()). Until 2026-10-29 the output was an
// iter.Seq and the group-by after the benchmark window ran serially —
// 1.6 s of the 5.0 (DFC158 §3.11).
func TestTypedWindowFeedsParallelGroupBy(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,dept,years\nAlice,eng,8\nBob,ops,3\nCarol,eng,2\nDan,ops,5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" window -partition dept -order years -sum years total | "+bin+" group-by dept -max total top | "+bin+" to csv")
	for _, want := range []string{"typed.ReadCSVParallel", "typed.WindowParallel(", "typed.GroupByParallel"} {
		if !strings.Contains(src, want) {
			t.Errorf("the window serialised its consumer: generated source lacks %q\n%s", want, src)
		}
	}
	if strings.Contains(src, "typed.GroupBy(") {
		t.Errorf("group-by took its serial alternative after the window\n%s", src)
	}
	out := goRunGenerated(t, src)
	if want := "dept,top\neng,10\nops,8\n"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n%s", want, out)
	}
	// A serial consumer after the window sees the window's order.
	src = runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" window -partition dept -order years -sum years total | "+bin+" limit 3 | "+bin+" to csv")
	out = goRunGenerated(t, src)
	if want := "name,dept,years,total\nCarol,eng,2,2\nAlice,eng,8,10\nBob,ops,3,3\n"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n%s", want, out)
	}
}

// TestTypedWindowFrameStatistics: median, percentile, stddev, variance
// and mode over a window frame have a typed form (typed.WAgg), so a
// pipeline like Ross's `window -presorted -preceding 5 -following 5
// -median n running` runs typed instead of falling to the record path —
// where `-presorted` streams and the streaming window refuses a frame
// with rows after the current one (2026-10-29). The typed window ignores
// -presorted and materialises either way.
func TestTypedWindowFrameStatistics(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from range 20 | "+bin+" window -presorted -preceding 2 -following 2 -median n med -stddev n sd -mode n mo -order n -count c | "+bin+" where -if c eq 5 | "+bin+" limit 2 | "+bin+" to csv")
	for _, want := range []string{"typed.MedianOf(", "typed.StdDevOf(", "typed.ModeOf("} {
		if !strings.Contains(src, want) {
			t.Errorf("the frame statistics took the record path: generated source lacks %q\n%s", want, src)
		}
	}
	if strings.Contains(src, "ssql.WAggregate(") {
		t.Errorf("window took the record path\n%s", src)
	}
	out := goRunGenerated(t, src)
	// Rows 3 and 4 are the first with a full five-row frame: the median is
	// the row, the sample stddev of five consecutive integers is sqrt(2.5),
	// the mode of five distinct values is the first seen (the lowest).
	if want := "n,c,sd,med,mo\n3,5,1.5811388300841898,3,1\n4,5,1.5811388300841898,4,2\n"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n%s", want, out)
	}
}

// TestTypedSortKeepsPipelineParallel: the sort takes the source's Stream
// and produces an ordered one (typed.SortParallelBy / SortParallelFunc),
// so the source is not fanned in through a channel before it and the
// consumer after it runs parallel; the serial typed.SortBy must not
// appear, nor a Serial() before the sort. Until 2026-10-29 the sort was
// SerialOnly — 3.5 s of a 5.5 s program on 10 M rows (DFC158 §3.13).
// The output order is the stable one (ties in input order), as exec's.
func TestTypedSortKeepsPipelineParallel(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,dept,years\nAlice,eng,8\nBob,ops,3\nCarol,eng,2\nDan,ops,3\nEve,eng,8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort years | "+bin+" group-by dept -first name f | "+bin+" to csv")
	for _, want := range []string{"typed.ReadCSVParallel", "typed.SortParallelBy(", "typed.GroupByParallel"} {
		if !strings.Contains(src, want) {
			t.Errorf("the sort serialised the pipeline: generated source lacks %q\n%s", want, src)
		}
	}
	for _, bad := range []string{"typed.SortBy(", ".Serial()"} {
		if strings.Contains(src, bad) {
			t.Errorf("the sort took the serial path (%q present)\n%s", bad, src)
		}
	}
	out := goRunGenerated(t, src)
	// By years ascending, ties in input order: Carol 2, Bob 3, Dan 3, Alice 8, Eve 8 → first per dept.
	if want := "dept,f\neng,Carol\nops,Bob\n"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n%s", want, out)
	}
	// A multi-field sort (the comparator form) and a serial consumer.
	src = runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort dept + years -desc | "+bin+" limit 3 | "+bin+" to csv")
	if !strings.Contains(src, "typed.SortParallelFunc(") {
		t.Errorf("multi-field sort lacks SortParallelFunc\n%s", src)
	}
	out = goRunGenerated(t, src)
	if want := "name,dept,years\nAlice,eng,8\nEve,eng,8\nCarol,eng,2\n"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n%s", want, out)
	}
}

// TestTypedWindowPresorted: `sort | window -presorted` takes
// typed.WindowPresorted (contiguous runs over the sort's rows in place,
// no sort, no gather — 2026-10-29, DFC158 §3.14), and clauses that do
// not share their partition and order fall back to the materialised
// typed.WindowParallel rather than trusting the flag for both.
func TestTypedWindowPresorted(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,dept,years\nAlice,eng,8\nBob,ops,3\nCarol,eng,2\nDan,ops,5\nEve,eng,8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort dept years | "+bin+" window -presorted -partition dept -order years -sum years total -preceding 1 -following 1 -avg years ma | "+bin+" to csv")
	if !strings.Contains(src, "typed.WindowPresorted(") || !strings.Contains(src, "typed.SortParallelFunc(") {
		t.Errorf("expected the presorted typed window after the parallel sort\n%s", src)
	}
	out := goRunGenerated(t, src)
	if want := "name,dept,years,total,ma\nCarol,eng,2,10,5\nAlice,eng,8,18,6\nEve,eng,8,16,8\nBob,ops,3,8,4\nDan,ops,5,8,4\n"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n%s", want, out)
	}
	src = runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" sort dept years | "+bin+" window -presorted -partition dept -order years -sum years total + -order name -row-number rn | "+bin+" to csv")
	if !strings.Contains(src, "typed.WindowParallel(") || strings.Contains(src, "typed.WindowPresorted(") {
		t.Errorf("clauses on different orders must take the materialised window\n%s", src)
	}
}

// TestTypedWindowProgramReadsPlainly: the generated window is one
// constructor per function and one key per order, the readers and the
// comparator built in the package (typed/window_spec.go); an update
// with one unconditional clause has no block around its assignments.
// Ross, 2026-10-29: the program must stay easy to read, with the
// complexity of optimisation in the packages.
func TestTypedWindowProgramReadsPlainly(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from range 100 | "+bin+" update -set-expr r 'hash(n)' | "+bin+" sort n | "+bin+" exclude r | "+bin+" window -presorted -preceding 5 -following 5 -avg n running -order n -count count | "+bin+" where -if count eq 11 | "+bin+" limit 3 | "+bin+" to csv")
	for _, want := range []string{
		"typed.CountRows[RangeRowUpdatedSubset]()",
		"typed.AvgOf(func(r RangeRowUpdatedSubset) int64 { return r.N })",
		"KeyInt: func(r RangeRowUpdatedSubset) int64 { return r.N },",
		"\t\tout.R = exprfn.Hash(r.N, 0)\n\t\treturn out",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source lacks %q\n%s", want, src)
		}
	}
	for _, bad := range []string{"Compare:", " Num:", "Kind: typed.", "{N: r.N}\n\t\t{\n"} {
		if strings.Contains(src, bad) {
			t.Errorf("generated source spells %q, which belongs in the package\n%s", bad, src)
		}
	}
	out := goRunGenerated(t, src)
	if want := "n,count,running\n6,11,6\n7,11,7\n8,11,8\n"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n%s", want, out)
	}
}

// TestGenerateRustBuildSurvivesOptimiser: `generate rust -build PATH` on
// a pipeline the optimiser rewrites (a pruned Parquet read) re-executes
// through bash, and the inner command must carry -build. Until 2026-10-28
// it did not: the program went to stdout and nothing was built, exit 0
// (scripts/bench-engines.sh found it). With cargo present the binary
// exists afterwards; without it the error names cargo. Either way the
// program must not appear on stdout.
func TestGenerateRustBuildSurvivesOptimiser(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	pq := codelabParquet(t)
	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command(bin, "generate", "rust", "-build", out, "-pipeline", bin+" from parquet "+pq+" | "+bin+" group-by dept -count n | "+bin+" to csv")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if strings.Contains(stdout.String(), "async fn pipeline") {
		t.Fatalf("-build was dropped by the optimiser re-execution: the program went to stdout\n%s", stdout.String()[:min(stdout.Len(), 600)])
	}
	if _, statErr := os.Stat(out); statErr != nil && !(err != nil && strings.Contains(stderr.String(), "cargo")) {
		t.Fatalf("no binary at %s and no cargo error: err=%v\nstderr: %s", out, err, stderr.String())
	}
}

func TestTypedUpdateSetLiteral(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name\n1,Alice\n2,Bob\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" update -set tier gold | "+bin+" to csv")
	if !strings.Contains(src, "PeopleRowUpdated") {
		t.Errorf("expected derived PeopleRowUpdated struct\n%s", src)
	}
	out := goRunGenerated(t, src)
	for _, want := range []string{"id,name,tier", "1,Alice,gold", "2,Bob,gold"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\n%s", want, out)
		}
	}
}

func TestTypedUpdateConditional(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,salary\n1,Alice,95000\n2,Bob,65000\n3,Carol,120000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	src := runTypedPipeline(t, bin,
		bin+" from "+emp+" | "+bin+" update -if salary gt 100000 -set tier premium + -if salary gt 70000 -set tier standard + -set tier basic | "+bin+" to csv")
	out := goRunGenerated(t, src)
	// Alice 95k -> standard, Bob 65k -> basic, Carol 120k -> premium
	for _, want := range []string{"Alice,95000,standard", "Bob,65000,basic", "Carol,120000,premium"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\n%s", want, out)
		}
	}
}

// TestTypedUpdateSetExprUnknownFieldLoud: transpilable -set-expr is native
// in typed mode since the expr transpiler (v4.57.0; success path covered by
// "update set-expr goes native typed setter" in generation_test.go), but an
// unknown field must remain a LOUD generation error naming the field and
// the schema — never silently empty output. (This test previously asserted
// -set-expr always failed in typed mode — a pre-transpiler expectation.)
func TestTypedUpdateSetExprUnknownFieldLoud(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,salary\n1,Alice,95000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=typed && "+bin+" from "+emp+" | "+bin+" update -set-expr bonus 'nosuchfield * 0.1' | "+bin+" to csv | "+bin+" generate go +O")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unknown-field -set-expr to fail generation, got:\n%s", out)
	}
	for _, want := range []string{"set-expr", `unknown field "nosuchfield"`, "schema has"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("error should contain %q, got:\n%s", want, out)
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
		"export SSQLGO=1 && "+bin+" from "+emp+" | "+bin+" where -if age gt 25 | "+bin+" to csv | "+bin+" generate go +O")
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

// TestTypedRollupIsNative pins the typed form of group-by -rollup/-cube
// (typed_rollup.go): the generated program must use the parallel detail
// group-by + typed.RollupEnrich, not the record ssql.Rollup fallback
// behind a typed→Record adapter (36 s vs 1.7 s on 14.6M rows), and its
// output must match exec exactly. -collect still ejects, loudly noted.
func TestTypedRollupIsNative(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "rc.csv")
	if err := os.WriteFile(csvPath, []byte("a,b,v\nx,p,1\nx,q,2\ny,p,3\nx,p,4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	pipe := bin + " from csv " + csvPath + " | " + bin + " group-by a b -count n -sum v tot -avg v mean -min v lo -max v hi -cube | " + bin + " sort a b | " + bin + " to csv"

	src := runTypedPipeline(t, bin, pipe)
	for _, want := range []string{"typed.GroupByParallel(", "typed.RollupEnrich(", "RollupAgg struct{ ", "typed.RollupSet[", "ABN", "AMean"} {
		if !strings.Contains(src, want) {
			t.Errorf("typed cube source lacks %q", want)
		}
	}
	for _, reject := range []string{"ssql.Rollup(", "typed→Record", "ToRecord"} {
		if strings.Contains(src, reject) {
			t.Errorf("typed cube source still uses the record fallback (%q)", reject)
		}
	}
	got := goRunGenerated(t, src)
	exec := equivShell(t, "exec", pipe)
	if got != exec {
		t.Errorf("typed cube output differs from exec:\n--- typed\n%s--- exec\n%s", got, exec)
	}

	// -collect has no mergeable state: the record fallback, noted.
	explain := equivShell(t, "explain", "export SSQL_MODE=typed && "+bin+" from csv "+csvPath+" | "+bin+" group-by a -collect v vs -count n -rollup | "+bin+" to csv | "+bin+" generate go +O -explain 2>&1 || true")
	if !strings.Contains(explain, "record fallback (-rollup/-cube with -collect") {
		t.Errorf("-collect rollup should fall back to record with a reason; explain:\n%s", explain)
	}
}

// TestTypedFromJSONL pins the typed form of `from jsonl` (roadmap §9):
// the generated program reads with typed.ReadJSONL into an inferred
// struct (not the record ReadJSONAuto fallback), for a plain file and
// for a tee'd file whose `_schema` header drives the struct; output
// matches exec. A JSON array file reads with typed.ReadJSON into a
// struct sampled from its elements (2026-10-09; it kept the record path
// with a reason before).
func TestTypedFromJSONL(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "e.jsonl")
	os.WriteFile(plain, []byte("{\"name\":\"Alice\",\"age\":35,\"salary\":95000.5}\n{\"name\":\"Bob\",\"age\":28,\"salary\":65000}\n{\"name\":\"Carol\",\"age\":42,\"salary\":105000}\n"), 0o644)
	hdr := filepath.Join(dir, "teed.jsonl")
	os.WriteFile(hdr, []byte("{\"_schema\":{\"fields\":[\"name\",\"age\"],\"types\":{\"name\":\"string\",\"age\":\"int\"}}}\n{\"name\":\"Alice\",\"age\":35}\n{\"name\":\"Bob\",\"age\":28}\n"), 0o644)
	arr := filepath.Join(dir, "arr.json")
	os.WriteFile(arr, []byte("[{\"name\":\"Alice\",\"age\":35}]"), 0o644)

	for _, c := range []struct{ file, want string }{
		{plain, "[ERow]("},
		{hdr, "[TeedRow]("},
	} {
		pipe := bin + " from jsonl " + c.file + " | " + bin + " where -if age gt 30 | " + bin + " include name age | " + bin + " to csv"
		src := runTypedPipeline(t, bin, pipe)
		// Either form: the planner picks ReadJSONLParallel when a
		// downstream accepts a Stream (where does), else ReadJSONL.
		if !strings.Contains(src, "typed.ReadJSONL") || !strings.Contains(src, c.want) || strings.Contains(src, "ReadJSONAuto") {
			t.Errorf("%s: typed source should read with typed.ReadJSONL(Parallel)%s, not the record reader:\n%s", c.file, c.want, src[:min(len(src), 1500)])
		}
		if got, exec := goRunGenerated(t, src), equivShell(t, "exec", pipe); got != exec {
			t.Errorf("%s: typed output differs from exec:\n--- typed\n%s--- exec\n%s", c.file, got, exec)
		}
	}

	arrPipe := bin + " from json " + arr + " | " + bin + " include name age | " + bin + " to csv"
	arrSrc := runTypedPipeline(t, bin, arrPipe)
	// Either form: ReadJSONParallel when a downstream accepts a Stream
	// (include does), else ReadJSON.
	if !strings.Contains(arrSrc, "typed.ReadJSON") || !strings.Contains(arrSrc, "[ArrRow](") || strings.Contains(arrSrc, "ReadJSONAuto") {
		t.Errorf("a JSON array file should read with typed.ReadJSON(Parallel)[ArrRow], not the record reader:\n%s", arrSrc[:min(len(arrSrc), 1500)])
	}
	if got, exec := goRunGenerated(t, arrSrc), equivShell(t, "exec", arrPipe); got != exec {
		t.Errorf("array file: typed output differs from exec:\n--- typed\n%s--- exec\n%s", got, exec)
	}
	explain := equivShell(t, "explain", "export SSQL_MODE=typed && "+bin+" from json "+arr+" | "+bin+" to csv | "+bin+" generate go +O -explain 2>&1 || true")
	if !strings.Contains(explain, "typed from json (array)") {
		t.Errorf("explain should note the array sampling; explain:\n%s", explain)
	}

	// A Stream-accepting downstream keeps the parallel reader (step 3).
	par := runTypedPipeline(t, bin, bin+" from jsonl "+plain+" | "+bin+" group-by name -count n -sum salary total | "+bin+" to csv")
	if !strings.Contains(par, "typed.ReadJSONLParallel[ERow]") || !strings.Contains(par, "typed.GroupByParallel(") {
		t.Errorf("group-by downstream should keep the parallel JSONL reader:\n%s", par[:min(len(par), 1500)])
	}
	got := goRunGenerated(t, par)
	for _, want := range []string{"Alice,1,95000.5", "Bob,1,65000", "Carol,1,105000"} {
		if !strings.Contains(got, want) {
			t.Errorf("parallel JSONL group-by output lacks %q:\n%s", want, got)
		}
	}
}

// A new field set only under a condition is absent on the other rows in
// exec (DFC124 §3); a typed struct cannot hold that, so typed falls back
// to record mode for the stage, with the reason. An unconditional final
// clause that sets it too keeps the stage typed.
func TestTypedUpdateConditionalNewFieldFallsBack(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "e.csv")
	if err := os.WriteFile(csvPath, []byte("name,dept\nBob,Sales\nAmy,Eng\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	explain := func(update string) string {
		return equivShell(t, "explain", "export SSQL_MODE=typed && "+bin+" from csv "+csvPath+" | "+bin+" update "+update+" | "+bin+" to csv | "+bin+" generate go +O -explain 2>&1 || true")
	}
	if got := explain("-if dept eq Sales -set-field who name"); !strings.Contains(got, `record fallback (new field "who" is set only under a condition`) {
		t.Errorf("conditional new field should fall back with a reason; explain:\n%s", got)
	}
	if got := explain("-if dept eq Sales -set dept x + -if dept eq Eng -set who x + -set who y"); !strings.Contains(got, `record fallback (new field "who"`) {
		t.Errorf("a gap before the else should fall back; explain:\n%s", got)
	}
	if got := explain("-if dept eq Sales -set-field who name + -set who none"); strings.Contains(got, "record fallback") {
		t.Errorf("an else that sets the field keeps the stage typed; explain:\n%s", got)
	}
}

// TestStageAfterSinkIsRefused: a sink (count, to csv, …) consumes the
// records, so a stage after it is a mistake; every generate format
// says so at generation time, naming the sink, instead of emitting code
// that does not compile (record and typed `generate go` both did, until
// 2026-10-29; found by a wrong pipeline in scripts/bench-engines.sh).
func TestStageAfterSinkIsRefused(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("name,years\nAlice,8\nBob,3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	cases := []struct{ mode, pipeline, want string }{
		{"typed", bin + " from " + emp + " | " + bin + " count | " + bin + " to csv | " + bin + " generate go", "ssql count is a sink"},
		{"record", bin + " from " + emp + " | " + bin + " count | " + bin + " to csv | " + bin + " generate go", "ssql count is a sink"},
		{"typed", bin + " from " + emp + " | " + bin + " to csv | " + bin + " where -if years gt 5 | " + bin + " generate go", "ssql to csv is a sink"},
		{"record", bin + " from " + emp + " | " + bin + " count | " + bin + " to csv | " + bin + " generate sql", "ssql count is a sink"},
		{"record", bin + " from " + emp + " | " + bin + " count | " + bin + " to csv | " + bin + " generate ssql", "ssql count is a sink"},
	}
	for _, c := range cases {
		out, err := exec.Command("bash", "-c", "export SSQL_MODE="+c.mode+" && "+c.pipeline).CombinedOutput()
		if err == nil {
			t.Errorf("%s: %s\nexpected a refusal, got exit 0:\n%s", c.mode, c.pipeline, out)
			continue
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("%s: %s\nwant %q in:\n%s", c.mode, c.pipeline, c.want, out)
		}
	}
	// The sink last is the normal pipeline: still generates and runs.
	src := runTypedPipeline(t, bin, bin+" from "+emp+" | "+bin+" count")
	if out := goRunGenerated(t, src); strings.TrimSpace(out) != "2" {
		t.Errorf("count alone: got %q, want 2", out)
	}
}
