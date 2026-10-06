# Library mode: `ssql generate go -package` — what was built, and an example

Reference: DFC143
Created: 2026-10-06
Last modified: 2026-10-06

[Back to Index](./README.md)

Ross, 2026-10-06: *"write a doc detailing exactly what was done to make
the package mode and an example of using it."* This is that account.
The decision record is [DFC142](./dfc142_panics_as_error_channel.md)
§5a and §8; this document is the implementation and a worked example.

## 1. What a generated program was, and why it could not be imported

Before 2026-10-06 `ssql generate go` had one output shape: a `package
main`. Every stage of the pipeline emits a *code fragment* (a JSON line
with the Go for that stage, its imports, its parameters and, in typed
mode, the struct it produces), and the assembler in `cmd/ssql/lib`
stitched the fragments into

```go
package main
var ( flagInput = flag.String("input", "employees.csv", …); flagParamMin = flag.Int(…) )
func main() { flag.Parse(); if err := ssql.Run(run); err != nil { …; os.Exit(1) } }
func run() error {
    records := typed.ReadCSVParallel[EmployeesRow](*flagInput, runtime.GOMAXPROCS(0))
    filtered := records.Where(func(r EmployeesRow) bool { return r.Age > int64(*flagParamMin) })
    grouped  := typed.GroupByParallel(filtered, …)
    if err := typed.WriteCSV(grouped, *flagOutput); err != nil { … os.Exit(1) }
    return nil
}
```

Four things made that unusable from inside another Go program:

1. **`package main`** cannot be imported.
2. **Parameters are process-wide flags.** Two requests with different
   `-param min` values cannot share a process.
3. **The data comes from a file path and goes to a file or stdout.** A
   service has rows in memory, or an `io.Reader` from a request body,
   and wants rows back.
4. **Failure exits the process.** `main()` recovers the library's
   fail-fast panic and calls `os.Exit(1)`; several stage templates
   called `os.Exit(1)` directly from inside the pipeline body.

[DFC142](./dfc142_panics_as_error_channel.md) fixed the fourth point at
the library level in v4.111.0 (`Recover`, `Run`, `Safely`; every panic
value an `error`; no goroutine lets a panic escape). Library mode is the
shape built on top of that.

## 2. The shape

```
ssql generate go -package reports -func Headcount -pipeline '…' reports/headcount.go
```

emits `package reports` with:

| Emitted | Meaning |
|---|---|
| `type HeadcountEmployeesRow struct {…}` | the source's row type, `ssql` and `json` tags naming the columns |
| `type HeadcountEmployeesRowGroup struct {…}` | the result row type (here a group-by result), tagged the same way |
| `type HeadcountParams struct { Min int; Shards int }` | one field per `-param` and per lifted literal; `Shards` (0 = every core) when the pipeline has a parallel form |
| `func HeadcountDefaults() HeadcountParams` | the values the pipeline was written with — a zero `HeadcountParams` is **not** the pipeline |
| `func Headcount(in iter.Seq[HeadcountEmployeesRow], p HeadcountParams) iter.Seq2[HeadcountEmployeesRowGroup, error]` | rows in, rows out; a stage failure is the sequence's last element `(zero, err)` |
| `func HeadcountFromCSV(r io.Reader, p HeadcountParams) iter.Seq2[…]` | the reader form, when the source stage was `from` a CSV or TSV file |

There is no `main`, no `flag`, no `os.Exit`, and nothing is printed.
Every generated type name is prefixed with the function's name, so
`Headcount` and `Attrition` over the same `employees.csv` can share a
package. Ross's decisions (DFC142 §8): rows in, `iter.Seq2` out, the
sink dropped, parameters as a struct, flags on `generate go` rather
than a new target, type names prefixed, and — after a first serial
cut — the same parallel plan a program gets.

## 3. Exactly what was done

In the order it was built. File paths are relative to the repository.

### 3.1 Options reach the assembler

`lib.AssembleCodeFragments(io.Reader)` took no options and chose the
record or typed assembler itself. It now delegates to
`AssembleCodeFragmentsWith(r, AssembleOptions{Package, Func})`
(`cmd/ssql/lib/codefragment.go`); after `ResolveBindings` (which gives
every fragment variable its final unique name) and the `-explain`
notes, `opts.Package != ""` routes to `assembleLibrary`
(`cmd/ssql/lib/codefragment_library.go`, new). Both program assemblers
are untouched.

