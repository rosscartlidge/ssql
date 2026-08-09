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
- [x] **`top` translation** (v4.55.0) — `translateTop` had gone stale: it looked for the long-removed `-by` flag (so it emitted **no `ORDER BY`**) and treated `args[0]` as N (so `-asc` became `LIMIT -asc`). Now emits `ORDER BY FIELD DESC|ASC LIMIT N` (N = first bare positional; field from `-field`/`-f`; `-asc` → ASC). Covered by `TestTranslateTopSQL`.

### Remaining quirks (found during the v4.55.0 `top` differential work)

- [x] **All WHERE values are quoted as string literals** (v4.56.0) — `translateCondition`/`translateUpdate` now render value tokens via `sqlLiteral`: numeric and boolean tokens stay bare (`n > 15`, `active = TRUE`), strings keep single quotes, Inf/NaN stay quoted. Guarded by `TestSQLLiteral`/`TestTranslateConditionLiterals` (DuckDB itself coerces by column type, so the unit tests are the semantic guard; the DuckDB lane guards end-to-end).
- [x] **`-if-expr` verbatim passthrough** (v4.56.0) — replaced with a real expr-lang→SQL translator (`generate_sql_expr.go`, `exprToSQL`): parses with the expr-lang parser and renders the SQL-safe subset (`&&`/`and`→AND, `||`/`or`→OR — SQL `||` is concat!, `==`→`=`, `"str"`→`'str'` — SQL double quotes are identifiers!, `??`→COALESCE, `?:`→CASE, `in [..]`→IN, contains/startsWith/endsWith/matches operators, upper/lower/trim/abs/round/floor/ceil/len/hasPrefix/hasSuffix/min/max/int/float/string functions, has()→IS NOT NULL, getOr()→COALESCE). Everything else **fails loudly** naming the construct. Also fixed silent drops found on the way: `update -set-expr`/`-if-expr` were ignored entirely (SQL returned unmodified rows), `+if`/`+if-expr` negation was ignored (SQL returned extra rows), and `group-by -expr/-stream-expr/-rollup/-cube` were silently dropped (now loud errors; ROLLUP/CUBE have direct SQL forms if wanted later — but note exec's subtotal-row representation must be matched, not just the clause).
- [x] **No general differential coverage for the SQL lane** (v4.56.0) — `TestPipelineEquivalence` now has a **duckdb lane**: `generate sql` output executed by `duckdb -json` (binary discovered on PATH or `~/.local/bin`, lane omitted when absent), rows re-emitted as JSONL with one normalisation (DuckDB renders HUGEINT — e.g. SUM over BIGINT — as a JSON string; canonical integer strings are converted back to numbers, mirroring ssql's own CSV inference). New discriminating cases `where_expr` (verbatim passthrough was a DuckDB parse error) and `update_set_expr` (the silent drop — caught as a multiset diff). Watch-it-fail done by temporarily reintroducing both bugs.
- [ ] **`update -set-expr` is rejected by typed codegen** (loud Tier-3 error: "not supported in typed mode") even though TODO Phase B claims expr commands fall through to record-mode codegen — the fallthrough only applies when prevSchema is nil. The equivalence case `update_set_expr` skips go-typed/go-parallel for this reason. Either wire the toRecord adapter for update-with-set-expr or update the Phase B claim.
- [ ] **`contains(str, sub)` documented as a function but it's an operator** — FIXED in docs (v4.56.0: functions.go reference, where.go example, EXPRESSIONS.md), noted here because other docs may still show the call form; `grep -rn 'contains(' doc/` when touching expression docs.
- [x] **Single-SELECT flattening ignored pipeline stage order** (v4.56.0, user-reported) — `update -set x 3 | limit 10 | group-by r | join …` folded into ONE SELECT whose fixed clause order (WHERE→GROUP BY→ORDER BY→LIMIT) silently computed a different pipeline (grouped everything, limited the *groups*), plus an invalid `CASE ELSE '3' END` (no WHEN) for the unconditional set. Fixed: `needsWrap` slot-conflict detection + `wrapAsSubquery` — a stage arriving out of SQL clause order (limit-before-group, second projection, join-after-group, where-after-group, sort-after-limit, offset-after-limit, re-sort, …) wraps the accumulated query as a `FROM (subquery)`. In-order pipelines still render flat. Also fixed on the way: unconditional `-set` emits the plain value; `distinct` renders `SELECT DISTINCT` (was a fake select column → `SELECT DISTINCT, col` / bare `SELECT DISTINCT`, both invalid); a later `sort` prepends its keys (stable re-sort: new primary, old tie-break). Guards: `TestSubqueryWrapping`, equivalence cases `limit_then_group` / `update_unconditional_then_group` / `include_distinct` (watched failing with wrapping disabled).
- [x] **`update -set` on a NEW column breaks `generate sql`** (v4.56.0) — the assembler now tracks columns (`sqlQuery.columns`): seeded from the source CSV/TSV header at generation time (`delimHeader`), advanced per stage via the same `schemaOps` pipeline-aware completion uses (`advanceColumns`; join/union → unknown). `update -set` on a field not in the schema emits `SELECT *, 3 AS x` instead of `* REPLACE`; conditional `-set` on a new field fails loudly (exec leaves the field absent on unmatched rows — SQL can't). Unknown schema falls back to assuming columns exist. Guards: `TestUpdateNewColumnSQL`, equivalence `update_new_column`.
- [x] **`union` SQL translation** (v4.56.0) — `translateUnion`: the accumulated query `UNION [ALL]` each `<(…)>` source subquery, wrapped as the new FROM. Bare UNION dedups = `union` without `-all`. Regular-file (schema-headed JSONL) sources fail loudly with a pointer to `<(ssql from csv FILE)`. Guards: `TestTranslateUnionSQL`, equivalence `union_dedup` (duckdb lane now included).
- [x] **Permutation coverage for the equivalence corpus** (v4.56.0) — `TestPipelinePermutations` enumerates every ordered pair of {where, sort, limit, group-by, distinct} (19 pipelines; group→limit skipped as genuinely nondeterministic — group emission order is unspecified, so lanes legitimately disagree on "first 5 groups"). Each runs through all lanes with a `sort id` prefix for deterministic first-N. **It caught a real bug on its first run**: any pipeline with two `sort` stages failed typed codegen with `no new variables on left side of :=` — the v4.50.1 variable-collision class, spot-fixed then only for include/rename/exclude/group-by. Fixed the CLASS: all 22 codegen sites with hardcoded output vars (`sorted`, `filtered`, `limited`, `joined`, …) now go through `uniqueVarName`.
- [ ] **Permutation gate extensions** — 3-stage permutations (ordered triples would cover wrap-of-wrap interactions), and adding `update`/`rename`/`top` to the stage set (needs care: update on a shared field creates ties that make downstream sort/limit nondeterministic).
- [ ] **`merge -catalog` / `from catalog` own `-if` pruning flags have no negated form** (found during the +if negation sweep, ported 2026-08-09): `parseMergeCatalogCmd`/`parseFromCatalogCmd` only decode `-if`, and `renderCmd` rebuilds filters as `-if` — a user-written `+if` pruning filter would be dropped on optimiser re-render. The optimiser now refuses to LIFT a negated where condition into catalog filters (`ruleCatalogPredicateExtraction` keeps them data-side), but the pruning flags themselves are still un-negatable. Also unverified whether the exec pruning path honours `+if` at all. Distributed feature — needs an SSH test rig to fix honestly.
- [ ] **Record codegen mis-references flags for duplicate field+op conditions** (found 2026-08-09 by the `where_negated_survives_simplify` equivalence case draft): `where -if pop gt 5 -if pop gt 8` declares `flagPopGt`/`flagPopGt2` but emits BOTH references as `flagPopGt2` — the first value is silently replaced by the second. Invisible for ANDed same-direction bounds (`pop>8 && pop>8` ≡ `pop>5 && pop>8`), but WRONG for `+if` mixes (`-if pop gt 5 +if pop gt 8` → `pop>8 && !(pop>8)` = empty) and multi-clause ORs; with THREE same-field+op conditions the emitted code references an undeclared `flagPopGt32` and doesn't compile. Reference generation is keyed by field+op with last-wins; needs per-condition keying. Check typed codegen for the same class. Add an equivalence case with `+if` on a duplicate field+op once fixed.

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
- [x] **Heap top-k in typed mode** (v4.53.0). `typed.TopBy`/`BottomBy` (bounded heap, O(N·log K)/O(K)) + parallel `typed.TopByParallel`/`BottomByParallel` (per-shard heap, merge survivors). `top` codegen now emits these via dual templates instead of desugaring to `SortByDesc`+`Limit` (was `SerialOnly` and O(N·log N)/O(N)); `from | top` stays parallel. Found because `generate go -mode typed` on `top` emitted a full sort. Also: `generate go -mode` gained a value completer (record/typed).
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
  - [x] **Optimise pipeline in-situ** (ssql v4.48.0, 2026-06-20) — `ssql -optimise-keybinding` emits a `bind -x` binding (Ctrl-T) that replaces the line with its optimised form: `READLINE_LINE=$( (export SSQL_MODE=record; eval "$READLINE_LINE") | ssql generate ssql )`. Reads no data (codegen fragment stream) so it's instant even on huge files; reversible via readline undo; guard skips non-ssql lines. `cmd/ssql/commands/optimise_keybinding.go`; single key in emacs/vi-insert/vi-command; `TestOptimiseKeybindingPTY` (real pty, both modes, low keyseq-timeout). *Preview sibling* (`generate ssql -explain` on a second key, show rules without committing) still open — a nice follow-up.
  - [x] **Help at the cursor** (Unreleased, 2026-06-22) — `ssql -help-keybinding` emits a `bind -x` binding (**Alt-h**) showing contextual help for the flag/command under the cursor (description + arg signature, current arg marked). Generalised into autocli as `Command.HelpAt(args, pos)` (reuses `analyzeCompletionContext`) + the `-help-at` protocol flag (parallel to `-complete`); ssql's binding just calls `ssql -help-at <pos> <words…>`. Display: `tmux display-popup` when `$TMUX`, inline otherwise. `cmd/ssql/commands/help_keybinding.go`, `autocli/help_at.go`; single key in emacs/vi-insert/vi-command; `TestHelpKeybindingPTY` (real pty, both modes — Alt-h's ESC-prefix survives vi). **Requires bumping the autocli `require` to a release carrying `HelpAt` before shipping** (currently developed via a local `replace`). Deferred: autocli-shell `AutoCompleteCallback` help-key branch, live `tmux split-window` help pane, structured `HelpResult` for the REPL.
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

## Release Infrastructure

- [x] **Regenerate the `GH_PAT` secret** (found 2026-07-03, v4.56.0 release): the goreleaser workflow's Homebrew-tap push failed with `401 Bad credentials` against `rosscartlidge/homebrew-ssql` — the PAT worked for v4.55.0 on 2026-07-01, so it expired/was revoked in between. The GitHub Release itself succeeded (14 assets); only the tap push uses `GH_PAT` (`.goreleaser.yml` line ~89). v4.56.0's cask was updated manually (homebrew-ssql commit 228e46a). Regenerated + secret updated 2026-07-03; will be exercised (and proven) by the next tagged release's automatic tap push.
