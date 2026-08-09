package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestExprGoDifferential is the transpiler's semantic gate: for every corpus
// expression it runs the TRANSPILED Go closure and the expr-lang VM
// (runtime.CompileExpr, exactly what exec and Tier-V use) over the same rows
// and asserts identical results — type and value, after normalising Go's
// int to int64. This is what catches coercion drift; the unit test pins
// source text, this pins meaning.
//
// The corpus deliberately EXCLUDES documented divergences (see the plan's
// §6): mixed-numeric ternary branches (the VM keeps each branch's own type;
// the transpiler unifies to float64) and constructs that refuse to
// transpile (which are fallback decisions, not divergences).
//
// Build pattern follows goRunGenerated: one temp module, ONE go build for
// the whole corpus.
func TestExprGoDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a generated program")
	}

	schema := exprGoTestSchema() // pop/qty int64, price float64, city string, active bool (+ time field, unused)

	corpus := []string{
		// §2a arithmetic and promotion — division is the headline
		"pop / qty", // includes a divide-by-zero row: both paths must give +Inf
		"7 / 2",
		"pop % 3",
		"pop + 1",
		"pop - qty",
		"pop * qty",
		"pop + price",
		"price * 2",
		"pop ** 2",
		"price ^ 2",
		"-pop",
		`city + "!"`,
		// §2b comparisons
		"pop > 15",
		"price > 15",
		"pop > price",
		"pop >= qty",
		"pop == 7.0",
		"pop != qty",
		`city >= "M"`,
		`city == "Oslo"`,
		"1 < pop < 3",
		// §2c helpers
		`has("pop")`,
		`has("nope")`,
		`getOr("pop", 0)`,
		`getOr("nope", 5)`,
		"pop ?? 0",
		"nope ?? 5",
		// §2d built-ins
		"len(city)", // rune count — corpus includes a multi-byte city
		"abs(pop)",
		"abs(price)",
		"round(price)", // corpus includes ±2.5 — half away from zero
		"round(pop)",
		"floor(price)",
		"ceil(price)",
		"min(pop, qty)",
		"max(pop, qty)",
		// NB min(pop, price) — mixed int/float — is deliberately absent: the
		// VM returns the winner with its own type, so it refuses to transpile.
		"int(price)", // truncates toward zero — corpus includes negatives
		"float(pop)",
		"string(pop)",
		"string(price)",
		"upper(city)",
		"lower(city)",
		"trim(city)",
		`city contains "o"`,
		`city startsWith "O"`,
		`city endsWith "o"`,
		`hasPrefix(city, "A")`,
		`hasSuffix(city, "o")`,
		`city matches "^[A-Z]"`,
		`city in ["Oslo", "cairo"]`,
		"pop in [7, 20]",
		"city | upper()",
		// ternary (same-type branches only — mixed numeric is a documented
		// divergence: VM keeps branch types, transpiler unifies to float64)
		`active ? "y" : "n"`,
		"active ? 1 : 2",
		"active ? 1.5 : 2.5",
		// logic
		`pop > 5 && city != "Oslo"`,
		`pop > 5 || active`,
		`not active`,
		`pop > 5 and not (city == "cairo")`,
	}

	var body strings.Builder
	imports := map[string]bool{
		"fmt": true, "os": true, "reflect": true,
		"github.com/rosscartlidge/ssql/v4":                      true,
		"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime": true,
	}
	hoisted := map[string]bool{}
	var hoistedOrder []string

	for i, expr := range corpus {
		res, err := exprToGo(expr, schema, "r")
		if err != nil {
			t.Fatalf("corpus expression %q does not transpile: %v", expr, err)
		}
		for _, imp := range res.Imports {
			imports[imp] = true
		}
		for _, h := range res.Hoisted {
			if !hoisted[h] {
				hoisted[h] = true
				hoistedOrder = append(hoistedOrder, h)
			}
		}
		fmt.Fprintf(&body, `	{
		native := func(r Row) %s { return %s }
		eval := runtime.MustCompileExpr(%s)
		for i, r := range rows {
			vm, err := eval(records[i])
			if err != nil {
				fmt.Printf("VMERR expr=%%q row=%%d err=%%v\n", %s, i, err)
				fail++
				continue
			}
			if !same(any(native(r)), vm) {
				fmt.Printf("MISMATCH expr=%%q row=%%d native=%%#v (%%T) vm=%%#v (%%T)\n", %s, i, native(r), native(r), vm, vm)
				fail++
			}
		}
	}
`, res.Type, res.Src, strconv.Quote(expr), strconv.Quote(expr), strconv.Quote(expr))
		_ = i
	}

	var importList []string
	for imp := range imports {
		importList = append(importList, strconv.Quote(imp))
	}
	sort.Strings(importList)

	src := fmt.Sprintf(`package main

import (
	%s
)

type Row struct {
	Pop    int64
	Price  float64
	Qty    int64
	City   string
	Active bool
}

// Rows are chosen to make wrong emissions diverge: negative values (int()
// truncation, abs), ±2.5 (round half away from zero), a zero divisor (+Inf
// parity), multi-byte runes (len), mixed case and affixes (string ops).
var rows = []Row{
	{Pop: 7, Price: 2.5, Qty: 2, City: "Oslo", Active: true},
	{Pop: -3, Price: -2.5, Qty: 3, City: "cairo", Active: false},
	{Pop: 20, Price: 15.5, Qty: 0, City: " héllo ", Active: true},
	{Pop: 0, Price: 0, Qty: 1, City: "Ao", Active: false},
}

%s

// same compares a transpiled result with a VM result, normalising machine
// ints to int64. Type differences (int64 vs float64) are REAL mismatches —
// coercion drift is exactly what this harness exists to catch.
func same(a, b any) bool {
	return reflect.DeepEqual(norm(a), norm(b))
}

func norm(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	}
	return v
}

func main() {
	records := make([]ssql.Record, len(rows))
	for i, r := range rows {
		records[i] = ssql.MakeMutableRecord().
			Int("pop", r.Pop).
			Float("price", r.Price).
			Int("qty", r.Qty).
			String("city", r.City).
			Bool("active", r.Active).
			Freeze()
	}
	fail := 0
%s
	if fail > 0 {
		fmt.Printf("%%d mismatches\n", fail)
		os.Exit(1)
	}
	fmt.Println("OK")
}
`, strings.Join(importList, "\n\t"), strings.Join(hoistedOrder, "\n"), body.String())

	out := buildAndRunExprProgram(t, src)
	if !strings.Contains(out, "OK") {
		t.Fatalf("differential harness reported mismatches:\n%s", out)
	}
}

// buildAndRunExprProgram writes src to a temp module (replace-directive to
// this repo), builds it once, runs it, and returns combined output.
func buildAndRunExprProgram(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module exprdiff\n\ngo 1.24\n\nrequire github.com/rosscartlidge/ssql/v4 v4.0.0\n\nreplace github.com/rosscartlidge/ssql/v4 => " + repo + "\n"
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
	out, _ := run.CombinedOutput()
	return string(out)
}
