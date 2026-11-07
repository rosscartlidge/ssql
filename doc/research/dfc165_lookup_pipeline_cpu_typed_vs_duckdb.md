# The lookup pipeline's extra CPU: typed Go against DuckDB

Reference: DFC165
Created: 2026-11-07
Last modified: 2026-11-07

[Back to Index](./README.md)

Status: **measurement, 2026-11-07; §6's first item built, see §8.** Every variant in §3 to
§5 is a hand-edited copy of a generated program, so it shows the size of a win and is
not a design. §6 ranks what to build. Follows
[DFC158 §3.15](./dfc158_performance_measured_fixed_next.md), which introduced
this pipeline.

## 0. The prompt

Ross ran his lookup pipeline over the 1.2 GB `shuffled.csv` (14,628,366 rows)
through both engines and wrote "So close :-)":

| | Wall | User CPU | Sys CPU |
|---|---:|---:|---:|
| `generate sql -run` (DuckDB) | 1.010 s | 5.2 s | 1.0 s |
| `./xxx` (`generate go -build`) | 1.040 s | 15.8 s | 0.8 s |

Then: "profile the typed Go program and find the extra CPU - it would be fun to
see...". The pipeline:

```bash
ssql from csv shuffled.csv \
  | ssql join kind.csv -on a_kind kind -as kind_name a_kind_name - -on z_kind kind -as kind_name z_kind_name \
  | ssql join relationship.csv -using relationship \
  | ssql include a_name a_kind_name relationship_name z_kind_name z_name \
  | ssql where -if a_kind_name eq EK_PORT -if relationship_name eq RK_TRAVERSES \
  | ssql to csv graph.csv
```

It writes 103,037 rows, so the filter drops 99.3% of its input. Ross's run also had a
bare `limit` after the source. With no number that means no limit, so the stage
leaves the plan and did not trigger the serial rule of DFC164 §7.

## 1. The answer

- **It was not a tie.** DuckDB alone takes 0.81 s. The rest of the 1.01 s is ssql
  translating the pipeline (the difference between two sessions, about 0.2 s). The
  Go time is the program alone.
- **Most of the extra CPU appears only in parallel.** On one P-core the program
  takes 7.2 s against DuckDB's 3.3 s. This machine has 8 fast cores and 16 slower
  ones, and Go spreads onto all of them, about 16 CPUs' worth, where DuckDB uses about 6.
- **The GC runs 160 times in one second,** and that costs more wall time than CPU.
  `GOGC=800` alone took the unchanged program to 0.77 s, ahead of DuckDB.
- **The variants together reach 0.54 s** (0.56 s in a repeat), 8.6 s of CPU, against
  DuckDB's 0.81 s and 4.1 s.
- **Most of what remains looks like string copies** for rows the filter then drops.

## 2. Method

Machine: Intel Core Ultra 9 275HX. Eight P-cores (`cpu_core` 0-7, 5.3 to 5.4 GHz)
and 16 E-cores (`cpu_atom` 8-23, 4.7 GHz), no SMT. Load average under 0.5. DuckDB
1.5.0 with its default 24 threads, tree at `dfeee4bc`.

Each timing is `/usr/bin/time` around the whole process, writing the CSV to a
scratch file. Unless a row says otherwise it is a median of five. §3's rows are
medians of three, and §5's core-pinned rows of five. The generated program, the two-edit
variant and DuckDB wrote identical rows, compared as sorted files, and the filter-only and
no-sequence variants the same line count. Only the §4 attribution row writes different
columns.

To reproduce the profile: `generate go` the pipeline into a scratch module whose
`go.mod` has `replace github.com/rosscartlidge/ssql/v4 => ~/src/ssql`, and add
`pprof.StartCPUProfile` around `ssql.Run(run)` in `main`. Pin with `taskset`. In the
variants the ids are hard-coded (`EK_PORT` is kind 114, `RK_TRAVERSES` is relationship 4).

**Not measured.** `perf` is refused (`perf_event_paranoid` is 4), so there are no
instruction or cycle counts. That leaves the split between E-core cost and memory
contention in §3 as an inference from CPU time. The reasons given for DuckDB's
behaviour are read from its `EXPLAIN ANALYZE` and not from its source.

## 3. Where the 11.5 s goes

The same program pinned to one core type (median of three):

