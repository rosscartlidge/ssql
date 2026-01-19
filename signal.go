package ssql

import (
	"iter"
	"math"
)

// Signal represents a time-domain signal as a sequence of float64 values.
// This type is used for FFT and convolution operations.
type Signal []float64

// Spectrum represents frequency-domain data from an FFT.
type Spectrum struct {
	Magnitude []float64 // Magnitude at each frequency bin
	Phase     []float64 // Phase in radians (optional, may be nil)
	N         int       // Original signal length
}

// FrequencyBin returns the frequency in Hz for a given bin index.
// sampleRate is the sampling rate of the original signal in Hz.
func (s *Spectrum) FrequencyBin(index int, sampleRate float64) float64 {
	return float64(index) * sampleRate / float64(s.N)
}

// Len returns the number of frequency bins in the spectrum.
func (s *Spectrum) Len() int {
	return len(s.Magnitude)
}

// ============================================================================
// FFT Operations
// ============================================================================

// FFT computes the Fast Fourier Transform of a signal.
// Returns a Spectrum with magnitude values. For phase, use FFTWithPhase.
// Automatically uses GPU acceleration when available and beneficial.
func FFT(signal Signal) (*Spectrum, error) {
	if len(signal) == 0 {
		return &Spectrum{Magnitude: []float64{}, N: 0}, nil
	}

	mag, err := fftMagnitudeImpl(signal)
	if err != nil {
		return nil, err
	}

	return &Spectrum{
		Magnitude: mag,
		N:         len(signal),
	}, nil
}

// FFTWithPhase computes the FFT and returns both magnitude and phase.
// Phase values are in radians, range [-π, π].
func FFTWithPhase(signal Signal) (*Spectrum, error) {
	if len(signal) == 0 {
		return &Spectrum{Magnitude: []float64{}, Phase: []float64{}, N: 0}, nil
	}

	mag, phase, err := fftMagnitudePhaseImpl(signal)
	if err != nil {
		return nil, err
	}

	return &Spectrum{
		Magnitude: mag,
		Phase:     phase,
		N:         len(signal),
	}, nil
}

// FFTMagnitude computes just the magnitude spectrum.
// This is a convenience function equivalent to FFT(signal).Magnitude.
func FFTMagnitude(signal Signal) ([]float64, error) {
	return fftMagnitudeImpl(signal)
}

// ============================================================================
// Convolution Operations
// ============================================================================

// Convolve computes the convolution of a signal with a kernel.
// Output length is len(signal) + len(kernel) - 1.
// Automatically uses GPU acceleration when available and beneficial.
func Convolve(signal, kernel Signal) (Signal, error) {
	if len(signal) == 0 || len(kernel) == 0 {
		return Signal{}, nil
	}
	return convolveImpl(signal, kernel)
}

// ConvolveSame computes convolution and returns output of same length as input.
// This is equivalent to "same" mode in numpy.convolve.
func ConvolveSame(signal, kernel Signal) (Signal, error) {
	if len(signal) == 0 || len(kernel) == 0 {
		return Signal{}, nil
	}

	full, err := convolveImpl(signal, kernel)
	if err != nil {
		return nil, err
	}

	// Extract central portion of same length as signal
	start := (len(kernel) - 1) / 2
	end := start + len(signal)
	if end > len(full) {
		end = len(full)
	}

	return full[start:end], nil
}

// ============================================================================
// Built-in Kernels
// ============================================================================

// MovingAverageKernel creates a simple moving average kernel.
// Each element is 1/size, so convolution computes the mean of size elements.
func MovingAverageKernel(size int) Signal {
	if size <= 0 {
		return Signal{}
	}
	kernel := make(Signal, size)
	val := 1.0 / float64(size)
	for i := range kernel {
		kernel[i] = val
	}
	return kernel
}

// GaussianKernel creates a Gaussian smoothing kernel.
// size is the kernel length (should be odd for symmetry).
// sigma is the standard deviation of the Gaussian.
func GaussianKernel(size int, sigma float64) Signal {
	if size <= 0 || sigma <= 0 {
		return Signal{}
	}

	kernel := make(Signal, size)
	center := float64(size-1) / 2.0
	sum := 0.0

	for i := range kernel {
		x := float64(i) - center
		kernel[i] = math.Exp(-(x * x) / (2 * sigma * sigma))
		sum += kernel[i]
	}

	// Normalize so kernel sums to 1
	for i := range kernel {
		kernel[i] /= sum
	}

	return kernel
}

