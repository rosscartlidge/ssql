//go:build gpu

package ssql

import (
	"github.com/rosscartlidge/ssql/v4/gpu"
)

// gpuAvailableForSignal checks if GPU is available for signal processing.
func gpuAvailableForSignal() bool {
	return gpu.Available()
}

// fftMagnitudeGPU computes FFT magnitude on GPU.
// Benchmarks show GPU wins at >= 16K samples (up to 54x faster at 64M).
func fftMagnitudeGPU(signal Signal) ([]float64, error) {
	return gpu.FFTMagnitude([]float64(signal))
}

// fftMagnitudePhaseGPU computes FFT magnitude and phase on GPU.
func fftMagnitudePhaseGPU(signal Signal) ([]float64, []float64, error) {
	return gpu.FFTMagnitudePhase([]float64(signal))
}

// convolveGPU computes convolution on GPU.
// Uses direct convolution for kernels < 2K, FFT-based for larger.
// Benchmarks show FFT wins at kernel >= 2K for 1M+ signals.
func convolveGPU(signal, kernel Signal) (Signal, error) {
	// FFT-based convolution is better for large kernels
	// Crossover point is ~2K based on benchmarks
	if len(kernel) >= 2048 {
		result, err := gpu.ConvolveFFT([]float64(signal), []float64(kernel))
		return Signal(result), err
	}
	result, err := gpu.ConvolveDirect([]float64(signal), []float64(kernel))
	return Signal(result), err
}
