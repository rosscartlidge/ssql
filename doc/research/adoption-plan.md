# ssql Adoption Plan

**Date:** 2026-03-19
**Status:** Planning (execute after GitHub repos restored)
**Goal:** Get ssql in front of developers who work with data at the command line

## Core pitch

ssql is the only tool that lets you prototype with Unix pipes, optimize automatically, and compile to Go — all from the same pipeline.

```
prototype → optimize → compile
  (CLI)    (generate-ssql)  (generate-go)
```

Lead with this workflow in every piece of content. Features are secondary to the story.

## Pre-launch checklist (before any publicity)

- [ ] GitHub repos restored with full history
- [ ] `go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest` verified working
- [ ] LICENSE file added (confirm with employer: MIT or Apache 2.0)
- [ ] GitHub topics added: `golang`, `cli`, `data-processing`, `csv`, `pipeline`, `sql`, `unix`, `stream-processing`
- [ ] Terminal recordings added to README (2-3 short demos with `asciinema` or `vhs`)
  - Demo 1: Basic pipeline (from csv → where → group-by → to table) — 15 seconds
  - Demo 2: Optimize + compile (generate-ssql → generate-go) — 20 seconds
  - Demo 3: SSH pushdown (from ssh → where → to table, showing the rewrite) — 15 seconds
- [ ] Pin repo on GitHub profile
- [ ] Verify examples in README actually run (copy-paste test)

## Phase 1: Launch (week 1)

### Hacker News — Show HN

**Timing:** Tuesday or Wednesday, 9-11am US Eastern (peak HN traffic).

**Title:** `Show HN: ssql – Unix-style data processing that optimizes and compiles to Go`

**Post body** — keep it short, show don't tell:
- One paragraph: what it is (CLI + Go library for data processing)
- The killer demo: naive pipeline → generate-ssql optimizes it → generate-go compiles it
- Link to GitHub
- "I built this because..." one sentence motivation

**Key angles for HN comments:**
- The pipeline optimizer is a query planner for shell pipes (HN loves this kind of thing)
- Unix philosophy: each command does one thing, pipes compose, but now with automatic optimization
- Go iterators (1.23+) — technically interesting for the Go crowd
- DuckDB SQL generation — bridges two worlds

### Reddit (same week, stagger by 1-2 days)

**r/golang** — Focus on the Go library, iterators, type safety, code generation. Title: "I built a stream processing library that generates optimized Go code from CLI pipelines"

**r/commandline** — Focus on the Unix tool angle. Title: "ssql: like awk/jq for structured data, with an automatic pipeline optimizer"

**r/dataengineering** — Focus on the distributed/SSH/catalog features and DuckDB bridge. Title: "ssql: CLI data processing with SSH pushdown, partition pruning, and DuckDB SQL generation"

### Go newsletters

