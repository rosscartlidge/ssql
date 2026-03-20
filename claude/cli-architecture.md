# CLI Tools Architecture (autocli v4.0.0+)

ssql CLI uses **autocli v4.0.0+** for native subcommand support with auto-generated help and tab completion.

## Architecture Overview

- `cmd/ssql/main.go` - All subcommands defined using autocli builder API
- `cmd/ssql/helpers.go` - Shared utilities (comparison operators, aggregation, extractNumeric, chainRecords)
- `cmd/ssql/version/version.txt` - Version string (manually maintained)
- All commands use context-based flag access: `ctx.GlobalFlags` and `ctx.Clauses`

## CLI Flag Design Principles

### 1. Prefer Named Flags Over Positional Arguments
- Named flags are self-documenting and enable better tab completion
- Exception: Commands with a single, obvious positional argument

### 2. Use Multi-Argument Flags Properly
```go
Flag("-if").
    Arg("field").FieldsFromFlag("FILE").Done().
    Arg("operator").Completer(&cf.StaticCompleter{Options: operators}).Done().
    Arg("value").FieldValuesFrom("FILE", "field").Done().
```
**Use `FieldsFromFlag`/`FieldValuesFrom` whenever a file flag exists — NEVER use `NoCompleter` for field names that can be derived from data files.**

### 3. Use `.Accumulate()` for Repeated Flags
When a flag can appear multiple times (e.g., `-if age gt 30 -if dept eq Sales`)

### 4. Provide Completers for Constrained Arguments
Use `StaticCompleter` for known options, `FileCompleter` with patterns for file paths.

### 5. Avoid In-Argument Delimiters
- BAD: `-rename "old:new"` (requires delimiter parsing)
- GOOD: `-as old new` (framework separates args)

### 6. Use Brace Expansion for File Completion Patterns
- CORRECT: `Pattern: "*.{json,jsonl}"`
- WRONG: `Pattern: "*.json,*.jsonl"`

### 7. Follow Unix Philosophy: Support stdin/stdout
All data processing commands MUST support stdin/stdout for Unix pipelines.

### 8. All Commands MUST Have Examples
Every CLI command MUST include 2-3 usage examples using `.Example()` calls.

### 9. Automatic Pipeline Field Caching (autocli v4.1.0)
When `FileCompleter` completes to a single data file, it automatically extracts and caches field names for downstream commands.

### 10. Field Value Completion with FieldValuesFrom()
Complete with actual data values from files, not just field names.
```go
Flag("-if").
    Arg("field").FieldsFromFlag("FILE").Done().
    Arg("operator").Completer(&cf.StaticCompleter{Options: [...]}).Done().
    Arg("value").FieldValuesFrom("FILE", "field").Done().
    Done()
```

## Subcommand Pattern

```go
Subcommand("command-name").
    Description("Brief description").
    Handler(func(ctx *cf.Context) error {
        // 1. Extract flags from ctx.GlobalFlags (for Global flags)
        // 2. Extract clause flags from ctx.Clauses (for Local flags)
        // 3. For -- separator: ctx.RemainingArgs
        // 4. Perform command operation
        return nil
    }).
    Flag("-myflag").String().Global().Help("Description").Done().
    Done().
```

## Key Patterns
- **Global flags**: `ctx.GlobalFlags["-flagname"]` - applies to entire command
- **Local flags**: `ctx.Clauses[i].Flags["-flagname"]` - applies per clause (with `+` separator)
- **Accumulated flags**: `.Accumulate()` and access as `[]any` slice
- **-- separator**: `ctx.RemainingArgs` for everything after `--` (requires autocli v3.0+)
- **Type assertions**: All flag values are `interface{}`, cast appropriately

## Important Lessons Learned

1. **Release with replace directive fails** - always remove before tagging
2. **Version display** - autocli `.Version()` adds "v" prefix automatically
3. **Version subcommand needed** - autocli doesn't auto-add `-version` flag
4. **Context-based flag access** - Use `ctx.GlobalFlags` and `ctx.Clauses` for flexibility
5. **-- separator support** - Requires autocli v3.0+
