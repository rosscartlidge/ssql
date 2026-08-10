# Converging the Flag-Condition and Expression Lowerings

**Status:** Design proposal, 2026-08-10. Follow-up to the expr transpiler
(phases 1–4, see [expr-transpiler-paper.md](expr-transpiler-paper.md)) and
sibling to [rvalues-as-expressions.md](rvalues-as-expressions.md), which
approaches the same unification from the other side.

---

## 1. The question, and the answer this doc assumes

The transpiler made `-if-expr 'age > 25'` generate essentially the same
native code as `-if age gt 25`, which raises the question: are the
flag-based forms still worth having? **Yes** — but their value proposition
has shifted from *performance* (now matched) to three things the
expression form structurally cannot provide:

1. **The interactive surface.** `-if <TAB>` completes field names from the
   live pipeline, operators from a whitelist, and values from the actual
   data (`FieldValuesFrom`). An expression is an opaque string with a
   `<boolean-expression>` hint — no completer can see inside it. Flag
   forms also need zero shell quoting (`-if region ne south` vs
   `-if-expr 'region != "south"'`), which compounds over SSH — and the
   remote/pushdown machinery (`ShellQuote`, catalog `-if` pruning, `--`
   pushdown) consumes flag forms directly.
2. **The optimizer's IR.** Every `generate ssql` rewrite — range
   tightening, contradiction detection, predicate reorder, catalog
   predicate extraction, join pushdown — operates on structured
   `{Field, Op, Value}` conditions and *bails* on expressions. Same for
   `generate sql`: `-if` translates totally; `-if-expr` goes through the
   translator's loud-failure subset.
3. **Predictability.** `-if` has one emission per backend and never falls
   back; `-if-expr` carries the tier ladder (an exotic construct quietly
   costs 1.2 µs/row again). Flag conditions also PARAMETERIZE in record
   codegen — `-if pop gt 25` becomes a runtime `-pop-gt` flag on the
   generated program, adjustable without regeneration. Expressions inline
   their literals.

So both syntaxes stay. What should NOT stay is the thing this doc
proposes to fix: **the flag forms are lowered independently in five
places**, and that duplication is exactly where this project's worst bug
class lives.

## 2. The problem: five lowerings of one semantics

Current implementations of `FIELD OP VALUE` conditions (verified against
the code, 2026-08-10):

| site | file | backend | ops |
|---|---|---|---|
| `applyOperator` | helpers.go:163 | exec | all 10, incl. `regex` |
| `generateCondition` | where.go (~598–690) | record `where` codegen (+ CodeParam flags) | all 10 |
| `generateConditionCode` | update.go:784 | record `update` codegen | all 10 |
| `typedWhereCondition` | where.go (~380) | typed `where` AND `update` codegen | 9 — **`regex` is a Tier-3 error** |
| `translateWhere` | generate_sql.go:351 | SQL | structured translation |

Plus the `generate ssql` optimizer's `parseWhereArgs`/`buildWhereArgs`
round-trip (structured, shared with the SQL path via the Command string),
and the exec `update` closure's own condition evaluation.

This is the "one semantics, many backends" trap in miniature. The
`+if`/`+if-expr` negation bug of v4.55–v4.56 lived in SIX sites precisely
because each site re-decoded the same flag shapes; the duplicate
field+op flag-naming bug lived only in `generateCondition` because only
that copy parameterizes. History says: every future conditional-operator
change (a new operator, a coercion fix, a negation variant) must be made
N times and will be made N−1 times.

Meanwhile the transpiler now maintains the project's ONLY
differentially-verified, per-backend emission of comparison semantics —
with a 62-expression transpiler-vs-VM oracle behind it. The flag
lowerings duplicate a worse-tested version of what `exprToGo` already
does.

## 3. Proposal: converge the lowerings, keep the surfaces

**Flags remain the completable, quotable, optimizer-friendly syntax.
Expressions remain the general escape hatch. Underneath, ONE lowering
per backend — the transpiler's.**