// DiffKernel returns a simple difference kernel for edge detection.
// Computes approximate first derivative: [-1, 1]
func DiffKernel() Signal {
	return Signal{-1, 1}
}

// LaplacianKernel returns a Laplacian kernel for second derivative.
// Useful for edge detection: [1, -2, 1]
func LaplacianKernel() Signal {
	return Signal{1, -2, 1}
}

// SobelKernel returns a Sobel operator for edge detection.
// This is a 1D version: [-1, 0, 1] with Gaussian smoothing.
func SobelKernel() Signal {
	return Signal{-1, 0, 1}
}

// ============================================================================
// Record Integration
// ============================================================================

// ExtractSignal extracts a float64 field from records as a Signal.
// Records are consumed (iterator is drained) to build the signal.
func ExtractSignal(records iter.Seq[Record], field string) Signal {
	var signal Signal
	for r := range records {
		val := GetOr(r, field, 0.0)
		signal = append(signal, val)
	}
	return signal
}

// ExtractSignalFromSlice extracts a float64 field from a slice of records.
func ExtractSignalFromSlice(records []Record, field string) Signal {
	signal := make(Signal, len(records))
	for i, r := range records {
		signal[i] = GetOr(r, field, 0.0)
	}
	return signal
}

// WithSignal adds a signal as a new field to records.
// If the signal is shorter than records, remaining records get 0.
// If the signal is longer, extra values are ignored.
func WithSignal(records iter.Seq[Record], field string, signal Signal) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		i := 0
		for r := range records {
			var val float64
			if i < len(signal) {
				val = signal[i]
			}
			mut := r.ToMutable()
			mut = mut.Float(field, val)
			if !yield(mut.Freeze()) {
				return
			}
			i++
		}
	}
}

// SpectrumToRecords converts a Spectrum to a sequence of Records.
// Each record has: index, frequency (if sampleRate > 0), magnitude, phase (if available).
func SpectrumToRecords(spectrum *Spectrum, sampleRate float64) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for i := 0; i < spectrum.Len(); i++ {
			mut := MakeMutableRecord()
			mut = mut.Int("index", int64(i))

			if sampleRate > 0 {
				mut = mut.Float("frequency", spectrum.FrequencyBin(i, sampleRate))
			}

			mut = mut.Float("magnitude", spectrum.Magnitude[i])

			if spectrum.Phase != nil && i < len(spectrum.Phase) {
				mut = mut.Float("phase", spectrum.Phase[i])
			}

			if !yield(mut.Freeze()) {
				return
			}
		}
	}
}

// ============================================================================
// Pipeline Filters (for code generation and CLI)
// ============================================================================

// FFTFilter returns a filter that computes FFT on a field and returns spectrum records.
// This is designed to work with ssql.Chain() and code generation.
func FFTFilter(field string, sampleRate float64, includePhase bool) Filter[Record, Record] {
	return func(records iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Collect all records to extract signal
			collected := make([]Record, 0)
			for r := range records {
				collected = append(collected, r)
			}

			// Extract signal from field
			signal := ExtractSignalFromSlice(collected, field)

			if len(signal) == 0 {
				return
			}

			// Compute FFT
			var spectrum *Spectrum
			var err error
			if includePhase {
				spectrum, err = FFTWithPhase(signal)
			} else {
				spectrum, err = FFT(signal)
			}
			if err != nil {
				// Can't return error from iterator, just return empty
				return
			}

			// Yield spectrum records
			for i := 0; i < spectrum.Len(); i++ {
				mut := MakeMutableRecord()
				mut = mut.Int("index", int64(i))

				if sampleRate > 0 {
					mut = mut.Float("frequency", spectrum.FrequencyBin(i, sampleRate))
				}

				mut = mut.Float("magnitude", spectrum.Magnitude[i])

				if spectrum.Phase != nil && i < len(spectrum.Phase) {
					mut = mut.Float("phase", spectrum.Phase[i])
				}

				if !yield(mut.Freeze()) {
					return
				}
			}
		}
	}
}

