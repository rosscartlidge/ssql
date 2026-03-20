# Catalog Code Generation

**Status:** Implemented (Option B)
**Date:** March 2026
**Depends on:** `from catalog` (implemented), code generation system

## Problem

`from catalog` works in execution mode but its `-generate` output is a placeholder that exits with an error. Users who prototype distributed pipelines via CLI can't generate standalone Go programs from them.

## What Needs to Be Generated

A catalog pipeline has three phases:

1. **Read and prune the catalog** — parse CSV, apply `-if` filters
2. **Process each shard** — SSH to host, run remote command, stream JSONL back
3. **Concatenate results** — merge shard outputs into a single stream

### Generated Code Structure

```go
package main

import (
    "encoding/csv"
    "fmt"
    "iter"
    "os"
    "os/exec"
    "strings"

    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Phase 1: Read and prune catalog
    entries := readCatalog("shards.csv")
    entries = pruneEntries(entries, "date", "ge", "2025-03-01")

    // Phase 2: Process shards, concatenating into single stream
    records := processCatalogShards(entries, "ssql", "")

    // Phase 3: Downstream pipeline (from other commands in the pipeline)
    sorted := ssql.Chain(
        ssql.GroupByFields("_group", "service"),
        ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
            "errors": ssql.Count(),
        }),
        ssql.SortRecords([]ssql.OrderField{{Field: "errors", Desc: true}}),
    )(records)
    ssql.DisplayTable(sorted, 50)
}
```

## Design Options

### Option A: Generate All Logic Inline

Generate the catalog parsing, pruning, SSH execution, and JSONL reading directly in the generated program.

**Pros:**
- Self-contained — no runtime dependency on catalog infrastructure
- Users can modify the generated code freely

**Cons:**
- 100+ lines of generated boilerplate (CSV parsing, pruning, SSH piping, JSONL reading)
- Violates "generated code should be readable" principle
- Duplicates logic that already exists in the ssql package

### Option B: Add Catalog Runtime to ssql Package (Recommended)

Export the catalog functions as a runtime library in `ssql` or a sub-package, then generate code that calls them.

**New exports needed:**

```go
// In ssql package or ssql/catalog sub-package

// CatalogEntry represents a shard in a catalog.
type CatalogEntry struct {
    Host     string
    Path     string
    Format   string
    Metadata map[string]string
}

// CatalogFilter represents a pruning condition.
type CatalogFilter struct {
    Field    string
    Operator string
    Value    string
}

// ReadCatalog parses a catalog CSV file.
func ReadCatalog(filename string) ([]CatalogEntry, error)

// PruneCatalog filters entries based on range and exact-value conditions.
func PruneCatalog(entries []CatalogEntry, filters []CatalogFilter) []CatalogEntry

// ProcessCatalogShards connects to each shard via SSH and returns a
// concatenated record stream. remoteBin is "ssql" or "ssql_gpu".
// pipelineArgs are the push-down commands (already split on "+").
// shardField, if non-empty, adds a provenance field to each record.
func ProcessCatalogShards(entries []CatalogEntry, remoteBin string, shardField string, pipelineArgs [][]string) iter.Seq[ssql.Record]
```

**Generated code would look like:**

```go
entries, err := ssql.ReadCatalog("shards.csv")
if err != nil {
    fmt.Fprintf(os.Stderr, "Error: %v\n", err)
    os.Exit(1)
}

entries = ssql.PruneCatalog(entries, []ssql.CatalogFilter{
    {Field: "date", Operator: "ge", Value: "2025-03-01"},
})

records := ssql.ProcessCatalogShards(entries, "ssql", "_shard", [][]string{
    {"where", "-if", "status", "ge", "400"},
})
```

**Pros:**
- Clean, readable generated code (5-10 lines for the catalog part)
- Consistent with how other commands generate code (call ssql package functions)
- Follows "CLI commands must use ssql package primitives" rule
- Catalog logic maintained in one place

**Cons:**
- Requires exporting functions that are currently internal to `cmd/ssql/commands`
- `ProcessCatalogShards` depends on `os/exec` and SSH — heavier than typical ssql functions

### Option C: Hybrid — Export Read/Prune, Inline SSH

Export `ReadCatalog` and `PruneCatalog` but generate the SSH loop inline. This keeps the ssql package free of `os/exec` dependencies.

```go
entries, err := ssql.ReadCatalog("shards.csv")
// ... prune ...

// Generated SSH loop
var allRecords []iter.Seq[ssql.Record]
for _, entry := range entries {
    cmd := exec.Command("ssh", entry.Host,
        "ssql from "+entry.Path+" | ssql where -if status ge 400")
    stdout, _ := cmd.StdoutPipe()
    cmd.Start()
    allRecords = append(allRecords, ssql.ReadJSONFromReader(stdout))
}
records := ssql.Concat(allRecords...)
```

**Pros:**
- Keeps `os/exec` out of the ssql package
- Read/Prune are pure functions, easy to export
- SSH loop is visible and modifiable

**Cons:**
- More generated code than Option B
- SSH error handling and cleanup adds complexity
- Need to handle cmd.Wait() properly (defer or after iteration)

## Recommendation

**Option B** is the cleanest approach and follows existing patterns. The SSH/exec dependency isn't a concern since `ssql` already has `ExecCommand()` in `io.go` which uses `os/exec`.

## Implementation Steps

1. Move `catalogEntry`, `catalogFilter`, `readCatalog`, `pruneCatalog`, `catalogEntryMatches`, `rangeMatches`, `applyStringOp` from `cmd/ssql/commands/from.go` to a new file (either `catalog.go` in the ssql package or `cmd/ssql/lib/catalog.go`)
2. Export the types and functions
3. Add `ProcessCatalogShards()` that encapsulates the SSH loop + JSONL reading + enrichment
4. Update `cmd/ssql/commands/from.go` to call the exported functions (dedup)
5. Update `generateFromCatalogCode()` to emit calls to the exported functions
6. Add generation tests that verify the generated code compiles and runs

## Where to Put the Code

**Option: `cmd/ssql/lib/runtime/`** — already exists for generated code runtime support. This keeps catalog-specific code out of the core ssql package while making it available to generated programs.

**Option: `ssql` package directly** — simpler, follows pattern of `ExecCommand()`. The catalog is a data source like CSV or JSON, so it fits alongside `ReadCSV()` and `ReadJSON()`.

Either works. The ssql package is the simpler path since `ReadCatalog` is analogous to `ReadCSV`.
