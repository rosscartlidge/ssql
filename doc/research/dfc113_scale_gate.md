# The Scale Gate: Budget Assertions on Large Fixtures

Reference: DFC113
Created: 2026-08-24
Last modified: 2026-08-24

[Back to Index](./README.md)

**Status:** Design — approved for later implementation (Ross,
2026-08-24: "write up the scale gate as DFC and a TODO - for doing
later"). Companion to
[multimode-equivalence-testing.md](./multimode-equivalence-testing.md):
that doc's doctrine covers the *what comes out* axis; this one covers
*how much work it took*.

## The observation (Ross, 2026-08-24)

"It shows you we need to test some things with large files to find
implementation bugs." One week of live testing on a real 1.2GB CSV
surfaced **five** bugs that every fixture-scale test passed over:

| Bug | Why fixtures missed it |
|---|---|
| `sample` reads its whole input (21.3s) — correct but unusable as a workspace default | full-read of a 4-row fixture is instant; the DESIGN fact only shows at scale |
| Byte-offset sampler read the 4MB line-cap per line (~4GB traffic, 1.1s) | small files fit inside one cap read; cost invisible |
| Serve stage-chain deadlock on early-exiting consumers (`from big \| limit 10` hung forever) | only bites when a stage's output exceeds the 64KB pipe buffer — fixtures fit inside it |
| Parquet schema mode decoded the ENTIRE file to list columns | instant on a 536-row file; seconds on 14.6M rows — and hit per completion probe |
| `from ssh` codegen used the per-line `json.Unmarshal` reader (4× slower than the CLI) | correctness identical; only a 3M-row benchmark showed the gap |

Common shape: **correctness oracles pass while complexity or resource
behavior is wrong.** It is the `top`-saga weak-oracle lesson
transposed to the performance axis — every existing gate asserts
output bytes; none assert work done.

## Design

### An opt-in gate, triples-style

`SSQL_SCALE=1 go test ./cmd/ssql -run TestScaleBudgets -timeout=20m`
— opt-in like `SSQL_PERM_TRIPLES`, on the release checklist's pre-tag
list, not part of normal runs.

### One cached large fixture

A deterministic generator writes ~200MB of CSV (~3M rows, several
column types, distinct values) plus derived parquet/tsv/jsonl
variants into `os.TempDir()/ssql-scale-<generatorVersion>/`, created
on first run and reused after (keyed by a version constant bumped when
the generator changes — the docsearch-cache pattern). 200MB is chosen
to clear every known trap threshold (pipe buffer ×3000, line-cap ×50,
schema-decode pain) while keeping first-run generation ~10s and total
gate time in low minutes.

### Budget assertions, not benchmarks

Each case runs an operation and asserts a WALL-TIME CEILING with 5–10×
headroom over measured reality — loose enough to never flicker on a
loaded machine or slower hardware, tight enough that a
complexity-class or read-amplification regression (the only kind we
care to catch) blows through it. No lower bounds, no comparisons
between runs, no stored baselines — those rot; a generous absolute
ceiling per known trap does not.

Initial case table (each pinned to a bug above):

| Case | Operation | Budget (≈10× headroom) |
|---|---|---|
| `schema-mode-parquet` | `SSQL_MODE=schema from big.parquet` | < 1s (footer read; full decode ≈ 10s+) |
| `schema-mode-csv` | `SSQL_MODE=schema from big.csv` | < 1s (header only) |
| `sample-source` | `from csv big.csv -sample 1000` | < 1s (was 21s full-read / 1.1s cap-read) |
| `serve-early-exit` | `/api/execute` of `from big.csv \| limit 10` | < 2s (deadlock = timeout; replaces the size-tuned 2MB fixture in TestServeExecuteEarlyExit) |
| `serve-line-count` | second head run over big.csv (count cached) | runMs delta < 0.5s vs first |
| `exec-csv-scan` | `from big.csv \| count` | < 10× `wc -l` wall on the same file (parse-path amplification guard) |
| `jsonl-roundtrip` | `tee big.jsonl` then `from big.jsonl \| count` | < 2× the csv scan (the legacy-reader 4× class) |

New scale-sensitive features add a case here the way result-changing
commands add an equivalence case.

### Watch it fail

Per doctrine, each budget is sabotage-verified once at introduction —
re-break the original bug (or simulate: insert the full-read, drop
the parent pipe-close) and confirm the case trips.

## What this is NOT

- Not a benchmark suite: no numbers are recorded or compared across
  runs; only ceilings.
- Not a replacement for benchmarking features on real data before
  shipping (the 1.2GB-file sessions stay — the gate catches
  *regressions* of known traps, live testing finds *new* ones).
- Not micro: `go test -bench` benchmarks keep their role for
  hot-path tuning.

## CLAUDE.md addition (when implemented)

Under the testing doctrine: **"A fixture that fits in a pipe buffer
tests nothing about scale.** Perf-sensitive paths (readers, samplers,
schema mode, serve chains) need a case in the opt-in scale gate
(`SSQL_SCALE=1`, see DFC113) asserting a wall-time ceiling — output
oracles pass while complexity bugs ship."
