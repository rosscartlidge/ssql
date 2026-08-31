# `ssql resample`: Snapping Time Series to a Regular Grid

Reference: DFC121
Created: 2026-08-31
Last modified: 2026-09-01

[Back to Index](./README.md)

**Status:** SHIPPED 2026-09-01 (same-day build) — exec + record +
typed/parallel lanes through one ResampleRecords (typed re-enters
typed via a synthesized output struct; equivalence-gated with a
sabotage-verified case pair; 3M-row scale budget: 2.9s), bucket()
in VM + transpiler with differential coverage, codelab section.
Build finding recorded below: the duplicate-timestamp rule became
HIGHEST-VALUE-WINS (input-order "last wins" was lane-dependent
across parallel shards). Edge policy became CLAMP-loudly (absence
is unrepresentable in typed structs — the prime directive caught a
cross-lane impossibility at design time). REMAINING from resolution
#4: the generate-sql DuckDB ASOF translation (equivalence cases
carry explicit skips naming it). The missing piece between ssql and
real time-series charting: DFC119's chart/animate sinks want evenly
gridded data, and nothing in the pipeline today produces it.

## The ask (Ross's formulation)

A command that takes a timestamp field and one or more numeric
fields, and creates new records whose timestamps are snapped to an
integral number of sample periods from the start time to the end
time. Each data field is set from the closest observation before or
after the grid point (settable), or by interpolating between them.

## Prior art (this is a very well-known capability)

| System | Spelling | Fill vocabulary |
|---|---|---|
| pandas | `df.resample('5min').asfreq()` | `ffill()` (LOCF), `bfill()`, `interpolate()` |
| TimescaleDB | `time_bucket_gapfill(interval, ts)` | `locf()`, `interpolate()`, NULL default |
| QuestDB | `SAMPLE BY 5m FILL(...)` | `PREV`, `LINEAR`, `NULL`, constants |
| InfluxDB | `GROUP BY time(5m) fill(...)` | `previous`, `linear`, `none` |
| kdb+ | `aj` (asof join) against a grid | prev by construction |
| DuckDB | `time_bucket()` + `ASOF JOIN` | composed manually |
| Postgres 14+ | `date_bin(stride, ts, origin)` | composed manually |

Consistent industry semantics worth adopting wholesale:

- **LOCF** ("last observation carried forward") = Ross's
  "closest before"; *next/backfill* = "closest after".
