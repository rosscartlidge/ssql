package ssql

import (
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// CHART CONFIG TESTS
// ============================================================================

func TestDefaultChartConfig(t *testing.T) {
	config := DefaultChartConfig()

	if config.ChartType != "line" {
		t.Errorf("Default chart type should be 'line', got %s", config.ChartType)
	}

	if config.Title != "Data Visualization" {
		t.Errorf("Default title should be 'Data Visualization', got %s", config.Title)
	}

	if config.Width != 1200 {
		t.Errorf("Default width should be 1200, got %d", config.Width)
	}

	if config.Height != 600 {
		t.Errorf("Default height should be 600, got %d", config.Height)
	}

	if !config.EnableZoom {
		t.Error("Default EnableZoom should be true")
	}
}

// ============================================================================
// INTERACTIVE CHART TESTS
// ============================================================================

func TestInteractiveChart(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test_chart.html")

	// Create test data
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(1)
			r.fields["y"] = int64(10)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(2)
			r.fields["y"] = int64(20)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(3)
			r.fields["y"] = int64(15)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()

	err := InteractiveChart(input, filename, config)
	if err != nil {
		t.Fatalf("InteractiveChart failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Chart file was not created")
	}

	// Read the file and check for basic content
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)

	// Check for essential HTML elements
	if !strings.Contains(htmlContent, "<html") {
		t.Error("Chart should contain HTML tag")
	}
	if !strings.Contains(htmlContent, "chart") {
		t.Error("Chart should include chart-related content")
	}
	if !strings.Contains(htmlContent, "canvas") {
		t.Error("Chart should contain canvas element")
	}
}

func TestInteractiveChartCustomConfig(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "custom_chart.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["category"] = "A"
			r.fields["value"] = int64(100)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["category"] = "B"
			r.fields["value"] = int64(200)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["category"] = "C"
			r.fields["value"] = int64(150)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()
	config.ChartType = "bar"
	config.Title = "Custom Bar Chart"

	err := InteractiveChart(input, filename, config)
	if err != nil {
		t.Fatalf("InteractiveChart with custom config failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Custom chart file was not created")
	}

	// Read and verify content
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)

	if !strings.Contains(htmlContent, "Custom Bar Chart") {
		t.Error("Chart should contain custom title")
	}
}

func TestInteractiveChartMultipleYFields(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "multi_series.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["month"] = "Jan"
			r.fields["sales"] = int64(1000)
			r.fields["profit"] = int64(200)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["month"] = "Feb"
			r.fields["sales"] = int64(1200)
			r.fields["profit"] = int64(300)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["month"] = "Mar"
			r.fields["sales"] = int64(1100)
			r.fields["profit"] = int64(250)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()

	err := InteractiveChart(input, filename, config)
	if err != nil {
		t.Fatalf("InteractiveChart with multiple Y fields failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Multi-series chart file was not created")
	}
}

// ============================================================================
// QUICKCHART TESTS
// ============================================================================

func TestQuickChart(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "quick_chart.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(1)
			r.fields["y"] = int64(10)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(2)
			r.fields["y"] = int64(20)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(3)
			r.fields["y"] = int64(15)
			return r.Freeze()
		}(),
	})

	err := QuickChart(input, "x", "y", filename)
	if err != nil {
		t.Fatalf("QuickChart failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("QuickChart file was not created")
	}

	// Read and verify basic content
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)

	if !strings.Contains(htmlContent, "canvas") {
		t.Error("QuickChart should contain canvas element")
	}
}

func TestQuickChartFloatValues(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "float_chart.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["time"] = 1.0
			r.fields["value"] = 10.5
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["time"] = 2.0
			r.fields["value"] = 20.3
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["time"] = 3.0
			r.fields["value"] = 15.7
			return r.Freeze()
		}(),
	})

	err := QuickChart(input, "time", "value", filename)
	if err != nil {
		t.Fatalf("QuickChart with float values failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Float chart file was not created")
	}
}

// ============================================================================
// TIMESERIES CHART TESTS
// ============================================================================

func TestTimeSeriesChart(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "timeseries.html")

	now := time.Now()
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["timestamp"] = now
			r.fields["value"] = int64(100)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["timestamp"] = now.Add(1 * time.Hour)
			r.fields["value"] = int64(150)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["timestamp"] = now.Add(2 * time.Hour)
			r.fields["value"] = int64(120)
			return r.Freeze()
		}(),
	})

	err := TimeSeriesChart(input, "timestamp", []string{"value"}, filename)
	if err != nil {
		t.Fatalf("TimeSeriesChart failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("TimeSeriesChart file was not created")
	}

	// Read and verify content
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)

	if !strings.Contains(htmlContent, "canvas") {
		t.Error("TimeSeriesChart should contain canvas element")
	}
}

