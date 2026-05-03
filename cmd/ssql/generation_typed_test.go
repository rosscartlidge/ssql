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
	// 'pivot' is still Tier 3 (deferred indefinitely per the
	// roadmap), so a typed pipeline that includes it should error.
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=typed && "+bin+" from "+emp+" | "+bin+" pivot name age | "+bin+" to csv | "+bin+" generate go")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected typed pipeline to fail, got output:\n%s", out)
	}
	if !strings.Contains(string(out), "does not yet support typed mode") {
		t.Errorf("error message should mention typed-mode unsupported, got:\n%s", out)
	}
	if !strings.Contains(string(out), "pivot") {
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

	if !strings.Contains(src, "typed.SortByDesc(") {
		t.Errorf("expected typed.SortByDesc call\n%s", src)
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
	if !strings.Contains(src, "typed.SortBy(") || strings.Contains(src, "typed.SortByDesc") {
		t.Errorf("expected typed.SortBy ascending\n%s", src)
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
	if !strings.Contains(src, "typed.Distinct(") {
		t.Errorf("expected typed.Distinct call\n%s", src)
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
		"export SSQLGO=typed && "+bin+" from "+a+" | "+bin+" union -file "+b+" | "+bin+" to csv | "+bin+" generate go")
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
	if !strings.Contains(src, "typed.SortByDesc") || !strings.Contains(src, "typed.Limit") {
		t.Errorf("expected SortByDesc + Limit composition\n%s", src)
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
	if !strings.Contains(src, "typed.SortByFunc") {
		t.Errorf("expected typed.SortByFunc for multi-field sort\n%s", src)
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

func TestTypedUpdateSetExprErrors(t *testing.T) {
	dir := t.TempDir()
	emp := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(emp, []byte("id,name,salary\n1,Alice,95000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	cmd := exec.Command("bash", "-c",
		"export SSQLGO=typed && "+bin+" from "+emp+" | "+bin+" update -set-expr bonus 'salary * 0.1' | "+bin+" to csv | "+bin+" generate go")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected -set-expr to fail in typed mode, got:\n%s", out)
	}
	if !strings.Contains(string(out), "set-expr") || !strings.Contains(string(out), "Tier 3") {
		t.Errorf("error should name -set-expr and Tier 3, got:\n%s", out)
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
