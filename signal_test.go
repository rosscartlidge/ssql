package ssql

import (
	"math"
	"slices"
	"testing"
)

func TestFFT(t *testing.T) {
	// Test with a simple sine wave - should have a peak at the fundamental frequency
	n := 1024
	signal := make(Signal, n)
	freq := 10.0 // 10 cycles in the signal
	for i := range signal {
		signal[i] = math.Sin(2 * math.Pi * freq * float64(i) / float64(n))
	}

	spectrum, err := FFT(signal)
	if err != nil {
		t.Fatalf("FFT failed: %v", err)
	}

	if spectrum.Len() != n/2+1 {
		t.Errorf("expected %d frequency bins, got %d", n/2+1, spectrum.Len())
	}

	// Find peak frequency
	maxIdx := 0
	maxMag := 0.0
	for i, mag := range spectrum.Magnitude {
		if mag > maxMag {
			maxMag = mag
			maxIdx = i
		}
	}

	// Peak should be at bin 10 (corresponding to 10 cycles)
	if maxIdx != int(freq) {
		t.Errorf("expected peak at bin %d, got bin %d", int(freq), maxIdx)
	}
}

func TestFFTWithPhase(t *testing.T) {
	n := 256
	signal := make(Signal, n)
	for i := range signal {
		signal[i] = math.Cos(2 * math.Pi * 5 * float64(i) / float64(n))
	}

	spectrum, err := FFTWithPhase(signal)
	if err != nil {
		t.Fatalf("FFTWithPhase failed: %v", err)
	}

	if spectrum.Phase == nil {
		t.Fatal("expected phase data")
	}

	if len(spectrum.Phase) != len(spectrum.Magnitude) {
		t.Errorf("phase length %d != magnitude length %d", len(spectrum.Phase), len(spectrum.Magnitude))
	}

	// Phase should be in range [-π, π]
	for i, p := range spectrum.Phase {
		if p < -math.Pi || p > math.Pi {
			t.Errorf("phase[%d] = %f out of range [-π, π]", i, p)
		}
	}
}

