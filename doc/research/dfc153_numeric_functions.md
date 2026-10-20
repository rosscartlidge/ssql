# Numeric Functions: The DuckDB Set, in Every Lane

Reference: DFC153
Created: 2026-10-20
Last modified: 2026-10-20

[Back to Index](./README.md)

Status: **proposal, 2026-10-20.** Ross: "i think we should add the trig
functions and the other numeric functions sqrt, exp, ln, log, ... the
ones duckdb has - write up a DFC."

## 1. The question

The expression language has six math functions: `round`, `floor`,
`ceil`, `abs`, `min`, `max` (plus the `**` / `^` power operator). A
pipeline that needs a square root, a logarithm, or an angle has no way
to say so:

```
$ ssql from abc.csv | ssql update -set-expr s 'sqrt(a)'
Error: record 1: evaluating expression for field "s": expression references unknown field(s): sqrt
```

The message is wrong too: `sqrt` is read as a column that is missing.
The question is which functions to add, under which names, with what
semantics at the edges (a square root of a negative number, a
logarithm of zero), and how each of the five lanes — exec, record
codegen, typed codegen, `generate sql` in three dialects — carries
them, so the equivalence gate can pin them the way it pins everything
else. The scope Ross set is DuckDB's numeric function set: what
DuckDB has, ssql should have, so a pipeline with one of them still
translates.

## 2. Where ssql stands

expr-lang's own built-ins stop at `abs`, `ceil`, `floor`, `round`,
`min`, `max`, `mean`, `median` (the last two over lists). Nothing
trigonometric, no roots, no logarithms. ssql adds functions to the
expression environment in `runtime.exprHelpers`
(`cmd/ssql/lib/runtime/runtime.go`: `has`, `getOr`, `bucket`, the
hashes, `replaceRegex`) and the DFC147 calendar functions beside them.

A function reaches every lane through six places, each a small table
or `case`:

| Lane / surface | Where | What it holds today for math |
|---|---|---|
| exec and the Tier-V VM (record and typed fall-back) | `runtime.exprHelpers` | nothing: `round`/`floor`/`ceil`/`abs` are expr-lang's |
| record and typed codegen (Tier N) | `exprToGo` cases in `commands/expr_go.go`, helpers in the root `exprfn` package | `abs` → `exprfn.Abs` (keeps int), `round`/`floor`/`ceil` → `math.*` on a float, `min`/`max` |
| `generate sql` | `exprFuncs` in `commands/generate_sql_expr.go` (name → SQL name), dialect `switch`es for the ones that differ | `abs round floor ceil` by name, `min`/`max` → `least`/`greatest` |
| the catalog (`ssql functions`, Alt-h in an expression) | `writeMathFunctions` in `commands/functions.go` | the six |
| the reference | `doc/EXPRESSIONS.md` "Math Functions" | the six |
| the gates | `TestExprToGo` rows, the `TestExprGoDifferential` corpus, `TestPipelineEquivalence` cases | `abs(pop)` twice, `round`, the arithmetic rows |

The macro loader (DFC152) refuses a macro named like a function by
reading the catalog (`FunctionEntry`), so a new function must be in
the catalog or a user can shadow it with a macro.

## 3. The function set

DuckDB's numeric functions, minus the ones that are not functions of
their arguments (`random`, `setseed`), the ones ssql already has under
another name (`greatest`/`least` are `max`/`min`; `power` is `**`;
`fmod` is `%` on floats), and the bit functions (ssql has them). What
remains, with the name ssql takes and where each dialect stands:

| Function | Result | Go | DuckDB | Postgres | DataFusion |
|---|---|---|---|---|---|
| `sqrt(x)` | float | `math.Sqrt` | `sqrt` | `sqrt` | `sqrt` |
| `cbrt(x)` | float | `math.Cbrt` | `cbrt` | `cbrt` | `cbrt` |
| `exp(x)` | float | `math.Exp` | `exp` | `exp` | `exp` |
| `ln(x)` | float | `math.Log` | `ln` | `ln` | `ln` |
| `log10(x)` | float | `math.Log10` | `log10` | `log10` | `log10` |
| `log2(x)` | float | `math.Log2` | `log2` | `log2` | `log2` |
| `log(b, x)` | float | `math.Log(x)/math.Log(b)` | `log(b, x)` | `log(b, x)` | `log(b, x)` |
| `pow(x, y)` | float | `math.Pow` | `pow` | `power` | `power` |
| `sin cos tan asin acos atan` | float | `math.*` | same | same | same |
| `atan2(y, x)` | float | `math.Atan2` | `atan2` | `atan2` | `atan2` |
| `sinh cosh tanh asinh acosh atanh` | float | `math.*` | same | same (12+) | same |
| `degrees(x)`, `radians(x)` | float | `x * 180 / π`, `x * π / 180` | same | same | same |
| `pi()` | float | `math.Pi` | `pi()` | `pi()` | `pi()` |
| `sign(x)` | int | `exprfn.Sign` | `sign` | `sign` | `signum` |
| `trunc(x)` | float | `math.Trunc` | `trunc` | `trunc` | `trunc` |
| `round(x, n)` | float | `exprfn.RoundTo` | `round(x, n)` | `round(x::numeric, n)` | `round(x, n)` |
| `isnan(x)`, `isinf(x)`, `isfinite(x)` | bool | `math.IsNaN` … | same | — | `isnan` only |
| `gcd(a, b)`, `lcm(a, b)` | int | `exprfn.GCD`/`LCM` | `gcd`/`lcm` | `gcd`/`lcm` (13+) | `gcd`/`lcm` |
| `factorial(n)` | int | `exprfn.Factorial` | `factorial` | — | `factorial` |
| `gamma(x)`, `lgamma(x)` | float | `math.Gamma`, `math.Lgamma` | same | — | — |

