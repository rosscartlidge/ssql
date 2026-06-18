# TODO

Tracked issues and feature gaps discovered during development.

## Multi-file `from` (see multi-file-from.md)

- [x] **Phase 1: Core multi-file support** — `ssql from csv *.csv` with `-merge-schemas` and `-source`. Supports csv, tsv, json, jsonl.
- [x] **Phase 2: Pushdown** — `ssql from csv *.csv -- where -if age gt 25`. Spawns sub-pipeline per file, merges JSONL output. Supports multi-stage via `+` separator. Works for csv, tsv, json, jsonl.
- [x] **Phase 3: Parallel reading** — pushdown subprocesses run concurrently (capped at NumCPU), output preserves file order. 4x faster on 10×100k row files.

## Build System

- [x] **Split from.go into per-format files** — `from.go` → `from_csv.go`, `from_tsv.go`, `from_json.go`, `from_arrow.go`, `from_parquet.go`, `from_wav.go`, `from_xlsx.go`, `from_ssh.go`, `from_catalog.go`, plus shared helpers in `from.go`.
- [x] **Split to.go similarly** — `to.go` (1802 lines) → 12 files: `to_table.go`, `to_csv.go`, `to_tsv.go`, `to_json.go`, `to_arrow.go`, `to_parquet.go`, `to_wav.go`, `to_xlsx.go`, `to_chart.go`, `to_animate.go`, `to_explore.go`, plus `to.go` (49 lines).
- [x] **Slim build tag** — `go build -tags slim` excludes arrow, parquet, xlsx. Binary: 52MB → 11MB. WASM: 68MB → 13MB. Both playgrounds deployed with slim builds.

## SQL Generation (`generate sql`)

- [x] **Window functions** — all 15 window functions translated: ranking (ROW_NUMBER, RANK, DENSE_RANK, PERCENT_RANK), offset (LAG, LEAD, FIRST_VALUE, LAST_VALUE), aggregate (SUM, AVG, COUNT, MIN, MAX), and NTILE. Multi-clause windows, custom frames, PARTITION BY, ORDER BY all supported.
- [x] **rename / cast / update** — translated using DuckDB's `* RENAME`, `* REPLACE (CAST(...))`, and `* REPLACE (CASE WHEN ... END)`.
- [x] **include / exclude** — `include` translates to explicit column list, `exclude` uses DuckDB's `SELECT * EXCLUDE (...)`.
- [x] **from ssh / from catalog** — errors clearly: "has no SQL equivalent — it is an ssql-specific distributed feature".

## WASM Playground

- [x] **Binary size** — slim build tag reduces WASM from 68MB to 13MB raw. Both playgrounds deployed with slim builds.
- [x] **GitHub Pages deployment** — live at `https://rosscartlidge.github.io/ssql/playground.html`. Manual push to `gh-pages` branch.
- [x] **Chart rendering** — `to chart` output rendered inline via iframe.
- [x] **Progressive examples** — 15 examples from simple to complex, with static data files.
- [x] **Data viewer** — click dataset names to view raw CSV.
- [x] **# comments** — comment out pipeline stages to build up incrementally.
- [ ] **GitHub Actions automation** — build WASM and deploy to `gh-pages` automatically on push to `main`.
- [ ] **Share links** — encode pipeline in URL fragment for sharing.
- [x] **Syntax highlighting** — Prism.js (CDN) highlights generated Go and SQL with Tokyo Night theme colors.
- [x] **Loading indicator** — not needed after slim build (13MB loads fast on phone and desktop).

## WebVM Terminal

- [x] **Field completion** — was missing `jq` in Docker image. Fixed.
- [x] **SSH support** — openssh-client, ping, .ssh/config with Tailscale defaults, auto-generated keypair.
- [x] **Build hash in welcome** — shows ssql version with commit hash on boot.
- [ ] **Mobile touch bar** — Tab/arrows/Ctrl keys for phones. Reverted — broke CheerpX boot on Pixel 7. Needs approach that doesn't touch Svelte component (pure DOM injection after boot).

## Tab Completion

- [x] **`ssql from <tab>` should also complete filenames** — autocli v4.3.7 returns both subcommand names and file completions when a command has both.

