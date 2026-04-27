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

DuckDB is ~12× faster than `ssql/typed`. Most of that gap comes from
**columnar storage with vectorized SIMD execution** — fundamental
architectural advantages that no row-based runtime can match without
rewriting around Apache Arrow or similar.

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
Empty CSV values become the zero value (or `nil` for pointer types).

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

### Writing

```go
func WriteCSV[T any](seq iter.Seq[T], filename string) error
func WriteCSVToWriter[T any](seq iter.Seq[T], w io.Writer) error
```

The header row is taken from struct field names (or tags). All exported fields
are written in declaration order. Unexported fields and `ssql:"-"`-tagged
fields are skipped.

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
```

Prebuilt aggregators: `Counter[T]`, `NewSummer(fn)`, `NewAverager(fn)`.
Custom accumulators implement the `Aggregator[T, R]` interface.

Use `GroupBy` for unordered input (buffers all groups in a map). Use
`GroupByOrdered` when the input is pre-sorted by key — it streams in
constant memory.

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
- [x] `SSQLGO=typed ssql generate go` — schema-aware code generation that
  emits calls into this package directly. Tier 1 covers
  `from FILE.csv` (header sampled at generation time, struct types
  auto-derived), `where -if FIELD OP VALUE` (literal operators only),
  `join FILE.csv -using FIELD` (single-key + process-substitution),
  `to csv`, and `to table`. Other commands abort with a clear error.
  See [`research/typed-codegen-proposal.md`](research/typed-codegen-proposal.md).

```bash
# Same prototype pipeline you'd run interactively...
SSQLGO=typed ssql from employees.csv \
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
| Record codegen (`SSQLGO=1`) | 2.69 s | 910 MB |
| **Typed codegen (`SSQLGO=typed`)** | **0.77 s** | **8.7 MB** |
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

Phase 2 — `SSQLGO=parallel` codegen shipped (2026-04-27):
- [x] Same pipeline shape as `SSQLGO=typed`, with `from`/`where`/`join`/`to csv`/`to table` emitting Stream-based parallel code (typed.ReadCSVParallel + Stream.Where + typed.HashJoinParallel + Serial() sink).
- [x] Other typed-aware commands (limit, group-by, sort, distinct, etc.) emit a clear error suggesting `SSQLGO=typed` instead.
- **When to use it:** filter-heavy / aggregating pipelines. **2× faster** when output rows ≪ input rows; **6.4× faster** for count-only sinks. **slower** for transform-and-write-everything pipelines (Serial() fan-in cost > parallel-filter savings). See [`research/typed-codegen-proposal.md` §5d](research/typed-codegen-proposal.md#5d-parallel-mode-codegen-ssqlgoparallel) for the workload-vs-mode table.

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