func TestTimeSeriesChartMultipleValues(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "multi_timeseries.html")

	now := time.Now()
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["time"] = now
			r.fields["temp"] = int64(20)
			r.fields["humidity"] = int64(60)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["time"] = now.Add(1 * time.Hour)
			r.fields["temp"] = int64(22)
			r.fields["humidity"] = int64(65)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["time"] = now.Add(2 * time.Hour)
			r.fields["temp"] = int64(21)
			r.fields["humidity"] = int64(63)
			return r.Freeze()
		}(),
	})

	err := TimeSeriesChart(input, "time", []string{"temp", "humidity"}, filename)
	if err != nil {
		t.Fatalf("TimeSeriesChart with multiple values failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Multi-value timeseries chart file was not created")
	}
}

func TestTimeSeriesChartWithConfig(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "custom_timeseries.html")

	now := time.Now()
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["date"] = now
			r.fields["sales"] = int64(1000)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["date"] = now.Add(24 * time.Hour)
			r.fields["sales"] = int64(1200)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()
	config.Title = "Daily Sales"
	config.ChartType = "bar"

	err := TimeSeriesChart(input, "date", []string{"sales"}, filename, config)
	if err != nil {
		t.Fatalf("TimeSeriesChart with config failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Custom timeseries chart file was not created")
	}

	// Verify custom title is in the file
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	if !strings.Contains(string(content), "Daily Sales") {
		t.Error("Chart should contain custom title 'Daily Sales'")
	}
}

// ============================================================================
// ERROR HANDLING TESTS
// ============================================================================

func TestInteractiveChartInvalidPath(t *testing.T) {
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(1)
			r.fields["y"] = int64(10)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()

	err := InteractiveChart(input, "/invalid/path/chart.html", config)
	if err == nil {
		t.Error("InteractiveChart should return error for invalid path")
	}
}

func TestQuickChartEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "empty_chart.html")

	empty := func(yield func(Record) bool) {
		// Yield nothing
	}

	err := QuickChart(iter.Seq[Record](empty), "x", "y", filename)
	// Should handle empty data gracefully - may succeed or fail depending on implementation
	// Just ensure it doesn't panic
	if err != nil {
		t.Logf("QuickChart with empty data returned error: %v", err)
	}
}

func TestTimeSeriesChartEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "empty_timeseries.html")

	empty := func(yield func(Record) bool) {
		// Yield nothing
	}

	err := TimeSeriesChart(iter.Seq[Record](empty), "time", []string{"value"}, filename)
	// Should handle empty data gracefully
	if err != nil {
		t.Logf("TimeSeriesChart with empty data returned error: %v", err)
	}
}

// ============================================================================
// INTEGRATION TESTS
// ============================================================================

func TestChartPipeline(t *testing.T) {
	tmpDir := t.TempDir()
	chartFile := filepath.Join(tmpDir, "pipeline_chart.html")

	// Create data, filter it, and chart it
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(1)
			r.fields["y"] = int64(10)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(2)
			r.fields["y"] = int64(5)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(3)
			r.fields["y"] = int64(20)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(4)
			r.fields["y"] = int64(8)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(5)
			r.fields["y"] = int64(25)
			return r.Freeze()
		}(),
	})

	// Filter: only values where y > 10
	filtered := Where(func(r Record) bool {
		y := GetOr(r, "y", int64(0))
		return y > 10
	})(input)

	err := QuickChart(filtered, "x", "y", chartFile)
	if err != nil {
		t.Fatalf("Chart pipeline failed: %v", err)
	}

	if _, err := os.Stat(chartFile); os.IsNotExist(err) {
		t.Error("Pipeline chart file was not created")
	}
}

