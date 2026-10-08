# `ssql/typed` Reference

The `ssql/typed` package provides a high-performance, struct-based data path
alongside the main `ssql.Record` API. Use it when your schema is known at
compile time and the pipeline is hot.

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

On a 10 M-row, three-chained-join workload `ssql/typed` runs in 4.94 s
where `ssql.Record` takes 74.8 s — 15× faster, 34× less memory — and
DuckDB does the same joins in 0.42 s. On a scan-and-aggregate cube over a
14.6 M-row parquet file the picture reverses: the typed program
`generate go` emits runs in 0.28 s against DuckDB's 0.96 s, because the
optimiser prunes the read to the columns used and the planner runs the
read and the group-by in parallel. Every number, the machine it ran on,
and the command that reproduces it is in
[Performance, Measured](performance.md).

Where `ssql/typed` competes with a columnar engine:

- **Zero native dependency** — pure Go, no CGO, no shared library, no
  `~/.local/bin/duckdb` install step. Drops into any Go program with
  `go get`.
- **Small** — the data path is a few files of plain Go, auditable in an
  afternoon.
- **Streaming, not materializing** — pipelines are `iter.Seq[T]` all the
  way down. DuckDB materializes intermediate join results.
- **Composes with the rest of Go** — joining streamed data against a
  `chan T` or a custom reader is one line. DuckDB requires bridging
  through SQL or a connection.

For pure throughput on static join-heavy datasets, DuckDB wins. For
embedded Go pipelines that need a typed, streaming, dependency-free fast
path, `ssql/typed` is the right tool.

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
An empty CSV/TSV cell is `""` for a `string` field and `nil` for a pointer field (`*int64`, `*string`, …). For any other field type it is a fatal `*typed.ReadError` — `column "Age": "" is not int64` — because a struct cannot hold absence, and a zero would silently disagree with the `Record` API, where an empty numeric or boolean cell is an *absent* field. Remedies: declare the field as a pointer type; in generated code, `-type COL string` on the `from` stage keeps the cell as text; or `fill` the data first. The check is a branch the decoder already took (`if s == ""`), so it costs nothing.

A non-empty value that does not parse as its field's type is **fatal** in the
lossy readers (`ReadCSV`, `ReadCSVParallel`, `ReadDelim*`, `ReadJSONL*`): they
panic with a `*typed.ReadError` naming the reader, the file, the row (CSV/TSV:
1-based data row; JSONL: physical line), the column and the value —
`typed.ReadCSVParallel late.csv: row 1002: column "v": "1.5" is not int64`. A
file that cannot be opened, a malformed row and a strict-mode header mismatch
are the same error. Programs from `ssql generate go` recover it into `Error: …`
and exit status 1, including when it starts inside a parallel shard. Use the
`*Safe` readers to receive these as error values instead.

> **Note on nullables**: pointer-to-T columns allocate one heap value per
> non-empty cell. For hot paths with many nullables, consider an explicit
> `Valid bool` field (sql.NullInt64-style) instead.

## API

### Reading

```go
func ReadCSV[T any](filename string, opts ...CSVOption) iter.Seq[T]
func ReadCSVFromReader[T any](r io.Reader, opts ...CSVOption) iter.Seq[T]
func ReadCSVSafe[T any](filename string, opts ...CSVOption) iter.Seq2[T, error]
func ReadCSVSafeFromReader[T any](r io.Reader, opts ...CSVOption) iter.Seq2[T, error]
func ReadCSVParallel[T any](filename string, n int) Stream[T]   // n shards, 0 = GOMAXPROCS; see "Stream[T]" below

func Strict() CSVOption   // the one CSV option
```

By default a CSV column with no matching struct field is dropped and a
struct field with no matching column stays at its zero value. `Strict()`
refuses both; pointer fields are always optional. `ReadCSVParallel` parses the header once and gives each shard a
contiguous range of lines; it holds the file in memory and assumes no
quoted field contains a newline (files `WriteCSV` produces satisfy that;
use `ReadCSV` for RFC-4180 parsing of such files).

