# Capability-Gap Survey: What Peer Tools Have That ssql Doesn't

Reference: DFC122
Created: 2026-08-31
Last modified: 2026-09-03

[Back to Index](./README.md)

**Status:** Survey — gaps ranked, nothing scheduled. Trigger: the
resample discovery (DFC121) made Ross ask what ELSE is missing.
Method mirrors DFC116: enumerate systematically, judge each against
ssql's soul, rank by (frequency of need × fit), record the
rejections so they aren't re-litigated.

## Baseline

ssql today (from `-spec-json`, the authority): cast, convolve,
correlate, count, distinct, exclude, fft, from (13 sources incl.
ssh/https/catalog), generate (go/sql/ssql), group-by (aggs, exprs,
streaming), ifft, include, join (equality hash), limit, merge,
offset, **pivot** (long→wide), rename, sample, serve, sort,
spectrogram, tee, to (13 sinks incl. chart/animate/explore), top,
union, update (set/set-expr/if), where (ops + expr), **window**
(partition/order/frame functions). Plus DFC121's `resample`
(designed). Strengths no peer matches: five-backend codegen, the
served/browser workspace, display sinks, DSP verbs, distributed
ssh/catalog.

Peers surveyed: Miller (mlr — richest verb set, ~60), qsv/xsv,
csvkit, jq, nushell (+polars plugin), datamash, and the
pandas/polars vocabulary users arrive with.

## Tier 1 — clear gaps, high value, strong soul-fit

1. **`limit -last N`** (was: `tail`; renamed 2026-09-03, Ross;
   **SHIPPED 2026-09-03** — ring buffer `ssql.TakeLast`/`typed.TakeLast`
   in all lanes, SQL reversed-order wrap with loud refusal on unsorted
   input, optimiser leaves `-last` alone, three equivalence pins +
   duckdb oracle, sabotage-verified) — last N records without knowing
   the count. We have limit (head) and
   offset. NOT a new `tail` verb, for three reasons recorded so it
   isn't re-litigated: (a) ssql's data verbs are SQL-flavored
   (where/limit/offset/group-by/distinct/join/union/top) — `tail`
   would be the first coreutils-flavored one and immediately begs
   "where's head?", and a `head` alias for `limit` would need every
   Kind-keyed table (optimiser rules, needsWrap, order declarations,
   schema ops, SQL translator) to know both names — the DFC115 drift
   machine; (b) "head"/"tail" already name the server-side and
   browser-side pipeline halves in the workspace ("reset tail",
   "tail-optimize") — an `ssql tail` stage inside the tail pipeline
   is a vocabulary collision; (c) as a mode of `limit` it inherits
   all of limit's optimiser/order/barrier knowledge for free and
   yields a mirror rule (`sort -desc x | limit -last 3` →
   `top 3 -asc -field x`). Build: ring buffer in every lane; typed is
   a barrier (limit is already SerialOnly); SQL lane must refuse
   loudly on an unordered source ("last N" needs an order — the
   sample -seed precedent) or lower via a preceding ORDER BY. Help
   text carries the pointer for coreutils muscle memory: "looking
   for tail? limit -last". *Do first — an afternoon.*

2. **`describe`** (mlr summary/stats1, csvkit csvstat, pandas
   df.describe) — per-field type, count, nulls/missing, distinct,
   min/max/mean/median for numerics, shortest/longest for strings.
   **SHIPPED 2026-09-03** with three recorded decisions: (a)
   record-shaped in every lane — rows are heterogeneous (numeric
   stats absent on string fields) and a typed struct would have to
   lie with zeros/NaN, so typed pipelines re-enter record mode here
   via the planner boundary (pivot precedent); (b) unrestricted row
   order is SORTED BY FIELD NAME, not first-seen — the equivalence
   gate showed first-seen order is lane-dependent (CSV reader keeps
   header order, generated record code iterates alphabetically) and
   a lane-dependent contract is no contract; explicit FIELDS keep
   their given order; (c) shortest/longest-for-strings deferred (the
   min/max columns stay numeric-only so one column has one type
   across lanes). SQL: exact distinct + `median`, DuckDB typeof
   mapped to int/float/string/bool, loud refusal without a known
   column list. Two bugs caught by the duckdb lane before shipping.
   THE first command every explorer wants; also exactly what the
   workspace's deleted Statistics panel should have been (a
   `describe` stage renders in the grid — no bespoke panel).
   Composes: `from x.parquet -records`-style cheap paths later.

