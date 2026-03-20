package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	fmt.Println("🪟 ssql Windowing Operations Demo")
	fmt.Println("=====================================")

	// Test 1: CountWindow - Fixed-size windows
	fmt.Println("📊 Test 1: CountWindow (groups of 3)")
	fmt.Println("====================================")
	testCountWindow()

	// Test 2: SlidingCountWindow - Overlapping windows
	fmt.Println("\n🔄 Test 2: SlidingCountWindow (window=3, step=1)")
	fmt.Println("===============================================")
	testSlidingCountWindow()

	// Test 3: TimeWindow - Time-based windows
	fmt.Println("\n⏰ Test 3: TimeWindow (1-minute windows)")
	fmt.Println("======================================")
	testTimeWindow()

	// Test 4: SlidingTimeWindow - Overlapping time windows
	fmt.Println("\n⏱️  Test 4: SlidingTimeWindow (2min window, 30s slide)")
	fmt.Println("===================================================")
	testSlidingTimeWindow()

	fmt.Println("\n✨ Key Benefits of Windowing:")
	fmt.Println("============================")
	fmt.Println("🌊 Essential for infinite stream processing")
	fmt.Println("📈 Enables real-time analytics and monitoring")
	fmt.Println("💾 Bounds memory usage for unbounded data")
	fmt.Println("⚡ Supports both count-based and time-based windows")
	fmt.Println("🔄 Overlapping windows for trend analysis")
}

func testCountWindow() {
	// Create test data
	data := []ssql.Record{
		ssql.MakeMutableRecord().Int("id", 1).Int("value", 10).Freeze(),
		ssql.MakeMutableRecord().Int("id", 2).Int("value", 20).Freeze(),
		ssql.MakeMutableRecord().Int("id", 3).Int("value", 30).Freeze(),
		ssql.MakeMutableRecord().Int("id", 4).Int("value", 40).Freeze(),
		ssql.MakeMutableRecord().Int("id", 5).Int("value", 50).Freeze(),
		ssql.MakeMutableRecord().Int("id", 6).Int("value", 60).Freeze(),
		ssql.MakeMutableRecord().Int("id", 7).Int("value", 70).Freeze(),
		ssql.MakeMutableRecord().Int("id", 8).Int("value", 80).Freeze(),
	}

	fmt.Println("Input data:", len(data), "records")

	// Apply CountWindow
	windowOp := ssql.CountWindow[ssql.Record](3)
	windows := windowOp(slices.Values(data))

	windowNum := 1
	for window := range windows {
		fmt.Printf("  Window %d: %d records\n", windowNum, len(window))
		for _, record := range window {
			id := ssql.GetOr(record, "id", int64(0))
			value := ssql.GetOr(record, "value", int64(0))
			fmt.Printf("    ID: %v, Value: %v\n", id, value)
		}
		windowNum++
	}
}

func testSlidingCountWindow() {
	// Create test data
	data := []ssql.Record{
		ssql.MakeMutableRecord().Int("id", 1).Int("value", 100).Freeze(),
		ssql.MakeMutableRecord().Int("id", 2).Int("value", 200).Freeze(),
		ssql.MakeMutableRecord().Int("id", 3).Int("value", 300).Freeze(),
		ssql.MakeMutableRecord().Int("id", 4).Int("value", 400).Freeze(),
		ssql.MakeMutableRecord().Int("id", 5).Int("value", 500).Freeze(),
	}

	fmt.Println("Input data:", len(data), "records")

	// Apply SlidingCountWindow (window=3, step=1)
	windowOp := ssql.SlidingCountWindow[ssql.Record](3, 1)
	windows := windowOp(slices.Values(data))

	windowNum := 1
	for window := range windows {
		fmt.Printf("  Sliding Window %d: %d records\n", windowNum, len(window))
		var ids []any
		for _, record := range window {
			id := ssql.GetOr(record, "id", int64(0))
			ids = append(ids, id)
		}
		fmt.Printf("    IDs: %v\n", ids)
		windowNum++
	}
}

