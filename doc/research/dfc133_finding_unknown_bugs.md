# Finding the Bugs We Do Not Know About

Reference: DFC133
Created: 2026-09-20
Last modified: 2026-09-20

[Back to Index](./README.md)

Status: **all four instruments built and run (§7): twenty-one defects
found and fixed against a suite that was fully green, plus two decisions
for Ross (§7.6).** Ross, 2026-09-20, after a week in which DFC128 closed
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
| 2026-09-20 | 4. Fuzz targets (`go test -fuzz`) | 6 targets × 45–90 s | 4 (one via a side probe) |
| 2026-09-20 | 3. Random differential (`TestRandomDifferential`, `SSQL_FUZZ=n`) | ~16,000 pipelines over the session; final 10,000 strict across 5 seeds clean; ~70/s | 7 real, 1 of my own, several tester artifacts |

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

### 7.3 Native fuzz targets (instrument 4)

`fuzz_test.go` (root) and `cmd/ssql/commands/fuzz_test.go`; 45–90 s per
target. `FuzzParseTime`, `FuzzReadCSV`, `FuzzExprToSQL` (all three
dialects) and `FuzzExprToGo` survived. Findings:

1. **The two JSON line parsers disagreed on a duplicate key whose later
   value is null** — `{"a":true,"a":null}`. JSON's convention is
   last-wins; the null-dropping parser kept the earlier `true`. Two
   seconds in. A dropped null now deletes the earlier value.
2. **Negative zero was not a fixed point**: `-0.0` wrote as `-0` and read
   back as the integer `0`. Written as `0`.
3. **`NaN` and `Infinity` made whole rows vanish.** Found by a side probe
   the fuzzing prompted, not by the fuzzer (text cannot produce a NaN):
   `update -set-expr z '0.0/0.0'` wrote the bare word `NaN`, the line
   stopped being JSON, and the NEXT stage skipped the unparseable line —
   the row gone, exit 0. JSON has no NaN; the writer emits `null` (no
   value) and the row survives. `to json` likewise.
4. **The fragment command splitter rewrote non-UTF-8 bytes** as U+FFFD
   (a Latin-1 file name) and dropped an explicitly empty `''` argument.
   Latent rather than live: `generate sql` reads structured arguments for
   the cases checked; the splitter is the fallback. Bytes, and `''` kept.

Filed, not fixed (§7.6): the readers SKIP a line that is not JSON. That
is what turned finding 3 from an error into silent loss.

### 7.4 Random differential testing (instrument 3)

`TestRandomDifferential`, `SSQL_FUZZ=<n>`: seeded adversarial tables
(NULLs weighted in, a whole NULL column, a NULL first row, ties, text
that looks numeric) × random valid pipelines of 1–4 stages over `where`
(incl. `+if`), `update -if -set`, `group-by` with order-insensitive
aggregates, `include`, `exclude`, `cast`, sort+limit and `top` on the
unique id; the interpreter against DuckDB running `generate sql`;
disagreements shrunk (stages, then rows) and de-duplicated by stage
shape. About 70 pipelines a second on eight workers: 3,000 in 40 s.

The first run agreed on 81 of 300 — almost all of it the tester's own
comparison, not ssql (an empty text cell is `""` here and NULL there, by
DFC124's decision; DuckDB prints booleans, HUGEINT sums and DECIMALs as
strings; the shrinker emptied whole columns, which changes how both
engines type them). Each artifact became a generator constraint or a
representation rule, as §5 requires, and what was left was real:

1. **SQL literals were typed by their spelling, not by the column.**
   `where -if code eq 12` on a text column rendered `code = 12`, and
   DuckDB refused to cast the column; `update -set code 12` mixed VARCHAR
   and INTEGER in a CASE. The assembler now samples the source's column
   kinds (the Postgres prologue's sampler), follows them through `cast`
   and the aggregate registry's own result types, and renders the literal
   for the column it meets (`sqlLiteralFor`).
2. **`update`'s SQL negation was not ssql's**: `+if` on a row with no
   value is true here, NULL there. §6g fixed `where` and missed `update`.
3. **Aggregates over NO values answered `""` or `0`.** `-max v` over a
   group with no v gave `""` — a present string — and the next stage's
   `where -if m le 2` compared `""` with `"2"` and matched. `-avg` gave
   0. They now have no value (a nil slot; `aggNoValue`). The empty SUM is
   0 and stays 0; the SQL lane says `COALESCE(SUM(x), 0)` to match. **This
   reverses a DFC129 choice and `Avg`'s documented 0.0 — see §7.6.**
4. **Aggregates counted the empty string as a value.** DFC124 defines
   missing for commands as absent ∨ null ∨ `""`; the aggregates skipped
   only nil, so MIN over {"Oslo", ""} answered `""` and COUNT(DISTINCT)
   counted it. One `aggMissing` at all nine sites.
5. **`cast` of an empty text cell gave 0 / false**; it stays missing, in
   exec and generated record code.
6. **`update -set zip 02134` gave three answers in four lanes**: exec
   parsed the literal and coerced it back (`"2134"`), record codegen
   stored the NUMBER 2134 in a text column, typed and SQL kept `"02134"`.
   Found only after the promoted case was given a hand-written golden —
   the tester's first comparison turned number-looking text into numbers
   on both sides and hid it. The comparison is strict now (text stays
   text), which immediately found:
7. **A CSV column holding `007` or `02134` was read as a number.** Type
   inference accepted whatever `ParseInt` accepts, so zero-padded
   identifiers — postcodes, part numbers — lost their zeros on read, for
   good. `ZeroPaddedNumber` keeps such a column text at all three
   inference sites (record reader, typed sampler, SQL sampler), as
   DuckDB's sniffer does. DataFusion 54 still reads such a column as
   Int64 — the promoted case skips that one lane and says why.

Also found on the way: a slip of my own — a trailing comment swallowed
the rest of the `-sum` registry line, leaving its wire type nil — caught
by the tester within one run and now pinned by
`TestAggRegistryEntriesAreComplete`.

Final state: 10,000 strict pipelines across five seeds, no disagreement.
Promoted: seven equivalence cases (`groupby_aggregates_over_no_values`,
`groupby_no_value_then_where`, `literal_typed_by_text_column`,
`update_literal_into_text_column_keeps_its_spelling`,
`update_negated_condition_on_missing`, `cast_empty_text_stays_missing`,
`zero_padded_codes_stay_text`), all with goldens, plus unit tests.

### 7.5 Lessons about the instruments themselves

- **A sweep's first run mostly tests the sweep** (the NUL argument; the
  81/300). Budget for it; do not read the first number as a verdict.
