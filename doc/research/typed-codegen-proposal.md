# `ssql generate go -typed` — Phase 2 Proposal

**Status:** Tier 1 + Tier 2 + Tier 3a SHIPPED (2026-04-26 / 2026-04-27). Activate with `SSQLGO=typed` instead of `SSQLGO=1`.

**Tier 1 (initial ship):** `from FILE.csv` (with schema sampling), `where -if FIELD OP VALUE` (literal operators), `join FILE.csv -using FIELD` / `-on LEFT RIGHT` (single-key, single-clause + process-substitution), `to csv [FILE]`, `to table`.

**Tier 2:** `limit N`, `offset N`, `include FIELDS…`, `exclude FIELDS…`, `rename -as OLD NEW`, `group-by FIELDS… -count NAME -sum F NAME -avg F NAME -min F NAME -max F NAME` (single- and multi-field group keys, synthesized aggregator + result struct).

**Tier 3a (this update — wired on top of typed Phase 1.7):** `sort FIELD [-desc]` (single-field), `distinct` (full-row dedup), `union -file FILE` and `union -file FILE -all` (with cross-source schema validation).

**Still deferred:** `-if-expr` / `-set-expr` (expression-language → Go), `update -set` (literal-only would be straightforward next), multi-field `sort`, `-rollup` / `-cube`, `-collect`, signal processing.

Pipelines containing a Tier-3 command abort with a clear error naming the offender and suggesting `drop -typed`.

The original proposal text below is preserved as the design record.

**Predecessors:**
- [`typed-package-proposal.md`](typed-package-proposal.md) — Phase 1 + 1.5 + 1.6 typed library, shipped
- [`typed-performance-notes.md`](typed-performance-notes.md) — single-thread optimizations explored
- [`typed-concurrency-proposal.md`](typed-concurrency-proposal.md) — parked, separate axis

This document proposes the **codegen integration** that turns shell pipelines into typed Go programs. The runtime (the `ssql/typed` package) already exists. What's missing is making `generate go -typed` emit calls into it.

---

## 1. Motivation

The Phase-1 typed runtime delivers **15× faster** execution and **34× less memory** than `ssql.Record`, validated end-to-end on a 10M × 3-join workload. But to get those wins, users today have to **write the Go code themselves** — declare structs, hand-wire the pipeline, build the merge functions for joins.

Phase 2 closes that gap:

```bash
SSQLGO=typed ssql from employees.csv \
    | ssql where -if years ge 5 \
    | ssql join <(ssql from departments.csv) -using dept_id \
    | ssql to csv seniors.csv \
    | ssql generate go > pipeline.go
```

`pipeline.go` becomes a self-contained typed Go program — auto-derived struct types for each CSV, a typed `Where`, a typed `HashJoin`, a typed `WriteCSV`. Same pipeline shape as the user typed; same runtime behaviour as if a human had hand-tuned it.

The user never declares a struct. The pipeline expression IS the program. Performance comes for free.

## 2. Where We Are: The Existing Fragment System

Worth grounding the design in what's already there. Today's `generate go` works like this:

1. Each ssql command supports `SSQLGO=1` mode. When set, instead of *executing* the operation, the command emits a `CodeFragment` JSON object on stdout describing the Go code that would do the same thing:

```go
// cmd/ssql/lib/codefragment.go
type CodeFragment struct {
    Type    string      // "init" | "stmt" | "final" | "func" | "error"
    Var     string      // output variable name (e.g. "filtered0")
    Input   string      // input variable name from previous command
    Code    string      // Go code for this operation
    Imports []string    // required imports
    Command string      // the original ssql command string
    Params  []CodeParam // parameterizable values (CLI flags in generated code)
    // ... plus subprocess function support for process substitution
}
```

2. The pipeline runs as: `ssql from … | ssql where … | ssql join … | ssql to … | ssql generate go`. Each command writes its fragment to stdout; the final `generate go` reads them all from stdin.