| User CPU | One P-core | One E-core | P-cores only | E-cores only | All 24 |
|---|---:|---:|---:|---:|---:|
| Typed Go | 7.18 s | 9.92 s | 8.19 s | 13.54 s | 15.66 s |
| DuckDB | 3.29 s | 4.34 s | 3.27 s | 4.37 s | 4.20 s |

| Wall | One P-core | One E-core | P-cores only | E-cores only | All 24 |
|---|---:|---:|---:|---:|---:|
| Typed Go | 7.26 s | 10.01 s | 1.13 s | 1.13 s | 1.01 s |
| DuckDB | 4.08 s | 4.99 s | 0.90 s | 0.72 s | 0.83 s |

An E-core costs 1.38 times a P-core for Go's work and 1.32 times for DuckDB's. The
gap of 15.66 − 4.20 = 11.5 s:

| Cause | CPU | How it is known |
|---|---:|---|
| Real extra work on the same core | 3.9 s | 7.18 − 3.29, measured |
| GC frequency | 1.9 s | 15.66 − 13.79 at `GOGC=800`, measured |
| E-cores and contention, net of DuckDB's own 0.9 s | about 5.7 s | the remainder |

Those three do not come from one controlled experiment, so read them as sizes rather
than exact shares. Go alone goes from 7.18 s to 8.19 s on the P-cores and from 9.92 s
to 13.54 s on the E-cores, so running in parallel costs noticeably more on the E-cores.

Go's extra CPU buys something. Its speed-up on 24 cores is 7.2 times (7.26 to 1.01 s)
and DuckDB's is 4.9 times (4.08 to 0.83 s). DuckDB's `EXPLAIN ANALYZE` charges
`READ_CSV` 15.1 s of thread time when the whole process used about 5 s of CPU, which
reads as threads waiting more than working; its wall time barely moves past 8 cores.
It is also not monotonic: 16 E-cores (0.72 s) beat all 24 (0.83 s). That was not
investigated.

### The profile by bucket

Flat samples, classified by hand from `pprof -top`. The totals are lower than user CPU
because the profile covers `run` only:

| Bucket | One P-core | All, `GOGC=800` | All 24 |
|---|---:|---:|---:|
| CSV split (`splitCSVLine`, `IndexByte`) | 1.56 s | 3.20 s | 2.63 s |
| Parse cells (`strconv`, decoders, `TrimSpace`, `Clone`) | 1.19 s | 2.77 s | 2.53 s |
| GC mark and sweep | 0.98 s | 0.93 s | 3.07 s |
| Lookup join, without the map | 0.75 s | 1.29 s | 1.09 s |
| Map lookup | 0.39 s | 1.21 s | 1.01 s |
| Select, where, sink | 0.46 s | 1.07 s | 0.89 s |
| Allocator | 0.35 s | 0.90 s | 0.73 s |
| Hash join | 0.66 s | 0.79 s | 0.57 s |
| Runtime other | 0.23 s | 0.19 s | 0.90 s |
| Scheduler, idle spin | 0.00 s | 0.04 s | 0.58 s |
| Scan loop, `memmove` | 0.18 s | 0.37 s | 0.36 s |
| **Total** | 6.75 s | 12.76 s | 14.36 s |

GC is the one bucket that `GOGC` changes by a factor of three. Most other buckets
grow 1.5 to 2.6 times between one P-core and all 24. An E-core alone accounts for 1.38
times, so a profile at 24 cores overstates their real cost, and the rest is presumably
contention, though without `perf` that stays an inference. Within the lookup probe, 2.48 s of its 4.02 s
is the call that builds and yields the merged row (`typed.lookupProbe`).

## 4. The wins, measured

All 24 cores, median of five:

| Program | Wall | User | Sys | RSS |
|---|---:|---:|---:|---:|
| DuckDB | 0.81 s | 4.11 s | 0.77 s | 679 MB |
| Go as generated | 1.01 s | 15.79 s | 0.72 s | 1232 MB |
| `GOGC=400` | 0.79 s | 14.29 s | 0.46 s | 1328 MB |
| `GOGC=800` | **0.77 s** | 13.86 s | 0.48 s | 1441 MB |
| `GOGC=1600` | 0.73 s | 13.66 s | 0.38 s | 1491 MB |
| `GOGC=off` | 0.87 s | 12.34 s | 1.69 s | 3515 MB |
| `GOGC=off GOMEMLIMIT=4GiB` | 0.88 s | 12.34 s | 1.76 s | 3514 MB |
| Filter before the joins | 0.83 s | 12.11 s | 0.58 s | 1234 MB |
| ...and the two unread columns gone | 0.76 s | 10.88 s | 0.63 s | 1246 MB |
| ...and a cheaper trim | 0.74 s | 10.58 s | 0.52 s | 1236 MB |
| ...and `GOGC=800` | **0.54 s** | 8.57 s | 0.44 s | 1454 MB |
| Attribution only: no `a_name` or `z_name`, default GC | 0.40 s | 7.18 s | 0.27 s | 1213 MB |

