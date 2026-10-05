# Panics as the Library's Error Channel: What Embedding ssql in a Server Needs

Reference: DFC142
Created: 2026-09-30
Last modified: 2026-10-05

[Back to Index](./README.md)

## 0. The prompt

Ross, 2026-09-30: *"There is a heavy use of `panic()` in the Go code.
This can make it hard to integrate the code into a server, as the panics
have to be recovered from."*

This document is the survey he asked for (no code changed): where the
library panics and why, what the CLI and generated programs already do
about it, what an embedding server would have to do today, and three
options with a recommendation. It is a proposal for Ross to decide on.

## 1. Inventory (v4.110.0 + ddff3b1)

| Package | panic sites | recover sites | Character |
|---|---:|---:|---|
| root `ssql` (the library) | 39 in 17 files | 0 | the subject of this DFC |
| `typed` | 4 in 1 file | 1 | all in `read_error.go`: shard panics captured and re-raised in the consumer goroutine as `*ReadError` |
| `cmd/ssql/lib` | 5 | 1 | JSON/JSONL `*LineError` (2), two programmer-error guards in `op.go`, the generated `main()`'s recover |
| `cmd/ssql/commands` | 1 | 2 | `recoverCellError` (helpers.go), a swallow in schema mode |
| `cmd/ssql` | 1 | 1 | `main.go`'s top-level recover |

The 39 root sites fall into three groups.

**A. Data errors discovered mid-stream (28).** A row or cell that cannot
be processed, found only when the pipeline pulls it:

| Kind | Sites | Panic value |
|---|---|---|
| a delimited cell not of its column's type | `io.go:146` (`ReadCSVFromReader`), `tsv.go:105`, `xlsx.go:138`, `coerce_types.go:42` | `*CellError` |
| a JSON Lines line that is not a record | `io.go:878`, `io.go:888` | `*LineError` |
| a value a cast cannot convert | `cast_value.go:156,189,193`, `time_parse.go:54` | `*CastError` (193: `error`) |
| a `-if` literal not of the field's kind | `field_ops.go:43,348,359,368` | `*CompareError` |
| aggregate on an unorderable or mixed group | `agg_ordered.go:59,66`, `agg_paired.go:41,48`, `agg_stats.go:39,193` | `error` |
| expression evaluation / non-scalar result | `expr_agg.go:141` | `error` |
| window RANGE frame preconditions on a row | `window_range.go:25,66,70,76,80` | `error` |
| spill run I/O | `spill.go:64,70,188,199` | `error` |

**B. Programmer or configuration errors (7).** Wrong use of the API,
detectable before any row flows; a panic is the conventional Go answer
and these should stay panics:

| Site | Value | Note |
|---|---|---|
| `window_code.go:56`, `:188` | **string** | unknown `WindowFunc` implementation (`WindowFuncCode`, `DescribeWindowFunc`) |
| `window_agg.go:30` | **string** | `WAggregate` with a nil aggregate |
| `sql.go:3159` | **string** | `MustStreamWindow`: the name says so |
| `expr_agg.go:30`, `:56` | `error` | `ExprAgg` / `StreamExprAgg` with an expression that does not compile |
| `agg_stats.go:39` | `error` | `Percentile` with p outside [0, 1] (also counted in A) |

**C. Explicitly unsafe by name (2).** `Unsafe()` (`core.go:2021`)
re-panics the first error of an `iter.Seq2`; `JSONString.MustParse`
(`core.go:312`, **string**) is the Must form of `Parse`.

Six sites panic with a **string**, not an `error`: the four marked above,
plus `lib/op.go:68,87`. Everything else panics with an `error`, and the
data errors are typed (`*CellError`, `*LineError`, `*CastError`,
`*CompareError`) so `errors.As` works after a recover.

## 2. Why the library is built this way

The pipeline shape is

```go
type Filter[T, U any] func(iter.Seq[T]) iter.Seq[U]
```

and `iter.Seq[T]` has no error channel. A stage that meets a bad cell
in row 48,211 has exactly three ways to report it:

