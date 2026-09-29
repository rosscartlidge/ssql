# Performance, Measured

Every number here comes from a pipeline or benchmark you can run
yourself, and every table says which machine produced it and when. This
page is the single source for the numbers the other docs quote; when a
number here changes, the others follow it.

[Back to Documentation](README.md)

**Machines.**

| | Machine | Notes |
|---|---|---|
| **A** | Intel Core Ultra 9 275HX laptop, 24 threads, 62 GB, Linux | the development machine; rows marked A were re-measured on 2026-09-29 |
| **B** | dual-socket Intel Xeon Gold 6154 workstation, 72 threads | a user's timings, April 2026 |
| **C** | a 32-core Linux machine | April 2026, not re-measured since |

Wall times are the best of three runs of the **built** program;
`generate go -run` adds about 1.4 s of compile on machine A before the
first row. Memory is peak RSS.

## 1. Faster than DuckDB on a real 14.6 M-row file (machine A)

DuckDB is the bar for "fast" on a laptop. Here is the same query on the
same 660 MB parquet file — a three-field cube with counts at every
level — as a compiled ssql pipeline and as DuckDB running the SQL that
`ssql generate sql` produced from it:

```bash
ssql generate go -run -pipeline 'ssql from parquet shuffled.parquet
    | ssql group-by a_kind relationship z_kind -count count -cube | ssql to csv'
```

| Engine | Wall | Peak memory |
|---|---:|---:|
| PostgreSQL 16, `generate sql` output, after an 11 s `COPY` load | 13.0 s | 1.7 GB table |
| PostgreSQL 16, native `GROUP BY CUBE`, after the same load | 5.1 s | 1.7 GB table |
| DuckDB 1.5, `generate sql` output | 0.96 s | 2.6 GB |
| ssql `generate go`, optimiser off (`+O`: all seven columns read) | 1.7 s | 8.0 GB |
| **ssql `generate go` (typed, default)** | **0.28 s** | **0.76 GB** |

**3.4× faster than DuckDB and under a third of the memory, in pure Go
with no CGO.** Two things make it so, and both are automatic. The
pipeline optimiser (`-O`, on by default) reads the stages downstream of
`from parquet`, sees that only three of the seven columns are used, and
prunes the read to them — the same projection DuckDB works out from the
query; the `+O` row shows what that is worth. Then the typed planner runs
the read and the group-by in parallel across every core, and the cube's
parent levels are merged from the detail groups' state rather than by
re-reading rows. The generated program's header records both the
pipeline you typed and the one it implements.

