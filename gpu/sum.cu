// CUDA parallel reduction, filter, and FFT kernels
// Optimized for modern GPUs with warp-level primitives

#include <cuda_runtime.h>
#include <cufft.h>
#include <stdint.h>
#include <math.h>

// ============================================================================
// GPU Array Management
// ============================================================================

// Allocate GPU memory for float64 array
extern "C" double* gpuAllocFloat64(int64_t n) {
    double* devPtr = nullptr;
    cudaError_t err = cudaMalloc(&devPtr, n * sizeof(double));
    return (err == cudaSuccess) ? devPtr : nullptr;
}

// Allocate GPU memory for int64 array
extern "C" int64_t* gpuAllocInt64(int64_t n) {
    int64_t* devPtr = nullptr;
    cudaError_t err = cudaMalloc(&devPtr, n * sizeof(int64_t));
    return (err == cudaSuccess) ? devPtr : nullptr;
}

// Free GPU memory
extern "C" void gpuFree(void* devPtr) {
    if (devPtr != nullptr) {
        cudaFree(devPtr);
    }
}

// Copy float64 data from host to device
extern "C" int gpuCopyFloat64ToDevice(double* devPtr, const double* hostData, int64_t n) {
    cudaError_t err = cudaMemcpy(devPtr, hostData, n * sizeof(double), cudaMemcpyHostToDevice);
    return (err == cudaSuccess) ? 0 : -1;
}

// Copy float64 data from device to host
extern "C" int gpuCopyFloat64ToHost(double* hostData, const double* devPtr, int64_t n) {
    cudaError_t err = cudaMemcpy(hostData, devPtr, n * sizeof(double), cudaMemcpyDeviceToHost);
    return (err == cudaSuccess) ? 0 : -1;
}

// ============================================================================
// Filter Kernels
// ============================================================================

// Count elements matching predicate: value > threshold
__global__ void countGreaterThanKernel(const double* input, int64_t n, double threshold, int64_t* count) {
    __shared__ int64_t blockCount;
    if (threadIdx.x == 0) blockCount = 0;
    __syncthreads();

    int64_t localCount = 0;
    int gridSize = blockDim.x * gridDim.x;

    for (int64_t i = blockIdx.x * blockDim.x + threadIdx.x; i < n; i += gridSize) {
        if (input[i] > threshold) {
            localCount++;
        }
    }

    // Warp reduction
    for (int offset = warpSize / 2; offset > 0; offset /= 2) {
        localCount += __shfl_down_sync(0xffffffff, localCount, offset);
    }

    // First thread in each warp adds to block count
    if (threadIdx.x % warpSize == 0) {
        atomicAdd((unsigned long long*)&blockCount, (unsigned long long)localCount);
    }
    __syncthreads();

    // First thread adds block count to global count
    if (threadIdx.x == 0) {
        atomicAdd((unsigned long long*)count, (unsigned long long)blockCount);
    }
}

// Compact filter: copy elements where value > threshold
__global__ void filterGreaterThanKernel(const double* input, int64_t n, double threshold,
                                         double* output, int64_t* outIndex) {
    int gridSize = blockDim.x * gridDim.x;

    for (int64_t i = blockIdx.x * blockDim.x + threadIdx.x; i < n; i += gridSize) {
        if (input[i] > threshold) {
            int64_t idx = atomicAdd((unsigned long long*)outIndex, 1ULL);
            output[idx] = input[i];
        }
    }
}

// Warp-level reduction using shuffle
__device__ __forceinline__ double warpReduceSum(double val) {
    for (int offset = warpSize / 2; offset > 0; offset /= 2) {
        val += __shfl_down_sync(0xffffffff, val, offset);
    }
    return val;
}

// atomicAdd for double (works on all architectures)
__device__ double atomicAddDouble(double* address, double val) {
    unsigned long long int* address_as_ull = (unsigned long long int*)address;
    unsigned long long int old = *address_as_ull, assumed;
    do {
        assumed = old;
        old = atomicCAS(address_as_ull, assumed,
                        __double_as_longlong(val + __longlong_as_double(assumed)));
    } while (assumed != old);
    return __longlong_as_double(old);
}

