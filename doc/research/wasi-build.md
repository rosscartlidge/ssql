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
- **Multi-file pushdown** — spawns subprocesses internally

## Performance

WASI startup overhead dominates for small data. For larger datasets the ratio improves significantly as processing dominates startup cost.

### Benchmarks (1M rows, 33MB CSV, 5 columns)

| Scenario | Native | WASI (wasmtime) | Ratio |
|----------|--------|-----------------|-------|
| 8 rows, 3 stages | 0.015s | 0.20s | 13x |
| 1M rows, passthrough (read + write) | 1.60s | 5.53s | 3.5x |
| 1M rows, filter (`where -if value gt 9900`) | 1.28s | 7.69s | 6x |
| 1M rows, filter + group-by + sort (5 stages) | 1.56s | 5.38s | 3.4x |

**Key insight:** For small data, startup overhead per WASI instance (~50ms each) dominates. For 1M+ rows, the ratio drops to **3-6x** — practical for batch workloads. The aggregation pipeline (3.4x) is faster than the filter pipeline (6x) because aggregation reduces output size, so downstream stages process less data.

### AOT compilation (precompiled WASM)

Wasmtime supports ahead-of-time compilation, which eliminates JIT startup overhead:

```bash
wasmtime compile ssql.wasm -o ssql.cwasm
wasmtime --allow-precompiled --dir=. ssql.cwasm from data.csv | wasmtime --allow-precompiled ssql.cwasm to table
```

AOT dramatically improves small-data latency:

| Mode | Small (8 rows, 3 stages) | 1M rows (5 stages) |
|------|--------------------------|---------------------|
| Native | 0.015s | 1.36s |
| WASI JIT | 0.195s (13x) | 5.51s (4.1x) |
| **WASI AOT** | **0.018s (1.2x native)** | 6.95s (5.1x) |

**Key insight:** AOT reduces small-data startup from 195ms to 18ms — nearly native speed. This makes AOT-compiled WASI viable for interactive exploration. The 1M row case is slightly slower than JIT because wasmtime's JIT can optimize hot loops at runtime.

**Recommendation:** Use AOT for interactive use (many small commands). Use JIT for batch processing of large files.

The `.cwasm` file is platform-specific (unlike `.wasm` which is portable). Generate it once per machine:
```bash
wasmtime compile ssql.wasm -o ssql.cwasm  # run once after download
```

### Benchmarking tips

- Startup overhead is per-command, not per-record. Longer pipelines pay more startup.
- Use `time` to compare: `time wasmtime --dir=. ssql.wasm from big.csv | wasmtime ssql.wasm to csv > /dev/null`
- For best WASI performance, use wasmtime (JIT compiled) over wazero (interpreted).
- The slim build (14MB) loads faster than a full build would (~37MB).

### Full comparison: CLI vs generated Go vs WASI (1M rows, filter + group-by + sort)

| Mode | Time | vs Native CLI |
|------|------|--------------|
| Generated Go (native) | 0.98s | 1.4x faster |
| CLI pipeline (native) | 1.36s | 1x baseline |
| **Generated Go (WASI AOT)** | **3.82s** | **2.8x** |
| Generated Go (WASI JIT) | 4.23s | 3.1x |
| CLI pipeline (WASI JIT) | 5.39s | 4.0x |
| CLI pipeline (WASI AOT) | 5.56s | 4.1x |

**Insights:**
- **Generated Go native is fastest** — single process, no pipe overhead. The `generate go` → `go build` workflow produces the best performance.
- **Generated Go AOT is the fastest WASI option** (3.82s) — single process with no JIT or pipe overhead. AOT eliminates the 54MB module JIT cost that made it slowest in JIT mode.
- **CLI WASI JIT and AOT are similar** for large data (~5.4s) — startup is a small fraction of total time. AOT helps more for small data where startup dominates.
- **For WASI batch processing:** use `generate go` → WASI AOT (2.8x native). For interactive use: CLI with AOT precompiled modules (near-native startup).

### When to use native vs WASI

**Native is better for:** Interactive exploration, SSH pushdown, multi-file pushdown (subprocess spawning), anything requiring sub-50ms latency, large-scale batch processing where 3x matters.

**WASI is fine for:** CI pipelines, sandboxed environments, anywhere you can't install native binaries, Docker+WASM deployments, cross-platform distribution, moderate batch sizes where 3-6x overhead is acceptable.

## Distribution

goreleaser builds `ssql_<version>_wasi.tar.gz` with every release, available on [GitHub Releases](https://github.com/rosscartlidge/ssql/releases).

## Other WASI runtimes

- **wasmtime** — recommended, fastest (https://wasmtime.dev/)
- **wasmer** — `wasmer ssql.wasm version`
- **wazero** — pure Go runtime, no CGO
- **Docker+WASM** — `docker run --runtime=io.containerd.wasmtime.v1 ssql.wasm`
