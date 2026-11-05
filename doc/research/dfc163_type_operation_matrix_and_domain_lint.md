# Finding Type Gaps Before Users Do: a Type × Operation Matrix and a Domain Lint

Reference: DFC163
Created: 2026-11-04
Last modified: 2026-11-05

[Back to Index](./README.md)

Status: **lint built 2026-11-05 (§6).** The matrix (§2.1) and the declared-types generator (§2.3) are not built yet.

## 0. The prompt

Ross, 2026-11-04, while the `inet` type was being built (DFC162 §9):
"great that you are finding new bugs — can you think of a way of finding
these more proactively?" Then: "finish inet first, then write the DFC".

## 1. What the bugs have in common

Every type ssql added after the first four (int, float, string, bool) has
found bugs of one shape, and found them late:

| Type | When | Bugs of this shape found | How |
|---|---|---|---|
| `time` | DFC128 | a time written as Go's `Time.String()`, which nothing parses back (DFC128 F3) | the interchange work |
| `json` | DFC136, DFC144 | six, in one addendum: the `json` schema type not honoured, nested values stringified | an addendum after release |
| `int128` | DFC155 C | the generated `-set-expr` write-back stored the digits as text; `isSimpleValue` dropped group keys | the first equivalence run |
| `decimal:S` | DFC155 E | the join merge's right-wins collision; typed window order comparator | the census, and luck |
| `inet` | DFC162 §9 | seven, all predating inet (below) | building inet |

The seven found while building inet (DFC162 §9.4):

1. `from jsonl -type F decimal:2` (and `time`) left the column as text,
   silently. `setAny` had no arm for either. `fill` and `unpivot`
   stringified them the same way.
2. `update -set-expr x 'decimal(x)'` retyped the values but not the
   header, so the next stage read the old type.
3. Typed `top -field` on a decimal or a time column emitted
   `typed.TopBy`, which needs `cmp.Ordered`, and did not compile.
4. Record codegen's native `update` setter table had no int128 or decimal
   entry, and emitted `mut.(…)`.
5. Typed `from ssh` gave a time field the default `""`, which did not
   compile.
6. `sort -spill` refused int128 and decimal columns.
7. `generate sql`'s `rename` dropped a column's kind.

Each is a **type switch, or a table keyed by Go type name, that a newer
type was never added to.** None needed unusual data. Each needed one
specific *type* to meet one specific *operation* in one specific *lane*,
and nothing enumerates those combinations.

### Why the existing instruments did not find them

- **The equivalence gate** runs the cases someone wrote. A type gets cases
  for the operations its author thought of.
- **DFC133's random differential test** builds tables whose types are
  *inferred* (int, float, string, bool, date-like text). It never
  declares a decimal, int128, time or inet column, and it compares exec
  with DuckDB, sampling the Go lanes only. Four of the seven were in
  typed codegen.
- **DFC133's crash sweep** runs every flag of every command, but over one
  CSV whose columns are inferred, and it asserts only "no crash, no
  hang".
- **The census** (DFC155 §7.6, §8.6, DFC162 §9) is a list in a document. A
  new type is checked against it by a person, or a planning agent, with
  grep.

## 2. The two instruments

### 2.1 A type × operation matrix (dynamic, exhaustive)

**Rows: every declarable type.** These are the types `ParseFieldType`
accepts: int, int128, decimal:2, float, string, bool, time, json, inet.
The fixture is one small CSV per type: shuffled, with distinct values
whose text order differs from their value order, with one empty cell and
one duplicate. It is declared through `-type`, so the column *is* the
type, as a user's would be.

**Columns: every argument that takes a field,** read from `ssql
-spec-json` (the arguments whose `completer` is `fields`). Today that is
124 arguments across 39 commands:
- sort, top, where, update;
- group-by and every aggregate flag;
- window, join, distinct, fill, unpivot, pivot, cast, rename;
- the `to` sinks;
- the source readers' `-type`.

The matrix reads the CLI's own description of itself, so a new command or
flag joins it with no test edit (DFC115: commands are the authority on
themselves).

**Lanes:** exec, record, typed, parallel, DuckDB, Postgres. DataFusion
and rust join where the type translates.

**The oracles.** None needs a hand-written answer.

1. **Loud or working.** The cell runs, or it fails with an `Error:` line.
   These are failures:
   - generated code that does not compile;
   - a Go runtime panic;
   - output where another lane refused.

   Bugs 3, 4 and 5 are this class.
