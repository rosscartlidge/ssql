# Group-by Aggregates: What Is Missing, and Flags versus Expressions

Reference: DFC129
Created: 2026-09-15
Last modified: 2026-09-15

[Back to Index](./README.md)

Status: **plan for decision** (§8). Ross, 2026-09-15, after a session of
group-bys: "we are missing some standard aggregation functions — first,
last, any — what else is missing?" then "lets write up the full plan
first. what do you think of the idea of implementing them in expr —
would that make it simpler but less efficient" and "you have
-stream-expr as well".

## 1. What group-by can do today

Flags: `-count R`, `-sum F R`, `-avg F R`, `-min F R`, `-max F R`,
`-collect F R` (a JSON list per group), plus two escape hatches:
`-expr EXPR R` (evaluate one expression over the whole group) and
`-stream-expr INIT EVERY FINAL R` (a fold). `-rollup` / `-cube` add the
parent levels (DFC-less, rollup-cube-design.md).

What each lane does with them — five implementations of one semantics,
so this table is the work list for any new aggregate:

| | exec | record codegen | typed codegen | `generate sql` | optimiser (`generate ssql`) |
|---|---|---|---|---|---|
| count/sum/avg | `ssql.Count/Sum/Avg` (`helpers.go:buildAggregator`) | same calls emitted (`group_by.go:736`) | native accumulator, **mergeable → parallel** (`typed_groupby.go`) | `COUNT(*)`, `SUM(f)`, `AVG(f)` | knows the flag arity (`generate_ssql.go:1985`) |
| min/max | `ssql.Min[float64]`, `Max[float64]` | same | native, numeric only (loud refusal on string) | `MIN(f)`, `MAX(f)` | knows arity |
| collect | `ssql.Collect` | same | **refused** ("not yet supported … drop -typed") | `LIST(f)` | knows arity |
| `-expr` | `ssql.ExprAgg`: build per-field arrays for the group, run the VM once | same | lowered to native accumulators **only for sum/len shapes** (`expr_agg_lower.go`); anything else → record fallback | **no translation** (loud) | **bails out** (`return nil, true`) |
| `-stream-expr` | `ssql.StreamExprAgg` | same | lowered when the state is literal + numeric; forces **SerialOnly** (fold not mergeable) | no translation | bails out |

