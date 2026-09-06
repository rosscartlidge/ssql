# `ssql/typed` Reference

The `ssql/typed` package provides a high-performance, struct-based data path
alongside the main `ssql.Record` API. Use it when your schema is known at
compile time and the pipeline is hot.

> **Status:** Phase 1.5 — CSV + JSONL I/O, core operations
> (Where, Limit, Skip, Select), full join family (Hash, HashMulti,
> Left, Right, Full), streaming aggregation (GroupBy, GroupByOrdered,
> standalone Sum/Count/Min/Max/Avg), field types
> string/bool/int/int32/int64/uint64/float32/float64/time.Time + pointers.
> Arrow I/O is the next major addition.
> See [`doc/research/typed-package-proposal.md`](research/typed-package-proposal.md)
> for the design and [`doc/research/typed-performance-notes.md`](research/typed-performance-notes.md)
> for known optimization opportunities.

## When to use it

| Use `ssql.Record` when… | Use `ssql/typed` when… |
|---|---|
| Schema is unknown or dynamic | Schema is known at compile time |
| Prototyping or one-off scripts | Pipeline runs nightly or processes millions of rows |
| Need to handle arbitrary CSV / JSON | Will declare the input/output types anyway |
| Fields used reflectively (e.g. `record.All()`) | Field access is positional |

The two APIs are complementary — most projects use both. `ssql.Record` for
exploratory work and dynamic-schema cases; `ssql/typed` for the inner loop.

## Performance

### Headline: 10M rows × 3 chained joins (end-to-end with CSV I/O)

| Implementation | Time | Memory allocated | Allocations |
|---|---:|---:|---:|
| `ssql.Record` | 74.8 s | 37.7 GB | 544 M |
| **`ssql/typed`** | **4.94 s** | **1.10 GB** | **20.0 M** |
| **vs Record** | **15.1×** | **34.2× less** | **27.2× fewer** |

All three pipelines produce 7.25 M output rows (correctness validated).
A 75-second batch job becomes 5 seconds; 38 GB of allocations becomes 1 GB.

### How does this compare to DuckDB?

For context, the same workload run via the DuckDB CLI on the same files:

| Implementation | Time | Notes |
|---|---:|---|
| `ssql.Record` | 74.8 s | row-based, `map[string]any` |
| **`ssql/typed`** | **4.94 s** | row-based, struct fields, pure Go |
| DuckDB CLI v1.5.0 | 0.42 s | columnar + SIMD, native C++, ~50 MB binary |

DuckDB is ~12× faster than `ssql/typed` on that join-heavy workload.
Most of that gap comes from **columnar storage with vectorized SIMD
execution** — fundamental architectural advantages that no row-based
runtime can match without rewriting around Apache Arrow or similar.

The picture reverses on scan-and-aggregate queries once the pipeline
optimiser prunes the read (2026-09-05, 14.6 M-row parquet, three-field
`-cube` with counts): DuckDB on the `generate sql` output 0.95 s / 2.7 GB;
the `generate go` typed program **0.27 s / 0.69 GB** — a pruned parallel
parquet read of the three columns used, a parallel detail group-by, and
`RollupEnrich` for the parent levels. `generate go` optimises by default
(`+O` disables); the unoptimised program read all seven columns in 1.73 s
and 8.6 GB, which is why the default changed.

Where `ssql/typed` competes:

- **Zero native dependency** — pure Go, no CGO, no shared library, no
  `~/.local/bin/duckdb` install step. Drops into any Go program with
  `go get`.
- **~5 KB of source on the data path** — `typed/io.go` + `typed/ops.go`
  + `typed/agg.go` total 600 LOC. Trivially auditable.
- **Streaming, not materializing** — pipelines are `iter.Seq[T]` all the
  way down. DuckDB materializes intermediate join results.
- **Composes with the rest of Go** — joining streamed data against a
  `chan T` or a custom reader is one line. DuckDB requires bridging
  through SQL or a connection.

For pure throughput on static datasets, DuckDB wins. For embedded
Go pipelines that need a typed, streaming, dependency-free fast path,
`ssql/typed` is the right tool.

### Smaller workload: 1M rows × 1 join

