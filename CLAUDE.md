# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## On Startup (DO THIS FIRST)

**ALWAYS read the latest journal entry before doing anything else:**
```bash
ls -t journal/*.md | head -1 | xargs cat
```

## Current Version

**ssql v4 is the current major version.** Always use the `/v4` module path:
```bash
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest
import "github.com/rosscartlidge/ssql/v4"
```

## Repository Hygiene (CRITICAL)

- **NEVER** build test programs in the root directory — use `/tmp/`
- **NEVER** create documentation files in the root directory — use `doc/research/` or `doc/archive/`
- **What Belongs in Root:** Core library source (`*.go`), tests (`*_test.go`), `README.md`, `CHANGELOG.md`, `go.mod`, `go.sum`, `Makefile`, `.gitignore`

## TODO Tracking (CRITICAL)

When you discover a missing feature, unsupported command, or limitation during development or testing, **add it to `doc/research/TODO.md`**. This includes: commands that `generate sql` doesn't support, build system improvements, playground gaps, optimizer bail-outs, etc.

## Development Journal (CRITICAL)

Maintain weekly journal entries in `journal/YYYY-WNN.md`. Update at end of sessions, when completing tasks, when making commits. Read latest on startup for context.

## Documentation Maintenance (CRITICAL)

Keep documentation in sync with API and CLI changes. Key files:
- `README.md` (kept to ~100 lines: one pipeline, install, why, learning path; everything else lives in `doc/`), `doc/install.md`, `doc/api-reference.md`, `doc/cli-codelab.md`
- `doc/cli-troubleshooting.md`, `doc/EXPRESSIONS.md`, `doc/ai-code-generation.md`
- Validate: `make doc-check` (L1), `make doc-test` (L2), `make doc-verify` (L3)
- Common mistakes: changing API/CLI without updating docs, using old import paths/command names/flag names
- **When creating new docs**, ALWAYS add them to the appropriate index: `doc/README.md` or `doc/research/README.md`. This is easy to forget — check BEFORE committing.

### DFC Convention for Research Docs (CRITICAL)

Every `doc/research/*.md` carries a DFC (Doc For Comment) reference — a stable, chronological handle (like an RFC number) for journals, commit messages, and cross-doc references. `scripts/dfc.py` is the tool; `make doc-check` gates it (Check 9).

- **New research doc**: get the number with `scripts/dfc.py --new TOPIC WORDS` (topic words trigger a prior-art docsearch first — read what it surfaces); name the file `dfcNNN_short_description.md` (lowercase); put the metadata block right below the `# Title`:
  ```
  Reference: DFCnnn
  Created: YYYY-MM-DD
  Last modified: YYYY-MM-DD

  [Back to Index](./README.md)
  ```
  (Pre-DFC files keep their original names — renaming breaks inbound links; only the metadata identifies them.)
- **After creating or editing research docs**: run `scripts/dfc.py --stamp && scripts/dfc.py --index` before committing — stamps dates from git/working-tree state and regenerates the chronological table in `doc/research/README.md`. Numbers are NEVER reassigned.
- **Superseding a doc**: add `Deprecates: [DFCnnn](./old_file.md)` to the new doc's metadata and `Deprecated-by: [DFCnnn](./new_file.md)` to the old one — future sessions must not implement from a superseded plan.
- **Refer to docs by DFC number** in journals and commit messages (`Ref: DFC085`) — short, stable, greppable in both directions.
- **Context discipline**: locate docs via `scripts/docsearch.sh` or the README index, then read only the 1–2 DFCs the task needs — never bulk-load the research dir.

## Development Principles (CRITICAL)

### Commands Are the Authority on Themselves (CRITICAL)
A command owns its flag grammar, format knowledge, and semantics — consumers needing facts about what a command reads, produces, or means MUST ask it through a protocol (`-complete`, `-help-at`, `SSQL_MODE=schema`, `-generate` fragments, `from -records`), NEVER re-parse its arguments elsewhere. An external parse is a second implementation of the command's grammar, and second implementations drift: serve's hand-rolled from-args parser took three drift bugs in one week before being replaced by exec'ing `from … -records` (the fix was deleting the copy, not improving it). When a consumer needs a NEW fact about a command, grow the command a protocol surface for it. The principle covers STATE too (matured 2026-08-29): any config that determines what the user sees must be expressible as command text — UIs are views/editors of the pipeline, never owners of state (widget steps, grid ops, and chart axis picks each drifted or got lost until they became pipeline text; see DFC115 §matured, DFC117–DFC119). When a UI needs new state, grow a command's grammar to hold it. Known legacy exception under repair: the SQL assembler parses fragment Command strings (see DFC115 and codegen-ir-evolution.md) — do not copy that approach. Full reasoning: `doc/research/dfc115_commands_are_the_authority.md`.

### Refactor While You Work
Whenever fixing bugs or adding features, always look for opportunities to refactor and simplify the surrounding code. Extract shared helpers, remove duplication, simplify control flow. Leave the code better than you found it.

### If It's Not Tested, It Will Break
Features without tests will eventually be removed during refactoring (learned when field/value completion was lost in v3.2.0).

### A Fixture That Fits in a Pipe Buffer Tests Nothing About Scale (CRITICAL)
Perf-sensitive paths (readers, samplers, schema mode, serve chains) need a case in the opt-in scale gate (`SSQL_SCALE=1 … TestScaleBudgets`, `cmd/ssql/scale_test.go`, DFC113) asserting a wall-time ceiling — output oracles pass while complexity bugs ship (five fixture-invisible bugs in one week proved it). Budgets are generous absolute ceilings, never stored baselines. New scale-sensitive features add a case the way result-changing commands add an equivalence case.

