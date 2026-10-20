package ssql

import "testing"

func TestRangeRecords(t *testing.T) {
	var got []int64
	for r := range RangeRecords(10, -3, 4, "i") {
		got = append(got, GetOr(r, "i", int64(0)))
		if GetOr(r, "n", int64(-1)) != -1 {
			t.Fatal("field n present under -field i")
		}
	}
	if want := []int64{10, 7, 4, 1}; len(got) != 4 || got[0] != want[0] || got[3] != want[3] {
		t.Fatalf("RangeRecords(10,-3,4) = %v, want %v", got, want)
	}
	for range RangeRecords(1, 1, 0, "n") {
		t.Fatal("count 0 yielded a record")
	}
	for range RangeRecords(1, 1, -2, "n") {
		t.Fatal("negative count yielded a record")
	}
	// Early stop ends the sequence.
	seen := 0
	for range RangeRecords(1, 1, 1000, "n") {
		seen++
		if seen == 3 {
			break
		}
	}
	if seen != 3 {
		t.Fatalf("stopped at %d", seen)
	}
}
