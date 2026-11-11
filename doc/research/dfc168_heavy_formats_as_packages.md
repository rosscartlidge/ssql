# Heavy formats as packages a program imports

Reference: DFC168
Created: 2026-11-11
Last modified: 2026-11-11

[Back to Index](./README.md)

## 1. Why

Every program that imports `github.com/rosscartlidge/ssql/v4` links Apache
Arrow and Parquet, excelize, and `net/http`, whether it reads them or not.
That covers every program `generate go` writes, every library-mode
package, and every service that embeds ssql (DFC142). gRPC and protobuf
come along too: Arrow's `parquet/pqarrow` imports `arrow/flight`.

The cost was measured on 2026-11-11 while looking for why the `cmd/ssql`
test suite had grown to 1,465 s:

- **Size and link time.** A generated typed program that reads three rows
  from `range` is 43 MB and links in ~1.4 s. The same program built
  `-tags slim` is 5 MB and links in ~0.37 s, with the same output.
- **The test suite.** A `GOFLAGS=-toolexec` log counted 2,559 links of
  generated programs in one suite run, 6,805 s of link time summed, with a
  link running for 1,064 s of the 1,475 s wall.
- **The stopgap.** The suite now builds a generated program slim when its
  source names nothing slim leaves out, and retries in full when that
  guess is wrong (`cmd/ssql/slim_build_test.go`). With 30 slow tests made
  parallel, the suite runs in 556 s.

That stopgap helps only the tests, and it is a heuristic scan of source
text. Users pay the full link on every `generate go -run` and `-build`, and
library users pay it on every build of their own program.

Ross (2026-11-11): split out the heavy formats but still be able to use
them. He asked whether fork/exec of a binary, or three builds
(slim/normal/heavy), is the way. This DFC answers that and proposes a
design.

## 2. What is heavy

Each row adds one dependency to the slim build of the same three-row
program. Link times are the best of three, with the build cache warm.