| Implementation | Time | Memory | Allocs |
|---|---:|---:|---:|
| `ssql.Record`, end-to-end | 2,006 ms | 909 MB | 19.6 M |
| `ssql/typed`, end-to-end | **386 ms** | **96 MB** | **2.0 M** |
| `ssql.Record`, compute-only | 1,009 ms | 644 MB | 11.6 M |
| `ssql/typed`, compute-only | **69 ms** | **0.3 MB** | **20** |

End-to-end: **5.2× faster, 9.4× less memory.**
Compute-only (CSV stripped): **14.5× faster, 2,000× less memory.**

The compute-only number isolates the Record-vs-struct cost. The end-to-end
number includes CSV reading on both sides; ssql/typed's reflection-built
decoder costs ~20% over a hand-rolled positional reader, which is the price
of keeping the API generic.

### Reproducing

```bash
# Quick benches (~1 minute, 1M-row workload)
go test -bench=. -benchtime=3x -run=^$ ./typed/...

# Headline benches (~2 minutes, 10M × 3-join workload — generates 600 MB CSV)
go test -bench=Scale -benchtime=1x -run=^$ -timeout=30m ./typed/...

# DuckDB baseline (requires duckdb on PATH, reuses the dataset)
go test -bench=DuckDB -benchtime=1x -run=^$ -timeout=10m ./typed/...
```

Hardware: Intel Core Ultra 9 275HX, single-threaded.

## Field tags

CSV column names map to struct fields case-insensitively. Override with a tag:

```go
type Employee struct {
    Name     string                          // matches "Name", "NAME", "name"
    DeptID   string  `ssql:"dept_id"`        // matches "dept_id"
    Years    int64                           // matches "Years", "YEARS", "years"
    Internal string  `ssql:"-"`              // skipped from CSV I/O
}
```

`ssql:"name"` is the preferred form. `csv:"name"` is also accepted as a fallback
for ecosystem compatibility (e.g. structs already tagged for `encoding/csv`).
A tag value of `"-"` excludes the field entirely.

## Supported field types

`string`, `bool`, `int`, `int32`, `int64`, `uint64`, `float32`, `float64`,
`time.Time` (RFC3339 in CSV), and **pointer-to-T** for nullable columns.
Empty CSV values become the zero value (or `nil` for pointer types). **Note:** this differs from the record lanes, where an empty numeric/boolean cell is *absent* (missing) since v4.86 — see `doc/research/dfc124_missing_values.md` §3 for the typed gap and its planned fix (pointer types for partially-empty columns).

Other parse errors silently zero the field in `ReadCSV`; use `ReadCSVSafe`
to surface them.

> **Note on nullables**: pointer-to-T columns allocate one heap value per
> non-empty cell. For hot paths with many nullables, consider an explicit
> `Valid bool` field (sql.NullInt64-style) instead.

## API

### Reading

```go
func ReadCSV[T any](filename string) iter.Seq[T]
func ReadCSVFromReader[T any](r io.Reader) iter.Seq[T]

func ReadCSVSafe[T any](filename string) iter.Seq2[T, error]
func ReadCSVSafeFromReader[T any](r io.Reader) iter.Seq2[T, error]
```

`ReadCSV` is the lossy/fast variant — parse errors and missing files yield no
rows. `ReadCSVSafe` returns an `iter.Seq2[T, error]` so the consumer can choose
to halt, log, or skip on each error. Mirrors the `ssql.ReadCSV` /
`ssql.ReadCSVSafe` split.

### Writing CSV

```go
func WriteCSV[T any](seq iter.Seq[T], filename string) error
func WriteCSVToWriter[T any](seq iter.Seq[T], w io.Writer) error

// Parallel-aware sinks on Stream[T] — each shard formats its rows
// into its own bytes.Buffer concurrently, then buffers are dumped in
// shard order. Avoids the per-row Serial() fan-in channel cost on
// transform-and-write workloads (~4.4× faster than typed-serial on a
// 7M-row CSV write benchmark).
func (s Stream[T]) WriteCSV(filename string) error
func (s Stream[T]) WriteCSVToWriter(w io.Writer) error
```

