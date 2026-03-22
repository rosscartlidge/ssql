# TODO

Tracked issues and feature gaps discovered during development.

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
- [x] **GitHub Pages deployment** — live at `https://rosscartlidge.github.io/ssql/playground.html`. Currently manual (push to `gh-pages` branch).
- [ ] **GitHub Actions automation** — build WASM and deploy to `gh-pages` automatically on push to `main`. Removes the 68MB binary from git history and keeps playground always up-to-date.
- [ ] **Share links** — encode pipeline in URL fragment for sharing.
- [ ] **Syntax highlighting** — generated Go/SQL code should be highlighted (highlight.js or Prism).
- [x] **Chart rendering** — `to chart` output rendered inline via iframe.
- [ ] **Loading indicator** — show progress while 12MB WASM loads.
- [ ] **Mobile layout** — currently untested on mobile.

## Tab Completion

- [ ] **`ssql from <tab>` should also complete filenames** — currently only shows subcommands (csv, ssh, parquet, etc.). Most users type `ssql from data.csv` directly without the subcommand, so file completion at this position would be more useful.
- [ ] **WebVM: AUTOCLI field cache not set** — tab completion works for filenames but `AUTOCLI_CACHE_FILE`/`AUTOCLI_FIELDS` env vars don't propagate back to the parent shell in CheerpX's bash. This means field name completion after `-if` doesn't work in the WebVM terminal. Likely a CheerpX limitation with how bash completion functions export env vars. Works fine on real bash.

## Pipeline Optimizer (`generate ssql`)

- [ ] **SSH pushdown with expressions** — currently bails out on `-if-expr` for SSH pushdown. Could push simple expressions that don't reference functions unavailable on remote.
- [ ] **Catalog column analysis** — currently reads catalog CSV at optimization time; fails silently if file not accessible. Could cache column info in fragments.

## Adoption (see adoption-plan.md)

- [ ] LICENSE file (confirm with employer: MIT or Apache 2.0)
- [ ] Terminal recordings for README (asciinema/vhs)
- [ ] Homebrew formula
- [ ] goreleaser for GitHub Releases binaries
