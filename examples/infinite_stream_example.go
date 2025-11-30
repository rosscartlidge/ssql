package main

import (
	"fmt"
	"time"

	"github.com/rosscartlidge/ssql/v3"
)

func main() {
	fmt.Println("🌊 Infinite Stream Processing with Windowing")
	fmt.Println("============================================\n")

	// Test 1: Simulated infinite stream with CountWindow
	fmt.Println("📊 Test 1: CountWindow on simulated infinite stream")
	fmt.Println("==================================================")
	testInfiniteCountWindow()

	// Test 2: Time-based windowing simulation
	fmt.Println("\n⏰ Test 2: TimeWindow on time-series data")
	fmt.Println("========================================")
	testTimeWindowProcessing()

	// Test 3: Sliding window for trend analysis
	fmt.Println("\n🔄 Test 3: SlidingCountWindow for moving averages")
	fmt.Println("================================================")
	testSlidingWindowAnalysis()

	fmt.Println("\n✨ Key Benefits for Infinite Streams:")
	fmt.Println("====================================")
	fmt.Println("🌊 Bounded memory usage with windowing")
	fmt.Println("📈 Real-time processing without buffering entire stream")
	fmt.Println("⚡ Immediate results as each window completes")
	fmt.Println("🔄 Continuous processing without interruption")
	fmt.Println("📊 Enables real-time analytics and monitoring")
}

func testInfiniteCountWindow() {
	// Simulate an infinite stream using a generator
	dataGenerator := func(yield func(ssql.Record) bool) {
		for i := 0; i < 50; i++ { // Simulate first 50 records
			record := ssql.MakeMutableRecord().
				Int("id", int64(i)).
				Float("value", float64(100+i*5)).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Second).Format("2006-01-02 15:04:05")).
				String("sensor", fmt.Sprintf("sensor_%d", i%3)).
				Freeze()

			if !yield(record) {
				return // Stream consumer stopped
			}
		}
	}

	fmt.Println("Processing infinite stream in windows of 5...")

	// Apply CountWindow to break infinite stream into manageable chunks
	windowOp := ssql.CountWindow[ssql.Record](5)
	windows := windowOp(dataGenerator)

	windowCount := 0
	for window := range windows {
		windowCount++
		if windowCount > 3 { // Only show first 3 windows
			fmt.Printf("... (showing only first 3 windows out of %d total)\n", windowCount)
			break
		}

		// Process each window
		var sum float64
		var ids []any
		for _, record := range window {
			sum += ssql.GetOr(record, "value", 0.0)
			id := ssql.GetOr(record, "id", int64(0))
			ids = append(ids, id)
		}

		avgValue := sum / float64(len(window))
		fmt.Printf("  Window %d: IDs %v, Avg Value: %.2f\n", windowCount, ids, avgValue)
	}
}

func testTimeWindowProcessing() {
	// Create time-series data spanning multiple minutes
	baseTime := time.Now().Truncate(time.Minute)

	dataGenerator := func(yield func(ssql.Record) bool) {
		for i := 0; i < 30; i++ {
			// Spread data across 3 minutes
			timestamp := baseTime.Add(time.Duration(i*6) * time.Second)

			record := ssql.MakeMutableRecord().
				Int("id", int64(i)).
				Float("temperature", 20.0+float64(i%10)).
				Float("humidity", 50.0+float64(i%5)*2).
				String("timestamp", timestamp.Format("2006-01-02 15:04:05")).
				String("location", fmt.Sprintf("room_%d", i%3)).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing time-series data in 1-minute windows...")

	// Apply TimeWindow for time-based aggregation
	windowOp := ssql.TimeWindow[ssql.Record](time.Minute, "timestamp")
	windows := windowOp(dataGenerator)

	windowCount := 0
	for window := range windows {
		windowCount++

		if len(window) == 0 {
			continue
		}

		// Calculate statistics for this time window
		var tempSum, humiditySum float64
		var locations = make(map[string]int)

		for _, record := range window {
			tempSum += ssql.GetOr(record, "temperature", 0.0)
			humiditySum += ssql.GetOr(record, "humidity", 0.0)
			location := ssql.GetOr(record, "location", "unknown")
			locations[location]++
		}

		avgTemp := tempSum / float64(len(window))
		avgHumidity := humiditySum / float64(len(window))

		fmt.Printf("  Time Window %d: %d readings\n", windowCount, len(window))
		fmt.Printf("    Avg Temperature: %.1f°C\n", avgTemp)
		fmt.Printf("    Avg Humidity: %.1f%%\n", avgHumidity)
		fmt.Printf("    Locations: %v\n", locations)
	}
}

func testSlidingWindowAnalysis() {
	// Generate stock price-like data
	dataGenerator := func(yield func(ssql.Record) bool) {
		basePrice := 100.0

		for i := 0; i < 20; i++ {
			// Simulate price movements
			change := float64(i%5-2) * 2.5 // -5, -2.5, 0, 2.5, 5
			basePrice += change

			record := ssql.MakeMutableRecord().
				Int("tick", int64(i)).
				Float("price", basePrice).
				Int("volume", int64(1000+i*50)).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Minute).Format("15:04:05")).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Calculating moving averages with sliding windows (window=5, step=1)...")

	// Apply SlidingCountWindow for moving averages
	windowOp := ssql.SlidingCountWindow[ssql.Record](5, 1)
	windows := windowOp(dataGenerator)

	windowCount := 0
	for window := range windows {
		windowCount++

		if windowCount > 8 { // Show first 8 sliding windows
			fmt.Printf("... (showing only first 8 sliding windows)\n")
			break
		}

		// Calculate moving average
		var priceSum float64
		var ticks []any

		for _, record := range window {
			priceSum += ssql.GetOr(record, "price", 0.0)
			tick := ssql.GetOr(record, "tick", int64(0))
			ticks = append(ticks, tick)
		}

		movingAvg := priceSum / float64(len(window))
		fmt.Printf("  Sliding Window %d (ticks %v): Moving Avg = $%.2f\n",
			windowCount, ticks, movingAvg)
	}

	fmt.Println("\n💡 This demonstrates how sliding windows enable:")
	fmt.Println("   • Real-time trend analysis")
	fmt.Println("   • Moving averages without buffering entire stream")
	fmt.Println("   • Continuous monitoring with bounded memory")
}