`ReadCSV` is the fast variant and it **fails fast**: a missing file, a
malformed row or a cell that does not fit its field's type panics with a
`*typed.ReadError` (see "Supported field types" above). `ReadCSVSafe` returns an
`iter.Seq2[T, error]` so the consumer can choose to halt, log, or skip on each
error. Mirrors the `ssql.ReadCSV` / `ssql.ReadCSVSafe` split (`*ssql.CellError`
there).

```go
type ReadError struct {
    Op     string // "typed.ReadCSV", "typed.ReadJSONLParallel", …
    Source string // file name; "" for an io.Reader
    Row    int64  // CSV/Delim: 1-based data row (0 = not row-specific)
    Line   int64  // JSONL: 1-based physical line
    Err    error
}
func (e *ReadError) Error() string
```

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

Tables:

```go
func WriteTableToWriter[T any](seq iter.Seq[T], w io.Writer, maxWidth ...int) error   // width-aligned; numbers right-justified
func WriteTableSelectedToWriter[T any](seq iter.Seq[T], w io.Writer, cols []TableColumn[T], maxWidth ...int) error
```

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
func WithDelim(b byte) DelimOption
func DelimStrict() DelimOption   // mirrors Strict for CSV
```

Same struct-tag mapping as `ReadCSV`; same `Stream[T]` per-shard buffer
sink; same fail-fast `*ReadError` contract. **Differs from `ReadCSV` only in that fields are split on a
single byte with no quote/escape handling — embedded delimiters or
newlines produce wrong rows.** Use this when your data is clean
delimited text; use `ReadCSV` for RFC-4180-correct parsing.

The parallel reader (`ReadDelimParallel`) does zero-copy field
strings (each row's strings alias into the file's mmap'd bytes via
`unsafe.String`) and uses `bytes.IndexByte` for SIMD-accelerated
field splitting. This makes the parser cost competitive with the
memory-bandwidth ceiling.

```go
func ReadDelimSafeFromReader[T any](r io.Reader, opts ...DelimOption) iter.Seq2[T, error]
```

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

func ParquetStrict() ParquetOption              // reject schema mismatches
func ParquetColumns(names ...string) ParquetOption  // read only the listed columns
```

Snappy compression by default. Reads use the existing
`github.com/apache/arrow/go/v18/parquet` dependency that ssql
already imports for Record-mode Parquet.

**`ParquetColumns` is the primary lever.** Restricting a 14.6 M-row
group-by to its one key column made it 10× faster
([Performance, Measured](performance.md) §2 has the run). For wide tables it's the difference
between Parquet feeling fast and feeling like CSV-with-extra-steps.

The parallel reader assigns Parquet row groups to shards
round-robin; each shard owns its own `pqarrow.FileReader`. Peak
memory is roughly `nShards × max-row-group-size`. If the file has
fewer row groups than `n`, `n` is reduced to match — Parquet
doesn't allow splitting within a row group without re-decoding it.

Write options (shared with the record package):

```go
func WithCompression(name string) ParquetWriteOption   // snappy (default), gzip, zstd, none
func WithRowGroupSize(n int) ParquetWriteOption        // default 1_000_000; one row group is one ReadParquetParallel shard
```

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

```go
func TakeLast[T any](n int) func(iter.Seq[T]) iter.Seq[T]   // the last n, a ring buffer; a barrier
```

### Sorting, distinct, concatenation

```go
func SortBy[T any, K Ordered](key func(T) K) func(iter.Seq[T]) iter.Seq[T]        // ascending by key; not stable
func SortByDesc[T any, K Ordered](key func(T) K) func(iter.Seq[T]) iter.Seq[T]
func SortByStable[T any, K Ordered](key func(T) K) func(iter.Seq[T]) iter.Seq[T]  // keeps input order among equal keys
func SortByFunc[T any](cmp func(a, b T) int) func(iter.Seq[T]) iter.Seq[T]        // any comparator: multi-key sorts

func Distinct[T any, K comparable](key func(T) K) func(iter.Seq[T]) iter.Seq[T]   // first occurrence of each key; O(distinct keys) memory
func Concat[T any](seqs ...iter.Seq[T]) iter.Seq[T]                               // one after another, streaming
func Union[T any, K comparable](key func(T) K, seqs ...iter.Seq[T]) iter.Seq[T]   // Concat then Distinct, one pass
```

