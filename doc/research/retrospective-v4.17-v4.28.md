# Post-Italy: v4.17 → v4.28

Reference: DFC066
Created: 2026-03-20
Last modified: 2026-03-20

[Back to Index](./README.md)

**Period:** February 16 – March 15, 2026 (4 weeks)
**Previous:** [Italy Sprint v4.11–v4.16](italy-sprint-v4.11-v4.16.md)
**Versions:** v4.17.0 → v4.28.0 (12 releases)
**Commits:** ~120

## Where We Were

v4.16.0 left us with a capable single-machine tool: 6 I/O formats, 5 visualization commands, GPU acceleration, code generation, and an AI prompt system. The Italy sprint was feature-heavy and outward-facing.

The post-Italy period shifted inward: performance, architecture, streaming algorithms, and distributed processing. Less visible, more foundational.

## What We Built

### WASM Hardening (v4.17–v4.18, Week 8)

The Italy sprint got WASM working. This period made it production-quality.

- **Binary size**: 15MB → 245KB by fully decoupling from the ssql package and switching to TinyGo with a custom JSON parser
- **Pipeline builder UI**: 8 operation types with AG-Grid integration, computed columns, pivot tables
- **44 → 79 tests**: Custom JSON parser, operators, expressions, window functions
- **Embedded binary**: WASM encoded as base64 in the HTML output (362KB, total ~439KB self-contained)

The key insight: TinyGo can't handle `encoding/json`, `reflect`, or most of the standard library. Rather than fighting conditional compilation, we wrote a completely separate module with its own types. More work up front, dramatically better result.

### Pivot Tables (v4.18, Week 8)

Cross-tabulation for the CLI:

```bash
ssql from sales.csv | ssql pivot -row dept -col quarter -val revenue -func sum
```

- `ssql.Pivot()` library function with count/sum/avg/min/max
- Zero-filled missing cells, first-seen ordering
- CLI command with code generation support

### SQL Window Functions (v4.20–v4.26, Weeks 9-10)

15 window functions — the most algorithmically complex feature in ssql.