- **Linear interpolation needs both neighbors**; every system
  leaves the value empty when one side is missing (TimescaleDB:
  "if the previous and next windows do not exist, the value is
  left empty").
- **Grid anchoring is explicit** (Postgres `date_bin` takes an
  origin) because "5-minute buckets" is ambiguous without one.

## Proposed grammar (house rules: accumulate, no delimiters)

```
ssql resample -time ts -every 5m \
     -value cpu -value mem \
     [-fill previous|next|linear] \
     [-from 2026-08-31T00:00:00Z] [-to ...] \
     [-by host]                    # phase 2
```

- `-time FIELD` — the timestamp field. Required.
  Accepted types, using the coercions core.go already has: int64 /
  float64 epoch seconds (auto-detect ms/µs/ns by magnitude, the
  usual heuristic — document it), or strings via RFC3339 and SQL
  datetime. `-time-format LAYOUT` for exotics (autocli ArgTime
  machinery exists). Epoch magnitude is auto-detected (s/ms/µs/ns
  by range) with a LOUD stderr note of the chosen unit; `-time-unit
  ns|us|ms|s` (Go duration unit vocabulary) overrides — resolved
  2026-09-01.
- `-every DURATION` — the sample period (Go duration syntax; autocli
  has ArgDuration). Required.
- `-value FIELD` — accumulate; each names a numeric field to carry
  onto the grid. Required (≥1). Non-numeric value fields: loud
  refusal at first record, with the vocabulary.
- `-fill previous|next|linear` — default **previous** (RESOLVED
  2026-09-01: LOCF matches every peer's default and dashboard
  intuition; the always-written workspace machinery will spell the
  default out when it writes stages, keeping Ross's no-implicit
  taste satisfied where it matters). `nearest` is a cheap fourth
  mode; note it, don't promise it.
- **The grid is EPOCH-ALIGNED** (resolved 2026-09-01, Ross + the
  three arguments below): grid points are `k*every` from the Unix
  epoch, i.e. `floor(ts/every)*every` buckets — the `date_bin`/
  `time_bucket` convention. Why: (a) STABILITY — appending data
  never shifts existing bucket boundaries, so reruns and streaming
  agree; (b) JOINABILITY — two independently resampled series land
  on the same grid and can be joined; a data-anchored grid almost
  never aligns across series; (c) human boundaries (5m buckets hit
  :00/:05). Cost: partial coverage at the edges — accepted, as
  every peer does. No `-align data` opt-out until someone needs it.
- `-from`/`-to` — grid bounds, themselves snapped DOWN/UP to the
  epoch grid (with a stderr note when snapping changed them).
  Default: the epoch-grid points covering [min, max] observed.
- Output records: the grid timestamp (same field name, same type
  family as input — RFC3339 in → RFC3339 out) plus the value
  fields. Other input fields are dropped (a resampled record is a
  NEW observation, not a decorated old one). `-by` (phase 2)
  carries the group fields.

### Edge semantics (loudness doctrine)

- `previous` before the first observation, `next` after the last,
  `linear` outside the observed range: the value field is **absent
  from that record** and — because silent nulls burned us before —
  resample prints a one-line stderr note ("resample: 3 grid points
  before the first observation have no cpu value"). Records still
  emit (grid completeness is the point).
- Empty input: zero records, no grid (a grid over nothing is
  invention).
- Unsorted input: resample **materializes and sorts by the time
  field** (like sort; it cannot stream anyway for `next`/`linear`).
  A `-presorted` streaming mode is a later optimization with
  group-by's precedent.
- Duplicate timestamps: last one wins, noted on stderr (count).

## Where it sits in the architecture

- **All five backends, day one gates** (DFC102 doctrine): exec,
  record codegen, typed codegen get the same `ssql.Resample(...)`
  primitive (CLI thin over package function, per house rules).
  **`generate sql`**: RESOLVED 2026-09-01 — v1 SHIPS the DuckDB
  translation: `generate_series` over the epoch grid + `ASOF JOIN`
  for previous/next, two asof joins + arithmetic for linear (epoch
  alignment makes the series generation trivial —
  `time_bucket`-compatible). The
  equivalence harness runs every lane that exists, with Golden
  oracles on shuffled, gap-ridden fixtures (fixtures MUST contain
  gaps, duplicates, and unsorted rows — a clean fixture tests
  nothing here).
- **Scale gate case** (DFC113): resampling a multi-million-row
  series to a coarse grid must be O(n) — a naive
  per-grid-point-scan is O(n·m) and would pass every functional
  test.
- **Display-sink synergy is the payoff**: `… | ssql resample -time
  ts -every 1m -value cpu | ssql to chart -type line -x ts -y cpu`
  — and animate's `-frame` over resampled buckets gives
  evenly-paced animations for free.
- **Name collision note**: gpu-feature-opportunities.md sketched
  `resample -factor 2` for DSP-style factor resampling. Compatible:
  same command, `-every` (time grid) vs `-factor` (integer
  up/down) as mutually exclusive modes if that ever lands.
- **Completion**: `-time`/`-value`/`-by` are FieldsFromFlag slots;
  `-fill` is a StaticCompleter enum — all of which flows into
  -spec-json, Ctrl-O, and the workspace for free.

## Algorithm (v1, exec + codegen)

Materialize → coerce timestamps → sort → single merge pass: walk
grid points and observations together (two pointers), maintaining
prev/next observation per value field; previous/next are O(n+m),
linear the same with one division. Numeric output as float64 for
interpolated values, input type preserved for previous/next
(coercion rules already canonical: int64/float64).

## Gates before features

- Equivalence cases: gappy shuffled fixture × three fill modes ×
  every lane; hand-written Golden for a fixture computed by hand.
- Property test: output timestamps are EXACTLY `from + k*every`
  (integer arithmetic on epoch-nanos — no float accumulation
  drift; this is a classic resampler bug).
- Edge gates: empty input, all-gaps segment, duplicate timestamps,
  single observation (linear must not divide by zero).
- Scale-gate budget on the merge pass.
- Sabotage: break the two-pointer advance; the Golden must scream.

## Downsampling: the other half, by COMPOSITION (added 2026-09-01)

Aggregating many observations per bucket (mean/max/sum per 5m) is
downsampling. Ross asked for it to be fleshed out; the design
answer is composition, not a second aggregation vocabulary:

**group-by already owns aggregation grammar** (sum/avg/min/max/
collect/expr/stream-expr). Duplicating it inside resample as
`-agg avg` would be a second implementation of that grammar — the
DFC115 disease. Instead, downsampling = bucketing + group-by:

1. Grow ONE expression function: `bucket(ts, "5m")` — snaps a
   timestamp to its epoch-aligned bucket (the same snapping code
   resample uses; one implementation, exported). Then:

       ssql from metrics.csv \
         | ssql update -set-expr b 'bucket(ts, "5m")' \
         | ssql group-by b -avg cpu cpu -max mem mem

   gives classic downsampling with group-by's FULL vocabulary,
   every backend, and streaming (-presorted) for free.
2. Gap-filling a downsampled series is then… resample applied
   AFTER group-by (`| ssql resample -time b -every 5m -value cpu`)
   — buckets with no data get filled per -fill. The two halves
   compose exactly because both snap to the SAME epoch grid
   (argument (b) for epoch alignment, again).
3. A future `resample -agg` convenience can be revisited only if
   the composition proves too verbose in practice; if so it must
   DELEGATE to group-by's machinery, never re-implement it.

Deliverables added to the build: the `bucket(ts, dur)` expr
function (with expr-transpiler + SQL translations — DuckDB
time_bucket — and TestExprGoDifferential coverage), plus a codelab
"time series" section showing align, downsample, and
downsample-then-fill.

## Resolutions (2026-09-01, Ross)

1. `-fill` defaults to **previous**.
2. Timestamp output **preserves the input family** (RFC3339 in →
   RFC3339 out; epoch in → epoch out, same unit).
3. Epoch-unit handling: auto-detect with a loud note; `-time-unit
   ns|us|ms|s` (Go duration unit strings) to override.
4. `generate sql` v1 ships the **DuckDB ASOF translation** — no
   bail-out.
5. Downsampling fleshed out above — composition via `bucket()` +
   group-by, not a second aggregation vocabulary.
6. The grid is **epoch-aligned** (stability, joinability, human
   boundaries; partial edge buckets accepted).
