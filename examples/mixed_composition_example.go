package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v3"
	"slices"
	"strings"
)

func main() {
	// Sample sales data
	sales := []ssql.Record{
		ssql.MakeMutableRecord().String("region", "North").String("product", "Laptop").Float("amount", 1200).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Phone").Float("amount", 800).Freeze(),
		ssql.MakeMutableRecord().String("region", "North").String("product", "Phone").Float("amount", 900).Freeze(),
		ssql.MakeMutableRecord().String("region", "East").String("product", "Laptop").Float("amount", 1100).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Laptop").Float("amount", 1300).Freeze(),
		ssql.MakeMutableRecord().String("region", "West").String("product", "Tablet").Float("amount", 600).Freeze(),
		ssql.MakeMutableRecord().String("region", "North").String("product", "Tablet").Float("amount", 500).Freeze(),
	}

	fmt.Println("🔥 Functional Composition: Chained Operations")
	fmt.Println("===============================================\n")

	// Step 1: Apply preprocessing filters
	chained := ssql.Chain(
		ssql.Where(func(r ssql.Record) bool {
			amount := ssql.GetOr(r, "amount", 0.0)
			return amount >= 600 // Filter >= $600
		}),
		ssql.Where(func(r ssql.Record) bool {
			product := ssql.GetOr(r, "product", "")
			return product != "Tablet" // Exclude tablets
		}),
	)(slices.Values(sales))

	// Apply limit separately since it has different type signature
	filtered := ssql.Limit[ssql.Record](10)(chained)

	// Step 2: Apply grouping operation
	groups := ssql.GroupByFields("sales_data", "region")(filtered)

	// Final aggregation step
	results := ssql.Aggregate("sales_data", map[string]ssql.AggregateFunc{
		"total_revenue": ssql.Sum("amount"),
		"avg_deal":      ssql.Avg("amount"),
		"count":         ssql.Count(),
	})(groups)

	fmt.Println("High-value non-tablet sales by region:")
	for result := range results {
		region := ssql.GetOr(result, "region", "Unknown")
		totalRevenue := ssql.GetOr(result, "total_revenue", 0.0)
		avgDeal := ssql.GetOr(result, "avg_deal", 0.0)
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("  %s: $%.0f revenue, $%.0f avg (%d sales)\n", region, totalRevenue, avgDeal, count)
	}

	fmt.Println("\n✨ What happened:")
	fmt.Println("  1. Chain: Filter high-value sales + Exclude tablets + Limit")
	fmt.Println("  2. GroupByFields: Groups records and adds sequence field")
	fmt.Println("  3. Aggregate: Applies aggregations to grouped records")
	fmt.Println("\n💡 One clear way to compose operations!")

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🚀 Alternative: Step-by-Step Functional Pipeline")

	// Same thing but step-by-step functional composition
	fmt.Println("Same result using step-by-step function calls:")

	// Step by step functional composition
	filtered2 := ssql.Where(func(r ssql.Record) bool {
		amount := ssql.GetOr(r, "amount", 0.0)
		product := ssql.GetOr(r, "product", "")
		return amount >= 600 && product != "Tablet"
	})(slices.Values(sales))

	grouped2 := ssql.GroupByFields("sales_data", "region")(filtered2)

	functionalResults := ssql.Aggregate("sales_data", map[string]ssql.AggregateFunc{
		"total_revenue": ssql.Sum("amount"),
		"avg_deal":      ssql.Avg("amount"),
		"count":         ssql.Count(),
	})(grouped2)
	for result := range functionalResults {
		region := ssql.GetOr(result, "region", "Unknown")
		totalRevenue := ssql.GetOr(result, "total_revenue", 0.0)
		avgDeal := ssql.GetOr(result, "avg_deal", 0.0)
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("  %s: $%.0f revenue, $%.0f avg (%d sales)\n", region, totalRevenue, avgDeal, count)
	}
}
