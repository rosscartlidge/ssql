# Sampling a 1.2GB CSV in 14 Milliseconds: A Case Study

Reference: DFC111
Created: 2026-08-23
Last modified: 2026-08-23

[Back to Index](./README.md)

**Status:** Retrospective / shareable write-up of the design
conversation behind `ssql sample` and `from -sample`
([DFC110](./dfc110_sample_command.md)). Written to stand alone for
readers outside the project.

## The problem

You have a 1.2GB CSV (14.6 million rows) and you want to *look at
it* — a representative thousand rows to explore, chart, and build a
pipeline against. The tools at hand:

- **`limit 1000`** (SQL LIMIT, `head`): instant (0.013s), but it
  returns the *file's head* — which in real data is rarely
  representative. Sorted exports, time-ordered logs, shard-
  concatenated dumps: the first thousand rows are one day, one
  region, one shard.
- **`shuf -n 1000`** (coreutils): uniform, but it reads everything,
  breaks structured headers, and streams nothing.

So we built `ssql sample` — and immediately learned the first lesson.

## Round 1: exact uniform sampling must read everything

`sample 1000` was implemented as textbook reservoir sampling
(Algorithm R): keep a 1000-row reservoir, and for row *i* replace a
reservoir slot with probability 1000/(i+1). Statistically perfect:
every row has exactly equal probability, and the output is exactly
1000 rows.

First live run on the 1.2GB file: **21.3 seconds**.

This is not a bug and not fixable by optimization — it is the
definition of the problem. Uniformity means row 14,628,366 must get
its fair chance, and the only way to give it one is to *reach* it:
read, and (worse) CSV-parse, every row. The interesting number
alongside: `wc -l` on the same file takes 0.174s. Touching the bytes
is cheap; parsing 14.6M rows of CSV is the 21 seconds.

An interim compromise — `limit 100000 | sample 1000`, i.e. uniform
sampling within a bounded prefix window (0.18s) — traded the bias
problem down rather than away. It lasted one day.

## Round 2: the byte-offset idea

The observation (Ross): if touching bytes is nearly free and parsing
is the cost, stop parsing rows you won't keep. Estimate where lines
live by *position*, not by reading them:

1. Pick N random **byte offsets** uniformly in the file's data
   region.
2. For each offset, **seek** there and scan to the enclosing line's
   boundaries.
3. Parse only those N lines.

Cost: N seeks + N line parses, independent of file size. This is
what databases call **system (block) sampling** — DuckDB's
`USING SAMPLE ... (system)` is the same idea at page granularity.

### The honest asterisk

"Every line has equal chance" needs one correction: a random byte
offset lands *inside* some line, so a line's selection probability is
proportional to its **byte length**, not exactly uniform. A 200-byte
row is twice as likely to be picked as a 100-byte row.

For files with near-uniform row lengths (most machine-generated CSV),
the bias is negligible. For wildly skewed rows it is real. The
resolution is not to pretend: the feature is documented as
*approximate* sampling, the exact reservoir remains available as the
`sample` pipeline stage, and the two are named consistently with the
database world's system-vs-reservoir distinction so the trade is
recognizable.

### Mechanics that matter

- **Line identity by start offset.** Scan *backward* from the drawn
  offset to the previous newline: that's the line's start, and it is
  the dedup key. Two offsets landing in the same line must not
  produce a duplicate — collisions are redrawn (bounded; on
  pathological inputs the code falls back to the exact reservoir).
- **Header handling.** The CSV header line is read first and excluded
  from the sampled region; each sampled line is then parsed by
  replaying `header + line` through the *standard* CSV reader — one
  parser, one set of type-inference rules, no second CSV dialect to
  drift. (The JSONL variant honours a leading schema-header line the
  same way; JSON *arrays* are refused loudly — they aren't
  line-oriented, so the technique cannot apply.)
- **Emit in file order.** Sampling selects; it does not shuffle.
  Keeping input order preserves the pipeline's ordering story and
  makes results comparable.

