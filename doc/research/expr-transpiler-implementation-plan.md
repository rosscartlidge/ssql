# Expr→Go transpiler: implementation plan

**Status:** Detailed implementation plan, 2026-07-03. No code written yet.
This is the concrete follow-up to
[expr-codegen-transpilation.md](expr-codegen-transpilation.md) (the design
exploration, 2026-06-25): that doc argues *why* and sketches *what*; this doc
pins down *exactly how* — semantics (empirically probed), file-level changes,
API shapes, the fallback ladder, phasing, and the test gates.

Everything in §2 and §3 was verified against the actual codebase and against
expr-lang v1.17.6 with probe programs on 2026-07-03 — none of it is assumed.

---

## 1. Executive summary

Build `exprToGo`, an expr-lang AST → Go source transpiler living next to
`exprToSQL` (`generate_sql_expr.go`, shipped v4.56.0 — the AST-walk shape is
now proven on a *more* alien backend). Use it to replace the four typed-mode
Tier-3 hard-errors and the record-mode VM embeddings with native Go, falling
back — per expression, at codegen time — through a three-tier ladder:

    native inline Go  →  VM with a generated (static) env  →  whole-stage Record fallback

The headline wins, in order of value:
1. Expression pipelines stop being ejected from the typed/parallel path
   (today `update -set-expr` *hard-errors* in typed mode; `where -if-expr`
   silently downgrades the whole downstream pipeline to Record).
2. The per-row cost drops from map-alloc + field-copy + 2 closure allocs +
   VM dispatch + interface boxing to plain Go (`r.Price > 15`): zero
   allocations for the curated subset.
3. Three latent correctness bugs found during this investigation get fixed
   as part of the work (§9).

---

## 2. Ground truth: expr-lang semantics the transpiler MUST reproduce

Probed against expr-lang v1.17.6 (the pinned version), both with typed envs
and with the dynamic (`map[string]any`) env shape ssql actually uses.
**These are the divergence traps; each row is a differential-test case.**

### 2a. Arithmetic and numeric promotion

| expr | result | transpiled Go emission |
|---|---|---|
| `int / int` | **always float64** — `7/2 = 3.5` | `float64(a) / float64(b)` — NEVER Go integer division |
| `int / 0` | `+Inf` (float div-by-zero, no panic) | float division gives this for free |
| `int % int` | int | `a % b` (int64); float operand → fallback |
| `int + int`, `-`, `*` | int (Go-width, **wraps on overflow** like Go) | `a + b` on int64 — Go wrap matches |
| `int OP float` | float64 | coerce the int side: `float64(a) OP b` |
| `**`, `^` | **always float64** (both are power) | `math.Pow(float64(a), float64(b))` |
| `-i` (unary) | preserves type | `-a` |
| `string + string` | concat | `a + b` |
| `string + int` | **runtime error** | codegen-time error (see divergence policy §6) |

### 2b. Comparison and equality

| expr | result | emission |
|---|---|---|
| `int OP float` (`< <= > >=`) | numeric compare | coerce int side to float64 |
| `int == float` | numeric (`7 == 7.0` → true) | `float64(a) == b` |
| `string OP string` | lexicographic | Go string compare |
| `string == int` (dynamic env) | **false, silently** (no error) | codegen error — see §6 |
| `string > int` (dynamic env) | runtime error | codegen error |
| `1 < 2 < 3` | **chained comparisons are legal** → true | lower to `(1 < 2) && (2 < 3)`; verify the AST shape when implementing — if it's not a recognizable pattern, fallback |
| `time < time` / `==` | works (Before/Equal) | MVP: time-typed fields → fallback; Phase 5 may lower to `.Before/.Equal` |

### 2c. Missing fields, nil, and the helpers

Typed mode changes the picture fundamentally: **struct fields always exist**.
CSV-sourced records have every column on every row in record mode too, so
these matter mainly for JSONL-sourced sparse data — which is record-mode
territory anyway.

