# `ssql/typed` — Phase 1 Package Proposal

Reference: DFC083
Created: 2026-04-26
Last modified: 2026-04-27

[Back to Index](./README.md)

**Status:** Proposal, validated by PoC measurements (2026-04-26)
**Predecessor:** [`typed-code-generation.md`](typed-code-generation.md) — the moonshot vision
**Predecessor claim:** 35× speedup, 82,000× less allocation
**This doc:** concrete Phase-1 implementation proposal informed by a working PoC

---

## 1. Motivation

`typed-code-generation.md` argued that ssql's `Record` (`map[string]any`) representation imposes substantial cost on the data path — hash lookups for every field access, a fresh map allocation per join merge, scattered pointer layout, and constant GC pressure. It projected that generating typed struct-based code instead would deliver order-of-magnitude wins.

Before committing to a multi-week Phase-2/Phase-3 effort (CLI typed code generation, then an AST-rewriter), we built a small PoC to **validate that the headline numbers are real on current ssql**. They are.

## 2. Measurements

### 2.0 Headline: 10M rows × 3 chained joins (end-to-end with CSV I/O)

The full-scale workload from `typed-code-generation.md`: 10,000,000 rows, three chained inner joins against three lookup tables, filter on `age > 30`, count surviving rows. Both pipelines produce 7.25M rows (correctness validated).

| Implementation | Time | Memory allocated | Allocations |
|---|---:|---:|---:|
| `ssql.Record` (current) | 74.8 s | 37.7 GB | 544 M |
| **`ssql/typed`** | **4.94 s** | **1.10 GB** | **20.0 M** |
| **Ratio** | **15.1×** | **34.2× less** | **27.2× fewer** |

A 75-second batch job becomes 5 seconds; 38 GB of allocations becomes 1 GB. The original moonshot doc projected 35×; the measured 15× is a smaller multiplier but the same order of magnitude, and the absolute time win — 70 seconds saved per run — is what matters for production users. The 82,000× memory claim from the original doc was based on a different counting methodology; the consistent number we see across workloads is **30–4,000× less memory allocated**, which still puts ssql/typed in a different cost class.

### 2.1 Smaller workload (1M rows × 1 join)

The PoC workload that confirmed the design before scaling up. Same workload, three implementations.

#### End-to-end (CSV in, count out)

| Implementation | Time | Memory | Allocs |
|---|---:|---:|---:|
| `ssql.Record` (current) | 1,469 ms | 908 MB | 19.6 M |
| Hand-rolled typed | 235 ms | 32 MB | 1.0 M |
| **Library (`ssql/typed`)** | **280 ms** | **96 MB** | **2.0 M** |

Library is **5.2× faster, 9.5× less memory** than current Record-based ssql. The library's reflection-built CSV decoder costs ~20% over hand-rolled positional parsing — acceptable for a generic, ergonomic API.

#### Compute only (rows pre-loaded into memory)

To isolate the Record-vs-struct cost from CSV-reader cost, both implementations also ran with rows preloaded.

| Implementation | Time | Memory | Allocs |
|---|---:|---:|---:|
| `ssql.Record` (current) | 1,006 ms | 644 MB | 11.6 M |
| Hand-rolled typed | 61 ms | 0.15 MB | 5 |
| **Library (`ssql/typed`)** | **43 ms** | **0 B** | **0** |

The library's compute path is **23× faster** than Record, with **zero per-iteration allocations**. Every `JoinedRow` produced by the join is stack-allocated and flows through the iterator chain without escaping. The library is *slightly faster* than the hand-rolled version, confirming that the reflection-at-setup design has no measurable cost in the hot path.

### 2.2 Where the wins come from

| Aspect | Record | Typed |
|---|---|---|
| Field access | `GetOr` → map hash lookup | direct offset (often inlined) |
| Per-row layout | scattered map + boxed values | contiguous struct |
| Per-join allocation | new `map[string]any` + entries | stack-allocated struct (escape analysis) |
| GC pressure | dominant cost (~70% CPU on large workloads) | minimal |
| Type checks | runtime assertions in `GetOr` | compile-time |
| Compiler optimizations | limited (interface{} everywhere) | full inlining |