2. **The type survives.** Where the operation keeps a column (sort,
   where, top, distinct, fill, unpivot's ids, a join, min, max, first,
   last, a rename, `update` of another column), the `_schema` wire type
   of that column is unchanged after the stage. Bugs 1, 2 and the
   stringifying writers are this class. Printed text cannot catch them,
   because a stringified address prints the same as the address.
3. **The lanes agree,** with the equivalence harness's normalisation. A
   compare through text instead of the type differs as soon as the
   fixture's text order differs from its value order.
4. **The operation's own refusal is declared.** A cell may refuse (sum of
   an inet, `-avg` of a string) only if every lane refuses. One lane
   refusing while another succeeds is a failure, and so is a refusal
   whose message does not name the type.

**Cost.** 9 types × 124 arguments is about 1,100 cells per lane. The
interpreted and SQL lanes run in tens of milliseconds a cell, so a
minute or two for both. The Go lanes are what cost: a `go build` per
cell would be hours. They batch instead, as the rust lane does (DFC157
§5): every cell becomes a function in one program, built once per lane,
with `generate go -func`, which exists (as `generate rust -func` does).
The matrix is opt-in like the other sweeps (`SSQL_MATRIX=1`), and part
of the pre-tag gates.

**What it would have done here.** Adding the `inet` row to a matrix built
before DFC162 would have shown every cell the type did not reach, on day
one, in place of the planning agent's census. The seven bugs above sit
in the rows that already existed, so the matrix run against v4.137.2 is
its own acceptance test: it must show those seven as red, and they are
fixed in the tree now.

### 2.2 A domain lint for type switches (static)

The matrix finds a gap when it runs. A lint finds it when the switch is
written.

**Measured 2026-11-04:** 104 type switches in non-test code name at least
one of the struct scalars (`Int128`, `Decimal`, `time.Time`, `Inet`).
85 name only some of them. Most of those 85 are right to: a numeric switch
has no business with an address. So "every switch handles every type" is
the wrong rule; it would be noise, and it would be silenced.

**The rule: a switch declares its domain, and the lint checks it covers
it.**
- One table names the domains. `scalar` is every scalar type a record
  holds. `numeric` is int64, Int128, Decimal and float64. `ordered` is
  numeric plus string, time.Time and Inet. `exact` is int64, Int128 and
  Decimal.
- A type switch over a record value that names any struct scalar carries
  a comment: `// ssql:domain numeric`.
- The lint, a `go/analysis` pass run in `make vet`, checks four things:
  - each such switch carries a domain;
  - its cases cover every member of that domain;
  - its `default:` does not stringify (`fmt.Sprint`, `%v`,
    `mut.String`) when the domain is `scalar`;
  - the same rules hold for the codegen tables keyed by Go type name
    (`map[string]string{"ssql.Int128": …}`, `switch f.GoType`), which
    declare a domain the same way. Bug 4 was one of those.

**What a new type costs then.** Add it to the domain table, and the lint
names every switch and table that must handle it. That is the census DFC155
§7.6 and DFC162 §9 kept by hand, made mechanical. The 85 partial switches
are annotated once, and that annotation pass is itself an audit: each one
either names its domain or turns out to be a bug.

### 2.3 A smaller change to DFC133's random differential

The generator also *declares* column types: `-type col decimal:2`,
`int128`, `time`, `inet`, chosen per column. Each run includes one typed
program over a sample of its pipelines. This is cheap, and it reaches
interactions of declared types with multi-stage pipelines, which the
matrix's single-stage cells do not.

## 3. Recommendation

Build them in this order:
1. **The lint (2.2).** It is the cheapest; it is static, so it needs no
   fixtures and no lanes. It turns the census into a gate. The annotation
   pass over the 85 partial switches is a code review the codebase is
   owed.
2. **The matrix (2.1)**, batching the Go lanes with `generate go -func`.
3. **The declared-types generator (2.3).**

Acceptance: run the matrix against v4.137.2 (the release before DFC162's
fixes). It must be red for each of §1's seven bugs, and green on HEAD.

## 4. Questions for Ross

1. **Order:** lint first, as recommended, or the matrix first, since it
   finds behaviour rather than code?
2. **Annotation:** a comment on each switch (`// ssql:domain numeric`),
   or a call the lint can see (`domainSwitch(numeric, v)`)? The comment
   costs nothing at run time. The call cannot be forgotten silently, but
   it adds a line to hot paths.
3. **Gate placement:** the matrix in the pre-tag gates only, or on every
   push of a change to a type switch?

## 6. Decided and built: the lint (2026-11-05)

### 6.1 Ross's answers to §4

1. **The lint comes first.**
2. **The annotation is a comment.**
3. **The matrix runs in the pre-tag gates only.** It is not built yet.

