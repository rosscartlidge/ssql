package ssql

import (
	"fmt"
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
// Inverse FFT Operations
// ============================================================================

// IFFT computes the Inverse Fast Fourier Transform from magnitude and phase.
// Reconstructs the original time-domain signal from frequency-domain data.
// magnitude and phase must have the same length (N/2 + 1 frequency bins).
// Returns a signal of length 2*(len(magnitude)-1).
// Automatically uses GPU acceleration when available and beneficial.
func IFFT(magnitude, phase []float64) (Signal, error) {
	if len(magnitude) == 0 {
		return Signal{}, nil
	}
	if len(magnitude) != len(phase) {
		return nil, fmt.Errorf("magnitude and phase must have same length: %d != %d", len(magnitude), len(phase))
	}

	return ifftImpl(magnitude, phase)
}

// IFFTToLength computes IFFT and returns a signal of specified length.
// This is useful when the original signal length is known.
func IFFTToLength(magnitude, phase []float64, length int) (Signal, error) {
	signal, err := IFFT(magnitude, phase)
	if err != nil {
		return nil, err
	}

	if length <= 0 || length > len(signal) {
		return signal, nil
	}
	return signal[:length], nil
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
// Window Functions (for Spectrogram / STFT)
// ============================================================================

// HannWindow generates a Hann (raised cosine) window of length n.
// w[k] = 0.5 - 0.5*cos(2πk/(n-1))
func HannWindow(n int) Signal {
	if n <= 0 {
		return Signal{}
	}
	if n == 1 {
		return Signal{1}
	}
	w := make(Signal, n)
	for k := 0; k < n; k++ {
		w[k] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(k)/float64(n-1))
	}
	return w
}

// HammingWindow generates a Hamming window of length n.
// w[k] = 0.54 - 0.46*cos(2πk/(n-1))
func HammingWindow(n int) Signal {
	if n <= 0 {
		return Signal{}
	}
	if n == 1 {
		return Signal{1}
	}
	w := make(Signal, n)
	for k := 0; k < n; k++ {
		w[k] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(k)/float64(n-1))
	}
	return w
}

// BlackmanWindow generates a Blackman window of length n.
// w[k] = 0.42 - 0.5*cos(2πk/(n-1)) + 0.08*cos(4πk/(n-1))
func BlackmanWindow(n int) Signal {
	if n <= 0 {
		return Signal{}
	}
	if n == 1 {
		return Signal{1}
	}
	w := make(Signal, n)
	for k := 0; k < n; k++ {
		w[k] = 0.42 - 0.5*math.Cos(2*math.Pi*float64(k)/float64(n-1)) + 0.08*math.Cos(4*math.Pi*float64(k)/float64(n-1))
	}
	return w
}

// ApplyWindow multiplies a signal by a window function element-wise.
// If the signal and window have different lengths, the shorter length is used.
func ApplyWindow(signal, window Signal) Signal {
	n := len(signal)
	if len(window) < n {
		n = len(window)
	}
	result := make(Signal, n)
	for i := 0; i < n; i++ {
		result[i] = signal[i] * window[i]
	}
	return result
}

// ============================================================================
// Spectrogram (Short-Time Fourier Transform)
// ============================================================================

// SpectrogramOptions configures the STFT computation.
type SpectrogramOptions struct {
	WindowSize int     // FFT window size (default: 1024)
	HopSize    int     // Samples between windows (default: WindowSize/4)
	Window     string  // "hann", "hamming", "blackman", "none" (default: "hann")
	SampleRate float64 // For frequency axis (default: 1.0)
}

// SpectrogramBin represents a single time-frequency magnitude value.
type SpectrogramBin struct {
	TimeIndex int
	TimeStart float64 // start sample / sampleRate
	FreqIndex int
	Frequency float64
	Magnitude float64
}

