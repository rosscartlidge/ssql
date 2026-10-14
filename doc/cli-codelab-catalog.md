# Catalogs and Views

Part of the [CLI Codelab](cli-codelab.md) — sections 8 and 9 introduce
catalogs and `ssql run`; this is the longer walk through both, ending
where they meet: a table of views over partitioned data, each view a
pipeline document, run with no shell. Every block runs against the
files `ssql codelab` writes (`./codelab-run.sh catalog` runs this
document against your own install), in the directory they were written
to:

```bash
# codelab: skip — run once by hand
ssql codelab ~/ssql-codelab && cd ~/ssql-codelab
```

## 1. A catalog is a CSV

*Why:* data that has outgrown one file is many files, often on many
machines. A catalog is the list of them — a CSV with a `host` and a
`path` column, and any columns that describe each file — and
`from catalog` reads the list as one stream:

```bash
cat shards.csv
ssql from catalog shards.csv | ssql count
```

The extra columns are metadata: they prune, and they tag. `-if` on a
catalog column skips the files that cannot match before any is opened,
and `-catalog-used` writes the list of files that were:

```bash
ssql from catalog shards.csv -if month eq 2026-02 -catalog-used used.csv | ssql count
cat used.csv
```

Every record also carries the metadata of its file (`month` here), and
`-shard-field` adds the file itself:

```bash
ssql from catalog shards.csv -shard-field shard | ssql group-by shard month -count n -sum amount total | ssql to table
```

The stages after `--` run *inside* each shard, before the streams
merge. For a `where` that is a filter per file; for a `group-by` it is
the first half of a two-phase aggregate, and the stage after the merge
is the second half:

```bash
ssql from catalog shards.csv -- where -if status eq shipped + group-by status -sum amount total | ssql group-by status -sum total total | ssql to table
```

Locally that is parallelism. On a cluster the `host` column names SSH
hosts from `~/.ssh/config`, every shard runs where its file is, and the
pushed stages decide how much of the file crosses the network: a
`group-by` on the shard sends one row per group. ssql must be installed
on each host; a `bin` column names it when it is somewhere unusual.

You rarely write the `--` yourself. The optimiser lifts a `where` on a
catalog column into `-if`, and `generate ssql -explain` shows the
rewrite:

```bash
(export SSQL_MODE=record; ssql from catalog shards.csv | ssql where -if month eq 2026-02 | ssql group-by status -count n | ssql to table) | ssql generate ssql -explain
```

A catalog answers schema mode like any source, so completion and the
typed lane see its fields — the shard's own, then the catalog's columns:

```bash
(export SSQL_MODE=schema; ssql from catalog shards.csv) | ssql generate schema
```

## 2. A hive layout is a catalog

*Why:* the other way to describe many files is to put the description in
their paths. `year=2026/month=02/part.parquet` is the hive layout —
what Spark, DuckDB and most lake tools write — and `from catalog -hive`
reads it as a catalog whose metadata columns are the `key=value`
directories. Build one from the two monthly files:

```bash
for m in 2026-01 2026-02; do
  mkdir -p orders/month=$m
  ssql from orders_$m.csv | ssql to parquet orders/month=$m/orders.parquet
done
find orders -type f | sort
```

With `-hive`, FILE is a glob of the data files. Each file is a shard,
and `month` is a column of every record it holds:

```bash
ssql from catalog -hive 'orders/month=*/*.parquet' | ssql group-by month -count n -sum amount total | ssql sort month | ssql to table
```

It prunes as a catalog does. In a shell pipeline each stage is its own
process, so `from` cannot see a `where` after it: put the condition on
`from catalog` as `-if`, and only the matching files are opened
(`-catalog-used` is the proof). The optimiser does the same rewrite for
you in `generate ssql`, `generate go` and `run`:

```bash
(export SSQL_MODE=record; ssql from catalog -hive 'orders/month=*/*.parquet' | ssql where -if month eq 2026-02 | ssql count) | ssql generate ssql -explain
ssql from catalog -hive 'orders/month=*/*.parquet' -if month eq 2026-02 -catalog-used hive.csv | ssql count
cat hive.csv
```

Look at `hive.csv`: it is a plain catalog, with the derived column
written out. That is how a hive layout moves to a cluster — write the
catalog, edit the `host` column, and read the catalog without `-hive`
(the column is a catalog column now; with `-hive` as well it would be
derived a second time, which is an error):

```bash
ssql from catalog hive.csv | ssql count
```

A derived column is a string in every lane, whatever the value looks
like (`month=02` is `"02"`). `cast` types one; and a column named like
a calendar function — `year`, `month`, `day` are the usual keys — is the
field when used bare and the function when called:

```bash
ssql from catalog -hive 'orders/month=*/*.parquet' | ssql update -set-expr label 'month + " (from the path)"' -set-expr m 'month(date)' | ssql limit 2 | ssql include order_id month label m | ssql to table
```

Pushdown and `-shard-field` work as for any catalog; here the derived
column is appended after the pushed stages, so a per-shard `group-by`
still carries it:

