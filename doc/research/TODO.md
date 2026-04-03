# TODO

Tracked issues and feature gaps discovered during development.

## Multi-file `from` (see multi-file-from.md)

- [x] **Phase 1: Core multi-file support** — `ssql from csv *.csv` with `-merge-schemas` and `-source`. Supports csv, tsv, json, jsonl.
- [x] **Phase 2: Pushdown** — `ssql from csv *.csv -- where -if age gt 25`. Spawns sub-pipeline per file, merges JSONL output. Supports multi-stage via `+` separator. Works for csv, tsv, json, jsonl.
- [x] **Phase 3: Parallel reading** — pushdown subprocesses run concurrently (capped at NumCPU), output preserves file order. 4x faster on 10×100k row files.

## Build System

- [x] **Split from.go into per-format files** — `from.go` (2156 lines) → 11 files: `from_csv.go`, `from_tsv.go`, `from_json.go`, `from_arrow.go`, `from_parquet.go`, `from_wav.go`, `from_xlsx.go`, `from_ssh.go`, `from_catalog.go`, `from_command.go`, plus shared helpers in `from.go` (435 lines).
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

- [ ] **Phase 1: Core `-catalog` flag** — `ssql merge -catalog shards.csv -by timestamp`. Opens SSH to each catalog entry, streams JSONL into K-way merge heap. Supports pushdown via `--` and partition pruning via `-if`. Streaming, O(K) memory.
- [ ] **Phase 2: Shard metadata enrichment** — `-shard source` adds host:path to each record.
- [ ] **Phase 3: GPU support** — `-gpu` flag to use `ssql_gpu` on remote nodes.

## Pipeline Optimizer (`generate ssql`)

- [x] **Join predicate pushdown for process substitutions** — `<(ssql from file.csv)` joins now support predicate pushdown.
- [ ] **SSH pushdown with expressions** — currently bails out on `-if-expr` for SSH pushdown. Could push simple expressions that don't reference functions unavailable on remote.
- [ ] **Catalog column analysis** — currently reads catalog CSV at optimization time; fails silently if file not accessible. Could cache column info in fragments.

## Adoption (see adoption-plan.md)

- [x] LICENSE file — MIT License
- [x] Terminal recordings for README — VHS demo GIF showing filter, group-by, and generate sql
- [x] goreleaser for GitHub Releases binaries — full + slim builds for linux/darwin/windows × amd64/arm64
- [ ] Homebrew — tap repo created (`rosscartlidge/homebrew-ssql`), goreleaser configured. Needs GH_PAT secret and first tagged release to populate.
