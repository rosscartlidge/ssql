package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v3"
	"os"
	"slices"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		showUsage()
		return
	}

	command := os.Args[1]

	switch command {
	case "generate":
		generateData()
	case "filter":
		filterData()
	case "aggregate":
		aggregateData()
	case "format":
		formatOutput()
	case "demo":
		runChainDemo()
	default:
		showUsage()
	}
}

func showUsage() {
	fmt.Println("🔗 JSON Process Chain Demo")
	fmt.Println("==========================")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run json_process_chain.go <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  generate  - Generate sample data (CSV → JSON)")
	fmt.Println("  filter    - Filter records (JSON → JSON)")
	fmt.Println("  aggregate - Aggregate data (JSON → JSON)")
	fmt.Println("  format    - Format output (JSON → human readable)")
	fmt.Println("  demo      - Run complete chain demonstration")
	fmt.Println()
	fmt.Println("Chain Example:")
	fmt.Println("  go run json_process_chain.go generate | \\")
	fmt.Println("  go run json_process_chain.go filter | \\")
	fmt.Println("  go run json_process_chain.go aggregate | \\")
	fmt.Println("  go run json_process_chain.go format")
}

// Step 1: Generate sample data and output as JSON
func generateData() {
	fmt.Fprintln(os.Stderr, "🏭 Generating sample sales data...")

	// Create sample sales data with complex types
	tags1 := slices.Values([]string{"electronics", "mobile"})
	tags2 := slices.Values([]string{"electronics", "computer"})
	tags3 := slices.Values([]string{"books", "education"})
	tags4 := slices.Values([]string{"clothing", "winter"})

	salesData := []ssql.Record{
		ssql.MakeMutableRecord().
			String("id", "SALE-001").
			String("product", "iPhone 15").
			String("category", "electronics").
			Float("price", 999.99).
			Int("quantity", 2).
			String("region", "North").
			StringSeq("tags", tags1).
			Freeze(),

		ssql.MakeMutableRecord().
			String("id", "SALE-002").
			String("product", "MacBook Pro").
			String("category", "electronics").
			Float("price", 2499.99).
			Int("quantity", 1).
			String("region", "South").
			StringSeq("tags", tags2).
			Freeze(),

		ssql.MakeMutableRecord().
			String("id", "SALE-003").
			String("product", "Python Guide").
			String("category", "books").
			Float("price", 49.99).
			Int("quantity", 3).
			String("region", "North").
			StringSeq("tags", tags3).
			Freeze(),

		ssql.MakeMutableRecord().
			String("id", "SALE-004").
			String("product", "Winter Jacket").
			String("category", "clothing").
			Float("price", 199.99).
			Int("quantity", 2).
			String("region", "North").
			StringSeq("tags", tags4).
			Freeze(),

		ssql.MakeMutableRecord().
			String("id", "SALE-005").
			String("product", "iPad Air").
			String("category", "electronics").
			Float("price", 599.99).
			Int("quantity", 1).
			String("region", "South").
			StringSeq("tags", tags1).
			Freeze(),
	}

	stream := ssql.From(salesData)
	err := ssql.WriteJSONToWriter(stream, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "✅ Generated 5 sales records")
}

// Step 2: Filter high-value sales (>= $500)
func filterData() {
	fmt.Fprintln(os.Stderr, "🔍 Filtering high-value sales (>= $500)...")

	// Read JSON from stdin
	stream := ssql.ReadJSONFromReader(os.Stdin)

	// Process records manually for simplicity
	var records []ssql.Record
	for record := range stream {
		price := ssql.GetOr(record, "price", float64(0))

		// Filter: only high-value items
		if price >= 500.0 {
			// Add calculated fields
			quantity := ssql.GetOr(record, "quantity", int64(0))
			totalValue := price * float64(quantity)

			// Create new record with additional fields
			// Create new record with additional fields
			mutable := record.ToMutable()
			mutable.Float("total_value", totalValue)
			mutable.String("tier", "premium")
			records = append(records, mutable.Freeze())
		}
	}

	err := ssql.WriteJSONToWriter(ssql.From(records), os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "✅ Filtered to %d premium sales\n", len(records))
}

