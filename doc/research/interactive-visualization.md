# Interactive Visualization Research

## Current State

ssql currently generates static HTML charts using Chart.js:
- Line, bar, scatter, pie, radar charts
- Single X/Y field selection
- Basic zoom/pan via Chart.js plugins
- Export to PNG/CSV
- No server required - standalone HTML files

**Limitations:**
- Single chart type per output
- No dynamic data exploration (filtering, aggregation)
- No heatmaps or spectrograms (only scatter approximation)
- No multi-variable comparison
- Fixed at generation time - can't change fields without re-running

---

## Vision: Interactive Data Analysis App

Generate a self-contained React application that allows:

1. **Dynamic Data Exploration**
   - Filter/search records interactively
   - Sort by any column
   - Paginated data table view
   - Column visibility toggle

2. **Interactive Aggregations**
   - Group by any field(s)
   - Apply aggregations (sum, avg, count, min, max)
   - Pivot tables
   - Drill-down into groups

3. **Multi-Variable Charting**
   - Select X, Y, and optionally Z (color/size) variables
   - Multiple series on same chart
   - Switch chart types on the fly
   - Axis scaling (linear, log)

4. **Spectrogram/Heatmap Visualization**
   - Time × Frequency × Magnitude heatmaps
   - Color scale selection
   - Zoom into regions of interest
   - Cursor readout of values

5. **Real-Time/Streaming Updates**
   - WebSocket connection for live data
   - Scrolling time windows
   - Auto-refresh capability

---

## Technical Approaches

### Approach 1: Enhanced Standalone HTML (No Build Step)

**How it works:**
- Generate single HTML file with embedded React (via CDN)
- Data embedded as JSON in `<script>` tag
- All dependencies loaded from CDN (React, Recharts, AG-Grid, etc.)

**Pros:**
- No build step required
- Works offline (if CDN cached)
- Simple to generate
- Consistent with current approach

**Cons:**
- Large file sizes for big datasets
- Limited to ~10-50MB of data (browser memory)
- CDN dependency for first load
- Complex UI harder without build tooling

**Libraries:**
- React (via CDN): UI framework
- Recharts or Plotly.js: Charts including heatmaps
- AG-Grid (community): Data tables with sorting/filtering
- Simple CSS framework (Pico, Water.css)

**Example structure:**
```html
<!DOCTYPE html>
<html>
<head>
  <script src="https://unpkg.com/react@18/umd/react.production.min.js"></script>
  <script src="https://unpkg.com/react-dom@18/umd/react-dom.production.min.js"></script>
  <script src="https://unpkg.com/recharts/umd/Recharts.min.js"></script>
  <script src="https://unpkg.com/ag-grid-community/dist/ag-grid-community.min.js"></script>
</head>
<body>
  <div id="root"></div>
  <script>
    const DATA = [/* embedded JSON records */];
    const SCHEMA = {/* field names and types */};
    // React app code here
  </script>
</body>
</html>
```

---

### Approach 2: Generate React Project

**How it works:**
- Generate a complete React project directory
- User runs `npm install && npm start`
- Full development experience with hot reload

**Pros:**
- Full React ecosystem (TypeScript, testing, etc.)
- Better code organization
- Easier to customize/extend
- Can handle larger datasets via lazy loading

**Cons:**
- Requires Node.js installed
- Build step needed
- More complex output (directory vs single file)
- User must run npm commands

**Generated structure:**
```
output/
├── package.json
├── src/
│   ├── App.tsx
│   ├── components/
│   │   ├── DataTable.tsx
│   │   ├── ChartPanel.tsx
│   │   ├── FilterPanel.tsx
│   │   └── Spectrogram.tsx
│   └── data/
│       └── records.json
└── public/
    └── index.html
```

---

### Approach 3: Local Server Mode

**How it works:**
- `ssql serve` command starts local HTTP server
- Serves React app + provides data API
- Enables streaming/real-time updates

**Pros:**
- Handles unlimited data sizes (paginated API)
- Real-time streaming support
- Can run transformations server-side
- WebSocket for live updates

**Cons:**
- Requires running server process
- More complex implementation
- Not a "file" output anymore
- Security considerations for local server

**Architecture:**
```
┌─────────────┐     HTTP/WS      ┌─────────────┐
│   Browser   │ ◄──────────────► │  ssql serve │
│  React App  │                  │  (Go HTTP)  │
└─────────────┘                  └─────────────┘
                                       │
                                       ▼
                                 ┌─────────────┐
                                 │  Data File  │
                                 │  or Stream  │
                                 └─────────────┘
```

