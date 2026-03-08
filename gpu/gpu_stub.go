//go:build !gpu

// Package gpu provides CUDA GPU acceleration for ssql operations.
// This is the stub implementation for builds without GPU support.
//
// To enable GPU support, build with: go build -tags gpu
package gpu

import (
	"fmt"
	"math"
)

// ErrNotCompiled is returned when GPU functions are called but GPU support was not compiled in.
var ErrNotCompiled = fmt.Errorf("GPU support not compiled in (build with -tags gpu)")

// Init is a no-op without GPU support.
func Init() bool {
	return false
}

// Available returns false when GPU support is not compiled in.
func Available() bool {
	return false
}

// LastError returns a message indicating GPU support is not available.
func LastError() string {
	return "GPU support not compiled in"
}

// SumFloat64 returns an error when GPU support is not compiled in.
func SumFloat64(data []float64) (float64, error) {
	return 0, ErrNotCompiled
}

// SumInt64 returns an error when GPU support is not compiled in.
func SumInt64(data []int64) (int64, error) {
	return 0, ErrNotCompiled
}

// SumFloat64CPU computes sum on CPU.
func SumFloat64CPU(data []float64) float64 {
	var sum float64
	for _, v := range data {
		sum += v
	}
	return sum
}

// SumInt64CPU computes sum on CPU.
func SumInt64CPU(data []int64) int64 {
	var sum int64
	for _, v := range data {
		sum += v
	}
	return sum
}

// FilterThenSum returns an error when GPU support is not compiled in.
func FilterThenSum(data []float64, threshold float64) (float64, error) {
	return 0, ErrNotCompiled
}

// FilterThenSumCPU filters elements > threshold and sums them on CPU.
func FilterThenSumCPU(data []float64, threshold float64) float64 {
	var sum float64
	for _, v := range data {
		if v > threshold {
			sum += v
		}
	}
	return sum
}

// SumFloat64Batch returns an error when GPU support is not compiled in.
func SumFloat64Batch(columns [][]float64) ([]float64, error) {
	return nil, ErrNotCompiled
}

// SumFloat64BatchCPU sums multiple columns on CPU.
func SumFloat64BatchCPU(columns [][]float64) []float64 {
	results := make([]float64, len(columns))
	for i, col := range columns {
		for _, v := range col {
			results[i] += v
		}
	}
	return results
}

// BatchedFFTMagnitude returns an error when GPU support is not compiled in.
func BatchedFFTMagnitude(frames []float64, windowSize, numFrames int, window []float64) ([]float64, int, error) {
	return nil, 0, ErrNotCompiled
}

// FFTMagnitude returns an error when GPU support is not compiled in.
func FFTMagnitude(data []float64) ([]float64, error) {
	return nil, ErrNotCompiled
}

// FFTMagnitudePhase returns an error when GPU support is not compiled in.
func FFTMagnitudePhase(data []float64) (magnitude []float64, phase []float64, err error) {
	return nil, nil, ErrNotCompiled
}

// FFTMagnitudeCPU computes FFT magnitude on CPU using a naive DFT.
// Note: This is O(n²) and much slower than GPU FFT for large n.
func FFTMagnitudeCPU(data []float64) []float64 {
	n := len(data)
	if n == 0 {
		return []float64{}
	}

	// For real input, only need n/2+1 positive frequencies
	outN := n/2 + 1
	magnitude := make([]float64, outN)

	for k := range outN {
		var real, imag float64
		for t := range n {
			angle := -2 * math.Pi * float64(k) * float64(t) / float64(n)
			real += data[t] * math.Cos(angle)
			imag += data[t] * math.Sin(angle)
		}
		magnitude[k] = math.Sqrt(real*real + imag*imag)
	}

	return magnitude
}