| expr | VM behaviour | typed emission |
|---|---|---|
| `missing` (identifier not in env) | `nil` | unknown identifier vs schema → **codegen error** (like `where -if` unknown-field validation; better than the VM's first-record check) |
| `missing > 5`, `missing + 1` | **runtime error** per row | n/a (caught at codegen) |
| `x ?? d` | `d` iff x nil | field ref is never nil in typed mode → emit the field; non-field LHS → fallback |
| `has("f")` | env lookup | constant `true`/`false` against the schema |
| `getOr("f", d)` | value or default | the field itself (type-checked against `d`) |

### 2d. Built-ins (probed result types — these drive type inference)

| call | result type | emission |
|---|---|---|
| `abs(int)` / `abs(float)` | **type-preserving** (int→int, float→float) | generic helper `exprfn.Abs[T]` |
| `round(f)` | float64, **half away from zero** (`round(2.5)=3`, `round(-2.5)=-3`) | `math.Round` (identical semantics — verified) |
| `floor`, `ceil` | float64 | `math.Floor` / `math.Ceil` |
| `min(a,b)` / `max(a,b)` | type-preserving; **mixed int/float returns the WINNER with its own type** (`min(-3, -2.5)` is int64 `-3` — NOT promoted; corrected 2026-08-09, caught by the differential harness on its first run) | Go built-in `min`/`max` for same-type args; mixed int/float → **refuses** (no static Go type expresses "winner's type") |
| `int(f)` | truncates toward zero; `int("12")` parses; `int("12.7")` **runtime error** | `int64(f)` for numeric; string→int → helper or fallback (MVP: fallback) |
| `float(i)`, `string(i)` | float64 / decimal string | `float64(i)` / `strconv.FormatInt` |
| `len(s)` | **rune count**, not bytes (`len("héllo") = 5`) | `int64(utf8.RuneCountInString(s))` |
| `upper`, `lower`, `trim` | unicode-correct | `strings.ToUpper/ToLower/TrimSpace` |
| `contains`/`startsWith`/`endsWith` (operators) | bool | `strings.Contains/HasPrefix/HasSuffix` |
| `hasPrefix`/`hasSuffix` (call forms) | bool | same. NB `contains(a,b)` call form is a parse error — operator only (fixed in docs v4.56.0) |
| `matches` | bool, RE2 | pattern literal → hoisted `regexp.MustCompile` package var; dynamic pattern → fallback |
| `x in [a, b]` | bool | `(x == a || x == b)` for literal lists; non-literal → fallback |
| `a ? b : c` | branches keep their own types (`b ? 1 : 2.5` → int) | inline `func() T { if a { return b }; return c }()`; branch types unified (both-numeric → float64), else fallback |
| `s \| upper()` (pipe) | sugar for `upper(s)` | parser yields a CallNode — handle transparently, else fallback |

### 2e. Current VM behaviours that are part of the observable contract

- **`where -if-expr`**: `MustCompileExprFilter` returns **false** on any eval
  error and on non-bool results (runtime.go:51-58). The exec path prints an
  error to stderr per row and also excludes.
- **`update -set-expr`**: eval error → field set to `""` (update.go:619-652);
  result typed via a runtime type-switch (int64/float64/bool/string/int/
  float32, default → `fmt.Sprintf("%v")`).
- **`runtime.CompileExpr`** validates unknown identifiers on the *first
  record only*, and skips validation entirely if the expression text contains
  `??`, `has(` or `getOr(` (runtime.go:94-96).
- **`group-by -expr`**: result coerced through `toFloat64` (expr_agg.go:120)
  — ints and floats become float64; **anything else silently becomes 0**.
- **`group-by -stream-expr`**: 4-phase map-state fold: init must return an
  object; every runs with env = state ∪ record fields and must return an
  object; final evaluates over the state map.

---

## 3. Current emission sites (what gets replaced)

From the code inventory (verbatim refs, 2026-07-03):

| site | today | plan |
|---|---|---|
| `where.go:518-553` record `-if-expr` | emits `var exprFilterN = runtime.MustCompileExprFilter("…")` + `exprFilterN(r)` in the predicate | Phase 4: emit transpiled Go predicate; VM var only on fallback |
| `where.go:243` typed guard | `!whereHasExpr(ctx.Clauses)` → any `-if-expr` silently forces WHOLE-stage Record fallback (planner inserts Serial + toRecord) | Phase 1: transpile into the existing dual-template `Stream.Where`/`typed.Where` emission (where.go:379-392) |
| `typed_update.go:44-55` | hard Tier-3 errors for `-if-expr`/`-set-expr` | Phase 1: transpile into the (new, v4.56.0) dual-template StreamSelect/Select closure; fallback ladder replaces the error |
| `update.go:592-652` record `-if-expr`/`-set-expr` | `MustCompileExprFilter` guard + `MustCompileExpr` with runtime type-switch | Phase 4: native assignment `mut.Int/Float/Bool/String(field, <expr>)` with the type known at codegen |
| `group_by.go:470-472` typed | hard Tier-3 error for `-expr`/`-stream-expr` | Phases 2-3: typed accumulator lowering |
| `group_by.go:665-681` record | `ssql.ExprAgg("…")` / `ssql.StreamExprAgg(…)` in the Aggregate map | Phase 4 (optional): record-mode accumulator; low priority — the VM agg runs once per *group element*, not per output row, but is still the biggest interpreter loop |

Reusable machinery already in place:
- `exprToSQL`'s walker structure (`generate_sql_expr.go`) — same node dispatch,
  different renderer. **Do not merge them**: SQL and Go disagree on too much
  (result typing, error strategy), but keep the same file/test layout.
- `lookupSchemaField` (case-insensitive schema lookup), `typedLiteral`,
  `schemaUsesTime` (drives `time` import), `uniqueVarName`.
- The dual-template fragment fields (`Code`/`AltCodeIfSeq`/`Capabilities`/
  `AltCapabilitiesIfSeq`/`IsStream`) and the planner's reach analysis —
  update just moved onto this (v4.56.0), so every Phase-1 site has a
  ready-made parallel emission slot.
- The aggregation patcher (`expr_agg.go:212-346`) — normalizes
  `sum(salary*bonus)` → `sum(_records, .salary*.bonus)`, `count()` →
  `len(_records)`, `avg(e)` → `sum(...)/len(...)`. Phase 3 walks the SAME
  normalized shape to find accumulator terms.
- The planner boundary machinery (`planner.go:109-131`,
  `buildToRecordBoundary` at `codefragment_typed.go:692-731`) — the cost of a
  whole-stage fallback is `Stream.Serial()` + one struct→Record per row.

---

## 4. Architecture

### 4a. Package placement

`cmd/ssql/commands/expr_go.go` + `expr_go_test.go`, package `commands` —
exactly like `generate_sql_expr.go`. Rationale: the emitters it serves and
the helpers it reuses (`lookupSchemaField`, `uniqueVarName`, TypedSchema
plumbing) are all in this package; extraction into `cmd/ssql/lib/exprgen`
is mechanical later if another consumer appears. (Resolves open question #1
of the design doc.)

**Plus one new tiny runtime package**: `github.com/rosscartlidge/ssql/v4/exprfn`
— generic helpers the generated code calls where inlining would be ugly:

```go
package exprfn // zero-dependency, generic, all inlinable

func Abs[T int64 | float64](v T) T
func Min[T cmp.Ordered](a, b T) T          // Go 1.21 built-in min/max may suffice — decide at impl time
func Max[T cmp.Ordered](a, b T) T
func RuneLen(s string) int64               // int64(utf8.RuneCountInString(s))
func IntFromString(s string) (int64, bool) // Phase 5, if string→int casts leave fallback
```

This is the "middle tier" of design-doc open question #3, kept deliberately
tiny: only functions whose inline expansion would hurt readability. Everything
else (strings.*, math.*) is called directly. Generated code stays readable —
a hard CLAUDE.md requirement.

### 4b. Core API

```go
// exprGoType is the transpiler's type lattice.
type exprGoType string // "int64" | "float64" | "string" | "bool" | ("time.Time" Phase 5)

type exprGo struct {
    Src     string     // Go expression source, parenthesized as needed
    Type    exprGoType
    Imports []string   // e.g. "strings", "math", ".../exprfn"
    Hoisted []string   // package-level decls (e.g. regexp vars), deduped by the assembler
}

// exprToGo transpiles an expression against a typed schema.
// A non-nil error means "cannot transpile natively" — the CALLER decides
// which fallback tier applies; the error text names the construct (same
// loud-and-specific style as exprToSQL).
func exprToGo(expression string, schema *lib.TypedSchema) (exprGo, error)

// exprToGoBool is exprToGo + "result must be bool" (for -if-expr).
func exprToGoBool(expression string, schema *lib.TypedSchema) (exprGo, error)
```

Internals mirror `exprNodeToSQL`: `parser.Parse` → recursive
`exprNodeToGo(node, env) (exprGo, error)` with a type-checking step at each
binary/call node. Every subexpression returns `(src, type)`; parent nodes
insert coercions per the §2 tables. Parenthesize every binary emission
(`(a + b)`) — proven cheap and safe in exprToSQL.

**The field env.** `map[lowercase field name] → TypedSchemaField` built once
from the schema. Identifier resolution: field hit → `r.<GoName>` with the
field's `GoType` (only int64/float64/string/bool admitted in MVP; `time.Time`,
pointers, sequences → error → fallback). Identifier miss → codegen error
naming the field and the available fields (matches the `where -if`
fail-loudly convention).

