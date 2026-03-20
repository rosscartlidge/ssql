# GPU Feature Opportunities

This document explores potential GPU-accelerated features for ssql, based on lessons learned from FFT/convolution implementation.

## GPU Performance Characteristics

From our benchmarks (RTX 5090 + Intel Core Ultra 9 275HX):

| Operation | CPU | GPU | Winner |
|-----------|-----|-----|--------|
| Sum 1M float64 | 86μs | 601μs | CPU 7x faster |
| Convolution 100K×1K | 195ms | 603μs | GPU 320x faster |
| FFT 1M points | hours | 2.9ms | GPU ∞ faster |

**GPU wins when:**
- Compute-heavy (many operations per byte transferred)
- Large datasets (amortize kernel launch overhead)
- Parallel-friendly algorithms (same operation on many elements)

**GPU loses when:**
- Memory-bound (simple aggregations)
- Small datasets (<10K elements)
- Sequential dependencies

---

## Tier 1: High Value, Natural Fit

### 1. Spectrogram

Sliding window FFT - essential for audio, vibration, and time-series analysis.

**CLI Interface:**
```bash
# Basic spectrogram
ssql from audio.arrow | ssql spectrogram -field amplitude -window 1024 -hop 256

# With parameters
ssql spectrogram -file signal.arrow -field value \
  -window 2048 \
  -hop 512 \
  -window-type hann \
  -output-format magnitude

# Output: time, frequency, magnitude (3-column output for heatmap visualization)
```

**Flags:**
- `-field` - input signal field (required)
- `-window` - FFT window size (default: 1024)
- `-hop` - samples between windows (default: window/4)
- `-window-type` - none, hann, hamming, blackman (default: hann)
- `-output-format` - magnitude, power, db (default: magnitude)
- `-rate` - sample rate for frequency axis labeling

**Why GPU helps:** Each window is an independent FFT. With 1M samples and hop=256, that's ~4000 FFTs - perfect for GPU parallelism.

**Estimated speedup:** 20-50x over CPU

---

### 2. Correlation Matrix

Compute pairwise correlations between all numeric fields. O(N²) field comparisons, each scanning all records.

**CLI Interface:**
```bash
# Correlate all numeric fields
ssql from stocks.csv | ssql correlation-matrix

# Select specific fields
ssql from data.csv | ssql correlation-matrix -fields price volume trades

# Output as heatmap-ready format
ssql from data.csv | ssql correlation-matrix -format long
# Output: field1, field2, correlation

# Output as matrix
ssql from data.csv | ssql correlation-matrix -format wide
# Output: field, price, volume, trades (matrix format)
```

**Flags:**
- `-fields` - fields to include (default: all numeric)
- `-format` - long (field1, field2, corr) or wide (matrix)
- `-method` - pearson, spearman, kendall (default: pearson)

**Why GPU helps:** For 10 fields, that's 45 pairs. Each pair requires scanning all records. GPU can compute all pairs in parallel.

**Estimated speedup:** 10-30x for many fields

---

### 3. Histogram / Binning

Count values falling into bins. Useful for distribution analysis.

**CLI Interface:**
```bash
# Auto-detect bins (Sturges' rule)
ssql from data.csv | ssql histogram -field age

# Fixed number of bins
ssql from data.csv | ssql histogram -field price -bins 50

# Custom bin edges
ssql from data.csv | ssql histogram -field score -edges 0,50,70,85,100

# 2D histogram (heatmap)
ssql from data.csv | ssql histogram -x age -y income -bins 20
```

**Output:**
```
bin_start, bin_end, count, frequency
0, 10, 1523, 0.152
10, 20, 2847, 0.285
...
```

**Flags:**
- `-field` - field to bin (1D histogram)
- `-x`, `-y` - fields for 2D histogram
- `-bins` - number of bins (default: auto)
- `-edges` - explicit bin edges
- `-cumulative` - output cumulative distribution
- `-normalize` - output frequencies instead of counts

**Why GPU helps:** Atomic increment operations on GPU are fast. Can bin millions of values in parallel.

**Estimated speedup:** 5-15x for large datasets

---

## Tier 2: Valuable, Moderate Complexity

