# The Go Library, by Example

ssql is a Go library first; the CLI is a thin layer over it and `generate go` emits programs that call it. This tour shows the `Record` API one capability at a time. The [Getting Started Guide](codelab-intro.md) is the tutorial; the [API Reference](api-reference.md) is complete.

[Back to Documentation](README.md)

## A first pipeline

**Go Library:**
```go
// Read data, filter, group, and visualize - all type-safe
sales, err := ssql.ReadCSV("sales.csv")
if err != nil {
    log.Fatal(err)
}

topRegions := ssql.Chain(
    ssql.GroupByFields("sales", "region"),
    ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
        "total_revenue": ssql.Sum("amount"),
    }),
    ssql.SortBy(func(r ssql.Record) float64 {
        return -ssql.GetOr(r, "total_revenue", 0.0) // Descending
    }),
    ssql.Limit[ssql.Record](5),
)(sales)

ssql.QuickChart(topRegions, "region", "total_revenue", "top_regions.html")
```

<details>
<summary>💡 <b>Click for complete, runnable code with sample data</b></summary>

```go
package main

import (
    "log"
    "os"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Create sample sales data in /tmp/sales.csv
    csvData := `region,product,amount
North,Widget,1500
South,Gadget,2300
East,Widget,1800
West,Gadget,2100
North,Gadget,3200
South,Widget,1200
East,Gadget,2800
West,Widget,1600
North,Widget,2500
South,Gadget,1900
East,Widget,2200
West,Gadget,3100`

    if err := os.WriteFile("/tmp/sales.csv", []byte(csvData), 0644); err != nil {
        log.Fatalf("Failed to create sample data: %v", err)
    }

    // Read data, filter, group, and visualize - all type-safe
    sales, err := ssql.ReadCSV("/tmp/sales.csv")
    if err != nil {
        log.Fatal(err)
    }

    topRegions := ssql.Chain(
        ssql.GroupByFields("sales", "region"),
        ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
            "total_revenue": ssql.Sum("amount"),
        }),
        ssql.SortBy(func(r ssql.Record) float64 {
            return -ssql.GetOr(r, "total_revenue", 0.0) // Descending
        }),
        ssql.Limit[ssql.Record](5),
    )(sales)

    if err := ssql.QuickChart(topRegions, "region", "total_revenue", "/tmp/top_regions.html"); err != nil {
        log.Fatalf("Failed to create chart: %v", err)
    }

    log.Println("Chart created: /tmp/top_regions.html")
    log.Println("Sample data: /tmp/sales.csv")
}
```

</details>

**Or use the CLI:**

## Core capabilities

### SQL-Style Data Processing

**Quick view:
```go
// Group sales by region, calculate totals, get top 5
topRegions := ssql.Chain(
    ssql.GroupByFields("sales", "region"),
    ssql.Aggregate("sales", aggregations),
    ssql.SortBy(keyFunc),
    ssql.Limit[ssql.Record](5),
)(salesData)
```

<details>
<summary>📋 <b>Click for complete, runnable code</b></summary>

```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read sales data
    salesData, err := ssql.ReadCSV("sales.csv")
    if err != nil {
        log.Fatal(err)
    }

    // Define aggregations
    aggregations := map[string]ssql.AggregateFunc{
        "total_revenue": ssql.Sum("amount"),
        "sale_count":    ssql.Count(),
    }

    // Define sort key function
    keyFunc := func(r ssql.Record) float64 {
        return -ssql.GetOr(r, "total_revenue", 0.0) // Negative for descending
    }

    // Group sales by region, calculate totals, get top 5
    topRegions := ssql.Chain(
        ssql.GroupByFields("sales", "region"),
        ssql.Aggregate("sales", aggregations),
        ssql.SortBy(keyFunc),
        ssql.Limit[ssql.Record](5),
    )(salesData)

    // Display results
    fmt.Println("Top 5 Regions by Revenue:")
    for region := range topRegions {
        name := ssql.GetOr(region, "region", "")
        revenue := ssql.GetOr(region, "total_revenue", 0.0)
        count := ssql.GetOr(region, "sale_count", int64(0))
        fmt.Printf("%s: $%.2f (%d sales)\n", name, revenue, count)
    }
}
```

