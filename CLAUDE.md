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
- `README.md`, `doc/api-reference.md`, `doc/cli-codelab.md`, `doc/cli-debugging.md`
- `doc/cli-troubleshooting.md`, `doc/EXPRESSIONS.md`, `doc/ai-code-generation.md`
- Validate: `make doc-check` (L1), `make doc-test` (L2), `make doc-verify` (L3)
- Common mistakes: changing API/CLI without updating docs, using old import paths/command names/flag names

## Development Principles (CRITICAL)

### Refactor While You Work
Whenever fixing bugs or adding features, always look for opportunities to refactor and simplify the surrounding code. Extract shared helpers, remove duplication, simplify control flow. Leave the code better than you found it.

### If It's Not Tested, It Will Break
Features without tests will eventually be removed during refactoring (learned when field/value completion was lost in v3.2.0).

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
- **Profile before optimizing**: `go test -cpuprofile cpu.prof -bench BenchmarkName`

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
- **Use absolute paths for ALL commands executed remotely** (`/usr/bin/ssql`, `/usr/bin/head`, etc.) — bare command names can be hijacked via PATH manipulation
- Never concatenate raw strings from files or user input into command strings

## Development Commands

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

**Builds:**
- [ ] `make deb` — build `ssql_X.Y.Z_amd64.deb` and `ssql-gpu_X.Y.Z_amd64.deb`, commit to repo
- [ ] `make build-gpu` — build and test `ssql_gpu` binary
- [ ] `make playground` — rebuild WASM playground

**Deployments:**
- [ ] Update `gh-pages` branch with new playground (see `claude/playground.md` for steps)
- [ ] Cross-compile for WebVM: `CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -ldflags "..." -o ~/src/ssql-terminal/dockerfiles/ssql ./cmd/ssql`
- [ ] Push WebVM binary and trigger deploy: `gh workflow run Deploy --ref main -f DOCKERFILE_PATH=dockerfiles/ssql_mini -f IMAGE_SIZE=750M -f DEPLOY_TO_GITHUB_PAGES=true -f GITHUB_RELEASE=false`

**Documentation audit:**
- [ ] Run `make doc-check` (L1 validation)
- [ ] Check README.md — version numbers, examples reflect new features
- [ ] Check doc/cli-codelab.md — command syntax, flags, examples up to date
- [ ] Check doc/api-reference.md — new/changed functions documented
- [ ] Check doc/ai-code-generation.md — new features have examples
- [ ] Update doc/research/TODO.md to reflect current state

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

## WASM Explore Module

