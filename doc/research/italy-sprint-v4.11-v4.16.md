# Italy Sprint: v4.11.0 to v4.16.0

**Period:** Jan 31 - Feb 15, 2026 (while on holiday in Italy)
**Versions released:** v4.11.0, v4.12.0, v4.14.0, v4.15.0, v4.16.0

---

## Summary

Five releases in two weeks covering three major themes: signal processing, visualization, and format support. ssql went from a CSV/JSON/Arrow pipeline tool to one with full audio processing, interactive browser-based exploration, animated visualizations, and Excel support.

## Releases

### v4.11.0 - Spectrogram + GPU Batched FFT (Jan 27)

Full STFT (Short-Time Fourier Transform) implementation with GPU acceleration.

- **`spectrogram` command** - Window functions (Hann, Hamming, Blackman), configurable window size/hop, output as magnitude/power/dB
- **Batched GPU FFT** - `cufftPlanMany` processes all spectrogram frames in a single GPU call, amortizing PCIe transfer overhead
- **GPU speedup**: 7-10x over CPU (RTX 5090 vs Intel Core Ultra 9 275HX)

### v4.12.0 - WAV Audio Format (Feb 8)

Read and write WAV audio files directly in pipelines.

- **Read**: 8/16/24/32-bit PCM and IEEE float, mono/stereo, normalized to [-1.0, 1.0]
- **Write**: Configurable sample rate, bit depth, channels
- **Stereo**: `-channel 0|1` flag for channel extraction, default mixes to mono
- **Pipeline integration**: `ssql from audio.wav | ssql fft -field amplitude | ssql to chart`

### v4.14.0 - Visualization Suite (Feb 9)

Five visualization commands in one release, completing phases 1-3 of the visualization roadmap.

**Phase 1: Enhanced Charts**
- Multi-series Y-axis (`-y revenue -y expenses -y profit`)
- Heatmap chart type via Plotly.js
- Logarithmic axes (`-log-x`, `-log-y`)
- Color-by-field for scatter plots

**Phase 2: Interactive Data Explorer (`to explore`)**
- Self-contained HTML app with AG-Grid + Plotly.js + React
- Sortable/filterable data table, chart type switcher, aggregation UI
- CSV/PNG export, light/dark themes
- Code generation support

**Phase 3: Specialized Heatmap (`to heatmap`)**
- Optimized Plotly.js template for spectrogram visualization
- Logarithmic frequency axis for audio spectrograms
- Custom Z-axis range and color scale selection

**Bug fix**: `update` command now merges new fields into schema header (fields added by `-set` were silently dropped by downstream commands).

### v4.15.0 - Animated Visualization + WASM (Feb 11-12)

**Phase 4: Animated Visualization (`to animate`)**
- "MPEG for data" - heatmap or histogram evolving frame-by-frame
- Video-player controls: play/pause, step, scrubber, speed (0.25x-8x), loop
- Keyboard shortcuts, `Plotly.react()` for instant frame updates

**Phase 5: WASM In-Browser Transforms**
- WebAssembly module exposing Where, Sort, GroupBy, Distinct, Limit, Pipeline to JavaScript
- Explorer loads ssql.wasm (~3MB gzip) for client-side transforms
- Falls back to JS aggregation if WASM unavailable

### v4.16.0 - Excel Support + Release Tooling (Feb 15)

**Excel (XLSX) file support** using `github.com/xuri/excelize/v2`:
- `ssql from sales.xlsx -sheet "Q4 Results"` - read with optional sheet selection
- `ssql to xlsx output.xlsx -sheet Summary` - write with sheet naming
- Type inference, schema sharing, code generation support
- 9 unit tests + 2 generation tests

**Release tooling**:
- `make deb` target - builds both standard and GPU .deb packages from version.txt
- Dynamic completion hint - `ssql_gpu` now shows correct completion eval command

## Design Work

### Distributed SSH Processing

Research document (`doc/research/distributed-ssh-processing.md`) designing how ssql can process data where it lives via SSH:

- `ssh://` URL syntax for `from` and `join`
- `--remote` flag for pipeline push-down (filter/aggregate on server)
- GPU auto-detection on remote hosts
- Browser-initiated processing via `ssql serve` WebSocket relay
- 5 implementation phases from URL sugar to connection optimization

### AI Prompt Engineering

Systematic approach to making LLMs generate correct ssql code:

- **Ralph Wiggum Loop** - iterative prompt improvement using automated testing
- **30 test cases** (15 Go, 15 CLI) with pattern matching and integration testing
- **Multi-LLM validation** - both Claude and Gemini achieve 100% pass rate
- **Research paper** on LLM-guided API design (`doc/research/llm-guided-api-design.md`)
- **Key finding**: API design choices (SQL-style naming, type-safe encapsulation) directly affect LLM code generation quality

## Stats

| Metric | Value |
|--------|-------|
| Releases | 5 |
| New commands | 5 (spectrogram, to explore, to heatmap, to animate, to xlsx) |
| New format support | 2 (WAV, XLSX) |
| New tests | ~60 |
| Visualization phases completed | 5 of 6 |
| AI prompt test cases | 30 |
| LLM pass rate | 100% (Claude + Gemini) |
| Commits | ~50 |

## Format Support (as of v4.16.0)

| Format | Read | Write | Stdin | Code Gen |
|--------|------|-------|-------|----------|
| CSV | yes | yes | yes | yes |
| TSV | yes | yes | yes | yes |
| JSON/JSONL | yes | yes | yes | yes |
| Arrow | yes | yes | yes | yes |
| WAV | yes | yes | no | yes |
| XLSX | yes | yes | no | yes |

## Visualization Commands (as of v4.15.0)

| Command | Output | Technology |
|---------|--------|------------|
| `to chart` | Static/interactive chart | Chart.js + Plotly.js |
| `to heatmap` | Spectrogram/heatmap | Plotly.js |
| `to explore` | Interactive data app | React + AG-Grid + Plotly.js |
| `to animate` | Animated heatmap/histogram | Plotly.js with player controls |
