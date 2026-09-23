# Closing Three DuckDB Gaps: Spilling Sort and Group-by, ASOF Join, INTERSECT/EXCEPT

Reference: DFC137
Created: 2026-09-23
Last modified: 2026-09-23

[Back to Index](./README.md)

Status: **proposal, for review. Nothing built.** DFC136 §4 ranked the
gaps between ssql and DuckDB; Ross asked how three of them would be done
and then for a DFC. Each part is independent and can ship on its own.
Together they are about a week.

## 1. Out-of-core `sort` and `group-by`

### 1.1 The gap

`sort`, `group-by` (without `-presorted`), `top` and `distinct` are
barriers: they hold their input in memory. A `sort` of a 20 GB CSV on
an 8 GB machine dies; DuckDB spills to disk and finishes. Everything
else in ssql streams, so the barriers are where the bounded-memory
promise breaks.

### 1.2 Design: external merge sort, from parts that exist

ssql already has both halves: an in-memory sort and `merge`, a
streaming k-way merge of pre-sorted inputs in O(k) memory. The missing
piece is the run writer.

```
sort -spill DIR [-memory SIZE]         (default: no spill, as today)
```

1. Read rows into memory until the run reaches `-memory` (default 1G,
   measured as the JSONL bytes read, which is cheap and honest enough);
   sort the run with the existing comparator; write it to
   `DIR/run-NNNN.jsonl` with the library's JSONL writer (schema header
   included, so the run is a normal ssql file); free it.
2. Repeat until the input ends. A 20 GB input at 1 GB is 20 runs.
3. K-way merge the runs with the `merge` machinery (a min-heap over the
   run heads, one open file per run), yielding in sort order. Delete
   each run as it drains.

Cost: one write and one read of the data; O(n log n) compares as now.
Stability: `sort` uses `slices.SortedFunc`, which is NOT guaranteed
stable, so the in-memory form has no tie order to preserve today (the
row-order sweep, DFC133, tolerates it by sorting on a unique key). The
spilling form should be stable anyway (`SortStableFunc` per run, and
the merge taking from the earlier run on ties) since it costs nothing,
and making the in-memory sort stable to match is a one-line change
worth taking at the same time so the two forms agree on ties.

`top N` and `distinct` do not need this: `top` is already a bounded
heap, and `distinct` needs the distinct set, which is the group-by case
below with no aggregates.

### 1.3 Design: group-by, two forms

**Sort then stream (first, and probably last).** `group-by -presorted`
already aggregates in O(1) memory per group when its input is sorted by
the key. So out-of-core group-by is `sort -spill DIR KEYS | group-by
-presorted KEYS …`, which works today by hand, with every aggregate
including median, percentile and mode (they see a whole group at a
time). The unit is to let `group-by -spill DIR` do that pair itself,
and to let the `generate ssql` optimiser insert it when the user asked
for a spill. One implementation, no new aggregation code.

**Hash partitioning (only if measured to matter).** Hash the group key
into 64 buckets, append each row to its bucket's file, then aggregate
one bucket at a time in memory. Saves the sort's O(n log n) and one
pass, at the price of a second implementation of "where do rows go".
DuckDB does this. I would not build it until the sort-then-stream form
is measured too slow on the scale fixture; the common case (many rows,
few groups) never needs to spill at all and should keep the in-memory
path, which a spill flag does not disturb.

### 1.4 Flags and defaults

- `-spill DIR`: opt in. No spill flag means today's behaviour, so no
  pipeline changes without asking. `DIR` must exist; runs are removed on
  success and on interrupt (signal handler; `ssql run` already has one).
- `-memory SIZE`: run size, `1G` default; `512M`, `4G` accepted.
- No automatic spilling. An estimate of "will this fit" is the risky
  part, and a wrong guess either spills needlessly (slow) or does not
  (dies). Later, if wanted: a `-memory` without `-spill` could mean
  "fail early when the budget is reached" rather than "spill".

### 1.5 Lanes

- exec and record codegen share the library sort, so `ssql.SortSpill`
  serves both; the fragment carries the flags.
- typed codegen has its own `typed.Sort` over `[]T`; it needs a typed run
  writer (the typed JSONL fast path exists) and a typed merge. A day on
  its own; until then typed with `-spill` falls back to record codegen
  with a plan note.
