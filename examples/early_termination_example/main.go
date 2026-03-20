package main

import (
	"fmt"
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	fmt.Println("🛑 Early Termination Patterns for Infinite Streams")
	fmt.Println("==================================================")

	// Test 1: Limit - Limit infinite stream to first N elements
	fmt.Println("📊 Test 1: Limit - Processing First N Elements")
	fmt.Println("===============================================")
	testLimit()

	// Test 2: TakeWhile - Stop when condition becomes false
	fmt.Println("\n🎯 Test 2: TakeWhile - Conditional Processing")
	fmt.Println("==============================================")
	testTakeWhile()

	// Test 3: TakeUntil - Stop when condition becomes true
	fmt.Println("\n⏰ Test 3: TakeUntil - Event-Driven Termination")
	fmt.Println("===============================================")
	testTakeUntil()

	// Test 4: Timeout - Time-based termination
	fmt.Println("\n⌛ Test 4: Timeout - Time-Based Termination")
	fmt.Println("==========================================")
	testTimeout()

	// Test 5: TimeBasedTimeout - Field-based time termination
	fmt.Println("\n📅 Test 5: TimeBasedTimeout - Field Time Termination")
	fmt.Println("====================================================")
	testTimeBasedTimeout()

	// Test 6: SkipWhile/SkipUntil - Conditional skipping
	fmt.Println("\n⏭️  Test 6: SkipWhile/SkipUntil - Conditional Skipping")
	fmt.Println("=====================================================")
	testSkipPatterns()

	fmt.Println("\n✨ Key Benefits of Early Termination Patterns:")
	fmt.Println("===============================================")
	fmt.Println("🛑 Safe exit conditions for infinite streams")
	fmt.Println("⏱️  Time-based processing limits")
	fmt.Println("🎯 Condition-based stream control")
	fmt.Println("💾 Prevents memory exhaustion")
	fmt.Println("🔄 Perfect for real-time data processing")
}

func testLimit() {
	// Simulate infinite sensor readings
	sensorGenerator := func(yield func(ssql.Record) bool) {
		for i := 1; ; i++ { // Infinite loop
			record := ssql.MakeMutableRecord().
				String("reading_id", fmt.Sprintf("SENSOR-%04d", i)).
				Float("value", 20.0+float64(i%10)*2.5).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Second).Format("15:04:05")).
				Freeze()

			if !yield(record) {
				return
			}

			// Add small delay to simulate real-time data
			time.Sleep(10 * time.Millisecond)
		}
	}

	fmt.Println("Processing infinite sensor stream - taking first 5 readings...")

	// Apply Limit to limit to first 5 elements
	limitOp := ssql.Limit[ssql.Record](5)
	limitedStream := limitOp(sensorGenerator)

	for record := range limitedStream {
		readingId := ssql.GetOr(record, "reading_id", "unknown")
		value := ssql.GetOr(record, "value", 0.0)
		timestamp := ssql.GetOr(record, "timestamp", "unknown")

		fmt.Printf("  %s: %.1f°C at %s\n", readingId, value, timestamp)
	}

	fmt.Println("  ✅ Successfully terminated infinite stream after 5 elements")
}

func testTakeWhile() {
	// Simulate stock price monitoring
	priceGenerator := func(yield func(ssql.Record) bool) {
		prices := []float64{100.0, 102.5, 105.2, 108.1, 95.3, 92.8, 98.7, 101.2}

		for i, price := range prices {
			record := ssql.MakeMutableRecord().
				String("symbol", "TECH").
				Float("price", price).
				Int("tick", int64(i+1)).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Minute).Format("15:04")).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing stock prices - continue while price >= $100...")

	// Take while price is above $100
	takeWhileOp := ssql.TakeWhile(func(record ssql.Record) bool {
		price := ssql.GetOr(record, "price", 0.0)
		return price >= 100.0
	})

	filteredStream := takeWhileOp(priceGenerator)

	for record := range filteredStream {
		symbol := ssql.GetOr(record, "symbol", "unknown")
		price := ssql.GetOr(record, "price", 0.0)
		tick := ssql.GetOr(record, "tick", 0)
		timestamp := ssql.GetOr(record, "timestamp", "unknown")

		fmt.Printf("  %s Tick %d: $%.2f at %s\n", symbol, tick, price, timestamp)
	}

	fmt.Println("  ✅ Stopped processing when price dropped below $100")
}

