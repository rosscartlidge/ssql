# TODO

Tracked issues and feature gaps discovered during development.

## Multi-file `from` (see multi-file-from.md)

- [x] **Phase 1: Core multi-file support** — `ssql from csv *.csv` with `-merge-schemas` and `-source`. Supports csv, tsv, json, jsonl.
- [ ] **Phase 2: Pushdown** — `ssql from csv *.csv -- where -if age gt 25`. Spawn sub-pipeline per file, merge JSONL output. Pattern proven in `from ssh`/`from catalog`.
- [ ] **Phase 3: Parallel reading** — goroutine-per-file reader, capped at NumCPU. Preserves file order by default.

## Build System

- [ ] **Split from.go into per-format files** — `from_csv.go`, `from_arrow.go`, `from_parquet.go`, `from_xlsx.go`, `from_wav.go` etc. with build tags. Enables slim builds that exclude heavy dependencies (Apache Arrow, excelize). Same for `to.go`. Current WASM playground binary is 68MB (12MB gzipped) because it pulls in everything.
- [ ] **Split to.go similarly** — `to_arrow.go`, `to_parquet.go`, `to_xlsx.go`, `to_wav.go` with matching build tags.
- [ ] **Slim build tag** — `go build -tags slim` excludes arrow, parquet, xlsx, wav. Useful for WASM playground, embedded systems, or users who only need CSV/JSON.

## SQL Generation (`generate sql`)

- [ ] **Window functions** — `generate sql` returns "unsupported command" for `window`. DuckDB supports full window function syntax (`ROW_NUMBER() OVER (PARTITION BY ... ORDER BY ...)`), so translation is feasible.
- [ ] **rename / cast / update** — not yet translated to SQL equivalents (`AS`, `CAST()`, `CASE WHEN`).
- [ ] **include / exclude** — translate to explicit column list or `SELECT * EXCLUDE(...)`.
- [ ] **from ssh / from catalog** — currently treats "ssh" as a filename. Should either error clearly or skip (these are ssql-specific distributed features with no SQL equivalent).

## WASM Playground

- [ ] **Binary size** — 68MB raw / 12MB gzipped. Blocked on from.go/to.go split (see Build System above). Target: ~15MB raw / ~3MB gzipped with slim build.
- [x] **GitHub Pages deployment** — live at `https://rosscartlidge.github.io/ssql/playground.html`. Manual push to `gh-pages` branch.
- [x] **Chart rendering** — `to chart` output rendered inline via iframe.
- [x] **Progressive examples** — 12 examples from simple to complex, with static data files.
- [x] **Data viewer** — click dataset names to view raw CSV.
- [x] **# comments** — comment out pipeline stages to build up incrementally.
- [ ] **GitHub Actions automation** — build WASM and deploy to `gh-pages` automatically on push to `main`.
- [ ] **Share links** — encode pipeline in URL fragment for sharing.
- [ ] **Syntax highlighting** — generated Go/SQL code should be highlighted (highlight.js or Prism).
- [ ] **Loading indicator** — show progress while 12MB WASM loads.

## WebVM Terminal

- [x] **Field completion** — was missing `jq` in Docker image. Fixed.
- [x] **SSH support** — openssh-client, ping, .ssh/config with Tailscale defaults, auto-generated keypair.
- [x] **Build hash in welcome** — shows ssql version with commit hash on boot.
- [ ] **Mobile touch bar** — Tab/arrows/Ctrl keys for phones. Reverted — broke CheerpX boot on Pixel 7. Needs approach that doesn't touch Svelte component (pure DOM injection after boot).

## Tab Completion

- [ ] **`ssql from <tab>` should also complete filenames** — currently only shows subcommands (csv, ssh, parquet, etc.). Most users type `ssql from data.csv` directly without the subcommand, so file completion at this position would be more useful.

## Pipeline Optimizer (`generate ssql`)

- [x] **Join predicate pushdown for process substitutions** — `<(ssql from file.csv)` joins now support predicate pushdown.
- [ ] **SSH pushdown with expressions** — currently bails out on `-if-expr` for SSH pushdown. Could push simple expressions that don't reference functions unavailable on remote.
- [ ] **Catalog column analysis** — currently reads catalog CSV at optimization time; fails silently if file not accessible. Could cache column info in fragments.

## Adoption (see adoption-plan.md)

- [ ] LICENSE file (confirm with employer: MIT or Apache 2.0)
- [ ] Terminal recordings for README (asciinema/vhs)
- [ ] Homebrew formula
- [ ] goreleaser for GitHub Releases binaries