1. **Swallow it** (coerce to zero, drop the row). This was the behaviour
   until v4.86/v4.91 and was removed deliberately: a program that turned
   an unparsable cell into 0 disagreed with the CLI on the same file
   ([DFC124](./dfc124_missing_values.md)). It is not coming back.
2. **Panic**, and recover at a boundary that can report it.
3. **Use `iter.Seq2[T, error]`**, the `FilterWithErrors` shape, where
   every element carries an error slot.

The design chose 2 for the plain forms and 3 for a set of `*Safe`
variants. The Safe set today:

| Sources (return `iter.Seq2`) | Filters (`FilterWithErrors`) |
|---|---|
| `ReadCSVSafe[FromReader]`, `ReadJSONSafe[FromReader]`, `ReadJSONFastSafe[FromReader]`, `ReadLinesSafe`, `ExecCommandSafe`, `ReadCommandOutputSafe`, `FromChannelSafe`, `Safe` (adapter) | `SelectSafe`, `WhereSafe`, `LimitSafe`, `OffsetSafe` |

So a caller can read safely, but every operation between source and sink
other than select/where/limit/offset (sort, group-by, aggregate, join,
window, cast, fill, extract, pivot, spill, expressions) exists only in
the plain form and reports through a panic.

**Where the panics are recovered today.**

- `cmd/ssql/main.go:176`: one `defer recover()` around command
  execution; prints `Error: <value>` and exits 1 (`SSQL_DEBUG=1` keeps
  the trace).
- Generated programs: `lib/codefragment.go:1020` emits the same recover
  into every generated `main()`, converting a recovered `error` to
  `Error: …` / exit 1 and re-panicking anything else.
- `commands/helpers.go:68` `recoverCellError`: per-command, turns a
  `*CellError` into an error that also names the `-type` override.
- `typed/read_error.go`: parallel shards run under `shardGroup.Go`,
  which captures a shard's panic and re-raises it in the consumer
  goroutine via `Wait`/`rethrow`, so the boundary above sees it.
- `serve` (`serve_http.go:575`) runs every pipeline as a **subprocess**
  of itself, so a pipeline panic is process-isolated by construction;
  the HTTP layer sees an exit status and stderr.

That is: the CLI, generated programs and `serve` are all safe because
each owns a process boundary. An **embedding** that calls the library
in-process does not have one, and that is the gap Ross is pointing at.

## 3. What an in-process server has to do today, and where it breaks

**3.1 Recover in the goroutine that drives the loop.** A panic surfaces
in whichever goroutine is executing `for r := range seq`. If the handler
consumes the pipeline itself, `defer func() { recover() }()` in the
handler works and catches every group-A site, because pulls happen in
the caller's goroutine. The typed shards are covered by `shardGroup`.

**3.2 Four root helpers move the loop into a goroutine of their own,
with no recover.** Verified 2026-09-30 (`grep 'go func'` over the root
package, non-test):

| Helper | What its goroutine does | Effect of an upstream panic |
|---|---|---|
| `ToChannel` (`io.go:2403`) | `for item := range sb { ch <- item }` | escapes every caller recover; **crashes the process** |
| `ToChannelWithErrors` (`io.go:2417`) | same, on an `iter.Seq2` | same (a panic from a plain stage upstream of the `Safe` adapter) |
| `LazyTee` (`operations.go:761`) | one goroutine pulls the input and fans out | same |
| `Timeout` (`operations.go:1278`) | pulls the input in a goroutine so it can stop on the clock | same |

The other goroutine sites (`spill.go:236` SIGINT cleanup,
`sample_file.go:216` byte-offset probes, `catalog_remote_go.go` remote
shard runners) do not consume a user sequence and are low risk, though
the catalog runners also have no recover.

**3.3 Not every panic value is an `error`.** The six string sites in §1
mean a generic `recover()` → `error` conversion needs a second case;
both existing copies handle it differently (`main.go` prints `%v` of
anything; the generated `main()` re-panics a non-error). An embedder
would write a third copy.

**3.4 There is no per-row error path mid-pipeline.** With only the four
Safe filters, a server that wants "log the bad row and continue" for a
cast or an aggregate has nothing to call; recover-and-abort is the only
granularity.

## 4. Options

### Option 1 — one contract, one helper (small)

