# The type week: what landed, what it fixed, what remains

Reference: DFC164
Created: 2026-11-06
Last modified: 2026-11-06

[Back to Index](./README.md)

Status: **retrospective, 2026-11-06.** Ross asked: "could you write a summary DFC
and what we have added and fixed in the recent work and what still needs
to be done". It covers 2026-11-03 to 2026-11-06:
- releases v4.137.2, v4.138.0, v4.139.0 and v4.140.0;
- the batch committed after v4.140.0 as 536a34e8, which is not
  released yet.

The designs live in [DFC162](./dfc162_uuid_and_ip_address_types.md)
(UUIDs and IP addresses) and
[DFC163](./dfc163_type_operation_matrix_and_domain_lint.md) (the lint and
the matrix). This doc is the ledger, a single starting point for the
release note and the next TODO triage.

## 1. Where the week came from

It began with a question: "does ssql have a uuid function?" DFC162
surveyed UUIDs and IP addresses, and the probing found two Parquet read
bugs. Building `inet` then found seven bugs that had nothing to do with
inet. Each was a type switch, or a table keyed by type name, that a newer
type had never been added to.

Ross: "great that you are finding new bugs — can you think of a way of
finding these more proactively?" That question turned the rest of the
week into instruments rather than features. DFC163 proposed two:
- a static **domain lint**;
- a dynamic **type × operation matrix**.

Both were built, and between them they found over forty bugs. Ross
decided the semantics questions the matrix raised. The last batch
(DFC163 §7.7) built those decisions and fixed what was left.

**Size.** `git diff v4.137.1 536a34e8`: 147 files, about 9,200 lines
added. The equivalence suite went from 344 cases to 399, and there are
50 CHANGELOG entries since v4.137.1.

## 2. Features landed

| Feature | Release | Design | Gate |
|---|---|---|---|
| Parquet nested columns read by leaf. A nested value is `json`, a map an object, a UUID its canonical text (record and typed readers, `internal/pqcols`) | v4.137.2 | DFC162 §1 | `parquet_nested_uuid`, `parquet_past_nested_columns` |
| `uuid4()`, `uuid5(ns, name)`, `uuid7()`: canonical text, no type. All three are on DuckDB; `uuid4` also on Postgres and DataFusion | v4.138.0 | DFC162 §8 | shape-checking equivalence cases; `uuid5` Golden from Python |
| `+` on strings translates to SQL `\|\|` | v4.138.0 | journal 2026-11-04 | `expr_string_concat` |
| **The `inet` type** with `isip`, `ip`, `inet`, `ipfamily`, `ipint`, `ipkey`, `ipnet` and `ipin`. Ordered as `netip.Prefix.Compare`, which is DuckDB's and Postgres's order. DataFusion refuses | v4.138.0 | DFC162 §9 | 18 cases with Goldens from Python `ipaddress`; `TestInetExec` |
| **The domain lint.** `// ssql:domain D` on every switch over record values, checked in `make vet`. 193 sites | v4.139.0 | DFC163 §6 | `internal/lint/typedomain` tests; `TestScalarDomainIsTheValueConstraint` |
| `int128(decimal)` truncates, as `int()` does | v4.139.0 | DFC163 §6.5 | `expr_int128_of_decimal_truncates` |
| A window clause's columns follow the written order in every lane | v4.139.0 | DFC163 §6.5 | `window_columns_in_written_order` (and its `-presorted` form) |
| **The type × operation matrix**: every declarable type × every field-taking flag × every lane. 1,476 cells, a pre-tag gate (`SSQL_MATRIX=1`) | v4.140.0 | DFC163 §7 | itself; `TestMatrixTypesCoverTheDomain` |
| Sums and averages of non-numbers refuse. Bools are ordered false < true. json compares with eq/ne only. Times sort by instant | v4.140.0 | DFC163 §7.3 | matrix cells; `sort_time_mixed_zones` |
| lag/lead `-default` only past the partition's edge | unreleased | DFC163 §7.7 | `lag_lead_default_only_past_the_edge` (and its `_presorted` form) |
| group-by `-first`/`-last`/`-any`/`-collect` skip a missing value in every lane | unreleased | DFC163 §7.7 | `groupby_first_last_skip_missing`, `groupby_collect_skips_missing` |
| Statistics in DuckDB's arithmetic, to the bit: percentile `lo·(1−d)+hi·d`, a plain Welford. A variance of one value has none | unreleased | DFC163 §7.7 | `groupby_statistics_match_duckdb`, `window_statistics_match_duckdb`, bit-exact unit asserts |
| `resample` and `bucket()` over int128 and decimal, as values and as the axis. `resample` skips and counts a record with no time | unreleased | DFC163 §7.7 | `resample_wide_values_and_missing_time`, `resample_decimal_axis`, `decimal_set_field_and_bucket` |
| `ssql.IntoColumn` and `ssql.SetLiteral`: an `update -set` literal takes its column's type in every lane | unreleased | DFC163 §7.7 | `update_set_literal_takes_the_column_type`, `update_set_literal_into_json` |
| Missing values sort first ascending and last descending in SQL and rust, as in exec | unreleased | DFC163 §7.7 | `sort_missing_first_ascending`, `sort_missing_last_descending`, `window_order_and_limit_last_missing` |

