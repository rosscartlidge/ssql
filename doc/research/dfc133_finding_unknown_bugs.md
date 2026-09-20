# Finding the Bugs We Do Not Know About

Reference: DFC133
Created: 2026-09-20
Last modified: 2026-09-20

[Back to Index](./README.md)

Status: **instruments 1 and 2 built and run (§7): seven defects found
and fixed, none of which any existing test could see. Instruments 3 and
4 are next.** Ross, 2026-09-20, after a week in which DFC128 closed
six planned decisions and surfaced about fifteen defects nobody had asked
about: "I am worried we might have bugs we don't know about — can you
think of a way of exercising the system to find bugs?" then "let's write
a doc on this and then run them".

## 1. What the week's bugs say

The differential gate (`TestPipelineEquivalence`, DFC102) is the best
instrument the project has: it found most of what follows. But every
case in it is hand-written, so it sees only what someone thought to try.
The clearest example: `empties_where_numeric` used `n gt 5` for months.
An absent value read as zero also fails `gt 5`, so the case could not
see record codegen comparing NULL as 0. `n ge 0` could, and did, the
first time it was written. The oracle was fine; the input was not
discriminating, and nobody knew.

Classifying what 2026-09-14…20 found, by what would have found it
WITHOUT a person guessing the case:

| Class | Instances this week | What finds it mechanically |
|---|---|---|
| **Result depends on which row is first** | nullable column dropped (header from record 1); type locked by a first-row null; `where` validation against record 1; typed `where` literal; JSON array type lock | reorder the input rows; a multiset result must not change (§3) |
| **Absent / NULL treated as a value** | NULL → 0 in JSON arrays; record codegen absent-as-zero in `where` and `update -if`; SQL `NOT` over NULL; all-NULL column loses its name | adversarial rows in generated data, differential against SQL (§5) |
| **Degenerate input or flag value** | `to table -max-width 0` panic; `-range-preceding 10d` on a numeric field ran silently | every command × empty/one-row/all-null input, every flag × 0/−1/unknown (§4) |
| **Lane fixed, other lanes not** | `_line_number` (exec fixed, `ReadJSONAuto` not); `update -set-expr` storing `%v` times; typed cast/sort/where lacking time | any generated pipeline through every lane (§5) |
| **Representation** | column order on a pipe; TSV order; Go `Time.String()` in sinks; float association in SQL interpolation | round-trip identities (§3), differential (§5) |
| **A doc example that never worked** | correlate's `-x lag`; the original DuckDB claim | already gated: `TestCodelabRuns`, the interchange tests |

Two things stand out. The largest class needs **no oracle at all** — only
the observation that a multiset result cannot depend on row order. And
nearly everything else is reachable by the differential oracle that
already exists, if the inputs stop being chosen by hand.

## 2. The instruments, in build order

| # | Instrument | Oracle | Cost | Targets |
|---|---|---|---|---|
| 1 | Row-order / first-record sweep | metamorphic: output multiset invariant under input reordering | ~1 day | first-record dependence, type locks, header inference |
| 2 | Degenerate-input crash sweep | no panic; output or a clear error; exit status honest | ~½ day | panics, silent nonsense on edge flags and empty input |
| 3 | Random differential testing | exec vs DuckDB (fast), Go lanes on shrunk failures | 2–3 days | NULL semantics, lane drift, anything in the grammar nobody tried |
| 4 | Native Go fuzz targets | no panic; parse/print round trips; VM vs transpiler | ~½ day | parsers: JSON line, CSV, `ParseTime`, expression transpilers |

All four are **opt-in gates** like the scale and triples gates
(`SSQL_SWEEP=1`, `SSQL_FUZZ=N`), with fixed seeds so a failure is
reproducible, and with every surviving finding **promoted to a permanent
hand-written case** in the equivalence corpus or a unit test. The sweep
is the search; the corpus is the memory.

## 3. Instrument 1: the row-order sweep

**Relation.** For a pipeline P whose output has no defined order (or is
made orderless by comparing as a multiset), and any permutation π of the
input's data rows: `P(data) ≡ P(π(data))` as multisets of rows, and the
**header (field set, order and types) must be identical**. No second
implementation is needed; ssql is compared with itself.

**Permutations that matter**, not random ones: for each row r, the
variant with r moved to the FRONT (n variants, the first-record class
exactly); full reversal; and one seeded shuffle.

**Rows that matter.** The sweep appends adversarial rows to each fixture
before permuting, because the existing fixtures are clean: a row with
every optional cell empty; a row with a float in an all-int column; a row
with an int-looking string in a text column; for JSON, an explicit null
in each field in turn, and a field present in only one row.

