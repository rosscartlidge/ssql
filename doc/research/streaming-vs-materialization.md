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

### Full Materialization (13 commands)

Must collect all records before producing output. Cannot handle infinite streams.

| Command | Materialization Point | Why |
|---------|----------------------|-----|
| `sort` | `slices.SortedFunc()` in `operations.go` | Must see all records to determine order. |
| `group-by` | `map[string][]Record` in `sql.go` | Must see all records per group to compute aggregates. |
| `group-by -rollup/-cube` | `Rollup()`/`Cube()` in `sql.go` | Collects all records for subtotal/grand total generation. |
| `pivot` | `Pivot()` in `sql.go` + `slices.Collect` in `pivot.go` | Must see all records to discover pivot column values. |
| `window` | `slices.Collect` in `sql.go` Window() | Ranking/lag/lead need full partition visibility. |
| `to table` | `DisplayTableWithFields()` | Scans all records for column width alignment. |
| `to json` (pretty) | `json.MarshalIndent(all)` | JSON array `[{...},{...}]` requires all records. |
| `to arrow` | `WriteArrow()` | Columnar batch writing. |
| `to wav` | `WriteWAV()` | WAV header requires total sample count. |
| `to xlsx` | `WriteXLSX()` | Excel requires random cell access. |
| `to chart` / `to animate` / `to explore` | Various chart functions | Need all data for axes, scales, layout. |
| `fft` / `ifft` / `convolve` / `correlate` / `spectrogram` | `slices.Collect` in each | Signal processing is inherently global. |

### Non-Data Commands (3)

`version`, `functions`, `generate-go` — not applicable.

## Summary

| Classification | Count | % of data commands |
|---------------|-------|-------------------|
| Pure streaming | 12 | 48% |
| Streaming + growing state | 2 | 8% |
| Partial materialization | 1 | 4% |
| Full materialization | 13 | 52% |

The core pipeline commands (where, update, include, exclude, rename, limit, offset) are all pure streaming, so the most common pipeline patterns already handle arbitrarily large data.

## Optimization Opportunities

### 1. Streaming group-by with pre-sorted input (HIGH VALUE)

**Current:** `group-by` collects all records into `map[string][]Record`.

**Opportunity:** If input is already sorted by group key, aggregation can be streaming — emit group result when key changes. This is a standard database optimization.

**Approach:** Add a `-presorted` flag (or auto-detect sorted input by comparing consecutive keys):
```bash
# Input sorted by dept — can stream
ssql from data.csv | ssql sort -field dept | ssql group-by -presorted dept -count n -sum salary total
```

**Implementation:** Track current group key. When key changes, emit aggregate for previous group and start new group. Memory = O(1 group) instead of O(all records).

**Complexity:** Medium. Requires a new code path in `GroupByFields()` or a new `StreamGroupBy()` function.

### 2. Streaming window with bounded frames (MEDIUM VALUE)

**Current:** `Window()` materializes all records with `slices.Collect`.

**Opportunity:** Window functions with bounded ROWS frames (e.g., `-rows 3,0` for a 4-row window) could stream with a fixed-size ring buffer. Only needs to buffer `preceding + following + 1` records.

**Limitations:** Does NOT work for:
- Unbounded frames (running sum, cumulative count)
- Ranking functions (row_number, rank, dense_rank) — need full partition
- Lag with unbounded offset
- Any function using PARTITION BY (need full partition)

**Approach:** Add a streaming fast path when: no partitioning, bounded frame, and aggregate-only functions (sum/avg/min/max/count).

**Complexity:** High. Two separate code paths, careful edge case handling.

### 3. Streaming to-table with fixed widths (LOW-MEDIUM VALUE)

**Current:** `to table` scans all records for column width calculation.

**Opportunity:** Offer a streaming mode with:
- Fixed column widths (user-specified or inferred from first N records)
- Truncation with `...` for overflow

**Approach:** Add a `-stream` or `-widths` flag:
```bash
# Infer widths from first 100 records, stream the rest
ssql from huge.csv | ssql to table -stream

# Fixed widths
ssql from huge.csv | ssql to table -widths 20,10,15
```

**Complexity:** Low. Just skip the pre-scan and use default/specified widths.

### 4. Chunked Arrow writing (LOW VALUE)

**Current:** Arrow writer collects all records for columnar batch writing.

**Opportunity:** Arrow IPC supports chunked writing. Write fixed-size batches (e.g., 64K records) without collecting all records. Schema from first record.

**Complexity:** Medium. Requires buffering a batch, writing it, clearing, repeat.

### 5. Streaming WAV with seekable output (LOW VALUE)

**Current:** WAV header requires sample count upfront.

**Opportunity:** Write placeholder header, stream samples, seek back to update count.

**Limitation:** Only works with seekable file output, not stdout pipes.

**Complexity:** Low.

### 6. sort + limit fusion (MEDIUM VALUE)

**Current:** `sort | limit N` materializes ALL records for sort, then takes N.

**Opportunity:** A top-K algorithm uses a heap of size N, processing each record in O(log N) time with O(N) memory instead of O(all records).

**Approach:** Detect `sort | limit` pattern in pipeline, or add a dedicated `top` command:
```bash
# Instead of materializing millions of records:
ssql from huge.csv | ssql sort -field salary -desc | ssql limit 10

# Could use top-K with O(10) memory:
ssql from huge.csv | ssql top 10 -field salary -desc
```

**Complexity:** Medium. Heap-based top-K is straightforward; pipeline fusion is harder.

## Workarounds for Users Today

For datasets too large to materialize:

1. **Pre-filter:** Use streaming `where` before materializing commands to reduce data size.
2. **Pre-sort externally:** Use Unix `sort` for pre-sorted group-by.
3. **Chunk processing:** Split large files, process chunks, union results.
4. **Use JSONL mode:** Avoid `to json` (pretty) and `to table` — use `to json` (JSONL, the default) which streams.
5. **Right-size joins:** Put the smaller dataset on the right side of `join`.
6. **Limit early:** Use `limit` before materializing commands during development/testing.

## Recommended Priority

| # | Optimization | Value | Complexity | Recommendation |
|---|-------------|-------|------------|----------------|
| 1 | Streaming group-by (`-presorted`) | High | Medium | Do first — most common analytical operation |
| 2 | sort + limit fusion (`top` command) | Medium | Medium | Second — very common pattern |
| 3 | Streaming to-table (`-stream`) | Low-Med | Low | Easy win, nice UX improvement |
| 4 | Streaming window (bounded frames) | Medium | High | Defer — complex, narrow use case |
| 5 | Chunked Arrow writing | Low | Medium | Defer — niche format |
| 6 | Seekable WAV writing | Low | Low | Defer — niche format |
