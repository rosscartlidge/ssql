# Typed Parquet Reader Proposal

**Status:** Design + implementation in flight (2026-04-27).

This proposal adds Parquet input to the `typed` package, mirroring
the existing `ReadCSV` / `ReadDelim` API surface. Motivation is the
finding from the user-corpus group-by benchmark
(`journal/2026-W18.md` 2026-04-27 entry): on the
14.6 M-row CSV the Go pipeline is hitting the **memory-bandwidth
ceiling** of `bytes.IndexByte` at ~600 MB/s while DuckDB is hitting
the **disk-read ceiling** at ~1.5 GB/s. The CSV reader cannot get
faster within an order of magnitude without abandoning text parsing
entirely.

Parquet skips both ceilings by:
- **Reading 5–10× less data off disk** (Snappy-compressed columnar)
- **Native types — no string parsing** (no per-field allocation, no
  strconv)
- **Row groups as natural shards** (file metadata tells us where the
  partition boundaries are; no two-pass scan needed)

Projected wall-time on the same workload: in the 0.5–0.8 s range,
i.e. at or under DuckDB's CSV reader. (DuckDB itself can also read
Parquet, of course; the goal is parity within the typed pipeline.)

## 1. Library choice

Apache Arrow Go (`github.com/apache/arrow/go/v18`) is **already a
dependency** of `ssql` (used by the Record-mode `ReadParquet` in
`parquet.go`). We reuse it. No new third-party dependency.

Specifically we use:
- `parquet/file` — low-level row-group-aware reader
- `parquet/pqarrow` — Parquet → Arrow bridge (decompresses + decodes
  into Arrow column arrays)
- `arrow/array` — column accessors

The Record-mode reader builds an `arrow.Table` then converts each
row to a `Record`. The typed reader skips the conversion — it
copies values directly out of the Arrow column arrays into struct
fields via offset decoders, the same pattern used by the typed CSV
reader.

## 2. API surface

Mirror of CSV/Delim:

```go
// ParquetOption configures a Parquet reader.
type ParquetOption func(*parquetOpts)

// ParquetStrict mirrors typed.Strict and typed.DelimStrict.
func ParquetStrict() ParquetOption

// ParquetColumns selects a subset of columns to read. Skipping
// unwanted columns at the Parquet level is the primary I/O lever
// for wide tables — reading 3 of 50 columns means ~94% less I/O.
func ParquetColumns(names ...string) ParquetOption

// Serial readers.
func ReadParquet[T any](filename string, opts ...ParquetOption) iter.Seq[T]
func ReadParquetSafe[T any](filename string, opts ...ParquetOption) iter.Seq2[T, error]

// Parallel reader. Partitions row groups across n shards.
// n=0 means runtime.GOMAXPROCS(0). If the file has fewer row
// groups than n, n is reduced accordingly.
func ReadParquetParallel[T any](filename string, n int, opts ...ParquetOption) Stream[T]

// Writers (Snappy compression by default — same as Record-mode).
func WriteParquet[T any](seq iter.Seq[T], filename string) error
func (s Stream[T]) WriteParquet(filename string) error
```

No `*FromReader` variants because Parquet is a random-access format
that wants `io.ReaderAt` plus `io.Seeker`. The library exposes
that via `parquet.ReaderAtSeeker`; the helper variant takes that
type instead of `io.Reader`.

## 3. Schema mapping

Same struct-tag rules as CSV: `ssql:"name"` preferred, `csv:"name"`
fallback. Field name (case-insensitive) matches a Parquet column.

Go struct → Parquet column type mapping for **read**:

| Struct field type | Parquet types accepted |
|---|---|
| `string` | BYTE_ARRAY (UTF8) |
| `int8/16/32/64`, `uint8/16/32/64` | INT32, INT64 (with size check on read) |
| `float32` | FLOAT, DOUBLE |
| `float64` | FLOAT, DOUBLE |
| `bool` | BOOLEAN |
| `time.Time` | INT64 (timestamp ns/us/ms/s, all logical types) |
| pointer to any of the above | nullable form of above |

For **write**, struct fields drive the Parquet schema:

| Go type | Parquet column |
|---|---|
| `string` | BYTE_ARRAY (UTF8) |
| `int64` | INT64 |
| `int32` | INT32 |
| `float64` | DOUBLE |
| `float32` | FLOAT |
| `bool` | BOOLEAN |
| `time.Time` | INT64 (timestamp microseconds, UTC) |
| pointer thereof | optional column |

Snappy compression by default (matches Record-mode and DuckDB
defaults). No row-group-size knob in v1; library default is fine
for most workloads.

## 4. Parallelism strategy

A Parquet file is naturally partitioned into **row groups** (often
~100k–1M rows each, set by the writer). Each row group is
independently readable. So:

- `ReadParquetParallel[T](filename, n)` opens the file once, reads
  the metadata, and assigns row groups to shards round-robin (or
  contiguous; both work).
- Each shard owns a `pqarrow.FileReader` (the Arrow library
  supports concurrent reads via separate `FileReader` instances on
  the same file handle).