**Pipelines.** Every `TestPipelineEquivalence` case whose `Ordered` is
false, run in the exec lane first (fast, and where the first-record
class lived), then the same relation through `generate go` record mode
for the cases that survive. Order-defining pipelines (`sort`, `top`)
are included when the sort key is unique in the fixture — then the
ORDERED output must be invariant too.

**Expected exclusions**, stated up front so the sweep is not drowned:
arrival-order aggregates (`-first`, `-last`, `-any`, `-string-agg`,
`-collect`, `-arg-max` on ties), `limit`/`offset` without a sort,
`sample`, `window` without `-order`, `fill -down`, `from -last`. These
depend on row order by definition; the sweep skips a pipeline containing
them rather than "fixing" the comparison.

**Formats.** The same data as CSV, TSV, JSONL and a JSON array, because
the first-record bugs were format-specific (headers inferred differently
per reader).

## 4. Instrument 2: the degenerate-input crash sweep

Driven by `ssql -spec-json`, the CLI's own description of every command
and flag — no second copy of the grammar (DFC115).

- **Inputs** for every data command: empty stdin; a header and no rows; a
  single row; an all-NULL column; a 1 MB single cell; a header with a
  duplicate name; a column named like a builtin or keyword (`date`,
  `count`, `select`, `type`).
- **Flag values** for every flag by declared type: ints get `0`, `-1`, a
  huge value, a non-number; field-typed flags get a name that does not
  exist; enumerated flags get a value outside the set; durations get
  `0s` and garbage.
- **Assertions.** Never a Go panic or stack trace (main recovers panics
  into `Error:` lines — a trace means something escaped). Exit status 0
  implies output that parses; non-zero implies a message on stderr.
  An unknown field name is an error, never an empty result.
- Each command also runs under `SSQL_MODE=record … | generate go` for the
  same inputs: generation must succeed or refuse with a message, never
  emit code that does not compile (compile a sample, not all — seconds
  each).

## 5. Instrument 3: random differential testing

**Generator.** Small tables (3–12 rows, 3–6 columns) from a seeded RNG
with adversarial cells weighted in: NULL/empty (including the whole
first row, and a whole column), 0, −1, large ints past 2^53, floats that
are whole numbers, negative zero, duplicate keys and ties, strings that
look like numbers, booleans, ISO dates in mixed forms, a quoted comma, a
unicode string, a very long string, column names with spaces, capitals
and reserved words.

**Pipelines** drawn from the commands whose semantics are fully
specified and SQL-translatable: `where` (flag and `+if` forms, all
operators), `sort`, `top`, `group-by` (the registry aggregates that are
order-insensitive), `update -if/-set`, `cast`, `include`, `exclude`,
`rename`, `distinct`, `limit` after a total sort, `join -using` against a
second generated table, `union`. Length 1–4 stages. Field arguments are
drawn from the CURRENT column set, tracked through the same schema
operations completion uses, so generated pipelines are valid far more
often than not; an invalid one must fail identically in both lanes.

**Oracle.** exec vs the DuckDB lane with the equivalence harness's own
normalisation (null ≡ absent, int ≡ float, time spellings). About 50 ms
per case, so a minute buys over a thousand pipelines. The Go lanes cost
seconds each to compile: run them only on the corpus of SHRUNK failures
and on a small random sample per run.

**Shrinking.** On disagreement: drop stages from the end, then from the
start, then rows, then columns, re-checking the disagreement each time.
A finding is reported as the smallest pipeline and table that still
disagree, with the seed.

**Known differences** are constraints on the generator, not exceptions
in the oracle: no arrival-order aggregates, no `limit` without a total
order, float aggregates compared with a relative tolerance of 1e-12
(documented last-place effects), no sampling, DuckDB's session-zone
shift of `+00` strings (no zoned time strings in generated data).

## 6. Instrument 4: native fuzz targets

`go test -fuzz` over the pure functions where a crash or a disagreement
is unambiguous: `ParseJSONLine`/`ParseJSONLineWithNulls` (no panic;
parse → `AppendJSON` → parse is a fixed point); the CSV reader (no panic;
header order preserved); `ParseTime` (accepts ⇒ `Format(RFC3339Nano)`
re-parses to the same instant); `exprToGo`/`exprToSQL` against the VM,
seeded from the existing `TestExprGoDifferential` corpus;
`parseCommandArgs` (the fragment command splitter: split → join → split
is stable). Short runs in CI-less development: a fixed `-fuzztime` per
target when the gate is asked for, corpus checked in under `testdata/fuzz`.

## 7. Run log

Filled in as each instrument runs: what ran, how much, what it found,
what was fixed or filed.