func TestConvolve(t *testing.T) {
	tests := []struct {
		name     string
		signal   Signal
		kernel   Signal
		expected Signal
	}{
		{
			name:     "simple",
			signal:   Signal{1, 2, 3},
			kernel:   Signal{1, 1},
			expected: Signal{1, 3, 5, 3},
		},
		{
			name:     "identity",
			signal:   Signal{1, 2, 3, 4},
			kernel:   Signal{1},
			expected: Signal{1, 2, 3, 4},
		},
		{
			name:     "impulse response",
			signal:   Signal{0, 0, 1, 0, 0},
			kernel:   Signal{1, 2, 3},
			expected: Signal{0, 0, 1, 2, 3, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Convolve(tt.signal, tt.kernel)
			if err != nil {
				t.Fatalf("Convolve failed: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Errorf("length: got %d, want %d", len(result), len(tt.expected))
				return
			}

			for i := range result {
				if math.Abs(result[i]-tt.expected[i]) > 1e-10 {
					t.Errorf("result[%d] = %f, want %f", i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestConvolveSame(t *testing.T) {
	signal := Signal{1, 2, 3, 4, 5}
	kernel := Signal{1, 1, 1} // 3-point moving sum

	result, err := ConvolveSame(signal, kernel)
	if err != nil {
		t.Fatalf("ConvolveSame failed: %v", err)
	}

	// Output should be same length as input
	if len(result) != len(signal) {
		t.Errorf("expected length %d, got %d", len(signal), len(result))
	}
}

func TestMovingAverageKernel(t *testing.T) {
	kernel := MovingAverageKernel(5)

	if len(kernel) != 5 {
		t.Errorf("expected length 5, got %d", len(kernel))
	}

	// Should sum to 1
	sum := 0.0
	for _, v := range kernel {
		sum += v
	}
	if math.Abs(sum-1.0) > 1e-10 {
		t.Errorf("expected sum 1.0, got %f", sum)
	}

	// All values should be equal
	expected := 1.0 / 5.0
	for i, v := range kernel {
		if math.Abs(v-expected) > 1e-10 {
			t.Errorf("kernel[%d] = %f, want %f", i, v, expected)
		}
	}
}

func TestGaussianKernel(t *testing.T) {
	kernel := GaussianKernel(11, 2.0)

	if len(kernel) != 11 {
		t.Errorf("expected length 11, got %d", len(kernel))
	}

	// Should sum to 1
	sum := 0.0
	for _, v := range kernel {
		sum += v
	}
	if math.Abs(sum-1.0) > 1e-10 {
		t.Errorf("expected sum 1.0, got %f", sum)
	}

	// Should be symmetric
	for i := 0; i < len(kernel)/2; i++ {
		j := len(kernel) - 1 - i
		if math.Abs(kernel[i]-kernel[j]) > 1e-10 {
			t.Errorf("not symmetric: kernel[%d]=%f != kernel[%d]=%f", i, kernel[i], j, kernel[j])
		}
	}

	// Peak should be in the center
	maxIdx := 0
	maxVal := 0.0
	for i, v := range kernel {
		if v > maxVal {
			maxVal = v
			maxIdx = i
		}
	}
	if maxIdx != 5 { // center of 11-element kernel
		t.Errorf("expected peak at center (5), got %d", maxIdx)
	}
}

func TestDiffKernel(t *testing.T) {
	kernel := DiffKernel()
	expected := Signal{-1, 1}

	if !slices.Equal(kernel, expected) {
		t.Errorf("DiffKernel = %v, want %v", kernel, expected)
	}
}

func TestLaplacianKernel(t *testing.T) {
	kernel := LaplacianKernel()
	expected := Signal{1, -2, 1}

	if !slices.Equal(kernel, expected) {
		t.Errorf("LaplacianKernel = %v, want %v", kernel, expected)
	}
}

func TestExtractSignal(t *testing.T) {
	records := []Record{
		MakeMutableRecord().Float("value", 1.0).Freeze(),
		MakeMutableRecord().Float("value", 2.0).Freeze(),
		MakeMutableRecord().Float("value", 3.0).Freeze(),
	}

	signal := ExtractSignalFromSlice(records, "value")

	expected := Signal{1.0, 2.0, 3.0}
	if !slices.Equal(signal, expected) {
		t.Errorf("ExtractSignal = %v, want %v", signal, expected)
	}
}

func TestWithSignal(t *testing.T) {
	records := []Record{
		MakeMutableRecord().String("name", "a").Freeze(),
		MakeMutableRecord().String("name", "b").Freeze(),
		MakeMutableRecord().String("name", "c").Freeze(),
	}

	signal := Signal{10.0, 20.0, 30.0}

	result := slices.Collect(WithSignal(slices.Values(records), "value", signal))

	if len(result) != 3 {
		t.Fatalf("expected 3 records, got %d", len(result))
	}

	for i, r := range result {
		val := GetOr(r, "value", 0.0)
		if val != signal[i] {
			t.Errorf("record[%d].value = %f, want %f", i, val, signal[i])
		}
	}
}

func TestSpectrumToRecords(t *testing.T) {
	spectrum := &Spectrum{
		Magnitude: []float64{1.0, 2.0, 3.0},
		Phase:     []float64{0.1, 0.2, 0.3},
		N:         4,
	}

	records := slices.Collect(SpectrumToRecords(spectrum, 100.0))

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	// Check first record
	r := records[0]
	if GetOr(r, "index", int64(-1)) != 0 {
		t.Errorf("record[0].index = %d, want 0", GetOr(r, "index", int64(-1)))
	}
	if GetOr(r, "magnitude", 0.0) != 1.0 {
		t.Errorf("record[0].magnitude = %f, want 1.0", GetOr(r, "magnitude", 0.0))
	}
	if GetOr(r, "phase", 0.0) != 0.1 {
		t.Errorf("record[0].phase = %f, want 0.1", GetOr(r, "phase", 0.0))
	}

	// Check frequency calculation
	expectedFreq := 0.0 // bin 0 = DC = 0 Hz
	if math.Abs(GetOr(r, "frequency", -1.0)-expectedFreq) > 1e-10 {
		t.Errorf("record[0].frequency = %f, want %f", GetOr(r, "frequency", -1.0), expectedFreq)
	}
}

func TestFrequencyBin(t *testing.T) {
	spectrum := &Spectrum{
		Magnitude: make([]float64, 513), // n/2+1 for n=1024
		N:         1024,
	}

	// At 1000 Hz sample rate, each bin is 1000/1024 ≈ 0.977 Hz
	sampleRate := 1000.0

	// Bin 0 = DC = 0 Hz
	if spectrum.FrequencyBin(0, sampleRate) != 0 {
		t.Errorf("bin 0 should be 0 Hz")
	}

	// Bin 10 should be 10 * 1000/1024 ≈ 9.77 Hz
	expected := 10.0 * sampleRate / float64(spectrum.N)
	if math.Abs(spectrum.FrequencyBin(10, sampleRate)-expected) > 1e-10 {
		t.Errorf("bin 10 = %f Hz, want %f Hz", spectrum.FrequencyBin(10, sampleRate), expected)
	}
}

// Benchmarks

func BenchmarkFFTCPU(b *testing.B) {
	sizes := []int{256, 1024, 4096}

	for _, n := range sizes {
		signal := make(Signal, n)
		for i := range signal {
			signal[i] = math.Sin(2 * math.Pi * float64(i) / float64(n) * 10)
		}

		b.Run(toString(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				fftMagnitudeCPU(signal)
			}
		})
	}
}

func BenchmarkConvolveCPU(b *testing.B) {
	signal := make(Signal, 10000)
	for i := range signal {
		signal[i] = float64(i % 100)
	}

	kernelSizes := []int{10, 100, 1000}

	for _, ksize := range kernelSizes {
		kernel := MovingAverageKernel(ksize)

		b.Run(toString(ksize), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				convolveCPU(signal, kernel)
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
	// Handle numbers less than 1000
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if s == "" {
		return "0"
	}
	return s
}