Concretely: a flag condition `{Field, Op, Value, Negated}` gets a single
translation into the expression walker's world, and the per-backend
emissions (`typedWhereCondition`, `generateCondition`,
`generateConditionCode`) are deleted in favour of calls into `exprToGo`
machinery with the appropriate resolver (typed struct / record advisory
GetOr / `frozen` receiver).

```
                    ┌──────────────┐        ┌────────────────────────┐
  -if f gt v  ──────▶ condition IR ├──┬────▶│ exprToGo (one lowering │
  -if-expr '…' ─────▶ expr AST     ├──┘     │  per backend: typed /  │
                    └──────┬───────┘        │  record / update)      │
                           │ structured     └────────────────────────┘
                           ▼
              optimizer / SQL / pushdown (unchanged)
```

The structured representation is not weakened: the optimizer, SQL
translator, catalog/join pushdown, and schema rules keep consuming
`{Field, Op, Value}` exactly as today (the Command-string round-trip is
untouched). Only the *final emission step* changes.

## 4. What needs to be done

### Phase A — pin current behaviour before touching anything

> **✅ SHIPPED 2026-08-10 — and it found three real bugs on its first
> run**, vindicating the gates-first sequencing. `TestFlagExprMetamorphic`
> (equivalence_test.go): 19 pairs covering all 10 operators, negation,
> AND/OR clause composition, and update conditions; each pair asserts
> internal lane-consistency of BOTH pipelines plus exec(flag)==exec(expr).
> Found and fixed:
> 1. **Record `where` string ordering silently returned zero rows** —
>    `generateCondition` emitted gt/ge/lt/le numerically UNCONDITIONALLY,
>    so `-if city gt Lima` compared `float64(0) > 0` on every row (exec
>    compares lexicographically). Now branches on the advisory field type
>    when known (exactly exec's field-type branch), value-form heuristic
>    otherwise.
> 2. **Record `update` string ordering didn't even compile** — the same
>    unconditional-numeric emission in `generateConditionCode` produced
>    `float64(0) > "Lima"`. Same fix.
> 3. **Typed contains/startswith/endswith emitted `strings.*` without
>    importing `strings`** — generated programs failed to compile in both
>    typed lanes (where AND typed-update paths). Import now rides the
>    fragment.
> Watched failing: a sabotaged `ge`→`gt` emission was caught by the
> `ge_int` pair's go-record lane. Known capability gap encoded in the
> gate: `-if … regex` is a Tier-3 error in typed codegen (skip'd lanes) —
> the expression form is native, which is convergence unlock C.7.
>
> Residual divergences documented, deliberately NOT changed in Phase A:
> a string-typed field compared against a numeric-looking value takes the
> numeric branch in codegen without advisory types (exec branches on the
> runtime field type); an int64 field compared via `-if f gt 15.5` is
> silently false-for-every-row in exec (`ParseInt` fails) while
> `-if-expr 'f > 15.5'` compares numerically — these are audit-table
> items for Phase B's shared lowering to resolve, not quiet fixes.

1. **Semantic audit table.** For each operator × field type × backend,
   document today's behaviour: numeric coercion in `applyOperator` vs
   `GetOr(float64)` in record codegen vs `typedLiteral` in typed codegen
   vs SQL. Known suspects to check: `-if` on a string field with a
   numeric-looking value; `contains` on a non-string field; `regex` with
   an invalid pattern (loud vs silent-false, per backend); `eq` between
   int-typed field and float-formatted value.
2. **Metamorphic flag≡expr equivalence cases.** New axis in
   `TestPipelineEquivalence`: for every operator, `-if f OP v` and the
   equivalent `-if-expr` must produce identical output *in every lane*
   (`-if pop gt 15` ≡ `-if-expr 'pop > 15'`; `-if city contains x` ≡
   `-if-expr 'city contains "x"'`; …). These cases are valuable
   *immediately* — they pin the very equivalence convergence relies on,
   and any divergence they expose today is a latent bug worth fixing
   regardless. **Write these first and watch what fails.** Divergences
   found here need an explicit decision (fix vs document) before Phase B.

### Phase B — the shared lowering

3. **`condToExprGo(cond, resolver)`** in the transpiler: map each
   operator onto the walker's existing emissions (`gt` → the `>`
   comparison with §2 coercion rules; `contains`/`startswith`/`endswith`
   → the strings.* emissions; `regex` → the hoisted `matches`
   machinery). The value string types against the field's type exactly
   as `typedLiteral` does today (loud on `-if age gt banana`).
4. **A parameter-reference leaf.** Record codegen must keep emitting
   CodeParam-backed values (`ssql.ParseFloat64(*flagPopGt)`), not inline
   literals — that runtime-adjustable-flag behaviour is a real feature.
   The walker gains a leaf node kind "typed parameter reference" that
   `condToExprGo` uses in record mode; the existing collectParams
   machinery is unchanged.
5. **Replace call sites; delete the copies.** `typedWhereCondition`
   (where + typed_update), `generateCondition`, `generateConditionCode`
   all route through the shared lowering. Exec's `applyOperator` stays —
   it IS the oracle — but gets covered by the metamorphic gates.
6. **Hoisted-decl support in the record assembler** (prerequisite for
   `regex` convergence and a standing TODO anyway): give record
   fragments a package-level decl slot like typed StructDefs, so hoisted
   `regexp.MustCompile` vars work in record mode. Until then, `regex`
   keeps a bespoke emission or compiles per-call.

### Phase C — capability and cleanup wins the convergence unlocks

7. **`regex` in typed mode** stops being a Tier-3 error — it inherits
   the `matches` hoisted emission for free.
8. **Expression canonicalization (optional, high leverage):** recognize
   trivial `-if-expr` shapes (`field OP literal`, conjunctions thereof)
   at parse/optimize time and normalize them into structured conditions
   in the Command string — those expressions then inherit range
   tightening, catalog extraction, join pushdown, and total SQL
   translation. `-explain` notes the normalization.
9. **Docs:** EXPRESSIONS.md and the codelab state the real division of
   labour: flags = completable/quotable/optimizable subset, expressions
   = general form, identical semantics guaranteed by the metamorphic
   gates.

### Gates (per project rules)

- The Phase-A metamorphic cases, watched failing wherever behaviour
  diverges today.
- After Phase B: byte-identical generated source (or at minimum
  byte-identical normalized *output* across the full equivalence +
  permutation + corpus suites) for every existing flag-condition
  pipeline; sabotage one operator's shared emission and confirm the
  metamorphic gate catches it.
- `TestNegatedConditionGeneration` and the dup-field+op cases must stay
  green — those histories are the reason this refactor exists.

## 5. Risks and non-goals

- **Semantic drift during replacement** is the main risk — mitigated by
  doing Phase A first; the metamorphic gates make silent drift loud.
- **`applyOperator` (exec) is deliberately NOT converged** in this
  proposal: exec is the reference oracle, and rewriting the oracle and
  its subject simultaneously destroys the differential methodology.
  (A later phase could generate exec's evaluator from the same tables,
  but only after the gates have soaked.)
- **Completion, help text, and the flag surface are untouched** — this
  is an internals refactor; users see identical syntax, plus `regex` in
  typed mode and (with C.8) faster SQL/optimizer coverage for simple
  expressions.
- **Not a step toward removing flags.** The paper's §7 framing applies:
  the flag syntax is the *curated, analyzable subset* and that subset is
  the optimizer's food. Convergence strengthens the case for keeping
  both surfaces by deleting their duplicated guts.

## 6. Effort estimate

Phase A: ~half a day (the audit is mostly test-writing; expect it to
find something). Phase B: 1–2 days (the parameter-leaf and record
hoisted-decl slots are the only genuinely new machinery). Phase C: a day
including docs. All independently shippable, gates green throughout —
the same increment discipline as transpiler phases 1–4.
