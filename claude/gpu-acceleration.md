# GPU Acceleration (Experimental)

**GPU acceleration has been implemented and benchmarked. Results were surprising.**

## Actual Benchmark Results (RTX 5090 + Intel Core Ultra 9 275HX)

| Operation | CPU | GPU | Result |
|-----------|-----|-----|--------|
| Sum (1M float64) | 86us | 601us | **CPU 7x faster** |
| Filter+Sum (10M float64) | 0.8ms | 5.3ms | **CPU 6.6x faster** |
| Convolve (100K x 1K) | 195ms | 603us | **GPU 320x faster** |
| FFT (1K points) | 5.2ms | 0.25ms | **GPU 21x faster** |
| FFT (1M points) | hours | 2.9ms | **GPU inf faster** |

**Key finding:** GPU wins big for compute-heavy operations (convolution: 18-320x, FFT: 21-100x+). For memory-bound operations (aggregations), CPU wins.

## Why GPU Loses for Aggregations

PCIe transfer overhead dominates:

```
1M float64 values (8MB):
  PCIe to GPU:    ~500us+
  GPU sum:        ~0.1ms
  PCIe from GPU:  ~0.01ms
  Total GPU:      ~600us

  CPU sum:        ~86us (no transfer, fast memory)
```

Modern CPUs have 50-100 GB/s memory bandwidth. For simple arithmetic, the CPU finishes before the GPU transfer completes.

## The Record Extraction Problem

ssql's `Record` type uses Schema + `[]any`. Extracting values requires CPU work:

```go
// This is CPU-bound and often slower than the aggregation itself
values := make([]float64, len(records))
for i, r := range records {
    values[i] = ssql.GetOr(r, "price", 0.0)
}
```

**Arrow columnar format bypasses this** - data is already contiguous.

## Current GPU Implementation

```
gpu/
├── sum.cu           # CUDA kernels (sum, filter, FFT)
├── gpu.go           # Go wrappers (build tag: gpu)
├── gpu_stub.go      # Stubs for non-GPU builds
├── gpu_test.go      # Tests and benchmarks
└── Makefile         # Builds libssqlgpu.so
```

## Building with GPU Support

**Option 1: Docker Build (Recommended - no local CUDA needed)**

```bash
git clone https://github.com/rosscartlidge/ssql
cd ssql
make docker-gpu-extract
sudo cp libssqlgpu.so /usr/local/lib && sudo ldconfig
./ssql_gpu version
```

**Option 2: Local CUDA Toolkit**

```bash
make build-gpu
sudo make install-gpu
./ssql_gpu version
```

**Option 3: Docker Image (for container workflows)**

```bash
make docker-gpu-image
docker run --gpus all ssql:gpu version
```

**Available Makefile Targets:**

| Target | Description |
|--------|-------------|
| `make gpu` | Build CUDA library only (gpu/libssqlgpu.so) |
| `make build-gpu` | Build ssql_gpu binary with GPU support |
| `make install-gpu` | Install library to /usr/local/lib (requires sudo) |
| `make docker-gpu-image` | Build Docker image with ssql_gpu |
| `make docker-gpu-extract` | Build via Docker and extract binary |
| `make docker-gpu` | Alias for docker-gpu-extract |

## What Works Now

```go
// Convolution (18-320x speedup) - compute-heavy
gpu.ConvolveDirect(signal, kernel)  // Best for kernel < 10K
gpu.ConvolveFFT(signal, kernel)     // Best for very large kernels

// FFT (21-100x+ speedup) - genuinely compute-bound
gpu.FFTMagnitude(data)
gpu.FFTMagnitudePhase(data)
```

## Don't Use GPU For

- **Simple aggregations** (sum, avg, count, min, max) - CPU is 7x faster
- **Chained filter operations** - CPU still wins on fast hardware
- **Small datasets** (<100K elements) - kernel launch overhead dominates
- **Anything memory-bound** - fast CPUs win

## Benchmark Validation Lesson (January 2026)

**Always sanity-check benchmark results against theoretical expectations.**

We incorrectly concluded "GPU FFT provides no benefit" based on flawed benchmarks showing:
```
Old (WRONG):  1M-point FFT = 4.2ms CPU, 4.2ms GPU  -> "Tie"
New (CORRECT): 1M-point FFT = 125ms CPU, 4.4ms GPU -> GPU 28x faster
```

**Rule:** If benchmark results seem too good, they probably are. Verify that:
1. Results are actually being used (prevent dead code elimination)
2. You're timing the right code path
3. Numbers make sense given algorithm complexity

## Future GPU Opportunities

1. **FFT CLI command** - leverage existing cuFFT implementation
2. **Arrow to GPU direct transfer** - bypass Record extraction entirely
3. **Compute-heavy operations** - matrix ops, convolution, spectral analysis

**Reference:** See `doc/research/gpu-arrow-learnings.md` for detailed analysis and benchmark data.
