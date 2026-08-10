package commands

import (
	"fmt"
	"strings"
	"testing"
)

// TestExprGoNativeZeroAllocs is the Phase-1 performance gate: the transpiled
// predicate and assignment must run with ZERO allocations per row, while the
// VM path pays the Record→env copy + dispatch. Builds one program (same temp-
// module pattern as the differential harness) that measures both with
// testing.AllocsPerRun and wall-clock ns/op, prints the numbers, and fails
// if the native path allocates. The measured ratio is logged for the journal
// — measure, don't assert, on speed (only allocs are load-bearing here).
func TestExprGoNativeZeroAllocs(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a generated program")
	}

	schema := exprGoTestSchema()

	pred, err := exprToGoBool(`price * float(qty) > 1000 && city != "x"`, schema, "r")
	if err != nil {
		t.Fatal(err)
	}
	set, err := exprToGo(`price * 1.1`, schema, "r")
	if err != nil {
		t.Fatal(err)
	}
	// Record-mode native (Phase 4): same predicate over ssql.GetOr access.
	recPred, err := exprToGoRecord(`price * float(qty) > 1000 && city != "x"`,
		map[string]string{"price": "float64", "qty": "int64", "city": "string"}, "rec")
	if err != nil {
		t.Fatal(err)
	}
	if recPred.Type != exprGoBool {
		t.Fatalf("record predicate type: %s", recPred.Type)
	}

	src := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
)

type Row struct {
	Pop    int64
	Price  float64
	Qty    int64
	City   string
	Active bool
}

var rows = []Row{
	{Pop: 7, Price: 250.5, Qty: 2, City: "Oslo", Active: true},
	{Pop: -3, Price: 999.25, Qty: 3, City: "cairo", Active: false},
	{Pop: 20, Price: 15.5, Qty: 40, City: "Lagos", Active: true},
	{Pop: 0, Price: 0, Qty: 1, City: "Ao", Active: false},
}

var boolSink bool
var floatSink float64

func timePerOp(n int, f func(i int)) time.Duration {
	start := time.Now()
	for i := 0; i < n; i++ {
		f(i)
	}
	return time.Since(start) / time.Duration(n)
}

func main() {
	records := make([]ssql.Record, len(rows))
	for i, r := range rows {
		records[i] = ssql.MakeMutableRecord().
			Int("pop", r.Pop).Float("price", r.Price).Int("qty", r.Qty).
			String("city", r.City).Bool("active", r.Active).Freeze()
	}

	nativePred := func(r Row) bool { return %s }
	vmPred := runtime.MustCompileExprFilter(%q)
	nativeSet := func(r Row) float64 { return %s }
	vmSet := runtime.MustCompileExpr(%q)
	recPred := func(rec ssql.Record) bool { return %s }

	predAllocs := testing.AllocsPerRun(10000, func() { boolSink = nativePred(rows[0]) })
	setAllocs := testing.AllocsPerRun(10000, func() { floatSink = nativeSet(rows[0]) })
	recAllocs := testing.AllocsPerRun(10000, func() { boolSink = recPred(records[0]) })

	const N = 1_000_000
	nPred := timePerOp(N, func(i int) { boolSink = nativePred(rows[i%%4]) })
	vPred := timePerOp(N, func(i int) { boolSink = vmPred(records[i%%4]) })
	rPred := timePerOp(N, func(i int) { boolSink = recPred(records[i%%4]) })
	nSet := timePerOp(N, func(i int) { floatSink = nativeSet(rows[i%%4]) })
	vSet := timePerOp(N, func(i int) {
		v, _ := vmSet(records[i%%4])
		floatSink, _ = v.(float64)
	})

	fmt.Printf("pred-native-allocs=%%g set-native-allocs=%%g rec-native-allocs=%%g\n", predAllocs, setAllocs, recAllocs)
	fmt.Printf("pred-native=%%v pred-vm=%%v pred-record-native=%%v set-native=%%v set-vm=%%v\n", nPred, vPred, rPred, nSet, vSet)
	if predAllocs != 0 || setAllocs != 0 || recAllocs != 0 {
		os.Exit(1)
	}
	fmt.Println("OK")
}
`, pred.Src, `price * float(qty) > 1000 && city != "x"`, set.Src, `price * 1.1`, recPred.Src)

	out := buildAndRunExprProgram(t, src)
	t.Logf("expr benchmark:\n%s", out)
	if !strings.Contains(out, "OK") {
		t.Fatalf("native expression path allocates:\n%s", out)
	}
}
