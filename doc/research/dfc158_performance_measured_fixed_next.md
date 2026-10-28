# Performance against the engines: what we found, what we fixed, what is left

Reference: DFC158
Created: 2026-10-28
Last modified: 2026-10-29

[Back to Index](./README.md)

**Status:** write-up of the 2026-10-27 measurements and the five fixes
that followed them (two on the 27th; the `cast` downgrade found by
writing this on the 28th, the CSV column projection and the typed
window's sort the same day), with a ranked list of what to do next. Ross
(2026-10-28): "could you write up a DFC on what we discovered about
performance, what we have fixed and what we can do". The measurements
are DFC157 §4's; the fixes are commits 3bd4919 (window), 436c8b0
(CSV scanner) and the `cast` template (§3.3), unreleased on main as of
this writing. Related:
[DFC084](./typed-performance-notes.md) (the typed runtime's
performance notes, 2026-04), [DFC113](./dfc113_scale_gate.md) (the
scale gate), [DFC114](./dfc114_performance_journey.md) (642 k to 98 M
rows per second), [DFC132](./dfc132_rust_target_datafusion.md) and
[DFC157](./dfc157_generate_rust.md) (`generate rust`).

## 1. The measurement that started it

`generate rust` (DFC157) put a third engine under the same pipelines
and, for the first time, the typed Go program had a vectorised
competitor on every shape rather than DuckDB on a cube. Machine A (24
cores, 62 GB), the 14.6 M-row file (660 MB Parquet, 1.23 GB CSV),
best of three, whole-process wall, output to `/dev/null`. The typed
column shows the number before the day's fixes and, where one
changed it, the number after.

| Pipeline | DataFusion | typed Go before → after | DuckDB |
|---|---:|---:|---:|
| `group-by a_kind relationship z_kind -count -cube` (the README cube) | **0.19 s** | 0.32 s | 0.36 s |
| `group-by relationship -count`, Parquet | **0.03 s** | 0.15 s | 0.03 s |
| `group-by relationship -count`, CSV | **0.24 s** | 1.63 → 1.05 → 0.39 s | 0.71 s |
| `where -if relationship eq 7 \| include a_kind a_name z_name \| to csv` (2.7 M rows out) | **0.70 s** | 1.51 s | 2.9 s (0.41 s in the first table was the display mode, §3.6) |
| 10 M rows, `where age gt 30`, three chained `join -using`, `count` | **0.26 s** | 0.89 s | 0.29 s |
| `cast -type relationship decimal:2 \| group-by a_kind -sum` | **0.04 s** | 1.29 → 0.23 s | 0.06 s |
| `window -partition a_kind -order a_name -sum relationship` then `group-by a_kind -max` | **5.7 s** | > 1800 s → 35 → 9.2 s | 14.0 s |

Peak RSS: cube 185 MB / 940 MB / 437 MB; CSV group-by 88 MB / 1.5 GB /
760 MB; filtered write 1.9 GB / 8.1 GB / 1.4 GB; window 1.7 GB / 3.8 GB
/ 3.0 GB (DataFusion / typed Go / DuckDB).

Three of the seven rows were pathologies, not engine differences, and
all three are fixed (§3): two were found on the 27th and the third,
the decimal row, while writing §2 of this document. The other four are
the honest gap between a row-at-a-time program over Go structs and a
vectorised executor over Arrow batches: 1.7× on the cube, 2–5× on the
scans and the join, and the one row where the Go program is within 2×
of the winner is the streaming write, which is bounded by formatting
(and where it beats DuckDB, §3.6).

## 2. What we discovered

### 2.1 The window was quadratic in both lanes, since it was written

`computeWindowFunc` (sql.go) and `typed.windowValue` computed one row's
value by scanning that row's frame, and over the default frame
(UNBOUNDED PRECEDING → CURRENT ROW) the frame always starts at the
partition's first row: n²/2 steps per partition for every sum,
average, count, extreme and rank. Measured before the cause was read:
exec 52.8 s at 50 k rows and 210.7 s at 100 k, typed 9.0 s and 29.8 s;
doubling the rows quadrupled the time. The 14.6 M-row benchmark (26
partitions of about 560 k rows) was still running at a 30-minute cap
on one core.

Only the `-presorted` streaming path was linear: it had had one-pass
accumulators since it was written and ring-buffer sliding ones since
DFC130; the DFC156 fold had a one-pass precompute
(`runningFoldValues`) for the same frame. The materialising path,
which is what every pipeline without `-presorted` runs, never got the
same treatment, and no fixture was large enough to notice: the
equivalence cases hold tens of rows, and the scale gate (DFC113) had
no window case. This is exactly the failure the "a fixture that fits
in a pipe buffer tests nothing about scale" rule describes, and the
rule was two months old when the quadratic was found by a benchmark
run for a different reason.

### 2.2 The CSV reader's cost was the splitter, not the decoding

A profile of the generated one-column group-by over the 1.23 GB file
(the program, not `Stream.Serial()`, whose per-row channel dominates
any profile): `encoding/csv` 64 % of CPU, a third of all CPU in the
collector. `encoding/csv` builds one record buffer per row and slices
every field out of it: 14.6 M allocations, about 1.2 GB of garbage,
and the write barriers and span scans that come with it. Pruning the
struct to one column had moved the time only from 1.9 s to 1.55 s,
which said the same thing: the decoding was cheap, the splitting was
not.

DFC084 §1 had recorded a byte-level reader TRIED in April and deleted,
17 % slower than `encoding/csv`, with the note that the remaining real
win was a SIMD delimiter scan. The difference in October: the parallel
reader holds the whole file as one mapped `[]byte`, so a scanner over
it IS that scan (`bytes.IndexByte` is assembly on amd64) and fields can
be views of the mapping rather than copies. The April result was about
the serial path's access pattern, not about byte-level readers.

### 2.3 The decimal row is a planner downgrade, not decimal arithmetic

Found while writing this DFC. `generate go -explain` on the decimal
pipeline:

```
[plan]   source: parallel (Stream[T]) — at least one downstream accepts ShapeStream
[plan]   ssql cast -type relationship decimal:2: accepts=iter.Seq[T], produces=iter.Seq[T], SerialOnly=true
[plan] downgraded ssql group-by a_kind -sum relationship total to serial alternative (upstream is iter.Seq[T])
[plan] downgraded ssql from parquet … to serial alternative
```

`cast`'s typed template (`commands/typed_cast.go`) declares
`SerialOnly`, so the planner reads the Parquet file serially, maps
serially and groups serially: the 1.29 s is the plain Parquet group-by
(0.15 s parallel) run on one core with a per-row struct rebuild in
between. `ssql.Decimal` is an `Int128` plus a scale and its `Add` on a
common scale is one checked add and one bound check (`decimal.go`);
the arithmetic is not the cost. A cast is a per-row map with no
cross-shard state, the most parallel stage there is, and the per-shard
map it needs has existed since the parallel PoC: `typed.StreamSelect`,
which `update`, `include`, `rename` and `exclude` all emit with the
serial `typed.Select` as the planner's alternative (`update`'s template
carries a comment recording the same downgrade, fixed there earlier).
The cast template predated the dual-template convention and was never
revisited; nothing failed, because every fixture was small. The other
`SerialOnly` typed templates are justified: `group-by -presorted` and
`-stream-expr` (contiguous keys, unmergeable fold state), `window` and
`union` (barriers). Fixed the same day, §3.3.

