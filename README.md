# ssql

**Unix pipelines for tabular data.** Read a file, filter, join, group, sort, and
write it back, one small command per step, with tab completion that knows
your columns. Then, when it matters, compile the same pipeline to a
parallel Go program that beats DuckDB on the workloads it covers.

```bash
ssql from orders.csv | ssql where -if status eq shipped | ssql join customers.csv -using customer_id \
  | ssql group-by country -sum amount revenue -count orders | ssql sort -desc revenue | ssql to table
```
```
country   revenue   orders
--------------------------
US           3240        4
DE           2300        2
```

![ssql at the prompt](doc/demo.gif)

## Install

```bash
brew tap rosscartlidge/ssql && brew install ssql        # macOS and Linux
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest   # any Go 1.21+; then add ~/go/bin to PATH
```

Debian and Ubuntu, no Go needed:

```bash
curl -LO https://github.com/rosscartlidge/ssql/raw/main/ssql_4.110.0_amd64.deb && sudo dpkg -i ssql_4.110.0_amd64.deb
```

Then `ssql version`, and `eval "$(ssql -shell-init)"` in `~/.bashrc` for
completion and the key bindings. Prebuilt binaries, WASI, the GPU build
(`ssql_gpu`) and the Go library: [doc/install.md](doc/install.md). Or try it with no
install at all in the [browser playground](https://rosscartlidge.github.io/ssql/playground.html).

## Ten minutes

`ssql codelab` writes the sample files above into a directory; the
[CLI codelab](doc/cli-codelab.md) takes it from there: look at a file,
answer questions about it, save and share, time series, make it fast,
generate code, distributed data. Every block in it is run by a script
against the current release, so what you read is what happens.

## Why this and not a database, jq, or pandas

- **A pipeline is the mental model.** It reads in the order it runs, each
  stage sees only what the one before produced, and any prefix can be
  cut and looked at (`| ssql to table`). Tab completes commands, flags,
  field names and values with knowledge of the whole line; `Alt-h`
  explains the word under the cursor.
- **The same pipeline is a compiled program.** `ssql generate go -run
  -pipeline 'ssql from … | ssql group-by … | ssql to csv'` builds and
  runs a standalone parallel Go binary with struct types and no
  reflection. On a 14.6 M-row parquet cube: ssql 0.28 s, DuckDB 0.96 s,
  a quarter of the memory ([measured](doc/performance.md)).
- **The same pipeline is SQL, too.** `ssql generate sql` emits DuckDB,
  Postgres or DataFusion SQL from the stages you typed, and the project's
  tests run every pipeline five ways and assert they agree.
- **Pipelines are data.** A pipeline is an argv, never text a shell parses,
  so a program can build one from untrusted input with no query builder
  and no injection ([JSON documents](doc/cli-codelab.md#9-pipelines-from-programs), `ssql run`).
- **Streams, remote files, signals, charts.** Live input, `from https://`
  with range reads, files over SSH with the filter pushed to the far end,
  sharded catalogs, FFT, convolution and spectrograms (with an optional
  CUDA build, `ssql_gpu`, that runs the heavy ones 20 to 300× faster),
  self-contained HTML charts and an explorer.

Honest limits: no persistent database, no SQL as the interface, no
correlated subqueries. The full comparison with DuckDB is
[DFC136](doc/research/dfc136_duckdb_feature_comparison_2026_09.md).

## Learning Path

1. **[CLI Codelab](doc/cli-codelab.md)** — start here.
2. **[Signal Processing](doc/cli-signal-processing.md)** — an optional CLI branch.
3. **[Getting Started Guide](doc/codelab-intro.md)** — the Go library the CLI is built on.
4. **[Typed Codelab](doc/typed-codelab.md)** — the `ssql/typed` struct API, and what `generate go` emits.
5. **[API Reference](doc/api-reference.md)** and **[Typed Reference](doc/typed-reference.md)**.

Side paths: [The shell experience](doc/cli-shell.md) ·
[Performance, measured](doc/performance.md) ·
[The Go library by example](doc/library-tour.md) ·
[AI code generation](doc/ai-human-guide.md) ·
[Troubleshooting](doc/cli-troubleshooting.md).

## Documentation

**[All documentation →](doc/README.md)** · **[Research and design docs →](doc/research/README.md)** · **[Changelog](CHANGELOG.md)**

Questions, issues and contributions are welcome on
[GitHub](https://github.com/rosscartlidge/ssql). ssql installs with Go 1.21+ (it fetches the toolchain it
builds with), pure Go, no CGO; `import "github.com/rosscartlidge/ssql/v4"`.
