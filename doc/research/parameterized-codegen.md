# Parameterized Code Generation

Reference: DFC063
Created: 2026-03-20
Last modified: 2026-03-20

[Back to Index](./README.md)

**Status:** Implemented (v4.28.0, March 2026)
**Date:** March 2026
**Decision:** Option A — always parameterize (zero-cost defaults)

## Problem

Generated code hardcodes all literal values from the original CLI pipeline:

```bash
ssql from catalog /home/rossc/src/ssql/test-data/test-catalog.csv \
  -- where -if timestamp ge 2025-02-01 -if timestamp le 2025-02-28 \
  | ssql group-by region -count count \
  | ssql to table
```

Produces:

```go
entries, err := ssql.ReadCatalog("/home/rossc/src/ssql/test-data/test-catalog.csv")
entries = ssql.PruneCatalog(entries, []ssql.CatalogFilter{
    {Field: "timestamp", Operator: "ge", Value: "2025-02-01"},
    {Field: "timestamp", Operator: "le", Value: "2025-02-28"},
})
```

Every value — file paths, filter thresholds, field names, limit counts — is baked in. To reuse the program with a different catalog file or date range, users must edit the generated source.

## Goal

Generated programs should accept command-line flags for values that are likely to change between runs, while keeping sensible defaults from the original pipeline.

**Before:**
```bash
go run gen.go   # always reads test-catalog.csv, always filters Feb 2025
```

**After:**
```bash
go run gen.go                                              # defaults from original pipeline
go run gen.go -catalog prod-shards.csv                     # different catalog
go run gen.go -timestamp-ge 2025-06-01 -timestamp-le 2025-06-30  # different date range
go run gen.go -catalog prod.csv -timestamp-ge 2025-06-01   # both
```

## What Is Parameterized

### Tier 1: File Paths (Always parameterized)

These are almost always different between dev and prod:

| Command | Flag | Variable |
|---------|------|----------|
| `from csv/tsv/json/jsonl/arrow/wav/xlsx FILE` | `-input` | `*flagInput` |
| `from catalog FILE` | `-catalog` | `*flagCatalog` |
| `from ssh HOST PATH` | `-host`, `-path` | `*flagHost`, `*flagPath` |
| `join FILE` | `-join` | `*flagJoin` |
| `to csv/tsv/json/arrow/wav/xlsx FILE` | `-output` | `*flagOutput` |
| `to chart FILE` | `-output` | `*flagOutput` |

**stdin/stdout commands are not parameterized** — when no file is specified, no flag is emitted.

### Tier 2: Filter/Threshold Values (Parameterized when present)

| Command | Flag | Type | Variable |
|---------|------|------|----------|
| `from catalog -if date ge 2025-02-01` | `-date-ge` | string | `*flagDateGe` |
| `from catalog -if date le 2025-02-28` | `-date-le` | string | `*flagDateLe` |
| `limit 100` | `-limit` | int | `*flagLimit` |
| `offset 50` | `-offset` | int | `*flagOffset` |
| `top 10 -field score` | `-top` | int | `*flagTop` |

### Tier 3: Structural Parameters (Not parameterized)

These change the program's structure and can't be swapped via flags:

- Group-by field names (`-by dept`)
- Sort field names (`-field salary`)
- Aggregation functions (`-count`, `-sum`)
- Expression strings (`-expr 'sum(salary * bonus)'`)
- Output format (`to table` vs `to csv`)
- Window specifications (`-partition`, `-order`, `-rows`)

Changing these would require regenerating the code.

### Not Yet Parameterized

- `where -if` filter values — requires runtime parsing for numeric comparisons (`strconv.ParseFloat`). The catalog `-if` filters work because `CatalogFilter.Value` is already a string. A future phase could add this with a small parse helper.
- `union -file` — complex due to process substitution patterns.

## Implementation

### Fragment-Level Parameter Declaration

Each code fragment declares its parameterizable values via a `Params` field:

```go
type CodeParam struct {
    Name    string `json:"name"`           // Flag name (e.g., "input", "catalog")
    Default string `json:"default"`        // Default value from original pipeline
    Help    string `json:"help"`           // Flag help text
    VarName string `json:"var"`            // Go variable name used in code (e.g., "flagInput")
    Type    string `json:"type,omitempty"` // "string" (default), "int"
}

type CodeFragment struct {
    // ... existing fields ...
    Params []CodeParam `json:"params,omitempty"`
}
```

### How Commands Emit Parameters

**Example — `from csv`:**

```go
params = append(params, lib.CodeParam{
    Name: "input", Default: filename, Help: "input CSV file", VarName: "flagInput",
})
code = `records, err := ssql.ReadCSV(*flagInput)
    if err != nil { ... }`
frag := lib.NewInitFragment("records", code, imports, getCommandString())
frag.Params = params
```

