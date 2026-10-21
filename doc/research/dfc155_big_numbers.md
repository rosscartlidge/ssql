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

### A. Status quo, documented

int64 exact to 2^63, float beyond, wrap past it. Cost: a paragraph in
`doc/EXPRESSIONS.md`. Leaves finding 1 — a silent wrong answer in
three lanes where the fourth stops — which is the thing the Fibonacci
run actually demonstrated.

### B. Integer overflow is an error, in every lane

The int rule that matches the float rule ("a non-finite number is
absent", DFC133/DFC153 §4.2) and matches all three engines: an int64
`+`, `-`, `*` that overflows stops the pipeline with the expression in
the message, as DuckDB does. Also: `-sum` and `-avg` over integers
accumulate in int64 (exact to 2^63, error past it — or widen, see C)
rather than float64.

- **VM**: an `expr.Operator` overload routing `int64 (+|-|*) int64`
  through `exprfn.AddInt64` etc. (`math/bits.Add64` carry checks; a
  nanosecond), or — if the overload cannot replace built-in int
  arithmetic — a post-hoc check is impossible, so the overload is the
  design and must be proven first.
- **Transpiler**: emit `exprfn.Add(a, b)` for int operands (readable;
  the float path unchanged); `BenchmarkExprGo` says what it costs.
- **SQL**: nothing — every engine already raises.
- **Tests**: an equivalence case whose exec lane errors is the
  existing "every lane refuses" shape; the `expr_div_zero_absent`
  precedent.

Half a day to a day. This is the correctness fix; everything below is
capacity.

### C. 128-bit integers (`int128`), DuckDB's `HUGEINT` twin

A value type `ssql.Int128` (two `uint64`s, `math/bits` arithmetic; or
`big.Int` behind a fixed-width check) with one promotion rule: an
int64 result that overflows becomes int128; an int128 that overflows
is an error. Header type `int128`; written to JSON as a bare number
(JSON allows it; DuckDB writes the same; browsers lose it past 2^53 —
the explore page would show it as a string).

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

### D. Arbitrary-precision integers (`math/big`)

Everything in C, with `*big.Int` as the value. Differences: no upper
bound (fib(300), 100-digit ids); an allocation per operation and no
comparability (group-by keys by string); DuckDB's `BIGNUM` is exact
only for `+ - sum max` — a pipeline that multiplies falls to DOUBLE
there, so the DuckDB lane would have to refuse `*` on a big column, and
DataFusion has no form at all. The capability is wider than any
engine's but one; the use beyond Fibonacci is unclear. A week plus a
performance tax on every value switch.

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

### F. Big floats

`big.Float` has no SQL form (DuckDB, Postgres and DataFusion stop at
`DOUBLE`), no Arrow form, and no use that a decimal or a big integer
does not serve better. Not an option; say so in the docs.

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
   int128 (DuckDB's behaviour, needs C)?