**API endpoints:**
- `GET /api/data?offset=0&limit=100` - Paginated data
- `GET /api/schema` - Field names and types
- `POST /api/aggregate` - Run aggregation
- `POST /api/filter` - Apply filter
- `WS /api/stream` - Real-time updates

---

### Approach 4: Go/WASM in Browser

**How it works:**
- Compile ssql to WebAssembly using Go's WASM target
- Load WASM module in browser
- Run ssql operations (filter, aggregate, FFT, etc.) client-side
- No server needed, even for complex transformations

**Pros:**
- Full ssql power in the browser
- No server required
- Works offline
- Same code for CLI and browser
- Can handle moderate data sizes (limited by browser memory)

**Cons:**
- WASM binary size (~15-30MB for full ssql)
- Initial load time
- Memory constraints (browser tab limit ~2-4GB)
- Some Go features don't work in WASM (e.g., file system)

**Architecture:**
```
┌─────────────────────────────────────────────────┐
│                    Browser                       │
│  ┌──────────────┐    ┌─────────────────────┐   │
│  │  React UI    │◄──►│  ssql.wasm          │   │
│  │  - Tables    │    │  - ReadCSV          │   │
│  │  - Charts    │    │  - Where/Filter     │   │
│  │  - Controls  │    │  - GroupBy/Agg      │   │
│  └──────────────┘    │  - FFT/Spectrogram  │   │
│                      │  - Join/Union       │   │
│                      └─────────────────────┘   │
└─────────────────────────────────────────────────┘
```

**JavaScript ↔ Go/WASM interface:**
```go
// Go side - exported to WASM
//go:export filterRecords
func filterRecords(jsonData string, field string, op string, value string) string {
    records := parseJSON(jsonData)
    filtered := ssql.Where(func(r ssql.Record) bool {
        return compare(ssql.GetOr(r, field, ""), op, value)
    })(records)
    return toJSON(filtered)
}

//go:export computeFFT
func computeFFT(jsonData string, field string, sampleRate int) string {
    records := parseJSON(jsonData)
    result := ssql.FFT(field, sampleRate)(records)
    return toJSON(result)
}
```

```javascript
// JavaScript side
const go = new Go();
const result = await WebAssembly.instantiateStreaming(
    fetch('ssql.wasm'), go.importObject
);
go.run(result.instance);

// Call ssql functions
const filtered = window.filterRecords(jsonData, "age", "gt", "18");
const spectrum = window.computeFFT(audioData, "amplitude", 44100);
```

**Build process:**
```bash
# Compile ssql to WASM
GOOS=js GOARCH=wasm go build -o ssql.wasm ./cmd/ssql-wasm

# Copy Go's WASM support file
cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" .
```

**WASM-specific ssql module:**
Would need a dedicated `cmd/ssql-wasm` that:
- Exports key functions for JS interop
- Removes file system dependencies
- Optimizes for code size (exclude unused commands)
- Uses `syscall/js` for browser integration

**Size optimization:**
```bash
# Full ssql: ~30MB WASM
# Optimized build with TinyGo: ~5-10MB
# With compression (gzip): ~3-5MB

# Build with TinyGo for smaller output
tinygo build -o ssql.wasm -target wasm ./cmd/ssql-wasm
```

**Performance considerations:**
- WASM is ~1.5-2x slower than native Go
- But no network latency (all client-side)
- For interactive use, likely fast enough
- FFT on 10K samples: ~50ms in WASM vs ~25ms native

---

### Approach 5: Hybrid (HTML + Optional Server)

**How it works:**
- Generate standalone HTML for small datasets
- HTML can optionally connect to `ssql serve` for large data
- Best of both worlds

**Pros:**
- Simple case stays simple (just open HTML)
- Scales to large datasets when needed
- Progressive enhancement

**Cons:**
- Two code paths to maintain
- Slightly more complex

---

## Visualization Components

### 1. Data Table Component

**Features:**
- Virtual scrolling for large datasets
- Column sorting (click header)
- Column filtering (text, numeric ranges)
- Column resizing and reordering
- Row selection
- Export selected rows

**Libraries:**
- AG-Grid Community (free, feature-rich)
- TanStack Table (headless, flexible)
- react-window + custom (lightweight)

### 2. Chart Panel Component

**Features:**
- Chart type selector (line, bar, scatter, area, pie)
- X/Y/Color/Size field dropdowns
- Multiple series support
- Axis configuration (linear, log, time)
- Zoom and pan
- Tooltips with full record data
- Export as PNG/SVG

**Libraries:**
- Recharts (React-native, good defaults)
- Plotly.js (feature-rich, heatmaps)
- ECharts (performant, many chart types)
- Nivo (beautiful, React-native)

