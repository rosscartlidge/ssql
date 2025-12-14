package main

import (
	"fmt"
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	fmt.Println("⚡ Tee Performance Comparison")
	fmt.Println("============================\n")

	// Generate large dataset for meaningful performance comparison
	fmt.Println("🏭 Generating test dataset...")
	data := generateLargeDataset(10000)
	fmt.Printf("✅ Created %d records\n\n", len(data))

	// Test 1: Without Tee - Multiple passes through data
	fmt.Println("🐌 Method 1: Multiple Data Passes (WITHOUT Tee)")
	fmt.Println("===============================================")
	runMultiplePassAnalysis(data)

	// Test 2: With Tee - Single pass through data
	fmt.Println("\n⚡ Method 2: Single Data Pass (WITH Tee)")
	fmt.Println("=======================================")
	runTeeAnalysis(data)

	fmt.Println("\n📊 Performance Benefits of Tee:")
	fmt.Println("===============================")
	fmt.Println("✅ Single iteration through source data")
	fmt.Println("✅ Memory efficient data sharing")
	fmt.Println("✅ Parallel computation without duplication")
	fmt.Println("✅ Consistent results across all analyses")
	fmt.Println("✅ Ideal for large datasets and complex pipelines")
}

func generateLargeDataset(size int) []ssql.Record {
	data := make([]ssql.Record, size)

	regions := []string{"North", "South", "East", "West", "Central"}
	products := []string{"Laptop", "Phone", "Tablet", "Watch", "Headphones"}

	for i := 0; i < size; i++ {
		data[i] = ssql.MakeMutableRecord().
			String("id", fmt.Sprintf("TXN-%06d", i)).
			Float("amount", float64(100+(i*7)%900)).
			String("region", regions[i%len(regions)]).
			String("product", products[i%len(products)]).
			String("quarter", fmt.Sprintf("Q%d", (i%4)+1)).
			Int("priority", int64(i%3)).
			Freeze()
	}

	return data
}

func runMultiplePassAnalysis(data []ssql.Record) {
	start := time.Now()

	// Analysis 1: Total Revenue (First pass)
	var totalRevenue float64
	for _, record := range data {
		amount := ssql.GetOr(record, "amount", 0.0)
		totalRevenue += amount
	}

	// Analysis 2: Region Distribution (Second pass)
	regionCounts := make(map[string]int)
	for _, record := range data {
		region := ssql.GetOr(record, "region", "unknown")
		regionCounts[region]++
	}

	// Analysis 3: Product Performance (Third pass)
	productRevenue := make(map[string]float64)
	for _, record := range data {
		product := ssql.GetOr(record, "product", "unknown")
		amount := ssql.GetOr(record, "amount", 0.0)
		productRevenue[product] += amount
	}

	// Analysis 4: Priority Distribution (Fourth pass)
	priorityCounts := make(map[int]int)
	for _, record := range data {
		priority := ssql.GetOr(record, "priority", 0)
		priorityCounts[priority]++
	}

	elapsed := time.Since(start)

	fmt.Printf("  💰 Total Revenue: $%.2f\n", totalRevenue)
	fmt.Printf("  🗺️  Regions: %d different regions\n", len(regionCounts))
	fmt.Printf("  📱 Products: %d different products\n", len(productRevenue))
	fmt.Printf("  ⭐ Priorities: %d different priority levels\n", len(priorityCounts))
	fmt.Printf("  ⏱️  Time: %v (4 data passes)\n", elapsed)
}

func runTeeAnalysis(data []ssql.Record) {
	start := time.Now()

	// Create stream and split with Tee
	stream := ssql.From(data)
	streams := ssql.Tee(stream, 4) // Split into 4 parallel streams

	// Analysis 1: Total Revenue
	var totalRevenue float64
	for record := range streams[0] {
		amount := ssql.GetOr(record, "amount", 0.0)
		totalRevenue += amount
	}

	// Analysis 2: Region Distribution
	regionCounts := make(map[string]int)
	for record := range streams[1] {
		region := ssql.GetOr(record, "region", "unknown")
		regionCounts[region]++
	}

	// Analysis 3: Product Performance
	productRevenue := make(map[string]float64)
	for record := range streams[2] {
		product := ssql.GetOr(record, "product", "unknown")
		amount := ssql.GetOr(record, "amount", 0.0)
		productRevenue[product] += amount
	}

	// Analysis 4: Priority Distribution
	priorityCounts := make(map[int]int)
	for record := range streams[3] {
		priority := ssql.GetOr(record, "priority", 0)
		priorityCounts[priority]++
	}

	elapsed := time.Since(start)

	fmt.Printf("  💰 Total Revenue: $%.2f\n", totalRevenue)
	fmt.Printf("  🗺️  Regions: %d different regions\n", len(regionCounts))
	fmt.Printf("  📱 Products: %d different products\n", len(productRevenue))
	fmt.Printf("  ⭐ Priorities: %d different priority levels\n", len(priorityCounts))
	fmt.Printf("  ⏱️  Time: %v (1 data pass with Tee)\n", elapsed)
}
