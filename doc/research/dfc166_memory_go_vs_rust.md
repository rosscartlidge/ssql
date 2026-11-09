# Where the System Time Goes: Memory in the Generated Go and Rust Programs

Reference: DFC166
Created: 2026-11-10
Last modified: 2026-11-10

[Back to Index](./README.md)

## Summary

The same ssql pipeline was compiled to Go (`generate go -build`) and to Rust on Apache
DataFusion (`generate rust -build`). The Rust program spent 3.6 s of its 2.3 s wall in the
kernel (summed over its threads); the Go program spent 0.7 s. Nearly all of that kernel
time is **first-touch page faults**. Each time a program touches memory it has not used
before, the kernel takes a fault and hands it a zeroed page.

| | wall | user | sys | page faults | peak heap |
|---|---:|---:|---:|---:|---:|
| Go | **0.87 s** | 13.1 s | **0.74 s** | 84 k | **65 MB** |
| Rust (DataFusion), glibc malloc | 2.30 s | 7.5 s | 3.57 s | 1,311 k | 3,575 MB |
| Rust, mimalloc | 1.37 s | 9.1 s | 1.40 s | 10–48 k | 4,399 MB |

- **Go reuses a small heap.** Its garbage collector ran 188 times and kept the heap under
  about 35 MB, so the same memory is recycled while it is still mapped (and in cache). Most of
  Go's 1.2 GB RSS is the input file, memory-mapped from the page cache, which needs no
  zeroing. With the GC turned off (`GOGC=off`), Go grows to 2.3 GB of fresh memory and its
  kernel time more than doubles. The language is not the difference; the mechanism is the
  same.
- **The Rust program streams through 3.6 GB of fresh memory.** glibc's allocator backs it
  with 4 KB pages, one fault each: 1.3 million faults.
- **mimalloc** asks the kernel for 2 MB pages (transparent huge pages), so the same memory
  arrives in about 1/100th of the faults. Kernel time falls from 3.6 to 1.4 s and wall from
  2.3 to 1.4 s, at the price of more memory (4.4 GB).

## 1. The question

Ross ran the two compiled programs on the same input. The Rust program had a lot more system
time than the Go one, and he asked why.

## 2. The setup

The pipeline is DFC165's lookup. It reads a 1.2 GB, 14.6 M-row CSV, joins two small lookup
tables, keeps five columns and filters to 103,037 rows written as CSV:

```bash
ssql from csv shuffled.csv \
  | ssql join kind.csv -on a_kind kind -as kind_name a_kind_name - -on z_kind kind -as kind_name z_kind_name \
  | ssql join relationship.csv -using relationship \
  | ssql include a_name a_kind_name relationship_name z_kind_name z_name \
  | ssql where -if a_kind_name eq EK_PORT -if relationship_name eq RK_TRAVERSES \
  | ssql to csv graph.csv
```

- **Machine:** Intel Core Ultra 9 275HX (24 cores), 62 GB of RAM, Linux 6.17, glibc 2.39, and
  transparent huge pages in `madvise` mode (Ubuntu's default: a program gets 2 MB pages only
  if it asks for them).
- **Toolchains:** ssql v4.143.0 with the pinned Rust dependencies, Go 1.26.1, Rust 1.99.1, and
  DataFusion 55.2.0.
- **The binaries:** `generate go -build` (typed, parallel) and `generate rust -build` (fully
  optimised). The mimalloc variant is the same generated Rust with two added lines:

  ```rust
  #[global_allocator]
  static GLOBAL: mimalloc::MiMalloc = mimalloc::MiMalloc;
  ```

- **Measurement:** GNU `time` with a warm page cache. Each figure is from the fastest of five
  runs. Every program wrote the same set of rows (checked by hashing the sorted output).
- **Memory split:** peak anonymous memory (heap) and peak file-backed memory were sampled from
  `/proc/PID/status` every 50 ms. Huge-page use was sampled from `/proc/PID/smaps_rollup`.
  System calls were counted with `strace -f -c`.

## 3. The measurements