`generate go` (`cmd/ssql/commands/generate_go.go`) gained `-package
NAME` and `-func NAME` (`Pipeline` when only `-package` is given;
`-func` without `-package` is an error). `-package` with `-run` or
`-build` is refused — a library has nothing to run. The optimiser's
re-execution (`runOptimiseThenGo`, which reruns a rewritten pipeline
through an inner `generate go +O`) forwards both flags, so the default
`-O` path produces the library too and its header records the rewrite.
`OUTPUT`'s directory is created on demand (`reports/headcount.go` into
a fresh `reports/`). Three `-help` examples that still said
`SSQL_MODE=parallel` now say `typed`, and a library-mode example was
added.

### 3.2 `assembleLibrary`: one skeleton for record and typed fragments

The function (about 300 lines) does these things to the fragment list:

**Type-name prefixing.** Every `type X struct` name in any fragment's
`StructDefs` (and every `InputTypedSchema`/`OutputTypedSchema.TypeName`)
is collected, sorted longest first, and replaced on word boundaries
with `Func+X` in `Code`, `AltCodeIfSeq`, `StructDefs`, the schemas and
recursively in subprocess function bodies. Doing this in the assembler
rather than in the stage processes means it works for fragments that
arrive on stdin and through the optimiser's re-execution with no
environment variable.

**The source becomes the input.** Exactly one `init` fragment is
allowed (several sources — `union`, `merge`, set-operation side files —
are refused with a message naming both). Its original code is kept
aside to decide the reader form, then replaced. Record mode: `records
:= in`. Typed mode: `records := typed.ParallelBatched(in, *flagShards)`
with `Capabilities{None→Stream}`, and `records := in` as
`AltCodeIfSeq` with `{None→SeqTyped}`. The planner
(`applyPlannerBoundaries`) then runs exactly as for a program: if any
stage accepts a `Stream` the parallel plan stands and the stages keep
their `Stream` forms; otherwise the planner's phase 1 swaps the source
and every stage to the serial alternative, and no `Shards` parameter
appears. The source's own parameters (`input`, `sample-n`, …) and
imports that served the replaced code (`runtime`, `fmt`, `os`, `flag`)
are dropped; imports its struct still needs (`time` for `time.Time`
fields) are kept.

**The sink is dropped.** `final` fragments contribute no code, no
parameters (`output`) and no imports. `count` is a sink too: the
function returns the rows it would have counted.

**Parameters become a struct.** `collectParams` (the same routine the
program assemblers use) gathers every `CodeParam`; those belonging to
the source and the sinks are removed; `shards` is appended when the
parallel source survived planning. Each becomes a field: `param-min` →
`Min`, `pop-gt` → `PopGt` (through `GoNameFromColumn`, collisions
numbered), Go type from the param's type (`int`, `float64`, `bool`,
`string`), default rendered with the same quoting `flagDecl` uses.
`FDefaults()` returns those defaults. At the top of the function every
field is bound as `flagParamMin := &p.Min; _ = flagParamMin`, so the
stage code — written against `*flagParamMin`, the pointer `flag.Int`
would have returned — compiles unchanged. This is the "second renderer
of the one `CodeParam` list" DFC142 asked for; no stage template
changed for library mode.

