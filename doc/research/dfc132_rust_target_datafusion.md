# Generating Rust: Decision, and What the DataFusion Check Found

Reference: DFC132
Created: 2026-09-17
Last modified: 2026-09-17

[Back to Index](./README.md)

Status: **decision recorded; one generator fix shipped; nothing else
built.** Ross, 2026-09-17: "I have also been getting questions about
generating rust code. I know we talked about this with the IR codegen.
Do you think it's a good idea (I am unconvinced)" — then "if we did
should we use an existing runtime or build one in Rust of the same shape
as our Go one?" — then "write it up as a DFC and run the datafusion
check". This records the answer so the question stops being re-derived,
and the numbers from the check, which turned out to matter more than the
argument.

## 1. The question behind the question

`codegen-ir-evolution.md` §7 (2026-06) already drew the line: the
language-neutral `Op` IR makes a new target a *lowering pass*, but **the
IR does not give you the runtime**. Generated Go is a thin program calling
the typed library — `typed.ReadCSVParallel`, `typed.HashJoinParallel`,
`typed.GroupBy`, the sinks, and since DFC130 `typed.Window`. A Rust target
needs something to call.

Three things a person asking for "Rust" may actually want, with three
different answers:

1. **Performance.** No case. Compiled Go runs the README cube in
   0.23–0.28 s against DuckDB's 0.92 s (14.6 M rows, Parquet, this
   machine), with no CGO. Rust would not change the algorithm or the
   memory model.
2. **Running inside a Rust codebase / ecosystem.** Real, and the check
   below is the answer: DataFusion executes SQL, and `generate sql`'s
   output already runs on it almost unchanged.
3. **Liking Rust.** Not a reason to carry a lane.

Ask which one before doing anything.

## 2. Existing runtime, or a port of the Go one?

**Existing runtime — DataFusion.** The port only looks attractive
because the typed Go runtime is small (~600 lines on the data path; it
would map: iterators for `iter.Seq`, a sharded stream, accumulators with
`Add`/`Merge`; ownership bites in the generated structs and join buffers
— fiddly, not hard). What it costs is not lines but a **second owner of
ssql's semantics**: a sixth lane in the equivalence matrix, in a language
neither of us writes daily, needing its own CSV/JSONL/Parquet readers,
compensated sums, window frames, and the least-exercised lane is the
first to drift. The five we have found five lane bugs this month with
the gate watching; a sixth would be maintaining ssql twice.

DataFusion is the fit: Arrow-native, its own logical-plan IR with an
optimiser, parallel execution, SQL front end. ssql's `Op` lowers to it
the way it lowers to DuckDB SQL now — your IR → their IR → their
runtime. The generated artefact can still be a Rust program (a
`main.rs` that builds the plan and runs it), so "Rust code" is
literally delivered, readable and extendable. Polars is the record-model
alternative (dynamic schema). Either way the target would be **`generate
datafusion`, not `generate rust`**: a lowering plus a dialect note,
refusing loudly what does not map (signal commands, `resample`, the
expression language, `-presorted` streaming), exactly as `generate sql`
refuses today.

The one thing a port buys — ssql's own semantics by construction, where
an engine has SQL's (absent vs NULL, RANGE peers in the default frame,
arrival-order first/last, stddev of one row) — is already the DuckDB
lane's situation, documented and arbitrated by the equivalence gate. A
DataFusion lane inherits a known list, not a new one.

## 3. The check: today's `generate sql` output on DataFusion

DataFusion 54.0.0 (Python bindings, `SessionContext` with
`enable_url_table`, so `FROM 'file.csv'` works as in DuckDB), on the
codelab data and the README Parquet. Ten pipelines → eight SQL files
(`sample -seed` and `update -set-bucket` with a non-duration width are
refused at generation, as designed):

