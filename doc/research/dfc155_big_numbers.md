# Big Numbers: Options for Integers Past 2^63 and Exact Decimals

Reference: DFC155
Created: 2026-10-22
Last modified: 2026-10-22

[Back to Index](./README.md)

Status: **survey, 2026-10-22.** Ross: "the fibonacci testing made me
realise that we dont support BigInts or BigFloats — I am not sure that
we should but its worth investigating. What is duckdb's support? Lets
write a DFC on our options."

## 0. The prompt

`from range 94 | window -stream-expr '{a:0, b:1}' '{a:b, b:a+b}' 'b' fib`
is exact to row 91 (fib(92) = 7540113804746346429 < 2^63) and wraps at
row 92: `-6246583658587674878`, then `1293530146158671551`, then another
negative. Nothing says so. v4.121.2 made aggregation results keep their
int64 type precisely so this fold would be exact *to* 2^63; the question
is what happens past it, and whether ssql wants numbers wider than a
machine word at all.

"Big numbers" is four different things, and the answer differs for each:

| | What | Who has it |
|---|---|---|
| **(a) wider ints** | 128-bit integers, ±1.7 × 10^38 | DuckDB `HUGEINT` (native, fast) |
| **(b) arbitrary ints** | any number of digits | DuckDB `BIGNUM` (half-done, §3), Postgres `NUMERIC`, Go `math/big` |
| **(c) decimals** | fixed-point `DECIMAL(p, s)`: money | every engine, Arrow, Parquet; Go only via a library |
| **(d) big floats** | arbitrary-precision floating point | Go `big.Float` only; no SQL engine |

## 1. Where ssql stands (measured 2026-10-22, v4.122.0)

Canonical scalars are `int64` and `float64` ([CLAUDE.md](../../CLAUDE.md)
§Canonical Numeric Types): every reader parses an integer with
`strconv.ParseInt` and falls back to `float64` when that fails; the
typed lane's struct fields are `int64`, `float64`, `string`, `bool`,
`time.Time`; the JSONL header types are `int`, `float`, `string`,
`bool`, `time`, `list`, `map`.

| Input or operation | exec | go-record / go-typed | DuckDB lane |
|---|---|---|---|
| CSV `123456789012345678901234567890` | `float` 1.2345678901234568e+29 | same | `DOUBLE`, same value |
| JSON `{"h":123456789012345678901234567890}` | `float`, same | same | `DOUBLE` |
| `9223372036854775807 + n` (n = 1) | `-9223372036854775808`, silently | same | **error** `Overflow in addition of INT64` |
| `n * 9223372036854775807 * 2` | `-2`, silently | same | **error** |
| `2 ** 70` | `float` 1.18e21 (`**` is float in expr-lang) | same | `DOUBLE` |
| `9007199254740993 * n` (past 2^53) | exact int64 | exact | exact `BIGINT` |
| `group-by -sum x` with x = 2^63 − 1, three rows | `float` 2.7670116110564327e+19 | same | `HUGEINT` 27670116110564327421 exactly |
| fib fold, row 92 onward | wraps | wraps | (no SQL form of `-stream-expr`) |

Three things stand out:

1. **Integer overflow is silent in three lanes and an error in the
   fourth.** That is a divergence of the kind
   [multimode-equivalence-testing.md](./multimode-equivalence-testing.md)
   exists to catch; it is invisible to `TestPipelineEquivalence` only
   because no fixture overflows. Go wraps (the transpiler plan,
   [expr-transpiler-implementation-plan.md](./expr-transpiler-implementation-plan.md)
   §2a, chose "wraps on overflow like Go" to match the VM, which also
   wraps). DuckDB, Postgres and DataFusion all raise.
2. **`-sum` of integers is a float**, exact only to 2^53 ([sql.go](../../sql.go)
   `Sum` accumulates `float64`), where DuckDB widens `sum(BIGINT)` to
   `HUGEINT`. The equivalence normaliser compares as float64 and so
   does not see it. v4.121.2 fixed the same shape for `-min`/`-max`/
   `-first`/`-last`/the folds; `-sum` and `-avg` were left as floats.