// Block-level reduction
__device__ __forceinline__ double blockReduceSum(double val) {
    __shared__ double shared[32]; // One slot per warp

    int lane = threadIdx.x % warpSize;
    int wid = threadIdx.x / warpSize;

    // Warp-level reduction
    val = warpReduceSum(val);

    // Write reduced value from each warp to shared memory
    if (lane == 0) {
        shared[wid] = val;
    }
    __syncthreads();

    // Only first warp does final reduction
    val = (threadIdx.x < blockDim.x / warpSize) ? shared[lane] : 0.0;
    if (wid == 0) {
        val = warpReduceSum(val);
    }

    return val;
}

// Kernel: Sum float64 array with grid-stride loop for large arrays
__global__ void sumFloat64Kernel(const double* __restrict__ input, double* __restrict__ output, int64_t n) {
    __shared__ double shared[256];

    int tid = threadIdx.x;
    int gridSize = blockDim.x * gridDim.x;

    // Grid-stride loop: each thread sums multiple elements
    double threadSum = 0.0;
    for (int64_t i = blockIdx.x * blockDim.x + threadIdx.x; i < n; i += gridSize) {
        threadSum += input[i];
    }

    // Store in shared memory
    shared[tid] = threadSum;
    __syncthreads();

    // Tree-based reduction in shared memory
    for (int s = blockDim.x / 2; s > 0; s >>= 1) {
        if (tid < s) {
            shared[tid] += shared[tid + s];
        }
        __syncthreads();
    }

    // Thread 0 writes the block result
    if (tid == 0) {
        atomicAddDouble(output, shared[0]);
    }
}

// Kernel: Sum int64 array
__global__ void sumInt64Kernel(const int64_t* __restrict__ input, int64_t* __restrict__ output, int64_t n) {
    int64_t sum = 0;

    // Grid-stride loop
    for (int64_t i = blockIdx.x * blockDim.x + threadIdx.x; i < n; i += blockDim.x * gridDim.x) {
        sum += input[i];
    }

    // Warp reduction for int64
    for (int offset = warpSize / 2; offset > 0; offset /= 2) {
        sum += __shfl_down_sync(0xffffffff, sum, offset);
    }

    // Block reduction via shared memory
    __shared__ int64_t shared[32];
    int lane = threadIdx.x % warpSize;
    int wid = threadIdx.x / warpSize;

    if (lane == 0) {
        shared[wid] = sum;
    }
    __syncthreads();

    sum = (threadIdx.x < blockDim.x / warpSize) ? shared[lane] : 0;
    if (wid == 0) {
        for (int offset = warpSize / 2; offset > 0; offset /= 2) {
            sum += __shfl_down_sync(0xffffffff, sum, offset);
        }
    }

    if (threadIdx.x == 0) {
        atomicAdd((unsigned long long*)output, (unsigned long long)sum);
    }
}

// C interface for Go CGO
extern "C" {

// Initialize CUDA (optional, for warmup)
int gpuInit() {
    cudaError_t err = cudaSetDevice(0);
    if (err != cudaSuccess) {
        return -1;
    }
    // Warmup: allocate and free small buffer
    void* tmp;
    cudaMalloc(&tmp, 1024);
    cudaFree(tmp);
    return 0;
}

// Sum float64 array on GPU
// Returns 0 on success, negative error code on failure
int gpuSumFloat64(const double* hostData, int64_t n, double* result) {
    if (hostData == nullptr || result == nullptr) {
        return -100;  // Null pointer
    }

    if (n <= 0) {
        *result = 0.0;
        return 0;
    }

    double* devInput = nullptr;
    double* devOutput = nullptr;
    cudaError_t err;

    // Allocate device memory
    err = cudaMalloc(&devInput, n * sizeof(double));
    if (err != cudaSuccess) return -1;

    err = cudaMalloc(&devOutput, sizeof(double));
    if (err != cudaSuccess) {
        cudaFree(devInput);
        return -2;
    }

    // Copy input to device
    err = cudaMemcpy(devInput, hostData, n * sizeof(double), cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        cudaFree(devInput);
        cudaFree(devOutput);
        return -3;
    }

    // Initialize output to zero
    err = cudaMemset(devOutput, 0, sizeof(double));
    if (err != cudaSuccess) {
        cudaFree(devInput);
        cudaFree(devOutput);
        return -4;
    }

    // Launch kernel
    int blockSize = 256;
    int numBlocks = min((int)((n + blockSize - 1) / blockSize), 1024);
    sumFloat64Kernel<<<numBlocks, blockSize>>>(devInput, devOutput, n);

    // Synchronize and check for kernel errors
    err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        cudaFree(devInput);
        cudaFree(devOutput);
        return -5;
    }

    // Copy result back
    err = cudaMemcpy(result, devOutput, sizeof(double), cudaMemcpyDeviceToHost);
    if (err != cudaSuccess) {
        cudaFree(devInput);
        cudaFree(devOutput);
        return -6;
    }

    // Cleanup
    cudaFree(devInput);
    cudaFree(devOutput);

    return 0;
}

