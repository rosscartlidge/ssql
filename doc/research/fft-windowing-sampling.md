# FFT Windowing and Sampling Considerations

**Status:** Draft for expert review
**Date:** January 2026
**Purpose:** Document FFT best practices and propose API design for ssql

## Executive Summary

ssql v4.8.0 added FFT and convolution commands for signal processing. The current implementation is functional but lacks windowing support and uses a naive DFT algorithm. This document outlines what serious users need and proposes API additions for expert review.

## Current Implementation

### What We Have

```go
// Current API
spectrum, err := ssql.FFT(signal)           // Returns magnitude spectrum
spectrum, err := ssql.FFTWithPhase(signal)  // Returns magnitude + phase

// CLI
ssql from data.csv | ssql fft -field value -rate 44100
```

### Current Limitations

1. **No windowing** - Signal assumed to be periodic
2. **Naive DFT** - O(n²) algorithm on CPU, cuFFT on GPU
3. **No zero-padding** - Fixed frequency resolution
4. **Any sample size** - No power-of-2 optimization on CPU

---

## Problem 1: Spectral Leakage

### The Issue

The DFT assumes the input signal is one period of an infinitely repeating waveform. When the signal doesn't complete an integer number of cycles within the sample window, discontinuities occur at the boundaries, causing energy to "leak" into adjacent frequency bins.

**Example:**
- Signal: 10 Hz sine wave sampled at 100 Hz
- 256 samples = 2.56 seconds = 25.6 complete cycles
- The 0.6 partial cycle creates a discontinuity
- Result: Energy spreads across multiple bins instead of a single sharp peak

### Visual Representation

```
Without windowing (rectangular window):
Signal: ____/‾‾‾‾\____/‾‾‾‾\____/‾‾‾‾\___| discontinuity
                                         ↑
FFT sees this as: ...‾‾‾‾\____/‾‾‾‾\____/‾‾‾‾\____/‾‾‾‾\...
                  (wraps around - creates sharp edge)

Spectrum: Smeared peak with sidelobes
    │    ███
    │   █████
    │  ███████
    │ █████████
    └──────────── frequency

With Hann window:
Signal: ____/‾‾‾‾\____/‾‾‾‾\____/‾‾‾‾\___
Window: __/‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾\__  (tapers to zero)
Result: Smooth transition at edges

Spectrum: Sharp peak, minimal sidelobes
    │     █
    │    ███
    │    ███
    │    ███
    └──────────── frequency
```

### When Windowing Matters

| Use Case | Windowing Needed? | Reason |
|----------|-------------------|--------|
| Finding dominant frequencies | Yes | Leakage obscures nearby peaks |
| Measuring exact amplitudes | Yes | Leakage spreads energy |
| Periodic signal, exact cycles | No | No discontinuity |
| Noise floor analysis | Yes | Sidelobes raise apparent noise |
| Quick spectral overview | Maybe | Depends on precision needed |

---

## Problem 2: Window Function Selection

### Common Window Functions

Each window trades off between **main lobe width** (frequency resolution) and **sidelobe level** (leakage suppression).

#### Rectangular (No Window)
```
w[n] = 1
```
- Main lobe: Narrowest (best frequency resolution)
- Sidelobes: -13 dB (worst leakage)
- Use: Only when signal is exactly periodic in window

#### Hann (Hanning)
```
w[n] = 0.5 - 0.5 * cos(2πn / (N-1))
```
- Main lobe: 1.5x rectangular
- Sidelobes: -31 dB
- Use: General purpose, good compromise
- Note: Smooth, exactly zero at endpoints

#### Hamming
```
w[n] = 0.54 - 0.46 * cos(2πn / (N-1))
```
- Main lobe: 1.4x rectangular
- Sidelobes: -42 dB (first sidelobe), but doesn't decay as fast
- Use: When first sidelobe matters most
- Note: Doesn't reach zero at endpoints (small discontinuity)

#### Blackman
```
w[n] = 0.42 - 0.5 * cos(2πn / (N-1)) + 0.08 * cos(4πn / (N-1))
```
- Main lobe: 1.7x rectangular
- Sidelobes: -58 dB
- Use: When sidelobe suppression is critical
- Note: Wider main lobe means lower frequency resolution