// Spectrogram computes the Short-Time Fourier Transform (STFT) of a signal.
// Returns a slice of SpectrogramBin representing the time × frequency × magnitude output.
// Each time frame is windowed, then FFT'd to produce magnitude values.
//
// Automatically uses GPU acceleration when available. The batched GPU path processes
// all frames in a single GPU call using cufftPlanMany, which amortizes PCIe transfer
// overhead across all frames. This provides significant speedup for spectrograms with
// many frames (e.g., 1700 frames of audio at 44.1kHz).
func Spectrogram(signal Signal, opts SpectrogramOptions) ([]SpectrogramBin, error) {
	// Apply defaults
	if opts.WindowSize <= 0 {
		opts.WindowSize = 1024
	}
	if opts.HopSize <= 0 {
		opts.HopSize = opts.WindowSize / 4
	}
	if opts.SampleRate <= 0 {
		opts.SampleRate = 1.0
	}

	n := len(signal)
	if n == 0 {
		return nil, nil
	}
	if opts.WindowSize > n {
		opts.WindowSize = n
	}

	// Build window
	var window Signal
	switch opts.Window {
	case "hamming":
		window = HammingWindow(opts.WindowSize)
	case "blackman":
		window = BlackmanWindow(opts.WindowSize)
	case "none", "rectangular", "rect":
		window = nil // No windowing
	default: // "hann" or empty
		window = HannWindow(opts.WindowSize)
	}

	// Compute number of frames
	numFrames := 0
	for start := 0; start+opts.WindowSize <= n; start += opts.HopSize {
		numFrames++
	}
	if numFrames == 0 {
		return nil, nil
	}

	// Try batched GPU path: use GPU when there are enough frames to amortize transfer overhead.
	// Even small windows (1024) benefit when there are many frames (e.g., 100+).
	// Threshold: total samples across all frames >= 32K (e.g., 32 frames × 1024 window).
	totalFrameSamples := numFrames * opts.WindowSize
	if gpuAvailableForSignal() && totalFrameSamples >= 32768 {
		bins, err := spectrogramGPU(signal, opts, window, numFrames)
		if err == nil {
			return bins, nil
		}
		// GPU failed, fall through to CPU path
	}

	return spectrogramCPU(signal, opts, window, numFrames)
}

// spectrogramGPU computes spectrogram using batched GPU FFT.
// All frames are extracted, laid out contiguously, and processed in a single GPU call.
func spectrogramGPU(signal Signal, opts SpectrogramOptions, window Signal, numFrames int) ([]SpectrogramBin, error) {
	n := len(signal)
	freqBins := opts.WindowSize/2 + 1

	// Extract all frames into a contiguous buffer for GPU transfer
	frames := make([]float64, numFrames*opts.WindowSize)
	frameIdx := 0
	for start := 0; start+opts.WindowSize <= n; start += opts.HopSize {
		copy(frames[frameIdx*opts.WindowSize:], signal[start:start+opts.WindowSize])
		frameIdx++
	}

	// Call batched GPU FFT (windowing is applied on GPU if window is non-nil)
	var windowArg []float64
	if window != nil {
		windowArg = []float64(window)
	}
	magnitudes, binsPerFrame, err := batchedFFTMagnitudeGPU(frames, opts.WindowSize, numFrames, windowArg)
	if err != nil {
		return nil, err
	}

	// Build SpectrogramBin results from flat magnitude array
	bins := make([]SpectrogramBin, numFrames*binsPerFrame)
	binIdx := 0
	start := 0
	for ti := 0; ti < numFrames; ti++ {
		timeStart := float64(start) / opts.SampleRate
		magOffset := ti * binsPerFrame
		for fi := 0; fi < binsPerFrame; fi++ {
			freq := float64(fi) * opts.SampleRate / float64(opts.WindowSize)
			bins[binIdx] = SpectrogramBin{
				TimeIndex: ti,
				TimeStart: timeStart,
				FreqIndex: fi,
				Frequency: freq,
				Magnitude: magnitudes[magOffset+fi],
			}
			binIdx++
		}
		start += opts.HopSize
	}

	// Trim in case binsPerFrame differs from expected freqBins
	if binIdx < len(bins) {
		bins = bins[:binIdx]
	}
	_ = freqBins // used for documentation, GPU returns actual binsPerFrame

	return bins, nil
}

// spectrogramCPU computes spectrogram using per-frame CPU FFT.
func spectrogramCPU(signal Signal, opts SpectrogramOptions, window Signal, numFrames int) ([]SpectrogramBin, error) {
	n := len(signal)
	freqBins := opts.WindowSize/2 + 1

	bins := make([]SpectrogramBin, 0, numFrames*freqBins)

	timeIdx := 0
	for start := 0; start+opts.WindowSize <= n; start += opts.HopSize {
		frame := signal[start : start+opts.WindowSize]

		// Apply window
		if window != nil {
			frame = ApplyWindow(frame, window)
		}

		// Compute FFT magnitude for this frame
		mag, err := FFTMagnitude(frame)
		if err != nil {
			return nil, fmt.Errorf("FFT at frame %d (sample %d): %w", timeIdx, start, err)
		}

		timeStart := float64(start) / opts.SampleRate

		for fi := 0; fi < len(mag); fi++ {
			freq := float64(fi) * opts.SampleRate / float64(opts.WindowSize)
			bins = append(bins, SpectrogramBin{
				TimeIndex: timeIdx,
				TimeStart: timeStart,
				FreqIndex: fi,
				Frequency: freq,
				Magnitude: mag[fi],
			})
		}

		timeIdx++
	}

	return bins, nil
}