- **A loose comparison hides exactly the class it normalises away.**
  Converting number-looking text to numbers was convenient and hid
  findings 6 and 7. Normalise representation only where an engine forces
  it (DuckDB's string-printed sums), per column, and nothing else.
- **Goldens find what differential agreement cannot.** Finding 6 was four
  lanes giving three answers, invisible to a two-lane comparison that
  normalised both; writing the expected rows by hand exposed it.
- **Shrinking changes the question** unless constrained: a table shrunk
  to one row has all-empty columns, and an all-empty column is its own
  special case in every engine.

### 7.6 Decisions for Ross — all three taken 2026-09-20

Ross: "what is your feeling on 1, 2 and 3" → "let's do that, I agree."
Decision 1 stands as shipped. Decisions 2 and 3 were implemented the same
day (CHANGELOG "stricter about bad data"):

- **`cast` is strict** (was 3). One conversion, `ssql.CastValue`, behind
  the interpreter, generated record code (`ssql.CastField` — replacing a
  sixty-line emitted type switch per field, its own copy of the rules)
  and typed code (`ssql.MustCast`). A value that is not of the target
  type panics with a `*CastError`, which IS an error, so the CLI and
  generated programs print one line. (`MustParseTime` had panicked with a
  string; a generated program printed a Go stack trace for a bad time.)
  `-invalid missing` leaves such values without a value and counts them;
  in SQL it is `TRY_CAST`. Re-testing found the SQL lane ROUNDING on
  cast-to-int where ssql truncates; it truncates explicitly now.
- **JSON Lines readers are strict** (was 2). `*LineError` with the line
  number; `from jsonl|json -skip-invalid` is the counted opt-out, refused
  in generation mode rather than silently emitting a strict program.
  Surveying the readers for this found two more silent losses: a record
  longer than the scanner's 1 MB limit ENDED THE READ (nothing checked
  `scanner.Err()`) — the limit is 64 MB and crossing it is an error; and
  a stage whose first stdin line was not JSON returned an empty result,
  exit 0 (a JSON array piped to `where`) — it now says "this looks like a
  JSON ARRAY … `from json`".
- **What strictness exposed at once.** The first full run with strict
  readers failed `TestUnionMergeSideFileSchemaHeaderIsNotARecord` with
  "read … file already closed". Generated record-mode `join FILE.jsonl`
  deferred the side file's Close inside the function that RETURNED the
  lazy reader. Small files were already in the 64 KB buffer; a 450 KB
  side file joined 214 of 20,000 rows, exit 0 — in every release up to
  v4.102.0 — because the scanner's error was ignored. `ssql.CloseWhenDone`,
  and `TestGeneratedJoinReadsAWholeSideFile`. An ignored error is not a
  style problem; it was hiding the worst bug of the week.
- The equivalence harness learned that DuckDB 1.5's `-json` prints
  BOOLEANs as strings, per column, like its HUGEINT rule.

The original text of the three questions follows.


1. **Aggregates over no values (finding 3) reverse recorded choices**:
   DFC129 made "nothing" the empty string, and `ssql.Avg` documented
   `0.0` for an empty group. The evidence for changing — `""` is a
   present value that later conditions compare; SQL agrees with no value
   — seemed strong enough to proceed, with the tests updated to say so.
   It is one commit to reverse if you disagree.
2. **Readers skip lines that are not JSON** (`continue` on a parse
   error), in every JSONL reader. Between ssql stages a malformed line is
   a bug somewhere and should be loud; for a user's own file, leniency is
   arguable. Not changed. It is what made the NaN bug silent.
3. **`cast` of unparseable text is still 0** (`abc` → 0), the behaviour
   DFC124 removed from the reader. Only the empty cell was changed here.
   `cast … time` is already loud.

### 7.7 What the four instruments say together

Twenty-one defects in about five minutes of machine time, against a
suite that was fully green. Several silently lose or change ordinary
data: a join returning nothing, rows vanishing on NaN, postcodes losing
their zeros, a NULL read as 0. None needed imagination, only inputs
nobody had typed. The cheap oracle-free sweeps found as much as the
differential one; they are not substitutes — each found what the others
structurally could not.

## 8. References

- [DFC102](./multimode-equivalence-testing.md) — the N-way differential
  harness; "a test's power = oracle strength × input discrimination".
- [DFC128](./dfc128_json_interchange_and_time_type.md) §6a–§6g — the
  week's findings this plan is derived from.
- [DFC113](./dfc113_scale_gate.md) — the model for an opt-in gate with
  generous budgets and no stored baselines.
- [DFC115](./dfc115_commands_are_the_authority.md) — why the sweeps are
  driven by `-spec-json` and the schema-ops, never a second grammar.
