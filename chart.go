package ssql

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"iter"
	"maps"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// INTERACTIVE CHART.JS VISUALIZATION SINK
// ============================================================================

// ChartConfig configures the interactive chart generation.
// Provides comprehensive control over chart appearance, behavior, and export options.
//
// Use DefaultChartConfig() to get sensible defaults, then customize as needed.
type ChartConfig struct {
	Title              string            `json:"title"`
	Width              int               `json:"width"`
	Height             int               `json:"height"`
	ChartType          string            `json:"chartType"`  // line, bar, scatter, pie, doughnut, radar, polarArea, heatmap
	TimeFormat         string            `json:"timeFormat"` // For time-based X axis
	XAxisType          string            `json:"xAxisType"`  // linear, logarithmic, time, category
	YAxisType          string            `json:"yAxisType"`  // linear, logarithmic
	ShowLegend         bool              `json:"showLegend"`
	ShowTooltips       bool              `json:"showTooltips"`
	EnableZoom         bool              `json:"enableZoom"`
	EnablePan          bool              `json:"enablePan"`
	EnableAnimations   bool              `json:"enableAnimations"`
	ShowDataLabels     bool              `json:"showDataLabels"`
	EnableInteractive  bool              `json:"enableInteractive"`  // Field selection UI
	EnableCalculations bool              `json:"enableCalculations"` // Running averages, etc.
	ColorScheme        string            `json:"colorScheme"`        // default, vibrant, pastel, monochrome
	Theme              string            `json:"theme"`              // light, dark
	ExportFormats      []string          `json:"exportFormats"`      // png, svg, pdf, csv
	CustomCSS          string            `json:"customCSS"`
	Fields             map[string]string `json:"fields"` // field -> data type hints

	// Extended fields for advanced chart types
	XField     string   `json:"xField"`     // Explicit X-axis field
	YFields    []string `json:"yFields"`    // Multiple Y-axis fields for multi-series charts
	ZField     string   `json:"zField"`     // Z-axis field for heatmaps (color value)
	ColorField string   `json:"colorField"` // Field for point colors in scatter plots
	ColorScale string   `json:"colorScale"` // Color scale for heatmaps: viridis, plasma, inferno, magma
}

// DefaultChartConfig provides sensible defaults for interactive chart generation.
// Returns a ChartConfig with common settings that work well for most visualizations.
//
// Default settings include:
//   - Line chart with category X-axis
//   - Interactive features enabled (zoom, pan, field selection)
//   - Vibrant color scheme with light theme
//   - Export to PNG and CSV formats
//
// Example:
//
//	// Use defaults as-is
//	ssql.InteractiveChart(data, "chart.html")
//
//	// Customize from defaults
//	config := ssql.DefaultChartConfig()
//	config.Title = "Sales Dashboard"
//	config.ChartType = "bar"
//	config.Theme = "dark"
//	ssql.InteractiveChart(data, "chart.html", config)
func DefaultChartConfig() ChartConfig {
	return ChartConfig{
		Title:              "Data Visualization",
		Width:              1200,
		Height:             600,
		ChartType:          "line",
		XAxisType:          "category",
		YAxisType:          "linear",
		ShowLegend:         true,
		ShowTooltips:       true,
		EnableZoom:         true,
		EnablePan:          true,
		EnableAnimations:   true,
		ShowDataLabels:     false,
		EnableInteractive:  true,
		EnableCalculations: true,
		ColorScheme:        "vibrant",
		Theme:              "light",
		ExportFormats:      []string{"png", "csv"},
		Fields:             make(map[string]string),
		ColorScale:         "viridis",
	}
}

// ChartData represents the complete chart data structure
type ChartData struct {
	Records       []Record         `json:"records"`
	Fields        []string         `json:"fields"`
	NumericFields []string         `json:"numericFields"`
	DateFields    []string         `json:"dateFields"`
	Categories    map[string][]any `json:"categories"`
	Summary       ChartSummary     `json:"summary"`
}

// ChartSummary provides statistical summary of the data
type ChartSummary struct {
	RecordCount  int                    `json:"recordCount"`
	FieldTypes   map[string]string      `json:"fieldTypes"`
	NumericStats map[string]NumericStat `json:"numericStats"`
}