Always use TinyGo to build (`make wasm`). Embedded in ssql binary via `//go:embed`.
```bash
ssql from data.csv | ssql to explore -wasm output.html
make wasm && go install ./cmd/ssql
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

- **Scalars**: Always `int64` and `float64` (never `int`, `int32`, `float32`)
- **Sequences**: Flexible (`iter.Seq[int]`, `iter.Seq[float32]`, etc.)
- CSV auto-parsing produces `int64`/`float64`. Use `int64(0)`/`float64(0)` as defaults with `GetOr()`.

## CLI Command Rules (CRITICAL)
See `claude/cli-architecture.md` for full autocli patterns, subcommand registration, and examples.

**Rules (never violate):**
- **Fail loudly on invalid input** — unknown field names, unknown operators, and invalid flag values MUST terminate the command with a clear error message. NEVER silently produce empty or wrong results. Validate field names against the first record (list available fields in the error). Validate operators and constrained values at parse time. This applies in both normal execution and `-generate` code generation mode.
- **`FieldsFromFlag("FILE")` for field name completion** — NEVER use `NoCompleter` when field names can be derived from a data file. This applies to `-if`, `-sum`, `-avg`, `-min`, `-max`, `-field`, `-group`, etc.
- **`FieldValuesFrom("FILE", "field")` for field value completion** — complete with actual data values, not hints
- **Every command MUST have 2-3 `.Example()` calls**
- **Use `.Accumulate()` for repeated flags** (e.g., `-if` appearing multiple times)
- **NEVER use in-argument delimiters or comma-separated values in a single flag** — use `.Accumulate()` for multiple values instead. BAD: `-columns "a,b,c"` or `-rename "old:new"`. GOOD: `-columns a -columns b -columns c` or `-as old new`. This applies to ALL flags that accept multiple values.
- **All data commands MUST support stdin/stdout** for Unix pipelines
- **Commands that accept file inputs (join, merge, union) MUST require schema-header JSONL** — plain JSONL without a `_schema` header will silently lose field information. Error with a message suggesting `<(ssql from jsonl FILE)` to add the schema. Only `ssql from jsonl` accepts plain JSONL.
- **Add new commands to `TestFieldCompletionConfiguration`** in `completion_test.go`
- **Debug completion with `-complete N`** — e.g., `./ssql -complete 5 from catalog file.csv -if ""`
- See `claude/completion-system.md` for field cache mechanism, SSH warmup, catalog completion, and debugging guide

## Code Generation Rules (CRITICAL)
See `claude/code-generation.md` for fragment system, testing patterns, and full examples.

**Rules (never violate):**
- **Every data-processing command MUST support `-generate` / `SSQLGO=1`** — a single missing command breaks entire pipelines
- **CLI commands must use ssql package primitives**, not raw Go code
- **Generated code must be readable** — move complexity to helper functions in ssql package
- **All errors must cause pipeline failure** with clear messages (use error fragments in generation mode)
- **When adding new CLI features**: (1) add function to ssql package, (2) use it in CLI, (3) generate code that calls it
- **Test generation**: add tests to `cmd/ssql/generation_test.go`, test full pipeline round-trip
- **When changing a command, test ALL generate formats** — `generate go`, `generate sql`, and `generate ssql` may each have their own translation logic (e.g. SQL assembler parses the Command string). A feature that works in execution mode can silently break in generation.
- **`generate sql` reuses the same fragments** — no separate SQL generation path needed per command. The SQL assembler parses the `Command` string from each fragment.

## WASM Playground Rules
See `claude/playground.md` for data files, testing workflow, and chart support.

- **Always test playground examples against `cmd/ssql-playground/data/` with real ssql before adding to `playground.html`**
- Static CSV data lives in `cmd/ssql-playground/data/` — same files used by playground and local testing
- No WASM rebuild needed for HTML/JS/data changes — just refresh browser

## GPU Acceleration Rules
See `claude/gpu-acceleration.md` for benchmarks, build instructions, and detailed analysis.

**TL;DR:** GPU wins for compute-heavy ops (convolution 320x, FFT 21x+). CPU wins for memory-bound ops (aggregations 7x faster). Don't use GPU for simple aggregations or small datasets (<100K elements).

## Reference Reading

**WARNING: `claude/` files are NOT automatically loaded into context.** If a rule only exists in a `claude/` file and not in this file, it will be ignored. Every critical rule must have at least a one-liner in CLAUDE.md — the `claude/` files provide detailed examples and rationale, not the rules themselves. When adding new conventions to `claude/` files, always add the corresponding rule here too.

**The journal is equally important.** It contains recent decisions, what was tried, what worked, what didn't, and what's in progress. Without reading it, you risk re-solving solved problems, contradicting recent decisions, or duplicating work. Always read the latest entry on startup (see "On Startup" above).

For detailed examples, rationale, and history, read the relevant `claude/` file:

| Topic | File |
|---|---|
| CLI patterns, completers, subcommands | `claude/cli-architecture.md` |
| Completion system, field cache, SSH warmup, debugging | `claude/completion-system.md` |
| Code generation system | `claude/code-generation.md` |
| Performance patterns | `claude/performance-patterns.md` |
| Record API | `claude/record-api.md` |
| GPU acceleration | `claude/gpu-acceleration.md` |
| autocli migration | `claude/autocli-migration.md` |
| Version history | `claude/project-history.md` |
| WASM playground | `claude/playground.md` |

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
(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -sum salary total | ssql to table) | ssql generate sql | duckdb
```
DuckDB is installed at `~/.local/bin/duckdb` for testing generated SQL.