The header row is taken from struct field names (or tags). All exported fields
are written in declaration order. Unexported fields and `ssql:"-"`-tagged
fields are skipped.

The `Stream[T]` variants peak at ~2× output size in memory (each shard buffers
its slice before dump). For outputs that don't fit in RAM, fall back to the
serial form: `Stream.Serial()` then `WriteCSV` — slower but streaming. Order
within each shard is preserved; across shards it is shard-concatenation order
(rows from shard 0 before shard 1, etc.) — same as `Stream.Serial()`.

### TSV / Delimited (no quoting)

```go
func ReadDelim[T any](filename string, opts ...DelimOption) iter.Seq[T]
func ReadDelimFromReader[T any](r io.Reader, opts ...DelimOption) iter.Seq[T]
func ReadDelimSafe[T any](filename string, opts ...DelimOption) iter.Seq2[T, error]
func ReadDelimParallel[T any](filename string, n int, opts ...DelimOption) Stream[T]

func WriteDelim[T any](seq iter.Seq[T], filename string, opts ...DelimOption) error
func WriteDelimToWriter[T any](seq iter.Seq[T], w io.Writer, opts ...DelimOption) error
func (s Stream[T]) WriteDelim(filename string, opts ...DelimOption) error
func (s Stream[T]) WriteDelimToWriter(w io.Writer, opts ...DelimOption) error

// Default delimiter is '\t'. Pass typed.WithDelim(',') for fast clean
// CSV reading WITHOUT quote handling, '|' / ':' for pipe / colon
// formats.
typed.WithDelim(byte) DelimOption
typed.DelimStrict() DelimOption  // mirrors typed.Strict for CSV
```

Same struct-tag mapping as `ReadCSV`; same `Stream[T]` per-shard buffer
sink. **Differs from `ReadCSV` only in that fields are split on a
single byte with no quote/escape handling — embedded delimiters or
newlines produce wrong rows.** Use this when your data is clean
delimited text; use `ReadCSV` for RFC-4180-correct parsing.

The parallel reader (`ReadDelimParallel`) does zero-copy field
strings (each row's strings alias into the file's mmap'd bytes via
`unsafe.String`) and uses `bytes.IndexByte` for SIMD-accelerated
field splitting. This makes the parser cost competitive with the
memory-bandwidth ceiling.

### Parquet

```go
func ReadParquet[T any](filename string, opts ...ParquetOption) iter.Seq[T]
func ReadParquetSafe[T any](filename string, opts ...ParquetOption) iter.Seq2[T, error]
func ReadParquetFromReaderAt[T any](r parquet.ReaderAtSeeker, opts ...ParquetOption) iter.Seq[T]
func ReadParquetSafeFromReaderAt[T any](r parquet.ReaderAtSeeker, opts ...ParquetOption) iter.Seq2[T, error]
func ReadParquetParallel[T any](filename string, n int, opts ...ParquetOption) Stream[T]

func WriteParquet[T any](seq iter.Seq[T], filename string) error
func WriteParquetToWriter[T any](seq iter.Seq[T], w io.Writer) error
func (s Stream[T]) WriteParquet(filename string) error
func (s Stream[T]) WriteParquetToWriter(w io.Writer) error

typed.ParquetStrict() ParquetOption       // reject schema mismatches
typed.ParquetColumns(names...) ParquetOption  // read only listed columns
```

Snappy compression by default. Reads use the existing
`github.com/apache/arrow/go/v18/parquet` dependency that ssql
already imports for Record-mode Parquet.

**`ParquetColumns` is the primary lever.** A 14.6M-row corpus
group-by-with-count benchmark went from **1.51 s** (read all 7
columns) to **0.15 s** (read only the column needed for the
group key) — a 10× speedup. For wide tables it's the difference
between Parquet feeling fast and feeling like CSV-with-extra-steps.

The parallel reader assigns Parquet row groups to shards
round-robin; each shard owns its own `pqarrow.FileReader`. Peak
memory is roughly `nShards × max-row-group-size`. If the file has
fewer row groups than `n`, `n` is reduced to match — Parquet
doesn't allow splitting within a row group without re-decoding it.

### Operations

