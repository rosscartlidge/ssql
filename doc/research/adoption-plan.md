# ssql Adoption Plan

Reference: DFC058
Created: 2026-03-20
Last modified: 2026-04-08

[Back to Index](./README.md)

**Date:** 2026-03-19 (updated 2026-04-08)
**Status:** Launched. HN posted, Reddit and newsletters next.
**Goal:** Get ssql in front of developers who work with data at the command line

## Core pitch

ssql is the only tool that lets you prototype with Unix pipes, optimize automatically, and compile to Go — all from the same pipeline.

```
prototype → optimize → compile
  (CLI)    (generate ssql)  (generate go)
```

Lead with this workflow in every piece of content. Features are secondary to the story.

## Pre-launch checklist

- [x] GitHub repos restored with full history
- [x] `go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest` verified working
- [x] MIT License added
- [ ] GitHub topics added: `golang`, `cli`, `data-processing`, `csv`, `pipeline`, `sql`, `unix`, `stream-processing`
- [x] Terminal demo GIF in README (VHS, filter → group-by → generate sql)
- [ ] Two more demo GIFs: optimize+compile workflow, SSH pushdown
- [ ] Pin repo on GitHub profile
- [ ] Verify examples in README actually run (copy-paste test)
- [x] Homebrew formula (`brew tap rosscartlidge/ssql && brew install ssql`)
- [x] goreleaser — cross-platform binaries on GitHub Releases
- [x] WASM playground live (https://rosscartlidge.github.io/ssql/playground.html)
- [x] WebVM terminal live (https://rosscartlidge.github.io/ssql-terminal/)

## Phase 1: Launch (week 1)

### Hacker News — Show HN

**Timing:** Tuesday or Wednesday, 9-11am US Eastern (peak HN traffic).

**Title:** `Show HN: ssql – Unix-style data processing that optimizes and compiles to Go`

**Post body** — keep it short, show don't tell:
- One paragraph: what it is (CLI + Go library for data processing)
- The killer demo: naive pipeline → generate ssql optimizes it → generate go compiles it
- Link to playground (people can try immediately)
- Link to GitHub
- "I built this because..." one sentence motivation

**Key angles for HN comments:**
- The pipeline optimizer is a query planner for shell pipes
- Unix philosophy: each command does one thing, pipes compose, but now with automatic optimization
- Go iterators (1.23+) — technically interesting for the Go crowd
- DuckDB SQL generation — bridges two worlds
- Browser playground — try it without installing
- 4x parallel multi-file pushdown — real performance story

### Reddit (same week, stagger by 1-2 days)

**r/golang** — Focus on the Go library, iterators, type safety, code generation.
Title: "I built a stream processing library that generates optimized Go code from CLI pipelines"

**r/commandline** — Focus on the Unix tool angle.
Title: "ssql: like awk/jq for structured data, with an automatic pipeline optimizer"

**r/dataengineering** — Focus on the distributed/SSH/catalog features and DuckDB bridge.
Title: "ssql: CLI data processing with SSH pushdown, partition pruning, and DuckDB SQL generation"

### Go newsletters

Submit to:
- **Golang Weekly** (https://golangweekly.com) — submit via their form
- **Awesome Go** (https://github.com/avelino/awesome-go) — PR to add ssql under "Data Processing" or "Command Line"

## Phase 2: Content (weeks 2-4)

### Blog posts

Write 3 posts, each solving a specific problem. Publish on personal blog + cross-post to dev.to and/or Hashnode for reach.

**Post 1: "From CSV to optimized Go binary in 30 seconds"**
- Walk through the full workflow: explore data with CLI → optimize → compile → deploy
- Embed the demo GIF
- Show real performance numbers
- Link to playground so readers can try it
- Target: Go developers, data engineers

**Post 2: "Analyzing logs across 3 servers in one command"**
- SSH pushdown + catalog + partition pruning
- Show naive vs optimized (3 SSH connections → 1 with pushdown)
- Target: DevOps, SRE

**Post 3: "I built a query optimizer for shell pipelines"**
- Deep dive on the optimizer: how the rules work, what gets rewritten
- Multi-file parallel pushdown benchmarks (4x faster)
- Target: HN/technical audience, database nerds

### Comparison page

Add `doc/comparison.md` to the repo. Honest comparison with:
- **csvkit** — ssql is faster (compiled), has code generation, but csvkit has more CSV-specific tools
- **miller (mlr)** — similar Unix philosophy, mlr has its own DSL, ssql generates Go code
- **xsv** — xsv is faster for pure CSV ops, ssql handles JSON/Parquet/Arrow/XLSX and has aggregations
- **jq** — jq is better for JSON transformation, ssql is better for tabular data and aggregations
- **DuckDB CLI** — DuckDB is faster for SQL queries, ssql is better for streaming pipelines and SSH pushdown; ssql can generate DuckDB SQL
- **awk** — awk is universal, ssql is higher-level with named fields and type safety

Don't trash the alternatives. Show where each tool is the better choice.

## Phase 3: Distribution (weeks 3-6)

Already done:
- [x] Homebrew tap
- [x] goreleaser (GitHub Releases with 12 cross-platform binaries)
- [x] Debian packages (standard + GPU)
- [x] WASM playground
- [x] WebVM terminal
- [x] `go install` path

Still useful:
- [ ] **Awesome Go PR** — high visibility, curated list
- [ ] **Arch AUR** — popular with command-line power users
- [ ] **Docker image** — `docker run rosscartlidge/ssql from data.csv | ...`

## Phase 4: Community (ongoing)

### Conference talks

- **GopherCon** (or GopherCon EU) — "Unix Pipes Meet Query Optimization: Building a Pipeline Compiler in Go"
- **Local Go meetups** — shorter version, great for practice
- **Data engineering meetups** — focus on the distributed processing angle

### GitHub engagement

- Respond to every issue and PR quickly
- Add "good first issue" labels
- Write a CONTRIBUTING.md
- Add GitHub Discussions for Q&A

### DuckDB community

- Post in DuckDB Discord showing the `generate sql` bridge
- The DuckDB community is active and welcoming of integrations

## What to measure

- GitHub stars (vanity metric but indicates awareness)
- `go install` downloads (Go module proxy stats)
- GitHub issues/discussions (indicates actual usage)
- Homebrew install counts

## What NOT to do

- Don't spam — one thoughtful post per community
- Don't compare to pandas/Spark/Flink — different niche
- Don't lead with feature lists — lead with problems solved and the workflow demo
- Don't neglect the first issue/PR from a stranger — that's your first potential contributor

## Progress

- [x] Add GitHub topics to the repo
- [x] Pin repo on GitHub profile
- [x] Copy-paste test all README examples
- [x] Demo GIFs (basic + optimize+compile) + chart screenshot
- [x] Show HN post — posted 2026-04-08
- [ ] Reddit posts — r/golang, r/commandline, r/dataengineering (drafts ready)
- [ ] Submit to Golang Weekly (draft ready)
- [ ] PR to Awesome Go (draft ready)
- [ ] Blog post 1: "From CSV to optimized Go binary in 30 seconds"
- [ ] Blog post 2: "Analyzing logs across 3 servers in one command"
- [ ] Blog post 3: "I built a query optimizer for shell pipelines"
- [ ] Comparison page (`doc/comparison.md`)
- [ ] DuckDB community post