// NumericStat holds statistical information for numeric fields
type NumericStat struct {
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"stdDev"`
	Count  int     `json:"count"`
}

// InteractiveChart creates an HTML file with a fully interactive Chart.js visualization.
// Generates a complete web-based dashboard with field selection, zoom/pan, and export capabilities.
//
// Features:
//   - Interactive field selection UI
//   - Multiple chart types (line, bar, scatter, pie, etc.)
//   - Zoom and pan controls
//   - Statistical overlays (trend lines, moving averages)
//   - Export to PNG and CSV
//   - Automatic data type detection
//
// The generated HTML file is self-contained and can be opened in any modern browser.
//
// Example:
//
//	// Create interactive chart with default settings
//	sales, _ := ssql.ReadCSV("sales.csv")
//	ssql.InteractiveChart(sales, "sales_dashboard.html")
//
//	// Customize appearance and behavior
//	config := ssql.DefaultChartConfig()
//	config.Title = "Q4 Revenue Analysis"
//	config.ChartType = "bar"
//	config.Theme = "dark"
//	config.EnableCalculations = true  // Show trend lines and moving averages
//	ssql.InteractiveChart(sales, "dashboard.html", config)
//
//	// Time-based data with custom axis settings
//	config.XAxisType = "time"
//	config.TimeFormat = "YYYY-MM-DD"
//	ssql.InteractiveChart(timeSeries, "timeseries.html", config)
func InteractiveChart(sb iter.Seq[Record], filename string, config ...ChartConfig) error {
	cfg := DefaultChartConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	// Collect all records
	var records []Record
	for record := range sb {
		records = append(records, record)
	}
	if len(records) == 0 {
		return fmt.Errorf("no data to chart")
	}

	// Analyze data structure
	chartData := analyzeData(records, cfg)

	// Generate HTML with embedded Chart.js
	return generateInteractiveHTML(chartData, cfg, filename)
}

// QuickChart creates a simple chart with minimal configuration.
// The easiest way to create a visualization - just specify X and Y fields.
//
// Perfect for quick data exploration and prototyping. Uses sensible defaults
// for all chart settings.
//
// Example:
//
//	// One-line chart creation
//	sales, _ := ssql.ReadCSV("sales.csv")
//	ssql.QuickChart(sales, "month", "revenue", "revenue_chart.html")
//
//	// Visualize aggregated data
//	topRegions := ssql.Limit[ssql.Record](5)(
//	    ssql.SortBy(func(r ssql.Record) float64 {
//	        return -ssql.GetOr(r, "total_sales", 0.0)
//	    })(ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
//	        "total_sales": ssql.Sum("amount"),
//	    })(ssql.GroupByFields("sales", "region")(sales))))
//
//	ssql.QuickChart(topRegions, "region", "total_sales", "top_regions.html")
//
// The generated HTML file includes all interactive features (zoom, pan, export).
func QuickChart(sb iter.Seq[Record], xField, yField, filename string) error {
	cfg := DefaultChartConfig()
	cfg.Title = fmt.Sprintf("%s vs %s", yField, xField)

	var records []Record
	for record := range sb {
		records = append(records, record)
	}
	if len(records) == 0 {
		return fmt.Errorf("no data to chart")
	}

	// Create simple chart focusing on specified fields
	chartData := analyzeData(records, cfg)
	chartData.Fields = []string{xField, yField}

	return generateInteractiveHTML(chartData, cfg, filename)
}

// TimeSeriesChart creates a time-based chart optimized for temporal data.
// Automatically sorts data by time and configures Chart.js for time-series visualization.
//
// The time field should contain values that can be parsed as dates/times:
//   - time.Time objects
//   - RFC3339 strings ("2006-01-02T15:04:05Z")
//   - Common date formats ("2006-01-02", "01/02/2006", etc.)
//
// Example:
//
//	// Single metric over time
//	metrics, _ := ssql.ReadCSV("metrics.csv")
//	ssql.TimeSeriesChart(
//	    metrics,
//	    "timestamp",
//	    []string{"cpu_usage"},
//	    "cpu_chart.html",
//	)
//
//	// Multiple metrics on one chart
//	ssql.TimeSeriesChart(
//	    metrics,
//	    "timestamp",
//	    []string{"cpu_usage", "memory_usage", "disk_io"},
//	    "system_metrics.html",
//	)
//
//	// Customize time axis format
//	config := ssql.DefaultChartConfig()
//	config.TimeFormat = "YYYY-MM-DD HH:mm"
//	config.Title = "Hourly Sales Data"
//	ssql.TimeSeriesChart(
//	    sales,
//	    "timestamp",
//	    []string{"revenue", "orders"},
//	    "hourly_sales.html",
//	    config,
//	)
//
// The chart automatically includes zoom/pan for exploring time ranges.
func TimeSeriesChart(sb iter.Seq[Record], timeField string, valueFields []string, filename string, config ...ChartConfig) error {
	cfg := DefaultChartConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	cfg.ChartType = "line"
	cfg.XAxisType = "time"
	cfg.TimeFormat = "YYYY-MM-DD HH:mm:ss"

	var records []Record
	for record := range sb {
		records = append(records, record)
	}
	if len(records) == 0 {
		return fmt.Errorf("no data to chart")
	}

	// Sort records by time field
	sort.Slice(records, func(i, j int) bool {
		timeI := getFieldAsTime(records[i], timeField)
		timeJ := getFieldAsTime(records[j], timeField)
		return timeI.Before(timeJ)
	})

	chartData := analyzeData(records, cfg)
	return generateInteractiveHTML(chartData, cfg, filename)
}

// EnhancedChart creates an interactive chart with extended features including:
//   - Multiple Y-axis fields (multi-series)
//   - Heatmaps with Z-axis color values (uses Plotly.js)
//   - Logarithmic axes
//   - Color-by-field for scatter plots
//
// Example:
//
//	// Multi-series line chart
//	config := ssql.DefaultChartConfig()
//	config.XField = "date"
//	config.YFields = []string{"revenue", "expenses", "profit"}
//	ssql.EnhancedChart(data, config, "multi_series.html")
//
//	// Heatmap from spectrogram
//	config := ssql.DefaultChartConfig()
//	config.ChartType = "heatmap"
//	config.XField = "time"
//	config.YFields = []string{"frequency"}  // Y-axis for heatmap
//	config.ZField = "magnitude"              // Color values
//	config.ColorScale = "viridis"
//	ssql.EnhancedChart(spectrogramData, config, "spectrogram.html")
//
//	// Scatter plot with categorical coloring
//	config := ssql.DefaultChartConfig()
//	config.ChartType = "scatter"
//	config.XField = "age"
//	config.YFields = []string{"income"}
//	config.ColorField = "region"  // Color points by region
//	ssql.EnhancedChart(customerData, config, "customers.html")
func EnhancedChart(sb iter.Seq[Record], config ChartConfig, filename string) error {
	// Collect all records
	var records []Record
	for record := range sb {
		records = append(records, record)
	}
	if len(records) == 0 {
		return fmt.Errorf("no data to chart")
	}

	// For heatmaps, use Plotly.js
	if config.ChartType == "heatmap" {
		return generateHeatmapHTML(records, config, filename)
	}

	// For other chart types, use Chart.js with enhanced config
	chartData := analyzeData(records, config)

	// Override fields if explicitly specified
	if config.XField != "" {
		if len(config.YFields) > 0 {
			fields := []string{config.XField}
			fields = append(fields, config.YFields...)
			chartData.Fields = fields
		}
	}

	return generateEnhancedHTML(chartData, config, filename)
}

// HeatmapConfig configures specialized heatmap/spectrogram visualization.
// Provides fine-grained control over color scales, axis types, and value ranges.
type HeatmapConfig struct {
	Title      string  `json:"title"`
	XField     string  `json:"xField"`     // X-axis field (e.g., "time")
	YField     string  `json:"yField"`     // Y-axis field (e.g., "frequency")
	ZField     string  `json:"zField"`     // Color value field (e.g., "magnitude")
	ColorScale string  `json:"colorScale"` // viridis, plasma, inferno, magma, cividis, turbo
	ZMin       float64 `json:"zMin"`       // Minimum value for color scale (0 = auto)
	ZMax       float64 `json:"zMax"`       // Maximum value for color scale (0 = auto)
	LogFreq    bool    `json:"logFreq"`    // Use logarithmic Y-axis (for frequency)
	Theme      string  `json:"theme"`      // light or dark
	Width      int     `json:"width"`
	Height     int     `json:"height"`
}

// DefaultHeatmapConfig returns sensible defaults for heatmap visualization.
func DefaultHeatmapConfig() HeatmapConfig {
	return HeatmapConfig{
		Title:      "Heatmap",
		ColorScale: "viridis",
		ZMin:       0, // 0 = auto
		ZMax:       0, // 0 = auto
		LogFreq:    false,
		Theme:      "light",
		Width:      1200,
		Height:     600,
	}
}

// HeatmapChart creates a specialized heatmap visualization optimized for spectrograms.
// Provides features like adjustable color range, logarithmic frequency axis, and
// interactive cursor readout.
//
// Example - Spectrogram visualization:
//
//	config := ssql.DefaultHeatmapConfig()
//	config.XField = "time"
//	config.YField = "frequency"
//	config.ZField = "magnitude"
//	config.ColorScale = "viridis"
//	config.ZMin = -80  // dB scale
//	config.ZMax = 0
//	config.LogFreq = true  // Logarithmic frequency axis
//	ssql.HeatmapChart(spectrogramData, config, "spectrogram.html")
//
// Example - Correlation matrix:
//
//	config := ssql.DefaultHeatmapConfig()
//	config.XField = "var1"
//	config.YField = "var2"
//	config.ZField = "correlation"
//	config.ZMin = -1
//	config.ZMax = 1
//	config.ColorScale = "RdBu"
//	ssql.HeatmapChart(corrMatrix, config, "correlation.html")
func HeatmapChart(sb iter.Seq[Record], config HeatmapConfig, filename string) error {
	// Collect all records
	var records []Record
	for record := range sb {
		records = append(records, record)
	}
	if len(records) == 0 {
		return fmt.Errorf("no data to chart")
	}

	if config.XField == "" {
		return fmt.Errorf("X-axis field required (XField)")
	}
	if config.YField == "" {
		return fmt.Errorf("Y-axis field required (YField)")
	}
	if config.ZField == "" {
		return fmt.Errorf("Z-axis field required (ZField)")
	}

	return generateSpecializedHeatmapHTML(records, config, filename)
}

// generateSpecializedHeatmapHTML creates optimized heatmap HTML with advanced features
func generateSpecializedHeatmapHTML(records []Record, config HeatmapConfig, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", filename, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	xField := config.XField
	yField := config.YField
	zField := config.ZField

	// Extract unique X and Y values and build value grid
	xValues := make(map[string]int)
	yValues := make(map[string]int)
	var xLabels, yLabels []string
	var yNumeric []float64 // For log scale

	for _, record := range records {
		xVal := fmt.Sprintf("%v", GetOr(record, xField, ""))
		yVal := fmt.Sprintf("%v", GetOr(record, yField, ""))

		if _, exists := xValues[xVal]; !exists {
			xValues[xVal] = len(xLabels)
			xLabels = append(xLabels, xVal)
		}
		if _, exists := yValues[yVal]; !exists {
			yValues[yVal] = len(yLabels)
			yLabels = append(yLabels, yVal)
			// Try to parse Y as numeric for log scale
			if f := getNumericValue(GetOr(record, yField, 0.0)); !math.IsNaN(f) {
				yNumeric = append(yNumeric, f)
			}
		}
	}

	// Create 2D grid initialized with NaN
	grid := make([][]float64, len(yLabels))
	for i := range grid {
		grid[i] = make([]float64, len(xLabels))
		for j := range grid[i] {
			grid[i][j] = math.NaN()
		}
	}

	// Fill grid with Z values and track min/max
	zMin := math.Inf(1)
	zMax := math.Inf(-1)
	for _, record := range records {
		xVal := fmt.Sprintf("%v", GetOr(record, xField, ""))
		yVal := fmt.Sprintf("%v", GetOr(record, yField, ""))
		zVal := getNumericValue(GetOr(record, zField, 0.0))

		xi := xValues[xVal]
		yi := yValues[yVal]
		grid[yi][xi] = zVal

		if !math.IsNaN(zVal) {
			if zVal < zMin {
				zMin = zVal
			}
			if zVal > zMax {
				zMax = zVal
			}
		}
	}

	// Sort Y values numerically when all are numeric (required for log scale)
	if len(yNumeric) == len(yLabels) {
		// Build sort index
		indices := make([]int, len(yNumeric))
		for i := range indices {
			indices[i] = i
		}
		sort.Slice(indices, func(a, b int) bool {
			return yNumeric[indices[a]] < yNumeric[indices[b]]
		})

		// Reorder yNumeric, yLabels, and grid rows
		sortedNumeric := make([]float64, len(yNumeric))
		sortedLabels := make([]string, len(yLabels))
		sortedGrid := make([][]float64, len(grid))
		for newIdx, oldIdx := range indices {
			sortedNumeric[newIdx] = yNumeric[oldIdx]
			sortedLabels[newIdx] = yLabels[oldIdx]
			sortedGrid[newIdx] = grid[oldIdx]
		}
		yNumeric = sortedNumeric
		yLabels = sortedLabels
		grid = sortedGrid
	}

	// Use config zmin/zmax if set, otherwise use detected values
	if config.ZMin != 0 || config.ZMax != 0 {
		if config.ZMin != 0 {
			zMin = config.ZMin
		}
		if config.ZMax != 0 {
			zMax = config.ZMax
		}
	}

	// Convert grid to JSON-safe format (NaN -> null)
	gridJSON, err := json.Marshal(nanToNullGrid(grid))
	if err != nil {
		return fmt.Errorf("marshaling grid data: %w", err)
	}

	xLabelsJSON, err := json.Marshal(xLabels)
	if err != nil {
		return fmt.Errorf("marshaling x labels: %w", err)
	}

	// Use numeric Y values when all values are numeric (needed for log scale)
	var yLabelsJSON []byte
	if len(yNumeric) == len(yLabels) {
		yLabelsJSON, err = json.Marshal(yNumeric)
	} else {
		yLabelsJSON, err = json.Marshal(yLabels)
	}
	if err != nil {
		return fmt.Errorf("marshaling y labels: %w", err)
	}

	// Execute template
	tmpl := template.Must(template.New("spectrogram").Parse(spectrogramHTMLTemplate))
	zMinStr := fmt.Sprintf("%g", zMin)
	zMaxStr := fmt.Sprintf("%g", zMax)

	templateData := struct {
		Title      string
		XField     string
		YField     string
		ZField     string
		XLabels    template.JS
		YLabels    template.JS
		GridData   template.JS
		ColorScale string
		ZMin       string
		ZMax       string
		LogFreq    bool
		Theme      string
	}{
		Title:      config.Title,
		XField:     xField,
		YField:     yField,
		ZField:     zField,
		XLabels:    template.JS(xLabelsJSON),
		YLabels:    template.JS(yLabelsJSON),
		GridData:   template.JS(gridJSON),
		ColorScale: config.ColorScale,
		ZMin:       zMinStr,
		ZMax:       zMaxStr,
		LogFreq:    config.LogFreq,
		Theme:      config.Theme,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}

	return writer.Flush()
}

// AnimateConfig configures animated heatmap or histogram visualization.
// Records are grouped by a frame field and played back with video-player controls.
type AnimateConfig struct {
	Title      string `json:"title"`
	FrameField string `json:"frameField"` // field that partitions into frames
	XField     string `json:"xField"`
	YField     string `json:"yField"`
	ZField     string `json:"zField"`     // heatmap only
	ChartType  string `json:"chartType"`  // "heatmap" or "histogram"
	FPS        int    `json:"fps"`        // playback frames per second
	Loop       bool   `json:"loop"`       // loop playback
	ColorScale string `json:"colorScale"` // Plotly color scale (heatmap only)
	Theme      string `json:"theme"`      // light or dark
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// DefaultAnimateConfig returns sensible defaults for animated visualization.
func DefaultAnimateConfig() AnimateConfig {
	return AnimateConfig{
		Title:      "Animation",
		ChartType:  "heatmap",
		FPS:        5,
		Loop:       false,
		ColorScale: "viridis",
		Theme:      "light",
		Width:      1200,
		Height:     700,
	}
}

// AnimateChart creates an animated visualization where a heatmap or histogram
// evolves frame-by-frame with video-player controls (play/pause, scrub, speed).
//
// Example - Animated heatmap (spectrogram over segments):
//
//	config := ssql.DefaultAnimateConfig()
//	config.FrameField = "segment"
//	config.XField = "freq"
//	config.YField = "time"
//	config.ZField = "magnitude"
//	config.ChartType = "heatmap"
//	ssql.AnimateChart(data, config, "animation.html")
//
// Example - Animated histogram (distribution changing over time):
//
//	config := ssql.DefaultAnimateConfig()
//	config.FrameField = "year"
//	config.XField = "bin"
//	config.YField = "count"
//	config.ChartType = "histogram"
//	ssql.AnimateChart(data, config, "animation.html")
func AnimateChart(sb iter.Seq[Record], config AnimateConfig, filename string) error {
	var records []Record
	for record := range sb {
		records = append(records, record)
	}
	if len(records) == 0 {
		return fmt.Errorf("no data to animate")
	}

	if config.FrameField == "" {
		return fmt.Errorf("frame field required (FrameField)")
	}
	if config.XField == "" {
		return fmt.Errorf("X-axis field required (XField)")
	}
	if config.YField == "" {
		return fmt.Errorf("Y-axis field required (YField)")
	}
	if config.ChartType == "heatmap" && config.ZField == "" {
		return fmt.Errorf("Z-axis field required for heatmap (ZField)")
	}
	if config.ChartType == "" {
		config.ChartType = "heatmap"
	}
	if config.FPS <= 0 {
		config.FPS = 5
	}

	return generateAnimateHTML(records, config, filename)
}

// animateFrameGroup groups records belonging to one animation frame.
type animateFrameGroup struct {
	label   string
	records []Record
}

// generateAnimateHTML builds the animated HTML visualization
func generateAnimateHTML(records []Record, config AnimateConfig, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", filename, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Group records by frame, preserving insertion order
	frameOrder := make(map[string]int)
	var frames []animateFrameGroup

	for _, record := range records {
		label := fmt.Sprintf("%v", GetOr(record, config.FrameField, ""))
		if idx, exists := frameOrder[label]; exists {
			frames[idx].records = append(frames[idx].records, record)
		} else {
			frameOrder[label] = len(frames)
			frames = append(frames, animateFrameGroup{label: label, records: []Record{record}})
		}
	}

	if config.ChartType == "heatmap" {
		return generateAnimateHeatmapHTML(writer, frames, config)
	}
	return generateAnimateHistogramHTML(writer, frames, config)
}

// generateAnimateHeatmapHTML creates animated heatmap HTML
func generateAnimateHeatmapHTML(writer *bufio.Writer, frames []animateFrameGroup, config AnimateConfig) error {
	// Collect global X/Y label sets
	xSet := make(map[string]int)
	ySet := make(map[string]int)
	var xLabels, yLabels []string

	for _, frame := range frames {
		for _, record := range frame.records {
			xVal := fmt.Sprintf("%v", GetOr(record, config.XField, ""))
			yVal := fmt.Sprintf("%v", GetOr(record, config.YField, ""))
			if _, exists := xSet[xVal]; !exists {
				xSet[xVal] = len(xLabels)
				xLabels = append(xLabels, xVal)
			}
			if _, exists := ySet[yVal]; !exists {
				ySet[yVal] = len(yLabels)
				yLabels = append(yLabels, yVal)
			}
		}
	}

	// Build per-frame grids, track global zMin/zMax
	type heatmapFrame struct {
		Label string  `json:"label"`
		Grid  [][]any `json:"grid"`
	}
	globalZMin := math.Inf(1)
	globalZMax := math.Inf(-1)

	var jsonFrames []heatmapFrame
	for _, frame := range frames {
		grid := make([][]float64, len(yLabels))
		for i := range grid {
			grid[i] = make([]float64, len(xLabels))
			for j := range grid[i] {
				grid[i][j] = math.NaN()
			}
		}
		for _, record := range frame.records {
			xVal := fmt.Sprintf("%v", GetOr(record, config.XField, ""))
			yVal := fmt.Sprintf("%v", GetOr(record, config.YField, ""))
			zVal := getNumericValue(GetOr(record, config.ZField, 0.0))

			xi := xSet[xVal]
			yi := ySet[yVal]
			grid[yi][xi] = zVal

			if !math.IsNaN(zVal) {
				if zVal < globalZMin {
					globalZMin = zVal
				}
				if zVal > globalZMax {
					globalZMax = zVal
				}
			}
		}
		jsonFrames = append(jsonFrames, heatmapFrame{
			Label: frame.label,
			Grid:  nanToNullGrid(grid),
		})
	}

	if math.IsInf(globalZMin, 1) {
		globalZMin = 0
	}
	if math.IsInf(globalZMax, -1) {
		globalZMax = 1
	}

	framesJSON, err := json.Marshal(jsonFrames)
	if err != nil {
		return fmt.Errorf("marshaling frame data: %w", err)
	}
	xLabelsJSON, err := json.Marshal(xLabels)
	if err != nil {
		return fmt.Errorf("marshaling x labels: %w", err)
	}
	yLabelsJSON, err := json.Marshal(yLabels)
	if err != nil {
		return fmt.Errorf("marshaling y labels: %w", err)
	}

	tmpl := template.Must(template.New("animate").Parse(animateHTMLTemplate))
	templateData := struct {
		Title      string
		ChartType  string
		FrameField string
		XField     string
		YField     string
		ZField     string
		Frames     template.JS
		XLabels    template.JS
		YLabels    template.JS
		ZMin       float64
		ZMax       float64
		FPS        int
		Loop       bool
		ColorScale string
		Theme      string
	}{
		Title:      config.Title,
		ChartType:  "heatmap",
		FrameField: config.FrameField,
		XField:     config.XField,
		YField:     config.YField,
		ZField:     config.ZField,
		Frames:     template.JS(framesJSON),
		XLabels:    template.JS(xLabelsJSON),
		YLabels:    template.JS(yLabelsJSON),
		ZMin:       globalZMin,
		ZMax:       globalZMax,
		FPS:        config.FPS,
		Loop:       config.Loop,
		ColorScale: config.ColorScale,
		Theme:      config.Theme,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}
	return writer.Flush()
}

// generateAnimateHistogramHTML creates animated histogram HTML
func generateAnimateHistogramHTML(writer *bufio.Writer, frames []animateFrameGroup, config AnimateConfig) error {
	type histogramFrame struct {
		Label string    `json:"label"`
		X     []string  `json:"x"`
		Y     []float64 `json:"y"`
	}

	globalYMin := 0.0
	globalYMax := 0.0
	first := true
	var jsonFrames []histogramFrame
	for _, frame := range frames {
		var xs []string
		var ys []float64
		for _, record := range frame.records {
			xVal := fmt.Sprintf("%v", GetOr(record, config.XField, ""))
			yVal := getNumericValue(GetOr(record, config.YField, 0.0))
			if math.IsNaN(yVal) {
				yVal = 0
			}
			xs = append(xs, xVal)
			ys = append(ys, yVal)
			if first || yVal > globalYMax {
				globalYMax = yVal
			}
			if first || yVal < globalYMin {
				globalYMin = yVal
			}
			first = false
		}
		jsonFrames = append(jsonFrames, histogramFrame{
			Label: frame.label,
			X:     xs,
			Y:     ys,
		})
	}

	framesJSON, err := json.Marshal(jsonFrames)
	if err != nil {
		return fmt.Errorf("marshaling frame data: %w", err)
	}

	tmpl := template.Must(template.New("animate").Parse(animateHTMLTemplate))
	templateData := struct {
		Title      string
		ChartType  string
		FrameField string
		XField     string
		YField     string
		ZField     string
		Frames     template.JS
		XLabels    template.JS
		YLabels    template.JS
		ZMin       float64
		ZMax       float64
		FPS        int
		Loop       bool
		ColorScale string
		Theme      string
	}{
		Title:      config.Title,
		ChartType:  "histogram",
		FrameField: config.FrameField,
		XField:     config.XField,
		YField:     config.YField,
		ZField:     "",
		Frames:     template.JS(framesJSON),
		XLabels:    template.JS("[]"),
		YLabels:    template.JS("[]"),
		ZMin:       globalYMin,
		ZMax:       globalYMax,
		FPS:        config.FPS,
		Loop:       config.Loop,
		ColorScale: config.ColorScale,
		Theme:      config.Theme,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}
	return writer.Flush()
}

const animateHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
    <script src="https://cdn.plot.ly/plotly-2.27.0.min.js"></script>
    <style>
        body { margin: 0; padding: 0; font-family: system-ui, sans-serif; }
        #chart { width: 100%; height: calc(100vh - 80px); }
        .player-bar {
            position: fixed; bottom: 0; left: 0; right: 0;
            height: 80px; background: #1a1a2e; color: #fff;
            display: flex; align-items: center; padding: 0 20px; gap: 12px;
            box-shadow: 0 -2px 10px rgba(0,0,0,0.3); z-index: 1000;
        }
        .player-bar button {
            background: none; border: 1px solid #555; color: #fff;
            width: 36px; height: 36px; border-radius: 50%; cursor: pointer;
            display: flex; align-items: center; justify-content: center;
            font-size: 14px; transition: background 0.15s;
        }
        .player-bar button:hover { background: #333; }
        .player-bar button.play-btn { width: 44px; height: 44px; font-size: 18px; border-color: #7c3aed; }
        .player-bar button.play-btn:hover { background: #7c3aed; }
        .scrubber { flex: 1; }
        .scrubber input[type=range] {
            width: 100%; height: 6px; -webkit-appearance: none; appearance: none;
            background: #333; border-radius: 3px; outline: none;
        }
        .scrubber input[type=range]::-webkit-slider-thumb {
            -webkit-appearance: none; width: 16px; height: 16px;
            background: #7c3aed; border-radius: 50%; cursor: pointer;
        }
        .frame-info { font-size: 13px; white-space: nowrap; min-width: 160px; text-align: right; }
        .speed-select {
            background: #1a1a2e; color: #fff; border: 1px solid #555;
            border-radius: 4px; padding: 4px 8px; font-size: 13px; cursor: pointer;
        }
        .loop-btn.active { border-color: #7c3aed; color: #7c3aed; }
    </style>
</head>
<body>
    <div id="chart"></div>
    <div class="player-bar">
        <button id="btn-first" title="First frame (Home)">&#x23EE;</button>
        <button id="btn-prev" title="Previous frame (Left)">&#x23F4;</button>
        <button id="btn-play" class="play-btn" title="Play/Pause (Space)">&#x25B6;</button>
        <button id="btn-next" title="Next frame (Right)">&#x23F5;</button>
        <button id="btn-last" title="Last frame (End)">&#x23ED;</button>
        <div class="scrubber">
            <input type="range" id="scrubber" min="0" max="0" value="0">
        </div>
        <div class="frame-info" id="frame-info">Frame 1/1</div>
        <select class="speed-select" id="speed-select">
            <option value="0.25">0.25x</option>
            <option value="0.5">0.5x</option>
            <option value="1" selected>1x</option>
            <option value="2">2x</option>
            <option value="4">4x</option>
            <option value="8">8x</option>
        </select>
        <button id="btn-loop" class="loop-btn{{if .Loop}} active{{end}}" title="Toggle loop (L)">&#x1F501;</button>
    </div>
    <script>
        const CHART_TYPE = '{{.ChartType}}';
        const FRAMES = {{.Frames}};
        const X_LABELS = {{.XLabels}};
        const Y_LABELS = {{.YLabels}};
        const GLOBAL_ZMIN = {{.ZMin}};
        const GLOBAL_ZMAX = {{.ZMax}};
        const BASE_FPS = {{.FPS}};
        const COLOR_SCALE = '{{.ColorScale}}';
        const FRAME_FIELD = '{{.FrameField}}';
        const X_FIELD = '{{.XField}}';
        const Y_FIELD = '{{.YField}}';
        const Z_FIELD = '{{.ZField}}';

        let currentFrame = 0;
        let playing = false;
        let playInterval = null;
        let speedMultiplier = 1;
        let loopEnabled = {{if .Loop}}true{{else}}false{{end}};

        const chartDiv = document.getElementById('chart');
        const scrubber = document.getElementById('scrubber');
        const frameInfo = document.getElementById('frame-info');
        const btnPlay = document.getElementById('btn-play');
        const btnLoop = document.getElementById('btn-loop');

        scrubber.max = FRAMES.length - 1;

        function getTraceData(idx) {
            const frame = FRAMES[idx];
            if (CHART_TYPE === 'heatmap') {
                return [{
                    z: frame.grid,
                    x: X_LABELS,
                    y: Y_LABELS,
                    type: 'heatmap',
                    colorscale: COLOR_SCALE,
                    zmin: GLOBAL_ZMIN,
                    zmax: GLOBAL_ZMAX,
                    colorbar: { title: Z_FIELD }
                }];
            } else {
                return [{
                    x: frame.x,
                    y: frame.y,
                    type: 'bar',
                    marker: { color: '#7c3aed' }
                }];
            }
        }

        function getLayout(idx) {
            const frame = FRAMES[idx];
            const layout = {
                title: frame.label,
                margin: { t: 50, b: 60, l: 60, r: 30 },
                xaxis: { title: X_FIELD },
            };
            if (CHART_TYPE === 'heatmap') {
                layout.yaxis = { title: Y_FIELD };
            } else {
                const yPad = (GLOBAL_ZMAX - GLOBAL_ZMIN) * 0.05;
                layout.yaxis = { title: Y_FIELD, range: [GLOBAL_ZMIN - yPad, GLOBAL_ZMAX + yPad] };
            }
            return layout;
        }

        // Initial render
        Plotly.newPlot(chartDiv, getTraceData(0), getLayout(0), { responsive: true });

        function showFrame(idx) {
            if (idx < 0 || idx >= FRAMES.length) return;
            currentFrame = idx;
            Plotly.react(chartDiv, getTraceData(idx), getLayout(idx));
            scrubber.value = idx;
            frameInfo.textContent = 'Frame ' + (idx + 1) + '/' + FRAMES.length + ' (' + FRAMES[idx].label + ')';
        }

        function startPlayback() {
            if (playing) return;
            playing = true;
            btnPlay.innerHTML = '&#x23F8;';
            const interval = 1000 / (BASE_FPS * speedMultiplier);
            playInterval = setInterval(() => {
                let next = currentFrame + 1;
                if (next >= FRAMES.length) {
                    if (loopEnabled) { next = 0; } else { stopPlayback(); return; }
                }
                showFrame(next);
            }, interval);
        }

        function stopPlayback() {
            playing = false;
            btnPlay.innerHTML = '&#x25B6;';
            if (playInterval) { clearInterval(playInterval); playInterval = null; }
        }

        function restartPlayback() {
            if (playing) { stopPlayback(); startPlayback(); }
        }

        // Controls
        document.getElementById('btn-play').addEventListener('click', () => playing ? stopPlayback() : startPlayback());
        document.getElementById('btn-first').addEventListener('click', () => { stopPlayback(); showFrame(0); });
        document.getElementById('btn-last').addEventListener('click', () => { stopPlayback(); showFrame(FRAMES.length - 1); });
        document.getElementById('btn-prev').addEventListener('click', () => showFrame(Math.max(0, currentFrame - 1)));
        document.getElementById('btn-next').addEventListener('click', () => showFrame(Math.min(FRAMES.length - 1, currentFrame + 1)));
        scrubber.addEventListener('input', (e) => showFrame(parseInt(e.target.value)));
        document.getElementById('speed-select').addEventListener('change', (e) => {
            speedMultiplier = parseFloat(e.target.value);
            restartPlayback();
        });
        document.getElementById('btn-loop').addEventListener('click', () => {
            loopEnabled = !loopEnabled;
            btnLoop.classList.toggle('active', loopEnabled);
        });

        // Keyboard shortcuts
        document.addEventListener('keydown', (e) => {
            if (e.target.tagName === 'SELECT') return;
            switch(e.code) {
                case 'Space': e.preventDefault(); playing ? stopPlayback() : startPlayback(); break;
                case 'ArrowLeft': e.preventDefault(); showFrame(Math.max(0, currentFrame - 1)); break;
                case 'ArrowRight': e.preventDefault(); showFrame(Math.min(FRAMES.length - 1, currentFrame + 1)); break;
                case 'Home': e.preventDefault(); showFrame(0); break;
                case 'End': e.preventDefault(); showFrame(FRAMES.length - 1); break;
                case 'KeyL': loopEnabled = !loopEnabled; btnLoop.classList.toggle('active', loopEnabled); break;
            }
        });

        showFrame(0);
    </script>
</body>
</html>`

// generateHeatmapHTML creates an HTML file with Plotly.js heatmap visualization
func generateHeatmapHTML(records []Record, config ChartConfig, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", filename, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Pivot data into 2D grid
	xField := config.XField
	yField := ""
	if len(config.YFields) > 0 {
		yField = config.YFields[0]
	}
	zField := config.ZField

	if yField == "" {
		return fmt.Errorf("Y-axis field required for heatmap (use -y)")
	}
	if zField == "" {
		return fmt.Errorf("Z-axis field required for heatmap (use -z)")
	}

	// Extract unique X and Y values and build value grid
	xValues := make(map[string]int)
	yValues := make(map[string]int)
	var xLabels, yLabels []string

	for _, record := range records {
		xVal := fmt.Sprintf("%v", GetOr(record, xField, ""))
		yVal := fmt.Sprintf("%v", GetOr(record, yField, ""))

		if _, exists := xValues[xVal]; !exists {
			xValues[xVal] = len(xLabels)
			xLabels = append(xLabels, xVal)
		}
		if _, exists := yValues[yVal]; !exists {
			yValues[yVal] = len(yLabels)
			yLabels = append(yLabels, yVal)
		}
	}

	// Create 2D grid initialized with NaN
	grid := make([][]float64, len(yLabels))
	for i := range grid {
		grid[i] = make([]float64, len(xLabels))
		for j := range grid[i] {
			grid[i][j] = math.NaN()
		}
	}

	// Fill grid with Z values
	for _, record := range records {
		xVal := fmt.Sprintf("%v", GetOr(record, xField, ""))
		yVal := fmt.Sprintf("%v", GetOr(record, yField, ""))
		zVal := getNumericValue(GetOr(record, zField, 0.0))

		xi := xValues[xVal]
		yi := yValues[yVal]
		grid[yi][xi] = zVal
	}

	// Convert grid to JSON-safe format (NaN -> null)
	gridJSON, err := json.Marshal(nanToNullGrid(grid))
	if err != nil {
		return fmt.Errorf("marshaling grid data: %w", err)
	}

	xLabelsJSON, err := json.Marshal(xLabels)
	if err != nil {
		return fmt.Errorf("marshaling x labels: %w", err)
	}

	yLabelsJSON, err := json.Marshal(yLabels)
	if err != nil {
		return fmt.Errorf("marshaling y labels: %w", err)
	}

	// Execute template
	tmpl := template.Must(template.New("heatmap").Parse(heatmapHTMLTemplate))
	templateData := struct {
		Title      string
		XField     string
		YField     string
		ZField     string
		XLabels    template.JS
		YLabels    template.JS
		GridData   template.JS
		ColorScale string
		Theme      string
	}{
		Title:      config.Title,
		XField:     xField,
		YField:     yField,
		ZField:     zField,
		XLabels:    template.JS(xLabelsJSON),
		YLabels:    template.JS(yLabelsJSON),
		GridData:   template.JS(gridJSON),
		ColorScale: config.ColorScale,
		Theme:      config.Theme,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}

	return writer.Flush()
}

// generateEnhancedHTML creates Chart.js HTML with extended features
func generateEnhancedHTML(data ChartData, config ChartConfig, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", filename, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Convert data to JSON
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling data: %w", err)
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Execute template
	tmpl := template.Must(template.New("chart").Parse(enhancedChartHTMLTemplate))
	templateData := struct {
		Title      string
		DataJSON   template.JS
		ConfigJSON template.JS
		Theme      string
		CustomCSS  string
	}{
		Title:      config.Title,
		DataJSON:   template.JS(dataJSON),
		ConfigJSON: template.JS(configJSON),
		Theme:      config.Theme,
		CustomCSS:  config.CustomCSS,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}

	return writer.Flush()
}

// ============================================================================
// DATA ANALYSIS
// ============================================================================

// analyzeData examines the records to understand data types and structure
func analyzeData(records []Record, _ ChartConfig) ChartData {
	if len(records) == 0 {
		return ChartData{}
	}

	// Collect all field names
	fieldSet := make(map[string]bool)
	for _, record := range records {
		for field := range record.All() {
			if !strings.HasPrefix(field, "_") { // Skip metadata fields
				fieldSet[field] = true
			}
		}
	}

	fields := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	// Analyze field types
	numericFields := []string{}
	dateFields := []string{}
	categories := make(map[string][]any)
	fieldTypes := make(map[string]string)
	numericStats := make(map[string]NumericStat)

	for _, field := range fields {
		values := extractFieldValues(records, field)
		fieldType, isNumeric, isDate := analyzeFieldType(values)
		fieldTypes[field] = fieldType

		if isNumeric {
			numericFields = append(numericFields, field)
			numericStats[field] = calculateNumericStats(values)
		}
		if isDate {
			dateFields = append(dateFields, field)
		}

		// Collect unique values for categorical fields (up to 50 values)
		if !isNumeric && !isDate {
			uniqueValues := getUniqueValues(values, 50)
			categories[field] = uniqueValues
		}
	}

	return ChartData{
		Records:       records,
		Fields:        fields,
		NumericFields: numericFields,
		DateFields:    dateFields,
		Categories:    categories,
		Summary: ChartSummary{
			RecordCount:  len(records),
			FieldTypes:   fieldTypes,
			NumericStats: numericStats,
		},
	}
}

// extractFieldValues gets all values for a specific field
func extractFieldValues(records []Record, field string) []any {
	values := make([]any, 0, len(records))
	for _, record := range records {
		if value, exists := Get[any](record, field); exists {
			values = append(values, value)
		}
	}
	return values
}

// analyzeFieldType determines the primary type of a field
func analyzeFieldType(values []any) (string, bool, bool) {
	if len(values) == 0 {
		return "string", false, false
	}

	numericCount := 0
	dateCount := 0

	for _, value := range values {
		if value == nil {
			continue
		}

		// Check if numeric
		if isNumericValue(value) {
			numericCount++
			continue
		}

		// Check if date/time
		if isDateValue(value) {
			dateCount++
			continue
		}
	}

	totalCount := len(values)
	numericRatio := float64(numericCount) / float64(totalCount)
	dateRatio := float64(dateCount) / float64(totalCount)

	// Field is considered numeric if >80% of values are numeric
	if numericRatio > 0.8 {
		return "numeric", true, false
	}

	// Field is considered date if >80% of values are dates
	if dateRatio > 0.8 {
		return "date", false, true
	}

	return "string", false, false
}

// isNumericValue checks if a value can be treated as numeric
func isNumericValue(value any) bool {
	switch v := value.(type) {
	case int, int32, int64, float32, float64:
		return true
	case string:
		_, err := strconv.ParseFloat(v, 64)
		return err == nil
	default:
		return false
	}
}

// isDateValue checks if a value can be treated as a date
func isDateValue(value any) bool {
	switch v := value.(type) {
	case time.Time:
		return true
	case string:
		// Try common date formats
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
			"15:04:05",
			"01/02/2006",
			"02/01/2006",
		}
		for _, format := range formats {
			if _, err := time.Parse(format, v); err == nil {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// calculateNumericStats computes statistical summary for numeric values
func calculateNumericStats(values []any) NumericStat {
	var nums []float64
	for _, value := range values {
		if num := getNumericValue(value); !math.IsNaN(num) {
			nums = append(nums, num)
		}
	}

	if len(nums) == 0 {
		return NumericStat{}
	}

	// Calculate basic statistics
	min := nums[0]
	max := nums[0]
	sum := 0.0

	for _, num := range nums {
		if num < min {
			min = num
		}
		if num > max {
			max = num
		}
		sum += num
	}

	mean := sum / float64(len(nums))

	// Calculate standard deviation
	variance := 0.0
	for _, num := range nums {
		variance += math.Pow(num-mean, 2)
	}
	stdDev := math.Sqrt(variance / float64(len(nums)))

	return NumericStat{
		Min:    min,
		Max:    max,
		Mean:   mean,
		StdDev: stdDev,
		Count:  len(nums),
	}
}

// nanToNullGrid converts a 2D float64 grid with NaN values to a grid where NaN is represented as nil
// This is necessary because JSON doesn't support NaN, but the JavaScript side handles null values
func nanToNullGrid(grid [][]float64) [][]any {
	result := make([][]any, len(grid))
	for i, row := range grid {
		result[i] = make([]any, len(row))
		for j, val := range row {
			if math.IsNaN(val) {
				result[i][j] = nil
			} else {
				result[i][j] = val
			}
		}
	}
	return result
}

// getNumericValue safely converts any value to float64
func getNumericValue(value any) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case float32:
		return float64(v)
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return math.NaN()
}

// getFieldAsTime safely converts a field value to time.Time
func getFieldAsTime(record Record, field string) time.Time {
	value, exists := Get[any](record, field)
	if !exists {
		return time.Time{}
	}

	switch v := value.(type) {
	case time.Time:
		return v
	case string:
		// Try common formats
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
		}
		for _, format := range formats {
			if t, err := time.Parse(format, v); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// getUniqueValues returns unique values up to a limit
func getUniqueValues(values []any, limit int) []any {
	seen := make(map[string]bool)
	unique := []any{}

	for _, value := range values {
		if value == nil {
			continue
		}

		key := fmt.Sprintf("%v", value)
		if !seen[key] && len(unique) < limit {
			seen[key] = true
			unique = append(unique, value)
		}
	}

	return unique
}

// ============================================================================
// HTML GENERATION
// ============================================================================

// generateInteractiveHTML creates the complete HTML file with Chart.js
func generateInteractiveHTML(data ChartData, config ChartConfig, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", filename, err)
	}
	defer file.Close()

	// Wrap file in buffered writer for better performance with large HTML files
	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Convert data to JSON
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling data: %w", err)
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Execute template
	tmpl := template.Must(template.New("chart").Parse(chartHTMLTemplate))
	templateData := struct {
		Title      string
		DataJSON   template.JS
		ConfigJSON template.JS
		Theme      string
		CustomCSS  string
	}{
		Title:      config.Title,
		DataJSON:   template.JS(dataJSON),
		ConfigJSON: template.JS(configJSON),
		Theme:      config.Theme,
		CustomCSS:  config.CustomCSS,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}

	return writer.Flush()
}

// chartHTMLTemplate is the HTML template for the interactive chart
const chartHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>

    <!-- Chart.js and plugins -->
    <script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-plugin-zoom@2.0.1/dist/chartjs-plugin-zoom.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-plugin-annotation@3.0.1/dist/chartjs-plugin-annotation.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/date-fns@2.29.3/index.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-adapter-date-fns@3.0.0/dist/chartjs-adapter-date-fns.bundle.min.js"></script>

    <!-- UI Framework -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/js/bootstrap.bundle.min.js"></script>

    <!-- Icons -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.10.0/font/bootstrap-icons.css" rel="stylesheet">

    <style>
        :root {
            --bg-color: {{if eq .Theme "dark"}}#1a1a1a{{else}}#ffffff{{end}};
            --text-color: {{if eq .Theme "dark"}}#ffffff{{else}}#333333{{end}};
            --border-color: {{if eq .Theme "dark"}}#444444{{else}}#dee2e6{{end}};
            --panel-bg: {{if eq .Theme "dark"}}#2d2d2d{{else}}#f8f9fa{{end}};
        }

        body {
            background-color: var(--bg-color);
            color: var(--text-color);
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
        }

        .control-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 20px;
            margin-bottom: 20px;
        }

        .chart-container {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 20px;
            position: relative;
        }

        .stats-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 15px;
            margin-top: 20px;
        }

        .field-selector {
            max-height: 200px;
            overflow-y: auto;
            border: 1px solid var(--border-color);
            border-radius: 4px;
            padding: 10px;
        }

        .export-buttons {
            position: absolute;
            top: 10px;
            right: 10px;
            z-index: 1000;
        }

        .btn-outline-primary {
            color: var(--text-color);
            border-color: var(--border-color);
        }

        .btn-outline-primary:hover {
            background-color: #0d6efd;
            border-color: #0d6efd;
            color: white;
        }

        {{.CustomCSS}}
    </style>
</head>
<body>
    <div class="container-fluid">
        <!-- Header -->
        <div class="row mt-3">
            <div class="col-12">
                <h1 class="text-center">{{.Title}}</h1>
            </div>
        </div>

        <!-- Controls -->
        <div class="row">
            <div class="col-12">
                <div class="control-panel">
                    <div class="row g-3">
                        <!-- Chart Type -->
                        <div class="col-md-2">
                            <label class="form-label">Chart Type</label>
                            <select id="chartType" class="form-select">
                                <option value="line">Line</option>
                                <option value="bar">Bar</option>
                                <option value="scatter">Scatter</option>
                                <option value="pie">Pie</option>
                                <option value="doughnut">Doughnut</option>
                                <option value="radar">Radar</option>
                            </select>
                        </div>

                        <!-- X Field -->
                        <div class="col-md-2">
                            <label class="form-label">X-Axis Field</label>
                            <select id="xField" class="form-select">
                            </select>
                        </div>

                        <!-- Y Fields -->
                        <div class="col-md-3">
                            <label class="form-label">Y-Axis Fields</label>
                            <div id="yFields" class="field-selector">
                            </div>
                        </div>

                        <!-- Options -->
                        <div class="col-md-2">
                            <label class="form-label">Options</label>
                            <div class="form-check">
                                <input class="form-check-input" type="checkbox" id="showTrendLine">
                                <label class="form-check-label" for="showTrendLine">Trend Line</label>
                            </div>
                            <div class="form-check">
                                <input class="form-check-input" type="checkbox" id="showMovingAvg">
                                <label class="form-check-label" for="showMovingAvg">Moving Average</label>
                            </div>
                            <div class="form-check">
                                <input class="form-check-input" type="checkbox" id="stackedMode">
                                <label class="form-check-label" for="stackedMode">Stacked</label>
                            </div>
                        </div>

                        <!-- Actions -->
                        <div class="col-md-3">
                            <label class="form-label">Actions</label>
                            <div class="btn-group d-block" role="group">
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="updateChart()">
                                    <i class="bi bi-arrow-clockwise"></i> Update
                                </button>
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="resetZoom()">
                                    <i class="bi bi-zoom-out"></i> Reset Zoom
                                </button>
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="exportChart()">
                                    <i class="bi bi-download"></i> Export
                                </button>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <!-- Chart -->
        <div class="row">
            <div class="col-12">
                <div class="chart-container">
                    <canvas id="mainChart"></canvas>
                </div>
            </div>
        </div>

        <!-- Statistics -->
        <div class="row">
            <div class="col-12">
                <div class="stats-panel">
                    <h5>Data Summary</h5>
                    <div id="dataSummary" class="row">
                    </div>
                </div>
            </div>
        </div>
    </div>

    <script>
        // Global variables
        let chartData = {{.DataJSON}};
        let chartConfig = {{.ConfigJSON}};
        let mainChart = null;

        // Initialize the application
        document.addEventListener('DOMContentLoaded', function() {
            initializeControls();
            createChart();
            updateDataSummary();
        });

        // Initialize form controls
        function initializeControls() {
            // Populate field selectors
            const xFieldSelect = document.getElementById('xField');
            const yFieldsDiv = document.getElementById('yFields');

            chartData.fields.forEach(field => {
                // X field options
                const option = document.createElement('option');
                option.value = field;
                option.textContent = field;
                xFieldSelect.appendChild(option);

                // Y field checkboxes
                const checkDiv = document.createElement('div');
                checkDiv.className = 'form-check';
                checkDiv.innerHTML = ` + "`" + `
                    <input class="form-check-input" type="checkbox" id="y_${field}" value="${field}">
                    <label class="form-check-label" for="y_${field}">${field}</label>
                ` + "`" + `;
                yFieldsDiv.appendChild(checkDiv);
            });

            // Set initial values
            if (chartData.fields.length > 0) {
                xFieldSelect.value = chartData.fields[0];
            }

            // Auto-select numeric fields for Y axis
            chartData.numericFields.forEach(field => {
                const checkbox = document.getElementById(` + "`" + `y_${field}` + "`" + `);
                if (checkbox) {
                    checkbox.checked = true;
                }
            });

            // Set chart type
            document.getElementById('chartType').value = chartConfig.chartType;
        }

        // Create or update the chart
        function createChart() {
            const ctx = document.getElementById('mainChart').getContext('2d');

            if (mainChart) {
                mainChart.destroy();
            }

            const chartType = document.getElementById('chartType').value;
            const xField = document.getElementById('xField').value;
            const selectedYFields = getSelectedYFields();
            const showTrendLine = document.getElementById('showTrendLine').checked;
            const showMovingAvg = document.getElementById('showMovingAvg').checked;
            const stackedMode = document.getElementById('stackedMode').checked;

            // Prepare data
            const labels = chartData.records.map(record => record[xField]);
            const datasets = [];

            // Generate colors
            const colors = generateColors(selectedYFields.length);

            selectedYFields.forEach((field, index) => {
                const data = chartData.records.map(record => {
                    const value = record[field];
                    return typeof value === 'number' ? value : parseFloat(value) || 0;
                });

                datasets.push({
                    label: field,
                    data: data,
                    backgroundColor: colors[index] + '80', // Add transparency
                    borderColor: colors[index],
                    borderWidth: 2,
                    fill: chartType === 'area',
                    tension: 0.4
                });

                // Add moving average if requested
                if (showMovingAvg && data.length > 5) {
                    const movingAvgData = calculateMovingAverage(data, 5);
                    datasets.push({
                        label: ` + "`" + `${field} (5-period MA)` + "`" + `,
                        data: movingAvgData,
                        backgroundColor: 'transparent',
                        borderColor: colors[index],
                        borderWidth: 1,
                        borderDash: [5, 5],
                        fill: false,
                        tension: 0.1,
                        pointRadius: 0
                    });
                }
            });

            // Chart configuration
            const config = {
                type: chartType,
                data: {
                    labels: labels,
                    datasets: datasets
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    scales: {
                        x: {
                            type: chartData.dateFields.includes(xField) ? 'time' : 'category',
                            title: {
                                display: true,
                                text: xField
                            }
                        },
                        y: {
                            stacked: stackedMode,
                            title: {
                                display: true,
                                text: 'Values'
                            }
                        }
                    },
                    plugins: {
                        legend: {
                            display: chartConfig.showLegend
                        },
                        tooltip: {
                            enabled: chartConfig.showTooltips,
                            mode: 'index',
                            intersect: false
                        },
                        zoom: {
                            zoom: {
                                wheel: {
                                    enabled: chartConfig.enableZoom
                                },
                                pinch: {
                                    enabled: chartConfig.enableZoom
                                },
                                mode: 'x'
                            },
                            pan: {
                                enabled: chartConfig.enablePan,
                                mode: 'x'
                            }
                        }
                    },
                    animation: {
                        duration: chartConfig.enableAnimations ? 1000 : 0
                    }
                }
            };

            mainChart = new Chart(ctx, config);
        }

        // Helper functions
        function getSelectedYFields() {
            const checkboxes = document.querySelectorAll('#yFields input[type="checkbox"]:checked');
            return Array.from(checkboxes).map(cb => cb.value);
        }

        function generateColors(count) {
            const colors = [
                '#FF6384', '#36A2EB', '#FFCE56', '#4BC0C0', '#9966FF',
                '#FF9F40', '#FF6384', '#C9CBCF', '#4BC0C0', '#36A2EB'
            ];

            const result = [];
            for (let i = 0; i < count; i++) {
                result.push(colors[i % colors.length]);
            }
            return result;
        }

        function calculateMovingAverage(data, period) {
            const result = [];
            for (let i = 0; i < data.length; i++) {
                if (i < period - 1) {
                    result.push(null);
                } else {
                    const sum = data.slice(i - period + 1, i + 1).reduce((a, b) => a + b, 0);
                    result.push(sum / period);
                }
            }
            return result;
        }

        function updateChart() {
            createChart();
        }

        function resetZoom() {
            if (mainChart) {
                mainChart.resetZoom();
            }
        }

        function exportChart() {
            if (mainChart) {
                const url = mainChart.toBase64Image();
                const link = document.createElement('a');
                link.download = 'chart.png';
                link.href = url;
                link.click();
            }
        }

        function updateDataSummary() {
            const summaryDiv = document.getElementById('dataSummary');
            let html = ` + "`" + `
                <div class="col-md-3">
                    <strong>Records:</strong> ${chartData.summary.recordCount}
                </div>
                <div class="col-md-3">
                    <strong>Fields:</strong> ${chartData.fields.length}
                </div>
                <div class="col-md-3">
                    <strong>Numeric Fields:</strong> ${chartData.numericFields.length}
                </div>
                <div class="col-md-3">
                    <strong>Date Fields:</strong> ${chartData.dateFields.length}
                </div>
            ` + "`" + `;

            // Add numeric statistics
            for (const [field, stats] of Object.entries(chartData.summary.numericStats)) {
                html += ` + "`" + `
                    <div class="col-md-12 mt-2">
                        <small><strong>${field}:</strong>
                        Min: ${stats.min.toFixed(2)},
                        Max: ${stats.max.toFixed(2)},
                        Mean: ${stats.mean.toFixed(2)},
                        StdDev: ${stats.stdDev.toFixed(2)}
                        </small>
                    </div>
                ` + "`" + `;
            }

            summaryDiv.innerHTML = html;
        }
    </script>
</body>
</html>`

// heatmapHTMLTemplate is the HTML template for Plotly.js heatmap visualization
const heatmapHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>

    <!-- Plotly.js for heatmaps -->
    <script src="https://cdn.plot.ly/plotly-2.27.0.min.js"></script>

    <!-- UI Framework -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/js/bootstrap.bundle.min.js"></script>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.10.0/font/bootstrap-icons.css" rel="stylesheet">

    <style>
        :root {
            --bg-color: {{if eq .Theme "dark"}}#1a1a1a{{else}}#ffffff{{end}};
            --text-color: {{if eq .Theme "dark"}}#ffffff{{else}}#333333{{end}};
            --border-color: {{if eq .Theme "dark"}}#444444{{else}}#dee2e6{{end}};
            --panel-bg: {{if eq .Theme "dark"}}#2d2d2d{{else}}#f8f9fa{{end}};
        }

        body {
            background-color: var(--bg-color);
            color: var(--text-color);
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
        }

        .control-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 20px;
            margin-bottom: 20px;
        }

        .chart-container {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 20px;
        }

        .stats-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 15px;
            margin-top: 20px;
        }

        #heatmapChart {
            width: 100%;
            height: 600px;
        }
    </style>
</head>
<body>
    <div class="container-fluid">
        <div class="row mt-3">
            <div class="col-12">
                <h1 class="text-center">{{.Title}}</h1>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="control-panel">
                    <div class="row g-3">
                        <div class="col-md-3">
                            <label class="form-label">Color Scale</label>
                            <select id="colorScale" class="form-select" onchange="updateColorScale()">
                                <option value="Viridis" {{if eq .ColorScale "viridis"}}selected{{end}}>Viridis</option>
                                <option value="Plasma" {{if eq .ColorScale "plasma"}}selected{{end}}>Plasma</option>
                                <option value="Inferno" {{if eq .ColorScale "inferno"}}selected{{end}}>Inferno</option>
                                <option value="Magma" {{if eq .ColorScale "magma"}}selected{{end}}>Magma</option>
                                <option value="Cividis" {{if eq .ColorScale "cividis"}}selected{{end}}>Cividis</option>
                                <option value="Turbo" {{if eq .ColorScale "turbo"}}selected{{end}}>Turbo</option>
                                <option value="Hot">Hot</option>
                                <option value="Blues">Blues</option>
                                <option value="RdBu">Red-Blue</option>
                            </select>
                        </div>
                        <div class="col-md-3">
                            <label class="form-label">Actions</label>
                            <div class="btn-group d-block" role="group">
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="resetView()">
                                    <i class="bi bi-zoom-out"></i> Reset Zoom
                                </button>
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="exportChart()">
                                    <i class="bi bi-download"></i> Export PNG
                                </button>
                            </div>
                        </div>
                        <div class="col-md-6">
                            <label class="form-label">Cursor Position</label>
                            <div id="cursorInfo" class="form-control-plaintext">Hover over heatmap to see values</div>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="chart-container">
                    <div id="heatmapChart"></div>
                </div>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="stats-panel">
                    <h5>Data Summary</h5>
                    <div class="row">
                        <div class="col-md-4">
                            <strong>X-Axis ({{.XField}}):</strong> <span id="xCount"></span> values
                        </div>
                        <div class="col-md-4">
                            <strong>Y-Axis ({{.YField}}):</strong> <span id="yCount"></span> values
                        </div>
                        <div class="col-md-4">
                            <strong>Z-Values ({{.ZField}}):</strong> Min: <span id="zMin"></span>, Max: <span id="zMax"></span>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <script>
        const xLabels = {{.XLabels}};
        const yLabels = {{.YLabels}};
        const gridData = {{.GridData}};

        // Calculate Z-axis statistics
        let zMin = Infinity, zMax = -Infinity;
        for (const row of gridData) {
            for (const val of row) {
                if (!isNaN(val) && val !== null) {
                    if (val < zMin) zMin = val;
                    if (val > zMax) zMax = val;
                }
            }
        }

        document.getElementById('xCount').textContent = xLabels.length;
        document.getElementById('yCount').textContent = yLabels.length;
        document.getElementById('zMin').textContent = zMin.toFixed(4);
        document.getElementById('zMax').textContent = zMax.toFixed(4);

        const trace = {
            z: gridData,
            x: xLabels,
            y: yLabels,
            type: 'heatmap',
            colorscale: '{{.ColorScale | js}}' || 'Viridis',
            colorbar: {
                title: '{{.ZField}}'
            },
            hoverongaps: false,
            hovertemplate: '{{.XField}}: %{x}<br>{{.YField}}: %{y}<br>{{.ZField}}: %{z:.4f}<extra></extra>'
        };

        const layout = {
            title: '',
            xaxis: {
                title: '{{.XField}}',
                tickangle: -45
            },
            yaxis: {
                title: '{{.YField}}'
            },
            margin: {
                l: 80,
                r: 50,
                t: 30,
                b: 100
            }
        };

        const config = {
            responsive: true,
            scrollZoom: true,
            modeBarButtonsToRemove: ['lasso2d', 'select2d'],
            displaylogo: false
        };

        Plotly.newPlot('heatmapChart', [trace], layout, config);

        // Update cursor info on hover
        document.getElementById('heatmapChart').on('plotly_hover', function(data) {
            const pt = data.points[0];
            document.getElementById('cursorInfo').innerHTML =
                ` + "`" + `<strong>{{.XField}}:</strong> ${pt.x} | <strong>{{.YField}}:</strong> ${pt.y} | <strong>{{.ZField}}:</strong> ${pt.z?.toFixed(4) || 'N/A'}` + "`" + `;
        });

        function updateColorScale() {
            const colorScale = document.getElementById('colorScale').value;
            Plotly.restyle('heatmapChart', {colorscale: colorScale});
        }

        function resetView() {
            Plotly.relayout('heatmapChart', {
                'xaxis.autorange': true,
                'yaxis.autorange': true
            });
        }

        function exportChart() {
            Plotly.downloadImage('heatmapChart', {
                format: 'png',
                width: 1920,
                height: 1080,
                filename: 'heatmap'
            });
        }
    </script>
</body>
</html>`

// enhancedChartHTMLTemplate is the Chart.js template with multi-series and advanced features
const enhancedChartHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>

    <!-- Chart.js and plugins -->
    <script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-plugin-zoom@2.0.1/dist/chartjs-plugin-zoom.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-plugin-annotation@3.0.1/dist/chartjs-plugin-annotation.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/date-fns@2.29.3/index.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-adapter-date-fns@3.0.0/dist/chartjs-adapter-date-fns.bundle.min.js"></script>

    <!-- UI Framework -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/js/bootstrap.bundle.min.js"></script>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.10.0/font/bootstrap-icons.css" rel="stylesheet">

    <style>
        :root {
            --bg-color: {{if eq .Theme "dark"}}#1a1a1a{{else}}#ffffff{{end}};
            --text-color: {{if eq .Theme "dark"}}#ffffff{{else}}#333333{{end}};
            --border-color: {{if eq .Theme "dark"}}#444444{{else}}#dee2e6{{end}};
            --panel-bg: {{if eq .Theme "dark"}}#2d2d2d{{else}}#f8f9fa{{end}};
        }

        body {
            background-color: var(--bg-color);
            color: var(--text-color);
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
        }

        .control-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 20px;
            margin-bottom: 20px;
        }

        .chart-container {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 20px;
            position: relative;
            height: 600px;
        }

        .stats-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 15px;
            margin-top: 20px;
        }

        .field-selector {
            max-height: 200px;
            overflow-y: auto;
            border: 1px solid var(--border-color);
            border-radius: 4px;
            padding: 10px;
        }

        .btn-outline-primary {
            color: var(--text-color);
            border-color: var(--border-color);
        }

        .btn-outline-primary:hover {
            background-color: #0d6efd;
            border-color: #0d6efd;
            color: white;
        }

        {{.CustomCSS}}
    </style>