// SpectrogramToRecords converts spectrogram bins to a sequence of Records.
// Each record has: time_index, time, frequency, magnitude.
func SpectrogramToRecords(bins []SpectrogramBin) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for _, bin := range bins {
			mut := MakeMutableRecord()
			mut = mut.Int("time_index", int64(bin.TimeIndex))
			mut = mut.Float("time", bin.TimeStart)
			mut = mut.Float("frequency", bin.Frequency)
			mut = mut.Float("magnitude", bin.Magnitude)
			if !yield(mut.Freeze()) {
				return
			}
		}
	}
}

// SpectrogramFilter returns a filter that computes a spectrogram on a field
// and returns time × frequency × magnitude records.
func SpectrogramFilter(field string, opts SpectrogramOptions) Filter[Record, Record] {
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

			// Compute spectrogram
			bins, err := Spectrogram(signal, opts)
			if err != nil {
				return
			}

			// Yield spectrogram records
			for _, bin := range bins {
				mut := MakeMutableRecord()
				mut = mut.Int("time_index", int64(bin.TimeIndex))
				mut = mut.Float("time", bin.TimeStart)
				mut = mut.Float("frequency", bin.Frequency)
				mut = mut.Float("magnitude", bin.Magnitude)
				if !yield(mut.Freeze()) {
					return
				}
			}
		}
	}
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

// IFFTFilter returns a filter that computes inverse FFT from magnitude and phase fields.
// This reconstructs the time-domain signal from frequency-domain data.
// Output records have index and the specified outputField.
func IFFTFilter(magnitudeField, phaseField, outputField string) Filter[Record, Record] {
	return func(records iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Collect all records
			collected := make([]Record, 0)
			for r := range records {
				collected = append(collected, r)
			}

			// Extract magnitude and phase from fields
			magnitude := make([]float64, len(collected))
			phase := make([]float64, len(collected))
			for i, r := range collected {
				magnitude[i] = GetOr(r, magnitudeField, 0.0)
				phase[i] = GetOr(r, phaseField, 0.0)
			}

			if len(magnitude) == 0 {
				return
			}

			// Compute inverse FFT
			signal, err := IFFT(magnitude, phase)
			if err != nil {
				// Can't return error from iterator, just return empty
				return
			}

			// Yield signal records
			for i, v := range signal {
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

// ifftImpl computes inverse FFT, using GPU when beneficial.
func ifftImpl(magnitude, phase []float64) (Signal, error) {
	// Use GPU for large spectra (>=16K bins) where it's significantly faster
	if gpuAvailableForSignal() && len(magnitude) >= 16384 {
		return ifftGPU(magnitude, phase)
	}
	return ifftCPU(magnitude, phase), nil
}

// ifftCPU computes inverse FFT on CPU using Cooley-Tukey.
// Takes magnitude and phase arrays (N/2 + 1 positive frequency bins),
// reconstructs the full complex spectrum, and applies inverse FFT.
func ifftCPU(magnitude, phase []float64) Signal {
	numBins := len(magnitude)
	if numBins == 0 {
		return Signal{}
	}

	// Original signal length: if we have N/2 + 1 bins, original was N = 2*(numBins-1)
	n := 2 * (numBins - 1)
	if n == 0 {
		// Special case: single bin means single sample
		return Signal{magnitude[0]}
	}

	// Pad to power of 2 for Cooley-Tukey
	size := nextPowerOf2(n)
	x := make([]complex128, size)

	// Reconstruct complex spectrum from magnitude and phase
	// Positive frequencies: bins 0 to N/2
	for i := 0; i < numBins; i++ {
		x[i] = cmplx.Rect(magnitude[i], phase[i])
	}

	// Negative frequencies: conjugate symmetry
	// x[N-k] = conj(x[k]) for k = 1 to N/2-1
	for k := 1; k < numBins-1; k++ {
		x[n-k] = cmplx.Conj(x[k])
	}

	// Apply inverse FFT
	ifftCooleyTukey(x)

	// Extract real part and normalize
	result := make(Signal, n)
	for i := 0; i < n; i++ {
		result[i] = real(x[i]) / float64(size)
	}

	return result
}

// ifftCooleyTukey performs in-place inverse Cooley-Tukey radix-2 FFT.
// This is the same as forward FFT with conjugate twiddle factors.
func ifftCooleyTukey(x []complex128) {
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

	// Cooley-Tukey iterative IFFT (positive angle for inverse)
	for size := 2; size <= n; size *= 2 {
		halfSize := size / 2
		step := 2 * math.Pi / float64(size) // Positive for IFFT (vs negative for FFT)
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