| Date | Instrument | Volume | Findings |
|---|---|---|---|
| 2026-09-20 | 1. Row-order sweep (`TestRowOrderSweep`, `SSQL_SWEEP=1`) | 64 pipelines from the equivalence corpus × ~12 reorderings × 3 formats (CSV, JSONL, JSON array), adversarial rows appended; ~30 s | 3 real, 1 expected |
| 2026-09-20 | 2. Crash sweep (`TestCrashSweep`, `SSQL_SWEEP=1`) | 26 commands, every flag × degenerate values × 6 inputs = 4,998 runs; ~55 s | 1 panic (+2 of the same shape found by reading), 22 unknown fields accepted, 92 artifacts of the sweep itself |

### 7.1 Row-order sweep

1. **`join` matched NOTHING when the key was an int column on one side
   and a float column on the other.** One `2.5` anywhere in a CSV column
   makes the reader type the whole column float. The library's hash key
   printed both sides (`"3"`), so the bucket matched — and then the
   confirming `Match` compared the raw interfaces, where `int64(3) !=
   float64(3)`. Every row of the join vanished, exit 0. The field-pair
   predicate already compared printed values; the two disagreed inside
   one file. Fixed with `joinValuesEqual` (numbers as numbers, a number
   never equal to text). The typed lane refused the same join ("join key
   types differ"); it now widens a numeric key to float64. Promoted:
   equivalence case `join_int_key_to_float_key` (golden),
   `TestJoinIntKeyToFloatKey`. *How it was found:* the sweep's appended
   float row changed a key column's type; the original order returned
   zero rows and a permuted order returned four.
2. **A CSV header's column TYPE came from the first row.** D3 fixed JSON
   and left the branch for sources with their own header alone: a first
   row with an empty cell wrote `pop:string` though the reader had typed
   the column float; any other row first wrote `pop:float`. Downstream
   typed generation, Parquet output and the workspace read that type.
   `from` now reads on until every column has shown a value (one record
   for clean data, as before). Promoted:
   `TestHeaderDoesNotDependOnRowOrder` (CSV, TSV, JSONL, JSON array).
3. **The JSON array reader coerced a column to the type of its FIRST
   value.** A column holding `12` and `"12"` came out all numbers or all
   strings depending on element order, and a filter kept or lost rows
   accordingly; the JSONL reader kept each value's own type. The lock is
   gone (with ~150 lines: `setValueWithType`, `inferJSONFieldType`,
   `coerceValueToType`); both readers now behave identically and the
   header widens int+float as before. Promoted in the same test.
4. *Expected, handled in the sweep:* sample standard deviation differs
   in the last place with accumulation order. Floats compare to 12
   significant digits.

### 7.2 Crash sweep

1. **`sample 999999999999999999` panicked** — `makeslice: cap out of
   range`: the reservoir pre-allocated capacity N. The same shape sat in
   `TakeLast` (behind `limit -last N`) in both the record and typed
   packages. Capacity is now a hint (`min(n, 65536)`).
2. **22 flags accepted a field that does not exist** and exited 0:
   every `window` function's input field (`window -sum nosuchfield t`
   summed nothing), `exclude`, `rename -as`, `fill -default` (which
   silently ADDED a column holding the default), and `fft` /
   `spectrogram -field` (an unknown field read as a signal of zeros and
   produced a confident, meaningless spectrum). All now validate against
   the schema, as the project rule has always required. `update -set`
   names a new field by design and is whitelisted in the sweep.
3. *Artifacts:* 92 "non-zero exit with nothing on stderr" were the
   sweep passing a NUL byte as an argument, which the OS refuses, so the
   command never started. Replaced by an embedded newline. Worth
   recording: a sweep's first run mostly tests the sweep.
4. *Seen by hand while triaging, filed not fixed:* an empty result name
   is accepted (`group-by dept -sum age ''` creates a field named `""`).

### 7.3 What this says about instruments 3 and 4

Two cheap, oracle-free sweeps found seven defects in under two minutes
of machine time, against a suite that was fully green — including one
(the join) that silently returns an empty result for ordinary data. None
needed imagination, only inputs nobody had typed. That is a strong prior
that random differential testing (§5) will pay: it explores the same
space with a second engine as oracle, where these two could only compare
ssql with itself.

## 8. References

- [DFC102](./multimode-equivalence-testing.md) — the N-way differential
  harness; "a test's power = oracle strength × input discrimination".
- [DFC128](./dfc128_json_interchange_and_time_type.md) §6a–§6g — the
  week's findings this plan is derived from.
- [DFC113](./dfc113_scale_gate.md) — the model for an opt-in gate with
  generous budgets and no stored baselines.
- [DFC115](./dfc115_commands_are_the_authority.md) — why the sweeps are
  driven by `-spec-json` and the schema-ops, never a second grammar.