</head>
<body>
    <div class="container-fluid">
        <div class="row mt-3">
            <div class="col-12">
                <h1 class="text-center">{{.Title}}</h1>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="control-panel">
                    <div class="row g-3">
                        <div class="col-md-2">
                            <label class="form-label">Chart Type</label>
                            <select id="chartType" class="form-select">
                                <option value="line">Line</option>
                                <option value="bar">Bar</option>
                                <option value="scatter">Scatter</option>
                                <option value="pie">Pie</option>
                                <option value="doughnut">Doughnut</option>
                                <option value="radar">Radar</option>
                            </select>
                        </div>

                        <div class="col-md-2">
                            <label class="form-label">X-Axis Field</label>
                            <select id="xField" class="form-select"></select>
                        </div>

                        <div class="col-md-3">
                            <label class="form-label">Y-Axis Fields</label>
                            <div id="yFields" class="field-selector"></div>
                        </div>

                        <div class="col-md-2">
                            <label class="form-label">Options</label>
                            <div class="form-check">
                                <input class="form-check-input" type="checkbox" id="showTrendLine">
                                <label class="form-check-label" for="showTrendLine">Trend Line</label>
                            </div>
                            <div class="form-check">
                                <input class="form-check-input" type="checkbox" id="showMovingAvg">
                                <label class="form-check-label" for="showMovingAvg">Moving Average</label>
                            </div>
                            <div class="form-check">
                                <input class="form-check-input" type="checkbox" id="stackedMode">
                                <label class="form-check-label" for="stackedMode">Stacked</label>
                            </div>
                        </div>

                        <div class="col-md-3">
                            <label class="form-label">Actions</label>
                            <div class="btn-group d-block" role="group">
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="updateChart()">
                                    <i class="bi bi-arrow-clockwise"></i> Update
                                </button>
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="resetZoom()">
                                    <i class="bi bi-zoom-out"></i> Reset Zoom
                                </button>
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="exportChart()">
                                    <i class="bi bi-download"></i> Export
                                </button>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="chart-container">
                    <canvas id="mainChart"></canvas>
                </div>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="stats-panel">
                    <h5>Data Summary</h5>
                    <div id="dataSummary" class="row"></div>
                </div>
            </div>
        </div>
    </div>

    <script>
        let chartData = {{.DataJSON}};
        let chartConfig = {{.ConfigJSON}};
        let mainChart = null;

        document.addEventListener('DOMContentLoaded', function() {
            initializeControls();
            createChart();
            updateDataSummary();
        });

        function initializeControls() {
            const xFieldSelect = document.getElementById('xField');
            const yFieldsDiv = document.getElementById('yFields');

            chartData.fields.forEach(field => {
                const option = document.createElement('option');
                option.value = field;
                option.textContent = field;
                xFieldSelect.appendChild(option);

                const checkDiv = document.createElement('div');
                checkDiv.className = 'form-check';
                checkDiv.innerHTML = ` + "`" + `
                    <input class="form-check-input" type="checkbox" id="y_${field}" value="${field}">
                    <label class="form-check-label" for="y_${field}">${field}</label>
                ` + "`" + `;
                yFieldsDiv.appendChild(checkDiv);
            });

            // Set initial X field from config
            if (chartConfig.xField) {
                xFieldSelect.value = chartConfig.xField;
            } else if (chartData.fields.length > 0) {
                xFieldSelect.value = chartData.fields[0];
            }

            // Set initial Y fields from config or auto-select numeric fields
            if (chartConfig.yFields && chartConfig.yFields.length > 0) {
                chartConfig.yFields.forEach(field => {
                    const checkbox = document.getElementById(` + "`" + `y_${field}` + "`" + `);
                    if (checkbox) checkbox.checked = true;
                });
            } else {
                chartData.numericFields.forEach(field => {
                    const checkbox = document.getElementById(` + "`" + `y_${field}` + "`" + `);
                    if (checkbox) checkbox.checked = true;
                });
            }

            document.getElementById('chartType').value = chartConfig.chartType;
        }

        function createChart() {
            const ctx = document.getElementById('mainChart').getContext('2d');

            if (mainChart) {
                mainChart.destroy();
            }

            const chartType = document.getElementById('chartType').value;
            const xField = document.getElementById('xField').value;
            const selectedYFields = getSelectedYFields();
            const showTrendLine = document.getElementById('showTrendLine').checked;
            const showMovingAvg = document.getElementById('showMovingAvg').checked;
            const stackedMode = document.getElementById('stackedMode').checked;

            const labels = chartData.records.map(record => record[xField]);
            const datasets = [];
            const colors = generateColors(selectedYFields.length);

            // Check if we have a color field for scatter plots
            const colorField = chartConfig.colorField;
            const hasColorField = colorField && chartType === 'scatter';

            if (hasColorField) {
                // Group data by color field value
                const groups = {};
                chartData.records.forEach(record => {
                    const groupKey = record[colorField] || 'Other';
                    if (!groups[groupKey]) groups[groupKey] = [];
                    groups[groupKey].push(record);
                });

                const groupKeys = Object.keys(groups);
                const groupColors = generateColors(groupKeys.length);

                groupKeys.forEach((groupKey, index) => {
                    const groupRecords = groups[groupKey];
                    selectedYFields.forEach(yField => {
                        const data = groupRecords.map(record => ({
                            x: getNumericValue(record[xField]),
                            y: getNumericValue(record[yField])
                        }));

                        datasets.push({
                            label: ` + "`" + `${yField} (${groupKey})` + "`" + `,
                            data: data,
                            backgroundColor: groupColors[index] + '80',
                            borderColor: groupColors[index],
                            borderWidth: 1,
                            pointRadius: 4
                        });
                    });
                });
            } else {
                selectedYFields.forEach((field, index) => {
                    const data = chartData.records.map(record => {
                        const value = record[field];
                        return typeof value === 'number' ? value : parseFloat(value) || 0;
                    });

                    datasets.push({
                        label: field,
                        data: data,
                        backgroundColor: colors[index] + '80',
                        borderColor: colors[index],
                        borderWidth: 2,
                        fill: chartType === 'area',
                        tension: 0.4
                    });

                    if (showMovingAvg && data.length > 5) {
                        const movingAvgData = calculateMovingAverage(data, 5);
                        datasets.push({
                            label: ` + "`" + `${field} (5-period MA)` + "`" + `,
                            data: movingAvgData,
                            backgroundColor: 'transparent',
                            borderColor: colors[index],
                            borderWidth: 1,
                            borderDash: [5, 5],
                            fill: false,
                            tension: 0.1,
                            pointRadius: 0
                        });
                    }
                });
            }

            // Determine scale types
            const xAxisType = chartConfig.xAxisType === 'logarithmic' ? 'logarithmic' :
                              (chartData.dateFields.includes(xField) ? 'time' : 'category');
            const yAxisType = chartConfig.yAxisType === 'logarithmic' ? 'logarithmic' : 'linear';

            const config = {
                type: chartType,
                data: {
                    labels: labels,
                    datasets: datasets
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    scales: {
                        x: {
                            type: xAxisType,
                            title: {
                                display: true,
                                text: xField
                            }
                        },
                        y: {
                            type: yAxisType,
                            stacked: stackedMode,
                            title: {
                                display: true,
                                text: 'Values'
                            }
                        }
                    },
                    plugins: {
                        legend: {
                            display: chartConfig.showLegend
                        },
                        tooltip: {
                            enabled: chartConfig.showTooltips,
                            mode: 'index',
                            intersect: false
                        },
                        zoom: {
                            zoom: {
                                wheel: { enabled: chartConfig.enableZoom },
                                pinch: { enabled: chartConfig.enableZoom },
                                mode: 'xy'
                            },
                            pan: {
                                enabled: chartConfig.enablePan,
                                mode: 'xy'
                            }
                        }
                    },
                    animation: {
                        duration: chartConfig.enableAnimations ? 1000 : 0
                    }
                }
            };

            mainChart = new Chart(ctx, config);
        }

        function getSelectedYFields() {
            const checkboxes = document.querySelectorAll('#yFields input[type="checkbox"]:checked');
            return Array.from(checkboxes).map(cb => cb.value);
        }

        function generateColors(count) {
            const colors = [
                '#FF6384', '#36A2EB', '#FFCE56', '#4BC0C0', '#9966FF',
                '#FF9F40', '#FF6384', '#C9CBCF', '#4BC0C0', '#36A2EB'
            ];
            const result = [];
            for (let i = 0; i < count; i++) {
                result.push(colors[i % colors.length]);
            }
            return result;
        }

        function getNumericValue(value) {
            if (typeof value === 'number') return value;
            const parsed = parseFloat(value);
            return isNaN(parsed) ? 0 : parsed;
        }

        function calculateMovingAverage(data, period) {
            const result = [];
            for (let i = 0; i < data.length; i++) {
                if (i < period - 1) {
                    result.push(null);
                } else {
                    const sum = data.slice(i - period + 1, i + 1).reduce((a, b) => a + b, 0);
                    result.push(sum / period);
                }
            }
            return result;
        }

        function updateChart() {
            createChart();
        }

        function resetZoom() {
            if (mainChart) {
                mainChart.resetZoom();
            }
        }

        function exportChart() {
            if (mainChart) {
                const url = mainChart.toBase64Image();
                const link = document.createElement('a');
                link.download = 'chart.png';
                link.href = url;
                link.click();
            }
        }

        function updateDataSummary() {
            const summaryDiv = document.getElementById('dataSummary');
            let html = ` + "`" + `
                <div class="col-md-3">
                    <strong>Records:</strong> ${chartData.summary.recordCount}
                </div>
                <div class="col-md-3">
                    <strong>Fields:</strong> ${chartData.fields.length}
                </div>
                <div class="col-md-3">
                    <strong>Numeric Fields:</strong> ${chartData.numericFields.length}
                </div>
                <div class="col-md-3">
                    <strong>Date Fields:</strong> ${chartData.dateFields.length}
                </div>
            ` + "`" + `;

            for (const [field, stats] of Object.entries(chartData.summary.numericStats)) {
                html += ` + "`" + `
                    <div class="col-md-12 mt-2">
                        <small><strong>${field}:</strong>
                        Min: ${stats.min.toFixed(2)},
                        Max: ${stats.max.toFixed(2)},
                        Mean: ${stats.mean.toFixed(2)},
                        StdDev: ${stats.stdDev.toFixed(2)}
                        </small>
                    </div>
                ` + "`" + `;
            }

            summaryDiv.innerHTML = html;
        }
    </script>
