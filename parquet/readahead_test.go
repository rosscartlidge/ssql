package parquet

import (
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// TestParquetHTTPRequestEfficiency: reading a parquet over http must
// stay request-efficient — the reader fetches column chunks, not
// pages. If this fails after a dependency upgrade, see the negative
// result above before reaching for a readahead layer.
func TestParquetHTTPRequestEfficiency(t *testing.T) {
	dir := t.TempDir()
	rows := func(yield func(ssql.Record) bool) {
		for i := 0; i < 300000; i++ {
			r := ssql.MakeMutableRecord().
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
	if err := Write(rows, pq); err != nil {
		t.Fatal(err)
	}
	srv := rangeServer(t, dir)
	h, err := ssql.OpenHTTPFile(srv.URL + "/big.parquet")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := ReadFrom(h)
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

// TestParquetHTTPConcurrentChunks: over a high-latency link the cost
// is round trips, not requests (2026-10-11: 34 requests at ~350ms from
// Australia took 13.5s for a 1.6MB file that curl fetched in 0.5s).
// The selected column chunks must be fetched concurrently, the result
// must equal the local read, and column pruning must still fetch only
// the selected columns.
func TestParquetHTTPConcurrentChunks(t *testing.T) {
	dir := t.TempDir()
	rows := func(yield func(ssql.Record) bool) {
		for i := 0; i < 200000; i++ {
			if !yield(ssql.MakeMutableRecord().
				Int("id", int64(i)).
				String("name", fmt.Sprintf("row-%d", i)).
				Float("val", float64(i)*1.5).
				Freeze()) {
				return
			}
		}
	}
	pq := dir + "/rg.parquet"
	if err := Write(rows, pq, WithRowGroupSize(25000)); err != nil { // 8 row groups x 3 columns
		t.Fatal(err)
	}
	collect := func(seq iter.Seq[ssql.Record]) []string {
		var out []string
		for r := range seq {
			out = append(out, fmt.Sprint(ssql.GetOr(r, "id", int64(-1)), ssql.GetOr(r, "name", ""), ssql.GetOr(r, "val", float64(-1))))
		}
		return out
	}
	local, err := Read(pq)
	if err != nil {
		t.Fatal(err)
	}
	want := collect(local)

	// Small ranges, so the chunks stay separate and must overlap in
	// flight; the default limit's coalescing is checked further down.
	srv, peak := latencyRangeServer(t, dir, 20*time.Millisecond)
	h, err := ssql.OpenHTTPFile(srv.URL + "/rg.parquet")
	if err != nil {
		t.Fatal(err)
	}
	h.SetPrefetchMaxRange(64 << 10)
	seq, err := ReadFrom(h)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(seq); !slices.Equal(got, want) {
		t.Fatalf("remote read differs from local: %d rows vs %d", len(got), len(want))
	}
	if peak.Load() < 4 {
		t.Fatalf("column chunks fetched serially: peak %d request(s) in flight over %d requests", peak.Load(), h.Requests())
	}
	full := h.Requests()

	// Pruned to one column: only that column's chunks are prefetched.
	hp, err := ssql.OpenHTTPFile(srv.URL + "/rg.parquet")
	if err != nil {
		t.Fatal(err)
	}
	hp.SetPrefetchMaxRange(64 << 10)
	pseq, err := readParquetFromReaderWithColumns(hp, []string{"val"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for range pseq {
		n++
	}
	if n != 200000 {
		t.Fatalf("pruned rows = %d", n)
	}
	if hp.Requests() >= full || hp.Requests() > 8+4 {
		t.Fatalf("pruned read issued %d requests (full read %d): pruning lost", hp.Requests(), full)
	}
	t.Logf("full read %d requests, pruned %d, peak in flight %d", full, hp.Requests(), peak.Load())

	// The default limit coalesces adjacent chunks: this file (well
	// under 4MB) is the probe, the footer and one prefetch.
	hc, err := ssql.OpenHTTPFile(srv.URL + "/rg.parquet")
	if err != nil {
		t.Fatal(err)
	}
	cseq, err := ReadFrom(hc)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(cseq); !slices.Equal(got, want) {
		t.Fatalf("coalesced read differs from local")
	}
	if hc.Requests() > 4 {
		t.Fatalf("adjacent chunks not coalesced: %d requests", hc.Requests())
	}
}

// latencyRangeServer is rangeServer with a fixed delay per request and
// a high-water mark of requests in flight: the oracle for "are the
// column chunks fetched concurrently" (a count can't tell serial from
// parallel; the peak can, and the delay makes overlap certain).
func latencyRangeServer(t *testing.T, dir string, delay time.Duration) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var inFlight, peak atomic.Int64
	files := http.FileServer(http.Dir(dir))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(delay)
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &peak
}

// rangeServer serves dir with Range support (http.FileServer honours
// Range natively). A copy of the root package's test helper.
func rangeServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv
}
