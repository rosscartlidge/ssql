# `ssql/typed` Codelab

*A hands-on tutorial for the high-performance struct-based data path*

## Table of Contents

### Documentation Navigation
- **[Getting Started Guide](codelab-intro.md)** - Introduction to ssql with `Record`
- **[API Reference](api-reference.md)** - Full ssql library documentation
- **[Typed Reference](typed-reference.md)** - Concise reference for `ssql/typed`
- **[CLI Tutorial](cli-codelab.md)** - Command-line data processing

### Steps
- [When to use it](#when-to-use-it)
- [Setup](#setup)
- [Step 1: Read a typed CSV](#step-1-read-a-typed-csv)
- [Step 2: Filter with Where](#step-2-filter-with-where)
- [Step 3: Inner join two tables](#step-3-inner-join-two-tables)
- [Step 4: Aggregate with GroupBy](#step-4-aggregate-with-groupby)
- [Step 5: Outer join with LeftJoin](#step-5-outer-join-with-leftjoin)
- [Step 6: Write the output](#step-6-write-the-output)
- [Step 7: Read and write JSONL](#step-7-read-and-write-jsonl)
- [Step 8: Measure the speedup yourself](#step-8-measure-the-speedup-yourself)
- [Cheat sheet](#cheat-sheet)
- [Where to next](#where-to-next)

---

## When to use it

ssql ships two complementary APIs for the same pipeline shape:

| Use `ssql.Record` when… | Use `ssql/typed` when… |
|---|---|
| Schema is unknown or dynamic | Schema is known at compile time |
| Prototyping, REPL-style work | Pipeline runs nightly or processes millions of rows |
| You want to handle arbitrary CSV/JSON | You're declaring the input/output types anyway |
| Fields used reflectively | Field access is positional |

The two APIs interoperate, so it's normal to use Record for the
exploration phase and switch to typed for the hot pipelines that
ship to production.

**Performance**, measured on a 10M-row, 3-chained-join workload:

| Implementation | Time | Memory allocated | Allocations |
|---|---:|---:|---:|
| `ssql.Record` | 74.8 s | 37.7 GB | 544 M |
| **`ssql/typed`** | **4.94 s** | **1.10 GB** | **20 M** |
| DuckDB v1.5 CLI | 0.42 s | — | — |

**15× faster, 34× less memory** — and in pure Go with no CGO. You'll
reproduce these numbers yourself in [Step 8](#step-8-measure-the-speedup-yourself).

## Setup

Make a fresh module for the codelab:

```bash
mkdir typed-codelab && cd typed-codelab
go mod init typed-codelab
go get github.com/rosscartlidge/ssql/v4@latest
```

Generate a small employees + departments dataset that every step uses:

```bash
cat > employees.csv <<'EOF'
id,name,dept_id,years,salary
1,Alice,D01,8,95000
2,Bob,D02,3,65000
3,Carol,D01,12,120000
4,David,D03,1,55000
5,Eve,D02,7,80000
6,Frank,D01,5,90000
7,Grace,D03,2,60000
8,Henry,D04,4,70000
EOF

cat > departments.csv <<'EOF'
dept_id,dept_name,location
D01,Engineering,SF
D02,Sales,NYC
D03,Marketing,NYC
EOF
```

Note that `D04` is in employees but missing from departments — we'll
use that to demonstrate inner vs left joins.

## Step 1: Read a typed CSV

Create `step1.go`:

```go
package main

import (
	"fmt"
	"log"

	"github.com/rosscartlidge/ssql/v4/typed"
)

type Employee struct {
	ID     int64
	Name   string
	DeptID string `ssql:"dept_id"`
	Years  int64
	Salary float64
}

func main() {
	for emp := range typed.ReadCSV[Employee]("employees.csv") {
		fmt.Printf("%+v\n", emp)
	}
	_ = log.Println
}
```

Run it:

```
$ go run step1.go
{ID:1 Name:Alice DeptID:D01 Years:8 Salary:95000}
{ID:2 Name:Bob DeptID:D02 Years:3 Salary:65000}
...
```

**What just happened.**

- `Employee` is an ordinary Go struct. Field names match CSV columns
  case-insensitively. The `ssql:"dept_id"` tag is the explicit mapping
  for `DeptID` → CSV column `dept_id`.
- `ReadCSV[Employee]` returns an `iter.Seq[Employee]` — a Go 1.23+
  iterator yielding values one at a time, lazily. The file is opened
  on first iteration and closed when iteration ends.
- Reflection happens **once**, when the header is read — to build the
  per-column decoder. Every subsequent row is decoded via precomputed
  byte-offset writes, no reflection per row.

Supported field types: `string`, `bool`, `int`, `int32`, `int64`,
`uint64`, `float32`, `float64`, `time.Time` (RFC3339), and pointer-to-T
for nullable columns.

## Step 2: Filter with Where

`Where` filters in place — same shape as `ssql.Where` but operates on
your concrete type. Direct field access, no `GetOr`:

```go
package main

import (
	"fmt"

	"github.com/rosscartlidge/ssql/v4/typed"
)

type Employee struct {
	ID     int64
	Name   string
	DeptID string `ssql:"dept_id"`
	Years  int64
	Salary float64
}

func main() {
	employees := typed.ReadCSV[Employee]("employees.csv")

	seniors := typed.Where(func(e Employee) bool {
		return e.Years >= 5
	})(employees)

	for s := range seniors {
		fmt.Printf("%-7s %d years\n", s.Name, s.Years)
	}
}
```

Output:

```
Alice   8 years
Carol   12 years
Eve     7 years
Frank   5 years
```

**What just happened.**

- `typed.Where(pred)` returns a function `iter.Seq[T] -> iter.Seq[T]`.
  Apply it by calling the returned function on the input sequence.
- The predicate accesses `e.Years` directly — direct struct field
  read, not a map lookup. The compiler can inline the predicate and
  often elide the per-row allocation entirely.
- Like `iter.Seq[T]` everywhere in ssql, the iterator is lazy. The
  file isn't fully read; rows stream through one at a time.

You can compose multiple stages by nesting:

```go
youngSeniors := typed.Where(func(e Employee) bool {
    return e.Salary > 70000
})(seniors)
```

`Limit[T](n)`, `Skip[T](n)`, and `Select[T,U](fn)` round out the
single-input operations.

## Step 3: Inner join two tables

This is where the typed API really pays off. Hash joins on Records
allocate a fresh map per output row; on typed structs they're nearly
allocation-free.

```go
package main

import (
	"fmt"

	"github.com/rosscartlidge/ssql/v4/typed"
)

type Employee struct {
	ID     int64
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
	DeptName string
	Location string
}

func main() {
	employees := typed.ReadCSV[Employee]("employees.csv")
	depts := typed.ReadCSV[Department]("departments.csv")

	seniors := typed.Where(func(e Employee) bool {
		return e.Years >= 5
	})(employees)

	joined := typed.HashJoin(seniors, depts,
		func(e Employee) string { return e.DeptID },
		func(d Department) string { return d.DeptID },
		func(e Employee, d Department) Senior {
			return Senior{
				Name: e.Name, Years: e.Years, Salary: e.Salary,
				DeptName: d.DeptName, Location: d.Location,
			}
		})

	for s := range joined {
		fmt.Printf("%-7s %-12s %s (%d years, $%.0f)\n",
			s.Name, s.DeptName, s.Location, s.Years, s.Salary)
	}
}
```

Output:

```
Alice   Engineering  SF (8 years, $95000)
Carol   Engineering  SF (12 years, $120000)
Eve     Sales        NYC (7 years, $80000)
Frank   Engineering  SF (5 years, $90000)
```

**What just happened.**

- `HashJoin` takes both sides plus three functions:
  - `leftKey` — extract the join key from a left row
  - `rightKey` — extract the join key from a right row
  - `merge` — build the output row from a matched pair
- The right side is fully consumed first (build phase) into a
  `map[K]R`, then the left side streams (probe phase). Inner-join
  semantics: a left row with no matching right row is dropped.
- The output type `Senior` is your choice. The merge function builds
  it from any combination of fields from `Employee` and `Department`.
- For multi-column joins, return a tuple/struct from the key
  functions: `func(e Employee) struct{A,B string} { return struct{A,B string}{e.A, e.B} }`.

If your right side has duplicate keys, only the last value per key is
kept. Use `HashJoinMulti` for many-to-many joins. If you know the
right side's row count up front (e.g. it's already a slice),
`HashJoinSized` takes a capacity hint and avoids rehash growth.

## Step 4: Aggregate with GroupBy

`GroupBy` partitions rows by a key function, runs a per-group
accumulator, and emits one output per distinct key. Build the
accumulator with one of the prebuilt helpers (`Counter[T]`,
`NewSummer(fn)`, `NewAverager(fn)`) or implement the
`Aggregator[T, R]` interface for custom aggregations.

```go
package main

import (
	"fmt"

	"github.com/rosscartlidge/ssql/v4/typed"
)

type Employee struct {
	ID     int64
	Name   string
	DeptID string `ssql:"dept_id"`
	Years  int64
	Salary float64
}

type DeptStats struct {
	DeptID    string
	HeadCount int64
	AvgSalary float64
}

func main() {
	employees := typed.ReadCSV[Employee]("employees.csv")

	// Run two passes — one for count, one for avg.
	// (For a single-pass multi-aggregation, define a custom Aggregator;
	// see typed-reference.md.)
	counts := typed.GroupBy(employees,
		func(e Employee) string { return e.DeptID },
		func() typed.Aggregator[Employee, int64] {
			return &typed.Counter[Employee]{}
		},
		func(deptID string, n int64) struct {
			DeptID string
			N      int64
		} {
			return struct {
				DeptID string
				N      int64
			}{deptID, n}
		})

	for r := range counts {
		fmt.Printf("dept %s: %d employees\n", r.DeptID, r.N)
	}
}
```

Output:

```
dept D01: 3 employees
dept D02: 2 employees
dept D03: 2 employees
dept D04: 1 employees
```

**Standalone aggregations** (no grouping) are also available:

```go
total := typed.Sum(employees, func(e Employee) float64 { return e.Salary })
n := typed.Count(employees)
avg, _ := typed.Avg(employees, func(e Employee) float64 { return e.Salary })
```

**Streaming variant.** If your input is already sorted by the group
key, use `GroupByOrdered` — it runs in O(1) memory rather than
buffering all groups:

```go
out := typed.GroupByOrdered(sortedRows, keyFn, newAgg, build)
```

## Step 5: Outer join with LeftJoin

Inner joins drop unmatched rows. The full outer-join family lets you
keep them and observe which side matched:

```go
type Annotated struct {
	Name     string
	DeptName string  // empty string if no match
	Found    bool
}

employees := typed.ReadCSV[Employee]("employees.csv")
depts := typed.ReadCSV[Department]("departments.csv")

annotated := typed.LeftJoin(employees, depts,
	func(e Employee) string { return e.DeptID },
	func(d Department) string { return d.DeptID },
	func(e Employee, d Department, found bool) Annotated {
		return Annotated{Name: e.Name, DeptName: d.DeptName, Found: found}
	})

for a := range annotated {
	if !a.Found {
		fmt.Printf("ORPHAN  %-7s (no department)\n", a.Name)
	} else {
		fmt.Printf("OK      %-7s -> %s\n", a.Name, a.DeptName)
	}
}
```

Henry has `dept_id=D04` but no matching department — `LeftJoin`
emits him with `Found=false` and a zero-valued `Department`. With
`HashJoin` he'd be silently dropped.

The full family:

| Function | Behavior |
|---|---|
| `HashJoin` | Inner: drop unmatched left rows |
| `HashJoinMulti` | Inner with many-to-many right side |
| `HashJoinSized` | Inner with build-side capacity hint |
| `LeftJoin` | Keep every left row; merge gets `(l, r, found bool)` |
| `RightJoin` | Keep every right row; symmetric mirror |
| `FullJoin` | Keep both sides; merge gets `(l, r, leftFound, rightFound bool)` |

## Step 6: Write the output

Symmetric API. Pass the output sequence and a filename:

```go
if err := typed.WriteCSV(joined, "seniors.csv"); err != nil {
	log.Fatal(err)
}
```

The header row comes from struct field names (or `ssql:"name"` tags).
All exported fields are written in declaration order. Unexported
fields and `ssql:"-"`-tagged fields are skipped.

`WriteCSVToWriter` takes any `io.Writer` if you want to write to
stdout, a buffer, or a network connection.

## Step 7: Read and write JSONL

For newline-delimited JSON (one object per line — common for log
streams and Snowflake exports), the parallel API is `ReadJSONL` /
`WriteJSONL`:

```go
type Event struct {
	Timestamp string  `json:"ts"`
	UserID    int64   `json:"user_id"`
	Action    string  `json:"action"`
	Score     float64 `json:"score"`
}

events := typed.ReadJSONL[Event]("events.jsonl")
filtered := typed.Where(func(e Event) bool { return e.Score > 0.5 })(events)
typed.WriteJSONL(filtered, "interesting.jsonl")
```

Field tags use `json:"name"` (standard `encoding/json` convention).
The reader uses `encoding/json` with `bufio.Scanner` under the hood;
for very high JSONL throughput see the performance notes.

## Step 8: Measure the speedup yourself

Clone the ssql repo and run the benchmarks against the same workload
described in the docs:

```bash
git clone https://github.com/rosscartlidge/ssql && cd ssql

# Quick comparison (~1 minute, 1M rows × 1 join)
go test -bench=End2End -benchtime=3x -run=^$ ./typed/...

# Full headline workload (~2 minutes, 10M rows × 3 chained joins —
# generates 600 MB of CSV under os.TempDir on first run)
go test -bench=Scale -benchtime=1x -run=^$ -timeout=30m ./typed/...
```

Expected output (single-threaded, on modern x86):

```
BenchmarkScaleRecord3Join-24    1   74.8 s    37.7 GB     544 M allocs
BenchmarkScaleTyped3Join-24     1    4.94 s    1.10 GB    20.0 M allocs
```

If you have DuckDB installed (`~/.local/bin/duckdb` or anywhere on
PATH), the same workload as a SQL query:

```bash
go test -bench=DuckDB -benchtime=1x -run=^$ -timeout=10m ./typed/...
# BenchmarkScaleDuckDB3Join-24    1    0.42 s
```

Three-way ratio: ssql.Record → ssql/typed = **15×**. ssql/typed →
DuckDB = ~12×. So `ssql/typed` is genuinely an order of magnitude
faster than `ssql.Record`, and within an order of magnitude of DuckDB
in pure Go with no native dependencies.

## Cheat sheet

```go
import "github.com/rosscartlidge/ssql/v4/typed"

// I/O
typed.ReadCSV[T](filename)               // iter.Seq[T] — lossy on parse error
typed.ReadCSVSafe[T](filename)           // iter.Seq2[T, error]
typed.ReadCSV[T](filename, typed.Strict()) // reject schema mismatch
typed.WriteCSV(seq, filename)
typed.ReadJSONL[T](filename)
typed.WriteJSONL(seq, filename)

// Operations (single-input)
typed.Where(pred)(seq)
typed.Limit[T](n)(seq)
typed.Skip[T](n)(seq)
typed.Select(fn)(seq)

// Joins
typed.HashJoin(left, right, leftKey, rightKey, merge)
typed.HashJoinSized(left, right, sizeHint, leftKey, rightKey, merge)
typed.HashJoinMulti(left, right, leftKey, rightKey, merge)  // many-to-many
typed.LeftJoin(left, right, leftKey, rightKey, mergeWithFound)
typed.RightJoin(left, right, leftKey, rightKey, mergeWithFound)
typed.FullJoin(left, right, leftKey, rightKey, mergeWithBothFlags)

// Aggregation (standalone)
typed.Count(seq)                         // int64
typed.Sum(seq, fn)                       // numeric
typed.Avg(seq, fn)                       // (float64, n)
typed.Min(seq, fn)                       // (T, ok)
typed.Max(seq, fn)                       // (T, ok)

// Aggregation (per-group)
typed.GroupBy(seq, keyFn, newAgg, build)         // buffered
typed.GroupByOrdered(seq, keyFn, newAgg, build)  // streaming

// Aggregator constructors
typed.Counter[T]{}
typed.NewSummer(fn)
typed.NewAverager(fn)
```

### Field tags

```go
type Row struct {
    Name     string                          // matches "Name", "NAME", "name"
    DeptID   string  `ssql:"dept_id"`        // matches "dept_id"
    Internal string  `ssql:"-"`              // skipped
}
```

`ssql:"name"` is preferred. `csv:"name"` is also accepted as a
fallback for ecosystem compatibility.

## Where to next

- **[Typed Reference](typed-reference.md)** — concise function-by-function reference
- **[Performance Notes](research/typed-performance-notes.md)** — known optimization opportunities (and a writeup of one that didn't pay off)
- **[Concurrency Proposal](research/typed-concurrency-proposal.md)** — design sketch for closing the remaining gap to DuckDB via opt-in `Stream[T]`
- **[Phase 2 vision](research/typed-package-proposal.md#5b-what-phase-2-delivers--and-why-its-different)** — `ssql generate go -typed`: shell-pipeline → typed Go program in one flag
- **[Side-by-side example](../examples/typed_pipeline)** — runnable Record vs typed comparison that prints the speedup live