```bash
ssql from catalog -hive 'orders/month=*/*.parquet' -shard-field shard -- group-by status -sum amount total | ssql sort shard status | ssql to table
```

The rules are strict, and each failure names the file. Every file must
have the same keys in the same order; a key that is also a column of
the file, or of the catalog, is a clash; and a path with no `key=value`
directory has nothing to derive:

```bash
ssql from catalog -hive 'orders_*.csv' 2>&1 || true
```

The same pipeline compiles to a Go program (`generate go -run`), with
the shards read in-process for local files and over SSH for remote
ones:

```bash
ssql generate go -run -pipeline "ssql from catalog -hive 'orders/month=*/*.parquet' | ssql where -if month eq 2026-02 | ssql group-by status -sum amount total | ssql to table"
```

## 3. A pipeline is a document

*Why:* a program that assembles a pipeline from a user's choices needs
the choices to stay data. The pipeline *is* a list of argument lists, so
`generate json` writes it as one and `ssql run` executes it with no
shell. Capture the hive query from section 2:

```bash
(export SSQL_MODE=record; ssql from catalog -hive 'orders/month=*/*.parquet' | ssql where -if month eq 2026-02 | ssql group-by status -sum amount total | ssql to table) | ssql generate json > february.json
cat february.json
```

(`from` writes its resolved time zone into the document, so the file
runs the same anywhere.) `-check` validates every stage against the real
command grammar and then runs each on its input's *schema* — a field no
earlier stage produces is reported with the stage and the fields
available there — and `-print` renders the shell form:

```bash
ssql run -check february.json && echo valid
ssql run -print february.json
ssql run february.json
```

```bash
echo '[["from","csv","-arg","orders.csv"],["where","-if","region","eq","eu"],["to","table"]]' | ssql run -check -stdin 2>&1 || true
```

A document is also a source for `generate`, so the optimiser's rewrite
and the SQL come from the same file a program holds:

```bash
ssql generate ssql -json february.json
```

```bash
echo '[["from","csv","-arg","orders.csv"],["where","-if","status","eq","shipped"],["group-by","status","-sum","amount","total"],["to","table"]]' > shipped.json
ssql generate sql -json shipped.json
```

The three habits that make a document safe are in the main codelab's
[section 9](cli-codelab.md#9-pipelines-from-programs): positionals as
`-arg VALUE`, values in flag slots rather than in expression text, and
`-check` before running.

## 4. A table of views

*Why:* a view in a database is a named query. Here it is a row — a name
and a pipeline document — in a table that lives wherever `from` reads: a
CSV in the working directory, a Parquet file, a URL, a database. Three
views over the orders:

```bash
cat > views.csv <<'EOF'
name,pipeline
february,"[[""from"",""catalog"",""-hive"",""orders/month=*/*.parquet""],[""where"",""-if"",""month"",""eq"",""2026-02""]]"
shipped,"[[""from"",""catalog"",""-hive"",""orders/month=*/*.parquet""],[""where"",""-if"",""status"",""eq"",""shipped""]]"
shipped_view,"[[""from"",""csv"",""-arg"",""views.csv""],[""where"",""-if"",""name"",""eq"",""shipped""],[""to"",""lines"",""pipeline""]]"
EOF
ssql from views.csv | ssql describe | ssql to table
```

`where` is the lookup, `to lines FIELD` writes the selected row's
pipeline as one line of text, and `run -stdin` runs it. The view's
output is a stream like any other, so the stages that use it follow:

```bash
ssql from views.csv | ssql where -if name eq february | ssql to lines pipeline | ssql run -stdin | ssql group-by status -sum amount total | ssql to table
```

A view is pruned every time it runs. `run` applies the optimiser's
rules to the document before executing it (the rewrites `generate ssql`
prints), so the `where` in the `february` view becomes `-if` on the
`from` stage and one file is opened. `-explain` names the rule and the
pipeline that ran; `+O` runs the document exactly as written:

```bash
ssql from views.csv | ssql where -if name eq february | ssql to lines pipeline | ssql run -stdin -explain 2>&1 >/dev/null
```

The third view selects another view. Its output is a document, so a
second `run -stdin` runs that:

```bash
ssql from views.csv | ssql where -if name eq shipped_view | ssql to lines pipeline | ssql run -stdin | ssql run -stdin | ssql where -if amount gt 1000 | ssql to table
```

`run` takes one document. A `where` that matches two rows is an error
that says so, not two pipelines run in sequence:

```bash
ssql from views.csv | ssql where -if name ne nothing | ssql to lines pipeline | ssql run -stdin 2>&1 || true
```

The views table is data. `describe` lists the views, `update` rewrites
one, `to csv` or `to parquet` saves the table, and the same table at a
URL serves every machine that can fetch it — the DuckDB "view without a
database" sequence, with the database replaced by a file of rows
([DFC148](research/dfc148_duckdb_view_only_mode.md),
[DFC149](research/dfc149_hive_partitions_as_derived_catalog.md)).