// ConvolveFilter returns a filter that applies convolution and adds the result as a new field.
// Use same=true to output same length as input (values added to original records).
// Use same=false to output full convolution result (new records with index and value).
func ConvolveFilter(field, outputField string, kernel Signal, same bool) Filter[Record, Record] {
	return func(records iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Collect all records
			collected := make([]Record, 0)
			for r := range records {
				collected = append(collected, r)
			}

			// Extract signal from field
			signal := ExtractSignalFromSlice(collected, field)

			if len(signal) == 0 {
				return
			}

			// Apply convolution
			var result Signal
			var err error
			if same {
				result, err = ConvolveSame(signal, kernel)
			} else {
				result, err = Convolve(signal, kernel)
			}
			if err != nil {
				// Can't return error from iterator, just return empty
				return
			}

			if same {
				// Same length - add to original records
				for i, r := range collected {
					mut := r.ToMutable()
					if i < len(result) {
						mut = mut.Float(outputField, result[i])
					}
					if !yield(mut.Freeze()) {
						return
					}
				}
			} else {
				// Full convolution - create new records with index and value
				for i, v := range result {
					mut := MakeMutableRecord()
					mut = mut.Int("index", int64(i))
					mut = mut.Float(outputField, v)
					if !yield(mut.Freeze()) {
						return
					}
				}
			}
		}
	}
}

// ============================================================================
// Internal Implementations
// ============================================================================

// fftMagnitudeImpl computes FFT magnitude, using GPU if available.
func fftMagnitudeImpl(signal Signal) ([]float64, error) {
	// GPU threshold: use GPU for signals >= 1024 points
	if gpuAvailableForSignal() && len(signal) >= 1024 {
		return fftMagnitudeGPU(signal)
	}
	return fftMagnitudeCPU(signal), nil
}

// fftMagnitudePhaseImpl computes FFT magnitude and phase, using GPU if available.
func fftMagnitudePhaseImpl(signal Signal) ([]float64, []float64, error) {
	if gpuAvailableForSignal() && len(signal) >= 1024 {
		return fftMagnitudePhaseGPU(signal)
	}
	mag, phase := fftMagnitudePhaseCPU(signal)
	return mag, phase, nil
}

// convolveImpl computes convolution, using GPU if available.
func convolveImpl(signal, kernel Signal) (Signal, error) {
	// GPU threshold: use GPU for kernels >= 64 points
	if gpuAvailableForSignal() && len(kernel) >= 64 {
		return convolveGPU(signal, kernel)
	}
	return convolveCPU(signal, kernel), nil
}

// ============================================================================
// CPU Implementations (fallback)
// ============================================================================

// fftMagnitudeCPU computes FFT magnitude on CPU using naive DFT.
// This is O(n²) - only use for small signals or as fallback.
func fftMagnitudeCPU(signal Signal) []float64 {
	n := len(signal)
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
			real += signal[t] * math.Cos(angle)
			imag += signal[t] * math.Sin(angle)
		}
		magnitude[k] = math.Sqrt(real*real + imag*imag)
	}

	return magnitude
}

// fftMagnitudePhaseCPU computes FFT magnitude and phase on CPU.
func fftMagnitudePhaseCPU(signal Signal) ([]float64, []float64) {
	n := len(signal)
	if n == 0 {
		return []float64{}, []float64{}
	}

	outN := n/2 + 1
	magnitude := make([]float64, outN)
	phase := make([]float64, outN)

	for k := 0; k < outN; k++ {
		var real, imag float64
		for t := 0; t < n; t++ {
			angle := -2 * math.Pi * float64(k) * float64(t) / float64(n)
			real += signal[t] * math.Cos(angle)
			imag += signal[t] * math.Sin(angle)
		}
		magnitude[k] = math.Sqrt(real*real + imag*imag)
		phase[k] = math.Atan2(imag, real)
	}

	return magnitude, phase
}

// convolveCPU computes convolution on CPU using direct O(n*m) method.
func convolveCPU(signal, kernel Signal) Signal {
	if len(signal) == 0 || len(kernel) == 0 {
		return Signal{}
	}

	outputLen := len(signal) + len(kernel) - 1
	result := make(Signal, outputLen)

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

