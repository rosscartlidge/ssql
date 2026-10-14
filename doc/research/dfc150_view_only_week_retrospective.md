# The view-only week: what landed, what it broke open, what remains

Reference: DFC150
Created: 2026-10-15
Last modified: 2026-10-15

[Back to Index](./README.md)

Status: **retrospective, 2026-10-15.** Ross: "could you write a DFC on the
recently added features and the bugs addressed and what remain." Covers
2026-10-11 to 2026-10-15: releases v4.115.0, v4.116.0 and v4.117.0, and
the unreleased tree (the DFC149 §4.4/§4.6 commits plus the TODO bug
batch, gate green, not yet committed). The design documents for the
features are [DFC148](./dfc148_duckdb_view_only_mode.md) and
[DFC149](./dfc149_hive_partitions_as_derived_catalog.md); this one is
the ledger, so a release note, John's page and the next TODO triage
have one place to start from.

## 1. Where the week came from

A friend sent Ross DuckDB's "A DuckDB Database with No Data in It"
(2026-10-07). Reproducing its three steps in ssql (DFC148) was a day's
probe that found more than it reproduced: a slow remote Parquet reader,
a `run` that reported SIGPIPE as a failure, a `generate sql` that
emitted FROM-less SQL after a failing stage, a `to parquet` that wrote
times as text, and two generated-program JSONL readers that had never
been right. Every one of those was a defect in a feature that already
existed and had passed its own tests.

The feature work that followed was driven by Ross's question about
hive partitions — "it feels to me like this is similar to some of the
features of our catalog — see if we can unify this" — which turned a
proposed `from parquet -hive` flag into `from catalog -hive` (DFC149)
and made the catalog, not a reader flag, the thing that gained a schema
mode, a local fast path and a SQL lane. The bug batch at the end came
from Ross's "lets look at the bugs in TODO and fix them before we add
any new features."

Size, from `git diff v4.114.1`: 145 files, about 7,800 lines added;
19 new equivalence cases; 37 CHANGELOG entries across the three
releases and the unreleased section.

## 2. Features landed

By release. "Gate" names the test that would fail if the feature were
removed; a feature without one is not listed here, because it would not
survive (CLAUDE.md, "If It's Not Tested, It Will Break").

### 2.1 v4.115.0 (2026-10-12)

| Feature | Design | Gate |
|---|---|---|
| `from parquet` takes several files and a glob; bare `from` routes them | DFC148 §4 (f) | `parquet_glob_groupby`, `parquet_bare_glob`, `parquet_hive_dir_single` |
| Remote Parquet reads a row group's column chunks concurrently (13.5 s → 2.4 s from Australia, 34 requests → 3) | DFC112 addendum, DFC148 (c) | `TestParquetHTTPConcurrentChunks` |
| `run` ends by SIGPIPE when a downstream stage closes early, silent, like a shell pipeline | DFC148 (d) | `TestRunEarlyReaderExit`, `TestKilledByBrokenPipe` |
| A failing stage under a codegen mode is loud; `generate sql` refuses a sourceless chain | DFC148 (e) | `TestGenerateStageFailureIsLoud` |
| `to parquet` and `to arrow` type every column from all values and the header; times are Timestamp(µs, UTC), the same file typed writes | journal 2026-10-12 | `TestToParquetTypesAllLanes`, `TestWriteColumnTypes` |
| Generated JSONL readers honour the header's types (record) and refuse an absent value (typed, `typed.RequireValues()`) | DFC124 §3, DFC128 | `TestJSONLAbsentValueIsLoudInTyped`, `TestJSONLRequireValues` |

### 2.2 v4.116.0 (2026-10-14)