| Linked in addition to slim | Link | Binary |
|---|---|---|
| nothing (slim) | 0.37 s | 5 MB |
| `net/http` | 0.53 s | 8 MB |
| `github.com/xuri/excelize/v2` | 0.44 s | 8 MB |
| `arrow/go/v18/parquet/pqarrow` | 1.29 s | 41 MB |
| everything (today's default) | ~1.4 s | 43 MB |

Arrow and Parquet are nearly all of it: 36 MB and ~0.9 s a link. excelize
and `net/http` cost ~3 MB and 0.1–0.2 s each.

### 2.1 Where they sit today

- **Root package:**
  - `arrow.go`: ReadArrow, ReadArrowFromReader, WriteArrow,
    WriteArrowToWriter, WithArrowColumnTypes, ExtractSignalFromArrow,
    ExtractSignalFromArrowReader, ReadAtSeeker.
  - `parquet.go`: ReadParquet, ReadParquetColumns, ReadParquetFiles,
    ReadParquetFromReader, CheckParquetSchemas, ParquetRowCount,
    ParquetSchemaFields, WriteParquet, WriteParquetToWriter, and the
    WithRowGroupSize, WithCompression and WithColumnTypes options.
  - `xlsx.go`: XLSXConfig, DefaultXLSXConfig, ReadXLSX, WriteXLSX,
    ReadXLSXSheetNames.
  - `http_source.go`: OpenHTTPStream, OpenHTTPFile, HTTPFile.
- **`typed` package:**
  - `io_parquet.go`: ReadParquet[T], ReadParquetFiles[T],
    ReadParquetSafe[T], the FromReaderAt forms, the Parallel forms,
    WriteParquet[T], WriteParquetToWriter, and the options.
  - `io_parquet_catalog.go`: ParquetSource, ParquetConstant,
    ReadParquetSources and its Parallel form, CatalogParquetSources.
- **`internal/pqcols`** imports Arrow and Parquet directly.

The `typed` package matters as much as the root package. Every typed
generated program imports `typed`, so as long as typed Parquet lives
there, a typed CSV program links Parquet.

Inside the library, the root package's other code calls these in four
places:

- `catalog_local.go` reads a catalog's Parquet entry with `ReadParquet`;
- `open_input.go` sends a URL to `OpenHTTPStream` or `OpenHTTPFile`;
- `sample_file.go` and `tail_file.go` sample or tail a URL.

These are the seams the slim stubs already cover (DFC151 §2).

Generated code names them in about twenty command files:
- `from_parquet` and `from_parquet_typed`;
- `from_arrow`, `to_arrow`, `to_parquet` and `to_xlsx`;
- `from_xlsx` and `from_catalog_typed`;
- `ParquetRowCount` in `from_records`;
- `ExtractSignalFromArrow` in the five signal commands (fft, ifft,
  convolve, correlate, spectrogram).

## 3. The options

### 3.1 Fork/exec a binary that has the formats

The generated program would run `ssql from parquet FILE` and read JSON
Lines or Arrow back through a pipe. The CLI already works this way, since
it is the binary with everything. For generated programs it is wrong:

- **It stops being a standalone binary.** Library mode and the service use
  of DFC142 exist so that a pipeline deploys as one binary. This one would
  need a matching `ssql` installed beside it.
- **It loses the speed the typed reader was built for.** The typed Parquet
  reader decodes Arrow column arrays straight into struct fields, reads row
  groups in parallel, and projects columns (DFC158). Through a pipe that
  becomes text again. Parquet exists for speed.
- **It brings version skew.** The program and the installed `ssql` can
  disagree on wire types. ssql has had that bug class before, with the
  remote binary for `from ssh` (v4.94.0).

Fork/exec stays the right tool where it already is: the CLI's stages are
processes.

### 3.2 Go plugins

`plugin.Open` is Linux and macOS only. Host and plugin must be built by the
same toolchain with identical versions of every shared package. Nothing
can be unloaded. Not for a tool people `go install`.

### 3.3 Three builds: slim, normal, heavy

A build tag makes whoever builds a binary choose one bundle for every
program it will ever build. For a generated program, the generator would
have to pick the tag (`generate go -run -tags heavy`), and library users
would have to know to pass it. Each bundle is another configuration to
test: the slim build already has its own gates (`TestSlimBuildDependencies`,
the wasm budget), and a third doubles that. A tag selects code at compile
time by a name nobody can see in the source.

### 3.4 Packages, imported by what uses them (proposed)

Each heavy format becomes its own package. A program links a format when,
and only when, it imports that package. The generator knows exactly which
formats a pipeline touches, because each stage's fragment already lists
its imports, so a generated program imports exactly what it reads and
writes. No tag needs choosing, no binary needs installing, and nothing is
guessed from source text.

This is how Go itself does it: `image/png` registers a decoder with
`image`, a SQL driver registers with `database/sql`, and a program links
the format it imports.

## 4. Design

### 4.1 Packages

| Package | Holds | Depends on |
|---|---|---|
| `ssql/v4/parquet` | record and typed Parquet: read, write, the files and parallel forms, catalog sources, schema checks | Arrow, Parquet, `ssql`, `typed` |
| `ssql/v4/arrow` | Arrow IPC read and write, signal extraction | Arrow, `ssql` |
| `ssql/v4/xlsx` | XLSX read and write, sheet names | excelize, `ssql` |

Two choices in this layout:

- **One package per format holds both the record and the typed API.** The
  split is about the dependency, not about record against typed. A typed
  CSV program must not link Parquet just because typed Parquet exists.
- **`net/http` stays in the root package** (§7, decision 2). URL sources
  are decided at run time: a generated program whose input is a parameter
  can be handed a URL. It costs 3 MB and 0.16 s, and it is the standard
  library. The slim build keeps its stub for it (DFC151).

`internal/pqcols` stays internal; only `ssql/v4/parquet` imports it.

### 4.2 The registry: how the root package reaches a format it does not import

The root package cannot import `ssql/v4/parquet`, because that package
imports the root package for `Record`. Where the root package needs a
format it does not link, it calls through a hook that the format package
sets in its `init`:

```go
// root package, formats.go (no third-party imports)
type ParquetFormat interface {
	Read(path string, columns []string) (iter.Seq[Record], error)
	// … what the four internal seams of §2.1 need
}

var parquetFormat ParquetFormat // set by package parquet's init

func needParquet() (ParquetFormat, error) {
	if parquetFormat == nil {
		return nil, fmt.Errorf(`parquet support is not linked: import _ "github.com/rosscartlidge/ssql/v4/parquet"`)
	}
	return parquetFormat, nil
}
```

The hook is unexported, set through one exported registration function.
The interface is only as wide as the root package's own callers need: the
catalog's Parquet entry, and whatever generic dispatch exists. It is not
the format's public API. The public API is the format package itself.

A missing format is an error that names the import to add, never a silent
empty result: "fail loudly" (CLAUDE.md). The slim stubs already refuse
this way; the difference is that the refusal now names the remedy.

### 4.3 Generated code

Each fragment that reads or writes a heavy format adds that format's
package to its `Imports`, and the emitted call names it:

```go
import "github.com/rosscartlidge/ssql/v4/parquet"

records, err := parquet.ReadFiles(input)        // was ssql.ReadParquetFiles
rows := parquet.ReadTypedParallel[Sales](files) // was typed.ReadParquetParallel[Sales]
```

The import machinery already exists: the planner already swaps imports
with templates (`AltCodeIfSeq`), and unused imports are already pruned.
The names in the table above are placeholders; §7, decision 3 asks for
them.

### 4.4 The CLI and the slim build

The `ssql` binary links every format: one file in `cmd/ssql` with blank
imports of `ssql/v4/parquet`, `ssql/v4/arrow` and `ssql/v4/xlsx`, gated
`!slim`. The slim build is that file left out:

- The six stub files across the root, `typed` and `commands` packages
  (DFC151 §2) shrink to the CLI's own stubs for commands it cannot run.
- The `slim` tag stops being a property of the library and becomes a
  property of one binary.
- `TestSlimBuildDependencies` keeps its job of naming any forbidden package
  that drifts back into the slim binary.

### 4.5 Typed Parquet across the registry (only if §5's bridge is chosen)

A generic function cannot be stored in a registry. The typed reader is
already nearly non-generic underneath:

- `buildParquetReadPlan` maps the file's columns to struct fields through
  `reflect` once, at open;
- each field then decodes through a `parquetDecodeFn` writing at an
  `unsafe.Pointer`;
- `T` is used only to allocate the row and to yield it.

So a non-generic core can sit behind the hook:

```go
// typed package: a thin generic shell, no Arrow import
func ReadParquet[T any](filename string, opts ...ParquetOption) iter.Seq[T] {
	return func(yield func(T) bool) {
		var row T
		core := needParquetCore() // registered by package parquet
		core.Read(filename, reflect.TypeFor[T](), opts, unsafe.Pointer(&row), func() bool { return yield(row) })
	}
}
```

The core fills `row` in place and calls back once per row: one indirect
call per row on top of the per-field decoders. That has to be measured,
not assumed (CLAUDE.md: benchmark a change to a hot loop against HEAD
even when it "cannot" cost anything). The gate is the typed Parquet
benchmark against a HEAD worktree, plus the scale gate's Parquet cases.

### 4.6 Exec mode: the CLI keeps every format, and pays for it at startup

Exec mode is the `ssql` binary, and §4.4 links every format into it, so
nothing a user sees changes:

- **Commands call the format packages directly.** The command code for
  `from parquet`, `to xlsx` and `join x.parquet` lives in
  `cmd/ssql/commands`, which may import anything. It calls
  `parquet.ReadFiles` itself and needs no registry.
- **The registry fills before `main`.** Each format package registers in
  its `init`, so the root package's own seams (§4.2: a catalog's Parquet
  entry, for example) find their format in every CLI process.
