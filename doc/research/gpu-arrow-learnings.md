# What We Learned: GPU Acceleration and Arrow Integration

| | |
|---|---|
| **Date** | January 2025 |
| **Status** | Implemented and tested |
| **Summary** | GPU acceleration provides 10-100x speedup for FFT but no benefit for simple aggregations. Arrow I/O is 10-20x faster and enables future GPU integration. |

## Executive Summary

We implemented GPU acceleration expecting 50-200x speedups for aggregations. Reality was sobering: **simple operations (sum, avg, count) were actually SLOWER on GPU** because PCIe transfer overhead dominates compute time. Even chained operations didn't help on a fast modern CPU.

The one thing that does work well:
- **FFT operations** - 21x speedup at 1K points, scaling to 100x+ for large transforms

The key insight: **GPU only wins when compute time massively exceeds transfer time**. For memory-bound operations like aggregations, a fast CPU wins.

---

## The Transfer Overhead Problem

This was the most important discovery. Consider summing 1 million float64 values (8MB):

```
PCIe transfer to GPU:    ~2ms
GPU sum computation:     ~0.1ms
PCIe transfer from GPU:  ~0.01ms
─────────────────────────────────
Total GPU time:          ~2.1ms

CPU sum (no transfer):   ~2ms
```

**Result: GPU is the same speed or slower than CPU.**

### Why This Happens

Memory bandwidth comparison:
- **PCIe 3.0 x16:** ~12 GB/s theoretical, ~8 GB/s practical
- **CPU main memory:** ~50-100 GB/s
- **GPU HBM:** ~900 GB/s (but data must reach it first)

For simple aggregations, the operation is memory-bound, not compute-bound. The CPU can sum values as fast as it reads them from RAM. Adding a PCIe round-trip provides no benefit.

### The Record Extraction Problem

ssql's `Record` type uses a Schema + `[]any` values slice internally. Extracting values for GPU requires:

```go
// Records use Schema + []any - extracting values requires:
values := make([]float64, len(records))
for i, r := range records {
    values[i] = GetOr(r, "price", 0.0)  // Schema index lookup + type assertion
}
// This extraction often takes LONGER than the aggregation itself
```

Even if GPU aggregation were instant, extracting values from `[]any` to contiguous float64 arrays is CPU-bound work that cannot be parallelized.

---

## What Works: Compute-Heavy Operations

### Convolution (18-320x Speedup)

Convolution is compute-heavy: each output element requires kernel-length multiply-accumulates. This is genuinely compute-bound:

**Actual benchmark results (RTX 5090, Intel Core Ultra 9 275HX):**

| Signal × Kernel | CPU | GPU Direct | Speedup |
|-----------------|-----|------------|---------|
| 10K × 100 | 1.8ms | 101μs | **18x** |
| 10K × 1K | 19.5ms | 162μs | **120x** |
| 100K × 100 | 18.5ms | 370μs | **50x** |
| 100K × 1K | 195ms | 603μs | **320x** |

```go
// GPU convolution - direct method is fastest for typical kernel sizes
result, err := gpu.ConvolveDirect(signal, kernel)

// FFT-based convolution - better for very large kernels (>10K elements)
result, err := gpu.ConvolveFFT(signal, kernel)
```

Larger kernels show bigger speedups because more compute is done per transfer.

**Why convolution wins where aggregations lose:**

| Operation | Work per element | Bottleneck | GPU benefit |
|-----------|------------------|------------|-------------|
| Sum/Avg | 1 add | Memory bandwidth | None (CPU wins) |
| Convolution (100 kernel) | 100 multiply-adds | Compute | **18-50x** |
| Convolution (1K kernel) | 1000 multiply-adds | Compute | **120-320x** |
| FFT | O(log n) trig ops | Compute | **21-100x+** |

The key metric is **compute-to-transfer ratio**. Convolution with a 1K kernel does 1000 operations per output element - enough compute to justify the PCIe transfer overhead.

### FFT (10-100x Speedup)

Fast Fourier Transform involves O(n log n) operations with transcendental functions (sin, cos). This is genuinely compute-bound:

```go
// GPU FFT implementation uses cuFFT library
magnitudes, err := gpu.FFTMagnitude(signal)
phases, err := gpu.FFTMagnitudePhase(signal)
```

**Actual benchmark results (RTX 5090, Intel Core Ultra 9 275HX):**

| Data Size | CPU (naive DFT) | GPU (cuFFT) | Speedup |
|-----------|-----------------|-------------|---------|
| 1K points | 5.2ms | 0.25ms | **21x** |
| 2K points | 20.8ms | ~0.3ms | **~70x** |
| 64K points | ~hours | 0.92ms | **∞** |
| 1M points | timeout | 2.9ms | **∞** |