```go
func Where[T any](pred func(T) bool) func(iter.Seq[T]) iter.Seq[T]
func Limit[T any](n int)             func(iter.Seq[T]) iter.Seq[T]
func Skip[T any](n int)              func(iter.Seq[T]) iter.Seq[T]
func Select[T, U any](fn func(T) U)  func(iter.Seq[T]) iter.Seq[U]
```

Each returns a function that transforms an `iter.Seq[T]` — same composition
shape as the main `ssql` package, so a typed pipeline reads identically:

```go
result := typed.Where(pred1)(typed.Skip[T](10)(typed.Limit[T](100)(input)))
```

### Top-k selection

```go
func TopBy[T any, K Ordered](n int, keyFn func(T) K)    func(iter.Seq[T]) iter.Seq[T]
func BottomBy[T any, K Ordered](n int, keyFn func(T) K) func(iter.Seq[T]) iter.Seq[T]

// Parallel forms — consume a Stream[T], return the ≤ n winners as iter.Seq[T].
func TopByParallel[T any, K Ordered](in Stream[T], n int, keyFn func(T) K)    iter.Seq[T]
func BottomByParallel[T any, K Ordered](in Stream[T], n int, keyFn func(T) K) iter.Seq[T]
```

`TopBy` / `BottomBy` keep a bounded heap of size `n`: **O(N·log n)** time and
**O(n)** memory, versus the O(N·log N) time + O(N) memory of a full
`SortByDesc` + `Limit`. Results are yielded best-first (descending key for
`TopBy`, ascending for `BottomBy`). These are the typed analogues of
`ssql.TopBy` / `ssql.BottomBy`.

Top-k is an associative reduction, so it parallelises: `TopByParallel` keeps a
per-shard heap over each `Stream[T]` shard concurrently, then merges the
survivors (≤ shards·n entries) through one final size-n selection. The `top`
CLI command emits the parallel form when the upstream is a `Stream` and the
serial form otherwise (the planner picks per pipeline).

### Hash join

```go
func HashJoin[L, R, O any, K comparable](
    left      iter.Seq[L],
    right     iter.Seq[R],
    leftKey   func(L) K,
    rightKey  func(R) K,
    merge     func(L, R) O,
) iter.Seq[O]
```

Materializes `right` in a `map[K]R` (build phase), then streams `left`
(probe phase). Inner-join semantics: a left row with no matching right row
is dropped. For multi-column joins, pass a tuple type as `K`.

If the right side has duplicate keys, only the last value per key is kept.
Use `HashJoinMulti` for many-to-many joins, or `LeftJoin` / `RightJoin` /
`FullJoin` for outer-join semantics with an explicit `found bool` flag.

```go
func HashJoinMulti[L, R, O any, K comparable](...) iter.Seq[O]
func LeftJoin[L, R, O any, K comparable](
    left, right ..., merge func(L, R, found bool) O,
) iter.Seq[O]
func RightJoin[L, R, O any, K comparable](...) iter.Seq[O]
func FullJoin[L, R, O any, K comparable](
    left, right ..., merge func(L, R, leftFound, rightFound bool) O,
) iter.Seq[O]
```

## JSONL I/O

For newline-delimited JSON (`one object per line`), use the JSONL pair:

```go
func ReadJSONL[T any](filename string) iter.Seq[T]
func ReadJSONLSafe[T any](filename string) iter.Seq2[T, error]
func WriteJSONL[T any](seq iter.Seq[T], filename string) error
```

Field mapping follows standard `json:"name"` struct tags. Implementation
uses `encoding/json` (reflection per row); for high-throughput JSONL
pipelines see [`doc/research/typed-performance-notes.md`](research/typed-performance-notes.md).

