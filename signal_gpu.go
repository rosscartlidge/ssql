//go:build gpu

package ssql

import (
	"github.com/rosscartlidge/ssql/v4/gpu"
)

// gpuAvailableForSignal checks if GPU is available for signal processing.
func gpuAvailableForSignal() bool {
	return gpu.Available()
}

// fftMagnitudeGPU computes FFT magnitude on GPU using cuFFT.
func fftMagnitudeGPU(signal Signal) ([]float64, error) {
	return gpu.FFTMagnitude([]float64(signal))
}

// fftMagnitudePhaseGPU computes FFT magnitude and phase on GPU.
func fftMagnitudePhaseGPU(signal Signal) ([]float64, []float64, error) {
	return gpu.FFTMagnitudePhase([]float64(signal))
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
