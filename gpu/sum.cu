// CUDA parallel sum reduction kernel
// Optimized for modern GPUs with warp-level primitives

#include <cuda_runtime.h>
#include <stdint.h>

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

// Get last CUDA error message
const char* gpuGetLastError() {
    return cudaGetErrorString(cudaGetLastError());
}

} // extern "C"
