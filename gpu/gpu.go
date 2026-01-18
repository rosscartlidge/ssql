// Package gpu provides CUDA GPU acceleration for ssql operations.
//
// Build requirements:
//   - NVIDIA GPU with CUDA support
//   - CUDA Toolkit installed (nvcc compiler)
//   - Build the shared library: cd gpu && make
//
// The GPU functions fall back to CPU if GPU is not available.
package gpu

/*
#cgo LDFLAGS: -L${SRCDIR} -L/usr/local/lib -L/usr/local/cuda/lib64 -lssqlgpu -lcudart -lstdc++
#cgo CFLAGS: -I/usr/local/cuda/include

#include <stdint.h>

// Forward declarations of CUDA C functions
int gpuInit();
int gpuSumFloat64(const double* hostData, int64_t n, double* result);
int gpuSumInt64(const int64_t* hostData, int64_t n, int64_t* result);
const char* gpuGetLastError();
*/
import "C"
import (
	"fmt"
	"sync"
	"unsafe"
)

var (
	gpuInitOnce sync.Once
	gpuAvailable bool
)

// Init initializes the GPU. Call this once at startup for faster first operation.
// Returns true if GPU is available, false otherwise.
func Init() bool {
	gpuInitOnce.Do(func() {
		result := C.gpuInit()
		gpuAvailable = (result == 0)
	})
	return gpuAvailable
}

// Available returns true if GPU acceleration is available.
func Available() bool {
	Init()
	return gpuAvailable
}

// LastError returns the last CUDA error message.
func LastError() string {
	return C.GoString(C.gpuGetLastError())
}

// SumFloat64 computes the sum of a float64 slice on the GPU.
// Returns the sum and any error that occurred.
func SumFloat64(data []float64) (float64, error) {
	if !Available() {
		return 0, fmt.Errorf("GPU not available")
	}

	if len(data) == 0 {
		return 0, nil
	}

	var result C.double
	ret := C.gpuSumFloat64(
		(*C.double)(unsafe.Pointer(&data[0])),
		C.int64_t(len(data)),
		&result,
	)

	if ret != 0 {
		return 0, fmt.Errorf("GPU sum failed (code %d): %s", int(ret), LastError())
	}

	return float64(result), nil
}

// SumInt64 computes the sum of an int64 slice on the GPU.
// Returns the sum and any error that occurred.
func SumInt64(data []int64) (int64, error) {
	if !Available() {
		return 0, fmt.Errorf("GPU not available")
	}

	if len(data) == 0 {
		return 0, nil
	}

	var result C.int64_t
	ret := C.gpuSumInt64(
		(*C.int64_t)(unsafe.Pointer(&data[0])),
		C.int64_t(len(data)),
		&result,
	)

	if ret != 0 {
		return 0, fmt.Errorf("GPU sum failed: %s", LastError())
	}

	return int64(result), nil
}

// SumFloat64CPU computes sum on CPU (for benchmarking comparison).
func SumFloat64CPU(data []float64) float64 {
	var sum float64
	for _, v := range data {
		sum += v
	}
	return sum
}

// SumInt64CPU computes sum on CPU (for benchmarking comparison).
func SumInt64CPU(data []int64) int64 {
	var sum int64
	for _, v := range data {
		sum += v
	}
	return sum
}