Inside `-expr` the group is an environment where every field is an
array (`buildAggBatchEnv`, `expr_agg.go:312`) and `_records` is the
list of row maps. Functions available there: ssql's `max min first
last` (`aggMax…aggLast`, type-preserving), `count()`, `avg/mean`, and
every expr-lang v1.17.6 array builtin — `sum mean median min max first
last count uniq sort sortBy groupBy reduce join map filter …`. So
`-expr 'first(name)'`, `-expr 'median(salary)'`, `-expr
'len(uniq(city))'`, `-expr 'join(sort(name), ", ")'` **all work today in
the interpreted lane**. Nobody finds them, and they stop at the exec
and record lanes.

Registration sites a new flag has to touch (counted while reading —
this is why §6 proposes one table):

1. `group_by.go` — the `Flag(...)` builder with `FieldsFromFlag`, examples.
2. `group_by_specs.go:parseAggSpecs` — flag → `aggSpec{function}`.
3. `helpers.go:buildAggregator` — function → `ssql.AggregateFunc` (exec).
4. `group_by.go:~736` — function → emitted library call (record codegen).
5. `group_by.go:aggResultType` — function → wire type.
6. `typed_groupby.go:buildTypedAggregator` — accumulator state, Add, Merge, Result.
7. `generate_sql.go:~660` — flag → SQL function.
8. `generate_ssql.go:~1985` and `schema_ops.go:~194` — flag arity for the optimiser and the schema walker.
9. `completion_test.go:TestFieldCompletionConfiguration`, `equivalence_test.go`, docs (`README`, `cli-codelab` §3, `ai-cli-generation.md`, `EXPRESSIONS.md`).

## 2. Found while looking (fix regardless of the plan)

- **`-min` / `-max` on a string field.** Three lanes, three answers:
  exec prints `0` for every group (`Min[float64]` coerces the string to
  its zero and says nothing); typed refuses loudly ("requires a numeric
  type, got string"); DuckDB returns `Bob`. `-expr 'min(name)'` returns
  `Alice` (type-preserving `aggCompare`). The exec answer violates
  fail-loudly and the lanes disagree — exactly the `top`-by-string shape
  (v4.54–55). Fix: exec and record codegen use the ordered comparison
  the `-expr` path already has (strings, ints, floats, times), typed
  accepts `string` for min/max, equivalence case with a shuffled string
  fixture and a Golden.
- **`doc/ai-code-generation.md` lists `ssql.First("field")` and
  `ssql.Last("field")`** as library aggregates. They do not exist (§1
  grep). The LLM prompt doc is teaching a call that fails to compile.
  Either add them (this plan does) or delete the lines now.
- **`-collect` under explicit `-typed`** exits with "drop -typed for
  now" instead of falling back — the only aggregate that turns a mode
  choice into a failure.

## 3. What is missing, against SQL / DuckDB / Postgres

| Aggregate | DuckDB | Postgres | Today in ssql | Wanted flag |
|---|---|---|---|---|
| first / last | `first(x)` / `last(x)` (`arg_min`-style, order-dependent) | none (`array_agg(x)[1]`, or DISTINCT ON) | `-expr 'first(x)'` only | `-first F R`, `-last F R` |
| any value | `any_value(x)` | `any_value(x)` (PG 16) | — | `-any F R` (= first, weaker promise) |
| count distinct | `count(DISTINCT x)` | same | `-expr 'len(uniq(x))'` | `-count-distinct F R` |
| median | `median(x)` | `percentile_cont(0.5)` | `-expr 'median(x)'` | `-median F R` |
| percentile | `quantile_cont(x, p)` | `percentile_cont(p) WITHIN GROUP` | — | `-percentile F P R` |
| stddev / variance | `stddev_samp`, `var_samp` (+`_pop`) | same | `-stream-expr` Welford, in theory | `-stddev F R`, `-variance F R` (sample; `-pop` variants later if asked) |
| string agg | `string_agg(x, sep)` | same | `-expr 'join(x, sep)'` | `-string-agg F SEP R` |
| arg max / arg min | `arg_max(arg, val)` | subquery | `-expr '_records[indexOf(salary, max(salary))].name'` | `-arg-max F BY R`, `-arg-min F BY R` |
| mode | `mode(x)` | `mode() WITHIN GROUP` | — | `-mode F R` |
| bool and / or | `bool_and`, `bool_or` | same | `-expr 'all(x, #)'` | `-all F R`, `-any-true F R` (rare; defer) |
| product | `product(x)` | none | `-expr 'reduce(x, #acc * #, 1)'` | defer |

Naming follows the existing flags (one aggregate per flag, field then
result, `Accumulate()`, no comma lists). `-percentile` takes P as its
own argument (`-percentile salary 0.9 p90`); `-string-agg` takes the
separator as its own argument; `-arg-max name salary top_earner` reads
"the name at the max salary".

## 4. Flags or expressions? (Ross's question)

**Simpler, yes; less efficient, yes — and it is the typed and SQL
lanes, not the interpreter, where it costs.**

*Interpreted lane, measured* (group-by stage alone, 1 M rows of the
benchmark CSV as 165 MB JSONL on stdin, one key, this machine):

| Form | Wall | Peak RSS |
|---|---:|---:|
| `-sum a_sequence t` | 1.33 s | 0.45 GB |
| `-expr 'sum(a_sequence)' t` | 1.85–2.13 s | 0.64–0.83 GB |
| `-stream-expr '{s:0}' '{s:s+a_sequence}' 's' t` | 1.95 s | 0.60 GB |
| `-expr 'first(a_name)' t` | 2.08 s | 0.69 GB |
| `-collect a_name t` | 1.47 s | 0.54 GB |

Both forms materialise each group — `AggregateFunc` is
`func([]Record)` and `GroupByFields` builds `map[key][]Record` — so
the interpreter pays the group's memory either way; `-expr` adds
~1.5× time and ~1.5× memory for building the per-field arrays and row
maps and running the VM. `-stream-expr` evaluates incrementally but the
group is still a slice underneath, so it saves nothing on memory here.
Tolerable for an escape hatch; not what you want as the implementation
of `first`.

*Typed lane.* `-expr` is lowered to a native mergeable accumulator
only when the patched tree is sums and lengths joined by arithmetic
(`expr_agg_lower.go`). `first`, `last`, `min`, `max`, `median`, `uniq`
are `aggShapeNone` → the whole group-by falls back to record codegen.
That is the efficiency cliff: from the parallel typed group-by (the
README's 0.27 s cube) to record mode (minutes on the same file).
Making expressions fast means teaching the lowering each new shape —
the same accumulator per aggregate that a flag would need, written
inside a transpiler instead of a template, plus the `-stream-expr`
problem that a fold is not mergeable so it serialises the group-by.

*SQL lane.* `-expr` and `-stream-expr` have **no translation** and the
optimiser bails out of any stage that has them. A flag is a declarative
fact — "first of `name`" — that every lane can act on: exec calls a
library function, codegen emits it, typed emits an accumulator, SQL
writes `first(name)`, the optimiser knows its arity and its input field.
An expression is a program; only the interpreter runs programs. This is
DFC115's line: the command should own the *what*, and `-expr` stays as
the *how* for the cases no flag covers.

**Position:** add the flags; keep `-expr`/`-stream-expr` as they are.
Where an expression already does the job (median, first, uniq, join),
the flag's exec implementation can reuse the same Go helper the
expression environment uses (`aggFirst`, `aggCompare`, …) so there is
one comparison and one median in the codebase, not two.

## 5. Accumulator kinds — the typed lane is the real design

Every proposed aggregate is one of six state shapes. Naming them is
what keeps §6's table small:

| Kind | State per group | Add | Merge (shards) | Result | Members |
|---|---|---|---|---|---|
| scalar-fold | one value (+count) | fold | fold | value | sum, avg, count |
| ordered-extreme | best value + have-flag | compare | compare | value | min, max **(strings too)** |
| positional | value + have-flag **+ shard ordinal** | keep first (or overwrite for last) | take the peer with the lower (first) / higher (last) ordinal | value | first, last, any |
| paired-extreme | best key + carried value | compare on key | compare on key | carried value | arg-max, arg-min |
| set | `map[T]struct{}` | insert | union | `len` | count-distinct (memory O(distinct), like SQL) |
| ordered-list | `[]T` | append | concatenate in shard order | sort → statistic; or join | median, percentile, mode, string-agg (memory O(group), same as `-collect` today) |
| welford | n, mean, M2 | Welford update | Chan et al. parallel merge | var / stddev | variance, stddev |

`positional` needs one thing the typed runtime lacks: the shard's
ordinal reaching the accumulator so `Merge` can pick the earlier
shard's first. `GroupByParallel` already merges shards in shard order
(`claude/concurrency.md` §12 — per-shard dump order is deterministic);
passing the ordinal into `ParallelAggFunc` (or merging left-to-right and
defining "first" as first-in-merge-order) keeps first/last exactly
equal to the serial answer on a file source. On an unordered source the
serial answer is itself arbitrary, which is what `any` documents.

`ordered-list` kinds cost memory proportional to the group. So does
`-collect` today, and so does DuckDB's `median` (it sorts). Acceptable
with a note in the flag help; a t-digest sketch is a later option if
someone hits it.

## 6. The refactor: one registry, derived lanes

Adding one flag today touches nine files (§1). Before adding ten flags,
collapse the per-aggregate facts into one table in `group_by_specs.go`:

```go
type aggDef struct {
    flag, fn   string          // "-first", "first"
    args       []string        // {"field", "result-name"}; percentile adds "p"; string-agg adds "sep"
    kind       aggKind         // scalar-fold | ordered-extreme | positional | paired | set | ordered-list | welford
    resultType func(fieldType string) string  // "int" | "float" | field's own type | "string" | "json"
    libCall    string          // "ssql.First(%q)" — exec and record codegen share it
    sql        string          // "first(%s)" — DuckDB; "" = loud refusal
    numericOnly bool
}
```

Derived from it: the autocli `Flag()` declarations (loop, keeping the
hierarchical builder style for the hand-written ones), `parseAggSpecs`,
`buildAggregator` (exec), the record-codegen call, `aggResultType`
(which finally gets the **input field's type**, so `-first name` is a
`string` on the wire instead of today's default `float`), the typed
accumulator (one template per *kind*, seven templates for fifteen
flags), the SQL map, and the arity tables the optimiser and schema
walker keep by hand today. The `-expr`/`-stream-expr` paths stay as
they are. This is "refactor while you work" at the point where it pays
for itself, and it removes the class of bug where a flag exists in four
lanes and not the fifth.

## 7. Phases

**Phase 0 — fixes and the registry (½ day).** §2 fixes; the `aggDef`
table with today's six flags moved onto it, behaviour-preserving,
existing equivalence cases green; `aggResultType` from the field type.

**Phase 1 — the ones people type from memory (1 day).** `-first`,
`-last`, `-any`, `-count-distinct`, `-string-agg`, plus `-min`/`-max`
on strings. Kinds: positional, set, ordered-list (join only). Library:
`ssql.First/Last/CountDistinct/StringAgg` (the doc drift closes). SQL:
`first/last/any_value/count(DISTINCT)/string_agg`. Typed: positional
needs the shard ordinal (§5). Equivalence cases on **shuffled fixtures
with distinct values** and `Ordered:false` multiset comparison, DuckDB
lane, Golden for first/last on a `-presorted` input where the answer is
defined.

**Phase 2 — statistics (1 day).** `-median`, `-percentile F P R`,
`-stddev`, `-variance`, `-mode`. Kinds: ordered-list, welford. SQL:
`median`, `quantile_cont`, `stddev_samp`, `var_samp`, `mode`. Golden
oracles from a hand-computed fixture (float normalisation in the
harness already tolerates the last digit). Population variants only if
asked.

**Phase 3 — paired (½ day).** `-arg-max F BY R`, `-arg-min F BY R`.
SQL: `arg_max(F, BY)`; the Postgres note goes in DFC060's portability
list. Ties: first seen wins, documented, matches DuckDB.

Each phase ends with the corpus (`TestPipelineCorpus`), the equivalence
gate, `TestFieldCompletionConfiguration`, docs (`README` group-by
bullet, codelab §3 gets `-first`/`-count-distinct` in one example,
`ai-cli-generation.md` flag list, `EXPRESSIONS.md` cross-reference from
the array builtins to the flags), and a CHANGELOG entry. Total ≈ 3
days; Phase 0+1 is a release on its own.

## 8. Decisions

| # | Question | Recommendation |
|---|---|---|
| 1 | Flags or expressions? | Flags (§4); `-expr`/`-stream-expr` unchanged as escape hatches |
| 2 | Registry refactor first? | Yes (§6) — nine touch points per flag otherwise |
| 3 | Batch 1 contents | `-first -last -any -count-distinct -string-agg` + string min/max |
| 4 | Names | `-count-distinct` (not `-distinct-count`), `-any` (SQL says `any_value`, we say `any`), `-string-agg` (SQL's name), `-arg-max F BY R` |
| 5 | Result types | the field's type for first/last/any/arg-*; `int` for count-distinct; `float` for statistics; `string` for string-agg |
| 6 | `-median`/`-percentile` memory | O(group) sorted slice, like `-collect` and DuckDB; note in help; sketch later if needed |
| 7 | `-stddev` default | sample (n−1), like DuckDB/Postgres `stddev`; `-pop` variants deferred |
| 8 | first/last in parallel | shard ordinal in Merge so the parallel answer equals the serial one on files |

## 9. References

- `doc/research/dfc115_commands_are_the_authority.md` — flags are facts other lanes can read; expressions are programs.
- `doc/research/multimode-equivalence-testing.md` — why every aggregate needs a case in every lane, with shuffled fixtures.
- `doc/research/expr-transpiler-implementation-plan.md` — Phase 3 (`-expr` lowering) and why only sum/len shapes are native.
- `doc/research/rollup-cube-design.md` — parent levels are merged from detail state; new aggregates must be mergeable for cube to stay one pass.
- `doc/research/typed-groupby-parallel-proposal.md` — the Merge phase the positional kind hooks into.
- `claude/concurrency.md` §12 — per-shard order is deterministic.
- `doc/research/dfc060` (`duckdb-vs-ssql.md`) — Postgres portability notes for the SQL lane.
