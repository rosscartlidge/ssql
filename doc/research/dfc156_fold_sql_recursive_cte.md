# The Fold in SQL: `-stream-expr` as a Recursive CTE

Reference: DFC156
Created: 2026-10-26
Last modified: 2026-10-26

[Back to Index](./README.md)

Status: **design, 2026-10-26, built the same day (§8).** Ross: "I see
that duckdb has recursive CTEs - so it can do our fibonacci example",
then the counter-driven form from the DuckDB docs, then "write a design
DFC and then do it". Until this, `window -stream-expr` and `group-by
-stream-expr` were the two stages `generate sql` refused outright ("a
fold over the frame is ssql-specific"), and the Fibonacci example ran in
four of the five lanes.

## 1. What a fold is, exactly

`-stream-expr INIT EVERY FINAL NAME` (DFC129 for group-by, v4.121.3 for
window) is a left fold with a map as its state (`window_fold.go`,
`exprFold`):

- `state₀ = INIT` (a map literal, evaluated with an empty environment);
- `stateᵢ = EVERY(stateᵢ₋₁ ∪ rowᵢ)` — the row's fields shadow state keys
  of the same name; EVERY returns the WHOLE new state;
- `FINAL(stateᵢ)` is the value — per row for a window over the default
  frame (UNBOUNDED PRECEDING → CURRENT ROW, `runningFoldValues`), once
  per group for group-by (the last state).

The order of the rows is the window's `-order` within `-partition`, else
the rows as they arrive. Over any other frame (`-preceding N`, RANGE) the
fold restarts at the frame's first row — not a recurrence, and not this
DFC: those stay refused.

## 2. The recursive CTE

A left fold over an ordered sequence is what `WITH RECURSIVE` computes:
the anchor is `state₀`, each recursive step applies EVERY to the previous
state and the next row, and the working table carries the state. Two
shapes were measured (DuckDB 1.5.0, this machine, 2026-10-26):

| shape | rows | time |
|---|---|---|
| join the source each step (`s.__rn = f.__rn + 1`) | 20,000 | 28 s (quadratic: 5,000 rows 2.8 s) |
| counter only, no source (Ross's form) | 1,000,000 | 47 s |
| the partition as a LIST, indexed each step | 20,000 / 200,000 | 2.5 s / 43 s |

So the join is quadratic, and the recursion itself costs the engine
50–200 µs per step whatever the shape (ssql's fold is ~1 µs per row).
The LIST shape is linear and general: **each partition is aggregated
once into an ordered list of structs, and the recursion indexes it.**

```sql
WITH RECURSIVE
__src AS (<the query so far>),
__parts AS (
  SELECT dept, list(struct_pack(name, salary, dept) ORDER BY salary) AS __xs
  FROM __src GROUP BY dept),                       -- one row per partition
__fold(dept, __i, __xs, __st0_s, __st0_n) AS (
  SELECT dept, 0, __xs, CAST(0 AS BIGINT), CAST(0 AS BIGINT) FROM __parts   -- state₀ = INIT, no row consumed
  UNION ALL
  SELECT dept, __i + 1, __xs,
         __st0_s + __xs[__i + 1].salary,            -- EVERY: state refs are the CTE's own columns,
         __st0_n + 1                                --        row refs index the list at the next row
  FROM __fold WHERE __i < len(__xs))
SELECT __xs[__i].name AS name, __xs[__i].salary AS salary, __xs[__i].dept AS dept,
       __st0_s / __st0_n AS avg_so_far               -- FINAL per row (window)