- `generate sql`: the flags are dropped; spilling is the engine's job.
  A plan note says so.

### 1.6 Tests

- Scale gate (DFC113): a fixture larger than `-memory` (the cached
  120 MB fixture with `-memory 16M` gives eight runs) with a wall-time
  ceiling; a byte-identical result against the in-memory sort.
- Equivalence: `sort -spill` and `group-by -spill` cases; the lanes must
  agree with and without the flag.
- Interrupt: runs are gone after SIGINT.

## 2. ASOF join

### 2.1 The gap

An ASOF join gives each left row the right row that is *current as of*
its time: the quote in force when a trade happened, the sensor reading
nearest before an event. Two time series sampled at different instants
have no equal timestamps, so an equi-join finds nothing. ssql users have
time series (`resample`, `window -lag`, `fill -down`, `bucket()` all
exist for them), and joining two of them is the next thing they do; the
workaround today is to `resample` both onto a grid and equi-join on the
grid, which loses precision and needs a grid choice.

### 2.2 Syntax

`join` gains a mode, keeping its existing shape:

```bash
ssql from trades.csv | ssql join quotes.csv -using sym -asof ts             # right.ts <= left.ts, nearest
ssql from trades.csv | ssql join quotes.csv -using sym -asof ts -after     # right.ts >= left.ts, nearest
ssql from events.csv | ssql join readings.csv -asof ts -on sensor id        # equality part with -on
ssql from trades.csv | ssql join quotes.csv -using sym -asof ts -type left  # keep unmatched trades
```

- `-asof FIELD`: the ordered column, present on both sides under the
  same name (or `-asof-on LEFT RIGHT` for different names). Numeric or
  time; text is refused (order on text is not "as of").
- Direction: default backward (`<=`, the quote before the trade), `-after`
  for forward. Strict forms (`<`, `>`) via `-strict`.
- Equality part: `-using` / `-on` as today, optional; without one the
  whole right side is one series.
- `-type inner|left`: inner drops left rows with no match, left keeps
  them with the right fields absent. Right and full have no ASOF meaning
  and are refused.
- `-tolerance DURATION` (optional): no match beyond this distance
  (`-tolerance 5m`); a common guard.
- The right side is a file or process substitution as now, or a nested
  pipeline in a document (DFC134).

### 2.3 Semantics

For each left row: among right rows with equal key and `right.ts <=
left.ts` (backward), the one with the greatest `right.ts`; ties on
`right.ts` take the last in input order (DuckDB takes an arbitrary one;
being deterministic is better). Absent `ts` on either side is no match
(DFC124: a condition on a missing value is false).

### 2.4 Algorithm

Sort both sides by (key, ts) (or trust `-presorted` on both), then a
merge walk per key: advance the right cursor while `right.ts <= left.ts`,
remembering the last right row; emit. O(n + m) after the sort, streaming,
O(1) memory per key beyond the current right row. With §1's spill, the
sort is out-of-core too. The typed hash join is the template for the
key part; the walk replaces the probe.

### 2.5 Lanes

- exec and record codegen: one `ssql.AsofJoin` in the library.
- typed: `typed.AsofJoin[L, R]` beside `HashJoin`; the result struct is
  the same shape `join` produces today.
- `generate sql`: DuckDB has `ASOF JOIN … ON a.sym = b.sym AND a.ts >=
  b.ts` natively (backward is `>=` there: the left is later). Postgres
  and DataFusion have no ASOF: emulate with a lateral `ORDER BY b.ts
  DESC LIMIT 1` (Postgres) or a window over the union (DataFusion), or
  refuse loudly with `dialectRefuse` until someone needs it. Refuse
  first; emulate on demand.

### 2.6 Tests

Equivalence cases on a shuffled trades/quotes fixture with a hand
golden: backward, forward, with and without key, left type, tolerance,
absent ts, ties. The DuckDB lane is the oracle for backward and forward.
The random tester gains an ASOF stage over two numeric columns of the
same table joined to itself (a self-ASOF is well defined and the fuzz
already has the columns).

## 3. INTERSECT and EXCEPT

### 3.1 The gap

