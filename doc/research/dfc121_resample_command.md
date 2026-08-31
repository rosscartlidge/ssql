# `ssql resample`: Snapping Time Series to a Regular Grid

Reference: DFC121
Created: 2026-08-31
Last modified: 2026-08-31

[Back to Index](./README.md)

**Status:** Design — researched and specified (Ross's ask,
2026-08-31); not yet scheduled. The missing piece between ssql and
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
  machinery exists).
- `-every DURATION` — the sample period (Go duration syntax; autocli
  has ArgDuration). Required.
- `-value FIELD` — accumulate; each names a numeric field to carry
  onto the grid. Required (≥1). Non-numeric value fields: loud
  refusal at first record, with the vocabulary.
- `-fill previous|next|linear` — default **previous** (LOCF is every
  system's default-ish and matches intuition for dashboards).
  `nearest` is a cheap fourth mode; note it, don't promise it.
- `-from`/`-to` — grid bounds. Default: min/max observed timestamp,
  with the grid ORIGIN at `-from` (or the min) — i.e. grid points
  are `from + k*every`, k = 0..N, covering through `to`. This is
  Ross's "integral number of periods from the start"; explicitly
  NOT epoch-aligned by default (differs from date_bin's origin
  convention — document loudly; a later `-align epoch` can add it).
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
  **`generate sql`**: DuckDB can express previous/next via
  `generate_series` + `ASOF JOIN` and linear via two asof joins +
  arithmetic — feasible but fiddly; v1 may declare a loud
  translator bail-out (precedented) with the DuckDB translation as
  its own follow-up. DECIDE AT BUILD TIME; either way the
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

## Open questions

1. Default `-fill`: previous (proposed) or refuse-without-flag
   (maximally explicit, Ross's no-implicit taste)? Cheap to flip.
2. Timestamp output format for string inputs: preserve input
   format vs canonical RFC3339? Leaning preserve-family (RFC3339
   in → RFC3339 out) for round-trip friendliness.
3. Epoch-magnitude auto-detect (s/ms/µs/ns): convenient but a
   guess — is a loud note enough, or should ambiguous magnitudes
   require `-time-unit`?
4. `generate sql` v1: loud bail-out or the DuckDB ASOF translation?
5. Aggregating resample (many observations per bucket → mean/max…)
   is DOWNSAMPLING — deliberately out of scope here; it's
   `group-by` on a bucketed field and may deserve its own
   `-bucket` helper someday. This DFC is the alignment/gridding
   half only.