FROM __fold WHERE __i >= 1
```

Points of the shape:

- **The anchor is INIT with `__i = 0`**, consuming no row, and the output
  keeps `__i ≥ 1`: then one EVERY text serves both the anchor-to-first-row
  step and every later step, and INIT needs no substitution into EVERY.
- **UNION ALL types are the anchor's**, so INIT is CAST to the state's
  type. The state type is what the typed lowering infers
  (`lowerStreamAgg`: INIT's literal type widened by EVERY's result —
  `{s:0}` with `{s: s + price}` over a float column is a float state):
  BIGINT, DOUBLE, HUGEINT (`int128(0)`), VARCHAR, BOOLEAN. The SQL lane
  runs that lowering over a schema built from `sqlColumnKinds`; a fold it
  cannot type is refused with the lowering's reason.
- **Identifiers in EVERY**: a row column resolves to `__xs[__i + 1]."col"`
  (the row shadows the state, as the VM does), a state key to its
  `__stN_key` column, a `-param` to its literal, anything else is an
  error. FINAL sees state columns only. Several folds in one window clause
  share one CTE with namespaced states (`__st0_*`, `__st1_*`).
- **Partition keys** are the GROUP BY of `__parts` and ride through the
  recursion; without `-partition` there is one list. **Order** is
  `-order` (with `-desc`); a fold without `-order` on a source that is not
  `from range` is refused the way every frame function without an order
  is (`windowNeedsOrder`): SQL has no arrival order. On a `from range`
  source the range column is the order.
- **Output columns** are the struct's fields, expanded by name (the
  assembler knows `q.columns`), so the rest of the query sees the same
  columns plus the fold results; other window functions in the same
  clause are applied on top as today.
- **group-by `-stream-expr`** is the same CTE with FINAL taken once per
  partition at `__i = len(__xs)`, the group keys being the partition.

## 3. Dialects

- **DuckDB**: `list(struct_pack(...) ORDER BY ...)`, `__xs[i].field`,
  `len(__xs)`; HUGEINT state through `int128()` → `CAST(x AS HUGEINT)`.
  Verified: fib(184) exact, fib(185) "Overflow in addition of INT128" —
  the same digits and edge as ssql.
- **Postgres**: has `WITH RECURSIVE` and `array_agg(... ORDER BY)`, but a
  field of an anonymous `ROW(...)` cannot be read back without a named
  composite type, and a JSON detour (`jsonb_agg`, `->>` with casts) is a
  second translator. Refused with that reason for now.
- **DataFusion**: recursive CTEs since v37 and `array_agg(named_struct)`
  with `arr[i]['f']`; the lane is not configured here, so it refuses with
  "not verified" rather than ship text no gate has run (the rule from
  DFC154).

## 4. Cost, documented

The translation is for **correctness and short partitions**: a few
hundred rows per partition is instant, 20,000 is seconds, a million is
most of a minute, where ssql's exec does the same fold in a second. The
one sentence in `doc/EXPRESSIONS.md` and the `generate sql` note says
so; a long recurrence belongs in ssql.

## 5. What changes

- `cmd/ssql/commands/generate_sql.go`: `translateWindow` collects
  `-stream-expr` specs per clause instead of refusing; after the clause
  parse, a running-frame clause with folds wraps the query in the CTE
  (`translateFoldCTE`), then the clause's other functions apply. A
  non-running frame with a fold refuses as before, naming the frame.
  `translateGroupBy`'s `-stream-expr` case builds the group form.
- `cmd/ssql/commands/generate_sql_expr.go`: an identifier resolver hook
  (`sqlIdentResolver`) consulted before the column quoting, set for the
  duration of a fold's EVERY/FINAL translation.
- State typing reuses `lowerStreamAgg` (expr_stream.go) over a
  `lib.TypedSchema` synthesised from `sqlColumnKinds`.
- Equivalence: `cast_int128_fib_184`, `window_stream_expr_running` and
  the three `group-by -stream-expr` cases drop their DuckDB skips; the
  sliding-fold cases keep theirs with the frame named; a partitioned
  window fold over `employees.csv` is added. `TestRangeSourceSQL`-style
  unit tests pin the emitted text.
- Docs: EXPRESSIONS.md's fold section (the SQL form and its cost),
  CHANGELOG, DFC136's table row, TODO.

## 6. Not in this DFC

The counter-only form (no source list) for `from range` folds whose
EVERY reads only state and the range column — an optimisation, not a
semantics; Postgres and DataFusion forms; sliding folds (a different
recurrence per row, no CTE shape without the quadratic join).

## 7. Open question for Ross

None blocking: the design follows his counter form's insight (no join)
with the list for a general source. If the Postgres rig lane comes up,
§3's JSON detour is the way to add it.