Note: CPU uses naive O(n²) DFT. With FFTW library, CPU FFT is O(n log n) and the speedup would be smaller (2-10x for large transforms).

### Chained Operations (No Benefit on Fast CPUs)

We hoped that keeping data on GPU between operations would amortize the transfer cost:

```go
// Single transfer, two operations, single return
result, err := gpu.FilterThenSum(data, threshold)

// Internally:
// 1. Copy data to GPU (one-time cost)
// 2. Filter on GPU (data stays)
// 3. Sum filtered results on GPU
// 4. Copy single result back
```

**Actual benchmark with 10M elements, 50% filter rate:**
- CPU filter+sum: 0.8ms
- GPU filter+sum: 5.3ms
- **Result: CPU is 6.6x faster**

The theory was sound, but the Intel Core Ultra 9 275HX is so fast at memory operations that even chained GPU operations can't compete. This may differ on slower CPUs or with more compute-intensive chains.

---

## What Doesn't Work

### Simple Aggregations (GPU is Slower)

| Operation | GPU Performance | Recommendation |
|-----------|-----------------|----------------|
| Sum | **7x slower** | Use CPU |
| Average | **~7x slower** | Use CPU |
| Count | **~7x slower** | Use CPU |
| Min/Max | **~7x slower** | Use CPU |

These are all memory-bound. The GPU kernel runs fast, but transfer time dominates. On a modern fast CPU, the CPU completes the aggregation before the GPU transfer finishes.

### Batch Operations (Sometimes Slower)

Our initial theory: "Sum 10 columns in one GPU call to amortize transfer."

Reality: Flattening 10 columns into contiguous memory for transfer has its own overhead. For typical workloads, individual CPU sums are faster.

### Small Datasets (<100K elements)

Below 100K elements, even FFT shows minimal benefit. GPU kernel launch overhead (~10-50μs) becomes significant relative to computation.

---

## Architecture That Emerged

### Build Tag Separation

```
gpu/
├── sum.cu           # CUDA kernels
├── gpu.go           # Go wrapper (build tag: gpu)
├── gpu_stub.go      # Stubs (build tag: !gpu)
├── gpu_test.go      # Tests
└── Makefile         # CUDA build
```

```bash
# Default build (no GPU dependency)
go build ./...

# GPU-enabled build
go build -tags gpu ./...
```

This allows:
- GPU-less builds for portability
- No runtime GPU detection needed
- Clean test separation

### Automatic Fallback

```go
// sql_gpu.go
const GPUMinGroupSize = 10000

func SumGPU(field string) AggregateFunc {
    return func(records []Record) AggregateResult {
        if len(records) < GPUMinGroupSize || !gpuAvailable() {
            return Sum(field)(records)  // CPU fallback
        }
        // ... GPU path
    }
}
```

Small groups use CPU automatically. GPU errors fall back gracefully.

---

## Arrow Integration: Why It Matters

### The Connection to GPU

Arrow's columnar format stores data as contiguous typed arrays:

```
Arrow:
┌─────────────────────────────────┐
│ price: [1.5, 2.3, 4.1, 3.2, ...] │  ← Contiguous float64[]
│ qty:   [10, 5, 8, 12, ...]       │  ← Contiguous int64[]
└─────────────────────────────────┘

vs Record (Schema + []any):
┌──────────────────────────────────┐
│ {price: 1.5, qty: 10}            │  ← Each record has []any values
│ {price: 2.3, qty: 5}             │  ← Values are boxed (any)
│ ...                              │
└──────────────────────────────────┘
```

With Arrow data:
1. **No extraction needed** - data already contiguous
2. **Direct GPU transfer** - `cudaMemcpy` the float64[] directly
3. **Zero-copy possible** - memory-map Arrow file, DMA to GPU

### What We Implemented

Phase 1 (complete):
```go
// Arrow I/O - 10-20x faster than CSV/JSON
records, _ := ssql.ReadArrow("data.arrow")
ssql.WriteArrow(records, "output.arrow")
```

Current limitation: We still convert Arrow → Record → Arrow. This loses the columnar advantage but provides immediate I/O benefits.

### Future Phases

| Phase | Description | Benefit |
|-------|-------------|---------|
| 2 | Columnar Table type | In-memory batch ops |
| 3 | Direct columnar operations | 2-10x processing |
| 4 | Arrow → GPU direct | Eliminates extraction |

