package ssql

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"iter"
	"math"
	"os"
	"path/filepath"
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

	yLabelsJSON, err := json.Marshal(yLabels)
	if err != nil {
		return fmt.Errorf("marshaling y labels: %w", err)
	}

	// Execute template
	tmpl := template.Must(template.New("spectrogram").Parse(spectrogramHTMLTemplate))
	templateData := struct {
		Title      string
		XField     string
		YField     string
		ZField     string
		XLabels    template.JS
		YLabels    template.JS
		GridData   template.JS
		ColorScale string
		ZMin       float64
		ZMax       float64
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
		ZMin:       zMin,
		ZMax:       zMax,
		LogFreq:    config.LogFreq,
		Theme:      config.Theme,
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}

	return writer.Flush()
}

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
                            <label class="form-label mb-1">Z Min: <span id="zMinValue" class="slider-value">{{printf "%.2f" .ZMin}}</span></label>
                            <input type="range" id="zMinSlider" class="form-range range-slider"
                                   min="{{printf "%.2f" .ZMin}}" max="{{printf "%.2f" .ZMax}}"
                                   value="{{printf "%.2f" .ZMin}}" step="0.01" onchange="updateZRange()">
                        </div>

                        <!-- Z Max -->
                        <div class="col-md-2">
                            <label class="form-label mb-1">Z Max: <span id="zMaxValue" class="slider-value">{{printf "%.2f" .ZMax}}</span></label>
                            <input type="range" id="zMaxSlider" class="form-range range-slider"
                                   min="{{printf "%.2f" .ZMin}}" max="{{printf "%.2f" .ZMax}}"
                                   value="{{printf "%.2f" .ZMax}}" step="0.01" onchange="updateZRange()">
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
        const initialZMin = {{printf "%.6f" .ZMin}};
        const initialZMax = {{printf "%.6f" .ZMax}};
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
                tickangle: -45
            },
            yaxis: {
                title: '{{.YField}}',
                type: initialLogFreq ? 'log' : 'linear'
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
            Plotly.relayout('heatmapChart', {'yaxis.type': logFreq ? 'log' : 'linear'});
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
	SsqlWasmJS    string `json:"-"`           // Content of ssql-wasm.js (inlined in HTML)
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
	if len(recordSlice) == 0 {
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
		m := make(map[string]any)
		for k, v := range record.All() {
			m[k] = v
		}
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
		WasmExecJS  template.JS
		SsqlWasmJS  template.JS
	}{
		Title:       config.Title,
		DataJSON:    template.JS(dataJSON),
		SchemaJSON:  template.JS(schemaJSON),
		ConfigJSON:  template.JS(configJSON),
		Theme:       config.Theme,
		WasmEnabled: config.WasmEnabled,
		WasmExecJS:  template.JS(config.WasmExecJS),
		SsqlWasmJS:  template.JS(config.SsqlWasmJS),
	}

	if err := tmpl.Execute(writer, templateData); err != nil {
		return err
	}

	return writer.Flush()
}