The sorts materialise their input (O(N) memory) and are barriers: the
planner emits them in the serial form and puts a `Serial()` boundary in
front when the upstream is a `Stream`. For whole-row distinctness pass
the row itself as the key (`T` must be comparable); for several columns
return a small struct. `sort`, `distinct` and `union` in a generated
program are these functions; `top` is `TopBy` below, not a sort.

### Stream[T]: the parallel form

```go
type Stream[T any] struct{ /* n shards, each an iter.Seq[T] */ }

func Parallel[T any](in iter.Seq[T], n int) Stream[T]           // shard any iter.Seq: a distributor goroutine, one channel transit PER ROW (slow; see ParallelBatched)
func ParallelBatched[T any](in iter.Seq[T], n int) Stream[T]    // shard any iter.Seq in batches of 1024 rows: streams, one transit per batch (a generated library's entry point)
func ParallelFromSlice[T any](data []T, n int) Stream[T]        // shard a slice into n contiguous chunks, no channel transit
func FromRecords[R, T any](src iter.Seq[R], conv func(R) T) iter.Seq[T]              // the Record → typed re-entry boundary
func FromRecordsParallel[R, T any](src iter.Seq[R], conv func(R) T, n int) Stream[T]

func (s Stream[T]) Where(pred func(T) bool) Stream[T]           // filters every shard independently; pred must be goroutine-safe
func StreamSelect[T, U any](s Stream[T], fn func(T) U) Stream[U] // Select over a Stream (a free function: methods cannot add type parameters)
func DistinctParallel[T any, K comparable](in Stream[T], key func(T) K) iter.Seq[T]  // per-shard dedupe, then a serial merge
func (s Stream[T]) Serial() iter.Seq[T]                         // fan the shards back into one sequence; unordered
func (s Stream[T]) SerialCount() int64                          // drain every shard concurrently and count, no fan-in channel
func (s Stream[T]) Shards() int
```

A `Stream[T]` is a pipeline of `T` partitioned across `n` worker shards.
It is a separate type from `iter.Seq[T]` because its contract differs:
output order is shard-concatenation order, not input order, and each
stage runs in a goroutine per shard. The readers produce one directly
(`ReadCSVParallel`, `ReadDelimParallel`, `ReadParquetParallel`,
`ReadJSONLParallel`); `ParallelBatched` shards an existing sequence
(`Parallel` is its per-row ancestor, kept for reference). The
parallel joins, set operations, top-k and `GroupByParallel` take a
`Stream` on the left and return a `Stream` or a merged `iter.Seq`; the
`Stream` sinks (`WriteCSV`, `WriteDelim`, `WriteParquet` methods) format
each shard into its own buffer and dump the buffers in shard order,
skipping the per-row fan-in. `Serial()` is the boundary back to the
serial API: a `sort`, `distinct` or `to table` downstream forces it, and
the codegen planner inserts it where needed. Never put a channel between
every row and its consumer — that is what the shard buffers avoid.

### Set operations

```go
func Except[L comparable, R any, K comparable](right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, all bool) func(iter.Seq[L]) iter.Seq[L]
func Intersect[L comparable, R any, K comparable](right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, all bool) func(iter.Seq[L]) iter.Seq[L]
func ExceptAll[T comparable](right iter.Seq[T]) func(iter.Seq[T]) iter.Seq[T]      // SQL EXCEPT ALL: each right row cancels one left row
func IntersectAll[T comparable](right iter.Seq[T]) func(iter.Seq[T]) iter.Seq[T]   // SQL INTERSECT ALL
func ExceptParallel[L any, R any, K comparable](left Stream[L], right iter.Seq[R], leftKey func(L) K, rightKey func(R) K) Stream[L]
func IntersectParallel[L any, R any, K comparable](left Stream[L], right iter.Seq[R], leftKey func(L) K, rightKey func(R) K) Stream[L]
```

