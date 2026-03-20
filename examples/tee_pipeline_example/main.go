package main

import (
	"fmt"
	"iter"
	"os"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	if len(os.Args) < 2 {
		showUsage()
		return
	}

	command := os.Args[1]

	switch command {
	case "generate":
		generateSalesData()
	case "analyze":
		analyzeSalesData()
	case "demo":
		runFullDemo()
	default:
		showUsage()
	}
}

func showUsage() {
	fmt.Println("🔀 Tee Pipeline Example")
	fmt.Println("=======================")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run tee_pipeline_example.go <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  generate  - Generate sample sales data (outputs JSON)")
	fmt.Println("  analyze   - Read JSON from stdin and run 4 parallel analyses")
	fmt.Println("  demo      - Run complete pipeline demonstration")
	fmt.Println()
	fmt.Println("Pipeline Example:")
	fmt.Println("  go run tee_pipeline_example.go generate | \\")
	fmt.Println("  go run tee_pipeline_example.go analyze")
	fmt.Println()
	fmt.Println("Benefits of using Tee in pipelines:")
	fmt.Println("  🔄 Single data pass for multiple outputs")
	fmt.Println("  ⚡ Memory efficient parallel processing")
	fmt.Println("  📊 Generate multiple reports simultaneously")
	fmt.Println("  🎯 Unix philosophy: do one thing well")
}

func generateSalesData() {
	fmt.Fprintln(os.Stderr, "🏭 Generating sales data...")

	// Generate realistic sales data
	salesData := []ssql.Record{
		ssql.MakeMutableRecord().String("date", "2024-01-01").String("region", "North").String("product", "Laptop").Float("amount", 1299.99).String("salesperson", "Alice").String("customer_type", "enterprise").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-01").String("region", "South").String("product", "Phone").Float("amount", 899.99).String("salesperson", "Bob").String("customer_type", "consumer").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-02").String("region", "East").String("product", "Tablet").Float("amount", 649.99).String("salesperson", "Carol").String("customer_type", "education").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-02").String("region", "West").String("product", "Laptop").Float("amount", 1199.99).String("salesperson", "David").String("customer_type", "enterprise").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-03").String("region", "North").String("product", "Phone").Float("amount", 799.99).String("salesperson", "Eva").String("customer_type", "consumer").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-03").String("region", "South").String("product", "Headphones").Float("amount", 299.99).String("salesperson", "Frank").String("customer_type", "consumer").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-04").String("region", "East").String("product", "Watch").Float("amount", 399.99).String("salesperson", "Grace").String("customer_type", "consumer").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-04").String("region", "West").String("product", "Tablet").Float("amount", 599.99).String("salesperson", "Henry").String("customer_type", "education").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-05").String("region", "North").String("product", "Laptop").Float("amount", 1399.99).String("salesperson", "Iris").String("customer_type", "enterprise").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-05").String("region", "South").String("product", "Phone").Float("amount", 949.99).String("salesperson", "Jack").String("customer_type", "consumer").Freeze(),
	}

	// Output as JSON to stdout for piping
	stream := ssql.From(salesData)
	err := ssql.WriteJSONToWriter(stream, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "✅ Generated 10 sales records")
}

func analyzeSalesData() {
	fmt.Fprintln(os.Stderr, "📊 Running parallel analysis using Tee...")

	// Read JSON from stdin
	stream := ssql.ReadJSONFromReader(os.Stdin)

	// Use Tee to split into 4 parallel analysis streams
	streams := ssql.Tee(stream, 4)

	fmt.Println("🔀 TEE PARALLEL ANALYSIS RESULTS")
	fmt.Println("=================================")

	// Analysis 1: Revenue by Region
	fmt.Println("💰 REVENUE BY REGION:")
	fmt.Println("--------------------")
	revenueByRegion(streams[0])

	// Analysis 2: Top Performers
	fmt.Println("\n🏆 TOP PERFORMERS:")
	fmt.Println("------------------")
	topPerformers(streams[1])

	// Analysis 3: Customer Segment Analysis
	fmt.Println("\n👥 CUSTOMER SEGMENTS:")
	fmt.Println("--------------------")
	customerSegmentAnalysis(streams[2])

	// Analysis 4: Daily Trends
	fmt.Println("\n📈 DAILY TRENDS:")
	fmt.Println("----------------")
	dailyTrends(streams[3])

	fmt.Fprintln(os.Stderr, "\n✅ Completed all 4 analyses in parallel")
}

func revenueByRegion(stream iter.Seq[ssql.Record]) {
	regionRevenue := make(map[string]float64)

	for record := range stream {
		region := ssql.GetOr(record, "region", "Unknown")
		amount := ssql.GetOr(record, "amount", 0.0)
		regionRevenue[region] += amount
	}

	for region, revenue := range regionRevenue {
		fmt.Printf("  %s: $%.2f\n", region, revenue)
	}
}

