# Expression support in `generate go`: transpile to native Go (performance-first)

**Status:** Research / design exploration, 2026-06-25. Prompted by: expressions
(`-if-expr`, `-set-expr`, `-expr`, `-stream-expr`) are a weak spot in code
generation — when a pipeline uses one you lose native-Go performance, and that
loss can be large. This doc maps the current state and designs the fix, with
**generated-code performance as the explicit priority**.

## 1. The problem, stated accurately

Expressions are *partially* supported in codegen, but never as **native Go**:

- **Record mode** (`SSQL_MODE=record`) *does* emit code for expressions — but it
  emits a call into the **expr-lang bytecode VM**:
  - `where -if-expr` → `runtime.MustCompileExprFilter("…")` (`where.go:519-537`)
  - `update -set-expr` → `runtime.MustCompileExpr("…")` + a runtime type-switch
    on the `any` result (`update.go:619-651`)
  - `group-by -expr` / `-stream-expr` → `ssql.ExprAgg(…)` / `ssql.StreamExprAgg(…)`
    (`group_by.go:666-679`)
  So the generated program *runs the interpreter*, not compiled logic.
- **Typed mode** (`SSQL_MODE=typed`) **refuses expressions outright** — the Tier 3
  bail-outs:
  - `where.go:316-320`, `typed_update.go:44-54`, `group_by.go:470-472`
    (`"… need expression-language → Go translation; drop -typed"`).
  A pipeline with any expression therefore can't take the fast typed/parallel
  path at all — it falls back to record mode (with the VM) or errors.

**Net:** the moment an expression appears, you drop from "native Go, parallel,
zero-overhead" to "interpreter in a loop." For a hot expression over millions of
rows that's the difference the user is worried about.

## 2. Why it costs so much (the per-row reality)

The VM path is not just "a bit slower per eval" — it allocates per row. The
generated `runtime.CompileExpr` closure (`runtime.go:104-150`), **for every
record**:

1. `env := make(map[string]interface{})` — a **map allocation per row**;
2. copies *every* field into it (`for k, v := range record.All() { env[k] = v }`);
3. allocates fresh `has` / `getOr` **closures** per row (lines 112-122);
4. runs the bytecode VM (`expr.Run(program, env)`), whose result is `any`
   (forcing a downstream type-switch / boxing).

So a single `-if-expr price > 15` costs ≈ a map alloc + N map inserts + 2 closure
allocs + a VM dispatch + an interface box — **per row**. The equivalent native
Go is `r.Price > 15` (in typed mode: zero allocations, one comparison).

Rough shape (to be measured, not asserted): native field arithmetic is on the
order of nanoseconds and **zero-allocation**; the VM path is ~1–2 µs **plus
several allocations** per row. On 10–100 M rows that is the entire performance
story. The codegen win for *non-expr* pipelines (10–100× over the CLI) comes
precisely from being native + allocation-free; expressions currently forfeit it.

## 3. What we have to work with

- **The AST is already available.** ssql uses **expr-lang** (`github.com/expr-lang/expr`,
  not a custom language). Its parser yields a typed AST (`github.com/expr-lang/expr/ast`):
  `IdentifierNode` (field ref), `BinaryNode` (op, left, right), `UnaryNode`,
  `CallNode`/`BuiltinNode` (functions), `MemberNode`, `StringNode`/literals,
  `ConditionalNode` (ternary), etc. ssql already walks this AST today — the
  aggregation **patcher** (`expr_agg.go:202-374`) and `extractIdentifiers`
  (`runtime.go:154-159`) are existing visitors. So a transpiler is "another
  visitor over `ast.Node`," not a new parser.
- **Typed mode gives us Go types for fields.** The fragment system threads
  `OutputTypedSchema` (Go field names + types) through every stage. At the point
  an expr command emits its fragment it knows the input struct's field types —
  exactly what's needed to emit `r.Salary * r.Bonus` as `float64` with no
  boxing.
- **A clean place to plug in.** The emission sites are already isolated
  (`where.go:519`, `update.go:619`, `group_by.go:666`, `typed_update.go:44`).
  The transpiler replaces "emit a `MustCompileExpr` string" with "emit native Go
  for the expr AST," falling back to the VM string when it can't.

## 4. Goal

Add a **expr-lang AST → Go source transpiler**, used by `generate go`, that emits
**native Go** for expressions — **typed mode first** (where the win is largest:
known types, zero boxing, parallel-safe), then record mode. Where native
translation isn't faithful/possible, **fall back to the existing VM path** so
correctness is never traded for speed.

