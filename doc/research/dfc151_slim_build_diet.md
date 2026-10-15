# The slim build: size, composition, uses, and what to cut

Reference: DFC151
Created: 2026-10-15
Last modified: 2026-10-15

[Back to Index](./README.md)

## 1. Why now

The slim build is the engine every browser visitor downloads and the
engine every `to explore` page carries. It has grown with everything
else:

| When | Embedded explore engine (gzipped) | Source |
|---|---|---|
| 2026-08-16, DFC107 | ~3.4 MB | CLAUDE.md, "gzipped ~3.4MB" |
| 2026-09-01, v4.81.0 | 5.22 MB | git history of `cmd/ssql/wasm/ssql-playground.wasm.gz` |
| 2026-10-05, v4.111.0 | 5.84 MB | same |
| 2026-10-15, v4.119.0 | 6.47 MB | same |

Nearly double in two months, a quarter in the last six weeks. The
`claude/wasm.md` table still says "~13MB (slim)" for the browser
playground; the raw wasm is 28.3 MB today. Ross asked (2026-10-15)
whether anything can come out of slim while it stays useful. This DFC
measures what is in it, who uses it, and what each candidate removal
is worth, with every number from a build made on the v4.119.0 tree.

## 2. What slim is today

`-tags slim` swaps six files for stubs:

| Gated out (`!slim`) | Stub (`slim`) | What goes |
|---|---|---|
| `arrow.go`, `parquet.go`, `typed/io_parquet.go`, `commands/from_parquet_typed.go` | `arrow_slim.go`, `parquet_slim.go`, `from_parquet_typed_slim.go` | Apache Arrow and Parquet (the large dependency tree) |
| `xlsx.go` | `xlsx_slim.go` | excelize |
| `commands/serve.go`, `serve_cli.go`, `serve_schema.go`, `serve_validate.go` | `serve_slim.go` | the `serve` command (its HTTP file is NOT gated, §4) |
| `cmd/ssql/wasm/embed_full.go` | `embed_slim.go` | the 6.5 MB embedded engine (a wasm cannot embed itself) |

Everything else is in: every command the CLI registers (the playground
must match the CLI, `TestPlaygroundMainMatchesCLIRegistration`), all
three code generators and the SQL translator with its three dialects,
the optimiser, the expression language, completion and help, the
chart and animate templates, the signal-processing commands, the
codelab data (84 KB), and `net/http` with TLS.

Sizes on the v4.119.0 tree, Go 1.26.1, `-ldflags "-s -w"`:

| Build | Raw | Gzipped (-9) |
|---|---|---|
| `cmd/ssql` linux/amd64, full | 48.5 MB | |
| `cmd/ssql` linux/amd64, slim | 14.5 MB | |
| `cmd/ssql-playground` js/wasm, slim | 28.3 MB | 6.47 MB |
| `cmd/ssql` wasip1/wasm, slim | 27.7 MB | |

The full native binary is the slim one plus roughly 27 MB of
Arrow/Parquet/xlsx and the 6.5 MB embedded engine.

## 3. Who uses it

