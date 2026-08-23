# `ssql sample` — Seeded Random Row Sampling

Reference: DFC110
Created: 2026-08-21
Last modified: 2026-08-23

[Back to Index](./README.md)

**Status:** Implemented 2026-08-22 as designed (one emendation:
`-percent 0` is a loud error rather than pass-through — `sample 0` is
the dial; a zero percentage reading as "everything" was too confusing
to ship).

## Problem

Exploring big data means looking at a representative slice, and today
ssql only offers `limit` — a PREFIX, not a sample. The bias is not
theoretical: the served workspace opens >32MB files through
`ssql limit 1000`, which shows the file's head (often one shard, one
day, one region); the docs' workaround is coreutils `shuf`, which
breaks schema headers, streams nothing, and exists on no one's phone.
SQL has TABLESAMPLE; ssql should have `sample`.

## Command surface

```bash
ssql from big.csv | ssql sample 1000            # exactly N rows (reservoir)
ssql from big.csv | ssql sample -percent 5      # ~5% of rows (Bernoulli)
ssql from big.csv | ssql sample 1000 -seed 42   # reproducible
```

- `N` (positional) and `-percent P` are mutually exclusive; one is
  required. `sample 0` = pass-through, mirroring `limit 0`'s
  no-limit dial (Ross's convention: dial a stage off instead of
  editing it out).
- `-seed S` (int64). **Default: seeded randomly, and the chosen seed
  is printed to stderr** (`sample: seed 1755741234`), so any
  exploratory result can be reproduced after the fact. Loud
  reproducibility instead of silent nondeterminism.
- Row ORDER of the output: input order (reservoir emits in a single
  pass at end-of-input; Bernoulli streams). Order among sampled rows
  is NOT randomized — `sample` selects, it does not shuffle. (A
  `-shuffle` flag can come later if wanted; keeping selection and
  permutation separate keeps the streaming story honest.)

## The hard requirement: cross-lane determinism

The one-semantics-many-backends doctrine makes this command's real
design constraint explicit: **`sample -seed 42` must produce the
IDENTICAL row set in every lane** — exec, record codegen, typed
codegen, parallel, and `generate sql` — or the equivalence gate can
never cover it and it becomes the next `top`-by-string saga.

Consequences:

1. **The RNG lives in the ssql package** (one implementation, used by
   exec and generated code alike): a small, explicitly-specified
   generator (splitmix64 — 8 lines, stable forever), NEVER math/rand
   (whose stream is not guaranteed across Go versions).
2. **Selection must be a pure function of (seed, row index), not of
   goroutine scheduling.** Bernoulli: keep row i iff
   hash(seed, i) < P·2⁶⁴ — trivially parallel-safe since every shard
   knows its rows' global indices... which parallel shards DON'T
   cheaply know. So:
   - Serial lanes: row counter.
   - Parallel lane: `Capabilities.SerialOnly` for v1 — the planner
     already inserts Serial() boundaries; sampling typically REDUCES
     data enough that downstream can re-parallelize later (and
     ParallelFromSlice exists for re-entry). A sharded deterministic
     design (per-shard sub-seeds over stable shard indices) is a v2
     upgrade if profiles demand it.
3. **Reservoir (`sample N`) with Algorithm R is also index-driven**
   (replacement decisions are hash(seed, i) draws), so the same
   purity argument applies; SerialOnly in v1.
4. **`generate sql`:** DuckDB has `USING SAMPLE N ROWS (reservoir,
   SEED)` / `USING SAMPLE P% (bernoulli, SEED)` — but its internal
   RNG can NOT match ours, so seeded byte-identity across the duckdb
   lane is impossible. Honest resolution: translate UNSEEDED sample
   to DuckDB's forms (statistically valid), and FAIL LOUDLY on
   `-seed` in `generate sql` ("seeded sampling has no cross-engine
   deterministic equivalent — drop -seed or use generate go"). The
   equivalence case for the duckdb lane then asserts CARDINALITY and
   schema, not row identity; the Go lanes assert byte-identity on a
   fixed seed.

## Testing

- `TestPipelineEquivalence` case: `from shuffled.csv | sample 20
  -seed 42 | sort key` — byte-identical across exec/record/typed/
  parallel (Ordered after the sort; shuffled fixture per doctrine);
  duckdb lane covered by an unseeded cardinality case.
- Property tests: sample N of M<N rows returns all M; percent 100 =
  identity; percent 0 / N=0 pass-through semantics; seed printed to
  stderr exactly once.
- Statistical smoke (non-flaky): chi-squared bound over a large fixed
  seed set, tolerances wide enough to never flicker.
- Sabotage: flip the hash constant once — the equivalence case must
  fail.

## Workspace integration (the motivating consumer)

**Amended after live testing (Ross, 2026-08-23):** pure `sample 1000`
reads the whole file — reservoir sampling cannot early-exit — which
cost 21s on the 1.2GB fixture vs limit's 0.013s. The redirect
therefore composes the dials: `ssql from X | ssql limit 100000 |
ssql sample 1000` (0.18s) — a bounded read window with uniform
sampling inside it, both stages visible and dialable (`limit 0` =
true whole-file uniformity, accepting the read). The codelab
documents the full-read characteristic. ⚡ typed heads compose
automatically (the RNG is package code the codegen calls).

## Amendment 2 (2026-08-23): byte-offset sampling at the source

Ross, after the windowed compromise: "what about [an] algorithm that
picks N lines and estimates their position ... uses seek and search
for the next line — it would be fast and every line has equal chance."
Implemented as `ssql.SampleCSVFile` + `from csv/tsv -sample N
[-sample-seed S]`: draw offsets via the same splitmix64, scan back to
line start, dedupe by start offset, parse each line under the header
schema via the STANDARD CSV reader (one parser, one dialect). 14ms
for 1000 rows of the 1.2GB fixture — after fixing a first version
that read the 4MB line-cap per sampled line (~4GB of page-cache
traffic; chunked grow-on-demand reads now). One honest asterisk:
selection probability is proportional to line LENGTH (system/block
sampling), vs the stage's exact reservoir — documented, and the
equal-chance claim holds only for near-uniform rows. Seeded
determinism survives (offsets are pure functions of seed and draw
index over fixed file bytes): the equivalence gate runs
from_sample_seeded byte-identical across the Go lanes; generate sql
translates unseeded -sample to USING SAMPLE N ROWS (reservoir) and
refuses -sample-seed loudly. Fallbacks to the exact reservoir: file
smaller than ~8n bytes, or collision-redraw exhaustion. The
workspace big-file redirect now opens `from csv X -sample 1000` —
whole-file representative AND fast, superseding the limit-window
compromise. Same-day follow-up (Ross): the core generalized to
`sampleLineStarts` with TSV (delimiter auto-detect via the standard
reader) and JSONL wrappers (leading _schema header honoured; JSON
arrays refuse loudly — not line-oriented); all three `from`
subcommands carry -sample/-sample-seed with identical stdin/
multi-file/pushdown refusals.

## Out of scope (v1)

- `-shuffle` (permutation of output order)
- Weighted / stratified sampling (`-by field`)
- Parallel-native sharded sampling (needs stable global indices)
- `tail` and `melt` from the same DFC050 wishlist — separate work