## 5. Design

### 5a. The transpiler core
`func transpile(node ast.Node, env *typeEnv) (goSrc string, goType GoType, ok bool)`
— a recursive visitor that returns Go source for the sub-expression, its inferred
Go type, and whether it could translate the whole subtree natively (`ok=false`
triggers fallback). `typeEnv` maps field name → Go type (from the typed schema in
typed mode; "dynamic/any" in record mode).

Node lowering (the common, high-value subset):
- **IdentifierNode** (field): typed → `r.<GoName>` (native type); record →
  `ssql.GetOr(r, "<field>", <zeroOfInferredType>)`.
- **Literals**: emit Go literal with the canonical numeric type (int64/float64).
- **BinaryNode** arithmetic/comparison/logical: emit the Go operator, inserting
  **explicit numeric coercion** to match expr-lang's promotion (see §8). `**` →
  `math.Pow`. `??` → a small inline `func() T { … }()` or a precomputed temp.
- **UnaryNode** (`not`, `-`): direct.
- **ConditionalNode** (`a ? b : c`): Go has no ternary → emit an inline
  `func() T { if a { return b }; return c }()` (the Go compiler inlines these).
- **String tests** (`contains`/`startsWith`/`endsWith`/`in`): `strings.Contains`,
  `strings.HasPrefix`, `strings.HasSuffix`, a generated `in` helper.
- **Curated built-ins** (the hot set): `upper`/`lower`/`trim*`/`len`/`abs`/
  `round`/`floor`/`ceil`/`min`/`max`/`int`/`float`/`string`/`split`/`join`/
  `replace`/`has`/`getOr`/`sha256`/`md5`/`replaceRegex` → direct `strings.`/`math.`
  calls or tiny `ssql`-package helpers.

### 5b. Tiered native ⁄ fallback (the safety valve)
The transpiler is **total** only over a curated subset. For anything outside it
(rare built-ins, exotic constructs, or a subtree whose type it can't infer), it
returns `ok=false` and the command emits the **current VM path** for that
expression. So:
- common expressions → fully native (the 95% case, full speed);
- long-tail expressions → VM (works exactly as today, no regression).

In **typed mode**, a VM-fallback expression forces a typed→Record boundary for
that stage (the planner already inserts `Serial()`/adapters for Record-only
fragments — `codefragment_typed.go`), so the rest of the pipeline stays typed.
The transpiler decides native-vs-fallback **at codegen time**, per expression, by
attempting the walk.

### 5c. Per-feature lowering
- **`where -if-expr`** → inline the transpiled bool directly into the
  `Stream.Where` / `iter.Seq` predicate closure: `func(r T) bool { return <expr> }`.
  No `MustCompileExprFilter`, no env map.
- **`update -set-expr`** → `r.<Field> = <expr>` with the inferred type; the
  runtime type-switch on `any` disappears because the type is known at codegen.
- **`update -if-expr`** → the transpiled bool guards the assignment.
- **`group-by -expr`** (`sum(salary*bonus)`): the patcher (`expr_agg.go`) already
  recognizes `agg(perElementExpr)`. Lower to a **typed accumulator loop**: for
  `sum`, `acc += <perElem>`; `count` → `acc++`; `mean` → sum+count then divide;
  `min`/`max` → compare. This is the biggest native win (an aggregate over a
  whole group, currently fully interpreted).
- **`group-by -stream-expr`** (`init` `every` `final`): this is *literally a fold*
  — it maps onto a typed accumulator struct + loop more cleanly than anything
  else. `{s:0}` → `var s float64`; `{s:s+salary}` → `s += r.Salary`; final `s`.
  Arguably the easiest and most rewarding to transpile.

## 6. Performance-first principles (the explicit priority)

1. **Eliminate the per-row env map and closures** — the single biggest win
   (§2). Native transpilation removes all four per-row allocations.
2. **Typed mode = zero boxing.** Use the struct's Go field types; never route a
   transpilable typed expression through `any`.
3. **Infer and propagate Go types** through the AST so emitted arithmetic is
   native (`float64`/`int64`), not `interface{}` with runtime dispatch.
4. **Inline into the stage closure** (no per-expr function-call indirection); let
   the Go compiler inline/vectorize. Parallel-safe by construction (no shared
   env), so expr stages keep the `Stream[T]`/parallel path instead of forcing
   `Serial()`.
5. **Zero per-row allocation** for the transpiled subset (string ops that must
   allocate, e.g. `upper`, are unavoidable but explicit and rare in predicates).
6. **Fall back, never regress.** Anything not natively transpilable uses today's
   VM path — slower but correct and already shipping.
7. **A benchmark gate.** Add `go test -bench` comparing VM-embedded vs transpiled
   for representative `-if-expr`/`-set-expr`/`-expr` over N rows, and assert the
   transpiled form is allocation-free where claimed (`-benchmem`).

## 7. Correctness strategy (non-negotiable)

The transpiled Go MUST produce **identical** results to the VM, or CLI and
generated output diverge silently.

- **Differential testing.** A test harness evaluates a corpus of expressions ×
  sample rows through BOTH the VM (`runtime.CompileExpr`) and the transpiled Go,
  asserting equality. This is the primary gate and the way to discover coercion
  mismatches.
- **Coercion fidelity.** expr-lang's numeric rules are specific (field refs
  preserve type; arithmetic promotes to `int`, overflowing to `int64`;
  aggregations go through `toFloat64`, `expr_agg.go:120-150`). The transpiler must
  reproduce these exactly for the subset it claims — and when unsure, **return
  `ok=false`** and fall back rather than guess.
