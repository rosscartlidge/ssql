# DuckDB's "view-only mode" post, reproduced in ssql: findings, fixes and gaps

Reference: DFC148
Created: 2026-10-12
Last modified: 2026-10-12

[Back to Index](./README.md)

Status: **five of the post's problems fixed 2026-10-11 (unreleased, after
v4.114.1); six gaps open (§4).**

## 1. The post

DuckDB's blog post of 2026-10-07, "A DuckDB Database with No Data in It"
(https://duckdb.org/2026/10/07/view-only-mode.html), shows a database file
that holds only views over remote Parquet. Because the file stores no data,
it is tiny and can be shared as a single URL. Whoever attaches it queries
the views as if they were tables, and the data always comes from the remote
files. The post builds this up in three steps:

1. A view over one remote file:
   `https://blobs.duckdb.org/train_services.parquet` (1.6 MB, 380,959 rows,
   4 row groups × 8 columns). A consumer query ranks stations by number of
   calls. The answer is Utrecht Centraal 7663, Amsterdam Centraal 7591,
   Zwolle 5013.
2. The data is re-laid out as partitioned files and the view is redefined
   over a glob (`read_parquet('…/type=*/*.parquet')`). Consumers do not
   change.
3. A column is renamed in the new layout, and the view maps the new name
   back to the old one, so consumers still do not change.

The point is that a view is a stable interface over data whose location,
layout and column names can all change.

## 2. The ssql equivalent

In ssql the view is a **run document** (DFC134): a JSON pipeline that
`ssql run` executes. The equivalent of step 1 is 71 bytes:

```json
[["from","parquet","https://blobs.duckdb.org/train_services.parquet"]]
```

```bash
ssql run services.json | ssql group-by station_name -count calls \
  | ssql sort -desc calls | ssql limit 3
```

This gives the post's exact answer. Step 2 replaces the source with a glob
(`["from","parquet","part/type=*/*.parquet"]`), and step 3 adds a
`["rename","-as","new","old"]` stage. All three versions answer the same
consumer query identically. `generate sql` through `run` emits DuckDB SQL
for the document, including DuckDB's own `RENAME` for step 3.

Running the post step by step found five problems in ssql (§3). All five are
fixed. It also left six gaps (§4).

## 3. Problems found and fixed

### 3.1 Remote Parquet was six times slower than DuckDB

**Found.** Step 1 took 13.5 s in ssql and 2.3 s in DuckDB. Downloading with
curl and reading the local copy took 1.2 s. ssql issued 34 HTTP Range
requests, which is not too many. But from Australia each one costs about
350 ms, and arrow issues them one at a time.

DFC112 had closed readahead as a negative result, measured on a low-latency
link. There, the request count was the only lever, and it was already right.
At high latency the lever is concurrency.

**Fix.** `HTTPFile.Prefetch` (`http_source.go`) fetches all the needed
column chunks before arrow reads anything:

- `parquetChunkRanges` (`parquet.go`) works out each selected column chunk's
  byte range from the footer, the same way arrow's RowGroupReader does
  (dictionary page first, plus PARQUET-816 padding).
- Ranges that are adjacent are merged, up to 4 MB.
- The merged ranges are fetched 16 at a time.
- `ReadAt` serves any read that falls inside a fetched range from memory.
  A read outside every range still goes to the network, so the result is
  correct even if the computed ranges are wrong.

**Result.** 3 requests and 2.4 s, byte-identical to the local read. The cost
is peak memory: the compressed bytes are held until the row groups have been
decoded.

**Gate.** `TestParquetHTTPConcurrentChunks` (`readahead_test.go`) runs
against a server that adds a delay to each request and counts the peak
number of requests in flight. It checks three things:

- With the merge limit lowered, more than one request is in flight at a
  time. This failed at a peak of 1 before the fix.
- At the default limit, adjacent chunks are merged. This failed at 26
  requests when merging was bypassed.
- The output equals the local read, and pruning to one column fetches only
  that column's chunks.

A request count alone cannot tell serial fetching from concurrent fetching,
which is why the merge limit is a variable the test can lower.
`TestCoalesceRanges` (`http_source_test.go`) covers the merging itself.

### 3.2 `generate sql` emitted SQL with no FROM clause, and exited 0

**Found.** Under `SSQL_MODE=record`, a run document with a failing stage
still let `generate sql` print a SELECT with no FROM clause and exit 0.

The bug was not specific to `run`. In generate mode, any stage the grammar
refuses exits before its handler can write an error fragment, so it leaves
an empty fragment stream. Every command reads "no upstream fragments" as
"I am the first stage". The next stage therefore started a new chain, and
`generate sql`, `generate ssql` and `generate json` assembled what was left.
`generate go` alone refused it ("no source (init) fragment").

**Fix.** There are two defences:

- `commands.ReportGenerateFailure` in main's error path writes an error
  fragment for any failure in generate mode. A stage that receives an error
  fragment passes it on unchanged and stays quiet, using the typed
  `lib.UpstreamFailure`. The user sees the failing stage's own error, plus
  one line from the generator naming that stage. The first version
  re-wrapped the error at every stage, so one typo produced four errors.
- `generate sql` refuses a chain whose first stage is not `from`. This
  covers a stage that crashed without writing anything.

`generate json` used to print an empty `Code` for a failure; it now uses the
same type.

**Gate.** `TestGenerateStageFailureIsLoud` covers all four generators, a
shell pipe, a run document and a chain with no source. The first draft of
this gate ran bash with `pipefail`. That made the pipe fail at the failing
stage, which hid the exit 0 the gate was written to catch. Without
`pipefail`, the status is the generator's own, and on the old code the gate
reports `generate sql/ssql/json exited 0`.

### 3.3 No multi-file or glob Parquet (the post's step 2)

**Found.** `from parquet` took one file and no glob, and a quoted glob
reached it unexpanded. Bare `from` refused several files, and its error
suggested `from csv` for Parquet files.

**Fix.** The FILE argument of `from parquet` is now variadic and accepts
quoted globs. Globs are expanded by `ssql.ExpandFilePatterns` when the
pipeline runs, so a saved document picks up new files the way DuckDB's
`read_parquet` glob does. The rules:

- **All files must share one schema exactly**: the same column names, in
  the same order, with the same arrow types (`CheckParquetSchemas`). DuckDB
  binds a file list to the first file's schema, and typed mode decodes into
  a struct built from the first file. Without this rule, records would vary
  from file to file.
- **A glob that matches nothing is an error**, and so is `**`. Go's matcher
  treats `**` as `*`, which is silently shallower than DuckDB's recursive
  `**`.

Each backend got its own support:

| Lane | Support |
|---|---|
| exec | `ReadParquetFiles` |
| record codegen | a single plain file keeps its `-input` parameter; lists and patterns are written into the program as given |
| typed | `ReadParquetFiles`; `ReadParquetFilesParallel` shards over (file, row group) units, and `ReadParquetParallel` is now the one-file case of it |
| SQL | `read_parquet([…], hive_partitioning=false)` on DuckDB; other dialects refuse |
| bare `from` | routes Parquet lists and globs to `from parquet` |

The new equivalence cases found four bugs that predate this work:

- **The optimiser dropped columns in every compiled lane.**
  `from parquet F | sort id` was pruned to `-columns id`, because
  `collectDownstreamFields` collected the referenced fields without asking
  whether any stage narrows the record. Pass-through stages (sort, where,
  limit, to) send every column to the output. Pruning is now done only when
  an `include` or `group-by` is reached. Exec was right, so nobody comparing
  against exec would have seen the bug. `generate ssql`, ⚙ Optimise and
  every `generate go` lane printed one column.
- **DuckDB added a hive column nobody asked for.** On a single file under
  `region=north/`, DuckDB's hive auto-detection added a `region` column.
  ssql now passes `hive_partitioning=false`.
- **`-columns` order differed between lanes, and SQL misread `-columns`.**
  The record reader returned `-columns` in file order, while typed, schema
  mode and SQL used the order given. The SQL translator read `-columns a`
  as a FILE and emitted `read_csv`. `-columns` now keeps the given order in
  every lane, and an unknown column is an error that lists the available
  columns.
- **Typed Parquet reads swallowed every error.** A missing file gave an
  empty table and exit 0. Typed reads now fail loudly, as `ReadCSV` does.

**Gates.** The equivalence cases `parquet_glob_groupby`,
`parquet_list_columns`, `parquet_bare_glob` and `parquet_hive_dir_single`;
`TestExpandFilePatterns`; `TestReadParquetFiles`; typed `TestParquetFiles`.

Making FILE variadic broke Tab completion of `-columns`. autocli read the
source flag only as a string. This was fixed in autocli v4.21.1. autocli,
shell and ssh then gained a changelog, a version constant and a
version-matches-changelog test (autocli v4.21.2, shell v0.5.1, ssh v0.1.15).

### 3.4 `run` reported a downstream `limit` as an error

**Found.** `ssql run doc | ssql limit 3` printed
`Error: run: stage 1 (…): signal: broken pipe` and exited 1. The equivalent
shell pipeline is silent and exits 141. Because of the exit status, a
script using `pipefail` treated a successful query as a failure.

`waitNamed` (`pipeline_doc.go`) already excused a stage killed by SIGPIPE,
but only an intermediate stage. The last stage writes to run's own stdout,
so when the shell's reader exits, the last stage is the one that dies.

**Fix.** `run` now ends the way its last stage would have:

- `waitNamed` returns `errStdoutClosed`.
- `exitBrokenPipe` writes once to the closed stdout, so the Go runtime kills
  the process with SIGPIPE. It exits 141 if that does not happen.

The fix also exposed a second bug. Both wait loops (`waitNamed`, and serve's
`stageChain.wait`) excused death by **any** signal, so a stage killed by the
OOM killer passed silently. `killedByBrokenPipe` now checks for SIGPIPE
specifically. The check is build-tagged, because Windows and js/wasm have no
SIGPIPE, so nothing is excused there. The first version broke the Windows
and js builds; cross-building every target caught it.

**Gates.** `TestRunEarlyReaderExit` runs one- and two-stage documents and
checks that each matches the direct shell form's status with empty stderr.
It also checks that a failing document still reports its error. On the old
code it failed with status 1 and the error. `TestKilledByBrokenPipe` checks
that PIPE is excused and that KILL, TERM and exit 3 are not.

### 3.5 The rename mapping (the post's step 3)

This needed no fix. A `rename -as` stage plays the mapping view, and
`generate sql` emits DuckDB's `RENAME`. It is listed here because it is the
step most likely to break if `rename`'s grammar changes.

## 4. Still to fix

In rough order of value against effort.

### 4.1 `ssql run URL` is refused

The post's whole idea is sharing **one URL**. `ssql run
https://…/services.json` fails with "open: no such file". The workaround is
`curl -s URL | ssql run -stdin`.

**Proposal.** `run` fetches `http(s)://` documents itself, using the same
HTTP client as the Parquet reader.

Two decisions are needed. First, a remote document runs commands on the
caller's machine, so it needs the same trust model as a local one: it is
documented as code, not data. `run -print URL` lets the user look before
running it. Second, a timeout and a size cap.

Effort: small.

### 4.2 Hive partition columns are not derived

`from parquet 'part/type=*/*.parquet'` now reads all the files, but it does
not add `type` as a column, as DuckDB's `hive_partitioning` does. The post
needs this only if a consumer filters or groups on the partition key. That
is the common reason to partition, so it matters.

**Proposal.** A `-hive` flag on `from parquet`, also accepted by bare
`from`. It parses `key=value` directory segments into string columns,
appended after the file's columns. It needs to work in every lane:

- exec: the reader.
- record and typed: the generated reader call. In typed mode the struct
  gains the fields.
- SQL: `hive_partitioning=true` on DuckDB; other dialects refuse.

The rules need settling:

- Every file must have the same keys.
- Partition values are strings, unless DuckDB's type inference is to be
  matched. Matching it would need a DuckDB probe first; this is the
  semantics most likely to differ between lanes.
- A partition key that clashes with a column name is an error.

The equivalence harness must guard it, because `parquet_hive_dir_single`
has already shown DuckDB's auto-detection diverging.

Effort: medium.

### 4.3 `s3://` is not a source

ssql reads only `https://` URLs, including presigned S3 URLs; this was
DFC112's choice. The post's examples use `s3://`. A public bucket can be
read over its https endpoint, but the user has to rewrite the URL.

**Rewriting by hand (works today).** The rewrite turns `s3://BUCKET/KEY`
into `https://BUCKET.s3.amazonaws.com/KEY`. This is the virtual-hosted
style; S3 redirects to the bucket's region. The path-style
`https://s3.amazonaws.com/BUCKET/KEY` also works for us-east-1 buckets.
The pinned form is `https://BUCKET.s3.REGION.amazonaws.com/KEY`; the
region is in the `x-amz-bucket-region` header of a `HEAD` on the bucket
root. Keys are used as they are, apart from URL-escaping: `=` and `/` pass
through, and a space becomes `%20` or `+`.

The example is NOAA's public GHCN-Daily bucket (us-east-1). This was
checked on 2026-10-12 from Australia, and a single-file read took 2.2 s.

```bash
# s3://noaa-ghcn-pds/parquet/by_station/STATION=ACW00011604/ELEMENT=PGTM/1319b53f4d184b32aa3415f9beaf7cd2_0.snappy.parquet
# becomes
ssql from parquet 'https://noaa-ghcn-pds.s3.amazonaws.com/parquet/by_station/STATION=ACW00011604/ELEMENT=PGTM/1319b53f4d184b32aa3415f9beaf7cd2_0.snappy.parquet' \
  | ssql limit 3 | ssql to table
```

```
ID            DATE       DATA_VALUE   M_FLAG   Q_FLAG   S_FLAG   OBS_TIME
-------------------------------------------------------------------------
ACW00011604   19490201         1300            (null)   X        (null)
ACW00011604   19490210          624            (null)   X        (null)
ACW00011604   19490215          724            (null)   X        (null)
```

The same bucket shows the limits of the rewrite:

- **No globs over https.** `ExpandFilePatterns` passes a URL through
  unexpanded, so `…/STATION=*/ELEMENT=PGTM/*.parquet` cannot be read.
  Expanding it needs a ListObjects call, which is what S3 offers for
  directories. A public bucket answers that without credentials:
  `https://noaa-ghcn-pds.s3.amazonaws.com/?list-type=2&prefix=parquet/by_station/`.
  So glob support for public buckets is within reach of option (a).
- **The partition keys stay in the path.** `STATION` and `ELEMENT` do not
  become columns (§4.2). Here `ID` repeats the station anyway, but
  `ELEMENT` is lost.
- **Private buckets need a presigned URL.** `aws s3 presign s3://BUCKET/KEY`
  prints an https URL that `from parquet` reads as is (DFC112).

**Proposal options:**

- (a) Rewrite only public `s3://bucket/key` URLs to the virtual-hosted
  https endpoint. No credentials, small.
- (b) Full credentialed S3 through the AWS SDK. A large dependency; listing
  a bucket for globs is the hard part.
- (c) Leave it, and document the rewrite.

Recommendation: (a) when someone needs it, covering single objects first.
Globs need ListObjects; a public bucket answers it without credentials, so
glob support can come in a second step of (a), without the SDK.

### 4.4 No named views in one file

One DuckDB file can hold many named views. One run document is one
pipeline, and `from catalog` is for shards, not views.

The possible shape is a document holding a map from names to pipelines,
used as `ssql run views.json NAME`, or a `from view NAME` source that
resolves through a views file. This is a design question about what a
"catalog of relations" means in ssql. It touches DFC134 (pipeline
documents) and DFC138 (the document on the wire), and needs a proposal
before any code.

### 4.5 `union` of Parquet sides needs process substitution

`union` reads Parquet only through `<(ssql from parquet …)`, and a run
document cannot express process substitution. A view that combines two
Parquet sources therefore cannot be a single document.

Two fixes are possible. `union`, `join` and `merge` could read `.parquet`
directly through `readAuxInput`; CLAUDE.md already lists this ("Parquet/arrow
route via procsub for now"). Or documents could gain nested sub-pipelines.
The first is small and general. For a same-schema union, multi-file
`from parquet` (§3.3) already covers most cases.

### 4.6 Pushdown happens once at generate time, not every time the view is queried

When DuckDB queries a view, it reads only the columns the query uses, every
time. ssql can push the needed columns into a run document's read, but only
in the modes where it can see the whole pipeline, and only at the moment the
pipeline is generated.

Run plainly, `run` cannot see the commands after it in the shell pipe, so it
reads every column:

```bash
ssql run services.json | ssql group-by station_name -count calls \
  | ssql sort -desc calls | ssql limit 3          # all 8 columns
```

The generate modes see the whole chain, including what is inside the
document. `generate ssql` optimises the interpreted pipeline, and checked on
2026-10-12 it emits:

```
$ (export SSQL_MODE=record; ssql run services.json | ssql group-by station_name -count calls \
    | ssql sort -desc calls | ssql limit 3) | ssql generate ssql
ssql from parquet https://blobs.duckdb.org/train_services.parquet -tz Australia/Sydney -columns station_name | ssql group-by station_name -count calls | ssql top 3 -field calls
```

It reads one column and turns `sort | limit` into `top`. `generate go`
uses the same field collection (`collectDownstreamFields`), and DuckDB
prunes the SQL lane's `read_parquet` itself; neither was timed here. On this 1.6 MB file the
optimised form took 2.1–2.3 s, against 2.5–2.7 s for the plain one. On a
large remote file, with most columns unused, the gap would be much larger,
because fewer column chunks would be fetched (§3.1).

**The gap is when the optimising happens, not whether it can.** The
generated pipeline has the document's contents copied into it: the URL, the
zone and the column list. If the document changes later (the data moves to a
glob, as in step 2, or a column is renamed, as in step 3), the generated
pipeline keeps reading the old source until someone regenerates it. That
loses the main point of a view: a stable interface over a definition that
can change. ssql currently offers two choices:

| Form | Follows the document when it changes | Reads only the columns used |
|---|---|---|
| `ssql run services.json \| …` | yes | no |
| `generate ssql` / `go` / `sql` output | no (copied in at generate time) | yes |
| DuckDB view | yes | yes |

**Proposal options:**

- (a) **`run` takes a projection.** `run services.json -columns
  station_name` resolves the document when it runs, then applies the
  optimiser's pushdown to it as if an `include` stage followed.
  `generate ssql` would emit `ssql run services.json -columns station_name |
  …`, keeping the reference instead of copying the document in. A
  `rename` inside the document means the consumer's names are not the
  file's names, so the projection must be mapped back through the
  document's stages. An equivalence case over step 3 (the rename view)
  must pin this. The generated Go and SQL lanes could
  do the same only by reading the document when the program runs, which they
  do not do today. Small for exec and `generate ssql`, medium for the rest.
- (b) **Documents include documents.** The consumer's query is itself a
  document whose first stage refers to the view
  (`[["run","services.json"],["group-by","station_name","-count","calls"], …]`).
  `run` resolves the reference, then optimises the combined chain before
  executing it. This is a view and a query composed at query time, which is
  exactly what DuckDB does. It also needs a rule against cycles. It overlaps
  with named views (§4.4), and the two should be designed together.
- (c) **Leave it, and document it.** Regenerate when the view changes.

Recommendation: (a) now, for exec and `generate ssql`, because it is the
smallest change that keeps both properties in the interpreted path. Fold (b)
into the §4.4 design.

## 5. Lessons

- **Measure at the user's latency.** DFC112's negative result was right on
  the link it was measured on. One run from Australia reversed it. A perf
  conclusion is only as general as the conditions it was measured under.
- **Equivalence cases find old bugs.** Four new cases found four bugs older
  than this work. The worst one, the optimiser dropping columns, was
  invisible from exec.
- **A gate's own harness can hide the bug.** `pipefail` in
  `TestGenerateStageFailureIsLoud`, and a request count in the concurrency
  test, each made the first draft unable to fail. Both were caught by
  watching the gate fail on the old code first.
- **Excusing more errors than intended is a bug too.** "Any signal" being
  excused looked like the broken-pipe handling. It also excused the OOM
  killer.

Ref: DFC112 (remote Parquet, readahead), DFC134 (pipeline documents),
DFC136 (DuckDB comparison), DFC138 (document on the wire).