### 4. Bandpass Filter

Filter signals to specific frequency ranges. Common in signal processing.

**CLI Interface:**
```bash
# Lowpass filter (remove high frequencies)
ssql from signal.csv | ssql filter -field value -lowpass 100 -rate 1000

# Highpass filter (remove low frequencies)
ssql from signal.csv | ssql filter -field value -highpass 10 -rate 1000

# Bandpass (keep frequencies between bounds)
ssql from audio.arrow | ssql filter -field amplitude -bandpass 300 3000 -rate 44100

# Bandstop/notch (remove specific frequency range)
ssql from signal.csv | ssql filter -field value -bandstop 59 61 -rate 1000
```

**Flags:**
- `-field` - signal field (required)
- `-rate` - sample rate in Hz (required)
- `-lowpass FREQ` - keep frequencies below FREQ
- `-highpass FREQ` - keep frequencies above FREQ
- `-bandpass LOW HIGH` - keep frequencies between LOW and HIGH
- `-bandstop LOW HIGH` - remove frequencies between LOW and HIGH
- `-order` - filter order (default: 4)
- `-output` - output field name (default: field_filtered)

**Implementation:** FFT → multiply by frequency response → IFFT. All GPU-accelerated.

**Estimated speedup:** 15-40x (same as FFT)

---

### 5. Resampling

Change sample rate of signals. Essential for combining data from different sources.

**CLI Interface:**
```bash
# Upsample by factor
ssql from signal.csv | ssql resample -field value -factor 2

# Downsample by factor
ssql from audio.arrow | ssql resample -field amplitude -factor 0.5

# Resample to specific rate
ssql from signal.csv | ssql resample -field value -from-rate 1000 -to-rate 44100

# Resample to specific length
ssql from signal.csv | ssql resample -field value -length 10000
```

**Flags:**
- `-field` - signal field (required)
- `-factor` - resampling factor (>1 upsample, <1 downsample)
- `-from-rate`, `-to-rate` - convert between sample rates
- `-length` - target output length
- `-method` - linear, cubic, sinc (default: sinc for quality)
- `-output` - output field name

**Implementation:** Sinc interpolation via FFT (GPU) or polyphase filter.

**Estimated speedup:** 10-30x

---

### 6. Distance Matrix

Compute pairwise distances between records. Foundation for clustering and similarity.

**CLI Interface:**
```bash
# Euclidean distance between all records
ssql from points.csv | ssql distance-matrix -fields x y z

# Different metrics
ssql from data.csv | ssql distance-matrix -fields f1 f2 f3 -metric manhattan
ssql from geo.csv | ssql distance-matrix -lat latitude -lon longitude -metric haversine

# Output top-k nearest neighbors instead of full matrix
ssql from data.csv | ssql distance-matrix -fields x y -nearest 5
```

**Output (full matrix - long format):**
```
record1, record2, distance
0, 1, 3.14
0, 2, 5.67
...
```

**Output (nearest neighbors):**
```
record, neighbor, distance, rank
0, 5, 1.23, 1
0, 12, 2.34, 2
...
```

**Flags:**
- `-fields` - numeric fields to use (required, unless using lat/lon)
- `-lat`, `-lon` - latitude/longitude fields for geographic distance
- `-metric` - euclidean, manhattan, cosine, haversine (default: euclidean)
- `-nearest K` - only output K nearest neighbors per record
- `-threshold D` - only output pairs with distance < D

**Why GPU helps:** O(N²) comparisons, each independent. Perfect parallelism.

**Estimated speedup:** 20-100x for large datasets

---

### 7. K-Means Clustering

Partition data into K clusters. Common unsupervised learning task.

**CLI Interface:**
```bash
# Basic clustering
ssql from data.csv | ssql kmeans -fields x y -k 5

# With options
ssql from customers.csv | ssql kmeans \
  -fields age income score \
  -k 4 \
  -max-iter 100 \
  -output cluster

# Output includes cluster assignment and distance to centroid
```

**Output:**
```
# Original fields plus:
cluster, centroid_distance
2, 3.45
0, 1.23
...
```

