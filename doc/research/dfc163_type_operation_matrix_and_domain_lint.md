# Finding Type Gaps Before Users Do: a Type × Operation Matrix and a Domain Lint

Reference: DFC163
Created: 2026-11-04
Last modified: 2026-11-04

[Back to Index](./README.md)

Status: **proposal, 2026-11-04.** Nothing is built.

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

## 5. Related

- DFC133: the four instruments for unknown bugs; this is a fifth and a
  sixth.
- DFC155 §7.6, §8.6; DFC162 §9.3–9.4: the hand-kept census this replaces.
- DFC115: commands are the authority on themselves (the matrix reads
  `-spec-json`).
- DFC157 §5: one crate for every rust case, the model for the batched Go
  lanes.
- DFC136, DFC144: the `json` type's addendum, the same shape of bug.
