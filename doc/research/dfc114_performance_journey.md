# 642k to 98M Rows per Second: A Performance Journey

Reference: DFC114
Created: 2026-08-25
Last modified: 2026-08-25

[Back to Index](./README.md)

**Status:** Retrospective / shareable write-up. Written to stand
alone for readers outside the project. Companion to
[DFC111](./dfc111_sampling_case_study.md) (the sampling case study).

## Setting

ssql is a Unix-pipeline data tool: the same pipeline —

```
ssql from shuffled.parquet | ssql group-by relationship -count n
```

— can run interpreted (each stage a process, rows as JSONL between
them), or be compiled to a native Go program, or run against
different storage formats. The data here is one real file: 14.6
million rows, 1.2GB as CSV.

The journey below happened in a browser workspace where pipelines
run against a server. One small feature made it visible.

## The instrument: show the work, not just the time

The workspace showed how long a run took. The request that started
this: *"it would be really nice to show the records per second so
people can see how fast this is — but I don't want to make it slow
doing it."*

The subtlety is which number to show. This pipeline emits **14
rows** — output rows/sec would read "10 rows/s" for a run that
processed millions. The honest, impressive number is **input** rows
over wall time. And input-row counts are obtainable with zero
per-run overhead:

- **CSV/TSV/JSONL**: count newlines once (`bytes.Count` at memory
  bandwidth, ~0.15s/GB), cache by (path, size, mtime). Every later
  run costs one `stat`.
- **Parquet**: the file *footer* stores the exact row count — a
  few-KB read, no data touched.
- Sampled sources report their sample size; unknowable cases omit
  the number rather than guess.

Result, one status line: `Head OK — 14.6M → 14 rows in 1.65s
(8.9M rows/s)`.

## The ladder

With the number visible, one user session walked the same query up
four rungs:

| Rung | Configuration | Wall | Throughput |
|---|---|---|---|
| 1 | CSV, interpreted pipeline | 22.8s | 642k rows/s |
| 2 | CSV, compiled ("typed") | 1.63s | 9.0M rows/s |
| 3 | Parquet, compiled | 1.44s | ~10M rows/s |
| 4 | Parquet, compiled, **column-pruned** | **0.15s** | **98.2M rows/s** |

Each rung is one architectural idea:

- **1→2 (14×): stop interpreting.** The compiled form is the same
  pipeline emitted as a Go program — typed structs instead of
  dynamic records, parallel readers and aggregation, no
  inter-process serialization. The server compiles on first use and
  caches the binary by pipeline hash, so the compile cost (~1.6s) is
  paid once.
- **2→3: stop parsing text.** Parquet's binary columnar layout
  replaces CSV parsing — the biggest single cost in rung 1 and 2 was
  never I/O, it was turning bytes into values.
- **3→4 (10×): stop reading what you don't need.** The query touches
  one column of seven. Parquet's footer records where each column's
  bytes live, so `-columns relationship` reads roughly a seventh of
  the file — and the aggregation runs over exactly one column's
  values.

153× end to end, no hardware change, no query change.

## The instrument found a bug on day one

The first thing the rows/s display did was expose a performance bug
nobody had noticed: the same group-by *without* `-count` ran at 2.2M
rows/s (6.7s) where the `-count` form ran 9M rows/s (1.5s).

The cause: a group-by with no aggregations is really SQL `DISTINCT`,
and the compiled form routed it through a *serial* dedupe — 14.6M
rows funnelled through one core — while aggregating group-bys used
the parallel path. It had always been that way; nothing displayed
the difference until now.

The fix is a textbook parallel dedupe: each of N shards dedupes
locally in parallel (that's the expensive part — hashing every row),
then a serial pass merges the survivors (at most N × distinct-count
rows — here, 14 × shards). 6.7s → 1.5s, output verified identical
across every execution backend.

## Takeaways

1. **Make throughput visible and cheap.** A rows/s figure that costs
   nothing per run changes behavior: users climb the ladder because
   they can see the rungs, and regressions can't hide.
2. **Metadata beats scanning.** Twice here — parquet's footer
   answered "how many rows" and "which bytes are column X" without
   touching data. The right file format carries answers to questions
   you haven't asked yet.
3. **The interpreter tax and the parse tax dominate** until removed;
   after that, I/O selectivity dominates. Optimize in that order.
4. **Instruments find bugs specifications miss.** Every correctness
   test passed while the serial-distinct path shipped; the first
   afternoon of a visible rows/s number caught it, because a 4×
   asymmetry between two spellings of the same query is impossible
   to ignore once printed.
