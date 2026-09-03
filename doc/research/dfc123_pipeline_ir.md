# The Pipeline IR: Finishing the Intermediate Representation We Already Have

Reference: DFC123
Created: 2026-09-03
Last modified: 2026-09-03

[Back to Index](./README.md)

**Status:** Arc charter, 2026-09-03 (Ross + Claude); slice 1
(assembler-owned bindings) SHIPPED same day — see §10. This anchors
the IR arc the way DFC118/DFC119 anchored the workspace arc. Builds on [DFC099](./codegen-ir-evolution.md) (which
stands; nothing here deprecates it) and adds three things: the evidence
accumulated since June, the **verification** axis, and the **Authority
Invariant** that keeps the IR from eroding the commands-own-themselves
principle ([DFC115](./dfc115_commands_are_the_authority.md)).

---

## 1. The prompt (Ross's formulation)

> Given the complexity of the go code snippets I am beginning to think
> the IR might be the next place to work… we need to think about a good
> IR that allows optimisation, verification, and generation to multiple
> languages.

And the follow-up worry, which becomes this doc's central invariant:

> Each ssql command generates the go code frags that are assembled at
> the end, so they can use the same go functions as the ssql command —
> the ssql command knows how it itself works. I am worried this could
> be lost if abstract IR is assembled.

## 2. Position, up front

**Agree on the direction, with a framing adjustment: this is not "build
an IR" — it is "finish the IR we already have."** The fragment stream is
already a serializable, per-process, planner-analyzed IR (DFC099 §1).
It is under-structured in exactly two places — variable names are baked
into strings instead of modelled as bindings, and the `sql`/`ssql`
backends re-parse strings because structure was thrown away upstream.
The plan is DFC099 §4 (additive `Op` descriptor + assembler-owned
bindings), extended here with order/protocol properties, a verification
program, and one non-negotiable invariant. Explicitly NOT a rewrite,
NOT `go/ast` (DFC099 §3 still governs), and the per-process pipe
architecture stays — the serialization boundary is an asset.

## 3. Evidence accumulated since DFC099 (June → September 2026)

Every one of these is the same wound — structure discarded at the
fragment boundary, re-derived downstream by a second implementation:

- **`translateResample` (v4.82.0, days old).** The SQL translation of
  resample is a THIRD parser of resample's flag grammar (exec parses
  it, codegen parses it, now `generate_sql.go` parses it). Every
  `translate*` function is a DFC115 violation we recommit per feature
  because the fragment carries no structured operation. CLAUDE.md
  already marks the SQL assembler as "known legacy exception under
  repair" — `Op` is the repair.
- **`fixErrorHandling` (the v4.81.0 typed-sink bug).** A record sink's
  `return fmt.Errorf(...)` landed verbatim in typed `main()` and broke
  compilation, because fragments cannot SAY "I am a fallible sink" —
  the error protocol is implicit in Go syntax, so each assembler does
  textual surgery on code strings to recover it. Protocol facts belong
  on the fragment, not inside the string.
- **The `top`-by-string saga (v4.54–v4.55).** One semantic decision
  (numeric coercion) implemented four times, wrong in three for two
  releases. An IR does not eliminate the N-runtimes problem (§8), but
  it shrinks the parse/description surface to one.
- **`walkStage` arity maps.** Pipeline-aware completion re-parses each
  command's flag grammar a fourth time to propagate schemas. `Op`
  should feed schema propagation too.
- **The equivalence gate now exists** (`TestPipelineEquivalence`,
  post-DFC099) — the byte-identical N-lane differential is precisely
  the safety net DFC099 wanted for incremental migration.

## 4. The three goals, honestly assessed

### 4a. Optimisation
Today's `generate ssql` re-parses `Command` strings into `pipelineCmd`
to run rewrite rules. With `Op`, rules become typed IR→IR
transformations, testable in isolation, composable, and certifiable
against declared operator properties. The planner already proves the
model: its `Capabilities` reach-analysis (shape, SerialOnly) IS
IR-level optimisation. §7 works a concrete new rule end-to-end.

### 4b. Verification — the new axis
Today the only checks on a generated program are "it compiles" and "the
corpus/equivalence gates pass." The IR adds a layer where whole bug
classes become unrepresentable rather than merely caught:

- **Schema/type flow checked before generation.** Unknown fields,
  type mismatches, and shape errors detected uniformly at the IR
  level, once, instead of per-command per-backend.
