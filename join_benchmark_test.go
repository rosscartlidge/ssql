package ssql

import (
	"slices"
	"testing"
)

// generateRecords creates n test records with a key field
// Creates duplicates by using modulo to ensure some join matches
func generateRecords(n int, keyField string) []Record {
	var records []Record
	for i := range n {
		r := MakeMutableRecord()
		r.fields[keyField] = int64(i % (n / 10)) // Create ~10% match rate
		r.fields["value"] = int64(i)
		r.fields["data"] = "test_data_" + string(rune('A'+i%26))
		records = append(records, r.Freeze())
	}
	return records
}

// ============================================================================
// INNER JOIN BENCHMARKS
// ============================================================================

func BenchmarkInnerJoin_Hash_100x100(b *testing.B) {
	left := generateRecords(100, "id")
	right := generateRecords(100, "id")
	pred := OnFields("id") // Uses hash join

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkInnerJoin_Nested_100x100(b *testing.B) {
	left := generateRecords(100, "id")
	right := generateRecords(100, "id")
	// Custom predicate forces nested loop
	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkInnerJoin_Hash_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnFields("id")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkInnerJoin_Nested_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkInnerJoin_Hash_10Kx10K(b *testing.B) {
	left := generateRecords(10000, "id")
	right := generateRecords(10000, "id")
	pred := OnFields("id")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkInnerJoin_Nested_10Kx10K(b *testing.B) {
	left := generateRecords(10000, "id")
	right := generateRecords(10000, "id")
	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

// ============================================================================
// LEFT JOIN BENCHMARKS
// ============================================================================

func BenchmarkLeftJoin_Hash_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnFields("id")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := LeftJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkLeftJoin_Nested_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := LeftJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

// ============================================================================
// RIGHT JOIN BENCHMARKS
// ============================================================================

func BenchmarkRightJoin_Hash_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnFields("id")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := RightJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkRightJoin_Nested_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := RightJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

// ============================================================================
// FULL JOIN BENCHMARKS
// ============================================================================

func BenchmarkFullJoin_Hash_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnFields("id")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := FullJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkFullJoin_Nested_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := FullJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

// ============================================================================
// MULTI-FIELD JOIN BENCHMARKS
// ============================================================================

func BenchmarkInnerJoin_Hash_MultiField_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnFields("id", "value") // Two-field join

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkInnerJoin_Nested_MultiField_1Kx1K(b *testing.B) {
	left := generateRecords(1000, "id")
	right := generateRecords(1000, "id")
	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0)) &&
			GetOr(l, "value", int64(0)) == GetOr(r, "value", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}
