# Non-scalar field values: a survey, what DuckDB does, and the options

Reference: DFC144
Created: 2026-10-07
Last modified: 2026-10-08
Deprecates: [DFC052](./compound-types-investigation.md)

[Back to Index](./README.md)

## 0. The prompt

Ross, 2026-10-07: *"the next thing to look at is how we handle
non-scalar field types. In core.go `type Value` does allow for
non-scalar types but it's never really been used. We should look at
what DuckDB does and decide if it's worth such support. Let's do a
survey and write a doc on the possibilities. We want to make sure we
stay close to the ssql CLI style and philosophy of being easy to
complete and args having as little internal structure as possible."*

[DFC052](./compound-types-investigation.md) (2026-02-27) surveyed the
same ground and recommended two steps; neither was built, and the
repository has moved since (five lanes, the `_schema` header as the
type authority, typed codegen, SQL dialects, library mode). This
document replaces it: it re-establishes the facts by running the
binary, records what DuckDB does, and lays out the options against the
constraints Ross named. It decides nothing; §8 is the list of
decisions.

## 1. Where ssql stands today (measured on v4.112.0)

The one-line summary: **nested JSON survives the pipeline unchanged,
as text, and nothing can look inside it except by hand.**

### 1.1 The probe

```bash
printf '{"name":"Alice","tags":["go","rust"],"addr":{"city":"NYC","zip":"10001"},"scores":[10,20,30]}\n' > nested.jsonl
ssql from jsonl nested.jsonl | ssql to table
```
```
name    tags            addr                           scores
-----------------------------------------------------------------
Alice   ["go","rust"]   {"city":"NYC","zip":"10001"}   [10,20,30]
```

| What a user would try | Result today |
|---|---|
| `update -set-expr n 'len(tags)'` | `13` — the length of the text `["go","rust"]` |
| `update -set-expr first 'tags[0]'` | `91` — the byte `[` |
| `update -set-expr city 'addr.city'` | error: `invalid operation: int(string)` |
| `where -if-expr '"go" in tags'` | error: `operator "in" not defined on ssql.JSONString` |
| `group-by dept -collect name names \| update -set-expr n 'len(names)'` | `15` — a list one stage ago, text now |
| `to csv` | the JSON text, CSV-quoted: `"[""go"",""rust""]"` |
| `SSQL_MODE=schema` | every nested field is typed `any` |
| `generate go` (typed) | `Tags string`, `Addr string`, `Scores string` |
| `generate sql` of `-collect` | `LIST(name) AS names` — DuckDB gets a real list |
| the workaround that works | `len(fromJSON(string(tags)))` → `2`; `fromJSON(string(addr)).city` → `NYC` |

### 1.2 Three representations, chosen by the reader

The survey (file references in the appendix) found that *which* Go
value a nested field holds depends on how it arrived:

1. **The wire and `from jsonl`:** `ssql.JSONString`, the raw JSON text
   as a string type (`parseJSONValue`, `core.go:1714`: an array or
   object is captured by a balanced-bracket scan). Every stage's stdin
   is this path, so after one pipe hop every nested value is a
   `JSONString` whatever it was before.
2. **`from json` on a JSON *array* file:** `[]any` for arrays and a
   nested `ssql.Record` for objects (`lib/jsonl.go:74-107`), built by
   walking a Go map — so the nested record's key order is random
   (bug, §1.5).
3. **The legacy library reader `ssql.ReadJSON`:** `[]any` and
   `map[string]any`; `map[string]any` is not even in the `Value`
   constraint, but reaches records through `NewRecord(map)`.

Generated code takes yet another path: `from json` emits
`ssql.ReadJSONAuto`, which marshals nested values back to
`JSONString`. So exec and the generated program hold different Go types
for the same input, and the DuckDB lane, reading the file itself, holds
native `STRUCT`/`LIST`.

### 1.3 The schema header is inconsistent about it

`_schema` has a wire type `json` ("arrays, nested objects",
`lib/schema.go:20`), but it is emitted only when the writer's in-memory
value was a `[]any`/`Record`/`map`; a `JSONString` infers as `string`.
So `from json` on an array file declares `tags: json`, `from jsonl` on
the same data declares `tags: string`, and the type flips to `string`
after any intermediate stage. On the reading side `json` maps to
`FieldTypeAuto` and the declaration is ignored; `ParseFieldType` and
`cast` reject `json`; typed codegen refuses a `json` field outright
("cannot map to a Go type").

### 1.4 What exists that is list-shaped