func testTimeWindow() {
	// Create test data with timestamps
	baseTime := time.Now().Truncate(time.Minute)
	data := []ssql.Record{
		ssql.MakeMutableRecord().Int("id", 1).Int("value", 10).String("timestamp", baseTime.Format("2006-01-02 15:04:05")).Freeze(),
		ssql.MakeMutableRecord().Int("id", 2).Int("value", 20).String("timestamp", baseTime.Add(15*time.Second).Format("2006-01-02 15:04:05")).Freeze(),
		ssql.MakeMutableRecord().Int("id", 3).Int("value", 30).String("timestamp", baseTime.Add(30*time.Second).Format("2006-01-02 15:04:05")).Freeze(),
		ssql.MakeMutableRecord().Int("id", 4).Int("value", 40).String("timestamp", baseTime.Add(70*time.Second).Format("2006-01-02 15:04:05")).Freeze(), // Next minute
		ssql.MakeMutableRecord().Int("id", 5).Int("value", 50).String("timestamp", baseTime.Add(90*time.Second).Format("2006-01-02 15:04:05")).Freeze(),
		ssql.MakeMutableRecord().Int("id", 6).Int("value", 60).String("timestamp", baseTime.Add(130*time.Second).Format("2006-01-02 15:04:05")).Freeze(), // Next minute
	}

	fmt.Println("Input data:", len(data), "records with timestamps")

	// Apply TimeWindow (1 minute windows)
	windowOp := ssql.TimeWindow[ssql.Record](time.Minute, "timestamp")
	windows := windowOp(slices.Values(data))

	windowNum := 1
	for window := range windows {
		fmt.Printf("  Time Window %d: %d records\n", windowNum, len(window))
		for _, record := range window {
			id := ssql.GetOr(record, "id", int64(0))
			timestamp := ssql.GetOr(record, "timestamp", "")
			fmt.Printf("    ID: %v, Time: %v\n", id, timestamp)
		}
		windowNum++
	}
}

func testSlidingTimeWindow() {
	// Create test data spread over time
	baseTime := time.Now().Truncate(time.Minute)
	data := []ssql.Record{
		ssql.MakeMutableRecord().Int("id", 1).String("event", "login").String("timestamp", baseTime.Format(time.RFC3339)).Freeze(),
		ssql.MakeMutableRecord().Int("id", 2).String("event", "page_view").String("timestamp", baseTime.Add(45*time.Second).Format(time.RFC3339)).Freeze(),
		ssql.MakeMutableRecord().Int("id", 3).String("event", "click").String("timestamp", baseTime.Add(90*time.Second).Format(time.RFC3339)).Freeze(),
		ssql.MakeMutableRecord().Int("id", 4).String("event", "purchase").String("timestamp", baseTime.Add(135*time.Second).Format(time.RFC3339)).Freeze(),
		ssql.MakeMutableRecord().Int("id", 5).String("event", "logout").String("timestamp", baseTime.Add(180*time.Second).Format(time.RFC3339)).Freeze(),
	}

	fmt.Println("Input data:", len(data), "records over 3 minutes")

	// Apply SlidingTimeWindow (2 minute window, 30 second slide)
	windowOp := ssql.SlidingTimeWindow[ssql.Record](2*time.Minute, 30*time.Second, "timestamp")
	windows := windowOp(slices.Values(data))

	windowNum := 1
	for window := range windows {
		fmt.Printf("  Sliding Time Window %d: %d records\n", windowNum, len(window))
		for _, record := range window {
			event := ssql.GetOr(record, "event", "")
			timestamp := ssql.GetOr(record, "timestamp", "")
			fmt.Printf("    Event: %v at %v\n", event, timestamp)
		}
		windowNum++
	}
}
