# Performance, Measured

Every number here comes from a pipeline you can run yourself; the sections say which machine and which file. The short version is in the README.

[Back to Documentation](README.md)

## Faster than DuckDB on a real 14.6 M-row file

DuckDB is the bar for "fast" on a laptop. Here is the same query on the
same 660 MB parquet file — a three-field cube with counts at every
level — as a compiled ssql pipeline and as DuckDB running the SQL that
`ssql generate sql` produced from it:

```bash
ssql generate go -run -pipeline 'ssql from parquet shuffled.parquet \
    | ssql group-by a_kind relationship z_kind -count count -cube | ssql to csv'
```

| Engine | Wall | Peak memory |
|---|---:|---:|
| PostgreSQL 16, `generate sql` output, after an 11 s `COPY` load | 13.0 s | 1.7 GB table |
| PostgreSQL 16, native `GROUP BY CUBE`, after the same load | 5.1 s | 1.7 GB table |
| DuckDB 1.5, `generate sql` output | 0.95 s | 2.7 GB |
| **ssql `generate go` (typed, default)** | **0.27 s** | **0.69 GB** |

**3.5× faster and a quarter of the memory, in pure Go with no CGO.** Two
things make it so, and both are automatic. The pipeline optimiser
(`-O`, on by default) reads the stages downstream of `from parquet`,
sees that only three of the seven columns are used, and prunes the
read to them — the same projection DuckDB works out from the query.
Then the typed planner runs the read and the group-by in parallel
across every core, and the cube's parent levels are merged from the
detail groups' state rather than by re-reading rows. `+O` turns the
optimiser off; the generated program's header records both the pipeline
you typed and the one it implements.

