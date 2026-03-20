# Streaming vs Materialization: Command Survey

## Problem Statement

ssql commands process data through Unix-style pipelines. Some commands stream records one-by-one (O(1) memory, infinite stream compatible), while others materialize all records into memory before producing output. This document surveys every command's behavior and identifies opportunities to improve streaming support.

## Classification

### Pure Streaming (12 commands)

These process records one at a time via Go iterators. They work with infinite streams and use O(1) memory.

| Command | Library Function | Notes |
|---------|-----------------|-------|
| `from` | `ReadCSV()`, `ReadJSON()` | Lazy iterator. Buffers first record for schema inference via `iter.Pull`. |
| `where` | `Where(predicate)` | Pure filter. |
| `update` | `Update(fn)` / `Select()` | Per-record transformation. |
| `cast` | `Update(fn)` | Per-record type casting. |
| `include` | `Select(includer)` | Per-record field projection. |
| `exclude` | `Select(excluder)` | Per-record field removal. |
| `rename` | `Select(renamer)` | Per-record field renaming. |
| `limit` | `Limit[Record](n)` | Takes first N, stops iteration. |
| `offset` | `Offset[Record](n)` | Skips first N, streams rest. |
| `to csv` | `WriteCSVToWriter()` | Writes each record immediately. |
| `to tsv` | `WriteTSVToWriter()` | Same as CSV, tab-separated. |
| `to json` (JSONL) | Direct JSON marshal | One JSON object per line. Default for stdout/`.jsonl`. |

### Streaming with Growing State (2 commands)

These stream records but maintain a set that grows with the number of distinct keys. Memory is O(distinct keys), not O(total records).

| Command | State | Notes |
|---------|-------|-------|
| `distinct` | `seen map[K]bool` | Emits record only if key not seen before. Bounded cardinality = effectively O(1). |
| `union` (no `-all`) | `seen map[K]bool` | `Concat` is pure streaming; dedup adds seen map. |
| `union -all` | None | Pure streaming — just chains iterators. |

### Partial Materialization (1 command)

| Command | What Materializes | What Streams |
|---------|-------------------|-------------|
| `join` | **Right side** into hash table | **Left side** streams, probing the hash table per record. |

This is the standard hash join approach. User should put the smaller dataset on the right.

### Streaming with Bounded Buffer (1 command)

Uses O(K) memory where K is a user-specified constant.

| Command | State | Notes |
|---------|-------|-------|
| `top N` | Min/max-heap of size N | Heap-based top-K. O(N·log(K)) time, O(K) memory. |

### Full Materialization (13 commands)

Must collect all records before producing output. Cannot handle infinite streams. Some have streaming alternatives.

| Command | Materialization Point | Why |
|---------|----------------------|-----|
| `sort` | `slices.SortedFunc()` in `operations.go` | Must see all records to determine order. |
| `group-by` | `map[string][]Record` in `sql.go` | Must see all records per group to compute aggregates. Use `-presorted` for streaming alternative. |
| `group-by -rollup/-cube` | `Rollup()`/`Cube()` in `sql.go` | Collects all records for subtotal/grand total generation. |
| `pivot` | `Pivot()` in `sql.go` + `slices.Collect` in `pivot.go` | Must see all records to discover pivot column values. |
| `window` | `slices.Collect` in `sql.go` Window() | Ranking/lag/lead need full partition visibility. Use `-presorted` for streaming alternative (13 of 15 functions). |
| `to table` | `DisplayTableWithFields()` | Scans all records for column width alignment. Use `-stream` for streaming alternative. |
| `to json` (pretty) | `json.MarshalIndent(all)` | JSON array `[{...},{...}]` requires all records. |
| `to arrow` | `WriteArrow()` | Columnar batch writing. |
| `to wav` | `WriteWAV()` | WAV header requires total sample count. |
| `to xlsx` | `WriteXLSX()` | Excel requires random cell access. |
| `to chart` / `to animate` / `to explore` | Various chart functions | Need all data for axes, scales, layout. |
| `fft` / `ifft` / `convolve` / `correlate` / `spectrogram` | `slices.Collect` in each | Signal processing is inherently global. |

### Non-Data Commands (3)

`version`, `functions`, `generate go` — not applicable.

## Summary

| Classification | Count | % of data commands |
|---------------|-------|-------------------|
| Pure streaming | 12 | 44% |
| Streaming + growing state | 2 | 7% |
| Streaming + bounded buffer | 1 | 4% |
| Partial materialization | 1 | 4% |
| Full materialization | 13 | 48% |

Note: `group-by -presorted`, `window -presorted`, and `to table -stream` provide streaming alternatives for 3 of the 13 materializing commands.

The core pipeline commands (where, update, include, exclude, rename, limit, offset) are all pure streaming, so the most common pipeline patterns already handle arbitrarily large data.

## Optimization Opportunities

### 1. Streaming group-by with pre-sorted input (HIGH VALUE) — DONE v4.23.0

**Implemented:** `StreamGroupByFields()` in `sql.go` + `-presorted` flag in CLI.

Tracks current group key. When key changes, emits aggregate for previous group and starts new group. Memory = O(1 group) instead of O(all records). Output format identical to `GroupByFields` so `Aggregate()` works unchanged.