- `group-by -collect F R` — the only command that produces a
  collection: `[]any` in memory, wire type `json`, `LIST()` /
  `array_agg()` in SQL. No typed form (record fallback), no `window`
  form.
- `group-by -string-agg F SEP R` — a delimiter-joined string.
- `group-by -expr` — inside the expression each field *is* the group's
  array (`len(x)` is the group size, `sort(x)[0]` works), but the result
  must be a scalar.
- The expression language (expr-lang) has the full array and map
  toolkit — `split`, `join`, `len`, `map`, `filter`, `sort`, `uniq`,
  `reduce`, `any/all`, `keys/values`, `fromJSON/toJSON`, `x in list`,
  `list[i]`, `a.b` — and it all works on values that are real Go
  arrays. A non-scalar *result* is stored as `fmt.Sprintf("%v")`:
  `split(a, ",")` becomes the text `[x y]`, a map becomes `map[p:1]`.
- Library-only: `DotFlatten`/`CrossFlatten` (flatten nested `Record`s
  to `prefix.key` columns; zip or cross-product `iter.Seq` fields) and
  `Materialize`. They do not recognise `[]any` or `JSONString`, and no
  CLI command exposes them.
- No `explode`/`unnest`, no `split`-to-rows, no `flatten`, no window
  `-collect`.

### 1.5 Bugs and hazards found on the way (independent of any decision)

- **(a)** `to json` (the pretty array writer) double-encodes nested
  values: `"t": "[\"x\"]"`. `to jsonl` and `to csv` emit the JSON.
- **(b)** `from json` on an array file gives a nested object a random
  key order (Go map walk into a `MutableRecord`).
- **(c)** `Record.Equal` panics on a `[]any` value (`!=` on an
  interface holding a slice, `core.go:705`).
- **(d)** `isSimpleValue` returns true for `[]any`, so grouping by an
  in-memory `[]any` key would panic in `GroupByFields`; masked in the
  CLI because a pipe hop makes it a `JSONString` first.
- **(e)** `isValueType` omits `[]any`, which the `Value` constraint
  includes.
- **(f)** The lanes disagree on the type a nested field holds (§1.2),
  and on `len`: exec counts characters of the text, DuckDB `length()`
  counts list elements.

## 2. What DuckDB does

DuckDB has two tiers, and the distinction is the useful lesson.

### 2.1 The typed nested tier: LIST, ARRAY, STRUCT, MAP, UNION

- **`LIST`**: variable length, one element type per column (`INTEGER[]`,
  `VARCHAR[][]`); literal `[1, 2, 3]` or `list_value(...)`; **1-based**
  indexing with negatives and slices (`l[1]`, `l[-1]`, `l[1:2]`); lists
  compare lexicographically. `ARRAY` is the fixed-length variant.
- **`STRUCT`**: fixed keys with one type each, the same keys in every
  row (that is what makes it vectorisable); literal `{'k': v}` or
  `struct_pack(k := v)`; access by dot `s.key`, by bracket `s['key']`
  or `struct_extract(s, 'key')`; `s.*` expands to columns.
- **`MAP`**: when keys vary per row. **`UNION`**: a tagged sum type.
- **Functions**: a complete `list_*` family (aliases `array_*`):
  `list_extract`, `list_contains`, `list_position`, `list_has_any/all`,
  `list_transform`, `list_filter`, `list_reduce` with lambdas
  (`lambda x: x + 1`, also `x -> x + 1`), `list_aggregate(l, 'sum')`,
  `list_sum/avg/min/max/count`, `list_distinct`, `list_sort`,
  `list_reverse`, `list_intersect`, `list_concat`/`||`, `flatten`,
  `list_zip`, `array_to_string`, `string_split`, `range`,
  `generate_series`, `unnest`. Aggregates `list()`/`array_agg()` build
  them.
- **`UNNEST`**: a list becomes one row per element (empty or NULL list:
  zero rows); a struct becomes columns; several lists side by side are
  zipped with NULL padding, not cross-producted; other columns are
  repeated per emitted row; `recursive := true` and `max_depth` go
  deeper; `keep_parent_names` prefixes struct keys.

### 2.2 The untyped tier: the JSON type

- A `JSON` logical type holds text whose structure may vary per row.
- Extraction by path: `j -> '$.a.b[0]'` (JSON), `j ->> '$.a'` (text),
  `json_extract`, `json_extract_string`, `json_value`, `json_exists`;
  JSONPath (`$.a.b[0]`, `$.arr[#-1]`, `$."field.name"`, `$.a[*]`) or
  JSONPointer (`/a/b/0`); the simplified `j.family`. Array indices here
  are **0-based** (unlike LIST).