| Pipeline | DataFusion | vs DuckDB |
|---|---|---|
| `where` + `group-by -count -avg` | runs | identical |
| `join -using` + `group-by -sum -count` | runs | identical |
| `sort -desc` + `limit` | runs | identical |
| `window -rank -sum` (partition, order, default ROWS frame) | runs | identical |
| `window -range-preceding 10000 -count` | runs | identical |
| `group-by -count -cube` | runs | identical |
| `group-by -median -stddev -count-distinct -string-agg -arg-max` | **fails**: `Invalid function 'arg_max'` | — |
| `unpivot` + `group-by -sum` | **fails**: parse error at `UNPIVOT` | — |

Function-by-function, under the spellings ssql emits:

| Works on DataFusion | Does not (DuckDB-specific spelling) |
|---|---|
| `median`, `quantile_cont`, `stddev_samp`, `var_samp`, `count(DISTINCT)`, `string_agg`, `array_agg`, `bool_and`, `first_value(x ORDER BY …)` | `mode`, `arg_max`/`arg_min`, `first`/`last`, `any_value`, `list` (→ `array_agg`) |
| windows: `cume_dist`, `nth_value`, `lag(x, n, default)`, `ntile`, and `median` / `count(DISTINCT)` / `string_agg` over a frame | `UNPIVOT` (DataFusion has `unnest`, not the DuckDB clause) |

**The README cube on the 660 MB Parquet failed at first** — "Cannot
infer common argument type for logical boolean operation Int64 AND
Boolean" — and the cause was ours: DataFusion parses `a IS NOT DISTINCT
FROM b AND c` as `a IS NOT DISTINCT FROM (b AND c)`. The generator now
parenthesises each null-safe comparison (harmless for DuckDB and
Postgres; the equivalence cube/rollup cases stayed green). With that one
change the unmodified generator output runs, and:

| Engine, same file, same query, same machine | Wall |
|---|---:|
| **DataFusion 54, ssql's generated cube SQL** | **0.16–0.20 s** |
| DataFusion, native `GROUP BY CUBE` | 0.23 s |
| ssql `generate go` (typed, run only) | 0.23–0.28 s |
| DuckDB 1.5, generated cube SQL | 0.92 s |
| PostgreSQL 16 (DFC060 §Measured) | 5.1–13 s after an 11 s load |

So the "Rust ecosystem" answer is not hypothetical: **the SQL lane
already runs on DataFusion faster than on DuckDB**, with two dialect gaps.

## 4. What this decides

1. **No Rust backend.** Not a port, not a lowering, not on the roadmap.
   Recorded here so the question has a pointer.
2. **If a Rust-ecosystem ask persists, the answer is DataFusion via
   `generate sql`**: run the output on it. Two cheap improvements would
   make that a supported statement rather than an observation:
   - a `-dialect datafusion` (or `postgres`) knob on `generate sql` that
     swaps the five aggregate spellings (`arg_max`→ refuse or a
     `first_value … ORDER BY` rewrite, `mode`→refuse, `first`/`last`→
     `first_value`/`last_value` with the clause's order, `any_value`→
     `first_value`, `list`→`array_agg`) and refuses `UNPIVOT` — a
     rendering table, not a lane; DFC060's Postgres portability notes
     are the same table;
   - a `TestDataFusionInterchange`, gated on the Python bindings being
     present like the DuckDB lane is gated on its binary, running the
     equivalence corpus's SQL on DataFusion. That would be a *sixth
     oracle for the existing SQL lane*, not a sixth implementation.
3. **Shipped now:** parenthesised null-safe joins in the rollup/cube
   SQL (the only generator change the check needed).

## 5. References

- `doc/research/codegen-ir-evolution.md` §7 — the original "one IR, N
  backends" analysis and its runtime caveat.
- `doc/research/dfc123_pipeline_ir.md` — the Op IR the SQL lane lowers from.
- `doc/research/duckdb-vs-ssql.md` (DFC060) §Measured — the DuckDB and
  Postgres numbers this doc's table extends.
- `doc/research/multimode-equivalence-testing.md` — why a sixth
  implementation is the expensive thing and a sixth oracle the cheap one.
- Check script and corpus: this session's scratchpad (`dfrun.py`,
  `dfprobe.py`, `dfbench.py`); the pipelines are in §3.