- **Corpus coverage.** The 3-mode corpus (`corpus_test.go`) currently has **no**
  expr pipelines (they're record-only). Add expr cases there so record + typed +
  parallel are all exercised end-to-end and stay in lockstep.

## 8. The hard parts / honest risks

- **expr-lang surface is large** (~80 built-in functions + ternary, pipe, `in`,
  array `map`/`filter`/`reduce`, etc.). A *faithful, total* transpiler is a huge
  undertaking. The tiered design (native subset + VM fallback) is what makes this
  tractable — but it means "native" coverage grows incrementally, and we must be
  honest in `-explain` about which expressions ran native vs VM.
- **Coercion edge cases** are the silent-divergence risk. Differential testing is
  the mitigation; conservative fallback is the backstop.
- **Record mode is messier than typed.** Without struct types, the transpiler
  emits `GetOr` with an *inferred* type (from literals/operators in the expr),
  which is heuristic. Typed mode is where this is clean and fast — hence
  typed-first. Record-mode native transpilation is a later, optional tier.
- **`map`/`filter`/`reduce` and array ops** over nested fields are genuinely hard
  to transpile and low-frequency in predicates/sets — good first candidates for
  permanent VM fallback.

## 9. Scope & sequencing

1. **MVP — typed-mode native subset for `where -if-expr` + `update -set/-if-expr`.**
   Transpiler core + type inference + the curated operator/built-in set + the
   fallback detector. Differential tests + corpus entries + a bench. This alone
   removes the per-row map/VM cost for the most common expression uses and lets
   expr-bearing pipelines stay on the typed/parallel path.
2. **`group-by -stream-expr`** (the natural fold) → typed accumulator loop.
3. **`group-by -expr`** aggregations → accumulator loop via the existing patcher.
4. **Record-mode native transpilation** (optional) for the same subset.
5. Grow the curated built-in set as real pipelines demand, guided by `-explain`
   telling users what fell back to the VM.

## 10. Open questions

- Where does the transpiler live — a new `cmd/ssql/lib/exprgen` package consumed
  by the command emitters? (Keeps `where.go`/`update.go`/`group_by.go` thin.)
- Do we expose `generate go -explain` lines for "expr X: native" vs "expr X: VM
  fallback" so performance is observable?
- Is a small **runtime helper library** (Go funcs mirroring expr-lang built-ins,
  called from generated code) a better middle tier than VM-fallback for
  medium-frequency functions — native call, no map/VM, but not hand-inlined?
- Should the transpiler reuse/extend the existing aggregation **patcher**
  (`expr_agg.go`) rather than re-walking, since it already normalizes
  `agg(expr)` forms?

## Prior art / related
- `doc/EXPRESSIONS.md` — the user-facing expr-lang reference (operators,
  ~80 functions, examples).
- `doc/research/expression-evaluation-design.md` — why expr-lang (over CEL,
  yaegi, `go build`); the compile-once/run-many model.
- `doc/research/expr-integration.md`, `doc/research/expr-ast-patching.md`,
  `doc/research/custom-aggregation-expressions.md` — the existing integration and
  the aggregation patcher this would reuse.
- `doc/research/codegen-ir-evolution.md` — the fragment-IR direction; a structured
  `Op` there would carry the expression AST (not a re-parsed string), making the
  transpiler's input cleaner.
- Tier 3 bail-outs to delete/replace: `where.go:316`, `typed_update.go:44`,
  `group_by.go:470`.
