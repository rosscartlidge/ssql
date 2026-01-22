//go:build gpu

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
int gpuFilterThenSum(const double* hostData, int64_t n, double threshold, double* result);
int gpuSumFloat64Batch(const double* flatData, int numColumns, int64_t n, double* results);
int gpuFFTMagnitude(const double* hostData, int64_t n, double** hostMagnitude, int64_t* outN);
int gpuFFTMagnitudePhase(const double* hostData, int64_t n, double** hostMagnitude, double** hostPhase, int64_t* outN);
void gpuFFTFree(double* ptr);
int gpuConvolveDirect(const double* hostSignal, int64_t signalLen, const double* hostKernel, int64_t kernelLen, double** hostOutput, int64_t* outLen);
int gpuConvolveFFT(const double* hostSignal, int64_t signalLen, const double* hostKernel, int64_t kernelLen, double** hostOutput, int64_t* outLen);
int gpuIFFT(const double* hostMagnitude, const double* hostPhase, int64_t numBins, double** hostOutput, int64_t* outLen);
const char* gpuGetLastError();
*/
import "C"
import (
	"fmt"
	"math"
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

// FilterThenSum filters elements > threshold and sums them, all on GPU.
// This demonstrates chained GPU operations where data stays on GPU between operations,
// avoiding the host-device transfer overhead that dominates single operations.
func FilterThenSum(data []float64, threshold float64) (float64, error) {
	if !Available() {
		return 0, fmt.Errorf("GPU not available")
	}

	if len(data) == 0 {
		return 0, nil
	}

	var result C.double
	ret := C.gpuFilterThenSum(
		(*C.double)(unsafe.Pointer(&data[0])),
		C.int64_t(len(data)),
		C.double(threshold),
		&result,
	)

	if ret != 0 {
		return 0, fmt.Errorf("GPU filter+sum failed (code %d): %s", int(ret), LastError())
	}

	return float64(result), nil
}

// FilterThenSumCPU filters elements > threshold and sums them on CPU (for benchmarking).
func FilterThenSumCPU(data []float64, threshold float64) float64 {
	var sum float64
	for _, v := range data {
		if v > threshold {
			sum += v
		}
	}
	return sum
}

// SumFloat64Batch sums multiple columns in one GPU pass.
// All columns must have the same length.
// Returns a slice of sums (one per column) and any error.
// This is more efficient than calling SumFloat64 multiple times because
// it amortizes the GPU transfer overhead across all columns.
func SumFloat64Batch(columns [][]float64) ([]float64, error) {
	if !Available() {
		return nil, fmt.Errorf("GPU not available")
	}

	numColumns := len(columns)
	if numColumns == 0 {
		return []float64{}, nil
	}

	// Get length from first column
	n := len(columns[0])
	if n == 0 {
		results := make([]float64, numColumns)
		return results, nil
	}

	// Verify all columns have same length
	for i, col := range columns {
		if len(col) != n {
			return nil, fmt.Errorf("column %d has length %d, expected %d", i, len(col), n)
		}
	}

	// Flatten columns into contiguous array (column-major)
	flatData := make([]float64, numColumns*n)
	for i, col := range columns {
		copy(flatData[i*n:], col)
	}

	// Allocate results
	results := make([]float64, numColumns)

	ret := C.gpuSumFloat64Batch(
		(*C.double)(unsafe.Pointer(&flatData[0])),
		C.int(numColumns),
		C.int64_t(n),
		(*C.double)(unsafe.Pointer(&results[0])),
	)

	if ret != 0 {
		return nil, fmt.Errorf("GPU batch sum failed (code %d): %s", int(ret), LastError())
	}

	return results, nil
}

// SumFloat64BatchCPU sums multiple columns on CPU (for comparison).
func SumFloat64BatchCPU(columns [][]float64) []float64 {
	results := make([]float64, len(columns))
	for i, col := range columns {
		for _, v := range col {
			results[i] += v
		}
	}
	return results
}

// FFTMagnitude computes the FFT of a real-valued signal and returns the magnitude spectrum.
// Input: n real values (time domain)
// Output: n/2+1 magnitude values (frequency domain, positive frequencies only)
// This is a compute-bound operation that benefits significantly from GPU acceleration.
func FFTMagnitude(data []float64) ([]float64, error) {
	if !Available() {
		return nil, fmt.Errorf("GPU not available")
	}

	if len(data) == 0 {
		return []float64{}, nil
	}

	var magnitude *C.double
	var outN C.int64_t

	ret := C.gpuFFTMagnitude(
		(*C.double)(unsafe.Pointer(&data[0])),
		C.int64_t(len(data)),
		&magnitude,
		&outN,
	)

	if ret != 0 {
		return nil, fmt.Errorf("GPU FFT failed (code %d): %s", int(ret), LastError())
	}

	// Copy result to Go slice and free C memory
	n := int(outN)
	result := make([]float64, n)
	for i := 0; i < n; i++ {
		result[i] = float64(*(*C.double)(unsafe.Pointer(uintptr(unsafe.Pointer(magnitude)) + uintptr(i)*8)))
	}
	C.gpuFFTFree(magnitude)

	return result, nil
}

// FFTMagnitudePhase computes the FFT and returns both magnitude and phase spectra.
// Input: n real values (time domain)
// Output: n/2+1 magnitude values and n/2+1 phase values (in radians)
func FFTMagnitudePhase(data []float64) (magnitude []float64, phase []float64, err error) {
	if !Available() {
		return nil, nil, fmt.Errorf("GPU not available")
	}

	if len(data) == 0 {
		return []float64{}, []float64{}, nil
	}

	var magPtr, phasePtr *C.double
	var outN C.int64_t

	ret := C.gpuFFTMagnitudePhase(
		(*C.double)(unsafe.Pointer(&data[0])),
		C.int64_t(len(data)),
		&magPtr,
		&phasePtr,
		&outN,
	)

	if ret != 0 {
		return nil, nil, fmt.Errorf("GPU FFT failed (code %d): %s", int(ret), LastError())
	}

	// Copy results to Go slices and free C memory
	n := int(outN)
	magnitude = make([]float64, n)
	phase = make([]float64, n)
	for i := 0; i < n; i++ {
		magnitude[i] = float64(*(*C.double)(unsafe.Pointer(uintptr(unsafe.Pointer(magPtr)) + uintptr(i)*8)))
		phase[i] = float64(*(*C.double)(unsafe.Pointer(uintptr(unsafe.Pointer(phasePtr)) + uintptr(i)*8)))
	}
	C.gpuFFTFree(magPtr)
	C.gpuFFTFree(phasePtr)

	return magnitude, phase, nil
}

// FFTMagnitudeCPU computes FFT magnitude on CPU using a naive DFT (for comparison).
// Note: This is O(n²) and much slower than GPU FFT for large n.
func FFTMagnitudeCPU(data []float64) []float64 {
	n := len(data)
	if n == 0 {
		return []float64{}
	}

	// For real input, only need n/2+1 positive frequencies
	outN := n/2 + 1
	magnitude := make([]float64, outN)

	for k := 0; k < outN; k++ {
		var real, imag float64
		for t := 0; t < n; t++ {
			angle := -2 * math.Pi * float64(k) * float64(t) / float64(n)
			real += data[t] * math.Cos(angle)
			imag += data[t] * math.Sin(angle)
		}
		magnitude[k] = math.Sqrt(real*real + imag*imag)
	}

	return magnitude
}

// ConvolveDirect computes convolution using direct O(n*m) method on GPU.
// Best for small kernels where FFT overhead isn't worth it.
func ConvolveDirect(signal, kernel []float64) ([]float64, error) {
	if !Available() {
		return nil, fmt.Errorf("GPU not available")
	}

	if len(signal) == 0 || len(kernel) == 0 {
		return []float64{}, nil
	}

	var output *C.double
	var outLen C.int64_t

	ret := C.gpuConvolveDirect(
		(*C.double)(unsafe.Pointer(&signal[0])),
		C.int64_t(len(signal)),
		(*C.double)(unsafe.Pointer(&kernel[0])),
		C.int64_t(len(kernel)),
		&output,
		&outLen,
	)

	if ret != 0 {
		return nil, fmt.Errorf("GPU convolution failed (code %d): %s", int(ret), LastError())
	}

	// Copy result to Go slice and free C memory
	n := int(outLen)
	result := make([]float64, n)
	for i := 0; i < n; i++ {
		result[i] = float64(*(*C.double)(unsafe.Pointer(uintptr(unsafe.Pointer(output)) + uintptr(i)*8)))
	}
	C.gpuFFTFree(output)

	return result, nil
}

// ConvolveFFT computes convolution using FFT-based O(n log n) method on GPU.
// Uses the convolution theorem: signal * kernel = IFFT(FFT(signal) * FFT(kernel))
// Best for larger signals/kernels where FFT efficiency dominates.
func ConvolveFFT(signal, kernel []float64) ([]float64, error) {
	if !Available() {
		return nil, fmt.Errorf("GPU not available")
	}

	if len(signal) == 0 || len(kernel) == 0 {
		return []float64{}, nil
	}

	var output *C.double
	var outLen C.int64_t

	ret := C.gpuConvolveFFT(
		(*C.double)(unsafe.Pointer(&signal[0])),
		C.int64_t(len(signal)),
		(*C.double)(unsafe.Pointer(&kernel[0])),
		C.int64_t(len(kernel)),
		&output,
		&outLen,
	)

	if ret != 0 {
		return nil, fmt.Errorf("GPU FFT convolution failed (code %d): %s", int(ret), LastError())
	}

	// Copy result to Go slice and free C memory
	n := int(outLen)
	result := make([]float64, n)
	for i := 0; i < n; i++ {
		result[i] = float64(*(*C.double)(unsafe.Pointer(uintptr(unsafe.Pointer(output)) + uintptr(i)*8)))
	}
	C.gpuFFTFree(output)

	return result, nil
}

// ConvolveCPU computes convolution on CPU using direct O(n*m) method (for comparison).
func ConvolveCPU(signal, kernel []float64) []float64 {
	if len(signal) == 0 || len(kernel) == 0 {
		return []float64{}
	}

	outputLen := len(signal) + len(kernel) - 1
	result := make([]float64, outputLen)

	for i := 0; i < outputLen; i++ {
		for k := 0; k < len(kernel); k++ {
			signalIdx := i - k
			if signalIdx >= 0 && signalIdx < len(signal) {
				result[i] += signal[signalIdx] * kernel[k]
			}
		}
	}

	return result
}

// IFFT computes the inverse FFT from magnitude and phase spectra.
// Input: numBins magnitude values and numBins phase values (in radians)
// Output: reconstructed time-domain signal of length 2*(numBins-1)
// This reconstructs the original signal from frequency-domain data.
func IFFT(magnitude, phase []float64) ([]float64, error) {
	if !Available() {
		return nil, fmt.Errorf("GPU not available")
	}

	if len(magnitude) == 0 {
		return []float64{}, nil
	}

	if len(magnitude) != len(phase) {
		return nil, fmt.Errorf("magnitude and phase must have same length: %d != %d", len(magnitude), len(phase))
	}

	var output *C.double
	var outLen C.int64_t

	ret := C.gpuIFFT(
		(*C.double)(unsafe.Pointer(&magnitude[0])),
		(*C.double)(unsafe.Pointer(&phase[0])),
		C.int64_t(len(magnitude)),
		&output,
		&outLen,
	)

	if ret != 0 {
		return nil, fmt.Errorf("GPU IFFT failed (code %d): %s", int(ret), LastError())
	}

	// Copy result to Go slice and free C memory
	n := int(outLen)
	result := make([]float64, n)
	for i := 0; i < n; i++ {
		result[i] = float64(*(*C.double)(unsafe.Pointer(uintptr(unsafe.Pointer(output)) + uintptr(i)*8)))
	}
	C.gpuFFTFree(output)

	return result, nil
}
