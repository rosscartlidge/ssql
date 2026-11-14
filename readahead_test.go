//go:build !slim

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
//
// 2026-10-11: the count stayed right but the requests were SERIAL —
// over a ~350ms round trip that alone cost 13.5s for a 1.6MB file.
// HTTPFile.Prefetch now fetches the chunks concurrently, merging
// adjacent ones; TestParquetHTTPConcurrentChunks pins it.