// Sum int64 array on GPU
int gpuSumInt64(const int64_t* hostData, int64_t n, int64_t* result) {
    if (n <= 0) {
        *result = 0;
        return 0;
    }

    int64_t* devInput = nullptr;
    int64_t* devOutput = nullptr;
    cudaError_t err;

    // Allocate device memory
    err = cudaMalloc(&devInput, n * sizeof(int64_t));
    if (err != cudaSuccess) return -1;

    err = cudaMalloc(&devOutput, sizeof(int64_t));
    if (err != cudaSuccess) {
        cudaFree(devInput);
        return -1;
    }

    // Copy input to device
    err = cudaMemcpy(devInput, hostData, n * sizeof(int64_t), cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        cudaFree(devInput);
        cudaFree(devOutput);
        return -1;
    }

    // Initialize output to zero
    err = cudaMemset(devOutput, 0, sizeof(int64_t));
    if (err != cudaSuccess) {
        cudaFree(devInput);
        cudaFree(devOutput);
        return -1;
    }

    // Launch kernel
    int blockSize = 256;
    int numBlocks = min((int)((n + blockSize - 1) / blockSize), 1024);
    sumInt64Kernel<<<numBlocks, blockSize>>>(devInput, devOutput, n);

    // Synchronize and check for kernel errors
    err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        cudaFree(devInput);
        cudaFree(devOutput);
        return -1;
    }

    // Copy result back
    err = cudaMemcpy(result, devOutput, sizeof(int64_t), cudaMemcpyDeviceToHost);

    // Cleanup
    cudaFree(devInput);
    cudaFree(devOutput);

    return (err == cudaSuccess) ? 0 : -1;
}

// ============================================================================
// GPU Array Operations (data stays on GPU)
// ============================================================================

// Sum float64 array already on GPU (no host-device transfer)
int gpuSumFloat64OnDevice(const double* devInput, int64_t n, double* result) {
    if (n <= 0) {
        *result = 0.0;
        return 0;
    }

    double* devOutput = nullptr;
    cudaError_t err;

    err = cudaMalloc(&devOutput, sizeof(double));
    if (err != cudaSuccess) return -1;

    err = cudaMemset(devOutput, 0, sizeof(double));
    if (err != cudaSuccess) {
        cudaFree(devOutput);
        return -2;
    }

    int blockSize = 256;
    int numBlocks = min((int)((n + blockSize - 1) / blockSize), 1024);
    sumFloat64Kernel<<<numBlocks, blockSize>>>(devInput, devOutput, n);

    err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        cudaFree(devOutput);
        return -3;
    }

    err = cudaMemcpy(result, devOutput, sizeof(double), cudaMemcpyDeviceToHost);
    cudaFree(devOutput);

    return (err == cudaSuccess) ? 0 : -4;
}

// Filter float64 array on GPU: keep elements where value > threshold
// Returns new array size in *outN, caller must free returned pointer
double* gpuFilterGreaterThan(const double* devInput, int64_t n, double threshold, int64_t* outN) {
    if (n <= 0) {
        *outN = 0;
        return nullptr;
    }

    cudaError_t err;

    // First pass: count matching elements
    int64_t* devCount = nullptr;
    err = cudaMalloc(&devCount, sizeof(int64_t));
    if (err != cudaSuccess) return nullptr;

    err = cudaMemset(devCount, 0, sizeof(int64_t));
    if (err != cudaSuccess) {
        cudaFree(devCount);
        return nullptr;
    }

    int blockSize = 256;
    int numBlocks = min((int)((n + blockSize - 1) / blockSize), 1024);
    countGreaterThanKernel<<<numBlocks, blockSize>>>(devInput, n, threshold, devCount);

    int64_t count = 0;
    err = cudaMemcpy(&count, devCount, sizeof(int64_t), cudaMemcpyDeviceToHost);
    cudaFree(devCount);

    if (err != cudaSuccess || count <= 0) {
        *outN = 0;
        return nullptr;
    }

    // Allocate output array
    double* devOutput = nullptr;
    err = cudaMalloc(&devOutput, count * sizeof(double));
    if (err != cudaSuccess) {
        *outN = 0;
        return nullptr;
    }

    // Second pass: compact matching elements
    int64_t* devOutIndex = nullptr;
    err = cudaMalloc(&devOutIndex, sizeof(int64_t));
    if (err != cudaSuccess) {
        cudaFree(devOutput);
        *outN = 0;
        return nullptr;
    }

    err = cudaMemset(devOutIndex, 0, sizeof(int64_t));
    if (err != cudaSuccess) {
        cudaFree(devOutput);
        cudaFree(devOutIndex);
        *outN = 0;
        return nullptr;
    }

    filterGreaterThanKernel<<<numBlocks, blockSize>>>(devInput, n, threshold, devOutput, devOutIndex);

    err = cudaDeviceSynchronize();
    cudaFree(devOutIndex);

    if (err != cudaSuccess) {
        cudaFree(devOutput);
        *outN = 0;
        return nullptr;
    }

    *outN = count;
    return devOutput;
}