- Each shard's `iter.Seq[T]` walks its row groups one at a time,
  reading them into Arrow column arrays, then yields rows by
  copying values into struct fields.

Per-shard memory: roughly one row group's worth of decompressed
column data (typically 50–500 MB depending on row-group size and
data shape). That's higher than the Record reader's whole-file
materialisation only when there are very few row groups.

If `n > nRowGroups`, we cap `n` at `nRowGroups`. (Could also split
within a row group, but Parquet doesn't help with that — the
column chunks are encoded as units.)

## 5. Performance hypotheses

Hypothesis ranked by likely contribution:

1. **Less I/O.** A 1.23 GB CSV becomes a ~250 MB Parquet (Snappy)
   for typical analytics data. Disk read time goes from ~700 ms
   to ~150 ms.
2. **No string parsing.** `int64` and `float64` columns come out
   of Arrow as `[]int64` / `[]float64` directly — zero string
   allocation, zero `strconv.Parse*` calls.
3. **No newline pre-scan.** Row group boundaries are in the file
   metadata; we never scan bytes for `\n`. Saves ~600 MB of
   memory bandwidth on the 14.6 M-row workload.
4. **String aliasing.** Arrow's `BYTE_ARRAY` column stores all
   strings in a contiguous buffer; per-row strings can `unsafe.String`
   into that buffer (same trick as the TSV reader). Zero alloc on
   string fields too.

Combined: the row-scan phase should be O(decompression speed),
and decompression of Snappy is typically ~1–2 GB/s. So 250 MB
decompresses in ~150 ms. With parallelism, the bottleneck moves to
the per-row "copy values into struct" cost, which is small.

Target wall time: **~0.5–0.8 s** on the user-corpus 14.6 M-row
workload, i.e. at or under DuckDB's CSV read time.

## 6. Implementation plan

1. Convert the user-corpus CSV to Parquet (one-time, via DuckDB).
   Use as the headline benchmark dataset.
2. Implement `ReadParquet[T]` (serial). Same shape as `ReadCSV`:
   open file → get Arrow schema → match struct fields to columns
   → iterate rows.
3. Add `ParquetColumns(...)` and `ParquetStrict()` options.
4. Implement `ReadParquetParallel[T]`. Each shard owns its row
   group set; FileReader per shard is safe per Arrow's docs.
5. Implement `WriteParquet[T]` (serial) using the Arrow writer
   path + Snappy compression. Match the Record-mode output.
6. Implement `Stream[T].WriteParquet` per-shard buffer dump (each
   shard writes its rows into a Parquet `*bytes.Buffer`, dumped
   sequentially in shard order — same pattern as
   `Stream.WriteCSV`).
7. Tests: round-trip per type, schema mismatch, ParquetStrict,
   ParquetColumns, parallel parity vs serial, race-clean.
8. Benchmark on the user corpus + synthetic 10 M-row corpus.
9. CLI surface deferred (per user direction): the Go API is enough
   for this milestone.
10. Update `claude/concurrency.md` if a new pattern emerges, plus
    `typed-codegen-proposal.md` future-work list, plus journal.

## 7. v1 limitations / out of scope

- **No CLI codegen.** `ssql from parquet FILE` already exists for
  Record mode; not extending to typed-mode codegen in this
  milestone (deferred to next sprint).
- **No predicate pushdown.** Could in theory tell Parquet "skip
  row groups whose statistics rule out a `where` clause" — the
  pqarrow library supports this — but it adds complexity to
  codegen that we don't need yet.
- **No schema evolution / nested types.** Flat structs only. Lists,
  maps, nested groups all defer.
- **No streaming write.** `WriteParquet` always buffers the full
  output before flushing (Parquet's footer-last format requires
  some buffering anyway). Not a concern unless we hit a workload
  where this matters.
- **Row-group-size and compression tuning.** Library defaults only
  in v1. Add `WithCompression` / `WithRowGroupSize` options later
  if a use case justifies it.

## 8. Out-of-scope follow-ups (after this milestone)

- **CLI surface.** `ssql from parquet FILE` and `ssql to parquet FILE`
  in typed mode. Will need codegen support.
- **Predicate pushdown** for `where` clauses with simple equality /
  range checks. The Parquet column statistics let us skip whole
  row groups whose min/max excludes the predicate. Substantial
  speedup for selective queries on large files.
- **Column projection** at the codegen level: when the pipeline only
  uses 3 of 50 columns, emit `ParquetColumns(...)`. The Parquet
  reader skips reading the other 47, which is the biggest single
  I/O lever for wide tables.

## See also

- [`typed-codegen-proposal.md`](typed-codegen-proposal.md) §5d —
  parallel codegen status, includes Parquet as a pending future-work
  item.
- [`typed-groupby-parallel-proposal.md`](typed-groupby-parallel-proposal.md) —
  the concurrency pattern we'll reuse for `Stream[T].WriteParquet`.
- [`../../parquet.go`](../../parquet.go) — existing Record-mode
  Parquet support; reference for the Arrow library calls.
- [`../../typed/io_delim.go`](../../typed/io_delim.go) — TSV reader,
  which pioneered the zero-copy + parallel pattern this builds on.