- Every panic the library raises is an `error` (fix the six string sites;
  `MustStreamWindow` and `MustParse` wrap theirs in `fmt.Errorf`).
- Export one recovery helper, and make the three existing copies use it:

```go
// Recover converts a pipeline panic into err. Use as
//   defer ssql.Recover(&err)
// in the function whose loop drives the pipeline. Non-error panic
// values are wrapped; nil is a no-op.
func Recover(err *error)

// Run calls fn and returns its error or the pipeline panic as an error.
func Run(fn func() error) error
```

- Document the contract in api-reference §Error Handling: *the plain
  forms fail fast with a panic whose value is an `error` (typed for data
  errors); recover it with `ssql.Recover` in the goroutine that consumes
  the pipeline, or use the Safe forms.*
- Close the four goroutine escape hatches (§3.2) by capturing the panic
  in the spawned goroutine and re-raising it in the consumer, exactly as
  `typed.shardGroup` does (the fan-out channel closes, the consumer's
  next pull re-panics). This is a bug fix independent of the rest.

Cost: a day. Risk: none to lane semantics; the CLI's and generated
programs' behaviour is unchanged (they already do this).

### Option 2 — a generic Safe adapter (medium)

Instead of writing a `*Safe` twin for each of the 28 group-A operations:

```go
// Safely runs f's output under a recover: the elements come through
// unchanged with a nil error, and a pipeline panic ends the sequence
// with (zero, err) as its last element instead of propagating.
func Safely[T, U any](f Filter[T, U]) FilterWithErrors[T, U]
```

Implementation: wrap the output `iter.Seq[U]`; inside the pull loop,
`defer` a recover that yields the error once and stops. It works because
group-A panics occur during the pull, in the consumer goroutine (after
Option 1 closes the four exceptions). An embedder writes

```go
out := ssql.Safely(ssql.Chain(ssql.GroupByFields(...), ssql.Aggregate(...)))(ssql.ReadCSVSafe(path))
for r, err := range out { … }
```

