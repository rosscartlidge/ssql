# Show HN Draft

Reference: DFC075
Created: 2026-04-04
Last modified: 2026-04-04

[Back to Index](./README.md)

**Title:** `Show HN: ssql – Unix-style data processing that optimizes and compiles to Go`

**Body:**

ssql is a CLI tool and Go library for stream processing CSV, JSON, Parquet, and more.
The workflow: prototype with Unix pipes, let the optimizer rewrite your pipeline, then
compile to a standalone Go binary.

  ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -count n | ssql to table

The optimizer merges redundant filters, pushes predicates into SSH connections,
and collapses sort+limit into top-N. Then `generate go` compiles the optimized
pipeline to readable Go code with CLI flags.

  (export SSQLGO=1; ...) | ssql generate ssql -explain  # see what changes
  (export SSQLGO=1; ...) | ssql generate go              # compile to Go
  (export SSQLGO=1; ...) | ssql generate sql              # or DuckDB SQL

Other things it does:
- Window functions (ROW_NUMBER, LAG, running totals) without collapsing rows
- SSH pushdown: filter on the remote host, stream results back
- Multi-file parallel processing (4x faster with pushdown)
- Interactive charts and animated frequency spectra from WAV files
- GPU acceleration for compute-heavy ops (FFT 21x, convolution 320x)
- Tab completion for field names, operators, and values

The ssql Go package works independently of the CLI — you can use it as a library
for building data pipelines in pure Go with type-safe iterators and composable
filters. Built on Go 1.23+ iterators and generics.

The CLI is built with autocli (https://github.com/rosscartlidge/autocli), a
fluent interface for building CLIs with flag/arg handling, man pages, bash
completion, and clause-based parsing — all from a single declarative definition.

Try it in the browser (no install): https://rosscartlidge.github.io/ssql/playground.html

Install: brew tap rosscartlidge/ssql && brew install ssql
Or: go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

GitHub: https://github.com/rosscartlidge/ssql

I built this because I love the Unix pipeline paradigm — small tools composed
with pipes is still the most productive way to explore data. But traditional
pipes leave performance on the table: redundant work across stages, no pushdown
to remote hosts, no way to compile the result. I wanted to keep the simplicity
of "command | command | command" while adding what modern systems offer: query
optimization, code generation, GPU acceleration, and distributed execution.