## 3. Bugs fixed

Each one is pinned by a test that was watched failing without its fix.

### 3.1 Found by the survey and by building inet (DFC162)

- **A Parquet struct column silently shifted or dropped every later
  column.** The record and typed readers passed field indices where
  pqarrow wants leaf indices.
- **A Parquet UUID read as base64.**
- **The seven type gaps (DFC163 §1):**
  - `from jsonl -type` silently left decimals and times as text;
  - `update -set-expr 'decimal(x)'` kept the old header type;
  - typed `top` on a decimal or a time did not compile;
  - record codegen's setter table had no int128 or decimal entry;
  - typed `from ssh` gave a time field `""`;
  - `sort -spill` refused wide numbers;
  - `generate sql`'s `rename` dropped a column's kind.

### 3.2 Found by the lint's audit (DFC163 §6.4, §6.6)

There were twelve. The ones a user would see:
- typed `string-agg` on a decimal or inet did not compile;
- DuckDB window results over inet printed INET structs;
- `-lag-default` with an address was an error;
- typed `cast` of "abc" to decimal was a silent 0;
- `from jsonl -type ts string` wrote Go's `Time.String()`.

The rest were library paths and messages: `StableKey`,
`WriteJSONPretty`, `GetOr` to a bool, `TableFieldsFromRecords`, the chart,
and serve's type names.

The lanes then found two more while gating the lint:
- Postgres `string_agg` of an inet added `/32`;
- `generate rust` read a JSONL `_schema` line as a row of nulls.

### 3.3 Found by the matrix (DFC163 §7.4, §7.7)

**In v4.140.0:**
- exec sorted times in different zones by their text (this shipped in
  v4.139.0);
- an empty declared time was an error;
- sums of non-numbers gave a silent 0;
- bools could not be ordered in exec's extremes or in typed `sort`,
  `top`, `lt` and `window -order`;
- json ordered by its text;
- typed json results became strings;
- `update -set j [1]` stored text.

**In the unreleased batch:**
- **SQL:**
  - inet results of `-mode`, `-arg-*`, `unpivot` and `update -set`
    printed INET structs;
  - `string-agg` and `-collect` of an inet or a decimal used the
    engine's text;
  - a float `-set` literal was written as text;
  - `describe` called a decimal "float".
- **`describe`'s int128 max** went through float64 and came out as
  −2^63.
- **`window -min`/`-max`** adopted a missing value.
- **Record codegen** ignored the column's type for a `-set` literal.
- **`extract`:** its error lacked `Error:`, and typed code refused a
  non-text field.
- **Typed `resample -time i -value i`** did not compile. It is now
  refused in every lane.
- **`generate rust`** read a declared time as text.

**Found while building the batch:**
- SQL `bucket()` put a negative epoch in the bucket above (−3 s went to
  0, not −86400);