### 4.1 The GC

`GODEBUG=gctrace=1` shows 160 cycles in about a second, with lines like
`22->32->17 MB, 26 MB goal` and `35->38->19 MB, 38 MB goal`. Almost nothing stays
live: rows die within their shard, so the goal stays small and every core allocating
reaches it again within milliseconds.

The wall gain is larger than the CPU gain (24% against 12% at `GOGC=800`), and `gctrace`
reports the GC at only 9% of CPU, so CPU alone does not explain it. That was not
investigated. Turning the GC off is slower than `GOGC=1600`, with sys CPU going from
0.38 s to 1.69 s and RSS from 1.5 to 3.5 GB; the likely cause is faulting in fresh memory
instead of reusing it, but that was not separately measured.

### 4.2 The filter

The generated program filters last: all 14.6 M rows go through both joins and the
projection first. Filtering on the two keys straight after the read saved 3.7 s of CPU
and 0.18 s of wall. DuckDB does part of this. Its plan puts each predicate on the small
table (`FILTER`, one row, over `kind.csv` and over `relationship.csv`), so 4,747,193 rows
leave the first join and 103,037 the last. It still scans all 14.6 M rows, since the plan
shows no filter on the scan.

### 4.3 The unread columns

`a_sequence` and `z_sequence` are never read, and removing them saved 1.2 s. The
optimiser already has this rule, and it stops at a join: `from csv | include a_name z_name
| where …` gets `-columns a_name -columns z_name`, while the same with a `join` before the
`include` gets none (`generate ssql`, checked both).

### 4.4 The trim

`rowSchema.decode` calls `strings.TrimSpace` on every cell, 0.90 s flat at 24 cores.
The trim is required (every lane trims; `typed/io.go` says why). Checking the first and
last byte first and calling it only if they could be space saved just 0.3 s, a third of
the flat number. The standalone build of this variant failed, so 0.3 s comes from the
stacked rows only.

### 4.5 The strings

The attribution row removes `a_name` and `z_name` from the row struct altogether. It
goes from 10.88 s to 7.18 s and from 0.76 s to 0.40 s, though its output is wrong: it
bounds what a lazier decode could win, with 99.3% of those strings dropped unread.
`strings.Clone` in the reader (`typed/stream.go`, `schema.copyCols`) is 1.83 s cumulative,
before the GC work those strings cause.

## 5. Fewer cores

| Cores | DuckDB | Go as generated | `GOGC=800` | All variants, `GOGC=800` |
|---|---:|---:|---:|---:|
| 24 | 0.81 s | 1.01 s | 0.77 s | 0.54 s |
| 8 P | 0.86 s | 1.17 s | 0.95 s | 0.64 s |
| 4 P | 1.28 s | 1.92 s | 1.67 s | 1.08 s |
| 1 P | 4.06 s | 7.26 s | 6.31 s | 3.96 s |

DuckDB is ahead of the generated program on every row and further ahead on fewer
cores. The stacked variant beats DuckDB on every row, by very little on one core.

`GOMAXPROCS` without pinning, single runs:

| `GOMAXPROCS` | 1 | 2 | 4 | 8 | 12 | 16 | 24 |
|---|---:|---:|---:|---:|---:|---:|---:|
| Wall | 7.31 s | 5.59 s | 2.89 s | 1.68 s | 1.20 s | 1.02 s | 1.00 s |
| User | 7.23 s | 10.66 s | 10.53 s | 11.83 s | 11.49 s | 12.50 s | 15.59 s |

Going from 16 to 24 costs 3.1 s of CPU for 0.02 s of wall on this run. The jump from
1 to 2 (3.4 s) was not explained; concurrent GC and an unpinned thread landing on an
E-core are the candidates.

## 6. What to build, ranked