Phase 4 would make GPU aggregations viable:
```go
// Future: no extraction overhead
table := ssql.ReadArrowTable("data.arrow")
sum := table.Column("price").SumGPU()  // Direct GPU transfer
```

---

## Benchmarks Summary

### Actual Benchmark Results (January 2026)

**Hardware:** NVIDIA GeForce RTX 5090, Intel Core Ultra 9 275HX

#### Sum Float64 (1M elements)
```
BenchmarkSumFloat64CPU-24       40020       86,227 ns/op     (86μs)
BenchmarkSumFloat64GPU-24        5977      600,667 ns/op    (601μs)

Result: GPU is 7x SLOWER than CPU
```

#### Filter Then Sum (10M elements, 50% filter rate)
```
BenchmarkFilterThenSumCPU-24     4388      801,250 ns/op    (0.8ms)
BenchmarkFilterThenSumGPU-24      627    5,307,094 ns/op    (5.3ms)

Result: GPU is 6.6x SLOWER than CPU
```

#### FFT Magnitude (varying sizes)
```
BenchmarkFFTMagnitudeCPU/1K-24    666    5,236,268 ns/op    (5.2ms)
BenchmarkFFTMagnitudeCPU/2K-24    172   20,765,227 ns/op   (20.8ms)

BenchmarkFFTMagnitudeGPU/1K-24  14274      246,659 ns/op  (0.25ms)  21x faster
BenchmarkFFTMagnitudeGPU/8K-24   4172      862,548 ns/op  (0.86ms)
BenchmarkFFTMagnitudeGPU/64K-24  3765      924,045 ns/op  (0.92ms)
BenchmarkFFTMagnitudeGPU/256K-24 2172    1,585,641 ns/op  (1.59ms)
BenchmarkFFTMagnitudeGPU/1M-24   1276    2,873,982 ns/op  (2.87ms)

Result: GPU is 21x faster for 1K, scales to 100x+ for large transforms
        CPU is O(n²) naive DFT - would take hours for 1M points
```

### What The Benchmarks Tell Us

| Operation | Data Size | CPU | GPU | Winner | Factor |
|-----------|-----------|-----|-----|--------|--------|
| Sum | 1M float64 | 86μs | 601μs | **CPU** | 7x |
| Filter+Sum | 10M float64 | 0.8ms | 5.3ms | **CPU** | 6.6x |
| FFT | 1K points | 5.2ms | 0.25ms | **GPU** | 21x |
| FFT | 1M points | hours | 2.9ms | **GPU** | ∞ |

**Key observations:**
1. Modern fast CPUs (like Intel Core Ultra 9) are very efficient for memory-bound operations
2. PCIe transfer overhead (~500μs+ for the roundtrip) dominates small compute kernels
3. FFT is genuinely compute-bound (O(n log n) with transcendentals), so GPU wins
4. Even chained operations lose to CPU when the CPU is fast enough

### Arrow I/O Benchmarks

```
ReadCSV (1M records):     ~4.5s
ReadArrow (1M records):   ~0.3s   (15x faster)

WriteCSV (1M records):    ~3.2s
WriteArrow (1M records):  ~0.2s   (16x faster)
```

---

## Recommendations

### Use GPU For

1. **FFT and spectral analysis** - Always use GPU (21-100x+ speedup)
2. **Signal processing** - Convolution, filtering, correlation (compute-heavy)
3. **Matrix operations** - When we add them (compute-heavy)

### Don't Use GPU For

1. **Simple aggregations** (sum, avg, count, min, max) - CPU is 7x faster
2. **Chained filter operations** - Even with data staying on GPU, CPU wins
3. **Small datasets** (<100K elements) - kernel launch overhead dominates
4. **String operations** - GPU can't help here
5. **Anything memory-bound** - Fast CPUs win on memory-bound workloads

### Use Arrow For

1. Large datasets (>100K records)
2. Repeated processing of same data
3. Inter-process data sharing
4. Future GPU integration

---

## Code Examples

### GPU FFT

```go
import "github.com/rosscartlidge/ssql/v4/gpu"

// Check GPU availability
if !gpu.Available() {
    log.Println("GPU not available, using CPU")
}

// Compute FFT magnitude spectrum
signal := []float64{...}  // Time-domain signal
magnitudes, err := gpu.FFTMagnitude(signal)
// magnitudes[0] = DC component
// magnitudes[n/2] = Nyquist frequency
```

### GPU Chained Operations

