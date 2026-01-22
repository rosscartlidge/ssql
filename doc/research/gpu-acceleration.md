# GPU Acceleration for ssql: Research and Implementation Plan

## Experimental Findings (January 2025)

**We implemented GPU acceleration and tested it. Here's what we learned:**

### Reality Check: Initial Projections Were Too Optimistic

The original projections below (100-1000x speedups for aggregations) assumed:
1. Data already in GPU-friendly format
2. Compute-bound operations
3. No Record extraction overhead

**Actual findings from implementation:**

| Operation | Projected Speedup | Actual Speedup | Why |
|-----------|------------------|----------------|-----|
| Simple Sum | 50-200x | **1x (no benefit)** | Memory-bound, transfer dominates |
| Simple Avg | 50-200x | **1x (no benefit)** | Memory-bound, transfer dominates |
| Batched Sums | 10-50x | **<1x (slower)** | Copy overhead to flatten data |
| Filter+Sum | 20-50x | **5-7x** | Data stays on GPU between ops |
| FFT | 10-100x | **28-54x** | cuFFT beats Cooley-Tukey for large signals |
| Convolution | 10-100x | **4-500x** | O(n*m) - GPU wins massively |
| Correlation | 10-100x | **4-500x** | Uses convolution internally |

### FFT: GPU Wins for Large Signals

Benchmarks show GPU (cuFFT) significantly outperforms CPU (Cooley-Tukey) for signals >= 16K samples:

| Signal Size | CPU (Cooley-Tukey) | GPU (cuFFT) | Speedup | Winner |
|-------------|-------------------|-------------|---------|--------|
| 1K | 64µs | 333µs | 0.19x | CPU |
| 4K | 279µs | 557µs | 0.50x | CPU |
| 16K | 1.4ms | 1.1ms | **1.3x** | GPU |
| 64K | 7.8ms | 1.8ms | **4.2x** | GPU |
| 256K | 29ms | 2ms | **14x** | GPU |
| 1M | 125ms | 4.4ms | **28x** | GPU |
| 4M | 564ms | 14ms | **40x** | GPU |
| 16M | 2.5s | 68ms | **36x** | GPU |
| 64M | 10.7s | 198ms | **54x** | GPU |

**Conclusion:** GPU FFT enabled for signals >= 16K samples (crossover point).

### Convolution: GPU Dominates

Unlike FFT, convolution has no O(n log n) CPU algorithm for general kernels. GPU wins at **all sizes**:

| Configuration | CPU | GPU | Speedup |
|--------------|-----|-----|---------|
| 100K signal, 64-pt kernel | 11ms | 2.6ms | **4x** |
| 100K signal, 256-pt kernel | 51ms | 684µs | **74x** |
| 100K signal, 1K kernel | 207ms | 1ms | **204x** |
| 1M signal, 64-pt kernel | 112ms | 5.6ms | **20x** |
| 1M signal, 256-pt kernel | 474ms | 5.8ms | **82x** |
| 1M signal, 1K kernel | 2.0s | 8.2ms | **240x** |
| 1M signal, 4K kernel | 7.9s | 16ms | **495x** |
| 10M signal, 64-pt kernel | 1.1s | 55ms | **20x** |
| 10M signal, 256-pt kernel | 4.7s | 62ms | **76x** |
| 10M signal, 1K kernel | ~10s | 86ms | **119x** |

**Key insight:** Speedup increases with kernel size. Large kernels see 200-500x improvement.

### Correlation: Same as Convolution

Cross-correlation uses convolution internally: `Correlate(a, b) = Convolve(a, reverse(b))`

Same GPU benefits apply - tested 100K signal with 1K pattern:
- CPU: ~207ms (estimated)
- GPU: 1.3ms
- Speedup: **~160x**

### The Transfer Overhead Problem

For 1M float64 values (8MB):
```
PCIe transfer to GPU:    ~1-2ms
GPU sum computation:     ~0.1ms
PCIe transfer from GPU:  ~0.01ms
Total GPU time:          ~2-3ms

CPU sum time:            ~2ms (no transfer)
```

**GPU loses because transfer time > compute time for simple operations.**

### The Record Extraction Problem

ssql's `Record` type is `map[string]any`. Extracting values for GPU requires:
```go
values := make([]float64, len(records))
for i, r := range records {
    values[i] = ssql.GetOr(r, "price", 0.0)  // Map lookup per record
}
```

This extraction is CPU-bound and often takes longer than the actual aggregation.

### What Actually Works

1. **FFT (28-54x speedup for >= 16K samples)**
   - cuFFT highly optimized for GPU
   - Crossover at ~16K samples
   - 1M samples: 5ms GPU vs 125ms CPU

2. **Convolution/Correlation (4-500x speedup)**
   - O(n*m) algorithm - compute-bound
   - GPU wins at all sizes, even small kernels
   - Speedup scales with kernel size

3. **Chained Operations (5-7x speedup)**
   - `FilterThenSum`: Filter + aggregate in one GPU pass
   - Data stays on GPU between operations
   - Transfer cost amortized across multiple operations

4. **Arrow Columnar Format (not yet implemented)**
   - Bypasses Record extraction
   - Data already contiguous
   - Direct GPU transfer possible

### What Doesn't Work

1. **Simple aggregations** - Transfer overhead > compute
2. **Small datasets** - GPU initialization overhead dominates
3. **Small FFTs (<16K samples)** - Transfer overhead exceeds compute benefit

### Current Implementation Status

```
gpu/
├── sum.cu           # CUDA kernels (sum, filter, FFT, convolution)
├── gpu.go           # Go wrappers (build tag: gpu)
├── gpu_stub.go      # Stubs for non-GPU builds
├── gpu_test.go      # Tests and benchmarks
├── Makefile         # Builds libssqlgpu.so
└── libssqlgpu.so    # Compiled library
```

