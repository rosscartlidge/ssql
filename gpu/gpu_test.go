//go:build gpu

package gpu

import (
	"math"
	"testing"
)

func TestGPUAvailable(t *testing.T) {
	// This test just checks that we can query GPU availability
	// It may return true or false depending on hardware
	_ = Available()
}

func TestSumFloat64(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	tests := []struct {
		name     string
		data     []float64
		expected float64
	}{
		{"empty", []float64{}, 0.0},
		{"single", []float64{42.0}, 42.0},
		{"ten_ones", make10Ones(), 10.0},
		{"hundred", make100Values(), 100 * 50.5}, // 1+2+...+100 = 5050, avg = 50.5
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SumFloat64(tt.data)
			if err != nil {
				t.Fatalf("SumFloat64 error: %v", err)
			}

			if tt.expected == 0 {
				if result != 0 {
					t.Errorf("expected 0, got %f", result)
				}
				return
			}

			relErr := math.Abs(result-tt.expected) / tt.expected
			if relErr > 1e-10 {
				t.Errorf("expected %f, got %f (relative error: %e)", tt.expected, result, relErr)
			}
		})
	}
}

func TestSumInt64(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	tests := []struct {
		name     string
		data     []int64
		expected int64
	}{
		{"empty", []int64{}, 0},
		{"single", []int64{42}, 42},
		{"ten_ones", []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, 10},
		{"sequence", makeSequence(100), 5050}, // 1+2+...+100
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SumInt64(tt.data)
			if err != nil {
				t.Fatalf("SumInt64 error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestSumFloat64Large(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	// Test with 1M elements
	n := 1_000_000
	data := make([]float64, n)
	for i := range data {
		data[i] = 1.0
	}

	result, err := SumFloat64(data)
	if err != nil {
		t.Fatalf("SumFloat64 error: %v", err)
	}

	expected := float64(n)
	relErr := math.Abs(result-expected) / expected
	if relErr > 1e-10 {
		t.Errorf("expected %f, got %f (relative error: %e)", expected, result, relErr)
	}
}

// Helper functions
func make10Ones() []float64 {
	data := make([]float64, 10)
	for i := range data {
		data[i] = 1.0
	}
	return data
}

func make100Values() []float64 {
	data := make([]float64, 100)
	for i := range data {
		data[i] = float64(i + 1)
	}
	return data
}

func makeSequence(n int) []int64 {
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i + 1)
	}
	return data
}

func BenchmarkSumFloat64CPU(b *testing.B) {
	data := make([]float64, 1_000_000)
	for i := range data {
		data[i] = float64(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SumFloat64CPU(data)
	}
}

func BenchmarkSumFloat64GPU(b *testing.B) {
	if !Available() {
		b.Skip("GPU not available")
	}

	data := make([]float64, 1_000_000)
	for i := range data {
		data[i] = float64(i)
	}

	// Warmup
	SumFloat64(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SumFloat64(data)
	}
}

func TestFilterThenSum(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	tests := []struct {
		name      string
		data      []float64
		threshold float64
		expected  float64
	}{
		{"empty", []float64{}, 0.0, 0.0},
		{"all_pass", []float64{5, 6, 7, 8}, 0.0, 26.0},
		{"all_fail", []float64{1, 2, 3, 4}, 10.0, 0.0},
		{"half_pass", []float64{1, 5, 2, 8, 3, 9, 4, 7, 6, 10}, 5.0, 40.0}, // 6+7+8+9+10
		{"exact_threshold", []float64{5, 10, 15}, 5.0, 25.0},               // 10+15
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FilterThenSum(tt.data, tt.threshold)
			if err != nil {
				t.Fatalf("FilterThenSum error: %v", err)
			}

			if math.Abs(result-tt.expected) > 1e-10 {
				t.Errorf("expected %f, got %f", tt.expected, result)
			}

			// Also verify against CPU
			cpuResult := FilterThenSumCPU(tt.data, tt.threshold)
			if math.Abs(result-cpuResult) > 1e-10 {
				t.Errorf("GPU (%f) doesn't match CPU (%f)", result, cpuResult)
			}
		})
	}
}

func TestFilterThenSumLarge(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	// Test with 10M elements, threshold filters ~50%
	n := 10_000_000
	data := make([]float64, n)
	for i := range data {
		data[i] = float64(i % 100) // Values 0-99
	}
	threshold := 50.0 // Keep values > 50

	gpuResult, err := FilterThenSum(data, threshold)
	if err != nil {
		t.Fatalf("FilterThenSum error: %v", err)
	}

	cpuResult := FilterThenSumCPU(data, threshold)

	relErr := math.Abs(gpuResult-cpuResult) / cpuResult
	if relErr > 1e-10 {
		t.Errorf("GPU (%f) doesn't match CPU (%f), relative error: %e",
			gpuResult, cpuResult, relErr)
	}
}

