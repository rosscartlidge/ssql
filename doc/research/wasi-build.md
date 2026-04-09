# WASI Build — Portable WebAssembly Binary

## Overview

ssql can be compiled to WASI (WebAssembly System Interface), producing a single `.wasm` binary that runs on any platform with a WASI runtime — no Go installation or cross-compilation needed.

## Building

```bash
make wasi                    # builds ssql.wasm (slim, ~14MB)
```

Or manually:
```bash
GOOS=wasip1 GOARCH=wasm go build -tags slim -ldflags "-s -w" -o ssql.wasm ./cmd/ssql
```

The slim tag excludes arrow, parquet, and xlsx to keep the binary small.

## Running

Requires a WASI runtime. [wasmtime](https://wasmtime.dev/) is recommended:

```bash
# Install wasmtime
curl https://wasmtime.dev/install.sh -sSf | bash

# Run ssql
wasmtime ssql.wasm version
```

### File access

WASI sandboxes filesystem access. Grant access to directories with `--dir`:

```bash
wasmtime --dir=. ssql.wasm from data.csv | wasmtime ssql.wasm to table
wasmtime --dir=/data ssql.wasm from /data/events.csv | wasmtime ssql.wasm where -if age gt 25 | wasmtime ssql.wasm to table
```

### Environment variables

Pass env vars with `--env`:

```bash
wasmtime --env SSQLGO=1 --dir=. ssql.wasm from data.csv
```

### Full pipelines

Unix pipes work naturally — each stage is a separate WASI instance:

```bash
W="wasmtime --dir=. ssql.wasm"
$W from data.csv | $W where -if dept eq Engineering | $W group-by dept -count n | $W to table
```

## What works

- All commands: from, where, group-by, sort, window, join, merge, rename, cast, etc.
- File I/O (with `--dir`)
- Stdin/stdout piping
- Code generation: `generate go`, `generate sql`, `generate ssql`
- Tab completion (via `-complete` protocol)

## What doesn't work

- **SSH** — no network access in WASI
- **GPU** — no CUDA in WASI
- **from command** — no subprocess spawning
- **Multi-file pushdown** — spawns subprocesses internally

## Performance

WASI startup overhead dominates for small data. For larger datasets the gap narrows as processing dominates.

| Scenario | Native | WASI (wasmtime) | Ratio |
|----------|--------|-----------------|-------|
| 8 rows, 3 pipeline stages | 0.015s | 0.195s | 13x |

**Key insight:** Each pipe stage spawns a new WASI instance (~50ms startup each). A 3-stage pipeline pays ~150ms in startup alone. For interactive exploration of small files this is noticeable; for batch processing of large files it's negligible.

**Benchmarking tips:**
- Startup overhead is per-command, not per-record. Longer pipelines pay more.
- Use `time` to compare: `time wasmtime --dir=. ssql.wasm from big.csv | wasmtime ssql.wasm to csv > /dev/null`
- For best WASI performance, use wasmtime (JIT compiled) over wazero (interpreted).
- The slim build (14MB) loads faster than a full build would (~37MB).

**When native is better:** Interactive exploration, SSH pushdown, multi-file pushdown (subprocess spawning), anything requiring sub-50ms latency.

**When WASI is fine:** Batch processing, CI pipelines, sandboxed environments, anywhere you can't install native binaries, Docker+WASM deployments.

## Distribution

goreleaser builds `ssql_<version>_wasi.tar.gz` with every release, available on [GitHub Releases](https://github.com/rosscartlidge/ssql/releases).

## Other WASI runtimes

- **wasmtime** — recommended, fastest (https://wasmtime.dev/)
- **wasmer** — `wasmer ssql.wasm version`
- **wazero** — pure Go runtime, no CGO
- **Docker+WASM** — `docker run --runtime=io.containerd.wasmtime.v1 ssql.wasm`