func TestChartWithGroupedData(t *testing.T) {
	tmpDir := t.TempDir()
	chartFile := filepath.Join(tmpDir, "grouped_chart.html")

	// Create sales data
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["dept"] = "Eng"
			r.fields["month"] = "Jan"
			r.fields["sales"] = int64(1000)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["dept"] = "Eng"
			r.fields["month"] = "Feb"
			r.fields["sales"] = int64(1200)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["dept"] = "Sales"
			r.fields["month"] = "Jan"
			r.fields["sales"] = int64(800)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["dept"] = "Sales"
			r.fields["month"] = "Feb"
			r.fields["sales"] = int64(900)
			return r.Freeze()
		}(),
	})

	// Group by department and aggregate using Chain instead of Pipe
	grouped := Chain(
		GroupByFields("records", "dept"),
		Aggregate("records", map[string]AggregateFunc{
			"total_sales": Sum("sales"),
		}),
	)(input)

	err := QuickChart(grouped, "dept", "total_sales", chartFile)
	if err != nil {
		t.Fatalf("Grouped chart failed: %v", err)
	}

	if _, err := os.Stat(chartFile); os.IsNotExist(err) {
		t.Error("Grouped chart file was not created")
	}
}

// ============================================================================
// CHART TYPE TESTS
// ============================================================================

func TestDifferentChartTypes(t *testing.T) {
	tmpDir := t.TempDir()

	chartTypes := []string{"line", "bar", "scatter", "pie", "radar"}

	for _, chartType := range chartTypes {
		t.Run(chartType, func(t *testing.T) {
			filename := filepath.Join(tmpDir, chartType+"_chart.html")

			config := DefaultChartConfig()
			config.ChartType = chartType

			// Need to re-collect input for each test since it's consumed
			testInput := slices.Values([]Record{
				func() Record {
					r := MakeMutableRecord()
					r.fields["category"] = "A"
					r.fields["value"] = int64(10)
					return r.Freeze()
				}(),
				func() Record {
					r := MakeMutableRecord()
					r.fields["category"] = "B"
					r.fields["value"] = int64(20)
					return r.Freeze()
				}(),
				func() Record {
					r := MakeMutableRecord()
					r.fields["category"] = "C"
					r.fields["value"] = int64(15)
					return r.Freeze()
				}(),
			})

			err := InteractiveChart(testInput, filename, config)
			if err != nil {
				t.Fatalf("%s chart failed: %v", chartType, err)
			}

			if _, err := os.Stat(filename); os.IsNotExist(err) {
				t.Errorf("%s chart file was not created", chartType)
			}
		})
	}
}

func TestChartWithStringValues(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "string_chart.html")

	// CSV data often comes in as strings
	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = "1"
			r.fields["y"] = "10"
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = "2"
			r.fields["y"] = "20"
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = "3"
			r.fields["y"] = "15"
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()

	err := InteractiveChart(input, filename, config)
	if err != nil {
		t.Fatalf("Chart with string values failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("String chart file was not created")
	}
}

func TestChartConfigOptions(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "options_chart.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(1)
			r.fields["y"] = int64(10)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(2)
			r.fields["y"] = int64(20)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()
	config.Title = "Test Options"
	config.Width = 1024
	config.Height = 768
	config.EnableZoom = false
	config.ShowLegend = false

	err := InteractiveChart(input, filename, config)
	if err != nil {
		t.Fatalf("Chart with custom options failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Options chart file was not created")
	}

	// Verify custom settings in file
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)
	if !strings.Contains(htmlContent, "Test Options") {
		t.Error("Chart should contain custom title")
	}
}

// ============================================================================
// ENHANCED CHART TESTS
// ============================================================================

func TestEnhancedChartMultiSeries(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "enhanced_multi_series.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["month"] = "Jan"
			r.fields["sales"] = int64(1000)
			r.fields["profit"] = int64(200)
			r.fields["costs"] = int64(800)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["month"] = "Feb"
			r.fields["sales"] = int64(1200)
			r.fields["profit"] = int64(350)
			r.fields["costs"] = int64(850)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["month"] = "Mar"
			r.fields["sales"] = int64(1100)
			r.fields["profit"] = int64(280)
			r.fields["costs"] = int64(820)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()
	config.Title = "Multi-Series Test"
	config.XField = "month"
	config.YFields = []string{"sales", "profit", "costs"}

	err := EnhancedChart(input, config, filename)
	if err != nil {
		t.Fatalf("EnhancedChart multi-series failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Multi-series chart file was not created")
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)
	if !strings.Contains(htmlContent, "Multi-Series Test") {
		t.Error("Chart should contain title")
	}
	if !strings.Contains(htmlContent, "canvas") {
		t.Error("Chart should contain canvas element")
	}
}