func topPerformers(stream iter.Seq[ssql.Record]) {
	salesPersonRevenue := make(map[string]float64)

	for record := range stream {
		salesperson := ssql.GetOr(record, "salesperson", "Unknown")
		amount := ssql.GetOr(record, "amount", 0.0)
		salesPersonRevenue[salesperson] += amount
	}

	// Find top performer
	var topPerson string
	var topRevenue float64

	for person, revenue := range salesPersonRevenue {
		if revenue > topRevenue {
			topRevenue = revenue
			topPerson = person
		}
	}

	fmt.Printf("  🥇 Top Salesperson: %s ($%.2f)\n", topPerson, topRevenue)

	// Show all performers
	for person, revenue := range salesPersonRevenue {
		if person != topPerson {
			fmt.Printf("  📊 %s: $%.2f\n", person, revenue)
		}
	}
}

func customerSegmentAnalysis(stream iter.Seq[ssql.Record]) {
	segmentStats := make(map[string]struct {
		count   int
		revenue float64
	})

	for record := range stream {
		customerType := ssql.GetOr(record, "customer_type", "unknown")
		amount := ssql.GetOr(record, "amount", 0.0)

		stats := segmentStats[customerType]
		stats.count++
		stats.revenue += amount
		segmentStats[customerType] = stats
	}

	for segment, stats := range segmentStats {
		avgDeal := stats.revenue / float64(stats.count)
		fmt.Printf("  %s: %d deals, $%.2f avg, $%.2f total\n",
			strings.Title(segment), stats.count, avgDeal, stats.revenue)
	}
}

func dailyTrends(stream iter.Seq[ssql.Record]) {
	dailyRevenue := make(map[string]float64)
	dailyCount := make(map[string]int)

	for record := range stream {
		date := ssql.GetOr(record, "date", "unknown")
		amount := ssql.GetOr(record, "amount", 0.0)

		dailyRevenue[date] += amount
		dailyCount[date]++
	}

	for date := range dailyRevenue {
		revenue := dailyRevenue[date]
		count := dailyCount[date]
		avgDeal := revenue / float64(count)
		fmt.Printf("  %s: %d deals, $%.2f total, $%.2f avg\n",
			date, count, revenue, avgDeal)
	}
}

func runFullDemo() {
	fmt.Println("🚀 Full Tee Pipeline Demo")
	fmt.Println("=========================")

	fmt.Println("This demonstrates how Tee enables efficient parallel processing:")
	fmt.Println("1. Generate sample data")
	fmt.Println("2. Split stream into 4 parallel analyses")
	fmt.Println("3. Run all analyses simultaneously")
	fmt.Println()

	// Simulate the pipeline in memory
	fmt.Println("🔄 Simulating: generate | analyze")
	fmt.Println()

	// Generate data
	salesData := []ssql.Record{
		ssql.MakeMutableRecord().String("date", "2024-01-01").String("region", "North").String("product", "Laptop").Float("amount", 1299.99).String("salesperson", "Alice").String("customer_type", "enterprise").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-01").String("region", "South").String("product", "Phone").Float("amount", 899.99).String("salesperson", "Bob").String("customer_type", "consumer").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-02").String("region", "East").String("product", "Tablet").Float("amount", 649.99).String("salesperson", "Carol").String("customer_type", "education").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-02").String("region", "West").String("product", "Laptop").Float("amount", 1199.99).String("salesperson", "David").String("customer_type", "enterprise").Freeze(),
		ssql.MakeMutableRecord().String("date", "2024-01-03").String("region", "North").String("product", "Phone").Float("amount", 799.99).String("salesperson", "Eva").String("customer_type", "consumer").Freeze(),
	}

	// Create stream and use Tee for parallel analysis
	stream := ssql.From(salesData)
	streams := ssql.Tee(stream, 4)

	fmt.Println("📊 PARALLEL ANALYSIS RESULTS:")
	fmt.Println("=============================")

	// Run all analyses
	fmt.Println("💰 Revenue by Region:")
	revenueByRegion(streams[0])

	fmt.Println("\n🏆 Top Performers:")
	topPerformers(streams[1])

	fmt.Println("\n👥 Customer Segments:")
	customerSegmentAnalysis(streams[2])

	fmt.Println("\n📈 Daily Trends:")
	dailyTrends(streams[3])

	fmt.Println("\n✨ Pipeline Benefits:")
	fmt.Println("   🔄 Single data pass for all analyses")
	fmt.Println("   ⚡ Memory efficient processing")
	fmt.Println("   📊 Comprehensive insights")
	fmt.Println("   🎯 Unix-style composability")
	fmt.Println()
	fmt.Println("💡 Try the real pipeline:")
	fmt.Println("   go run tee_pipeline_example.go generate | go run tee_pipeline_example.go analyze")
}
