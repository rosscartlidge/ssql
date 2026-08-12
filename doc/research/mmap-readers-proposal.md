# mmap + MADV Readers Proposal

Reference: DFC090
Created: 2026-04-30
Last modified: 2026-08-11

[Back to Index](./README.md)

**Status:** Prototype + benchmarks, 2026-04-30. Not yet implemented in main.

This proposal evaluates replacing the file-I/O layer in ssql's
data readers with `mmap()` + `madvise(MADV_SEQUENTIAL)`, and quantifies
the actual win.

## TL;DR

- **`os.ReadFile` → `mmap`** on the parallel CSV/TSV slurp path
  (currently in `typed.ReadCSVParallel` / `ReadDelimParallel`):
  **1.7-1.9× faster slurp on a 1.23 GB file** (1.07 s → 0.56 s
  warm cache; 1.24 s → 0.72 s cold). The win is mostly avoiding
  the kernel→user-space copy and the matching 1.23 GB Go heap
  allocation.
- **`os.Open` → `mmap`** on the Arrow IPC reader (`ssql.ReadArrow`):
  ~7-8% faster on cold cache, no measurable difference on warm
  cache. Apache Arrow Go's IPC reader still copies into Go
  buffers regardless of source, so the bigger I/O wins are
  swallowed by internal allocation.
- **`madvise(MADV_SEQUENTIAL)`** adds nothing beyond mmap's
  default behaviour — Linux's kernel readahead already detects
  sequential access patterns on mmap'd regions.
- **`madvise(MADV_RANDOM)`** *hurts* by ~25% (disables readahead),
  confirming that the readahead is doing useful work either way.

The two findings together: **the original "mmap unlocks zero-copy
reads" pitch was mostly wrong** (Arrow / Parquet libraries copy
internally), but **the simpler "mmap eliminates the slurp copy
+ heap alloc"** is a real ~2× win for the parallel CSV/TSV path.

## Prototype + measurements

Bench machine: same workstation (Intel Xeon Gold 6154, 72 logical
cores, NVMe SSD). Two micro-benchmarks against the
`/home/rossc/csvs/` corpus:

### Bench A — Arrow IPC reader (1.23 GB CSV → 431 MB Arrow file)

The benchmark calls `ipc.NewFileReader` against either an
`*os.File` or a `bytes.NewReader(mmap'd)`, then iterates all 14.6 M
rows via `fr.Record(i)` / `rec.NumRows()`.

| Mode | Wall (cold, mean of 3) | Wall (warm, single) | Total Go allocations |
|---|---:|---:|---:|
| `os.Open` + `ipc.NewFileReader` | 4.03 s | 3.43 s | 2025 MB |
| mmap + `ipc.NewFileReader` (no advise) | **3.73 s** | 3.81 s | 2025 MB |
| mmap + `MADV_SEQUENTIAL` | 3.73 s | 4.11 s | 2025 MB |
| mmap + `MADV_RANDOM` | 5.13 s | 3.97 s | 2025 MB |

Observations:
- **All four configs allocate 2025 MB of Go memory.** That's the
  Apache Arrow Go IPC reader copying every column into Go-managed
  arrays — it doesn't matter whether the bytes came from `pread()`
  or from a memory-mapped buffer, the library does its own internal
  copy. So our nominally "zero-copy" mmap doesn't actually
  eliminate the dominant allocation.
- **Cold cache: ~7-8% faster** with mmap. Real but modest. The win
  is the kernel→user-space copy on `pread()` becoming a page fault
  (which still copies into Arrow buffers, just one less hop).
- **Warm cache: indistinguishable.** Within run-to-run noise.
- **`MADV_SEQUENTIAL` adds nothing** — Linux's default readahead
  already does the right thing on sequentially-faulted mmap regions.
- **`MADV_RANDOM` hurts measurably** — disables readahead, forcing
  per-page synchronous I/O. Useful negative result: confirms
  readahead is doing useful work in the default case.

### Bench B — Slurp + scan (`os.ReadFile` vs `mmap` on 1.23 GB CSV)