- `json_structure(j)` infers a structure template;
  `json_transform(j, structure)` turns JSON into typed STRUCT/LIST
  (lenient; `_strict` variant errors); `json_keys`, `json_type`.

### 2.3 The bridge: auto-detection with a fallback

`read_json` samples the file and infers STRUCT/LIST types for nested
values up to `maximum_depth`; beyond that, or where rows disagree, the
column falls back to `JSON`. So a file whose `addr` always has `city`
and `zip` gets a STRUCT and `addr.city` just works; a field whose shape
varies stays JSON and needs `->`.

### 2.4 The other engines ssql translates to

Postgres: typed arrays (`int[]`, 1-based, `unnest()`, `array_agg`,
`cardinality`) and `jsonb` with `->`/`->>`/`#>` paths and
`jsonb_array_elements`; no STRUCT (composite types are heavier).
DataFusion: `LIST` with `array_*`/`list_*` functions and `unnest`;
struct support is partial; no JSON type. Anything ssql adds in the SQL
lane must have a spelling in all three or refuse by dialect, as the
aggregates do today.

## 3. The constraints

1. **CLI style (Ross).** Arguments are plain tokens with as little
   internal structure as possible, so Tab can complete them and a
   flag's meaning is visible without parsing. No comma lists, no
   `key:value` micro-syntax; repeated flags instead
   ([CLAUDE.md](../../CLAUDE.md)). The expression language is the one
   deliberate exception, quoted, with its own grammar.
2. **One semantics, five lanes.** exec, record codegen, typed codegen,
   parallel, SQL — plus the library form. A result-changing feature
   needs every lane or a loud refusal in the lanes that lack it, and a
   `TestPipelineEquivalence` case
   ([multimode-equivalence-testing](./multimode-equivalence-testing.md)).
3. **The schema header is the authority on types**
   ([DFC115](./dfc115_commands_are_the_authority.md)). Anything a
   downstream stage must know about a nested field — that it is a list,
   what its keys are — has to be sayable in `_schema` and in
   `SSQL_MODE=schema`, which is also what completion reads.
4. **Performance.** The JSONL wire is parsed at every stage boundary; a
   nested value costs a balanced-bracket scan today. Parsing it into a
   structure at every hop, for stages that never look inside, is the
   cost DFC052 Option A was rejected for. The typed lane's speed comes
   from flat structs and the fast positional JSONL decoder, which falls
   back to `encoding/json` for slice or nested fields.
5. **Scalar-first is a feature.** Most of ssql's value is in CSV-shaped
   work; the design must not make the flat case pay for the nested one.

## 4. The design space, as levels

Each level includes the ones before it.

### Level 0 — one representation, consistent types, no new semantics

- One in-memory form for a nested value: **`JSONString`**, everywhere.
  `from json` on an array file and `ssql.ReadJSON` stop producing
  `[]any`/`Record`/`map` (which also fixes the random key order), or
  canonicalise to `JSONString` at the record boundary. `-collect` keeps
  `[]any` inside the aggregator and emits a `JSONString`.
- `_schema` type **`json` means "a nested value"**, inferred from a
  `JSONString` too, preserved across hops, accepted by `ParseFieldType`,
  shown by `SSQL_MODE=schema` (today: `any`).
- Fix (a)–(f) of §1.5; `len` in SQL left as is (see Level 1).
- Typed codegen: a `json` field becomes a Go `string` field holding the
  text (it is today, by accident of inference; make it the rule) and
  `to csv`/`to table` print it as the CLI does.

Cost: small. Value: the lanes agree, the schema tells the truth, and
every later level has one representation to build on.

### Level 1 — expressions see nested values (DFC052 Option C, updated)

When a `where -if-expr`, `update -set-expr` or `group-by -expr`
environment is built, a `JSONString` is parsed once per record into the
Go values expr-lang works on (`[]any`, `map[string]any`), lazily and
only for fields the expression references (the compiled expression
knows its identifiers). Then, with no new syntax:

```bash
ssql where -if-expr '"go" in tags'
ssql update -set-expr n 'len(tags)' -set-expr first 'tags[0]' -set-expr city 'addr.city'
ssql update -set-expr top 'sort(scores)[-1]' -set-expr langs 'join(filter(tags, {# != "c"}), " ")'
```