3. **Reading** a wide integer loses precision identically everywhere
   (ssql and DuckDB both make it a double), so there is no divergence
   on input — only a loss nobody is told about. A 20-digit id column
   silently becomes a double in every lane.

## 2. What the engines support

### DuckDB 1.5.0 (measured here)

- **`BIGINT`** arithmetic is checked: `+`, `*` raise `Out of Range
  Error`. `sum(BIGINT)` returns `HUGEINT`.
- **`HUGEINT` / `UHUGEINT`** are native 128-bit (`INT128`); `+ - * // %`
  exact, checked (`Overflow in addition of INT128`), cast to and from
  `DOUBLE`. Fibonacci in `HUGEINT` is exact to fib(186). Written to
  JSON as a bare number (`{"h":123456789012345678901234567890}`), which
  `read_json` then reads back as `DOUBLE` — DuckDB's own JSON round
  trip loses it.
- **`BIGNUM`** (the type `VARINT` introduced in 1.1 was renamed; both
  spellings cast to `BIGNUM`) is arbitrary precision — but only
  partly: `+`, `-`, comparison, `sum`, `max` are exact (fib(300), 63 digits, computed exactly by a
  recursive CTE); **`*`, `//`, `%`, `**` return `DOUBLE`**. So it holds a
  big number and adds it, and loses it on the first multiplication.
- **`DECIMAL(p, s)`**, p ≤ 38, native (`INT128` underneath): `+ - *`
  exact and checked ("Overflow in multiplication of DECIMAL(38)"),
  **`/` returns `DOUBLE`**.
- **CSV and JSON readers** infer `DOUBLE` for an integer literal past
  `BIGINT`, exactly as ssql does. There is no big float: `DOUBLE`
  overflows to `inf`.

### Postgres (documented; the rig lane was not up this session)

`BIGINT` raises `bigint out of range`; no 128-bit type; **`NUMERIC`**
is arbitrary precision (131072 digits before the point, 16383 after)
with exact `+ - * /` at a declared or unbounded scale; `sum(bigint)`
returns `numeric`. The one engine where (b) and (c) are both complete.

### DataFusion (documented)

`Int64` arithmetic is checked (an Arrow compute overflow error);
`Decimal128(38, s)` and `Decimal256(76, s)`; no arbitrary-precision
integer; `sum(Int64)` stays `Int64` and overflows loudly.

### Go and ssql's dependencies

- No native `int128`. `math/big` gives `Int`, `Float`, `Rat`
  (arbitrary; pointer types, an allocation per operation, roughly a
  hundred times a machine add; not comparable, so not a map key — a
  group-by key would be its decimal string).
- No standard decimal; `shopspring/decimal`, `cockroachdb/apd`,
  `ericlagergren/decimal` are the usual choices — a new dependency in
  the core module and in every generated program.