**The body.** `return ssql.Safely(func(in iter.Seq[In]) iter.Seq[Out]
{ <source line>; <stages>; return <last> })(ssql.Safe(in))`. `Safely`
(v4.111.0) drives the inner sequence under `Recover` and yields `(zero,
err)` once when a stage panics with an error. Stage code is emitted
verbatim (the record assembler's `ssql.Chain` rewrite is not needed:
every stage is already `out := F(…)(in)`), with two adaptations: an
`if err != nil { return fmt.Errorf(…) }` block written for `run()`
becomes `panic(fmt.Errorf(…))` (a closure returning `iter.Seq` cannot
return an error), and hoisted `var … = runtime.MustCompile…`
declarations go to package level — **unless they read a parameter**
(the expression VM's thunk does: `func() any { return *flagParamSince
}`), in which case they stay in the body where the bindings are in
scope. The `go-lib` lane found that one (`param_time_where` did not
compile). When the last fragment produces a `Stream`, the body returns
`last.Serial()`; when it produces `ssql.Record` (a typed→Record
boundary, as for `window` or `describe`), `Out` is `ssql.Record`.

**Subprocess functions** (`rightSourceN()` for a join's side file) are
emitted as package-level functions as before, but take `p FParams` and
bind their own parameters, and the stage's call becomes
`rightSource1(p)`.

**Imports** are the skeleton's (`iter`, `ssql`, `typed`, `io` for the
reader form) plus the kept fragments', then **pruned against the
emitted text**: a stage that listed `os` for an error path that is now
a panic would otherwise fail the build with an unused import.

**The reader form** is emitted when the original source code was a
plain `ReadCSV`/`ReadTSV` (record) or
`ReadCSV[T]`/`ReadDelim[T]`/their `Parallel` forms (typed) of
`*flagInput` with no sampling: `typed.ReadCSVFromReader[In](r)`,
`typed.ReadDelimFromReader[In](r)`, `ssql.ReadCSVFromReader(r[, cfg])`,
`ssql.ReadTSVFromReader(r)`. The header comment names the source and
each dropped sink.

### 3.3 `typed.ParallelBatched`: entering the parallel runtime from a sequence

A program's typed source is `ReadCSVParallel`, which maps the file and
partitions it by byte range — no channel on the row path, the house
rule of `claude/concurrency.md` §1 (the per-row distributor
`typed.Parallel` measured 3× slower than serial). A library function's
input is a sequence its caller owns, so neither byte-range nor slice
partitioning applies. The first cut therefore forced the serial plan;
Ross flagged the same afternoon that losing parallelism would matter,
and `typed.ParallelBatched(in, n)` (`typed/stream.go`) replaced it: the
feeder pulls rows into batches of 1024 and sends each batch once over a
channel of `2n` batches; every shard takes whole batches and iterates
them in stack code; batches recycle through a `sync.Pool`; a source
panic is captured in the feeder (`shardGroup`) and re-raised by the
shards once the channel drains; a consumer that stops early closes a
`stop` channel the feeder selects on.

Measured on the 10 M-row three-join scale workload, 24 threads, idle
machine, `SerialCount` sink in every variant
(`typed/concurrency_bench_test.go`):

| Entry point | Wall |
|---|---|
| `ParallelFromSlice` (the floor: no distributor) | 0.13 s |
| `ParallelBatched` from an in-memory sequence | 0.17 s |
| per-row `Parallel` from the same sequence | 5.3 s |
| `ParallelBatched` fed by `typed.ReadCSV` | 3.4 s (the serial parse is the bound; typed serial end to end is 5.3 s) |

So the distributor costs about 4 ns a row. The first comparison said
4.4 s and sent the work after allocation — a trap worth recording: the
copied benchmark drained through `Serial()` (a per-row fan-in channel
over 7.25 M rows) while the slice benchmark counted with
`SerialCount()`; the sink was the number, not the distributor.

### 3.4 Prerequisites found by the surveys and the gates

- **Stage code exited the process.** Three stage templates were known
  (`extract` without `-skip`, the typed `update` expression error,
  `resample`); a scan over every stage family's fragments
  (`TestStageCodeNeverExits`, `cmd/ssql/stage_exit_test.go`) found
  **twenty** emitters calling `os.Exit(1)`: every side-file read
  (`join`, `union`, `merge`, set operations), the sampled, Parquet,
  XLSX, WAV, SSH and catalog sources, the four signal commands. Those
  at the top of `run()` now `return fmt.Errorf(…)` (the shape `from
  csv` already used); those inside closures `panic(fmt.Errorf(…))`.
  Under `main()` the behaviour is identical (`ssql.Run` prints `Error:`
  and exits 1); inside a library `Safely` delivers the error.
- **A phantom `ctx`.** `fft`, `ifft`, `convolve`, `correlate`,
  `spectrogram` and the typed `to table FIELDS` sink emitted
  `ctx.Stderr()`/`ctx.Stdout()`, names that do not exist in a generated
  program; `to table name dept -only` had never compiled in typed mode
  (corpus case `to_table_selected_fields`).
- **Result structs had no `json` tags.** The first library driver
  printed `{"N":1,"Product":…}` against exec's `{"n":1,"product":…}`:
  the group-by, join, update, cast, projection, extract, resample,
  unpivot and rollup templates rendered their structs with an `ssql`
  tag only (join with none). The CLI never noticed because `to jsonl`
  converts typed rows to Records by schema name. Every generated row
  struct now carries `ssql:"x" json:"x"`.
- **`LazyTee` dropped values.** Its broadcast `select` had a
  `default:` branch that discarded a value for any consumer whose
  buffer was full (1,000 values → 201 and 150). Fixed first, test
  watched to fail.

### 3.5 Gates

- `cmd/ssql/lib/codefragment_library_test.go`: string-level checks of
  the record skeleton, the typed skeleton with the parallel plan
  (prefixed names, `ParallelBatched`, `Shards`, `.Serial()`), the serial
  alternative when nothing is parallel, the refusals, and the
  return→panic adaptation.
- `cmd/ssql/library_mode_test.go` (`TestLibraryMode`): generates the
  library for a corpus pipeline in both modes, compiles it beside a
  driver `main` (`goRunGeneratedModule`, the multi-file generalisation
  of the test helper), and compares the rows with the exec lane through
  the equivalence canonicaliser; a changed parameter changes the result;
  a join's side file takes `p`; a bad cell is a `*typed.ReadError` and
  the process survives; two functions share a package; the optimiser
  forwards the flags; the refusals.
- A **`go-lib` lane** in `TestPipelineEquivalence`
  (`cmd/ssql/equivalence_test.go`): every equivalence case whose source
  is a plain delimited file is also generated as a library, driven
  through its reader form and compared with every other lane. It
  inherits `Skip["go-typed"]`, skips when there is no reader form or
  library mode refuses, and was watched to fail by dropping a row. It
  runs inside the permutation and metamorphic suites too.
- `TestParallelBatched*` in `typed/stream_test.go` (round trip, panic
  reaches the consumer, early stop releases the feeder) under `-race`.
- The usual: corpus, `go test -race . ./typed/...`, the CLI codelab run
  (§7 now has a block that generates, compiles and runs a caller),
  `make doc-check`, the full `cmd/ssql` package.

### 3.6 Documentation

CLI codelab §7, library tour (leads with the generated function),
typed codelab Step 9, typed reference (`ParallelBatched`), AI CLI
generation table, `claude/concurrency.md` §1 and §10, CHANGELOG
`[Unreleased]`, DFC142 §8, TODO.

## 4. Worked example

On the codelab data (`doc/codelab-data/employees.csv`: `name, age, dept,
salary, city, level, hire_date, status`).

### 4.1 Generate

```bash
ssql generate go -package reports -func Headcount \
  -pipeline 'ssql from employees.csv | ssql where -param min int 30 -if-expr "age > min" | ssql group-by dept -count n -avg salary avg_salary | ssql to csv headcount.csv' \
  reports/headcount.go