Submit to:
- **Golang Weekly** (https://golangweekly.com) — submit via their form
- **Awesome Go** (https://github.com/avelino/awesome-go) — PR to add ssql under "Data Processing" or "Command Line"

## Phase 2: Content (weeks 2-4)

### Blog posts

Write 3 posts, each solving a specific problem. Publish on personal blog + cross-post to dev.to and/or Hashnode for reach.

**Post 1: "From CSV to optimized Go binary in 30 seconds"**
- Walk through the full workflow: explore data with CLI → optimize → compile → deploy
- Show real performance numbers (the benchmark data we already have)
- Target: Go developers, data engineers

**Post 2: "Analyzing logs across 3 servers in one command"**
- SSH pushdown + catalog + partition pruning
- Show naive vs optimized (3 SSH connections → 1 with pushdown)
- Target: DevOps, SRE

**Post 3: "I built a query optimizer for shell pipelines"**
- Deep dive on the optimizer: how the rules work, what was rejected (column dropping was slower!)
- The benchmark results and key insight about Unix pipe overhead
- Target: HN/technical audience, database nerds

### Comparison page

Add `doc/comparison.md` to the repo. Honest comparison with:
- **csvkit** — ssql is faster (compiled), has code generation, but csvkit has more CSV-specific tools
- **miller (mlr)** — similar Unix philosophy, mlr has its own DSL, ssql generates Go code
- **xsv** — xsv is faster for pure CSV ops, ssql handles JSON/Parquet/Arrow/XLSX and has aggregations
- **jq** — jq is better for JSON transformation, ssql is better for tabular data and aggregations
- **DuckDB CLI** — DuckDB is faster for SQL queries, ssql is better for streaming pipelines and SSH pushdown; ssql can generate DuckDB SQL via `generate-sql`
- **awk** — awk is universal, ssql is higher-level with named fields and type safety

Don't trash the alternatives. Show where each tool is the better choice.

## Phase 3: Distribution (weeks 3-6)

### Homebrew

Create a Homebrew formula so macOS/Linux users can `brew install ssql` without needing Go:

```ruby
class Ssql < Formula
  desc "SQL-style stream processing for the command line and Go"
  homepage "https://github.com/rosscartlidge/ssql"
  url "https://github.com/rosscartlidge/ssql/archive/refs/tags/v4.28.0.tar.gz"
  license "MIT"  # or Apache-2.0
  depends_on "go" => :build

  def install
    system "go", "build", "-o", bin/"ssql", "./cmd/ssql"
  end
end
```

Submit to homebrew-core, or start with a tap: `brew tap rosscartlidge/tap && brew install ssql`.

### Other package managers

- **Arch AUR** — popular with command-line power users
- **Nix** — growing developer audience, easy to add
- **APT repo** — for the .deb packages already being built
- **Docker image** — `docker run rosscartlidge/ssql from data.csv | ...`
- **GitHub Releases** — attach pre-built binaries (linux/amd64, darwin/arm64, darwin/amd64) to each release. Use `goreleaser` to automate this.

### WASM playground

You already have a WASM build (`make wasm`). Create a simple static site where people can type ssql pipelines and see results in the browser. Host on GitHub Pages. This is the single biggest friction-reducer — people can try it without installing anything.

## Phase 4: Community (ongoing)

### Conference talks

- **GopherCon** (or GopherCon EU) — submit a talk proposal: "Unix Pipes Meet Query Optimization: Building a Pipeline Compiler in Go"
- **Local Go meetups** — shorter version, great for practice
- **Data engineering meetups** — focus on the distributed processing angle

### GitHub engagement

- Respond to every issue and PR quickly (first impressions matter)
- Add "good first issue" labels to easy tasks
- Write a CONTRIBUTING.md
- Add GitHub Discussions for Q&A

### DuckDB community

- Post in DuckDB Discord showing the `generate-sql` bridge
- Write a DuckDB community blog post: "Using ssql as a pipeline front-end for DuckDB"
- The DuckDB community is active and welcoming of integrations

## What to measure

- GitHub stars (vanity metric but indicates awareness)
- `go install` downloads (check via Go module proxy stats)
- GitHub issues/discussions (indicates actual usage)
- Homebrew install counts (if accepted)

## What NOT to do

- Don't spam — one thoughtful post per community, not cross-posting identical content
- Don't compare to pandas/Spark/Flink — different niche, invites unfavorable comparisons
- Don't lead with feature lists — lead with problems solved and the workflow demo
- Don't post until the README has terminal recordings — a wall of text README loses people in 5 seconds
- Don't neglect the first issue/PR from a stranger — that's your first potential contributor

## Timeline

| Week | Action |
|---|---|
| 0 | Restore GitHub repos, pre-launch checklist |
| 1 | Show HN, Reddit posts (r/golang, r/commandline, r/dataengineering) |
| 1 | Submit to Golang Weekly, PR to Awesome Go |
| 2 | Blog post 1 (CSV to Go binary) |
| 3 | Blog post 2 (logs across servers) |
| 3 | Homebrew formula, goreleaser for GitHub Releases |
| 4 | Blog post 3 (query optimizer deep dive) |
| 4 | Comparison page in repo |
| 5 | WASM playground on GitHub Pages |
| 6 | DuckDB community post |
| 6+ | Conference talk proposals, ongoing community engagement |