### 6.2 What was built

- **`internal/lint/typedomain`** is a `go/analysis` pass, and
  **`internal/lint/cmd/typedomain`** is its `singlechecker` main.
  `make vet` runs it after `go vet`. `golang.org/x/tools` is now a
  direct dependency. The lint is not linked into ssql, so the slim
  build is unchanged.
- **The domains:**

  | Domain | Members |
  |---|---|
  | `wide` | Int128, Decimal |
  | `exact` | int64 + wide |
  | `numeric` | exact + float64 |
  | `ordered` | numeric + string, time.Time, Inet |
  | `scalar` | ordered + bool |

  `TestScalarDomainIsTheValueConstraint` reads `ssql.Value`'s terms
  through go/packages and fails if `scalar` differs from them. Adding a
  type to `Value` therefore forces the domain table to be updated, and
  `make vet` then names every switch that must handle the new type.
- **The grammar.** The comment goes on the line above the switch, or
  above the statement holding a map literal:
  - `// ssql:domain D`;
  - unions, such as `numeric+string+bool`;
  - `// ssql:domain fixed: <reason>` for a switch that is really about a
    fixed set. The reason is required.
- **Three shapes qualify.** A shape qualifies when it names a struct
  scalar (time.Time, Int128, Decimal or Inet), or the name of one:
  1. a Go type switch;
  2. a switch on an `ssql.FieldType`;
  3. a string table: a switch on a string, or a `map[string]…` literal,
     keyed by Go type names (`"ssql.Int128"`) or wire names
     (`"int128"`).

  The basic types alone never qualify, and neither does `"time"`
  alone.
- **Decimal is never a case label.** Its scale rides in the FieldType
  and in the wire name. So a FieldType or name switch counts as
  covering decimal when its function calls `IsDecimal` or
  `ParseWireType`, or tests a `"decimal"` prefix.
- **§2.2's "default must not stringify" rule was dropped.** A switch
  that declares `scalar` must name every scalar, so its default can
  never see one.

### 6.3 The annotation pass: 194 sites

The lint flagged 193 switches and tables, and one more was written
during the pass (`strictCastTarget`, bug 4 below). Each was read and
given a domain:
- **110 take a named domain** or a union of named domains:
  - `scalar` for every "every value" switch;
  - `numeric`, `exact` and `wide` for arithmetic;
  - unions where the switch's own arms are the contract.
- **84 are `fixed:` with a reason**, for example:
  - a time parse;
  - function names that happen to be type words (`decimal()`,
    `inet()`);
  - the sources of one cast target;
  - a resample axis.

The rule that emerged: **where a `default:` would silently do the wrong
thing for a new type, declare `scalar` and give `string` an explicit
arm.** That applies to type-to-name maps such as `auxTypeString`,
`duckColumnType`, `castTargetGoConst` and `arrowKindForWireType`. Only
then does a new type surface there.

The rehearsal: adding a dummy `uuid` member to `ordered` (and so to
`scalar`) makes `make vet` name **52 switches across 28 files**. That is
the census DFC155 §7.6 and DFC162 §9.3 kept by hand.

**Watched fail:**
- deleting the Inet arm from `setScalar` is named at `operations.go`;
- deleting `exprGoDecimal` from update's native setter table (bug 4's
  shape) is named at `update.go`;
- the analysistest cases cover a missing annotation, a partial
  `ordered`, a reasonless `fixed:`, an unknown domain, a FieldType
  switch, a map literal and a wire-name switch.

### 6.4 Bugs the audit found, and fixed

Each was checked against every lane and pinned by a test that was
watched to fail without the fix.

