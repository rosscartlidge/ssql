package ssql

// The readahead NEGATIVE RESULT (2026-08-30, closing DFC112's last
// item). Premise: remote parquet was slow because of many small
// Range requests; a chunked ReadaheadFile would collapse them.
// Measurement (1.2GB / 14.6M-row parquet, column-pruned group-by,
// instrumented server): the raw path issued 18 requests for 22MB —
// arrow's reader already reads whole column chunks. Readahead
// changed 7.18s → 6.83s remote… and LOCAL exec of the same pipeline
// is 6.97s. The gap was engine compute, not transport (the famous
// "7s vs 0.15s" compared exec against the typed-COMPILED head).
// Readahead was deleted, not shipped: a byte cache with no measured
// benefit. The test below pins the PREMISE — if a future parquet
// reader becomes chatty (small per-page reads), this gate fails and
// readahead becomes worth building that day.

import (
	"fmt"
	"io"
	"testing"
)

// TestParquetHTTPRequestEfficiency: reading a parquet over http must
// stay request-efficient — the reader fetches column chunks, not
// pages. If this fails after a dependency upgrade, see the negative
// result above before reaching for a readahead layer.
func TestParquetHTTPRequestEfficiency(t *testing.T) {
	dir := t.TempDir()
	rows := func(yield func(Record) bool) {
		for i := 0; i < 300000; i++ {
			r := MakeMutableRecord().
				Int("id", int64(i)).
				String("name", fmt.Sprintf("row-%d", i)).
				Float("val", float64(i)*1.5).
				Freeze()
			if !yield(r) {
				return
			}
		}
	}
	pq := dir + "/big.parquet"
	if err := WriteParquet(rows, pq); err != nil {
		t.Fatal(err)
	}
	srv := rangeServer(t, dir)
	h, err := OpenHTTPFile(srv.URL + "/big.parquet")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := ReadParquetFromReader(h)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for range seq {
		n++
	}
	if n != 300000 {
		t.Fatalf("rows = %d, want 300000", n)
	}
	t.Logf("Range requests for full read: %d", h.Requests())
	if h.Requests() > 40 {
		t.Fatalf("parquet-over-http went chatty: %d Range requests (was ~5-18; see the negative result above before adding readahead)", h.Requests())
	}
	var _ io.ReaderAt = h // the surface the premise depends on
}