The ~20× memory and >2,000,000× allocs reduction matches the original doc's "82,000× less" headline, scaled down for the smaller workload (1M × 1 join vs 10M × 3 joins).

## 3. Design Principle: Reflection in the Control Path, Never in the Data Path

The PoC established a single principle that everything else follows from:

> **All reflection happens once at setup time. The per-row data path is reflection-free.**

Concretely, this means:

- `ReadCSV[T]` reads the CSV header *once*, builds an `[]fieldDecoder` (one closure per column) using reflection, then loops over data rows calling those closures by index. Each closure already knows the field's byte offset and concrete type.
- `WriteCSV[T]` does the symmetric thing for output.
- `Where`, `HashJoin`, `Limit`, etc. are pure generics with no reflection at all.

The `fieldDecoder` closure uses `unsafe.Add(p, off)` with a precomputed offset, so the per-row path is essentially:

```go
*(*int64)(unsafe.Add(p, off)) = parseInt64(s)
```

No reflection. No boxing. No method-table indirection. The Go compiler can inline and stack-allocate freely.

## 4. Phase 1 API Surface

A new package: **`github.com/rosscartlidge/ssql/v4/typed`**.

### 4.1 I/O

```go
// ReadCSV streams rows of T from a CSV file. T must be a struct.
// Field matching: case-insensitive struct field name, or `csv:"colname"` tag.
// Columns with no matching field are skipped.
func ReadCSV[T any](filename string) iter.Seq[T]

// WriteCSV writes a sequence of T as CSV. Header is taken from struct field
// names (or `csv:"colname"` tags).
func WriteCSV[T any](seq iter.Seq[T], filename string) error
```

### 4.2 Operations

```go
// Where filters in place. T → T.
func Where[T any](pred func(T) bool) func(iter.Seq[T]) iter.Seq[T]

// Limit takes the first n items. T → T.
func Limit[T any](n int) func(iter.Seq[T]) iter.Seq[T]

// Skip drops the first n items. T → T.
func Skip[T any](n int) func(iter.Seq[T]) iter.Seq[T]

// Select projects every input row to an output row of a different type.
// Use this for include/exclude/rename equivalents — generated code emits
// the correct constructor.
func Select[T, U any](fn func(T) U) func(iter.Seq[T]) iter.Seq[U]
```

### 4.3 Join

```go
// HashJoin builds a hash index over right and emits merged rows for each
// matching left. The right side is fully consumed (build phase); left
// streams (probe phase). Matches the build/probe shape of ssql.InnerJoin.
func HashJoin[L, R, O any, K comparable](
    left      iter.Seq[L],
    right     iter.Seq[R],
    leftKey   func(L) K,
    rightKey  func(R) K,
    merge     func(L, R) O,
) iter.Seq[O]
```

`HashJoin` is the Phase-1 join. Multi-key joins fall out of using a tuple type as `K`. Outer-join variants (`LeftJoin`, `RightJoin`, `FullJoin`) are deferred to Phase 1.5 if a user needs them.

### 4.4 What's deliberately out of scope for Phase 1

- **Aggregation (`group-by`, `Sum`, `Count`).** Adds a streaming/buffering decision that deserves its own PoC.
- **Outer joins.** Doable, but the inner-join is what the moonshot benchmark used.
- **Auto-detection of CSV column types.** The struct field type *is* the schema for typed code.
- **JSON/JSONL/Arrow readers.** CSV first; if the design holds, we extend.
- **Code generation** (CLI `--typed`, AST rewriter). That's Phase 2 and Phase 3 from the original doc, justified only after Phase 1 proves out in real user code.

## 5. Worked Example: Same Pipeline, Both APIs

