# `ssql/typed` Roadmap History

Reference: DFC141
Created: 2026-09-29
Last modified: 2026-09-29

[Back to Index](./README.md)

The phase-by-phase record of what the `ssql/typed` package and its
codegen shipped, and when, lifted out of `doc/typed-reference.md` on
2026-09-29 (DFC140 batch 4: a user reference states the current API;
history lives here). Nothing below is a plan — every unchecked item is
listed in typed-reference's "What falls back to Record" section, which is
the live list. The design documents are
[typed-package-proposal.md](./typed-package-proposal.md) and
[typed-codegen-proposal.md](./typed-codegen-proposal.md).

## The status box typed-reference carried until 2026-09-29

> **Status:** Phase 1.5 — CSV + JSONL I/O, core operations
> (Where, Limit, Skip, Select), full join family (Hash, HashMulti,
> Left, Right, Full), streaming aggregation (GroupBy, GroupByOrdered,
> standalone Sum/Count/Min/Max/Avg), field types
> string/bool/int/int32/int64/uint64/float32/float64/time.Time + pointers.
> Arrow I/O is the next major addition.
> See [`doc/research/typed-package-proposal.md`](research/typed-package-proposal.md)
> for the design and [`doc/research/typed-performance-notes.md`](research/typed-performance-notes.md)
> for known optimization opportunities.

The box was written at Phase 1.5; Parquet, TSV, the parallel `Stream[T]`
runtime, window functions, rollup/cube, ASOF joins and set operations
all shipped after it without the box changing.

## Roadmap, as it stood on 2026-09-29

Phase 1 (shipped):
- [x] CSV I/O with header inference
- [x] `Where`, `Limit`, `Skip`, `Select`
- [x] `HashJoin` (inner)
- [x] Benchmarks demonstrating the gap

Phase 1.5 (shipped):
- [x] `time.Time` (RFC3339), `int32`, `uint64`, `float32`
- [x] Pointer-to-T for nullable columns
- [x] Full join family: `HashJoinMulti`, `LeftJoin`, `RightJoin`, `FullJoin`
- [x] JSONL reader/writer (`ReadJSONL`, `ReadJSONLSafe`, `WriteJSONL`)
- [x] Streaming aggregation: `Count`, `Sum`, `Min`, `Max`, `Avg`,
      `GroupBy`, `GroupByOrdered`, `Counter`, `Summer`, `Averager`

Phase 1.6 (shipped 2026-04-26):
- [x] `HashJoinSized` with capacity hint for known right-side size
- [x] Strict-mode CSV reader via `Strict()` option
- (Tried and rejected: custom byte-level CSV reader — see
  [`research/typed-performance-notes.md`](research/typed-performance-notes.md))

