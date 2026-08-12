# `from` Subcommands: Mirroring `to`

Reference: DFC055
Created: 2026-03-10
Last modified: 2026-03-10

[Back to Index](./README.md)

**Status:** Implemented (v4.27.0)
**Date:** March 2026

## Problem Statement

`to` has clean subcommands — `to csv`, `to json`, `to table`, `to chart`, etc. Each subcommand has its own flags and help. `from` is a monolith: one handler with a big switch statement that detects format from file extensions, and accumulating flags that only apply to some formats (`-channel` for WAV, `-sheet` for XLSX, `-type` for CSV).

As we add `from catalog` and `from ssh`, this gets worse. Catalog needs `-merge`, `-on-error`, `-parallel`, `-shard-field`. SSH needs host and path arguments. These have nothing to do with CSV parsing or WAV channels. They're different commands sharing a flag namespace.

## Current State

```go
// from.go — one handler, 600+ lines
RegisterFrom(cmd) → single Subcommand("from") with:
    -format csv|tsv|json|jsonl|arrow|wav|xlsx
    -channel (WAV only)
    -sheet (XLSX only)
    -type field type (CSV only)
    -default-type (CSV only)
    FILE
    -- command args (exec mode)
```

Flags like `-channel` are meaningless for CSV. `-sheet` is meaningless for WAV. The help text lists everything, making it harder to understand.

## Proposed Structure

Mirror `to` exactly:

```bash
# Format subcommands — one per format
ssql from csv data.csv
ssql from tsv data.tsv
ssql from json data.json
ssql from jsonl data.jsonl
ssql from arrow data.arrow
ssql from wav audio.wav -channel 0
ssql from xlsx workbook.xlsx -sheet Sales

# Operational subcommands — distinct data sources
ssql from command -- ps aux
ssql from ssh prod-server /data/logs.csv
ssql from catalog shards.csv -merge timestamp -on-error skip
```

Each subcommand gets only the flags it needs:

```
ssql from csv -help
  FILE          Input CSV file (or stdin)
  -type         Override field type: -type zipcode string
  -default-type Default type for all fields (auto|string|int|float)
  -generate     Generate Go code

ssql from wav -help
  FILE          Input WAV file
  -channel      Extract specific channel (0=left, 1=right)
  -generate     Generate Go code

ssql from catalog -help
  FILE          Catalog CSV file
  -merge        K-way merge by field (shards must be pre-sorted)
  -on-error     Error handling: fail (default), skip, retry
  -shard-field  Add provenance field to each record
  -parallel     Max concurrent SSH connections (default: 4)
  -remote       Pipeline to push down to each shard
  -where        Partition filter: -where field op value
  -generate     Generate Go code

ssql from ssh -help
  HOST          SSH host (from ~/.ssh/config)
  PATH          Remote file path
  -format       Remote file format (default: infer from extension)
  -remote       Pipeline to push down to remote
  -generate     Generate Go code
```

## Bare `from` for Convenience

Keep `ssql from data.csv` working as a shorthand that infers format from extension. This is too convenient to lose for interactive use.

```bash
# These are equivalent:
ssql from data.csv           # shorthand — infers csv from extension
ssql from csv data.csv       # explicit

# Subcommand required when format can't be inferred:
ssql from csv                # stdin as CSV (was: ssql from -format csv)
ssql from jsonl              # stdin as JSONL

# Subcommand required for non-file sources:
ssql from command -- ps aux
ssql from ssh prod /data/logs.csv
ssql from catalog shards.csv
```

The bare `from` handler becomes trivial — detect extension, delegate to the right subcommand handler.

## Implementation

### Registration pattern (matches `to`)

```go
func RegisterFrom(cmd *cf.CommandBuilder) *cf.CommandBuilder {
    fromCmd := cmd.Subcommand("from").
        Description("Read data from files, commands, or remote sources")

    // Format subcommands
    registerFromCSV(fromCmd)
    registerFromTSV(fromCmd)
    registerFromJSON(fromCmd)
    registerFromJSONL(fromCmd)
    registerFromArrow(fromCmd)
    registerFromWAV(fromCmd)
    registerFromXLSX(fromCmd)

    // Operational subcommands
    registerFromCommand(fromCmd)
    registerFromSSH(fromCmd)
    registerFromCatalog(fromCmd)

    // Bare "from FILE" handler — infers format from extension
    fromCmd.
        Flag("FILE").
        String().
        Completer(&cf.FileCompleter{Pattern: "*.{csv,tsv,json,jsonl,arrow,wav,xlsx}"}).
        Global().
        Default("").
        Help("Input file (format inferred from extension)").
        Done().
        Handler(func(ctx *cf.Context) error {
            // Detect extension, delegate to format-specific handler
        }).
        Done()

    fromCmd.Done()
    return cmd
}
```

### Individual subcommands get focused flags