### 4c. The fallback ladder (per expression, decided at codegen)

**Tier N — native.** `exprToGo` succeeds → inline into the stage closure.
Stage keeps `Stream[T]` shape, dual templates, parallel-safe.

**Tier V — VM with a generated static env.** `exprToGo` fails but we are in
typed mode: emit the *existing* VM call with an env built **statically from
the struct** instead of from a Record:

```go
var exprEval1 = runtime.MustCompileExprEnv("<expr>")   // new: env-map variant
...
env := map[string]any{"pop": r.Pop, "city": r.City, ...} // generated per schema
v, err := exprEval1(env)
```

This still pays the map alloc + VM dispatch, but the stage **stays typed**
(`Seq[T]→Seq[T]` or even `Stream[T]` via StreamSelect/Where) — so one exotic
expression no longer drags the *rest of the pipeline* through a
Serial()+toRecord boundary into Record mode. Requires one small runtime
addition: `runtime.CompileExprEnv(expr) (func(map[string]any) (any, error), error)`
— the existing `CompileExpr` minus the Record→env copy (the has/getOr
closures are installed the same way). For `-set-expr` under Tier V the result
still needs a type: require the *declared* field type (existing field) or
infer-from-expression (new field); mismatch at runtime → the documented
error semantics (§6).

