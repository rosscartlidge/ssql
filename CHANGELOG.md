# Changelog

All notable changes to ssql will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
