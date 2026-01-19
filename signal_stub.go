//go:build !gpu

package ssql

import "fmt"

// gpuAvailableForSignal checks if GPU is available for signal processing.
// Stub version: always returns false when GPU support not compiled in.
func gpuAvailableForSignal() bool {
	return false
}

// convolveGPU computes convolution on GPU.
// Stub version: returns error when GPU support not compiled in.
func convolveGPU(signal, kernel Signal) (Signal, error) {
	return nil, fmt.Errorf("GPU support not compiled in - build with -tags gpu")
}
