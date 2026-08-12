# Changelog

All notable changes to ssql will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v4.60.0] - 2026-08-12

### New Features
- **The persistent value-completion cache is gone** (autocli v4.13.0,
  now required) — tab-completing a filename no longer exports
  `AUTOCLI_CACHE_FILE` into the shell, closing the stale-cache hole
  (values offered from a previously-completed file). Cross-pipe value
  slots hint the actionable `Values-Use-Ctrl-O` token, mirroring the
  field-name `Use-Ctrl-O`; Ctrl-O deletes either placeholder and
  completes from the pipeline's own source. In-stage value completion
  is unaffected. The completion script also sheds its jq-based JSON
  directive machinery.
- **Ctrl-O now completes field VALUES too** — the CLI keybinding is
  position-aware: at a value slot (`where -if dept eq <C-o>`) it
  completes actual data values from the file feeding the pipeline (the
  new `-value-source` protocol flag — no AUTOCLI_CACHE_FILE tab-dance),
  quoting spaced values (`'Peter Allworth'`); at field slots it behaves
  exactly as before. (Bash Tab can't see across pipes — the whole reason
  Ctrl-O exists.) The playground's
  value-source derivation now shares this Go implementation. Real-pty
  tested in emacs + vi for both phases.

### Bug Fixes
- **Playground value completion no longer requires tab-completing the
  file path first** — the value source is now derived from the pipeline
  itself (via `-complete-source` + the virtual FS), so typed, pasted, or
  shared pipelines complete values too; a stale cache from an earlier
  pipeline can't win over the file the current one reads. Values
  containing spaces ("Peter Allworth") are quoted on insertion.
- **Field AND value completion work for non-tab "TSV" files** — pipe-,
  semicolon-, colon-delimited files (which `from tsv` reads fine via
  delimiter auto-detection) silently broke completion: schema mode
  hard-split headers on tab (fixed in ssql), and autocli's value sampler
  hard-coded '\t' (fixed in autocli v4.12.1, now required). Field names,
  values, playground and CLI all agree with the readers now.
- **Field completion works after `group-by -rollup`/`-cube`** — the
  schema op treated rollup output as undeterminable and returned an
  empty schema, killing Ctrl-O / playground field completion downstream.
  The enriched schema is fully determinable from argv (group keys + one
  prefixed copy of each result per grouping set — the exec handler's own
  `computeGroupingSetsForSchema`); only pivot is truly data-dependent.
- **Playground Tab completion no longer shows protocol directives as
  candidates** — completing a data-file path surfaced the engine's
  `{"type":"field_cache",...}` line in the popup. The playground now
  consumes protocol lines instead of displaying them — and gained
  **field-value completion** (`where -if country eq <Tab>` → real
  values) on the way, previously CLI-only.
- **Schema mode now honours `from tsv`'s delimiter auto-detection** — the
  schema-mode branch hard-split headers on tab, so Ctrl-O / playground
  field completion on a pipe- (or colon-, semicolon-, …) delimited file
  offered one bogus concatenated field. Detection is now a single shared
  rule (`lib.DetectDelimInHeader`) used by typed sampling and schema
  mode alike, and the header is parsed with proper quote handling.

### New Features (playground & pipeline)
- **Playground "Generate Typed Go" button** — generates the typed-mode
  program (derived row structs, `typed.*` runtime, planner-selected
  parallel forms) alongside the existing record-mode Generate Go. The
  header now also announces Tab completion and the "? Help" button so
  the interactive features are discoverable without scrolling.
- **Playground pipeline-aware field completion** — Tab at a field
  position now completes real field names from the live upstream
  pipeline (the stages before the cursor run in schema mode, so after a
  `group-by dept -count cnt` it offers `dept` and `cnt`, not the raw CSV
  columns). This is the CLI's Ctrl-O — but better placed: the browser
  sees the whole pipeline, so plain Tab triggers it automatically
  (Ctrl-O is also bound for CLI parity). Under the hood: `ssqlExec`
  gained a per-invocation env argument (the WASM process env is frozen
  at startup, so `SSQL_MODE=schema` is set/restored around each call).
- **Playground Tab completion** — Tab in the pipeline textarea completes
  subcommands, flags, operators, formats, and files (the virtual FS's
  sample datasets and uploads), driven by the CLI's own completion
  engine (`-complete`) through the WASM bridge. Bash-style behaviour:
  single candidates insert directly, multiple extend to the longest
  common prefix and open a popup at the caret (arrows/Tab cycle,
  Enter/click accept, Esc dismisses). Field-name slots show the honest
  hint (pipeline-aware names are the next step). Fixed on the way:
  the WASM fs polyfill treated `./x`, `/x`, and `x` as different files,
  which made Go's `os.ReadDir` silently return empty listings.
- **Playground help-at-cursor** — a "? Help" button (and Alt-h inside the
  pipeline textarea) shows contextual help for the command or flag at the
  caret, exactly like the CLI's Alt-h keybinding: paren-aware stage
  extraction, autocli help-at, and the expression-function reference on
  expression arguments. The cursor-context protocol
  (`-cursor-stage`/`-help-at`/`-complete-source`) moved to
  `commands.HandleCursorProtocol`, shared by the CLI and the playground
  WASM — one implementation, no drift. New headless end-to-end gate:
  `scripts/playground-test.sh` (drives the real page in headless Chrome;
  also covers share links).
- **Playground share links** — the browser playground gained a Share
  button that encodes the current pipeline into the URL fragment
  (`#p=<base64url>`) and copies the link; opening a shared link
  preloads the pipeline and runs it automatically. Unicode-safe,
  malformed links ignored gracefully.
