# Signal Processing with ssql: GPU-Accelerated Tutorial

This codelab teaches you how to use ssql's signal processing commands for frequency analysis, filtering, smoothing, and pattern detection. With GPU acceleration, these operations run 10-50x faster on large datasets.

## Table of Contents

1. [Installation](#installation)
2. [Understanding Signal Data](#understanding-signal-data)
3. [Frequency Analysis with FFT](#frequency-analysis-with-fft)
4. [Frequency Domain Filtering](#frequency-domain-filtering)
5. [Smoothing with Convolution](#smoothing-with-convolution)
6. [Pattern Detection with Correlation](#pattern-detection-with-correlation)
7. [Real-World Examples](#real-world-examples)
8. [Performance Tips](#performance-tips)

---

## Installation

### Standard Installation (CPU only)

```bash
# Install ssql - works on any system
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# Verify installation
ssql version
```

All signal processing commands work immediately with CPU. GPU acceleration is optional but provides significant speedups for large datasets.

### GPU-Accelerated Installation (10-50x faster)

**Requirements:**
- NVIDIA GPU with CUDA support
- CUDA Toolkit installed (`nvcc` compiler in PATH)

```bash
# Clone the repository
git clone https://github.com/rosscartlidge/ssql.git
cd ssql

# Build the CUDA library
cd gpu && make && cd ..

# Build ssql with GPU support
go build -tags gpu -o ssql_gpu ./cmd/ssql

# Install to your Go bin directory
cp ssql_gpu ~/go/bin/

# Add alias to ~/.bashrc for convenience
echo 'alias ssql_gpu="LD_LIBRARY_PATH='$(pwd)'/gpu ~/go/bin/ssql_gpu"' >> ~/.bashrc
source ~/.bashrc

# Verify GPU version works
ssql_gpu version
```

**When is GPU faster?**

| Operation | GPU Threshold | Speedup |
|-----------|---------------|---------|
| FFT/IFFT | ≥16K samples | 28-54x |
| Convolution | ≥16 point kernel | 4-500x |
| Correlation | ≥16 point window | 4-500x |

Below these thresholds, CPU is used automatically (faster due to transfer overhead).

---

## Understanding Signal Data

ssql processes signals as records with numeric fields. Each record represents one sample point.

### Creating Test Signals

**Simple sine wave:**
```bash
# Generate 1024 samples of a 10 Hz sine wave (at 1000 Hz sample rate)
cat > /tmp/sine_wave.csv << 'EOF'
time,amplitude
EOF

for i in $(seq 0 1023); do
  t=$(echo "scale=6; $i / 1000" | bc)
  amp=$(echo "scale=6; s(2 * 3.14159 * 10 * $t)" | bc -l)
  echo "$t,$amp"
done >> /tmp/sine_wave.csv

# View the signal
ssql from /tmp/sine_wave.csv | ssql limit 10 | ssql to table
```

**Multi-frequency signal (more interesting for FFT):**
```bash
# 10 Hz + 50 Hz + 120 Hz components
cat > /tmp/generate_signal.py << 'EOF'
import math
print("time,amplitude")
for i in range(4096):
    t = i / 1000.0  # 1000 Hz sample rate
    amp = (math.sin(2 * math.pi * 10 * t) +      # 10 Hz
           0.5 * math.sin(2 * math.pi * 50 * t) +  # 50 Hz
           0.3 * math.sin(2 * math.pi * 120 * t))  # 120 Hz
    print(f"{t:.6f},{amp:.6f}")
EOF

python3 /tmp/generate_signal.py > /tmp/multi_freq.csv

# Visualize the time-domain signal (first 500 samples)
ssql from /tmp/multi_freq.csv | \
  ssql limit 500 | \
  ssql to chart -x time -y amplitude -output /tmp/signal_time.html

echo "Open /tmp/signal_time.html to see the signal"
```

---

## Frequency Analysis with FFT

The Fast Fourier Transform (FFT) converts time-domain signals to frequency-domain, revealing the frequency components.

### Basic FFT

```bash
# Compute FFT of our multi-frequency signal
ssql from /tmp/multi_freq.csv | \
  ssql fft -field amplitude -rate 1000 | \
  ssql to table | head -20
```

Output shows frequency bins with their magnitudes:
```
index   frequency   magnitude
0       0           0.00234
1       0.244       0.00156
...
41      10.0        2048.5    <- 10 Hz peak!
...
205     50.0        1024.2    <- 50 Hz peak!
...
492     120.0       614.5     <- 120 Hz peak!
```

### Visualizing the Frequency Spectrum

```bash
# Create a frequency spectrum chart
ssql from /tmp/multi_freq.csv | \
  ssql fft -field amplitude -rate 1000 | \
  ssql where -where frequency le 200 | \
  ssql to chart -x frequency -y magnitude -output /tmp/spectrum.html

echo "Open /tmp/spectrum.html to see the frequency spectrum"
```

The chart will show clear peaks at 10 Hz, 50 Hz, and 120 Hz - exactly the frequencies we put into the signal!

### Finding Dominant Frequencies

```bash
# Find the top 5 frequency peaks
ssql from /tmp/multi_freq.csv | \
  ssql fft -field amplitude -rate 1000 | \
  ssql where -where frequency gt 1 | \
  ssql sort -desc magnitude | \
  ssql limit 5 | \
  ssql to table
```

### Including Phase Information

Phase tells you the offset of each frequency component:

```bash
# FFT with phase (needed for reconstruction via IFFT)
ssql from /tmp/multi_freq.csv | \
  ssql fft -field amplitude -rate 1000 -phase | \
  ssql where -where frequency le 150 | \
  ssql to table | head -20
```

---

## Frequency Domain Filtering

One of the most powerful signal processing techniques: transform to frequency domain, remove unwanted frequencies, transform back.

### Low-Pass Filter (Remove High Frequencies)

```bash
# Add noise to our signal first
cat > /tmp/add_noise.py << 'EOF'
import math
import random
print("time,amplitude")
for i in range(4096):
    t = i / 1000.0
    clean = (math.sin(2 * math.pi * 10 * t) +
             0.5 * math.sin(2 * math.pi * 50 * t))
    noise = 0.3 * (random.random() - 0.5)  # High-frequency noise
    print(f"{t:.6f},{clean + noise:.6f}")
EOF

python3 /tmp/add_noise.py > /tmp/noisy_signal.csv

# View noisy signal
ssql from /tmp/noisy_signal.csv | \
  ssql limit 500 | \
  ssql to chart -x time -y amplitude -output /tmp/noisy.html

# Apply low-pass filter: FFT -> remove high frequencies -> IFFT
ssql from /tmp/noisy_signal.csv | \
  ssql fft -field amplitude -rate 1000 -phase | \
  ssql where -where frequency le 100 | \
  ssql ifft -magnitude magnitude -phase phase | \
  ssql to chart -x index -y signal -output /tmp/filtered.html

echo "Compare /tmp/noisy.html and /tmp/filtered.html"
```

### Band-Pass Filter (Keep Only Specific Frequencies)

```bash
# Keep only frequencies between 40-60 Hz (isolate the 50 Hz component)
ssql from /tmp/multi_freq.csv | \
  ssql fft -field amplitude -rate 1000 -phase | \
  ssql where -where frequency ge 40 | \
  ssql where -where frequency le 60 | \
  ssql ifft -magnitude magnitude -phase phase | \
  ssql to chart -x index -y signal -output /tmp/bandpass.html
```

### Notch Filter (Remove Specific Frequency)

```bash
# Remove 50 Hz (e.g., power line interference)
ssql from /tmp/multi_freq.csv | \
  ssql fft -field amplitude -rate 1000 -phase | \
  ssql where -where-expr 'frequency < 48 || frequency > 52' | \
  ssql ifft -magnitude magnitude -phase phase | \
  ssql to chart -x index -y signal -output /tmp/notch.html
```

---

## Smoothing with Convolution

Convolution applies a kernel (filter) to smooth, sharpen, or detect edges in signals.

### Moving Average Smoothing

```bash
# Create a noisy signal
cat > /tmp/noisy_steps.py << 'EOF'
import random
print("index,value")
for i in range(1000):
    base = 0 if i < 300 else (50 if i < 600 else 25)
    noise = random.gauss(0, 5)
    print(f"{i},{base + noise:.2f}")
EOF

python3 /tmp/noisy_steps.py > /tmp/noisy_steps.csv

# View original noisy signal
ssql from /tmp/noisy_steps.csv | \
  ssql to chart -x index -y value -output /tmp/noisy_steps.html

# Apply 21-point moving average
ssql from /tmp/noisy_steps.csv | \
  ssql convolve -field value -kernel moving-average -size 21 -same | \
  ssql to chart -x index -y convolved -output /tmp/smoothed.html

echo "Compare /tmp/noisy_steps.html and /tmp/smoothed.html"
```

### Gaussian Smoothing

Gaussian smoothing provides smoother results than moving average:

```bash
# Apply Gaussian smoothing (sigma=5, size=21)
ssql from /tmp/noisy_steps.csv | \
  ssql convolve -field value -kernel gaussian -size 21 -sigma 5 -same | \
  ssql to chart -x index -y convolved -output /tmp/gaussian_smooth.html
```

### Edge Detection with Derivative Kernel

```bash
# Detect edges/transitions in the step signal
ssql from /tmp/noisy_steps.csv | \
  ssql convolve -field value -kernel moving-average -size 11 -same | \
  ssql convolve -field convolved -kernel diff -same | \
  ssql to chart -x index -y convolved -output /tmp/edges.html
```

### Custom Kernels

```bash
# Create a custom high-pass filter kernel
ssql from /tmp/noisy_steps.csv | \
  ssql convolve -field value -kernel "-1,-1,-1,8,-1,-1,-1" -same | \
  ssql to chart -x index -y convolved -output /tmp/highpass.html
```

---

## Pattern Detection with Correlation

Cross-correlation finds where a pattern appears in a signal. Autocorrelation finds repeating patterns.

### Cross-Correlating Two Sensors

Cross-correlation finds the time delay or similarity between two signals. This is useful for:
- Finding time lag between sensors
- Detecting signal propagation delays
- Comparing similar signals for alignment

```bash
# Create two related sensor signals with a time delay
cat > /tmp/two_sensors.py << 'EOF'
import math
import random
print("index,sensor1,sensor2")

# sensor2 is sensor1 delayed by 20 samples + noise
delay = 20
for i in range(500):
    # Base signal: sine wave with some variation
    base = math.sin(2 * math.pi * i / 50) + 0.5 * math.sin(2 * math.pi * i / 23)

    sensor1 = base + 0.2 * random.gauss(0, 1)

    # sensor2 sees the same signal, but delayed
    delayed_i = i - delay
    if delayed_i >= 0:
        delayed_base = math.sin(2 * math.pi * delayed_i / 50) + 0.5 * math.sin(2 * math.pi * delayed_i / 23)
        sensor2 = delayed_base + 0.2 * random.gauss(0, 1)
    else:
        sensor2 = 0.2 * random.gauss(0, 1)

    print(f"{i},{sensor1:.4f},{sensor2:.4f}")
EOF

python3 /tmp/two_sensors.py > /tmp/two_sensors.csv

# View both sensors
ssql from /tmp/two_sensors.csv | \
  ssql limit 200 | \
  ssql to chart -x index -y sensor1 -output /tmp/sensor1.html

ssql from /tmp/two_sensors.csv | \
  ssql limit 200 | \
  ssql to chart -x index -y sensor2 -output /tmp/sensor2.html

# Cross-correlate to find the delay
ssql from /tmp/two_sensors.csv | \
  ssql correlate -field sensor1 -with sensor2 | \
  ssql to chart -x lag -y correlation -output /tmp/cross_corr.html

echo "Peak in /tmp/cross_corr.html shows the 20-sample delay"
```

**Finding the exact delay:**
```bash
# Find the lag with maximum correlation
ssql from /tmp/two_sensors.csv | \
  ssql correlate -field sensor1 -with sensor2 | \
  ssql sort -desc correlation | \
  ssql limit 1 | \
  ssql to table
```

### Autocorrelation for Periodicity Detection

```bash
# Create a periodic signal
cat > /tmp/periodic.py << 'EOF'
import math
import random
print("index,value")
for i in range(1000):
    # Period of 50 samples
    periodic = math.sin(2 * math.pi * i / 50) * 10
    noise = random.gauss(0, 2)
    print(f"{i},{periodic + noise:.2f}")
EOF

python3 /tmp/periodic.py > /tmp/periodic.csv

# View the signal
ssql from /tmp/periodic.csv | \
  ssql to chart -x index -y value -output /tmp/periodic.html

# Autocorrelation to find the period
ssql from /tmp/periodic.csv | \
  ssql correlate -field value -auto -max-lag 200 | \
  ssql to chart -x lag -y correlation -output /tmp/autocorr.html

echo "Peaks at lag=50, 100, 150... show period of 50 samples"
```

---

## Real-World Examples

### Example 1: Audio Spectrum Analyzer

```bash
# Simulate audio samples (mix of musical notes)
cat > /tmp/audio_sim.py << 'EOF'
import math
print("sample,amplitude")
sample_rate = 44100
duration = 0.1  # 100ms

# Musical notes: A4 (440 Hz), E5 (659 Hz), A5 (880 Hz)
for i in range(int(sample_rate * duration)):
    t = i / sample_rate
    amp = (0.5 * math.sin(2 * math.pi * 440 * t) +   # A4
           0.3 * math.sin(2 * math.pi * 659 * t) +   # E5
           0.2 * math.sin(2 * math.pi * 880 * t))    # A5
    print(f"{i},{amp:.6f}")
EOF

python3 /tmp/audio_sim.py > /tmp/audio.csv

# Compute and visualize spectrum
ssql from /tmp/audio.csv | \
  ssql fft -field amplitude -rate 44100 | \
  ssql where -where frequency le 2000 | \
  ssql where -where frequency ge 100 | \
  ssql to chart -x frequency -y magnitude -output /tmp/audio_spectrum.html

echo "Open /tmp/audio_spectrum.html - peaks at 440, 659, 880 Hz"
```

### Example 2: Vibration Analysis (Machine Health)

```bash
# Simulate machine vibration with bearing fault frequency
cat > /tmp/vibration.py << 'EOF'
import math
import random
print("time,acceleration")
sample_rate = 10000
duration = 1.0

# Normal rotation: 25 Hz
# Bearing fault: 156 Hz (typical for ball pass frequency)
for i in range(int(sample_rate * duration)):
    t = i / sample_rate
    normal = math.sin(2 * math.pi * 25 * t)
    fault = 0.3 * math.sin(2 * math.pi * 156 * t)  # Bearing defect
    noise = 0.1 * random.gauss(0, 1)
    print(f"{t:.6f},{normal + fault + noise:.6f}")
EOF

python3 /tmp/vibration.py > /tmp/vibration.csv

# Analyze vibration spectrum
ssql from /tmp/vibration.csv | \
  ssql fft -field acceleration -rate 10000 | \
  ssql where -where frequency le 500 | \
  ssql to chart -x frequency -y magnitude -output /tmp/vibration_spectrum.html

echo "Open /tmp/vibration_spectrum.html - fault peak at 156 Hz"
```

### Example 3: ECG Signal Processing

```bash
# Simulate ECG-like signal
cat > /tmp/ecg_sim.py << 'EOF'
import math
import random
print("time,voltage")

def qrs_complex(t, center):
    """Generate a QRS complex centered at 'center'"""
    dt = t - center
    if abs(dt) > 0.1:
        return 0
    # Simplified QRS shape
    return 1.5 * math.exp(-500 * dt * dt) - 0.3 * math.exp(-100 * (dt - 0.03) ** 2)

sample_rate = 500  # 500 Hz
duration = 5.0
heart_rate = 72  # BPM
beat_interval = 60.0 / heart_rate

for i in range(int(sample_rate * duration)):
    t = i / sample_rate
    voltage = 0
    # Add QRS complexes
    beat_num = 0
    while beat_num * beat_interval < duration:
        voltage += qrs_complex(t, beat_num * beat_interval + 0.1)
        beat_num += 1
    # Add noise
    voltage += 0.05 * random.gauss(0, 1)
    # Add baseline wander (low frequency)
    voltage += 0.1 * math.sin(2 * math.pi * 0.3 * t)
    print(f"{t:.4f},{voltage:.4f}")
EOF

python3 /tmp/ecg_sim.py > /tmp/ecg.csv

# View raw ECG
ssql from /tmp/ecg.csv | \
  ssql where -where time le 3 | \
  ssql to chart -x time -y voltage -output /tmp/ecg_raw.html

# Remove baseline wander with high-pass filter (FFT method)
ssql from /tmp/ecg.csv | \
  ssql fft -field voltage -rate 500 -phase | \
  ssql where -where frequency ge 0.5 | \
  ssql ifft -magnitude magnitude -phase phase | \
  ssql update -set-expr time 'index / 500.0' | \
  ssql where -where time le 3 | \
  ssql to chart -x time -y signal -output /tmp/ecg_filtered.html

# Find heart rate using autocorrelation
ssql from /tmp/ecg.csv | \
  ssql correlate -field voltage -auto -max-lag 500 | \
  ssql to chart -x lag -y correlation -output /tmp/ecg_autocorr.html

echo "Compare /tmp/ecg_raw.html and /tmp/ecg_filtered.html"
echo "Autocorrelation peak in /tmp/ecg_autocorr.html shows beat interval"
```

---

## Performance Tips

### 1. Use GPU for Large Datasets

```bash
# CPU version - fine for small signals
ssql from small_signal.csv | ssql fft -field value

# GPU version - 28-54x faster for signals >= 16K samples
ssql_gpu from large_signal.csv | ssql_gpu fft -field value
```

### 2. Chain Operations Efficiently

```bash
# Good: Single pipeline
ssql from data.csv | \
  ssql fft -field value -rate 1000 -phase | \
  ssql where -where frequency le 100 | \
  ssql ifft -magnitude magnitude -phase phase

# Avoid: Multiple separate invocations with intermediate files
ssql from data.csv | ssql fft -field value > /tmp/fft.csv
ssql from /tmp/fft.csv | ssql where ... > /tmp/filtered.csv
ssql from /tmp/filtered.csv | ssql ifft ...
```

### 3. Use -max-lag for Autocorrelation

When searching for periodicity, limit the search range:

```bash
# Slow: Full autocorrelation (O(n^2) via FFT)
ssql from data.csv | ssql correlate -field value -auto

# Fast: Limited lag (O(n * maxLag) direct computation)
ssql from data.csv | ssql correlate -field value -auto -max-lag 1000
```

### 4. Filter Before FFT When Possible

```bash
# If you only need part of the spectrum, filter first
ssql from huge_signal.csv | \
  ssql limit 16384 | \
  ssql fft -field value -rate 1000
```

### 5. GPU Memory Considerations

For very large signals (>100M samples), process in chunks:

```bash
# Process 1M samples at a time
for offset in 0 1000000 2000000; do
  ssql from huge.csv | \
    ssql offset $offset | \
    ssql limit 1000000 | \
    ssql_gpu fft -field value >> /tmp/spectra.jsonl
done
```

---

## Command Reference

| Command | Description | Key Flags |
|---------|-------------|-----------|
| `fft` | Time to frequency domain | `-field`, `-rate`, `-phase` |
| `ifft` | Frequency to time domain | `-magnitude`, `-phase`, `-output` |
| `convolve` | Apply filter kernel | `-field`, `-kernel`, `-size`, `-same` |
| `correlate` | Cross/auto correlation | `-field`, `-with`, `-auto`, `-max-lag` |

### Built-in Kernels for Convolution

| Kernel | Description | Example |
|--------|-------------|---------|
| `moving-average` | Simple smoothing | `-kernel moving-average -size 21` |
| `gaussian` | Smooth Gaussian | `-kernel gaussian -size 21 -sigma 5` |
| `diff` | First derivative | `-kernel diff` |
| `laplacian` | Second derivative | `-kernel laplacian` |
| Custom | Your own values | `-kernel "1,2,3,2,1"` |

---

## Next Steps

- [API Reference](../api-reference.md) - Use signal processing in Go code
- [GPU Acceleration Details](../research/gpu-acceleration.md) - Technical deep-dive
- [CLI Tutorial](cli-codelab.md) - General ssql usage