`Except` keeps the left rows whose key is absent from the right (an anti-join
with field keys, SQL `EXCEPT` with identity keys); `Intersect` the rows whose
key is present (a semi-join). `all=false` yields each distinct left row once,
so `L` must be comparable, which every generated row type is; `all=true` keeps
duplicates. The parallel forms are a per-shard probe of the shared set (the
`all=true` semantics); the distinct form composes `DistinctParallel` on top.
Behind the `except` and `intersect` commands.

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
func HashJoinSized[L, R, O any, K comparable](left iter.Seq[L], right iter.Seq[R], rightSizeHint int, leftKey func(L) K, rightKey func(R) K, merge func(L, R) O) iter.Seq[O]  // pre-size the build map when the right side's count is known
func HashJoinMulti[L, R, O any, K comparable](...) iter.Seq[O]
func LeftJoin[L, R, O any, K comparable](
    left, right ..., merge func(L, R, found bool) O,
) iter.Seq[O]
func RightJoin[L, R, O any, K comparable](...) iter.Seq[O]
func FullJoin[L, R, O any, K comparable](
    left, right ..., merge func(L, R, leftFound, rightFound bool) O,
) iter.Seq[O]
```

The parallel forms take a `Stream[L]` on the left and probe one shared map:

```go
func HashJoinParallel[L, R, O any, K comparable](left Stream[L], right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, merge func(L, R) O) Stream[O]
func HashJoinMultiParallel[L, R, O any, K comparable](left Stream[L], right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, merge func(L, R) O) Stream[O]
```

The CLI's `join` emits the **Multi** forms: a right side with a repeated key
(orders per customer) is the ordinary case, and the single-match form would
keep only the last match per key.

### ASOF join

```go
type AsofOptions struct {
    Forward   bool    // the nearest at or after instead of at or before
    Strict    bool    // never an equal time
    Tolerance float64 // when > 0, the largest distance that matches, in T's unit
}
func AsofJoin[L, R, O any, K comparable, T int64 | float64](
    right iter.Seq[R], leftKey func(L) K, rightKey func(R) K,
    leftTime func(L) T, rightTime func(R) T, opts AsofOptions, merge func(L, R) O,
) func(iter.Seq[L]) iter.Seq[O]
func AsofJoinParallel[L, R, O any, K comparable, T int64 | float64](left Stream[L], right iter.Seq[R], ...) Stream[O]
```

Each left row takes the right row that is current *as of* its time: among the
right rows with the same key, the nearest at or before (or after) the left
time, ties taking the last in input order. `T` is the ordered axis: `int64`
nanoseconds for a time field (`l.Ts.UnixNano()`) or the number itself. Inner
semantics; the right side is indexed once, the left streams in input order.
Behind `join -asof`.

## JSONL and JSON array I/O

For newline-delimited JSON (`one object per line`), use the JSONL pair;
for a JSON **array** document (`[ {…}, {…} ]`, the shape an API export or
`to json` writes), the `ReadJSON` twins:

```go
func ReadJSONL[T any](filename string) iter.Seq[T]
func ReadJSONLFromReader[T any](rd io.Reader) iter.Seq[T]
func ReadJSONLSafe[T any](filename string) iter.Seq2[T, error]
func ReadJSONLSafeFromReader[T any](r io.Reader) iter.Seq2[T, error]
func ReadJSONLParallel[T any](filename string, n int) Stream[T]
func ReadJSON[T any](filename string) iter.Seq[T]              // a JSON array file
func ReadJSONFromReader[T any](rd io.Reader) iter.Seq[T]
func ReadJSONParallel[T any](filename string, n int) Stream[T]
func WriteJSONL[T any](seq iter.Seq[T], filename string) error
func WriteJSONLToWriter[T any](seq iter.Seq[T], w io.Writer) error
```

Field mapping follows standard `json:"name"` struct tags; an `ssql` tag
names the key when there is no `json` tag.

**Header lines.** Both readers skip a leading `{"_schema": …}` line (the
header `tee` and every ssql stage write), so a tee'd file reads as its
rows. **Errors.** A line that is not a JSON object, or a value that does not
fit its field (`{"v":1.5}` into `int64`), is a fatal `*ReadError` naming the
physical line in `ReadJSONL` / `ReadJSONLParallel`; `ReadJSONLSafe` yields it.
In a generated program the fix is `-type v float` on the `from jsonl` stage.
**Decoding is positional:** the type is reflected over once into a
key → field plan that reuses the CSV reader's field decoders, and each line
is walked once (about 3.6× the throughput of `encoding/json`). Slice, map
and nested-struct fields fall back to `encoding/json`. A string field accepts
any JSON value as its raw text, as the CLI does — and that is how a `json`
wire-type field (a nested array or object) is typed: a `string` holding
the text, written back as JSON at the typed→Record boundary
(`ssql.JSONOrNull`), so a typed program passes nested values through
unchanged (DFC144 Level 0). **Codegen.** `from jsonl
FILE` in typed mode infers the row struct from the file (the `_schema`
header when present, else a sample of lines) and emits `ReadJSONL`, or
`ReadJSONLParallel` — mmap, a newline index, a shard per run of lines, the
CSV twin's shape — when a downstream stage accepts a `Stream`.

**JSON array files.** `ReadJSON` streams the elements of an array
document with `encoding/json`'s Decoder (the file is not loaded whole)
and decodes each with the same positional plan, so a nested value lands
in a `string` field as its raw text. A document that is not an array,
or an element that is not an object or does not fit, is a fatal
`*ReadError` whose `Row` is the 1-based element index. There is no
newline index to shard on, so `ReadJSONParallel` has one goroutine scan
the document into elements and hand them to `n` decoding shards in
batches (`ParallelBatched` applied before the decode); a consumer that
stops early releases the scanner. `from json FILE.json` in typed mode
samples the first elements for the row struct (key order of the first
element that has each key) and emits these readers (2026-10-09; array
files took the record path before).

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

## Aggregation

```go
func Count[T any](seq iter.Seq[T]) int64
func Sum[T any, N Number](seq iter.Seq[T], fn func(T) N) N
func Min[T any, N Ordered](seq iter.Seq[T], fn func(T) N) (N, bool)
func Max[T any, N Ordered](seq iter.Seq[T], fn func(T) N) (N, bool)
func Avg[T any, N Number](seq iter.Seq[T], fn func(T) N) (float64, int64)
```

Standalone aggregates over an entire stream (the `Counter`, `Summer` and
`Averager` accumulators expose `Result()` for a finished group). For per-group
results:

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

```go
type Aggregator[T, R any] interface { Add(T); Result() R }
type ParallelAggregator[T, R any] interface { Aggregator[T, R]; Merge(other Aggregator[T, R]) }
type AggFunc[T, R any] func() Aggregator[T, R]                   // a fresh accumulator per group
type ParallelAggFunc[T, R any] func() ParallelAggregator[T, R]