**Available Functions:**
```go
// Basic operations (limited benefit - transfer overhead dominates)
gpu.SumFloat64(data []float64) (float64, error)
gpu.SumInt64(data []int64) (int64, error)

// Chained operations (good benefit - data stays on GPU)
gpu.FilterThenSum(data []float64, threshold float64) (float64, error)

// FFT operations (used for signals >= 16K - 28-54x faster)
gpu.FFTMagnitude(data []float64) ([]float64, error)
gpu.FFTMagnitudePhase(data []float64) ([]float64, []float64, error)

// Convolution operations (excellent benefit - always use GPU)
gpu.ConvolveDirect(signal, kernel []float64) ([]float64, error)
gpu.ConvolveFFT(signal, kernel []float64) ([]float64, error)  // For very large kernels
```

### Current GPU Usage in ssql

| Operation | GPU Usage | Threshold | Reason |
|-----------|-----------|-----------|--------|
| FFT | **When large** | signal ≥ 16K | 28-54x faster |
| Convolution | **Always** | kernel ≥ 16 | 4-500x faster |
| Correlation | **Always** | kernel ≥ 16 | Uses convolution |
| Aggregations | **Never** | - | Transfer overhead dominates |

### Recommendations

**Do pursue:**
1. FFT operations for large signals (current implementation working well)
2. Convolution/correlation operations (current implementation working well)
3. Arrow columnar integration (bypass Record extraction)
4. Pipeline fusion (compile multiple ops to single GPU kernel)

**Don't pursue:**
1. Simple aggregations (sum, avg, count, min, max) - transfer overhead dominates
2. Single-operation GPU calls for memory-bound operations
3. Small signal FFT (<16K samples) - CPU is faster

### Future Priorities

1. **Arrow columnar reader** - direct GPU transfer without Record extraction
2. **Pipeline fusion** - compile multiple operations to single GPU kernel
3. **Batched convolution** - multiple convolutions in single GPU call

---

## Original Planning Document

The sections below contain the original theoretical analysis. The experimental findings above supersede the projected speedups for simple aggregations.

---

## Executive Summary

This document explores GPU acceleration for ssql data processing pipelines. With modern GPUs like the RTX 5090 offering 33 TFLOPs of compute, there's potential for **100-1000x speedups** over current CPU-based processing for suitable workloads.