</details>

### Real-Time Stream Processing

**Quick view:
```go
// Process sensor data in 5-minute windows
windowed := ssql.TimeWindow[ssql.Record](5*time.Minute, "timestamp")(sensorStream)
for window := range windowed {
    // Analyze each time window
}
```

<details>
<summary>📋 <b>Click for complete, runnable code</b></summary>

```go
package main

import (
    "fmt"
    "log"
    "time"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read sensor data
    sensorStream, err := ssql.ReadCSV("sensor_data.csv")
    if err != nil {
        log.Fatal(err)
    }

    // Process sensor data in 5-minute windows
    windowed := ssql.TimeWindow[ssql.Record](5*time.Minute, "timestamp")(sensorStream)

    fmt.Println("Processing 5-minute windows:")
    for window := range windowed {
        // Analyze each time window
        count := len(window)

        // Calculate average temperature
        var totalTemp float64
        for _, record := range window {
            temp := ssql.GetOr(record, "temperature", 0.0)
            totalTemp += temp
        }
        avgTemp := totalTemp / float64(count)

        fmt.Printf("Window: %d readings, avg temp: %.2f°C\n", count, avgTemp)
    }
}
```

</details>

### Interactive Dashboards

**Quick view:
```go
config := ssql.DefaultChartConfig()
config.Title = "Sales Dashboard"
config.ChartType = "line"
ssql.InteractiveChart(data, "dashboard.html", config)
```

<details>
<summary>📋 <b>Click for complete, runnable code</b></summary>

```go
package main

import (
    "log"
    "slices"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Create sample sales data
    salesData := []ssql.Record{
        ssql.MakeMutableRecord().String("month", "Jan").Float("revenue", 120000).Freeze(),
        ssql.MakeMutableRecord().String("month", "Feb").Float("revenue", 135000).Freeze(),
        ssql.MakeMutableRecord().String("month", "Mar").Float("revenue", 145000).Freeze(),
        ssql.MakeMutableRecord().String("month", "Apr").Float("revenue", 132000).Freeze(),
    }

    data := slices.Values(salesData)

    // Create interactive dashboard
    config := ssql.DefaultChartConfig()
    config.Title = "Sales Dashboard"
    config.ChartType = "line"
    config.Width = 1200
    config.Height = 600
    config.EnableZoom = true
    config.EnablePan = true

    err := ssql.InteractiveChart(data, "dashboard.html", config)
    if err != nil {
        log.Fatalf("Failed to create chart: %v", err)
    }

    log.Println("Dashboard created: dashboard.html")
}
```

</details>

### Signal Processing

**Quick view:
```go
// FFT analysis, filtering, and reconstruction
spectrum, _ := ssql.FFTWithPhase(signal)
reconstructed, _ := ssql.IFFT(spectrum.Magnitude, spectrum.Phase)
smoothed, _ := ssql.Convolve(signal, ssql.GaussianKernel(11, 2.0))
corr, _ := ssql.Correlate(signal1, signal2)  // Find pattern matches
```

<details>
<summary>📋 <b>Click for complete, runnable code</b></summary>

```go
package main

import (
    "fmt"
    "math"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Create sample signal: 10Hz + 25Hz sine waves
    sampleRate := 100.0 // 100 samples per second
    signal := make(ssql.Signal, 256)
    for i := range signal {
        t := float64(i) / sampleRate
        signal[i] = math.Sin(2*math.Pi*10*t) + 0.5*math.Sin(2*math.Pi*25*t)
    }

    // FFT to find frequency components
    spectrum, err := ssql.FFT(signal)
    if err != nil {
        panic(err)
    }

    // Find peak frequencies
    fmt.Println("Top frequencies:")
    for i, mag := range spectrum.Magnitude {
        if mag > 50 { // Threshold for significant peaks
            freq := spectrum.FrequencyBin(i, sampleRate)
            fmt.Printf("  %.1f Hz: magnitude %.1f\n", freq, mag)
        }
    }

    // Smooth with Gaussian kernel
    smoothed, err := ssql.ConvolveSame(signal, ssql.GaussianKernel(11, 2.0))
    if err != nil {
        panic(err)
    }
    fmt.Printf("\nSmoothed signal: %d points\n", len(smoothed))
}
```