**1. Move a `where` on a joined column into the small table.** The largest single
structural win (3.7 s), and it lives in the optimiser, so every lane that uses it
gains. Questions a design must answer:
- It is safe for an inner join only. A left join keeps the row, so it must be refused.
- `-on a_kind … - -on z_kind …` uses one table for two clauses, and a predicate on
  `a_kind_name` restricts only the first. The rewrite cannot simply filter `kind.csv`.
- The hand-edited variant filtered on hard-coded ids. A real rule has a key set from the
  right side, or a filtered side table, and that costs something; measure it.

**2. Carry `-columns` through a join.** A column is needed if a join key uses it or a
later stage reads it. 1.2 s here for two columns, and more on wider files. The optimiser
applied no rule at all to this pipeline.

**3. Raise the GC floor while the live heap is small.** The largest wall win for the
least code, and the one with a trap. A fixed `GOGC=800` lets a program with a large live
heap grow to nine times that heap, and DFC158 §3.15's table already has window and filtered-write
programs at 4 GB. The shape needed is a high minimum goal that stops mattering once the
heap is large. Candidates, none tried:
- raising `GOGC` until the live heap passes a threshold, then restoring it;
- a large untouched ballast, which raises the goal without touching memory;
- a generous `GOGC` plus `GOMEMLIMIT` near a fraction of RAM. `GOGC=off` with a 4 GiB
  limit above did not beat 1600: RSS stayed under the limit at 3.5 GB and sys CPU rose.

It belongs in the package rather than in each generated `main`, behind the existing
`ssql.MarkGeneratedProgram` call, in keeping with one call per stage. It should
honour a user's own `GOGC`. It needs a measurement across `bench-engines.sh`, because
one pipeline with a small live heap says nothing about the others.

**4. Decode strings after the filter.** Up to 3.7 s and 0.36 s of wall on this pipeline, the
most left after the rest. It is also the largest change: the filter has to reach
the reader, which has to decode in two passes, and it pays only when the filter is on a
non-string column and selective. Do 1 first, since that decides whether the predicate
can reach the reader at all.

**5. The cheap trim.** 0.3 s for a few lines in a hot path. Worth doing, but only with a
benchmark, and non-ASCII whitespace must still reach `TrimSpace`.

## 7. What this says about measuring

- **On a hybrid CPU, `user` time is not a measure of work.** The same program reports
  7.2 s or 15.7 s depending on where it runs. Compare CPU pinned to one core type.
- **A profile at 24 cores overstates everything that runs on E-cores.** The 1-core
  profile puts the GC, the CSV split and the parser in a different order.
- **Time the program, not the wrapper.** The "tie" was 0.2 s of ssql in one lane
  and none in the other. `bench-engines.sh` already times the generated SQL with
  `duckdb` directly.
- **Vary one setting before reading any more profile.** `GOGC` was the largest wall win,
  and the 160 cycles were visible only once someone counted them. The profile shows GC mark
  and sweep as about a fifth of samples, which reads as ordinary.
- **A number that disagrees with an old table needs a note.** DFC158 §3.15 has DuckDB at 1.04 and 1.36 s on this
  pipeline, against 0.81 s here. Those runs wrote to `/dev/null` through a different driver
  on an earlier build; the difference was not investigated, so do not read 0.81 s as a
  change to DFC158's table.

## 8. Built: the where moves into the side table (2026-11-07)

Ross: "build the where pushdown into the lookup table and benchmark it".
`ruleJoinPredicatePushdown` already existed and missed this pipeline four ways: an
`include` sat between the join and the where; it did not understand `-as`; it knew
only the source file's columns, so a column from an earlier join could not move left;
and one filter cannot serve two clauses over one table. The rewrite
(`commands/generate_ssql_join_pushdown.go`) produces:

```bash
ssql from csv shuffled.csv \
  | ssql join <(ssql from kind.csv | ssql where -if kind_name eq EK_PORT) -on a_kind kind -as kind_name a_kind_name \
  | ssql join kind.csv -on z_kind kind -as kind_name z_kind_name \
  | ssql join <(ssql from relationship.csv | ssql where -if relationship_name eq RK_TRAVERSES) -using relationship \
  | ssql include a_name a_kind_name relationship_name z_kind_name z_name
```

Exec does not run the optimiser, so exec against the generated lanes is the
differential that checks it.