// Step 3: Aggregate by region
func aggregateData() {
	fmt.Fprintln(os.Stderr, "📊 Aggregating sales by region...")

	// Read JSON from stdin
	stream := ssql.ReadJSONFromReader(os.Stdin)

	// Collect all records
	var records []ssql.Record
	for record := range stream {
		records = append(records, record)
	}

	// Group by region and aggregate
	results := ssql.Chain(
		ssql.GroupByFields("group_data", "region"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"total_sales":   ssql.Count(),
			"total_revenue": ssql.Sum("total_value"),
			"avg_price":     ssql.Avg("price"),
			"products":      ssql.Collect("product"),
		}),
	)(slices.Values(records))

	// Convert results to slice and output
	var aggregated []ssql.Record
	for result := range results {
		aggregated = append(aggregated, result)
	}

	err := ssql.WriteJSONToWriter(ssql.From(aggregated), os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "✅ Aggregated into %d regional summaries\n", len(aggregated))
}

// Step 4: Format output for human consumption
func formatOutput() {
	fmt.Fprintln(os.Stderr, "📝 Formatting final report...")

	// Read JSON from stdin
	stream := ssql.ReadJSONFromReader(os.Stdin)

	fmt.Println("🏪 REGIONAL SALES REPORT")
	fmt.Println("========================")
	fmt.Println()

	for record := range stream {
		region := ssql.GetOr(record, "region", "Unknown")
		totalSales := ssql.GetOr(record, "total_sales", int64(0))
		totalRevenue := ssql.GetOr(record, "total_revenue", float64(0))
		avgPrice := ssql.GetOr(record, "avg_price", float64(0))

		fmt.Printf("📍 Region: %s\n", region)
		fmt.Printf("   Sales Count: %d\n", totalSales)
		fmt.Printf("   Total Revenue: $%.2f\n", totalRevenue)
		fmt.Printf("   Average Price: $%.2f\n", avgPrice)

		// Show products if available
		if products, ok := ssql.Get[[]any](record, "products"); ok {
			fmt.Print("   Products: ")
			for i, product := range products {
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Print(product)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	fmt.Fprintln(os.Stderr, "✅ Report generated successfully")
}

// Demo: Run the complete chain in memory
func runChainDemo() {
	fmt.Println("🔗 JSON Process Chain Demo")
	fmt.Println("===========================\n")

	fmt.Println("This demo shows how JSON preserves data across process boundaries.")
	fmt.Println("Each step reads JSON from stdin and outputs JSON to stdout.\n")

	// Step 1: Generate data
	fmt.Println("Step 1: Generate → JSON")
	fmt.Println("Commands: go run json_process_chain.go generate")

	var step1Output strings.Builder
	tags := slices.Values([]string{"electronics", "premium"})
	sampleData := []ssql.Record{
		ssql.MakeMutableRecord().
			String("id", "DEMO-001").
			String("product", "Premium Laptop").
			Float("price", 1999.99).
			Int("quantity", 1).
			String("region", "North").
			StringSeq("tags", tags).
			Freeze(),
	}
	ssql.WriteJSONToWriter(ssql.From(sampleData), &step1Output)
	fmt.Printf("Output: %s\n", strings.TrimSpace(step1Output.String()))

	// Step 2: Filter data
	fmt.Println("\nStep 2: JSON → Filter → JSON")
	fmt.Println("Commands: ... | go run json_process_chain.go filter")

	step2Input := strings.NewReader(step1Output.String())
	inputStream := ssql.ReadJSONFromReader(step2Input)

	var filteredRecords []ssql.Record
	for record := range inputStream {
		price := ssql.GetOr(record, "price", float64(0))
		if price >= 500.0 {
			quantity := ssql.GetOr(record, "quantity", int64(0))
			totalValue := price * float64(quantity)

			// Create new record with additional fields
			// Create new record with additional fields
			mutable := record.ToMutable()
			mutable.Float("total_value", totalValue)
			mutable.String("tier", "premium")
			filteredRecords = append(filteredRecords, mutable.Freeze())
		}
	}

	var step2Output strings.Builder
	ssql.WriteJSONToWriter(ssql.From(filteredRecords), &step2Output)
	fmt.Printf("Output: %s\n", strings.TrimSpace(step2Output.String()))

	fmt.Println("\n✅ Key Benefits Demonstrated:")
	fmt.Println("   🔄 Full data preservation: iter.Seq, complex types maintained")
	fmt.Println("   🔗 Perfect process chaining: JSON → Process → JSON")
	fmt.Println("   🧪 Testable: Each step can be tested independently")
	fmt.Println("   🔧 Composable: Mix and match processing steps")
	fmt.Println("   📊 Type safety: JSONString prevents double-encoding")

	fmt.Println("\n🚀 Try the full pipeline:")
	fmt.Println("   go run json_process_chain.go generate | \\")
	fmt.Println("   go run json_process_chain.go filter | \\")
	fmt.Println("   go run json_process_chain.go aggregate | \\")
	fmt.Println("   go run json_process_chain.go format")
}