Phase 1.7 (shipped 2026-04-27 — unblocks Tier 3 codegen):
- [x] `SortBy[T,K]`, `SortByDesc[T,K]`, `SortByStable[T,K]`
- [x] `Distinct[T,K]` (streaming, hash-set state)
- [x] `Concat[T]`, `Union[T,K]`
- Tier 3 codegen for `sort` / `distinct` / `union` can now wire into
  these directly. See
  [`research/typed-package-proposal.md` §6a](research/typed-package-proposal.md#6a-library-phases-after-phase-1).

Phase 1.8+ (open):
- [ ] Arrow reader/writer (`ReadArrow[T]`, `WriteArrow[T]`)
- [ ] Faster JSONL via `goccy/go-json` or per-type generated unmarshallers
- [ ] Hand-rolled RFC3339 time parser (~3× over `time.Parse`)

Phase 2 — Tier 1 shipped (2026-04-26):
- [x] `SSQL_MODE=typed ssql generate go` — schema-aware code generation that
  emits calls into this package directly. Tier 1 covers
  `from FILE.csv` (header sampled at generation time, struct types
  auto-derived), `where -if FIELD OP VALUE` (literal operators only),
  `join FILE.csv -using FIELD` (single-key + process-substitution),
  `to csv`, and `to table`. Other commands abort with a clear error.
  See [`research/typed-codegen-proposal.md`](research/typed-codegen-proposal.md).

```bash
# Same prototype pipeline you'd run interactively...
SSQL_MODE=typed ssql from employees.csv \
    | ssql where -if years ge 5 \
    | ssql join departments.csv -using dept_id \
    | ssql to csv seniors.csv \
    | ssql generate go > pipeline.go

# ...is now a self-contained, type-safe Go program.
go run pipeline.go              # uses defaults
go run pipeline.go -input emp_q4.csv
```

**Measured impact of typed codegen vs the alternatives**, on 1M
employees × 1k departments, identical pipeline expression
(see `cmd/ssql/codegen_bench_test.go`):

| Mode | Wall time | Peak RSS |
|---|---:|---:|
| CLI pipeline (interactive) | 3.08 s | 33 MB |
| Record codegen (`SSQL_MODE=record`) | 2.69 s | 910 MB |
| **Typed codegen (`SSQL_MODE=typed`)** | **0.77 s** | **8.7 MB** |
| Speedup vs CLI | **4.0× faster** | — |
| Speedup vs Record codegen | **3.5× faster** | **104× less memory** |

Typed codegen wins on every dimension simultaneously: single-process
execution beats the CLI pipeline's per-stage process+pipe overhead,
and stack-allocated structs beat Record codegen's `map[string]any`
peak RSS. Reproduce: `go test ./cmd/ssql/ -run TestCodegenBench -timeout 10m -v`.

Phase 2 — Tier 2 shipped (2026-04-26):
- [x] `limit N` (typed.Limit), `offset N` (typed.Skip)
- [x] `include` / `exclude` / `rename` (typed.Select with derived struct)
- [x] `group-by FIELDS… -count -sum -avg -min -max` (typed.GroupBy with
      synthesized aggregator + result struct, single- or multi-field keys)

Phase 2 — parallel-form codegen shipped (2026-04-27):

> **Merged into `SSQL_MODE=typed` in v4.40.** What started as a separate
> `SSQL_MODE=parallel` is now what the `typed` planner emits automatically
> when the pipeline can exploit it; `SSQL_MODE=parallel` survives only as a
> deprecated alias. The entries below describe that parallel form.

- [x] Same pipeline shape as the serial typed form, with `from`/`where`/`join`/`group-by`/`to csv`/`to table` emitting Stream-based parallel code (typed.ReadCSVParallel + Stream.Where + typed.HashJoinParallel + typed.GroupByParallel).
- [x] Other typed-aware commands (limit, sort, distinct, union, top, cast, update, include/exclude/rename) drop the pipeline to the serial `iter.Seq[T]` form rather than erroring — the planner picks per stage (since v4.40; before that, parallel mode rejected them).
- [x] **Per-shard buffer dump CSV sink (2026-04-27)** — `to csv` in the parallel form emits `Stream.WriteCSVToWriter` (no `Serial()` fan-in). Each shard formats into its own buffer in parallel, dumped in shard order. Wide-output workload (7.25M-row CSV write) went from 0.73× typed-serial to **4.4× faster** with this fix. Trade-off: peak memory ~2× output size.
- [x] **`GroupByParallel` with Sink/Combine/Finalize (2026-04-27)** — `group-by` in the parallel form emits `typed.GroupByParallel`. Each shard accumulates its own partial `map[K]Aggregator`; Combine merges per shard sequentially; Finalize yields rows lazily. Synthesized `<Input>Aggregator` gets a `Merge` method generated from the aggregation specs. **4.0× faster than typed-serial** on the 10M-row × 1 000-group workload (count+sum+avg+min+max).
- **When the planner picks it:** filter-heavy / aggregating / transform-and-write / group-by pipelines. **4.4× faster** on the CSV write workload (1.3 s vs serial 5.7 s; DuckDB 0.7 s — 1.86× ahead). **4.0× faster** on the group-by workload (0.95 s vs 3.80 s; DuckDB 0.39 s — 2.4× ahead). **6.4× faster** for count-only sinks. The planner keeps the **serial** form when the output is too large to buffer in RAM, when input-order output is required, or when `group-by -presorted` is used. See [`research/typed-codegen-proposal.md` §5d](research/typed-codegen-proposal.md#5d-parallel-mode-codegen-ssqlgoparallel) and [`research/typed-groupby-parallel-proposal.md`](research/typed-groupby-parallel-proposal.md).

Phase 1.8 — TSV / Parquet readers (2026-04-28):
- [x] **`typed.ReadDelim` / `ReadDelimParallel` / `WriteDelim`** — fast delimited-text reader with no quoting (default '\t'). Zero-copy field strings via `unsafe.String` (parallel only); SIMD-accelerated split via `bytes.IndexByte`. 18% faster than `ReadCSV` on a 14.6 M-row corpus; memory-bandwidth-bound at ~600 MB/s. Use when data is clean (no embedded quotes / delimiters / newlines).
- [x] **`typed.ReadParquet` / `ReadParquetParallel` / `WriteParquet`** — Parquet input/output via existing Apache Arrow Go dependency. Snappy compression by default. Row groups partition naturally to shards. **`ParquetColumns(...)` is the primary speed lever**: on the 14.6 M-row corpus group-by-with-count benchmark, restricting the read to the single grouped column dropped wall time from 1.51 s to **0.15 s** — a 10× win, within 5× of DuckDB's 0.03 s on the same query. Without column projection Parquet performs about the same as TSV (decompression at ~1–2 GB/s ≈ TSV memory bandwidth ceiling).

Phase 2 — Tier 3a shipped (2026-04-27, on top of Phase 1.7):
- [x] `sort FIELD` and `sort FIELD -desc` (single-field)
- [x] `distinct` (full-row dedup; pointer fields compare by identity)
- [x] `union -file FILE` and `union -file FILE -all` (cross-source
      schema validation — mismatched fields error with a clear message)

Phase 2 — Tier 3b shipped (2026-04-27, Sprint 1+2 of the Tier 3 roadmap):
- [x] `top N -field F` (sort + limit composition)
- [x] Multi-field `sort` via composite comparator (`typed.SortByFunc`)
- [x] `cast -type FIELD TYPE` — string/int/float/bool conversions; emits a
      derived struct with the field's Go type changed
- [x] `update -set FIELD LITERAL` (unconditional, literal values only;
      adds a derived "Updated" struct when new fields are introduced)
- [x] `update` with conditional clauses (`-if F OP V -set ...
      + ...`); first-match-wins as an if/else-if chain

Phase 2 — since shipped (the list this section carried as "still
deferred" until 2026-09-29; see the journals for each):
- [x] `-if-expr` / `-set-expr` / `-expr` aggregations: the expr→Go
  transpiler (`expr_go.go`), with a differential gate against the VM;
  an untranspilable construct falls back to record codegen loudly
- [x] `-rollup` / `-cube`: `RollupEnrich` (`typed_rollup.go`)
- [x] Window analytic functions: `Window` (`typed_window.go`)
- [x] JSONL and Parquet typed I/O: `ReadJSONL[Parallel]`,
  `ReadParquet[Parallel]`, `WriteParquet`; `from ssh` in typed mode
- [x] ASOF join (`AsofJoin[Parallel]`), set operations (`Except`,
  `Intersect`, the ALL and Parallel forms), many-to-many parallel join
  (`HashJoinMultiParallel`), `DistinctParallel`, `TopByParallel`,
  `TakeLast`, the Record→typed boundary (`FromRecords[Parallel]`)
  (DFC137, 2026-09-26/27)

Still record-only (typed codegen falls back to record for the stage,
with the reason under `-explain`, or refuses loudly):
- [ ] `-collect` (a slice-typed result field)
- [ ] Multi-clause joins and `-as` renames; `join -type left|right|full`
  and `join -asof -type left` (a struct cannot hold an absent right
  field, DFC124 §3)
- [ ] `sort`/`group-by -spill` (the out-of-core sort is record-only)
- [ ] `pivot`, `merge`, signal processing (FFT, convolve, spectrogram)

See [`doc/research/typed-package-proposal.md`](research/typed-package-proposal.md)
for the full design and Phase 2 vision.