</body>
</html>`

// spectrogramHTMLTemplate is specialized for spectrogram/heatmap with advanced controls
const spectrogramHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>

    <!-- Plotly.js for heatmaps -->
    <script src="https://cdn.plot.ly/plotly-2.27.0.min.js"></script>

    <!-- UI Framework -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/js/bootstrap.bundle.min.js"></script>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.10.0/font/bootstrap-icons.css" rel="stylesheet">

    <style>
        :root {
            --bg-color: {{if eq .Theme "dark"}}#1a1a1a{{else}}#ffffff{{end}};
            --text-color: {{if eq .Theme "dark"}}#ffffff{{else}}#333333{{end}};
            --border-color: {{if eq .Theme "dark"}}#444444{{else}}#dee2e6{{end}};
            --panel-bg: {{if eq .Theme "dark"}}#2d2d2d{{else}}#f8f9fa{{end}};
        }

        body {
            background-color: var(--bg-color);
            color: var(--text-color);
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
            margin: 0;
            padding: 0;
        }

        .control-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 15px;
            margin-bottom: 15px;
        }

        .chart-container {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 10px;
        }

        #heatmapChart {
            width: 100%;
            height: 500px;
        }

        .stats-panel {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 15px;
            margin-top: 15px;
        }

        .cursor-info {
            font-family: monospace;
            padding: 8px 12px;
            background-color: var(--bg-color);
            border: 1px solid var(--border-color);
            border-radius: 4px;
            min-height: 40px;
        }

        .range-slider {
            width: 100%;
        }

        .slider-value {
            font-family: monospace;
            font-size: 0.9em;
        }
    </style>
</head>
<body>
    <div class="container-fluid p-3">
        <div class="row mb-2">
            <div class="col-12">
                <h4 class="text-center mb-0">{{.Title}}</h4>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="control-panel">
                    <div class="row g-3 align-items-end">
                        <!-- Color Scale -->
                        <div class="col-md-2">
                            <label class="form-label mb-1">Color Scale</label>
                            <select id="colorScale" class="form-select form-select-sm" onchange="updateColorScale()">
                                <option value="Viridis" {{if eq .ColorScale "viridis"}}selected{{end}}>Viridis</option>
                                <option value="Plasma" {{if eq .ColorScale "plasma"}}selected{{end}}>Plasma</option>
                                <option value="Inferno" {{if eq .ColorScale "inferno"}}selected{{end}}>Inferno</option>
                                <option value="Magma" {{if eq .ColorScale "magma"}}selected{{end}}>Magma</option>
                                <option value="Cividis" {{if eq .ColorScale "cividis"}}selected{{end}}>Cividis</option>
                                <option value="Turbo" {{if eq .ColorScale "turbo"}}selected{{end}}>Turbo</option>
                                <option value="Hot">Hot</option>
                                <option value="Blues">Blues</option>
                                <option value="RdBu">Red-Blue</option>
                                <option value="Greys">Greys</option>
                            </select>
                        </div>

                        <!-- Z Min -->
                        <div class="col-md-2">
                            <label class="form-label mb-1">Z Min: <span id="zMinValue" class="slider-value">{{.ZMin}}</span></label>
                            <input type="range" id="zMinSlider" class="form-range range-slider"
                                   min="{{.ZMin}}" max="{{.ZMax}}"
                                   value="{{.ZMin}}" step="0.01" onchange="updateZRange()">
                        </div>

                        <!-- Z Max -->
                        <div class="col-md-2">
                            <label class="form-label mb-1">Z Max: <span id="zMaxValue" class="slider-value">{{.ZMax}}</span></label>
                            <input type="range" id="zMaxSlider" class="form-range range-slider"
                                   min="{{.ZMin}}" max="{{.ZMax}}"
                                   value="{{.ZMax}}" step="0.01" onchange="updateZRange()">
                        </div>

                        <!-- Log Frequency Toggle -->
                        <div class="col-md-2">
                            <div class="form-check">
                                <input class="form-check-input" type="checkbox" id="logFreq" {{if .LogFreq}}checked{{end}} onchange="updateLogFreq()">
                                <label class="form-check-label" for="logFreq">Log Y-Axis</label>
                            </div>
                        </div>

                        <!-- Actions -->
                        <div class="col-md-2">
                            <div class="btn-group" role="group">
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="resetView()">
                                    <i class="bi bi-zoom-out"></i> Reset
                                </button>
                                <button type="button" class="btn btn-outline-primary btn-sm" onclick="exportChart()">
                                    <i class="bi bi-download"></i> PNG
                                </button>
                            </div>
                        </div>

                        <!-- Cursor Info -->
                        <div class="col-md-2">
                            <div id="cursorInfo" class="cursor-info">
                                Hover for values
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="chart-container">
                    <div id="heatmapChart"></div>
                </div>
            </div>
        </div>

        <div class="row">
            <div class="col-12">
                <div class="stats-panel">
                    <div class="row">
                        <div class="col-md-3">
                            <strong>{{.XField}}:</strong> <span id="xCount"></span> points
                        </div>
                        <div class="col-md-3">
                            <strong>{{.YField}}:</strong> <span id="yCount"></span> points
                        </div>
                        <div class="col-md-3">
                            <strong>{{.ZField}} Range:</strong> <span id="zRange"></span>
                        </div>
                        <div class="col-md-3">
                            <strong>Grid Size:</strong> <span id="gridSize"></span>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <script>
        const xLabels = {{.XLabels}};
        const yLabels = {{.YLabels}};
        const gridData = {{.GridData}};
        const initialZMin = parseFloat("{{.ZMin}}");
        const initialZMax = parseFloat("{{.ZMax}}");
        const initialLogFreq = {{.LogFreq}};

        // Display stats
        document.getElementById('xCount').textContent = xLabels.length;
        document.getElementById('yCount').textContent = yLabels.length;
        document.getElementById('zRange').textContent = initialZMin.toFixed(2) + ' to ' + initialZMax.toFixed(2);
        document.getElementById('gridSize').textContent = xLabels.length + ' × ' + yLabels.length + ' = ' + (xLabels.length * yLabels.length).toLocaleString();

        // Create initial plot
        const trace = {
            z: gridData,
            x: xLabels,
            y: yLabels,
            type: 'heatmap',
            colorscale: 'Viridis',
            zmin: initialZMin,
            zmax: initialZMax,
            colorbar: {
                title: '{{.ZField}}',
                titleside: 'right'
            },
            hoverongaps: false,
            hovertemplate: '{{.XField}}: %{x}<br>{{.YField}}: %{y}<br>{{.ZField}}: %{z:.4f}<extra></extra>'
        };

        const layout = {
            title: '',
            xaxis: {
                title: '{{.XField}}',
                type: 'category',
                tickangle: -45
            },
            yaxis: {
                title: '{{.YField}}',
                type: initialLogFreq ? 'log' : 'category'
            },
            margin: {
                l: 70,
                r: 50,
                t: 20,
                b: 80
            }
        };

        const config = {
            responsive: true,
            scrollZoom: true,
            modeBarButtonsToRemove: ['lasso2d', 'select2d'],
            displaylogo: false
        };

        Plotly.newPlot('heatmapChart', [trace], layout, config);

        // Update cursor info on hover
        document.getElementById('heatmapChart').on('plotly_hover', function(data) {
            const pt = data.points[0];
            document.getElementById('cursorInfo').innerHTML =
                '<strong>{{.XField}}:</strong> ' + pt.x +
                '<br><strong>{{.YField}}:</strong> ' + pt.y +
                '<br><strong>{{.ZField}}:</strong> ' + (pt.z !== null ? pt.z.toFixed(4) : 'N/A');
        });

        document.getElementById('heatmapChart').on('plotly_unhover', function() {
            document.getElementById('cursorInfo').innerHTML = 'Hover for values';
        });

        function updateColorScale() {
            const colorScale = document.getElementById('colorScale').value;
            Plotly.restyle('heatmapChart', {colorscale: colorScale});
        }

        function updateZRange() {
            const zMin = parseFloat(document.getElementById('zMinSlider').value);
            const zMax = parseFloat(document.getElementById('zMaxSlider').value);
            document.getElementById('zMinValue').textContent = zMin.toFixed(2);
            document.getElementById('zMaxValue').textContent = zMax.toFixed(2);
            Plotly.restyle('heatmapChart', {zmin: zMin, zmax: zMax});
        }

        function updateLogFreq() {
            const logFreq = document.getElementById('logFreq').checked;
            Plotly.relayout('heatmapChart', {'yaxis.type': logFreq ? 'log' : 'category'});
        }

        function resetView() {
            // Reset sliders
            document.getElementById('zMinSlider').value = initialZMin;
            document.getElementById('zMaxSlider').value = initialZMax;
            document.getElementById('zMinValue').textContent = initialZMin.toFixed(2);
            document.getElementById('zMaxValue').textContent = initialZMax.toFixed(2);

            Plotly.update('heatmapChart',
                {zmin: initialZMin, zmax: initialZMax},
                {'xaxis.autorange': true, 'yaxis.autorange': true}
            );
        }

        function exportChart() {
            Plotly.downloadImage('heatmapChart', {
                format: 'png',
                width: 1920,
                height: 1080,
                filename: 'heatmap'
            });
        }
    </script>
</body>
</html>`