### 3. Heatmap/Spectrogram Component

**Features:**
- 2D grid with color-coded values
- Color scale selection (viridis, plasma, etc.)
- Axis labels (time, frequency)
- Zoom into regions
- Cursor crosshairs with value readout
- Logarithmic color scale option

**Libraries:**
- Plotly.js `heatmap` trace (best option)
- D3.js custom (most flexible)
- ECharts heatmap
- react-heatmap-grid (simple)

**Data format for spectrogram:**
```json
{
  "times": [0, 0.1, 0.2, ...],
  "frequencies": [0, 100, 200, ...],
  "magnitudes": [[...], [...], ...]  // 2D array
}
```

### 4. Aggregation/Pivot Panel

**Features:**
- Drag-and-drop field selection
- Row grouping fields
- Column pivot fields
- Value aggregation (sum, avg, count, etc.)
- Hierarchical expansion
- Subtotals and grand totals

**Libraries:**
- AG-Grid pivot mode (built-in)
- react-pivottable (dedicated pivot)
- Custom with TanStack Table

### 5. Filter Panel

**Features:**
- Field type-aware filters
  - String: contains, equals, regex
  - Numeric: range slider, comparison
  - Boolean: checkbox
- Multiple filter combination (AND/OR)
- Save/load filter presets
- Quick search across all fields

---

## Spectrogram-Specific Requirements

For audio/signal visualization, spectrograms need special handling:

### Display Requirements
- Large data grids (e.g., 1000 time bins × 512 frequency bins = 512K points)
- Smooth color gradients
- Responsive to window resize
- Fast pan/zoom (WebGL acceleration)

### Interaction Requirements
- Click to play audio at that time position
- Selection rectangle for frequency/time range
- Frequency axis: linear or logarithmic (mel scale)
- Time axis: absolute or relative
- Colorbar with adjustable range

### Technical Options

**Option A: Canvas-based (recommended)**
```javascript
// Direct canvas rendering for performance
const canvas = document.getElementById('spectrogram');
const ctx = canvas.getContext('2d');
const imageData = ctx.createImageData(width, height);
// Fill pixels from magnitude data with colormap
ctx.putImageData(imageData, 0, 0);
```

**Option B: WebGL-based (for very large spectrograms)**
- Use Three.js or raw WebGL
- Texture-based rendering
- GPU colormap computation
- Handles millions of points

**Option C: Plotly.js heatmap**
- Easy to implement
- Good interactivity
- May lag with >100K points

### Audio Playback Integration
```javascript
// Click on spectrogram to play from that position
const audioContext = new AudioContext();
const audioBuffer = await audioContext.decodeAudioData(wavData);

canvas.onclick = (e) => {
  const time = (e.offsetX / canvas.width) * duration;
  const source = audioContext.createBufferSource();
  source.buffer = audioBuffer;
  source.connect(audioContext.destination);
  source.start(0, time);
};
```

---

## Implementation Phases

### Phase 1: Enhanced Chart Output

Extend `to chart` with heatmaps, multi-series, and better axis control.

**New commands:**
```bash
# Heatmap from any X/Y/Z data (color = Z value)
ssql from data.csv | ssql to chart -x time -y category -z value -type heatmap

# Multiple Y-series on same chart
ssql from data.csv | ssql to chart -x date -y revenue -y expenses -y profit

# Logarithmic axes
ssql from data.csv | ssql to chart -x frequency -y magnitude -log-y

# Color by third variable (scatter)
ssql from data.csv | ssql to chart -x age -y income -color region -type scatter
```

**Existing command (unchanged):**
```bash
ssql from data.csv | ssql to chart -x date -y sales -output sales.html
```

**Effort:** 1-2 days
**Output:** Same HTML format, more chart types

---

### Phase 2: Interactive Data Explorer

New `to explore` command generates a React-based data exploration app.

**New commands:**
```bash
# Generate interactive explorer (data table + charts)
ssql from sales.csv | ssql to explore output.html

# With initial chart configuration
ssql from sales.csv | ssql to explore -x date -y revenue output.html

# From any pipeline
ssql from logs.jsonl | ssql where -where level eq ERROR | ssql to explore errors.html
```

**What the generated HTML provides:**
- Sortable/filterable data table
- Field selector dropdowns for X/Y axes
- Chart type switcher (line, bar, scatter, pie)
- Basic aggregation UI (group by field, apply sum/avg/count)
- Export filtered data as CSV

**Example workflow:**
```bash
# Generate explorer
ssql from customers.csv | ssql to explore customers.html

# Open in browser - then interactively:
# 1. Filter table: status = "active"
# 2. Group by: region
# 3. Aggregate: count, avg(lifetime_value)
# 4. Chart: bar chart of count by region
```