### 2.4 The streaming write is formatting-bound; the 7.9 GB was the reader's (corrected §3.8)

The filtered write was reported as the one row where DuckDB beat
DataFusion; it was not (§3.6: DuckDB's 0.41 s was its `duckbox` display
of 40 rows; writing the 2.7 M rows as CSV takes 2.9 s). DataFusion
0.70 s, typed Go 1.51 s, DuckDB 2.9 s: 2.7 M rows of output, each
formatted once, bounds every engine, and the Go program's row-wise
writer is the second fastest of the three. Typed Go's 8.1 GB peak RSS
was written up here as the parallel CSV sink's buffer-per-shard trade
(claude/concurrency.md §12). It was not: the output is 238 MB, the
same pipeline ending in `count` peaked at 7.4 GB, and the holder was the
parallel Parquet reader decoding whole row groups (§3.8). The sink's
trade is real and small.

### 2.5 The Parquet scan and the join: unprofiled, probably the row model

The Parquet group-by (0.15 s against 0.03 s) and the three-way join
(0.89 s against 0.26 s) have not been profiled. Both read Parquet
through `pqarrow` into Go structs one row at a time and hash on Go
values; DataFusion keeps the Arrow batches and hashes on columns. The
2.4× on the cube says what a vectorised engine buys on a shape where
the Go program is already parallel and column-pruned, and the join
gap is the same size. Nothing here suggests a pathology, and nothing
here should be touched without a profile of the generated program
first (DFC084's lesson held three times in one evening, see §3.2).

### 2.6 Method

- **Profile before assuming**, and profile the generated program. The
  first CSV cut was built on a plausible story (plain lines are the
  common case, send quoted ones to `encoding/csv`) and was 2× slower;
  the profile said half the file's lines quote a name with a comma and
  every fallback built a 4 KB reader.
- **A benchmark run with a gate competing for the machine lies.** The
  one-column CSV program measured 2.35 s under a full `cmd/ssql` run and
  1.55 s alone. Benchmarks run on an idle machine, best of three.
- **The scale gate is the only fixture that sees complexity.** The
  window quadratic, the CSV reader's newline index (a 117 MB slice built
  serially before any shard ran, half a second of the 0.85 s that was
  left) and the planner downgrade are all invisible at a thousand rows.