```go
func registerFromCSV(cmd *cf.SubcommandBuilder) {
    cmd.Subcommand("csv").
        Description("Read CSV file or stdin").
        Example("ssql from csv data.csv | ssql to table", "Read CSV file").
        Example("cat data.csv | ssql from csv | ssql to json", "Read CSV from stdin").
        Example("ssql from csv data.csv -type zipcode string", "Force zipcode to string").

        Flag("FILE").
        String().
        Completer(&cf.FileCompleter{Pattern: "*.csv"}).
        Global().
        Default("").
        Help("Input CSV file (or stdin if not specified)").
        Done().

        Flag("-type", "-t").
        Arg("field").Completer(cf.NoCompleter{Hint: "<field-name>"}).Done().
        Arg("type").Completer(&cf.StaticCompleter{Options: []string{"string", "int", "float", "bool", "auto"}}).Done().
        Accumulate().
        Global().
        Help("Override type for field: -type zipcode string").
        Done().

        Flag("-default-type", "-dt").
        // ...
        Done().

        Flag("-generate", "-g").
        // ...
        Done().

        Handler(fromCSVHandler).
        Done()
}

func registerFromWAV(cmd *cf.SubcommandBuilder) {
    cmd.Subcommand("wav").
        Description("Read WAV audio file").
        Example("ssql from wav audio.wav | ssql fft -field amplitude", "Read WAV for FFT").
        Example("ssql from wav stereo.wav -channel 0", "Read left channel only").

        Flag("FILE").
        String().
        Completer(&cf.FileCompleter{Pattern: "*.wav"}).
        Global().
        Help("Input WAV file").
        Done().

        Flag("-channel", "-ch").
        Int().
        Global().
        Default(-1).
        Help("Extract specific channel (0=left, 1=right). Default: mix to mono.").
        Done().

        // No -type, -default-type, -sheet — irrelevant for WAV

        Handler(fromWAVHandler).
        Done()
}

func registerFromCatalog(cmd *cf.SubcommandBuilder) {
    cmd.Subcommand("catalog").
        Description("Read shards from a catalog file across multiple machines").
        Example("ssql from catalog shards.csv | ssql to table", "Read all shards").
        Example("ssql from catalog shards.csv -where date ge 2025-03-01", "Partition pruning").
        Example("ssql from catalog shards.csv -merge timestamp -on-error skip", "Ordered merge, skip failures").

        Flag("FILE").
        String().
        Completer(&cf.FileCompleter{Pattern: "*.csv"}).
        Global().
        Help("Catalog CSV file mapping shards to hosts").
        Done().

        Flag("-merge").
        String().
        Global().
        Help("K-way merge by field (shards must be pre-sorted)").
        Done().

        Flag("-on-error").
        String().
        Global().
        Default("fail").
        Completer(&cf.StaticCompleter{Options: []string{"fail", "skip", "retry"}}).
        Help("Error handling: fail (default), skip, retry").
        Done().

        Flag("-shard-field").
        String().
        Global().
        Help("Add field showing shard origin (host:path)").
        Done().

        Flag("-parallel").
        Int().
        Global().
        Default(4).
        Help("Max concurrent SSH connections").
        Done().

        Flag("-remote").
        String().
        Global().
        Help("Pipeline to push down to each shard").
        Done().

        Flag("-where").
        Arg("field").Completer(cf.NoCompleter{Hint: "<field-name>"}).Done().
        Arg("operator").Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt", "ge", "lt", "le"}}).Done().
        Arg("value").Completer(cf.NoCompleter{Hint: "<value>"}).Done().
        Accumulate().
        Global().
        Help("Partition filter (prunes shards before reading)").
        Done().

        Handler(fromCatalogHandler).
        Done()
}
```

## Migration

This is a **breaking change** for anyone using `from` flags like `-format`, `-channel`, `-sheet`. But since bare `from FILE` still works (extension inference), the most common usage is unaffected.

| Old | New | Bare `from` still works? |
|-----|-----|--------------------------|
| `ssql from data.csv` | `ssql from data.csv` or `ssql from csv data.csv` | Yes |
| `ssql from data.wav -channel 0` | `ssql from wav data.wav -channel 0` | No — `-channel` moves to `from wav` |
| `ssql from -format csv` (stdin) | `ssql from csv` (stdin) | No — use subcommand |
| `ssql from data.xlsx -sheet Sales` | `ssql from xlsx data.xlsx -sheet Sales` | No — `-sheet` moves to `from xlsx` |
| `ssql from -- ps aux` | `ssql from command -- ps aux` | No — use `from command` |

The flags that move (`-channel`, `-sheet`, `-format`) are format-specific and rarely used with the bare form. The most common case — `ssql from file.csv` — continues to work unchanged.

## Benefits

1. **Symmetry with `to`** — learn one pattern, use everywhere
2. **Focused help** — `ssql from wav -help` shows only WAV-relevant flags
3. **Focused completion** — `from csv` completes `.csv` files, `from wav` completes `.wav` files
4. **Clean flag namespaces** — catalog flags don't clutter CSV reading
5. **Extensible** — adding `from parquet` or `from sqlite` is just another subcommand
6. **Simpler code** — each handler is small and focused, no format switch statements

## Relationship to Distributed Processing

This design is a prerequisite for clean distributed processing. Without subcommands, `from` would need to absorb all of these flags into one handler:

```
-format, -type, -default-type, -channel, -sheet,     (existing format flags)
-merge, -on-error, -shard-field, -parallel, -remote,  (catalog flags)
-where (partition pruning)                             (catalog flags)
```

That's 12+ flags, most irrelevant to any given invocation. With subcommands, each source type gets exactly the flags it needs.