A non-scalar **result** is stored as a `JSONString` with schema type
`json` (today: `%v` text), so `-set-expr s 'split(csv, ",")'` makes a
real list a later stage can `explode`.

Lanes: exec and record codegen share the VM and the environment
builder, so both get it at once. The typed lane's native transpiler
handles only scalars and already returns "untranspilable → VM
fallback" for anything else; that fallback covers this. SQL: `len` →
`length` becomes *correct* (DuckDB counts elements when the file was
read as a LIST), `x in list` → `list_contains`, `a.b` → STRUCT dot or
`->>` — a per-function table in `generate_sql_expr.go`, with refusals
where a dialect has no form. Equivalence cases on a nested fixture
would pin it.

Cost: one environment-builder change plus a result-coercion change plus
SQL table entries; the differential corpus for the transpiler
(`TestExprGoDifferential`) and equivalence cases. Value: the whole
expr-lang collection toolkit, which users already have the docs for,
starts working on JSON data.

### Level 2 — structural commands and paths as field names

Expressions change a value inside a row; they cannot change the number
of rows or the set of columns. Two structural operations cover what
DuckDB's `UNNEST` and `s.*` cover:

- **`explode FIELD`** — one row per list element, the field replaced by
  the element, other fields repeated; an empty or missing list gives no
  rows (DuckDB) or one row with null (`-keep-empty`). The inverse of
  `-collect`. Library: `Explode(field) Filter[Record, Record]`; typed:
  a `[]T` field once Level 3's typing exists, until then a record-mode
  stage (the planner's typed→Record boundary already exists).
- **`flatten FIELD`** — an object's keys become sibling columns
  `FIELD.key` (the library's `DotFlatten`, exposed); `-depth N`.
  `explode` on an object could do the same, as DuckDB's `unnest(struct)`
  does, but two verbs read better at the prompt.

And the grammar question Ross raised — how to *name* something inside a
value with no internal structure in the token:

- **Dotted paths as field names**: wherever a command takes a field
  (`select`, `where -if`, `sort`, `group-by`, `-sum`, `to table`),
  accept `addr.city`, and for list elements `tags.0` (dot-number, like
  JSONPointer's `/tags/0`), resolved at runtime on the `JSONString`. A
  path is one token, has no quoting problem at the shell, and is what
  every tool from jq to DuckDB uses. Completion stays honest because
  `SSQL_MODE=schema` would grow nested field names from the sample
  (`addr.city`, `addr.zip`, `tags` as `json`), so Tab offers the paths
  it has seen. Brackets (`tags[0]`) are left to expressions: they need
  shell quoting and are the one piece of internal structure the rule
  forbids.
- The alternative — separate flags (`-field addr -key city`) — is more
  flags for less, and does not compose into `sort`/`group-by`.

Lanes: `explode` → `UNNEST` (DuckDB), `unnest` (Postgres arrays,
`jsonb_array_elements` for jsonb), `unnest` (DataFusion); `flatten` →
`s.*`/`struct_extract` where the engine has a STRUCT, `->>` otherwise;
dotted-path fields → STRUCT dot / `->>`. Each needs an equivalence case
over a nested fixture.

### Level 3 — typed nested schemas (DuckDB's typed tier)

`_schema` types `list<int>`, `list<string>`, `struct{city:string,
zip:string}`; the sample infers them (DuckDB's `read_json` rule: same
keys and types across the sample, else `json`); typed codegen emits
`Tags []string`, `Addr struct{City string; Zip string}`; `-collect` gets
its typed form (`[]T`); Arrow and Parquet LIST/STRUCT columns map
directly instead of stringifying; the SQL lane can rely on STRUCT dot.

This is where the typed lane becomes fast on nested data and where
Parquet users stop losing their lists. It is also a second type system
in the schema header, a struct-inference rule to specify and test, a
typed `explode` (a `[]T` field to rows), and `cast -type tags list`
questions. Nothing in §1 needs it; a Parquet-with-lists workload would.

## 5. What each lane needs, per level

| | exec | record codegen | typed / parallel | SQL (DuckDB · PG · DF) | library form |
|---|---|---|---|---|---|
| L0 one representation, `json` type | canonicalise readers; schema infers `json` for `JSONString` | `ReadJSONAuto` already `JSONString` | `json` → `string` field, by rule | source read by the engine: unchanged | unchanged |
| L1 expressions see JSON | env builder parses referenced `JSONString`s; result → `JSONString` | same code path | transpiler: untranspilable → VM (exists) | function table: `len`→`length`, `in`→`list_contains`, `a.b`→dot / `->>`; refuse where no form | unchanged |
| L2 `explode`, `flatten`, paths | new filters; path resolution on `JSONString` | fragments calling the same filters | record-mode stage via the typed→Record boundary (exists) | `UNNEST` · `unnest`/`jsonb_array_elements` · `unnest`; `s.*` · `->>` · `struct_extract` | sinks dropped as usual |
| L3 typed nested schema | inference rule | — | `[]T`, nested structs, typed `explode`, typed `-collect` | STRUCT/LIST types assumed | row types gain slices |

The existing gates carry each level: `TestPipelineEquivalence` with a
nested fixture (shuffled, distinct values), `TestPipelineCorpus`,
`TestExprGoDifferential` for anything the transpiler touches, and a
scale case if the environment parsing shows up in `TestScaleBudgets`.

## 6. Is it worth it?

Arguments for stopping at Level 0: the CSV-shaped majority never meets
a nested value; the workaround `fromJSON(string(x))` exists; every
level above adds surface to five lanes.

Arguments for Levels 1 and 2: the data that arrives as JSON — API
exports, logs, `-collect` output — is the data people reach for ssql to
look at, and today the first question (`len(tags)`) gives a wrong
number silently, which is the failure mode ssql exists to prevent ("fail
loudly, never silently produce wrong results"). Level 1 is almost free
because expr-lang already does the work; Level 2's `explode` is the one
operation that turns nested data into the flat rows the rest of ssql is
good at, and `-collect` already produces its inverse. Together they make
`from json | explode tags | group-by tags -count n` a sentence.

Arguments about Level 3: real, but driven by Parquet/Arrow and the typed
lane's speed on nested data, which no current user has asked for.

## 6a. A position on the old dichotomy (2026-10-08)

Ross asked where this stands on the long-standing argument in database
design: should every value be scalar, with structure expressed through
further tables, or may a value itself be structured? The position this
document takes, and which §7 follows from:

**The dichotomy is real at the storage layer and mostly dissolves at the
pipeline layer, and ssql sits at the pipeline layer.** Codd's case for
first normal form was about data at rest, owned by the database and
mutated over time: a nested list cannot be indexed, joined on or
updated one element at a time without rewriting the row, and the
anomalies follow. Those arguments are strong there, and the nested
forms that won in practice, JSON columns and document stores, paid for
their convenience with exactly those anomalies. A normalised schema is
the honest shape of persistent data.

ssql owns nothing at rest. It reads a stream, transforms it, and
writes it out; its rows are a snapshot of a moment, not a model of the
world. A nested value in that setting is not a design choice someone
made badly, it is the shape the data arrived in, and the only question
is whether the tool can take it apart. So ssql does not need a view on
how databases should be designed. It needs the two operations that
move between the shapes: `explode`, which turns one row holding a list
into many scalar rows, and `-collect`, which comes back. Those are the
normalise and denormalise moves; a stream tool with both can meet data
in either form and hand it on flat, which is where ssql is good.

Inside a row, the scalar discipline should win. A list inside a field
is a second, weaker table with no name and no join key. The moment a
user wants to filter it, aggregate it or join on its elements, they
are better served by exploding it into rows and using the commands
that exist than by a parallel family of list functions. That is why
Level 2 (the structural commands) ranks above Level 3 (a typed nested
tier). DuckDB can afford both tiers because it is a database and
people keep nested columns in it for years; ssql's nested values are
transient, something to unpack early or carry through untouched.

The one place the scalar discipline must not win is identity. A nested
value that is passing through must survive unchanged, and the lanes
must agree on what it is. Flattening it on the way in, as some tools
do, destroys information and makes the output differ from the input
for a user who never asked. Carry it as one typed thing (`json`), let
the user open it when they need to, and keep the rest of the tool
scalar.

In three words: normalised at rest, scalar in the operators, honest
about the shape at the edges. Levels 0–2 are that position made
concrete; Level 3 is the point where ssql would start to be a
database, which is a reason to defer it rather than a reason it is
wrong.

## 7. Recommendation

1. **Level 0 now**, as a bug batch with no new semantics: one
   representation (`JSONString`), `json` as the schema type for every
   nested value and visible in `SSQL_MODE=schema`, the six bugs of §1.5.
2. **Level 1 next**: expressions parse referenced `JSONString`s lazily;
   non-scalar results stored as `json`; the SQL function table; an
   equivalence fixture with lists and objects. Document `fromJSON(string(x))`
   as no longer needed.
3. **Level 2 after**: `explode FIELD [-keep-empty]`, `flatten FIELD
   [-depth N]`, dotted paths accepted wherever a field name is, nested
   names from `SSQL_MODE=schema` for completion, `window -collect` and
   a typed `-collect` as the obvious gaps.
4. **Level 3 deferred** until a workload needs it; the schema grammar
   for it (`list<T>`, `struct{…}`) is the one thing worth deciding early
   so Level 0's `json` is its untyped fallback, exactly as DuckDB's
   `JSON` is the fallback for `read_json`'s inference.

## 8. Decisions for Ross

1. Scope: Level 0 only, 0+1, 0+1+2, or plan Level 3 too?
2. One in-memory representation: `JSONString` everywhere (recommended),
   even inside `ssql.ReadJSON`, whose structured output a test
   (`TestJSONComplexTypesRoundTrip`) calls a public contract?
3. Paths in field positions: dotted `addr.city` and `tags.0`
   (recommended), or expressions only?
4. `explode` on an empty list: zero rows (DuckDB) or one row with null?
   Default and flag.
5. Two verbs (`explode` for lists, `flatten` for objects) or one
   (`explode` doing both, as `unnest` does)?
6. Should `-set-expr` store a non-scalar result as `json` (recommended)
   or keep stringifying with a warning?
7. Level 3's schema spelling, decided now for later: `list<int>` /
   `struct{city:string}` or DuckDB's `INTEGER[]` / `STRUCT(city VARCHAR)`?

## 9. Related

- [DFC052](./compound-types-investigation.md) — the first survey
  (2026-02); its Options A–D and two phases are Levels 1–2 here.
- [DFC115](./dfc115_commands_are_the_authority.md) — the schema header
  and `SSQL_MODE=schema` as the authority completion and downstream
  stages read.
- [DFC129](./dfc129_groupby_aggregates.md) — `-collect`, "a JSON list
  per group".
- [typed-codegen-tier3-roadmap](./typed-codegen-tier3-roadmap.md) §
  typed `-collect`; [typed-parquet-proposal](./typed-parquet-proposal.md)
  "flat structs only".
- [multimode-equivalence-testing](./multimode-equivalence-testing.md) —
  the gate any level must pass.
- `doc/EXPRESSIONS.md` — the array and map functions users already have.

## Appendix: where the facts live (for the implementer)

- `Value` constraint `core.go:354-371`; `JSONString` `core.go:297-350`;
  `isValueType` `core.go:1914`; `isSimpleValue` `core.go:2622`;
  `Record.Equal` `core.go:705`; `RecordKey` `core.go:1150`.
- Wire reader `parseJSONValue` `core.go:1714-1765`,
  `parseJSONRawValue` `core.go:1846`; writer `appendJSONValue`
  `core.go:1251-1310`.
- `from json` array path `cmd/ssql/commands/from_json.go:383-409` →
  `lib/json.go:52-102` → `setValueFromJSON` `lib/jsonl.go:74-107`;
  generated `ReadJSONAuto` `io.go:2585-2611`.
- Schema types `lib/schema.go:15-23`, `InferTypeString`
  `lib/schema.go:278-293`, `SchemaTypeToFieldType` `:315-331`;
  `inferJSONType` `io.go:1051`; `FieldType` `io.go:51-114`.
- Expression environment `cmd/ssql/lib/runtime/runtime.go:127-131`;
  result coercion `cmd/ssql/commands/helpers.go:474-660`,
  `update.go:336-385`, `:817-838`; typed coercers
  `cmd/ssql/lib/runtime/env.go:110-155`; transpiler scalar types
  `cmd/ssql/commands/expr_go.go:33-40`.
- `-collect` `cmd/ssql/commands/group_by_specs.go:143`, `ssql.Collect`
  `sql.go:1816`, SQL `generate_sql_dialect.go:442-447`, typed fallback
  `group_by.go:727-733`; `group-by -expr` arrays `expr_agg.go:312-350`.
- `DotFlatten`/`CrossFlatten` `core.go:2356-2381`.
- Typed inference `cmd/ssql/lib/typed_schema.go:356-416`,
  `typed_schema_jsonl.go:104-136`; fast decoder fallback
  `typed/jsonl_fast.go:21-42, 283-292`.
- SQL expr table `cmd/ssql/commands/generate_sql_expr.go:59-64, 110-119`.
- Display `io.go:1400-1441`, `to json` double-encode
  `lib/jsonl.go:110-145`.