- **`-explain` is a performance tool.** The decimal row's cause was
  three lines of planner output; a profile would have said "serial"
  too, but the plan says why.

## 3. What we fixed (2026-10-27)

### 3.1 The window: one pass over the running frame

`runningWindowValues` (root `window_running.go`) and `runningSpecValues`
(`typed/window_running.go`) carry sum, average, count, count-field, min,
max, rank, dense rank, percent rank and cume dist across the sorted
partition in one pass over the running frame, with the same operations
in the same order as the scan it replaces, so a compensated float sum
carried forward IS the sum the scan from row 0 would compute and every
value is byte-identical; the whole-partition frame (no `-order`, no
bounds) computes each aggregate once and broadcasts it. Bounded ROWS
frames (O(n·k)), RANGE frames, lag and lead stay per row, which is what
their semantics say. Typed keeps a `done` mask per spec so the per-row
loop skips what the pass filled.

Proof: `TestWindowRunningMatchesPerRow` (400 records with ties, missing
values and int/float/string mixes; ten functions; both frames;
`reflect.DeepEqual` against `computeWindowFunc` at every position), the
equivalence window cases in every lane, and two new scale cases
(`window-running-frame-exec` 11.6 s and `-typed` 4.3 s over six
partitions of 500 k rows, against 60 s and 90 s budgets).

| Rows | exec before → after | typed before → after |
|---:|---:|---:|
| 50 k | 52.8 s → 0.15 s | 9.0 s → 0.02 s |
| 1 M | — → 2.3 s | — → 0.25 s |
| 14.6 M (the benchmark) | > 1800 s → 219 s, 28 GB | > 1800 s → 35 s, 3.8 GB |

CLAUDE.md's performance rules now say every window aggregate over the
running frame is carried in one pass and a new window function gets a
carried form or a note saying why not.

### 3.2 The CSV reader: a zero-copy scanner

`typed/csv_scan.go` with `typed/stream.go` and `typed/io.go`: byte-range
shards cut at a newline (no global newline index; each shard finds its
own lines with `IndexByte`); each line split by `bytes.IndexByte` into
`unsafe.String` views of the mapping; quoted fields handled in the
scanner (RFC 4180 within a line, a doubled quote unescaped into a
per-shard scratch); one `strings.Clone` per kept string column
(`rowSchema.copyCols`); decode visits only the columns the struct has
(`rowSchema.active`); `encoding/csv` only for a malformed line, for its
error. Three steps, each from a profile of the one-column program:

| Step | One-column wall |
|---|---:|
| before | 1.55 s |
| scanner for plain lines, `encoding/csv` for any line with a quote | 4.2 s (2× slower) |
| quoting in the scanner | 0.90 s |
| decode visits only active columns | 0.85 s |
| byte-range shards, no newline index | **0.39 s** |

The full seven-column struct went 1.63 s → 1.05 s, the two name
columns cloned 14.6 M times each being the remainder. Proof:
`TestReadCSVParallelMatchesSerial` (quoted commas and doubled quotes,
CRLF, blank lines, padded cells, empty optional and required cells, a
column the struct lacks; five shard counts against the serial
`encoding/csv` reader), strings survive the mapping (the file is
removed after the read), field-count and bare-quote errors as
`encoding/csv` names them, the typed race suite, the full equivalence
suite, the scale gate.

### 3.3 `cast` keeps the pipeline parallel (2026-10-28)

`commands/typed_cast.go` emits both templates, as `update` does:
`typed.StreamSelect` over the stream with `typed.Select` as the
`AltCodeIfSeq`, and `Accepts`/`Produces` `ShapeStream`. `-explain` on
the decimal pipeline now shows no downgrade: the Parquet read stays
parallel, the cast maps per shard, the group-by stays
`GroupByParallel`. The generated program's output is byte-identical to
the serial one (the same closure, per shard).

| Program | Wall (three runs) | Peak RSS |
|---|---:|---:|
| before: serial read, map and group-by | 1.27–1.41 s | 585 MB |
| after: parallel on both sides of the cast | **0.22–0.26 s** | 470–640 MB |

Proof: `TestTypedCastKeepsPipelineParallel` (`cmd/ssql/generation_typed_test.go`)
generates `from csv | cast | group-by` in typed mode and asserts the
source holds `ReadCSVParallel`, `StreamSelect` and `GroupByParallel`
and not the serial `typed.Select`, then runs it; with the old template
stashed back in it fails on all three markers (watched). The
equivalence cast cases pin the values in every lane. A scale case was
considered and not added: at the gate's 3 M rows a serial plan still
fits any honest budget, so a wall-time ceiling would not discriminate;
the plan assertion is the gate.