**Effort:** 3-5 days
**Output:** Single HTML file (~500KB + data)

---

### Phase 3: Heatmap/Spectrogram Visualization

New `to heatmap` command for visualizing 2D data grids (spectrograms, matrices, etc.).

**New commands:**
```bash
# Spectrogram visualization (uses existing spectrogram command output)
ssql from audio.wav | \
  ssql spectrogram -field amplitude -rate 44100 | \
  ssql to heatmap -x time -y frequency -z magnitude output.html

# With options
ssql from audio.wav | \
  ssql spectrogram -field amplitude -rate 44100 -output db | \
  ssql to heatmap -x time -y frequency -z magnitude \
    -colorscale viridis \
    -zmin -80 -zmax 0 \
    output.html

# Any X/Y/Z data works (not just spectrograms)
ssql from matrix.csv | ssql to heatmap -x row -y col -z value output.html

# Correlation matrix
ssql from data.csv | ssql correlate-matrix | ssql to heatmap output.html
```

**Existing commands used as input:**
```bash
# spectrogram command already exists - outputs time, frequency, magnitude
ssql from audio.wav | ssql spectrogram -field amplitude -rate 44100 | ssql to table
# time        frequency   magnitude
# 0.023       0.000       0.001
# 0.023       43.066      0.234
# ...
```

**What the generated HTML provides:**
- Canvas-based heatmap (fast rendering for large grids)
- Color scale selector (viridis, plasma, inferno, etc.)
- Zoom and pan (mouse wheel, drag)
- Cursor crosshairs with value readout
- Adjustable color range (min/max sliders)
- Logarithmic frequency axis option (for audio)
- Export as PNG

**Effort:** 2-3 days
**Output:** Specialized HTML for heatmap visualization

---

### Phase 4: Server Mode

New `ssql serve` command for large datasets and real-time streaming.

**New commands:**
```bash
# Serve a data file (opens browser automatically)
ssql serve data.csv

# Specify port
ssql serve -port 8080 data.csv

# Serve from pipeline (buffers data)
ssql from logs.jsonl | ssql where -where level eq ERROR | ssql serve -port 3000

# Stream live data (WebSocket)
tail -f /var/log/app.log | ssql from jsonl | ssql serve -stream

# Multiple data sources
ssql serve -port 8080 \
  -data sales:sales.csv \
  -data products:products.csv \
  -data customers:customers.csv
```

**API endpoints provided:**
```bash
# REST API for data access
GET  /api/data?limit=100&offset=0     # Paginated data
GET  /api/schema                       # Field names and types
POST /api/query                        # Run ssql query on data
POST /api/aggregate                    # Group by + aggregate
WS   /ws/stream                        # WebSocket for live data
```

**Example workflow:**
```bash
# Terminal 1: Start server
ssql serve -port 8080 big_data.csv

# Terminal 2: Query via API
curl 'http://localhost:8080/api/data?limit=10'
curl -X POST 'http://localhost:8080/api/query' \
  -d '{"where": "age > 30", "fields": ["name", "age"]}'

# Or just open browser to http://localhost:8080 for UI
```

**Effort:** 5-7 days
**Output:** Long-running HTTP server with React UI

---

### Phase 5: Go/WASM Integration

Compile ssql to WebAssembly for in-browser transformations.

**New commands:**
```bash
# Generate explorer with WASM support (enables in-browser transformations)
ssql from data.csv | ssql to explore -wasm output.html

# Or explicitly include WASM module
ssql to explore -wasm -include-wasm output.html
```

**What WASM enables in the browser (no server needed):**
```javascript
// User clicks "Filter" button in the UI
const filtered = ssql.where(data, "age", "gt", "30");

// User selects aggregation
const grouped = ssql.groupBy(data, "region", {
  count: "count",
  total_sales: "sum(sales)"
});

// User runs FFT on selected column
const spectrum = ssql.fft(data, "amplitude", 44100);

// All runs client-side via WASM - no server round-trip
```

**Build outputs:**
```bash
# Standard explore (no WASM, ~500KB)
ssql from data.csv | ssql to explore output.html

# With WASM (~5MB, but enables transformations)
ssql from data.csv | ssql to explore -wasm output.html
```

**Effort:** 3-5 days
**Output:** ssql.wasm (~5-10MB) + JS bindings

---

### Phase 6: Full Analysis Workbench

Complete data analysis environment with dashboards and persistence.

