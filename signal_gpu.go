//go:build gpu

package ssql

import (
	"github.com/rosscartlidge/ssql/v4/gpu"
)

// gpuAvailableForSignal checks if GPU is available for signal processing.
func gpuAvailableForSignal() bool {
	return gpu.Available()
}

// convolveGPU computes convolution on GPU.
// Uses direct convolution for kernels < 10K, FFT-based for larger.
func convolveGPU(signal, kernel Signal) (Signal, error) {
	// FFT-based convolution is better for very large kernels
	if len(kernel) >= 10000 {
		result, err := gpu.ConvolveFFT([]float64(signal), []float64(kernel))
		return Signal(result), err
	}
	result, err := gpu.ConvolveDirect([]float64(signal), []float64(kernel))
	return Signal(result), err
}