### 3.4 The optimiser prunes CSV reads (2026-10-28)

`from csv` and `from tsv` take `-columns`, with Parquet's semantics:
the named columns in the order named, an unknown or repeated name a
loud error listing the header. Every lane honours it — exec and record
mode `Project`, the typed struct holds only those columns
(`lib.TypeOptions.Columns`; the zero-copy scanner then decodes and
clones only those), the SQL assembler wraps the projection around the
source, as the Parquet one is (the Rust lowering emits `select_columns`
on the leaf), and `SSQL_MODE=schema` answers with the projection. The pruning rule
(`ruleSourceColumnPruning`, the renamed Parquet rule) adds it to a
single local CSV/TSV file when every column the pipeline uses is in the
header and the header has more, rewriting a bare `from F.csv` to
`from csv F.csv`, where the flag lives. Pushdown (`--`) and `-records`
are refused with it. A projected struct is named by its columns
(`EmployeesRowOfDept`, Parquet too), so the unpruned row type of the
same file in a process substitution's inner pipeline (which bash runs
unoptimised) cannot clash with it; and a source whose pipeline names a
column with `-arg` (a name beginning with `-` or `+`) is left unpruned,
because a flag's value has no `-arg` escape — the full `cmd/ssql` run
found both on the day. The release's `make lanes` found a third: the
first cut put the projection in the layer's select list, so a later
`include` wrapped the layer as a subquery with the sort's ORDER BY
inside it; DuckDB honours a subquery's order by accident, DataFusion
returned the file's first three rows. The projection now wraps the
source (`csv_prune_sort_include_limit` pins it; the local lanes alone
could not have, which is what the oracle lanes are for). The rust lane
then found that with `-empty zero` the projection must go INSIDE the
coalesce wrapper, since the Rust lowering projects the leaf before
running that wrapper's SQL over it; it does, and the wrapper names only
the projected columns.

| Program | Wall (three runs) |
|---|---:|
| full seven-column struct (after the scanner) | 1.60–1.68 s |
| the optimiser's `-columns relationship` | **0.38–0.41 s** |

Proof: `csv_list_columns` (a non-file order with a Golden, every lane)
and `csv_prune_group_by` (the rule's output against exec) in the
equivalence suite; `TestGenerateGoPrunesCSVColumns` (typed struct pruned
to the one column, record mode projected, bare form rewritten);
`TestTranslateFromCSVColumns` (the value is a column, not a second
file); the sampler and helper unit tests.

### 3.5 The typed window: 35 s → 9 s (2026-10-28)

The profile of the generated program, not the guess in §4 item 3: the
sort was 21 s of the 35 — `sort.SliceStable`'s symmerge (O(n log² n)
moves through a reflection swapper) with the string comparator doing
`<` then `>` (two byte comparisons on every "greater"); the partition
key through `fmt.Sprint` 1.1 s; the boxed results and GC small. Four
steps, each measured on the program:

| Step | Wall |
|---|---:|
| before | 35.4 s |
| pdqsort with the row index as the tiebreak (the one stable order, far fewer moves, no reflection); partitions sorted and evaluated concurrently; one flat results slice; `strconv`/`strings.Compare` in the template | 14.5 s |
| the sort inside a large partition chunked across cores and merged pairwise (a third of the file sits in ONE partition of 4.7 M rows, so the per-partition parallelism left its sort as the critical path) | 13.2 s |
| `typed.WindowParallel`: the window takes the source's Stream, materialising the shards concurrently in shard order, instead of the planner reading the source serially (2.4 s) into one slice grown by appends (1.9 s) | **9.2 s** |

Shard order is input order for every parallel source — a CSV shard is a
byte range, a range shard an interval — and from today the Parquet
reader's shards are contiguous runs of row groups too (they were
round-robin), so the window's first-seen partition order and the
parallel sinks' output order are the file's. The small benchmark
(`BenchmarkWindowRunningSumString`, 200 k rows, string key): 200 ms →
47 ms, two allocations per row → one.

What remains is memory-bound: 15 s of CPU in `cmpbody` across the
sorting goroutines, each comparison chasing two row structs and two
string bodies at random across 600 MB, and the emit loop reading rows in
sorted order from file-ordered storage (2 s). DataFusion's 5.7 s is 1.6×
away. A typed sort key (the order column copied once into a contiguous
slice beside the index) would make the compares sequential and is the
next shave if one is wanted; the boxed result per row (one allocation,
14.6 M of them) is the other.