The benchmark loads the whole file into a `[]byte` (either
`os.ReadFile` or `mmap`), then iterates byte-by-byte counting
newlines. This models the prologue of `ReadCSVParallel` /
`ReadDelimParallel` which calls `os.ReadFile` then `bytes.IndexByte`-
scans for partition boundaries.

| Mode | Wall (warm, single) | Wall (cold, mean of 3) |
|---|---:|---:|
| `os.ReadFile` + scan | 1.07 s | 1.24 s |
| **mmap + scan** | **0.56 s (1.91× faster)** | **0.72 s (1.72× faster)** |
| mmap + `MADV_SEQUENTIAL` + scan | 0.59 s | 0.72 s |

Observations:
- **mmap halves the wall time** in both cache states.
- The win comes from two effects:
  1. **No kernel→user-space copy.** `os.ReadFile` does ~1.23 GB of
     `read()` syscall data movement; mmap aliases the pages into
     user space directly, with byte-scan reading them on demand
     (page-faulting cold pages, hitting cache for warm).
  2. **No 1.23 GB Go heap allocation.** mmap'd memory is
     kernel-managed; the Go runtime sees a `[]byte` header pointing
     at it, but never allocates the 1.23 GB on its heap. Saves
     allocation time, GC pressure, and (for the lifetime of the
     reader) Go heap residency.
- `MADV_SEQUENTIAL` gives no extra speedup beyond mmap's default —
  consistent with Bench A and with Linux kernel docs (default
  readahead is already sequential-friendly).

## What this means for ssql

### Where mmap is a clear win

**Parallel CSV/TSV readers** (`typed.ReadCSVParallel`,
`ReadDelimParallel` in `typed/io.go` and `typed/io_delim.go`).

Both currently start with `os.ReadFile(filename)` — slurps the
whole file into a Go `[]byte`. On the user-corpus 1.23 GB CSV,
that's the dominant cost of the parallel CSV path:

```
typed-parallel CSV total wall: 1.23 s on workstation
  - os.ReadFile slurp:       ~1.0 s of that (per Bench B warm)
  - newline pre-scan + parse: ~0.2 s
```

Replacing `os.ReadFile` with mmap should bring the slurp to
~0.56 s — saving ~440 ms on the headline number, dropping
`SSQLGO=parallel` CSV from 1.23 s to **~0.79 s wall** on this
machine.

For the multi-row-group Parquet path, the same `os.ReadFile`
pattern doesn't apply (Parquet uses `os.Open` + Arrow lib, like
the IPC reader). So mmap helps there at the modest 7-8% rate
from Bench A.

### Where mmap is a modest win

**Arrow IPC reader** (`ssql.ReadArrow`, `typed.ReadParquet*`).
~7-8% cold-cache improvement; nothing on warm cache.

The bottleneck isn't I/O — it's Apache Arrow Go's IPC reader
copying every column into Go-allocated buffers. Eliminating that
would need either:
1. A different Arrow library that supports buffer aliasing (the
   C++ library does this; Arrow Go currently does not, AFAICT).
2. Hand-rolled IPC parsing that aliases buffers from the mmap'd
   region. Massive scope, would essentially fork the IPC reader.

For v1, ship the modest win and document the residual ceiling.

### Where mmap can't help

**Streaming readers** that process data in chunks without ever
seeing the whole file (`ssql.ReadCSV`, `typed.ReadCSV` serial,
JSONL streaming). They use `bufio.NewReader` over `*os.File` and
process records as they're produced. mmap doesn't apply because
they never want the whole file at once — they're already
memory-efficient.

## Proposed surface

Single helper in `cmd/ssql/lib/mmap` (or new `internal/mmap`):

```go
// MmapReadOnly returns a []byte aliasing the file's contents,
// read-only. The returned cleanup function MUST be called when
// the data is no longer needed (typically via defer).
//
// On Linux/macOS uses unix.Mmap + MAP_SHARED + PROT_READ; the
// kernel's default readahead handles sequential access patterns.
// On Windows falls back to os.ReadFile (Windows mmap support is
// less portable; the binary still works, just without the win).
//
// SAFETY: the returned []byte is backed by kernel-managed memory.
// Slices into it remain valid until cleanup() is called. Strings
// derived via unsafe.String are also valid for that lifetime.
// After cleanup, ANY access to the slice or derived data is a
// segfault.
func MmapReadOnly(filename string) (data []byte, cleanup func() error, err error)
```

