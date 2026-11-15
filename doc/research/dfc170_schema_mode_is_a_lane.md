# Schema Mode Is a Lane: Making Its Predictions Exact

Reference: DFC170
Created: 2026-11-15
Last modified: 2026-11-15

[Back to Index](./README.md)

Status: built 2026-11-15, §6 included (Ross: "yes, type the expression results with the transpiler").

## 1. Why

`SSQL_MODE=schema` predicts each stage's output columns and types from a
header alone, without reading the data. Its consumers are:
- Ctrl-O completion;
- `generate schema`;
- `run`'s field check;
- the playground;
- next, the DFC169 FAQ's pre-flight, which leans on it harder than anything
  so far.

While writing DFC169 §4.0, `group-by department` (an unknown field) passed
silently in schema mode, though exec fails loudly. A probe of 18 commands
and a survey of the code showed this is a class of bug, not one bug. Ross:
"we need to make sure that schema has as few bugs as possible."

**Schema mode is the seventh implementation of the same semantics.** It is
a prediction of exec's output header. Unlike the other six (CLAUDE.md, "One
Semantics, Many Backends"), it had:
- no differential lane;
- no gate anyone had watched fail;
- a second, hand-written grammar.

DFC096 §9 designed the missing test, a shadow assertion comparing schema
mode with the runtime `_schema` over a corpus, in May 2026. It was never
built.

## 2. What the survey found (2026-11-15)

### 2.1 Unknown fields pass silently

Many commands start with `if schemaMode() { return
runSchemaModeTransform(ctx, "cmd") }`. That early return skips the
command's own `validateFieldsSchema`. Probed with the field `nope` after
`from employees.csv`, each of these answered quietly in schema mode where
exec failed with "unknown field(s)":
- group-by (group field and aggregate field)
- fill
- update `-if`
- exclude
- include
- window
- explode
- flatten
- describe

extract, resample, pivot, unpivot and rename share the shape. `where` and
`sort` have no early branch, run their exec handler on the header-only
stdin, and fail correctly.

### 2.2 "Unknown" is written as "empty"

pivot's op returns ok=false, because its columns come from data values.
`runSchemaModeTransform` then writes a header with `fields: []`. A nil
input does the same through an identity op. The next stage reads "known,
no fields":

```
SSQL_MODE=schema: from employees.csv | pivot -row dept -col status -val salary | where -if x eq 1
Error: where references unknown field(s): x (available: )
```

Exec already has the right convention:
- with no header, `ReadJSONLWithSchema` gives a nil schema;
- `validateFieldsSchema` passes on nil;
- `WriteJSONLWithSchema(nil)` writes nothing.

So "unknown" flows through as unknown. Schema mode broke the convention.
`TestSchemaModePipeline` asserts the broken pivot answer.

### 2.3 A second grammar, already drifted

Every schema op re-parses the command's raw arguments with a hand-written
flag→arity map (`walkStage`, `commands/schema_ops.go`; also the ops in
fill, explode, extract, flatten, resample, unpivot and describe). That is
the anti-pattern DFC115 names. Drift found by reading:
- update's `-s`, `-e` and `-b` aliases add no column, and `-p` is missing
  from the map;
- group-by's `-e` alias of `-expr` drops its result column;
- group-by's type op is a second copy of its name op's arity map;
- the rollup suffix-typing loop (`schema_mode.go:268`) gives a group key
  named `net_total` the type of a result named `total`.

The ops are also used in process: by serve's walk (`serve_schema.go`), and
by `generate sql` and `generate ssql`'s `fragmentFields`. In serve's walk,
join, union, traverse and fft are all treated as identity.

### 2.4 Sources and the signal commands

- **Full reads.** arrow, xlsx and wav have no schema branch, so they read
  every row. So do `from csv/tsv/jsonl -sample` (the comment at
  `from_csv.go:314` claims otherwise) and multi-file csv/tsv/jsonl.
- **Multi-file pushdown is broken.** For `from csv a b -- stages`, the
  child processes inherit schema mode and emit only headers, which the
  parent drops; the parent then writes nothing.
- **Signal commands.** fft, ifft, convolve, correlate and spectrogram write
  no header when they get zero records, so everything downstream of them
  is "unknown".

### 2.5 Types

Exec knows these types, and schema mode says "any":
- window results (`inferWindowResultType`);
- update literals, `-set-field` copies and `-set-bucket`;
- fill `-default`;
- extract captures;
- unpivot and resample outputs;
- explode elements;
- `from lines`.

### 2.6 Tests

None of the 432 `TestPipelineEquivalence` cases checks the schema. The
exec lane throws its header away.

### 2.7 The model to follow

`checkPipelineFields` (`pipeline_doc.go:572`, used by `run` and the LSP)
runs every stage after the source in **plain exec** on a header-only stdin.
Each command uses its own grammar and its own validation. Most transforms
already write the correct header with zero records: where, sort, cast,
join, union, traverse and top. **A command answers schema mode with its own
handler, from its parsed configuration.**

## 3. Principle

Schema mode is a lane. A command:
1. validates its fields against the incoming header exactly as exec does;
2. answers from the same parsed configuration exec uses;
3. says "unknown" by writing no header.

A hand-written parse of a command's arguments is a second implementation.
Where one must stay, for the in-process consumers, a test holds it equal to
the command's own answer.

## 4. Plan

1. **Oracles first, each watched failing on the unfixed tree.**
   - A schema lane in `TestPipelineEquivalence`: predicted names and types
     against the header exec wrote. Gaps go in a `schemaKnown` list, like
     `matrixKnown`.
   - `TestSchemaModeRejectsUnknownFields`: every field-taking flag argument
     from `-spec-json`, given `nope`. Schema mode must error exactly when
     exec does.
   - A shadow test holding the in-process ops equal to schema mode.
