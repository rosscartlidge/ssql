# Window Analytics: Gaps Against the SQL Set

Reference: DFC130
Created: 2026-09-15
Last modified: 2026-09-15

[Back to Index](./README.md)

Status: **audit with a proposed plan** (§5). Ross, 2026-09-15, as the
DFC129 aggregates shipped: "then we should look to see if we have gaps
in our window analytic functions". This is the audit; nothing is built.

## 1. What `window` has today

`ssql window` (DFC049 design, streaming variant in
streaming-window-functions.md) is clause-based: each clause carries its
own `-partition` (repeatable), `-order` (repeatable), one `-desc` for
the clause, a ROWS frame (`-preceding N` / `-following N`, −1 =
unbounded, default unbounded-preceding to current row), and any number
of function flags:

| Group | Flags | SQL |
|---|---|---|
| ranking | `-row-number R`, `-rank R`, `-dense-rank R`, `-ntile N R`, `-percent-rank R` | ROW_NUMBER, RANK, DENSE_RANK, NTILE, PERCENT_RANK |
| offset / value | `-lag F N R`, `-lead F N R`, `-first F R`, `-last F R` | LAG, LEAD, FIRST_VALUE, LAST_VALUE |
| aggregate | `-sum F R`, `-avg F R`, `-count R`, `-min F R`, `-max F R` | windowed SUM/AVG/COUNT(*)/MIN/MAX |

`-presorted` switches to the streaming O(1)-memory path when the input
is already ordered by partition + order fields.

Lanes: exec (`ssql.Window` / `ssql.StreamWindow`), record codegen
(same library calls), and `generate sql` (`translateWindow` handles
every flag above, including per-clause PARTITION/ORDER and ROWS frames).
**No typed lane**: `window.go` emits record fragments only, so a typed
pipeline pays the typed→Record boundary at every window stage. The
optimiser knows the flag arity (`generate_ssql.go:1954`) and the schema
walker appends result names (`schema_ops.go:237`).

Types: `-min`/`-max` compare with `CompareAny` (numbers, strings,
times — no string-zero bug here); `-sum`/`-avg` read `Get[float64]`
and are plain `+=` (not compensated — the DFC129 follow-up list).

**Tests:** `grep window equivalence_test.go` finds **no case**. Window
is covered by the smoke corpus and unit tests, not by the N-way
differential gate. Given what the gate found in group-by this month
(string min/max, the dead-sort rule), this is the first gap to close.

## 2. The standard set, and what is missing

Against SQL:2003 window functions as DuckDB and Postgres implement them:

| Function | DuckDB | Postgres | ssql | Gap |
|---|---|---|---|---|
| row_number, rank, dense_rank, ntile, percent_rank | ✓ | ✓ | ✓ | — |
| **cume_dist** | ✓ | ✓ | — | ranking family; `count(rows ≤ current) / partition size` |
| lag, lead (offset, **default value**) | ✓ (`lag(x, n, default)`) | ✓ | offset only | `-lag F N R` yields missing/null before the start; a default is the common third argument |
| first_value, last_value | ✓ | ✓ | ✓ (`-first`, `-last`) | — |
| **nth_value(F, n)** | ✓ | ✓ | — | offset family |
| sum, avg, count, min, max | ✓ | ✓ | ✓ | — |
| **count(F)** (non-null count) | ✓ | ✓ | `-count R` is COUNT(*) only | minor |
| **stddev / variance** (windowed) | ✓ | ✓ | — | rolling volatility is the classic use of a window |
| **median / quantile** (windowed) | ✓ (`quantile_cont` over a frame) | ✓ | — | rolling median |
| **count-distinct, string-agg, first/last-by-arrival, mode, arg-max** over a frame | ✓ (any aggregate can be windowed) | ✓ | — | in SQL every aggregate is a window function; ssql has fifteen aggregates and five of them windowed |
| ROWS frame | ✓ | ✓ | ✓ | — |
| **RANGE frame** (by value: `RANGE BETWEEN INTERVAL 5 MINUTE PRECEDING AND CURRENT ROW`) | ✓ | ✓ | — | the time-series frame; `resample` covers the gridded case, nothing covers "last 5 minutes per row" |
| **GROUPS frame** | ✓ | ✓ | — | rare |
| **EXCLUDE CURRENT ROW / TIES** | ✓ | ✓ | — | rare |
| per-key sort direction (`ORDER BY a ASC, b DESC`) | ✓ | ✓ | one `-desc` per clause | minor; a second clause works around it |
| **filtered aggregate** (`sum(x) FILTER (WHERE …)`) | ✓ | ✓ | — | `where` before the window covers the whole-row case; a per-function filter does not exist |

Three of these matter for the codelab audience and the GopherCon story;
the rest are completeness:

1. **Every aggregate as a window function.** DFC129 just put fifteen
   aggregates on one registry table with typed accumulator kinds that
   have `Add` and `Merge`. A frame is a sliding multiset; a rolling
   stddev, a rolling distinct count, a rolling string-agg are the same
   accumulators fed by the frame. Today `window` has its own five
   hand-written aggregate cases in `computeWindowFunc`.