| Question | Decision |
|---|---|
| Which columns a join brings in | read from its arguments, clause by clause (`parseJoinShape`) |
| Columns before the join | walked from the source through each stage's schema op (`columnsThrough`); not knowing means no rewrite |
| What a condition crosses | `include` and `exclude`, if the column survives them; never `update`, `rename`, `limit` or `sort` |
| A multi-clause lookup | one join per clause, in clause order, so the columns keep their order |
| A `<(…)` side | split only if it is one file's read; a side with its own stages is not run twice |
| What stays put | an OR, an expression, a `-using` key, a right column under `-type left` or ASOF, and any join with `-suffix` or `-exclude-*` |

### 8.1 What it found

- **The old rule hid errors.** `join k.csv -on a kind -as kind_name x | where -if kind_name
  eq Y` fails in exec, because `kind_name` never comes in. The old rule saw the name in the
  right file and pushed it there, and the rewrite ran. `-suffix` and `-exclude-right` did the
  same. A condition now moves only if its column reaches the where
  (`TestJoinPushdownKeepsTheError`).
- **A side with a where had no stable order.** It is a parallel read, and `return
  filtered.Serial()` fans in as shards finish, so one key's matches changed order between
  runs: 2 distinct outputs in 40 runs over 200,000 rows after a sort, 1 in 60 after the fix.
  Any hand-written `<(from | where)` side had this; the rule would have added it to pipelines
  that never asked. `Stream.InOrder()` runs the shards concurrently and yields them in shard
  order. A JSON array side, whose shards are batches, is read serially.

### 8.2 The benchmark

Medians of seven, generated programs, 14.6 M rows. The load average was 16 and falling when it
began, from the test run before it; the no-rule row matches §4's idle run. The four Go and
DuckDB outputs of the first table were identical to exec's as sorted files.

| Program | Wall | User | Sys | RSS |
|---|---:|---:|---:|---:|
| Go, no rule (`+O`) | 1.02 s | 15.50 s | 0.81 s | 1232 MB |
| **Go, rule** | **0.90 s** | **13.28 s** | 0.73 s | 1234 MB |
| Go, no rule, `GOGC=800` | 0.74 s | 13.76 s | 0.42 s | 1384 MB |
| **Go, rule, `GOGC=800`** | **0.64 s** | 11.53 s | 0.40 s | 1452 MB |
| DuckDB, no rule | 0.80 s | 4.17 s | 0.77 s | 784 MB |
| DuckDB, rule | 0.79 s | 4.21 s | 0.75 s | 649 MB |

| Pinned | Go, no rule | Go, rule | DuckDB, rule |
|---|---:|---:|---:|
| One P-core, wall | 7.31 s | 5.99 s | 4.07 s |
| One P-core, user | 7.24 s | 5.91 s | 3.30 s |
| 8 P-cores, wall | 1.12 s | 0.96 s | 0.90 s |
| 8 P-cores, user | 8.17 s | 6.85 s | 3.28 s |

- On one core the rule removes 1.3 s of 7.2 s of real work. All 14.6 M rows are still read and
  decoded.
- It is worth less than §4's hand-edited filter (13.3 s against 12.1 s), as §6 expected. That
  filter ran before any join; here the rows still reach the first lookup, and the relationship
  filter acts only at the third join.
- DuckDB gains nothing: it already pushes these predicates down itself.
- The rule alone leaves Go behind DuckDB (0.90 s against 0.79 s). With `GOGC=800` it is ahead
  (0.64 s), but that setting is not built, and §6 item 3 says why not to fix it at 800.

**The cost when the filter drops nothing.** With `ne NOTHING` on both columns, all 14.6 M rows
survive:

| | Wall | User | Sys | RSS |
|---|---:|---:|---:|---:|
| No rule | 2.58 s | 22.38 s | 3.50 s | 4239 MB |
| Rule | 2.60 s | 24.87 s | 3.64 s | 4938 MB |

Wall is unchanged, but it costs 2.5 s of CPU (11%) and 700 MB. The likely cause is the extra row
copy per split, which was not measured. The rule does not know selectivity. It could look, since
generation already reads these files, but that is open, in TODO.

### 8.3 Watched fail

Filtering the whole shared table, forgetting the rename, pushing through a left join, and fanning
a side in with `Serial()` were each put in and caught.

## 9. References

- [DFC158](./dfc158_performance_measured_fixed_next.md) §3.15, the pipeline and the engine table
- [DFC164](./dfc164_type_correctness_week_retrospective.md) §7, the serial rule the bare `limit` did not trigger
- `doc/research/TODO.md`, the open item for this work
