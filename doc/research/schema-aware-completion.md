# Pipeline-Aware Completion via `SSQLGO=schema`

**Status:** Design — not yet implemented. Discussion captured 2026-05-20 (W21 Wed), immediately after shipping Position 2 pipes (v4.45.0 / shell v0.3.1). **De-risk spike run 2026-06-16 (W25 Tue)** — see §0 for verified reality, corrections to this doc, and decisions taken. Read §0 before implementing; several inline sketches below (the §4.3 table, the §7 API names) were written from memory and are corrected there.

## 0. Spike findings & decisions (2026-06-16)

Two parallel investigations verified the load-bearing assumptions against the real code (ssql commands + autocli). Verdict: **proceed — architecture is sound, but the doc is wrong in specific, implementation-breaking ways, and Phase 1 is bigger than originally framed.**

### Decisions taken

- **Env var: `SSQL_MODE=schema`** (not `SSQLGO=schema`). Chosen as an umbrella var. Implication: `SSQL_MODE` becomes the canonical mode selector for *all* modes; the existing `SSQLGO=record/typed/parallel` migrate to `SSQL_MODE=record/typed/parallel`, with **`SSQLGO` kept as a deprecated alias** for back-compat (mirrors how `SSQLGO=1/true` already alias `record`). This migration is its own slice of work — it touches the corpus tests, CLAUDE.md, and every doc that names `SSQLGO`. Supersedes §5.1, §6, §12.5.
- **Scope: bundle Phase 1 + Phase 2** (serve + bash) in one release arc. The per-command `SchemaOp`s are shared between both runtimes, so the marginal cost over serve-only is the bash plumbing (`SSQL_MODE=schema` wrapper, `generate schema`, the bash shim). Revised estimate **≈5–6 days**.

### Part A — SchemaOp separability: HOLDS, with caveats

Confirmed sound: every schema-mutating command builds an explicit `*lib.Schema` *before* and *independent of* record iteration. `pivot` is the only genuinely data-dependent case (correctly `ok=false`). **Key de-risk:** `(*cf.Command).Parse(args)` is reusable and runs `matchPositionals`+`applyDefaults` without invoking the handler — so a SchemaOp does **not** hand-parse argv; it calls `Parse` and reads the same `ctx.GlobalFlags`/`ctx.Clauses` the handler does.

Corrections to §4.3 (the table there is wrong in three rows — fix before implementing):
- **`rename`** uses `-as old new` (accumulated), **not** positional `old new` pairs.
- **`window`** has **no `-into` flag**. It has ~17 result-name-bearing flags (`-row-number`, `-sum field result`, `-lag field n result`, …) and infers real result types via `inferWindowResultType` (window.go:558). Not "identity-ish."
- **`cast`** emits **no runtime schema at all** (uses `ReadJSONL`/`WriteJSONL`, no `_schema`). A SchemaOp must *synthesize* the type-replace, and the §9 corpus shadow-test has **no runtime `_schema` to compare against** — cast needs `SkipSchema` or a different assertion.

Estimate reality: "5–15 lines each" holds for `rename`/`include`/`exclude`/`update`/`cast`, but **`group-by` (~25–40 lines: distinct/standard/rollup/cube sub-shapes) and `join` (~20–40 lines) are the risk commands**. `join` is **not** a pure `(inputFields, args)` function — it must do I/O on the right file's `_schema` header and return `ok=false` on a `/dev/fd/N` process-substitution right side. Budget group-by and join at ~½ day each.