## Round 3: the performance war story

The first implementation of the idea benchmarked at **1.1 seconds**.
Better than 21 — but seeks should cost microseconds; where did a
second go?

The line-reading helper, defending against pathological
newline-free input, had a 4MB "maximum line length" cap — and read
**the whole 4MB per sampled line**, then looked for the newline.
1000 lines × 4MB = ~4GB of page-cache traffic, which at memory
bandwidth is almost exactly the observed 1.1s.

The fix: read 4KB, and only grow (×8, up to the cap) if the line
didn't fit. Result: **14 milliseconds** — a 1500× speedup over the
exact reservoir, and effectively `limit`'s speed with whole-file
coverage.

The moral is the old one: the algorithm was right, the constant
factors were the entire story, and only measuring end-to-end on the
real 1.2GB file (not a toy fixture) exposed it.

## Round 4: reproducibility across engines

ssql pipelines run five ways (interpreted, three flavours of
generated Go, and generated SQL executed by DuckDB), and the
project's differential-testing rule is that the same pipeline must
produce byte-identical results in every lane. Random sampling
threatens that unless randomness is pinned down:

- The RNG is **reference SplitMix64**, implemented in ~8 lines in the
  library itself and spec-pinned by a test against the published test
  vector. Never a standard library's `rand` — Go documents no
  cross-version stream stability.
- Every selection decision is a **pure function of (seed, row/draw
  index)** — no dependence on goroutine scheduling or iteration
  order. That's what makes "same seed, same rows" hold across the
  interpreter and all generated-code backends, verified by the
  equivalence gate.
- When no seed is given, one is chosen **and printed to stderr**
  (`sample: seed 1755741234 (pass -seed … to reproduce)`) — so even
  exploratory results can be reproduced after the fact.
- The SQL backend is the honest exception: DuckDB's sampling RNG
  cannot reproduce ours, so unseeded sampling translates to
  `USING SAMPLE` (statistically equivalent) and a *seeded* sample in
  the SQL backend is a loud error rather than a silent approximation.

## The final shape

| Method | 1000 rows of 1.2GB / 14.6M rows | Coverage |
|---|---|---|
| `limit 1000` | 0.013s | file head (biased) |
| `sample 1000` (exact reservoir stage) | 21.3s | exactly uniform |
| `limit 100000 \| sample 1000` (interim) | 0.18s | uniform within a prefix window |
| **`from csv big.csv -sample 1000`** | **0.014s core / 0.030s end-to-end** | whole file, ~uniform (∝ line length) |

Two complementary primitives, one seeded RNG:

- **`sample N` / `sample -percent P`** — the exact stage: works on
  any input including pipes, mid-pipeline; reads everything by
  necessity.
- **`from csv|tsv|jsonl FILE -sample N`** — the approximate source
  sampler: byte-offset seeks, milliseconds on gigabytes, single
  seekable file only (stdin/multi-file refuse loudly and point at the
  stage).

The interactive workspace defaults big files to the source sampler,
with the stage as the visible, editable fallback — speed *and*
representativeness, with the trade-offs stated rather than hidden.

## Takeaways

1. **Name the bias, don't average over it.** `limit` is fast-and-
   biased; reservoir is uniform-and-slow; byte-offset is fast-and-
   approximately-uniform. Users can choose when the axes are named.
2. **Sampling by position beats sampling by reading** whenever
   parsing dominates I/O — which for CSV is a factor of ~100.
3. **The correctness of an algorithm says nothing about its
   constants.** 4MB-per-line reads were "correct" and consumed 98% of
   the runtime.
4. **Determinism is a feature you design in**, not sprinkle on: a
   spec-stable RNG plus index-pure selection is what lets five
   execution engines agree byte-for-byte.
5. **Refuse loudly where equivalence is impossible** (seeded sampling
   in SQL) instead of silently returning something plausible.