func BenchmarkFilterThenSumCPU(b *testing.B) {
	data := make([]float64, 10_000_000)
	for i := range data {
		data[i] = float64(i % 100)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FilterThenSumCPU(data, 50.0)
	}
}

func BenchmarkFilterThenSumGPU(b *testing.B) {
	if !Available() {
		b.Skip("GPU not available")
	}

	data := make([]float64, 10_000_000)
	for i := range data {
		data[i] = float64(i % 100)
	}

	// Warmup
	FilterThenSum(data, 50.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FilterThenSum(data, 50.0)
	}
}

// FFT Tests

func TestFFTMagnitude(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	tests := []struct {
		name string
		data []float64
	}{
		{"empty", []float64{}},
		{"single", []float64{1.0}},
		{"power_of_two", make([]float64, 1024)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.data) > 1 {
				// Fill with sine wave
				for i := range tt.data {
					tt.data[i] = math.Sin(2 * math.Pi * float64(i) / float64(len(tt.data)) * 10)
				}
			}

			result, err := FFTMagnitude(tt.data)
			if err != nil {
				t.Fatalf("FFTMagnitude error: %v", err)
			}

			expectedLen := len(tt.data)/2 + 1
			if len(tt.data) == 0 {
				expectedLen = 0
			}
			if len(result) != expectedLen {
				t.Errorf("expected length %d, got %d", expectedLen, len(result))
			}
		})
	}
}

func TestFFTMagnitudePhase(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	n := 1024
	data := make([]float64, n)
	for i := range data {
		data[i] = math.Sin(2 * math.Pi * float64(i) / float64(n) * 10)
	}

	mag, phase, err := FFTMagnitudePhase(data)
	if err != nil {
		t.Fatalf("FFTMagnitudePhase error: %v", err)
	}

	expectedLen := n/2 + 1
	if len(mag) != expectedLen {
		t.Errorf("magnitude: expected length %d, got %d", expectedLen, len(mag))
	}
	if len(phase) != expectedLen {
		t.Errorf("phase: expected length %d, got %d", expectedLen, len(phase))
	}

	// Phase should be between -pi and pi
	for i, p := range phase {
		if p < -math.Pi || p > math.Pi {
			t.Errorf("phase[%d] = %f is outside [-pi, pi]", i, p)
		}
	}
}

func TestFFTMagnitudeLarge(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	// Test with 1M points
	n := 1_000_000
	data := make([]float64, n)
	for i := range data {
		t := float64(i) / float64(n)
		data[i] = math.Sin(2*math.Pi*10*t) + 0.5*math.Sin(2*math.Pi*25*t)
	}

	result, err := FFTMagnitude(data)
	if err != nil {
		t.Fatalf("FFTMagnitude error: %v", err)
	}

	expectedLen := n/2 + 1
	if len(result) != expectedLen {
		t.Errorf("expected length %d, got %d", expectedLen, len(result))
	}

	// First few values should be small (DC component and low frequencies)
	if result[0] > 1.0 {
		t.Logf("DC component magnitude: %f", result[0])
	}
}

func BenchmarkFFTMagnitudeGPU(b *testing.B) {
	if !Available() {
		b.Skip("GPU not available")
	}

	sizes := []int{1024, 8192, 65536, 262144, 1048576}

	for _, n := range sizes {
		data := make([]float64, n)
		for i := range data {
			data[i] = math.Sin(2 * math.Pi * float64(i) / float64(n) * 10)
		}

		// Warmup
		FFTMagnitude(data)

		b.Run(toString(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				FFTMagnitude(data)
			}
		})
	}
}

func BenchmarkFFTMagnitudeCPU(b *testing.B) {
	// Only benchmark small sizes - CPU DFT is O(n²)
	sizes := []int{1024, 2048}

	for _, n := range sizes {
		data := make([]float64, n)
		for i := range data {
			data[i] = math.Sin(2 * math.Pi * float64(i) / float64(n) * 10)
		}

		b.Run(toString(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				FFTMagnitudeCPU(data)
			}
		})
	}
}

func toString(n int) string {
	if n >= 1_000_000 {
		return string(rune('0'+n/1_000_000)) + "M"
	}
	if n >= 1_000 {
		return string(rune('0'+n/1_000)) + "K"
	}
	return string(rune('0' + n))
}

// Convolution Tests