The other two set operations. `EXCEPT` is the one people reach for:
"customers who have never ordered", "ids in yesterday's file missing
from today's". Without it the answer is a left join filtered on the
unmatched side, clumsier and wrong under duplicate keys. Keyed forms are
the anti-join and semi-join, which DFC136 §4.1 also lists as missing,
so two verbs close four gaps.

### 3.2 Syntax

Two commands shaped like `union`, which already has the pattern (stdin
is the left side, `-file` the right, a bool chooses ALL):

```bash
ssql from today.csv     | ssql intersect -file yesterday.csv          # rows in both (whole row)
ssql from customers.csv | ssql except -file ordered.csv               # rows on stdin not in the file
ssql from customers.csv | ssql except -file orders.csv -using customer_id     # anti-join: keyed
ssql from customers.csv | ssql intersect -file orders.csv -using customer_id  # semi-join: keyed
ssql from a.csv         | ssql except -file b.csv -all                # keep duplicates (EXCEPT ALL)
```

- Whole-row form (no key): rows compared on all fields, as SQL does;
  distinct by default, `-all` keeps duplicates with multiset semantics
  (`EXCEPT ALL` removes one right occurrence per left occurrence).
- Keyed form: `-using FIELD` or `-on LEFT RIGHT`, as `join`. The LEFT
  row is what comes out, unchanged (a semi-join, not a join: no right
  fields are added). Distinctness is then by the left row as a whole.
- Absent key on the left: `except` keeps the row (it matches nothing),
  `intersect` drops it, the absent-value rule.

### 3.3 Algorithm

Read the right side into a set (whole rows canonicalised as their
JSONL, or the key) or, for `-all`, a multiset of counts; stream the
left. O(right) memory, streaming left, as `join` is. With §1, a
sort-based form exists for a right side too large for memory, but the
right side is the small one by convention.

### 3.4 Lanes

- exec and record codegen: `ssql.Except` / `ssql.Intersect` in the
  library, keyed and whole-row.
- typed: `typed.Except[T]` over a hashed key, the whole-row form hashing
  the struct.
- `generate sql`: whole-row is `EXCEPT` / `INTERSECT` (`ALL` variants) in
  all three dialects; keyed is `WHERE key NOT IN (SELECT key FROM right)`
  for except and `IN` for intersect, with the NULL-safe `NOT EXISTS`
  spelling (a NULL in a `NOT IN` list is the classic SQL trap; DFC128's
  `NOT COALESCE` rule applies).

### 3.5 Tests

Equivalence cases with goldens: whole-row distinct and ALL, keyed both
ways, duplicates on each side, absent keys; DuckDB as the oracle for the
whole-row forms. Crash sweep and random tester pick the new commands up
from `-spec-json`.

## 4. Order and size

1. `except` / `intersect` (half a day plus cases): smallest, closes four
   gaps, no new concepts.
2. ASOF join (two days): the largest gap for the users ssql has; the
   typed lane is the long part.
3. Spilling sort, then group-by as sort-then-stream (two days), typed
   spill later on demand.

## 5. Open points for review

1. §1.4: opt-in `-spill DIR` versus automatic spilling. I propose opt-in.
2. §2.2: `-asof FIELD` on `join` versus a separate `asof-join` command.
   A mode on `join` keeps one place for the equality part and the
   result shape; a command is easier to find. I lean mode.
3. §2.3: tie rule on equal `ts`: last in input order (proposed) versus
   first.
4. §3.2: whether keyed `intersect` should optionally ADD the right
   fields (then it is a semi-join that becomes an inner join, and
   `join` already does that): no, keep it a filter.
5. §1.3: hash partitioning for group-by only after measurement.

## 6. References

- [DFC136](./dfc136_duckdb_feature_comparison_2026_09.md) — the
  comparison that ranked these.
- [DFC113](./dfc113_scale_gate.md) — the scale gate for §1's ceilings.
- [DFC124](./dfc124_missing_values.md) — the absent-value rules the
  join and set operations follow.
- [DFC128](./dfc128_json_interchange_and_time_type.md) — the time type
  `-asof` orders on; `NOT COALESCE`.
- [DFC130](./dfc130_window_analytics_gaps.md) — the window work that
  put time series in scope.
- `cmd/ssql/commands/merge.go` — the k-way merge §1 reuses.