type Counter[T any] struct{ N int64 }
func (c *Counter[T]) Add(T)
func (c *Counter[T]) Merge(other Aggregator[T, int64])
func (c *Counter[T]) Result() int64
type Summer[T any, N Number] struct{ /* private */ }
func (s *Summer[T, N]) Add(v T)
func (s *Summer[T, N]) Merge(other Aggregator[T, N])
func (s *Summer[T, N]) Result() N
type Averager[T any, N Number] struct{ /* private */ }
func (a *Averager[T, N]) Add(v T)
func (a *Averager[T, N]) Merge(other Aggregator[T, float64])
func (a *Averager[T, N]) Result() float64

func NewSummer[T any, N Number](fn func(T) N) AggFunc[T, N]
func NewAverager[T any, N Number](fn func(T) N) AggFunc[T, float64]
func NewCounter[T any]() ParallelAggFunc[T, int64]
func NewParallelSummer[T any, N Number](fn func(T) N) ParallelAggFunc[T, N]
func NewParallelAverager[T any, N Number](fn func(T) N) ParallelAggFunc[T, float64]
```

The prebuilt accumulators all implement `Merge`, so they serve both
`GroupBy` (through `NewSummer` / `NewAverager`, or `&Counter[T]{}`) and
`GroupByParallel` (through the `New*` constructors typed as
`ParallelAggFunc`). A custom accumulator implements `Aggregator[T, R]`;
add a `Merge` method to use it in the parallel form.

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
costs what the plain group-by costs (about 1.7 s unoptimised on the
14.6 M-row file, 0.28 s with the read pruned — [Performance,
Measured](performance.md) §1; the record-mode `Rollup` that re-keys
every row per set took 36 s). Generated by
`SSQL_MODE=typed … group-by … -cube`; aggregations without a Merge
(`-collect`, expressions) fall back to record codegen.

### Window functions

```go
func Window[T, O any](clauses []WindowClause[T], build func(T, []any) O) func(iter.Seq[T]) iter.Seq[O]

