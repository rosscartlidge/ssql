# `ssql sample` — Seeded Random Row Sampling

Reference: DFC110
Created: 2026-08-21
Last modified: 2026-08-21

[Back to Index](./README.md)

**Status:** Design — for comment (queued by Ross 2026-08-20; expands
the sketch in [DFC050](./future-development.md) Priority 4)

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

The served workspace's big-file redirect changes from
`ssql from X | ssql limit 1000` to `ssql from X | ssql sample 1000`
— head sampling becomes representative instead of prefix-biased, and
the visible-stage convention is preserved (the user sees and can
edit/dial the sample). ⚡ typed heads compose automatically (the RNG
is package code the codegen calls).

## Out of scope (v1)

- `-shuffle` (permutation of output order)
- Weighted / stratified sampling (`-by field`)
- Parallel-native sharded sampling (needs stable global indices)
- `tail` and `melt` from the same DFC050 wishlist — separate work