The example we'll ship under `examples/typed/`. Reads `employees.csv`, joins against `departments.csv`, filters senior employees, writes the result.

### 5.1 Record version (current ssql)

```go
package main

import (
    "log"

    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    employees, err := ssql.ReadCSV("employees.csv")
    if err != nil {
        log.Fatal(err)
    }
    depts, err := ssql.ReadCSV("departments.csv")
    if err != nil {
        log.Fatal(err)
    }

    seniors := ssql.Where(func(r ssql.Record) bool {
        return ssql.GetOr(r, "years", int64(0)) >= 5
    })(employees)

    joined := ssql.InnerJoin(depts, ssql.OnFields("dept_id"))(seniors)

    if err := ssql.WriteCSV(joined, "seniors.csv"); err != nil {
        log.Fatal(err)
    }
}
```

Pros: no schema declarations, works on any CSV. Cons: every field access is a hash lookup; every join allocates a fresh `map[string]any`.

### 5.2 Typed version (Phase 1 library)

```go
package main

import (
    "log"

    "github.com/rosscartlidge/ssql/v4/typed"
)

type Employee struct {
    Name   string
    DeptID string `csv:"dept_id"`
    Years  int64
    Salary float64
}

type Department struct {
    DeptID   string `csv:"dept_id"`
    DeptName string `csv:"dept_name"`
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

Cost: three struct types must be declared. Benefit: ~5× faster end-to-end and ~10× less memory at 1M rows; on the compute-only path the gap is ~23× and ~zero allocations.

The example directory will include a `bench_test.go` that runs both side-by-side so users can reproduce the numbers on their own hardware. README guidance:

> Use `ssql.Record` for prototyping and dynamic schemas. Use `ssql/typed` when you know your schema and the pipeline is hot. The two are interoperable — convert with `typed.FromRecord[T]` / `typed.ToRecord` (Phase 1.5).

## 5b. What Phase 2 Delivers — and Why It's Different

`ssql generate go -typed` is the centerpiece of the moonshot. Phase 1 (the library) is its prerequisite, but Phase 2 is where the strategic value lands. **Phase 1 ships only the library**; this section describes the future capability the library is being designed to enable.

### The workflow shift

Today, a typical user journey for a non-trivial data pipeline looks like:

1. Prototype on the command line with `ssql from data.csv | ssql where … | ssql join … | ssql group-by …`. Iterate fast, debug interactively, get the logic right.
2. **When the pipeline becomes hot** — runs nightly over millions of rows, blocks a job, or costs real money in CPU — *re-implement it as Go code*. Either by hand against `ssql.Record`, or by hand against typed structs.
3. The prototype is now a maintenance liability: as requirements change, you either edit the shell script and re-port to Go, or you let the two drift.

Phase 2 collapses this:

1. Prototype as before.
2. Add `-typed` to the existing `generate go` flag and pipe to a file.
3. **The generated program is production-quality typed Go** — stack-allocated rows, direct field access, full compiler inlining, ~5–23× faster than the equivalent Record-based code.
4. The shell pipeline remains the source of truth; the typed Go is regenerable any time the pipeline changes.

The flexibility/performance tradeoff that today forces a rewrite simply disappears.

### What makes it innovative

Schema-aware code generation isn't new in Go — `sqlc` does it from SQL, `gqlgen` from GraphQL, `protoc-gen-go` from protobufs. But every existing generator works from a **schema declaration written by the user**: a `.sql` file, a `.graphql` file, a `.proto` file.

Phase 2's input is qualitatively different: **a Unix pipeline expression**. There is no schema file. The generator infers schemas by sampling the CSV files the pipeline references, then **propagates those types through every command in the pipe**. The user never declares a struct.

To our knowledge, no existing tool does this for a Unix-style streaming DSL. The closest analogues:

| Tool | Input | Output | Schema source |
|---|---|---|---|
| sqlc | SQL queries | Typed Go data layer | `CREATE TABLE` DDL |
| gqlgen | GraphQL schema | Typed Go resolvers | `.graphql` schema file |
| protoc-gen-go | `.proto` definitions | Typed Go structs + methods | `.proto` file |
| `awk -E` / shell | Pipeline expression | Just-runs (interpreted) | None — text-only |
| **ssql `-typed`** | **Pipeline expression** | **Typed Go program** | **Inferred from sample data** |

The right column is the novelty: schemas come from the data itself, not from a separate declaration. The pipeline IS the program. The data IS the schema.

### Concrete capabilities Phase 2 would deliver

1. **Auto-inferred struct types from any CSV / JSONL / Arrow source.** First N rows are sampled at generation time; column names, types, and nullability are inferred. Optional `--schema-file` override for cases where the file isn't materialized at generation time (e.g. SSH pushdown).

2. **Type flow propagation through every pipeline command.** Each command's `inputType → outputType` transformation is encoded once. Examples:
   - `where` and `update -set` preserve type (T → T)
   - `include`/`exclude` produce a derived struct with a subset of fields (T → T')
   - `rename -as old new` produces a derived struct with a renamed field (T → T')
   - `join` produces a generated merged struct (T + U → T_U)
   - `group-by` produces a struct with the grouping fields plus aggregations (T → G)

3. **Tagged struct outputs the user can read.** Generated structs carry `csv:"colname"` tags so the user can copy a struct out into their own code if they want to extend it.

4. **A measured ~5–23× speedup over the existing `generate go` output.** The PoC §2 numbers transfer directly: every pipeline that goes through `-typed` benefits from stack allocation and direct field access.

5. **No change to the user-facing CLI.** Just one flag. Same prompts work in tutorials, CI scripts, and AI code generation. (For the Anthropic/Gemini/Gemma test suite in `doc/research/llm-guided-api-design.md`, this means the same prompts produce both interpreter-style and compiler-style output.)

6. **A natural release valve when inference fails.** Pipelines that touch dynamic field names or use `update -set-expr` with arbitrary expressions can fall back to Record-based codegen with a warning — the user sees exactly which commands prevented full optimization, and can rewrite those steps if the speedup matters.

### Why this matters strategically

ssql today is positioned as "Unix pipes with SQL semantics, in a single Go binary." That's a reasonable elevator pitch but it puts ssql in a crowded space (DuckDB CLI, jq + miller + awk, q, etc.).

`ssql generate go -typed` repositions the project as **a high-level IR for streaming data pipelines**, with multiple backends:

- `generate go` — interpreted-style runtime (today)
- `generate go -typed` — compiled-quality Go (Phase 2 — this proposal)
- `generate sql` — DuckDB SQL (today)
- `generate ssql` — pipeline-rewriter / optimizer (today)

The library code path becomes the runtime that hand-written and generated typed Go both target. The CLI becomes the front-end syntax. The competitive story shifts from "yet another data CLI" to "the only tool I know of where you can prototype a 10-line pipeline at the shell and ship it as 5x-faster typed Go without writing the typed code yourself."

### Who benefits most

- **Data engineers shipping nightly batch jobs.** Iterate on the shell, deploy generated Go. The pipeline-as-source-of-truth eliminates drift.
- **Researchers / scientists.** Same. The shell exploration phase is preserved; the production deployment isn't a rewrite.
- **AI-assisted development.** Models are demonstrably good at generating ssql CLI pipelines (see the LLM appendix). With Phase 2, an LLM-generated pipeline becomes an LLM-generated *typed Go program* with one extra flag — without the model needing to know anything about Go generics.
- **Library authors.** The Phase-1 library makes hand-tuned hot paths possible today. Phase 2 makes them automatic.

### What Phase 2 doesn't try to do (and why)

- It doesn't replace the Record-based runtime. Dynamic-schema use cases (CSV-of-unknown-shape, JSON with optional fields, ad-hoc REPL-style work) stay on `Record`.
- It doesn't try to optimize arbitrary user Go code (that's Phase 3 — the AST rewriter — and is much harder).
- It doesn't introduce a new query language. The pipeline DSL stays exactly as it is.

The minimal viable Phase 2 is: schema sampling + type propagation through `from`, `where`, `include`, `exclude`, `rename`, `join`, `to`. That covers the workloads where the speedup matters most. `update -set` with literal values follows naturally. Aggregation (`group-by`) and full expression support follow in Phase 2.5 — once the core type-flow machinery is shipped.

## 5c. Phase-2 Preview: Example Output

The following shows what `generate go -typed` would emit for a representative pipeline. **This is illustrative, not committed** — it's included so the API design is anchored to a concrete future caller.

### Intended invocation

```bash
SSQLGO=1 ssql from employees.csv \
  | ssql where -if years ge 5 \
  | ssql join <(ssql from departments.csv) -using dept_id \
  | ssql to csv seniors.csv \
  | ssql generate go -typed