func TestEnhancedChartHeatmap(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "heatmap.html")

	// Create spectrogram-like data
	var records []Record
	for x := 0; x < 10; x++ {
		for y := 0; y < 5; y++ {
			r := MakeMutableRecord()
			r.fields["time"] = float64(x) * 0.1
			r.fields["frequency"] = float64(y) * 100
			r.fields["magnitude"] = float64((x + y) * 10)
			records = append(records, r.Freeze())
		}
	}

	input := slices.Values(records)

	config := DefaultChartConfig()
	config.Title = "Heatmap Test"
	config.ChartType = "heatmap"
	config.XField = "time"
	config.YFields = []string{"frequency"}
	config.ZField = "magnitude"
	config.ColorScale = "viridis"

	err := EnhancedChart(input, config, filename)
	if err != nil {
		t.Fatalf("EnhancedChart heatmap failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Heatmap file was not created")
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read heatmap file: %v", err)
	}

	htmlContent := string(content)
	if !strings.Contains(htmlContent, "Plotly") {
		t.Error("Heatmap should use Plotly.js")
	}
	if !strings.Contains(htmlContent, "heatmap") {
		t.Error("Heatmap should contain heatmap type")
	}
	if !strings.Contains(htmlContent, "Viridis") && !strings.Contains(htmlContent, "viridis") {
		t.Error("Heatmap should contain color scale")
	}
}

func TestEnhancedChartLogarithmicAxes(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "log_axes.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["frequency"] = int64(10)
			r.fields["magnitude"] = int64(100)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["frequency"] = int64(100)
			r.fields["magnitude"] = int64(10)
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["frequency"] = int64(1000)
			r.fields["magnitude"] = int64(1)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()
	config.Title = "Logarithmic Test"
	config.XField = "frequency"
	config.YFields = []string{"magnitude"}
	config.XAxisType = "logarithmic"
	config.YAxisType = "logarithmic"

	err := EnhancedChart(input, config, filename)
	if err != nil {
		t.Fatalf("EnhancedChart log axes failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Log axes chart file was not created")
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)
	if !strings.Contains(htmlContent, "logarithmic") {
		t.Error("Chart should contain logarithmic axis configuration")
	}
}

func TestEnhancedChartColorByField(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "color_by_field.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["age"] = int64(25)
			r.fields["income"] = int64(50000)
			r.fields["region"] = "North"
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["age"] = int64(35)
			r.fields["income"] = int64(75000)
			r.fields["region"] = "South"
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["age"] = int64(45)
			r.fields["income"] = int64(90000)
			r.fields["region"] = "North"
			return r.Freeze()
		}(),
		func() Record {
			r := MakeMutableRecord()
			r.fields["age"] = int64(30)
			r.fields["income"] = int64(60000)
			r.fields["region"] = "West"
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()
	config.Title = "Color By Field Test"
	config.ChartType = "scatter"
	config.XField = "age"
	config.YFields = []string{"income"}
	config.ColorField = "region"

	err := EnhancedChart(input, config, filename)
	if err != nil {
		t.Fatalf("EnhancedChart color by field failed: %v", err)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Color by field chart was not created")
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read chart file: %v", err)
	}

	htmlContent := string(content)
	if !strings.Contains(htmlContent, "colorField") {
		t.Error("Chart should contain colorField configuration")
	}
}

func TestHeatmapMissingZField(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "missing_z.html")

	input := slices.Values([]Record{
		func() Record {
			r := MakeMutableRecord()
			r.fields["x"] = int64(1)
			r.fields["y"] = int64(2)
			return r.Freeze()
		}(),
	})

	config := DefaultChartConfig()
	config.ChartType = "heatmap"
	config.XField = "x"
	config.YFields = []string{"y"}
	// ZField intentionally not set

	err := EnhancedChart(input, config, filename)
	if err == nil {
		t.Error("Heatmap without ZField should return error")
	}
	if err != nil && !strings.Contains(err.Error(), "Z-axis") {
		t.Errorf("Error should mention Z-axis, got: %v", err)
	}
}

func TestEnhancedChartEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "empty_enhanced.html")

	empty := func(yield func(Record) bool) {
		// Yield nothing
	}

	config := DefaultChartConfig()
	config.XField = "x"
	config.YFields = []string{"y"}

	err := EnhancedChart(iter.Seq[Record](empty), config, filename)
	if err == nil {
		t.Error("EnhancedChart with empty data should return error")
	}
}