## Merge with Catalog (see merge-catalog.md)

- [x] **Phase 1: Core `-catalog` flag** — `ssql merge -catalog shards.csv -by timestamp`. Opens SSH to each catalog entry, streams JSONL into K-way merge heap. Supports pushdown via `--` and partition pruning via `-if`. Streaming, O(K) memory.
- [x] **Phase 2: Shard metadata enrichment** — `-shard-field source` adds host:path to each record.
- [x] **Phase 3: GPU support** — `-gpu` flag to use `ssql_gpu` on remote nodes.
- [x] **Optimizer rules** — `merge-catalog-predicate-pushdown` and `merge-catalog-aggregation-pushdown` push where/group-by into `--` pushdown.
- [x] **Catalog glob expansion** — paths with `*`, `?`, `[` expanded before processing. `-catalog-used FILE` for auditing.
- [x] **Hostname-aware local routing** (v4.43.1). Catalog rows with `host=$HOSTNAME` (matching `os.Hostname()`) run locally instead of via ssh. Catalog CSVs are now portable across hosts. Public helper `ssql.IsLocalHost(host)` applies symmetrically to `ProcessCatalogShards` and `ProcessCatalogShardsRemoteGo`.

## Pipeline Optimizer (`generate ssql`)

- [x] **Join predicate pushdown for process substitutions** — `<(ssql from file.csv)` joins now support predicate pushdown.
- [x] **Multi-file predicate/aggregation pushdown** — `from csv *.csv | where ...` → `from csv *.csv -- where ...`
- [x] **Merge catalog predicate/aggregation pushdown** — same pattern for `merge -catalog`
- [x] **SSH pushdown with expressions** — already works. The pushdown rule copies all `where` args (including `-if-expr`) wholesale.

## Unified Typed Mode (see typed-auto-parallel-proposal.md, mixed-mode-pipelines-proposal.md)

- [x] **Phase A — typed-mode planner + auto-parallel selection** (v4.39.0). Per-fragment Capabilities + Shape; planner picks Stream[T] vs iter.Seq[T] per pipeline stage; `generate go -explain` shows decisions; 24-pipeline regression corpus across record/typed/parallel modes.
- [x] **SSQLGO=typed becomes auto-parallel default** (v4.40.0). Drops the per-command `parallelMode()` gate; SSQLGO=parallel now a silent alias. Dual-template emission extended to join (HashJoinParallel/HashJoin) and group-by (GroupByParallel/GroupBy).
- [x] **`ssql count` sink** (v4.40.0). Discoverable wc -l for pipelines with planner-picked runtime (record loop / typed.Count / Stream.SerialCount). Surfaced and fixed planner Phase 1b (per-fragment shape coercion).
- [x] **Phase B — mixed-mode pipelines (typed→Record adapter)** (v4.40.0). Tier 3 commands (pivot, signal/fft/ifft/convolve/correlate/spectrogram, merge -catalog, from ssh, from catalog, -if-expr, -set-expr) no longer error under SSQLGO=typed; planner inserts toRecord adapter automatically. Stream→Record chains Serial() + toRecord(). 15 typed-aware commands fall through to record-mode codegen when prevSchema is nil.
- [x] **Parallel projection (StreamSelect)** (v4.40.0). emitTypedProjection emits typed.StreamSelect (Stream[T]→Stream[U]) as default; planner picks vs typed.Select.
- [ ] **Phase C — Record→typed reverse adapter via `--into MyStruct` hint**. Lets pipelines like `from ssh ... | ssql where -if x gt 5 --into MyRow | typed group-by ...` work. Estimate: ~1 week.
- [ ] **Phase D (deferred indefinitely)** — Record→Stream[T] (parallel typed source from a Record stream). Useful but no critical path.
- [ ] **mmap CSV reader** — see `mmap-readers-proposal.md`. Replacing `os.ReadFile` with `mmap` in `typed.ReadCSVParallel` / `ReadDelimParallel` measured 1.7-1.9× faster slurp on a 1.23 GB CSV (~0.79s → ~0.56s wall on the headline parallel-CSV path). Helper: linux/darwin amd64/arm64 use real mmap; Windows/386/wasi fall back to os.ReadFile. Add MADV_DONTDUMP. Document SIGBUS risk for files modified during use. Estimate: ~half-day.
- [x] **Remote Go execution Phase A — interactive `-remote` flag** (v4.41.0). Shipped with the .ssql-script-as-unit framing; replaced by codegen-symmetric path in v4.42 (see below).
- [x] **Remote Go execution Phase B — codegen-symmetric pushdown** (v4.42.0). Codegen-symmetric ssh pushdown for `from ssh`. Whatever mode the local pipeline runs in, the remote runs in too. Generated Go embeds the `.ssql` script as a const, ships+runs via ssh.
- [x] **Remote Go execution Phase C — catalog extension** (v4.43.0). Per-shard ship-and-run via the codegen-symmetric path; embedded .ssql template substituted with each shard's path at runtime; parallel orchestration with `-shard-concurrency`/`-shard-order`/`-keep-going` flags; auto-emitted `# require: vX.Y.Z` header for version-skew detection. End-to-end verified: CLI baseline, SSQLGO=record, SSQLGO=typed all produce byte-identical cross-shard aggregation. Optional Phase C-bis (CLI-baseline parallelism for the v4.27 path) deferred.
- [x] **Codegen wrapper** (v4.41.0 / v4.42.0). `ssql generate go -script PATH` reads the pipeline from a file or `<(heredoc)`. Used internally by the v4.42+ codegen-symmetric ssh path; available to users for hand-authored pipelines too. The `ssqlgen` shell-helper part of the original proposal hasn't shipped — power-user form (`(export SSQLGO=...; ...) | ssql generate go`) keeps working.