2. **RANGE frames by value and by time.** "Sum of the last five minutes
   for each reading" is the time-series question; ROWS frames answer it
   only for a regular grid.
3. **A typed lane.** Window is the one row-preserving analytic stage
   with no typed template, so the fast path stops at it.

## 3. The design that closes 1 and 3 together

Do not add windowed variants one by one. `computeWindowFunc`'s five
aggregate cases should become **one path that takes an `aggDef`**: the
frame's records (materialised path) or the frame's sliding multiset
(streaming path) feed the registry aggregate's `build` function, and
the result is the flag's result field. Then `window -stddev F R`,
`-median F R`, `-count-distinct F R`, `-string-agg F SEP R`, `-mode F
R`, `-arg-max F BY R`, `-percentile F P R` exist for the price of
registering the flags on `window` and pointing at the same `aggDefs`
entries (the flag grammar is already identical: FIELD RESULT, with the
same extra arguments).

For the streaming path (`-presorted`), a sliding frame needs `Remove`
as well as `Add`. Sums, counts, Welford (via the two-pass identity or by
keeping the frame's values), sets with counts, and count-based mode
support removal; min/max, median and arg-max over a sliding frame need
the frame's values (a deque or a sorted structure) — which the
materialised path already has. The honest v1: **materialised frames for
everything, streaming for the kinds that support Remove**; the rest fall
back to the materialised path with the reason under `-explain`, the same
shape as typed group-by's record fallback.

The typed lane follows from the same registry: a typed `window` template
is the group-by accumulator per partition applied over a frame — the
seven kinds already emit `Add`/`Merge`/`Result`; a frame adds `Remove`
for the kinds that have it. That is a bigger unit (the ranking and
offset functions need their own typed code), so it is phase 3 below.

SQL: `translateWindow` already renders `FN(F) OVER (...)`; the
registry's `sql` renderer supplies FN for the new flags (`stddev_samp`,
`quantile_cont`, `count(DISTINCT …)` is not allowed in DuckDB window
functions — refuse loudly, `string_agg`, `mode`, `arg_max`).

## 4. RANGE frames

Grammar, following the existing flags: `-range-preceding V` /
`-range-following V` where V is a number for numeric order fields or a
duration (`5m`, `1h`) for time order fields — the order field's type
decides, like `resample`'s `-time-unit`. Exactly one `-order` field is
required for a RANGE frame (SQL says the same). Materialised path:
binary search on the sorted partition for the frame bounds; streaming
path: a deque by order value. SQL: `RANGE BETWEEN INTERVAL '5 minutes'
PRECEDING AND CURRENT ROW` for time, `RANGE BETWEEN 5 PRECEDING AND
CURRENT ROW` for numbers — DuckDB and Postgres both accept both.

## 5. Plan

| # | Unit | Size | Notes |
|---|---|---|---|
| 0 | **Equivalence cases for window** — one per function group, shuffled fixture, Goldens from DuckDB, `-presorted` variant, ROWS frames | ½ day | the gate first; it will say whether the five existing aggregates and the offset functions agree across exec / record / DuckDB today |
| 1 | `cume_dist`, `nth_value`, `lag`/`lead` default value, `count(F)` | ½ day | completes the ranking/offset families; small, no design |
| 2 | **Aggregates over frames from the registry**: window's aggregate flags become `aggDefs` lookups; new flags `-stddev -variance -median -percentile -count-distinct -string-agg -mode -arg-max -arg-min -first-arrival?` (no — `-first` is FIRST_VALUE already), compensated sums come for free | 1–1½ days | §3; streaming for Remove-capable kinds, materialised otherwise |
| 3 | **RANGE frames** (numeric and time) | 1 day | §4; equivalence against DuckDB's RANGE |
| 4 | **Typed window template** | 2 days | the frame accumulator per kind + ranking/offset code; SerialOnly per partition unless `-presorted` |
| 5 | GROUPS frame, EXCLUDE, per-key direction, FILTER | — | only if asked |

Order: 0 → 1 → 2 → 3, with 4 when the typed story needs it (GopherCon
planning). Units 0–3 are about three days.

## 6. Decisions

| # | Question | Recommendation |
|---|---|---|
| 1 | Windowed aggregates: hand-written per function, or from `aggDefs`? | From the registry (§3); window's five existing cases move onto it too |
| 2 | RANGE grammar | `-range-preceding V` / `-range-following V`; the order field's type decides number vs duration |
| 3 | Streaming vs materialised for sliding frames | Materialised everywhere first; streaming where `Remove` exists; fallback with reason |
| 4 | Typed lane now? | After units 0–3 |
| 5 | Window in the equivalence gate | Yes, first — it has none today |

## 7. References

- `doc/research/window-functions-design.md` (DFC049) — the design the
  command implements; its taxonomy is §2's baseline.
- `doc/research/streaming-window-functions.md` — the `-presorted` path
  and what it can and cannot stream.
- `doc/research/dfc129_groupby_aggregates.md` — the registry and the
  seven accumulator kinds this doc proposes to reuse.
- `doc/research/dfc121_resample_command.md` — the gridded time-series
  case RANGE frames complement.
- `doc/research/multimode-equivalence-testing.md` — why unit 0 comes
  first.