// Chained operation: filter then sum (all on GPU)
// Filters elements where value > threshold, then sums the result
int gpuFilterThenSum(const double* hostData, int64_t n, double threshold, double* result) {
    if (n <= 0) {
        *result = 0.0;
        return 0;
    }

    cudaError_t err;

    // Copy input to device
    double* devInput = nullptr;
    err = cudaMalloc(&devInput, n * sizeof(double));
    if (err != cudaSuccess) return -1;

    err = cudaMemcpy(devInput, hostData, n * sizeof(double), cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        cudaFree(devInput);
        return -2;
    }

    // Filter on GPU
    int64_t filteredN = 0;
    double* devFiltered = gpuFilterGreaterThan(devInput, n, threshold, &filteredN);
    cudaFree(devInput);

    if (filteredN == 0) {
        *result = 0.0;
        return 0;
    }

    // Sum filtered data (still on GPU)
    int ret = gpuSumFloat64OnDevice(devFiltered, filteredN, result);
    cudaFree(devFiltered);

    return ret;
}

// Get last CUDA error message
const char* gpuGetLastError() {
    return cudaGetErrorString(cudaGetLastError());
}

// Batched sum: sum multiple columns in one GPU pass
// flatData: all columns concatenated (column-major: col0[0..n-1], col1[0..n-1], ...)
// numColumns: number of columns
// n: number of rows per column
// results: output array (numColumns elements)
// Returns 0 on success, negative on error
int gpuSumFloat64Batch(const double* flatData, int numColumns, int64_t n, double* results) {
    if (numColumns <= 0 || n <= 0) {
        for (int i = 0; i < numColumns; i++) {
            results[i] = 0.0;
        }
        return 0;
    }

    cudaError_t err;

    // Allocate device memory for all columns at once
    double* devData = nullptr;
    int64_t totalSize = (int64_t)numColumns * n;
    err = cudaMalloc(&devData, totalSize * sizeof(double));
    if (err != cudaSuccess) return -1;

    // Copy all data to device (already contiguous)
    err = cudaMemcpy(devData, flatData, totalSize * sizeof(double), cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        cudaFree(devData);
        return -2;
    }

    // Allocate device memory for results
    double* devResults = nullptr;
    err = cudaMalloc(&devResults, numColumns * sizeof(double));
    if (err != cudaSuccess) {
        cudaFree(devData);
        return -3;
    }
    err = cudaMemset(devResults, 0, numColumns * sizeof(double));
    if (err != cudaSuccess) {
        cudaFree(devData);
        cudaFree(devResults);
        return -4;
    }

    // Launch kernel for each column
    int blockSize = 256;
    int numBlocks = min((int)((n + blockSize - 1) / blockSize), 1024);

    for (int c = 0; c < numColumns; c++) {
        sumFloat64Kernel<<<numBlocks, blockSize>>>(devData + c * n, devResults + c, n);
    }

    // Synchronize
    err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        cudaFree(devData);
        cudaFree(devResults);
        return -5;
    }

    // Copy results back
    err = cudaMemcpy(results, devResults, numColumns * sizeof(double), cudaMemcpyDeviceToHost);

    cudaFree(devData);
    cudaFree(devResults);

    return (err == cudaSuccess) ? 0 : -6;
}