**Functions:**
- Ranking: `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `NTILE`, `PERCENT_RANK`
- Offset: `LAG`, `LEAD`, `FIRST`, `LAST`
- Aggregate: `SUM`, `AVG`, `COUNT`, `MIN`, `MAX`
- ROWS frame support (UNBOUNDED PRECEDING/FOLLOWING, fixed offsets)

**Streaming implementation** was the real work. Standard window functions buffer the entire partition — O(N²) for large datasets. The streaming versions use ring buffers and monotonic deques for O(N):

| Operation | Buffered | Streaming | Speedup |
|-----------|----------|-----------|---------|
| Running SUM (10K) | 465ms | 6.5ms | **71x** |
| RANK (10K) | 858ms | 6.4ms | **134x** |
| Combined 7 funcs | 208ms | 20ms | **10.5x** |
| Sliding SUM (ROWS 2,0) | 7.9ms | 6.5ms | 1.2x |

Phase 2 added bounded frames with ring buffer aggregates (`swSlidingSum`, `swSlidingAvg`, etc.) and monotonic deque for O(1) sliding MIN/MAX. Phase 3 validated with benchmarks.

### Streaming Optimizations (v4.19–v4.25, Weeks 9-10)

Several commands got streaming variants:

- **`TopBy[T,K](n, keyFn)`**: O(N·log(K)) heap instead of full sort for `top` command
- **`StreamGroupByFields(sequenceField, fields...)`**: O(1) memory when input is pre-sorted — tracks current key, buffers only one group
- **`DisplayTableStreaming()`**: Samples first N records for column widths, streams the rest
- **k-way merge**: Min-heap merge of pre-sorted inputs from multiple sources

### Multi-field Sort Fix (v4.25, Week 10)

Sort was broken for multiple fields and for strings:

- `ssql sort dept age` now works (was single-field only)
- String sorting fixed: `extractNumeric()` was mapping all strings to 0
- New `SortRecords([]OrderField)` library function
- `CompareRecordFields` for cross-type comparison

### Expression Language (v4.23, Week 9)

- `replaceRegex(str, pattern, replacement)` with capture group support
- Documented all 71 expression builtins across 8 categories
- String builtins expanded from 8 → 14, Array from 5 → 19

### Distributed Processing (v4.27, Week 11)

The biggest architectural leap: single-machine → distributed.

**`from ssh HOST PATH`:**
- Read remote files via SSH
- Push-down filtering: `-- where -if age gt 25`
- Multi-step push-down: `-- where -if age gt 25 + group-by dept -count cnt`
- `-gpu` flag for remote GPU acceleration
- Code generation emits `exec.Command("ssh", ...)`

**`from catalog shards.csv`:**
- Shard catalog (CSV mapping host+path to data files)
- Partition pruning: `-if date ge 2025-02-01` skips irrelevant shards before connecting
- Range pruning via `X_from`/`X_to` columns with interval overlap logic
- Push-down + pruning combined
- `-shard-field _shard` adds provenance to each record

**Infrastructure:**
- 3 LXD containers (ssql-node1/2/3) with distributed test data
- SSH key auth + connection multiplexing
- Static IP assignment (worked around LXD/UFW DHCP issue)
- `ssql.ShellQuote()`, `ssql.SplitOnPlus()`, `ssql.BuildRemoteCommand()` exported

### `from` Subcommand Refactor (v4.27, Week 11)

Mirrored the `to` subcommand pattern:

```bash
ssql from csv data.csv      # explicit format
ssql from data.csv           # inferred from extension (still works)
ssql from json -             # explicit stdin
ssql from ssh host /path     # remote
ssql from catalog shards.csv # distributed
ssql from command -- cmd args  # replaces old `from -- cmd args`
```

Each format gets focused flags (`-channel` only on `from wav`, `-sheet` only on `from xlsx`).

### Code Generation Maturation (v4.27–v4.28, Weeks 11-14)

**Parameterized output:** Generated programs accept command-line flags instead of hardcoded values:

```bash
go run gen.go                              # defaults work as before
go run gen.go -catalog prod-shards.csv     # different catalog
go run gen.go -input other.csv -limit 1000 # different input and limit
```

`CodeParam` struct + `flag.String()`/`flag.Int()` declarations emitted automatically.

**Package exports for codegen:**
- `ReadCatalog()`, `PruneCatalog()`, `ProcessCatalogShards()` — catalog operations
- `StreamExprAgg()` — streaming expression aggregations
- `SpectrogramOptions.OutputFormat` — spectrogram output format in library

**Dead code cleanup:** 493 lines deleted when package functions replaced CLI internals (agg_patcher.go, group_by_expr_test.go).

### Smart Completion System (v4.28, Week 11)

Tab completion that understands distributed data:

- **SSH host warmup**: Completing the host backgrounds `ssh -N -f host` to warm the multiplexed connection
- **Remote field discovery**: PATH completer SSHs `head -1`, parses CSV header, caches field names for downstream `where -if <TAB>`
- **Catalog completion**: Collapses range columns (`date_from`/`date_to` → `date`), completes pruning values from catalog data, shows `date_from` values for range fields
- **`~/.ssh/config` parsing**: HOST completer reads SSH aliases

### CLAUDE.md Restructuring (Weeks 11, 14)

Two-phase restructuring:

**Phase 1 (Week 11):** Extracted 7 reference sections into `claude/` files, reducing CLAUDE.md from 77k → 7.3k chars.

**Phase 2 (Week 14):** Discovered the extraction caused knowledge loss — rules in `claude/` files weren't loaded into context and were silently ignored. Fixed by inlining all critical one-liner rules back into CLAUDE.md (→ 10k chars) while keeping detailed examples in `claude/`. Added explicit warning and created `claude/completion-system.md` for the new completion architecture.

### Other Changes

- **Go 1.26 modernization**: 32 files updated with `range n`, `maps.Insert`, `slices.Contains`
- **Heatmap bug fixes**: Axis types, Z-range formatting, Y-label sorting
- **`-if`/`-if-expr` rename**: `where`/`update` flags renamed from `-where`/`-where-expr`
- **Security hardening**: Absolute paths for remote binaries (`/usr/bin/ssql`, `/usr/bin/head`)
- **GitHub repo migration**: Repos deleted in preparation for employer-sponsored open-source, local mirrors preserved
- **Examples restructured**: 43 files moved to individual directories, fixing `go test ./...` failures

## What We Learned

### Streaming First

The window function benchmarks (71-134x speedup) proved that streaming implementations aren't premature optimization — they're a different algorithmic class. The pattern: assume the input is larger than memory, design for streaming first, buffer only when required. This applies to group-by, top-K, table display, and window functions.

### Export Early, Simplify Immediately

Every time a function was exported to the ssql package, ~100 lines of CLI code could be deleted. `StreamExprAgg` eliminated manual grouping. Catalog functions eliminated duplicated logic. The checklist in CLAUDE.md now enforces this: every new export triggers a CLI simplification review.

### Completion Is UX

The SSH warmup trick — background-starting a connection when the host is tab-completed — makes remote field discovery feel local. The catalog value completion — showing `date_from` values when the user types `-if date ge <TAB>` — teaches the data format through the shell. Good completion replaces documentation.

### AI Context Is an Engineering Problem

The CLAUDE.md split optimized for size but broke knowledge transfer. The lesson: what's always in context matters more than what's theoretically available. Critical rules need to be in CLAUDE.md (always loaded), detailed examples in reference files (loaded on demand). Documentation structure directly affects AI assistant behavior.

### Security Rules Need Examples

"Use absolute paths for remote binaries" → we still missed `/usr/bin/head`. Changed to: *"Use absolute paths for ALL commands executed remotely"* with concrete examples. Abstract rules get applied inconsistently.

## Stats

| Metric | Value |
|--------|-------|
| Releases | 12 (v4.17 → v4.28) |
| Period | 4 weeks (Feb 16 – Mar 15) |
| Window functions | 15 (9 streaming aggregate types) |
| Best speedup | 134x (streaming RANK) |
| WASM binary | 15MB → 245KB |
| Expression builtins | 71 |
| SSH containers | 3 |
| Dead code removed | 493 lines |
| Examples restructured | 43 files |
| Codebase | 45,602 lines Go |

## Format Support (as of v4.28.0)

| Format | Read | Write | Stdin | Code Gen | Remote (SSH) |
|--------|------|-------|-------|----------|--------------|
| CSV | yes | yes | yes | yes | yes |
| TSV | yes | yes | yes | yes | yes |
| JSON/JSONL | yes | yes | yes | yes | yes |
| Arrow | yes | yes | yes | yes | yes |
| WAV | yes | yes | no | yes | no |
| XLSX | yes | yes | no | yes | no |

## What's Next

- **Union + multi-SSH codegen**: Multiple `union -file <(from ssh ...)` produces broken code (redeclared vars, missing commas in Chain)
- **Arrow → GPU direct transfer**: Bypass Record extraction for GPU operations
- **Open-source re-publishing**: Through employer sponsorship