```

The sink `to csv headcount.csv` is part of the pipeline as you would
run it at the shell; the library drops it and says so in its header.

### 4.2 What came out (`reports/headcount.go`, verbatim apart from the aggregator's method bodies)

```go
package reports

/*
Generated by ssql 4.111.0 (library mode, typed):

(export SSQL_MODE=typed
ssql from employees.csv |
ssql where -param min int 30 -if-expr 'age > min' |
ssql group-by dept -count n -avg salary avg_salary |
ssql to csv headcount.csv |
ssql generate go -package reports -func Headcount)

The source stage `ssql from employees.csv` supplies the `in` argument (HeadcountFromCSV reads it from an io.Reader).
The sink `ssql to csv headcount.csv` is dropped: the caller consumes the rows.
*/

import (
	"io"
	"iter"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/typed"
)

type HeadcountEmployeesRow struct {
	Name     string `ssql:"name" json:"name"`
	Age      int64  `ssql:"age" json:"age"`
	Dept     string `ssql:"dept" json:"dept"`
	Salary   int64  `ssql:"salary" json:"salary"`
	City     string `ssql:"city" json:"city"`
	Level    int64  `ssql:"level" json:"level"`
	HireDate string `ssql:"hire_date" json:"hire_date"`
	Status   string `ssql:"status" json:"status"`
}