Honest framing: this is a scan-and-aggregate query, where a pruned
parallel read wins. On the join-heavy benchmark below DuckDB is still
ahead. Postgres is in the table for scale, not as a target: it has to
load the file first, its grouping sets do not parallelise, and the
cube's `IS NOT DISTINCT FROM` joins cannot hash-join there — on a plain
three-key `GROUP BY` of the loaded table it runs 0.30 s, level with
ssql. Same machine and file for every row; the full Postgres run
(tuned settings, `file_fdw`, plans) is in
[DuckDB vs ssql](research/duckdb-vs-ssql.md#measured-the-readme-cube-benchmark-on-duckdb-postgresql-and-ssql-2026-09-15).
Reproduce it with any parquet file you have — the pipeline is the only
input.

## High-performance typed pipelines: `ssql/typed`

When the schema is known at compile time and the pipeline is hot, the
`ssql/typed` subpackage gives you a struct-based fast path with the same
shape as the main API. Measured on the same 10M row × 3 chained
join workload (Intel Core Ultra 9 275HX, single-threaded):

| Implementation | Time | Memory | Allocations |
|---|---:|---:|---:|
| `ssql.Record` (current) | 74.8 s | 37.7 GB | 544 M |
| **`ssql/typed`** | **4.94 s** | **1.10 GB** | **20 M** |
| DuckDB v1.5 CLI | 0.42 s | — | — |

**15× faster, 34× less memory** vs the Record API — within an order of
magnitude of DuckDB, in pure Go with zero CGO. Same iter.Seq[T]
composition shape as the main API:

```go
type Employee struct {
    Name   string
    DeptID string `ssql:"dept_id"`
    Years  int64
}

type Department struct {
    DeptID   string `ssql:"dept_id"`
    DeptName string `ssql:"dept_name"`
}

employees := typed.ReadCSV[Employee]("employees.csv")
depts     := typed.ReadCSV[Department]("departments.csv")

seniors := typed.Where(func(e Employee) bool {
    return e.Years >= 5
})(employees)

joined := typed.HashJoin(seniors, depts,
    func(e Employee) string   { return e.DeptID },
    func(d Department) string { return d.DeptID },
    func(e Employee, d Department) Senior { ... })
```

Use `ssql.Record` for prototyping and dynamic schemas; switch to
`ssql/typed` when you know your schema and the pipeline is hot.

**Or skip the rewrite entirely** — `ssql generate go -pipeline '…'`
(typed is the default mode) translates a shell pipeline directly into a
typed Go program with auto-derived struct
types. The same prototype pipeline you'd run interactively becomes a
self-contained, compiled, schema-safe binary:

```bash
ssql generate go -pipeline 'ssql from employees.csv
    | ssql where -if years ge 5
    | ssql join departments.csv -using dept_id
    | ssql to csv seniors.csv' > pipeline.go
go run pipeline.go
```

Measured against the same shell pipeline run three ways (1M rows ×
1 join, see `cmd/ssql/codegen_bench_test.go`):

| Mode | Wall time | Peak RSS |
|---|---:|---:|
| CLI pipeline (interactive) | 3.08 s | 33 MB |
| `SSQL_MODE=record` codegen (Record) | 2.69 s | 910 MB |
| **`SSQL_MODE=typed` codegen** | **0.77 s** | **8.7 MB** |
| Typed vs CLI | **4.0× faster** | — |
| Typed vs Record codegen | **3.5× faster** | **104× less memory** |

**Typed mode runs the same pipeline across all cores when there is
parallelism to exploit.** A planner
inspects every stage and picks per-pipeline: parallel CSV read +
parallel `Where`/`HashJoin`/`GroupBy` + per-shard CSV output
buffers when reachable, serial `iter.Seq[T]` when a `sort` /
`distinct` / `to table` later in the pipeline forces it. No env-
var flip needed. Measured on a 32-core machine, 10 M-row corpus:

| Workload | typed-serial | **typed (planner-parallel)** | DuckDB |
|---|---:|---:|---:|
| Filter + write 7.25 M-row CSV | 5.7 s | **1.3 s (4.4× faster)** | 0.7 s |
| Group-by 1 000 dept_ids, count + sum + avg + min + max | 3.80 s | **0.95 s (4.0× faster)** | 0.39 s |

```bash
ssql generate go -run -pipeline 'ssql from data.csv
    | ssql group-by dept_id -count n -sum salary total -avg salary mean
    | ssql to csv'
```

The planner backs off to serial when a stage needs it: `from | sort |
to csv` reads with the serial `typed.ReadCSV`, because a global sort
would only pay for the parallel fan-in. A stage that has no typed form
yet runs on Records behind an adapter, so the parallel parse still
happens; the [Typed Reference](typed-reference.md) lists those stages,
and `generate go -explain` says which form each stage of your pipeline
got.

### Real-world: 14.6 M-row group-by on a 72-thread Xeon

Same pipeline (`from … | group-by relationship -count number | to table`)
run six different ways against a user-supplied 1.23 GB / 14.6 M-row
CSV on a dual-socket Xeon Gold 6154 workstation:

| Form | Wall | vs CLI baseline |
|---|---:|---:|
| Interactive CLI pipeline (3 processes via JSONL pipes) | 68.7 s | 1.0× |
| `SSQL_MODE=record` codegen (Record, 1 process) | 15.5 s | 4.4× faster |
| `SSQL_MODE=typed`, serial form (1 thread, struct types) | 22.0 s | 3.1× faster |
| `SSQL_MODE=typed`, planner-parallel (CSV, multi-shard) | 1.23 s | **56× faster** |
| `SSQL_MODE=typed`, planner-parallel (Parquet, single row group) | 0.76 s | **90× faster** |
| **`SSQL_MODE=typed`, planner-parallel (Parquet, 15 row groups, column projection)** | **0.32 s** | **215× faster** |

All four typed rows come from the *same* `SSQL_MODE=typed` — the planner
auto-selects the parallel form when reachable (the serial row is what it emits
when forced serial, shown here to isolate where the speed comes from).
`SSQL_MODE=parallel` is a deprecated alias for the same path.

The 215× from interactive CLI to typed-parallel-Parquet decomposes
into independent wins multiplied together: ~4× from collapsing 3
processes to 1 (no JSONL transit), ~13× from struct types over
`map[string]any` (no allocation per row, no GC), ~1.6× from
parallelism on the CSV path, ~1.6× from column projection on Parquet,
and ~2.4× from multi-row-group parallelism. The column projection is
automatic: the optimiser (`-O`, on by default in `generate go` and
`generate ssql`) reads the downstream stages and prunes the Parquet read
to the columns they use. `ssql to parquet` writes 1 M-row row groups by
default (`-row-group-size`), which is what the parallel reader shards
across.

[**Codelab →**](typed-codelab.md) | [**Reference →**](typed-reference.md) | Design notes: [typed codegen](research/typed-codegen-proposal.md), [parallel group-by](research/typed-groupby-parallel-proposal.md)
