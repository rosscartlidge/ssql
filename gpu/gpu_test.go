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
