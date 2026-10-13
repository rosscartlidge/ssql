# Hive Partitions Are a Derived Catalog

Reference: DFC149
Created: 2026-10-13
Last modified: 2026-10-13

[Back to Index](./README.md)

**Status:** Proposal, 2026-10-13. Replaces the `-hive` flag on `from
parquet` proposed in [DFC148 §4.2](./dfc148_duckdb_view_only_mode.md).
Ross: "it feels to me like this is similar to some of the features of
our catalog — see if we can unify this."

## 1. The finding

DFC148 §4.2 proposed `from parquet 'day=*/*.parquet' -hive`: derive a
column from each `key=value` directory segment, as DuckDB's
`hive_partitioning` does. Before building it, the same layout was tried
through `from catalog` ([DFC054](./distributed-shard-catalog.md)) with a
hand-written catalog over the files from the DFC148 walk-through:

```csv
host,path,day
local,…/rail/services/day=2023-05-15/data.parquet,2023-05-15
local,…/rail/services/day=2023-05-16/data.parquet,2023-05-16
local,…/rail/services/day=2023-05-17/data.parquet,2023-05-17
```

```bash
ssql from catalog cat.csv -if day eq 2023-05-16 | ssql group-by day -count n | ssql to table
# day          n
# 2023-05-16   60409        (0.47 s: one file opened)
```

Three things the `-hive` design would have had to build already exist:

1. **The partition column is attached to every record**, as a string —
   the type rule DFC148 §4.2 chose after a DuckDB probe showed its own
   inference is uneven (`year=2023` → BIGINT, `day=2023-05-15` → DATE,
   `n=1.5` → VARCHAR).
2. **`-if day eq` prunes before any file is opened.** DFC148 §4.2 called
   partition pruning a follow-up; the catalog has had it since v4.27.
3. **The optimiser already extracts the predicate.** `from catalog
   cat.csv | where -if day eq 2023-05-16 | count` through `generate
   ssql` becomes `from catalog cat.csv -if day eq 2023-05-16 | count`
   (`ruleCatalogPredicateExtraction`, which distinguishes exact columns,
   whose row filter can be dropped, from `_from`/`_to` ranges, whose
   row filter must stay). A consumer of a view writes an ordinary
   `where` and gets pruning.

The catalog also has what hive does not: range columns, hosts, a
per-row `bin`, pushdown of stages to each shard, `-shard-field`
provenance, `-keep-going`, `-catalog-used`. Hive partitioning is a
strict subset of the catalog model. What hive adds is only *where the
metadata comes from*: the path, not a CSV column.

## 2. The model

A catalog is a list of shards, each with a location and metadata. A
hive directory is a catalog whose rows can be **derived**: the paths
from a glob, the metadata from the `key=value` segments of each path.
The two should be one command with one pruning rule, one enrichment
rule, one optimiser rule and one set of lanes — not a `from parquet`
flag that re-implements a slice of `from catalog` for local files.

## 3. What is missing

All three gaps are in the catalog, not in `from parquet`:

1. **Per-file metadata from the path.** A catalog row with a glob
   expands (`ExpandCatalogGlobs`, locally with `filepath.Glob`, remotely
   with `ssh HOST echo GLOB`), but every expanded file shares the row's
   metadata (`Metadata: entry.Metadata // shared`). Nothing reads
   `day=2023-05-16` out of a path.
2. **Schema mode is empty.** `SSQL_MODE=schema ssql from catalog
   cat.csv` prints nothing and exits 0, so completion, the typed lane
   and anything that asks a source for its fields are blind to a
   catalog. A gap today, hive or not.
3. **The catalog path is slower and has no SQL lane.** Each shard is a
   subprocess with JSONL between it and the reader: 1.74 s for the seven
   local Parquet files against 0.71 s for `from parquet
   'day=*/*.parquet'`, which reads in-process and, in the typed lane, in
   parallel by row group (`typed.ReadParquetFilesParallel`). `generate
   sql` refuses catalogs outright.

## 4. Design

### 4.1 `from catalog -hive`

A boolean flag. After glob expansion, each entry's path is split into
directory segments (the file name is never one); every segment of the
form `key=value` adds `key → value` to that entry's metadata. Rules:

- Keys are taken as written; values are percent-decoded as DuckDB and
  Spark write them (`%2F` for `/`) — the one transformation.
- A key already in the row's metadata (a CSV column of the same name)
  is an error naming the file: the catalog author and the layout
  disagree, and silently preferring either would be the bug that only a
  hand comparison finds.
- A key that matches a column in the file is an error, found when the
  first record arrives (the same place `-shard-field` would clash).
- Every file expanded from one glob must yield the same keys in the
  same order; the error names the file that differs, as
  `CheckParquetSchemas` names a column.
- A path with no `key=value` segment under `-hive` is an error for that
  entry ("no key=value directory in …"), not a silent no-op; a
  catalog mixing hive and plain rows is a mistake worth hearing about.
- Values are strings. The types of partition columns are the same in
  every lane; `cast -type year int` downstream gives a typed column.
  The SQL lane pins this with `hive_types_autocast=false`.

Derived keys are exact-value columns: pruning uses `catalogStringOp`
and the optimiser may drop the row filter after extraction. Ranges
cannot come from a path.

### 4.2 A glob in place of the FILE

`from catalog -hive 'rail/services/day=*/*.parquet'`: when FILE is a
glob (or several paths — the `from parquet` list form), the catalog is
a single synthesised local row per argument and `-hive` is implied.
There is no CSV. The DFC148 post's view becomes

```json
[["from","catalog","-hive","rail/services/day=*/*.parquet"]]
```

and moving the data to three machines is a change from the glob to a
CSV with a `host` column; the consumer's query does not change. The
plain `from parquet GLOB` form stays as it is (no partition columns),
so `parquet_hive_dir_single` in the equivalence corpus keeps its
meaning.

### 4.3 `-catalog-used` is the discover tool

`from catalog -hive GLOB -catalog-used shards.csv | ssql count` writes
the derived catalog — host, path, the partition columns — which the
user then edits to add hosts, ranges or a `bin`. This is DFC054 §Pattern
4's `catalog-discover`, never built as its own command; it falls out of
two existing flags.

### 4.4 A local fast path

When every pruned entry is local and the same format, and that format
has an in-process multi-file reader (Parquet today: `ReadParquetFiles`;
Arrow and CSV lists can follow), `from catalog` reads in-process and
attaches each file's metadata as its records are produced, instead of
spawning a process per shard. The subprocess path remains for remote
hosts, mixed formats and pushdown stages (`-- where …`). This makes a
local catalog as fast as `from parquet`, for hand-written catalogs too.

The per-file enrichment needs `ReadParquetFiles` to expose a per-file
hook (or to take a `[]ParquetSource{Path, Extra map[string]string}`);
the record schema is built once per file (file columns + keys), never
per record (the #1 performance rule).

### 4.5 Schema mode

`SSQL_MODE=schema` for a catalog: the first pruned shard's schema (via
the shard's own schema mode, remote or local — a command is the
authority on itself) plus the metadata columns as `string`, plus
`-shard-field` when set. Needed for completion, for the typed struct
and for the optimiser's column knowledge; a gap worth closing on its
own.

### 4.6 Lanes

| Lane | Change |
|---|---|
| exec | §4.1 derivation in `ExpandCatalogGlobs` (per-entry metadata, no longer shared); §4.4 fast path. |
| generate ssql | The optimiser's `readCatalogMetadataColumns` must learn derived columns: for a glob FILE, from the pattern's own `key=*` segments; for a CSV with `-hive`, by expanding the catalog as exec would. Otherwise the rule sees no catalog columns and a `where -if day …` stays a row filter — correct but unpruned, a silent performance regression the gate must catch. |
| go record | The generated program already reads the catalog at run time and calls `ProcessCatalogShardsRemoteGo` (DFC059); it gains the `-hive` option and the fast path. |
| go typed | Needs §4.5: the struct is the first shard's schema plus the partition keys as `string`. The catalog is re-read at run time, but the key *names* are fixed by the layout, so the struct is stable; a run-time layout change that adds a key is a loud mismatch. |
| generate sql | For an all-local Parquet catalog only: `read_parquet([the pruned files], hive_partitioning=true, hive_types_autocast=false)` when the paths are hive-shaped, else `read_parquet([...], hive_partitioning=false)` with metadata columns as literals (`SELECT *, 'v' AS day FROM …` per file, `UNION ALL`). Remote and mixed catalogs keep refusing. |