Pre-req refactor: group-by parses its agg flags ~140 lines, **twice** (handler exec + `generate*Code`). Extract a shared `parseAggSpecs(ctx)` helper **first**, or the SchemaOp becomes a third divergent copy that the shadow-test must police (aligns with the repo's "Refactor While You Work" rule). **✅ Done (slice 2):** `cmd/ssql/commands/group_by_specs.go` now holds `parseGroupBySpecs(ctx)` (+ `parseAggSpecs`, the package-level `streamExprSpec`, and a `groupBySpecs` struct); both call sites decode once. The group-by SchemaOp (slice 5) is now a third *caller*, not a third copy.

### Part B — autocli completion surface: sound strategy, every API name in §7 is fictional

The *approach* works: `Completer` is a clean one-method interface (`Complete(ctx CompletionContext) ([]string, error)`), a new `UpstreamFieldsCompleter` is a drop-in, and the existing `ChainCompleter` gives "try upstream fields, fall back to file" for free. But the §7 code block must be rewritten — corrections:
- **`cli.CompleteWithContext(...)` does not exist.** Real entry point: `(*cf.Command).Complete(args []string, pos int) ([]string, error)` (completion_script.go:282) — **no injectable context**; it builds `CompletionContext` internally via `analyzeCompletionContext`.
- **`ctx.UpstreamFields` and `ctx.State` do not exist** on `CompletionContext` (completion.go:16). Both are net-new fields.
- **The completion path has no `State` analog.** Runtime `cf.Context.State` (flag.go:114) never reaches completion. Adding it is genuinely new.
- **`tabComplete` throws away everything before the last `|`** today (shell.go:405-410) — the per-stage schema walk is entirely unwritten. `splitOnPipe` already exists in shell/pipeline.go to build on.

Two things the doc missed:
- **Layering**: `shell` is ssql-agnostic and **cannot** resolve ssql's `SchemaOp`s directly (§7's `lookupSchemaOp(cli, stage[0])` is wrong). Shell needs a **generic callback hook** (e.g. an `Options.SchemaWalk func(stages [][]string) []Field`); ssql supplies the walker. This is the single biggest implementation risk.
- **Release order**: this is **not** "shell-only." It requires **autocli core (≈v4.7.0)** — new `CompleteWithContext(args, pos, seed)` + `CompletionContext.{UpstreamFields,State}` — then **shell (≈v0.4.0)** (the `tabComplete` rewrite + `SchemaWalk` hook + `opts.State` plumbed into `tabComplete`), then **ssql**. Core must tag first; shell can't reference an untagged API. Supersedes the §11 "shell v0.4.0 + ssql" framing.

`Field{Name,Type}` is an ssql concept — keep it out of autocli core (core carries field *names* or an `any`/neutral shape; ssql owns the typed `Field` and the `UpstreamFieldsCompleter`).

## 1. The problem

Tab completion's job is to answer "what field name could go here?" For trivial pipelines this is well-handled by `FieldsFromFlag("FILE")` — the completer peeks at the file referenced in the same command and offers its header columns.

It falls apart as soon as a transformation rewrites the schema:

```bash
$ ssql from data.csv | ssql rename name=person | ssql group-by <TAB>
```

The user wants to see `person` (renamed) and the other survivor fields. The current completer for `group-by` sees only its own argv — no upstream context — so it can offer only what was on the original `data.csv` header, plus or minus whatever its own flag history scraped together. The user gets `name` (wrong) instead of `person` (right).

The same shape applies to `from-loaded | <TAB>` inside `ssql serve`. The shell session has loaded a dataset whose schema is known to the *server* but invisible to the completer running inside an individual command.

## 2. The core idea

Make schema flow through a pipeline the same way *code* does in `SSQLGO=record` mode:

- Each ssql command, when running under `SSQLGO=schema`, ignores its data path and instead reads an *input schema* from stdin, applies its own rule (e.g. "rename adds the new name, removes the old"), and writes the *output schema* to stdout.
- A terminal `ssql generate schema` consumes the last stage's output schema and emits the field list in a form completion can use (JSON or one-per-line).
- Cost is ~zero — no data is read, transformed, or written. The pipeline becomes a chain of schema-transform stages, each finishing in microseconds.

In `bash`, the completion script `eval`s the upstream pipeline in this mode and parses the result. In `autocli-shell`, the same per-command rules are called in-process — no subprocess, the upstream is invisible to the user, and completion is instant.

Same rules, two runtimes. Single source of truth.

## 3. Two implementations, one rule

Each command declares a single function:

```go
// SchemaOp returns the output schema given the upstream fields and
// the command's own argv. It is total: never panics, returns nil
// only when the schema is genuinely undeterminable (e.g. pivot, where
// the column set depends on data values).
type SchemaOp func(inputFields []Field, args []string) (outputFields []Field, ok bool)

type Field struct {
    Name string
    Type string // "string", "int", "float", "bool", "time", "any"
}
```

Two runtimes consume the same `SchemaOp`:

1. **`SSQLGO=schema` subprocess mode** (for `bash`): a thin generic wrapper in `cmd/ssql/lib/schemamode.go` is invoked at the top of every command. It reads the upstream schema header from `ctx.Stdin()`, calls `SchemaOp`, writes the result to `ctx.Stdout()`, and `return`s. No data flow.