// CopyExploreWasmFile copies ssql.wasm to the same directory as the output HTML file.
// The JS runtime files (wasm_exec.js, ssql-wasm.js) are inlined in the HTML template.
func CopyExploreWasmFile(htmlPath string, wasmPath string) error {
	dir := filepath.Dir(htmlPath)

	wasmData, err := os.ReadFile(wasmPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", wasmPath, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ssql.wasm"), wasmData, 0644); err != nil {
		return fmt.Errorf("writing ssql.wasm: %w", err)
	}

	return nil
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
    <link rel="stylesheet" href="https://unpkg.com/ag-grid-community/styles/ag-grid.css">
    <link rel="stylesheet" href="https://unpkg.com/ag-grid-community/styles/ag-theme-alpine.css">
    <script src="https://unpkg.com/ag-grid-community/dist/ag-grid-community.min.noStyle.js"></script>

    <!-- Plotly for charts -->
    <script src="https://cdn.plot.ly/plotly-2.27.0.min.js"></script>

    <!-- Bootstrap -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.10.0/font/bootstrap-icons.css" rel="stylesheet">

    {{if .WasmEnabled}}
    <script>{{.WasmExecJS}}</script>
    <script>{{.SsqlWasmJS}}</script>
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

        .explorer-container {
            display: flex;
            height: 100vh;
        }

        .left-panel {
            width: 280px;
            background: var(--panel-bg);
            border-right: 1px solid var(--border-color);
            overflow-y: auto;
            padding: 16px;
            flex-shrink: 0;
        }

        .main-content {
            flex: 1;
            display: flex;
            flex-direction: column;
            overflow: hidden;
            padding: 16px;
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
            background: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            margin-bottom: 16px;
        }

        .table-area {
            flex: 1;
            min-height: 200px;
        }

        .section-title {
            font-size: 0.875rem;
            font-weight: 600;
            text-transform: uppercase;
            color: {{if eq .Theme "dark"}}#adb5bd{{else}}#6c757d{{end}};
            margin-bottom: 8px;
            padding-bottom: 8px;
            border-bottom: 1px solid var(--border-color);
        }

        .field-list {
            list-style: none;
            padding: 0;
            margin: 0 0 24px 0;
        }

        .field-item {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 6px 8px;
            border-radius: 4px;
            font-size: 0.875rem;
            cursor: pointer;
        }

        .field-item:hover {
            background: var(--hover-bg);
        }

        .field-name {
            font-weight: 500;
        }

        .field-type {
            font-size: 0.75rem;
            color: {{if eq .Theme "dark"}}#6c757d{{else}}#adb5bd{{end}};
            background: {{if eq .Theme "dark"}}#343a40{{else}}#e9ecef{{end}};
            padding: 2px 6px;
            border-radius: 3px;
        }

        .stat-item {
            display: flex;
            justify-content: space-between;
            padding: 4px 0;
            font-size: 0.875rem;
        }

        .stat-label {
            color: {{if eq .Theme "dark"}}#adb5bd{{else}}#6c757d{{end}};
        }

        .stat-value {
            font-weight: 500;
        }

        .agg-panel {
            background: var(--hover-bg);
            border-radius: 8px;
            padding: 12px;
            margin-top: 16px;
        }

        .agg-panel select, .agg-panel button {
            width: 100%;
            margin-bottom: 8px;
            padding: 8px;
            border: 1px solid var(--border-color);
            border-radius: 4px;
            background: var(--panel-bg);
            color: var(--text-color);
        }

        .agg-panel button {
            background: #0d6efd;
            color: white;
            border: none;
            cursor: pointer;
            font-weight: 500;
        }

        .agg-panel button:hover {
            background: #0b5ed7;
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
        .ag-theme-alpine {
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
    <div id="root"></div>

    <script>
        // Data from the pipeline
        const DATA = {{.DataJSON}};
        const SCHEMA = {{.SchemaJSON}};
        const CONFIG = {{.ConfigJSON}};

        // WASM module (initialized asynchronously if enabled)
        let ssqlWasm = null;
        {{if .WasmEnabled}}
        (async function() {
            try {
                ssqlWasm = new SsqlWasm();
                await ssqlWasm.init('./ssql.wasm');
                console.log('ssql WASM module loaded');
                const badge = document.getElementById('wasm-badge');
                if (badge) { badge.style.display = 'inline'; badge.textContent = 'WASM'; }
            } catch(e) {
                console.warn('ssql WASM failed to load, using JS fallback:', e);
                ssqlWasm = null;
            }
        })();
        {{end}}

        const { useState, useEffect, useRef, useMemo } = React;

        function App() {
            const [chartType, setChartType] = useState('line');
            const [xField, setXField] = useState(CONFIG.initialXField || (SCHEMA.fields && SCHEMA.fields[0]) || '');
            const [yField, setYField] = useState(CONFIG.initialYField || (SCHEMA.numericFields && SCHEMA.numericFields[0]) || '');
            const [aggGroupBy, setAggGroupBy] = useState('');
            const [aggFunc, setAggFunc] = useState('count');
            const [aggValueField, setAggValueField] = useState('');
            const [displayData, setDisplayData] = useState(DATA);
            const [isAggregated, setIsAggregated] = useState(false);
            const chartRef = useRef(null);
            const gridRef = useRef(null);
            const gridApiRef = useRef(null);

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

            // Grid options
            const gridOptions = useMemo(() => ({
                defaultColDef: {
                    sortable: true,
                    filter: true,
                    resizable: true,
                    minWidth: 100
                },
                animateRows: true,
                pagination: true,
                paginationPageSize: CONFIG.pageSize || 50,
                paginationPageSizeSelector: [20, 50, 100, 500],
                rowSelection: 'multiple',
                onGridReady: (params) => {
                    gridApiRef.current = params.api;
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

                const layout = {
                    margin: { t: 30, r: 30, b: 50, l: 60 },
                    xaxis: { title: xField },
                    yaxis: { title: yField },
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

            // Apply aggregation (uses WASM when available, JS fallback)
            const applyAggregation = () => {
                if (!aggGroupBy) {
                    setDisplayData(DATA);
                    setIsAggregated(false);
                    return;
                }

                let aggregated;

                if (ssqlWasm) {
                    // Use WASM for aggregation (real ssql GroupBy + Aggregate)
                    try {
                        aggregated = ssqlWasm.groupBy(DATA, aggGroupBy, aggValueField || '', aggFunc);
                    } catch(e) {
                        console.warn('WASM aggregation failed, falling back to JS:', e);
                        aggregated = jsAggregation(DATA, aggGroupBy, aggFunc, aggValueField);
                    }
                } else {
                    aggregated = jsAggregation(DATA, aggGroupBy, aggFunc, aggValueField);
                }

                setDisplayData(aggregated);
                setIsAggregated(true);

                // Update chart fields for aggregated data
                setXField(aggGroupBy);
                setYField(aggFunc === 'count' ? 'count' : aggFunc);
            };

            // JS fallback aggregation
            function jsAggregation(data, groupBy, func_, valueField) {
                const groups = {};
                data.forEach(row => {
                    const key = String(row[groupBy] || '');
                    if (!groups[key]) groups[key] = [];
                    groups[key].push(row);
                });

                return Object.entries(groups).map(([key, rows]) => {
                    const result = { [groupBy]: key };
                    switch (func_) {
                        case 'count':
                            result['count'] = rows.length;
                            break;
                        case 'sum':
                            result['sum'] = rows.reduce((acc, r) => acc + (parseFloat(r[valueField]) || 0), 0);
                            break;
                        case 'avg':
                            const s = rows.reduce((acc, r) => acc + (parseFloat(r[valueField]) || 0), 0);
                            result['avg'] = rows.length > 0 ? s / rows.length : 0;
                            break;
                        case 'min':
                            result['min'] = Math.min(...rows.map(r => parseFloat(r[valueField]) || 0));
                            break;
                        case 'max':
                            result['max'] = Math.max(...rows.map(r => parseFloat(r[valueField]) || 0));
                            break;
                    }
                    return result;
                });
            }

            // Reset to original data
            const resetData = () => {
                setDisplayData(DATA);
                setIsAggregated(false);
                setAggGroupBy('');
                if (SCHEMA.fields && SCHEMA.fields.length > 0) {
                    setXField(SCHEMA.fields[0]);
                }
                if (SCHEMA.numericFields && SCHEMA.numericFields.length > 0) {
                    setYField(SCHEMA.numericFields[0]);
                }
            };

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
                // Left Panel
                React.createElement('div', { className: 'left-panel' },
                    React.createElement('div', { className: 'section-title' }, 'Fields'),
                    React.createElement('ul', { className: 'field-list' },
                        (SCHEMA.fields || []).map(field =>
                            React.createElement('li', {
                                key: field,
                                className: 'field-item',
                                onClick: () => setXField(field)
                            },
                                React.createElement('span', { className: 'field-name' }, field),
                                React.createElement('span', { className: 'field-type' },
                                    SCHEMA.summary.fieldTypes[field] || 'string'
                                )
                            )
                        )
                    ),

                    React.createElement('div', { className: 'section-title' }, 'Statistics'),
                    React.createElement('div', null,
                        React.createElement('div', { className: 'stat-item' },
                            React.createElement('span', { className: 'stat-label' }, 'Records'),
                            React.createElement('span', { className: 'stat-value' }, displayData.length.toLocaleString())
                        ),
                        React.createElement('div', { className: 'stat-item' },
                            React.createElement('span', { className: 'stat-label' }, 'Fields'),
                            React.createElement('span', { className: 'stat-value' }, allFields.length)
                        ),
                        React.createElement('div', { className: 'stat-item' },
                            React.createElement('span', { className: 'stat-label' }, 'Numeric'),
                            React.createElement('span', { className: 'stat-value' }, (SCHEMA.numericFields || []).length)
                        )
                    ),

                    React.createElement('div', { className: 'section-title', style: { marginTop: '24px' } }, 'Aggregation'),
                    React.createElement('div', { className: 'agg-panel' },
                        React.createElement('select', {
                            value: aggGroupBy,
                            onChange: (e) => setAggGroupBy(e.target.value)
                        },
                            React.createElement('option', { value: '' }, '-- Group By --'),
                            ...(SCHEMA.fields || []).map(f =>
                                React.createElement('option', { key: f, value: f }, f)
                            )
                        ),
                        React.createElement('select', {
                            value: aggFunc,
                            onChange: (e) => setAggFunc(e.target.value)
                        },
                            React.createElement('option', { value: 'count' }, 'Count'),
                            React.createElement('option', { value: 'sum' }, 'Sum'),
                            React.createElement('option', { value: 'avg' }, 'Average'),
                            React.createElement('option', { value: 'min' }, 'Min'),
                            React.createElement('option', { value: 'max' }, 'Max')
                        ),
                        aggFunc !== 'count' && React.createElement('select', {
                            value: aggValueField,
                            onChange: (e) => setAggValueField(e.target.value)
                        },
                            React.createElement('option', { value: '' }, '-- Value Field --'),
                            ...(SCHEMA.numericFields || []).map(f =>
                                React.createElement('option', { key: f, value: f }, f)
                            )
                        ),
                        React.createElement('button', { onClick: applyAggregation }, 'Apply'),
                        isAggregated && React.createElement('button', {
                            onClick: resetData,
                            style: { background: '#6c757d' }
                        }, 'Reset')
                    )
                ),

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
                                value: chartType,
                                onChange: (e) => setChartType(e.target.value)
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
                                value: xField,
                                onChange: (e) => setXField(e.target.value)
                            },
                                ...allFields.map(f =>
                                    React.createElement('option', { key: f, value: f }, f)
                                )
                            )
                        ),
                        React.createElement('div', { className: 'control-group' },
                            React.createElement('label', null, 'Y-Axis'),
                            React.createElement('select', {
                                value: yField,
                                onChange: (e) => setYField(e.target.value)
                            },
                                ...allFields.map(f =>
                                    React.createElement('option', { key: f, value: f }, f)
                                )
                            )
                        )
                    ),

                    // Chart Area
                    React.createElement('div', {
                        className: 'chart-area',
                        ref: chartRef
                    }),

                    // Table Area
                    React.createElement('div', {
                        className: 'table-area ag-theme-alpine',
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