- **Each stage is a process of the same binary.** In
  `ssql from x.parquet | ssql where … | ssql to xlsx out.xlsx`, every stage
  has every format, as today.
- **The slim CLI** (playground, WebVM, `to explore -wasm`) leaves §4.4's
  import file out. Its registry stays empty and `from parquet` refuses
  loudly, as the stubs do today.

What the split does not change is the cost of initialising the heavy
packages in every process. Go has no lazy package loading: every
package's `init` runs before `main`, even for `ssql version`. Measured on
2026-11-11 (v4.146.0, 100 starts of `ssql version`, and
`GODEBUG=inittrace=1`):

| Binary | Size | Per process start | Package init |
|---|---|---|---|
| full `ssql` | 51 MB | 11.8 ms | 8.1 ms, allocating 4.4 MB |
| slim `ssql` | 12 MB | 3.7 ms | 1.2 ms |

Where the init time goes, largest first:

- **`arrow/go/v18/arrow/compute`, 2.8 ms.** It registers its kernels.
  Arrow's own `parquet/pqarrow` imports it, so ssql cannot drop it while it
  reads Parquet through pqarrow.
- **excelize, 1.3 ms.**
- **snappy, arrow flight, gRPC and protobuf**, a few tenths of a
  millisecond each.

Every exec-mode stage pays about 8 ms of CPU for this. So do every Tab
completion (`ssql -complete`) and every process Ctrl-O starts to sample
an upstream. A six-stage pipeline spends ~50 ms of CPU on it, mostly
concurrent across the stages, so little wall time. It is below what
anyone notices when typing, but it is paid everywhere.