func testTakeUntil() {
	// Simulate system monitoring - stop when error occurs
	systemGenerator := func(yield func(ssql.Record) bool) {
		statuses := []string{"OK", "OK", "WARNING", "OK", "ERROR", "OK", "OK"}

		for i, status := range statuses {
			record := ssql.MakeMutableRecord().
				String("check_id", fmt.Sprintf("SYS-%03d", i+1)).
				String("status", status).
				Float("cpu_usage", float64(30+i*5)).
				String("timestamp", time.Now().Add(time.Duration(i)*time.Second).Format("15:04:05")).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing system checks - stop when ERROR is encountered...")

	// Take until we encounter an ERROR status
	takeUntilOp := ssql.TakeUntil(func(record ssql.Record) bool {
		status := ssql.GetOr(record, "status", "unknown")
		return status == "ERROR"
	})

	filteredStream := takeUntilOp(systemGenerator)

	for record := range filteredStream {
		checkId := ssql.GetOr(record, "check_id", "unknown")
		status := ssql.GetOr(record, "status", "unknown")
		cpuUsage := ssql.GetOr(record, "cpu_usage", 0.0)
		timestamp := ssql.GetOr(record, "timestamp", "unknown")

		fmt.Printf("  %s: %s (CPU: %.1f%%) at %s\n", checkId, status, cpuUsage, timestamp)
	}

	fmt.Println("  ✅ Stopped processing when ERROR status was encountered")
}

func testTimeout() {
	// Simulate slow data source
	slowGenerator := func(yield func(ssql.Record) bool) {
		for i := 1; i <= 10; i++ {
			record := ssql.MakeMutableRecord().
				String("data_id", fmt.Sprintf("DATA-%03d", i)).
				Int("value", int64(i*10)).
				Freeze()

			if !yield(record) {
				return
			}

			// Simulate processing delay
			time.Sleep(200 * time.Millisecond)
		}
	}

	fmt.Println("Processing slow data source with 500ms timeout...")

	// Apply timeout of 500ms
	timeoutOp := ssql.Timeout[ssql.Record](500 * time.Millisecond)
	timedStream := timeoutOp(slowGenerator)

	startTime := time.Now()
	count := 0

	for record := range timedStream {
		count++
		dataId := ssql.GetOr(record, "data_id", "unknown")
		value := ssql.GetOr(record, "value", 0)

		fmt.Printf("  %s: %d\n", dataId, value)
	}

	elapsed := time.Since(startTime)
	fmt.Printf("  ✅ Processed %d elements in %v (terminated by timeout)\n", count, elapsed)
}

func testTimeBasedTimeout() {
	// Simulate time-series data
	timeSeriesGenerator := func(yield func(ssql.Record) bool) {
		baseTime := time.Now()

		for i := 0; i < 10; i++ {
			record := ssql.MakeMutableRecord().
				String("event_id", fmt.Sprintf("EVT-%03d", i+1)).
				Float("value", float64(100+i*5)).
				Time("timestamp", baseTime.Add(time.Duration(i)*time.Second)).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing time-series data with 5-second time window...")

	// Apply time-based timeout of 5 seconds
	timeBasedOp := ssql.TimeBasedTimeout("timestamp", 5*time.Second)
	timedStream := timeBasedOp(timeSeriesGenerator)

	for record := range timedStream {
		eventId := ssql.GetOr(record, "event_id", "unknown")
		value := ssql.GetOr(record, "value", 0.0)

		if t, ok := ssql.Get[time.Time](record, "timestamp"); ok {
			fmt.Printf("  %s: %.1f at %s\n", eventId, value, t.Format("15:04:05"))
		} else {
			fmt.Printf("  %s: %.1f\n", eventId, value)
		}
	}

	fmt.Println("  ✅ Stopped processing after 5-second time window")
}

func testSkipPatterns() {
	// Simulate log file processing with headers
	logGenerator := func(yield func(ssql.Record) bool) {
		lines := []string{
			"# Log started at 2024-01-01",
			"# Version 1.0",
			"# Configuration loaded",
			"INFO: Application started",
			"INFO: Database connected",
			"WARN: High memory usage",
			"ERROR: Connection timeout",
			"INFO: Retrying connection",
		}

		for i, line := range lines {
			record := ssql.MakeMutableRecord().
				Int("line_no", int64(i+1)).
				String("content", line).
				String("type", getLogType(line)).
				Freeze()

			if !yield(record) {
				return
			}
		}
	}

	fmt.Println("Processing log file - skipping header comments...")

	// Skip while lines start with '#' (comments)
	skipWhileOp := ssql.SkipWhile(func(record ssql.Record) bool {
		content := ssql.GetOr(record, "content", "")
		return len(content) > 0 && content[0] == '#'
	})

	filteredStream := skipWhileOp(logGenerator)

	for record := range filteredStream {
		lineNo := ssql.GetOr(record, "line_no", 0)
		content := ssql.GetOr(record, "content", "unknown")
		logType := ssql.GetOr(record, "type", "unknown")

		fmt.Printf("  Line %d [%s]: %s\n", lineNo, logType, content)
	}

	fmt.Println("  ✅ Successfully skipped header comments and processed log entries")
}

func getLogType(line string) string {
	if len(line) > 0 && line[0] == '#' {
		return "COMMENT"
	}
	if len(line) > 4 {
		switch line[:4] {
		case "INFO":
			return "INFO"
		case "WARN":
			return "WARN"
		case "ERRO":
			return "ERROR"
		}
	}
	return "UNKNOWN"
}