Usage:

```go
data, cleanup, err := mmap.MmapReadOnly(filename)
if err != nil { /* fall back to os.ReadFile */ }
defer cleanup()
// proceed as if data came from os.ReadFile
```

Linux/macOS implementation: `unix.Mmap(fd, 0, int(size), PROT_READ, MAP_SHARED)`.
Cleanup: `unix.Munmap(data)`.
Optional: `unix.Madvise(data, MADV_SEQUENTIAL)` is a no-op-or-worse
on this benchmark; we'd skip it by default. If a user has a
specific access pattern (random/willneed/dontneed), expose
`MmapReadOnlyWithAdvice(filename, advice int)`.

Windows fallback: `os.ReadFile`. cleanup is a no-op (Go GC reclaims).

## Implementation plan

### Phase A — slurp readers (the big win)

1. Add `cmd/ssql/lib/mmap/mmap.go` (linux/darwin) +
   `mmap_windows.go` (Windows fallback).
2. Replace `os.ReadFile(filename)` with `mmap.MmapReadOnly(filename)`
   in:
   - `typed/io_delim.go::ReadDelimParallel` (line ~248)
   - `typed/stream.go::ReadCSVParallel` (line ~430)
3. The cleanup function gets called from inside the `iter.Seq[T]`
   closure when iteration finishes. Each shard goroutine reads
   from a slice of the mmap'd buffer; the cleanup fires when the
   outer iter.Seq's caller has consumed all rows (the `range`
   loop terminates and the closure returns).
4. Aliased strings (the `splitLineAlias` zero-copy trick) are
   already safe under this model — strings alias into kernel-
   mapped memory which lives until cleanup, same lifetime as the
   iter.Seq.

Estimated effort: ~50 lines for the helper + ~10 lines per
caller. Maybe 2 hours including benchmarks.

Estimated speedup: **~440 ms saved on the 1.23 GB CSV workload**
(workstation: 1.23 s → ~0.79 s for `SSQLGO=parallel` CSV →
group-by). Other CPUs: roughly proportional to memory bandwidth.

### Phase B — IPC readers (the modest win)

Update `ssql.ReadArrow` and the `typed.ReadParquet*` family to
mmap the file and pass `bytes.NewReader(data)` to the Arrow IPC
reader. ~7-8% wall improvement on cold cache.

This phase is also where we should add `posix_fadvise(POSIX_FADV_DONTNEED)`
on close — for huge files this prevents the just-read pages from
evicting more useful pages elsewhere in the system. (Doesn't
help our wall time directly; helps the rest of the user's
workload.)

Estimated effort: ~20 lines per reader.

### Phase C — page-cache hygiene

After Phase A/B, both `mmap` and `os.ReadFile` paths benefit from
a `POSIX_FADV_DONTNEED` hint when the program is exiting (so the
OS knows to reclaim those pages first). This is a one-line
addition to the cleanup helper.

## Risks and edge cases

- **File modification mid-read.** `mmap` with `MAP_SHARED` exposes
  the live file. If another process truncates or modifies the
  file, the mmap'd view changes underfoot — undefined behaviour.
  Document: "Files passed to ssql parallel readers must not be
  modified during the read."
- **Sparse / hole-y files.** A sparse file mmap'd to its full
  reported size produces zero-pages for holes. Same as `read()`.
  No special handling needed.
- **Files larger than process address space.** 32-bit targets
  could in theory hit this; ssql doesn't run on 32-bit anymore
  (we assume `int = int64` in many places), so not a concern.
- **WSL / virtualised filesystems.** mmap on these has historically
  been slow; reproduce on user's setup before assuming Bench B's
  numbers transfer.
- **Empty files.** `mmap(fd, 0, 0, ...)` is undefined; the helper
  needs to special-case zero-byte files (return nil, no-op
  cleanup).