**The optional handoff.** Exec-mode stages are already processes, which
makes this the one place where §3.1's fork/exec fits:

- a lean `ssql` that links no heavy format starts in ~4 ms;
- a stage whose command reads or writes Parquet, Arrow or XLSX re-execs
  itself in a full binary installed beside it from the same release
  (found next to `os.Executable()`, never on `PATH`, which keeps it
  version-matched and out of reach of PATH hijacking);
- the command owns the decision (DFC115). The handoff happens inside the
  command once its arguments are parsed, so nothing else re-parses its
  arguments to decide.

What it would cost:

- **Two binaries per release** in the `.deb`, the `go install` line
  (`ssql` plus `ssql-full`, or a `-formats` binary), `make install-local`,
  and the goreleaser assets.
- **A failure mode where one binary is missing.** It must refuse loudly,
  naming the binary to install.
- **The `from ssh` and catalog remote-binary resolution**
  (`RemoteBinPrologue`) learning a second name.

It saves ~8 ms of CPU per process. It is a separate change from §4.1–4.5,
and none of the rest depends on it. This DFC recommends recording it here
and not building it unless the per-process cost becomes visible: for
example completion latency on a slow machine, or a pipeline of many short
stages run in a loop.

## 5. Compatibility: a v4 bridge or a v5 move

Moving `ssql.ReadParquet` to `parquet.Read` breaks every library user who
calls it. There are two ways to do it.

**A. v5: move cleanly.** The heavy names leave the root and `typed`
packages. Callers import the format package. The registry holds only what
the root package's own seams need (§4.2). This is the simplest code, but a
major version bump is Ross's call (CLAUDE.md: "Major version bumps only
when explicitly requested").

**B. v4 bridge, then A at v5.**
- The root and `typed` packages keep every heavy name, with today's
  signatures, as forwarders through the registry. This is today's slim
  stubs made permanent, with real delegation instead of refusal.
- Generated code and the CLI move to the format packages at once, so every
  `generate go` program links only what it reads in v4.