**New commands:**
```bash
# Launch full workbench
ssql workbench data.csv

# With multiple data sources
ssql workbench -data sales:sales.csv -data products:products.csv

# Load saved analysis session
ssql workbench -session my_analysis.json

# Generate standalone dashboard from session
ssql workbench -session my_analysis.json -export dashboard.html
```

**Workbench features:**
- Pivot tables (drag-and-drop row/column/value fields)
- Multiple linked charts (click one to filter others)
- Dashboard layout (arrange charts in grid)
- Save/load analysis sessions
- Export to PDF/PNG
- WASM-powered transformations
- SQL query editor

**Example session file:**
```json
{
  "data_sources": ["sales.csv"],
  "views": [
    {
      "type": "pivot",
      "rows": ["region"],
      "columns": ["quarter"],
      "values": [{"field": "revenue", "agg": "sum"}]
    },
    {
      "type": "chart",
      "chart_type": "bar",
      "x": "region",
      "y": "revenue"
    }
  ],
  "filters": [
    {"field": "year", "op": "eq", "value": "2024"}
  ]
}
```

**Effort:** 5-10 days
**Output:** Full-featured analysis application

---

## Recommended Path

**For immediate value with minimal complexity:**

1. **Start with Phase 1** - Enhanced `to chart` with heatmaps and multi-series
   ```bash
   ssql from data.csv | ssql to chart -x time -y temp -y humidity
   ```

2. **Then Phase 3** - `to heatmap` for spectrogram visualization
   ```bash
   ssql from audio.wav | ssql spectrogram ... | ssql to heatmap output.html
   ```

3. **Then Phase 2** - `to explore` for interactive data analysis
   ```bash
   ssql from sales.csv | ssql to explore output.html
   ```

4. **Phase 5 (WASM)** - When users need in-browser transformations
   ```bash
   ssql from data.csv | ssql to explore -wasm output.html
   ```

5. **Phase 4 (Server)** - Only if data sizes exceed browser limits
   ```bash
   ssql serve big_data.csv  # Handles GB+ datasets
   ```

6. **Phase 6 (Workbench)** - Full analysis environment
   ```bash
   ssql workbench -data sales.csv -data products.csv
   ```

**Key decisions needed:**

1. **Single HTML vs React project?**
   - Recommend: Single HTML (simpler, consistent with current)

2. **Which chart library?**
   - Recommend: Plotly.js (heatmaps, 3D, good performance)

3. **Data table library?**
   - Recommend: AG-Grid Community (feature-rich, free)

4. **Build system?**
   - Recommend: None for Phase 1-3 (CDN + inline)
   - Esbuild for Phase 4+ (embedded in Go binary)

5. **Go/WASM for in-browser transformations?**
   - High potential - runs ssql operations without server
   - Consider for Phase 3+ when interactive filtering/aggregation needed
   - Could be optional: load WASM only if user wants to transform data

---

## Appendix: Library Comparison

### Chart Libraries

| Library | Heatmap | 3D | Size | React | License |
|---------|---------|-----|------|-------|---------|
| Plotly.js | ✅ | ✅ | 3MB | ✅ | MIT |
| ECharts | ✅ | ✅ | 1MB | ✅ | Apache |
| Recharts | ❌ | ❌ | 500KB | ✅ | MIT |
| Chart.js | ❌ | ❌ | 200KB | ✅ | MIT |
| Nivo | ✅ | ❌ | 800KB | ✅ | MIT |
| D3.js | Manual | Manual | 300KB | ❌ | ISC |

### Data Table Libraries

| Library | Virtual Scroll | Pivot | Filter | Size | License |
|---------|---------------|-------|--------|------|---------|
| AG-Grid Community | ✅ | ❌ | ✅ | 500KB | MIT |
| AG-Grid Enterprise | ✅ | ✅ | ✅ | 500KB | Commercial |
| TanStack Table | ✅ | Manual | Manual | 50KB | MIT |
| react-table | ✅ | ❌ | ✅ | 100KB | MIT |

---

## Questions to Resolve

1. What's the typical data size for interactive exploration?
   - <1MB: Standalone HTML easy
   - 1-100MB: Need virtual scrolling, maybe server
   - >100MB: Definitely need server mode

2. Is audio playback integration important for spectrogram?
   - If yes, need to include WAV data or audio element

3. Should the app be customizable after generation?
   - If yes, generate React project
   - If no, single HTML is simpler

4. Real-time streaming priority?
   - If high, need server mode sooner

5. Target users?
   - Developers: OK with npm commands
   - Analysts: Prefer just opening HTML file

6. Go/WASM priority?
   - Enables powerful client-side transformations
   - Tradeoff: larger initial download (~5-10MB)
   - Could lazy-load: basic view first, WASM on demand