- **Cleanup races.** If the iter.Seq's caller forgets to consume
  all rows, the cleanup won't fire and the mmap leaks until the
  process exits. Documented in the helper's comment; acceptable
  for short-lived ssql programs (they exit on completion).

## Open questions

- **Per-file munmap timing.** When should cleanup fire? Options:
  a) at iter.Seq exit (current proposal — cleanly tied to consumer
     lifecycle, but holds the mapping until the consumer is done)
  b) at process exit only (simpler — relies on kernel cleanup;
     fine for short-lived programs)
  Recommend (a) — clean lifecycle, no surprises in long-running
  programs that read multiple files.
- **`MADV_DONTNEED` on close?** Yes for large files (>~100 MB) so
  we don't hold pages in cache after the read completes. Skip for
  small files (<10 MB) — the kernel's LRU handles them fine.
- **Should we expose this to library users?** Probably not as
  public API in v1. Internal helper only. If users have a need we
  can revisit.

## See also

- `golang.org/x/sys/unix` — `Mmap`, `Munmap`, `Madvise`, `Fadvise`
  bindings.
- `man 2 mmap`, `man 2 madvise`, `man 2 posix_fadvise` —
  syscall semantics.
- Apache Arrow Go IPC reader source — `ipc/file_reader.go` —
  the place where we'd need to fork to get true zero-copy IPC
  reads (Phase D, deferred).
- Bench code: `/tmp/mmap-bench/main.go` and `/tmp/mmap-slurp/main.go`
  (kept for reproducibility while iterating; not committed).

---

## Implementation results (2026-08-11 — SHIPPED, Phase A / CSV only)

Landed as `internal/mmap` (linux/darwin real mmap + MADV_DONTDUMP on
linux; os.ReadFile fallback elsewhere) wired into
`typed.ReadCSVParallel`. Lifetime is GC-driven (`runtime.AddCleanup` on
the *Mapped; shard closures `runtime.KeepAlive` it) — deferred unmap
costs address space only, since clean file-backed pages are
kernel-reclaimable regardless.

**Scope correction vs the plan: `ReadDelimParallel` deliberately NOT
converted.** Its `splitLineAlias` zero-copy strings alias the slurped
buffer; under os.ReadFile the string pointers keep the heap buffer
GC-alive for as long as any row survives (sort/group materialization
included). Under mmap the GC traces NOTHING into the mapping, so any
retained row after unmap would be a dangling string → SIGSEGV. The
proposal's "safe because the chunk lives for the duration of the
iter.Seq" claim was wrong for materializing pipelines. CSV is safe
because encoding/csv COPIES field strings (ReuseRecord reuses only the
record slice).

**Measured on the Ultra 9 275HX (24T), 1.15 GB / 50M-row CSV, warm
cache, medians of 3:**

| layer | os.ReadFile | mmap | delta |
|---|---:|---:|---|
| raw slurp + newline scan (1 thread) | 1.44 s | 1.22 s | **1.16–1.25× faster** |
| ReadCSVParallel + count (24T) | 3.44 s / **3.01 GB** RSS | 3.47 s / **2.07 GB** RSS | wall neutral, **−0.94 GB** |
| full pipeline (where-expr + group-by) | 3.73 s / 3.01 GB | 3.69 s / 2.21 GB | wall neutral, **−0.80 GB** |

**Honest revision of the headline claim:** the original 1.7–1.9× slurp
numbers were measured on a different (slower-memory) workstation; on
the Ultra 9 the raw-layer win is 1.16–1.25×, and in the full parallel
pipeline it vanishes into the 24-thread parse (page-fault first-touch
distributes across parsers roughly cancelling the saved copy). The
robust, machine-independent win is MEMORY: the file-sized heap
allocation (and its GC pressure) is gone — peak RSS drops by
approximately the input size. For a 1.15 GB input that is the
difference between ~3 GB and ~2 GB resident; proportionally more
important as inputs grow.

Phase B (Arrow IPC, ~7–8% cold-cache) and Phase C (fadvise hygiene)
remain unimplemented — revisit if profiles ever show them.