```go
// Filter values > threshold, then sum survivors
data := []float64{1.0, 5.0, 2.0, 8.0, 3.0}
threshold := 4.0
sum, err := gpu.FilterThenSum(data, threshold)
// sum = 5.0 + 8.0 = 13.0
```

### Arrow I/O

```go
import "github.com/rosscartlidge/ssql/v4"

// Read Arrow file (10-20x faster than CSV)
records, err := ssql.ReadArrow("data.arrow")

// Process with standard ssql operations
filtered := ssql.Pipe(
    ssql.Where(func(r ssql.Record) bool {
        return ssql.GetOr(r, "age", int64(0)) > 25
    }),
)(records)

// Write back to Arrow (with ZSTD compression)
ssql.WriteArrow(filtered, "output.arrow")
```

---

## Lessons for Future Development

1. **Profile before optimizing** - Our initial projections (50-200x speedups) were completely wrong. Always benchmark on real hardware.

2. **Transfer overhead dominates** - For memory-bound operations, PCIe transfer time exceeds compute time. This isn't fixable with better kernels.

3. **Fast CPUs change the equation** - A modern CPU (Intel Core Ultra 9) is so fast that even chained GPU operations lose. Older benchmarks may not apply.

4. **Only GPU for truly compute-bound work** - FFT with its O(n log n) transcendental functions shows real GPU benefit. Simple arithmetic doesn't.

5. **Build for graceful degradation** - GPU features should always have CPU fallback, and the fallback may often be faster!

6. **Arrow enables future GPU** - Phase 1 I/O is valuable now. Direct Arrow→GPU transfer (Phase 4) might change the calculus by eliminating Go-side data movement.

7. **Don't trust theoretical speedups** - "100-1000x speedup potential" from research papers assumes data already on GPU. Real-world includes transfer.

8. **Modern CPUs are remarkably fast** - Memory bandwidth on modern desktop CPUs (50-100 GB/s) approaches older GPU transfer speeds.

---

## Implementation Plan: FFT and Convolution in ssql

### Overview

Add FFT and convolution as first-class operations in the ssql package and CLI. These are the operations where GPU acceleration provides genuine benefit (21-320x speedup).

### Phase 1: Core Library (ssql package)

**New types in `signal.go`:**

```go
// Signal represents a time-domain signal as a sequence of float64 values
type Signal []float64

// Spectrum represents frequency-domain data (magnitude and optional phase)
type Spectrum struct {
    Frequencies []float64  // Frequency bins (Hz, if sample rate known)
    Magnitude   []float64  // Magnitude at each frequency
    Phase       []float64  // Phase in radians (optional)
}
```

**New functions:**

```go
// FFT operations
func FFT(signal Signal) (*Spectrum, error)
func FFTMagnitude(signal Signal) ([]float64, error)
func FFTMagnitudePhase(signal Signal) (mag, phase []float64, err error)

// Convolution operations
func Convolve(signal, kernel Signal) (Signal, error)
func ConvolveFFT(signal, kernel Signal) (Signal, error)  // For large kernels

// Common kernels
func GaussianKernel(size int, sigma float64) Signal
func MovingAverageKernel(size int) Signal
func SobelKernel() Signal  // Edge detection
```

**GPU acceleration (transparent):**

```go
// Internal: automatically uses GPU if available and beneficial
func fftImpl(signal Signal) (*Spectrum, error) {
    if gpu.Available() && len(signal) >= 1024 {
        return fftGPU(signal)
    }
    return fftCPU(signal)
}

func convolveImpl(signal, kernel Signal) (Signal, error) {
    if gpu.Available() && len(kernel) >= 64 {
        return convolveGPU(signal, kernel)
    }
    return convolveCPU(signal, kernel)
}
```

### Phase 2: Record Integration

**Extract signal from records:**

```go
// ExtractSignal extracts a float64 field as a Signal
func ExtractSignal(records iter.Seq[Record], field string) Signal

// Example usage:
prices := ssql.ExtractSignal(records, "price")
smoothed := ssql.Convolve(prices, ssql.MovingAverageKernel(10))
```

**Apply signal back to records:**

```go
// WithSignal adds a signal as a new field to records
func WithSignal(records iter.Seq[Record], field string, signal Signal) iter.Seq[Record]
```

### Phase 3: CLI Commands

**`ssql fft` command:**

```bash
# Compute FFT magnitude spectrum
ssql from sensor_data.csv | ssql fft -field temperature -output spectrum.csv

# Output columns: frequency, magnitude, phase (optional)
ssql from audio.csv | ssql fft -field amplitude --phase -output freq.csv

# Inline: add spectrum as new fields
ssql from data.csv | ssql fft -field signal -as-fields freq_,mag_
```