Honest framing: this is a scan-and-aggregate query, where a pruned
parallel read wins. On the join-heavy benchmark in §3 DuckDB is still
ahead. Postgres is in the table for scale, not as a target: it has to
load the file first, its grouping sets do not parallelise, and the
cube's `IS NOT DISTINCT FROM` joins cannot hash-join there — on a plain
three-key `GROUP BY` of the loaded table it runs 0.30 s, level with
ssql. The Postgres rows are from the 2026-09-15 run on the same machine
and file; the full run (tuned settings, `file_fdw`, plans) is in
[DuckDB vs ssql](research/duckdb-vs-ssql.md#measured-the-readme-cube-benchmark-on-duckdb-postgresql-and-ssql-2026-09-15).
Reproduce it with any parquet file you have — the pipeline is the only
input.

## 2. The same file, a plain group-by, six ways (machines B and A)

`from … | group-by relationship -count number | to table` over the
14.6 M-row file (1.23 GB as CSV), run six different ways on machine B:

| Form | Wall | vs CLI baseline |
|---|---:|---:|
| Interactive CLI pipeline (3 processes via JSONL pipes) | 68.7 s | 1.0× |
| `SSQL_MODE=record` codegen (Record, 1 process) | 15.5 s | 4.4× faster |
| `SSQL_MODE=typed`, serial form (1 thread, struct types) | 22.0 s | 3.1× faster |
| `SSQL_MODE=typed`, planner-parallel (CSV, multi-shard) | 1.23 s | **56× faster** |
| `SSQL_MODE=typed`, planner-parallel (Parquet, single row group) | 0.76 s | **90× faster** |
| **`SSQL_MODE=typed`, planner-parallel (Parquet, 15 row groups, column projection)** | **0.32 s** | **215× faster** |

Three of those rows on machine A:

| Form | Wall (A) |
|---|---:|
| Interactive CLI pipeline | 24.2 s |
| `SSQL_MODE=typed`, planner-parallel (CSV) | 1.76 s |
| **`SSQL_MODE=typed`, planner-parallel (Parquet, column projection)** | **0.15 s** |

All the typed rows come from the *same* `SSQL_MODE=typed` — the planner
selects the parallel form when it is reachable; the serial row is what
it emits when forced serial, shown to isolate where the speed comes
from. The 215× from interactive CLI to typed-parallel-Parquet decomposes
into independent wins multiplied together: ~4× from collapsing 3
processes to 1 (no JSONL transit), ~13× from struct types over
`map[string]any` (no allocation per row, no GC), ~1.6× from parallelism
on the CSV path, ~1.6× from column projection on Parquet, and ~2.4× from
multi-row-group parallelism. The column projection is automatic: the
optimiser reads the downstream stages and prunes the Parquet read to the
columns they use (the unprojected read of this file takes 1.51 s on
machine A, 10× longer). `ssql to parquet` writes 1 M-row row groups by
default (`-row-group-size`), which is what the parallel reader shards
across.

## 3. The library: `ssql.Record` vs `ssql/typed` (machine A, single-threaded)

When the schema is known at compile time and the pipeline is hot, the
`ssql/typed` subpackage is a struct-based fast path with the same
composition shape as the `Record` API (the [Typed Codelab](typed-codelab.md)
is the tutorial). On a 10 M-row × 3 chained-join workload with CSV I/O
on both sides, run by the benchmarks in `typed/`:

| Implementation | Time | Memory allocated | Allocations |
|---|---:|---:|---:|
| `ssql.Record` | 74.8 s | 37.7 GB | 544 M |
| **`ssql/typed`** | **4.94 s** | **1.10 GB** | **20 M** |
| DuckDB v1.5 CLI, same files | 0.42 s | — | — |

**15× faster, 34× less memory** than the Record API; within an order of
magnitude of DuckDB, in pure Go with no CGO. All three produce the same
7.25 M output rows. DuckDB's remaining lead is columnar storage with
vectorised execution, which a row-at-a-time runtime does not get without
rewriting around Arrow; §1 shows where a pruned parallel scan turns that
round.

The smaller 1 M-row × 1-join workload separates the CSV cost from the
compute cost:

| Implementation | Time | Memory | Allocs |
|---|---:|---:|---:|
| `ssql.Record`, end-to-end | 2,006 ms | 909 MB | 19.6 M |
| `ssql/typed`, end-to-end | **386 ms** | **96 MB** | **2.0 M** |
| `ssql.Record`, compute-only | 1,009 ms | 644 MB | 11.6 M |
| `ssql/typed`, compute-only | **69 ms** | **0.3 MB** | **20** |

End-to-end 5.2× faster and 9.4× less memory; compute-only (CSV stripped)
14.5× faster. The typed CSV decoder, built by reflection once per file,
costs about 20% over a hand-written positional reader — the price of
keeping the API generic.

## 4. One pipeline, three execution models (machine A)

The same shell pipeline — 1 M employees joined to 1 k departments,
filtered, written as CSV — run interactively, as a Record-mode generated
program, and as the default typed generated program
(`cmd/ssql/codegen_bench_test.go`):

| Mode | Wall time | Peak RSS |
|---|---:|---:|
| CLI pipeline (interactive, one process per stage) | 2.07 s | 36 MB |
| `generate go -mode record` (Record, 1 process) | 2.44 s | 958 MB |
| **`generate go` (typed, planner-parallel, default)** | **0.14 s** | **183 MB** |
| Typed vs CLI | **14.6× faster** | — |
| Typed vs Record codegen | **17.2× faster** | **5.2× less memory** |

The interactive pipeline is cheap on memory because each stage streams;
Record codegen is one process but keeps `map[string]any` rows; the typed
program is one process with struct rows, parallel read, join and write.
`generate go -pipeline '…'` is how you get the last row from the first
one:

```bash
ssql generate go -run -pipeline 'ssql from employees.csv
    | ssql where -if years ge 5
    | ssql join departments.csv -using dept_id
    | ssql to csv seniors.csv'
```

## 5. Parallel vs serial typed programs, 10 M rows (machine C)

Typed mode runs the pipeline across all cores when there is parallelism
to exploit: a planner inspects every stage and picks the parallel form
(parallel CSV read, `Stream.Where`, `HashJoinParallel`,
`GroupByParallel`, per-shard output buffers) when it is reachable, and
the serial `iter.Seq[T]` form when a `sort`, `distinct` or `to table`
later in the pipeline forces it. On a 10 M-row corpus:

| Workload | typed, serial form | **typed, planner-parallel** | DuckDB |
|---|---:|---:|---:|
| Filter + write 7.25 M-row CSV | 5.7 s | **1.3 s (4.4× faster)** | 0.7 s |
| Group-by 1 000 dept_ids, count + sum + avg + min + max | 3.80 s | **0.95 s (4.0× faster)** | 0.39 s |

The planner backs off to serial when a stage needs it: `from | sort | to
csv` reads with the serial `typed.ReadCSV`, because a global sort would
only pay for the parallel fan-in. A stage that has no typed form yet runs
on Records behind an adapter, so the parallel parse still happens; the
[Typed Reference](typed-reference.md) lists those stages, and `generate
go -explain` says which form each stage of your pipeline got.

## Reproducing

```bash
# §1, §2: any parquet/CSV file you have; -build then time the binary
ssql generate go -pipeline 'ssql from parquet FILE | ssql group-by a b c -count n -cube | ssql to csv' -build ./cube
time ./cube > /dev/null
(export SSQL_MODE=record; ssql from parquet FILE | ssql group-by a b c -count n -cube | ssql to csv) | ssql generate sql > cube.sql
time duckdb < cube.sql > /dev/null

# §3: the typed/ benchmarks (the 10 M-row corpus is generated once, 600 MB under os.TempDir)
go test -bench=. -benchtime=3x -run=^$ ./typed/...                                   # 1 M-row workload, ~1 min
go test -bench='Scale(Record|Typed)3Join' -benchtime=1x -run=^$ -timeout=30m ./typed/... # 10 M × 3 joins
go test -bench=DuckDB -benchtime=1x -run=^$ -timeout=10m ./typed/...                  # needs duckdb on PATH

# §4: the three-model comparison
go test ./cmd/ssql/ -run TestCodegenBench -timeout 10m -v
```

[**Typed Codelab →**](typed-codelab.md) | [**Typed Reference →**](typed-reference.md) | Design notes: [typed codegen](research/typed-codegen-proposal.md), [parallel group-by](research/typed-groupby-parallel-proposal.md)