- **`Op` cross-checks `Code`.** With both present (§5), the assembler
  can verify that a fragment's declared schema flow matches what its
  `Op` implies — catching a command whose codegen drifted from its own
  description. Verification STRENGTHENS command authority instead of
  trading against it.
- **Optimiser rewrites certified against operator properties** (§7) —
  a rule that would reorder past an order-consumer is rejected by
  construction, not by hoping a fixture catches it.
- The equivalence gate remains the ultimate arbiter of runtime
  agreement; the IR reduces how much reaches it.

### 4c. Multiple languages
DFC099 §7 stands: we already lower one stream to Go, DuckDB SQL, and
ssql — and SQL (declarative) is a harder leap than Go→Rust would be.
The gating question per target is never the IR, it's "what runtime do I
lower to": DataFusion for Rust (their `LogicalPlan` is itself a
relational IR — ours lowers to theirs), Polars lazy for the
record-shaped model. Design headroom, not roadmap.

## 5. THE AUTHORITY INVARIANT (the load-bearing section)

Ross's worry names the one way this arc could go wrong. A "pure"
abstract IR — commands emit only `Op`, a central Go backend
pattern-matches on `Op.Kind` to produce code — is a regression on two
fronts: it is a second implementation of every command's
self-knowledge (the central lowerer must know resample means
`ssql.ResampleRecords`, knowledge the command has because it EXECUTES
that function), and adding a command stops being self-contained (N
central backends need edits). That is the drift machine DFC115 exists
to kill.

The resolution is a division of labor, stated as an invariant:

> **The command owns "what I mean" (`Op`) and "how I lower to Go"
> (`Code`, calling the command's own runtime). The backend owns "how
> ops compose in my target." `Op` describes; `Code` lowers; `Op` never
> replaces `Code` for imperative targets.**

Consequences:

- The Go path does not change. Fragments keep carrying
  command-emitted `Code`/`AltCodeIfSeq` that call the same runtime
  functions the command itself executes (`ssql.ResampleFilter`,
  `ssql.SortRecords`, `typed.GroupByParallel`). The
  one-implementation property — exec and codegen cannot disagree —
  is untouched.
- `Op`'s consumers are the backends that cannot call Go functions
  anyway: the SQL translator and the optimiser. For them, central
  knowledge ALREADY exists and is worse-fed (`translateResample`,
  `parsePipelineCmd`). `Op` moves no knowledge out of commands; it
  gives the already-central backends structured input instead of
  strings to re-parse.
- The SQL split is principled, not a compromise: SQL composition is
  non-local (`needsWrap` depends on everything accumulated), so no
  single command CAN own it. "How resample becomes SQL" decomposes
  into *what resample means* (command's knowledge → `Op`) and *how
  DuckDB expresses it in this query context* (target-language
  knowledge → assembler). The command was never the authority on
  DuckDB; it is the authority on itself.
- **The erosion guard.** The predictable failure is future
  well-intentioned "simplification": generating Go from `Op.Kind`
  centrally and deprecating command-emitted `Code`. Write it plainly:
  any backend generating imperative code from `Op.Kind` centrally is
  architectural drift, to be reverted on sight. (Same standing as
  DFC115's "the fix was deleting the copy, not improving it.")

## 6. The IR's ingredients (all additive to `CodeFragment`)

1. **`Op` — the language-neutral operation descriptor** (DFC099 §4a):
   `Kind` + operand fields + op-specific args. Emitted by the command
   alongside `Code` and `Command`; `Command` demotes to human comment.
2. **Bindings, not names** (DFC099 §4b): assembler assigns final
   variable names in one pass; `uniqueVarName`'s per-process heuristic
   retires. Small, self-contained, first to ship.
3. **Protocol facts as fragment fields** (new): fallible-sink,
   barrier, order behavior (§7) — declared, not recovered by textual
   surgery. `fixErrorHandling` retires in favor of the assembler
   choosing the wrapper from a declaration.
4. **Operator properties for analysis** (new): the order dimension
   below, alongside the existing shape/parallel `Capabilities`. Each
   property earns its place by powering a rule or a check — no
   speculative taxonomy.

## 7. Worked example: dead-order elimination (`sort | sort`)

Ross hit this in the workspace: grid gestures compose over user-typed
stages, and by the never-edit-user-text law the workspace cannot
collapse `ssql sort count | ssql sort name` itself — the accumulation
is the ownership laws working as designed. The optimiser is the honest
surface: redundancy shown, collapse offered, applying it is the user's
edit (the existing tail-optimize / Ctrl-T path).

**The rule.** A sort is dead iff no order-sensitive operator consumes
its output before the next order-destroyer. Not an adjacency pattern:

- `sort x | where p | sort y` → first sort DEAD (where preserves and
  ignores order) — adjacency matching misses it.
- `sort x | limit 5 | sort y` → first sort LIVE (limit selects WHICH
  rows by order) — careless adjacency matching miscompiles it.

**The property.** Each `Op` declares order behavior: **establishes**
(sort, top), **consumes** (limit, offset, top, window; resample's
internal sort makes it order-insensitive), **preserves** (where,
include, update, rename), **destroys** (sort, group-by, parallel
shuffle). Dead-sort elimination is then "no consumer on the path
between two establishers" — the same shape of reach-analysis the
planner already does for parallelism.

**The ssql-specific fact that makes the rewrite faithful.**
`SortRecords` → `SortFunc` → `slices.SortFunc`, which is documented
NOT stable (`typed/ops.go`). Tie order after `sort yyy` is unspecified
with or without the earlier sort, so `sort xxx | sort yyy → sort yyy`
is exact under ssql's own contract. PIN THIS: if sort ever becomes
stable, the faithful rewrite changes to `sort yyy xxx` — the
instability is now load-bearing and must be a documented decision, not
an accident.

**SHIPPED 2026-09-03** (post-slice-3, so the rule already sits on
Op-fed structure): `ruleSortElimination` grown from the adjacent
sort-before-group-by rule into the forward scan over the
`orderTransparent` / `orderReset` tables (conservative default:
unknown kinds consume order → sort stays). Gates: an 11-row unit
table, and TWO equivalence liveness pins — because the first
sabotage (limit added to orderReset) sailed PAST the desc-sort pin:
`ruleSortLimitToTop` rewrites `sort -desc | limit` to `top` before
the dead-sort rule runs, so that case pins rule COMPOSITION only.
The ascending variant reaches the classification and fails the
sabotage in the ssql-opt lane. Lesson recorded: when rules
interact, a gate must pin each rule's own decision, not just the
pipeline's end state. Order behavior still lives as tables inside
the optimiser — promoting it to command-declared properties belongs
to the protocol-facts slice (§6.3/§10.4).

## 8. What the IR does NOT solve (honesty section)

- **The N-runtimes problem survives.** The `top` bug was in runtime
  coercion behavior, not parsing — five engines still execute the
  semantics independently, and only the equivalence gate polices their
  agreement. The IR centralizes description, not execution.
- **Complexity relocates, it does not vanish.** The typed resample
  shim is hairy because the problem is hairy; the IR moves it into a
  lowering function with clean inputs. The win is structure at the
  boundaries, not fewer lines.
- **A new language target still needs a runtime** (DFC099 §7's
  caveat). The IR makes translation tractable; DataFusion/Polars/
  DuckDB supply engines so we never hand-port streaming primitives.

## 9. The expression sub-IR (least-explored, flagged open)

Relational ops contain scalar expressions, and the expression layer
already has its own convergence story:

- Flag conditions carry structure today — `{Field, Op, Value}` is
  consumed by the optimiser, SQL translator, and catalog/SSH pushdown.
- [DFC105](./flag-expr-convergence.md) proposes one lowering per
  backend (the differentially-verified `exprToGo` transpiler) beneath
  both the flag and expression surfaces.

For the pipeline IR this suggests: `Op.Args` carries conditions in
their structured form and general expressions as expr-lang source,
with the transpiler/VM as the (command-owned) lowerings and per-target
translation (expr→SQL) as its own future slice with its own
differential gate. Multi-language generation is gated on this more
than on anything relational. Deliberately left open here.

## 10. Sequencing (each slice ships alone, corpus + equivalence gated)

1. **Bindings at the assembler** (DFC099 §4b) — smallest, retires a
   proven bug class (v4.50.1). **SHIPPED 2026-09-03**:
   `lib.ResolveBindings` walks the chain, assigns unique names
   (env-based most-recent-binding resolution; go/scanner
   identifier-aware rewriting that skips strings, comments, and
   selector fields; Var==Input shared-name split — first occurrence
   is the definition; AltCodeIfSeq rewritten too since the planner
   swaps it in later). `uniqueVarName` and its 28 per-command call
   sites deleted — commands emit bare base names. Gates: binder unit
   tests, collision corpus cases (include|include, where|where,
   include|group-by), sabotage-verified (disabling the pass
   reproduces the v4.50.1 "no new variables" error). One contract
   documented on fragment Code: Var is declared at its first
   occurrence (the universal `out := F(in)` shape).
2. **`Op` on the fragment, optimiser consumes it** — `pipelineCmd`
   becomes a view over `Op`; fall back to `Command`-parsing for
   fragments not yet emitting `Op`; migrate per-command.
   **SHIPPED 2026-09-03**: `lib.Op{Kind, Argv, Fields, Args}` on
   CodeFragment, stamped centrally in the four stage constructors
   from the emitting process's own os.Args (continuation fragments
   stay Op-less — one stage, one Op; func fragments defer to their
   body fragments' own Ops). The optimiser's `pipelineCmdFor`
   prefers Op and falls back to Command parsing (older-ssql
   fragments across SSH degrade gracefully). Building the slice
   found a LIVE bug the string path had all along: the Command
   tokenizer can't represent an embedded single quote, so
   `where -if name eq O'Brien` came out of `generate ssql` as
   `"OBrien"` — a silently different pipeline. Op.Argv is lossless
   by construction; regression pinned end-to-end and
   sabotage-verified (forcing the fallback reproduces the mangling).
   Fields/Args stay empty until slice 3 grows consumers.
3. **SQL translator consumes `Op`** — the `translate*` arg-parsers go
   (my week-old `translateResample` parser is the first candidate for
   deletion-by-structure). **SHIPPED 2026-09-03 (first cut)**:
   `stageArgs` gives every translate dispatch (main + func-body
   sites) the lossless Op.Argv view with Command-parse fallback —
   the O'Brien quoting class is now dead in `generate sql` too
   (`WHERE name = 'O''Brien'`, verified against DuckDB). The
   structured-Args pattern is pinned end-to-end on resample: the
   command stamps its SEMANTIC config (time/every-ns/values/fill/
   unit + refusal-relevant from/to/time_format) via
   `stampResampleOp`, and `translateResample` reads it instead of
   re-implementing the flag grammar; `buildResampleSQL` is the one
   lowering behind both front doors (unit test asserts
   byte-identical SQL). Sabotage: poisoning the stamped fill fails
   BOTH resample equivalence cases in the duckdb lane. Remaining in
   this slice's spirit: migrate the other translate* functions to
   structured Args per-command as they next change (each migration
   follows the resample pattern).
4. **Protocol facts** — fallible/barrier/sink declarations; retire
   `fixErrorHandling`.
5. **Order properties + dead-sort elimination** (§7) as the first new
   IR-certified rule; port `sort+limit→top` and merge-wheres onto the
   same footing.
6. **Verification passes** (§4b) — schema-flow check, `Op`-vs-`Code`
   consistency.
7. *(Headroom, not roadmap:)* DataFusion/Polars lowering experiments.

Every slice obeys: `A gate you haven't watched fail isn't a gate` —
each rewrite/verification rule gets a sabotage check on landing.

## 11. Open questions

1. `Op` granularity: one `Kind` per command, or normalize to a smaller
   relational algebra (include/exclude/rename → one `project`)?
   Leaning: start 1:1 with commands (authority-preserving, mechanical),
   let the optimiser normalize internally if rules want algebra.
2. Where does `Op` live in schema-mode? (`SSQL_MODE=schema` and
   codegen mode could share the emission — one self-description serving
   completion AND backends.)
3. Does the workspace ever consume `Op` directly (e.g. explaining what
   a stage does in the UI), or strictly via existing protocols?
4. Expression sub-IR ownership and the expr→SQL differential story
   (§9).
5. Stable-sort: do we ever want it? (Changes §7's rewrite; decide
   deliberately.)

## Prior art / related

- [DFC099](./codegen-ir-evolution.md) — the foundation; §3 (why not
  go/ast) and §4 (the two refactors) govern unchanged.
- [DFC115](./dfc115_commands_are_the_authority.md) — the principle §5
  defends; the SQL assembler's string-parsing is its known legacy
  exception, repaired by slice 3.
- [DFC105](./flag-expr-convergence.md) — the expression-side sibling
  (§9).
- `doc/research/typed-auto-parallel-proposal.md` — the
  planner/Capabilities analysis that order properties (§7) extend.
- `doc/research/multimode-equivalence-testing.md` — why the
  equivalence gate remains the arbiter (§8).
