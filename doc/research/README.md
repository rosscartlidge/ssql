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

## Distributed Processing

- [Distributed SSH](distributed-ssh-processing.md) — SSH pushdown design (shipped v4.27.0)
- [Shard Catalog](distributed-shard-catalog.md) — catalog-based distributed queries (shipped v4.27.0)
- [Catalog Codegen](catalog-codegen.md) — code generation for catalog operations
- [Parallel Processing](parallel-processing.md) — parallelism opportunities
- [SSH Test Environment](ssh-test-environment.md) — test setup for SSH features
- [Remote Go Execution](remote-go-execution-proposal.md) — ship a small `.ssql` script (~500 B) to remote SSH hosts that already have ssql installed; remote runs `ssql generate go -script -run` and streams just the result back. Unlocks v4.40 typed-parallel speedups for `from ssh`/`from catalog` pipelines without copying source data. Single-mode design (ssql required on remote — same prerequisite as `--` pushdown); ~2 days total. Builds on the codegen-wrapper proposal's `-script PATH` flag.
- [Codegen Wrapper Proposal](codegen-wrapper-proposal.md) — `ssql -shell-helpers` + `ssql generate go -script PATH` to lower the bar from `(export SSQLGO=...; ...) | ssql generate go` to `ssqlgen 'pipeline'` or `-script <(heredoc)`

## CLI & Framework

- [CLI Tools Design](cli-tools-design.md) — overall CLI architecture
- [Update Command](cli-update-command-design.md) — conditional update syntax
- [From Subcommands](from-subcommands.md) — `from csv/tsv/json` design
- [+flag Negation](plus-flag-negation.md) — `+flag` as negation of `-flag`
- [Go CLI Frameworks](go-cli-frameworks-comparison.md) — framework comparison (Dec 2025)
- [Autocli Improvements](autocli-improvements.md) — multi-arg flag handling
- [Autocli Helper Methods](autocli-helper-methods-proposal.md) — accumulated flag helpers
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
