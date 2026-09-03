# Missing Values Across the Lanes: An Empty Cell Is Absent

Reference: DFC124
Created: 2026-09-03
Last modified: 2026-09-03

[Back to Index](./README.md)

**Status:** Decision, 2026-09-03 (Ross: "settle the empty-cell question
first", before `fill`). Record lanes implemented same day; the typed
lane's gap is recorded as the next step, not hidden.

## 1. The finding

Building `unpivot` (DFC122) showed `Bob,5,` unpivoting to `Bob,feb,0`
in the Go lanes and to nothing in the DuckDB lane. The fact table:

| Reader | empty numeric cell | empty text cell | JSON `null` |
|---|---|---|---|
| record CSV (`ReadCSVFromReader`) | **`0`** (column parser's error fallback) | `""` | field dropped (absent) |
| typed CSV | **zero value** (documented, `typed-reference.md`) | `""` | n/a |
| DuckDB | `NULL` | `NULL` | `NULL` |

So `describe` on a numeric column with empties reported `missing 0` and
a mean pulled toward zero, while the SQL lane reported the truth. The
coercion site is `parseAsInt/Float/Bool` in `io.go`: a per-column fast
parser that **returns the zero value on any parse failure** — an empty
cell, but also `N/A` or `abc`. `describe`'s own definition of missing
(absent ∨ null ∨ empty string) was right; the reader had already
destroyed the information before any command saw it.

## 2. The decision

**The record model has ONE representation of missing: the field is
absent.** JSON `null` already becomes absent on read. From today, an
empty CSV/TSV cell in a numeric or boolean column is absent too (a `nil`
slot in the schema-shared record: `Has` stays true — the column exists —
while `GetOr` yields the default, `All` yields `nil`, JSON writes
`null`, CSV/table write an empty cell). Commands define "missing" as
absent ∨ null ∨ empty string — `describe` already did; `unpivot` now
skips empty strings too; `fill` will use the same definition.

**Empty text cells stay `""`.** CSV cannot distinguish an empty string
from a missing string; ssql keeps the value (a legitimate string) and
lets commands treat it as missing. DuckDB reads it as NULL, so
command-level outputs (`describe`, `unpivot`) agree across lanes while
`where -if s eq ""` stays expressible.

Why absent rather than a typed NULL: the record model has no null
scalar and never needed one — `GetOr` with a default is the universal
read, and absence composes with it. Introducing a null would create a
second missing to keep consistent with the first.

## 3. What this does NOT fix yet (recorded, not hidden)

- **The typed lane.** A typed struct cannot hold absence; the typed
  reader writes the zero value for an empty cell (`typed/io.go`
  decoders: `if s == "" { *p = 0 }`). Pointer types (`*int64`) exist and
  the sampler already picks `*string` for all-empty columns, but making
  it pick pointer types for *partially* empty columns ripples through
  every typed template (projections, joins, group-by, resample, unpivot
  all assume scalar GoTypes). That is its own slice. Until then the
  empty-cell equivalence cases SKIP the typed/parallel lanes with this
  DFC as the stated reason — a visible, named divergence rather than a
  fixture-invisible one.
- **Unparsable non-empty cells** (`N/A` in an int column) still become
  the zero value silently in the record reader. Same class of bug
  (information destroyed before any command sees it); the honest
  behaviors are absent-with-a-loud-count or a hard error. Follow-up.
- **First-row-only inference.** The record CSV reader infers each
  column's type from the FIRST row; an empty first cell makes the whole
  column a string column. The typed sampler looks at N rows. Follow-up:
  sample for the record reader too.

## 4. Gates

- Equivalence fixture `empties.csv` (`id,n,f,s,b` with one all-empty
  row): `describe`, `unpivot -id id`, `from | to csv` identity, and a
  `where` over the numeric column — exec, go-record, generate-ssql, and
  duckdb must agree byte-for-byte (typed/parallel skipped per §3).
- Sabotage: restoring the zero fallback must fail `describe` against
  the DuckDB oracle.
- Root unit tests on the reader (empty → absent, non-empty unchanged,
  string column keeps `""`).

## Prior art / related
- [DFC122](./dfc122_capability_gap_survey.md) — `describe`'s missing
  definition, `unpivot`'s skip rule, `fill` (blocked on this).
- `doc/typed-reference.md` §Supported field types — the typed zero-value
  rule this DFC leaves in place for now.
- `doc/research/multimode-equivalence-testing.md` — why the DuckDB lane
  is the independent oracle that surfaced this.