// ============================================================================
// INTERACTIVE DATA EXPLORER
// ============================================================================

// ExploreConfig configures the interactive data explorer.
// Provides control over the explorer's appearance and initial state.
//
// Use DefaultExploreConfig() to get sensible defaults, then customize as needed.
type ExploreConfig struct {
	Title         string `json:"title"`
	Theme         string `json:"theme"`         // "light" or "dark"
	InitialXField string `json:"initialXField"` // Optional initial X axis field
	InitialYField string `json:"initialYField"` // Optional initial Y axis field
	PageSize      int    `json:"pageSize"`      // Rows per page in table (default 50)
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	WasmEnabled   bool   `json:"wasmEnabled"` // Load ssql.wasm for client-side transforms
	WasmExecJS    string `json:"-"`           // Content of wasm_exec.js (inlined in HTML)
	FsPolyfillJS  string `json:"-"`           // Content of fs-polyfill.js (inlined in HTML)
	SsqlUIJS      string `json:"-"`           // Content of ssql-ui.js (shared completion/help/pipeline layer)
	WasmBinary    string `json:"-"`           // Base64 of the GZIPPED slim engine wasm (embedded in HTML)
	AllowEmpty    bool   `json:"-"`           // Permit zero records (served empty workspace); loud error otherwise
	Version       string `json:"-"`           // ssql version shown in the ready text (set by the CLI; stale-page diagnosis)
}

// DefaultExploreConfig provides sensible defaults for interactive data exploration.
// Returns an ExploreConfig with common settings that work well for most use cases.
//
// Example:
//
//	// Use defaults as-is
//	ssql.DataExplore(data, ssql.DefaultExploreConfig(), "explore.html")
//
//	// Customize from defaults
//	config := ssql.DefaultExploreConfig()
//	config.Title = "Sales Data Explorer"
//	config.Theme = "dark"
//	ssql.DataExplore(data, config, "sales_explorer.html")
func DefaultExploreConfig() ExploreConfig {
	return ExploreConfig{
		Title:    "Data Explorer",
		Theme:    "light",
		PageSize: 50,
		Width:    1400,
		Height:   800,
	}
}

// DataExplore creates an interactive HTML data exploration app.
// Generates a self-contained React-based explorer with:
//   - Sortable/filterable data table (AG-Grid Community)
//   - Field selector dropdowns for X/Y axes
//   - Chart type switcher (line, bar, scatter, pie)
//   - Basic aggregation UI (group by, sum/avg/count)
//   - Export filtered data as CSV
//
// Example:
//
//	// Create interactive explorer with default settings
//	data, _ := ssql.ReadCSV("data.csv")
//	ssql.DataExplore(data, ssql.DefaultExploreConfig(), "explore.html")
//
//	// Customize the explorer
//	config := ssql.DefaultExploreConfig()
//	config.Title = "Customer Analysis"
//	config.InitialXField = "date"
//	config.InitialYField = "revenue"
//	ssql.DataExplore(data, config, "customers.html")
func DataExplore(records iter.Seq[Record], config ExploreConfig, filename string) error {
	// Collect all records
	var recordSlice []Record
	for record := range records {
		recordSlice = append(recordSlice, record)
	}
	if len(recordSlice) == 0 && !config.AllowEmpty {
		// Loud by default: an accidentally-empty pipeline should not
		// produce a quietly blank page. AllowEmpty is for pages that
		// are MEANT to start blank (the served empty workspace, where
		// the user types the head and the data arrives from the
		// server).
		return fmt.Errorf("no data to explore")
	}

	// Analyze data structure
	chartData := analyzeData(recordSlice, ChartConfig{})

	return generateExploreHTML(recordSlice, chartData, config, filename)
}

// generateExploreHTML creates the interactive explorer HTML file
func generateExploreHTML(records []Record, chartData ChartData, config ExploreConfig, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", filename, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Convert records to JSON-safe map format
	var recordMaps []map[string]any
	for _, record := range records {
		m := maps.Collect(record.All())
		recordMaps = append(recordMaps, m)
	}

	// Convert data to JSON
	dataJSON, err := json.Marshal(recordMaps)
	if err != nil {
		return fmt.Errorf("marshaling data: %w", err)
	}

	schemaJSON, err := json.Marshal(chartData)
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Execute template
	tmpl := template.Must(template.New("explore").Parse(exploreHTMLTemplate))
	templateData := struct {
		Title       string
		DataJSON    template.JS
		SchemaJSON  template.JS
		ConfigJSON  template.JS
		Theme       string
		WasmEnabled bool
		WasmExecJS   template.JS
		FsPolyfillJS template.JS
		SsqlUIJS     template.JS
		WasmBinary   template.JS
		Version      string
	}{
		Title:       config.Title,
		DataJSON:    template.JS(dataJSON),
		SchemaJSON:  template.JS(schemaJSON),
		ConfigJSON:  template.JS(configJSON),
		Theme:       config.Theme,
		WasmEnabled: config.WasmEnabled,
		WasmExecJS:   template.JS(config.WasmExecJS),
		FsPolyfillJS: template.JS(config.FsPolyfillJS),
		SsqlUIJS:     template.JS(config.SsqlUIJS),
		WasmBinary:   template.JS(config.WasmBinary),
		Version:      config.Version,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}

	return writer.Flush()
}

// exploreHTMLTemplate is the HTML template for the interactive data explorer
const exploreHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>

    <!-- React -->
    <script src="https://unpkg.com/react@18/umd/react.production.min.js" crossorigin></script>
    <script src="https://unpkg.com/react-dom@18/umd/react-dom.production.min.js" crossorigin></script>

    <!-- AG-Grid -->
    <script src="https://unpkg.com/ag-grid-community@35.1.0/dist/ag-grid-community.min.js"></script>

    <!-- Plotly for charts -->
    <script src="https://cdn.plot.ly/plotly-2.27.0.min.js"></script>

    <!-- Bootstrap -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.10.0/font/bootstrap-icons.css" rel="stylesheet">

    {{if .WasmEnabled}}
    <script>{{.FsPolyfillJS}}</script>
    <script>{{.WasmExecJS}}</script>
    {{end}}

    <style>
        :root {
            --bg-color: {{if eq .Theme "dark"}}#1a1a1a{{else}}#f8f9fa{{end}};
            --text-color: {{if eq .Theme "dark"}}#e9ecef{{else}}#212529{{end}};
            --border-color: {{if eq .Theme "dark"}}#495057{{else}}#dee2e6{{end}};
            --panel-bg: {{if eq .Theme "dark"}}#212529{{else}}#ffffff{{end}};
            --hover-bg: {{if eq .Theme "dark"}}#343a40{{else}}#e9ecef{{end}};
        }

        * { box-sizing: border-box; }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            margin: 0;
            padding: 0;
            background-color: var(--bg-color);
            color: var(--text-color);
        }

        /* Single column, full width: header, controls, chart, grid.
           (The two-panel flex layout left with the widget builder,
           DFC117.) */
        .explorer-container {
            height: 100vh;
            height: 100dvh; /* mobile URL bars — dvh where supported */
        }

        /* Mobile: single column, data first, builder below. Inputs go to
           16px — anything smaller triggers iOS zoom-on-focus, which is
           most of what "broken on a phone" feels like. */
        @media (max-width: 768px) {
            .explorer-container {
                height: auto;
                min-height: 100dvh;
            }
            .main-content {
                overflow: visible;
                padding: 8px;
            }
            .table-area {
                /* autoHeight grid: the container must NOT constrain it
                   (!important beats the React inline height:100%) */
                height: auto !important;
            }
            #pipelineBar { padding: 8px; }
            /* inline font-size:13px on the elements — override it */
            #pipeline, #headInput { font-size: 16px !important; }
            #serverHead { flex-wrap: wrap; }

            #pipelineBar button { padding: 8px 14px; }
            #completions { max-width: calc(100vw - 24px); }
            /* vertical swipes over the chart scroll the PAGE (Plotly's
               touch handlers would otherwise trap them); pinch/tap still
               reach the plot */
            .chart-area { touch-action: pan-y; }
        }

        .main-content {
            height: 100%;
            display: flex;
            flex-direction: column;
            overflow: hidden;
            padding: 16px;
            box-sizing: border-box;
        }

        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding-bottom: 16px;
            border-bottom: 1px solid var(--border-color);
            margin-bottom: 16px;
        }

        .header h1 {
            margin: 0;
            font-size: 1.5rem;
        }

        .chart-controls {
            display: flex;
            gap: 16px;
            flex-wrap: wrap;
            margin-bottom: 16px;
            padding: 16px;
            background: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
        }

        .control-group {
            display: flex;
            flex-direction: column;
            gap: 4px;
        }

        .control-group label {
            font-size: 0.75rem;
            font-weight: 600;
            text-transform: uppercase;
            color: {{if eq .Theme "dark"}}#adb5bd{{else}}#6c757d{{end}};
        }

        .control-group select {
            padding: 6px 12px;
            border: 1px solid var(--border-color);
            border-radius: 4px;
            background: var(--bg-color);
            color: var(--text-color);
            min-width: 140px;
        }

        .chart-area {
            height: 350px;
            min-height: 200px;
            background: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            margin-bottom: 16px;
            /* drag the bottom-right corner to give dense charts room */
            resize: vertical;
            overflow: hidden;
        }

        .table-area {
            flex: 1;
            min-height: 200px;
        }


        .btn-group {
            display: flex;
            gap: 8px;
        }

        .btn {
            padding: 6px 12px;
            border: 1px solid var(--border-color);
            border-radius: 4px;
            background: var(--panel-bg);
            color: var(--text-color);
            cursor: pointer;
            display: flex;
            align-items: center;
            gap: 4px;
        }

        .btn:hover {
            background: var(--hover-bg);
        }

        .btn-primary {
            background: #0d6efd;
            color: white;
            border-color: #0d6efd;
        }

        .btn-primary:hover {
            background: #0b5ed7;
        }

        {{if eq .Theme "dark"}}
        .ag-theme-quartz {
            --ag-background-color: #212529;
            --ag-header-background-color: #343a40;
            --ag-odd-row-background-color: #2b3035;
            --ag-foreground-color: #e9ecef;
            --ag-border-color: #495057;
            --ag-header-foreground-color: #e9ecef;
        }
        {{end}}
    </style>
