//go:build !gpu

package ssql

import "fmt"

// GPUAvailable returns true if GPU support is compiled in and a GPU is detected.
// Stub version: always returns false when GPU support not compiled in.
func GPUAvailable() bool {
	return false
}

// gpuAvailableForSignal checks if GPU is available for signal processing.
// Stub version: always returns false when GPU support not compiled in.
func gpuAvailableForSignal() bool {
	return false
}

// fftMagnitudeGPU computes FFT magnitude on GPU.
// Stub version: returns error when GPU support not compiled in.
func fftMagnitudeGPU(signal Signal) ([]float64, error) {
	return nil, fmt.Errorf("GPU support not compiled in - build with -tags gpu")
}

// fftMagnitudePhaseGPU computes FFT magnitude and phase on GPU.
// Stub version: returns error when GPU support not compiled in.
func fftMagnitudePhaseGPU(signal Signal) ([]float64, []float64, error) {
	return nil, nil, fmt.Errorf("GPU support not compiled in - build with -tags gpu")
}

// convolveGPU computes convolution on GPU.
// Stub version: returns error when GPU support not compiled in.
func convolveGPU(signal, kernel Signal) (Signal, error) {
	return nil, fmt.Errorf("GPU support not compiled in - build with -tags gpu")
}

// ifftGPU computes inverse FFT on GPU.
// Stub version: returns error when GPU support not compiled in.
func ifftGPU(magnitude, phase []float64) (Signal, error) {
	return nil, fmt.Errorf("GPU support not compiled in - build with -tags gpu")
}