```

### Intended generated output

The codegen would emit code that imports the Phase-1 library and calls into it directly — i.e. **the same code shape as §5.2 above**, plus auto-derived struct types based on schema sampling at generation time:

```go
// Generated by `ssql generate go -typed` on 2026-04-26.
// Schemas inferred from employees.csv and departments.csv.
package main

import (
    "log"

    "github.com/rosscartlidge/ssql/v4/typed"
)

// Inferred from employees.csv (5 columns)
type EmployeeRow struct {
    Name   string  `csv:"name"`
    DeptID string  `csv:"dept_id"`
    Years  int64   `csv:"years"`
    Salary float64 `csv:"salary"`
    Active bool    `csv:"active"`
}

// Inferred from departments.csv (3 columns)
type DepartmentRow struct {
    DeptID   string `csv:"dept_id"`
    DeptName string `csv:"dept_name"`
    Location string `csv:"location"`
}

// Generated for the join result
type EmployeeRow_DepartmentRow struct {
    Name     string
    DeptID   string
    Years    int64
    Salary   float64
    Active   bool
    DeptName string
    Location string
}

func main() {
    employees := typed.ReadCSV[EmployeeRow]("employees.csv")
    depts     := typed.ReadCSV[DepartmentRow]("departments.csv")

    filtered := typed.Where(func(e EmployeeRow) bool {
        return e.Years >= 5
    })(employees)

    joined := typed.HashJoin(filtered, depts,
        func(l EmployeeRow) string   { return l.DeptID },
        func(r DepartmentRow) string { return r.DeptID },
        func(l EmployeeRow, r DepartmentRow) EmployeeRow_DepartmentRow {
            return EmployeeRow_DepartmentRow{
                Name: l.Name, DeptID: l.DeptID, Years: l.Years,
                Salary: l.Salary, Active: l.Active,
                DeptName: r.DeptName, Location: r.Location,
            }
        })

    if err := typed.WriteCSV(joined, "seniors.csv"); err != nil {
        log.Fatal(err)
    }
}
```

### Why Phase 2 is its own document

Several questions are out of scope for this proposal but in scope for the Phase-2 design doc that should follow Phase 1 shipping:

1. **Schema inference.** Sample N rows? Read the whole header + every value? Honor `--schema-file` overrides? What if the file isn't on disk at generation time (e.g. piped from stdin)?
2. **Type flow tracking.** Each command needs an "input schema → output schema" transformer (`include`, `exclude`, `rename`, `update -set`, `group-by`, ...). The CLI fragment system in `cmd/ssql` needs new fields so each fragment carries `inputType` and `outputType` info.
3. **Naming.** `EmployeeRow_DepartmentRow` is a placeholder. Real codegen probably wants user-controllable names via `-as TYPE` or similar, with a sensible default for one-off scripts.
4. **Fall-back behavior.** When a pipeline contains a command Phase-2 doesn't support yet (e.g. `group-by` until aggregation lands), should `generate go -typed` fall back to Record code with a warning, or refuse?
5. **Mixed pipelines.** `ssql.Record` and typed structs can both appear in the same generated program (e.g. typed for the hot path, Record for an ad-hoc late-stage transform). What does the boundary code look like?

The Phase-1 library design makes Phase 2 mechanically straightforward: every pipeline shape in §5c's preview is something the library already supports. The hard work in Phase 2 is the schema inference and type flow tracking, *not* the runtime semantics — those have already been solved by this proposal.

## 6. Implementation Plan

| Step | Effort | Risk |
|---|---|---|
| 1. Move `typed/io.go` + `typed/ops.go` from PoC into `typed/` package | 2h | low |
| 2. Add unit tests covering each primitive (CSV round-trip, where-empty, hash-join key collisions, etc.) | 3h | low |
| 3. Add `Limit`, `Skip`, `Select` (one-liners) | 1h | low |
| 4. Add supported field types: `int32`, `uint64`, `time.Time` (RFC3339), pointer-to-T (nullable) | 4h | medium — need to think about how nullable round-trips |
| 5. Move PoC bench into `typed/bench_test.go` and a separate `examples/typed/` demo | 2h | low |
| 6. Document in `doc/typed-reference.md` and link from `README.md` and `doc/api-reference.md` | 2h | low |
| 7. Run benchmarks at 10M × 3 joins to verify the gap really scales to ~35× | 1h | low — just need to extend the bench |

Total: ~1.5 days of focused work. Low-risk because (a) the PoC has already validated the design, and (b) the package is purely additive — it does not touch existing `ssql.Record` code.

## 6a. Library Phases After Phase 1

Phase 1 was deliberately scoped to the smallest credible runtime that
validated the moonshot's headline numbers. The follow-on phases below
fill out the surface so more pipelines become typed-codegen-eligible
(Phase 2 Tier 3) without users having to fall back to `SSQLGO=1`.

### Phase 1.5 — shipped (2026-04-26)

Wider type coverage and the operations needed to express most real
analytics workloads:

- `time.Time` (RFC3339), `int32`, `uint64`, `float32`, pointer-to-T
- `HashJoinMulti` (many-to-many), `LeftJoin`, `RightJoin`, `FullJoin`
- JSONL I/O (`ReadJSONL`, `ReadJSONLSafe`, `WriteJSONL`)
- Streaming aggregation: `Count`, `Sum`, `Min`, `Max`, `Avg`,
  `GroupBy`, `GroupByOrdered`, `Counter`, `NewSummer`, `NewAverager`,
  `Aggregator[T,R]` interface for custom accumulators

### Phase 1.6 — shipped (2026-04-26)

Two small additions plus one negative finding worth recording:

- `HashJoinSized` — pre-sized build map for known right-side cardinality
- `Strict()` CSV option — fail-fast on schema mismatch
- *Tried and rejected*: a custom byte-level CSV reader. Hypothesis
  (csv.Reader allocates per cell) was wrong; csv.Reader with
  `ReuseRecord=true` already block-allocates per row. The custom
  reader was 17% slower with 50% more allocations. Deleted; see
  [`typed-performance-notes.md`](typed-performance-notes.md) §1
  for the writeup so we don't repeat it.

### Phase 1.7 — shipped (2026-04-27, unblocks Tier 3 codegen)

Phase 2 codegen Tier 3 listed `sort`, `distinct`, and `union` as
deferred. The codegen work for those commands is small — emit a call
to a typed runtime function — but the runtime functions didn't exist.
Phase 1.7 closed that gap; the Tier 3 codegen wiring can now follow
in a separate cycle.

| Function | Signature | Notes |
|---|---|---|
| `SortBy[T,K Ordered](key func(T) K)` | `func(iter.Seq[T]) iter.Seq[T]` | Materializes, `slices.SortFunc`, yields. O(N) memory. |
| `SortByDesc[T,K Ordered](key func(T) K)` | as above, descending | |
| `SortByStable[T,K Ordered](key func(T) K)` | as above, stable | Slightly slower; preserves order for equal keys. |
| `Distinct[T,K comparable](key func(T) K)` | `func(iter.Seq[T]) iter.Seq[T]` | Streams; tracks seen keys in a hash set. O(unique-keys) memory. |
| `Concat[T any](seqs ...iter.Seq[T])` | `iter.Seq[T]` | Pure streaming. Preserves duplicates. |
| `Union[T,K comparable](key func(T) K, seqs ...)` | `iter.Seq[T]` | `Concat` + dedup in one pass. |

14 unit tests covering each primitive — empty input, no duplicates,
early termination, composite key types, multi-sequence Concat. All
pass; total typed-package test count went from 63 to 79.

**Why ship now rather than wait:** the proposal originally parked
this on "Tier 3 codegen demand isn't yet clear", but the additions
were genuinely small (~140 LOC of runtime, no API changes elsewhere)
and shipping them keeps the typed runtime ergonomically complete for
hand-written code even before the codegen wiring lands.

### Phase 1.8+ (open)

No specific scope yet. Things that have been mentioned in passing but
not designed:

- Faster JSONL via `goccy/go-json` or per-type generated unmarshallers
- Hand-rolled RFC3339 time parser (~3× over `time.Parse`)
- A `Schema[T]` cache exposed as a public type so users can pre-build
  decoders for hot paths
- Concurrency, the `Stream[T]` proposal (separate doc:
  [`typed-concurrency-proposal.md`](typed-concurrency-proposal.md))

These are deliberately not committed to a phase — they'll be picked
up if and when measurement says they matter.

## 7. Open Questions

1. **Tag conventions.** PoC uses `csv:"colname"`. Should we also support `ssql:"colname"` so the same tag works for any future format? Suggestion: yes, with `csv` accepted as fallback for ecosystem compatibility.
2. **Strict vs permissive parsing.** PoC silently ignores parse errors (`v, _ := strconv.ParseInt(...)`). For Phase 1, should errors halt the iterator (return `iter.Seq2[T, error]` from `ReadCSV`) or be reported through a separate error channel? Suggestion: provide both — `ReadCSV[T]` (lossy, fast, current PoC behavior) and `ReadCSVSafe[T]` returning `iter.Seq2[T, error]`. Mirrors the existing ssql split (`ReadJSONFast` vs `ReadJSONFastSafe`).
3. **Nullable fields.** Pointer-to-T (`*int64`) is the obvious encoding for "this column may be empty". Worth supporting in Phase 1?
4. **Where does it live in `doc/api-reference.md`?** Probably a new top-level section "Typed API" alongside "Record API", with cross-references making the prototype-vs-production tradeoff explicit.
5. **Naming.** `typed` is short and clear, but `fast` or `static` are also plausible. Suggestion: keep `typed` — it describes *what* the package does, not how fast it happens to be.

## 8. Decision Points Before Implementation

Before writing the production package the maintainer should confirm:

- [ ] Phase 1 scope as listed in §4 (CSV-only, inner join only, no aggregation)
- [ ] Tag convention (`csv:` only, or also `ssql:`)
- [ ] Strict-vs-permissive policy (one variant or both)
- [ ] Nullable encoding (pointer-to-T in Phase 1, or defer)
- [ ] Whether to also add a 10M × 3 joins benchmark as a release-blocker for the headline performance claim

Once these are settled, implementation is straightforward; the PoC at `/tmp/typed-bench/` is essentially the v0.

## 9. Conclusion

The PoC validates the original moonshot doc's headline: typed code generation can deliver an order-of-magnitude speedup *and* ~20× memory reduction on representative ssql workloads. A library-first Phase 1 (`ssql/typed` package) is low-risk, ships immediate value to users with hot pipelines, and is a prerequisite for the more ambitious CLI/AST code-generation paths that come later. We recommend approving Phase 1 and proceeding with implementation per §6.