// ============================================================================
// FFT Operations using cuFFT
// ============================================================================

// Kernel to compute magnitude from complex FFT output
__global__ void computeMagnitudeKernel(const cufftDoubleComplex* input, double* output, int64_t n) {
    int64_t idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < n) {
        double real = input[idx].x;
        double imag = input[idx].y;
        output[idx] = sqrt(real * real + imag * imag);
    }
}

// Kernel to compute phase from complex FFT output
__global__ void computePhaseKernel(const cufftDoubleComplex* input, double* output, int64_t n) {
    int64_t idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < n) {
        output[idx] = atan2(input[idx].y, input[idx].x);
    }
}

// Kernel to compute magnitude and phase together
__global__ void computeMagnitudePhaseKernel(const cufftDoubleComplex* input,
                                             double* magnitude, double* phase, int64_t n) {
    int64_t idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < n) {
        double real = input[idx].x;
        double imag = input[idx].y;
        magnitude[idx] = sqrt(real * real + imag * imag);
        phase[idx] = atan2(imag, real);
    }
}

// FFT of real-valued input, returns magnitude spectrum
// Input: n real values (time domain)
// Output: n/2+1 magnitude values (frequency domain, positive frequencies only)
// Returns output size in *outN, caller receives magnitude array
int gpuFFTMagnitude(const double* hostData, int64_t n, double** hostMagnitude, int64_t* outN) {
    if (n <= 0) {
        *outN = 0;
        *hostMagnitude = nullptr;
        return 0;
    }

    cudaError_t cudaErr;
    cufftResult fftErr;

    // FFT of n real points produces n/2+1 complex values
    int64_t complexN = n / 2 + 1;
    *outN = complexN;

    // Allocate device memory for input
    double* devInput = nullptr;
    cudaErr = cudaMalloc(&devInput, n * sizeof(double));
    if (cudaErr != cudaSuccess) return -1;

    // Copy input to device
    cudaErr = cudaMemcpy(devInput, hostData, n * sizeof(double), cudaMemcpyHostToDevice);
    if (cudaErr != cudaSuccess) {
        cudaFree(devInput);
        return -2;
    }

    // Allocate device memory for complex FFT output
    cufftDoubleComplex* devComplex = nullptr;
    cudaErr = cudaMalloc(&devComplex, complexN * sizeof(cufftDoubleComplex));
    if (cudaErr != cudaSuccess) {
        cudaFree(devInput);
        return -3;
    }

    // Create cuFFT plan for real-to-complex transform
    cufftHandle plan;
    fftErr = cufftPlan1d(&plan, n, CUFFT_D2Z, 1);
    if (fftErr != CUFFT_SUCCESS) {
        cudaFree(devInput);
        cudaFree(devComplex);
        return -4;
    }

    // Execute FFT
    fftErr = cufftExecD2Z(plan, devInput, devComplex);
    if (fftErr != CUFFT_SUCCESS) {
        cufftDestroy(plan);
        cudaFree(devInput);
        cudaFree(devComplex);
        return -5;
    }

    // Free input and plan (no longer needed)
    cudaFree(devInput);
    cufftDestroy(plan);

    // Allocate device memory for magnitude output
    double* devMagnitude = nullptr;
    cudaErr = cudaMalloc(&devMagnitude, complexN * sizeof(double));
    if (cudaErr != cudaSuccess) {
        cudaFree(devComplex);
        return -6;
    }

    // Compute magnitude
    int blockSize = 256;
    int numBlocks = (complexN + blockSize - 1) / blockSize;
    computeMagnitudeKernel<<<numBlocks, blockSize>>>(devComplex, devMagnitude, complexN);

    cudaErr = cudaDeviceSynchronize();
    if (cudaErr != cudaSuccess) {
        cudaFree(devComplex);
        cudaFree(devMagnitude);
        return -7;
    }

    // Free complex data
    cudaFree(devComplex);

    // Allocate host memory for result
    *hostMagnitude = (double*)malloc(complexN * sizeof(double));
    if (*hostMagnitude == nullptr) {
        cudaFree(devMagnitude);
        return -8;
    }

    // Copy result back to host
    cudaErr = cudaMemcpy(*hostMagnitude, devMagnitude, complexN * sizeof(double), cudaMemcpyDeviceToHost);
    cudaFree(devMagnitude);

    if (cudaErr != cudaSuccess) {
        free(*hostMagnitude);
        *hostMagnitude = nullptr;
        return -9;
    }

    return 0;
}

