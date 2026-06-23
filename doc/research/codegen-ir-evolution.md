# Codegen: evolving the fragment system (and why *not* go/ast)

**Status:** Design exploration, 2026-06-23. Prompted by the recurring question
"should the code generator assemble an AST instead of strings?" — sharpened by
the v4.50.1 bug (two projection stages both named `included`, a string-codegen
variable-hygiene failure). Conclusion up front: **don't move to `go/ast`.** The
fragment-IR-plus-planner architecture is good; the soft spots are (a) variable
names are baked into strings rather than treated as bindings, and (b) the
`sql`/`ssql` backends *re-parse* the `Command` string because the structure was
thrown away upstream. Both are fixable incrementally, toward a small
**serializable, language-neutral IR**, keeping string templates as the readable
last mile.

## 1. What the system is today (accurately)

It is **not** naive string concatenation. It's a distributed fragment IR:

- Each `ssql` stage runs as its **own process** in codegen mode
  (`SSQL_MODE=record|typed`). It reads the upstream fragments from stdin,
  passes them through, and appends its own — a JSON stream of `CodeFragment`
  (`cmd/ssql/lib/codefragment.go`). `ssql generate go|sql|ssql` consumes the
  whole stream and assembles. Reads no data; instant.
- `CodeFragment` already carries real structure: `Var`/`Input` (variable
  threading), pre-rendered `Code` (a Go string), `Imports`, `StructDefs`,
  `Command` (the original ssql text), typed `Capabilities`
  (Shape/Accepts/Produces/SerialOnly), `Input/OutputTypedSchema`, and a **dual
  template** (`AltCodeIfSeq`/`AltCapabilitiesIfSeq`).
- The **planner** (`cmd/ssql/lib/planner`) does reach analysis over the
  `Capabilities` and atomically swaps `Code`+`Imports`+`Capabilities` between
  the parallel (`Stream[T]`, `ReadCSVParallel`, `HashJoinParallel`, …) and
  serial (`iter.Seq[T]`) forms.
- **Three backends from one stream:** `generate go` (assembler →
  `assembleTypedFragments` / the record assembler), `generate sql` (SQL
  assembler), `generate ssql` (the optimiser).

This is a good design. The serialization boundary (JSON over a pipe) is what
makes the per-process, reads-no-data property work, and the planner is genuine
IR-level analysis.

## 2. The two real weaknesses

### 2a. Variable names are strings, not bindings
Commands render output names from a tiny fixed set —
`emitTypedProjection` (`typed_projection.go`) gives `include`→`included`,
`rename`→`renamed`, `exclude`→`excluded`, and a no-aggregation `group-by`
projects to its keys *like* an include, also `included`. Independent processes
can't see each other's choices, so `include … | group-by FIELD` emitted two
`included :=` → `go build` failed ("no new variables on left side of :=" + a
`Stream` type mismatch). Fixed in v4.50.1 with `uniqueVarName(base, fragments)`
— a *per-process* heuristic (each process checks the upstream stream it
received). It works, but it's a workaround for "names aren't modelled as
bindings."

### 2b. The `Command` string is the IR the optimiser wishes it had
`generate ssql` **re-parses** each fragment's `Command` back into `pipelineCmd`
structs (`parsePipelineCmd` in `generate_ssql.go`) to run its rewrite rules;
`generate sql` parses `Command` too. So `Command` does triple duty — human
comment, SQL source-of-truth, optimiser parse target — and the optimiser
reconstructs structure by re-parsing strings the producing command already had
in hand. That round-trip is fragile (a feature that works in `generate go` can
silently break `generate sql`/`generate ssql` because each re-parses
independently — a known footgun, see CLAUDE.md "test ALL generate formats").

## 3. Why `go/ast` is the wrong fix

The instinct ("assemble an AST") targets 2a, but `go/ast` specifically fights
this architecture:

1. **The pipeline is distributed across processes.** An AST is an in-memory
   tree; `go/ast` doesn't serialize cleanly (positions, scopes, no JSON). The
   per-process/pipe model needs a *serializable* node, which is what a string
   `Code` is today. Going "AST" here means serializing *your own* node types
   anyway — so the question isn't "AST vs strings," it's "how structured should
   the serialized fragment be."
