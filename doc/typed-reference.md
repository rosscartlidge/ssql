# `ssql/typed` Reference

The `ssql/typed` package provides a high-performance, struct-based data path
alongside the main `ssql.Record` API. Use it when your schema is known at
compile time and the pipeline is hot.

> **Status:** Phase 1 — CSV I/O and core operations (Where, Limit, Skip, Select, HashJoin).
> JSON/JSONL/Arrow readers, outer joins, aggregation, and `time.Time` /
> nullable field types are planned for Phase 1.5.
> See [`doc/research/typed-package-proposal.md`](research/typed-package-proposal.md)
> for the full design.

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
| **Speedup** | **15.1×** | **34.2× less** | **27.2× fewer** |

Both pipelines produce 7.25 M output rows (correctness validated).
A 75-second batch job becomes 5 seconds; 38 GB of allocations becomes 1 GB.

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

## Supported field types (Phase 1)

`string`, `bool`, `int`, `int64`, `float64`.

Empty CSV values become the type's zero value. Other parse errors silently
zero the field in `ReadCSV`; use `ReadCSVSafe` to surface them.

`int32`, `uint64`, `time.Time` (RFC3339), and pointer-to-T (nullable columns)
are planned for Phase 1.5.

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
Outer joins (`LeftJoin`, `RightJoin`, `FullJoin`) are planned for Phase 1.5.

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

Phase 1 (this release):
- [x] CSV I/O with header inference
- [x] `Where`, `Limit`, `Skip`, `Select`
- [x] `HashJoin` (inner)
- [x] Benchmarks demonstrating the gap

Phase 1.5 (planned):
- [ ] `time.Time` (RFC3339), `int32`, `uint64`
- [ ] Pointer-to-T for nullable columns
- [ ] Outer joins (`LeftJoin`, `RightJoin`, `FullJoin`)
- [ ] JSONL reader/writer
- [ ] Streaming aggregation primitives (`Sum`, `Count`, `GroupBy`)

Phase 2 (the moonshot):
- [ ] `ssql generate go -typed` — schema-aware code generation that emits
  calls into this package directly. The library is being designed to be
  the runtime that hand-written and generator-emitted typed Go both target.

See [`doc/research/typed-package-proposal.md`](research/typed-package-proposal.md)
for the full design and Phase 2 vision.