| Consumer | Build | Evidence of use | Size that matters |
|---|---|---|---|
| Browser playground (GitHub Pages) | js/wasm slim | every visitor; Pages serves it gzipped: `content-encoding: gzip`, 7.3 MB on the wire (Pages' gzip level is lower than `-9`) | the gzipped wasm |
| `to explore` pages | the same js/wasm, embedded gzipped in every full binary (`embed_full.go`) and written into every explore HTML (engine on by default; `-light` turns it off) | every `to explore` output, every full binary | the gzipped wasm, once per artifact |
| WASI (`ssql_X_wasi.tar.gz`) | wasip1 slim | zero downloads on v4.96.0, v4.111.0 and v4.118.0 (`gh release view --json assets`) | nobody's, today |
| goreleaser `ssql-slim_*` archives | native slim | zero downloads on the same three releases | nobody's, today |
| WebVM | documented as slim (`claude/wasm.md`); every release since at least v4.116.0 shipped the FULL 46 MB linux/386 build, which keeps `to explore -wasm` (journal 2026-10-15) | the image is 750 MB; either fits | not a slim consumer in practice |

So the slim build has two real consumers, the playground and the
explore embed, and both pay in gzipped wasm bytes. The native slim
archives and WASI are built and shipped but, by the download counts,
unused. Nothing here argues for keeping a feature in slim because a
native or WASI user needs it.

What the two real consumers can do with it:

- The playground page runs any pipeline typed into it (the JS splits
  the pipeline and calls `ssqlExec` per stage, with process
  substitution emulated through the virtual fs); shows `generate go`,
  `generate sql` and `generate ssql -explain` output; draws charts.
- An explore page is the same engine over the page's embedded rows:
  the UI's pipeline bar runs any pipeline, plus `-complete-source` for
  completion and `generate schema` for the field list.

What is registered but cannot work in a browser, and is kept only so
registration matches the CLI: `serve` (stubbed), `run` and the
`-pipeline`/`-json` sources of `generate` (they exec the binary),
`generate go -run`/`-build` (no toolchain), `from ssh`, remote
catalogs, `merge -catalog`, `to explore -wasm` (errors loudly by
design), and the optimiser re-execution in `generate go`/`generate sql`
(skipped under `GOOS=js`, so those translate the pipeline as typed).
`from URL` through `net/http` would work in principle (Go's js/wasm
transport is the Fetch API) but no playground example, test or codelab
block uses it.

## 4. Composition: what the bytes are

Two instruments. First, `go tool nm -size` over the native slim
binary with symbols, text and data only, attributed by package (an
approximation: pclntab and rodata are not per-symbol, so the shares
are of about 8.7 MB of the 14.5 MB file):

| Share | Package group |
|---|---|
| 2.5 MB | `ssql` itself: root 0.5, `cmd/ssql/commands` 1.8, `cmd/ssql/lib` 0.15 |
| 1.0 MB | runtime type data, itabs |
| 0.7 MB | `runtime` |
| 0.5 MB | `net/http`, `net`, `crypto/tls`, `crypto/x509` (text and data only; see below) |
| 0.4 MB | `expr-lang/expr` |
| 0.3 MB | `go/parser`, `go/printer`, `go/ast` (from `go/format`) |
| 0.3 MB | `encoding/*` (json, gob, asn1) |
| 0.2 MB | `text/template`, `html/template` |
| 0.2 MB | `autocli` |
| 0.2 MB | `reflect` |

Inside `cmd/ssql/commands` and `lib` (2.0 MB): exec paths 36%, record
codegen with the fragment assembler and the expr transpiler 20%,
registration, help and completion 17%, typed and parallel codegen
13%, the SQL translator 8%, the optimiser 3%, ssh and catalog 2%. The
largest single functions are the codegen emitters
(`generateUpdateCode` 40 KB, `buildTypedAggregator` 34 KB,
`emitTypedUpdate` 31 KB, `assembleLibrary` 29 KB).

Second, and more useful for js/wasm where the per-package shares are
different, probe programs: a hello world and the same program plus one
dependency, built for js/wasm with `-s -w`. The delta over hello
(2.46 MB) is what the dependency brings in on its own; deltas overlap
(reflect, fmt and friends are shared), so they do not add up to the
binary:

| Dependency | js/wasm delta over hello |
|---|---|
| `net/http` client (`http.Get` of an https URL) | +7.2 MB (+6.4 with `nethttpomithttp2`) |
| `net/http` server (`ListenAndServe`) | +6.7 MB; client and server together +8.0 |
| `expr-lang/expr` (compile and run) | +4.1 MB |
| `html/template` | +3.7 MB (`text/template` alone +2.3) |
| `go/format` | +1.4 MB |
| `regexp` | +0.5 MB |
| `encoding/json` | +0.45 MB |
| `encoding/gob` | +0.44 MB |
| `os/exec` | +0.4 MB |
| `autocli` | +0.03 MB |

Who pulls the heavy ones, in the slim build:

| Package | Imported by | Reachable in a browser? |
|---|---|---|
| `net/http` (with `crypto/tls`, `crypto/x509`, `net`) | root `http_source.go` (`from URL`, DFC112); `commands/serve_http.go` (the `serve` protocol, NOT gated by slim although `serve` is stubbed; it also holds `stageChain` and `killedByBrokenPipe`, which `run` uses) | `from URL` only, unexercised |
| `go/format` | `lib/codefragment_library.go`, formatting the library-mode output of `generate go -package` | never (library mode writes a file for a Go build) |
| `html/template` | root `chart.go`, six templates (chart, spectrogram, animate, …) | yes, `to chart` |
| `encoding/gob` | root `spill.go` (sort spill files) | yes |
| `expr-lang/expr` | the expression language | yes, everywhere |

## 5. What each cut is worth

Every row is a build of the v4.119.0 tree with the change applied in
a scratch worktree, js/wasm slim, `-s -w`, gzip -9:

| Change | Raw | Gzipped | Saving (gz) |
|---|---|---|---|
| baseline | 28.25 MB | 6.47 MB | |
| `-trimpath` | 28.25 MB | 6.47 MB | 0 |
| `-tags nethttpomithttp2` | 27.51 MB | 6.31 MB | 2.5% |
| `html/template` → `text/template` in chart.go | 27.87 MB | 6.37 MB | 1.5% |
| no `go/format` in library assembly | 27.16 MB | 6.23 MB | 3.7% |
| the three above together | 25.97 MB | 5.97 MB | 7.7% |
| the three above, plus `net/http` gated out of slim entirely | **18.95 MB** | **4.40 MB** | **32%** |

The last row is the finding. `net/http` and what it drags in (TLS,
x509, `net`, the HTTP/2 client, `encoding/asn1`, `math/big`, the
`vendor/golang.org/x/net` and `x/crypto` trees) is a third of the
slim wasm, and nothing a browser or WASI user can reach needs it.
With it gone, `go list -deps -tags slim` shows no `net/http`,
`crypto/tls` or `crypto/x509` at all.

What the no-http build needs, from the experiment:

1. `http_source.go` behind `!slim`, with a slim stub for `IsHTTPURL`
   (false), `HTTPURLExt` (""), `OpenHTTPStream`/`OpenHTTPFile` (an
   error naming the build), `ByteRange` and an `HTTPFile` whose
   methods are never reached. `from URL` in the browser then fails
   loudly with the reason instead of attempting a fetch.
2. `commands/serve_http.go` behind `!slim`, which means moving
   `stageChain`, `startStageChain`, `wait` and `killedByBrokenPipe`
   into an ungated file (`run` and `pipeline_doc.go` use them; they
   have nothing to do with HTTP). That is the only refactor, and it
   is a move, not a change.
3. Nothing in the playground or the explore page changes behaviour:
   both are already registered through `serve_slim.go`, and no
   example, test or codelab block fetches a URL in a browser.

Not worth doing, measured or reasoned:

- **`-trimpath`**: nothing (the paths are already stripped by `-w`).
- **Swapping `html/template` for `text/template`** alone: 1.5%, and
  the escaping is doing work in six templates. Only worth it as part
  of removing templates from `chart.go` altogether (string replacers
  over the embedded HTML, as the animate artifact already does with
  its JSON blobs), which the probe puts at ~3.7 MB raw at most; a
  later, separate change if the first cut is not enough.
- **A better compressor for the embed**: xz gives 4.31 MB and zstd
  4.73 MB against gzip's 6.47 MB, but the Go standard library decodes
  neither, and a pure-Go zstd decoder is a new dependency in the full
  binary to save 1.7 MB of embed. Pages serves whatever gzip it likes.
  Revisit only if the browser download is still the complaint after
  the http cut.
- **Dropping commands from the playground**: the registration drift
  gate exists because six commands silently vanished from the
  playground once; the dead-in-browser commands above cost little
  (ssh and catalog together are 2% of the commands package), and the
  explore page's pipeline bar is the whole CLI by design (DFC107).
- **Dropping codegen from the browser**: `generate go`/`sql`/`ssql`
  are playground features, and the codegen share of `commands` is
  about 40% of 2 MB of text; the no-http cut is four times larger for
  no feature loss.

## 6. Gates

A size that nobody watches grows (the ~3.4 MB in CLAUDE.md is a
measurement nobody re-ran). Two cheap, precise gates:

1. **Dependency gate**: a test in `cmd/ssql` that runs
   `go list -deps -tags slim` for `js/wasm` and fails if `net/http`,
   `crypto/tls`, `go/format` or any Arrow/Parquet/xlsx package is in
   the list, naming the importer chain (`go list -deps -f` with
   `.Imports`). This is the registration-drift idea applied to
   dependencies: the thing that drifts is named.
2. **Size budget**: `make explore-wasm` and `make playground` print
   the gzipped size and fail above a ceiling (a generous absolute
   number, never a stored baseline, as the scale gate does): 5.0 MB
   after the http cut, against 4.4 measured. The release checklist
   already runs both targets, so the ceiling is checked on every
   release without a new step.

Watch the first gate fail by reverting the `!slim` line on
`http_source.go`.

## 7. Plan

1. **The http cut** (one commit): gate `http_source.go` and
   `serve_http.go` behind `!slim`, move the stage-chain helpers to
   `commands/stage_chain.go`, add the two stubs, add the dependency
   gate and the size budget, refresh the embed with `make
   explore-wasm`, update `claude/wasm.md`'s sizes and CLAUDE.md's
   "~3.4MB". Expected: 6.47 → ~4.6 MB gzipped (the http cut alone,
   without the other two).
2. **The small ones** (same commit or the next): `nethttpomithttp2`
   is moot once http is out; skip `go/format` in slim (a `!slim`
   formatter and a slim identity, 3.7%); leave `html/template`.
3. **Shipping**: ask whether the goreleaser `ssql-slim_*` archives and
   the WASI tarball should keep being built (zero downloads over three
   releases, and WASI's JIT startup made the slim module matter when
   it was measured; nobody is measuring now). Not a size question; a
   release-surface one, for Ross.
4. **WebVM**: decide between the documented slim build and the full
   one that has shipped; the journal records the full one is the
   convention in practice. Fix the doc either way.

## 8. Decisions for Ross

Answered 2026-10-15 (plan mode): cut `from URL` from the browser (no
fetch-only source); drop both the slim archives and the WASI tarball;
WebVM ships the slim build and the doc stays as written.

**Built 2026-10-15**, same day: §7 steps 1–3 and the doc side of 4.
`http_source.go` and `serve_http.go` are `!slim` (the URL helpers in
`http_url.go`, the stage chain in `commands/stage_chain.go`, stubs in
`http_source_slim.go`); `go/format` behind `lib/gofmt_full.go` /
`gofmt_slim.go`; `TestSlimBuildDependencies` and
`scripts/wasm-budget.sh` (ceiling 5 MB) are the gates; goreleaser
builds the full binaries only. Measured: js/wasm 28.25 → 19.44 MB raw,
6.47 → 4.52 MB gzipped; linux/386 slim 9.6 MB; WASI 18.9 MB; native
slim 10.2 MB. Found on the way: `go test -tags slim .` had never
compiled (`readahead_test.go` reaches the gated Parquet reader), so
the Arrow, xlsx and three Parquet tests now carry `!slim` or a
`slimBuild` skip. The WebVM binary itself is rebuilt at the next
release.


- Is `from URL` in the browser a feature anyone wants? The cut assumes
  no (nothing exercises it; a browser has CORS in the way regardless).
  If yes, the alternative is a tiny fetch-only source behind the
  Fetch API with no `net/http` at all, which is a different, larger
  piece of work.
- Keep building the WASI and native-slim release artifacts?
- WebVM: slim as documented, or full as shipped?