### One Semantics, Many Backends → Test Them Differentially (CRITICAL)
The same pipeline runs **six ways**: interpreted exec, `generate go` in record/typed/parallel, `generate sql` (DuckDB, Postgres, DataFusion) and `generate rust` (DataFusion's DataFrame API, DFC157 — a second lowering onto the same engine, not a new owner of the semantics: its expressions are the datafusion dialect's text handed to DataFusion's parser, and the opt-in `rust` lane must equal the datafusion lane case for case). Each is a separate implementation of the same semantics, so **a bug fixed in one path is almost always still live in the others.** Hard-won lesson (the `top`-by-string saga, v4.54–v4.55): the numeric-coercion bug was fixed in exec and typed but left wrong in record codegen AND `generate sql` for two more releases, found only when a user compared two outputs by hand.

**Rules:**
- **When you change a command's *results* (ranking/filter/aggregate/selection), assume the bug is in ALL backends until a test proves otherwise.** Fix exec, record codegen, typed codegen, and the `generate sql` translator together; don't fix only where you see it.
- **Add/adjust a case in `TestPipelineEquivalence` (`cmd/ssql/equivalence_test.go`)** — the N-way differential gate that runs every lane and asserts **byte-identical normalised output**. This is the gate that catches "correct in mode X, silently wrong in mode Y"; the `TestPipelineCorpus` substring smoke test does NOT (it stayed green through the whole `top` bug).
- **A test's power = oracle strength × input discrimination — weakness in EITHER blinds it.** The `top` bug survived because the oracle was weak (`Contains` substrings, not equality) AND the fixture was non-discriminating (alphabetical data, so "first N" == "sorted N"). Assert normalised equality, and use **shuffled fixtures with distinct values** so a wrong answer actually diverges.
- **Guard the "all lanes agree" metamorphic oracle with an independent one** — hand-written `Golden` outputs and/or the DuckDB (SQL) lane — so unanimous-but-wrong can't pass.
- **A gate you haven't watched fail isn't a gate** — reintroduce the bug once and confirm the harness catches it.
- Full rationale + generalizable lessons: `doc/research/multimode-equivalence-testing.md`.

### Compile-Time Type Safety Over Runtime
- Use generics and type constraints to enforce correctness at compile time
- Use sealed interfaces to prevent invalid type construction
- Never bypass type constraints with `any` or reflection

### Performance-Critical Code Patterns
See `claude/performance-patterns.md` for full details and examples.

**Rules (never violate):**
- **Schema sharing is the #1 performance rule.** Never create schemas per-record. Create once with `NewSchema(headers)`, reuse via `NewRecordFromSchema(schema, values)`. Violating this cost 4x perf (43s→10.4s for 14.6M records).
- **Cache schemas for variable-schema data** (JSONL without schema header) — reuse when fields match.
- **Pre-allocate buffers outside loops**, reset with `buf = buf[:0]` (keep capacity).
- **A reader decodes in batches; it never materialises a row group or a file to walk it.** The typed Parquet readers read whole row groups into one Arrow table (the serial one the whole file) until 2026-10-28: a parallel scan of a 14.6 M-row file held 5.3 GB live where DataFusion, reading batches of a few thousand rows, held 1.7 GB — and the first write-up charged it to the sink without measuring (DFC158 §3.8). Decode a bounded batch, release it, stream a column chunk through a bounded buffer; measure the live heap (`GODEBUG=gctrace=1`) and a mid-run heap profile before deciding what holds the memory.
- **Profile before optimizing**: `go test -cpuprofile cpu.prof -bench BenchmarkName`
- **A hot closure that grows stops inlining its callees.** Past the compiler's big-function threshold a function inlines only tiny callees: adding the fold's precompute to `Window`'s closure un-inlined `Record.ToMutable` in its emission loop and cost every window function +3 allocations and ~20% time per row, with no new code on that path (BenchmarkWindow vs a HEAD worktree, 2026-10-21). Keep hot closures small — per-clause work lives in a named function (`windowResults`) — and benchmark a change to a hot loop against HEAD even when the diff "cannot" cost anything.
- **Every window aggregate over the running frame is carried across the partition in one pass; the whole-partition frame is computed once.** `computeWindowFunc` / `typed.windowValue` are called per (spec, row) with no state and scan the row's frame, and over UNBOUNDED PRECEDING → CURRENT ROW the frame starts at the partition's first row — O(n²): a 560 k-row partition was still running at a 30-minute cap (DFC157 §4, 2026-10-27) while DataFusion took 5.7 s. `Window` precomputes the folds (`runningFoldValues`, `window_fold.go`, DFC156) AND the sums, averages, counts, extremes and ranks (`runningWindowValues`, `window_running.go`; `typed/window_running.go` is the same split) with the same operations in the same order as the scan, so the values are byte-identical (`TestWindowRunningMatchesPerRow`); bounded ROWS frames (O(n·k)), RANGE frames, lag and lead stay per row, which is what their semantics say. A new window function gets a carried form or a note saying why not; the scale gate's `window-running-frame-*` cases catch the regression.

### Concurrency Patterns (CRITICAL)
See `claude/concurrency.md` for full details, measurements, and negative results.

**Rules (never violate):**
- **Never put a channel between every row and its next consumer.** Per-row channel transit is ~100 ns; on a 4-stage pipeline over 7M rows that's 2.8 s of pure overhead. The first PoC tried this and was 3x SLOWER than single-threaded. Use `ParallelFromSlice` (slice partitioning, no channel) instead of channel-based `Parallel` whenever the source is in memory.
- **Parallel-mode write-everything sinks use per-shard buffer dump, not `Serial()` fan-in.** Each shard formats into its own `bytes.Buffer`; final stage dumps in shard order. Skipping the `Serial()` channel was 4.4× faster on a 7.25 M-row CSV write. Trade: peak memory ~2× output size, output is shard-concatenation order. See `claude/concurrency.md` §12. Apply to future JSON/Arrow Stream sinks too.
- **Use `bytes.IndexByte` for byte-search in hot paths**, not `for i, b := range data { if b == '\n' { ... } }`. The first form is SIMD-accelerated on amd64 (~5-10× faster than byte-by-byte). Switching the parallel-CSV newline scan saved 210 ms on a 600 MB file.
- **A parallel source's shards are contiguous runs of the input, in order** — a CSV shard is a byte range, a range shard an interval, a Parquet shard a run of row groups (contiguous since 2026-10-28; round-robin before) — so the shards concatenated in shard order ARE the input. The parallel sinks dump in shard order and `typed.WindowParallel` materialises shard by shard on that guarantee (the window's first-seen partition order is the file's); a new parallel source keeps it, and `TestReadParquetParallelShardsAreInFileOrder` is the shape of the test. Only the channel-based `typed.Parallel` (never emitted by the planner) is unordered
- **A serial phase between parallel phases is the critical path on a many-core machine, and a CPU profile does not show it.** A profile apportions CPU across 24 cores; wall is apportioned by timing the phases of the program. The typed window's profile pointed at the sort (worth 0.5 s once parallel); a phase timing named three serial steps — a concatenation, a partition map, an emit reading rows at random — worth 2.9 s of 8, each a few dozen lines to run as ranges on every core (`parallelRanges`, `partitionIndex`, the gather in `windowRows`; DFC158 §3.9–§3.10, 2026-10-29). Before parallelising anything, time the phases; a serial pass over every row (a map, an append loop, an emit) between parallel ones goes first
- **Always shadow the loop variable** (`shard := shard`) inside `for _, shard := range shards { go func() { ... } }`. Go 1.22+ scoping helps but doesn't cover every case.
- **`go test -race ./typed/...` is a hard CI gate** for any commit that touches `typed/stream.go`.
- **Keep failed concurrency experiments as reference negative results** rather than deleting them. `BenchmarkScaleTypedParallel3Join` is the canonical example — channel-based Parallel measured 11.65s vs 5.30s single-threaded.

### Refactor CLI When Exporting New Package Functions (CRITICAL)
EVERY time a new public function is added to the ssql package, check whether existing CLI commands can be simplified. The CLI should be a thin layer over ssql package primitives.

**Checklist when exporting new functions:**
- [ ] Can any CLI command in `cmd/ssql/commands/` use this function instead of internal logic?
- [ ] Is there duplicated logic between CLI commands that this function could unify?
- [ ] Does the generated code call the same functions as the CLI execution path?

### Shell Command Injection Prevention (CRITICAL)
When constructing shell commands for remote execution (SSH, bash -c):
- Use `ssql.ShellQuote()` for all dynamic values
- Use `exec.Command("ssh", host, cmd)` — no shell interpretation
- Whitelist-validate constrained values (like format names)
- **Use absolute paths for ALL commands executed remotely** (`/usr/bin/head`, etc.) — bare command names can be hijacked via PATH manipulation. For ssql itself, never hard-code `/usr/bin/ssql`: use `ssql.BuildRemoteCommand` / `ssql.RemoteScriptCommand`, whose prologue resolves the remote binary from a FIXED list of absolute locations (`/usr/bin`, `/usr/local/bin`, `$HOME/go/bin`, `$HOME/.local/bin` — `ssql.RemoteBinPrologue`) or takes the `-remote-bin` / catalog `bin` absolute path; a go-install user has no `/usr/bin/ssql` (v4.94.0)
- Never concatenate raw strings from files or user input into command strings

## Development Commands

**Before testing any CLI behavior**, always rebuild and install the latest binary:
```bash
go build -ldflags "-s -w" -o ~/go/bin/ssql ./cmd/ssql && ssql version
```
Stale binaries in PATH are a common source of false failures.

- `go build` / `go test` / `go test -v` / `go test -run TestName`
- `go fmt ./...` / `go vet ./...` / `go mod tidy`
- `go build ./cmd/ssql/...` (not `go build ./...` which fails on examples dir)

## Release Process

Version is stored in `cmd/ssql/version/version.txt` (WITHOUT "v" prefix). Key rules:
- version.txt: `X.Y.Z` (no "v"), git tag: `vX.Y.Z` (with "v")
- Update `cmd/ssql/version/commit.txt` with `git rev-parse --short=8 HEAD` before tagging
- Use annotated tags: `git tag -a vX.Y.Z -m "..."`
- go.mod must NOT contain `replace` directive for releases
- **Never re-tag a Go module version** — the checksum database is immutable
- Always verify with `GOPROXY=direct go install` before announcing
- Major version bumps only when explicitly requested

## Post-Release Checklist (CRITICAL)

After a minor/major release, always do ALL of these:

**Pre-tag gates** (before tagging, in addition to the standard suites):
- [ ] `SSQL_PERM_TRIPLES=1 go test ./cmd/ssql -run TestPipelinePermutationTriples -timeout=40m` — the opt-in 3-stage permutation gate (~300 pipelines × every lane, several minutes; not part of normal test runs)
- [ ] `SSQL_SCALE=1 go test ./cmd/ssql -run TestScaleBudgets -timeout=20m` — the opt-in scale gate (DFC113): wall-time budgets on a cached ~120MB fixture; catches complexity/read-amplification regressions that output oracles pass (~15s warm, ~40s on first fixture generation)
- [ ] `make playground && scripts/playground-test.sh` — the playground end-to-end (headless Chrome driving the real page): completion, help-at-cursor, runs, and a pipeline that fails mid-stream (the wasm exec shim must recover a library panic like the CLI does; one missing `ssql.Run` killed the page on the first error, found by Ross 2026-10-23)
- [ ] `scripts/explore-test.sh` — the explore end-to-end (headless Chrome over the embedded engine): its optimise cases failed from v4.114.0 to v4.120.1 unnoticed because it ran only when someone touched the explore page (DFC151 found it; fixed 2026-10-16)
- [ ] `make lanes` — the three oracle lanes a `generate sql` dialect and `generate rust` are verified by: Postgres on the LXD rig (`SSQL_TEST_PG_HOST=ssql-node1`; `lxc list` shows ssql-node1 RUNNING, PostgreSQL 16 answers over ssh), DataFusion through its Python bindings (`SSQL_DATAFUSION_PYTHON=~/.venvs/df/bin/python`; `python3 -m venv ~/.venvs/df && ~/.venvs/df/bin/pip install datafusion` recreates it), and the `rust` lane (`SSQL_RUST_LANE=1`, cargo from rustup in `~/.cargo/bin`; every case's `generate rust -func` program built into ONE crate under `~/.cache/ssql/rust/target`, the first build compiling DataFusion for minutes, DFC157 §5), ~6 min each over the equivalence, interchange and overflow suites. Both sat unused from v4.101 to v4.126 while three features shipped "documented" forms into them; the first runs back found `AVG` an exact NUMERIC on Postgres, 24 cases Postgres rejected (one of them the harness's own syntax error), and an integer SUM that WRAPS past 2^63 on DataFusion (Ross, 2026-10-27: "shows you how things drift when not tested")
- [ ] `make doc-test` — the L2 doc gate: every code block of every codelab (CLI: `codelab-run.sh`; Go record and typed: `codelab-go-run.sh`) executed against the checkout. Added to the pre-tag gates 2026-09-29 after a week of codelab edits shipped in two releases with only L1 (which compiles a fixed file list that does not include the codelabs) having run; it was green, but by luck

**Builds:**
- [ ] `make deb` — build `ssql_X.Y.Z_amd64.deb` and `ssql-gpu_X.Y.Z_amd64.deb` (stamped with HEAD's hash via `RELEASE_LDFLAGS`, so run it on the tagged commit; dev builds keep Go's VCS stamp with `-dirty`); it also rewrites the README's `.deb` install pins to X.Y.Z. Commit the debs AND README.md together (`make doc-check` Check 10 fails if pins, debs and version.txt disagree)
- [ ] `make install-local` — refresh BOTH `$GOPATH/bin/ssql` and `$GOPATH/bin/ssql_gpu` so the developer's shell resolves the latest version on the next `ssql` / `ssql_gpu` invocation. Verify final lines print `ssql vX.Y.Z` for both (no `gpu: no` + `gpu: yes` version mismatch). Replaces the older "`make build-gpu` and test" step which left the gpu binary out of `$GOPATH/bin` — gpu drifted from v4.32.0 to v4.44.0 unnoticed before being caught at v4.44.0 release.
- [ ] `make playground` — rebuild WASM playground locally if you want to test it before the release push (CI rebuilds and deploys it anyway)
- [ ] `make explore-wasm` — refresh the embedded explore engine (`cmd/ssql/wasm/ssql-playground.wasm.gz`) and commit it, so `to explore -wasm` ships the released engine

**Deployments:**
- [ ] Playground deploys automatically — `.github/workflows/playground.yml` pushes WASM + playground.html to `gh-pages` on push to main. Verify with `gh run list --workflow=playground.yml -L 1` (manual fallback steps in `claude/playground.md`)
- [ ] Cross-compile for WebVM (slim, as `claude/wasm.md` says; releases v4.116.0–v4.119.0 shipped the full build by habit): `CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -tags slim -ldflags "-s -w -X github.com/rosscartlidge/ssql/v4/cmd/ssql/version.Commit=$(git rev-parse --short=8 HEAD)" -o ~/src/ssql-terminal/dockerfiles/ssql ./cmd/ssql`
- [ ] Push WebVM binary and trigger deploy: `gh workflow run Deploy --ref main -f DOCKERFILE_PATH=dockerfiles/ssql_mini -f IMAGE_SIZE=750M -f DEPLOY_TO_GITHUB_PAGES=true -f GITHUB_RELEASE=false`

**Documentation audit:**
- [ ] Run `make doc-check` (L1 validation)
- [ ] Check README.md — examples reflect new features (version pins are mechanical: `make deb` rewrites them, doc-check Check 10 asserts them; the manual version of this item let the deb pin sit at 4.34.0 for ~70 releases)
- [ ] Check doc/cli-codelab.md — command syntax, flags, examples up to date
- [ ] Check doc/api-reference.md — new/changed functions documented
- [ ] Check doc/ai-code-generation.md — new features have examples
- [ ] Update doc/research/TODO.md to reflect current state
- [ ] `scripts/bench-engines.sh ~/csvs/shuffled.parquet` on an idle machine — the engine table of `doc/performance.md` §1a (typed Go, DuckDB, generated Rust; best of three) — and update the table when a row moved. A measurement, not a gate: the 2026-10-27 table found two pathologies and a planner gap in an afternoon (DFC158)

## Project History
See `claude/project-history.md` for version history and migration details.

## Architecture Overview

**Core Types:**
- `iter.Seq[T]` and `iter.Seq2[T,error]` - Go 1.23+ iterators (lazy sequences)
- `Record` - Encapsulated struct with private fields
- `MutableRecord` - Efficient record builder with in-place mutation
- `Filter[T,U]` - Composable transformations (`func(iter.Seq[T]) iter.Seq[U]`)

**Key Architecture Files:**
- `core.go` - Core types, Filter functions, Record system, composition
- `operations.go` - Stream operations (Map, Where, Reduce, etc.)
- `chart.go` - Interactive Chart.js visualization
- `io.go` - CSV/JSON I/O, command parsing, file operations
- `sql.go` - GROUP BY aggregations and SQL-style operations

## Record API Rules (CRITICAL)
See `claude/record-api.md` for full API and migration guides.

**Rules (never violate):**
- **ALWAYS use `GetOr()` to read fields** — never direct map access or type assertions (will panic)
  - `ssql.GetOr(r, "name", "")` for strings
  - `ssql.GetOr(r, "age", int64(0))` for ints
  - `ssql.GetOr(r, "price", float64(0))` for floats
- **Use `MutableRecord` builder for construction**, convert to `Record` via `.Freeze()`
- **Iterate with `.All()`** (maps.All pattern), `.KeysIter()`, or `.Values()`
- **In generated code**: always `ssql.GetOr(r, field, default)` — never `r[field].(type)`

## WASM Explore Engine (DFC107)

`to explore -wasm` embeds the SAME slim playground wasm (gzipped ~4.5MB — DFC151 cut `net/http` out of slim, 6.5 → 4.5; `make explore-wasm` and `make playground` fail over `WASM_BUDGET_MB`, and `TestSlimBuildDependencies` names any forbidden package that drifts back in — `cmd/ssql/wasm/ssql-playground.wasm.gz`, `//go:embed` behind `!slim`) — the TinyGo mini-engine (a third, untested semantics implementation) was REMOVED; explore ops run through the real engine via an `ssqlExec` shim. Refresh the artifact with `make explore-wasm` (part of the release checklist); e2e gate: `scripts/explore-test.sh`. Slim builds error loudly on `-wasm` (a wasm can't embed itself).
```bash
ssql from data.csv | ssql to explore -wasm output.html
make explore-wasm && go install ./cmd/ssql
```

## API Naming Conventions (SQL-Style)

- **`SelectMany`** (NOT FlatMap) - Flattens nested sequences
- **`Where`** (NOT Filter) - Filters records based on predicate
- **`Select`** - Projects/transforms fields
- **`Update`** - Modifies record fields (convenience wrapper)
- **`Reduce`** - Aggregates sequence to single value
- **`Take`** / **`Skip`** - LIMIT / OFFSET
- **`GroupByFields`** / **`Aggregate`** - SQL GROUP BY

## Canonical Numeric Types

- **Scalars**: Always `int64` and `float64` (never `int`, `int32`, `float32`); `ssql.Int128` for width (the `int128` column: a cast, a literal past int64, or an integer sum — never an operator overflow; DFC155 C); `ssql.Decimal` for money (the `decimal:S` column: by declaration only — a cast, `-type` on a source, a header, a Parquet DECIMAL — never inferred from `12.34`; `+ -` at the larger scale, `*` at the summed scale, `/` a float, every rescale half away from zero; DFC155 E)
- **Sequences**: Flexible (`iter.Seq[int]`, `iter.Seq[float32]`, etc.)
- CSV auto-parsing produces `int64`/`float64`. Use `int64(0)`/`float64(0)` as defaults with `GetOr()`.

## CLI Command Rules (CRITICAL)
See `claude/cli-architecture.md` for full autocli patterns, subcommand registration, and examples.

**Rules (never violate):**
- **Hierarchical indentation for command builders** — indent to show the structure: command → flag → flag details → done. Blank lines between flag blocks. Example:
  ```go
  cmd.Subcommand("sort").
      Description("Sort records by one or more fields").
      ClauseDescription("Each clause specifies fields with a sort direction").

      Flag("FIELDS").
          String().
          Variadic().
          Required().
          Help("Fields to sort by").
          Done().

      Flag("-desc", "-d").
          Bool().
          Local().
          Help("Sort descending").
          Done().
  ```
- **Fail loudly on invalid input** — unknown field names, unknown operators, and invalid flag values MUST terminate the command with a clear error message. NEVER silently produce empty or wrong results. Validate field names against the first record (list available fields in the error). Validate operators and constrained values at parse time. This applies in both normal execution and `-generate` code generation mode.
- **`FieldsFromFlag("FILE")` for field name completion** — NEVER use `NoCompleter` when field names can be derived from a data file. This applies to `-if`, `-sum`, `-avg`, `-min`, `-max`, `-field`, `-group`, etc.
- **Nothing is cached across pipe boundaries** (the `AUTOCLI_FIELDS` name cache went in autocli ≥ v4.10.0; the `AUTOCLI_CACHE_FILE` value-path export went in v4.13.0 — both went stale across pipelines). Cross-pipe Tab returns actionable hints (`Use-Ctrl-O` for field names, `Values-Use-Ctrl-O` for values); **Ctrl-O** is position-aware and completes both — names via `SSQL_MODE=schema | generate schema`, values via `-value-source` sampling of the pipeline's own file. `AUTOCLI_CACHE_FILE` survives only as the per-invocation sampling parameter (never exported). See `claude/completion-system.md`.
- **`FieldValuesFrom("FILE", "field")` for field value completion** — complete with actual data values, not hints (still works cross-pipe via `AUTOCLI_CACHE_FILE`)
- **Every command MUST have 2-3 `.Example()` calls**
- **Use `.Accumulate()` for repeated flags** (e.g., `-if` appearing multiple times)
- **NEVER use in-argument delimiters or comma-separated values in a single flag** — use `.Accumulate()` for multiple values instead. BAD: `-columns "a,b,c"` or `-rename "old:new"`. GOOD: `-columns a -columns b -columns c` or `-as old new`. This applies to ALL flags that accept multiple values.
- **A flag whose arity an earlier argument decides uses `.Repeat(f)` on its last Arg** (autocli ≥ v4.22.0): `-set-macro FIELD NAME ARG...` takes as many fields as macro NAME has parameters, and the callback says so — exact parse errors, per-position completion. Never a variadic tail that guesses where the values end, never one flag per name registered from a config file. See `claude/cli-architecture.md` §4a and `commands/macro_flags.go` (DFC152).
- **Flags of one clause that create or assign columns apply in the order written** — read autocli's `Clause.Order` (v4.23.0) through one shared reader (`clauseSets` in `commands/update_sets.go` for update's `-set`/`-set-expr`/`-set-bucket`/`-set-field`), never the per-flag lists in an order the backend picks: four backends picked four orders for `update`'s new columns until 2026-10-20. A command that lowers one flag into another (`-set-macro` → `-set-expr`) keeps `Order` consistent (`expandClauseMacros`)
- **All data commands MUST support stdin/stdout** for Unix pipelines
- **Commands that accept file inputs (join, merge, union) read self-describing formats directly** — `.csv`/`.tsv`/`.json` files are extension-inferred through `readAuxInput` (`commands/aux_input.go`), the same convenience `from FILE` provides; `join customers.csv` ≡ `join <(ssql from csv customers.csv)` (pinned by the `join_direct_csv`/`join_procsub_csv` equivalence pair). Bare `.jsonl` still REQUIRES a `_schema` header (e.g. `ssql tee` output) — a headerless file silently loses field information; error with a message suggesting `<(ssql from jsonl FILE)`. Parquet/arrow route via procsub for now.
- **Add new commands to `TestFieldCompletionConfiguration`** in `completion_test.go`
- **New top-level commands MUST be registered in BOTH `cmd/ssql/main.go` AND `cmd/ssql-playground/main.go`** — the WASM browser playground has its own entry point with a separate `Register*` list. `TestPlaygroundMainMatchesCLIRegistration` in `cmd/ssql/registration_drift_test.go` enforces equality and names the drifted command. (Six commands silently disappeared from the playground for months because of this — `count`, `fft`, `ifft`, `convolve`, `correlate`, `spectrogram`.)
- **Debug completion with `-complete N`** — e.g., `./ssql -complete 5 from catalog file.csv -if ""`
- **Interactive completion / keybindings MUST be tested through a real pty** — never a hand-built `COMP_*`/`READLINE_*` or `bash -c`. bash scopes `COMP_LINE`/`COMP_WORDS` to the current command (so a hand-fed full line lies), and key bindings depend on the active keymap (emacs vs vi-insert vs vi-command) and `keyseq-timeout` — none of which a non-tty test exercises. `TestFieldKeybindingPTY` (`cmd/ssql/field_keybinding_pty_test.go`) drives real bash in emacs + vi with a low `keyseq-timeout`; mirror it for any new completion-keypress behaviour. (Three shipped bugs — Tab can't see the pipeline, vi keymap missing, chord vs keyseq-timeout — all passed non-pty tests and only a pty caught them.) See `doc/research/bash-pipeline-completion-options.md`.
- See `claude/completion-system.md` for field cache mechanism, SSH warmup, catalog completion, and debugging guide

## Code Generation Rules (CRITICAL)
See `claude/code-generation.md` for fragment system, testing patterns, and full examples.

**Rules (never violate):**
- **Mode env var is `SSQL_MODE`** (canonical since v4.46.0). The legacy `SSQLGO` is honoured as a **deprecated alias** (read via the single `modeEnv()` helper in `cmd/ssql/commands/helpers.go`; `SSQL_MODE` wins when both are set). `SSQLGO=1`/`SSQLGO=true` remain record-mode aliases. Emit `SSQL_MODE` in all new code/docs/examples; never reintroduce `SSQLGO` as the canonical name.
- **Every data-processing command MUST support `-generate` / `SSQL_MODE=record`** — a single missing command breaks entire pipelines.
- **PRIME DIRECTIVE for NEW commands (Ross, 2026-09-01): ship record codegen AND a typed template on day one, parallel where the semantics allow.** Barrier commands (global sort/materialize: sort, top, resample) declare SerialOnly so the planner inserts the Stream.Serial() boundary — that IS the correct parallel form for a barrier, not an excuse to skip typed. Schema-changing typed templates synthesize output structs (the group-by pattern in typed_groupby.go). New expr functions get transpiler support (or an explicit untranspilable error → record fallback) plus differential-corpus entries.
- **Every pipeline MUST generate working code in all modes** — `record` (`SSQL_MODE=record`) and `typed` (`SSQL_MODE=typed`). As of v4.40.0 `SSQL_MODE=typed` and `SSQL_MODE=parallel` are equivalent — both go through the planner, which picks the parallel form (Stream[T] + ReadCSVParallel + HashJoinParallel + GroupByParallel + per-shard sinks) when reachable and the serial form otherwise. The pipeline regression corpus in `cmd/ssql/corpus_test.go` runs three modes (record + typed + parallel) end-to-end as the regression gate (it drives the deprecated `SSQLGO=` alias, keeping back-compat covered); new pipelines and tutorial examples should be added there.
- **Typed-mode commands that have a parallel form MUST emit dual templates** — set `Code` to the parallel template (`Stream.Where`, `typed.HashJoinParallel`, `typed.GroupByParallel`, `typed.ReadCSVParallel`, …), `Capabilities = {Accepts: ShapeStream, Produces: ShapeStream}` (or the appropriate output shape), `AltCodeIfSeq` to the serial alternative, and `AltCapabilitiesIfSeq` to the iter.Seq[T] capabilities. The planner inspects downstream Capabilities and either keeps the parallel form or atomically swaps Code+Imports+Capabilities to the serial alternative. Single-template emission is correct only for commands that have no parallel runtime (e.g. `to table` — declares SerialOnly so the planner inserts a Stream.Serial() boundary upstream). A `SerialOnly` declaration is a performance claim about the WHOLE pipeline, not the stage — the planner downgrades the source read and every parallel-capable stage after it — so it carries a comment saying why (`group-by -presorted`: contiguous keys; `window`, `union`: barriers), and `generate go -explain` on `SOURCE | STAGE | group-by` must show no `downgraded` line for a per-row map. `cast` declared it without a reason from the day it was written to 2026-10-28, and `cast -type x decimal:2 | group-by` ran 1.29 s where the parallel group-by alone ran 0.15 s (DFC158 §2.3).
- **A synthetic typed source is a shard-aware generator, not a slice** — `typed.RangeParallel` (DFC154) splits the COUNT across shards and each shard counts its own interval, the way `ReadCSVParallel`'s shards know their first row; a generator whose rows derive from their offsets never materialises a slice to feed `ParallelFromSlice`. Make it generic over the row (`mk func(int64) T`) when a flag names the column: typed readers and writers take the column name from the struct tag, so a fixed struct cannot be renamed
- **CLI commands must use ssql package primitives**, not raw Go code
- **Generated code must be readable** — move complexity to helper functions in ssql package
- **All errors must cause pipeline failure** with clear messages (use error fragments in generation mode)
- **When adding new CLI features**: (1) add function to ssql package, (2) use it in CLI, (3) generate code that calls it
- **Test generation**: add tests to `cmd/ssql/generation_test.go`, test full pipeline round-trip
- **When changing a command, test ALL generate formats** — `generate go`, `generate sql`, and `generate ssql` may each have their own translation logic (e.g. SQL assembler parses the Command string). A feature that works in execution mode can silently break in generation.
- **`generate sql` reuses the same fragments** — no separate SQL generation path needed per command. The SQL assembler parses the `Command` string from each fragment.
- **Run the planner-driven corpus on every change touching codegen** — `go test ./cmd/ssql -run TestPipelineCorpus -timeout=10m`. It compiles + runs each pipeline through all three modes, catching things `go vet` and unit tests miss (unused imports after planner downgrade, type mismatches between Stream and iter.Seq, etc.).
- **A new expression function touches six places, and a test guards each** (DFC153): the VM binding (`expr_math.go` / `time_calendar.go`, the name in `ssql.ExprCompiledFunctions` so a call is a function and a bare name a field), the transpiler case (`expr_go.go` + `TestExprToGo` + the differential corpus), the SQL translation (`generate_sql_math.go` / `exprFuncs`, per-dialect names, `dialectRefuse` where an engine lacks it, `sqlFinite` on a result that can be NaN/±Inf — a non-finite number is ABSENT in every lane), the catalog (`functions.go`; `TestFunctionEntry` fails on a bound function without an entry), `doc/EXPRESSIONS.md`, and an equivalence case with a Golden from an independent oracle
- **Int64 arithmetic is checked in every lane** (DFC155 B): the VM patches `+ - *` into checked calls (`expr_checked.go`, carried by `ExprStandardOptions`), typed codegen emits `exprfn.Add/Sub/Mul`. Never emit a bare `+`/`-`/`*` on int64 operands and never sum integers as float64: an overflow is an error with the operands in the message, as every SQL engine raises it, not a wrap. `expr.Operator` overloading cannot do this — it needs operand types and ssql compiles against an untyped map
- **An integer sum is an int128 in every lane; nothing else widens** (DFC155 C): `-sum`, `-avg`, window and pivot sums, `sum()` and a `-expr` term accumulate in `ssql.Int128` (exec `numericSum`, typed `.Add`, the VM's `checkedSum`; wire type `int128`, `wireSum`); the SQL lane needs nothing (SUM(BIGINT) is HUGEINT). A column is int128 by `cast`, a wide source literal or a sum — an int64 operator overflow stays an error. The same VM patch carries the int128 arms for `+ - * / % **`, the six comparisons and `int/float/abs/min/max` (expr's own `Less` panics on a struct and `Equal` deep-compares); a new scalar type gets an arm at every `case int64` the surveys in DFC155 §7 list, and the equivalence harness compares integers as exact digits
- **The expr→Go transpiler (`cmd/ssql/commands/expr_go.go`) must reproduce expr-lang VM semantics EXACTLY** — int/int division is float64, `len()` counts runes, mixed-type `min()`/`max()` keeps the winner's own type (refuses to transpile). Any change to `exprToGo` MUST run `TestExprGoDifferential` (transpiled-vs-VM over a corpus — it caught a semantics assumption on its first run) and add the new construct to BOTH the unit table (`TestExprToGo`) and the differential corpus. Untranspilable constructs return an error → the caller falls back to record codegen; unknown fields are `exprUnknownFieldError` → loud. See `doc/research/expr-transpiler-implementation-plan.md`.
- **The N-way differential gate is `TestPipelineEquivalence` (`cmd/ssql/equivalence_test.go`)** — for any change to a command's *results* (ranking, filtering, aggregation, selection), add/adjust a case there, NOT just the smoke corpus. It runs every result-producing lane (exec / go-record / go-typed / go-parallel / generate-ssql, plus a duckdb lane — generate-sql executed by DuckDB, the independent second-engine oracle — when the binary is present) and asserts **byte-identical normalised output** (canonical JSONL, column-order and int/float normalised; ordered-list when the pipeline defines order else multiset), with optional `Golden` oracles on **shuffled** fixtures. This is what catches "correct in mode X, silently wrong in mode Y" — the `TestPipelineCorpus` Contains/Excludes smoke test does NOT (it passed while `top` on strings was wrong in 3 of 4 lanes). Prefer shuffled data with distinct values so a wrong selection actually diverges; set `Ordered: true` for sort/top pipelines.

## WASM and WASI Rules
See `claude/wasm.md` for all WASM/WASI knowledge: three build targets (browser, WebVM, WASI), performance characteristics, AOT compilation, deployment workflows, and the docker cp uid/gid bug.

See `claude/playground.md` for playground-specific data files, testing workflow, and chart support.

- **Three WASM targets:** browser (`js/wasm`), WebVM (`linux/386`), WASI (`wasip1/wasm`) — all use the slim build: no arrow/parquet/xlsx, no `serve`, no `net/http` (URL sources refuse loudly), no `go/format`, no embedded explore engine (DFC151). Only the browser build is a release artifact; WASI is `make wasi` locally
- **WASI AOT is near-native** for interactive use (1.2x native). Use `wasmtime compile` to precompile.
- **Always test playground examples against `cmd/ssql-playground/data/` with real ssql before adding to `playground.html`**
- **WASM deploy needs /tmp dance** — build on main, copy to /tmp, switch to gh-pages, copy from /tmp (WASM is gitignored on main)
- **WebVM docker cp -a loses uid/gid** — deploy workflow has chown fix, don't remove it
- No WASM rebuild needed for HTML/JS/data changes — just refresh browser

## GPU Acceleration Rules
See `claude/gpu-acceleration.md` for benchmarks, build instructions, and detailed analysis.

**TL;DR:** GPU wins for compute-heavy ops (convolution 320x, FFT 21x+). CPU wins for memory-bound ops (aggregations 7x faster). Don't use GPU for simple aggregations or small datasets (<100K elements).

## Reference Reading

**WARNING: `claude/` files are NOT automatically loaded into context.** If a rule only exists in a `claude/` file and not in this file, it will be ignored. Every critical rule must have at least a one-liner in CLAUDE.md — the `claude/` files provide detailed examples and rationale, not the rules themselves. When adding new conventions to `claude/` files, always add the corresponding rule here too.

**Searching the docs and journals:** `scripts/docsearch.sh 'your query'` — hybrid BM25 + embedding (local ollama nomic-embed-text) search over `doc/`, `claude/`, `journal/` and root docs, returning ranked `file:line` chunks. Concept queries work ("avoid copying file into memory" finds the mmap work without the word mmap); grep only wins when you already know the identifier. Use it at these moments, not just when something "smells" prior-art: (1) on startup, after the journal, for the session's task topic; (2) before writing any DFC — `scripts/dfc.py --new TOPIC WORDS` does this automatically and prints related docs with the number; (3) before answering any "why did we / have we ever" question; (4) before designing anything that may have been tried, decided, or measured before. A `UserPromptSubmit` hook (`scripts/docsearch-hook.sh`, registered in `.claude/settings.local.json`) also injects top hits for substantive prompts automatically — treat them as hints. `-lexical` skips embeddings (works without ollama); first run after doc changes re-embeds only changed chunks. Cache is `.docsearch-cache.jsonl` (gitignored).

**The journal is equally important.** It contains recent decisions, what was tried, what worked, what didn't, and what's in progress. Without reading it, you risk re-solving solved problems, contradicting recent decisions, or duplicating work. Always read the latest entry on startup (see "On Startup" above).

For detailed examples, rationale, and history, read the relevant `claude/` file:

| Topic | File |
|---|---|
| CLI patterns, completers, subcommands | `claude/cli-architecture.md` |
| Completion system, field cache, SSH warmup, debugging | `claude/completion-system.md` |
| Code generation system | `claude/code-generation.md` |
| Performance patterns | `claude/performance-patterns.md` |
| Concurrency patterns and lessons | `claude/concurrency.md` |
| Record API | `claude/record-api.md` |
| GPU acceleration | `claude/gpu-acceleration.md` |
| autocli migration | `claude/autocli-migration.md` |
| Version history | `claude/project-history.md` |
| WASM playground | `claude/playground.md` |
| WASM/WASI builds, AOT, WebVM | `claude/wasm.md` |
| Documentation validation levels (doc-check / doc-test / doc-verify) | `claude/doc-validation.md` |

## Arrow & Parquet Format Support

Arrow for high-performance I/O (10-20x faster, zero-copy, GPU-ready). Parquet for compressed columnar storage (DuckDB compatible, Snappy compression).
```bash
ssql from data.arrow | ssql where -if age gt 25 | ssql to arrow output.arrow
ssql from data.parquet | ssql where -if age gt 25 | ssql to parquet output.parquet
```
**Note:** Parquet requires a file (random-access format) — no stdin support.

## SQL Generation

`generate sql` produces DuckDB-compatible SQL from the same pipeline fragments as `generate go`:
```bash
(export SSQL_MODE=record; ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -sum salary total | ssql to table) | ssql generate sql | duckdb
```
DuckDB is installed at `~/.local/bin/duckdb` for testing generated SQL.
