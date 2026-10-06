package main

// Library mode (DFC142 §5a shape 2): `generate go -package P -func F`
// emits an importable function. These tests generate the library for a
// corpus pipeline, compile it beside a driver main through
// goRunGeneratedModule, and compare the driver's rows with the exec
// lane's through the equivalence canonicaliser — the same oracle the
// go-lib lane applies to every TestPipelineEquivalence case.

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// libraryGenerate runs pipeline (with {{.bin}}/{{.data}} substituted)
// in mode and assembles it as package pkg, function fn.
func libraryGenerate(t *testing.T, bin, data, mode, pkg, fn, pipeline string) (string, error) {
	t.Helper()
	cmdline := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(pipeline)
	full := "export SSQLGO=" + mode + " && " + cmdline + " | " + bin + " generate go -package " + pkg + " -func " + fn
	out, err := exec.Command("bash", "-c", full).CombinedOutput()
	return string(out), err
}

// libraryDriver is a main that reads fixture through FN's reader form,
// writes the rows as JSONL with the writer `to jsonl` uses for that row
// shape (typed.WriteJSONLToWriter for a struct, the schema-ordered
// record writer for ssql.Record) and reports a terminal error on stderr
// with exit 1.
func libraryDriver(pkg, fn, reader, fixture string) string {
	return fmt.Sprintf(`package main

import (
	"fmt"
	"iter"
	"os"
	"slices"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/typed"
	"libtest/%[1]s"
)

func main() {
	f, err := os.Open(%[4]q)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := dump(%[1]s.%[3]s(f, %[1]s.%[2]sDefaults())); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func dump[T any](seq iter.Seq2[T, error]) error {
	var rows []T
	for v, err := range seq {
		if err != nil {
			return err
		}
		rows = append(rows, v)
	}
	if recs, ok := any(rows).([]ssql.Record); ok {
		return ssql.WriteJSONLWithInferredSchemaToWriter(slices.Values(recs), os.Stdout)
	}
	return typed.WriteJSONLToWriter(slices.Values(rows), os.Stdout)
}
`, pkg, fn, reader, fixture)
}

