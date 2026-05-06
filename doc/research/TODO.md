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
- [ ] **Remote Go execution Phase B — codegen-symmetric pushdown** (v4.42 target). See `remote-go-execution-proposal.md` rev 4. Whatever mode the local pipeline runs in (CLI baseline / SSQLGO=record / SSQLGO=typed), the remote runs in too. Generated Go embeds the `.ssql` script as a const string and inlines a small ssh-and-cat-and-run helper. Single self-contained binary, no extra deployment artefacts. Drops the v4.41 transitional `-remote` flag. ~1 day.
- [ ] **Remote Go execution Phase C — catalog extension** (v4.43+). `from catalog` per-shard ship-and-run via the codegen-symmetric path; local merges results. Composes naturally with Phase B.
- [ ] **Codegen wrapper** — see `codegen-wrapper-proposal.md`. Lower the bar from `(export SSQLGO=record; ...) | ssql generate go` to friendlier shapes: `ssqlgen 'pipeline'` (a bash function shipped via `ssql -shell-helpers`, same install pattern as completion script) plus `ssql generate go -script PATH` (reads the pipeline from a file or `<(heredoc)`). Both build on the existing SSQLGO + fragment-pipe machinery; power-user form keeps working. Estimate: ~half a day total.

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