### 4.7 What this does not do

- Ranges from paths. A layout like `year=2023/month=05` gives two exact
  columns; a consumer's `where -if day ge 2023-05-16` on an exact
  column prunes by string comparison (`catalogStringOp` already handles
  `ge`), which is right for zero-padded dates and wrong for `month=5`
  vs `month=10` — the same caveat DuckDB has with autocast off. Noted,
  not solved.
- Partition pruning inside a file (row-group statistics). A different
  layer; `from parquet` does not do it either.
- Globs over HTTPS. DFC148 §4.3's limit stands; a remote hive layout is
  reached through a host, not a URL.

## 5. Gates

- Equivalence: `catalog_hive_glob` — `from catalog -hive
  'pqhive/region=*/data.parquet' | sort id` with `Golden` rows carrying
  `region`, the DuckDB lane as the independent oracle via §4.6's SQL
  form. Watch it fail by running the typed lane before §4.5.
- Equivalence: `catalog_hive_where_prunes` — a `where -if region eq
  north` after the glob form; `-explain`/`generate ssql` must show the
  extraction (`-if region eq north` on the from stage), and
  `-catalog-used` must list one file. Watch it fail by leaving
  `readCatalogMetadataColumns` unaware of derived columns.
- `parquet_hive_dir_single` stays: without `-hive`, no lane derives a
  column.
- Error paths in exec and generate mode: a key/CSV-column clash, a
  key/file-column clash, mismatched keys across files, a plain path
  under `-hive`.
- Schema mode: `TestSchemaModeCatalog` — fields of the first shard plus
  the keys; the typed struct matches.
- Scale (DFC113): the fast path over the ~120 MB fixture split into
  partitions, with a wall-time ceiling near `from parquet`'s; the
  subprocess path's cost is what §3.3 measured and must not come back
  for local catalogs.
- `TestFieldCompletionConfiguration` for `-hive`.

## 6. Order of work

1. §4.5 schema mode (its own slice; useful on its own).
2. §4.1 derivation + §4.2 glob form + the optimiser's derived columns,
   exec and `generate ssql`, with the gates. This is the post's step.
3. §4.4 fast path, Parquet first, with the scale case.
4. §4.6 typed and SQL lanes.

Effort: 1 is small; 2 is a day; 3 is the larger piece, a day or two
with the typed parallel reader; 4 half a day each.

## 7. Open questions

- Should `from parquet GLOB -hive` exist as sugar for the glob form of
  `from catalog -hive`? It would be one more spelling of the same
  thing. The proposal says no: one command owns shard lists.
- `-shard-order catalog` gives deterministic output for the subprocess
  path; the fast path reads files in catalog order already, and the
  typed parallel reader's cross-shard order is the consumer's. Fine
  for group-by and sort; a `limit` without a sort will differ between
  lanes as it does for every multi-file source today.
- Percent-decoding of values: confirm what DuckDB writes for a value
  containing `/` or `=` before committing to it (one `COPY … PARTITION_BY`
  probe).

## Prior art / related

- [DFC054](./distributed-shard-catalog.md) — the catalog: pruning,
  enrichment, Pattern 4 discover (never built; §4.3 here).
- [DFC059](./catalog-codegen.md) — catalog codegen, which the go lanes
  reuse.
- [DFC074](./merge-catalog.md) — `merge -catalog`; a hive catalog
  merges the same way.
- [DFC148 §4.2](./dfc148_duckdb_view_only_mode.md) — the `from parquet
  -hive` proposal this replaces; §4.6 there (pushdown at generate time)
  is the same shape as the pruning the optimiser already does here.
- [DFC112](./dfc112_cloud_data_strategy.md) — HTTPS Parquet; no
  globs over HTTPS.
- [DFC113](./dfc113_scale_gate.md) — the scale gate §5 adds to.