// HeadcountEmployeesRowAggregator is the typed.Aggregator[T, R] implementation generated for this group-by.
type HeadcountEmployeesRowAggregator struct { agg0 int64; agg1 int64; agg1_n int64 }
func (a *HeadcountEmployeesRowAggregator) Add(r HeadcountEmployeesRow)            { … }
func (a *HeadcountEmployeesRowAggregator) Result() HeadcountEmployeesRowAggregatorResult { … }
func (a *HeadcountEmployeesRowAggregator) Merge(other typed.Aggregator[…]) { … }
type HeadcountEmployeesRowAggregatorResult struct { N int64; AvgSalary float64 }

// HeadcountEmployeesRowGroup is the per-group result type produced by typed.GroupBy.
type HeadcountEmployeesRowGroup struct {
	Dept      string  `ssql:"dept" json:"dept"`
	N         int64   `ssql:"n" json:"n"`
	AvgSalary float64 `ssql:"avg_salary" json:"avg_salary"`
}

// HeadcountParams holds the pipeline's parameters. A zero value is NOT the
// pipeline's own literals: start from HeadcountDefaults().
type HeadcountParams struct {
	Min    int // -param-min 30
	Shards int // -shards 0
}

// HeadcountDefaults returns the parameter values the pipeline was written with.
func HeadcountDefaults() HeadcountParams {
	return HeadcountParams{Min: 30, Shards: 0}
}

// Headcount runs the pipeline over in. A failure in any stage ends the
// sequence with (zero, err) as its last element (ssql.Safely); nothing
// is printed and nothing exits. Stop early by returning false from the
// range body, as with any iterator.
func Headcount(in iter.Seq[HeadcountEmployeesRow], p HeadcountParams) iter.Seq2[HeadcountEmployeesRowGroup, error] {
	flagParamMin := &p.Min
	_ = flagParamMin
	flagShards := &p.Shards
	_ = flagShards
	return ssql.Safely(func(in iter.Seq[HeadcountEmployeesRow]) iter.Seq[HeadcountEmployeesRowGroup] {
		records := typed.ParallelBatched(in, *flagShards)
		filtered := records.Where(func(r HeadcountEmployeesRow) bool {
				return (r.Age > int64(*flagParamMin))
			})
		grouped := typed.GroupByParallel(filtered,
				func(r HeadcountEmployeesRow) string { return r.Dept },
				func() typed.ParallelAggregator[HeadcountEmployeesRow, HeadcountEmployeesRowAggregatorResult] { return &HeadcountEmployeesRowAggregator{} },
				func(k string, agg HeadcountEmployeesRowAggregatorResult) HeadcountEmployeesRowGroup {
					return HeadcountEmployeesRowGroup{Dept: k, N: agg.N, AvgSalary: agg.AvgSalary}
				})
		return grouped
	})(ssql.Safe(in))
}

// HeadcountFromCSV reads the pipeline's source format from r and runs Headcount over it.
func HeadcountFromCSV(r io.Reader, p HeadcountParams) iter.Seq2[HeadcountEmployeesRowGroup, error] {
	return Headcount(typed.ReadCSVFromReader[HeadcountEmployeesRow](r), p)
}
```

Things to notice: the `where` and `group-by` stages are the *same
text* a program gets (`records.Where`, `typed.GroupByParallel`); the
only differences from a program are the first line of the body, the
last line, and what surrounds them. `flagParamMin := &p.Min` is how a
stage written against a flag pointer reads a struct field. `Shards`
exists because `where` and `group-by` have parallel forms; a pipeline
of `sort` alone would get `records := in` and no `Shards`.

### 4.3 A caller

```go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v4/typed"
	"example/reports"
)

