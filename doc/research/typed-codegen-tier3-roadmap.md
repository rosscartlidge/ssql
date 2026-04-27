# Tier 3 Codegen — What's Left, and How to Prioritize

Forward-looking companion to
[`typed-codegen-proposal.md`](typed-codegen-proposal.md). The proposal
documents what shipped (Tier 1 + Tier 2 + Tier 3a). This doc enumerates
what's still deferred under "Tier 3", estimates the cost of each, and
recommends a priority order.

The goal is to make decisions easier — not to commit to anything.
Items are ordered roughly by `(demand) × (1 / effort)`.

---

## Quick summary

Items #1-5 shipped as **Tier 3b** on 2026-04-27 (commit forthcoming).
Surface and recommendations preserved below as a record.

| # | Command(s) | Effort | Demand | New runtime needed? | Status |
|---|---|---|---|---|---|
| 1 | `update -set FIELD LITERAL` (unconditional) | **low** (~half day) | **high** | No (uses `typed.Select`) | **shipped** |
| 2 | Multi-field `sort` | low (~2 h) | medium-high | No (composite-key `SortBy`) | **shipped** (with new `typed.SortByFunc`) |
| 3 | `cast -field F -type TYPE` | low (~2 h) | medium | No (uses `typed.Select` + conversion) | **shipped** |
| 4 | `top N -field F` | low (~1 h, syntactic sugar) | medium | No (it's `sort -desc | limit`) | **shipped** |
| 5 | `update` with conditional clauses (`-if`/`-if-expr`/multiple clauses) | medium (~1-2 days) | high | No, but needs expr-lang for `-if-expr` | **shipped** (literal `-if` only; `-if-expr` rejected as Tier 3) |
| 6 | `group-by … -collect F NAME` | medium (~half day) | medium | No (slice-typed result field) | Do alongside aggregation work |
| 7 | Multi-clause joins, `-as OLD NEW` field renames | medium (~1 day) | low-medium | No | Do when a real pipeline needs it |
| 8 | `-if-expr` / `-set-expr` / `-expr` aggregations | **very high** (~weeks) | **high** | expr-lang AST → Go AST | Decide before #5 |
| 9 | JSON/JSONL `from`/`to` in typed mode | low-medium (~half day) | medium | No (typed runtime has it) | Do when JSONL pipelines come up |
| 10 | Arrow / Parquet typed I/O | medium-high (~1-2 days) | medium | Yes (typed wrapper for Arrow reader) | Do when batch sizes warrant |
| 11 | Window analytic functions | high (~1 week) | medium | Yes (typed window primitives) | Niche; user-driven |
| 12 | Signal processing (`fft`, `convolve`, `spectrogram`, …) | high (~1-2 weeks) | niche but loud | Yes (typed Signal/Spectrum types) | Defer unless asked |
| 13 | `-rollup` / `-cube` | high (~1 week) | low | No (existing `GroupBy` underneath) | Defer |
| 14 | `pivot` | high | low | Possibly | Defer |
| 15 | `merge` (k-way merge of sorted streams) | medium | low | No | Defer |
| 16 | `from ssh` / `from catalog` | medium | medium | No, but interaction with codegen is non-trivial | Defer pending design |

The five "do next / do soon" items together would push typed-codegen
coverage from ~98% to comfortably ≥99% of real pipelines, in roughly
two days of focused work. Item 8 (expression language) is the elephant
— same demand as the top items, but ten to a hundred times the effort.

---

## 1. `update -set FIELD LITERAL` (unconditional)

**What the command does today.** Sets a field to a literal value on
every row. The most basic form: `update -set status active`.

**What typed codegen needs.** Emit `typed.Select(func(r T) T' { ... })`
where `T'` is `T` with the named field's value overridden. If the
field already exists in `T`, output type is `T` (same shape, mutated
field). If the field is new, derive a wider struct `T' = T + new
field` (similar to the join merge struct).

**New runtime?** No. `typed.Select` already exists.

**Effort.** ~Half day. Mostly mirroring the `include/exclude/rename`
projection helper to handle "copy all fields, override one or more."

**Demand.** High. `update -set` is a workhorse of real pipelines —
flagging records, normalizing values, adding constants.

**Recommendation.** **Do next.** Highest value-to-effort ratio of the
deferred items.

---

## 2. Multi-field `sort`

**What the command does today.** `sort FIELD1 FIELD2 -desc` sorts by
primary key, ties broken by secondary, etc. Each field can have its
own `-desc` flag.

**What typed codegen needs.** Emit `typed.SortBy(func(r T) Key { ... })`
where `Key` is a generated tuple struct. Sort closures already exist;
just need to compose comparators across fields, with descending fields
negated (or a custom `cmp` closure passed to a new `SortByFunc`).

**New runtime?** Probably yes — a `SortByFunc[T any](less func(a, b T) bool)`
or `SortByCmp[T any](cmp func(a, b T) int)` to handle mixed
asc/desc gracefully. ~2 hours of runtime work.

**Effort.** ~2 hours total once the runtime helper exists.

**Demand.** Medium-high. "Sort by region asc, revenue desc" is
common in dashboards and reports.

**Recommendation.** Do soon, after #1. Bundles cleanly with the
`SortByFunc` runtime addition.

---

## 3. `cast -field F -type TYPE`

**What the command does today.** Convert one field's type at runtime.
E.g. `cast -field age -type int` to coerce a string column.

**What typed codegen needs.** In the typed world, the field's type
is determined by schema sampling at codegen time, not by an explicit
runtime cast. So `cast` only makes sense when:

1. The user explicitly overrides the inferred type (e.g. samples
   produced `string` because the column was sometimes empty, but the
   user knows it's actually `int64`).
2. They want a typed conversion mid-pipeline (e.g. `int64` → `string`).

For (1): plumb a `cast` clause that participates in `from`'s schema
sampling — overrides the inferred Go type for that column. For (2):
emit a `typed.Select` that builds a new struct with the cast field's
type changed and a conversion expression.

**New runtime?** No.

**Effort.** ~2 hours for case (2). Case (1) is part of a broader
"schema overrides" feature (also useful for the existing `--schema-file`
follow-up mentioned in the codegen proposal).

**Demand.** Medium. Mostly a workaround for sampling mis-inferences,
which become rarer as data gets cleaner.

**Recommendation.** Bundle with #1 if cheap, otherwise defer.

---

## 4. `top N -field F`

**What the command does today.** Shorthand for `sort -desc -field F | limit N`.

**What typed codegen needs.** Emit the same composition: a
`typed.SortByDesc` followed by `typed.Limit`. Or, when `N` is small
relative to the input size, a future `typed.TopN` heap-based primitive
(O(N log K) instead of O(N log N) materialize-and-sort).

**New runtime?** No, for the simple desugaring. Optional `typed.TopN`
heap helper for performance.

**Effort.** ~1 hour for the desugaring. ~1 day with the heap-based
primitive.

**Demand.** Medium. Usually expressed as `sort -desc | limit`
explicitly, but `top` saves keystrokes.

**Recommendation.** Do soon, simple version. The heap-based optimiser
is its own project (proposal §15 of the original moonshot doc lists
"Top N optimization" but it's deferred).

---

## 5. `update` with conditional clauses

**What the command does today.** First-match-wins clause syntax:

```bash
update \
    -if revenue gt 10000 -set tier premium \
    + \
    -if revenue gt 1000 -set tier standard \
    + \
    -set tier basic
```

**What typed codegen needs.** Same emission shape as `where` (the
predicates) crossed with `update -set` (the assignments). Each clause
becomes an `if` arm in a closure passed to `typed.Select`. Field-name
validation and type-correct literal formatting reuse the helpers from
`where` and `update -set` (#1).

**Special cases:**

- An `-if-expr EXPR -set FIELD VALUE` clause needs the expression
  language. If we pre-emptively skip these (i.e. only support `-if FIELD OP VALUE`),
  most real `update` pipelines still work. Mark `-if-expr` as Tier 3
  with a clear error.
- An `-set-expr FIELD EXPR` clause needs the expression language. Same
  treatment.

**New runtime?** No.

**Effort.** ~1-2 days. Mostly clause iteration and error message work.

**Demand.** High. Conditional updates are common in ETL pipelines.

**Recommendation.** Do after #1. Build on the same helpers.

---

## 6. `group-by … -collect F NAME`

**What the command does today.** Aggregates a field into a slice
per group: `-collect timestamp timestamps` produces a per-group
`[]int64`-style result.

**What typed codegen needs.** Add a slice-typed result field to the
generated aggregator's `Result` struct (e.g. `Timestamps []int64`),
append to the running slice in the `Add()` method. The synthesised
aggregator code is straightforward; the generated `to csv`/`to table`
sinks need to decide how to render the slice (probably `fmt.Sprint`
of the slice).

**New runtime?** No.

**Effort.** ~half day. Most of the cost is making `to csv` /
`to table` deal sensibly with slice fields (CSV spec doesn't really
have a story for nested values; users would typically consume slice-
field results from typed Go directly).

**Demand.** Medium. Useful for "all timestamps per user", "all SKUs
per order".

**Recommendation.** Bundle with the aggregation enhancements when
they next come up.

---

## 7. Multi-clause joins / `-as OLD NEW` field renames

**What the command does today.** Multiple lookups against the same
right-side file in a single `join`, separated by `-`:

```bash
join kinds.csv \
    -on a_kind kind -as kind_name a_kind_name \
    - \
    -on z_kind kind -as kind_name z_kind_name
```

**What typed codegen needs.** Emit one `typed.HashJoin` per clause,
each producing its own merged struct (e.g. `T_KindsAOnly`,
`T_KindsZOnly`). The `-as OLD NEW` rename means the right-side field
is incorporated into the merged struct under the new name.

**New runtime?** No.

**Effort.** ~1 day. Mostly the rename-aware merge struct emission.

**Demand.** Low-medium. Multi-clause joins are uncommon outside
specific dimension-table patterns; `-as` renames come up more.

**Recommendation.** Do when a real pipeline needs it. Easy to slot in.

---

## 8. `-if-expr` / `-set-expr` / `-expr` aggregations (expression-lang → Go)

**What the commands do today.** Filter / set / aggregate using
expr-lang expressions like:

```bash
where -if-expr 'age > 30 && status == "active"'
update -set-expr discount 'amount * 0.1'
group-by region -expr 'sum(amount * (1 - discount_rate))' net_revenue
```

The expressions are interpreted at runtime via the expr-lang library.

**What typed codegen needs.** Translate each expression's AST to Go
source code that operates on the typed struct. Concretely:

- Field references become `r.GoFieldName` (already done in `where -if`).
- Operators map directly: `&&` → `&&`, `==` → `==`, `*` → `*`, etc.
- Function calls (`sum`, `avg`, `len`, `lower`, etc.) need a mapping
  table from expr-lang stdlib to Go equivalents.
- String concatenation, comparisons, type coercions need careful
  handling — expr-lang is dynamically typed, Go isn't.

**New runtime?** Possibly a small `typed/expr` helper package for
common builtins (e.g. `lower(s)` → `strings.ToLower(s)` is
straightforward, but `len(slice_field)` and `int(string)` need helpers).

**Effort.** **Very high.** Realistically 1-2 weeks for a credible
v1, with edge cases that would dribble in for months. The expr-lang
library has roughly 50 builtins, multiple operator families, and
type-coercion rules that must be made explicit in the emitted Go.

**Demand.** **High.** Users genuinely use these — both `-if-expr` for
filters that combine multiple fields and `-set-expr` for computed
columns.

**Recommendation.** **Decide deliberately, ideally after #1 and #5
ship.** This is the single biggest piece of remaining typed-codegen
work, and it's also the one with the biggest demand. Two paths:

- **Defer indefinitely.** Document the limitation; users wanting
  expression-language pipelines stay on `SSQLGO=1`. Loses some
  high-value pipelines but keeps Phase 2 surface tight.
- **Commit to a milestone.** Treat expr-lang→Go as its own Phase 2.5,
  with its own proposal doc, scope, and sign-off. Worth doing if
  there's evidence of users hitting this wall.

Either is defensible. The wrong path is: starting it without a clear
spec, then having half-translated expressions silently produce wrong
results.

---

## 9. JSON/JSONL `from`/`to` in typed mode

**What the commands do today.** `from data.jsonl` reads records;
`to json output.jsonl` writes them.

**What typed codegen needs.** The typed runtime already has
`typed.ReadJSONL[T]` and `typed.WriteJSONL[T]`. Codegen just needs
to emit calls to them. `from` schema sampling is trickier — JSON
isn't tabular by nature, so we'd sample N lines and infer struct
fields the same way as CSV.

**New runtime?** No.

**Effort.** ~half day for the simple JSONL case. JSON-array files are
their own thing.

**Demand.** Medium. JSONL pipelines come up for log processing and
event streams.

**Recommendation.** Do when a real JSONL pipeline is the bottleneck.

---

## 10. Arrow / Parquet typed I/O

**What the commands do today.** `from data.arrow` and `from data.parquet`
read columnar formats.

**What typed codegen needs.** Typed wrappers around Arrow/Parquet
readers that produce `iter.Seq[T]`. Schema discovery is direct (the
file has a real schema, no sampling needed) but type mapping from
Arrow types to Go types needs care.

**New runtime?** Yes — `typed.ReadArrow[T]`, `typed.WriteArrow[T]`,
similar for Parquet. Could be ~1 day each. Listed as Phase 1.8 in
the typed-package-proposal.

**Effort.** Medium-high. ~1-2 days runtime + ~half day codegen wiring.

**Demand.** Medium. Arrow / Parquet pipelines are typically the ones
that benefit *most* from typed codegen (large columnar workloads).

**Recommendation.** Do when batch performance becomes a focus.

---

## 11–16. Niche or low-demand items

These are listed for completeness but recommend **defer indefinitely**
unless demand surfaces:

- **Window analytic functions** — typed window primitives don't exist
  yet; rank/lag/lead/rolling-aggregate would each be its own runtime
  addition. ~1 week.
- **Signal processing** (`fft`, `convolve`, `correlate`, `spectrogram`,
  `ifft`) — would need typed `Signal` / `Spectrum` types and
  conversions. The user base for these is small but vocal. ~1-2 weeks
  to do well.
- **`-rollup` / `-cube`** — multi-level aggregations. The synthesised
  multi-aggregator code is non-trivial, and most users build rollup
  reports as separate group-bys. ~1 week.
- **`pivot`** — wide-to-long-to-wide reshaping. Schema is dynamic
  (depends on data), so sampling-based codegen is hard. Possibly
  fundamentally at odds with typed codegen.
- **`merge`** — k-way merge of pre-sorted streams. Nice in theory; in
  practice users sort and then process linearly.
- **`from ssh` / `from catalog`** — distributed sources. Codegen for
  these has a different shape (the right side runs remote) and
  requires its own design pass. Probably its own Phase 3.

---

## Recommended sequencing

If we want a tight near-term plan:

**Sprint 1 (~2 days):** Items #1, #2, #3, #4 — `update -set` literal,
multi-field sort, cast, top. All low-effort, high-or-medium demand,
no runtime additions. Pushes coverage to ≥99% of common pipelines.

**Sprint 2 (~1-2 days):** Item #5 — conditional update clauses
(without `-if-expr`/`-set-expr`). Builds directly on #1.

**Decision point:** Item #8 — expr-lang → Go translation. The biggest
remaining lever, but the biggest cost. Don't start without an explicit
proposal and sign-off.

**Then:** Items #6, #7, #9, #10 as user demand surfaces.

**Defer:** Items #11–16 unless requested.

After Sprint 1+2 the typed codegen story is "covers every common
pipeline shape that doesn't use expression-language operators."
That's a complete enough story to ship as a v1.0 announcement and
get real-user feedback before committing to the expr-lang work.