3. **`unpivot`** (was: `melt`; renamed 2026-09-03, Ross — SQL names:
   UNPIVOT is the term in DuckDB, SQL Server, Oracle, Snowflake, and it
   pairs with our existing `pivot`; `melt` is pandas/tidyverse jargon,
   mentioned in the help text as a pointer only). **SHIPPED
   2026-09-03**: `-id`/`-value` (accumulate) + `-col`/`-val` output
   names (the exact inverse of pivot's `-row`/`-col`/`-val`); default
   values = all non-id fields SORTED (record iteration order is
   lane-dependent, same contract as describe); absent/null → no row
   (SQL UNPIVOT default); typed template with synthesized struct when
   the folded fields share a Go type (all-numeric → float64), record
   fallback via the planner boundary otherwise; SerialOnly because the
   typed Stream runtime has no 1:N operator yet (TODO: Stream.SelectMany
   would make it parallel); DuckDB native UNPIVOT in generate sql
   (caveat: DuckDB coerces mixed value types to one). Loud refusal when
   an -id collides with -col/-val. Sabotage-verified via the duckdb
   oracle. *Original survey note (pre-rename):* ssql had pivot but NOT
   its inverse; wide→long is what `to chart`/multi-Y actually wants
   (one value column + a series column) — high chart synergy. The
   sketched grammar was `melt -keep … -value … -into metric value`;
   shipped as `unpivot -id … -value … -col metric -val value`.
4. **`extract`** (regex capture groups → fields; nushell parse, mlr
   sub/put, the grep→awk gap) — ssql can MATCH regex (where) but
   not EXTRACT. This is the log-processing door: `from lines
   app.log | extract -field line -re '(?P<ts>\S+) (?P<lvl>\w+)
   (?P<msg>.*)'` (named groups become fields, non-matches loud or
   -skip). Opens a whole audience (the awk/sed refugee). Needs a
   `from lines` (raw text → single-field records) sibling — check
   whether stdin JSONL fallback already half-covers. Medium build,
   outsized reach.

5. **`fill`** (mlr fill-down/fill-empty) — carry values down over
   missing fields, or default empties: `ssql fill -down region
   -default status unknown`. Resample's record-level sibling;
   ragged real-world data (merged sheets, sparse logs). Small
   build. *Prerequisite settled 2026-09-03:* "missing" is defined by
   [DFC124](./dfc124_missing_values.md) — absent ∨ null ∨ empty
   string; an empty CSV numeric cell is now absent (was 0), so
   `fill -default score 0` will actually see it. Design notes from
   the discussion: `-down` CONSUMES order (first Tier-1 verb to
   declare OrderConsumes; parallel form is semantically wrong, so
   SerialOnly on semantic grounds); `-default` alone is row-local;
   SQL: `-default` → COALESCE, `-down` → LAST_VALUE(x IGNORE NULLS)
   OVER (ORDER BY …) with the limit -last refusal when unsorted.

## Tier 2 — valuable, schedule on demand

6. **`diff`** — keyed dataset comparison (added/removed/changed
   records, changed fields): `ssql diff old.csv new.csv -on id`.
   No CLI peer does this well (csvdiff/daff are niche); constant
   real need; fits our equivalence-testing culture. Medium.
7. **`assert`** (mlr check/having-fields, dbt tests in spirit) —
   pipeline-embedded validation that FAILS LOUDLY: `ssql assert
   -not-null id -type age int -unique id -if price ge 0`. The
   loudness doctrine as a user feature; CI-friendly exit codes.
   Small-medium.
8. **`to … -by FIELD` (partitioned output)** — one file per group
   (mlr split). ETL fan-out; composes with existing sinks. Small.
9. **`from seq`** (mlr seqgen) — generated sequences for demos,
   docs, testing: `from seq 1 1000000` (+ maybe -expr per row).
   Trivial; disproportionate documentation value.
10. **Asof/range join** (kdb aj) — `join -asof -on ts [-tolerance
    5s]`. Resample's sibling for aligning irregular series without
    gridding. Post-DFC121; reuse its merge machinery. Medium.

## Tier 3 — surveyed and REJECTED (recorded so we don't re-ask)

- *Terminal bar charts* (mlr bar): `to chart`/workspace own this.
- *Statistical modeling* (bootstrap, surv, stats2 regression):
  R/Python territory; our exit is `to parquet`/DuckDB.
- *Case/whitespace/latin1 cleaners*: expression functions in
  update -set-expr, not verbs (add exprfn helpers if asked).
- *sec2gmt etc.*: cast/expr territory; time FUNCTIONS may grow
  with resample, not verbs.
- *tac/shuffle/decimate/repeat/fraction*: sort -desc, sample
  machinery, and exprs cover the real uses.
- *unsparsify/regularize*: the schema-header wire format already
  regularizes at boundaries; revisit only if a concrete ragged-
  JSONL failure shows up.
- *TUI explorer* (visidata): the workspace is our answer.
- *Lazy dataframes* (polars): `generate go` IS our answer.

## Recommended order (if Ross wants a "missing verbs" arc)

tail → describe → melt → fill → extract (+from lines) → assert →
the rest on demand. Every one: package primitive first, thin CLI,
all-backend fragments where results change (DFC102 gates,
gap-ridden fixtures for fill/melt), FieldsFromFlag completion,
spec-json for free, equivalence + scale cases per the checklists.

## Sources

Miller verb reference (miller.readthedocs.io); qsv/csvkit/datamash
manuals; nushell + polars plugin docs; pandas/polars API vocabulary.