| Feature | Design | Gate |
|---|---|---|
| `from catalog -hive`: `key=value` path segments become per-file string metadata; a glob may stand where FILE goes; `ssql.LoadCatalog` is the one way a catalog becomes entries | DFC149 §4.1, §4.2 | `catalog_hive_glob`, `catalog_hive_where`, `TestCatalogHive` |
| The optimiser learns derived columns, so a `where` on a hive key becomes `-if` and one file opens | DFC149 §4.6 (generate ssql) | `catalog_hive_where`, `TestSchemaModeCatalog` |
| Schema mode for `from catalog` and `from ssh`: the first pruned shard's schema plus the metadata columns as strings | DFC149 §4.5 | `TestSchemaModeCatalog`, `TestSchemaModeFromSSH` (rig) |
| `to lines FIELD`, the twin of `from lines`; named views are a table (`from csv URL \| where \| to lines pipeline \| run -stdin`) | DFC148 §4.4 (Ross's design) | `TestToLinesAllLanes`, `TestWriteLinesToWriter`, typed `TestWriteLines` |
| The Catalogs and Views codelab (`doc/cli-codelab-catalog.md`), 24 blocks under `make doc-test` | — | `TestCodelabRuns` |
| Every generated main calls `ssql.MarkGeneratedProgram()`; `SelfBin` matches the name exactly | journal 2026-10-14 (the fork bomb) | `TestSelfBinFor`, `TestCatalogLocalShardsRunThisBinary` under the name `ssqlgen` |

### 2.3 v4.117.0 (2026-10-14)

| Feature | Design | Gate |
|---|---|---|
| `run` applies the optimiser's rules to the document before executing it (`-optimise` default, `+O`, `-explain`); the shell form and the document form of an optimised stage come from one `cmdWords` | DFC148 §4.6 (for `run`) | `TestRunDocument` subtest, the hive document case in `TestCatalogHive` |

The tuesday view over the rail layout: 3.0 s as written, 0.57 s
optimised.

### 2.4 Unreleased (committed 76955c3, 9baac3f; and the bug batch)

| Feature | Design | Gate |
|---|---|---|
| `from catalog` reads a local Parquet shard in-process, per shard, in exec and generated programs (`catalog_local.go`); a missing shard is the command's error | DFC149 §4.4 | `TestCatalogLocalParquetFastPath`, scale case `catalog-hive-local-fast-path` |
| `generate sql` translates `from catalog` when every pruned shard is local Parquet: pruned at generate time, hive keys by DuckDB's `hive_partitioning`, other metadata as per-file literals (`translateCatalogSource`, DuckDB only) | DFC149 §4.6 | `TestTranslateCatalogSQL`; the hive equivalence cases run their DuckDB lane |
| `count` has a SQL translation | TODO | `TestTranslateCountSQL` |
| `ssql.RequireFields`, `MissingFields`, `UnknownFieldsError` in the library; `correlate`'s `CorrelationRecords`/`LagRecords` | §3.2 below | `require_fields_test.go`, `TestCorrelateFilterLag` |

Measured on the seven rail files (381k rows): exec 3.0 s → 0.93 s with
the fast path; the hive view 0.04 s under `generate sql -run`. On the
six 500k-row scale partitions: 8.9 s → 2.2 s against 1.9 s for a plain
glob read.

## 3. Bugs addressed

Grouped by how each was found, since that is what the next bug hunt
should learn from. "Live since" is how long it shipped.

### 3.1 Found by giving a feature a gate in another lane

These are the DFC133 class: a feature was correct where its author
looked and wrong in a lane nobody ran.

| Bug | Lanes | Live since | Found by |
|---|---|---|---|
| The aggregation pushdown deleted the parent `group-by`, so a key spanning shards came out once per shard | generate go, generate ssql, `run` since v4.117.0 | the rule shipped; never had a multi-shard fixture | the hive layout under the optimiser: the shard's empty `dept` hit the hive-clash check |
| `from catalog -hive GLOB` attached the derived key twice to every record | exec, generated | 76955c3 (hours) | the SQL lane's literal form: `'north' AS region, 'north' AS region` |
| A catalog's metadata columns came in a random order per record; `-catalog-used`'s header shuffled per run | exec | the enricher ranged a map | noticed writing schema mode; fixed with the fast path's per-file schema |
| Generated `to parquet`, `to arrow`, `to wav` had never compiled (undefined `ctx`); typed `to parquet` emitted only the Stream form | record, typed | since the sinks' codegen | the all-lanes `to parquet` test |
| Record `ReadJSONAuto` dropped the header's `time` type; typed JSONL decoded an absent key as a zero | record, typed | since the readers | the same test's record lane |
| `generate go -run` on a catalog fork-bombed the machine (`ssqlgen` matched `SelfBin`'s `ssql*` rule) | generated programs | v4.93.0 | running the record lane by hand (the harness names programs `prog`) |
| A generated program printed a shard error and exited 0 | record | since DFC059 | the hive clash test |
| The generated record filter for `-if-expr` returned false on every error, dropping bad rows silently | record | since the transpiler | the null-date probe's record lane |
| `where -if-expr`'s nil result was `""` in exec and the text `<nil>` in record programs | exec, record | since expressions | the same probe |
| The typed lane inferred an RFC 3339 CSV column as a time; exec and SQL kept a string | typed | since typed inference | `-param since time` (2026-09-22), fixed now by the string rule |

### 3.2 Found by a person using it

| Bug | Lanes | Found by |
|---|---|---|
| `to parquet` wrote times as Go's `Time.String()`, typed a column from its first record, left a 0-byte file on empty input | exec | recording output for John's page; DuckDB's `DESCRIBE` said VARCHAR |
| A null date broke the rows after it: `date(nil)` errored, `update` thinned the output instead of stopping, `where -if-expr` printed and exited 0 | exec | Ross: "a nil date seem to break later exprs" |
| A column named `month` (a hive key) failed to compile in an expression: the shadowing rule checked only expr-lang's own builtins in the bare-identifier case | exec, record, typed | writing the codelab's `update -set-expr` on a hive column |
| `run` applied no optimiser pass, so a `where` inside a view read every file | `run` | measuring for John's page: 3.1 s vs 0.68 s |
| The installed binary was stale during a measurement; every `generate sql` printed the old refusal in 0.02 s | — | the numbers were too good; CLAUDE.md's "rebuild first" rule exists for this |

### 3.3 The TODO batch (2026-10-14, unreleased)

Triage of every open TODO item that read as a defect, each reproduced
against the current build first: nine fixed, two closed as already
fixed, one needing a decision, four deferred (§5.3).

| Item | Fix | Gate |
|---|---|---|
| Record programs checked no fields: `where -if nosuch eq 1` filtered everything, `sort nosuch` passed rows, `group-by nosuch` grouped on an empty column, all exit 0 (TODO 89, generalised from "group-by") | `ssql.RequireFields(stage, fields...)` chained in front of each record stage's filter (`checkedFilter`, 22 commands), exec's wording through `ssql.UnknownFieldsError`; samples up to 100 records as exec's schema inference does; applied only after schema-preserving stages (`recordCheckSound`) | `TestRecordCodegenUnknownFieldIsLoud` (a bad and a good invocation per stage) |
| `group-by -sum age ''` produced no aggregate; `-count ''` a column named `""` (161) | `validateAggSpecs` refuses an empty result or field; the incomplete spec is no longer dropped before validation | `TestParseGroupBySpecs_IncompleteDropped` |
| `count` had no SQL form (32) | `translateCount`: `COUNT(*) AS count` over the query so far | `TestTranslateCountSQL` |
| `window -order F \| limit N` carried no ORDER BY in SQL (309) | the last clause's partition and order become the query's ORDER BY | `window_order_limit` (Ordered) |
| A JSON array file's columns came out sorted (164) | both array readers (`lib.readJSONArray`, `ssql.ReadJSONAuto`) hand each element to `ParseJSONLineWithNulls` | `json_array_column_order` (column order vs DuckDB's `read_json`) |
| Typed inferred RFC 3339 as `time.Time` (151) | the string rule: `colInfer.allTime` removed; `-type F time` is the explicit form every lane honours | `csv_iso_time_stays_string` (the `+11:00` row discriminates lexical from chronological) |
| `correlate` full mode had no `lag` (133) | `ssql.CorrelationRecords`/`LagRecords`, one place for the library filters and exec | `TestCorrelateFilterLag` |
| `union -help` said extra files must be JSONL; `DescribeConfig`'s comment said first-seen order | text | — |
| `test-ai-prompts.sh` wrote empty fix requests: `|||` as a separator under IFS | one unit-separator byte; two dead scripts removed | — |
| `-if-expr` on an absent operand printed per row (162); `contains` docs (46) | closed, already right | — |

## 4. What the week taught

Most of these are in the journal; the ones worth keeping are here.

1. **A lane's own gate finds the other lanes' bugs.** Four of the
   §3.1 rows were found by building the SQL lane or the all-lanes test
   for something else. The duplicate hive key is the sharpest case: it
   was invisible to every JSON reader (last duplicate wins) and to the
   equivalence gate's column comparison (which deduplicated), and
   visible the moment a SQL `SELECT` listed the columns. The gate now
   fails a lane on a duplicate key (`equivColumnOrder` returns it);
   something should still assert the raw `_schema` header's fields are
   distinct.

2. **A rule without a multi-shard fixture is untested.** The pushdown
   rule was wrong for every catalog with more than one shard per key
   from the day it shipped, and every fixture that existed had one
   shard per key. The codelab's hand-written two-phase aggregate was
   right, which hid that the rule was not. When a rule rewrites an
   aggregate, the fixture must have a key that spans the partition.

3. **A runtime field check in a program with no schema is sound only
   while the stream's field set is the source's.** The first
   `RequireFields` decided on the first record everywhere and failed
   five equivalence cases and a codelab: after a left join or a
   conditional `update -set` a field is legitimately absent from early
   records. The shipped form samples 100 records and codegen applies it
   only after `where`, `sort`, `top`, `limit`, `offset`, `distinct`,
   `sample`, `cast`, `fill` and `tee`. A corpus case was found relying
   on the old silence (`fill -default` on a missing column).

4. **Generated statements have a shape.** `lib.extractFilter` splits a
   stmt at its last `)(` to find the input variable, so a check must be
   chained inside the filter (`ssql.Chain(RequireFields, Where)(in)`),
   not wrapped around the input. The first attempt broke every program.

5. **The tree must not change while a gate runs.** Eight spurious
   failures (`TestScriptFlag_*`, `TestRunKeybinding*`, `TestSchemaMode*`)
   from editing `generate_ssql.go` while the full package built
   `/tmp/ssql_test`. Rebuild the installed binary before any
   measurement, for the same reason.

6. **Name a generated program something other than `ssql*` when
   probing, or let the program declare itself.** The fork bomb lived
   from v4.93.0 because the harness never used the name the user did.
   `MarkGeneratedProgram()` is the durable fix; the test now runs under
   the user's name too.

7. **The typed lane over a catalog buys nothing today.** Measured
   before the SQL lane: a generated program over a catalog is no
   faster than the shell pipeline and about three times slower than the
   typed program over the same files as a glob, because the catalog
   source compiles the whole program in record form. That is why §5.1
   leaves the typed lane unbuilt until a library user asks.

## 5. What remains

### 5.1 DFC149, the catalog arc

- **The typed lane** (§4.6): the struct is the first shard's schema
  plus the keys as strings; a layout that adds a key at run time is a
  loud mismatch. Deferred on the measurement in §4.7; the first
  library-mode request for a fast catalog program reopens it.
- **Ranges from paths** (§4.7): `month=5` vs `month=10` compares as a
  string, as DuckDB does with autocast off. Noted, not solved.
- **Percent-decoding of values** (§7): one `COPY … PARTITION_BY` probe
  before committing to a rule.
- **`-shard-order`**: the fast path reads in catalog order; a `limit`
  without a sort differs between lanes as for every multi-file source.
- **A gate on the raw header** (§4 item 1): assert `_schema` fields are
  distinct, independent of every parser.

### 5.2 DFC148, the post's gaps

- **`s3://` is not a source** (§4.3): presigned `https://` only, DFC112's
  choice; the rewrite to the virtual-hosted URL works by hand.
- **`union` of Parquet sides needs process substitution** (§4.5), which
  a run document cannot express.
- **`run URL`** is a convenience now, not a gap (the views table reads
  the URL through `from`): the Parquet reader's HTTP client, a `-print`
  look-before-run, a timeout and a size cap, if anyone asks.
- **A generated program keeps the view it was generated from** (§4.6):
  inherent to generation; `run` re-derives on every run and that is
  the documented form.
- **John's page** (artifact 6Pvvt8Mc9xahi99C2Ki7n3, v4) still lists
  "no SQL form for a hive view"; update when v4.118.0 ships.

### 5.3 The TODO's open defects

- **ssql trims CSV cells; DuckDB does not** (153): a one-space cell is
  `""` here and `" "` there, so `where -if s eq ' '` differs between
  the exec and SQL lanes. Ross's decision. The recommendation is to
  keep the trimming (padded CSVs are common; `trim()` on every text
  column would slow the SQL lane for a case nobody has hit) and record
  it beside the float-to-int truncation note (160) in the codelab's
  cast paragraph, closing both.
- **The argv readers that classify by a leading dash** (154–157): a
  positional beginning with `-`/`+` is refused by `generate sql`, a flag
  argument spelled `-generate` is dropped from the fragment, UIs emit
  bare positionals, and `parseCmdFields`/`serve_http.go` still read
  argv by hand. One root cause: the fragment carries argv, not parsed
  positionals. The fix is DFC123's `Op` with classified arguments, not
  four patches.
- **`from -skip-invalid` has no generated form** (159): generation
  refuses it rather than emit a strict program for a lenient request;
  a feature gap, not a defect.
- **`update`/`window` with an empty result name** say "no -set
  operations specified", because autocli drops the empty argument. They
  refuse, which is the requirement; the message misleads.

### 5.4 Release

The unreleased tree holds the two DFC149 commits and the bug batch
(full `cmd/ssql` gate green on the exact tree, 988 s; vet and every
package clean; `make doc-check` 45/45). The batch is not committed. The
release is v4.118.0: the pre-tag gates (scale, permutation triples,
`make doc-test`) have not yet run on this tree.

## Prior art / related

- [DFC148](./dfc148_duckdb_view_only_mode.md): the post, its
  reproduction, the fixes of 2026-10-11 and the gaps.
- [DFC149](./dfc149_hive_partitions_as_derived_catalog.md): hive
  partitions as a derived catalog; §4.4 and §4.6 built this week.
- [DFC133](./dfc133_finding_unknown_bugs.md): the classification of
  bugs by what finds them, which §3 reuses.
- [DFC142](./dfc142_panics_as_error_channel.md): why `RequireFields`
  panics and `ssql.Run` reports it.
- [DFC123](./dfc123_pipeline_ir.md): where the argv-reader items go.
- [DFC134](./dfc134_pipelines_as_data.md): run documents, which the
  views table and `run -stdin` build on.
- Journal `journal/2026-W41.md` (2026-10-11) and `journal/2026-W42.md`
  (2026-10-12 to 14): the day-by-day record each row above points to.