- **`group-by -rollup` / `-cube` now work in typed and parallel mode**
  (previously a hard error: "not yet supported in typed mode; drop
  -typed"). The stage ejects to the record `ssql.Rollup` path through
  the Phase B typed→Record boundary — the typed (parallel) CSV parse
  upstream is kept, and the fallback is surfaced under `-explain`
  ("record fallback (-rollup / -cube have no typed form)"). Gated by
  the `groupby_cube_typed_eject` equivalence case with a hand-checked
  golden. (`generate sql` still refuses rollup/cube loudly —
  translation tracked in TODO.)
- **`limit 0` means no limit** (previously an error): all records pass
  through, so a pipeline can keep a `limit N` sampling stage and dial it
  to `0` for full runs instead of deleting it. `offset 0` gets the same
  treatment. In code generation (`generate go` / `sql` / `ssql`) a
  zero-valued `limit`/`offset` stage emits no fragment at all — it
  vanishes from the generated program. Gated by the
  `limit_zero_passthrough` equivalence case (all lanes incl. DuckDB, with
  golden) and `TestLimitZeroSkipsGeneration`.

## [v4.59.0] - 2026-08-11

### New Features
- **`typed.ReadCSVParallel` memory-maps its input** (`internal/mmap`:
  real mmap on linux/darwin with MADV_DONTDUMP, `os.ReadFile` fallback
  elsewhere; GC-managed unmap). The file-sized heap allocation — and its
  GC pressure — is gone: peak RSS drops by roughly the input size
  (measured: 3.0 GB → 2.07 GB on a 1.15 GB / 50M-row CSV). Wall clock is
  neutral on a modern-memory machine (the raw slurp is 1.16–1.25×
  faster, but the 24-thread parse dominates); the honest measured
  numbers, including the revision of the proposal's older-workstation
  1.7–1.9× claim, are appended to
  `doc/research/mmap-readers-proposal.md`. `ReadDelimParallel` is
  deliberately NOT converted: its zero-copy field strings alias the
  buffer, and the GC cannot trace references into a mapping — retained
  rows after unmap would dangle. Race-gated (`go test -race ./typed/...`)
  with byte-identical pipeline output verified pre/post.

### Bug Fixes
- **Catalog range-column extraction no longer LEAKS rows.** The
  `generate ssql` optimizer's catalog-predicate-extraction rule deleted
  the row-level `where` when lifting a condition on a RANGE metadata
  column (`date_from`/`date_to`) into pruning flags — but range pruning is
  only conservative, so shards straddling the boundary silently returned
  non-matching rows (reproduced end-to-end on the LXD SSH rig). Extraction
  now distinguishes column kinds: EXACT metadata columns (the value holds
  for every row in the shard) still extract fully; RANGE columns prune AND
  keep the row filter — which the pushdown rule then ships shard-side
  (`-if date ge X -- where -if date ge X`), the ideal form.

### New Features
- **Catalog pruning flags gained the `+if` negated form.**
  `from catalog` / `merge -catalog` `+if FIELD OP VALUE` keeps shards that
  may contain rows NOT matching: exact metadata columns invert exactly;
  range columns prune only when the ENTIRE range satisfies the positive
  condition (no row could survive the negation) — conservative, like all
  pruning. Round-trips through the optimizer and generated code; the
  extraction rule now lifts negated where-conditions too (previously
  refused). Gated by a negated `PruneCatalog` unit table,
  `TestCatalogPredicateExtraction` optimizer pins, and the rig-gated
  `TestCatalogSSHPruning` integration test (`SSQL_TEST_SSH_HOST=<node>`,
  skips without it).
- **Permutation gate widened**: `top` and a tie-free `update -set-expr`
  stage joined the 2-stage permutation suite (56 pipelines, all lanes).

## [v4.58.0] - 2026-08-10

### New Features
- **One lowering for flag conditions (convergence Phase B) — and `-if …
  regex` now works in typed mode.** `FIELD OP VALUE` conditions from
  `where`/`update` are lowered by a single shared emission
  (`condOpToExprGo`), reusing the expression transpiler's own comparison
  and string-op machinery; the three per-backend implementations became
  thin field-resolution wrappers. User-visible wins: `-if f regex P` is no
  longer a Tier-3 error in typed codegen (literal patterns hoist a
  compiled var, like the expression form's `matches`); invalid regex
  patterns fail at codegen instead of panicking at program startup;
  record update's literal patterns are compiled once instead of PER ROW;
  unknown operators are loud codegen errors instead of silently dropping
  every row; bool-field eq/ne conditions gained typed emissions. Record
  where keeps its runtime-adjustable filter flags (`-pop-gt`-style)
  through the shared lowering. Gated by TestCondOpToExprGo, the full
  flag≡expr metamorphic suite (regex pair now running ALL lanes), and a
  sabotage watched failing — one gate now guards every backend at once.

- **The optimizer canonicalizes trivial expressions (convergence Phase
  C).** `generate ssql` now rewrites `-if-expr` predicates that are
  conjunctions of `field OP literal` into structured `-if` conditions
  (first rule in the pipeline, reported as `expr-canonicalization` under
  `-explain`), so simple expressions inherit every structural rewrite:
  `where -if-expr 'pop > 9 && pop > 5'` optimizes through to
  `-if pop gt 9`. Conservative by design: float literals refuse (an int
  column compared against `15.5` behaves differently under exec's flag
  semantics — the residual divergence documented in Phase A), OR and
  `matches` refuse, and negated expressions canonicalize only as single
  terms. EXPRESSIONS.md gained a flags-vs-expressions
  division-of-labour section.

### Bug Fixes
- **Flag-vs-expression metamorphic gate + three bugs it caught on its
  first run.** New `TestFlagExprMetamorphic` (convergence Phase A,
  `doc/research/flag-expr-convergence.md`) asserts that `-if FIELD OP
  VALUE` and its `-if-expr` equivalent produce identical output in every
  lane, for all 10 operators plus negation, AND/OR composition, and
  update conditions. Found and fixed: (1) record `where` codegen emitted
  gt/ge/lt/le numerically UNCONDITIONALLY — `-if city gt Lima` silently
  returned ZERO rows (exec compares strings lexicographically); it now
  branches on the advisory field type like exec branches on the runtime
  type. (2) record `update` codegen had the same bug worse —
  `float64(0) > "Lima"` didn't compile. (3) typed codegen emitted
  `strings.Contains/HasPrefix/HasSuffix` for contains/startswith/endswith
  conditions without importing `strings` — generated programs failed to
  compile in both typed lanes. The sabotage check (ge→gt) was watched
  failing before the gate was trusted.

## [v4.57.0] - 2026-08-10

### New Features
- **Expr transpiler Phase 4: record-mode `-if-expr`/`-set-expr` go native
  too.** `from csv FILE` now samples column types in record mode (the same
  inference typed mode trusts) and carries them as ADVISORY types on its
  fragment; `where` propagates them and `update` propagates them with
  retype tracking (an assignment that changes a column's type drops it
  from the advisory — a following expression will not use a stale type).
  With advisory types, `where -if-expr` emits a native GetOr predicate
  (`ssql.GetOr(r, "salary", int64(0)) > 150`) instead of the compiled-VM
  filter var, and `update -set-expr` collapses the eval + runtime
  type-switch into ONE typed setter (`mut = mut.Int("salary", …)`) — the
  result type is known at codegen and native expressions cannot eval-error.
  Without advisory types (stdin sources, type overrides, an intervening
  stage that doesn't propagate) the VM path is emitted exactly as before —
  zero regression, reason under `-explain`. Well-typed-column contract
  matches typed mode's and the existing `-if` GetOr emission. Measured:
  record-native predicate 47ns/op, 0 allocs vs 1.5µs VM (~32x). Gated by
  `TestExprToGoRecord`, `TestRecordNativeExprGeneration` (native emission,
  VM-without-advisory, propagation, retype safety), the existing
  record-lane equivalence corpus (now exercising native paths end to end),
  and the zero-alloc bench.
- **Expr transpiler Phase 3: `group-by -expr` aggregations run as native
  MERGEABLE accumulators — and keep the parallel group-by.** The expression
  is compiled through exec's own patcher (`ssql.CompileAggExprPatched`, the
  same env dummies and AST rewrite the VM uses — bare `count()` only parses
  because a dummy env function shadows the arity-checked builtin), and the
  patched normal form (`sum(_records, #.e)`, `len(_records)`, avg → sum/len)
  lowers to accumulator fields: each distinct sum element gets a `+=` term
  keeping the element's OWN type (an int sum stays int64 — `sum(pop) % 5`
  must be integer modulo), `count()` a shared counter, the outer arithmetic
  the Result() expression, float64-coerced. Sums and counts add across
  shards, so a Merge is emitted and `typed.GroupByParallel` is KEPT (unlike
  Phase 2's stream folds). Fallback to record codegen for shapes with no
  typed lowering — notably a field referenced OUTSIDE an aggregation, which
  the VM binds to the group's value ARRAY (`sum(salary)/len(salary)` is
  legal exec and stays record-mode). VM-compile failures are loud at
  codegen. Gated by `TestLowerExprAgg`, `TestExprAggTypedGeneration`, and
  four equivalence goldens (avg desugar, outer division, int-modulo
  fidelity, grouped+mixed) — with the Merge watched failing under a
  sabotaged merge in the go-parallel lane.
- **Expr transpiler Phase 2: `group-by -stream-expr` folds run as native
  typed accumulators.** The init/every/final map-state fold lowers onto the
  same synthesized aggregator struct the built-in aggregations use: init
  keys become typed state fields (int64 widening to float64 when the every
  expression demands it), the every object becomes ONE simultaneous
  multi-assignment in Add() — the VM computes the whole new state from the
  OLD state, so `{a: b, b: a}` must swap; sequential assignment is caught by
  the `groupby_stream_swap` equivalence golden — and final becomes the
  Result() expression, coerced to float64 (`mustAggFloat64` parity).
  Identifier resolution matches exec exactly: record fields SHADOW state
  fields in every (verified against `evalStreamAggExpr`'s env build; the
  plan's assumption was backwards), and final sees state only. Stream folds
  are not generally mergeable, so any `-stream-expr` forces the serial
  group-by form (SerialOnly fragment; the planner serialises upstream).
  Shapes a typed struct can't hold — non-literal init, every keys ≠ init
  keys (the VM legitimately reshapes the state object), non-numeric
  state/final — fall back to record codegen with the reason under
  `-explain`. Gated by `TestLowerStreamAgg`, `TestStreamExprTypedGeneration`,
  and four equivalence cases with goldens (avg fold, widening + division,
  the swap simultaneity gate — watched failing against sequential
  assignment — and mixed builtin+fold grouping).
- **Expr transpiler Phase 1.5: Tier V keeps exotic expressions typed, and
  `generate go -explain` reports the tier per expression.** An expression
  outside the native subset (e.g. `sha256(city)`) no longer ejects the stage
  — and everything downstream — to record mode: the generated typed code
  evaluates it with the expr-lang VM against a static env built from the
  struct (`runtime.CompileExprEnv` + a generated per-schema env constructor),
  so downstream stages keep their parallel forms. `-set-expr` under Tier V
  types its result with loud runtime coercers (`runtime.MustCoerce*`) — a
  result that would RETYPE the column fails the pipeline with a clear message
  (record mode retypes; typed columns can't). Record fallback remains only
  for shapes typed structs can't hold: a NEW field from an untranspilable
  expression, retyping `-set-expr` results, cross-clause new-field type
  conflicts. `generate go -explain` now prints per-expression tier lines
  (`native` / `VM with static env (reason)` / `record fallback (reason)`)
  carried on fragments via `PlanNotes`. Import rendering learned aliased
  entries (`exprvm "…/lib/runtime"`) since parallel programs also import Go's
  stdlib `runtime`. Gated by `TestTierVKeepsTypedPipeline` (asserts the
  parallel group-by SURVIVES a Tier-V where, and the record markers are
  gone), Tier-V equivalence cases with sha256 goldens (duckdb lane skipped —
  no SQL translation, by design), and runtime env/coercion unit tests.
- **Expr→Go transpiler (Phase 1): `-if-expr` and `-set-expr` now run NATIVE
  in typed/parallel generated code.** Previously any `-if-expr` silently
  downgraded the whole downstream pipeline to record mode, and typed
  `update -set-expr`/`-if-expr` hard-errored ("Tier 3"). `exprToGo`
  (`cmd/ssql/commands/expr_go.go`, the Go sibling of the v4.56.0 SQL
  expression translator) transpiles the curated expr-lang subset to plain Go
  with expr-lang semantics reproduced exactly — int/int division is float64
  (`pop/2` of 7 is 3.5), `len()` counts runes, `**` is `math.Pow`, `round()`
  is half-away-from-zero, `matches` patterns hoist to compiled package vars.
  Expressions outside the subset fall back to record-mode codegen exactly as
  before (per-expression, decided at codegen). New `exprfn` runtime package
  provides the two helpers inlining would uglify (`Abs`, `RuneLen`).
  Measured on 1M rows: predicate 4ns/op native vs 1.5µs VM (~375x),
  assignment 3ns vs 1.3µs, both 0 allocs/op. Gated by: emitted-source unit
  tests, a transpiled-vs-VM differential harness (which caught a mixed-type
  `min()` semantics assumption on its first run — the VM returns the winning
  operand with its own type, so mixed min/max deliberately falls back),
  previously-skipped typed equivalence lanes now unskipped, new
  `update_set_expr_division`/`update_set_expr_ternary` equivalence cases,
  and a `where -if-expr` stage in the permutation gate.

### Bug Fixes
- **Record codegen no longer mis-references flags for duplicate field+op
  conditions.** `where -if pop gt 5 -if pop gt 8` declared
  `flagPopGt`/`flagPopGt2` but emitted BOTH references as `flagPopGt2` — the
  first value was silently replaced by the second. Invisible for ANDed
  same-direction bounds, wrong for `+if` mixes (`pop>8 && !(pop>8)` = empty
  result); with THREE duplicates the generated code referenced an undeclared
  `flagPopGt32` and didn't compile. Fixed in two layers: duplicate field+op
  conditions now get numbered flag names at emission time (`where.go`), and
  `collectParams`' cross-fragment rename claims all kept names before
  renaming collisions and rewrites references with a word-boundary match
  (`lib/codefragment.go`). Typed codegen inlines values and was immune;
  exec, `generate sql`, and `generate ssql` were unaffected. Locked by
  equivalence cases `where_dup_fieldop_negated` / `where_dup_fieldop_three` /
  `where_dup_fieldop_two_stages` (all watched failing against the exact
  defect each guards) and `TestCollectParamsRename`.
- **`generate ssql`'s optimiser no longer drops `+if` / `+if-expr`.** The
  v4.56.1 negation sweep fixed exec and `generate go`, but the optimiser's
  where round-trip (`parseWhereArgs`/`buildWhereArgs`) still didn't recognise
  the `+` forms: any rewrite rule that rebuilt a where clause (predicate
  simplification, predicate reorder, catalog predicate extraction, join
  predicate pushdown) silently dropped the negated conditions from the
  optimised pipeline — returning extra rows. Negated conditions now
  round-trip through the rewrite rules, stay opaque to eq/range
  simplification (their bounds are inverted), and are never lifted into
  catalog pruning filters (which have no negated form). Ported from the
  parallel web-session fix of the same negation class. Locked by equivalence
  cases `where_negated_survives_simplify` and
  `where_negated_expr_survives_reorder` (both watched failing in the
  ssql-opt lane first) plus `generate ssql` round-trip subtests in
  `TestNegatedConditionGeneration`.

## [v4.56.1] - 2026-07-25

### Bug Fixes
- **`+if` / `+if-expr` negation now works in ALL generated code** (found
  during the expr-transpiler investigation). Negation was honoured only by
  `where`'s interpreted execution and by `generate sql`; everywhere else it
  silently produced wrong results: record and typed codegen for both `where`
  and `update` applied `+if` conditions UN-negated, and `+if-expr` conditions
  (which arrive as `{"expression":…, "_negated":true}` maps) were dropped
  entirely by record codegen — and by `update`'s interpreted execution too.
  All consumers now parse via a shared `parseExprConds` helper and emit
  `!(…)`. Locked by four new equivalence cases (`where_negated_if`,
  `where_negated_expr`, `update_negated_if`, `update_if_expr_only`), all
  watched failing first.
- **`update -if-expr … -set …` without a `-if` flag no longer generates an
  UNCONDITIONAL update.** Record codegen only parsed `-if-expr` when a `-if`
  flag was also present — the shipped help-example shape applied its `-set`
  to every row. (Fixing it also exposed a never-closed `if` block in the
  emitted code for expr-only clauses; both fixed.)
- **`update -set-expr` eval errors now fail the generated pipeline loudly.**
  Interpreted execution fails the pipeline on an expression eval error; the
  generated record code silently set the field to `""`. Generated code now
  reports the error and exits non-zero, matching exec.
- **Non-numeric `group-by -expr`/`-stream-expr` results now panic with a
  clear message** instead of silently aggregating as `0` (`toFloat64`'s
  default case). A string-valued aggregation is a bug in the expression;
  silent zeros looked like corrupted data.

## [v4.56.0] - 2026-07-03

### New Features
- **Real expression translation in `generate sql`.** `-if-expr` / `-set-expr`
  are no longer passed through verbatim (expr-lang and SQL disagree on more
  than function names: `&&` is a SQL parse error, `||` means string *concat*,
  and `"double quotes"` quote identifiers, not strings). A new expr-lang→SQL
  translator (`exprToSQL`) parses the expression and renders the SQL-safe
  subset — `and`/`&&`→`AND`, `or`/`||`→`OR`, `==`→`=`, string literals
  re-quoted, `??`→`COALESCE`, `?:`→`CASE WHEN`, `in [...]`→`IN`,
  `contains`/`startsWith`/`endsWith`/`matches` operators, and the
  upper/lower/trim/abs/round/floor/ceil/len/hasPrefix/hasSuffix/min/max/
  int/float/string functions (`has(f)`→`f IS NOT NULL`,
  `getOr(f, d)`→`COALESCE(f, d)`). Anything without a faithful SQL equivalent
  **fails loudly**, naming the construct, instead of emitting broken SQL.

- **`union` translates to SQL.** `generate sql` now renders `union` as a set
  operation — the accumulated query `UNION [ALL]` each `<(…)>` source — where
  it previously errored as unsupported. Bare `UNION` deduplicates, exactly
  matching `union` without `-all`.
- **`generate sql` is schema-aware.** The assembler tracks the pipeline's
  columns (seeded from the source CSV/TSV header, advanced per stage by the
  same schema rules pipeline-aware completion uses). First use: `update -set`
  on a NEW field now emits `SELECT *, 3 AS x` (exec creates the field;
  `* REPLACE` on a missing column is a binder error), while conditional
  `-set` on a new field fails loudly (unmatched rows would need a value SQL
  can't give them).

### Bug Fixes
- **Typed `update` no longer serialises the whole pipeline (user-reported).**
  `update`'s typed codegen emitted a single SerialOnly `typed.Select`
  template, so the planner downgraded the source read AND every
  parallel-capable stage downstream — `from csv | update | group-by | join`
  ran fully serial. update is a pure per-row map, so it now emits dual
  templates like the other projections: `typed.StreamSelect` (parallel,
  per-shard) with `typed.Select` as the Seq alternative. The same pipeline
  now plans `ReadCSVParallel → StreamSelect → GroupByParallel`.
- **Repeated commands no longer break `generate go` (permutation-gate
  catch).** Any pipeline using the same command twice — two `sort`s, two
  `where`s, `limit` twice — could fail typed codegen with "no new variables
  on left side of :=": 22 codegen sites drew output variable names from a
  fixed set (`sorted`, `filtered`, `limited`, `joined`, …). This is the same
  collision class as v4.50.1, which spot-fixed only four commands; all sites
  now go through `uniqueVarName`.
- **`generate sql` respects pipeline stage order (user-reported).** The
  assembler folded every stage into ONE `SELECT`, whose fixed clause order
  (WHERE→GROUP BY→ORDER BY→LIMIT) silently computed a different pipeline —
  `update -set x 3 | limit 10 | group-by r | join …` became "group ALL rows,
  then keep 10 groups", with an invalid `CASE ELSE '3' END` on top. Stages
  arriving out of SQL clause order (limit-before-group-by, a second
  projection, join/where after group-by, sort-after-limit, …) now wrap the
  accumulated query as a `FROM (subquery)`; in-order pipelines still render
  flat. Unconditional `update -set` emits the plain value (a `CASE` with no
  `WHEN` is a SQL syntax error), `distinct` renders `SELECT DISTINCT` (was an
  invalid fake select column), and a later `sort` re-sorts stably (its keys
  become primary, the earlier order the tie-break).
- **`distinct` and `union` dedup was broken by pointer-based keys.** Both
  keyed records by `fmt.Sprintf("%v", r)`, which embeds the internal schema
  *pointer* — so equal-valued records from different schema instances never
  matched. `union` (SQL UNION semantics) returned everything un-deduped;
  `distinct` failed in record-mode generated code after projections. Both now
  use the existing value-based `ssql.RecordKey` (which union's *generated*
  code already used — the CLI paths had just never adopted it).
- **`generate sql` no longer silently drops filters and assignments.**
  `update -set-expr`/`-if-expr` were ignored entirely (the SQL returned
  unmodified rows), negated `+if`/`+if-expr` conditions were ignored (the SQL
  returned extra rows), and `group-by -expr`/`-stream-expr`/`-rollup`/`-cube`
  aggregations were dropped. Expressions and negation are now translated;
  the group-by forms without a SQL translation error loudly.
- **`generate sql` types its literals.** WHERE/SET values were always quoted
  as strings (`n > '15'`, fragile: true for `'9' > '15'` as strings, false as
  numbers — DuckDB's column-type coercion usually hid it). Numeric and boolean
  tokens are now bare (`n > 15`, `active = TRUE`); strings keep quotes.
- **`contains` documented correctly as an operator.** `ssql functions`, a
  `where` example, and `doc/EXPRESSIONS.md` all showed `contains(email, "@")`,
  which is an expr-lang **parse error** (it's an infix operator: `email
  contains "@"`). All fixed; `hasPrefix`/`hasSuffix` remain the call-form
  equivalents.

### Testing
- **Permutation gate: `TestPipelinePermutations`.** Enumerates every ordered
  pair of {where, sort, limit, group-by, distinct} (19 two-stage pipelines)
  and runs each through all equivalence lanes — orderings are cheap to
  enumerate mechanically, so the stage-order bug class is now tested
  exhaustively at pair level instead of case-by-case. It caught the
  repeated-command variable collision on its first run.
- **DuckDB is now the sixth equivalence lane.** `TestPipelineEquivalence` runs
  `generate sql` output through `duckdb -json` and asserts it matches every
  other lane — an independent second-engine oracle that shares no code with
  ssql, so unanimous-but-wrong Go lanes can't fool it. Gated on a `duckdb`
  binary (PATH or `~/.local/bin`); normalises DuckDB's HUGEINT-as-JSON-string
  rendering. New discriminating cases `where_expr` and `update_set_expr` —
  both watched failing against the pre-fix translator (parse error and silent
  multiset diff respectively).

## [v4.55.0] - 2026-07-01

### New Features
- **Decluttered flag completion** (autocli v4.12.0). A broad `ssql <cmd> -<TAB>`
  now foregrounds that command's own options instead of a wall of aliases,
  inherited root globals, and the full `-help`/`-man`/`-completion-script` set.
  Only a flag's primary name shows on a broad prefix; aliases (`-dt`), inherited
  globals (`-verbose`), and meta built-ins appear only when the typed prefix
  matches them, and help collapses to a single `--help`. Nothing that completed
  before stops completing. Example: `from csv -<TAB>` → `-default-type -generate
  -merge-schemas -source -type -unordered --help`.

### Bug Fixes
- **`top` on a string field now ranks correctly in ALL paths.** v4.54.0 fixed
  CLI execution and typed codegen; this fixes the two that were missed:
  - **record-mode `generate go`** emitted a numeric-only key
    (`ssql.BottomBy(…, float64 GetOr(f, 0.0))`) that coerced every string to 0,
    so it returned arbitrary rows — now `ssql.BottomByFunc` + `ssql.CompareAny`,
    matching execution and typed.
  - **`generate sql`** `translateTop` looked for the long-removed `-by` flag (so
    it never emitted `ORDER BY`) and treated the first arg as `N` (so `-asc`
    became `LIMIT -asc`). Now emits `ORDER BY FIELD DESC|ASC LIMIT N`.

### Testing
- **N-way differential equivalence harness** (`TestPipelineEquivalence`). Runs
  each pipeline through every result-producing lane — exec, `generate go`
  record/typed/parallel, and `generate ssql` — and asserts byte-identical
  normalised output (canonical JSONL; column-order and int/float normalised;
  ordered-list when the pipeline defines order, else multiset), with golden
  oracles on shuffled fixtures. Catches "correct in mode X, silently wrong in
  mode Y" bugs the substring corpus can miss.

## [v4.54.1] - 2026-06-30

### Bug Fixes
- **Typed `to table` now honours `-max-width`.** The typed/parallel `generate go`
  path emitted `typed.WriteTableToWriter` with no width cap, so it ignored
  `-max-width` and never truncated — diverging from record mode (default 50).
  Long cells now truncate identically (`value…`) in both modes.
  `typed.WriteTableToWriter` / `WriteTableSelectedToWriter` gained an optional
  variadic `maxWidth ...int` (back-compatible: no argument means no truncation,
  so previously-generated code is unaffected).

## [v4.54.0] - 2026-06-30

### Bug Fixes
- **`top` now ranks by the field's natural type.** Previously `top` keyed every
  value through a numeric coercion that returned 0 for any non-number, so
  `top -field <string>` silently returned arbitrary rows instead of the
  lexicographic top-N. It now ranks with `ssql.CompareAny` (numeric when both
  values are numbers, lexicographic otherwise) — the same comparator `sort`
  uses — so execution matches the typed `generate go` output. New comparator-
  based `ssql.TopByFunc` / `ssql.BottomByFunc` (bounded heap, O(N·log K)) back
  this.

### Changed
- **The internal `_row_number` field is gone.** `from csv` / `from xlsx` no
  longer attach a hidden `_row_number` column (added at read time since the
  first commit). It leaked into `to table` / `to csv` / `to json` output and
  diverged from typed mode (which never had it); record and typed output now
  match. If you relied on it to recover input order, use `window -row-number`
  instead.

### New Features
- **`generate go -time`** (and **Alt-r**) report compile and run wall-clock
  times. `… | ssql generate go -run -time` prints `[ssql: compiled in …, ran in
  …]` to stderr (never mixing into stdout data); the Alt-r key binding shows it
  inline on success. Compile and run are timed separately so the run figure is
  the compiled pipeline's real speed.

## [v4.53.0] - 2026-06-30

### New Features
- **Heap-based top-k in typed mode.** `typed.TopBy` / `typed.BottomBy` (bounded
  heap of size n: **O(N·log n)** time, **O(n)** memory) are the typed analogues
  of `ssql.TopBy` / `ssql.BottomBy`, plus parallel forms `typed.TopByParallel` /
  `typed.BottomByParallel` that keep a per-shard heap over a `Stream[T]` and
  merge the survivors. The `top` CLI command's typed/parallel codegen now emits
  these instead of desugaring to a full `SortByDesc` + `Limit` (O(N·log N),
  O(N) memory). The fragment is no longer `SerialOnly` — `from | top` stays
  fully parallel via the per-shard-heap reduction instead of collapsing to
  serial.

### Improvements
- **`generate go -mode` now Tab-completes** to `record` / `typed` (it had no
  completer at all, so Tab offered nothing). `parallel` is omitted as a
  deprecated alias for `typed`.

## [v4.52.2] - 2026-06-30

### Documentation
- **Docs: `typed` and `parallel` are the same mode.** The typed tutorial framed
  `SSQL_MODE=parallel` as a separate "switch the env var for more speed" mode;
  since v4.40 the `typed` planner auto-emits the parallel form when reachable
  and `parallel` is a deprecated alias. Updated the typed tutorial, the typed
  reference, and the README benchmark labels.

## [v4.52.1] - 2026-06-30

### Documentation
- **README + codelab now showcase the interactive shell experience** — the
  `Ctrl-O` / `Alt-h` / `Alt-g` / `Alt-r` / `Ctrl-T` / `Alt-H` key bindings,
  `eval "$(ssql -shell-init)"`, and the `ssql functions` / `ssql conventions`
  reference commands (previously undocumented in the README).

## [v4.52.0] - 2026-06-29

### New Features
- **`ssql conventions`** — an in-binary reference for cross-cutting system
  semantics that span commands and tend to surprise (and that no single
  command's `-help` covers). Categories + `-category <evaluation|data|pipeline|
  codegen>` for detail; sibling of `ssql functions`. Entry #1 documents the
  `update` assignment semantics: every `-set`/`-set-expr`/`-if` in one `update`
  sees the **original** row (SQL `SET` semantics) — pipe to sequence.

### Improvements
- **Dispatcher commands now list their subcommands in `-help` and `-man`.**
  `ssql to -help` (and `from`, `generate`, …) previously showed no hint that
  `to table`, `to csv`, … exist — now a `COMMANDS:` section lists them, USAGE
  shows `<COMMAND>`, and the misleading "does not support clauses" is gone.
  `ssql to table -help` also renders the full path ("ssql to table", not
  "ssql table"). Requires autocli ≥ v4.11.0.

## [v4.51.2] - 2026-06-27

### New Features (interactive help)
- **Alt-h shows the expression-function reference on an expression argument.**
  When the cursor is on an `-if-expr` / `-set-expr` / `-expr` / `-stream-expr`
  expression (not the field name, result-name, or flag itself), Alt-h appends
  the full function listing (the same content as `ssql functions`) below the
  flag help — so you can see every available function without leaving the line.
  Detection is precise per command (`exprArgAtCursor`, unit-tested); the
  listing is a single source of truth (`FunctionsReference`) shared with the
  `ssql functions` command. Also fixes the Alt-h argument marker (`→`) to point
  at the expression on a trailing space, instead of mis-marking the field.

## [v4.51.1] - 2026-06-27

### New Features / Improvements (shell integration)
- **`ssql -shell-init`** — one line enables everything (completion + all
  keybindings + `ssqlgen`): `eval "$(ssql -shell-init)"` in `~/.bashrc`. Driven
  by a single `ShellIntegrations` source-of-truth table, so future integrations
  are picked up automatically; a drift test fails if a new emitter isn't added.
  The bare-`ssql` hint now recommends `-shell-init` first, then lists the
  individual flags.
- **Keybindings surface errors in a subwindow.** When **Alt-g** (show generated
  Go) or **Alt-r** (compile-and-run) can't generate, compile, or run, the error
  now shows in the popup (or inline outside tmux) instead of doing nothing /
  scrolling past. Alt-r still streams successful output inline; only failures
  pop up. A shared `_ssql_clean_err` strips the redundant per-stage re-reports
  so a pipeline with two real errors shows both — not a wall of duplicates.
  **Alt-h** likewise no longer flashes empty on failure.

## [v4.51.0] - 2026-06-24

### New Features / Fixes (interactive completion)
- **Process-substitution-aware Ctrl-O field completion + Alt-h help.** The
  keybindings used to split the line on the last `|` (`${line%|*}` /
  `${line##*|}`), which isn't paren-aware — a `|` *inside* a `<(ssql … | ssql …)`
  process substitution was mistaken for a top-level pipe and produced a
  malformed upstream (silent no-op). The split now happens in Go
  (`cursor_context.go`, paren-aware, unit-tested) behind two protocol flags:
  - `ssql -complete-source "<line>"` — which command's `SSQL_MODE=schema` output
    drives field completion at the cursor; and
  - `ssql -cursor-stage "<line>"` — the current stage, for `-help-at`.
- **Join right-side field completion (new).** Ctrl-O on a join's right-side
  field — the 2nd arg of `-on` (`-on <left> <RIGHT>`) or the 1st arg of `-as`
  (`-as <RIGHT> <new>`) — now completes from the join's `<(ssql from …)>` source
  rather than the upstream pipeline. Clause separators (`+`/`-`) reset the slot
  tracking, so multi-lookup joins complete every field. Cursor *inside* a
  `<(…)>` completes from that procsub's own internal upstream.
  - Real-pty coverage `TestFieldProcsubPTY` (emacs + vi); parsing exhaustively
    unit-tested in `cursor_context_test.go`.

## [v4.50.1] - 2026-06-23

### Bug Fixes
- **Typed codegen: projection variable-name collision.** A pipeline where two
  stages project to a derived struct — e.g. `include … | group-by FIELD` (a
  no-aggregation group-by projects to its keys, like an include), or two
  `include`s — generated two `included :=` statements, so `go build` failed
  with "no new variables on left side of :=" and a type mismatch. This broke
  the **Alt-r** run-typed binding and `generate go`/`-run` in typed mode for
  such pipelines (record mode and interpreted execution were unaffected).
  Projection output names are now made unique against the upstream fragment
  stream (`uniqueVarName`), and group-by threads the chosen name into its
  `Distinct`. Regression: corpus case `include_then_groupby` (all three modes).

## [v4.50.0] - 2026-06-23

### Changed (completion behavior)
- **Honest field-name Tab completion.** The cross-pipe field-NAME cache (the
  `AUTOCLI_FIELDS` env mechanism) is removed — it snapshotted a source header
  and went stale on `rename`/`group-by`/`join`, confidently completing the
  WRONG names. Field names across a pipe now come from the **live** schema via
  **Ctrl-O** (`ssql -field-keybinding`, which runs `SSQL_MODE=schema | generate
  schema`). At a field position Tab can't resolve it inserts a self-documenting
  **`Use-Ctrl-O`** placeholder; pressing **Ctrl-O** deletes it and completes
  for real. Requires autocli ≥ v4.10.0.
  - **Field VALUE completion is unchanged** — `-if dept eq <TAB>` still samples
    real values from the source file (via `AUTOCLI_CACHE_FILE`, a separate
    cache that was never stale).
  - Removed the now-pointless SSH header round-trip and catalog metadata-column
    directive (both only fed the deleted name cache).

### New Features
- **`Alt-g` (`ssql -code-keybinding`)** — show the typed Go the pipeline on the
  line generates, in a popup/inline, without running it. The inspection
  sibling of Alt-r; reads no data, no compile. Listed in the Alt-H cheat-sheet
  and bare-`ssql`.

### Internal
- Single-source-of-truth `KeyBindings` table (`commands/keybindings.go`) now
  generates the Alt-H cheat-sheet; `TestKeyBindingsInSync` scans every emitter
  script and fails if a bound key drifts from the table. Shared popup-display
  fragment between the help and code bindings.

## [v4.49.1] - 2026-06-23

### New Features
- **Convert-to-typed-and-run key binding (Alt-r).** `ssql -run-keybinding`
  emits a `bind -x` binding that compiles the ssql pipeline on the line as
  typed Go and runs it (via `generate go -run`) — the compiled-native result
  without losing the line you're editing. Output streams inline. The win is
  speed on large inputs. Needs a Go toolchain on PATH; fails loudly otherwise.
- **Key-binding cheat-sheet (Alt-H).** `ssql -help-keybinding` now also binds
  Alt-H (Alt-Shift-h) to a popup/inline list of the whole ssql key-binding
  family (Ctrl-O, Ctrl-T, Alt-r, Alt-h, Alt-H), for rediscovery at the prompt.

### Improvements
- Help popup (`-help-keybinding`): the pager now shows a bare `:` prompt
  instead of the temp-file path, and the popup is clamped to the client size
  so it no longer errors "width/height too large" in small terminals.

### Bug Fixes
- **`generate ssql` parquet column pruning** no longer emits phantom source
  columns for fields *produced* by `group-by` (e.g. a `-count` result named
  `count`). The downstream-field scan now stops at the group-by schema barrier
  — the source only needs the fields read up to and including the group-by.
  Previously this produced a misleading `-columns count` and, in the `generate
  go` path, an invalid projection of a column the file does not contain.

## [v4.49.0] - 2026-06-22

### New Features
- **Help-at-cursor key binding.** `ssql -help-keybinding` emits a `bind -x`
  binding (default **Alt-h**) that shows contextual help for the flag or
  command under the cursor — what it does and what arguments it expects —
  without disturbing the line:

  ```bash
  eval "$(ssql -help-keybinding)"         # add to ~/.bashrc
  # cursor on a flag, press Alt-h:
  ssql from data.csv | ssql group-by dept -sum salary
  #   → -sum, -s  field result-name
  #         Sum field values (field name, result name)
  ```

  The third member of the READLINE_LINE action family (Ctrl-O field
  completion, Ctrl-T optimise, Alt-h help). Help text comes from the new
  autocli `Command.HelpAt(args, pos)` primitive via the `-help-at` protocol
  flag (parallel to `-complete`); it reads only the command tree, so it's
  instant. Inside tmux it renders in a `display-popup` overlay, otherwise
  inline. Generalised into autocli so any autocli CLI can offer it. See
  `doc/research/interactive-help-at-cursor.md`.

### Dependencies
- Bumped `github.com/rosscartlidge/autocli/v4` to **v4.9.0**, which adds the
  `Command.HelpAt(args, pos)` primitive and the `-help-at` protocol flag that
  the help-at-cursor binding is built on.

## [v4.48.0] - 2026-06-20

### New Features
- **In-situ pipeline optimisation key binding.** `ssql -optimise-keybinding`
  emits a `bind -x` binding (default **Ctrl-T**) that rewrites the ssql
  pipeline on the readline line to its optimised form — the same rewrites
  as `generate ssql` (merge adjacent `where`s, `sort … | limit` → `top`,
  push filters into `from ssh`, column projection, …):

  ```bash
  eval "$(ssql -optimise-keybinding)"     # add to ~/.bashrc
  # type a pipeline, press Ctrl-T:
  ssql from x.csv | ssql where -if a gt 1 | ssql where -if b eq y | ssql sort -desc s | ssql limit 5
  #   → ssql from x.csv | ssql where -if b eq y -if a gt 1 | ssql top 5 -field s
  ```

  Runs the line through `generate ssql` under `SSQL_MODE=record`, so it
  reads no data (operates on the codegen fragment stream) — instant even
  on huge files. Replaces the line in place; undo with Ctrl-_ (emacs) or
  `u` (vi). Single key bound in emacs/vi-insert/vi-command, real-pty
  tested. The bare `ssql` hint lists it alongside the other integrations.

## [v4.47.4] - 2026-06-18

### Changed
- The bare `ssql` (no subcommand) hint now lists all three shell
  integrations together — `-completion-script`, `-field-keybinding`, and
  `-shell-helpers` (the `ssqlgen` codegen wrapper, which was previously
  undocumented for users). Also documented `ssqlgen` in the CLI codelab's
  Code Generation section.

## [v4.47.3] - 2026-06-18

### Changed
- The bare `ssql` (no subcommand) hint now also advertises the field
  keybinding (`eval "$(ssql -field-keybinding)"` → Ctrl-O), alongside the
  existing `-completion-script` line.

## [v4.47.2] - 2026-06-18

### Changed
- **`-field-keybinding` now binds a single key, `Ctrl-O`, instead of the
  `Ctrl-X Ctrl-F` chord.** A two-key chord depends on readline's
  `keyseq-timeout`, which vi users routinely lower for a snappy `Esc` —
  so the chord intermittently self-inserted as `^X^F` unless typed very
  fast. A single key has no timeout dependency and behaves identically in
  emacs and vi. (fzf uses single keys for the same reason.) Rebind by
  editing the `bind` lines the command emits. pty-verified in emacs and vi
  with a low `keyseq-timeout`.

## [v4.47.1] - 2026-06-18

### Fixes
- **`-field-keybinding` now works in vi editing mode too.** A bare
  `bind -x` installs only into the keymap active when sourced, so the
  Ctrl-X Ctrl-F binding was invisible to `set -o vi` users (the keys
  self-inserted as `^X^F`). The emitted script now binds into the emacs,
  vi-insert, and vi-command keymaps. pty-verified in both modes.

## [v4.47.0] - 2026-06-18

### New Features
- **Pipeline-aware field completion in bash, via a key binding.** v4.46.2
  established that bash *Tab* completion can't see the upstream pipeline
  (bash scopes a completion function to the current command). A `bind -x`
  key binding *can* — it reads `READLINE_LINE`, which holds the whole
  line. Install it:

  ```bash
  eval "$(ssql -field-keybinding)"      # add to ~/.bashrc
  ```

  Then inside a pipeline, at a field position, press **Ctrl-X Ctrl-F**:

  ```bash
  ssql from data.csv | ssql rename -as name person | ssql group-by <C-x C-f>
  #   → completes from  person dept …   (the upstream schema, renames and all)
  ```

  It runs the upstream under `SSQL_MODE=schema` (sources read only the
  header — near-instant, even on huge files) and completes the field:
  unique prefixes complete in place with a trailing space, ambiguous ones
  extend to the common prefix and list the candidates. Tab is untouched;
  rebind the chord by editing the emitted `bind` line. pty-verified.

## [v4.46.2] - 2026-06-18

### Changed
- **Corrected the v4.46.0/.1 claim that pipeline-aware tab completion
  works in bash.** It does not, and cannot via a completion function:
  bash scopes `COMP_LINE`/`COMP_WORDS` to the command under the cursor —
  the stage *after* the last pipe — so the upstream pipeline is invisible
  to a `complete -F` function (verified with a pty; see
  doc/research/bash-pipeline-completion-options.md). The `ssql
  -completion-script` schema wrapper has been **removed** (it was a no-op
  for pipes); `-completion-script` is again autocli's, which still gives
  command, flag, and single-command field completion (`ssql from data.csv
  -if <TAB>`). **Pipeline-aware completion remains fully working in `ssql
  serve`**, and `SSQL_MODE=schema` + `ssql generate schema` remain as a
  scriptable way to get a pipeline's output schema. A `bind -x` approach
  that *can* do bash pipeline completion (it reads `READLINE_LINE`, which
  has the full line) is pty-validated and documented for a future release.
- Docs updated accordingly (CHANGELOG v4.46.0 entry, cli-codelab).

## [v4.46.1] - 2026-06-17

### Fixes
- **bash pipeline-aware completion now actually fires.** The
  `_ssql_schema_complete` wrapper (from `ssql -completion-script`) read
  the upstream pipeline from `COMP_WORDS`, but bash scopes `COMP_WORDS`
  to the command under the cursor — the stage after the last pipe — so
  the upstream was invisible and completion silently fell back to
  autocli's (stale) field cache. It now parses `COMP_LINE`/`COMP_POINT`.
  Also fixes `doc/cli-codelab.md`, which referenced a non-existent
  `-bash-completion` flag (the real one is `-completion-script`).

## [v4.46.0] - 2026-06-17

### New Features
- **Pipeline-aware tab completion in `ssql serve`.** At the prompt,
  completing a field position now offers the schema flowing in from the
  upstream pipeline, not stale columns:

  ```
  > from-loaded | group-by <TAB>                       name  dept  salary
  > from-loaded | rename name person | group-by <TAB>  person  dept  salary
  > from-loaded | exclude salary | group-by <TAB>      name  dept
  ```

  Each serve command declares a schema rule (`from-loaded` reads the
  loaded schema; `rename`/`include`/`exclude`/`update`/`group-by`/
  `window` transform field names; `pivot` is undeterminable; the rest
  are identity). The session walks the upstream stages through these
  rules and seeds the result into completion. Built on autocli v4.8.0
  (`FieldsFromFlag` chains an upstream-fields completer), shell v0.4.0
  (`SchemaWalk` hook), and ssh v0.1.13 (`SchemaWalk` passthrough).
  See doc/research/schema-aware-completion.md.

- **`SSQL_MODE=schema` — compute a pipeline's output schema without
  reading data.** Each command, run under `SSQL_MODE=schema`, transforms a
  schema header instead of data (near-zero cost — sources read only the
  header), and a terminal `ssql generate schema` prints the resulting
  field list:

  ```
  (export SSQL_MODE=schema; ssql from data.csv | ssql group-by dept -count n) | ssql generate schema
    → dept
      n
  ```

  Sources `from csv/tsv/json[l]` and transforms reuse the same per-command
  rules as serve's in-process completion. (**Correction, see v4.46.2:**
  v4.46.0 also shipped a `ssql -completion-script` wrapper claiming this
  drove *bash tab completion* across pipes. It does not — bash scopes a
  completion function to the current command, so the upstream is invisible.
  Pipeline-aware completion is serve-only; the wrapper was removed in
  v4.46.2.)

### Changed
- **Code-generation mode is now selected by `SSQL_MODE`** (`record` /
  `typed` / `parallel`), replacing `SSQLGO` as the canonical variable.
  `SSQLGO` continues to work as a **deprecated alias** — existing
  scripts and `(export SSQLGO=…; …) | ssql generate go` pipelines are
  unaffected. When both are set, `SSQL_MODE` wins. `SSQLGO=1` /
  `SSQLGO=true` remain record-mode aliases. All examples, generated-code
  provenance comments, and the `ssqlgen` shell helper now emit
  `SSQL_MODE`. First slice of the `SSQL_MODE=schema` pipeline-aware
  completion work (see doc/research/schema-aware-completion.md §0).

## [v4.45.0] - 2026-05-20

### New Features
- **Position 2 pipes in `ssql serve`.** Operators can now compose
  pipelines at the prompt just like at a bash shell:

  ```
  > from-loaded | where -if dept eq Engineering | to table
  > from-loaded | group-by dept -avg salary mean | sort -desc mean | limit 5
  ```

  All standard transforms (`count`, `limit`, `sort`, `group-by`,
  `pivot`, `window`, …) compose over the in-memory dataset. Stages
  are connected with `io.Pipe` and run one goroutine each, mirroring
  the bash pipeline model. Tab completion after `|` now offers the
  next stage's subcommands rather than the upstream command's flags.

### Fixes
- **`to table` now honours the command's stdout in serve pipes.**
  `DisplayTable` / `DisplayTableWithFields` hardcoded `os.Stdout`, so
  table output vanished inside a serve pipeline. Added writer-accepting
  `DisplayTableTo` / `DisplayTableWithFieldsTo` variants in `io.go`;
  `to table` passes `ctx.Stdout()`.
- **Early-exit pipelines no longer error.** `… | limit 3 | to table`
  finishes once `limit` stops reading; the upstream stages hit closed
  pipe write-ends. `io.ErrClosedPipe` is now suppressed in the shell
  pipeline runner, matching the Unix SIGPIPE convention.
- **Ctrl-C cancels the line instead of killing the SSH session.**
  `x/term` returns `io.EOF` on Ctrl-C without consuming the `0x03`,
  so the next `ReadLine` saw the stuck byte and exited. The reader now
  translates `0x03` → `0x0d` in-stream; the shell discards the partial
  line with a `^C` banner and continues.
- **Bare transforms no longer hang.** Typing a transform (e.g.
  `where -if …`) without a `from-loaded |` upstream used to wire the
  keyboard into the command's stdin and block. Single commands and
  pipeline stage 0 now read from an empty reader, degrading gracefully
  to zero-record processing — same as `where < /dev/null`.

### Stack upgrades
- `github.com/rosscartlidge/autocli/shell` → v0.3.1

## [v4.44.6] - 2026-05-19

### Fixes
- **SSH window size now propagates to the terminal.** `ssql serve`
  sessions used to wrap lines at a hardcoded 80 cols regardless of
  the operator's actual terminal width. Now `autocli/ssh` parses
  `pty-req` (initial size) and `window-change` (live updates) SSH
  payloads, pushes `(cols, rows)` through `shell.Options.ResizeChan`,
  and `shell.Serve` calls `Terminal.SetSize` for each update. Wide
  rows in `head -t` and `to table` render correctly on wide
  terminals; resizing the terminal mid-session takes effect on the
  next prompt redraw.

### Stack upgrades
- `github.com/rosscartlidge/autocli/shell` → v0.2.2
- `github.com/rosscartlidge/autocli/ssh` → v0.1.11

## [v4.44.5] - 2026-05-18

### New Features
- **`:set` at the `ssql serve` prompt is now a generic settings
  interface.** First setting wired: `head-default-rows`. Operators
  tune the default row count for `head` at runtime without
  reconnecting:

  ```
  > :set
    head-default-rows = 10
                        (default row count for `head` when -n isn't passed)
  > :set head-default-rows 3
  head-default-rows = 3
  > head        # now returns 3 rows by default
  > head -n 5   # explicit -n still overrides
  ```

  Underlying mechanism is `autocli/shell` v0.2.1's Settings
  registry — services supply `[]shell.Setting{Name, Description,
  Get, Set}` and the shell handles the UX (listing, read, write,
  invalid-value feedback).

### Stack upgrades
- `github.com/rosscartlidge/autocli/shell` → v0.2.1
- `github.com/rosscartlidge/autocli/ssh` → v0.1.10

## [v4.44.4] - 2026-05-15

### Stack upgrades
- **autocli/shell migrated from `chzyer/readline` to `golang.org/x/term`**
  (shell v0.2.0). Three bugs in two weeks all of the same shape —
  `chzyer/readline` assumes `os.Stdin` even when given a different
  `Config.Stdin` — drove the rewrite. `x/term` is designed for the
  embedded-driver case (it's what `crypto/ssh`'s own shell example
  uses), takes an explicit `io.ReadWriter`, and never touches
  `os.Stdin` behind your back.

### Behaviour changes inherited from shell v0.2.0
- **No vi mode.** `golang.org/x/term` is emacs-only. `:set vi` at the
  serve prompt is now accepted-but-inactive (deprecation notice on
  invocation). Operators who really need vi can run their editor of
  choice; the shell prompt itself is emacs-keybindings.
- **No Ctrl-R reverse-incremental-search.** Up/Down arrows browse
  history, but no incremental search.
- **`prefs.json` is no longer written.** The only thing it persisted
  was the vi/emacs choice; that's gone. `-session-dir` still
  controls per-user history file.

### Stack
- `github.com/rosscartlidge/autocli/shell` → v0.2.0
- `github.com/rosscartlidge/autocli/ssh` → v0.1.8

## [v4.44.3] - 2026-05-15

### Fixes
- **Ctrl-C now actually kills `ssql serve` when SSH sessions are open**
  (autocli/shell v0.1.5). Same shape of root cause as the earlier
  `SetVimMode` race: `chzyer/readline.MakeRaw` is hardcoded to call
  on `syscall.Stdin` (FD 0) regardless of what `Config.Stdin` was
  passed. When an SSH session connected, readline put the *server
  process's controlling terminal* into raw mode, which disables
  ISIG, which made Ctrl-C in that terminal stop generating SIGINT
  (it just became a literal byte). `kill -INT` worked because it
  bypasses the tty driver entirely.

  Fix: when `shell.Options.Stdin` is not `os.Stdin` (i.e. we're
  driving readline from an SSH channel or any other non-tty
  source), `shell.Serve` now installs no-op `FuncMakeRaw` /
  `FuncExitRaw` on the readline config. SSH client manages its
  own terminal; the server has no business touching termios on
  the host. Local autocli/shell usage (real `os.Stdin`) is
  unchanged.

### Stack upgrades
- `github.com/rosscartlidge/autocli/shell` → v0.1.5
- `github.com/rosscartlidge/autocli/ssh` → v0.1.7

## [v4.44.2] - 2026-05-14

### New Features
- **`-session-dir` flag on `ssql serve`** — persist per-user shell
  state across reconnects. Wires into autocli/ssh's `HistoryDir`,
  which now also stores per-user editor preferences alongside command
  history. Empty (default) = no persistence.

### Fixes
- **`:set vi` / `:set emacs` actually persists** (autocli/shell v0.1.4).
  Previously bookkeeping flipped but readline stayed in the original
  mode and the next session re-read the autocli/ssh-level default —
  `:set` was a functional no-op. Now `:set` writes
  `$session-dir/$user/prefs.json` and the next session reads it back.
  Operator workflow:
  ```
  $ ssh -p 2222 alice@host
  > :set vi
  editing-mode: vi
  (saved — takes effect on next session)
  > :exit
  $ ssh -p 2222 alice@host
  > # vi keybindings active — <Esc>h moves cursor left, etc.
  ```
  Runtime in-session switch still blocked on the upstream
  `chzyer/readline.SetVimMode` race; reconnect is required.

- **Completion fixes** (autocli v4.6.1 + shell v0.1.3):
  `-h<TAB>` at the prompt now completes to `-help` (was missing the
  built-in flags at the root level). `to <TAB>` correctly suggests
  child subcommand `table` instead of re-echoing the parent `to`.

### Stack upgrades
- `github.com/rosscartlidge/autocli/v4` → v4.6.1
- `github.com/rosscartlidge/autocli/shell` → v0.1.4
- `github.com/rosscartlidge/autocli/ssh` → v0.1.6

## [v4.44.1] - 2026-05-14

### New Features
- **`ssql serve`: `to table` subcommand** — sink-style command symmetric
  to the regular ssql CLI's `to table`. Renders the loaded dataset as a
  fixed-width text table. Default prints all rows; `-n N` caps. Sits
  alongside `head -t` which previews the first N as a table.

### Fixes
- **Ctrl-C now exits `ssql serve` immediately when sessions are open**
  (autocli/ssh v0.1.4). Previously a connected SSH session sitting in
  `readline.Readline()` held the server in its 5-second grace-timeout
  window after Ctrl-C. The server's force-close path now closes
  active SSH connections when its context cancels — sessions disconnect
  immediately, the operator sees their `ssh` exit, and the server
  returns. Behaviour change for callers, but matches what users expect
  from Ctrl-C on a server console.

### Stack upgrades
- `github.com/rosscartlidge/autocli/v4` → v4.6.0
  (`ErrUnknownCommand` + `GenerateHelpEmbedded`)
- `github.com/rosscartlidge/autocli/shell` → v0.1.2
  (intercept `-help`, friendly unknown-command message, scoped `:help`)
- `github.com/rosscartlidge/autocli/ssh` → v0.1.4
  (CRLF translation for SSH-PTY sessions; force-close active sessions
  on ctx-cancel)

## [v4.44.0] - 2026-05-13

### New Features
- **`ssql serve` subcommand**: SSH-accessible operator console for an
  in-memory dataset. Load a CSV/TSV/JSON/JSONL once at startup, then
  accept SSH connections that run commands against the loaded data
  with zero per-query startup cost.

  ```bash
  ssql serve data.csv -listen :2222 \
      -host-key ./host_key \
      -authorized-keys ./authorized_keys
  ```

  Operator workflow:
  ```
  $ ssh -p 2222 alice@host
  > sta<TAB>tus
  > status
  uptime:  3h17m
  path:    data.csv
  rows:    87123421
  > schema
    name    string
    age     int
    ...
  > head -t -n 5
  ... table ...
  > :exit
  ```

  Built on the Phase A-C autocli-shell stack:
  - `github.com/rosscartlidge/autocli/v4` v4.5.0 (engine split)
  - `github.com/rosscartlidge/autocli/shell` v0.1.1 (readline driver)
  - `github.com/rosscartlidge/autocli/ssh` v0.1.0 (SSH server)

  Commands in v0: `status`, `schema`, `count`, `head [-n N] [-t]`.
  Future versions add pipelines (`from-loaded | where … | to table`),
  `let $name = pipeline` for named intermediates, `reload`, GPU-aware
  per-shard dispatch.

  Behind the `!slim` build tag because it pulls in `crypto/ssh` and
  `chzyer/readline`. Slim builds (WASM, WebVM playground) error
  clearly if `serve` is invoked.

## [v4.43.1] - 2026-05-08

### New Features
- **Catalog hostname-aware local routing**: `from catalog` now treats a
  `host` column matching the local machine's `os.Hostname()` (in addition
  to the literal `local`/`localhost` sentinels) as local. Catalog CSVs are
  portable across hosts — a row with `host=derra` runs locally on `derra`
  and via ssh from anywhere else, no rewriting per machine. Public helper
  `ssql.IsLocalHost(host)` exposes the predicate; applies symmetrically to
  the v4.27 CLI baseline (`ProcessCatalogShards`) and the v4.43
  codegen-symmetric path (`ProcessCatalogShardsRemoteGo`).

## [v4.43.0] - 2026-05-07

### New Features
- **Catalog remote-Go execution**: extends the v4.42 codegen-symmetric ssh
  pushdown to `from catalog`. Each shard host runs the same mode the local
  pipeline runs in (record / typed-parallel). Per-shard `.ssql` script is
  built at runtime from the embedded template, shipped via ssh stdin, and
  run with stock `ssql generate go -script -mode $mode -run`. Shards run
  concurrently by default; results merge to local stdout.
  - `ssql.ProcessCatalogShardsRemoteGo(entries, requireVersion, pushdownGroups, mode, shardField, opts)` — public orchestrator (returns `iter.Seq[Record]`).
  - `-shard-order completion` (default, low memory) or `catalog` (deterministic, buffers per shard).
  - `-shard-concurrency N` opt-in cap (default 0 = uncapped).
  - `-keep-going` opt-out of fail-fast (default: first shard error cancels remaining).
  - End-to-end byte-identical results across CLI baseline, SSQLGO=record, SSQLGO=typed.

- **Auto-emitted `# require: vX.Y.Z` directive**: every generated `.ssql`
  script gets a `# require: v$localVersion` header so remote `ssql generate
  go -script` can pre-flight check version skew before any records flow.
  Single clear error on stale shards instead of a cascade of symptoms.

### Fixes
- **Typed JSONL fallback Stream→Serial**: `(SSQLGO=typed; ...) | ssql
  generate go -run` against pipelines without an explicit sink now emits
  `for v := range stream.Serial()` when the last fragment produces
  `Stream[T]`. Previously emitted `for v := range stream` which fails to
  compile (Stream is a struct, not a range func). Affects every typed
  pipeline shipping JSONL — including the new catalog remote-Go path.
- **Absolute remote paths**: `from ssh -- ...` and `from catalog --` now
  invoke `/usr/bin/ssql` on the remote rather than bare `ssql`. Avoids
  PATH manipulation and ensures the deployed binary is the one used.

## [v4.27.0] - 2026-03-13

### New Features
- **`from ssh` command**: Read remote files via SSH
  - `ssql from ssh HOST PATH` reads a remote file and streams records locally
  - `-gpu` flag uses `ssql_gpu` on the remote host for GPU-accelerated reading
  - Push-down filtering with `--` separator: `ssql from ssh HOST PATH -- where -if age gt 25`
  - Multi-step push-down with `+` separator: `-- where -if age gt 25 + group-by service -count cnt`
  - Supports code generation via `-generate` / `SSQLGO=1`

- **`from catalog` command**: Read all shards from a catalog CSV for distributed processing
  - `ssql from catalog shards.csv` reads shards listed in a catalog CSV (`host` and `path` columns required)
  - `-if field op value` for partition pruning (skips irrelevant shards before connecting)
  - Range pruning: catalog columns named `X_from`/`X_to` enable interval overlap pruning with `-if X ge val`
  - Exact-value pruning for non-range catalog columns
  - `-shard-field _shard` adds provenance field (`host:path`) to each record
  - Push-down to each shard with `--` separator: `-- where -if status eq error`
  - Multi-step push-down: `-- where -if status ge 400 + group-by service -count cnt`
  - Supports local shards (`host=local` or `host=localhost`)

## [Unreleased]

### Breaking Changes
- **Mandatory Schema Headers**: `ssql from` now ALWAYS emits schema headers
  - Removed `-schema` flag - schema is now automatic
  - Enables strongly-typed pipelines and future GPU acceleration optimizations
  - No migration needed - downstream commands already handle schema headers
  - **Reason**: Schema information is essential for type-aware operations

- **JOIN API Change**: `JoinPredicate` changed from function type to interface
  - **Migration Required**: Custom join predicates must now use `OnCondition()` wrapper
  - **No Impact**: Code using `OnFields()` or `OnCondition()` remains unchanged
  - **Reason**: Enables hash join optimization for dramatic performance improvements

  **Before (v1.0.x):**
  ```go
  // This will NO LONGER compile:
  var pred ssql.JoinPredicate = func(left, right ssql.Record) bool {
      return left["id"] == right["id"]
  }
  ```

  **After (v1.1.0+):**
  ```go
  // Use OnCondition wrapper:
  pred := ssql.OnCondition(func(left, right ssql.Record) bool {
      return ssql.GetOr(left, "id", "") == ssql.GetOr(right, "id", "")
  })

  // OR use OnFields for automatic optimization:
  pred := ssql.OnFields("id")
  ```

### Performance Improvements
- **Hash Join Optimization**: 3-16x faster joins with `OnFields()`
  - `OnFields()` now uses O(n+m) hash join instead of O(n×m) nested loop
  - Custom predicates via `OnCondition()` still use nested loop (no change in behavior)
  - Applies to all join types: `InnerJoin`, `LeftJoin`, `RightJoin`, `FullJoin`
  - **Benchmark Results (1K×1K records)**:
    - InnerJoin: 3.6x faster (6.7ms vs 24ms)
    - LeftJoin: 3.7x faster (6.7ms vs 24.6ms)
    - Multi-field joins: 16x faster (1.4ms vs 22ms)
  - Automatic optimization - no code changes needed for existing `OnFields()` usage

### New Features
- **`-expr` Code Generation**: `group-by -expr` now supports code generation
  - `SSQLGO=1 ssql group-by dept -expr 'sum(salary * bonus)' total` generates compilable Go code
  - Uses `runtime.EvalBatchAgg()` for expression evaluation in generated code
  - Combined with built-in aggregations: `-count num -expr 'sum(salary)' total`

- Added `KeyExtractor` interface for custom optimized join predicates
  - Advanced users can implement both `JoinPredicate` and `KeyExtractor`
  - Enables custom hash-based join optimizations beyond field equality
  - See documentation for examples

### Fixed
- **Code Generation Error Handling**: Errors now prevent partial code output
  - Added error fragment type to code generation pipeline
  - `generate go` detects errors and fails cleanly instead of outputting broken code
  - Unsupported features now fail fast with clear error messages

### Added
- **Runtime Package**: `cmd/ssql/lib/runtime` for generated code helpers
  - `EvalBatchAgg()` for evaluating aggregation expressions on record groups
  - `ApplyValue()` for type-safe value assignment in generated code
  - Enables `-expr` code generation without duplicating complex logic

- Comprehensive benchmark suite (`join_benchmark_test.go`)
  - Compares hash vs nested loop performance
  - Tests various dataset sizes (100, 1K, 10K records)
  - Includes multi-field join benchmarks

### Internal Changes
- Split join implementations into `*JoinHash` and `*JoinNested` helper functions
- Automatic dispatch based on `KeyExtractor` interface support
- Maintains backward compatibility for all `OnFields()` and `OnCondition()` usage

## [v4.6.2] - 2025-01-15

### Performance Improvements
- **Comprehensive Schema Caching**: Extended schema sharing to all readers
  - `ReadCSVSafeFromReader`: Now shares schema across all records
  - `ReadJSONFastFromReader`: Added schema caching for consecutive records
  - `ReadJSONFastSafeFromReader`: Added schema caching for consecutive records
  - CLI `readJSONLines`: Fixed double schema creation (was calling Freeze twice)
  - Ensures consistent performance across all reading paths

## [v4.6.1] - 2025-01-15

### Performance Improvements
- **JSONL Schema Sharing**: 3x faster pipeline processing
  - Added `ParseJSONLineWithSchema()` for shared-schema JSON parsing
  - CLI's JSONL reader now shares schema from header across all records
  - Eliminates per-record schema creation in `from | group-by` pipelines
  - **Benchmark (14.6M records)**: `from | group-by` 47s → 15.8s

## [v4.6.0] - 2025-01-15

### Performance Improvements
- **CSV Schema Sharing**: 4.1x faster CSV reading
  - Schema created once and shared across all records (was created per-record)
  - Eliminated per-record schema creation, key sorting, and map allocation
  - Use `NewRecordFromSchema()` with pre-computed field index mapping
  - **Benchmark (14.6M records, 1.2GB)**: 43s → 10.4s

## [v4.5.1] - 2025-01-15

### Performance Improvements
- **Ordered JSON Output**: Eliminated reflection in field-ordered JSON output
  - Added `Record.AppendJSONOrdered()` for fast field-ordered JSON encoding
  - Replaced `json.Marshal()` with fast encoding in CLI output paths
  - Added buffer reuse to eliminate per-record allocations
  - **Benchmark**: 43s → 23.5s (1.8x faster)

## [v4.5.0] - 2025-01-15

### Performance Improvements
- **Record Refactor**: Internal representation changed from `map[string]any` to `*Schema + []any`
  - Shared schema across records reduces memory per record
  - O(1) field lookup via schema index map
  - Eliminates per-record map allocation overhead

- **Fast JSON Encoder**: 3x faster, 7x less memory, 2238x fewer allocations
  - Pre-computed JSON field prefixes (`"field":`) stored in schema
  - Direct `[]byte` buffer manipulation with `strconv.AppendInt/AppendFloat`
  - `sync.Pool` buffer reuse eliminates allocation per record
  - New `Record.AppendJSON(buf []byte) []byte` method for zero-copy encoding

- **Fast JSON Decoder**: 3.7x faster, 1.7x less memory, 2x fewer allocations
  - Manual JSON parsing via `ParseJSONLine()` avoids reflection
  - Direct type detection during parsing (int64 vs float64)
  - Streaming scanner with configurable buffer size

### Added
- `Record.AppendJSON()` - Append JSON representation to existing buffer
- `WriteJSONFastToWriter()` / `WriteJSONFast()` - Fast JSON writing functions
- `ReadJSONFastFromReader()` / `ReadJSONFast()` - Fast JSON reading functions
- `ParseJSONLine()` - Manual JSON line parser returning MutableRecord

### Internal Changes
- Schema now stores `jsonPrefixes [][]byte` for pre-computed field prefixes
- `jsonBufferPool` using `sync.Pool` for buffer reuse across records
- CLI `lib/jsonl.go` and `lib/json.go` updated to use fast JSON functions
- Type inference handles both `int64` (fast parser) and `float64` (json.Unmarshal)

## [v4.2.0] - 2025-01-05

### New Features
- **Schema Headers (`-schema` flag)**: Preserve field order and types through CLI pipelines
  - `ssql from data.csv -schema` emits a schema header as first line of JSONL output
  - Schema contains field names in order and their inferred types (string, int, float, bool)
  - Output commands (`to csv`, `to json`, `to table`) use schema for consistent field ordering
  - Solves non-deterministic JSON field order issue in pipelines

### Added
- Schema header format: `{"_schema":{"fields":["name","age"],"types":{"name":"string","age":"int"}}}`
- `ReadJSONLWithSchema()` in lib package for reading JSONL with schema headers
- `WriteJSONLWithSchemaOrdered()` for writing JSONL with schema and field ordering
- `WriteJSONWithFieldOrder()` for ordered JSON/JSONL output
- `InferFromRecordOrdered()` for schema inference with field ordering

### Documentation
- Updated README.md with `-schema` flag example
- Added "Schema Headers" section to CLI tutorial (doc/cli/codelab-cli.md)
- Added "JSONL Schema Headers" section to API reference (doc/api-reference.md)
- Documented all `from` command flags: `-schema`, `-type`, `-default-type`, `-format`

## [v4.1.0] - 2025-01-04

### New Features
- **Custom Aggregation Expressions**: `group-by` now supports `-expr` flag for custom aggregations
  - `ssql group-by region -expr 'sum(revenue) / count()' avg_revenue`
  - Uses expr-lang for powerful aggregation expressions
  - Access group records via `records` variable in expressions

## [v1.0.5] - 2024-11-02

### Changed
- Version management now tied to git tags
- Added embedded version.txt for reliable version tracking
- Improved bash completion with alias support

## [v1.0.0] - 2024-11-01

### Breaking Changes
- Record migrated to encapsulated struct with private fields
- Use `MakeMutableRecord()` builder pattern for record creation
- Access fields via `Get()`, `GetOr()`, `.All()` methods

### Added
- Complete Record encapsulation for better API design
- MutableRecord builder for efficient record construction
- Comprehensive test suite

[v4.27.0]: https://github.com/rosscartlidge/ssql/compare/v4.6.2...v4.27.0
[Unreleased]: https://github.com/rosscartlidge/ssql/compare/v4.27.0...HEAD
[v4.6.2]: https://github.com/rosscartlidge/ssql/compare/v4.6.1...v4.6.2
[v4.6.1]: https://github.com/rosscartlidge/ssql/compare/v4.6.0...v4.6.1
[v4.6.0]: https://github.com/rosscartlidge/ssql/compare/v4.5.1...v4.6.0
[v4.5.1]: https://github.com/rosscartlidge/ssql/compare/v4.5.0...v4.5.1
[v4.5.0]: https://github.com/rosscartlidge/ssql/compare/v4.2.0...v4.5.0
[v4.2.0]: https://github.com/rosscartlidge/ssql/compare/v4.1.0...v4.2.0
[v4.1.0]: https://github.com/rosscartlidge/ssql/compare/v1.0.5...v4.1.0
[v1.0.5]: https://github.com/rosscartlidge/ssql/compare/v1.0.0...v1.0.5
[v1.0.0]: https://github.com/rosscartlidge/ssql/releases/tag/v1.0.0