- A library user's existing code still compiles. If it calls
  `ssql.ReadParquet` without importing `ssql/v4/parquet`, it gets the
  loud "not linked" error at run time, naming the import to add.
- That runtime change is a behavioural break for those callers. The
  CHANGELOG states it, and the forwarders are marked `Deprecated:`
  pointing at the new names.
- At v5 the forwarders go.

B delivers the speed now without a major version; its cost is §4.5's
typed bridge and a deprecation period. A is less code, but waits for v5.
This DFC recommends B, because the generated programs are where the cost
is paid and they are all ssql's own code.

## 6. Gates

- **What a generated program links.** For pipelines over CSV, Parquet,
  Arrow and XLSX, in record and typed modes, run `go list -deps` on the
  generated module. Arrow is listed exactly when the pipeline names a
  Parquet or Arrow file, excelize exactly when it names an XLSX file.
  Watched to fail: a CSV fragment that keeps the root Parquet import.
- **The missing format is loud.** A program that calls a bridged name
  without the import fails with the message naming the import, in record
  and typed modes.
- **No registry cost on the hot path.** The typed Parquet benchmark
  against a HEAD worktree, and the scale gate's Parquet cases (DFC113).
  The bridge in §4.5 ships only if they hold.
- **The slim binary.** `TestSlimBuildDependencies` and the wasm size budget
  are unchanged, and slim now excludes the formats by not importing them.
- **The test-suite stopgap goes.** `slim_build_test.go`'s source scan and
  retry are deleted: the generated programs link only their formats by
  construction. The suite's link count and wall time are measured before
  and after, the same way as in §1.
- **The equivalence, corpus, matrix and lanes gates are unchanged.** They
  must stay byte-identical: this changes what links, not what runs.

## 7. Decisions for Ross

1. **The bridge (§5).** B, the v4 bridge now and the clean move at v5, or
   A, the move at v5 only.
2. **`net/http`.** Keep it in the root package (recommended: a run-time
   decision, the standard library, 3 MB), or a fourth package,
   `ssql/v4/httpsource`, with URL sources refusing loudly unless it is
   linked.
3. **Names.**
   - Package paths: top level (`ssql/v4/parquet`) or under a directory
     (`ssql/v4/format/parquet`).
   - Function names inside a format package: `parquet.Read` and
     `parquet.ReadTyped[T]`, or keep today's names (`parquet.ReadParquet`,
     which repeats itself).
4. **Before or after other work.** The suite is already at 556 s, so this
   is now user-facing speed, not test time:
   - `generate go -run` and `-build` link 1.4 → ~0.5 s;
   - a non-Parquet binary goes from 43 to ~8 MB;
   - library users build faster.
5. **The exec-mode handoff (§4.6).** Record it and leave it unbuilt
   (recommended: ~8 ms per process, against a second binary in every
   release), or build a lean `ssql` that re-execs a full binary for the
   heavy-format stages.

## 8. Plan

1. Measure the baseline: `generate go -run` wall time for a CSV pipeline
   and a Parquet pipeline; generated binary sizes; the typed Parquet
   benchmark; the suite with `SSQL_TEST_FULL_BUILD=1`.
2. `ssql/v4/xlsx`: the smallest move, three call sites. It proves the
   registry, the codegen imports and the gates.
3. `ssql/v4/arrow`: IPC and the signal commands.
4. `ssql/v4/parquet`, record side, then typed side; under B, the §4.5 core
   and its benchmark.
5. The CLI's blank-import file; delete the library's slim stubs; delete
   the test-suite stopgap.
6. Docs:
   - `doc/api-reference.md`, with the new packages and the deprecations;
   - `doc/ai-code-generation.md` and the Go codelabs, for the import lines;
   - DFC151, a note that the slim stubs are gone;
   - the CHANGELOG.
7. Measure again, and add the results to this DFC.