- expr-lang v1.17.6 has `expr.Operator("+", "fn")` (operator
  overloading by operand type), so a new value type can take `+ - * / %`
  in expressions without forking the VM. Whether it can also
  *replace* `int64 + int64` with a checked function is to be verified
  (the patcher matches by the overload's parameter types; `func(int64,
  int64) int64` should match).
- Arrow and Parquet have `Decimal128`/`Decimal256`; ssql writes
  neither today (`io.go` maps to int64/float64/string/bool/timestamp).
- The playground, `to explore` and `to table`'s browser view parse
  JSON with `JSON.parse`, which rounds past 2^53. A number wider than
  that must travel as a string to a browser, or be lost there.

## 3. The options

Each option ends with two lines added 2026-10-22 at Ross's request:
**Performance** (what existing pipelines pay) and **Code** (what has to
be written). The numbers come from a micro-benchmark and a 2M-row
pipeline, both run on this machine that day:

| measured (3-op expression `a + b * 2 - c`) | ns/op | allocs |
|---|---|---|
| Go, plain int64 | 0.85 | 0 |
| Go, int64 with overflow checks (sign test, `p/b != a`) | 2.3 | 0 |
| Go, int128 struct (`math/bits` add, mul) | 1.0 | 0 |
| Go, `math/big` with reused scratch values | 45 | 0 |
| Go, `math/big` with a fresh `*big.Int` per result (the `Record` shape) | 205 | 5 |
| expr VM, ssql's compile (`map[string]any` env), VM only | 290 | 5 |
| expr VM, `+ - *` patched to checked calls, VM only | 350 | 8 |
| expr VM, plain, with the per-row env map ssql builds | 540 | 9 |
| expr VM, patched, with the per-row env map | 645 | 12 |

| 2M rows: `from range 2000000`, then `update -set-expr s 'n + n * 2 - 1'`, then `to jsonl` | wall | per row |
|---|---|---|
| exec, or record codegen (both run the VM) | 5.3 s (0.6 s without the update) | 2.4 µs for the update step |
| typed codegen (parallel, `out.S = ((r.N + (r.N * 2)) - 1)`) | 1.7 s | 0.85 µs whole row, of which the arithmetic is 1 ns |

Two facts the probe settled. `expr.Operator` overloading does **not**
fire on ssql's env: the compiler sees `map[string]any` with
`AllowUndefinedVariables` (`cmd/ssql/lib/runtime/runtime.go:115`), so
operand types are unknown and `MaxInt64 + 1` still wraps with the
overload installed; it fires only on a typed struct env, which ssql
does not have. An `expr.Patch` visitor that rewrites every `+ - *`
BinaryNode into a call of a checked function (the `expr_agg.go:376`
precedent) does fire, errors on overflow, and is the mechanism B, C and
E all route through, so B's code is the foundation, not a throwaway.

A census of what a new numeric type touches, from grep: 78 `case int64`
switch arms in 36 non-test files (`core.go` 8, `commands/helpers.go` 6,
`resample.go`, `cast_value.go`, `sql.go`, `io.go`, `coerce_types.go`,
`update.go` 4–5 each, the rest 1–3); 26 `strconv.ParseInt` sites in 17
files, of which the reader fallbacks (`csv_infer.go`, `io.go`,
`core.go`, `cast_value.go`, `typed/io.go`) are the ones a wider literal
changes; 31 int-formatting sites in writers; the schema type enum
(`lib/schema.go:18`, 14 uses in 8 files); the SQL type map
(`generate_sql_dialect.go:745`); the typed sum emission
(`typed_groupby.go:462–499`, which casts every summand to `float64`).

### A. Status quo, documented

int64 exact to 2^63, float beyond, wrap past it. Cost: a paragraph in
`doc/EXPRESSIONS.md`. Leaves finding 1 — a silent wrong answer in
three lanes where the fourth stops — which is the thing the Fibonacci
run actually demonstrated.

- **Performance**: none.
- **Code**: a paragraph in `doc/EXPRESSIONS.md`; nothing else.

### B. Integer overflow is an error, in every lane

The int rule that matches the float rule ("a non-finite number is
absent", DFC133/DFC153 §4.2) and matches all three engines: an int64
`+`, `-`, `*` that overflows stops the pipeline with the expression in
the message, as DuckDB does. Also: `-sum` and `-avg` over integers
accumulate in int64 (exact to 2^63, error past it — or widen, see C)
rather than float64.

- **VM**: not `expr.Operator` — it needs operand types at compile time
  and ssql's env is `map[string]any` (probed 2026-10-22: the overflow
  wrapped with the overload installed). An `expr.Patch` visitor
  rewrites each `+ - *` BinaryNode into a call of a checked function
  that handles the int64 pair and hands every other pair to expr's own
  `vm/runtime` operator; proven on the untyped env, cost in §3's table.
- **Transpiler**: emit `exprfn.Add(a, b)` for int operands (readable;
  the float path unchanged); `BenchmarkExprGo` says what it costs.
- **SQL**: nothing — every engine already raises.
- **Tests**: an equivalence case whose exec lane errors is the
  existing "every lane refuses" shape; the `expr_div_zero_absent`
  precedent.

Half a day to a day. This is the correctness fix; everything below is
capacity.

- **Performance**: VM lanes (exec and record codegen) pay **+60 ns per
  three operators** — the patched calls cost 350 ns against 290 ns for
  the VM alone, and one boxing allocation per operator (the checked
  function returns `any`); that is +20% of the VM's own time and
  **+2.5% of the 2.4 µs per-row update step**, invisible against the
  reader and writer. Typed codegen pays +1.5 ns per three operators on
  a 0.85 µs row, under 0.2%. Float arithmetic, comparisons, strings,
  readers, writers, sort, join and group-by are untouched. `-sum` of
  integers in int64 is slightly *faster* than today (no int→float
  conversion per row). SQL: nothing.
- **Code**: ~400 lines plus tests. Root package: three checked functions
  in `exprfn` (add, sub, mul; 40 lines) and an `expr.Patch` visitor that
  routes an int64 pair through them and every other operand pair
  through expr's own `vm/runtime` Add/Subtract/Multiply so no other
  semantics moves (80 lines, added to `ExprStandardOptions`).
  Transpiler: `binary()` in `expr_go.go:453–495` emits
  `exprfn.AddInt(a, b)` when both operand types are int (30 lines; the
  generated line becomes a call instead of `+`). `-sum`/`-avg` in int64:
  `sql.go:1676,1699` and the typed emission at
  `typed_groupby.go:462–499,795–820` (60 lines). Tests: an equivalence
  case in the `expr_div_zero_absent` shape, transpiler table rows,
  differential corpus entries, a `TestExprGoDifferential` run.

### C. 128-bit integers (`int128`), DuckDB's `HUGEINT` twin

A value type `ssql.Int128` (two `uint64`s, `math/bits` arithmetic; or
`big.Int` behind a fixed-width check). Header type `int128`; written to
JSON as a bare number (JSON allows it; DuckDB writes the same; browsers
lose it past 2^53 — the explore page would show it as a string).

**The widening rule (settled with Ross, 2026-10-22): a column is int128
because it was declared so, never because a value overflowed.** The
first draft of this section said "an int64 result that overflows
becomes int128"; that rule cannot be kept by two lanes. The SQL lane
cannot match it — DuckDB, Postgres and DataFusion all raise on BIGINT
overflow, so agreeing would mean computing every integer expression in
HUGEINT to serve a rare case — and typed codegen cannot either, a
struct field has one Go type fixed at generation time, so no value can
change type at row 92. The consistent rule is the one int and float
already follow: a column's type is decided once, mixing widens, nothing
changes type mid-stream. A column becomes int128 in three ways, each
the same in every lane:

1. **A cast**: `cast -type int128 n` before the arithmetic — the
   explicit lever, and what the Fibonacci fold would use.
2. **A wide literal in the source**: a value past 2^63 in a CSV or JSON
   column types the column int128 (today: float). The SQL lane follows
   because ssql's generated SQL declares CSV column types itself
   (`generate_sql_dialect.go:267`) rather than trusting DuckDB's
   sniffer, which made a 30-digit column DOUBLE in §2.
3. **A sum**: `-sum` over an int64 column accumulates and emits int128,
   which is exactly DuckDB's `SUM(BIGINT) → HUGEINT`. The one widening
   that makes the lanes agree rather than diverge.

Once one operand is int128 the whole expression is (int64 beside int128
widens, as int beside float does), so a cast on one input column is
enough for `n * n + m` to run wide. A literal inside an expression stays
int, so `n * 1000000000000` on an int64 column still errors under B's
rule — DuckDB's answer too. An int128 result that overflows is an error.
A pipeline with no cast, no wide literal and no sum never touches an
int128 value, which is why existing pipelines pay only B's check.

- Covers Fibonacci to fib(186), any sum of an int64 column, money in
  cents to 10^38, 128-bit ids (UUIDs as numbers, IPv6).
- A struct of two words is **comparable**: a group-by key, a sort key,
  a typed struct field with no allocation. This is the property
  `big.Int` lacks.
- SQL: `HUGEINT` on DuckDB (exact twin, native speed); `NUMERIC` on
  Postgres; `Decimal128(38, 0)` on DataFusion holds 38 digits where
  int128 holds 39 — refuse past 10^38 or use `Decimal256`.
- Arrow/Parquet: `Decimal128(38, 0)` or a string.
- Touches: the `Record` value switch (compare, sort, `GetOr`
  promotions), every reader's parse fallback (a 20-digit literal
  becomes int128 instead of float — a visible behaviour change, and
  the right one), the JSON writer, `cast -type int128`, the VM (two
  `expr.Operator` sets), the transpiler (method calls, `Cmp` for
  comparison), typed struct fields and their reader/writer, `-sum`,
  the SQL type map, the catalog. A week.

- **Performance**: existing int64 pipelines pay B's check plus one
  promotion branch per operator; int128 arithmetic itself is 1.0 ns per
  three operators against 0.85 plain (no allocation; a 16-byte struct
  boxed into `any` allocates once, as an int64 does). A type switch
  gaining an arm costs nothing on the arms it does not reach, so
  readers, writers, sort and join stay at today's speed for int64 data;
  only rows that actually hold an int128 pay the wider compare and
  format. The reader fallback runs only when `ParseInt` has already
  failed (today's path to float), so parsing is unchanged. DuckDB's
  `HUGEINT` is native, so the SQL lane pays nothing.
- **Code**: 1,500–2,500 lines plus tests, a week. Every site in the
  census: a sibling arm beside the 78 `case int64` switches (or a
  deliberate skip, each one a decision), the five reader fallbacks, the
  31 formatting sites gaining a case, the schema enum and its 14 uses,
  `cast -type int128`, the typed struct field type with its reader and
  writer, Arrow/Parquet `Decimal128(38, 0)`, the SQL type map, the
  catalog and `doc/EXPRESSIONS.md`. VM and transpiler: the patched call
  from B grows an int128 arm and the promotion rule (100 lines). The
  explore page and the playground need a string form past 2^53.

### D. Arbitrary-precision integers (`math/big`)

Everything in C, with `*big.Int` as the value. Differences: no upper
bound (fib(300), 100-digit ids); an allocation per operation and no
comparability (group-by keys by string); DuckDB's `BIGNUM` is exact
only for `+ - sum max` — a pipeline that multiplies falls to DOUBLE
there, so the DuckDB lane would have to refuse `*` on a big column, and
DataFusion has no form at all. The capability is wider than any
engine's but one; the use beyond Fibonacci is unclear. A week plus a
performance tax on every value switch.

- **Performance**: the arithmetic is **205 ns and 5 allocations per
  three operators** in the `Record` shape (every result a fresh
  `*big.Int` held as `any`), 250× plain int64; 4% of a VM row but 25%
  of a typed row. The reuse form (45 ns, no allocation) is available
  only inside a typed fold, never for an immutable record value. No
  comparability: a big group-by or join key goes through `String()`
  (100 ns and an allocation per row), a sort compare through `Cmp` on
  interfaces. int64 rows pay what C pays; only big rows pay the rest,
  but a pipeline that promotes on overflow promotes every subsequent
  row of that column.
- **Code**: everything in C at the same sites, plus the paths that
  assume a comparable value (group-by, join, `distinct`, typed struct
  keys, `top`'s heap), plus the DuckDB lane refusing `*`, `/`, `%`, `**`
  on a big column and DataFusion refusing the type outright. About C
  plus a fifth, and the harder review.

### E. Fixed-point decimals (money)

`DECIMAL(p, s)` is the type every engine, Arrow and Parquet share, and
the one real users ask for (sums of prices that must be exact to the
cent). Two shapes:

- **A library decimal** (`shopspring/decimal`): full generality, a new
  dependency in the module and in generated programs, a pointer-ish
  value, slow.
- **Scaled int64 with the scale in the schema** — `decimal(18, 2)` is
  an int64 of hundredths, the header says `decimal:2`: `+ - compare sum`
  are int64 ops (exact, fast, comparable, no dependency); `*` rescales
  (18 digits total, overflow by B's rule); `/` rounds half-even to the
  scale, or is a float as DuckDB's is. SQL maps to `DECIMAL(18, s)` on
  every engine; Arrow/Parquet to `Decimal128(18, s)`. This is what
  people already do by hand ("store cents"); the type makes `to table`
  print `12.34` and `cast -type decimal 2` do the conversion once.
  Three or four days, and it composes with C (an int128 mantissa gives
  `DECIMAL(38, s)`, DuckDB's exact range).

- **Performance**: the scaled-int64 shape costs **nothing on existing
  pipelines** (a new type reached only when a column declares it) and,
  on decimal columns, int64 speed: `+ - compare sum` are one int64
  operation after a scale alignment that is a table lookup; `*`
  rescales by one division by a power of ten; parsing `12.34` as
  hundredths is a `ParseInt` on the digits, cheaper than `ParseFloat`;
  formatting inserts a point into a `FormatInt`, cheaper than
  `FormatFloat`. The library shape (`shopspring/decimal`) has D's
  profile: a `*big.Int` inside, ~200 ns and allocations per operator,
  and a dependency in every generated program's `go.mod`.
- **Code**: 1,000–1,500 lines plus tests, three or four days. A
  comparable value type `ssql.Decimal{Units int64; Scale uint8}`; the
  switch census of C but a narrower surface (most arms reduce to the
  int64 arm after alignment); the schema type string `decimal:2`
  through `lib/schema.go` and every writer's `_schema` header;
  `cast -type decimal 2`; the typed struct tag (`ssql:"price,decimal=2"`)
  with its reader and writer; SQL `DECIMAL(18, s)` on every engine;
  Arrow `Decimal128(18, s)`; the patched call from B grows a decimal
  arm; the catalog and `doc/EXPRESSIONS.md`.

### F. Big floats

`big.Float` has no SQL form (DuckDB, Postgres and DataFusion stop at
`DOUBLE`), no Arrow form, and no use that a decimal or a big integer
does not serve better. Not an option; say so in the docs.

- **Performance** and **Code**: not costed; a `*big.Float` would have
  D's allocation profile with nowhere to send the result.

## 4. Recommendation

1. **Do B now.** It is the bug the Fibonacci run showed: three lanes
   answer wrong where the fourth stops. Loud overflow is the rule in
   every engine ssql translates to, it is the int twin of the
   non-finite rule, and it costs a day. Include `-sum`/`-avg` of
   integers in int64 (today's float accumulation is a quieter form of
   the same thing).
2. **Park C, D and E until a use asks**, and record which to build when
   one does: **C for width** (one promotion rule, native in DuckDB,
   comparable, no dependency) and **E as scaled int64 for money**; D
   only for a request past 38 digits, which so far is only Fibonacci
   past 186. F never.
3. **Document the envelope** either way: integers exact to 2^63,
   floats exact to 2^53, an integer literal past int64 is read as a
   float in every lane (an id that wide belongs in a string column —
   `cast -type` it, or quote it in the CSV).

## 5. Questions for Ross

1. **The overflow rule**: error (every engine's answer, recommended) or
   promote to float (keeps magnitude, loses exactness, matches what
   `-sum` does today)?
2. **Is there a use beyond Fibonacci** — money sums that must be exact
   (→ E), wide ids (→ string, or C), 128-bit arithmetic (→ C)? If
   none, A + B and this DFC is the record of why.
3. **`-sum` of integers**: int64 (exact, error on overflow) or widen to
   int128 (DuckDB's behaviour, needs C)? *Answered in C's widening rule
   (2026-10-22): widen, once C exists — a sum is one of the three places
   the engines themselves widen, so it is the one widening that keeps
   the lanes in agreement. Until C exists, B's int64 accumulation with a
   loud overflow is the interim; the DuckDB lane disagrees only past
   2^63, where ssql stops and DuckDB answers.*