2. **autocli-shell in-process mode**: `tabComplete` walks the line from left to right, threading `inputFields` through each stage's `SchemaOp`. The current stage's input schema becomes part of `CompletionContext`. Completers read it instead of falling back to `FieldsFromFlag`.

Commands without an explicit `SchemaOp` are treated as identity (`inputFields, true`). That's safe for `where`, `sort`, `limit`, `offset`, `top`, `distinct`, `:set` (the boring case).

## 4. Per-command rules

### 4.1 Sources

| Command | Rule |
|---|---|
| `from data.csv` | Read CSV header, infer types from first row (existing logic, reused) |
| `from data.jsonl` | Read first `_schema` header line if present; else `nil, false` |
| `from data.parquet` | Read Parquet schema metadata (existing logic) |
| `from data.arrow` | Read IPC schema metadata (existing logic) |
| `from-loaded` (serve) | Read `srv.schema` from `ctx.State.(*serveState)` |
| `from-catalog` | Read catalog CSV's `_schema` column or peek at first shard |

Sources receive no `inputFields`. They peek at external state. For `from FILE` we already have the inference helpers; they just need a public entry point.

### 4.2 Identity transforms

`where`, `sort`, `limit`, `offset`, `top`, `distinct`, `head`. Default rule: `return inputFields, true`.

### 4.3 Schema-mutating transforms

> ⚠ **Corrected in §0.** The `rename`, `window`, and `cast` rows below are inaccurate (verified 2026-06-16): `rename` uses `-as old new`, `window` has no `-into`, `cast` emits no runtime schema. `group-by`/`join` are larger than the "5–15 line" framing. Use §0 as the spec.

| Command | Rule |
|---|---|
| `rename old1 new1 old2 new2 …` | Walk pairs; for each `old`, find in `inputFields`, replace name with `new`. Order preserved. |
| `include a b c` | Filter `inputFields` to those in `{a,b,c}`, in argument order. |
| `exclude a b` | Filter `inputFields` to those NOT in `{a,b}`. |
| `cast field type` | Replace `field`'s type in `inputFields`. |
| `update -set new=expr` | Append `{Name: new, Type: <inferred>}` to `inputFields`. Type from expr static analysis (best effort; default `any`). |
| `update -set old=expr` | Replace `old`'s type, leave others. |
| `group-by f1 f2 … -count n -sum salary total -avg age age_mean` | Output = `[f1, f2, …]` (group keys) + accumulator-output fields. Names come from the flag args. |
| `pivot key value` | **Undeterminable.** Return `nil, false`. Downstream falls back to file-based or empty completion. |
| `window` | If `-into NEW`, append `{NEW, "any"}`. Else identity. |
| `select expr1 expr2 …` | Parse each expr for output name + type. |
| `join LEFT_FILE LEFT_FIELD RIGHT_FILE RIGHT_FIELD` | Concatenate left and right schemas, deduplicate join keys. |

For each, the rule is a 5–15 line function. Spot-checking the existing command implementations, the schema transformations are already obvious in the runtime code — we're just extracting them.

### 4.4 Sinks

`to table`, `to csv`, `to json`, `to jsonl`, etc. Return `nil` (no downstream consumer). These rarely appear as upstream context for completion since they're terminal.

## 5. Wire format

A schema header on the wire is one JSON object per line, the same shape JSONL schema headers already use:

```json
{"_schema":[{"name":"dept","type":"string"},{"name":"salary","type":"int"}]}
```

The existing `lib.WriteJSONLWithSchema` / `lib.ReadJSONLWithSchema` already handle this format. `SSQLGO=schema` mode reuses them — it writes the schema header and *nothing else*. Downstream stages read that one line and their `ReadJSONLWithSchema` returns immediately.

### 5.1 Terminal command: `ssql generate schema`

Reads the schema header from stdin, emits the field list:

```bash
$ SSQLGO=schema ssql from data.csv | ssql rename name=person | ssql generate schema
person
dept
salary
```

Default output: one field name per line, in schema order. With `-format json`:

```json
[{"name":"person","type":"string"},{"name":"dept","type":"string"},{"name":"salary","type":"int"}]
```

The `bash` completion script uses the line format. The autocli-shell calls the underlying rules directly and never invokes this command.

## 6. Bash completion integration