Proof: `TestWindowParallelMatchesWindow` (shards interleaving the
partitions, ordered and unordered clauses, ties: the same rows in the
same order), `TestSortIdxMatchesStable` (the chunked sort against
`sort.SliceStable` on heavy ties, both sides of the chunk threshold, one
core and many), `TestReadParquetParallelShardsAreInFileOrder`,
`TestTypedWindowKeepsSourceParallel` (the plan), the 35 window
equivalence cases, the typed race suite, and the scale gate's typed
window case at 2.4 s (was 4.3).

### 3.6 The driver, and what its first run found (2026-10-28)

`scripts/bench-engines.sh FILE.parquet` (§4 item 7) runs the seven
pipelines through the typed Go program, DuckDB in CSV mode and the
generated Rust program, best of N, whole-process wall and peak RSS,
output to /dev/null, and prints this table. Its run on main after §3.7 and §3.8 (the first run, on the released
v4.129.0, read 14.4 s in the window's Rust cell and 7.9 GB in the
filtered write's typed one), machine A, idle:

| Pipeline (14.6 M rows; the join 10 M) | DataFusion (generated Rust) | ssql `generate go` (typed) | DuckDB 1.5.0 |
|---|---:|---:|---:|
| `group-by a_kind relationship z_kind -count -cube` (the README cube) | **0.18 s** | 0.26 s | 0.35 s |
| `group-by relationship -count`, Parquet | **0.02 s** | 0.14 s | 0.03 s |
| `group-by relationship -count`, the same rows as CSV | **0.24 s** | 0.32 s | 0.75 s |
| `where -if relationship eq 7 \| include a_kind a_name z_name \| to csv` | **0.68 s** | 1.46 s | 2.97 s |
| 10 M rows, `where age gt 30`, three chained `join -using`, `count` | **0.25 s** | 0.51 s | 0.35 s |
| `cast -type relationship decimal:2 \| group-by a_kind -sum` | **0.05 s** | 0.26 s | 0.06 s |
| `window -partition a_kind -order a_name -sum relationship` then `group-by a_kind -max` | **5.8 s** (14.4 s before §3.7) | 9.6 s (8.9–9.6 across runs) | 13.2 s |

Peak RSS, same order: cube 184 MB / 506 MB / 427 MB; Parquet group-by
68 MB / 199 MB / 37 MB; CSV group-by 77 MB / 1.2 GB / 666 MB; filtered
write 1.7 GB / 4.0 GB / 1.3 GB; join 310 MB / 405 MB / 353 MB; decimal
98 MB / 320 MB / 52 MB; window 1.7 GB / 4.2 GB / 2.9 GB.

The run is the whole table of §1 after the five fixes, and it found
three things on the way:

1. **The DuckDB filtered-write number in §1 was wrong.** 0.41 s was
   DuckDB's default `duckbox` display, which prints 40 of the 2.7 M rows
   and a summary; writing them as CSV takes 2.9 s. The claim that the
   streaming write was the one shape DataFusion did not win is
   withdrawn (§2.4 corrected; DFC157 §4 carries the same note). The
   driver runs DuckDB with `-csv`, so the mistake cannot recur.
2. **`generate rust -build` built nothing on a pipeline the optimiser
   rewrites** (since 4.128.0): the re-execution that applies the rewrite
   carried `-run`, `-func` and `-time` but not `-build`, so the program
   went to stdout and the command exited 0. Fixed;
   `TestGenerateRustBuildSurvivesOptimiser` fails on the old code.
3. **The generated Rust window is slower than DataFusion's own SQL
   plan**: 14.4 s against 5.9 s for the same pipeline through
   `generate sql -dialect datafusion`. The DataFrame lowering keeps the
   window stage's output order as a `sort` of 14.6 M rows that the
   aggregate after it never needs; the SQL optimiser drops the same
   ORDER BY inside a subquery. Fixed the same day, §3.7.

Two more readings of the table: the typed join (0.51 s, was 0.89 s on
the 27th) is the zero-copy CSV scanner reaching the join corpus, which
is CSV; and a first cut of the driver wrongly ended the join row in
`count | to csv` — `count` is a sink, nothing can follow it, and exec
says so; the generated code merely fails to compile instead, which is
a missing message, not a missing feature (TODO).

### 3.7 The Rust lowering leaves out a dead sort (2026-10-28)

DataFusion's plan for the SQL had no sort below the aggregate at all:
the subquery's ORDER BY is gone from the logical plan, the only
`SortExec` is the window's own requirement, run per hash partition in
parallel, and the aggregate runs in sorted mode on those partitions.
The DataFrame program said `.sort(a_kind, a_name)` after the window —
the window stage's output order, which ssql's own lanes keep — and the
API keeps a sort it is given: a global merge-sort of 14.6 M rows and a
single-partition aggregate after it. `sortIsDead` in the renderer
leaves a layer's sort out when the layer selects no rows by it (no
LIMIT/OFFSET of its own) and its consumer is an aggregate none of
whose functions reads arrival order (first, last, collect, string-agg
read it; mode, arg-max and arg-min break ties by it; the rest do not).
A sort feeding a limit, an arrival-order aggregate, a set operation, an
escape hatch or the output stays.