**Header lines.** Both readers skip a leading `{"_schema": …}` line (the
header `tee` and every ssql stage write), so a tee'd file reads as its
rows. **Codegen.** `SSQL_MODE=typed … from jsonl FILE` infers the row
struct from the file (`_schema` header when present, else a sample of
lines; `lib.SampleJSONLSchema`) and emits `ReadJSONL` — added 2026-09-06
because the previous record-mode fallback made a compiled JSONL
pipeline four times slower than the interpreted one (14.8 s / 3.9 GB →
4.25 s / 25 MB on a 3M-row group-by). **Decoding is positional**
(2026-09-06): the type is reflected over once into a key → field plan
using the CSV reader's own field decoders, and each line is walked once
— 1.13 s on the same group-by, 3.6× encoding/json's throughput in the
micro-benchmark. Slice, map and nested-struct fields fall back to
encoding/json. A string field accepts any JSON value as its raw text
(as exec does); an `ssql` tag names the key when there is no `json` tag.
`ReadJSONLParallel[T](file, n)` is the Stream form (mmap + newline
index + a shard per run of lines, the CSV twin's shape): the same 3M-row
group-by in **0.21 s**, and it is what `from jsonl` emits when a
downstream stage accepts a Stream.

## Aggregation

```go
func Count[T any](seq iter.Seq[T]) int64
func Sum[T any, N Number](seq iter.Seq[T], fn func(T) N) N
func Min[T any, N Ordered](seq iter.Seq[T], fn func(T) N) (N, bool)
func Max[T any, N Ordered](seq iter.Seq[T], fn func(T) N) (N, bool)
func Avg[T any, N Number](seq iter.Seq[T], fn func(T) N) (float64, int64)
```

Standalone aggregates over an entire stream. For per-group results:

```go
func GroupBy[T, S, O any, K comparable](
    seq iter.Seq[T],
    keyFn func(T) K,
    newAgg AggFunc[T, S],   // fresh accumulator per group
    build func(K, S) O,     // build output row from key + final state
) iter.Seq[O]

func GroupByOrdered[T, S, O any, K comparable](...)  // O(1) memory; pre-sorted input

// Parallel variant — Sink/Combine/Finalize three-phase contract.
// Each shard builds its own partial map; the orchestrator merges
// shards sequentially after Wait; the result iterator yields lazily.
// 4.0× faster than serial GroupBy on the 10M-row × 1 000-group
// benchmark (close to DuckDB).
func GroupByParallel[T, S, O any, K comparable](
    in     Stream[T],
    keyFn  func(T) K,
    newAgg ParallelAggFunc[T, S],   // newAgg() returns ParallelAggregator
    build  func(K, S) O,
) iter.Seq[O]
```

Prebuilt aggregators: `Counter[T]`, `NewSummer(fn)`, `NewAverager(fn)` —
all three implement `Merge` so they double as `ParallelAggregator`.
The parallel constructors are `NewCounter[T]()`, `NewParallelSummer(fn)`,
`NewParallelAverager(fn)`. Custom accumulators implement the
`Aggregator[T, R]` interface (serial); add a `Merge(other Aggregator[T, R])`
method to satisfy `ParallelAggregator[T, R]`.

Use `GroupBy` for unordered serial input (buffers all groups in a map).
Use `GroupByOrdered` when the input is pre-sorted by key (O(1) memory).
Use `GroupByParallel` when the input is a `Stream[T]` and `#rows ≫
#groups` — the Combine cost is `O(#shards × #groups)`, negligible
compared to the row scan. **Ordering:** within a shard, first-seen-key
order is preserved; across shards, shard-0's first-seen keys come
before shard-1's, etc. Deterministic but not the same as serial
GroupBy (which sees keys in true input order).

### Rollup / cube enrichment

```go
func RollupEnrich[D, A, O any](detail iter.Seq[D], sets []RollupSet[D, A], state func(D) A, build func(D, []A) O) iter.Seq[O]

type RollupSet[D, A any] struct {
    Key   func(D) string   // the grouping set's key, projected from a detail row
    New   func() A         // an empty partial state
    Merge func(acc, part A)
}
```

The typed form of `group-by … -rollup|-cube` (one row per detail group,
every grouping set's aggregates as prefixed columns). The detail
group-by — `GroupByParallel` with an aggregator whose result is its own
mergeable STATE — makes the only pass over the rows; `RollupEnrich`
then merges the detail states that share each set's key and builds the
output row. Work is #groups × #sets, so a 14.6M-row, 161-group cube
costs what the plain group-by costs (1.7 s; the record-mode `Rollup`
that re-keys every row per set took 36 s). Generated by
`SSQL_MODE=typed … group-by … -cube`; aggregations without a Merge
(`-collect`, expressions) fall back to record codegen.

## Worked example

```go
package main

import (
    "log"

    "github.com/rosscartlidge/ssql/v4/typed"
)

type Employee struct {
    Name   string
    DeptID string `ssql:"dept_id"`
    Years  int64
    Salary float64
}

type Department struct {
    DeptID   string `ssql:"dept_id"`
    DeptName string `ssql:"dept_name"`
    Location string
}

type Senior struct {
    Name     string
    Years    int64
    Salary   float64
    DeptName string `ssql:"dept_name"`
    Location string
}

func main() {
    employees := typed.ReadCSV[Employee]("employees.csv")
    depts     := typed.ReadCSV[Department]("departments.csv")

    seniors := typed.Where(func(e Employee) bool {
        return e.Years >= 5
    })(employees)

    joined := typed.HashJoin(seniors, depts,
        func(e Employee) string   { return e.DeptID },
        func(d Department) string { return d.DeptID },
        func(e Employee, d Department) Senior {
            return Senior{
                Name: e.Name, Years: e.Years, Salary: e.Salary,
                DeptName: d.DeptName, Location: d.Location,
            }
        })

    if err := typed.WriteCSV(joined, "seniors.csv"); err != nil {
        log.Fatal(err)
    }
}
```

A runnable side-by-side comparison with the `ssql.Record` equivalent (same
workload, both APIs, prints the speedup) lives at
[`examples/typed_pipeline`](../examples/typed_pipeline). Run it with:

```bash
go run ./examples/typed_pipeline -rows 1000000
```

## Design principle

> **All reflection happens once at setup time. The per-row data path is
> reflection-free.**

`ReadCSV[T]` reads the header once, builds a `[]fieldDecoder` (one closure
per CSV column) using reflection, then loops over data rows calling those
closures by index. Each closure already knows the field's byte offset and
concrete type. The per-row write is essentially:

```go
*(*int64)(unsafe.Add(p, off)) = parseInt64(s)
```

No reflection, no boxing, no method-table indirection. The Go compiler can
inline aggressively and the GC stays quiet — escape analysis routinely
allocates whole `JoinedRow` structs on the stack.

`Where`, `HashJoin`, `Limit`, `Skip`, and `Select` are pure generics with no
reflection at all.

## Roadmap

Phase 1 (shipped):
- [x] CSV I/O with header inference
- [x] `Where`, `Limit`, `Skip`, `Select`
- [x] `HashJoin` (inner)
- [x] Benchmarks demonstrating the gap

Phase 1.5 (shipped):
- [x] `time.Time` (RFC3339), `int32`, `uint64`, `float32`
- [x] Pointer-to-T for nullable columns
- [x] Full join family: `HashJoinMulti`, `LeftJoin`, `RightJoin`, `FullJoin`
- [x] JSONL reader/writer (`ReadJSONL`, `ReadJSONLSafe`, `WriteJSONL`)
- [x] Streaming aggregation: `Count`, `Sum`, `Min`, `Max`, `Avg`,
      `GroupBy`, `GroupByOrdered`, `Counter`, `Summer`, `Averager`

Phase 1.6 (shipped 2026-04-26):
- [x] `HashJoinSized` with capacity hint for known right-side size
- [x] Strict-mode CSV reader via `Strict()` option
- (Tried and rejected: custom byte-level CSV reader — see
  [`research/typed-performance-notes.md`](research/typed-performance-notes.md))

Phase 1.7 (shipped 2026-04-27 — unblocks Tier 3 codegen):
- [x] `SortBy[T,K]`, `SortByDesc[T,K]`, `SortByStable[T,K]`
- [x] `Distinct[T,K]` (streaming, hash-set state)
- [x] `Concat[T]`, `Union[T,K]`
- Tier 3 codegen for `sort` / `distinct` / `union` can now wire into
  these directly. See
  [`research/typed-package-proposal.md` §6a](research/typed-package-proposal.md#6a-library-phases-after-phase-1).

Phase 1.8+ (open):
- [ ] Arrow reader/writer (`ReadArrow[T]`, `WriteArrow[T]`)
- [ ] Faster JSONL via `goccy/go-json` or per-type generated unmarshallers
- [ ] Hand-rolled RFC3339 time parser (~3× over `time.Parse`)

Phase 2 — Tier 1 shipped (2026-04-26):
- [x] `SSQL_MODE=typed ssql generate go` — schema-aware code generation that
  emits calls into this package directly. Tier 1 covers
  `from FILE.csv` (header sampled at generation time, struct types
  auto-derived), `where -if FIELD OP VALUE` (literal operators only),
  `join FILE.csv -using FIELD` (single-key + process-substitution),
  `to csv`, and `to table`. Other commands abort with a clear error.
  See [`research/typed-codegen-proposal.md`](research/typed-codegen-proposal.md).

```bash
# Same prototype pipeline you'd run interactively...
SSQL_MODE=typed ssql from employees.csv \
    | ssql where -if years ge 5 \
    | ssql join departments.csv -using dept_id \
    | ssql to csv seniors.csv \
    | ssql generate go > pipeline.go

# ...is now a self-contained, type-safe Go program.
go run pipeline.go              # uses defaults
go run pipeline.go -input emp_q4.csv
```

**Measured impact of typed codegen vs the alternatives**, on 1M
employees × 1k departments, identical pipeline expression
(see `cmd/ssql/codegen_bench_test.go`):

| Mode | Wall time | Peak RSS |
|---|---:|---:|
| CLI pipeline (interactive) | 3.08 s | 33 MB |
| Record codegen (`SSQL_MODE=record`) | 2.69 s | 910 MB |
| **Typed codegen (`SSQL_MODE=typed`)** | **0.77 s** | **8.7 MB** |
| Speedup vs CLI | **4.0× faster** | — |
| Speedup vs Record codegen | **3.5× faster** | **104× less memory** |

Typed codegen wins on every dimension simultaneously: single-process
execution beats the CLI pipeline's per-stage process+pipe overhead,
and stack-allocated structs beat Record codegen's `map[string]any`
peak RSS. Reproduce: `go test ./cmd/ssql/ -run TestCodegenBench -timeout 10m -v`.

Phase 2 — Tier 2 shipped (2026-04-26):
- [x] `limit N` (typed.Limit), `offset N` (typed.Skip)
- [x] `include` / `exclude` / `rename` (typed.Select with derived struct)
- [x] `group-by FIELDS… -count -sum -avg -min -max` (typed.GroupBy with
      synthesized aggregator + result struct, single- or multi-field keys)

Phase 2 — parallel-form codegen shipped (2026-04-27):

> **Merged into `SSQL_MODE=typed` in v4.40.** What started as a separate
> `SSQL_MODE=parallel` is now what the `typed` planner emits automatically
> when the pipeline can exploit it; `SSQL_MODE=parallel` survives only as a
> deprecated alias. The entries below describe that parallel form.

- [x] Same pipeline shape as the serial typed form, with `from`/`where`/`join`/`group-by`/`to csv`/`to table` emitting Stream-based parallel code (typed.ReadCSVParallel + Stream.Where + typed.HashJoinParallel + typed.GroupByParallel).
- [x] Other typed-aware commands (limit, sort, distinct, union, top, cast, update, include/exclude/rename) drop the pipeline to the serial `iter.Seq[T]` form rather than erroring — the planner picks per stage (since v4.40; before that, parallel mode rejected them).
- [x] **Per-shard buffer dump CSV sink (2026-04-27)** — `to csv` in the parallel form emits `Stream.WriteCSVToWriter` (no `Serial()` fan-in). Each shard formats into its own buffer in parallel, dumped in shard order. Wide-output workload (7.25M-row CSV write) went from 0.73× typed-serial to **4.4× faster** with this fix. Trade-off: peak memory ~2× output size.
- [x] **`GroupByParallel` with Sink/Combine/Finalize (2026-04-27)** — `group-by` in the parallel form emits `typed.GroupByParallel`. Each shard accumulates its own partial `map[K]Aggregator`; Combine merges per shard sequentially; Finalize yields rows lazily. Synthesized `<Input>Aggregator` gets a `Merge` method generated from the aggregation specs. **4.0× faster than typed-serial** on the 10M-row × 1 000-group workload (count+sum+avg+min+max).
- **When the planner picks it:** filter-heavy / aggregating / transform-and-write / group-by pipelines. **4.4× faster** on the CSV write workload (1.3 s vs serial 5.7 s; DuckDB 0.7 s — 1.86× ahead). **4.0× faster** on the group-by workload (0.95 s vs 3.80 s; DuckDB 0.39 s — 2.4× ahead). **6.4× faster** for count-only sinks. The planner keeps the **serial** form when the output is too large to buffer in RAM, when input-order output is required, or when `group-by -presorted` is used. See [`research/typed-codegen-proposal.md` §5d](research/typed-codegen-proposal.md#5d-parallel-mode-codegen-ssqlgoparallel) and [`research/typed-groupby-parallel-proposal.md`](research/typed-groupby-parallel-proposal.md).

Phase 1.8 — TSV / Parquet readers (2026-04-28):
- [x] **`typed.ReadDelim` / `ReadDelimParallel` / `WriteDelim`** — fast delimited-text reader with no quoting (default '\t'). Zero-copy field strings via `unsafe.String` (parallel only); SIMD-accelerated split via `bytes.IndexByte`. 18% faster than `ReadCSV` on a 14.6 M-row corpus; memory-bandwidth-bound at ~600 MB/s. Use when data is clean (no embedded quotes / delimiters / newlines).
- [x] **`typed.ReadParquet` / `ReadParquetParallel` / `WriteParquet`** — Parquet input/output via existing Apache Arrow Go dependency. Snappy compression by default. Row groups partition naturally to shards. **`ParquetColumns(...)` is the primary speed lever**: on the 14.6 M-row corpus group-by-with-count benchmark, restricting the read to the single grouped column dropped wall time from 1.51 s to **0.15 s** — a 10× win, within 5× of DuckDB's 0.03 s on the same query. Without column projection Parquet performs about the same as TSV (decompression at ~1–2 GB/s ≈ TSV memory bandwidth ceiling).

Phase 2 — Tier 3a shipped (2026-04-27, on top of Phase 1.7):
- [x] `sort FIELD` and `sort FIELD -desc` (single-field)
- [x] `distinct` (full-row dedup; pointer fields compare by identity)
- [x] `union -file FILE` and `union -file FILE -all` (cross-source
      schema validation — mismatched fields error with a clear message)

Phase 2 — Tier 3b shipped (2026-04-27, Sprint 1+2 of the Tier 3 roadmap):
- [x] `top N -field F` (sort + limit composition)
- [x] Multi-field `sort` via composite comparator (`typed.SortByFunc`)
- [x] `cast -type FIELD TYPE` — string/int/float/bool conversions; emits a
      derived struct with the field's Go type changed
- [x] `update -set FIELD LITERAL` (unconditional, literal values only;
      adds a derived "Updated" struct when new fields are introduced)
- [x] `update` with conditional clauses (`-if F OP V -set ...
      + ...`); first-match-wins as an if/else-if chain

Phase 2 — still deferred:
- [ ] `-if-expr` / `-set-expr` / `-expr` aggregations (expression-lang → Go)
- [ ] Multi-clause joins, `-as` field renames
- [ ] `-rollup` / `-cube` / `-collect`
- [ ] JSONL/Arrow/Parquet typed I/O
- [ ] Window analytic functions
- [ ] Signal processing (FFT, convolve, etc.)
- [ ] `pivot`, `merge`, distributed sources

See [`doc/research/typed-package-proposal.md`](research/typed-package-proposal.md)
for the full design and Phase 2 vision.


## Text lines

```go
type Line struct {
	LineNumber int64  `ssql:"line_number"`
	Line       string `ssql:"line"`
}
func ReadLines(filename string) iter.Seq[Line]
func ReadLinesFromReader(r io.Reader) iter.Seq[Line]
```

The typed form of `ssql from lines`: one `Line` per text line, numbered
from 1. Serial (line boundaries are sequential); the planner inserts no
parallel form. `extract` in a typed pipeline synthesizes its output struct
from the kept fields plus one string per named group.