The completion shim (`scripts/ssql-completion.bash` or equivalent) on every TAB press:

1. Parse `COMP_LINE` and `COMP_POINT` to locate the *upstream pipeline* — everything before the current command on this logical line.
2. Run `SSQLGO=schema <upstream> | ssql generate schema 2>/dev/null` with a short timeout (e.g. 200 ms).
3. Cache the result keyed on the upstream string for the duration of this shell — schema rarely changes during interactive editing.
4. Feed the resulting field list to `compgen -W` for the current flag.

Pseudocode:

```bash
_ssql_complete() {
    local cur prev upstream fields
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    # Extract upstream pipeline (split on |, take everything before
    # the current segment).
    upstream=$(_ssql_upstream_pipeline "$COMP_LINE" "$COMP_POINT")

    if [[ -n "$upstream" ]] && _is_field_flag "$prev"; then
        fields=$(SSQLGO=schema eval "$upstream" | ssql generate schema 2>/dev/null)
        COMPREPLY=( $(compgen -W "$fields" -- "$cur") )
        return
    fi

    # Existing behaviour for non-pipeline cases.
    ...
}
```

Latency: with no I/O, every stage's `SSQLGO=schema` returns in <1 ms once warm. End-to-end for a 4-stage pipeline: ~5–10 ms cold (process startup dominates), <5 ms warm. Well under the perceptual threshold.

### 6.1 Failure modes

| Failure | Behaviour |
|---|---|
| Upstream pipeline has a syntax error | `2>/dev/null` swallows; `compgen` falls back to empty. User-facing effect: same as today. |
| One stage's `SchemaOp` returns `nil, false` (e.g. `pivot`) | The schema-mode wrapper writes a *poisoned* header `{"_schema":null}`. Downstream stages propagate `nil`. `generate schema` exits 0 with no output. Same fallback. |
| Source file does not exist | Source command's schema-mode wrapper exits 1 with the same error it would in real execution. `2>/dev/null` swallows. |
| Timeout (200ms) exceeded | `timeout` kills the subprocess; `compgen` sees nothing. |

In every case, the completion script falls back to the existing per-flag completer — never worse than today, often better.

## 7. autocli-shell integration

> ⚠ **API names below are fictional — corrected in §0.** `CompleteWithContext`, `ctx.UpstreamFields`, `ctx.State`, and `lookupSchemaOp(cli, …)` do not exist as written. Real entry point is `Command.Complete(args, pos)`; shell cannot resolve ssql `SchemaOp`s directly (needs a generic `SchemaWalk` callback); and autocli **core** must release before shell. Treat the block below as intent, not API.

The shell is the cleaner case. `tabComplete` already has the whole line. It does:

```go
func tabComplete(cli *cf.Command, line string, pos int, w io.Writer, listSink io.Writer) (string, int, bool) {
    args, partialStart := tokenizePartial(line[:pos])
    // ... existing trailing-whitespace handling ...

    // Split into stages.
    stages := splitOnPipeAllowingFinal(args)  // like splitOnPipe but lets the final stage be partial

    // Walk left-to-right computing schema.
    var inputFields []Field
    for i, stage := range stages[:len(stages)-1] {
        op := lookupSchemaOp(cli, stage[0])
        if op == nil {
            inputFields = inputFields  // identity default
            continue
        }
        out, ok := op(inputFields, stage[1:])
        if !ok {
            inputFields = nil  // poisoned; downstream gets empty
            break
        }
        inputFields = out
    }

    // The current stage is stages[len-1] (possibly partial).
    // Attach inputFields to CompletionContext so the current command's
    // completer can use it.
    ctx := CompletionContext{
        // ... existing fields ...
        UpstreamFields: inputFields,
    }
    completions, err := cli.CompleteWithContext(stages[len(stages)-1], len(stages[len(stages)-1]), ctx)
    // ... existing match/render logic ...
}
```

A new completer type, `UpstreamFieldsCompleter`, reads `ctx.UpstreamFields` instead of `FieldsFromFlag`'s file. Commands register either or both — if `UpstreamFields` is empty, fall back to the file-based one.

For `ssql serve`, the `from-loaded` source's `SchemaOp` returns `srv.schema` directly via `ctx.State`. Subsequent stages compose normally.

### 7.1 State threading

`CompletionContext` needs access to the same `State` that runtime commands see. Two ways:

- **Option A**: add `State any` to `CompletionContext`. `tabComplete` populates it from `opts.State`. Source commands like `from-loaded` use it in their `SchemaOp`.
- **Option B**: source commands' `SchemaOp` is a closure that captures state at registration time.

Option A is more flexible (per-session state changes propagate) and matches the runtime `cf.Context.State` pattern. Recommend it.

## 8. Where the rules live

Each command's source file gets a `<cmdname>_schema.go` companion (or a `SchemaOp` constant in the existing file):

```
cmd/ssql/commands/where.go         (runtime, unchanged)
cmd/ssql/commands/where_schema.go  (new: identity SchemaOp)
cmd/ssql/commands/rename.go
cmd/ssql/commands/rename_schema.go (new)
...
```

A registry in `cmd/ssql/lib/schemaop.go`:

```go
package lib

var schemaOps = map[string]SchemaOp{}

func RegisterSchemaOp(name string, op SchemaOp) {
    schemaOps[name] = op
}

func LookupSchemaOp(name string) SchemaOp {
    if op, ok := schemaOps[name]; ok {
        return op
    }
    return IdentitySchemaOp  // safe default
}
```

The autocli-shell completer calls `LookupSchemaOp` directly. The `SSQLGO=schema` subprocess wrapper is a one-line helper that every command invokes at the top of its handler when the env var is set:

```go
// At the top of every data-processing handler:
if lib.IsSchemaMode() {
    return lib.RunSchemaOp(ctx, "where", schemaOpForWhere)
}
```

`lib.RunSchemaOp` reads the input schema, calls the op, writes the result, returns nil. Idiomatic to the existing `shouldGenerate(generate)` pattern.

## 9. Testing strategy

The existing pipeline corpus (`cmd/ssql/corpus_test.go`) is the natural place to add schema-mode coverage. Each corpus pipeline runs three modes today (`record`, `typed`, `parallel`). Add a fourth: **`schema`**.

For each pipeline, the test:

1. Runs the pipeline with `SSQLGO=schema` and pipes through `ssql generate schema`.
2. Compares output to a checked-in `<name>.schema.txt` golden, or computes the expected schema from the runtime output's `_schema` header.

The second form is the cheaper one — it makes schema mode a "shadow assertion" on every existing pipeline. If `SSQLGO=schema` says `[a, b, c]` and the real run produces a `_schema` header listing `[a, b, c]`, they agree.

Pseudocode:

```go
func TestPipelineCorpusSchemaShadow(t *testing.T) {
    for _, p := range corpus {
        if p.SkipSchema { continue }
        runtimeSchema := runAndExtractSchemaHeader(t, p.Pipeline)
        schemaSchema  := runWithSSQLGOSchema(t, p.Pipeline)
        if !slicesEqual(runtimeSchema, schemaSchema) {
            t.Errorf("%s: runtime schema=%v, schema-mode=%v", p.Name, runtimeSchema, schemaSchema)
        }
    }
}
```

This catches every drift between the rule and the runtime, automatically, across the whole corpus.

Pipelines with `pivot` or other undeterminable transforms set `SkipSchema=true`.

## 10. Out of scope (for v1)

- **Cross-pipe schema inference for process substitution.** `<(ssql from foo)` is its own subprocess; the parent's completion can't see inside it. Punt.
- **Schema inference for `select expr`**. Parsing expression syntax to derive output field name + type is substantial; v1 emits `{Name: <argv>, Type: "any"}` and lets the user supply explicit names.
- **Type inference through arithmetic**. `update -set total=salary*1.1` could produce `float` but v1 returns `any`.
- **Glob expansion** (`from "shards/*.parquet"`). v1 takes the schema of the first matching file and assumes uniformity. Bad data → bad completion; same as `from-multi` runtime.

## 11. Implementation phases

> ⚠ **Revised in §0.** Phases 1+2 are now **bundled** (one release arc, ≈5–6 days). Phase 1 is **not** "shell-only": it needs an autocli **core** release (≈v4.7.0) before shell (≈v0.4.0), plus the `SchemaWalk` layering hook. The per-ten-commands estimate holds except **group-by and join (~½ day each)**. Sequence below stands as a logical decomposition; the release/effort framing is superseded by §0.

### Phase 1: shell-side only (no env var, no bash, no fragments)