## autocli-shell stack (see autocli-shell-proposal.md)

The same autocli `Command` tree powers the bash CLI today AND drives long-running services with a router-style operator console over SSH. Foundation for `ssql serve` (Phase 1, below).

- [x] **Phase A — autocli engine split** (autocli v4.5.0, 2026-05-12). `Command.Complete(args, pos) ([]string, error)` and `Command.ExecuteWith(args, base *Context) error` exposed as pure functions. `Context.Stdin/Stdout/Stderr/Ctx/State` accessors; bash CLI path unchanged.
- [x] **Phase B — autocli/shell readline driver** (autocli/shell v0.1.1, 2026-05-12). `chzyer/readline` loop wired to `cli.Complete`/`cli.ExecuteWith`. Built-in `:exit`/`:quit`/`:help`/`:history`/`:set vi`/`:set emacs`. Sub-module so core stays stdlib-only.
- [x] **Phase C — autocli/ssh server** (autocli/ssh v0.1.0, 2026-05-12). Wraps `crypto/ssh` around `autocli/shell`. ed25519 host-key load-or-generate, OpenSSH `authorized_keys` parsing, `AuthCallback` override, refuse-to-start safety, `ConnMeta` audit hooks, graceful shutdown.
- [x] **Phase D — ssql serve subcommand** (ssql v4.44.0, 2026-05-13). First consumer; behind `!slim` build tag.
- [x] **Migrated off `chzyer/readline`** (ssql v4.44.4, autocli/shell v0.2.0, 2026-05-15). Three bugs all of the same shape — library hardcoded `os.Stdin` — drove the rewrite onto `golang.org/x/term`. Retires the proposed upstream PR (we don't depend on the library any more). Trade-off: lost vi mode and Ctrl-R reverse-search, both deemed acceptable.
- [x] **Generic `:set` Settings registry** (shell v0.2.1, ssh v0.1.10, ssql v4.44.5, 2026-05-18). Replaces the dead vi/emacs toggle with a service-supplied `[]Setting{Name, Description, Get, Set}` registry. First setting wired: `head-default-rows` on `ssql serve`.
- [x] **Per-user history dir on `ssql serve`** (v4.44.2). `-session-dir DIR` flag exposes `autocli/ssh.Options.HistoryDir`. Per-user history + (formerly) prefs files under `$DIR/$user/`.
- [x] **Pipes in autocli/shell (Position 2)** (autocli/shell v0.3.0/v0.3.1, ssql v4.45.0, 2026-05-19/2026-05-20). `splitOnPipe` + `runPipeline` with `io.Pipe` between stages; pipe-aware tab completion; `io.ErrClosedPipe` suppression for early-exit stages; `ctrlCReader` translates 0x03→0x0d so Ctrl-C cancels the line instead of killing the session; single-command stdin is `bytes.NewReader(nil)` so bare transforms degrade to zero-record instead of swallowing keystrokes. `from-loaded` source in `ssql serve` emits schema-headed JSONL; all bash transforms (where, sort, group-by, pivot, window, …) and stream sinks (table/csv/tsv/json/jsonl) registered on the serve CLI tree. `DisplayTableTo` / `DisplayTableWithFieldsTo` writer-accepting variants in `ssql/io.go`.
- [ ] **`let $name = pipeline` variables** — REPL-friendly replacement for bash process substitution. Documented as v2-of-shell scope; grammar already reserved.
- [x] **SSH window-size propagation** (ssql v4.44.6, shell/v0.2.2, ssh/v0.1.11, 2026-05-19). `autocli/ssh` parses pty-req + window-change SSH payloads and pushes (cols, rows) through `shell.Options.ResizeChan`; `shell.Serve` calls `Terminal.SetSize`. Wide rows render at full terminal width; resize mid-session works.
- [x] **Handler IO migration** (ssql commit 340e025, 2026-05-19). Ssql's command handlers now use `ctx.Stdin/Stdout/Stderr` accessors instead of `os.Stdin/Stdout/Stderr` directly. Zero behaviour change for bash users; unblocks Position 2 pipes for `ssql serve`. From-sources, codegen, file-only sinks intentionally left on `os.*`.
- [x] **`os.Exit` audit** (2026-05-20). All 49 occurrences inspected; all are inside code-emission templates that emit `os.Exit(1)` into the generated standalone Go programs (correct). No handler-direct `os.Exit` exists, so Position 2 pipes are safe from stages terminating mid-flight.
- [x] **Pipeline-aware completion via `SSQL_MODE=schema`** (ssql v4.46.0, autocli v4.8.0 / shell v0.4.0 / ssh v0.1.13, 2026-06-15→17; see [schema-aware-completion.md](schema-aware-completion.md) §0). Shipped both runtimes: `ssql serve` walks the upstream stages through per-command `schemaOp`s in-process (autocli `CompleteWithContext` + `SchemaWalk` hook + `FieldsFromFlag`→`ChainCompleter{UpstreamFieldsCompleter,…}`); the `SSQL_MODE=schema` two-pass engine (`(export SSQL_MODE=schema; <upstream>) | ssql generate schema`) computes a pipeline's output schema without reading data. One rule per command (`cmd/ssql/commands/schema_ops.go`): from-loaded/csv/tsv/json sources, rename/include/exclude/update/group-by/window transforms, pivot→undeterminable, rest identity. Design note: autocli's `Command.Parse` doesn't descend subcommands, so the ops hand-decode argv (`walkStage`) rather than reusing `parseGroupBySpecs`. Env var also migrated `SSQLGO`→`SSQL_MODE` (deprecated alias kept).
  - [x] **bash *tab* completion across pipes is NOT achievable** via a completion function (v4.46.0/.1 wrongly claimed it; corrected + wrapper removed in v4.46.2). bash scopes `COMP_LINE`/`COMP_WORDS` to the current command — pty-proven. Bash keeps single-command field completion; pipeline-aware completion is serve-only. See [bash-pipeline-completion-options.md](bash-pipeline-completion-options.md).
  - [x] **bash pipeline completion via `bind -x` (Option A)** (ssql v4.47.0, 2026-06-18). `ssql -field-keybinding` emits a `bind -x '"\C-x\C-f"'` binding that reads `READLINE_LINE` (full line), runs the upstream under `SSQL_MODE=schema | generate schema`, and completes the field (unique → complete+space; ambiguous → common-prefix + list). Tab untouched. `cmd/ssql/commands/field_keybinding.go`; pty-verified + `TestFieldKeybinding`.
  - [ ] **Leftovers (minor, non-blocking):** `from parquet`/`from arrow` schema-mode (binary sources need their metadata readers); explicitly short-circuit identity transforms under schema mode (where/sort/limit/… currently pass through via the empty-record exec path — correct but not zero-cost); a `count` `schemaOp`; the §9 **schema-shadow corpus test** (add a 4th `schema` mode to `cmd/ssql/corpus_test.go` that asserts `SSQL_MODE=schema` output equals each pipeline's runtime `_schema` header).
- [ ] **Position 3 (in-process composition)** — composable handlers returning `iter.Seq[Record]` / `Filter[Record,Record]`. Big lift, perf-motivated; defer until needed.
- [ ] **Multi-key bindings beyond Tab** — design space sketched 2026-05-20: `<Shift-Tab>` shows help for the command/flag at the cursor, `<Ctrl-G>` shows generated Go for the pipeline so far, `<Ctrl-X Ctrl-E>` opens the current line in `$EDITOR`. autocli-shell case is trivial — `AutoCompleteCallback` already receives `(line, pos, key rune)` so we branch on key. bash case uses `bind -x` with `READLINE_LINE`/`READLINE_POINT` (the readline cousins of `COMP_LINE`/`COMP_POINT`). Same dispatch pattern as `fzf`'s Ctrl-T binding. **The pattern is now proven** — v4.47.x's `ssql -field-keybinding` (Ctrl-O field completion) is the first `bind -x`/`READLINE_LINE` action binding (`cmd/ssql/commands/field_keybinding.go`), with a real-pty test. Remaining: the rest of the family below.
  - [ ] **Optimise pipeline in-situ** (idea 2026-06-18, "very cute") — a key/chord that replaces the current line with its optimised form: `READLINE_LINE=$( (export SSQL_MODE=record; eval "$READLINE_LINE") | ssql generate ssql )`. `generate ssql` already does the rewriting (merge adjacent `where`, `sort|limit`→`top`, push filters into `from ssh`, column projection) and emits a single-line pipeline; it reads no data so it's near-instant even on huge files. Reversible via readline undo (Ctrl-_/`u`); no-op + leave the line on parse/optimise failure. Caveat: replaces the user's exact text with the optimiser's canonical form (flag/clause ordering normalises) — argues for a *preview* sibling (a second key running `generate ssql -explain` to show the rules it'd apply, without committing the edit). Same emit/install pattern as `-field-keybinding` (a new flag, e.g. `ssql -optimise-keybinding`, sourced in `.bashrc`; single key bound in emacs/vi-insert/vi-command; pty-tested per the CLAUDE.md rule).
  - [ ] Other `READLINE_LINE` action bindings off the same pattern: preview output schema (`SSQL_MODE=schema | generate schema`), show the DuckDB SQL (`generate sql`), pop the generated Go into `$EDITOR`.

## ssql serve (see ssql-serve-proposal.md rev 2)

- [x] **Phase 1 — SSH-CLI operator console** (v4.44.0). `status` / `schema` / `count` / `head [-n N] [-t]` against in-memory dataset. Pubkey auth, multi-user sessions, no per-query startup cost.
- [ ] **Phase 2 — HTTP+WebSocket+browser UI** (the original rev-1 design). `-listen-http :8080` alongside `-listen-ssh :2222`; shared `serveState`; embedded UI with charts, file browser, pipeline editor; REST endpoints per rev-1. ~1 week for core server, ~1 week for UI polish — shorter than rev-1 thought because the dataset-cache and `cli.Complete`/`cli.ExecuteWith` plumbing is already done.

## Adoption (see adoption-plan.md)

- [x] LICENSE file — MIT License
- [x] Terminal recordings for README — VHS demo GIF showing filter, group-by, and generate sql
- [x] goreleaser for GitHub Releases binaries — full + slim builds for linux/darwin/windows × amd64/arm64
- [x] Homebrew — `brew tap rosscartlidge/ssql && brew install ssql`. goreleaser auto-updates on each release.
- [x] WASI build — `ssql.wasm` ships with every release. AOT compilation gives near-native startup.
- [x] Show HN posted, Reddit drafts posted (r/golang)
- [ ] Reddit: r/commandline, r/dataengineering
- [ ] Golang Weekly submission
- [ ] Awesome Go PR
