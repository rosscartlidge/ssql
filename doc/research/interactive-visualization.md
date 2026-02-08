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

### Approach 4: Hybrid (HTML + Optional Server)

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
- Add heatmap chart type to existing `to chart`
- Support Z-axis (color) field
- Multiple Y-series on same chart
- Logarithmic axis option

**Effort:** 1-2 days
**Output:** Same HTML format, more chart types

### Phase 2: Interactive Data Explorer (Standalone HTML)
- New command: `ssql to explore output.html`
- Embedded React app with:
  - Data table with sort/filter
  - Chart panel with field selection
  - Basic aggregation UI
- All in single HTML file

**Effort:** 3-5 days
**Output:** Single HTML file, ~500KB + data

### Phase 3: Spectrogram Visualization
- New command: `ssql to spectrogram output.html`
- Canvas-based heatmap rendering
- Color scale selection
- Zoom/pan interaction
- Cursor readout

**Effort:** 2-3 days
**Output:** Specialized HTML for spectrogram data

### Phase 4: Server Mode
- New command: `ssql serve -port 8080`
- REST API for data access
- WebSocket for streaming
- React app served from Go binary
- Handles unlimited data sizes

**Effort:** 5-7 days
**Output:** Long-running server process

### Phase 5: Full Analysis Workbench
- Pivot table functionality
- Multiple linked visualizations
- Dashboard layout
- Save/load analysis sessions
- Export to PDF/PNG

**Effort:** 5-10 days
**Output:** Full-featured analysis app

---

## Recommended Path

**For immediate value with minimal complexity:**

1. **Start with Phase 1** - Enhanced charts are quick wins
2. **Then Phase 3** - Spectrogram is high-value for audio work
3. **Then Phase 2** - Data explorer for general analysis
4. **Phase 4 later** - Only if data sizes require it

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