2. **"Unknown" is a missing header.** ok=false or a nil input writes
   nothing. `generate schema` reports "undetermined" on stderr.
3. **Validate, then answer.** The early branch moves to after each
   command's existing `validateFieldsSchema`, via `answerSchemaMode(ctx,
   cmd, schema)`.
4. **The command is the authority.** Each transform builds its output
   header from its parsed config, in exec and schema mode alike. The hand
   arity maps go. The drift fixes in §2.3 fall out of that.
5. **Sources and signal commands.**
   - Schema branches for arrow, xlsx and wav, and for `-sample` and
     multi-file sources.
   - Multi-file pushdown answers in the parent.
   - The five signal commands write their header with zero rows.
   - A scale-gate case per source, so a source that reads all its data
     fails the budget.
6. **Types.** Close the "any" gaps with the functions exec calls.
7. **Docs.** CHANGELOG, a CLAUDE.md rule, the journal, and §5 "Built"
   below.

Out of scope: replacing serve's raw-argument walk with the grammar's own
parse. The shadow test keeps it honest until then.

## 5. Built (2026-11-15)

### 5.1 The gates, each watched failing on the unfixed tree

| Gate | What it compares | On the unfixed tree |
|---|---|---|
| `TestSchemaModeMatchesExec` (`cmd/ssql/schema_lane_test.go`) | Every equivalence case: the predicted header against the header exec wrote (names in order; a type both know) | 169 of 432 cases red; 22 were real wrong answers, the rest "any" gaps |
| `TestSchemaModeRejectsUnknownFields` (`schema_unknown_field_test.go`) | Every field-taking flag from `-spec-json`, given a missing column: schema mode refuses it exactly when exec does | 67 red: 61 silent passes, plus 6 exec bugs (below) |
| `TestSchemaOpsShadowSchemaMode` (`schema_shadow_test.go`) | The hand ops serve's walk uses, stage by stage, against schema mode | 41 red: join and traverse as identity, `-arg` ignored, include paths, group-by order |

Two lists hold the line between fixes:
- **`schemaKnown`.** Strict differences, each with its reason; a listed case
  that starts agreeing fails. It went from 22 entries to empty.
- **`schemaAnyCeiling`.** The columns predicted "any" where exec knows the
  type, a ratchet that may only fall. It went from 356 to 178.

### 5.2 How schema mode answers now

| How | Commands |
|---|---|
| **No branch.** Exec runs on the header-only input and writes its own header from its parsed config | include, exclude, rename, fill, describe, extract, unpivot, resample, explode, pivot (pivot writes none: unknown); where, sort, cast, join and the rest as before |
| **Exec's header code, with "any" for what the first row would type** | group-by (`groupByOutputSchema`), window (`windowOutputSchema`), update (the header exec builds before its first-row refinement) |
| **A hand op (`answerSchemaMode`)** | flatten (its new columns come from the data), tee and the `to` sinks (identity) |
| **Sources.** Header from the sample, then stop, centrally in `writeWithInferredSchema` and the multi-file path | arrow, xlsx, wav, `-sample`, file lists. `from lines` is typed. jsonl `-type` is applied. Multi-file pushdown runs its stages once on the first file. |
| **Signal commands.** Their own code runs on a zero signal (`schemaModeSignal`) | fft, ifft, convolve, correlate, spectrogram |

Unknown is no header (`answerSchemaMode`). After an unknown input, only
group-by and describe answer, because their arguments decide their columns.

### 5.3 Exec bugs found by the schema gates

- **convolve, correlate and ifft accepted a missing column**, in exec and in
  record and typed code. They printed a signal of zeros with exit 0. They
  now check against the header, and `ssql.RequireFields` at run time, as
  fft does.
- **`update -set-bucket`'s column was checked only at the first row.** It is
  now checked against the header with the other columns update reads.

### 5.4 Measured

Over 3 M rows, an arrow source followed by `sort` in schema mode took
3.06 s and 481 MB on v4.147.0. It now takes 0.10 s and 70 MB.

Two files followed by `sort` took 13 s; they now take 0.02 s. Both are
scale-gate cases (`schema-mode-*-then-sort`).

## 6. Expression result types (built 2026-11-15)

Exec types an expression's result from the first row it computes, which
schema mode doesn't have. Ross chose the transpiler as the stand-in.

**How it works.** `exprStaticWireType` (`commands/schema_mode.go`):
1. It makes a typed schema from the input header's known columns.
2. It transpiles the expression with `exprToGo`, the typed codegen
   transpiler, which `TestExprGoDifferential` holds to the VM.
3. It maps the result's Go type to a wire type (`exprGoWireType`).

The answer is "any" when the transpiler refuses, when the expression reads a
column of unknown type, or when the clauses of one update give the field
different types.

**Where it applies.** `update -set-expr` only:
- `group-by -expr` and window `-stream-expr` folds compute over a group's
  values, which the transpiler doesn't cover (typed codegen sends those back
  to the record path);
- explode's element type needs a row.

**Result.**
- `schemaAnyCeiling` went from 178 to 62, with no conflicting type in any
  of the 432 cases.
- A sabotage that mapped int to float was caught in 23 cases.
- The ratchet now logs, rather than fails, when the count falls: one run
  counted 61 and eleven counted 62, most likely a case skipped because exec
  failed once.

**What the 62 remaining "any" columns are:**
- group-by `-expr` results;
- window folds;
- explode elements;
- a few expressions over columns of unknown type.
