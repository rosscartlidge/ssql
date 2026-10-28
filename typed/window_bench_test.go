package typed

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
)

type benchWinRow struct {
	Kind int64
	Name string
	V    int64
}

type benchWinOut struct {
	Kind int64
	Run  int64
}

// BenchmarkWindowRunningSumString is the benchmark pipeline's shape in
// small: partitions by an int, ordered by a string, a running sum — the
// sort of each partition dominated the 14.6 M-row run (DFC158 §4.3).
func BenchmarkWindowRunningSumString(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	rows := make([]benchWinRow, 200_000)
	for i := range rows {
		rows[i] = benchWinRow{Kind: int64(rng.Intn(8)), Name: fmt.Sprintf("n%07d", rng.Intn(1_000_000)), V: int64(rng.Intn(100))}
	}
	clauses := []WindowClause[benchWinRow]{{
		Partition: func(r benchWinRow) string { return strconv.FormatInt(r.Kind, 10) },
		HasOrder:  true,
		Compare: func(a, c benchWinRow) int {
			if a.Name < c.Name {
				return -1
			}
			if a.Name > c.Name {
				return 1
			}
			return 0
		},
		KeyString: func(r benchWinRow) string { return r.Name }, // as the template emits beside Compare
		Frame:     WindowFrame{Preceding: -1, Following: 0},
		Specs:     []WindowSpec[benchWinRow]{{Kind: WSum, Int: func(r benchWinRow) (int64, bool) { return r.V, true }}},
	}}
	w := Window(clauses, func(r benchWinRow, res []any) benchWinOut {
		v, _ := res[0].(interface{ Int64() int64 })
		var run int64
		if v != nil {
			run = v.Int64()
		}
		return benchWinOut{Kind: r.Kind, Run: run}
	})
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		n := 0
		for range w(func(yield func(benchWinRow) bool) {
			for _, r := range rows {
				if !yield(r) {
					return
				}
			}
		}) {
			n++
		}
		if n != len(rows) {
			b.Fatal(n)
		}
	}
}