func TestLibraryMode(t *testing.T) {
	bin := corpusBin(t)
	data := corpusData(t)
	orders := data + "/orders.csv"

	// One pipeline, both modes, compared with exec.
	pipeline := `{{.bin}} from csv {{.data}}/orders.csv | {{.bin}} where -param min float 100 -if-expr 'amount > min' | {{.bin}} group-by product -count n -sum amount total | {{.bin}} to csv`
	for _, mode := range []string{"typed", "1"} {
		t.Run("matches_exec_"+mode, func(t *testing.T) {
			src, err := libraryGenerate(t, bin, data, mode, "reports", "Totals", pipeline)
			if err != nil {
				t.Fatalf("generate: %v\n%s", err, src)
			}
			wants := []string{"package reports", "func Totals(in iter.Seq[", "func TotalsFromCSV(r io.Reader, p TotalsParams)", "ssql.Safely("}
			if mode == "typed" {
				// the parallel plan: a Shards field beside the pipeline's own parameter
				wants = append(wants, "type TotalsParams struct {\n\tMin    float64", "Shards int", "typed.ParallelBatched(in, *flagShards)")
			} else {
				wants = append(wants, "type TotalsParams struct {\n\tMin float64", "records := in")
			}
			for _, want := range wants {
				if !strings.Contains(src, want) {
					t.Errorf("library lacks %q:\n%s", want, src)
				}
			}
			for _, bad := range []string{"package main", "flag.", "os.Exit(", "func main("} {
				if strings.Contains(src, bad) {
					t.Errorf("library contains %q:\n%s", bad, src)
				}
			}
			got, err := goRunGeneratedModule(t, "libtest", map[string]string{
				"reports/totals.go": src,
				"main.go":           libraryDriver("reports", "Totals", "TotalsFromCSV", orders),
			})
			if err != nil {
				t.Fatalf("driver failed: %v\n%s", err, got)
			}
			exec := equivShell(t, "exec", strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(
				strings.TrimSuffix(pipeline, " | {{.bin}} to csv")+" | {{.bin}} to jsonl"))
			want := equivCanon(equivParse(t, "exec", exec), false)
			have := equivCanon(equivParse(t, "go-lib", got), false)
			if strings.Join(want, "\n") != strings.Join(have, "\n") {
				t.Fatalf("library rows differ from exec:\n--- exec\n%s\n--- library\n%s", strings.Join(want, "\n"), strings.Join(have, "\n"))
			}
		})
	}

	t.Run("params_change_the_result", func(t *testing.T) {
		src, err := libraryGenerate(t, bin, data, "typed", "reports", "Totals", pipeline)
		if err != nil {
			t.Fatalf("generate: %v\n%s", err, src)
		}
		driver := strings.Replace(libraryDriver("reports", "Totals", "TotalsFromCSV", orders),
			"reports.TotalsDefaults()", "reports.TotalsParams{Min: 1e9}", 1)
		got, err := goRunGeneratedModule(t, "libtest", map[string]string{"reports/totals.go": src, "main.go": driver})
		if err != nil {
			t.Fatalf("driver failed: %v\n%s", err, got)
		}
		if strings.TrimSpace(got) != "" {
			t.Fatalf("Min 1e9 should select no order, got:\n%s", got)
		}
	})

	t.Run("join_side_file_takes_params", func(t *testing.T) {
		join := `{{.bin}} from csv {{.data}}/orders.csv | {{.bin}} join {{.data}}/customers.csv -using customer_id | {{.bin}} to csv`
		for _, mode := range []string{"typed", "1"} {
			src, err := libraryGenerate(t, bin, data, mode, "reports", "Enriched", join)
			if err != nil {
				t.Fatalf("mode %s generate: %v\n%s", mode, err, src)
			}
			got, err := goRunGeneratedModule(t, "libtest", map[string]string{
				"reports/enriched.go": src,
				"main.go":             libraryDriver("reports", "Enriched", "EnrichedFromCSV", orders),
			})
			if err != nil {
				t.Fatalf("mode %s driver failed: %v\n%s", mode, err, got)
			}
			exec := equivShell(t, "exec", strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(
				strings.TrimSuffix(join, " | {{.bin}} to csv")+" | {{.bin}} to jsonl"))
			want := equivCanon(equivParse(t, "exec", exec), false)
			have := equivCanon(equivParse(t, "go-lib", got), false)
			if strings.Join(want, "\n") != strings.Join(have, "\n") {
				t.Fatalf("mode %s: join rows differ from exec:\n--- exec\n%s\n--- library\n%s", mode, strings.Join(want, "\n"), strings.Join(have, "\n"))
			}
		}
	})

	t.Run("bad_cell_is_the_terminal_error", func(t *testing.T) {
		src, err := libraryGenerate(t, bin, data, "typed", "reports", "Totals", pipeline)
		if err != nil {
			t.Fatalf("generate: %v\n%s", err, src)
		}
		driver := `package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rosscartlidge/ssql/v4/typed"
	"libtest/reports"
)

func main() {
	in := strings.NewReader("order_id,customer_id,product,amount,order_date\n1,1,Widget,250.0,2024-01-01\n2,1,Widget,notanumber,2024-01-02\n")
	for _, err := range reports.TotalsFromCSV(in, reports.TotalsDefaults()) {
		if err != nil {
			var re *typed.ReadError
			fmt.Printf("terminal: ReadError=%v %v\n", errors.As(err, &re), err)
			fmt.Println("still alive")
			return
		}
	}
	fmt.Println("no error")
}
`
		got, err := goRunGeneratedModule(t, "libtest", map[string]string{"reports/totals.go": src, "main.go": driver})
		if err != nil {
			t.Fatalf("driver failed (the process must survive): %v\n%s", err, got)
		}
		if !strings.Contains(got, "terminal: ReadError=true") || !strings.Contains(got, "still alive") {
			t.Fatalf("bad cell did not surface as the typed terminal error:\n%s", got)
		}
	})

	t.Run("two_functions_share_a_package", func(t *testing.T) {
		a, err := libraryGenerate(t, bin, data, "typed", "reports", "Totals", pipeline)
		if err != nil {
			t.Fatalf("generate: %v\n%s", err, a)
		}
		b, err := libraryGenerate(t, bin, data, "typed", "reports", "Expensive", `{{.bin}} from csv {{.data}}/orders.csv | {{.bin}} where -if amount gt 200 | {{.bin}} to csv`)
		if err != nil {
			t.Fatalf("generate: %v\n%s", err, b)
		}
		got, err := goRunGeneratedModule(t, "libtest", map[string]string{
			"reports/totals.go":    a,
			"reports/expensive.go": b,
			"main.go":              libraryDriver("reports", "Expensive", "ExpensiveFromCSV", orders),
		})
		if err != nil {
			t.Fatalf("driver failed: %v\n%s", err, got)
		}
		if n := strings.Count(got, "\n"); n == 0 {
			t.Fatalf("no rows:\n%s", got)
		}
	})

	t.Run("optimiser_forwards_the_flags", func(t *testing.T) {
		// A dead sort before group-by fires sort-elimination, so the outer
		// generate go re-executes the pipeline and must hand -package/-func
		// to the inner one.
		cmdline := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(
			`{{.bin}} from csv {{.data}}/orders.csv | {{.bin}} sort amount | {{.bin}} group-by product -count n`)
		out, err := exec.Command("bash", "-c", "export SSQLGO=typed && "+cmdline+" | "+bin+" generate go -package reports -func Sorted").CombinedOutput()
		if err != nil {
			t.Fatalf("generate: %v\n%s", err, out)
		}
		for _, want := range []string{"package reports", "func Sorted(in iter.Seq[", "Optimised by generate go", "ssql sort amount"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("optimised library lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("refusals", func(t *testing.T) {
		for _, tc := range []struct{ args, want string }{
			{"-func X", "needs -package"},
			{"-package x -run", "nothing to run or build"},
			{"-package x -build /tmp/x", "nothing to run or build"},
		} {
			out, err := exec.Command("bash", "-c", "export SSQLGO=typed && "+bin+" from csv "+orders+" | "+bin+" generate go "+tc.args).CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Errorf("%s: want refusal containing %q, got err=%v:\n%s", tc.args, tc.want, err, out)
			}
		}
		out, err := libraryGenerate(t, bin, data, "1", "reports", "U", `{{.bin}} from csv {{.data}}/setops_left.csv | {{.bin}} union -file {{.data}}/setops_right.csv`)
		if err == nil || !strings.Contains(out, "several sources") {
			t.Errorf("union accepted in library mode, err=%v:\n%s", err, out)
		}
	})
}