**Example — `from catalog -if timestamp ge 2025-02-01`:**

```go
params = append(params, lib.CodeParam{
    Name: "catalog", Default: catalogFile, Help: "catalog CSV file", VarName: "flagCatalog",
})
for _, f := range filters {
    flagName := f.Field + "-" + f.Operator
    varName := "flag" + flagVarName(f.Field) + flagVarName(f.Operator)
    params = append(params, lib.CodeParam{
        Name: flagName, Default: f.Value, Help: f.Field + " " + f.Operator + " filter", VarName: varName,
    })
}
```

**Example — `limit 100`:**

```go
params := []lib.CodeParam{
    {Name: "limit", Default: "100", Help: "maximum number of records", VarName: "flagLimit", Type: "int"},
}
code := fmt.Sprintf("%s := ssql.Limit[ssql.Record](*flagLimit)(%s)", outputVar, inputVar)
```

### How `generate go` Assembles Parameters

The `AssembleCodeFragments` function:

1. Calls `collectParams()` to gather and deduplicate params from all fragments (including func body fragments)
2. Adds `"flag"` to the import set (only when params exist)
3. Emits a `var (...)` block with `flag.String()` or `flag.Int()` declarations
4. Adds `flag.Parse()` as the first line of `main()`

**Actual generated output:**

```go
package main

import (
    "flag"
    "fmt"
    "github.com/rosscartlidge/ssql/v4"
    "os"
)

var (
    flagCatalog     = flag.String("catalog", "/home/rossc/src/ssql/test-data/test-catalog.csv", "catalog CSV file")
    flagTimestampGe = flag.String("timestamp-ge", "2025-02-01", "timestamp ge filter")
    flagTimestampLe = flag.String("timestamp-le", "2025-02-28", "timestamp le filter")
)

func main() {
    flag.Parse()

    entries, err := ssql.ReadCatalog(*flagCatalog)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
    entries = ssql.PruneCatalog(entries, []ssql.CatalogFilter{
        {Field: "timestamp", Operator: "ge", Value: *flagTimestampGe},
        {Field: "timestamp", Operator: "le", Value: *flagTimestampLe},
    })
    // ...
}
```

### Flag Name Collision

Multiple commands may produce parameters with the same name. Resolution via `collectParams()`:

- First occurrence gets the bare name: `-input`
- Subsequent occurrences get numbered suffixes: `-input2`, `-input3`

In practice, different commands use different names (`-input`, `-join`, `-output`) so collisions are rare.

### Flag Variable Naming

The `flagVarName()` helper converts flag name segments to Go-safe camelCase:

- `"timestamp"` → `"Timestamp"`
- `"ge"` → `"Ge"`
- `"date-start"` → `"DateStart"`

Combined with the `"flag"` prefix: `-timestamp-ge` → `flagTimestampGe`.

### Zero-Cost When Unused

When no parameters are present (e.g., stdin-only pipeline), the assembler emits no flag infrastructure — no `import "flag"`, no `var` block, no `flag.Parse()`. The generated code is identical to before this feature.

## Files Modified

| File | Changes |
|------|---------|
| `cmd/ssql/lib/codefragment.go` | `CodeParam` struct, `Params` field on `CodeFragment`, `collectParams()`, flag emission in `AssembleCodeFragments` |
| `cmd/ssql/commands/helpers.go` | `flagVarName()` helper |
| `cmd/ssql/commands/from.go` | All format handlers (CSV, TSV, JSON, Arrow, WAV, XLSX), catalog with filter params, SSH with host/path params |
| `cmd/ssql/commands/to.go` | All output format handlers (CSV, TSV, JSON, Arrow, WAV, XLSX, chart/heatmap) |
| `cmd/ssql/commands/join.go` | Join file param (regular files) |
| `cmd/ssql/commands/limit.go` | Integer param |
| `cmd/ssql/commands/offset.go` | Integer param |
| `cmd/ssql/commands/top.go` | Integer param |
| `cmd/ssql/generation_test.go` | Updated existing tests + `TestParameterizedCodeGeneration` + `TestParameterizedStdinNoFlags` |

## Future Work

### `where` Filter Value Parameters

The `where` command's `-if` conditions embed comparison values directly in the filter function. Parameterizing these requires handling the type mismatch:

- String comparisons (`eq`, `ne`, `contains`, etc.) — straightforward, use `*flagFieldOp` directly
- Numeric comparisons (`gt`, `ge`, `lt`, `le`) — need `strconv.ParseFloat(*flagFieldOp, 64)` at startup

A clean approach: add a small `mustParseFloat` helper to the ssql package or runtime package, and emit a pre-parse block after `flag.Parse()`.

### `union -file` Parameters

Union with multiple `-file` flags or process substitution is structurally complex. Could be parameterized with indexed flags (`-union1`, `-union2`).