func TestConvolveDirect(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	tests := []struct {
		name     string
		signal   []float64
		kernel   []float64
		expected []float64
	}{
		{"simple", []float64{1, 2, 3}, []float64{1, 1}, []float64{1, 3, 5, 3}},
		{"identity", []float64{1, 2, 3, 4}, []float64{1}, []float64{1, 2, 3, 4}},
		{"impulse", []float64{0, 0, 1, 0, 0}, []float64{1, 2, 3}, []float64{0, 0, 1, 2, 3, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvolveDirect(tt.signal, tt.kernel)
			if err != nil {
				t.Fatalf("ConvolveDirect error: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Errorf("length mismatch: got %d, want %d", len(result), len(tt.expected))
				return
			}

			for i := range result {
				if math.Abs(result[i]-tt.expected[i]) > 1e-9 {
					t.Errorf("result[%d] = %f, want %f", i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestConvolveFFT(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	// Test with larger data to verify FFT-based convolution
	signal := make([]float64, 1000)
	kernel := make([]float64, 100)
	for i := range signal {
		signal[i] = math.Sin(2 * math.Pi * float64(i) / 100)
	}
	for i := range kernel {
		kernel[i] = 1.0 / float64(len(kernel)) // Moving average
	}

	gpuResult, err := ConvolveFFT(signal, kernel)
	if err != nil {
		t.Fatalf("ConvolveFFT error: %v", err)
	}

	cpuResult := ConvolveCPU(signal, kernel)

	if len(gpuResult) != len(cpuResult) {
		t.Errorf("length mismatch: GPU %d, CPU %d", len(gpuResult), len(cpuResult))
		return
	}

	// Compare results (allow some floating point tolerance)
	maxErr := 0.0
	for i := range gpuResult {
		err := math.Abs(gpuResult[i] - cpuResult[i])
		if err > maxErr {
			maxErr = err
		}
	}

	if maxErr > 1e-6 {
		t.Errorf("max error between GPU and CPU: %e", maxErr)
	}
}

func TestConvolveLarge(t *testing.T) {
	if !Available() {
		t.Skip("GPU not available")
	}

	// Large signal with medium kernel - realistic use case
	signal := make([]float64, 100000)
	kernel := make([]float64, 1000)
	for i := range signal {
		signal[i] = float64(i % 100)
	}
	for i := range kernel {
		kernel[i] = math.Exp(-float64(i) / 100) // Exponential decay
	}

	// Test both methods produce similar results
	directResult, err := ConvolveDirect(signal, kernel)
	if err != nil {
		t.Fatalf("ConvolveDirect error: %v", err)
	}

	fftResult, err := ConvolveFFT(signal, kernel)
	if err != nil {
		t.Fatalf("ConvolveFFT error: %v", err)
	}

	if len(directResult) != len(fftResult) {
		t.Errorf("length mismatch: direct %d, fft %d", len(directResult), len(fftResult))
		return
	}

	// Compare results
	maxErr := 0.0
	for i := range directResult {
		err := math.Abs(directResult[i] - fftResult[i])
		if err > maxErr {
			maxErr = err
		}
	}

	if maxErr > 1e-6 {
		t.Errorf("max error between direct and FFT: %e", maxErr)
	}
}

// Convolution Benchmarks

func BenchmarkConvolveCPU(b *testing.B) {
	// Test various signal/kernel size combinations
	cases := []struct {
		signalLen int
		kernelLen int
	}{
		{10000, 100},   // Small kernel
		{10000, 1000},  // Medium kernel
		{100000, 100},  // Large signal, small kernel
		{100000, 1000}, // Large signal, medium kernel
	}

	for _, c := range cases {
		signal := make([]float64, c.signalLen)
		kernel := make([]float64, c.kernelLen)
		for i := range signal {
			signal[i] = float64(i % 100)
		}
		for i := range kernel {
			kernel[i] = 1.0 / float64(c.kernelLen)
		}

		name := toString(c.signalLen) + "x" + toString(c.kernelLen)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ConvolveCPU(signal, kernel)
			}
		})
	}
}

func BenchmarkConvolveDirectGPU(b *testing.B) {
	if !Available() {
		b.Skip("GPU not available")
	}

	cases := []struct {
		signalLen int
		kernelLen int
	}{
		{10000, 100},
		{10000, 1000},
		{100000, 100},
		{100000, 1000},
	}

	for _, c := range cases {
		signal := make([]float64, c.signalLen)
		kernel := make([]float64, c.kernelLen)
		for i := range signal {
			signal[i] = float64(i % 100)
		}
		for i := range kernel {
			kernel[i] = 1.0 / float64(c.kernelLen)
		}

		// Warmup
		ConvolveDirect(signal, kernel)

		name := toString(c.signalLen) + "x" + toString(c.kernelLen)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ConvolveDirect(signal, kernel)
			}
		})
	}
}

func BenchmarkConvolveFFTGPU(b *testing.B) {
	if !Available() {
		b.Skip("GPU not available")
	}

	cases := []struct {
		signalLen int
		kernelLen int
	}{
		{10000, 100},
		{10000, 1000},
		{100000, 100},
		{100000, 1000},
	}

	for _, c := range cases {
		signal := make([]float64, c.signalLen)
		kernel := make([]float64, c.kernelLen)
		for i := range signal {
			signal[i] = float64(i % 100)
		}
		for i := range kernel {
			kernel[i] = 1.0 / float64(c.kernelLen)
		}

		// Warmup
		ConvolveFFT(signal, kernel)

		name := toString(c.signalLen) + "x" + toString(c.kernelLen)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ConvolveFFT(signal, kernel)
			}
		})
	}
}