- SQL resample rounded a float or decimal axis (30.25 s became 30);
- `-arg-max`/`-arg-min` on Postgres and DataFusion picked a row with no
  BY value;
- `-set-field` of a decimal wrote a string header;
- the optimiser pruned the column that decides a `-set` literal's type.

## 4. What the week taught

- **Instruments beat lists.** The census in DFC155 and DFC162 was a
  document checked by hand. The lint makes a new type's work list
  mechanical: a rehearsed `uuid` named 52 switches in 28 files. The
  matrix found the behaviour no switch shows.
- **A matrix reaches only what its cells express.** Against v4.137.2 it
  caught just one of DFC163 §1's seven bugs (§7.6). The others need a
  value-changing expression, two stages, a reader's `-type`, or a mode
  flag.
- **"Byte-exact with the engines" has to name an engine.** DuckDB,
  Postgres and DataFusion disagree with each other in the last digit of
  a variance or percentile. ssql now follows DuckDB, the oracle every run
  has. The typed lane folds a group's values once, because a merge of
  shard states rounds differently.
- **The harness hides things too.** A declared json column has no SQL
  translation, so the matrix's first run had no DuckDB lane. A hand-kept
  list of decimal-producing flags hid `-set-field`. An empty text cell is
  `""` in ssql and NULL in SQL by design. Each of these was a silent gap
  until someone noticed it.
- **A watch must end on the process's exit.** A pre-tag launch died on
  "Permission denied", and a monitor matching only success lines
  reported "no events" for 17 minutes (v4.139.0).

## 5. What remains

### 5.1 Decisions for Ross

1. **The text of a time.** exec keeps each value's source offset
   (`-05:00`), but a TIMESTAMPTZ keeps only the instant. So
   `-string-agg`, `-collect` and `cast -type t string` of a time differ
   from every SQL lane. The options:
   - exec writes a time as text in UTC;
   - exec writes it in the pipeline zone;
   - the SQL lanes refuse a time used as text.

   This decision is 6 of the matrix's 7 known cells.
2. **Order after a parallel typed segment.** A record-path stage after an
   unordered `Serial()` sees rows in shard order, not file order.
   `group-by -collect` has no typed form, so it shows this. The options:
   - a typed `-collect`, with a shard-ordered merge as first/last have;
   - an order-preserving `Serial()`, which runs the shards one after
     another.

### 5.2 Built-on-the-way follow-ups (TODO)

- **Matrix reach** (DFC163 §7.6):
  - a per-type value-changing `-set-expr`;
  - two-stage cells;
  - source cells over every reader's `-type`;
  - mode flags such as `sort -spill`.
- **Matrix harness:**
  - the type-survives oracle reads only exec's header;
  - `top` ties are arbitrary (the 7th known cell);
  - json cells have no SQL lane;
  - the resample time cells test only the refusal.
- **Declared types in DFC133's random differential** (DFC163 §2.3).
- **Rust:** a declared time in a pipeline zone other than UTC is refused.
- **inet:**
  - `in` with an inet, int128 or decimal is always false;
  - Parquet/Arrow `ssql.type=inet` metadata;
  - `-param p inet V`;
  - `from json -type`;
  - a codelab paragraph.
- **UUID:**
  - `to parquet` writing the UUID logical type;
  - `uuid5` on Postgres with `uuid-ossp`.
- **The full cmd/ssql suite slowed from about 450 s to about 1,350 s**
  around v4.138.0 and has not been investigated. It is now in TODO.

### 5.3 Release

536a34e8 is on `main` and not released. The next minor release carries
§2's unreleased rows and §3.3's second list. Its matrix gate expects
exactly the 7 known cells.

## 6. Related

- [DFC162](./dfc162_uuid_and_ip_address_types.md): the UUID and IP
  survey, and the inet build (§8, §9).
- [DFC163](./dfc163_type_operation_matrix_and_domain_lint.md): the lint
  (§6) and the matrix (§7).
- [DFC155](./dfc155_big_numbers.md): int128 and decimal, whose census
  the lint replaces.
- [DFC150](./dfc150_view_only_week_retrospective.md): the previous
  retrospective of this shape.