**Tier R — whole-stage Record fallback.** Not typed mode, or Tier V can't
apply (e.g. group-by aggregation state): today's behaviour, kept as the
floor. In typed pipelines the planner already handles this shape.

`generate go -explain` reports the tier per expression:
`[plan]   expr "price * qty > 1000": native` /
`… : VM (function "fromJSON" not transpilable)` /
`… : record fallback`. (Resolves design-doc open question #2: yes.)

---

## 5. Per-site lowering (exact shapes)

### 5a. `where -if-expr` (typed) — Phase 1

Remove the `!whereHasExpr` guard (where.go:243). In
`generateWhereCodeTyped`, per clause: `-if` conds render via
`typedWhereCondition` (unchanged); `-if-expr` conds render via
`exprToGoBool`. AND within clause, OR across clauses, `+if`/`+if-expr` → `!(…)`
— note the record codegen ignores negation today (§9 bug 1); the typed path
must get it right from day one and the record path gets fixed in Phase 4.
Emission slots into the existing dual templates (where.go:379-392):

```go
filtered := input.Where(func(r ShufRelRow) bool { return (r.Pop > 15) && !(r.City == "Oslo") })
// Alt (Seq): typed.Where(func(r ShufRelRow) bool { ... })(input)
```

### 5b. `update -set-expr` / `-if-expr` (typed) — Phase 1

`emitTypedUpdate` (typed_update.go) already: builds a derived schema for new
fields, first-match-wins clause chains, and (since v4.56.0) dual
StreamSelect/Select templates. Changes:
- `-if-expr` in a clause → `exprToGoBool` result joins the `&&` chain.
- `-set-expr FIELD EXPR` → `exprToGo`; assignment `out.<GoName> = <src>`.
  - Existing field: inferred type must equal the field's GoType, else one
    explicit numeric coercion (int64↔float64) is inserted; other mismatches
    (e.g. `-set-expr price 'string(price)'` changing a column's type) →
    **derived schema with the new type** — the machinery exists (update
    already emits derived structs); if it complicates Phase 1, fallback
    Tier V and log it in -explain.
  - New field: field's GoType = inferred type (replaces the literal-only
    `inferLiteralGoType`).

### 5c. `group-by -stream-expr` (typed) — Phase 2 (do this BEFORE -expr)

It is literally a fold, and the state shape is declared by the user:
`-stream-expr '{s:0, n:0}' '{s:s+salary, n:n+1}' 's/n' avg_salary`.

Lowering:
- init `{s:0, n:0}` (MapNode of PairNodes; values must be literals or
  literal arithmetic) → accumulator struct fields with inferred types
  (`s float64`/`int64` per §2a rules; every-expr may widen int→float64 —
  run inference over init AND every, then unify).
- every `{s: s+salary, n: n+1}` → per-row assignments inside the typed
  aggregator's `Add(r T)`: `a.s = a.s + float64(r.Salary); a.n = a.n + 1`.
  Env for identifier resolution = state fields ∪ schema fields (state wins,
  matching `evalStreamAggExpr`'s `maps.Copy` order — VERIFY order at impl).
- final `'s/n'` → the aggregator's `Result()` return, over state fields only.
- Result type: the final expression's inferred type — BUT the VM path coerces
  through `toFloat64` (returning 0 for non-numerics!). Match the VM: numeric
  result → float64; non-numeric → codegen error (§6 divergence: loud beats
  silently-0).

The typed group-by codegen already generates aggregator structs
(`<T>Aggregator` with Add/Result) for -count/-sum/…; -stream-expr adds
fields + statements to that same struct. Parallel form: GroupByParallel
merges per-shard aggregators — **stream-expr state is NOT generally
mergeable** (the fold isn't necessarily associative), so -stream-expr forces
the serial GroupBy alternative (planner handles via SerialOnly on the
fragment). Document this; -expr aggregations (sum/count/avg) ARE mergeable
and keep GroupByParallel in Phase 3.

### 5d. `group-by -expr` (typed) — Phase 3

Reuse the patcher's normal form. Walk the expression with the SAME rewrite
rules as `aggPatcher` (or literally run `parser.Parse` + the patcher, then
walk the patched tree): the tree becomes ordinary arithmetic over three
recognizable node shapes — `sum(_records, .field-expr)`, `len(_records)`,
and their `avg` composition. Lowering:
- each distinct `sum(_records, <elemExpr>)` term → one accumulator field
  `accN float64` + per-row `accN += <transpiled elemExpr over r>` (elemExpr
  transpiles with the ordinary Phase-1 walker; `#.salary` MemberNodes resolve
  like identifiers);
- `len(_records)` → shared `cnt int64` + `cnt++`;
- the outer expression (e.g. `sum(a)/count()` → `acc1 / float64(cnt)`)
  becomes `Result()`.
- Result coerced to float64 (VM parity via toFloat64 — same divergence rule
  as 5c for non-numerics).
- Mergeable across shards (sums and counts add) → keeps GroupByParallel;
  emit the merge method accordingly.

### 5e. Record mode native (Phase 4, optional but cheap after Phase 1)

Same walker, different identifier emission: field type is UNKNOWN, so run
**whole-expression inference first** (literals + operators + function
signatures pin most types: `price * qty > 1000` forces numeric), then emit
`ssql.GetOr(r, "price", float64(0))`. Any field whose type stays ambiguous →
Tier V (which in record mode is just today's VM path — zero regression).
This kills the per-row env map for the common cases in record mode too, and
is where the `+if-expr` negation bug (§9) gets fixed.

---

## 6. Divergence policy (transpiled vs VM) — decide once, document, test

The VM's error semantics are per-row and silent-ish; the transpiler moves
errors to codegen time where possible. Policy table (to be included in the
user docs and enforced by tests):

| situation | VM today | transpiled | rationale |
|---|---|---|---|
| unknown field in expr | first-record stderr error (skipped if `??`/`has(`/`getOr(` present) | codegen error listing fields | CLAUDE.md fail-loudly; strictly earlier |
| statically type-mismatched op (`s + 1`, `s > 5`) | per-row runtime error → row filtered / field `""` | codegen error | a predicate that errors on EVERY row is a bug, not a semantic |
| cross-type `==` (`s == 7`) | silently false | codegen error | silently-false comparisons hide typos; loud wins |
| non-bool `-if-expr` result | rows all filtered false (+ stderr in exec) | codegen error | same |
| `-expr` aggregation result non-numeric | silently 0 (toFloat64) | codegen error | silent 0 is data corruption |
| eval error mid-row (only possible in Tier V) | unchanged | unchanged | fallback preserves today's behaviour exactly |

Each divergence is a *strictening* (silent wrong → loud early), consistent
with the project's fail-loudly rule. The differential harness (§7) asserts
value-equality only for expressions that are valid under BOTH paths.

---

## 7. Test plan (the gates, in the order they get built)

1. **Unit: `expr_go_test.go`** — table-driven, mirroring `TestExprToSQL`:
   exact emitted-source assertions for every §2 row (division→float!,
   `**`→math.Pow, rune len, ternary closure, chained comparisons, has/getOr
   constant-folding, coercion insertions) + loud-error cases naming the
   construct.
2. **Differential expr harness** (the §7-of-design-doc gate, made concrete):
   a generator test that takes a corpus of (expression, rows) pairs, emits
   ONE Go file containing, per expression, the transpiled closure and a
   `runtime.CompileExpr` call, runs both over the rows, and asserts equal
   results — the same build-and-run pattern as `corpus_test.go` (single
   `go build` for the whole corpus, not per case). This is what catches
   coercion drift. Seed corpus: every §2 probe row + the doc/EXPRESSIONS.md
   examples that fall in the subset.
3. **Equivalence lanes**: the existing `where_expr` and `update_set_expr`
   cases currently skip go-typed/go-parallel (Tier-3 errors) — **unskipping
   them is the acceptance test for Phase 1**. Add `groupby_expr` and
   `groupby_stream_expr` cases (5 lanes each; the duckdb lane exercises
   exprToSQL against exprToGo — two independent translations of the same
   expression, which is a free extra oracle).
4. **Permutation gate**: add a `where -if-expr 'pop > 5 && city != "Hanoi"'`
   stage to `TestPipelinePermutations`' stage set once Phase 1 lands (watch
   for the update-ties caveat noted in TODO).
5. **Benchmarks** (`expr_bench_test.go` in the generated-program style):
   `BenchmarkIfExprVM` vs `BenchmarkIfExprNative` (same predicate, 1M rows),
   `-benchmem` asserting **0 allocs/op** for the native predicate; same pair
   for `-set-expr` and `-expr` aggregation. Record the numbers in this doc's
   follow-up (claimed ~1-2µs+allocs vs ~ns — measure, don't assert).
6. **Watch it fail**: after the differential harness is green, temporarily
   emit Go integer division for `/` — the harness MUST fail on `7/2`. (The
   single most likely real-world regression; prove the gate sees it.)
7. **-race** on anything touching typed parallel forms (CI rule already).

---

## 8. Phasing (each phase independently shippable, gates green throughout)

| phase | scope | acceptance | est. |
|---|---|---|---|
| **1** ✅ 2026-08-09 | `exprToGo` core + typed `where -if-expr` + typed `update -set/-if-expr` (Tier N + Tier R; Tier V machinery deferred if tight) | equivalence cases unskip typed lanes; differential harness green; benches show 0 allocs | 2-3 days |
| **1.5** ✅ 2026-08-10 | Tier V (`runtime.CompileExprEnv` + generated static env) + `-explain` tier reporting | exotic expr in typed pipeline no longer downgrades downstream stages; -explain names tiers | 1 day |
| **2** ✅ 2026-08-10 | `group-by -stream-expr` → typed accumulator (serial GroupBy) | new equivalence case green across lanes | 1-2 days |
| **3** ✅ 2026-08-10 | `group-by -expr` via patcher normal form → mergeable accumulators (keeps GroupByParallel) | equivalence + a parallel-vs-serial multiset test | 1-2 days |
| **4** | record-mode native (where/update) + fix the `+if-expr` negation bug + retire the per-row type-switch | record lanes byte-identical; negation regression test | 1-2 days |
| **5** | breadth: time.Time lowering, string→int casts, `in` on fields, split/join/replace/replaceRegex/sha256 via exprfn; grow from `-explain` fallback telemetry | incremental | ongoing |

Sequencing rationale: Phase 1 unlocks the parallel path (the performance
story); 2 before 3 because -stream-expr is structurally simpler (explicit
state) despite looking scarier; 4 is deliberately after the typed path has
soaked because record-mode inference is heuristic.

**Phase 1 shipped (2026-08-09).** What landed vs the plan:
- `expr_go.go` (`exprToGo`/`exprToGoBool`) + `exprfn` (Abs, RuneLen only —
  Go's built-in `min`/`max` cover the same-type cases; mixed-type min/max
  refuses, see the corrected §2d row). Unknown fields are a typed error
  (`exprUnknownFieldError`, loud); everything else refuses → Tier R.
- Chained comparisons need NO lowering — the parser desugars `1 < x < 3` to
  `&&` of two BinaryNodes (settled §10 question). Pipe syntax desugars too.
- Hoisted `matches` regexps ride the fragment's StructDefs (content-addressed
  names, deduped by the assembler).
- `-set-expr` into an existing column: only the value-preserving int64→float64
  coercion is inserted. A float result into an int64 column is a RETYPE in
  exec (pop/2 makes pop 3.5) — int64() truncation would silently diverge, so
  it falls back to record mode. Same for retypes and cross-clause new-field
  type conflicts.
- Measured (TestExprGoNativeZeroAllocs, 1M rows): predicate 4ns native vs
  1.50µs VM (~375x), assignment 3ns vs 1.31µs (~435x), both 0 allocs/op.
- Gates: TestExprToGo/TestExprToGoErrors (§7.1), TestExprGoDifferential
  (§7.2 — caught the min/max mixed-type promotion assumption on its FIRST
  run, and the §7.6 integer-division sabotage was watched failing),
  equivalence cases update_set_expr / update_if_expr_only unskipped +
  update_set_expr_division / update_set_expr_ternary added (§7.3), whereexpr
  permutation stage (§7.4), zero-alloc bench (§7.5).

**Phase 1.5 shipped (2026-08-10).** What landed vs the plan:
- `runtime/env.go`: `CompileExprEnv`/`CompileExprFilterEnv` (+ Must forms) —
  CompileExpr minus the Record→env copy; has/getOr close over the FIELD map
  (so `has("sha256")` is false, helper names aren't fields); no identifier
  validation (the schema was validated statically at codegen).
- Emission (`expr_go.go` Tier-V helpers): a per-schema env constructor
  (`exprEnv<TypeName>`, content-addressed → assembler-deduped) + hoisted
  compiled-VM vars (`exprFilterEnv<hash>`/`exprEvalEnv<hash>`). where and
  update -if-expr go native-else-TierV; -set-expr goes Tier V only for
  EXISTING columns with a `MustCoerce*` typing (loud exit on would-be
  retype — record mode retypes, typed columns can't; §10's open question
  resolved as predicted: new-field-from-untranspilable forces Tier R).
- Codegen validates the expression compiles in the VM before emitting Tier V
  (`exprTierVValidate`) — an expression invalid in EVERY mode errors at
  codegen, not at generated-program startup.
- `-explain` per-expression tier lines ride a new `CodeFragment.PlanNotes`
  field, printed by AssembleCodeFragments under SSQL_EXPLAIN_PLAN — works
  for the record assembler too (record-fallback reasons).
- Gotcha found: parallel programs import Go's stdlib `runtime`
  (GOMAXPROCS), colliding with `…/cmd/ssql/lib/runtime` — both assemblers'
  import renderers now accept "alias path" entries; Tier V imports as
  `exprvm`.
- Pre-existing limitation surfaced: `generate go -run` pins the PUBLISHED
  module version, so new runtime APIs (exprfn, env.go) only work with -run
  after the next release. Test harnesses use replace directives; unaffected.
- Gates: TestTierVKeepsTypedPipeline (parallel group-by SURVIVES a Tier-V
  where; record markers asserted absent; -explain names tiers; fallback
  note asserted), equivalence where_expr_tier_v (sha256 golden, 5/12 cities,
  duckdb lane skipped by design) + update_set_expr_tier_v, runtime env unit
  tests.

**Phase 2 shipped (2026-08-10).** What landed vs §5c:
- `lowerStreamAgg` (`expr_stream.go`): init MapNode → typed state fields
  (`se<i>_<key>` on the shared aggregator struct, INIT values in the
  aggregator constructor — `&T{}` zero value is wrong for `{s:1}`); every →
  ONE simultaneous multi-assignment in Add() (the VM computes the whole new
  state from the OLD state; `{a: b, b: a}` must swap — the
  `groupby_stream_swap` equivalence golden was watched failing against
  sequential assignment, both typed lanes); final → Result() expression,
  float64-coerced (mustAggFloat64 parity, non-numeric → refusal).
- **§5c's shadowing note corrected**: the env build is maps.Copy(state) THEN
  maps.Insert(record) — RECORD shadows state, not the reverse. The walker's
  new `vars` bindings resolve after schema fields accordingly (and has/getOr/
  ?? see them via a unified known()).
- Type inference runs to a widening fixpoint over init ∪ every (only
  int64→float64 exists, terminates in ≤ len(states) rounds).
- Every's key set must EQUAL init's — the VM legitimately reshapes the state
  object (dropped/added keys); a struct can't → record fallback with the
  reason under -explain. Same for non-literal init and non-numeric
  state/final. Unknown record fields stay loud.
- As planned, -stream-expr forces the serial single-template emission
  (SerialOnly; -presorted still selects GroupByOrdered) — fold state is not
  mergeable. Mixing with built-in aggregations in one aggregator works.
- Gates: TestLowerStreamAgg (lowering table + refusals + shadowing),
  TestStreamExprTypedGeneration (emission pinned incl. the multi-assign and
  serial form; fallback note), equivalence groupby_stream_{avg,widening,
  swap,grouped} with goldens (duckdb lane skipped: generate sql rejects
  -stream-expr loudly, v4.56.0 behaviour).

**Phase 3 shipped (2026-08-10).** What landed vs §5d:
- The lowering does NOT re-parse: `ssql.CompileAggExprPatched` runs exec's
  own compile (same env dummies, same patcher) and hands back the patched
  tree from `program.Node()`. This matters: bare `count()` is a PARSE error
  under `parser.Parse` — the arity-checked builtin — and only parses in exec
  because a dummy env function shadows it. First attempt re-parsed and every
  `count()` silently fell back; exec's own compile is the only faithful
  entry point.
- `lowerExprAgg` (`expr_agg_lower.go`): each distinct sum(_records, elem)
  becomes a `+=` accumulator (deduped by transpiled element source, KEEPING
  the element's own type — sum of ints is int in the VM, and `sum(pop) % 5`
  must stay integer modulo; §5d's blanket `accN float64` was wrong),
  len(_records) a shared counter, the outer expression Result() over
  placeholder identifiers substituted into the tree (walked with the vars
  bindings), float64-coerced. `#.field` MemberNodes resolve like bare
  identifiers in the walker.
- Merge adds terms and counts → dual templates KEPT (GroupByParallel), the
  whole point of 3-after-2. Watched failing: sabotaged the Merge and the
  go-parallel lane diverged on the avg golden.
- Fallback subtlety found: a field OUTSIDE an aggregation is legal in exec —
  the batch env binds it to the group's value ARRAY (`sum(salary)/
  len(salary)`: len of array = group size). No typed lowering → REFUSAL
  (record fallback preserves it), deliberately not the loud unknown-field
  path. VM-compile failures (typos inside sums) ARE loud (exprLoudError).
- Gates: TestLowerExprAgg (terms/dedup/int-fidelity/refusal-vs-loud),
  TestExprAggTypedGeneration (emission + parallel-kept + fallback note),
  equivalence groupby_expr_{avg,arith,int_mod,grouped} goldens (duckdb
  skipped: generate sql rejects -expr loudly).

---

## 9. Bugs found during this investigation — ALL FIXED 2026-07-04 (pre-Phase-1)

The fix sweep found the class was wider than the dig's headline. What
shipped (see the four `*negated*`/`update_if_expr_only` equivalence cases,
all watched failing first — notably the duckdb lane passed every one, since
the v4.56 SQL translator already handled negation):

1. **`+if`/`+if-expr` negation** was honoured only by where's exec path and
   generate sql. Fixed in: where record codegen (`+if` applied UN-negated,
   `+if-expr` dropped entirely), where typed codegen (`+if` un-negated),
   update exec (`+if-expr` dropped), update record codegen (both), update
   typed codegen (`+if` un-negated). Root cause was one shape repeated five
   times: negated single-arg flags arrive as `{"expression":…, "_negated":true}`
   maps and readers type-asserted only the string form → shared
   `parseExprConds` helper now used everywhere.

   **Addendum (2026-08-09):** the sweep had a SIXTH site — `generate ssql`'s
   optimiser round-trip (`parseWhereArgs`/`buildWhereArgs`) didn't recognise
   the `+` forms, so every rewrite rule that rebuilt a where (simplification,
   reorder, catalog extraction, join pushdown) silently dropped
   `+if`/`+if-expr` from the optimised pipeline. Fixed by porting the
   parallel web-session implementation of this sweep (which had caught it):
   negation round-trips, stays opaque to eq/range simplification, and is
   never lifted into catalog pruning filters. Gates: equivalence cases
   `where_negated_survives_simplify` / `where_negated_expr_survives_reorder`
   (watched failing in the ssql-opt lane) + `generate ssql` subtests in
   `TestNegatedConditionGeneration`.
2. **`update -if-expr … -set …` with no `-if`** generated an UNCONDITIONAL
   update (the `-if-expr` parse was gated on `-if` being present) — and
   fixing it exposed a never-closed `if` block for expr-only clauses in the
   emitted code. Both fixed.
3. **`update -set-expr` eval-error → `""`**: generated code now fails the
   pipeline loudly (stderr + exit 1), matching exec.
4. **`toFloat64` default → 0**: `ExprAgg`/`StreamExprAgg` now panic with a
   clear message on non-numeric results (`mustAggFloat64`), consistent with
   their existing compile/eval-error panics. The transpiled path will error
   at codegen instead (§6).

---

## 10. Resolved / remaining questions

**Resolved** (from the design doc's §10):
- *Where does the transpiler live?* → `cmd/ssql/commands/expr_go.go` +
  `exprfn` runtime package (§4a).
- *-explain visibility?* → yes, per-expression tier lines (§4c).
- *Runtime helper middle tier?* → yes, twice: tiny `exprfn` generics for
  native emission, and Tier V (VM-with-static-env) as the true middle tier —
  it's what keeps the *pipeline* typed even when one expression isn't
  transpilable (§4c).
- *Reuse the aggregation patcher?* → yes: transpile group-by `-expr` from
  the patcher's normal form rather than re-deriving agg recognition (§5d).

**Remaining** (to settle during Phase 1):
- Chained-comparison AST shape (`1 < 2 < 3`): find what the parser yields;
  lower or fallback (§2b).
- `-set-expr` changing an existing column's Go type: derived-schema-with-
  retyped-field vs Tier V (§5b) — decide when the first real case appears.
- Tier V for `-set-expr` new fields: where does the field's type come from
  when the expression isn't statically typeable? (Candidate: the VM
  type-switch exactly as today, on a derived schema field of type `any` —
  which typed structs can't hold → probably forces Tier R for that case.)
- State-vs-field shadowing order in `-stream-expr` env (VERIFY against
  `evalStreamAggExpr` maps.Copy order, §5c).
- Whether Go 1.21+ built-in `min`/`max` cover exprfn.Min/Max (they do for
  ordered types — check emission readability).

## Prior art / related
- [expr-codegen-transpilation.md](expr-codegen-transpilation.md) — the design
  exploration this plan implements.
- [rvalues-as-expressions.md](rvalues-as-expressions.md) — the decision record
  that sequences this work: "transpiler first, then make the value/aggregation
  slots expression-capable." Phase 1 here is the prerequisite for that
  unification; the `@field` sigil and `…-expr` flag folding build on it.
- `cmd/ssql/commands/generate_sql_expr.go` (v4.56.0) — the proven AST-walk
  shape; its whitelist tables are the SQL siblings of §2's Go tables.
- `doc/research/multimode-equivalence-testing.md` — the differential
  methodology all §7 gates follow.
- expr-lang probe programs (semantics tables in §2): scratchpad experiments,
  2026-07-03, expr-lang v1.17.6; re-run them if the pinned version changes.