| Program | Wall (three runs) |
|---|---:|
| with the sort | 14.4 s |
| without | **5.8 s** |

Same output. `TestGenerateRustDeadSort` pins nine shapes; the rust lane
runs every equivalence case through the renderer. The table in §3.6 is
updated.

### 3.8 The Parquet reader decodes in batches (2026-10-28)

The measurement before the design: the filtered write ending in `count`
instead of `to csv` still peaked at 7.4 GB, a hard memory limit of
1.5 GiB left 4.8 GB resident, the collector's trace put the live heap
at 5.3 GB, and the allocation profile put 4.3 of the 7.7 GB allocated
in Arrow's string builders inside `ReadRowGroups`. Both typed readers
called it: the serial one for every row group of the file as one
table, the parallel one for a whole row group per shard — the
reader's own comment said peak memory was shards × row-group size,
and with 1 M-row row groups and two string columns that was the
number. DataFusion reads batches of a few thousand rows and never
holds a row group: 1.7 GB.

Two changes in `typed/io_parquet.go`. The readers take a record reader
over the same row groups (`GetRecordReader`, 64 K rows a batch) and
decode and release one batch at a time (`yieldParquetBatches`,
`yieldRecordRows`; the whole-table walker is gone): live heap 5.3 →
1.8 GB mid-run, with 1.2 GB of what remained the parquet layer reading
each selected column's chunk whole into a buffer, per shard. The
reader properties now stream a column chunk through a 4 MB buffer
(`BufferedStreamEnabled`): the shards' read-ahead is 240 MB in the
profile and the live heap peaks at 2.3 GB.

| Program (14.6 M rows) | Resident before → after | Wall |
|---|---:|---:|
| `where … \| include … \| count` (the scan without a sink) | 7.4 GB → 3.9–5.0 GB | 1.1 s (same) |
| the README cube | 700 MB → 500 MB | 0.30 → 0.25 s |

Same output in both. What the resident size still carries is the
collector's headroom over a live set that is now mostly batches and
read-ahead; the allocation RATE (a Go string per string cell, 14.6 M
rows) is what is left to cut, and that is the reader's decoders, not
its buffers. `TestReadParquetBatchesAreInvisible` forces several
batches per row group on a small file and compares serial and parallel
reads to the rows written; the Parquet equivalence and scale cases and
the typed race suite pin the rest.

### 3.9 The typed window's sort key, and where the rest of its time is (2026-10-28)

The window clause takes the single order field as its own type
(`KeyString`, `KeyInt`, `KeyFloat`, emitted by the template beside
`Compare` for a string, int64 or float64 column), and a partition is
sorted as (key, index) pairs that move together — the sort reads the
slice it is sorting, sequentially, and the row once — instead of
comparing rows through `Compare`. A string key carries its first eight
bytes as one integer beside it, compared first; only equal prefixes
read the bodies (the benchmark's names are device and port strings,
distinct within eight bytes for all but a few groups). `Compare` still
decides peers and sorts the other shapes. `TestWindowKeySortMatchesCompare`
holds the two paths to the same rows, ties in input order, a NaN equal
to everything as the generated comparator has it, partitions on both
sides of the chunk threshold, ascending and descending.

The program: 9.0 → 8.5 s. The small benchmark (25 k-row partitions,
in cache) 47 → 51 ms: the pair extraction is overhead where the rows
were already cached. A profile said the key had done its job (the
compares no longer touch rows) and timing the phases said why the wall
hardly moved — with 24 cores the sort was already off the critical path
once it was parallel:

| Phase | Wall |
|---|---:|
| materialise the shards (parallel) | 0.71 s |
| concatenate them into one slice (serial copy, first-touch faults) | 0.76 s |
| partition map (serial: a key string and a map lookup per row) | 1.08 s |
| sort and the one-pass specs (parallel over partitions) | 2.33 s |
| emit (rows read at random in sorted order, the boxed results, the group-by after) | 3.68 s |

Three of those are serial and parallelisable without changing a
result: the concatenation (each shard copies into its own range of the
slice), the partition map (a map per range of rows, lists concatenated
in range order — the same first-seen order and ascending indices), and
the random row reads of the emit (each partition gathers its sorted
rows and results into contiguous output while it still has the
indices, so the emit walks memory in order). Together they are about
2.5 s of the 8.5; that is §3.10. DataFusion's
5.8 s is a global sort of 14.6 M rows and a sorted-mode aggregate on
24 partitions; the typed window is one process doing the same work
row by row, and 6 s is a fair floor for it.