3. `generate go` (`cmd/ssql/commands/generate_go.go`) deduplicates imports, threads input/output variable names, and emits a complete `package main` Go file.

This system already supports **process substitution** (the `<(ssql from ...)` form for join inputs becomes a generated function in the output Go), **CLI parameterization** (pipeline filename arguments become `-input` flags in the generated program), and **error fragments** (a command that can't generate emits a `Type: "error"` fragment that aborts assembly).

**Implication for Phase 2:** we don't need a new generator. We need a parallel emission path inside each command that produces typed-style fragments instead of Record-style fragments, plus a new mode in the assembler that knows how to handle them.

## 3. What `-typed` Adds

Three pieces, each localized:

### 3.1 Schema-aware fragments

`CodeFragment` gets new optional fields:

```go
type CodeFragment struct {
    // ... existing fields ...

    // Phase 2: type-flow info, populated only in SSQLGO=typed mode.
    InputSchema  *Schema   `json:"input_schema,omitempty"`
    OutputSchema *Schema   `json:"output_schema,omitempty"`
    StructDefs   []string  `json:"struct_defs,omitempty"`  // Go source for any new types this fragment needs
}

type Schema struct {
    TypeName string         `json:"type_name"` // Go type identifier (e.g. "EmployeeRow")
    Fields   []SchemaField  `json:"fields"`
}

type SchemaField struct {
    Name   string `json:"name"`   // CSV column name (e.g. "dept_id")
    GoName string `json:"go_name"` // Go field name (e.g. "DeptID")
    GoType string `json:"go_type"` // "string", "int64", "float64", "time.Time", "*int64", etc.
}
```

This is purely additive — fragments emitted in `SSQLGO=1` mode leave them empty; fragments emitted in `SSQLGO=typed` mode populate them.

### 3.2 Schema discovery in `from`

When `from FILE.csv` runs in `SSQLGO=typed` mode, it samples the file at code-generation time:

1. Read the header row → field names.
2. Read the next N rows (default 1000) → infer per-column Go types using the same logic ssql already uses for runtime CSV parsing (`int64` if all values parse as integers, `float64` if all parse as floats, `string` otherwise; `bool` for "true"/"false"; `time.Time` for RFC3339 timestamps).
3. Generate a Go struct definition with `ssql:"colname"` tags.
4. Emit a fragment with `OutputSchema` populated, `Code` containing a `typed.ReadCSV[GeneratedType](...)` call, and `StructDefs` containing the struct definition.

The "type inference from samples" approach mirrors what DuckDB and pandas do. Edge case handling:
- All-empty column → `*string` (nullable string)
- Mixed types in samples → `string` (defensive; users can override)
- Values larger than `int64` → `string` with a warning in `StructDefs` comment

### 3.3 Type flow through every command

Each Tier-1 command's typed-mode emitter knows its input/output type relationship:

| Command | Input → Output transform |
|---|---|
| `from FILE.csv` | (none) → `T` derived from CSV |
| `where -if F OP V` | `T` → `T` |
| `include F1 F2 …` | `T` → `T'` (subset of fields, new struct emitted) |
| `exclude F1 F2 …` | `T` → `T'` |
| `rename -as OLD NEW` | `T` → `T'` (field renamed) |
| `join FILE -using F` | `T` + `U` → `T_U` (merged struct emitted) |
| `to csv [FILE]` | `T` → (sink) |
| `to table` | `T` → (sink, prints aligned columns) |

When command N receives the previous command's `OutputSchema` as its `InputSchema`, it can generate type-correct code without seeing the data.

## 4. MVP Scope: Tier 1

The simplest pipelines that cover ~80% of real ssql usage. Each Tier-1 command emits typed fragments; everything else emits an error fragment under `SSQLGO=typed`.

### Tier 1 commands

**`from FILE.csv`**
- Sample header + 1000 rows.
- Emit `type GeneratedRow struct { … }` with `ssql:"colname"` tags.
- Emit `records := typed.ReadCSV[GeneratedRow](filename)`.
- Imports: `github.com/rosscartlidge/ssql/v4/typed`.

**`where -if FIELD OP VALUE`** (literal operators only — no `-if-expr`)
- `OP ∈ {eq, ne, lt, le, gt, ge, contains, startswith, endswith}`.
- Emit `filtered := typed.Where(func(r InputType) bool { return r.<GoField> <op> <typed_literal> })(records)`.
- Type of literal inferred from the input schema's field type.
- Output type = input type (no new struct).

**`join FILE -using FIELD` and `join FILE -on LEFT RIGHT`**
- Single-key joins for MVP. Multi-clause joins deferred to Tier 2.
- Right side is read via process substitution (the existing fragment system handles this).
- Emit a merged struct with the union of both sides' fields, prefixing right-side fields if there's a name collision.
- Emit `joined := typed.HashJoin(left, right, leftKey, rightKey, merge)`.
- Output type = merged struct.

**`to csv [FILE]`**
- Emit `typed.WriteCSV(records, filename)` or `typed.WriteCSVToWriter(records, os.Stdout)` if no filename.
- Sink — no output type.

**`to table`**
- Emit a `for r := range records { fmt.Printf("…", r.F1, r.F2, …) }` loop using known field types for formatting.

### Common pipeline shapes Tier 1 covers

- Read → filter → write
- Read → filter → join → write
- Read → join → filter → join → write
- Read → filter → write to table
- Process-sub joins (existing fragment infrastructure)

That's roughly the test set used in the [LLM API design study](llm-guided-api-design.md). Anything those test cases use, Tier 1 supports.

## 5. Tier 2 (Later)

Add when the MVP ships and we have user feedback:

- **`group-by FIELDS … -count N -sum F N -avg F N`** — emits `typed.GroupBy` with custom multi-aggregator. Group-key struct emitted as the grouping output.
- **`include` / `exclude` / `rename`** — emit `typed.Select` with a fresh struct.
- **`update -set FIELD VALUE`** — literal-value sets only. Becomes `typed.Select` that copies all fields and writes the new one.
- **Multi-clause joins** — same shape, just multiple `HashJoin` calls chained.
- **`distinct` / `sort` / `limit` / `skip` / `offset`** — `typed.Limit`/`typed.Skip`; `sort` likely needs a `typed.SortBy` runtime addition.
- **`union`** — concatenation of streams of the same type.

## 6. Tier 3 (Deferred Indefinitely)

Things that would require structural changes to either the runtime or the codegen:

- **`-if-expr` / `-set-expr`** — would need `expr-lang` → Go AST translation. Substantial; defer until there's clear demand.
- **Signal processing** (`fft`, `convolve`, `spectrogram`) — these consume rows and produce different shapes; the typed runtime would need new entry points.
- **Dynamic field names** — anything where a field name is a runtime value, not a CLI argument.
- **Catalog and SSH sources** — the SSH-pushdown code path is its own world; revisit later.
- **Mixed pipelines** — Record runtime in one stage, typed in another, with conversion at the boundary. Adds significant assembler complexity.

## 7. Fallback Strategy

When a pipeline contains a command without a typed-mode emitter, that command emits an error fragment:

```json
{"type": "error", "command": "ssql update", "error": "ssql generate go -typed: command 'update' not yet supported in typed mode (Tier 2). Re-run with SSQLGO=1 for the Record-based generator."}
```

`generate go` already aborts the build on error fragments. The user sees a clear message naming the offending command, with a one-line workaround (drop `-typed`).

This is intentionally **strict-by-default**. No silent partial optimization, no mixed-mode fallback. If the pipeline doesn't fit the typed model end-to-end, the user gets the Record-based output — which still works fine, it just doesn't get the speedup.

## 8. Worked Example

### 8.1 Input

```bash
SSQLGO=typed ssql from employees.csv \
    | ssql where -if years ge 5 \
    | ssql join <(ssql from departments.csv) -using dept_id \
    | ssql to csv seniors.csv \
    | ssql generate go > pipeline.go
```

Sample data assumed at generation time:

```csv
# employees.csv
id,name,dept_id,years,salary
1,Alice,D01,8,95000
2,Bob,D02,3,65000
…
```

```csv
# departments.csv
dept_id,dept_name,location
D01,Engineering,SF
D02,Sales,NYC
…
```

### 8.2 Generated `pipeline.go` (target output)

```go
// Generated by `ssql generate go -typed` on 2026-04-26.
// Schemas inferred from samples of employees.csv and departments.csv.
package main

import (
    "fmt"
    "log"
    "os"

    "github.com/rosscartlidge/ssql/v4/typed"
)

// Inferred from employees.csv
type EmployeeRow struct {
    ID     int64   `ssql:"id"`
    Name   string  `ssql:"name"`
    DeptID string  `ssql:"dept_id"`
    Years  int64   `ssql:"years"`
    Salary float64 `ssql:"salary"`
}

// Inferred from departments.csv (read via process substitution)
type DepartmentRow struct {
    DeptID   string `ssql:"dept_id"`
    DeptName string `ssql:"dept_name"`
    Location string `ssql:"location"`
}

// Generated for the join result. Right-side fields prefixed where they
// collided with left-side names.
type EmployeeRow_DepartmentRow struct {
    ID       int64
    Name     string
    DeptID   string
    Years    int64
    Salary   float64
    DeptName string
    Location string
}

func departmentsSource() iter.Seq[DepartmentRow] {
    return typed.ReadCSV[DepartmentRow]("departments.csv")
}

func main() {
    flagInput := flag.String("input", "employees.csv", "input CSV file")
    flagOutput := flag.String("output", "seniors.csv", "output CSV file")
    flag.Parse()

    employees := typed.ReadCSV[EmployeeRow](*flagInput)

    filtered := typed.Where(func(r EmployeeRow) bool {
        return r.Years >= 5
    })(employees)

    depts := departmentsSource()

    joined := typed.HashJoin(filtered, depts,
        func(l EmployeeRow) string   { return l.DeptID },
        func(r DepartmentRow) string { return r.DeptID },
        func(l EmployeeRow, r DepartmentRow) EmployeeRow_DepartmentRow {
            return EmployeeRow_DepartmentRow{
                ID: l.ID, Name: l.Name, DeptID: l.DeptID,
                Years: l.Years, Salary: l.Salary,
                DeptName: r.DeptName, Location: r.Location,
            }
        })

    if err := typed.WriteCSV(joined, *flagOutput); err != nil {
        fmt.Fprintf(os.Stderr, "write: %v\n", err)
        os.Exit(1)
    }
    _ = log.Println
}
```

The output is essentially what a human would write by hand from `typed-codelab.md`, plus parameterization for the input/output filenames (which the existing `CodeParam` infrastructure already supports).

### 8.3 Running it

```bash
go run pipeline.go                       # uses defaults (employees.csv → seniors.csv)
go run pipeline.go -input emp_q4.csv     # different input, same pipeline
```

Performance: the same as if the user had written this Go code themselves — i.e. ~15× faster than the same pipeline run as `ssql.Record`-based generated code.

## 9. Schema Discovery Details

The trickiest part of the MVP. A few decisions:

**Sampling strategy.** Read the first 1000 data rows after the header. Per-column types: pick the most-restrictive type that all sampled values parse as, falling back through the chain `int64 → float64 → bool → time.Time → string`. Empty values don't constrain the inference; if the column was *only* empty in samples, default to `*string` (nullable string).

**File availability.** The MVP requires the CSV files to be readable at generation time. If the file is on a remote SSH source, generation fails with a clear error (Tier 3 problem).

**Process substitution joins.** `ssql join <(ssql from departments.csv) -using dept_id` already works in the fragment system: the inner `ssql from` runs in a subshell and emits a fragment that becomes a generated function. For typed mode, the inner `from` does its own schema sampling; the outer `join` reads the inner's `OutputSchema` from the function fragment and uses it as its right-side type.

**Schema overrides.** Two approaches, both deferred past MVP:
- A `--schema-file SCHEMA.json` flag for cases where samples are wrong (e.g., column has all empty values in the first 1000 rows but contains data later).
- A `--schema-rows N` flag to widen the sample window.

For the MVP, we accept the sampling decision and document the behaviour. Users hitting bad inferences can either fall back to `SSQLGO=1` or wait for Tier 2 overrides.

**Type-name conflicts.** Multiple `from`s on the same file should produce the same type name; multiple `from`s on different files with the same field shape should still produce different type names. We default to `<UpperCamelFromFilename>Row` (e.g. `employees.csv` → `EmployeeRow`) and append a numeric suffix on collision.

## 10. Open Questions

1. **Numeric overflow.** A column with values that fit in `int64` for the first 1000 rows but blow past it on row 50000 will silently corrupt at runtime. Detect and warn? Default to `string`?
2. **Type-name strategy.** `EmployeeRow` from `employees.csv` is fine for one file. What about `data.csv`? `Tmp_q4_2025.csv`? Need a sanitizer + collision rule.
3. **Imports.** The generated code imports `flag`, `fmt`, `os`, `github.com/rosscartlidge/ssql/v4/typed`, plus possibly `time` (for time.Time fields), `iter` (for process-sub functions). The fragment system already merges imports — no new mechanism needed, just per-fragment additions.
4. **`fmt`/`log` only used for error paths.** When the pipeline produces no errors at runtime, `fmt` and `log` may be unused, breaking the build. Existing generator handles this; verify still works in typed mode.
5. **Comment provenance.** Should the generated code include the original ssql command as a comment? The fragment system already passes `Command` strings around; it's just a question of formatting in the assembler.
6. **`SSQLGO=typed` vs `-typed` flag.** The doc has been suggesting both. The fragment system uses env vars (`SSQLGO=1`) because each command runs in its own subprocess in the pipeline; an env var propagates naturally, a flag does not. Recommendation: `SSQLGO=typed` is the canonical mechanism; `ssql generate go -typed` could remain as a UX nicety that just sets the env var before re-execing the pipeline if the user prefers (likely overkill for v1).

## 11. Implementation Plan

| Step | Goal | Effort |
|---|---|---|
| 1 | Extend `CodeFragment` with `InputSchema` / `OutputSchema` / `StructDefs`. Pure additive change. | 1 hour |
| 2 | Implement schema sampling in `cmd/ssql/lib` — header + N rows → `Schema`. | 3-4 hours |
| 3 | Add typed-mode emitter to `from_csv.go`. | 2-3 hours |
| 4 | Add typed-mode emitter to `where.go` (literal operators only). | 2-3 hours |
| 5 | Add typed-mode emitter to `join.go` (single-key, single-clause). | 1 day |
| 6 | Add typed-mode emitters to `to_csv.go` and `to_table.go`. | 2-3 hours |
| 7 | Add typed-mode assembler path to `generate_go.go` — collect StructDefs, emit at top, swap import. | 4-6 hours |
| 8 | End-to-end tests: a handful of Tier-1 pipelines, generate the Go, build it, run it, assert output matches the Record-mode equivalent. | 1 day |
| 9 | Make every other command emit a Tier-1-not-supported error fragment under `SSQLGO=typed`. | 1-2 hours |
| 10 | Update README, codelab, and the typed-package-proposal.md to reflect Phase 2 shipped. | 2-3 hours |

**Total budget**: ~1 week of focused work for Tier 1 ship.

## 11a. Measured Results (Tier 1, 2026-04-26)

The codegen-comparison benchmark in `cmd/ssql/codegen_bench_test.go`
generates the **same pipeline** two ways and reports wall time and
peak RSS for each:

```bash
# Pipeline used in the bench:
SSQLGO={1|typed} ssql from employees.csv \
    | ssql where -if years ge 5 \
    | ssql join departments.csv -using dept_id \
    | ssql to csv \
    | ssql generate go
```

Workload: 1,000,000-row `employees.csv` joined against a 1,000-row
`departments.csv` on `dept_id`, filtered on `years >= 5`. Both
generated programs are compiled with `go build -ldflags "-s -w"` and
run via `/usr/bin/time -v` to capture peak resident set size.

| Mode             | Wall time | Peak RSS | Source size |
|------------------|----------:|---------:|------------:|
| CLI pipeline     | 3.08 s    | 33 MB    | (no source) |
| Record codegen   | 2.69 s    | 910 MB   | 1.2 KB      |
| **Typed codegen**| **0.77 s**| **8.7 MB**| 2.0 KB     |
| Speedup vs CLI   | **4.0× faster** | — |  |
| Speedup vs Record| **3.5× faster** | **104× less memory** | |

Three execution models, same shell pipeline:

- **CLI pipeline** is what users run interactively: each command in
  its own process, JSONL on the pipes between stages. Per-process
  memory is small (33 MB) because streaming through pipes never
  materializes everything in one address space, but the wall-time
  cost of process startup + per-row JSONL serialize/deserialize is
  substantial.
- **Record codegen** (`SSQLGO=1 … | ssql generate go`) collapses to a
  single Go process with native iterators and no inter-process
  serialization — wall time drops 13%, but in-process Record
  (`map[string]any`) state explodes peak RSS to 900+ MB.
- **Typed codegen** (`SSQLGO=typed … | ssql generate go`) eliminates
  both costs: single process *and* stack-allocated struct
  representation. **Best on every dimension simultaneously.**

The wall-time speedup (3.5–4× over the alternatives) is below the
15× the typed runtime delivers in pure micro-bench mode because the
codegen comparison includes process startup, CSV read latency, and
disk I/O that wash out some of the in-process advantage. The memory
ratio (104×) vs Record codegen is the real story: typed's
stack-allocated structs stay under 10 MB while Record's per-row
`map[string]any` blows past 900 MB on the same workload.

The 800-byte source-size delta is the auto-derived struct types
(`EmployeesRow`, `DepartmentsRow`, `EmployeesRow_DepartmentsRow`).
That's the *only* code the user didn't write.

Reproduce on your own hardware:

```bash
go test ./cmd/ssql/ -run TestCodegenBench -timeout 10m -v
```

The test materializes its dataset once under
`$TMPDIR/ssql-codegen-bench`, so re-runs are cheap.

## 12. What This Proposal Doesn't Try to Do

- It doesn't change `ssql.Record` codegen. `SSQLGO=1` keeps working exactly as today.
- It doesn't introduce a query optimizer or rewriter. Whatever pipeline the user types is what gets generated.
- It doesn't try to be schema-flexible. A typed pipeline knows its types at generation time. Schema-on-read is a Record use case.
- It doesn't tackle Tier 2 or Tier 3. Each is its own follow-up proposal.
- It doesn't introduce concurrency. The concurrency proposal is a separate, parked axis.

The MVP is deliberately small: Tier 1 covers the pipelines users actually write, the existing fragment infrastructure does most of the heavy lifting, and the runtime (`ssql/typed`) is already validated. The remaining work is mostly plumbing.

---

## Decision Points Before Implementation

The maintainer should confirm before code:

- [ ] Tier 1 scope as enumerated in §4
- [ ] `SSQLGO=typed` as the activation mechanism (vs a flag on `generate go`)
- [ ] Schema sampling defaults: 1000 rows, fail-closed on remote sources
- [ ] Type-name strategy: `<UpperCamelFromFilename>Row` with numeric collision suffix
- [ ] Strict fallback (any unsupported command = whole pipeline errors out)
- [ ] No mixed-mode (Record + typed in the same pipeline) in v1