// FFT of real-valued input, returns both magnitude and phase
// Input: n real values (time domain)
// Output: n/2+1 magnitude and phase values each
int gpuFFTMagnitudePhase(const double* hostData, int64_t n,
                          double** hostMagnitude, double** hostPhase, int64_t* outN) {
    if (n <= 0) {
        *outN = 0;
        *hostMagnitude = nullptr;
        *hostPhase = nullptr;
        return 0;
    }

    cudaError_t cudaErr;
    cufftResult fftErr;

    int64_t complexN = n / 2 + 1;
    *outN = complexN;

    // Allocate and copy input
    double* devInput = nullptr;
    cudaErr = cudaMalloc(&devInput, n * sizeof(double));
    if (cudaErr != cudaSuccess) return -1;

    cudaErr = cudaMemcpy(devInput, hostData, n * sizeof(double), cudaMemcpyHostToDevice);
    if (cudaErr != cudaSuccess) {
        cudaFree(devInput);
        return -2;
    }

    // Allocate complex output
    cufftDoubleComplex* devComplex = nullptr;
    cudaErr = cudaMalloc(&devComplex, complexN * sizeof(cufftDoubleComplex));
    if (cudaErr != cudaSuccess) {
        cudaFree(devInput);
        return -3;
    }

    // Create and execute FFT plan
    cufftHandle plan;
    fftErr = cufftPlan1d(&plan, n, CUFFT_D2Z, 1);
    if (fftErr != CUFFT_SUCCESS) {
        cudaFree(devInput);
        cudaFree(devComplex);
        return -4;
    }

    fftErr = cufftExecD2Z(plan, devInput, devComplex);
    cufftDestroy(plan);
    cudaFree(devInput);

    if (fftErr != CUFFT_SUCCESS) {
        cudaFree(devComplex);
        return -5;
    }

    // Allocate magnitude and phase arrays
    double* devMagnitude = nullptr;
    double* devPhase = nullptr;
    cudaErr = cudaMalloc(&devMagnitude, complexN * sizeof(double));
    if (cudaErr != cudaSuccess) {
        cudaFree(devComplex);
        return -6;
    }
    cudaErr = cudaMalloc(&devPhase, complexN * sizeof(double));
    if (cudaErr != cudaSuccess) {
        cudaFree(devComplex);
        cudaFree(devMagnitude);
        return -7;
    }

    // Compute magnitude and phase
    int blockSize = 256;
    int numBlocks = (complexN + blockSize - 1) / blockSize;
    computeMagnitudePhaseKernel<<<numBlocks, blockSize>>>(devComplex, devMagnitude, devPhase, complexN);

    cudaErr = cudaDeviceSynchronize();
    cudaFree(devComplex);

    if (cudaErr != cudaSuccess) {
        cudaFree(devMagnitude);
        cudaFree(devPhase);
        return -8;
    }

    // Allocate and copy results to host
    *hostMagnitude = (double*)malloc(complexN * sizeof(double));
    *hostPhase = (double*)malloc(complexN * sizeof(double));
    if (*hostMagnitude == nullptr || *hostPhase == nullptr) {
        cudaFree(devMagnitude);
        cudaFree(devPhase);
        if (*hostMagnitude) free(*hostMagnitude);
        if (*hostPhase) free(*hostPhase);
        return -9;
    }

    cudaMemcpy(*hostMagnitude, devMagnitude, complexN * sizeof(double), cudaMemcpyDeviceToHost);
    cudaMemcpy(*hostPhase, devPhase, complexN * sizeof(double), cudaMemcpyDeviceToHost);

    cudaFree(devMagnitude);
    cudaFree(devPhase);

    return 0;
}

// Free memory allocated by FFT functions
void gpuFFTFree(double* ptr) {
    if (ptr != nullptr) {
        free(ptr);
    }
}

// ============================================================================
// Convolution Kernels
// ============================================================================

// Direct convolution kernel - O(n*m) where n=signal length, m=kernel length
// Each thread computes one output element
__global__ void directConvolveKernel(const double* signal, int64_t signalLen,
                                      const double* kernel, int64_t kernelLen,
                                      double* output) {
    int64_t i = blockIdx.x * blockDim.x + threadIdx.x;
    int64_t outputLen = signalLen + kernelLen - 1;

    if (i < outputLen) {
        double sum = 0.0;
        // Convolution: output[i] = sum over k of signal[i-k] * kernel[k]
        for (int64_t k = 0; k < kernelLen; k++) {
            int64_t signalIdx = i - k;
            if (signalIdx >= 0 && signalIdx < signalLen) {
                sum += signal[signalIdx] * kernel[k];
            }
        }
        output[i] = sum;
    }
}

