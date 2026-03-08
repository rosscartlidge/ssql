package ssql

import (
	"fmt"
	"slices"
	"sort"
	"testing"
)

// generateWindowRecords creates n presorted records for window benchmarks.
// Records have dept (partition), salary (order), and val (aggregation) fields.
// numPartitions controls how many distinct dept values exist.
func generateWindowRecords(n, numPartitions int) []Record {
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		r := MakeMutableRecord()
		r.fields["dept"] = fmt.Sprintf("dept_%04d", i%numPartitions)
		r.fields["salary"] = float64(i) // monotonically increasing within each partition
		r.fields["val"] = float64(i%1000) * 1.5
		records[i] = r.Freeze()
	}
	// Sort by dept then salary (presorted for StreamWindow)
	sort.Slice(records, func(i, j int) bool {
		di := GetOr(records[i], "dept", "")
		dj := GetOr(records[j], "dept", "")
		if di != dj {
			return di < dj
		}
		return GetOr(records[i], "salary", float64(0)) < GetOr(records[j], "salary", float64(0))
	})
	return records
}

// ============================================================================
// RUNNING SUM (default frame) — StreamWindow vs Window
// ============================================================================

func benchWindowRunningSum(b *testing.B, n int) {
	records := generateWindowRecords(n, 1)
	configs := []WindowConfig{{
		OrderBy: []OrderField{{Field: "salary"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WSum("val"), ResultName: "running_sum"}},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowRunningSum_1K(b *testing.B)  { benchWindowRunningSum(b, 1_000) }
func BenchmarkWindowRunningSum_10K(b *testing.B) { benchWindowRunningSum(b, 10_000) }

// ============================================================================
// SLIDING SUM (ROWS 2,0) — StreamWindow vs Window
// ============================================================================

func benchWindowSlidingSum(b *testing.B, n int) {
	records := generateWindowRecords(n, 1)
	configs := []WindowConfig{{
		OrderBy: []OrderField{{Field: "salary"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0},
		Specs:   []WindowSpec{{Function: WSum("val"), ResultName: "sliding_sum"}},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowSlidingSum_1K(b *testing.B)  { benchWindowSlidingSum(b, 1_000) }
func BenchmarkWindowSlidingSum_10K(b *testing.B) { benchWindowSlidingSum(b, 10_000) }

// ============================================================================
// SLIDING MIN (monotonic deque vs scan) — StreamWindow vs Window
// ============================================================================

func benchWindowSlidingMin(b *testing.B, n int) {
	records := generateWindowRecords(n, 1)
	configs := []WindowConfig{{
		OrderBy: []OrderField{{Field: "salary"}},
		Frame:   WindowFrame{Preceding: 9, Following: 0}, // 10-row window
		Specs:   []WindowSpec{{Function: WMin("val"), ResultName: "sliding_min"}},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowSlidingMin_1K(b *testing.B)  { benchWindowSlidingMin(b, 1_000) }
func BenchmarkWindowSlidingMin_10K(b *testing.B) { benchWindowSlidingMin(b, 10_000) }

// ============================================================================
// LAG — StreamWindow vs Window
// ============================================================================

func benchWindowLag(b *testing.B, n int) {
	records := generateWindowRecords(n, 1)
	configs := []WindowConfig{{
		OrderBy: []OrderField{{Field: "salary"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WLag("val", 1), ResultName: "prev_val"}},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowLag_1K(b *testing.B)  { benchWindowLag(b, 1_000) }
func BenchmarkWindowLag_10K(b *testing.B) { benchWindowLag(b, 10_000) }

// ============================================================================
// LEAD — StreamWindow vs Window
// ============================================================================

func benchWindowLead(b *testing.B, n int) {
	records := generateWindowRecords(n, 1)
	configs := []WindowConfig{{
		OrderBy: []OrderField{{Field: "salary"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WLead("val", 1), ResultName: "next_val"}},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowLead_1K(b *testing.B)  { benchWindowLead(b, 1_000) }
func BenchmarkWindowLead_10K(b *testing.B) { benchWindowLead(b, 10_000) }

// ============================================================================
// RANK (O(N) streaming vs materializing) — StreamWindow vs Window
// ============================================================================

func benchWindowRank(b *testing.B, n int) {
	records := generateWindowRecords(n, 1)
	configs := []WindowConfig{{
		OrderBy: []OrderField{{Field: "salary"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WRank(), ResultName: "rnk"}},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowRank_1K(b *testing.B)  { benchWindowRank(b, 1_000) }
func BenchmarkWindowRank_10K(b *testing.B) { benchWindowRank(b, 10_000) }

// ============================================================================
// PARTITIONED — 100 partitions of ~100 records each
// ============================================================================

func benchWindowPartitioned(b *testing.B, n int) {
	records := generateWindowRecords(n, n/100) // ~100 records per partition
	configs := []WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "salary"}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{
			{Function: WRowNumber(), ResultName: "rn"},
			{Function: WSum("val"), ResultName: "running_sum"},
		},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowPartitioned_10K(b *testing.B)  { benchWindowPartitioned(b, 10_000) }
func BenchmarkWindowPartitioned_100K(b *testing.B) { benchWindowPartitioned(b, 100_000) }

// ============================================================================
// COMBINED — Multiple functions in one config
// ============================================================================

func benchWindowCombined(b *testing.B, n int) {
	records := generateWindowRecords(n, 10)
	configs := []WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "salary"}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{
			{Function: WRowNumber(), ResultName: "rn"},
			{Function: WSum("val"), ResultName: "running_sum"},
			{Function: WAvg("val"), ResultName: "running_avg"},
			{Function: WCount(), ResultName: "cnt"},
			{Function: WMin("val"), ResultName: "running_min"},
			{Function: WMax("val"), ResultName: "running_max"},
			{Function: WLag("val", 1), ResultName: "prev_val"},
		},
	}}

	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range Window(configs)(slices.Values(records)) {
			}
		}
	})

	b.Run("StreamWindow", func(b *testing.B) {
		filter, _ := StreamWindow(configs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for range filter(slices.Values(records)) {
			}
		}
	})
}

func BenchmarkWindowCombined_1K(b *testing.B)  { benchWindowCombined(b, 1_000) }
func BenchmarkWindowCombined_10K(b *testing.B) { benchWindowCombined(b, 10_000) }
