package ssql

import (
	"iter"
	"math"
	"math/cmplx"
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

// AutoConvolve computes the convolution of a signal with itself.
// Useful for probability distribution analysis and signal energy calculations.
// Output length is 2*len(signal) - 1.
func AutoConvolve(signal Signal) (Signal, error) {
	return Convolve(signal, signal)
}

// AutoConvolveSame computes auto-convolution with same-length output.
func AutoConvolveSame(signal Signal) (Signal, error) {
	return ConvolveSame(signal, signal)
}

// ============================================================================
// Correlation Operations
// ============================================================================

// Correlate computes the cross-correlation of two signals.
// Cross-correlation measures similarity as a function of lag.
// Output length is len(a) + len(b) - 1.
// Mathematically: Correlate(a, b) = Convolve(a, reverse(b))
// Automatically uses GPU acceleration when available and beneficial.
func Correlate(a, b Signal) (Signal, error) {
	if len(a) == 0 || len(b) == 0 {
		return Signal{}, nil
	}
	// Cross-correlation is convolution with reversed second signal
	reversed := make(Signal, len(b))
	for i := range b {
		reversed[i] = b[len(b)-1-i]
	}
	return convolveImpl(a, reversed)
}

// CorrelateSame computes cross-correlation and returns output of same length as first input.
// This is equivalent to "same" mode in numpy.correlate.
func CorrelateSame(a, b Signal) (Signal, error) {
	if len(a) == 0 || len(b) == 0 {
		return Signal{}, nil
	}

	full, err := Correlate(a, b)
	if err != nil {
		return nil, err
	}

	// Extract central portion of same length as a
	start := (len(b) - 1) / 2
	end := start + len(a)
	if end > len(full) {
		end = len(full)
	}

	return full[start:end], nil
}

// AutoCorrelate computes the autocorrelation of a signal.
// Autocorrelation measures how similar a signal is to a delayed copy of itself.
// Useful for finding repeating patterns and periodicities.
func AutoCorrelate(signal Signal) (Signal, error) {
	return Correlate(signal, signal)
}

// AutoCorrelateMax computes autocorrelation up to a maximum lag.
// Returns maxLag+1 values for lags 0 to maxLag.
// This is more efficient than full autocorrelation when searching for
// periodicity below a certain period (maxLag samples).
// Uses direct computation O(n * maxLag) which beats FFT when maxLag << n.
func AutoCorrelateMax(signal Signal, maxLag int) (Signal, error) {
	n := len(signal)
	if n == 0 {
		return Signal{}, nil
	}
	if maxLag < 0 {
		maxLag = 0
	}
	if maxLag >= n {
		maxLag = n - 1
	}

	result := make(Signal, maxLag+1)

	// Direct computation: R(k) = sum(signal[i] * signal[i+k]) for i = 0 to n-k-1
	for lag := 0; lag <= maxLag; lag++ {
		sum := 0.0
		for i := 0; i < n-lag; i++ {
			sum += signal[i] * signal[i+lag]
		}
		result[lag] = sum
	}

	return result, nil
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

// AutoConvolveFilter returns a filter that computes auto-convolution of a field.
// Auto-convolution convolves a signal with itself.
func AutoConvolveFilter(field, outputField string, same bool) Filter[Record, Record] {
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

			// Apply auto-convolution
			var result Signal
			var err error
			if same {
				result, err = AutoConvolveSame(signal)
			} else {
				result, err = AutoConvolve(signal)
			}
			if err != nil {
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
				// Full auto-convolution - create new records with index and value
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

// CorrelateFilter returns a filter that computes cross-correlation between two fields.
// Use same=true to output same length as first field (values added to original records).
// Use same=false to output full correlation result (new records with index and value).
func CorrelateFilter(fieldA, fieldB, outputField string, same bool) Filter[Record, Record] {
	return func(records iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Collect all records
			collected := make([]Record, 0)
			for r := range records {
				collected = append(collected, r)
			}

			// Extract signals from both fields
			signalA := ExtractSignalFromSlice(collected, fieldA)
			signalB := ExtractSignalFromSlice(collected, fieldB)

			if len(signalA) == 0 || len(signalB) == 0 {
				return
			}

			// Apply correlation
			var result Signal
			var err error
			if same {
				result, err = CorrelateSame(signalA, signalB)
			} else {
				result, err = Correlate(signalA, signalB)
			}
			if err != nil {
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
				// Full correlation - create new records with index and value
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

// AutoCorrelateFilter returns a filter that computes autocorrelation of a field.
// Autocorrelation measures how similar a signal is to a delayed copy of itself.
func AutoCorrelateFilter(field, outputField string, same bool) Filter[Record, Record] {
	return CorrelateFilter(field, field, outputField, same)
}

// AutoCorrelateMaxFilter returns a filter that computes autocorrelation up to maxLag.
// More efficient than full autocorrelation when searching for periodicity below maxLag samples.
func AutoCorrelateMaxFilter(field, outputField string, maxLag int) Filter[Record, Record] {
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

			// Compute autocorrelation with max lag
			result, err := AutoCorrelateMax(signal, maxLag)
			if err != nil {
				return
			}

			// Output records with lag and correlation value
			for lag, v := range result {
				mut := MakeMutableRecord()
				mut = mut.Int("lag", int64(lag))
				mut = mut.Float(outputField, v)
				if !yield(mut.Freeze()) {
					return
				}
			}
		}
	}
}

// ============================================================================
// Internal Implementations
// ============================================================================

// fftMagnitudeImpl computes FFT magnitude, using GPU when beneficial.
// GPU is ~28-54x faster for signals >= 16K samples.
// Crossover point is ~16K based on benchmarks.
func fftMagnitudeImpl(signal Signal) ([]float64, error) {
	// Use GPU for large signals (>=16K) where it's significantly faster
	if gpuAvailableForSignal() && len(signal) >= 16384 {
		return fftMagnitudeGPU(signal)
	}
	return fftMagnitudeCPU(signal), nil
}

// fftMagnitudePhaseImpl computes FFT magnitude and phase, using GPU when beneficial.
func fftMagnitudePhaseImpl(signal Signal) ([]float64, []float64, error) {
	// Use GPU for large signals (>=16K) where it's significantly faster
	if gpuAvailableForSignal() && len(signal) >= 16384 {
		return fftMagnitudePhaseGPU(signal)
	}
	mag, phase := fftMagnitudePhaseCPU(signal)
	return mag, phase, nil
}

// convolveImpl computes convolution, using GPU if available.
func convolveImpl(signal, kernel Signal) (Signal, error) {
	// GPU threshold: use GPU for kernels >= 16 points
	// Benchmarks show GPU wins at all kernel sizes, even kernel=16 (1.1x faster)
	if gpuAvailableForSignal() && len(kernel) >= 16 {
		return convolveGPU(signal, kernel)
	}
	return convolveCPU(signal, kernel), nil
}

// ============================================================================
// CPU Implementations - Cooley-Tukey FFT O(n log n)
// ============================================================================

// fftCooleyTukey performs in-place Cooley-Tukey radix-2 FFT.
// Input must be power-of-2 length.
func fftCooleyTukey(x []complex128) {
	n := len(x)
	if n <= 1 {
		return
	}

	// Bit-reversal permutation
	j := 0
	for i := 0; i < n-1; i++ {
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
		k := n / 2
		for k <= j {
			j -= k
			k /= 2
		}
		j += k
	}

	// Cooley-Tukey iterative FFT
	for size := 2; size <= n; size *= 2 {
		halfSize := size / 2
		step := -2 * math.Pi / float64(size)
		for i := 0; i < n; i += size {
			for k := 0; k < halfSize; k++ {
				w := cmplx.Exp(complex(0, step*float64(k)))
				even := x[i+k]
				odd := w * x[i+k+halfSize]
				x[i+k] = even + odd
				x[i+k+halfSize] = even - odd
			}
		}
	}
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	p := 1
	for p < n {
		p *= 2
	}
	return p
}

// fftMagnitudeCPU computes FFT magnitude on CPU using Cooley-Tukey O(n log n).
func fftMagnitudeCPU(signal Signal) []float64 {
	n := len(signal)
	if n == 0 {
		return []float64{}
	}

	// Pad to power of 2 for Cooley-Tukey
	size := nextPowerOf2(n)
	x := make([]complex128, size)
	for i := 0; i < n; i++ {
		x[i] = complex(signal[i], 0)
	}

	fftCooleyTukey(x)

	// Extract magnitudes (positive frequencies only)
	outN := n/2 + 1
	magnitude := make([]float64, outN)
	for i := 0; i < outN; i++ {
		magnitude[i] = cmplx.Abs(x[i])
	}

	return magnitude
}

// fftMagnitudePhaseCPU computes FFT magnitude and phase on CPU using Cooley-Tukey.
func fftMagnitudePhaseCPU(signal Signal) ([]float64, []float64) {
	n := len(signal)
	if n == 0 {
		return []float64{}, []float64{}
	}

	// Pad to power of 2 for Cooley-Tukey
	size := nextPowerOf2(n)
	x := make([]complex128, size)
	for i := 0; i < n; i++ {
		x[i] = complex(signal[i], 0)
	}

	fftCooleyTukey(x)

	// Extract magnitudes and phases (positive frequencies only)
	outN := n/2 + 1
	magnitude := make([]float64, outN)
	phase := make([]float64, outN)
	for i := 0; i < outN; i++ {
		magnitude[i] = cmplx.Abs(x[i])
		phase[i] = cmplx.Phase(x[i])
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