// Complex multiplication kernel for FFT-based convolution
__global__ void complexMultiplyKernel(cufftDoubleComplex* a, const cufftDoubleComplex* b, int64_t n) {
    int64_t i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i < n) {
        double ar = a[i].x, ai = a[i].y;
        double br = b[i].x, bi = b[i].y;
        a[i].x = ar * br - ai * bi;
        a[i].y = ar * bi + ai * br;
    }
}

// Direct convolution - simple O(n*m) approach
// Good for small kernels, baseline for comparison
int gpuConvolveDirect(const double* hostSignal, int64_t signalLen,
                      const double* hostKernel, int64_t kernelLen,
                      double** hostOutput, int64_t* outLen) {
    if (signalLen <= 0 || kernelLen <= 0) {
        *outLen = 0;
        *hostOutput = nullptr;
        return 0;
    }

    cudaError_t err;
    int64_t outputLen = signalLen + kernelLen - 1;
    *outLen = outputLen;

    // Allocate device memory
    double* devSignal = nullptr;
    double* devKernel = nullptr;
    double* devOutput = nullptr;

    err = cudaMalloc(&devSignal, signalLen * sizeof(double));
    if (err != cudaSuccess) return -1;

    err = cudaMalloc(&devKernel, kernelLen * sizeof(double));
    if (err != cudaSuccess) {
        cudaFree(devSignal);
        return -2;
    }

    err = cudaMalloc(&devOutput, outputLen * sizeof(double));
    if (err != cudaSuccess) {
        cudaFree(devSignal);
        cudaFree(devKernel);
        return -3;
    }

    // Copy data to device
    cudaMemcpy(devSignal, hostSignal, signalLen * sizeof(double), cudaMemcpyHostToDevice);
    cudaMemcpy(devKernel, hostKernel, kernelLen * sizeof(double), cudaMemcpyHostToDevice);

    // Launch kernel
    int blockSize = 256;
    int numBlocks = (outputLen + blockSize - 1) / blockSize;
    directConvolveKernel<<<numBlocks, blockSize>>>(devSignal, signalLen, devKernel, kernelLen, devOutput);

    err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        cudaFree(devSignal);
        cudaFree(devKernel);
        cudaFree(devOutput);
        return -4;
    }

    // Allocate and copy result
    *hostOutput = (double*)malloc(outputLen * sizeof(double));
    if (*hostOutput == nullptr) {
        cudaFree(devSignal);
        cudaFree(devKernel);
        cudaFree(devOutput);
        return -5;
    }

    cudaMemcpy(*hostOutput, devOutput, outputLen * sizeof(double), cudaMemcpyDeviceToHost);

    cudaFree(devSignal);
    cudaFree(devKernel);
    cudaFree(devOutput);

    return 0;
}