Smallest useful step. Adds `SchemaOp` per command, in-process schema walk in `tabComplete`, `UpstreamFieldsCompleter` reading from `CompletionContext.UpstreamFields`. Shipped as autocli/shell v0.4.0 + ssql vX.Y.0.

User-visible: `from-loaded | rename name=person | group-by <TAB>` offers `person` in `ssql serve`. Bash unchanged.

Effort: ~1 day per ten commands. Maybe two days total including the framework changes in autocli.

### Phase 2: subprocess mode for bash

Add `SSQLGO=schema` env var, the `lib.IsSchemaMode()` + `lib.RunSchemaOp()` helpers, and `ssql generate schema`. Wire the bash completion shim. Same per-command rules already exist from Phase 1; this phase is purely the bash plumbing + a thin wrapper.

Effort: ~1 day, mostly in the bash shim and the `generate schema` command.

### Phase 3: corpus shadow tests

Add the `SchemaShadow` mode to `cmd/ssql/corpus_test.go`. Audit failures, mark `SkipSchema` where appropriate. Probably exposes 2–5 places where the rule disagrees with runtime — fix the rule.

Effort: ~half a day.

### Phase 4: source-side schema peeking helpers

Public APIs for `from foo.csv` / `from foo.parquet` schema discovery, factored out of the existing inference logic. Some of this already exists internally (`from` uses it at runtime); just needs a public surface.

Effort: ~half a day.

**Total**: ~4 days of focused work, spread across as many sessions as makes sense.

## 12. Open questions

1. **Caching in the bash shim.** Schema queries are pure functions of the upstream pipeline string. Trivial to cache in `~/.cache/ssql/schema-cache/`. Worth doing in Phase 2 or punt to a later perf pass?

2. **How does `from-loaded` participate in bash mode?** It only makes sense inside `ssql serve`'s shell. So either: (a) `from-loaded` is shell-only and never registered for bash mode, or (b) we don't care because bash users won't type it. Lean (a) — explicit `serveCommandTreeRegistration` already gates it.

3. **Does Phase 1 ship with v4.46.0 or later?** Phase 1 alone unlocks the serve case (which we want) but doesn't help bash users. Phase 1+2 together is the better story. Probably better to plan one combined release.

4. **What about `:var NAME` (saved intermediates) once we add them?** Each saved intermediate would have its own schema; `from-var NAME` would need to consult that. Defer until intermediates exist.

5. **[DECIDED — see §0] Env var.** Chosen: `SSQL_MODE=schema` (umbrella var), with `SSQLGO` retained as a deprecated alias and the existing modes migrating to `SSQL_MODE=record/typed/parallel`. The original options were:
   - `SSQLGO=schema` (consistent with siblings)
   - `SSQLSCHEMA=1` (orthogonal)
   - `SSQL_MODE=schema` (new umbrella var)

   Lean `SSQLGO=schema` for consistency — sibling modes already mean "different output for the same pipeline", which is exactly what schema mode does.

## 13. Why this is the right shape

- **Single source of truth.** Each command's schema rule lives next to its runtime, gets the same code review, breaks loudly when out of sync via the corpus shadow test.
- **Same machinery as code generation.** `SSQLGO=record` and `SSQLGO=schema` are sibling modes; if a developer understands one, they understand the pattern of the other.
- **Works for two execution environments.** Bash users get pipeline-aware completion for the first time. Shell users get it instantly (no subprocess).
- **Composes with future features.** When `:var` (saved intermediates) lands, `from-var` is just another source with its own schema. When optimiser rewrites move things around, the schema flow still works because each rewritten command still has its own `SchemaOp`.
- **Low marginal cost per command.** A new command added to ssql adds its `SchemaOp` (5–15 lines) and is automatically schema-mode-capable. The corpus shadow test catches drift on the next CI run.

## 14. References

- Pipeline-relevant fragments: `cmd/ssql/lib/codefragment.go` (the existing `CodeFragment` JSON wire format we may extend or sibling).
- Existing JSONL schema header: `cmd/ssql/lib/jsonl_schema.go` and `doc/research/jsonl-schema-header.md`.
- Completion infrastructure: `/home/rossc/src/autocli/completion.go` (`CompletionContext`, `Completer`, `CompletionFunc`).
- Pipeline corpus: `cmd/ssql/corpus_test.go`.
- Position 2 pipes (ships v4.45.0, 2026-05-20): `shell/pipeline.go`, `cmd/ssql/commands/serve_cli.go`.