#### Blackman-Harris
```
w[n] = 0.35875 - 0.48829*cos(2πn/(N-1)) + 0.14128*cos(4πn/(N-1)) - 0.01168*cos(6πn/(N-1))
```
- Main lobe: 2x rectangular
- Sidelobes: -92 dB
- Use: Maximum sidelobe suppression needed

#### Kaiser
```
w[n] = I₀(β * sqrt(1 - ((n - N/2) / (N/2))²)) / I₀(β)
```
- Parameterized by β (shape parameter)
- β=0: Rectangular, β=5: Similar to Hamming, β=8.6: Similar to Blackman-Harris
- Use: When you need precise control over the tradeoff

#### Flat-Top
```
w[n] = 1 - 1.93*cos(2πn/(N-1)) + 1.29*cos(4πn/(N-1)) - 0.388*cos(6πn/(N-1)) + 0.028*cos(8πn/(N-1))
```
- Main lobe: Very wide
- Sidelobes: -93 dB
- **Special property:** Minimal amplitude error (<0.01%)
- Use: Amplitude calibration, measuring exact signal levels

### Comparison Table

| Window | Main Lobe Width | Sidelobe Level | Amplitude Accuracy | Best For |
|--------|-----------------|----------------|-------------------|----------|
| Rectangular | 1.0x | -13 dB | ±3.9 dB | Periodic signals only |
| Hann | 1.5x | -31 dB | ±1.4 dB | General purpose |
| Hamming | 1.4x | -42 dB | ±1.8 dB | Good sidelobe suppression |
| Blackman | 1.7x | -58 dB | ±1.1 dB | Low sidelobes needed |
| Blackman-Harris | 2.0x | -92 dB | ±0.8 dB | Very low sidelobes |
| Kaiser (β=8.6) | 2.0x | -90 dB | Varies | Tunable tradeoff |
| Flat-Top | 3.8x | -93 dB | ±0.01 dB | Amplitude measurement |

### Recommendation for Default

**Hann window** is the standard default for general-purpose spectral analysis:
- Good balance of resolution and leakage suppression
- Well-understood behavior
- No surprising artifacts
- Industry standard in many tools (MATLAB, NumPy, etc.)

---

## Problem 3: Sample Size Considerations

### Power-of-2 Sizes

The Cooley-Tukey FFT algorithm (the standard "fast" FFT) works most efficiently with power-of-2 sizes:

| Size | Complexity (naive DFT) | Complexity (FFT) | Speedup |
|------|----------------------|------------------|---------|
| 256 | 65,536 ops | 2,048 ops | 32x |
| 1024 | 1,048,576 ops | 10,240 ops | 102x |
| 4096 | 16,777,216 ops | 49,152 ops | 341x |
| 16384 | 268,435,456 ops | 229,376 ops | 1,170x |

**Current ssql situation:**
- CPU: Naive DFT, O(n²) - size doesn't matter for algorithm, but larger is slower
- GPU (cuFFT): Handles any size efficiently via mixed-radix algorithms

### Zero-Padding

Zero-padding adds zeros to the end of the signal before FFT:

**Benefits:**
1. Can pad to power-of-2 for algorithm efficiency
2. Interpolates between frequency bins (finer-looking spectrum)
3. Does NOT increase true frequency resolution (that's determined by signal duration)

**Example:**
```
Original: 1000 samples at 1000 Hz = 1 Hz frequency resolution
Padded to 2048: Still 1 Hz resolution, but interpolated to show 0.49 Hz bins
Padded to 4096: Still 1 Hz resolution, but interpolated to show 0.24 Hz bins
```

**When to use zero-padding:**
- Need power-of-2 size for FFT efficiency
- Want smoother-looking spectrum
- Preparing for cross-correlation or convolution

**When NOT to use:**
- Don't need it for quick analysis
- Memory constrained

### Frequency Resolution

True frequency resolution is determined by signal duration, not sample count:

```
Δf = 1 / T = sample_rate / N

Where:
  Δf = frequency resolution (Hz)
  T = signal duration (seconds)
  N = number of samples
  sample_rate = samples per second
```

**To improve frequency resolution:**
- Collect more data (longer recording)
- NOT: Zero-pad (only interpolates, doesn't add information)

---

## Problem 4: Overlap-Add for Long Signals

For very long signals or streaming data, processing in overlapping segments is standard:

### Short-Time Fourier Transform (STFT)

1. Divide signal into overlapping segments (typically 50-75% overlap)
2. Apply window to each segment
3. Compute FFT of each segment
4. Result: Time-frequency representation (spectrogram)

**Overlap compensation:**
- Hann window with 50% overlap: Perfect reconstruction
- Hann window with 75% overlap: Better time resolution
- Different windows need different overlap for unity gain

### When Users Need This

- Audio analysis (spectrograms)
- Real-time spectrum analyzers
- Time-varying frequency content
- Very long recordings that don't fit in memory

---

## Proposed API Design

### Option A: Separate Functions (Explicit)

```go
// Window functions
func HannWindow(n int) Signal
func HammingWindow(n int) Signal
func BlackmanWindow(n int) Signal
func BlackmanHarrisWindow(n int) Signal
func KaiserWindow(n int, beta float64) Signal
func FlatTopWindow(n int) Signal

// Apply window to signal
func ApplyWindow(signal, window Signal) Signal

// Zero-pad to next power of 2
func ZeroPadToPowerOf2(signal Signal) Signal

// Zero-pad to specific size
func ZeroPad(signal Signal, targetSize int) Signal

// Usage
signal := loadSignal()
window := ssql.HannWindow(len(signal))
windowed := ssql.ApplyWindow(signal, window)
padded := ssql.ZeroPadToPowerOf2(windowed)
spectrum, _ := ssql.FFT(padded)
```

**Pros:** Maximum control, composable, explicit
**Cons:** Verbose for common cases

### Option B: Options Struct (Convenient)

```go
type FFTOptions struct {
    Window     string  // "none", "hann", "hamming", "blackman", "blackman-harris", "flat-top"
    ZeroPad    bool    // Pad to next power of 2
    TargetSize int     // Specific target size (0 = auto)
}

func FFTWithOptions(signal Signal, opts FFTOptions) (*Spectrum, error)

// Usage
spectrum, _ := ssql.FFTWithOptions(signal, ssql.FFTOptions{
    Window:  "hann",
    ZeroPad: true,
})

// Simple case remains simple
spectrum, _ := ssql.FFT(signal)  // No window, no padding
```

**Pros:** Convenient, discoverable options
**Cons:** Less composable, options struct can grow unwieldy

### Option C: Functional Options (Go Idiomatic)

```go
func FFT(signal Signal, opts ...FFTOption) (*Spectrum, error)

type FFTOption func(*fftConfig)

func WithWindow(name string) FFTOption
func WithKaiserWindow(beta float64) FFTOption
func WithZeroPadding() FFTOption
func WithTargetSize(n int) FFTOption

// Usage
spectrum, _ := ssql.FFT(signal,
    ssql.WithWindow("hann"),
    ssql.WithZeroPadding(),
)

// Simple case
spectrum, _ := ssql.FFT(signal)  // No options = no window, no padding
```

**Pros:** Extensible, Go idiomatic, optional options
**Cons:** Slightly more complex implementation

### CLI Design

```bash
# Current (no windowing)
ssql fft -field value

# Proposed additions
ssql fft -field value -window hann
ssql fft -field value -window hamming
ssql fft -field value -window blackman
ssql fft -field value -window kaiser -beta 8.6
ssql fft -field value -window flat-top
ssql fft -field value -pad-power2
ssql fft -field value -pad-size 4096

# Combined
ssql fft -field value -window hann -pad-power2 -rate 44100
```

### Spectrogram (Future)

For STFT/spectrogram support:

```bash
# CLI
ssql spectrogram -field value -window hann -size 1024 -overlap 512 -rate 44100

# Library
spectrograms := ssql.STFT(signal, ssql.STFTOptions{
    WindowSize: 1024,
    HopSize:    512,  // overlap = WindowSize - HopSize
    Window:     "hann",
})
```

---

## Questions for Expert Review

1. **Default behavior:** Should `ssql fft` apply Hann window by default, or require explicit `-window` flag?
   - Argument for default: Most users need it, prevents common mistake
   - Argument against: Explicit is better, changing default is breaking change

2. **Window naming:** Should we use "hann" or "hanning"? (Technically "Hann" is correct, named after Julius von Hann, but "Hanning" is common)

3. **Amplitude correction:** Windows reduce signal amplitude. Should we automatically apply coherent gain correction?
   ```
   Hann: multiply result by 2.0
   Hamming: multiply result by 1.85
   etc.
   ```

4. **Power spectrum vs magnitude:** Currently we return magnitude. Should we also offer power spectrum (magnitude²) and power spectral density (normalized by bin width)?

5. **Negative frequencies:** Currently we only return positive frequencies (n/2+1 bins for real input). Should we offer option for full spectrum?

6. **dB output:** Should we add option to return results in decibels?
   ```
   dB = 20 * log10(magnitude / reference)
   ```

7. **Zero-padding default:** Should `-pad-power2` be default when using GPU (where FFT is more efficient for power-of-2)?

8. **STFT priority:** How important is spectrogram support? Should it be in initial release or future addition?

9. **Overlap-add:** For convolution of very long signals, should we implement overlap-add/overlap-save methods?

10. **Real FFT optimization:** The current implementation exploits real-input symmetry (only computing n/2+1 positive frequencies). Is this sufficient, or should we use dedicated real-FFT algorithms (RFFT)?

---

## Implementation Priority

Based on typical user needs, suggested implementation order:

### Phase 1 (Essential)
1. Window functions: Hann, Hamming, Blackman
2. Apply window helper
3. CLI `-window` flag
4. Zero-pad to power-of-2

### Phase 2 (Important)
5. Blackman-Harris, Kaiser, Flat-Top windows
6. Coherent gain correction option
7. CLI `-pad-power2` and `-pad-size` flags

### Phase 3 (Advanced)
8. STFT/Spectrogram
9. Power spectral density
10. dB output option

### Phase 4 (Specialized)
11. Overlap-add convolution
12. Dedicated RFFT algorithm

---

## References

1. Harris, F.J. (1978). "On the use of windows for harmonic analysis with the discrete Fourier transform." Proceedings of the IEEE, 66(1), 51-83.

2. Oppenheim, A.V., Schafer, R.W. (2010). Discrete-Time Signal Processing (3rd ed.). Pearson.

3. Smith, S.W. (1997). The Scientist and Engineer's Guide to Digital Signal Processing. California Technical Publishing. (Free online: dspguide.com)

4. NumPy documentation: numpy.fft module, numpy.windows module

5. MATLAB documentation: Signal Processing Toolbox, fft, window functions

---

## Appendix: Window Function Implementations

For reference, here are the mathematical definitions:

```go
// Hann window
func HannWindow(n int) Signal {
    w := make(Signal, n)
    for i := 0; i < n; i++ {
        w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
    }
    return w
}

// Hamming window
func HammingWindow(n int) Signal {
    w := make(Signal, n)
    for i := 0; i < n; i++ {
        w[i] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1))
    }
    return w
}

// Blackman window
func BlackmanWindow(n int) Signal {
    w := make(Signal, n)
    for i := 0; i < n; i++ {
        w[i] = 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)) +
               0.08*math.Cos(4*math.Pi*float64(i)/float64(n-1))
    }
    return w
}

// Apply window (element-wise multiplication)
func ApplyWindow(signal, window Signal) Signal {
    if len(signal) != len(window) {
        panic("signal and window must have same length")
    }
    result := make(Signal, len(signal))
    for i := range signal {
        result[i] = signal[i] * window[i]
    }
    return result
}

// Zero-pad to next power of 2
func ZeroPadToPowerOf2(signal Signal) Signal {
    n := len(signal)
    target := 1
    for target < n {
        target <<= 1
    }
    if target == n {
        return signal
    }
    result := make(Signal, target)
    copy(result, signal)
    return result
}
```
