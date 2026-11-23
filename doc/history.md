# A Short History of ssql

In brief: ssql began in September 2025 as StreamV3, a Go library for
streams of records, and gained its command line and Go code generation
in October. It was renamed ssql in November 2025, and v4, the current
major version, came that December. Through 2026 it added SQL generation
for DuckDB, Postgres and DataFusion, a browser playground, typed and
parallel Go programs, Rust programs on DataFusion, signal processing with
an optional GPU build, and nested JSON data.

Below are the milestones, in order, with the release that first carried
each one. For every change in detail, see the [CHANGELOG](../CHANGELOG.md).

## 2025: from a Go library to a command line

- **September 2025: StreamV3.** ssql began as StreamV3, a Go library for
  processing streams of records with Go 1.23's iterators, plus
  interactive charts.
- **October 2025: the command line.** The library gained a CLI (`from`,
  `where`, `group-by`, `chart` and friends), one small command per step
  joined by Unix pipes. From the start, a pipeline could also be turned
  into the equivalent Go program. The CLI codelab began the same month.
- **November 2025: the name ssql, and SQL's words.** StreamV3 was renamed
  ssql, and its completion library, written alongside it, became
  `autocli`. v3.0.0 aligned the flags with SQL (`where -if`, `-if-expr`).
- **December 2025: v4.** Transforms such as `where` and `update` became
  pure filters on stdin, leaving files to `from`. v4.0.0 reworked `join`
  (`-using`, `-on LEFT RIGHT`, `-as`) and moved the module to
  `github.com/rosscartlidge/ssql/v4`, the major version still current.

## 2026: more engines, more data, more places to run

- **January: formats and signals.** Apache Arrow I/O (v4.7.0). FFT,
  convolution and correlation for signal processing, and an optional CUDA
  build for large signals (v4.8.0). Debian packages followed in February.
- **February: exploring data.** `to explore`, an interactive explorer
  page (v4.14.0), soon with a pipeline builder and pivots (v4.18.0).
  Excel `.xlsx` files (v4.16.0). Parquet was supported by March.
- **March: SQL and the browser.** `generate sql` translated a pipeline to
  SQL for DuckDB, and catalogs gave many files one name (v4.29.0). The
  playground put the whole CLI in a web page, compiled to WebAssembly
  (v4.30.0).
- **April: typed and parallel Go.** The `ssql/typed` package, a
  struct-based data path (v4.35.0). Generated programs became typed and
  parallel across every core (v4.36.0), the route to DuckDB-class speed.
- **May: `ssql serve`.** A console over SSH for a machine that holds the
  data (v4.44.0).
- **June: completion that knows the pipeline.** Schema mode lets each
  command predict its output columns, so Tab and Ctrl-O complete field
  names from everything upstream of the cursor (v4.46.0).
- **September: time series and more SQL engines.** `resample` grids time
  series (v4.80.0). `generate sql -dialect` added Postgres and DataFusion
  beside DuckDB, each checked against the real engine (v4.101.0).
- **October: nested data, big numbers, Rust.** JSON values inside fields,
  with dotted paths, `explode` and `flatten` (v4.113.0). Checked 128-bit
  integers (v4.125.0) and exact decimals for money (v4.127.0).
  `generate rust` turned a pipeline into a Rust program on DataFusion
  (v4.128.0).
- **November: graphs and a FAQ.** `traverse` for graph walks such as
  shortest routes (v4.136.0). An FAQ answered by a local model, which
  runs every pipeline it suggests against sample data before showing it,
  opened to the public on November 22.