| program | wall | user | sys | peak RSS | of it heap | of it file | in 2 MB pages | minor faults |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Go | 0.87 | 13.05 | 0.74 | 1,231 MB | 65 MB | 1,146 MB | 0 | 84,387 |
| Go, `GOGC=off` | 0.78 | 9.82 | 1.73 | 3,513 MB | 2,317 MB | 1,190 MB | 0 | 617,147 |
| Rust, glibc | 2.30 | 7.53 | 3.57 | 3,286 MB | 3,575 MB | 53 MB | 0 | 1,311,474 |
| Rust, glibc, no trimming | 2.39 | 7.38 | 3.59 | 3,343 MB | — | — | — | 1,248,670 |
| Rust, glibc, `hugetlb=1` | 1.77 | 7.63 | 2.64 | 3,425 MB | — | — | 494 MB | 798,237 |
| Rust, mimalloc | 1.37 | 9.14 | 1.40 | 4,443 MB | 4,399 MB | 53 MB | 4,374 MB | 9,753 |

Times are in seconds. The sampled heap peak can exceed `time`'s peak RSS because they come
from different runs. mimalloc's fault count varied between 10 k and 48 k from run to run.

- **No trimming:** `GLIBC_TUNABLES=glibc.malloc.trim_threshold=4294967295:glibc.malloc.mmap_threshold=4294967295`.
  glibc no longer returns freed memory to the kernel or maps large blocks separately.
- **`hugetlb=1`:** `GLIBC_TUNABLES=glibc.malloc.hugetlb=1`. glibc asks for transparent huge
  pages.

The memory system calls, per run:

| program | `mmap` | `munmap` | `mprotect` | `madvise` | `read` | `futex` |
|---|---:|---:|---:|---:|---:|---:|
| Go | 177 | 23 | 54 | 2,395 | 27 | 61,025 |
| Rust, glibc | 315 | 296 | 19,377 | 109 | 150,304 | 18,764 |
| Rust, mimalloc | 173 | 103 | 147 | 4,169 | 150,305 | 3,336 |

Go's GC trace (`GODEBUG=gctrace=1`) shows 188 cycles in the run. The last was
`31->32->16 MB, 35 MB goal`.

## 4. What the numbers say

### 4.1 Go recycles a small heap

The generated Go program processes each shard's rows and lets them die before the next
batch. The GC runs often (188 cycles in under a second) and the heap goal stays near 35 MB.
Memory freed in one cycle is handed out again in the next: it is already mapped, so it costs
no fault and no zeroing, and it is probably still in cache.

The 1.15 GB of file-backed memory is the input. The typed CSV reader memory-maps the file
(`internal/mmap`) instead of reading it into buffers. Those pages are already in the page
cache, so mapping them costs a cheap fault per run of pages and no zeroing. Go's 84 k faults
are mostly these.

### 4.2 Without its GC, Go pays the same price

With `GOGC=off` nothing is reused: every allocation is fresh memory. The heap grows to 2.3 GB,
faults rise sevenfold to 617 k, and kernel time goes from 0.74 to 1.73 s. Wall improves
slightly, because the GC's own CPU (user time 13.0 → 9.8 s) is gone. DFC165 §4.1 saw the same
on this pipeline: turning the GC off cost sys time and memory. So the kernel time is not a
property of Rust or of Go. It is the price of touching fresh memory: 2–3 µs per 4 KB page
here, fault plus zeroing (1.9 µs for Go's extra faults, 2.7 µs for Rust's).

### 4.3 The Rust program streams through fresh memory, in 4 KB pages

DataFusion reads the CSV with `read()` into its own buffers (150 k reads of about 8 KB),
decodes each batch into new Arrow arrays, and builds new arrays for every join and
projection. Its peak heap is 3.6 GB, three times the input. Across 24 threads, glibc gives
each thread its own memory pool (arena). It grows a pool by enabling more of its reserved
region (19 k `mprotect` calls), and every new 4 KB page is a fault: 1.3 M faults, 3.6 s of
kernel time.

Telling glibc to stop returning memory to the kernel changed nothing (1.25 M faults). The
cost is not glibc giving memory back; it is how much memory the program touches, and in what
size of page.