and gets per-pipeline error delivery with no process boundary. It does
**not** give per-row continue-past-the-bad-row semantics for the
operations that panic (that would need each operation to decide what
"skip" means for a sort or an aggregate, which is DFC124's territory),
but it makes the failure an ordinary value.

Cost: a day plus tests. It composes with `PipeWithErrors` /
`ChainWithErrors`, which exist.

### Option 3 — an error-carrying stream (large; not recommended now)

Redesign the pipeline type to carry an error slot everywhere (make
`FilterWithErrors` the primary shape, or a `Stream` with an error
channel). Every operation, every codegen template (record and typed),
the typed `Stream[T]` shards and the planner change. The lanes would
have to be re-verified end to end. Nothing in the current use cases
needs it once Options 1 and 2 exist; leave it as the thing to reach for
if a real embedder needs row-level continue semantics across the board.

## 5. Recommendation and plan

Do Options 1 and 2, in that order, as one unit:

1. **Fix the four goroutine escape hatches** (`ToChannel`,
   `ToChannelWithErrors`, `LazyTee`, `Timeout`): capture in the spawned
   goroutine, re-raise in the consumer. Test: a source that panics on
   its third element, consumed through each helper under a `recover`
   in the test goroutine; today the process would die, after the fix
   the recover sees the panic. Watch each fail first.
2. **Every panic value is an `error`.** Fix the six string sites. Gate:
   a test that walks the root and `lib` sources for `panic(fmt.Sprintf`
   and `panic("` outside `_test.go` and fails on any hit (the
   `api-coverage.sh` pattern: a mechanical rule, not a review item).
3. **Export `Recover` and `Run`**; make `main.go`, the generated
   `main()` (`lib/codefragment.go`) and `recoverCellError` call
   `Recover` (or its typed-error branch) so there is one conversion.
   The generated program's behaviour is unchanged; the corpus and the
   equivalence suite prove it.
4. **`Safely`** with tests on every group-A kind (a bad cell, a bad
   cast, a mixed-kind aggregate, a RANGE precondition, a spill I/O
   failure via an unwritable `Dir`).
5. **Docs**: api-reference §Error Handling states the contract and
   shows `Recover` and `Safely`; the LLM prompt's error section gets the
   same two lines; library-tour gets one paragraph "embedding in a
   server".

Not in scope: changing which conditions are errors (DFC124 decided
that), row-level skip semantics for barrier operations, or Option 3.

## 5a. Generated programs inside a service

Ross, 2026-09-30: *"add a discussion about how the Go code generated by
`ssql generate go …` can be integrated into a service rather than as a
standalone Go program."*

### What `generate go` emits today

Generated for `ssql from employees.csv | ssql where -param min int 30
-if-expr "age > min" | ssql group-by dept -count n -avg salary avg |
ssql to csv out.csv` in typed mode (record mode has the same skeleton):

```go
package main

type EmployeesRow struct { … }                       // inferred from the file's header + sample
type EmployeesRowAggregator struct { … }             // Add / Result / Merge
type EmployeesRowGroup struct { … }

var (
    flagInput    = flag.String("input", "employees.csv", "input CSV file")
    flagParamMin = flag.Int("param-min", 30, "expression parameter min (int)")
    flagOutput   = flag.String("output", "out.csv", "output CSV file")
)

func main() {
    defer func() { /* recover an error panic → "Error: …", exit 1 */ }()
    flag.Parse()
    if err := run(); err != nil { fmt.Fprintln(os.Stderr, "Error:", err); os.Exit(1) }
}

func run() error {
    records := typed.ReadCSVParallel[EmployeesRow](*flagInput, runtime.GOMAXPROCS(0))
    filtered := records.Where(func(r EmployeesRow) bool { return r.Age > int64(*flagParamMin) })
    grouped := typed.GroupByParallel(filtered, …)
    if err := typed.WriteCSV(grouped, *flagOutput); err != nil { return … }
    return nil
}
```

Five properties matter for embedding:

1. **It is `package main`.** It cannot be imported; a service can only
   `exec` it (which is what `serve` does: `generate go -script … -mode
   typed -build BIN`, then run the binary per request with the data
   directory as cwd, `serve_http.go:849`).
2. **Inputs, parameters and outputs are package-level `flag` globals.**
   `run()` reads `*flagInput`; nothing is passed in. Two pipelines in
   one process would share the globals; one pipeline cannot run twice
   concurrently with different parameters.
3. **The source is a file path** (or `os.Stdin` for a bare `from csv`
   in record mode; typed mode refuses a stdin source because it needs a
   file to sample the schema from). A service's data usually arrives as
   an `io.Reader` or is already in memory as rows.
4. **The sink writes to a file or `os.Stdout`.** A service wants the
   rows back (an `iter.Seq[EmployeesRowGroup]`) or an `io.Writer`.
5. **Failure = process exit.** Stage fragments carry inline
   `fmt.Fprintf(os.Stderr, …); os.Exit(1)`; the assembler rewrites those
   into `if err != nil { panic(err) }` inside `run()`
   (`lib/codefragment.go:807-848`) and `main()` recovers. In a service
   there is no `main()` to recover, which is §3 again. Found on the way:
   the **record-mode `to csv FILE` sink ignores `WriteCSV`'s error**
   (`ssql.WriteCSV(aggregated, *flagOutput)` with no check; the typed
   sink checks it), so a record program exits 0 on an unwritable output
   file. That is a bug to fix regardless of this DFC (TODO).

### The integration shapes

**Shape 1 — sidecar binary (works today).** Build once with `generate
go -build`, run per request with `-input`/`-param-*`/`-output` flags or
with the data on stdin (record mode) and the rows on stdout as JSON
Lines / CSV. This is `serve`'s design and it has real merits: the
generated program's panics are process-isolated, the service and the
pipeline can be different Go versions, a pipeline can be rebuilt without
redeploying the service, and DFC138's pipeline document gives a
shell-free way to ship the pipeline text. Costs: a process per request
(tens of ms), serialisation through the wire format, and the schema must
come from a file (typed) or be inferred per run (record). For a batch
service or an operator console this is the right shape and needs
nothing new.

**Shape 2 — library mode (proposed).** A new target that emits an
importable package instead of a program:

```
ssql generate go -pipeline '…' -package reports -func SeniorHeadcount
```

producing

```go
package reports

// SeniorHeadcount is the pipeline: from employees.csv | where … | group-by dept … .
// in is the source's rows; the returned sequence is the sink's rows.
// A failure ends the sequence with err set (nothing is printed, nothing exits).
func SeniorHeadcount(in iter.Seq[EmployeesRow], p SeniorHeadcountParams) iter.Seq2[EmployeesRowGroup, error]

type SeniorHeadcountParams struct{ Min int64 }      // one field per -param

// SeniorHeadcountFromCSV reads the source the pipeline named, for callers that have a file or reader.
func SeniorHeadcountFromCSV(r io.Reader, p SeniorHeadcountParams) iter.Seq2[EmployeesRowGroup, error]
```

What changes in the assembler, and what does not:

- **The row types are emitted as today**, exported, with a caller-chosen
  prefix so several pipelines can share a package (`-prefix` or derive
  from `-func`). The schema still comes from sampling the named file at
  generation time; a service that gets data over the wire generates
  against a representative file (or a `_schema`-headed JSONL) and
  compiles once. This is the same contract as today's `-input` flag: the
  file at generation time fixes the types.
- **Parameters become a struct**, not flags. The `-param` machinery
  already knows name, type and default; `lib.CodeParam` is the list the
  flags are built from, so the struct is a second renderer of the same
  list.
- **The source stage is replaced by the function's `in` argument** when
  the caller supplies rows, or by the `*FromReader` reader (both
  packages have `ReadCSVFromReader`, `ReadJSONLFromReader`,
  `ReadDelimFromReader`) for the `FromCSV` form. Typed parallel readers
  need a file (mmap + line index), so the reader form is serial; the
  in-memory form can shard with `typed.ParallelFromSlice` if the caller
  passes a slice.
- **The sink stage is dropped**: the function returns the last stage's
  sequence. `to csv`/`to table`/`to jsonl` are the caller's business
  (`WriteCSVToWriter` etc. exist for the service to call). A pipeline
  whose sink is `count` returns `iter.Seq2[int64, error]` of one.
- **Errors are values.** The function body is `run()`'s body wrapped in
  `defer ssql.Recover(&err)` (Option 1) and delivered through the
  `iter.Seq2` (Option 2's `Safely` shape) — so Shape 2 depends on §4's
  Options 1 and 2 and is the reason to do them: without them a stage
  panic escapes the service's handler.
- **Shard count is a parameter** (`p.Shards`, 0 = GOMAXPROCS), not a
  hard-coded `runtime.GOMAXPROCS(0)`; a service running many pipelines
  concurrently wants to bound it.

Lanes are unaffected: library mode is a different *skeleton* around the
same stage fragments; the equivalence suite gains a lane that compiles
the library form, calls the function on the fixture rows and compares
the returned sequence with exec — which is also the test that the
skeleton composes.

**Shape 3 — the pipeline document, executed in-process (not now).**
`ssql run` interprets a JSON pipeline document with no shell; a service
could do the same in-process if the `commands` package were a library
(`ssql.RunDocument(doc, in, out)`). Today the commands write to
`os.Stdout`, read `os.Stdin` and call `os.Exit`, and the inter-stage
transport is JSON Lines through pipes; making that in-process is the
"exec as a library" redesign, much larger than Shape 2 and slower than
compiled code. Not proposed; noted so it is not rediscovered.

### Recommendation

Shape 1 is the answer for anything that can afford a process per
request; document it as such (the README's "same pipeline is a compiled
binary" line plus a paragraph in `doc/library-tour.md` or a new
"embedding" section of the typed codelab). Shape 2 is the answer for
in-process services and is a bounded change: one new skeleton renderer
in `lib/codefragment.go` next to `writeMainCallingRun`, the params
struct from `lib.CodeParam`, the reader/argument source, sink dropped,
`Recover` wrapped, exported types with a prefix — after §5 steps 1-4
have landed. Estimate: two to three days including the equivalence lane
and a worked example. Fix the record `to csv` error drop first, on its
own, with a corpus case that writes to an unwritable path and expects
exit 1 in every lane.

## 6. Open questions for Ross

1. **Keep group B and C as panics?** `MustStreamWindow`, `Unsafe`,
   `MustParse`, an unknown `WindowFunc` type: I would keep them
   panicking (they are programmer errors and the names say so), just
   with `error` values. Agree?
2. **`Recover` naming and shape.** `defer ssql.Recover(&err)` mirrors
   `recoverCellError`; `ssql.Run(func() error) error` is the closure
   form. Both, or one?
3. **Should `Safely` end the stream with the error, or yield it and
   stop?** Proposed: yield `(zero, err)` once, then stop — the same
   shape `ReadCSVSafe` uses for a read error, so consumers have one
   idiom.
4. **Do the catalog remote goroutines need the same capture?** They run
   `ssh` subprocesses, not user stages; a panic there would be a bug in
   ssql itself. Proposed: yes, for uniformity, but low priority.
5. **Library mode (§5a Shape 2): is the function signature right?**
   Rows in, `iter.Seq2` out, params as a struct, sink dropped, source
   optionally read from an `io.Reader`. The alternative is to keep the
   sink and take an `io.Writer`; returning the sequence is more
   composable and lets the service pick the format, at the cost of the
   caller writing the loop.
6. **Should Shape 2 be a `generate go` flag pair (`-package`, `-func`)
   or a fifth target (`generate lib`)?** A flag pair shares the source
   flags (DFC139's `pipelineSourceFlags`) for free and keeps "Go out" in
   one place; a target reads better in `-help`. Proposed: flags.

## 8. Decisions and status

Ross, 2026-10-05, on §6: (1) keep Must/Unsafe/programmer-error sites as
panics but with `error` values; (2) both `Recover` and `Run`; (3)
`Safely` yields `(zero, err)` once then stops; (4) the catalog
goroutines get the same capture; (5) library mode: rows in, `iter.Seq2`
out, sink dropped, params struct; (6) as `-package`/`-func` flags on
`generate go`; scope now: foundations (§5 steps 1-4 + the sink fix),
library mode as a separate plan.

**Done 2026-10-05 (foundations):** the record `to csv`/`to tsv` error
drop (both emissions each; `to tsv` had only the record one, so every
mode was affected) with two ExpectFail corpus cases; `panicGroup`
(`goroutine.go`) behind `LazyTee`, `Timeout` (which also stopped calling
`yield` from its producer goroutine), `ToChannelErr` (new; `ToChannel`
deprecated), `ToChannelWithErrors` (panic → `errCh`), and the two catalog
shard goroutines (panic → `recordErr`); the seven string panics are
errors and `TestPanicValuesAreErrors` scans the sources; `Recover`/`Run`
(`recover.go`) used by `cmd/ssql/main.go`, the generated `main()`
(`writeMainCallingRun`, which also made the typed assembler always
import `ssql`, `fmt`, `os`) and `recoverCellError`; `Safely` in
`core.go` with one test per failure kind. Each gate was watched to fail
first: the sink corpus case exited 0, the three goroutine tests took the
test binary down, the scan listed seven sites. A throwaway in-process
service handling a bad-cell CSV through `Run` and through `Safely` got
the `*CellError` both times and stayed alive. `generate go -run`
compiles against the released module, so until the next release it
needs `SSQL_MODULE_DIR=<checkout>` to find `ssql.Run`.

**Found on the way, TODO:** `LazyTee` drops a value for a consumer whose
buffered channel is full (its own comment says so) — silent data loss,
out of scope here.

**Next:** §5a Shape 2, library mode, as its own plan.

## 7. Related

- [DFC124](./dfc124_missing_values.md) — why absent values are never
  coerced and why the readers fail fast (the origin of most group-A
  sites).
- [DFC133](./dfc133_finding_unknown_bugs.md) — the degenerate-input
  crash sweep, the instrument that would catch a panic escaping the
  process boundary.
- [DFC138](./dfc138_pipeline_document_on_the_wire.md) — pipelines as
  documents between machines; an in-process embedder is the other
  integration shape and needs this DFC's contract.
- `typed/read_error.go` — the shard panic capture that Option 1 copies
  for the four root helpers.
- `doc/api-reference.md` §Error Handling — the contract's home.