// FFT-based convolution - O(n log n) using convolution theorem
// signal * kernel = IFFT(FFT(signal) * FFT(kernel))
int gpuConvolveFFT(const double* hostSignal, int64_t signalLen,
                   const double* hostKernel, int64_t kernelLen,
                   double** hostOutput, int64_t* outLen) {
    if (signalLen <= 0 || kernelLen <= 0) {
        *outLen = 0;
        *hostOutput = nullptr;
        return 0;
    }

    cudaError_t cudaErr;
    cufftResult fftErr;

    // Output length for linear convolution
    int64_t outputLen = signalLen + kernelLen - 1;
    *outLen = outputLen;

    // FFT size - pad to next power of 2 for efficiency
    int64_t fftSize = 1;
    while (fftSize < outputLen) fftSize *= 2;
    int64_t complexN = fftSize / 2 + 1;

    // Allocate device memory for padded signals
    double* devSignal = nullptr;
    double* devKernel = nullptr;
    cudaErr = cudaMalloc(&devSignal, fftSize * sizeof(double));
    if (cudaErr != cudaSuccess) return -1;
    cudaErr = cudaMalloc(&devKernel, fftSize * sizeof(double));
    if (cudaErr != cudaSuccess) {
        cudaFree(devSignal);
        return -2;
    }

    // Zero-pad and copy signal
    cudaMemset(devSignal, 0, fftSize * sizeof(double));
    cudaMemcpy(devSignal, hostSignal, signalLen * sizeof(double), cudaMemcpyHostToDevice);

    // Zero-pad and copy kernel
    cudaMemset(devKernel, 0, fftSize * sizeof(double));
    cudaMemcpy(devKernel, hostKernel, kernelLen * sizeof(double), cudaMemcpyHostToDevice);

    // Allocate complex arrays for FFT output
    cufftDoubleComplex* devSignalFFT = nullptr;
    cufftDoubleComplex* devKernelFFT = nullptr;
    cudaErr = cudaMalloc(&devSignalFFT, complexN * sizeof(cufftDoubleComplex));
    if (cudaErr != cudaSuccess) {
        cudaFree(devSignal);
        cudaFree(devKernel);
        return -3;
    }
    cudaErr = cudaMalloc(&devKernelFFT, complexN * sizeof(cufftDoubleComplex));
    if (cudaErr != cudaSuccess) {
        cudaFree(devSignal);
        cudaFree(devKernel);
        cudaFree(devSignalFFT);
        return -4;
    }

    // Create forward FFT plan (real-to-complex)
    cufftHandle planFwd;
    fftErr = cufftPlan1d(&planFwd, fftSize, CUFFT_D2Z, 1);
    if (fftErr != CUFFT_SUCCESS) {
        cudaFree(devSignal);
        cudaFree(devKernel);
        cudaFree(devSignalFFT);
        cudaFree(devKernelFFT);
        return -5;
    }

    // Forward FFT of signal
    fftErr = cufftExecD2Z(planFwd, devSignal, devSignalFFT);
    if (fftErr != CUFFT_SUCCESS) {
        cufftDestroy(planFwd);
        cudaFree(devSignal);
        cudaFree(devKernel);
        cudaFree(devSignalFFT);
        cudaFree(devKernelFFT);
        return -6;
    }

    // Forward FFT of kernel
    fftErr = cufftExecD2Z(planFwd, devKernel, devKernelFFT);
    cufftDestroy(planFwd);
    cudaFree(devSignal);
    cudaFree(devKernel);

    if (fftErr != CUFFT_SUCCESS) {
        cudaFree(devSignalFFT);
        cudaFree(devKernelFFT);
        return -7;
    }

    // Multiply in frequency domain
    int blockSize = 256;
    int numBlocks = (complexN + blockSize - 1) / blockSize;
    complexMultiplyKernel<<<numBlocks, blockSize>>>(devSignalFFT, devKernelFFT, complexN);
    cudaFree(devKernelFFT);

    // Allocate output for inverse FFT
    double* devOutput = nullptr;
    cudaErr = cudaMalloc(&devOutput, fftSize * sizeof(double));
    if (cudaErr != cudaSuccess) {
        cudaFree(devSignalFFT);
        return -8;
    }

    // Create inverse FFT plan (complex-to-real)
    cufftHandle planInv;
    fftErr = cufftPlan1d(&planInv, fftSize, CUFFT_Z2D, 1);
    if (fftErr != CUFFT_SUCCESS) {
        cudaFree(devSignalFFT);
        cudaFree(devOutput);
        return -9;
    }

    // Inverse FFT
    fftErr = cufftExecZ2D(planInv, devSignalFFT, devOutput);
    cufftDestroy(planInv);
    cudaFree(devSignalFFT);

    if (fftErr != CUFFT_SUCCESS) {
        cudaFree(devOutput);
        return -10;
    }

    cudaErr = cudaDeviceSynchronize();
    if (cudaErr != cudaSuccess) {
        cudaFree(devOutput);
        return -11;
    }

    // Allocate host output and copy (only outputLen elements, not full fftSize)
    *hostOutput = (double*)malloc(outputLen * sizeof(double));
    if (*hostOutput == nullptr) {
        cudaFree(devOutput);
        return -12;
    }

    cudaMemcpy(*hostOutput, devOutput, outputLen * sizeof(double), cudaMemcpyDeviceToHost);
    cudaFree(devOutput);

    // Normalize by FFT size (cuFFT doesn't normalize)
    double scale = 1.0 / fftSize;
    for (int64_t i = 0; i < outputLen; i++) {
        (*hostOutput)[i] *= scale;
    }

    return 0;
}

} // extern "C"