**Key findings:**
- Numeric filtering, aggregation, and sorting are excellent GPU candidates (100x+ potential)
- Joins can benefit significantly (20-50x potential)
- String operations remain CPU-bound
- Columnar storage is prerequisite for GPU efficiency
- Binary serialization (Cap'n Proto, Arrow) essential for I/O performance
- Implementation effort: 3-6 months for production-ready system

**Recommendation:** Pursue a phased approach starting with columnar storage and binary formats, which provide immediate CPU benefits before GPU work begins.

---

## Table of Contents

1. [GPU Computing Fundamentals](#1-gpu-computing-fundamentals)
2. [Operation Suitability Analysis](#2-operation-suitability-analysis)
3. [Columnar Storage Design](#3-columnar-storage-design)
4. [Binary Serialization Formats](#4-binary-serialization-formats)
5. [Implementation Architecture](#5-implementation-architecture)
6. [Performance Projections](#6-performance-projections)
7. [Implementation Phases](#7-implementation-phases)
8. [Risk Assessment](#8-risk-assessment)
9. [Conclusion](#9-conclusion)

---

## 1. GPU Computing Fundamentals

### 1.1 Why GPUs Are Fast

GPUs achieve massive parallelism through:

| Characteristic | CPU | GPU (RTX 5090) |
|----------------|-----|----------------|
| Cores | 8-32 | 21,760 CUDA cores |
| Clock speed | 3-5 GHz | 2.0-2.5 GHz |
| Memory bandwidth | 50-100 GB/s | 1,792 GB/s |
| FP32 performance | 0.5-1 TFLOP | 33 TFLOPs |
| Memory | System RAM | 32 GB VRAM |
| Latency per op | Low | High |
| Throughput | Medium | Extreme |

### 1.2 GPU Programming Model

```
┌─────────────────────────────────────────────────────────────┐
│                        GPU Architecture                      │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐           │
│  │ SM 0    │ │ SM 1    │ │ SM 2    │ │ SM N    │  ...      │
│  │ 128 cores│ │ 128 cores│ │ 128 cores│ │ 128 cores│         │
│  └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘           │
│       │          │          │          │                    │
│       └──────────┴──────────┴──────────┘                    │
│                         │                                    │
│                    Global Memory (32 GB)                     │
└─────────────────────────────────────────────────────────────┘

Execution model:
- Kernel: Function that runs on GPU
- Thread: Single execution unit
- Block: Group of threads (up to 1024)
- Grid: Collection of blocks

Example: Filter 10M records
- 10M threads, organized as 10,000 blocks × 1,024 threads
- Each thread evaluates ONE predicate
- All 10M evaluations happen "simultaneously"
```

### 1.3 When GPUs Win vs Lose

**GPUs excel at:**
- Same operation on millions of elements (SIMD)
- Predictable memory access patterns
- Floating-point arithmetic
- Parallel reductions (sum, min, max)
- Embarrassingly parallel problems

**GPUs struggle with:**
- Branching/conditional logic (thread divergence)
- Variable-length data (strings)
- Random memory access (hash tables)
- Small datasets (transfer overhead dominates)
- Sequential dependencies

---

## 2. Operation Suitability Analysis

### 2.1 ssql Operations Ranked by GPU Potential

| Operation | GPU Suitability | Speedup Potential | Notes |
|-----------|-----------------|-------------------|-------|
| **Where (numeric)** | ⭐⭐⭐⭐⭐ | 100-500x | Perfect parallel predicate |
| **Select (numeric transform)** | ⭐⭐⭐⭐⭐ | 100-500x | Pure map operation |
| **Aggregations (sum, avg, min, max)** | ⭐⭐⭐⭐⭐ | 50-200x | Parallel reduction |
| **Sort (numeric)** | ⭐⭐⭐⭐ | 50-100x | Radix/bitonic sort |
| **Group By (numeric keys)** | ⭐⭐⭐⭐ | 30-80x | Parallel hashing |
| **Join (hash, numeric keys)** | ⭐⭐⭐ | 20-50x | Memory-bound |
| **Distinct (numeric)** | ⭐⭐⭐ | 20-50x | Parallel hash set |
| **Where (string contains)** | ⭐⭐ | 5-15x | Variable length, branching |
| **Sort (string)** | ⭐⭐ | 5-10x | Comparison overhead |
| **String transforms** | ⭐ | 2-5x | Poor GPU fit |
| **Regex matching** | ⭐ | 1-3x | Heavy branching |

### 2.2 Detailed Analysis by Operation

#### Where (Numeric Filtering)

```
CPU approach (current):
  for each record:        // Sequential
    if age > 30:          // Branch
      emit record

GPU approach:
  // Phase 1: Parallel predicate evaluation
  kernel<<<10000, 1024>>>(ages, mask, 30)
  // Each of 10M threads: mask[i] = ages[i] > 30

  // Phase 2: Parallel compaction (stream compaction)
  compact<<<...>>>(data, mask, output)
  // Prefix sum + scatter

Performance:
  CPU: 10M records × 10ns = 100ms
  GPU: 10M records / 10000 parallelism × overhead = 0.5ms
  Speedup: ~200x
```

#### Aggregation (Sum, Count, Avg)

```
CPU approach:
  total = 0
  for each value:         // Sequential
    total += value        // Data dependency

GPU approach (parallel reduction):
  Step 1: 10M → 5M (each thread adds 2 values)
  Step 2: 5M → 2.5M
  Step 3: 2.5M → 1.25M
  ...
  Step 24: 2 → 1

  Total steps: log2(10M) ≈ 24 steps
  Each step is fully parallel

Performance:
  CPU: 10M additions × 1ns = 10ms
  GPU: 24 steps × 0.01ms = 0.24ms
  Speedup: ~40x (memory-bound, not compute-bound)
```

#### Hash Join

```
CPU approach:
  // Build hash table
  for each right record:
    hash_table[key] = record

  // Probe
  for each left record:
    if key in hash_table:
      emit merge(left, right)

GPU approach:
  // Phase 1: Parallel hash table build
  // - Compute hashes in parallel
  // - Use atomic operations for insertion
  // - Or use cuckoo hashing (GPU-friendly)

  // Phase 2: Parallel probe
  // - Each thread probes one left record
  // - Output positions computed via prefix sum
  // - Parallel scatter to output

Challenges:
  - Hash collisions cause thread divergence
  - Output size unknown (need two-pass or overallocation)
  - Memory access patterns less predictable

Performance:
  CPU: O(N + M) but sequential
  GPU: O((N + M) / parallelism) with overhead
  Speedup: 20-50x (memory-bound)
```

#### Sort

```
GPU-friendly sorting algorithms:

1. Radix Sort (best for integers/floats)
   - Process bits from LSB to MSB
   - Each pass is parallel scatter
   - O(k × n) where k = bits
   - 10M int64s: ~32 passes, each parallel
   - Speedup: 50-100x

2. Bitonic Sort (good for any comparable)
   - Recursively build bitonic sequences
   - O(log²n) parallel steps
   - Each step compares n/2 pairs
   - Speedup: 30-50x

3. Merge Sort (GPU variant)
   - Parallel merge of sorted chunks
   - Good for very large datasets
   - Speedup: 20-40x

Recommendation: Radix sort for numeric, bitonic for general
```

### 2.3 String Operations Analysis

Strings are problematic for GPUs because:

1. **Variable length** - Can't predict memory access
2. **Branching** - Character-by-character comparison
3. **Memory inefficient** - Pointer chasing for string data

Mitigation strategies:

```
Strategy 1: Dictionary Encoding
  Original: ["apple", "banana", "apple", "cherry", "banana"]
  Dictionary: {0: "apple", 1: "banana", 2: "cherry"}
  Encoded: [0, 1, 0, 2, 1]  // Now it's integers!

  GPU can filter/group on integer codes
  String operations only on small dictionary (CPU)

Strategy 2: Fixed-Width Strings
  Pad all strings to max length (e.g., 64 bytes)
  GPU processes fixed-size chunks
  Wasteful for short strings, impossible for long

Strategy 3: Hybrid CPU/GPU
  String predicates: CPU
  Numeric predicates: GPU
  Combine masks
```

---

## 3. Columnar Storage Design

### 3.1 Why Columnar is Essential

Current ssql storage (row-oriented):
```go
// Array of Structs (AoS)
type Record struct {
    Name   string
    Age    int64
    Salary float64
    Dept   string
}
records := []Record{
    {"Alice", 30, 75000, "Eng"},
    {"Bob", 25, 65000, "Sales"},
    // ...
}

Memory layout:
[Alice|30|75000|Eng][Bob|25|65000|Sales][...]
     ^--- Each record is contiguous
```

Problems for GPU:
- To filter by Age, must skip over Name, Salary, Dept
- Non-coalesced memory access (threads read scattered locations)
- Poor cache utilization

Columnar storage (Struct of Arrays - SoA):
```go
type Table struct {
    Names   []string
    Ages    []int64
    Salaries []float64
    Depts   []string
}

Memory layout:
Names:    [Alice][Bob][Carol][...]
Ages:     [30][25][35][...]
Salaries: [75000][65000][85000][...]
Depts:    [Eng][Sales][Eng][...]
          ^--- Each column is contiguous
```

Benefits for GPU:
- Filter by Age: read only Ages column
- Coalesced access: adjacent threads read adjacent memory
- Better compression: same-type values together
- SIMD-friendly: process 8 int64s with one instruction

### 3.2 Columnar Type System

```go
// Column types matching ssql Value constraint
type Column interface {
    Len() int
    Type() ColumnType
    Slice(start, end int) Column
    // GPU-specific
    ToDevice() DeviceColumn
    FromDevice(DeviceColumn)
}

type Int64Column struct {
    data     []int64
    nullMask []uint64  // Bit vector for NULL handling
}

type Float64Column struct {
    data     []float64
    nullMask []uint64
}

type StringColumn struct {
    // Dictionary-encoded for GPU efficiency
    dictionary []string      // Unique strings
    codes      []int32       // Index into dictionary
    nullMask   []uint64
}

type BoolColumn struct {
    data []uint64  // Bit-packed for efficiency
}

type TimestampColumn struct {
    data []int64  // Unix nanos
    nullMask []uint64
}

// Table is a collection of named columns
type Table struct {
    schema  Schema
    columns map[string]Column
    nrows   int
}
```

### 3.3 Row ↔ Column Conversion

```go
// Convert row-oriented Records to columnar Table
func RecordsToTable(records iter.Seq[Record]) *Table {
    // First pass: determine schema and count
    var schema Schema
    var count int
    for r := range records {
        if schema == nil {
            schema = inferSchema(r)
        }
        count++
    }

    // Allocate columns
    table := NewTable(schema, count)

    // Second pass: populate columns
    i := 0
    for r := range records {
        for field, col := range table.columns {
            col.Set(i, r.Get(field))
        }
        i++
    }

    return table
}

// Convert columnar Table back to row iterator
func TableToRecords(table *Table) iter.Seq[Record] {
    return func(yield func(Record) bool) {
        for i := 0; i < table.nrows; i++ {
            r := MakeMutableRecord()
            for field, col := range table.columns {
                r.Set(field, col.Get(i))
            }
            if !yield(r.Freeze()) {
                return
            }
        }
    }
}
```

### 3.4 Memory Layout Optimization

```
Optimal GPU memory layout:

┌─────────────────────────────────────────────────────────────┐
│                    GPU Global Memory                         │
├─────────────────────────────────────────────────────────────┤
│  Ages Column (aligned to 256 bytes)                         │
│  [30][25][35][28][42][31][...]                              │
│   ↑   ↑   ↑   ↑   ↑   ↑                                     │
│   Thread 0-7 read these 8 values in ONE memory transaction  │
├─────────────────────────────────────────────────────────────┤
│  Salaries Column (aligned to 256 bytes)                     │
│  [75000.0][65000.0][85000.0][...]                           │
├─────────────────────────────────────────────────────────────┤
│  Dept Codes Column (dictionary-encoded)                     │
│  [0][1][0][2][1][0][...]  // 0=Eng, 1=Sales, 2=HR           │
└─────────────────────────────────────────────────────────────┘

Memory access pattern for "WHERE age > 30":
- 32 threads in a warp read 32 consecutive int64 values
- Single 256-byte memory transaction
- 100% memory bandwidth utilization
```

---

## 4. Binary Serialization Formats

### 4.1 Why CSV/JSON Are Bottlenecks

Current I/O performance:

| Format | Parse Speed | Serialize Speed | Size (10M records) |
|--------|-------------|-----------------|-------------------|
| CSV | 50-100 MB/s | 100-200 MB/s | 500 MB |
| JSON | 30-80 MB/s | 50-100 MB/s | 800 MB |
| JSONL | 40-90 MB/s | 60-120 MB/s | 750 MB |

For 10M records with 10 fields:
- CSV read: 5-10 seconds
- Processing: 0.1 seconds (GPU)
- **I/O is 50-100x slower than GPU compute!**

### 4.2 Binary Format Comparison

| Format | Read Speed | Write Speed | Size | Zero-Copy | GPU-Ready |
|--------|------------|-------------|------|-----------|-----------|
| Cap'n Proto | 2-5 GB/s | 2-5 GB/s | 300 MB | ✅ Yes | ⚠️ Partial |
| FlatBuffers | 2-5 GB/s | 1-3 GB/s | 320 MB | ✅ Yes | ⚠️ Partial |
| Apache Arrow | 3-8 GB/s | 2-4 GB/s | 280 MB | ✅ Yes | ✅ Yes |
| Protocol Buffers | 0.5-1 GB/s | 0.3-0.5 GB/s | 250 MB | ❌ No | ❌ No |
| MessagePack | 0.5-1 GB/s | 0.5-1 GB/s | 350 MB | ❌ No | ❌ No |

### 4.3 Cap'n Proto Analysis

**Advantages:**
- Zero-copy deserialization (data usable without parsing)
- Memory-mapped file support
- Efficient random access
- Strong schema evolution
- Go support via `capnproto.org/go/capnp/v3`

**Schema example:**
```capnp
@0x85150b117366d14b;

struct Record {
    name @0 :Text;
    age @1 :Int64;
    salary @2 :Float64;
    dept @3 :Text;
    active @4 :Bool;
}

struct Table {
    schema @0 :List(Field);
    records @1 :List(Record);
}

struct Field {
    name @0 :Text;
    type @1 :FieldType;
}

enum FieldType {
    int64 @0;
    float64 @1;
    string @2;
    bool @3;
    timestamp @4;
}
```

**Go usage:**
```go
import "capnproto.org/go/capnp/v3"

// Write
msg, seg, _ := capnp.NewMessage(capnp.SingleSegment(nil))
table, _ := NewRootTable(seg)
// ... populate table

// Read (zero-copy!)
data, _ := os.ReadFile("data.capnp")
msg, _ := capnp.Unmarshal(data)
table, _ := ReadRootTable(msg)
// table.Records() returns view into original bytes
```

**Limitation for GPU:** Cap'n Proto is row-oriented. Need to transpose to columnar for GPU.

### 4.4 Apache Arrow Analysis

**Advantages:**
- Designed for columnar data (perfect for GPU!)
- Native GPU support via Arrow CUDA
- Zero-copy between processes
- Language-agnostic (Python, R, Java, Go interop)
- Compression support (LZ4, ZSTD)
- Go support via `github.com/apache/arrow/go`

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                    Arrow Columnar Format                     │
├─────────────────────────────────────────────────────────────┤
│  Schema: [age: int64, salary: float64, name: string]        │
├─────────────────────────────────────────────────────────────┤
│  RecordBatch 0 (rows 0-999,999)                             │
│    age:    [buffer: 8MB contiguous int64]                   │
│    salary: [buffer: 8MB contiguous float64]                 │
│    name:   [offsets: 4MB][data: variable]                   │
├─────────────────────────────────────────────────────────────┤
│  RecordBatch 1 (rows 1M-1.999M)                             │
│    ...                                                       │
└─────────────────────────────────────────────────────────────┘
```

**Go usage:**
```go
import (
    "github.com/apache/arrow/go/v14/arrow"
    "github.com/apache/arrow/go/v14/arrow/array"
    "github.com/apache/arrow/go/v14/arrow/memory"
    "github.com/apache/arrow/go/v14/arrow/ipc"
)

// Create schema
schema := arrow.NewSchema([]arrow.Field{
    {Name: "age", Type: arrow.PrimitiveTypes.Int64},
    {Name: "salary", Type: arrow.PrimitiveTypes.Float64},
    {Name: "name", Type: arrow.BinaryTypes.String},
}, nil)

// Build record batch
pool := memory.NewGoAllocator()
b := array.NewRecordBuilder(pool, schema)
defer b.Release()

b.Field(0).(*array.Int64Builder).AppendValues(ages, nil)
b.Field(1).(*array.Float64Builder).AppendValues(salaries, nil)
b.Field(2).(*array.StringBuilder).AppendValues(names, nil)

record := b.NewRecord()

// Write to file (IPC format)
f, _ := os.Create("data.arrow")
w, _ := ipc.NewFileWriter(f, ipc.WithSchema(schema))
w.Write(record)
w.Close()

// Read (memory-mapped, zero-copy)
f, _ := os.Open("data.arrow")
r, _ := ipc.NewFileReader(f)
record, _ := r.Read()
// record.Column(0) is direct view into mmap'd memory
```

**GPU integration:**
```go
// Arrow CUDA support (via cuDF or custom)
import "github.com/apache/arrow/go/v14/arrow/cuda"

// Copy Arrow buffer to GPU
gpuBuf := cuda.NewBuffer(ctx, record.Column(0).Data().Buffers()[1])

// Process on GPU
// ... CUDA kernels ...

// Copy back
cpuBuf := gpuBuf.CopyToHost()
```

### 4.5 Recommendation: Arrow as Primary Format

| Criterion | Cap'n Proto | Arrow | Winner |
|-----------|-------------|-------|--------|
| Columnar (GPU-ready) | ❌ Row-oriented | ✅ Native columnar | Arrow |
| Zero-copy read | ✅ Yes | ✅ Yes | Tie |
| Go support | ⚠️ Good | ✅ Excellent | Arrow |
| GPU libraries | ❌ None | ✅ cuDF, RAPIDS | Arrow |
| Ecosystem | ⚠️ Small | ✅ Large (Pandas, Spark) | Arrow |
| Compression | ❌ No | ✅ LZ4, ZSTD | Arrow |
| Streaming | ✅ Yes | ✅ Yes | Tie |

**Recommendation:** Use Apache Arrow as the primary binary format for:
- GPU data transfer
- Inter-process communication
- High-performance persistence

Keep CSV/JSON support for compatibility and human readability.

### 4.6 I/O Performance Projection

With Arrow format:

| Operation | CSV | Arrow | Speedup |
|-----------|-----|-------|---------|
| Read 10M records | 5-10s | 0.3-0.5s | 15-20x |
| Write 10M records | 3-5s | 0.2-0.3s | 15-20x |
| Memory usage | 2x data | 1x data | 2x less |
| GPU transfer | Parse + copy | Direct copy | 10x+ |

---

## 5. Implementation Architecture

### 5.1 System Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                      ssql GPU-Accelerated Pipeline               │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐  │
│  │   CLI    │    │   AST    │    │ Planner  │    │ Executor │  │
│  │  Parser  │───▶│  Builder │───▶│          │───▶│          │  │
│  └──────────┘    └──────────┘    └──────────┘    └──────────┘  │
│                                         │               │        │
│                                         ▼               ▼        │
│                               ┌─────────────────────────────┐   │
│                               │      Execution Targets      │   │
│                               ├─────────────────────────────┤   │
│                               │  CPU        │     GPU       │   │
│                               │  (Typed)    │   (CUDA)      │   │
│                               │             │               │   │
│                               │  - Strings  │  - Numeric    │   │
│                               │  - Regex    │  - Filter     │   │
│                               │  - Small N  │  - Aggregate  │   │
│                               │             │  - Sort       │   │
│                               │             │  - Join       │   │
│                               └─────────────────────────────┘   │
│                                         │                        │
│                                         ▼                        │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Storage Layer                          │   │
│  ├──────────────────────────────────────────────────────────┤   │
│  │  CSV/JSON    │    Arrow     │    Arrow GPU    │  Memory  │   │
│  │  (compat)    │   (disk)     │   (device)      │  (table) │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 5.2 Query Planning

The planner decides CPU vs GPU execution:

```go
type ExecutionPlan struct {
    Stages []Stage
}

type Stage struct {
    Operation   Operation
    Target      ExecutionTarget  // CPU or GPU
    InputCols   []string         // Columns needed
    OutputCols  []string         // Columns produced
    Predicates  []Predicate
}

type ExecutionTarget int
const (
    TargetCPU ExecutionTarget = iota
    TargetGPU
)

func (p *Planner) Plan(ast *AST) *ExecutionPlan {
    plan := &ExecutionPlan{}

    for _, node := range ast.Nodes {
        stage := Stage{Operation: node.Op}

        // Decide execution target
        switch {
        case !p.gpuAvailable:
            stage.Target = TargetCPU
        case node.Op.Type == OpWhere && isNumericPredicate(node.Predicate):
            stage.Target = TargetGPU
        case node.Op.Type == OpAggregate && isNumericAgg(node.Aggregation):
            stage.Target = TargetGPU
        case node.Op.Type == OpSort && isNumericKey(node.SortKey):
            stage.Target = TargetGPU
        case node.Op.Type == OpJoin && isNumericKey(node.JoinKey):
            stage.Target = TargetGPU
        default:
            stage.Target = TargetCPU
        }

        plan.Stages = append(plan.Stages, stage)
    }

    // Optimize: merge adjacent same-target stages
    plan = p.fuseStages(plan)

    // Optimize: minimize CPU↔GPU transfers
    plan = p.minimizeTransfers(plan)

    return plan
}
```

### 5.3 GPU Kernel Library

Pre-compiled CUDA kernels for common operations:

```go
package cuda

// Filter kernel - evaluates predicate on column
type FilterKernel struct {
    module *cuda.Module
}

func (k *FilterKernel) Execute(
    col DeviceColumn,
    op CompareOp,
    value interface{},
) DeviceBitVector {
    // Launch kernel
    // __global__ void filter_gt_int64(int64* data, bool* mask, int64 val, int n)
    // Returns bit vector of matching rows
}

// Aggregation kernels
type SumKernel struct { ... }
type CountKernel struct { ... }
type MinMaxKernel struct { ... }
type AvgKernel struct { ... }

// Sort kernel (radix sort for int64/float64)
type SortKernel struct { ... }

// Join kernel (hash join)
type HashJoinKernel struct { ... }

// Compact kernel (stream compaction using mask)
type CompactKernel struct { ... }
```

### 5.4 Memory Management

```go
package cuda

type MemoryPool struct {
    device      int
    allocated   map[uintptr]*DeviceBuffer
    freeList    []*DeviceBuffer
    totalBytes  int64
    usedBytes   int64
}

// Allocate GPU memory
func (p *MemoryPool) Alloc(size int64) (*DeviceBuffer, error) {
    // Check free list first
    for i, buf := range p.freeList {
        if buf.Size >= size {
            p.freeList = append(p.freeList[:i], p.freeList[i+1:]...)
            return buf, nil
        }
    }

    // Allocate new buffer
    ptr, err := cuda.Malloc(size)
    if err != nil {
        // Try to free unused buffers and retry
        p.GC()
        ptr, err = cuda.Malloc(size)
        if err != nil {
            return nil, fmt.Errorf("GPU OOM: need %d bytes, have %d free",
                size, p.totalBytes-p.usedBytes)
        }
    }

    buf := &DeviceBuffer{Ptr: ptr, Size: size}
    p.allocated[ptr] = buf
    p.usedBytes += size
    return buf, nil
}

// Transfer strategies
type TransferStrategy int
const (
    // Synchronous copy (simple, blocks CPU)
    TransferSync TransferStrategy = iota

    // Asynchronous copy (CPU continues while transfer happens)
    TransferAsync

    // Pinned memory (faster transfers, limited availability)
    TransferPinned

    // Unified memory (automatic migration, convenient but slower)
    TransferUnified
)
```

### 5.5 Hybrid Execution Example

```go
// Pipeline: FROM csv | WHERE age > 30 AND name CONTAINS 'son' | GROUP BY dept | SUM(salary)

func executePipeline(csvPath string) (*Table, error) {
    // 1. Read CSV to Arrow (CPU)
    table, err := ReadCSVToArrow(csvPath)
    if err != nil {
        return nil, err
    }

    // 2. Copy numeric columns to GPU
    ageCol := table.Column("age")
    salaryCol := table.Column("salary")
    deptCol := table.Column("dept")  // Dictionary-encoded

    gpuAge := cuda.ToDevice(ageCol)
    gpuSalary := cuda.ToDevice(salaryCol)
    gpuDept := cuda.ToDevice(deptCol.Codes)  // Just the integer codes

    // 3. GPU: WHERE age > 30 (produces bit mask)
    ageMask := cuda.FilterGT(gpuAge, int64(30))

    // 4. CPU: WHERE name CONTAINS 'son' (string operation)
    nameCol := table.Column("name")
    nameMask := cpuFilterContains(nameCol, "son")

    // 5. Combine masks (GPU)
    gpuNameMask := cuda.ToDevice(nameMask)
    combinedMask := cuda.And(ageMask, gpuNameMask)

    // 6. GPU: Compact (apply filter)
    filteredSalary := cuda.Compact(gpuSalary, combinedMask)
    filteredDept := cuda.Compact(gpuDept, combinedMask)

    // 7. GPU: GROUP BY dept, SUM(salary)
    // Hash-based grouping on integer dept codes
    groupedSums := cuda.GroupBySum(filteredDept, filteredSalary)

    // 8. Copy result back to CPU
    resultCodes := cuda.ToHost(groupedSums.Keys)
    resultSums := cuda.ToHost(groupedSums.Values)

    // 9. Decode department names
    deptDict := deptCol.Dictionary
    result := NewTable(schema)
    for i := range resultCodes {
        result.Append(deptDict[resultCodes[i]], resultSums[i])
    }

    return result, nil
}
```

---

## 6. Performance Projections

### 6.1 Benchmark Scenarios

#### Scenario A: Pure Numeric Filter + Aggregate
```
Pipeline: FROM data.csv | WHERE amount > 1000 | SUM(amount)
Records: 10,000,000
Columns: id (int64), amount (float64), category (int32)
```

| Implementation | Time | Speedup vs CLI |
|----------------|------|----------------|
| CLI (current) | 15s | 1x |
| Typed CPU | 0.4s | 37x |
| GPU | 0.01s | 1,500x |

Breakdown:
- CSV parse: 3s (CPU, one-time)
- Arrow load: 0.2s (or 0s if cached)
- GPU transfer: 0.08s (80MB @ 1GB/s effective)
- GPU compute: 0.001s
- Result transfer: negligible

#### Scenario B: Numeric Filter + String Predicate
```
Pipeline: FROM data.csv | WHERE amount > 1000 AND status = 'active' | COUNT
Records: 10,000,000
Columns: id, amount, status (string, 10 unique values)
```

| Implementation | Time | Speedup vs CLI |
|----------------|------|----------------|
| CLI (current) | 18s | 1x |
| Typed CPU | 0.5s | 36x |
| GPU (dict-encoded) | 0.02s | 900x |

Note: Dictionary encoding converts string comparison to integer comparison.

#### Scenario C: Multi-Join Pipeline (Original Benchmark)
```
Pipeline: FROM edges.csv
  | JOIN kinds.csv ON a_kind
  | JOIN kinds.csv ON z_kind
  | JOIN rels.csv ON rel
Records: 14,600,000 main, 500+500+30 lookup
```

| Implementation | Time | Speedup vs CLI |
|----------------|------|----------------|
| CLI (current) | 155s | 1x |
| Generated Go (map) | 70s | 2.2x |
| Typed CPU | 4.5s | 35x |
| GPU | 0.5s | 310x |

GPU join strategy:
- Build hash tables on GPU (small, fits in fast memory)
- Parallel probe with all 14.6M records
- Memory-bound, not compute-bound

#### Scenario D: Large Aggregation
```
Pipeline: FROM sales.csv | GROUP BY region, product | SUM(revenue), COUNT, AVG(quantity)
Records: 100,000,000
Groups: 10,000 (100 regions × 100 products)
```

| Implementation | Time | Speedup vs CLI |
|----------------|------|----------------|
| CLI (current) | 180s | 1x |
| Typed CPU | 5s | 36x |
| GPU | 0.15s | 1,200x |

GPU aggregation is extremely efficient for many-to-few reductions.

### 6.2 When GPU Doesn't Help

#### Scenario E: String-Heavy Processing
```
Pipeline: FROM logs.csv | WHERE message CONTAINS 'error' | REGEX 'user=(\d+)'
Records: 10,000,000
Columns: timestamp, level, message (avg 200 chars)
```

| Implementation | Time | Speedup vs CLI |
|----------------|------|----------------|
| CLI (current) | 45s | 1x |
| Typed CPU | 8s | 5.6x |
| GPU | 6s | 7.5x |

GPU provides minimal benefit for string operations.

#### Scenario F: Small Dataset
```
Pipeline: FROM config.csv | WHERE enabled = true | SELECT name, value
Records: 1,000
```

| Implementation | Time | Notes |
|----------------|------|-------|
| CLI (current) | 50ms | I/O dominated |
| Typed CPU | 5ms | |
| GPU | 15ms | Transfer overhead > compute |

GPU overhead exceeds benefit for small data.

### 6.3 Performance Summary by Workload Type

| Workload Type | GPU Benefit | Recommendation |
|---------------|-------------|----------------|
| Numeric filter + aggregate (>1M rows) | 100-1000x | Always GPU |
| Numeric sort (>1M rows) | 50-100x | Always GPU |
| Hash join on numeric keys (>1M rows) | 20-50x | GPU if available |
| Group by + aggregate (>100K rows) | 50-200x | Always GPU |
| String filtering | 2-10x | CPU unless huge |
| Regex matching | 1-3x | CPU |
| Small datasets (<100K rows) | 0.5-2x | CPU (overhead) |
| Mixed numeric/string | 10-50x | Hybrid |

---

## 7. Implementation Phases

### Phase 1: Columnar Storage Foundation (4-6 weeks)

**Goal:** Implement columnar storage with Arrow, providing immediate CPU benefits.

**Tasks:**
1. Add Arrow Go dependency
2. Implement `Table` type with columnar storage
3. Implement `RecordsToTable` and `TableToRecords` conversion
4. Add Arrow IPC file read/write
5. Benchmark columnar vs row-oriented operations

**Deliverables:**
- `ssql from data.arrow` - Read Arrow files
- `ssql to arrow output.arrow` - Write Arrow files
- `ssql convert data.csv -o data.arrow` - Format conversion
- Internal columnar representation for operations

**Expected Benefits:**
- 2-3x faster CPU operations (cache efficiency)
- 10-20x faster I/O with Arrow format
- Foundation for GPU work

**Effort:** 1 developer, 4-6 weeks

### Phase 2: GPU Infrastructure (4-6 weeks)

**Goal:** Establish GPU compute capability with basic operations.

**Tasks:**
1. Add CUDA Go bindings (gorgonia/cu or custom CGo)
2. Implement memory pool and transfer management
3. Implement basic kernels: filter_eq, filter_gt, sum, count
4. Add GPU availability detection and fallback
5. Benchmark GPU vs CPU for basic operations

**Deliverables:**
- GPU detection: `ssql info` shows GPU capabilities
- GPU-accelerated numeric filter
- GPU-accelerated sum/count aggregations

**Expected Benefits:**
- 50-100x speedup for pure numeric operations
- Proof of concept for full GPU acceleration

**Effort:** 1 developer, 4-6 weeks

### Phase 3: Query Planner (3-4 weeks)

**Goal:** Automatic CPU/GPU execution planning.

**Tasks:**
1. Implement AST analysis for operation classification
2. Implement cost model for CPU vs GPU decisions
3. Implement transfer minimization optimization
4. Add plan visualization for debugging

**Deliverables:**
- `ssql --explain pipeline...` - Show execution plan
- Automatic GPU offloading for suitable operations
- Efficient CPU/GPU hybrid execution

**Expected Benefits:**
- Transparent GPU acceleration
- Optimal execution without user intervention

**Effort:** 1 developer, 3-4 weeks

### Phase 4: Advanced GPU Operations (6-8 weeks)

**Goal:** Complete GPU operation coverage.

**Tasks:**
1. Implement radix sort kernel
2. Implement hash join kernel
3. Implement group-by with multiple aggregations
4. Implement dictionary encoding for strings
5. Optimize memory transfers (pinned memory, async)

**Deliverables:**
- GPU-accelerated sort
- GPU-accelerated join
- GPU-accelerated group-by
- Dictionary-encoded string support

**Expected Benefits:**
- Full pipeline GPU acceleration
- 100-1000x speedup for suitable workloads

**Effort:** 1-2 developers, 6-8 weeks

### Phase 5: Production Hardening (4-6 weeks)

**Goal:** Production-ready GPU acceleration.

**Tasks:**
1. Error handling and recovery
2. Memory limit handling (chunked processing)
3. Multi-GPU support
4. Comprehensive benchmarking suite
5. Documentation and examples

**Deliverables:**
- Robust error handling
- Large dataset support (>GPU memory)
- Performance regression tests
- User documentation

**Effort:** 1 developer, 4-6 weeks

### Total Effort Summary

| Phase | Duration | Effort | Cumulative Benefit |
|-------|----------|--------|-------------------|
| 1: Columnar/Arrow | 4-6 weeks | 1 dev | 2-20x (I/O + cache) |
| 2: GPU Infrastructure | 4-6 weeks | 1 dev | 50-100x (basic ops) |
| 3: Query Planner | 3-4 weeks | 1 dev | Automatic offload |
| 4: Advanced GPU | 6-8 weeks | 1-2 dev | 100-1000x (full) |
| 5: Production | 4-6 weeks | 1 dev | Robustness |
| **Total** | **21-30 weeks** | **~6-9 dev-months** | |

---

## 8. Risk Assessment

### 8.1 Technical Risks

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| CUDA Go bindings unstable | Medium | High | Use well-tested gorgonia/cu or write minimal CGo |
| GPU memory limits | Medium | Medium | Implement chunked processing |
| Transfer overhead dominates | Low | Medium | Use pinned memory, async transfers |
| Complex kernels hard to debug | Medium | Medium | Extensive unit tests, NVIDIA tools |
| Arrow Go API changes | Low | Low | Pin version, abstract behind interface |

### 8.2 Practical Risks

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Limited user GPU availability | High | Medium | Always have CPU fallback |
| NVIDIA driver compatibility | Medium | Medium | Test on multiple driver versions |
| Increased binary size | Medium | Low | Optional GPU build tag |
| Complexity increases maintenance | Medium | Medium | Modular design, extensive tests |

### 8.3 Strategic Risks

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Effort exceeds value | Low | High | Phase 1 provides value standalone |
| GPU computing evolves (AMD, Apple) | Medium | Medium | Abstract GPU layer, cuDF portability |
| Arrow format changes | Low | Low | Use stable Arrow version |

---

## 9. Conclusion

### 9.1 Key Findings

1. **GPU acceleration can provide 100-1000x speedup** for numeric-heavy workloads
2. **Columnar storage is prerequisite** and provides 2-20x benefit even without GPU
3. **Arrow is the right format** - columnar, zero-copy, GPU-ready ecosystem
4. **String operations remain CPU-bound** - dictionary encoding helps but doesn't eliminate
5. **Implementation is feasible** in 6-9 developer-months

### 9.2 Recommended Approach

**Short-term (Phase 1):** Implement Arrow support and columnar storage
- Immediate 2-20x I/O improvement
- CPU cache benefits
- No GPU dependency
- Foundation for future GPU work

**Medium-term (Phases 2-3):** GPU infrastructure and planning
- Prove GPU value with basic operations
- Automatic CPU/GPU routing
- 50-100x speedup for numeric filters/aggregations

**Long-term (Phases 4-5):** Full GPU acceleration
- Complete operation coverage
- Production hardening
- 100-1000x speedup for full pipelines

### 9.3 Decision Points

Before proceeding past Phase 1, evaluate:
1. Is columnar/Arrow benefit sufficient for current needs?
2. Do target users have NVIDIA GPUs?
3. Are workloads GPU-suitable (numeric-heavy)?

Before proceeding past Phase 2, evaluate:
1. Does GPU provide meaningful speedup in practice?
2. Is complexity manageable?
3. Is there user demand?

### 9.4 Success Metrics

| Metric | Phase 1 | Phase 2 | Phase 4 |
|--------|---------|---------|---------|
| I/O speedup | 10-20x | 10-20x | 10-20x |
| Numeric filter speedup | 2-3x | 50-100x | 100-500x |
| Join speedup | 2-3x | 2-3x | 20-50x |
| Aggregation speedup | 2-3x | 50-100x | 50-200x |
| Original benchmark (14.6M × 3 joins) | 20s | 10s | 0.5s |

### 9.5 Final Recommendation

**Proceed with Phase 1 (Columnar/Arrow) regardless of GPU plans.**

The columnar storage and Arrow format provide substantial benefits:
- Faster I/O for all users
- Better CPU cache utilization
- Industry-standard format for interop
- Optional GPU acceleration path

GPU acceleration (Phases 2-5) should be pursued if:
- Target users have NVIDIA GPUs
- Workloads are predominantly numeric
- Performance requirements justify 6-9 month investment

The phased approach allows value delivery at each stage while preserving optionality.