type WindowClause[T any] struct {
    Partition func(T) string   // partition key ("" = the whole input)
    HasOrder  bool
    Compare   func(a, b T) int // order comparator (0 = peers); nil when !HasOrder
    Desc      bool             // the single order field is descending (RANGE direction)
    RangeKey  func(T) float64  // the order field as a number / Unix seconds, for RANGE frames
    Frame     WindowFrame
    Specs     []WindowSpec[T]
}
type WindowSpec[T any] struct {
    Kind    WindowKind
    N       int                      // NTILE's n, LAG/LEAD's offset, NTH_VALUE's n
    Default any                      // LAG/LEAD default (nil = absent)
    Num     func(T) (float64, bool)  // the numeric field for sum/avg
    Val     func(T) (any, bool)      // any field for lag, lead, first, last, nth, count, min, max
}
type WindowFrame struct {
    Preceding, Following           int      // rows; -1 = unbounded
    Range                          bool
    RangePreceding, RangeFollowing float64  // -1 = unbounded
}
const (
	WRowNumber WindowKind = iota
	WRank
	WDenseRank
	WNtile
	WPercentRank
	WCumeDist
	WLag
	WLead
	WFirst
	WLast
	WNth
	WSum
	WAvg
	WCount
	WCountField
	WMin
	WMax
)
```

The typed form of the `window` command: one clause per `PARTITION BY /
ORDER BY / frame`, any number of functions per clause, one output row per
input row. `build` receives the results in clause order then spec order
and assembles the output struct, which a generated program synthesises
from the input fields plus one per function. It is a barrier (the
partition must be complete before a rank is known), so the planner emits
the serial form. Semantics match the `Record` API's `Window` and the SQL
translation exactly — the equivalence gate holds all lanes to the same
output.

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

## What falls back to Record

A `generate go` pipeline in typed mode compiles to this package wherever it
can. Stages that have no typed form yet run on `Record` — the planner
inserts the `FromRecords` boundary, or refuses loudly when it cannot — and
`generate go -explain` names the stage and the reason:

- `-collect` (a slice-typed result field)
- multi-clause joins and `-as` renames; `join -type left|right|full` and
  `join -asof -type left` (a struct cannot hold an absent right-hand field)
- `sort -spill` / `group-by -spill` (the out-of-core sort is record-only)
- `pivot`, `merge`, and the signal-processing commands (`fft`, `convolve`,
  `spectrogram`, …)

History and design: [DFC141](research/dfc141_typed_roadmap_history.md)
records what shipped when; the design is in
[typed-package-proposal.md](research/typed-package-proposal.md) and
[typed-codegen-proposal.md](research/typed-codegen-proposal.md).