**Flags:**
- `-fields` - numeric fields to cluster on (required)
- `-k` - number of clusters (required)
- `-max-iter` - maximum iterations (default: 100)
- `-init` - initialization method: random, kmeans++ (default: kmeans++)
- `-output` - cluster field name (default: cluster)
- `-seed` - random seed for reproducibility

**Why GPU helps:** Distance calculations and centroid updates are parallel. Each iteration processes all points.

**Estimated speedup:** 10-50x

---

## Tier 3: Specialized Use Cases

### 8. Percentiles / Quantiles

Compute percentiles efficiently for large datasets.

**CLI Interface:**
```bash
# Common percentiles
ssql from data.csv | ssql percentiles -field value -p 25 50 75 95 99

# Deciles
ssql from data.csv | ssql percentiles -field value -deciles

# Per-group percentiles
ssql from sales.csv | ssql percentiles -field revenue -p 50 90 -by region
```

**Why GPU helps:** Requires sorting. GPU radix sort is fast for large datasets.

**Estimated speedup:** 3-10x (sorting is memory-bound)

---

### 9. Rolling Statistics (GPU-accelerated windows)

Compute rolling statistics over large windows efficiently.

**CLI Interface:**
```bash
# Rolling mean with large window
ssql from signal.csv | ssql rolling -field value -window 10000 -stat mean

# Multiple statistics
ssql from data.csv | ssql rolling -field price -window 1000 -stat mean,std,min,max

# Exponential moving average
ssql from data.csv | ssql rolling -field price -ema -alpha 0.1
```

**Why GPU helps:** Large windows (>1000) benefit from parallel reduction.

**Estimated speedup:** 2-10x for large windows

---

### 10. Pattern Matching / Regex on Large Datasets

Match patterns across millions of strings.

**CLI Interface:**
```bash
# Count matches
ssql from logs.csv | ssql regex-count -field message -pattern 'ERROR|WARN'

# Extract matches
ssql from data.csv | ssql regex-extract -field text -pattern '\d{3}-\d{4}' -output phone

# Filter with GPU regex
ssql from logs.csv | ssql regex-filter -field message -pattern 'timeout.*retry'
```

**Why GPU helps:** Each string can be matched independently. GPU string processing libraries exist (cuDF).

**Estimated speedup:** 5-20x for large datasets

---

## Implementation Priority

Based on value, complexity, and fit with ssql's focus:

### Phase 1 (Immediate value)
1. **Spectrogram** - natural extension of FFT, high demand
2. **Histogram** - simple, broadly useful
3. **Bandpass filter** - completes signal processing toolkit

### Phase 2 (Data analysis)
4. **Correlation matrix** - common analysis task
5. **Distance matrix** - foundation for similarity/clustering
6. **K-means** - basic clustering

### Phase 3 (Specialized)
7. **Resampling** - signal processing
8. **Percentiles** - statistical analysis
9. **Rolling statistics** - time series
10. **Regex** - text processing

---

## Common Patterns

All GPU commands should follow existing patterns:

```bash
# Direct file input (fastest - bypasses Record conversion)
ssql spectrogram -file signal.arrow -field value -window 1024

# Pipeline input
ssql from signal.csv | ssql spectrogram -field value -window 1024

# Code generation
SSQLGO=1 ssql from signal.arrow | ssql spectrogram -field value | ssql generate go
```

**Automatic GPU selection:** Like FFT/convolution, use thresholds:
- Spectrogram: GPU when total FFTs > 100
- Histogram: GPU when records > 100K
- Distance matrix: GPU when records > 1K
- K-means: GPU when records × fields × k > 100K

---

## Questions to Resolve

1. **Output format for matrices** - long format (field1, field2, value) vs wide format (matrix)?
2. **Streaming vs batch** - some operations (k-means, percentiles) need all data. How to handle?
3. **Multi-GPU** - worth supporting for very large datasets?
4. **Memory limits** - what happens when data exceeds GPU memory? Automatic chunking?

---

## Next Steps

1. Choose 1-2 features from Phase 1 to prototype
2. Design detailed CLI interface
3. Implement CPU version first (for correctness testing)
4. Add GPU kernel
5. Add automatic CPU/GPU selection based on data size
6. Add code generation support
