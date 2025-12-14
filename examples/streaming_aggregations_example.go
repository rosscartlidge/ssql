package main

import (
	"fmt"
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	fmt.Println("📊 Streaming Aggregations for Infinite Streams")
	fmt.Println("===============================================\n")

	// Test 1: RunningSum - Cumulative totals
	fmt.Println("💰 Test 1: RunningSum - Real-time Revenue Tracking")
	fmt.Println("==================================================")
	testRunningSum()

	// Test 2: RunningAverage - Moving averages
	fmt.Println("\n📈 Test 2: RunningAverage - Moving Average Analysis")
	fmt.Println("==================================================")
	testRunningAverage()

	// Test 3: ExponentialMovingAverage - EMA analysis
	fmt.Println("\n⚡ Test 3: ExponentialMovingAverage - EMA Analysis")
	fmt.Println("=================================================")
	testExponentialMovingAverage()

	// Test 4: RunningMinMax - Range tracking
	fmt.Println("\n📊 Test 4: RunningMinMax - Range Tracking")
	fmt.Println("=========================================")
	testRunningMinMax()

	// Test 5: RunningCount - Frequency analysis
	fmt.Println("\n🔢 Test 5: RunningCount - Frequency Analysis")
	fmt.Println("============================================")
	testRunningCount()

	fmt.Println("\n✨ Key Benefits of Streaming Aggregations:")
	fmt.Println("==========================================")
	fmt.Println("📈 Continuous real-time analytics")
	fmt.Println("💾 Bounded memory usage for infinite streams")
	fmt.Println("⚡ Immediate results as data arrives")
	fmt.Println("🔄 Perfect for dashboards and monitoring")
	fmt.Println("📊 Statistical analysis without buffering")
}

func testRunningSum() {
	// Simulate sales transactions
	salesGenerator := func(yield func(ssql.Record) bool) {
		sales := []float64{100.50, 250.75, 175.25, 300.00, 450.25, 125.50, 275.00, 380.75}
		regions := []string{"North", "South", "East", "West", "North", "South", "East", "West"}

		for i, amount := range sales {
			record := ssql.MakeMutableRecord().
				String("transaction_id", fmt.Sprintf("TXN-%03d", i+1)).
				Float("amount", amount).
				String("region", regions[i]).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Minute).Format("15:04")).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing sales transactions with running totals...")

	// Apply RunningSum
	runningSumOp := ssql.RunningSum("amount")
	aggregatedStream := runningSumOp(salesGenerator)

	for record := range aggregatedStream {
		txnId := ssql.GetOr(record, "transaction_id", "unknown")
		amount := ssql.GetOr(record, "amount", 0.0)
		runningSum := ssql.GetOr(record, "running_sum", 0.0)
		runningCount := ssql.GetOr(record, "running_count", int64(0))
		runningAvg := ssql.GetOr(record, "running_avg", 0.0)

		fmt.Printf("  %s: $%.2f | Total: $%.2f | Count: %d | Avg: $%.2f\n",
			txnId, amount, runningSum, runningCount, runningAvg)
	}
}

func testRunningAverage() {
	// Simulate sensor temperature readings
	temperatureGenerator := func(yield func(ssql.Record) bool) {
		temperatures := []float64{22.5, 23.1, 22.8, 24.2, 25.0, 24.8, 23.9, 22.7, 21.8, 22.3}

		for i, temp := range temperatures {
			record := ssql.MakeMutableRecord().
				String("sensor_id", "TEMP-001").
				Float("temperature", temp).
				Int("reading_id", int64(i+1)).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Second).Format("15:04:05")).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing temperature readings with 5-point moving average...")

	// Apply RunningAverage with window size 5
	movingAvgOp := ssql.RunningAverage("temperature", 5)
	aggregatedStream := movingAvgOp(temperatureGenerator)

	for record := range aggregatedStream {
		readingId := ssql.GetOr(record, "reading_id", 0)
		temperature := ssql.GetOr(record, "temperature", 0.0)
		movingAvg := ssql.GetOr(record, "moving_avg", 0.0)
		windowSize := ssql.GetOr(record, "window_size", int64(0))

		fmt.Printf("  Reading %d: %.1f°C | Moving Avg (last %d): %.1f°C\n",
			readingId, temperature, windowSize, movingAvg)
	}
}