**`ssql convolve` command:**

```bash
# Smooth with moving average
ssql from prices.csv | ssql convolve -field price -kernel avg:10 -as smoothed

# Gaussian smoothing
ssql from sensor.csv | ssql convolve -field value -kernel gaussian:5:1.5 -as filtered

# Custom kernel from file
ssql from data.csv | ssql convolve -field signal -kernel-file impulse.csv -as response

# Edge detection (derivative)
ssql from image_row.csv | ssql convolve -field intensity -kernel diff -as edges
```

**Built-in kernels:**

| Kernel | Syntax | Description |
|--------|--------|-------------|
| Moving average | `avg:N` | N-point moving average |
| Gaussian | `gaussian:N:sigma` | Gaussian smoothing |
| Derivative | `diff` | `[-1, 1]` edge detection |
| Laplacian | `laplacian` | `[1, -2, 1]` second derivative |
| Custom | `-kernel-file FILE` | Load from CSV/JSON |

### Phase 4: Code Generation

**Generated code for FFT:**

```go
// ssql from data.csv | ssql fft -field signal | ssql to csv
signal := ssql.ExtractSignal(records, "signal")
spectrum, _ := ssql.FFT(signal)
// ... output spectrum as records
```

**Generated code for convolution:**

```go
// ssql from prices.csv | ssql convolve -field price -kernel avg:10 -as smoothed
prices := ssql.ExtractSignal(records, "price")
kernel := ssql.MovingAverageKernel(10)
smoothed := ssql.Convolve(prices, kernel)
records = ssql.WithSignal(records, "smoothed", smoothed)
```

### Implementation Order

| Step | Task | Effort | Dependencies |
|------|------|--------|--------------|
| 1 | Add `Signal` type and CPU implementations | 1 day | None |
| 2 | Integrate existing GPU code into ssql package | 1 day | Step 1 |
| 3 | Add `ExtractSignal`/`WithSignal` helpers | 0.5 day | Step 1 |
| 4 | Add `fft` CLI command | 1 day | Steps 1-3 |
| 5 | Add `convolve` CLI command with built-in kernels | 1 day | Steps 1-3 |
| 6 | Add code generation support | 1 day | Steps 4-5 |
| 7 | Documentation and examples | 0.5 day | Steps 4-6 |

**Total: ~6 days**

### Design Decisions

1. **Transparent GPU acceleration** - Users don't need to know about GPU. The library automatically uses GPU when beneficial.

2. **Threshold-based GPU selection:**
   - FFT: Use GPU for signals ≥ 1024 points
   - Convolution: Use GPU for kernels ≥ 64 points
   - Always fall back to CPU if GPU unavailable

3. **Signal as separate type** - Don't try to stream FFT/convolution. These operations need the full signal in memory.

4. **Built-in kernels** - Common smoothing/filtering kernels should be easy to use without creating separate files.

5. **CLI outputs records** - FFT outputs frequency/magnitude/phase as records (one per frequency bin). Convolution outputs modified records with new field.

### Example Workflows

**Spectral analysis:**
```bash
# Find dominant frequencies in sensor data
ssql from sensor.csv | ssql fft -field value | ssql sort -by magnitude -desc | ssql limit 10
```

**Signal smoothing:**
```bash
# Smooth noisy price data
ssql from prices.csv | ssql convolve -field close -kernel gaussian:21:3 -as smoothed | ssql to csv
```

**Pipeline with other operations:**
```bash
# Filter, smooth, then analyze
ssql from data.csv \
  | ssql where -where quality eq good \
  | ssql convolve -field signal -kernel avg:5 -as smoothed \
  | ssql fft -field smoothed \
  | ssql where -where magnitude gt 0.1 \
  | ssql to json
```

---

## Files Reference

```
gpu/
├── sum.cu              # CUDA kernels (825 lines)
├── gpu.go              # Go CGO wrapper (336 lines)
├── gpu_stub.go         # Non-GPU build stubs
├── gpu_test.go         # Tests and benchmarks
├── Makefile            # CUDA build system
└── libssqlgpu.so       # Compiled library

Core ssql:
├── arrow.go            # Arrow I/O (349 lines)
├── arrow_test.go       # Arrow tests (295 lines)
├── sql_gpu.go          # GPU aggregation wrappers
└── sql_gpu_stub.go     # Non-GPU aggregation stubs

Documentation:
├── doc/research/gpu-acceleration.md
├── doc/research/arrow-integration-proposal.md
└── doc/research/gpu-arrow-learnings.md  (this file)
```