// GET /headcount?min=40 with the CSV as the request body.
func headcount(w http.ResponseWriter, r *http.Request) {
	p := reports.HeadcountDefaults()
	if s := r.URL.Query().Get("min"); s != "" {
		p.Min, _ = strconv.Atoi(s)
	}
	enc := json.NewEncoder(w) // the row types carry json tags with the column names
	for g, err := range reports.HeadcountFromCSV(r.Body, p) {
		if err != nil {
			var bad *typed.ReadError
			if errors.As(err, &bad) {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		enc.Encode(g)
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		http.HandleFunc("/headcount", headcount)
		http.ListenAndServe(":8080", nil)
		return
	}
	// Command-line demo: defaults, then a changed parameter, then bad data.
	p := reports.HeadcountDefaults()
	fmt.Printf("defaults %+v\n", p)
	f, _ := os.Open("employees.csv")
	for g, err := range reports.HeadcountFromCSV(f, p) {
		fmt.Println(g, err)
	}
	p.Min = 45
	f2, _ := os.Open("employees.csv")
	fmt.Printf("\nmin=%d\n", p.Min)
	for g, _ := range reports.HeadcountFromCSV(f2, p) {
		fmt.Printf("%s: %d people, mean salary %.0f\n", g.Dept, g.N, g.AvgSalary)
	}
	fmt.Println("\nbad data:")
	bad := strings.NewReader("name,age,dept,salary,city,level,hire_date,status\nZed,forty,Eng,1,x,L1,2020-01-01,active\n")
	for _, err := range reports.HeadcountFromCSV(bad, p) {
		if err != nil {
			var re *typed.ReadError
			fmt.Printf("error: %v (typed.ReadError: %v)\nthe process is still running\n", err, errors.As(err, &re))
		}
	}
}
```

With a `go.mod` requiring `github.com/rosscartlidge/ssql/v4` (the
library uses `ssql.Safely` and `typed.ParallelBatched`, so it needs the
release that carries them; until then a `replace` to the checkout):

```
$ go run .
defaults {Min:30 Shards:0}
{Engineering 3 99666.66666666667} <nil>
{Marketing 3 80333.33333333333} <nil>
{Sales 1 82000} <nil>

min=45
Marketing: 1 people, mean salary 91000

bad data:
error: typed.ReadCSVFromReader: row 1: column "age": "forty" is not int64 (typed.ReadError: true)
the process is still running
```

Three requests with three parameter values in one process, the bad
one answered with a typed error and the process alive — which is what
DFC142 set out to make possible. The `(zero, err)` arrives after any
rows that were already complete; here `group-by` is a barrier, so the
bad row stops the group before anything is yielded.

### 4.4 Rows that are already in memory

`Headcount` takes any `iter.Seq[HeadcountEmployeesRow]`:

```go
rows := []reports.HeadcountEmployeesRow{{Name: "a", Age: 51, Dept: "Eng", Salary: 100}, …}
for g, err := range reports.Headcount(slices.Values(rows), reports.HeadcountDefaults()) { … }
```

The distributor batches the sequence into the shards; for a slice this
costs the one copy into batches (about 4 ns a row on the scale
workload). A `FromSlice` form that uses `ParallelFromSlice` directly
would remove even that; it was not needed for the service case and is
listed below.

## 5. Limits and follow-ups

- One source. `union`, `merge` and the set operations read their side
  files at run time inside the function (as a program would); a
  pipeline with several *source* fragments is refused. Side files as
  further inputs is the natural next step.
- The reader form exists for CSV and TSV sources only. Parquet, JSONL
  and the others have no `io.Reader` form yet; the function itself
  takes any sequence.
- Record mode has no parallel form (it never had one).
- A `FromSlice` form using `ParallelFromSlice`.
- The generated function reads parameters through pointer bindings
  (`flagX := &p.X`), a visible seam left so that no stage template had
  to change. If stage templates ever grow a parameter protocol other
  than `*flagX`, this is the one place to update.

## 6. Related

- [DFC142](./dfc142_panics_as_error_channel.md) — panics as the
  library's error channel; §5a the service shapes, §8 decisions and
  status.
- [DFC115](./dfc115_commands_are_the_authority.md) — commands are the
  authority on themselves; the assembler reads fragments, it does not
  re-parse commands.
- `claude/concurrency.md` §1 — never a channel per row; §10 the
  `ParallelBatched` entry.
- [Library Tour § Embedding in a service](../library-tour.md#embedding-in-a-service),
  [CLI Codelab § 7](../cli-codelab.md#7-generate-code).