| # | Bug | Lanes | Fix | Test |
|---|---|---|---|---|
| 1 | Typed `group-by -string-agg` on a decimal or inet column imported `strconv` for a value printed by its own `String()`, and the program **did not compile** | typed | `typedAggImports` | `decimal_string_agg`, `inet_string_agg` |
| 2 | `window -lag/-lead/-first/-last/-nth-value/-min/-max` over an inet column printed DuckDB's **INET struct** (not JSON): window results had no recorded kind, so `sqlInetOutput` never cast them | duckdb | value-carrying window results take their field's kind in `sqlColumnKinds` | `inet_window_values` |
| 3 | `-lag-default ip 1 1.1.1.1` was an **error** on DuckDB, which casts no text to INET implicitly | duckdb | the default is `sqlLiteralFor` the field | `inet_window_values` |
| 4 | Typed `cast -type s decimal:2` of the text "abc" was a **silent 0**, where exec and record refuse it. The strict-cast target table had no decimal | typed | `strictCastTarget`, a switch with a decimal arm | `TestCastIsStrict` (decimal:2, int128, inet added) |
| 5 | `from jsonl -type ts string` over a time column wrote **Go's `Time.String()`** ("2026-01-31 12:00:00 +0000 UTC"), the DFC128 F3 shape. `coerceValue`'s string arm had no time | exec, record | the string target is `CastValue`'s text | `jsonl_type_time_as_string` |
| 6 | `StableKey` (behind `ssql.Hash`) keyed a time by `%v`, so one instant in two zones gave two keys | library | the UTC RFC 3339 instant | `TestDomainLintGaps` |
| 7 | `WriteJSONPretty` wrote a time by `%v` | library | RFC 3339, as `to jsonl` writes it | `TestDomainLintGaps` |
| 8 | `GetOr(r, f, false)` of a decimal failed to convert, where an int128 converts | library | a Decimal arm in `convertToBool` | `TestDomainLintGaps` |
| 9 | `TableFieldsFromRecords` without a header left a decimal column untyped | library | a Decimal arm | `TestDomainLintGaps` |
| 10 | A comparison message named a decimal `ssql.Decimal`, not "number" (`kindName`) | exec | a Decimal arm | `TestDomainLintGaps` |
| 11 | A chart did not see a decimal column as numeric (`isNumericValue`), though `getNumericValue` reads one | chart | a Decimal arm | `TestDomainLintGaps` |
| 12 | `serve`'s schema listing called an int128, decimal or inet column "string" | serve | `lib.InferTypeString`, which knows every scalar | none: display only |

### 6.5 Found beyond the type gaps; decided by Ross (2026-11-05)

Ross asked for all three in plan mode, and all three are built.

- **`int128(d)` of a decimal truncates toward zero, as `int()` and
  `cast` do.** It was an error in the VM and the transpiler.
  - The SQL form is `CAST(trunc(x) AS HUGEINT)`, because a bare CAST of
    a DECIMAL rounds.
  - The case `expr_int128_of_decimal_truncates` runs over a new
    `fractions.csv` (±2.75, 0.50, −0.49), so rounding and truncating
    give different answers. Watched fail: without `trunc`, DuckDB and
    Postgres disagree.
  - The two sites are now `numeric+string`.
- **A window clause's new columns come out in the written order, in
  every lane.** There were three causes:
  1. The exec parser collected the specs grouped by function.
     `parseWindowClauses` now collects them per flag and lays them out
     by `Clause.Order` (`windowSpecsInOrder`), as `clauseSets` does for
     `update`.
  2. `ssql.Window` added each row's results by ranging over a **map**,
     so the generated program's columns came out in map order. It now
     walks `windowResultNames`, in spec order.
  3. The streaming form (`-presorted`) applied the leads after every
     other result. It now applies all of them in spec order.

  `BenchmarkWindow*` interleaved against a HEAD worktree shows no
  regression. Cases `window_columns_in_written_order` and
  `window_presorted_columns_in_written_order` cover it, and
  `inet_window_values` is one mixed clause again. Watched fail:
  - the old parse order is caught by the DuckDB lane's column-order
    check;
  - the map walk and the leads-last emission are caught by the record
    lane.
- **`addJSONField` is deleted.** It had no caller.

After the pass and these three, the tree holds 193 annotations: 112
named and 81 `fixed:`, including the lint's own three tables.

### 6.6 Found by the lanes while gating this work

- **Postgres `string_agg` over an inet column** cast it to text, which
  adds `/32`. It now uses `abbrev`, as other inet text does
  (`inet_string_agg`).
- **`generate rust` read a schema-headed JSONL file's `_schema` line as
  a row of nulls**, and silently ignored `-type` on a JSON source (the
  datafusion dialect refuses `-type`).
  - A headed JSONL is now filtered and the `_schema` column dropped, as
    the DuckDB form does.
  - `-type` on a JSON source is refused under rust.

  `jsonl_type_override_agrees` had passed in rust only because ignoring
  `-type age float` happened to print the same numbers. It is now
  refused by design.

## 7. Related

- DFC133: the four instruments for unknown bugs; this is a fifth and a
  sixth.
- DFC155 §7.6, §8.6; DFC162 §9.3–9.4: the hand-kept census this replaces.
- DFC115: commands are the authority on themselves (the matrix reads
  `-spec-json`).
- DFC157 §5: one crate for every rust case, the model for the batched Go
  lanes.
- DFC136, DFC144: the `json` type's addendum, the same shape of bug.