2. **Three backends, one of them is SQL.** A Go AST helps exactly `generate
   go`. `sql` and `ssql` want a **language-neutral relational IR**
   (Project/Filter/Aggregate/Join/Sort/Limit/Distinct), which each backend
   *lowers* to its target. `go/ast` is Go-only and is built for
   *parsing/analysis*, not synthesis — hand-constructing it is famously verbose
   (projects reach for `dave/jennifer` instead; still in-memory, still Go-only,
   still doesn't cross a pipe).
3. **Readable output is a hard requirement** (CLAUDE.md: "Generated code must
   be readable"). String templates give direct control over layout, comments,
   and the pipeline-comment header. `go/format` would flatten that into its own
   canonical shape. The *last mile* genuinely wants templates.

## 4. The proposed evolution (incremental, not a rewrite)

Two independent, separately-shippable refactors. Neither requires touching the
runtime or the planner's core.

### 4a. A structured `Op` on `CodeFragment` (kills 2b)
Add a small, JSON-friendly, **language-neutral** operation descriptor to the
fragment — alongside (not replacing) `Code` and `Command`:

```go
type Op struct {
    Kind   string            // "from" | "where" | "project" | "rename" | "group" | "join" | "sort" | "limit" | "distinct" | "to"
    Fields []string          // operands (keep-list, group keys, sort keys, …)
    Args   map[string]any    // op-specific (predicate clauses, agg specs, join keys, file, format, …)
}
```

- Each command populates `Op` when it emits a fragment (it already has every
  field — it's currently flattening them into `Command` and `Code`).
- `generate sql` and `generate ssql` read `Op` instead of `parsePipelineCmd`/
  `Command`-string parsing. The optimiser's `pipelineCmd` becomes a thin view
  over `Op` (or is replaced by it). The re-parse round-trip and its
  silent-divergence footgun go away.
- `Command` stays as the human comment only.
- `Code`/`AltCodeIfSeq` stay exactly as they are — `generate go` keeps lowering
  `Op` (or its own state) to Go strings. **No change to generated-Go output**,
  so the corpus is the safety net.

Migration is per-command and incremental: add `Op` to one command, switch the
SQL/ssql backends to prefer `Op` when present (fall back to `Command` parsing
when absent), repeat. Ship in slices.

### 4b. Centralized variable binding at the assembler (kills 2a structurally)
Treat `Var`/`Input` as **bindings**, not literal identifiers, and let the
assembler assign final names in one pass:

- Producing commands emit a *logical* binding id (or keep their readable base
  name as a *hint*).
- The assembler walks the fragment chain and assigns guaranteed-unique,
  still-readable names (`included`, `included2`, … — or SSA-style `v0,v1,…` for
  internal ones), rewriting each fragment's `Var` and the next fragment's
  `Input` together.
- Collisions become structurally impossible; `uniqueVarName`'s per-process
  heuristic is retired in favor of the assembler being the single authority.

This is the "AST benefit that actually matters here" (alpha-renaming / scope
hygiene) without an AST — because variable threading is already explicit in the
fragment (`Var`/`Input`), the assembler just needs to own naming.

## 5. What we explicitly do NOT do

- Replace string `Code` with `go/ast` or `jennifer`. (§3.)
- A ground-up rewrite. Both §4 pieces are additive and corpus-gated.
- Change the per-process/pipe architecture. The serialization boundary is an
  asset, not a liability.

## 6. Sequencing & risk

1. **§4b first** (small, self-contained, directly retires a class of bugs we've
   already hit; the v4.50.1 fix is the proof-of-need). Corpus + a few
   collision-shaped pipelines (two includes, include→group-by, two group-bys)
   are the gate.
2. **§4a next**, one command at a time, with `generate sql`/`generate ssql`
   reading `Op` when present. The 3-mode corpus (record/typed/parallel) plus the
   SQL-assembler tests catch divergence.

Net: keep the good bones (fragment IR + planner + readable string last mile),
add the two pieces of structure the system has been faking (variable binding
via `uniqueVarName`, operator structure via `Command` re-parsing), and stop
short of an AST that the distributed, multi-backend shape doesn't want.

## Prior art / related
- `doc/research/typed-auto-parallel-proposal.md` — the planner/Capabilities
  design this builds on.
- The v4.50.1 fix (`uniqueVarName`, `cmd/ssql/commands/typed_projection.go`) —
  the concrete symptom motivating §4b.
- `generate_ssql.go` (`parsePipelineCmd`) and `generate_sql.go` — the
  `Command`-re-parsing motivating §4a.