</details>

**CLI Usage:
```bash
# FFT analysis
ssql from audio.csv | ssql fft -field amplitude -rate 44100 | ssql to table

# Inverse FFT for signal reconstruction
ssql from spectrum.csv | ssql ifft -magnitude mag -phase phase | ssql to csv filtered.csv

# Smoothing with convolution
ssql from sensor.csv | ssql convolve -field reading -kernel gaussian -size 11 -same

# Cross-correlation to find patterns
ssql from signal.csv | ssql correlate -field reading -with template.csv
```

**Features:
- **FFT/IFFT** - Forward and inverse FFT for frequency analysis and signal reconstruction
- **Convolution** - Signal filtering with built-in kernels (avg, gaussian, diff, laplacian, sobel)
- **Correlation** - Cross-correlation and autocorrelation for pattern matching
- **Pipeline Integration** - Works with ssql's record-based pipelines
- **Works everywhere** - CPU implementations included, no special setup required

**GPU Acceleration (optional):
Signal processing works out of the box using CPU. For large datasets, optional CUDA GPU acceleration provides 10-100x speedup. See [GPU installation instructions](install.md#option-5-gpu-acceleration-optional) for setup via Docker (recommended) or local CUDA toolkit.

GPU is used automatically when available for FFT >= 1024 points or convolution kernels >= 64 points.

### Data Integration

**Quick view:
```go
// Join customer and order data
customerOrders := ssql.InnerJoin(
    orderStream,
    ssql.OnFields("customer_id")
)(customerStream)
```

<details>
<summary>📋 <b>Click for complete, runnable code</b></summary>

```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read customer data
    customerStream, err := ssql.ReadCSV("customers.csv")
    if err != nil {
        log.Fatal(err)
    }

    // Read order data
    orderStream, err := ssql.ReadCSV("orders.csv")
    if err != nil {
        log.Fatal(err)
    }

    // Join customer and order data
    customerOrders := ssql.InnerJoin(
        orderStream,
        ssql.OnFields("customer_id"),
    )(customerStream)

    // Display joined results
    fmt.Println("Customer Orders:")
    for record := range customerOrders {
        custName := ssql.GetOr(record, "customer_name", "")
        orderID := ssql.GetOr(record, "order_id", "")
        amount := ssql.GetOr(record, "amount", 0.0)
        fmt.Printf("%s - Order %s: $%.2f\n", custName, orderID, amount)
    }
}
```

</details>

### Distributed Processing

**Quick view:
```bash
# Read a remote file via SSH with push-down filtering
ssql from ssh myserver /data/events.csv -- where -if status eq error | ssql to table

# Read multiple shards from a catalog CSV with partition pruning
ssql from catalog shards.csv -if date ge 2025-03-01 | ssql group-by service -count n
```

<details>
<summary>Click for more examples</summary>

```bash
# Multi-step push-down: filter and aggregate on each remote shard
ssql from ssh myserver /data/events.csv \
  -- where -if status ge 400 + group-by service -count cnt | \
  ssql to table

# Catalog with range pruning and two-level aggregation
ssql from catalog shards.csv -if date ge 2025-02-01 \
  -- where -if status ge 400 + group-by service -count cnt | \
  ssql group-by service -sum cnt total_errors | \
  ssql to table

# Add provenance to track which shard each record came from
ssql from catalog shards.csv -shard-field _shard | ssql to table

# Use ssql_gpu on remote hosts
ssql from ssh myserver /data/events.csv -gpu | ssql to table
```

**Features:
- **`from ssh`** - Read remote files via SSH, push-down filters to reduce transfer
- **`from catalog`** - Read multiple shards from a catalog CSV mapping hosts to file paths
- **Partition pruning** - Skip irrelevant shards using range (`X_from`/`X_to`) or exact-value metadata
- **Push-down** - Send filter and aggregation stages to remote hosts with `--` separator
- **Local shards** - Catalog entries with `host=local` or `host=localhost` are read directly
- **Code generation** - `from ssh` supports `-generate` / `SSQL_MODE=record`
- **Pipeline optimizer** - `generate ssql` automatically pushes filters into SSH/catalog, collapses sort+limit to top, prunes Parquet columns, and more (12 optimization rules)

</details>

### Expression Support

**Quick view:
```bash
# Calculate derived fields with expressions
ssql update -set-expr total 'price * qty'
ssql update -set-expr tier 'revenue > 10000 ? "gold" : "silver"'

# Complex filtering with boolean expressions
ssql where -if-expr 'age >= 18 and status == "active"'
```

<details>
<summary>📋 <b>Click for complete, runnable code and features</b></summary>

ssql supports powerful expression evaluation for computed fields and complex filters using the [expr-lang](https://expr-lang.org/) library.

**CLI Examples:
```bash
# Calculated fields
echo 'name,price,qty
Widget,10.50,3
Gadget,25.00,2' | ssql from | \
  ssql update -set-expr total 'price * qty' | \
  ssql update -set-expr discount 'total > 50 ? total * 0.1 : 0'

# Complex filtering
echo 'name,age,email,status
Alice,30,alice@example.com,active
Bob,17,bob@example.com,pending
Carol,25,carol@example.com,active' | ssql from | \
  ssql where -if-expr 'age >= 18 and status == "active" and has("email")'

# String manipulation
echo 'email
  ALICE@EXAMPLE.COM
bob@test.com' | ssql from | \
  ssql update -set-expr email 'lower(trim(email))'
```

**Library Examples:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
    "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
)

func main() {
    // Read sales data
    sales, err := ssql.ReadCSV("sales.csv")
    if err != nil {
        log.Fatal(err)
    }

    // Compile expression once
    calcTotal := runtime.MustCompileExpr("price * qty")

    // Apply to all records
    updated := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
        frozen := mut.Freeze()
        result, _ := calcTotal(frozen)
        if total, ok := result.(float64); ok {
            return mut.Float("total", total)
        }
        return mut
    })(sales)

    // Process results
    for record := range updated {
        total := ssql.GetOr(record, "total", 0.0)
        fmt.Printf("Total: $%.2f\n", total)
    }
}
```

**Features:
- **30+ built-in functions** - Math (round, abs, min, max), string (upper, lower, trim, split), array (filter, map, sum), and type conversion
- **All operators** - Arithmetic (`+`, `-`, `*`, `/`, `%`, `**`), comparison (`==`, `!=`, `<`, `>`, `<=`, `>=`), logical (`and`, `or`, `not`)
- **Advanced syntax** - Ternary operator (`? :`), nil coalescing (`??`), membership (`in`), pipe (`|`)
- **Helper functions** - `has(field)` check existence, `getOr(field, default)` safe access with defaults
- **High performance** - Compile once (~100µs), evaluate many (~1-2µs per record)
- **Type safety** - Boolean expressions type-checked at compile time
- **Code generation** - Expressions pre-compiled in generated Go programs

**Use Cases:
- **Data validation** - `where -if-expr 'age >= 0 and age <= 120 and has("email")'`
- **Data cleaning** - `update -set-expr email 'lower(trim(email))'`
- **Calculations** - `update -set-expr total 'round(price * qty * (1 - discount / 100))'`
- **Categorization** - `update -set-expr tier 'revenue > 10000 ? "gold" : "silver"'`
- **Complex filters** - `where -expr '(age >= 18 and status == "active") or role == "admin"'`

**Performance:
```bash
# CLI execution (~1ms overhead for 1M records)
ssql from huge.csv | ssql where -if-expr 'price * qty > 1000'

# Code generation: expressions transpile to native Go (v4.57.0+) —
# ~3ns/row with zero allocations instead of ~1.3µs of interpreted VM,
# and a measured 19x end-to-end on a 5M-row filter+aggregate pipeline
# (use -explain to see the chosen tier per expression)
export SSQL_MODE=parallel
ssql from huge.csv | \
  ssql where -if-expr 'price * qty > 1000' | \
  ssql update -set-expr total 'price * qty' | \
  ssql generate go > optimized.go
go run optimized.go
```

**Full documentation:** [Expression Language Reference](EXPRESSIONS.md)

</details>

## Try the examples

Run these to see ssql in action:

```bash
# Interactive chart showcase
go run ./examples/chart_demo

# Data analysis pipeline
go run ./examples/functional_example

# Real-time processing
go run ./examples/early_termination_example
```
