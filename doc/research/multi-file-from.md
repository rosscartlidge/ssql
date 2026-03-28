# Multi-file `ssql from` — Design Doc

## Problem

Currently `ssql from csv file.csv` reads one file. Real-world usage often requires reading many CSVs (and other formats) at once. Users need:
- `ssql from csv 1.csv 2.csv 3.csv ...`
- Different headers across files merged into a unified schema
- Parallel reading for performance
- Pushdown filtering per file (like `from ssh` and `from catalog`)

## Phase 1: Core multi-file support (sequential)

### 1. Make FILE variadic

Change `String()` to `StringSlice().Variadic()` on `from csv`, `from tsv`, `from json`, `from jsonl`, `from arrow`, `from parquet`. Single-file remains backward compatible.

Follows the `merge` command pattern for variadic file args.

### 2. Schema merging

Add `MergeSchemas()` to `lib/schema.go`:
- Union of all field sets
- Type widening on conflicts: `int64 + float64 → float64`, `int + string → string`
- Field order: first file's order, then appended new fields from subsequent files
- Missing fields naturally absent in JSONL output (handled by `GetOr` downstream)

### 3. Package-level multi-read functions

Add to `io.go`:
```go
func ReadMultiCSV(filenames []string, config ...CSVConfig) (iter.Seq[Record], error)
func ReadMultiTSV(filenames []string) (iter.Seq[Record], error)
func ReadMultiJSON(filenames []string) (iter.Seq[Record], error)
// etc.
```

Open files lazily (next file when previous exhausted) to avoid fd limits. Pre-scan headers for merged schema.

### 4. `-source` flag

Optional flag adds a string field with the source filename to each record. Like catalog's `-shard-field`. Useful for provenance tracking.

### 5. Bare `from` form

`ssql from 1.csv 2.csv` — variadic, infer format from first file's extension, error if subsequent files have different extensions.

### 6. Code generation

- `generate go` emits `ssql.ReadMultiCSV(files)`
- `generate sql` emits `read_csv_auto(['f1', 'f2', ...])`

## Phase 2: Pushdown

### 7. `--` pushdown syntax

```bash
ssql from csv 1.csv 2.csv -- where -if age gt 25
```

Everything after `--` goes to `ctx.RemainingArgs`. Spawns sub-pipeline per file (`ssql from csv FILE | ssql <pushed-args>`), merges JSONL output. Follows `from ssh`/`from catalog` pattern exactly.

Multi-step pushdown via `+` separator: `-- where -if x eq 1 + group-by dept`.

## Phase 3: Parallel reading

### 8. Goroutine-per-file reader

- One goroutine per file, capped at `runtime.NumCPU()`
- Preserves file order by default (reads in parallel, yields file1 before file2)
- Optional `-parallel` flag for interleaved output (faster, non-deterministic)
- Uses `errgroup` for error propagation

## Key Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Variadic mechanism | `StringSlice().Variadic()` | Follows `merge` command pattern |
| Schema merge | Union with type widening | Matches SQL UNION semantics |
| Ordering | Sequential by default | Deterministic, simpler |
| Missing fields | Absent in JSON output | Natural JSONL behavior |
| Pushdown | `--` separator | Follows `from ssh`/`from catalog` patterns |
| Bare form | First-file format inference | Simple, prevents mixed-format confusion |
| Glob support | Not needed — shell does it | `ssql from csv *.csv` works once FILE is variadic |

## Critical Files

- `cmd/ssql/commands/from.go` — FILE flags, handlers, code generation
- `io.go` — `ReadMultiCSV` and friends
- `cmd/ssql/lib/schema.go` — `MergeSchemas`
- `cmd/ssql/commands/merge.go` — reference for variadic pattern
- `catalog.go` — reference for sequential multi-source, pushdown via subprocess
