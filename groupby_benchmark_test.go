package ssql

import (
	"fmt"
	"slices"
	"testing"
)

// generateGroupRecords builds n records of one int64 column `n` and a key
// column `k` with numGroups distinct values, one shared schema (the #1
// performance rule) — the shape of `group-by k -sum n total`.
func generateGroupRecords(n, numGroups int) []Record {
	schema := NewSchema([]string{"k", "n"})
	records := make([]Record, n)
	for i := range n {
		records[i] = NewRecordFromSchema(schema, []any{fmt.Sprintf("k%04d", i%numGroups), int64(i%1000 + 1)})
	}
	return records
}

// BenchmarkGroupBySum is the HEAD-vs-branch gate for DFC155 C: the
// integer sum's accumulator widens from int64 to Int128, and this is the
// measurement that says what every existing `-sum` pays for it. 1M rows,
// 1000 groups (the result side is per group, the accumulate side per row).
func BenchmarkGroupBySum(b *testing.B) {
	records := generateGroupRecords(1_000_000, 1000)
	aggs := []NamedAgg{{Name: "total", Fn: Sum("n")}, {Name: "mean", Fn: Avg("n")}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		grouped := GroupByFields("_group", "k")(slices.Values(records))
		for range AggregateOrdered("_group", aggs)(grouped) {
		}
	}
}

// BenchmarkNumericSum isolates the accumulator: a million int64 adds
// through numericSum, no grouping, no records.
func BenchmarkNumericSum(b *testing.B) {
	vals := make([]any, 1_000_000)
	for i := range vals {
		vals[i] = int64(i%1000 + 1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var s numericSum
		for _, v := range vals {
			s.add(v)
		}
		_ = s.result()
	}
}