```bash
# Input sorted by dept — streams with O(1 group) memory
ssql from data.csv | ssql sort dept | ssql group-by dept -count n -sum salary total -presorted
```

- Library: `StreamGroupByFields(sequenceField, fields...)` — same signature as `GroupByFields`
- CLI: `-presorted` flag (rejects `-rollup`/`-cube` combinations)
- Code generation: emits `ssql.StreamGroupByFields` instead of `ssql.GroupByFields`

### 2. Streaming window functions — DONE v4.24.0

**Implemented:** `StreamWindow()` in `sql.go` + `-presorted` flag in CLI.

Supports 13 of 15 window functions (not NTILE, PERCENT_RANK) with presorted input. Three algorithm categories:
- **Running aggregates** (default frame): O(1) memory — SUM, AVG, COUNT, FIRST, LAST, ROW_NUMBER, RANK, DENSE_RANK
- **Bounded frames** (ROWS N,0): Ring buffer / monotonic deque — sliding SUM, AVG, COUNT, FIRST, MIN, MAX
- **Offset functions**: LAG via ring buffer (immediate), LEAD via delayed emission buffer

**Benchmark results (10K rows):** Running SUM 71x faster, RANK 134x faster, combined (7 funcs) 10.5x faster vs `Window()`.

```bash
ssql from data.csv | ssql window -sum revenue total -order date -presorted
ssql from data.csv | ssql window -avg price ma3 -preceding 2 -following 0 -order date -presorted
ssql from data.csv | ssql window -lag price 1 prev -lead price 1 next -order date -presorted
```

### 3. Streaming to-table with fixed widths (LOW-MEDIUM VALUE) — DONE v4.23.0

**Implemented:** `DisplayTableStreaming()` / `DisplayTableStreamingTo()` in `io.go` + `-stream` flag in CLI.

Uses `iter.Pull` for two-phase processing: samples first N records (default 100) to infer column widths, prints header, prints sampled records, then streams remaining records one at a time. Memory = O(sample size) instead of O(all records).

```bash
# Infer widths from first 100 records, stream the rest
ssql from huge.csv | ssql to table -stream

# Custom sample size
ssql from huge.csv | ssql to table -stream -sample 500
```

- Library: `DisplayTableStreaming(records, maxWidth, sampleSize, fieldOrder, onlySpecified)`
- CLI: `-stream` flag + `-sample N` flag (default 100)
- Extracted helpers: `buildColumnOrder`, `calculateColumnWidths`, `printTableHeader`, `printTableSeparator`, `printTableRow`

### 4. Chunked Arrow writing (LOW VALUE)

**Current:** Arrow writer collects all records for columnar batch writing.

**Opportunity:** Arrow IPC supports chunked writing. Write fixed-size batches (e.g., 64K records) without collecting all records. Schema from first record.

**Complexity:** Medium. Requires buffering a batch, writing it, clearing, repeat.

### 5. Streaming WAV with seekable output (LOW VALUE)

**Current:** WAV header requires sample count upfront.

**Opportunity:** Write placeholder header, stream samples, seek back to update count.

**Limitation:** Only works with seekable file output, not stdout pipes.

**Complexity:** Low.

### 6. sort + limit fusion / `top` command (MEDIUM VALUE) — DONE v4.23.0

**Implemented:** `TopBy()` / `BottomBy()` in `operations.go` + `top` CLI command.

Heap-based top-K using `container/heap`. O(N·log(K)) time, O(K) memory. Results sorted descending (TopBy) or ascending (BottomBy).

```bash
# Instead of materializing millions of records:
ssql from huge.csv | ssql sort salary -desc | ssql limit 10

# Uses top-K with O(10) memory:
ssql from huge.csv | ssql top 10 -field salary

# Bottom 5 (ascending):
ssql from huge.csv | ssql top 5 -field age -asc
```

- Library: `TopBy[T, K](n, keyFn)` and `BottomBy[T, K](n, keyFn)` — generic, work with any ordered key
- CLI: `ssql top N -field FIELD [-asc] [-generate]`
- Code generation: emits `ssql.TopBy` or `ssql.BottomBy`

## Workarounds for Users Today

For datasets too large to materialize:

1. **Pre-filter:** Use streaming `where` before materializing commands to reduce data size.
2. **Pre-sort externally:** Use Unix `sort` for pre-sorted group-by.
3. **Chunk processing:** Split large files, process chunks, union results.
4. **Use JSONL mode:** Avoid `to json` (pretty) and `to table` — use `to json` (JSONL, the default) which streams.
5. **Right-size joins:** Put the smaller dataset on the right side of `join`.
6. **Limit early:** Use `limit` before materializing commands during development/testing.

## Recommended Priority

| # | Optimization | Value | Complexity | Status |
|---|-------------|-------|------------|--------|
| 1 | Streaming group-by (`-presorted`) | High | Medium | **DONE v4.23.0** |
| 2 | sort + limit fusion (`top` command) | Medium | Medium | **DONE v4.23.0** |
| 3 | Streaming to-table (`-stream`) | Low-Med | Low | **DONE v4.23.0** |
| 4 | Streaming window (`-presorted`) | High | High | **DONE v4.24.0** — 71-134x speedup |
| 5 | Chunked Arrow writing | Low | Medium | Defer — niche format |
| 6 | Seekable WAV writing | Low | Low | Defer — niche format |