Not taken: `even(x)` (DuckDB only, no use case named), `nextafter`,
`bit_count` (have), `greatest`/`least` (have as `max`/`min`),
`random` (not a function of the row — a `-seed` design if ever wanted,
not an expression).

Three naming decisions, all "DuckDB's name wins", because the point of
the scope is that a pipeline reads the same in `generate sql`:

- **`log(x)` is base 10 in DuckDB and Postgres, natural in Go, Python
  and the C library.** ssql takes no one-argument `log`: `ln(x)` is
  natural, `log10(x)` and `log2(x)` are explicit, `log(b, x)` takes a
  base. A reader of either tradition is never surprised.
- **`pow(x, y)`** duplicates `x ** y`. Added anyway: it is what a
  DuckDB user types, and the SQL translator already renders `**`.
- **`round(x, n)`** is expr-lang's `round(x)` with a second argument.
  expr-lang's built-in takes one; the two-argument form needs
  `expr.DisableBuiltin("round")` and ssql's own `round` in the
  environment that accepts one or two. The one-argument behaviour is
  unchanged (half away from zero, as DuckDB: `round(2.5)` = 3,
  `round(-2.5)` = -3, which is Go's `math.Round`).

## 4. Semantics decisions

### 4.1 Results are floats, except where every engine says int

Every function returns float64 — `sqrt(4)` is `2.0`, as `floor` and
`ceil` already are in the transpiler (`exprGoFloat`) and in DuckDB
(`floor` returns DOUBLE). An int argument is promoted (`asFloat`, the
existing rule). The exceptions are the functions that are integers
everywhere: `sign` (-1, 0, 1), `gcd`, `lcm`, `factorial`; and the three
predicates, which are bools.

### 4.2 A non-finite result is absent — already the rule, now in SQL too

Checked 2026-10-20: the JSON writer emits a NaN or infinite number as
`null` (`core.go:1395`, DFC133: "null = no value; the row survives"),
and the next stage reads a JSON null as absent (DFC124), so `update
-set-expr inf 'a / b'` with b = 0 reaches `to jsonl` with `inf`
absent; the record and typed lanes emit `null` directly, which the
canonical form reads as absent; DuckDB prints `inf` and `-nan`. The
existing arithmetic already diverges in the SQL lane, unnoticed
because the gate's division row compares exec with the transpiled Go
only.

The new functions make it a daily matter: `sqrt(-1)`, `ln(0)`,
`acos(2)`, `exp(1000)`. DuckDB and Postgres **raise** on the domain
errors (`Out of Range Error: cannot take square root of a negative
number`) and return `inf` for overflow; Go and DataFusion return NaN
and ±Inf. ssql's rule stands — **a non-finite number is absent** — and
the SQL translation renders it:

- DuckDB: `TRY(f(x))` turns a domain error into NULL (DuckDB ≥ 1.3;
  the rig has 1.5.0), and `CASE WHEN isfinite(v) THEN v END` turns an
  infinity into NULL. One wrapper, `sqlFinite(expr)`, applied to every
  function in §3 whose result can be non-finite, **and to `/`**, which
  closes the existing divergence.
- Postgres: no `TRY`; a domain guard per function (`CASE WHEN x >= 0
  THEN sqrt(x) END`, `CASE WHEN x > 0 THEN ln(x) END`, `CASE WHEN x
  BETWEEN -1 AND 1 THEN acos(x) END`) and `CASE WHEN v = 'Infinity' OR
  v = '-Infinity' THEN NULL ELSE v END` for overflow. Written for the
  functions with a one-sided domain; the rest translate bare.
- DataFusion: NaN and Inf come back as values; `CASE WHEN isnan(v) OR
  abs(v) = 'inf'::double THEN NULL ELSE v END`.

The canonical form already treats NULL and absent alike (the LAG/LEAD
edge, DFC130), so a Golden with the field missing passes every lane.

### 4.3 Overloads and arguments

`round` is the only overload. `log` with one argument is a compile
error naming `ln` and `log10` ("log takes a base and a value; ln(x)
is the natural logarithm, log10(x) base ten"). Wrong argument counts
are the existing expr-lang compile errors. A string argument is
refused by the transpiler (`needs a numeric argument`, the `abs`
shape) and by the VM.

### 4.4 Typed-lane constants

Found on the way: `-set-expr nan '0.0 / 0.0'` in the typed lane
produces `invalid operation: division by zero` from the Go compiler —
the transpiler emits the constant expression and Go folds it. A
constant domain error in a new function (`sqrt(-1.0)` as a literal)
compiles fine (`math.Sqrt` is a call), so this is a pre-existing gap
of the `/` operator alone; recorded in TODO with the DFC, not part of
it.

## 5. Where each lane changes

1. **VM** (`runtime.exprHelpers`): one entry per function, each a Go
   func taking `any` numeric arguments (int64 or float64, the
   existing `bucketFn` shape) and returning float64 / int64 / bool.
   `round` replaces the built-in (`DisableBuiltin`).
2. **Transpiler** (`exprToGo`): one `case` per function, the
   `round`/`floor`/`ceil` shape — numeric check, `asFloat`, `math.*`
   or an `exprfn` helper for the int-valued ones and `RoundTo`. The
   typed template inherits it.
3. **SQL** (`exprFuncs` and a `sqlFinite` wrapper in
   `generate_sql_expr.go`): the name map for the identical names;
   `pow` → `power` and `sign` → `signum` per dialect; `round(x, n)`
   casts to numeric on Postgres; `gamma`/`lgamma`/`factorial`/`isnan`
   family → `dialectRefuse` where the table says "—".
4. **Catalog and Alt-h** (`writeMathFunctions`): the entries, so
   `ssql functions -category math` shows them and a macro cannot take
   their names.
5. **Reference** (`doc/EXPRESSIONS.md` Math Functions): the table,
   the naming note on `ln`/`log10`/`log`, the non-finite rule.
6. **The codelab** gains one block (`sqrt`, `round(x, 2)`), run by
   `make doc-test`.

## 6. Tests

- `TestExprToGo`: one row per function (type and source), plus `log`
  with one argument refused, `round(x, n)` with two.
- `TestExprGoDifferential` corpus: every function over the schema's
  `pop`, `qty`, `price` columns, including the rows that violate a
  domain (the fixture has a negative and a zero), so the VM and the
  transpiled Go agree on NaN.
- `TestPipelineEquivalence`, Golden by hand on `employees.csv`:
  `expr_math_roots_logs` (`sqrt`, `cbrt`, `exp`, `ln`, `log10`,
  `log2`, `log(2, x)`, `pow` on `salary`, rounded to 6 places so the
  Golden is exact across engines); `expr_math_trig` (`sin`, `cos`,
  `atan2`, `degrees`, `radians`, `pi()` on `level`); `expr_math_ints`
  (`sign`, `trunc`, `round(x, 2)`, `gcd`, `lcm`, `factorial`);
  `expr_math_domain` (`sqrt` of a negative, `ln(0)`, `acos(2)`:
  absent in every lane; the DuckDB lane via `TRY`, Postgres via the
  guards, DataFusion via the NaN guard); `expr_div_zero_absent` (the
  §4.2 divergence the DuckDB lane has today: watched failing first).
- DuckDB-only functions: the dialect lanes skip by design through the
  existing "has no DIALECT translation" reading.

## 7. Plan and size

One day. Order: the VM entries and the catalog first (exec works and
`ssql functions` shows them), then the transpiler with its unit rows
and differential corpus, then SQL with the `sqlFinite` wrapper and
the `/` fix, then the equivalence cases, then the docs. Each step
leaves the tree green.

## 8. Questions for Ross

1. **`log`**: no one-argument form, `ln` natural, `log10`/`log2`
   explicit, `log(b, x)` with a base — as above? The alternative is
   DuckDB's `log(x)` = base 10, which reads as natural to anyone from
   Go or Python.
2. **The DuckDB-only tail** — `gamma`, `lgamma`, `factorial`: in, with
   the other dialects refusing by design? Or leave them out until
   asked.
3. **Non-finite in the SQL lane** (§4.2): apply the `isfinite` guard
   to `/` as well, closing the existing `inf` divergence, or only to
   the new functions.