### 3.10 The typed window's serial phases (2026-10-29)

The three serial phases of §3.9 now run on every core, and the
estimate was low: the program went 8.0 → 5.1 s (best of three on the
same idle machine; the 8.5 of §3.9 was a busier run), under
DataFusion's 5.8. None of it changes a result.

- **The concatenation.** `WindowParallel` sizes the slice from the
  shards' counts and each shard copies into its own range
  concurrently; the appends ran on one core, every page first-touched
  by it. 0.76 → 0.03 s.
- **The partition index.** `partitionIndex` keys the rows as ranges on
  every core, each range keeping its own map and first-seen order; the
  ranges merge in order, so a partition's first range is the first row
  that bears it and its indices concatenate ascending — the one map a
  single pass built. Each partition's slice is allocated at its exact
  length from the ranges' totals and the ranges copy into it
  concurrently, which also halved the allocations of the small
  benchmark (the single map grew every partition's slice by appends).
  1.08 → 0.20 s.
- **The gather.** After the clauses, the rows and their result slices
  are gathered into output order as ranges on every core (0.41 s of
  parallel cache misses), and the emit walks two slices in order:
  3.68 → 1.63 s, the rest being the `build` closure, the boxed
  result's read and the serial group-by after it. The copy is another
  slice of rows (820 MB for the three-column benchmark row); the
  materialised shards are garbage by then, and the resident size did
  not move (4.8–5.4 GB either way).

The phases of the 5.1 s program: materialise 0.74, concatenate 0.03,
partition index 0.20, sort and specs 2.19, gather 0.41, emit 1.63.
What is left is parallel (the sort and specs: the 4.75 M-row partition
dominates, and its sort is already chunked across cores) or is the
price of the row model (the emit's build and the group-by). Two things
could still shave it, both in TODO: an unboxed result column per spec
(one allocation per row and the type assertion in `build` go), and the
window producing a `Stream` whose shards are the sorted partitions'
output, so the group-by after it runs its parallel form — a planner
change that alters shard order, so it needs its own look.

The tests are the two that an independent oracle allows:
`TestPartitionIndexRangesMatchOnePass` holds the index built over 2, 7
and 24 ranges to the one built in one pass (a key a few rows share,
one most rows have to themselves, one that first appears in the last
range, no key), and `TestWindowParallelLargeMatchesReference` holds the
whole parallel window, past the range threshold, to a reference
written with `sort.SliceStable` over the rows alone. Both failed when
the range merge ran backwards.

The lesson of §3.9 held up: the phases named the three serial steps,
each a few dozen lines, worth 2.9 s between them; the profile had
named the sort, which was worth 0.5. On a many-core machine a serial
phase between parallel ones IS the critical path while it shows a few
percent of the CPU profile.

All fixes are on main, gated (vet, slim build, race packages, full
`cmd/ssql`, doc-test, equivalence, scale), and in CHANGELOG's
Unreleased section.

## 4. What we can do, ranked

Expected win first, cost second. Each item that changes a
perf-sensitive path adds a scale case (DFC113) and, where results
could change, an equivalence case; none of them changes a result.

1. **`cast` keeps the pipeline parallel** (§2.3). **Done 2026-10-28,
   §3.3**: dual templates on `typed.StreamSelect`, 1.29 s → 0.23 s,
   about 20 lines. The audit of the other per-row map templates
   (`update`, `include`, `rename`, `exclude`) found them already on
   `StreamSelect`; `cast` was the only one left, and the `SerialOnly`
   declarations that remain are each justified at the declaration.

2. **CSV column projection in the optimiser.** **Done 2026-10-28,
   §3.4**: `from csv/tsv -columns` in every lane and the pruning rule
   extended from Parquet to a single local CSV/TSV file; the CSV
   group-by runs in 0.39 s through the optimiser, between DataFusion
   and DuckDB.

3. **The typed window's 35 s.** **Done 2026-10-28/29, §3.5, §3.9 and
   §3.10**: 9.2 s, 8.5 s with the typed sort key, 5.1 s with the
   serial phases parallelised — under DataFusion's 5.8. The guess here
   was half right — the sort was the cost, but its algorithm and its
   serial source, not the boxing; and once parallel, the sort left the
   critical path, so the key bought little and the three serial phases
   the phase timing named bought 2.9 s. The unboxed result column and
   a `Stream` output for the group-by after it stay in TODO.

4. **The filtered write's memory.** **Done 2026-10-28, §3.8**, and
   not where this item said: the sink holds 238 MB; the typed Parquet
   reader held whole row groups. Batched, streamed decoding took the
   scan from 7.4 GB resident to about 4, live 5.3 → 2.3 GB, and the
   cube got faster. The sink's chunked drain stays unbuilt: it would
   save 200 MB.

5. **The Parquet scan and the join: profile first** (§2.5).
   **Measured 2026-10-29, neither built.** The filtered write (1.3 s
   wall, 11 s CPU over the file's 15 row groups): snappy decompression
   28% — and the same 3.1 s with klauspost's decoder registered through
   `compress.RegisterCodec`, a negative result; Arrow's byte-array
   record reader appending every string value into a doubling builder
   20%; the where, select and CSV write downstream 14%; GC and malloc
   11%; ssql's decoders about 5%. The guess that the typed decoder
   copies each string cell was wrong: it aliases Arrow's values buffer,
   which is why a selective filter pins every batch (the 4 GB). The one
   lever is reading byte-array columns through the column chunk reader
   with the values aliasing the page, worth about 20% of CPU for a
   second string decode path — in TODO, not worth it against
   DataFusion's 0.68 s. The join (0.47 s wall, 7.7 s CPU on 24 shards):
   the CSV parse 35% (`ParseFloat` 8%, `TrimSpace` 5%, the line split
   11%), the three string-keyed probes 31% (`mapaccess1_faststr` 15%),
   GC and allocation 15%; the three build sides are too small to appear.
   No serial phase, no single lever above a few percent: the row
   model's price, spread evenly. Typed Go stays second on both rows and
   the table says why.

6. **The exec path on big windows is documented as the wrong tool**, not
   a target. 219 s and 28 GB for 14.6 M records as maps is the record
   model's price; the fix is the typed program or `generate rust`, and
   `doc/performance.md` says so. No work planned.

7. **A benchmark driver in the repo.** **Done 2026-10-28**:
   `scripts/bench-engines.sh FILE.parquet` runs the seven pipelines
   through the typed Go program, DuckDB and the generated Rust program
   (cargo present), best of N, whole-process wall and peak RSS, output
   to /dev/null, and prints the table in `doc/performance.md` §1a's
   format with the output row counts checked across engines. The column
   names are options (`-k -s -v -k2 -s2 -eq`), the CSV twin is written
   once into the cache, the join row runs when the 10 M-row corpus is
   present. The 2026-10-27 table was produced by scratchpad scripts; this
   is the same method, so a change is measured in one command and a
   release note's numbers are reproducible. It is not a gate; the scale
   gate is the gate.

What is NOT on the list: a columnar or vectorised runtime for typed Go.
DFC132's reasoning stands: a third runtime would own the semantics a
third time, and the engine that already does this well is one
`generate rust` away. The typed program's floor is the row model, and
the remaining 2–3× on scan-and-aggregate shapes is the price of that
model; the items above are the pathologies and the planner gaps on top
of it, which are worth 3–8× each where they apply.

## 5. What this says about the rules

- The scale gate's coverage, not its budgets, is what finds complexity
  bugs. Every perf-sensitive path gets a case when it is written, and a
  quarterly pass over the commands with none (today: `cast`, `join`,
  the sinks) is cheaper than a 30-minute benchmark timeout.
- A template's `SerialOnly` is a performance claim and should be
  justified in a comment at the declaration, as `group-by`'s is. `cast`'s
  was not, and it serialised every pipeline it appeared in; a
  convention that was adopted for `update` had never been applied to
  its sibling, which is the "bug fixed in one path is still live in the
  others" rule at the planner level.
- Measure against the engines on every release, from a script in the
  repo. The 2026-10-27 table found two pathologies and a planner gap in
  an afternoon; the previous engine comparison was 2026-09-15 and
  measured one pipeline.

## 6. References

- [DFC157 §4](./dfc157_generate_rust.md) — the measurement; §7 the
  window fix note.
- [DFC084 §1](./typed-performance-notes.md) — the April byte-level reader
  and its "Revisited 2026-10-27" paragraph.
- [DFC113](./dfc113_scale_gate.md) — the scale gate and the fixture rule.
- [DFC114](./dfc114_performance_journey.md) — the 642 k → 98 M rows/s
  ladder the typed program climbed.
- [DFC132](./dfc132_rust_target_datafusion.md) — why no Rust runtime;
  revisited by DFC157 as a lowering.
- `doc/performance.md` §1a — the published table; `doc/research/TODO.md`
  — items 2 and 3 above as recorded on 2026-10-27.
- Journal `journal/2026-W44.md`, entries "the window was quadratic; now
  one pass" and "the typed CSV reader: 1.55 s → 0.39 s".
- Code: `window_running.go`, `typed/window_running.go`,
  `typed/csv_scan.go`, `commands/typed_cast.go`, `typed/stream.go`.
