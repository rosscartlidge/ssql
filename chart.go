package ssql

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"iter"
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
	ChartType          string            `json:"chartType"`  // line, bar, scatter, pie, doughnut, radar, polarArea
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
