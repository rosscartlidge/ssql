# Performance against the engines: what we found, what we fixed, what is left

Reference: DFC158
Created: 2026-10-28
Last modified: 2026-10-28

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
| `where -if relationship eq 7 \| include a_kind a_name z_name \| to csv` (2.7 M rows out) | 0.70 s | 1.51 s | **0.41 s** |
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
of the winner is the streaming write, which is bounded by formatting.

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

### 2.4 The streaming write is formatting-bound, and the sink buffers everything

The filtered write is the one row where DuckDB beats DataFusion and
typed Go is within 2× of the winner: 2.7 M rows of output, each
formatted once, bounds every engine. Typed Go's 8.1 GB peak RSS is the
documented trade of the parallel CSV sink (claude/concurrency.md §12:
each shard formats into its own buffer, dumped in shard order, 4.4×
faster than a `Serial()` fan-in). That trade was measured on a 7.25 M-
row write; on a 14.6 M-row input it is 2× the output size plus the
mapped input, and it is the largest RSS in the table by 4×.

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
clones only those), the SQL assembler makes it the SELECT list
(`translateInclude`, so the Rust lowering emits `select_columns`), and
`SSQL_MODE=schema` answers with the projection. The pruning rule
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
found both on the day.

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

All five fixes are on main, gated (vet, slim build, race packages,
full `cmd/ssql`, doc-test, equivalence, scale), and in CHANGELOG's
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

3. **The typed window's 35 s.** **Done 2026-10-28, §3.5**: 9.2 s. The
   guess here was half right — the sort was the cost, but its algorithm
   and its serial source, not the boxing; the profile said so before a
   line changed. What is left (a typed sort key, an unboxed result
   column) is in TODO with the numbers.

4. **The parallel CSV sink's memory.** 8.1 GB on the filtered write is
   2× the output plus the mapped input. The buffer-per-shard dump is
   4.4× faster than a `Serial()` fan-in, but a bounded alternative
   exists: each shard formats into chunks of a fixed size and a single
   writer drains the chunks in shard order as they complete (the first
   shard's chunks stream out while later shards are still formatting;
   peak is shards × chunk size, not the output). Measure against the
   dump on the 7.25 M-row write it was tuned on before changing the
   default; the time should hold and the RSS should fall by 4×.

5. **The Parquet scan and the join: profile first** (§2.5). A batch
   decode (`pqarrow` record batches into column slices, structs built
   per batch rather than per row) is the plausible lever for the scan;
   the join's hash build is the plausible lever there. Neither has a
   number behind it; both get a profile of the generated program before
   a line changes, and a note here either way.

6. **The exec path on big windows is documented as the wrong tool**, not
   a target. 219 s and 28 GB for 14.6 M records as maps is the record
   model's price; the fix is the typed program or `generate rust`, and
   `doc/performance.md` says so. No work planned.

7. **A benchmark driver in the repo.** The 2026-10-27 table was produced
   by scripts in a session scratchpad (`bench/run.sh`, `run2.sh`) that
   will not survive the session. A `scripts/bench-engines.sh` taking a
   Parquet file and running the seven pipelines through typed Go,
   DuckDB and `generate rust -build` (cargo present) on an idle machine,
   best of three, printing the table in `doc/performance.md` §1a's
   format, makes the comparison repeatable at each release instead of a
   one-off. It is not a gate; the scale gate is the gate.

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
