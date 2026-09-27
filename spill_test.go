package ssql

import (
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestSortRecordsSpillMatchesInMemory(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var recs []Record
	for i := 0; i < 5000; i++ {
		m := MakeMutableRecord().
			Int("k", int64(rng.Intn(50))).
			String("s", []string{"x", "y", "z"}[rng.Intn(3)]).
			Float("f", rng.Float64()).
			Int("id", int64(i)).
			Time("at", t0.Add(time.Duration(rng.Intn(1000))*time.Minute))
		if i%7 == 0 {
			m = m.Null("f") // an absent-ish value the codec must carry
		}
		recs = append(recs, m.Freeze())
	}
	orderBy := []OrderField{{Field: "k"}, {Field: "s", Desc: true}}
	want := slices.Collect(SortRecords(orderBy)(slices.Values(recs)))

	dir := t.TempDir()
	// a tiny budget: many runs, all merged
	got := slices.Collect(SortRecordsSpill(orderBy, SpillConfig{Dir: dir, MemoryBytes: 8 << 10})(slices.Values(recs)))
	if len(got) != len(want) {
		t.Fatalf("spill returned %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if RecordKey(got[i]) != RecordKey(want[i]) {
			t.Fatalf("row %d differs (stability or order):\n got %s\nwant %s", i, RecordKey(got[i]), RecordKey(want[i]))
		}
		if _, ok := Get[time.Time](got[i], "at"); !ok {
			t.Fatalf("row %d: the time field came back as %T, not time.Time", i, GetOr(got[i], "at", any(nil)))
		}
		if _, ok := Get[int64](got[i], "id"); !ok {
			t.Fatalf("row %d: the int field came back as %T", i, GetOr(got[i], "id", any(nil)))
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "ssql-spill-*")); len(left) != 0 {
		t.Errorf("run directory not removed after the merge: %v", left)
	}
	// a budget nothing exceeds: in memory, no files
	got = slices.Collect(SortRecordsSpill(orderBy, SpillConfig{Dir: dir})(slices.Values(recs)))
	if len(got) != len(want) || RecordKey(got[0]) != RecordKey(want[0]) {
		t.Errorf("in-memory path differs")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("in-memory path wrote files: %v", entries)
	}
}

func TestSortRecordsSpillRefusesSequences(t *testing.T) {
	recs := []Record{
		MakeMutableRecord().Int("k", 1).IntSeq("xs", slices.Values([]int{1, 2})).Freeze(),
		MakeMutableRecord().Int("k", 0).IntSeq("xs", slices.Values([]int{3})).Freeze(),
	}
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("a sequence field must refuse to spill")
		}
	}()
	_ = slices.Collect(SortRecordsSpill([]OrderField{{Field: "k"}}, SpillConfig{Dir: t.TempDir(), MemoryBytes: 1})(slices.Values(recs)))
}