func testExponentialMovingAverage() {
	// Simulate stock price data
	stockGenerator := func(yield func(ssql.Record) bool) {
		prices := []float64{100.0, 102.5, 101.8, 105.2, 103.7, 106.1, 104.9, 107.3, 105.8, 108.2}

		for i, price := range prices {
			record := ssql.MakeMutableRecord().
				String("symbol", "TECH").
				Float("price", price).
				Int("tick", int64(i+1)).
				String("time", fmt.Sprintf("09:%02d", 30+i)).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing stock prices with Exponential Moving Average (α=0.3)...")

	// Apply ExponentialMovingAverage with alpha = 0.3
	emaOp := ssql.ExponentialMovingAverage("price", 0.3)
	aggregatedStream := emaOp(stockGenerator)

	for record := range aggregatedStream {
		tick := ssql.GetOr(record, "tick", 0)
		price := ssql.GetOr(record, "price", 0.0)
		ema := ssql.GetOr(record, "ema", 0.0)
		symbol := ssql.GetOr(record, "symbol", "unknown")

		fmt.Printf("  %s Tick %d: $%.2f | EMA: $%.2f\n", symbol, tick, price, ema)
	}
}

func testRunningMinMax() {
	// Simulate system metrics
	metricsGenerator := func(yield func(ssql.Record) bool) {
		cpuUsages := []float64{45.2, 67.8, 34.1, 89.5, 23.7, 91.2, 56.3, 78.9, 42.6, 85.1}

		for i, cpu := range cpuUsages {
			record := ssql.MakeMutableRecord().
				String("metric_id", fmt.Sprintf("CPU-%03d", i+1)).
				Float("cpu_usage", cpu).
				String("server", fmt.Sprintf("srv-%d", (i%3)+1)).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Second).Format("15:04:05")).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing CPU usage metrics with running min/max tracking...")

	// Apply RunningMinMax
	minMaxOp := ssql.RunningMinMax("cpu_usage")
	aggregatedStream := minMaxOp(metricsGenerator)

	for record := range aggregatedStream {
		metricId := ssql.GetOr(record, "metric_id", "unknown")
		cpuUsage := ssql.GetOr(record, "cpu_usage", 0.0)
		runningMin := ssql.GetOr(record, "running_min", 0.0)
		runningMax := ssql.GetOr(record, "running_max", 0.0)
		runningRange := ssql.GetOr(record, "running_range", 0.0)

		fmt.Printf("  %s: %.1f%% | Min: %.1f%% | Max: %.1f%% | Range: %.1f%%\n",
			metricId, cpuUsage, runningMin, runningMax, runningRange)
	}
}

func testRunningCount() {
	// Simulate web requests by region
	requestGenerator := func(yield func(ssql.Record) bool) {
		regions := []string{"US", "EU", "ASIA", "US", "EU", "US", "ASIA", "ASIA", "EU", "US"}

		for i, region := range regions {
			record := ssql.MakeMutableRecord().
				String("request_id", fmt.Sprintf("REQ-%03d", i+1)).
				String("region", region).
				String("method", "GET").
				String("timestamp", time.Now().Add(time.Duration(i)*time.Second).Format("15:04:05")).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing web requests with running frequency counts...")

	// Apply RunningCount
	countOp := ssql.RunningCount("region")
	aggregatedStream := countOp(requestGenerator)

	for record := range aggregatedStream {
		requestId := ssql.GetOr(record, "request_id", "unknown")
		region := ssql.GetOr(record, "region", "unknown")
		totalCount := ssql.GetOr(record, "total_count", int64(0))
		distinctValues := ssql.GetOr(record, "distinct_values", int64(0))

		fmt.Printf("  %s (%s) | Total: %d | Distinct Regions: %d\n",
			requestId, region, totalCount, distinctValues)

		// Show the counts map for first few records
		if totalCount <= 5 {
			if counts, ok := ssql.Get[map[string]int64](record, "distinct_counts"); ok {
				fmt.Printf("    Counts: %v\n", counts)
			}
		}
	}
}