Why a streaming pipeline that keeps 0.7% of its rows peaks at 3.6 GB of heap is an open
question. A likely candidate is batches buffered between DataFusion's parallel stages when
the reader runs ahead of the joins. DFC158 §3.8 measured DataFusion at 1.7 GB on a Parquet
scan.

### 4.4 Huge pages cut the fault count, not the memory

On this machine a program gets 2 MB pages only if it asks for them (`madvise`). mimalloc
asks, and 4.37 GB of its 4.40 GB heap was in huge pages. The same volume of memory arrives in
roughly 1/100th of the faults: about 10–48 k instead of 1.3 M. Kernel time fell to 1.4 s, the
remaining cost being mostly the zeroing of 4.4 GB. Wall fell from 2.30 to 1.37 s, a 40%
cut. mimalloc used more memory, not less: 4.4 GB against 3.6 GB.

glibc can be asked too (`hugetlb=1`). Then only 494 MB of its heap was in huge pages, faults
roughly halved, and wall fell to 1.77 s. Its per-thread pools grow in steps too small to fill
many 2 MB pages.

## 5. What this means

- **For a colleague comparing the two:** the Rust program does less CPU work (7.5 s of user
  time against Go's 13.1 s) but more memory work. Go's advantage here is that its runtime
  recycles a small heap and its reader maps the input file rather than copying it. Neither is
  specific to the language: Go without its GC behaves like the Rust program.
- **For `generate rust`:** mimalloc as the program's global allocator is a one-dependency,
  two-line change worth 40% of wall on this pipeline. It is not adopted yet:
  - **Coverage:** the bench's other pipelines need measuring, the windows and the large writes
    above all.
  - **Memory:** it costs memory (here +0.8 GB).
  - **Build:** it compiles a C library, so the machine needs a C compiler. cargo already needs
    one as a linker.
  - **The lock:** the pinned dependency lock (DFC157 §3.4) needs regenerating.
- **For generated Go:** nothing to do. The interesting control is `GOGC`. A larger goal
  trades memory for GC CPU, and the TODO's GC-floor item (DFC165 §4.1) is the place for it.
- **Measurement advice:** when two programs differ in system time, count the minor page
  faults (`/usr/bin/time -f %R`) and split RSS into anonymous and file-backed memory
  (`/proc/PID/status`) before looking anywhere else.

## 6. Limits

- **One pipeline:** a streaming lookup that keeps 0.7% of its rows. A pipeline that holds its
  rows (a sort, a window) has a large live heap in every runtime, and reuse matters less.
- **One machine:** with THP in `madvise` mode. With THP set to `always`, glibc's numbers
  should move towards mimalloc's. That was not measured.
- **Variation:** five runs per row. mimalloc's fault count varied from 10 k to 48 k between
  runs; the wall times varied by about 5%.

## 7. Reproducing it

```bash
ssql generate go   -build go.bin   -pipeline "$PIPELINE"
ssql generate rust -build rust.bin -pipeline "$PIPELINE"
/usr/bin/time -f "wall %e user %U sys %S rss %MKB minflt %R" ./go.bin
/usr/bin/time -f "wall %e user %U sys %S rss %MKB minflt %R" ./rust.bin
GOGC=off ./go.bin                                      # Go without its GC
GLIBC_TUNABLES=glibc.malloc.hugetlb=1 ./rust.bin      # glibc asking for huge pages
grep -E 'RssAnon|RssFile' /proc/$PID/status            # heap against mapped files, while running
strace -f -c ./rust.bin                                # system call counts
```

## 8. References

- [DFC165](./dfc165_lookup_pipeline_cpu_typed_vs_duckdb.md) §4.1: the same pipeline's GC
  measurements, where `GOGC=off` raised Go's sys time.
- [DFC157](./dfc157_generate_rust.md): `generate rust`; §3.4 has the pinned dependencies and
  the program's opt-level.
- [DFC158](./dfc158_performance_measured_fixed_next.md) §3.8: memory of the typed Parquet
  readers against DataFusion.
