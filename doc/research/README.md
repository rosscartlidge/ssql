# Research & Design Documents

Internal design docs, proposals, retrospectives, and research notes. These capture the reasoning behind decisions and are not intended as user-facing documentation.

## Active

- [TODO](TODO.md) — tracked feature gaps and improvements
- [TODO — autocli](TODO-cli.md) — autocli framework issues
- [Adoption Plan](adoption-plan.md) — launch strategy and content plan
- [Show HN Draft](show-hn-draft.md) — Hacker News post draft
- [Reddit Post Drafts](reddit-drafts.md) — r/golang, r/commandline, r/dataengineering
- [Golang Weekly Submission](golang-weekly-submission.md) — newsletter submission text
- [Awesome Go PR](awesome-go-pr.md) — PR description and checklist
- [Merge with Catalog](merge-catalog.md) — K-way merge across distributed shards

## Architecture & Design

- [Pipeline Optimizer](pipeline-optimizer.md) — `generate ssql` rule system
- [SQL Generation](sql-generation.md) — DuckDB SQL from pipeline fragments
- [Multi-file From](multi-file-from.md) — multi-file reading with pushdown
- [JSONL Schema Header](jsonl-schema-header.md) — schema-aware JSONL streaming
- [Streaming vs Materialization](streaming-vs-materialization.md) — which commands buffer
- [Window Functions](window-functions-design.md) — window/analytic function design
- [Streaming Window Functions](streaming-window-functions.md) — O(1) memory streaming windows
- [Expression Evaluation](expression-evaluation-design.md) — `-if-expr` and `-set-expr`
- [Expr-lang Integration](expr-integration.md) — expr-lang library usage
- [Parameterized Codegen](parameterized-codegen.md) — CLI flags in generated code
- [Process Substitution Codegen](process-substitution-codegen.md) — fragment merging for nested pipelines
- [Typed Code Generation](typed-code-generation.md) — 35x performance via type specialization
- [Typed Package Proposal](typed-package-proposal.md) — Phase 1 `ssql/typed` package, validated by PoC (5–23× faster, 9–4000× less memory)
- [Typed Performance Notes](typed-performance-notes.md) — improvement opportunities observed during Phase 1/1.5 implementation
- [Typed Concurrency Proposal](typed-concurrency-proposal.md) — proposed `Stream[T]` parallel pipeline alongside `iter.Seq[T]` (DuckDB morsel-driven inspiration)
- [Typed Codegen Proposal (Phase 2)](typed-codegen-proposal.md) — `ssql generate go -typed` MVP scope, schema discovery, type flow, fallback strategy
- [Typed Codegen Tier 3 Roadmap](typed-codegen-tier3-roadmap.md) — what's left after Tier 1+2+3a, ranked by demand × ease, with explicit recommendations
- [Typed GroupByParallel Proposal](typed-groupby-parallel-proposal.md) — Sink/Combine/Finalize parallel group-by, `ParallelAggregator` interface, codegen plan
- [Typed Parquet Proposal](typed-parquet-proposal.md) — Parquet input for the `typed` package; row-group-as-shard parallelism; expected I/O ceiling at or under DuckDB
- [`generate go -optimise / -run` Proposal](generate-go-flags-proposal.md) — chain `generate ssql` rewrites into `generate go`, plus a one-shot compile-and-run flag mirroring `generate sql -run`
- [Mixed-Mode Pipelines Proposal](mixed-mode-pipelines-proposal.md) — design space for pipelines where some stages run typed and others run Record, with adapter fragments at the boundaries
- [mmap Readers Proposal](mmap-readers-proposal.md) — replace `os.ReadFile` slurp with `mmap` in parallel CSV/TSV readers (~1.7-1.9× faster slurp on 1.23 GB); modest 7-8% win on Arrow IPC reader. With benchmark numbers.
- [Typed Auto-Parallel Proposal](typed-auto-parallel-proposal.md) — merge `SSQLGO=typed` and `SSQLGO=parallel` into a single mode that auto-picks per-pipeline. Prototype validated; benchmark exposed a Serial()-channel-cost gotcha that shapes the design (parallelism reach analysis).
- [Codegen: evolving the fragment system (and why *not* go/ast)](codegen-ir-evolution.md) — answers the recurring "should we assemble an AST instead of strings?" Conclusion: no — the distributed per-process pipe model wants a *serializable* IR (go/ast doesn't serialize) and the three backends (go/sql/ssql) want a *language-neutral* relational IR (go/ast is Go-only). The real soft spots are variable names baked into strings (the v4.50.1 collision bug) and `sql`/`ssql` re-parsing the `Command` string. Proposes two incremental refactors: a structured `Op` on `CodeFragment` (so backends stop re-parsing) and centralized variable binding at the assembler (so collisions are structurally impossible) — keeping string templates as the readable last mile.
- [Should all rvalues be expressions? (and why the structured flags stay)](rvalues-as-expressions.md) — design review of a radical idea: drop the `…-expr` flags and make every rvalue a potential expression (the SQL/Polars/AWK model). Conclusion: pursue the unification, but **don't** collapse the grammar — the structured `FIELD OP VALUE` form is ergonomic sugar that buys three things ssql is good at (shell-friendly bare-string literals, schema-aware completion, guaranteed-native codegen for the common case). Recommendation: transpiler first, then make the *value/aggregation* slots expression-capable (folding the `…-expr` flags away) while keeping the structured `-if` default. Also covers closing two specific gaps: a `@field` value sigil for `field OP field` (recommended), and why a structured `-set-op` for computed sets is *not* worth it (the filter↔set asymmetry — binary fits `-if` but arithmetic outgrows it). Decision record so we don't re-litigate.
- [When one pipeline has five implementations: a divergence bug and the differential harness that kills it](multimode-equivalence-testing.md) — retrospective + design note (2026-07-01). A `top`-by-string-field bug that was correct in 2 of ssql's 5 execution/codegen backends and silently wrong in the other 3 (same numeric-coercion bug copied into paths fixed at different times; the SQL path even looked for a long-renamed flag and emitted no `ORDER BY`). Why the corpus missed it — **weak oracle** (`Contains` substrings, not equality) **and** a **non-discriminating fixture** (alphabetical data, where "first N" == "sorted N"). Systemic fix: an **N-way differential/metamorphic harness** (`TestPipelineEquivalence`) that runs every lane, normalizes only the legitimate differences (column order, row order for unordered ops, int/float), and asserts byte-identical output — with **golden** oracles + a **second engine (DuckDB)** to guard against "all lanes agree but all wrong", on **shuffled** fixtures so a wrong answer actually diverges. Includes the reintroduce-the-bug-and-watch-it-fail proof. Written to be shareable; lessons generalize to any interpreter-plus-compiler / multi-target system. Shipped v4.55.0.
- [Expression support in `generate go`: transpile to native Go (performance-first)](expr-codegen-transpilation.md) — `-if-expr`/`-set-expr`/`-expr`/`-stream-expr` are a codegen weak spot: record mode emits the expr-lang **bytecode VM** (a map + closures allocated *per row*, then `expr.Run`), and typed mode refuses expressions outright (Tier 3). Either way an expr forfeits native-Go performance. Designs an expr-lang **AST → Go transpiler** (the AST is already accessible; ssql already walks it), typed-mode-first (known struct types → zero boxing), with a **tiered native/VM-fallback** strategy so the common subset runs native and the long tail stays correct. Performance principles (kill the per-row map, inline into the stage closure, stay on the parallel path), a differential-testing correctness gate, and scoped sequencing (MVP: `where`/`update`; then `-stream-expr` as a natural fold; then `group-by -expr`).
- [Expr→Go transpiler: implementation plan](expr-transpiler-implementation-plan.md) — the concrete follow-up to the design doc above (2026-07-03): empirically probed expr-lang v1.17.6 semantics tables (`/` is ALWAYS float division; `**` always float; `abs`/`min`/`max` type-preserving; `len` counts runes; chained comparisons are legal; missing-field/nil/error behaviour), a verbatim inventory of every VM emission site and Tier-3 bail-out, the `exprToGo` API + `exprfn` helper package, a three-tier fallback ladder (native → VM-with-static-env → whole-stage Record), per-site lowering shapes (where/update/group-by `-expr`/`-stream-expr`), a divergence policy (silent VM behaviours become loud codegen errors), the test gates (differential harness, equivalence-lane unskips, 0-alloc benches, watch-it-fail), 5 shippable phases with estimates — and 3 latent bugs found on the way (`+if-expr` negation dropped in record codegen; `-set-expr` error→`""`; `toFloat64` silent 0).
- [Compiling an embedded expression language into a query pipeline code generator](expr-transpiler-paper.md) — experience report on the shipped transpiler (2026-08-10, phases 1–4): measured results on a 5M-row workload (filter+count 19× CPU / 3.6× memory over the previous best generated code; `-expr` aggregation 16× / 7.5× plus a typed-mode capability unlock; `-stream-expr` fold 1.80 GB → 29 MB peak RSS, 62×), per-row micro-costs (3 ns typed / 28 ns record / 1.26 µs + 1,088 B VM), the tier-ladder + differential-oracle methodology, implementation lessons (reuse the interpreter's own compile; loud-vs-fallback judgement), an honest cost accounting (~2.3k impl + ~1.5k test LOC in 2 days), and an assessment of when curated-subset transpilation generalizes.
- [Converging the flag-condition and expression lowerings](flag-expr-convergence.md) — design proposal (2026-08-10): keep both surfaces (`-if FIELD OP VALUE` = completable, shell-quotable, optimizer-analyzable, runtime-parameterized; `-if-expr` = general escape hatch) but delete the FIVE independent flag-condition lowerings (exec/record-where/record-update/typed/SQL — typed lacks `regex`) in favour of the transpiler's single per-backend emission. Phased: metamorphic flag≡expr equivalence gates first (standalone value — they pin the equivalence and may expose latent divergences), then `condToExprGo` + a parameter-reference leaf (preserving record codegen's runtime flags), then capability wins (`regex` in typed mode, normalizing trivial expressions into optimizer-visible conditions). Exec's `applyOperator` deliberately stays untouched as the differential oracle.
- [Schema-Aware Completion (`SSQLGO=schema`)](schema-aware-completion.md) — pipeline-aware tab completion via a fourth fragment-generation mode. Each command declares a `SchemaOp` rule; bash runs it as a subprocess (`SSQLGO=schema <upstream> | ssql generate schema`), autocli-shell runs it in-process. Same per-command rules in both worlds. Designed 2026-05-20 after shipping Position 2 pipes (v4.45.0). **Shipped as `SSQL_MODE=schema` in v4.46.0** (serve + the engine); bash *Tab* completion across pipes hit a hard bash limitation — see next.
- [Bash Pipeline Completion — Constraints & Options](bash-pipeline-completion-options.md) — decision doc (2026-06-17). bash scopes `COMP_LINE`/`COMP_WORDS` to the current command, so a `complete -F` function cannot see the upstream pipeline (proven with a pty on bash 5.2.21). Options weighed: dedicated `bind -x` chord (fzf-style, recommended), rebind Tab (not recommended), accept+document, or sidestep via the REPL. Includes the reproduction harness.
- [Interactive Help at the Cursor](interactive-help-at-cursor.md) — exploration (2026-06-20). Show help for the command/flag/arg *under the cursor* while editing, not just completion candidates. Core idea: help-at-point is the existing completion context (`analyzeCompletionContext`) rendered as help — proposed as a general autocli primitive `Command.HelpAt(args, pos)` + `-help-at` protocol flag, with ssql as first consumer. Triggers via a `bind -x` help key (`READLINE_LINE`) or the autocli-shell keystroke callback; displays inline, in a `tmux display-popup`, or a live `tmux split-window` help pane, degrading gracefully outside tmux. Third member of the Ctrl-O / Ctrl-T action-binding family.

## Distributed Processing

- [Distributed SSH](distributed-ssh-processing.md) — SSH pushdown design (shipped v4.27.0)
- [Shard Catalog](distributed-shard-catalog.md) — catalog-based distributed queries (shipped v4.27.0)
- [Catalog Codegen](catalog-codegen.md) — code generation for catalog operations
- [Parallel Processing](parallel-processing.md) — parallelism opportunities
- [SSH Test Environment](ssh-test-environment.md) — test setup for SSH features
- [Remote Go Execution](remote-go-execution-proposal.md) — codegen-symmetric ssh pushdown. Whatever mode the local pipeline runs in (CLI baseline / SSQLGO=record / SSQLGO=typed), the remote runs in too. Generated Go embeds the .ssql script as a const string and inlines a small ssh-and-cat-and-run helper — single self-contained binary, no extra deployment artefacts. Drops the v4.41 transitional `-remote` flag. Shipped v4.42.0.
- [Catalog Remote-Go](catalog-remote-go-proposal.md) — extend the codegen-symmetric ssh pushdown to `from catalog`. Per-shard ship-and-run, parallel orchestration, embedded .ssql template. One self-contained Go binary that orchestrates distributed typed-parallel execution across N shard hosts with stock ssh and a deployed ssql. ~2 days for v4.43.
- [Codegen Wrapper Proposal](codegen-wrapper-proposal.md) — `ssql -shell-helpers` + `ssql generate go -script PATH` to lower the bar from `(export SSQLGO=...; ...) | ssql generate go` to `ssqlgen 'pipeline'` or `-script <(heredoc)`

## CLI & Framework

- [CLI Tools Design](cli-tools-design.md) — overall CLI architecture
- [Update Command](cli-update-command-design.md) — conditional update syntax
- [From Subcommands](from-subcommands.md) — `from csv/tsv/json` design
- [+flag Negation](plus-flag-negation.md) — `+flag` as negation of `-flag`
- [Go CLI Frameworks](go-cli-frameworks-comparison.md) — framework comparison (Dec 2025)
- [Autocli Improvements](autocli-improvements.md) — multi-arg flag handling
- [Autocli Helper Methods](autocli-helper-methods-proposal.md) — accumulated flag helpers
- [autocli-shell + autocli-ssh Proposal](autocli-shell-proposal.md) — embed autocli's completion engine in a readline REPL or SSH-accessible service CLI (router-style operator console). Three layers: completion-engine split in autocli, readline driver, SSH server. First consumer: `ssql serve`. ~4 days.
- [Subcommand Migration](completionflags_subcommand_migration.md) — migration to autocli subcommands

## Performance

- [Record Optimization](record-performance-optimization.md) — Record type performance
- [Schema-Aware Records](schema-aware-records.md) — schema sharing optimization
- [JSON Serialization](json-serialization-performance.md) — serialization benchmarks
- [Gob vs JSONL](gob_vs_jsonl_streaming.md) — inter-process streaming formats
- [Join Performance](join_performance_analysis.md) — hash join optimization

## GPU & Formats

- [GPU Acceleration](gpu-acceleration.md) — CUDA integration research
- [GPU Arrow Learnings](gpu-arrow-learnings.md) — lessons from GPU + Arrow
- [GPU Opportunities](gpu-feature-opportunities.md) — future GPU features
- [Arrow Integration](arrow-integration-proposal.md) — Apache Arrow support
- [Serialization Formats](serialization-formats.md) — format comparison
- [FFT Windowing](fft-windowing-sampling.md) — signal processing details

## Visualization & Playground

- [Interactive Visualization](interactive-visualization.md) — Chart.js integration
- [WASM Playground](wasm-playground.md) — browser playground design
- [ssql serve Proposal](ssql-serve-proposal.md) — browser UI with native backend via WebSocket
- [WASM TinyGo Redesign](wasm-tinygo-redesign.md) — TinyGo build approach
- [WASI Build](wasi-build.md) — portable .wasm binary for wasmtime/wasmer/Docker
- [Browser Linux Options](wasm-linux-options.md) — WebVM/CheerpX research

## AI & LLM Development

- [AI Prompt System](ai-prompt-system.md) — prompt engineering architecture
- [LLM-Guided API Design](llm-guided-api-design.md) — iterative prompt engineering case study
- [Conference Paper](conference-paper-ai-assisted-go-development.md) — AI-assisted Go development
- [Conference Slides](conference-slides.md) — GopherCon talk materials

## Retrospectives

- [Italy Sprint v4.11-v4.16](italy-sprint-v4.11-v4.16.md) — major feature sprint
- [Post-Italy v4.17-v4.28](retrospective-v4.17-v4.28.md) — polish and optimization phase

## Historical / Completed

- [Join Implementation](join_implementation_plan.md) — hash join design (implemented)
- [Join Status](join_implementation_status.md) — implementation tracking
- [Join Interface](join_interface_approach.md) — breaking change analysis
- [Update Filter](UPDATE_Filter_Design.md) — conditional update design (implemented)
- [Conditional Syntax](update-conditional-syntax-proposal.md) — syntax alternatives
- [Rollup/Cube](rollup-cube-design.md) — ROLLUP and CUBE aggregation
- [Custom Aggregation](custom-aggregation-expressions.md) — expression-based aggregation
- [Compound Types](compound-types-investigation.md) — nested data investigation
- [DuckDB vs ssql](duckdb-vs-ssql.md) — comparison analysis
- [SeqFactory](SeqFactory_Design.md) — iterator factory pattern
- [Migration Checklist](subcommand_migration_checklist.md) — autocli migration tracking
- [Migration Complete](remaining_migration_work.md) — final migration status
- [Removing SetAny](removing-setany-analysis.md) — API cleanup analysis
- [GitHub Migration](github-repo-migration.md) — repo migration plan
- [GitHub Republish](github-republish.md) — republishing strategy
- [Implementation Priorities](implementation-priorities.md) — feature prioritization
- [Future Development](future-development.md) — roadmap ideas
- [My Ideas](my_ideas.md) — personal feature wishlist
- [Performance Plan](performance-improvement-plan.md) — optimization roadmap
- [Expr AST Patching](expr-ast-patching.md) — expression AST manipulation
- [Expr Implementation](expr-implementation-plan.md) — expression system implementation
- [Expr Reference](expr-lang-reference.md) — expr-lang syntax reference
- [Non-Regular Files](non-regular-file-fragment-detection.md) — process substitution detection
- [Typed Generation Compat](typed-generation-compatibility.md) — compatibility audit