</head>
<body>
    {{if .WasmEnabled}}
    <div id="pipelineBar" style="position:relative; padding:10px 16px; border-bottom:1px solid var(--border-color); background:var(--panel-bg); font-family:monospace;">
        <div id="status" style="font-size:12px; color:#6c757d; margin-bottom:6px;">Loading engine…</div>
        <div id="serverHead" style="display:none; margin-bottom:6px;">
            <textarea id="headInput" rows="2" spellcheck="false" style="width:100%; font-family:inherit; font-size:13px; padding:6px 8px; box-sizing:border-box; background:var(--bg-color); color:var(--text-color); border:1px solid #6c9bd1; border-radius:4px; resize:vertical;"></textarea>
            <div style="margin-top:6px; display:flex; gap:8px; align-items:center; flex-wrap:wrap;">
                <button id="headRun" title="This part runs on the ssql serve host; its result feeds the local tail as data.jsonl" style="padding:4px 14px; white-space:nowrap;">Run server pipeline</button>
                <button id="headOptimize" title="Rewrite the head with the pipeline optimizer (filter merges, sort+limit collapse, ssh pushdown) — preview before applying" style="padding:4px 10px;">&#9881; Optimise</button>
                <label style="font-size:12px; white-space:nowrap; cursor:pointer;" title="Compile the head to typed Go on the server (cached by pipeline) — big scans run several times faster"><input type="checkbox" id="headTyped"> &#9889; typed</label>
                <span style="opacity:0.6; font-size:12px; white-space:nowrap;">&#9656; data.jsonl</span>
            </div>
        </div>
        <div id="serverFiles" style="display:none; font-size:12px; margin-bottom:6px; color:#6c757d; gap:6px; align-items:flex-start;">
            <select id="serverFileSelect" multiple size="4" style="min-width:220px; max-width:60%; font-family:inherit; font-size:12px; background:var(--bg-color); color:var(--text-color); border:1px solid var(--border-color); border-radius:4px;"></select>
            <button id="serverFileLoad" style="padding:4px 12px; white-space:nowrap;">Load into workspace</button>
            <span style="max-width:30%;">Loaded files can be used by name in the local pipeline</span>
        </div>
        <textarea id="pipeline" rows="3" spellcheck="false" placeholder="ssql from data.jsonl | …  (suggestions as you type; Tab accepts; Enter runs; Shift+Enter = newline; Alt-h = help)" style="width:100%; resize:vertical; font-family:inherit; font-size:13px; padding:8px; box-sizing:border-box; background:var(--bg-color); color:var(--text-color); border:1px solid var(--border-color); border-radius:4px;"></textarea>
        <div id="completions" style="position:absolute; display:none; background:var(--panel-bg); border:1px solid #6c9bd1; border-radius:6px; max-height:200px; overflow-y:auto; z-index:1000; font-size:13px; min-width:160px; box-shadow:0 4px 12px rgba(0,0,0,0.3);"></div>
        <div style="margin-top:6px; display:flex; gap:8px; align-items:center; flex-wrap:wrap;">
            <button id="barRun" title="This part runs in the browser's embedded engine over data.jsonl; results fill the grid" style="padding:4px 14px;">Run local pipeline</button>
            <button id="barOptimize" title="Rewrite this pipeline with the optimizer — preview before applying" style="padding:4px 10px;">&#9881; Optimise</button>
            <button id="barCopyCli" style="padding:4px 14px; display:none;" title="Copy the composed head+tail as one ssql pipeline, runnable in the server's data directory">⎘ Copy CLI</button>
            <button id="barShare" style="padding:4px 14px; display:none;" title="Copy a link that restores this whole setup — head, ⚡ typed, loaded server files, and the tail pipeline">🔗 Share</button>
            <button id="barHelp" onmousedown="event.preventDefault()" style="padding:4px 14px;">? Help</button>
            <button id="barReset" style="padding:4px 14px;">Reset data</button>
            <input type="file" id="barUpload" accept=".csv,.tsv,.json,.jsonl" style="display:none">
            <button onclick="document.getElementById('barUpload').click()" style="padding:4px 14px;">Upload file</button>
            <span id="fileList" style="font-size:12px; color:#6c757d;"></span>
        </div>
        <div id="filesBar" style="display:none; font-size:12px; margin-top:6px;"></div>
        <pre id="output" style="display:none; max-height:200px; overflow:auto; font-size:12px; background:var(--bg-color); color:var(--text-color); border:1px solid var(--border-color); border-radius:4px; padding:8px; margin-top:6px;"></pre>
    </div>
    <style>#completions div { padding: 3px 12px; cursor: pointer; white-space: pre; } #completions div.sel { background: #6c9bd1; color: #fff; } #completions div:hover { background: var(--hover-bg); }</style>
    {{end}}
    <div id="root"></div>

    {{if .WasmEnabled}}<script>{{.SsqlUIJS}}</script>{{end}}
    <script>
        // Data from the pipeline. Null-coalesce: Go marshals a
        // zero-record slice as null, and DATA.length on null killed the
        // whole React app on the empty workspace page.
        const DATA = ({{.DataJSON}}) || [];
        const SCHEMA = ({{.SchemaJSON}}) || {};
        const CONFIG = {{.ConfigJSON}};

        // WASM module (initialized asynchronously if enabled)
        let ssqlWasm = null;
        {{if .WasmEnabled}}
        function opsToStages(ops) {
            const stages = [];
            let i = 0;
            while (i < ops.length) {
                const o = ops[i];
                switch (o.op) {
                    case 'where': {
                        const a = ['where'];
                        while (i < ops.length && ops[i].op === 'where') {
                            a.push('-if', ops[i].field, ops[i].operator, String(ops[i].value));
                            i++;
                        }
                        stages.push(a);
                        continue;
                    }
                    case 'sort': {
                        const a = ['sort'];
                        let first = true;
                        while (i < ops.length && ops[i].op === 'sort') {
                            if (!first) a.push('+');
                            a.push(ops[i].field);
                            if (ops[i].desc) a.push('-desc');
                            first = false;
                            i++;
                        }
                        stages.push(a);
                        continue;
                    }
                    case 'group_by': {
                        const a = ['group-by', ...(o.groupFields || [])];
                        for (const g of (o.aggs || [])) {
                            const alias = g.alias || g.func; // mini-engine naming contract
                            if (g.func === 'count') a.push('-count', alias);
                            else a.push('-' + g.func, g.field, alias);
                        }
                        stages.push(a); i++; continue;
                    }
                    case 'distinct':
                        stages.push(o.field ? ['group-by', o.field] : ['distinct']);
                        i++; continue;
                    case 'limit':
                        if (o.offset) stages.push(['offset', String(o.offset)]);
                        stages.push(['limit', String(o.n)]);
                        i++; continue;
                    case 'compute':
                        stages.push(['update', '-set-expr', o.name, o.expr]);
                        i++; continue;
                    case 'pivot':
                        stages.push(['pivot', '-row', o.rowField, '-col', o.colField, '-val', o.valField, '-func', o.aggFunc || 'sum']);
                        i++; continue;
                    case 'window': {
                        for (const wc of (o.windowConfigs || [])) {
                            const a = ['window'];
                            for (const pf of (wc.partitionBy || [])) a.push('-partition', pf);
                            for (const ob of (wc.orderBy || [])) { a.push('-order', ob.field); if (ob.desc) a.push('-desc'); }
                            if (wc.frame) {
                                if (wc.frame.preceding >= 0) a.push('-preceding', String(wc.frame.preceding));
                                if (wc.frame.following >= 0) a.push('-following', String(wc.frame.following));
                            }
                            for (const sp of (wc.specs || [])) {
                                switch (sp.type) {
                                    case 'row_number': a.push('-row-number', sp.result); break;
                                    case 'rank': a.push('-rank', sp.result); break;
                                    case 'dense_rank': a.push('-dense-rank', sp.result); break;
                                    case 'percent_rank': a.push('-percent-rank', sp.result); break;
                                    case 'count': a.push('-count', sp.result); break;
                                    case 'ntile': a.push('-ntile', String(sp.n), sp.result); break;
                                    case 'lag': a.push('-lag', sp.field, String(sp.n || 1), sp.result); break;
                                    case 'lead': a.push('-lead', sp.field, String(sp.n || 1), sp.result); break;
                                    default: a.push('-' + sp.type, sp.field, sp.result); // sum/avg/min/max/first/last
                                }
                            }
                            stages.push(a);
                        }
                        i++; continue;
                    }
                    default:
                        throw new Error('unsupported op: ' + o.op);
                }
            }
            return stages;
        };

        function shellQuoteArg(a) {
            a = String(a);
            return /[\s'"|<>()]/.test(a) || a === '' ? "'" + a.replace(/'/g, "'\\''") + "'" : a;
        }

        (async function() {
            try {
                // The embedded engine is the SAME slim playground wasm,
                // gzipped; decompress and boot it, then expose the old
                // mini-engine surface (pipeline(data, ops)) as a shim
                // that translates ops to REAL CLI stages via ssqlExec —
                // one semantics, no third engine (DFC107).
                const gz = Uint8Array.from(atob('{{.WasmBinary}}'), c => c.charCodeAt(0));
                const wasmBuf = await new Response(
                    new Blob([gz]).stream().pipeThrough(new DecompressionStream('gzip'))
                ).arrayBuffer();
                const go = new Go();
                const inst = await WebAssembly.instantiate(wasmBuf, go.importObject);
                go.run(inst.instance);
                for (let i = 0; i < 200 && !(typeof ssqlReady !== 'undefined' && ssqlReady); i++) {
                    await new Promise(r => setTimeout(r, 50));
                }
                if (typeof ssqlReady === 'undefined' || !ssqlReady) throw new Error('engine did not become ready');


                ssqlWasm = {
                    pipeline(data, ops) {
                        const stages = opsToStages(ops);
                        if (!stages.length) return data;
                        let payload = data.map(r => JSON.stringify(r)).join('\n') + '\n';
                        for (const args of stages) {
                            const res = ssqlExec(args, payload);
                            if (res.exitCode !== 0) throw new Error(res.stderr || ('stage failed: ' + args.join(' ')));
                            payload = res.stdout;
                        }
                        const out = [];
                        for (const line of payload.split('\n')) {
                            const t = line.trim();
                            if (t.startsWith('{') && !t.startsWith('{"_schema"')) out.push(JSON.parse(t));
                        }
                        return out;
                    }
                };
                window.ssqlEngine = ssqlWasm; // exposed for e2e harnesses
                // Display sinks (DFC119): ask the engine to describe
                // itself once; any subcommand marked displaySink in
                // -spec-json is recognized as a visual stage. No list
                // of visual commands exists in this page.
                try {
                    const specOut = ssqlExec(['-spec-json'], '');
                    if (specOut.exitCode === 0) {
                        window.exploreSpec = JSON.parse(specOut.stdout);
                        exploreSinks = findDisplaySinks(window.exploreSpec, []);
                    }
                } catch (e) { console.warn('spec-json unavailable:', e); }
                // Boot the shared interactive layer: the data lives in the
                // virtual FS so completion (fields, VALUES) and the
                // pipeline bar run against it like any file.
                _fsWriteFile('data.jsonl', DATA.map(r => JSON.stringify(r)).join('\n') + '\n');
                window.ssqlUIReady = true;
                window.SSQL_UI_READY_TEXT = 'Engine ready — pipeline runs against data.jsonl (ssql v{{.Version}})';
                const bar = document.getElementById('pipeline');
                if (bar && !bar.value) bar.value = 'ssql from data.jsonl | ';
                const st = document.getElementById('status');
                if (st) st.textContent = window.SSQL_UI_READY_TEXT;
                refreshFileList();
                console.log('ssql engine loaded (embedded slim wasm)');
                const showBadge = () => {
                    const badge = document.getElementById('wasm-badge');
                    if (badge) { badge.style.display = 'inline'; }
                    else { setTimeout(showBadge, 100); }
                };
                showBadge();
            } catch(e) {
                console.warn('ssql engine failed to load:', e);
                window.ssqlEngineError = String(e); // surfaced for e2e harnesses
                ssqlWasm = null;
                const st = document.getElementById('status');
                if (st) st.textContent = 'Engine failed to load — pipelines disabled; grid browsing only.';
            }
        })();

        // Pipeline-bar wiring (elements are static, functions come from
        // the shared ssql-ui layer). Results replace the grid rows.
        function showOutput(result, label) {
            const el = document.getElementById('output');
            if (!el) return;
            el.style.display = 'block';
            el.textContent = (label ? label + '\n\n' : '') +
                (result.exitCode !== 0 ? (result.stderr || 'error') : result.stdout);
        }
        function refreshFileList() {
            const names = _fsListFiles().filter(p =>
                !p.startsWith('tmp/') && !p.startsWith('/tmp/') &&
                !p.startsWith('dev/fd/') && !p.startsWith('/dev/fd/'));
            document.getElementById('fileList').textContent = 'files: ' + names.join(', ');
        }
        window.ssqlUIOnUpload = refreshFileList;
        document.getElementById('barUpload').addEventListener('change', (ev) => {
            const file = ev.target.files[0];
            if (!file) return;
            const reader = new FileReader();
            reader.onload = () => ssqlUIWriteUpload(file.name, reader.result);
            reader.readAsText(file);
        });
        // --- Display sinks (DFC119 Phase A) ---
        let exploreSinks = []; // [{path:['to','chart'], key:'chart', spec}]
        function findDisplaySinks(node, path) {
            let out = [];
            if (node.displaySink) out.push({ path: path.slice(), key: node.displaySink, spec: node });
            for (const [name, sub] of Object.entries(node.subcommands || {})) {
                out = out.concat(findDisplaySinks(sub, path.concat(name)));
            }
            return out;
        }
        // matchSinkStage: does this stage argv start with a sink's path?
        function matchSinkStage(argv) {
            for (const sk of exploreSinks) {
                if (sk.path.length <= argv.length && sk.path.every((w, i) => argv[i] === w)) return sk;
            }
            return null;
        }
        // decodeSinkArgv: generic argv→config, arities from the SPEC —
        // no renderer ever hand-parses argv. Returns null on any token
        // the spec doesn't know: the stage then EXECUTES normally so
        // errors surface loudly instead of being silently rendered.
        function decodeSinkArgv(sk, argv) {
            const flags = {};
            for (const f of sk.spec.flags || []) for (const nm of f.names) flags[nm] = f;
            const cfg = { _sink: sk.key, _positionals: [] };
            let i = sk.path.length;
            while (i < argv.length) {
                const t = argv[i];
                if (t.startsWith('-')) {
                    const f = flags[t];
                    if (!f) return null;
                    const name = f.names[0];
                    if (f.bool) { cfg[name] = true; i++; continue; }
                    const arity = (f.args || []).length || 1;
                    const vals = argv.slice(i + 1, i + 1 + arity);
                    if (vals.length < arity) return null;
                    if (f.accumulate) { (cfg[name] = cfg[name] || []).push(...vals); }
                    else cfg[name] = arity === 1 ? vals[0] : vals;
                    i += 1 + arity;
                } else {
                    cfg._positionals.push(t);
                    i++;
                }
            }
            return cfg;
        }
        // exploreWriteChartStage: the chart controls EDIT the trailing
        // sink stage through the model (grid-click pattern): rewrite
        // only the flags the control owns; append the stage on first
        // touch; user-authored flags survive.
        window.exploreWriteChartStage = function(updates, opts) {
            const ta = document.getElementById('pipeline');
            if (!ta) return;
            let m = window.ssqlUIParseModel(ta.value.trim() || 'ssql from data.jsonl');
            m = m.filter(st => st.text || (st.argv && st.argv.length));
            let st = m.length ? m[m.length - 1] : null;
            let sk = st && st.argv ? matchSinkStage(st.argv) : null;
            if (!sk) {
                st = { argv: ['to', 'chart'] };
                m.push(st);
                sk = matchSinkStage(st.argv);
                if (!sk) return; // spec unavailable
            }
            for (const [flag, val] of Object.entries(updates)) {
                const idx = st.argv.indexOf(flag, sk.path.length);
                if (idx >= 0) { st.argv[idx + 1] = val; }
                else { st.argv.push(flag, val); }
            }
            ta.value = window.ssqlUIPrintModel(m);
            if (!(opts && opts.norun)) window.exploreRunBar();
        };

        // Set by exploreRunHead around its tail rerun: a tail failure in
        // that window with an unknown-field error means the HEAD's schema
        // changed under the tail — offer a one-click reset instead of
        // making the user connect the dots (default stays persistence +
        // loud failure; reset is the USER'S choice at the useful moment).
        let exploreHeadJustRan = false;
        window.exploreRunBar = function() {
            let text = document.getElementById('pipeline').value.trim().replace(/[|\s]+$/, '');
            if (!text) return;
            // Display sink (DFC119): a decodable trailing sink stage is
            // CONFIG, not execution — strip it, run the data stages,
            // hand the config to the renderer. Undecodable (unknown
            // flag) → leave it in: the engine's error stays loud.
            let sinkCfg = null;
            {
                const m = window.ssqlUIParseModel(text);
                const last = m.length ? m[m.length - 1] : null;
                if (last && last.argv) {
                    const sk = matchSinkStage(last.argv);
                    if (sk) {
                        const cfg = decodeSinkArgv(sk, last.argv);
                        if (cfg) {
                            sinkCfg = cfg;
                            text = window.ssqlUIPrintModel(m.slice(0, -1));
                        }
                    }
                }
                // Always-written (Ross: no implicit state): a run with
                // no decodable sink stage writes one afterwards, so the
                // bar always spells the chart being shown.
                window.exploreSinkAbsent = !sinkCfg;
            }
            _fsResetWriteLog();
            const res = executePipeline(text + ' | ssql to jsonl', false);
            const afterHead = exploreHeadJustRan;
            exploreHeadJustRan = false;
            if (res.exitCode !== 0) {
                showOutput(res, 'Pipeline failed');
                if (afterHead && /unknown field/i.test(res.stderr || '')) {
                    const out = document.getElementById('output');
                    const note = document.createElement('div');
                    note.style.cssText = 'margin-top:8px;';
                    note.textContent = 'The server pipeline produced different fields than this local pipeline expects.';
                    const btn = document.createElement('button');
                    btn.textContent = 'Reset local pipeline';
                    btn.style.cssText = 'margin-top:6px; padding:4px 14px;';
                    btn.addEventListener('click', () => {
                        document.getElementById('pipeline').value = 'ssql from data.jsonl';
                        window.exploreRunBar();
                    });
                    out.appendChild(note);
                    out.appendChild(btn);
                }
                return;
            }
            document.getElementById('output').style.display = 'none';
            const rows = [];
            for (const line of res.stdout.split('\n')) {
                const t = line.trim();
                if (t.startsWith('{') && !t.startsWith('{"_schema"')) rows.push(JSON.parse(t));
            }
            if (window.exploreSetRows) window.exploreSetRows(rows);
            // Renderer registry (DFC119): decoded sink config goes to
            // the renderer registered for its key — adding a visual is
            // a declaration plus one registration, never an edit here.
            if (sinkCfg) {
                const render = (window.exploreRenderers || {})[sinkCfg._sink];
                if (render) render(sinkCfg, rows);
                else showOutput({ stdout: '', stderr: 'no renderer registered for display sink "' + sinkCfg._sink + '"', exitCode: 1 }, 'Display sink');
            }
            showCreatedFiles();
            refreshFileList();
            // Runs can write files (tee) that later stages complete from.
            if (window.ssqlUISchemaCacheClear) window.ssqlUISchemaCacheClear();
        };
        // Server mode (DFC108 2c). This artifact is byte-identical whether
        // opened from disk or served by ssql serve — the page decides at
        // LOAD time: the serve URL carries the head pipeline (?file=X →
        // "ssql from X"; ?pipeline=…) and /api/health answers same-origin.
        // In server mode the head input appears fused above the tail bar:
        // the head runs on the server, its result replaces data.jsonl, and
        // the local tail re-runs. Two pipeline SEGMENTS with distinct
        // roles — never two copies of one pipeline (the builder/bar
        // unification lesson).
        function srvFetch() { return window.__srvFetch || window.fetch.bind(window); }
        function srvUrl(path, extra) {
            const params = new URLSearchParams(extra || '');
            const token = new URLSearchParams(location.search).get('token');
            if (token) params.set('token', token);
            const q = params.toString();
            return path + (q ? '?' + q : '');
        }
        async function exploreDetectServerMode() {
            const params = new URLSearchParams(location.search);
            let head = '';
            if (params.get('file')) head = 'ssql from ' + params.get('file');
            else if (params.get('pipeline')) head = params.get('pipeline');
            // Server mode is decided by the PROBE, not the URL: an empty
            // workspace (GET /) has no head param, but the head input
            // still appears — prefilled "ssql from " so Tab immediately
            // completes the server's files. Standalone artifacts stay
            // dormant (the probe fails).
            if (!params.get('srvtest')) {
                try {
                    const r = await srvFetch()(srvUrl('/api/health'));
                    if (!r.ok) return;
                } catch (e) { return; }
            }
            document.getElementById('headInput').value = head || 'ssql from ';
            document.getElementById('serverHead').style.display = 'block';
            document.getElementById('barCopyCli').style.display = '';
            document.getElementById('barShare').style.display = '';
            renderServerFiles();
            restoreSharedSetup();
        }
        // Server files as click-to-load chips: loading writes the raw
        // bytes into the vFS under the SAME name, so direct-file join
        // ("ssql join kind.csv -using k") and file completion work on
        // them exactly like uploads. Large files are refused loudly by
        // the server — reduce those through a head pipeline instead.
        function exploreLoadServerFile(name, contents) {
            ssqlUIWriteUpload(name, contents);
            if (!loadedServerFiles.includes(name)) loadedServerFiles.push(name);
        }
        // Server files as a multi-select: pick several side tables, load
        // them into the browser FS in one go (each under its own name,
        // so direct-file join and completion work on them like uploads).
        // Oversized files are visible but disabled — the server refuses
        // them anyway; reduce those through a head pipeline.
        const SERVER_FILE_MAX = 32 * 1024 * 1024;
        async function renderServerFiles() {
            let files;
            try {
                const r = await srvFetch()(srvUrl('/api/files'));
                files = (await r.json()).files || [];
            } catch (e) { return; }
            if (!files.length) return;
            serverFileNames = files.map(f => f.name);
            const sel = document.getElementById('serverFileSelect');
            sel.innerHTML = '';
            for (const f of files) {
                const opt = document.createElement('option');
                opt.value = f.name;
                const kb = f.size >= (1 << 20) ? (f.size / (1 << 20)).toFixed(1) + 'MB' : (f.size >> 10) + 'KB';
                if (f.size > SERVER_FILE_MAX) {
                    opt.textContent = f.name + ' (' + kb + ' — too large; reduce via a head pipeline)';
                    opt.disabled = true;
                } else {
                    opt.textContent = f.name + ' (' + kb + ')';
                }
                sel.appendChild(opt);
            }
            document.getElementById('serverFiles').style.display = 'flex';
        }
        window.exploreLoadSelectedFiles = async function() {
            const sel = document.getElementById('serverFileSelect');
            const names = [...sel.selectedOptions].map(o => o.value);
            if (!names.length) {
                setTransientStatus('Select one or more server files first');
                return;
            }
            const ok = [], failed = [];
            for (const name of names) {
                try {
                    const r = await srvFetch()(srvUrl('/api/raw', 'file=' + encodeURIComponent(name)));
                    if (!r.ok) {
                        const err = await r.json().catch(() => ({}));
                        failed.push(name + (err.error ? ' (' + err.error + ')' : ''));
                        continue;
                    }
                    exploreLoadServerFile(name, await r.text());
                    ok.push(name);
                } catch (e) { failed.push(name + ' (' + e + ')'); }
            }
            let msg = ok.length ? 'Loaded ' + ok.join(', ') + ' — join by name, e.g. ssql join ' + ok[0] + ' -using FIELD' : '';
            if (failed.length) msg += (msg ? '  ⚠ ' : '⚠ ') + 'failed: ' + failed.join('; ');
            setTransientStatus(msg || 'Nothing loaded');
        };
        document.getElementById('serverFileLoad').addEventListener('click', () => window.exploreLoadSelectedFiles());
        // A head can emit millions of rows; the browser tab dies on the
        // result (vfs write + grid render). Cap what crosses over with
        // an implicit trailing limit on the EXECUTED pipeline only —
        // the input text stays untouched, and the status says when the
        // cap bit (loudness doctrine: truncate visibly, never silently).
        const HEAD_ROW_CAP = 10000;
        window.exploreRunHead = async function() {
            const head = document.getElementById('headInput').value.trim().replace(/[|\s]+$/, '');
            if (!head) return;
            const status = document.getElementById('status');
            status.textContent = 'Running head on server…';
            let res;
            const typed = document.getElementById('headTyped').checked;
            try {
                const r = await srvFetch()(srvUrl('/api/execute', 'mode=buffered' + (typed ? '&engine=typed' : '')),
                    { method: 'POST', body: JSON.stringify({ pipeline: head + ' | ssql limit ' + HEAD_ROW_CAP }) });
                res = await r.json();
            } catch (e) {
                status.textContent = 'Server unreachable: ' + e;
                return;
            }
            if (res.error || res.code !== 0) {
                status.textContent = 'Head failed: ' + (res.error || res.stderr || 'unknown error');
                showOutput({ stdout: '', stderr: res.detail || res.stderr || res.error || '', exitCode: 1 }, 'Head pipeline failed');
                return;
            }
            _fsWriteFile('data.jsonl', res.output);
            // data.jsonl changed under the same name — the tail's
            // field-name cache is keyed by pipeline text and would
            // serve the OLD schema (found by Ross: head producing new
            // fields, tail completing the old ones).
            if (window.ssqlUISchemaCacheClear) window.ssqlUISchemaCacheClear();
            status.textContent = 'Head OK — re-running tail…';
            exploreHeadJustRan = true;
            window.exploreRunBar();
            // Report the head's wall time so engine differences are
            // VISIBLE (exec vs typed on a big scan is the whole story) —
            // and INPUT rows/sec, the number that shows what the run
            // actually did (a group-by reads millions, emits dozens).
            // inputRows costs the server one cached line count.
            const rows = (res.output.match(/\n/g) || []).length - 1;
            const secs = res.runMs != null ? (res.runMs / 1000).toFixed(res.runMs < 10000 ? 2 : 1) + 's' : '';
            const fmtN = (n) => n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e4 ? Math.round(n / 1e3) + 'k' : String(n);
            let note = 'Head OK — ';
            if (res.inputRows != null) note += fmtN(res.inputRows) + ' → ';
            note += Math.max(rows, 0) + ' rows' + (secs ? ' in ' + secs : '');
            if (res.inputRows != null && res.runMs > 0) {
                note += ' (' + fmtN(Math.round(res.inputRows / (res.runMs / 1000))) + ' rows/s)';
            }
            if (res.engine === 'typed-compiled') note += ' (typed; +' + (res.compileMs / 1000).toFixed(1) + 's one-time compile, cached for re-runs)';
            else if (res.engine === 'typed-cached') note += ' (typed, cache hit)';
            else note += ' (exec — try ⚡ typed for big scans)';
            if (rows >= HEAD_ROW_CAP) note += ' — capped at ' + HEAD_ROW_CAP.toLocaleString() + ' rows; refine the head with where/sample/limit';
            status.textContent = note;
        };
        document.getElementById('headRun').addEventListener('click', () => window.exploreRunHead());
        // Completion for the head input rides the SAME machinery as the
        // tail bar, with an HTTP executor: the cursor protocol goes to
        // /api/cursor, so subcommands, flags, operators, SERVER file
        // paths and sampled values all complete against the serve host.
        // Bound before the Enter-runs-head listener: an arrow-activated
        // completion accept preventDefaults Enter, which the run
        // listener respects; a passive popup never steals Enter.
        const srvCursorExec = async (args, stdin, env) => {
            const body = { argv: args };
            if (env && env.AUTOCLI_CACHE_FILE) body.env = { AUTOCLI_CACHE_FILE: env.AUTOCLI_CACHE_FILE };
            const r = await srvFetch()(srvUrl('/api/cursor'), { method: 'POST', body: JSON.stringify(body) });
            const res = await r.json();
            return { stdout: res.stdout || '', stderr: res.stderr || '', exitCode: res.code || 0 };
        };
        // Pipeline-aware field names for the head (the CLI's Ctrl-O):
        // -complete-source picks the upstream at the cursor, then the
        // SERVER runs it under SSQL_MODE=schema via /api/schema-fields.
        const srvSchemaCache = new Map();
        const srvSchemaFields = async (before) => {
            const src = (await srvCursorExec(['-complete-source', before], '')).stdout.trim();
            if (!src) return null;
            if (srvSchemaCache.has(src)) return srvSchemaCache.get(src);
            try {
                const r = await srvFetch()(srvUrl('/api/schema-fields'),
                    { method: 'POST', body: JSON.stringify({ pipeline: src }) });
                const res = await r.json();
                const fields = res.fields && res.fields.length ? res.fields : null;
                if (srvSchemaCache.size > 30) srvSchemaCache.clear();
                srvSchemaCache.set(src, fields);
                return fields;
            } catch (e) { return null; }
        };
        window.ssqlUIBindCompletion({
            input: document.getElementById('headInput'),
            exec: srvCursorExec,
            ready: () => document.getElementById('serverHead').style.display !== 'none',
            schemaFields: srvSchemaFields,
            bigValueSource: () => false, // sampling runs (and is capped) server-side
        });
        document.getElementById('headInput').addEventListener('keydown', (e) => {
            // Enter runs; Shift+Enter inserts a newline (multi-line head).
            if (e.key === 'Enter' && !e.shiftKey && !e.defaultPrevented) { e.preventDefault(); window.exploreRunHead(); }
        });

        // Copy-as-CLI: the head and tail are ONE pipeline split at
        // data.jsonl — compose them back into a single ssql command
        // runnable in the server's data directory. Files that exist
        // only in the browser (uploads) get a loud note.
        let serverFileNames = [];
        window.exploreCopyCli = function() {
            const head = document.getElementById('headInput').value.trim().replace(/[|\s]+$/, '').replace(/\s*\n\s*/g, ' ');
            const tail = document.getElementById('pipeline').value.trim().replace(/[|\s]+$/, '').replace(/\s*\n\s*/g, ' ');
            let cmd;
            const m = tail.match(/^ssql\s+from\s+data\.jsonl\s*(\|\s*|$)/);
            if (m && head) {
                const rest = tail.slice(m[0].length).trim();
                cmd = rest ? head + ' | ' + rest : head;
            } else {
                // Tail reads a real file (e.g. a loaded server table) —
                // it IS the CLI pipeline already.
                cmd = tail;
            }
            window.exploreLastCopiedCli = cmd;
            const notes = [];
            for (const tok of cmd.split(/\s+/)) {
                if (/\.(csv|tsv|json|jsonl|parquet|arrow)$/i.test(tok) &&
                    tok !== 'data.jsonl' &&
                    serverFileNames.length && !serverFileNames.includes(tok)) {
                    notes.push(tok + ' exists only in this browser — copy it to the server first');
                }
            }
            const suffix = notes.length ? '  ⚠ ' + notes.join('; ') : '';
            // Warning shows SYNCHRONOUSLY — a slow/blocked clipboard
            // must not delay "this file only exists in your browser".
            if (notes.length) setTransientStatus('⚠ ' + notes.join('; '));
            const report = (prefix) => setTransientStatus(prefix + cmd + suffix);
            if (navigator.clipboard && navigator.clipboard.writeText) {
                navigator.clipboard.writeText(cmd).then(
                    () => report('Copied: '),
                    () => report('Clipboard blocked — pipeline: '));
            } else {
                report('Clipboard unavailable — pipeline: ');
            }
        };
        document.getElementById('barCopyCli').addEventListener('click', () => window.exploreCopyCli());

        // Share links: the WHOLE setup (head, ⚡ typed, loaded server
        // files, tail) rides the URL fragment as base64url JSON — the
        // fragment never reaches the server, so page generation stays
        // keyed by ?file/?pipeline. Restore is ONE-WAY at load time:
        // populate the inputs, reload the side files, run head then
        // tail. No live hash sync — that pattern already bit us once
        // (the builder/bar clobber bug).
        let loadedServerFiles = [];
        const b64url = {
            enc: (s2) => btoa(unescape(encodeURIComponent(s2))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, ''),
            dec: (s2) => decodeURIComponent(escape(atob(s2.replace(/-/g, '+').replace(/_/g, '/')))),
        };
        window.exploreShareSetup = function() {
            const setup = {
                h: document.getElementById('headInput').value,
                t: document.getElementById('pipeline').value,
                y: document.getElementById('headTyped').checked,
                f: loadedServerFiles,
            };
            const link = location.origin + location.pathname + location.search + '#s=' + b64url.enc(JSON.stringify(setup));
            window.exploreLastShareUrl = link;
            if (navigator.clipboard && navigator.clipboard.writeText) {
                navigator.clipboard.writeText(link).then(
                    () => setTransientStatus('Share link copied — restores head, files, and tail'),
                    () => setTransientStatus('Clipboard blocked — link: ' + link));
            } else {
                setTransientStatus('Clipboard unavailable — link: ' + link);
            }
        };
        document.getElementById('barShare').addEventListener('click', () => window.exploreShareSetup());
        async function restoreSharedSetup() {
            const m = location.hash.match(/^#s=([A-Za-z0-9_-]+)$/);
            if (!m) return;
            let setup;
            try { setup = JSON.parse(b64url.dec(m[1])); } catch (e) { return; }
            // Inputs first, so a failed run still leaves the setup
            // visible and editable.
            if (setup.h) document.getElementById('headInput').value = setup.h;
            if (typeof setup.y === 'boolean') document.getElementById('headTyped').checked = setup.y;
            if (setup.t) document.getElementById('pipeline').value = setup.t;
            for (const name of setup.f || []) {
                try {
                    const r = await srvFetch()(srvUrl('/api/raw', 'file=' + encodeURIComponent(name)));
                    if (r.ok) exploreLoadServerFile(name, await r.text());
                } catch (e) { /* file load is best-effort on restore */ }
            }
            if (setup.h) await window.exploreRunHead();
            if (setup.t && window.exploreRunBar) window.exploreRunBar();
        }
        // Kick off server-mode detection LAST: its srvtest path runs
        // synchronously to restoreSharedSetup, which needs every const
        // above to be initialized (TDZ — caught by the harness).
        exploreDetectServerMode();

        // Optimizer (DFC065 in the workspace): both buttons preview the
        // rewrite in the output panel with an Apply action — never a
        // silent input rewrite. Tail optimizes IN-BROWSER via the wasm
        // engine (the playground's proven flow); the head goes to
        // /api/optimize (server-side, where ssh pushdown is real).
        function showOptimizeResult(target, original, optimized, rewrites, apply) {
            const lines = [];
            if (!optimized || optimized === original.trim()) {
                showOutput({ stdout: 'No rewrites apply to this pipeline — it is already in optimal form.', stderr: '', exitCode: 0 }, 'Optimize — ' + target);
                return;
            }
            for (const rw of rewrites) lines.push(rw);
            lines.push('');
            lines.push('before: ' + original.trim());
            lines.push('after:  ' + optimized);
            showOutput({ stdout: lines.join('\n'), stderr: '', exitCode: 0 }, 'Optimize — ' + target);
            const out = document.getElementById('output');
            const btn = document.createElement('button');
            btn.textContent = 'Apply to ' + target;
            btn.style.cssText = 'margin-top:8px; padding:4px 14px;';
            btn.addEventListener('click', () => { apply(optimized); out.style.display = 'none'; });
            out.appendChild(btn);
        }
        window.exploreOptimizeTail = function() {
            const ta = document.getElementById('pipeline');
            const text = ta.value.trim().replace(/[|\s]+$/, '');
            if (!text) return;
            const res = executePipeline(text + ' | ssql generate ssql -explain', true);
            if (res.exitCode !== 0) { showOutput(res, 'Optimize failed'); return; }
            const rewrites = (res.stderr || '').split('\n').map(l => l.trim()).filter(l => l.startsWith('['));
            showOptimizeResult('pipeline', text, res.stdout.trim(), rewrites,
                (opt) => { ta.value = opt; });
        };
        window.exploreOptimizeHead = async function() {
            const head = document.getElementById('headInput').value.trim().replace(/[|\s]+$/, '');
            if (!head) return;
            let res;
            try {
                const r = await srvFetch()(srvUrl('/api/optimize'), { method: 'POST', body: JSON.stringify({ pipeline: head }) });
                res = await r.json();
            } catch (e) { setTransientStatus('Optimize failed: ' + e); return; }
            if (res.error) {
                showOutput({ stdout: '', stderr: (res.detail || res.error), exitCode: 1 }, 'Optimize failed');
                return;
            }
            showOptimizeResult('head', head, res.optimized, res.rewrites || [],
                (opt) => { document.getElementById('headInput').value = opt; });
        };
        document.getElementById('barOptimize').addEventListener('click', () => window.exploreOptimizeTail());
        document.getElementById('headOptimize').addEventListener('click', () => window.exploreOptimizeHead());

        document.getElementById('barRun').addEventListener('click', window.exploreRunBar);
        document.getElementById('pipeline').addEventListener('keydown', (e) => {
            // Enter runs; Shift+Enter inserts a newline. The completion
            // binding preventDefaults Enter when it consumes it.
            if (e.key === 'Enter' && !e.shiftKey && !e.defaultPrevented) { e.preventDefault(); window.exploreRunBar(); }
        });
        document.getElementById('barHelp').addEventListener('click', () => helpAtCursor());
        document.getElementById('barReset').addEventListener('click', () => {
            if (window.exploreSetRows) window.exploreSetRows(DATA);
            document.getElementById('pipeline').value = 'ssql from data.jsonl | ';
            document.getElementById('output').style.display = 'none';
        });
        {{end}}

        const { useState, useEffect, useRef, useMemo } = React;

        function App() {
            const [chartType, setChartType] = useState('bar'); // bar: reasonable for categorical AND numeric X (line spaghettis on the default non-numeric X pick)
            const [xField, setXField] = useState(CONFIG.initialXField || (SCHEMA.fields && SCHEMA.fields[0]) || '');
            const [yField, setYField] = useState(CONFIG.initialYField || (SCHEMA.numericFields && SCHEMA.numericFields[0]) || '');
            const [displayData, setDisplayData] = useState(DATA);
            // Seed a default into a BLANK bar only — overwriting
            // existing text clobbered share-link restores (this effect
            // fires on mount AFTER the restore ran).
            useEffect(() => {
                const ta = document.getElementById('pipeline');
                if (ta && !ta.value.trim()) ta.value = 'ssql from data.jsonl | ';
            }, []);
            const [pipelineResult, setPipelineResult] = useState(null);
            const chartRef = useRef(null);
            const gridRef = useRef(null);
            const gridApiRef = useRef(null);
            const gridOpsRef = useRef([]);  // Current grid filter/sort ops
            // Live axis selections for stale-closure callbacks: the
            // exploreSetRows preserve-if-valid check read the FIRST
            // render's xField/yField and clobbered user picks on every
            // head/bar rerun (found by Ross).
            const xFieldRef = useRef(xField);
            const yFieldRef = useRef(yField);
            const chartTypeRef = useRef(chartType);
            useEffect(() => { xFieldRef.current = xField; }, [xField]);
            useEffect(() => { yFieldRef.current = yField; }, [yField]);
            useEffect(() => { chartTypeRef.current = chartType; }, [chartType]);
            // Display sink bridge (DFC119): a decoded trailing
            // to-chart stage drives the panel. applyingSink guards
            // the write-back loop (applying config re-fires the
            // selects' onChange).
            const applyingSink = useRef(false);
            useEffect(() => {
                window.exploreRenderers = window.exploreRenderers || {};
                window.exploreRenderers.chart = (cfg) => {
                    applyingSink.current = true;
                    if (cfg['-type']) setChartType(cfg['-type']);
                    const cx = cfg['-x'], cy = Array.isArray(cfg['-y']) ? cfg['-y'][0] : cfg['-y'];
                    if (cx) setXField(cx);
                    if (cy) setYField(cy);
                    setTimeout(() => { applyingSink.current = false; }, 0);
                };
                return () => { delete window.exploreRenderers.chart; };
            }, []);
            const controlChanged = (flag, value, setter) => {
                setter(value);
                if (applyingSink.current) return;
                if (!window.exploreWriteChartStage) return;
                // Write the FULL panel state (changed flag substituted):
                // the stage must reproduce the chart on screen, not just
                // the last click — a partial stage shares a partial chart.
                const full = {
                    '-type': chartTypeRef.current,
                    '-x': xFieldRef.current,
                    '-y': yFieldRef.current,
                };
                full[flag] = value;
                window.exploreWriteChartStage(full);
            };
            const suppressGridEvents = useRef(false);  // Prevent re-entrant updates

            // Column definitions for AG-Grid
            const columnDefs = useMemo(() => {
                if (!displayData || displayData.length === 0) return [];
                const fields = Object.keys(displayData[0]);
                return fields.map(field => ({
                    field: field,
                    headerName: field,
                    sortable: true,
                    filter: true,
                    resizable: true,
                    minWidth: 100,
                    flex: 1
                }));
            }, [displayData]);

            // Convert AG-Grid filter model to WASM where ops
            const convertFilterModelToOps = (filterModel) => {
                const opMap = {
                    equals: 'eq', notEqual: 'ne',
                    greaterThan: 'gt', greaterThanOrEqual: 'ge',
                    lessThan: 'lt', lessThanOrEqual: 'le',
                    contains: 'contains', startsWith: 'startswith', endsWith: 'endswith'
                };
                const ops = [];
                for (const [field, filter] of Object.entries(filterModel)) {
                    if (filter.operator && filter.conditions) {
                        // Combined filter (AND/OR) — use first condition
                        for (const cond of filter.conditions) {
                            const wasmOp = opMap[cond.type];
                            if (wasmOp && (cond.filter !== undefined && cond.filter !== null)) {
                                ops.push({ op: 'where', field, operator: wasmOp, value: String(cond.filter) });
                            }
                        }
                    } else {
                        const wasmOp = opMap[filter.type];
                        if (wasmOp && (filter.filter !== undefined && filter.filter !== null)) {
                            ops.push({ op: 'where', field, operator: wasmOp, value: String(filter.filter) });
                        }
                    }
                }
                return ops;
            };

            // Convert AG-Grid sort model to WASM sort ops
            const convertSortModelToOps = (colState) => {
                return colState
                    .filter(c => c.sort)
                    .map(c => ({ op: 'sort', field: c.colId, desc: c.sort === 'desc' }));
            };

            // Run combined grid + pipeline ops through WASM
            // DFC118 Phase 2: grid gestures are MODEL EDITS on the bar.
            // A click parses the bar, replaces the grid-owned trailing
            // stages, prints, and reruns — ONE model, ONE run path. The
            // grid only ever removes stages IT appended, only from the
            // tail, and only if the user hasn't edited them (otherwise
            // the whole bar is treated as user-authored base and the
            // new stages simply append).
            const gridAppendedRef = useRef([]); // printed stage texts we own
            const printStage = (st) => window.ssqlUIPrintModel([st]);
            const applyGridOpsToBar = (newGridOps) => {
                gridOpsRef.current = newGridOps;
                if (!ssqlWasm) return; // -light: AG-Grid's local sort/filter only
                const ta = document.getElementById('pipeline');
                if (!ta) return;
                let m = window.ssqlUIParseModel(ta.value.trim() || 'ssql from data.jsonl');
                m = m.filter(st => st.text || (st.argv && st.argv.length));
                // A trailing display sink is CONFIG, not data flow — grid
                // stages insert BEFORE it (sort-after-chart would be a
                // broken pipeline). Detach, edit, reattach.
                let sinkStage = null;
                if (m.length && m[m.length - 1].argv && matchSinkStage(m[m.length - 1].argv)) {
                    sinkStage = m.pop();
                }
                const owned = gridAppendedRef.current;
                while (owned.length && m.length && printStage(m[m.length - 1]) === owned[owned.length - 1]) {
                    m.pop(); owned.pop();
                }
                gridAppendedRef.current = [];
                for (const argv of opsToStages(newGridOps)) {
                    const st = { argv: ['ssql'].includes(argv[0]) ? argv.slice(1) : argv };
                    m.push(st);
                    gridAppendedRef.current.push(printStage(st));
                }
                if (sinkStage) m.push(sinkStage);
                ta.value = window.ssqlUIPrintModel(m);
                window.exploreRunBar();
            };
            window.exploreApplyGridOps = applyGridOpsToBar; // test hook (harness)

            // Grid options
            const gridOptions = useMemo(() => ({
                defaultColDef: {
                    sortable: true,
                    filter: true,
                    resizable: true,
                    minWidth: 100
                },
                pagination: true,
                // Phones: autoHeight — the grid grows to fit its page of
                // rows and the PAGE scrolls (fixed-height mode collapsed
                // the rows viewport to 0px inside the mobile column
                // layout, leaving only the pagination footer visible).
                domLayout: window.matchMedia('(max-width: 768px)').matches ? 'autoHeight' : 'normal',
                paginationPageSize: window.matchMedia('(max-width: 768px)').matches ? 20 : (CONFIG.pageSize || 50),
                paginationPageSizeSelector: [20, 50, 100, 500],
                rowSelection: { mode: 'multiRow' },
                onGridReady: (params) => {
                    gridApiRef.current = params.api;
                    // Pipeline-bar integration: run results replace grid rows.
                    window.exploreSetRows = (rows) => {
                        window.exploreLastRowCount = rows.length;
                        suppressGridEvents.current = true;
                        params.api.setGridOption('rowData', rows);
                        setPipelineResult({ inputCount: DATA.length, outputCount: rows.length });
                        setDisplayData(rows);
                        if (rows.length > 0) {
                            const rf = Object.keys(rows[0]);
                            const numR = rf.filter(f => typeof rows[0][f] === 'number');
                            // Axis guess (Ross's rule): x = first string
                            // field, y = first numeric; ALL-numeric data
                            // takes the first two numerics — never the
                            // same field on both axes (the old fallback
                            // charted salary vs salary).
                            let fx = xFieldRef.current, fy = yFieldRef.current;
                            const nonNum = rf.filter(f => typeof rows[0][f] !== 'number');
                            if (!rf.includes(fx)) {
                                fx = nonNum[0] || numR[0] || rf[0] || '';
                                setXField(fx);
                            }
                            if (!rf.includes(fy) || fy === fx) {
                                fy = numR.find(f => f !== fx) || rf.find(f => f !== fx) || '';
                                setYField(fy);
                            }
                            // Always-written: the bar spells the chart.
                            if (window.exploreSinkAbsent && window.exploreWriteChartStage) {
                                window.exploreSinkAbsent = false;
                                window.exploreWriteChartStage(
                                    { '-type': chartTypeRef.current, '-x': fx, '-y': fy },
                                    { norun: true });
                            }
                        }
                        setTimeout(() => { suppressGridEvents.current = false; }, 0);
                    };
                },
                onFilterChanged: (params) => {
                    if (!ssqlWasm || suppressGridEvents.current) return;
                    const filterModel = params.api.getFilterModel();
                    const filterOps = convertFilterModelToOps(filterModel);
                    const colState = params.api.getColumnState() || [];
                    const sortOps = convertSortModelToOps(colState);
                    applyGridOpsToBar([...filterOps, ...sortOps]);
                },
                onSortChanged: (params) => {
                    if (!ssqlWasm || suppressGridEvents.current) return;
                    const filterModel = params.api.getFilterModel();
                    const filterOps = convertFilterModelToOps(filterModel);
                    const colState = params.api.getColumnState() || [];
                    const sortOps = convertSortModelToOps(colState);
                    applyGridOpsToBar([...filterOps, ...sortOps]);
                }
            }), []);

            // Initialize AG-Grid
            useEffect(() => {
                if (gridRef.current && !gridApiRef.current) {
                    agGrid.createGrid(gridRef.current, {
                        ...gridOptions,
                        columnDefs: columnDefs,
                        rowData: displayData
                    });
                } else if (gridApiRef.current) {
                    gridApiRef.current.setGridOption('columnDefs', columnDefs);
                    gridApiRef.current.setGridOption('rowData', displayData);
                }
            }, [displayData, columnDefs]);

            // Update chart when fields or data change
            useEffect(() => {
                if (!chartRef.current || !displayData || displayData.length === 0) return;

                const xValues = displayData.map(row => row[xField]);
                const yValues = displayData.map(row => {
                    const val = row[yField];
                    return typeof val === 'number' ? val : parseFloat(val) || 0;
                });

                let trace;
                switch (chartType) {
                    case 'bar':
                        trace = {
                            x: xValues,
                            y: yValues,
                            type: 'bar',
                            marker: { color: '#0d6efd' }
                        };
                        break;
                    case 'scatter':
                        trace = {
                            x: xValues,
                            y: yValues,
                            mode: 'markers',
                            type: 'scatter',
                            marker: { color: '#0d6efd', size: 8 }
                        };
                        break;
                    case 'pie':
                        trace = {
                            labels: xValues,
                            values: yValues,
                            type: 'pie'
                        };
                        break;
                    default: // line
                        trace = {
                            x: xValues,
                            y: yValues,
                            type: 'scatter',
                            mode: 'lines+markers',
                            line: { color: '#0d6efd' }
                        };
                }

                // Phones: drag-pan must NOT trap the page scroll — a swipe
                // on the chart should scroll to the table, not pan the plot
                // (taps still show tooltips). Paired with touch-action:
                // pan-y on .chart-area in the mobile CSS.
                const mobileChart = window.matchMedia('(max-width: 768px)').matches;
                const layout = {
                    margin: { t: 30, r: 30, b: 50, l: 60 },
                    dragmode: mobileChart ? false : 'zoom',
                    // automargin: long category labels (kind names, service
                    // paths) grow the axis margin — and auto-rotate — instead
                    // of being clipped by the fixed margins.
                    xaxis: { title: xField, automargin: true },
                    yaxis: { title: yField, automargin: true },
                    paper_bgcolor: 'transparent',
                    plot_bgcolor: 'transparent',
                    font: { color: '{{if eq .Theme "dark"}}#e9ecef{{else}}#212529{{end}}' }
                };

                const config = {
                    responsive: true,
                    displaylogo: false,
                    modeBarButtonsToRemove: ['lasso2d', 'select2d']
                };

                Plotly.react(chartRef.current, [trace], layout, config);
            }, [chartType, xField, yField, displayData]);

            // Pipeline step management


            // Export filtered data as CSV
            const exportCSV = () => {
                if (!displayData || displayData.length === 0) return;

                const headers = Object.keys(displayData[0]);
                const csvContent = [
                    headers.join(','),
                    ...displayData.map(row =>
                        headers.map(h => {
                            const val = row[h];
                            if (val === null || val === undefined) return '';
                            if (typeof val === 'string' && (val.includes(',') || val.includes('"'))) {
                                return '"' + val.replace(/"/g, '""') + '"';
                            }
                            return String(val);
                        }).join(',')
                    )
                ].join('\n');

                const blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8;' });
                const link = document.createElement('a');
                link.href = URL.createObjectURL(blob);
                link.download = 'exported_data.csv';
                link.click();
            };

            // Export chart as PNG
            const exportChart = () => {
                if (chartRef.current) {
                    Plotly.downloadImage(chartRef.current, {
                        format: 'png',
                        width: 1200,
                        height: 600,
                        filename: 'chart'
                    });
                }
            };

            // Get available fields for dropdowns
            const allFields = useMemo(() => {
                if (!displayData || displayData.length === 0) return [];
                return Object.keys(displayData[0]);
            }, [displayData]);

            return React.createElement('div', { className: 'explorer-container' },
                // Main Content
                React.createElement('div', { className: 'main-content' },
                    // Header
                    React.createElement('div', { className: 'header' },
                        React.createElement('h1', null,
                            CONFIG.title || 'Data Explorer',
                            {{if .WasmEnabled}}
                            React.createElement('span', {
                                id: 'wasm-badge',
                                style: { display: 'none', fontSize: '0.5em', marginLeft: '8px', padding: '2px 8px', borderRadius: '4px', background: '#198754', color: 'white', verticalAlign: 'middle' }
                            }, 'WASM')
                            {{end}}
                        ),
                        React.createElement('div', { className: 'btn-group' },
                            React.createElement('button', {
                                className: 'btn',
                                onClick: exportCSV,
                                title: 'Export data as CSV'
                            },
                                React.createElement('i', { className: 'bi bi-download' }),
                                ' CSV'
                            ),
                            React.createElement('button', {
                                className: 'btn',
                                onClick: exportChart,
                                title: 'Export chart as PNG'
                            },
                                React.createElement('i', { className: 'bi bi-image' }),
                                ' PNG'
                            )
                        )
                    ),

                    // Chart Controls
                    React.createElement('div', { className: 'chart-controls' },
                        React.createElement('div', { className: 'control-group' },
                            React.createElement('label', null, 'Chart Type'),
                            React.createElement('select', {
                                id: 'chartTypeSel',
                                value: chartType,
                                onChange: (e) => controlChanged('-type', e.target.value, setChartType)
                            },
                                React.createElement('option', { value: 'line' }, 'Line'),
                                React.createElement('option', { value: 'bar' }, 'Bar'),
                                React.createElement('option', { value: 'scatter' }, 'Scatter'),
                                React.createElement('option', { value: 'pie' }, 'Pie')
                            )
                        ),
                        React.createElement('div', { className: 'control-group' },
                            React.createElement('label', null, 'X-Axis'),
                            React.createElement('select', {
                                id: 'xAxisSel',
                                value: xField,
                                onChange: (e) => controlChanged('-x', e.target.value, setXField)
                            },
                                ...allFields.map(f =>
                                    React.createElement('option', { key: f, value: f }, f)
                                )
                            )
                        ),
                        React.createElement('div', { className: 'control-group' },
                            React.createElement('label', null, 'Y-Axis'),
                            React.createElement('select', {
                                id: 'yAxisSel',
                                value: yField,
                                onChange: (e) => controlChanged('-y', e.target.value, setYField)
                            },
                                ...allFields.map(f =>
                                    React.createElement('option', { key: f, value: f }, f)
                                )
                            )
                        )
                    ),

                    // Chart Area (resizable — a ResizeObserver tells Plotly
                    // to relayout when the user drags the frame)
                    React.createElement('div', {
                        className: 'chart-area',
                        ref: (el) => {
                            chartRef.current = el;
                            if (el && !el.dataset.resizeObserved) {
                                el.dataset.resizeObserved = '1';
                                new ResizeObserver(() => {
                                    if (el.querySelector('.js-plotly-plot')) Plotly.Plots.resize(el);
                                }).observe(el);
                            }
                        }
                    }),

                    // Table Area
                    React.createElement('div', {
                        className: 'table-area',
                        ref: gridRef,
                        style: { width: '100%', height: '100%' }
                    })
                )
            );
        }

        // Mount the app
        const root = ReactDOM.createRoot(document.getElementById('root'));
        root.render(React.createElement(App));
    </script>
</body>
</html>`
