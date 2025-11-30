package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v3"
	"slices"
)

func main() {
	// Sample sales data
	sales := []ssql.Record{
		ssql.MakeMutableRecord().String("region", "North").String("product", "Laptop").Float("amount", 1200).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Phone").Float("amount", 800).Freeze(),
		ssql.MakeMutableRecord().String("region", "North").String("product", "Phone").Float("amount", 900).Freeze(),
		ssql.MakeMutableRecord().String("region", "East").String("product", "Laptop").Float("amount", 1100).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Laptop").Float("amount", 1300).Freeze(),
	}

	fmt.Println("📊 StreamV2-Style Pipeline: From → Where → GroupBy → Sum")
	fmt.Println("==========================================================\n")

	// Try StreamV2-style functional composition
	filterStep := ssql.Where(func(r ssql.Record) bool {
		amount := ssql.GetOr(r, "amount", 0.0)
		return amount >= 1000
	})
	groupStep := ssql.GroupByFields("sales_data", "region")

	// Apply steps manually
	filtered := filterStep(slices.Values(sales))
	groups := groupStep(filtered)

	// Apply aggregation
	aggregationStep := ssql.Aggregate("sales_data", map[string]ssql.AggregateFunc{
		"total_sales": ssql.Sum("amount"),
		"count":       ssql.Count(),
	})

	totals := aggregationStep(groups)

	fmt.Println("High-value sales (>= $1000) by region:")
	for result := range totals {
		region := ssql.GetOr(result, "region", "Unknown")
		totalSales := ssql.GetOr(result, "total_sales", 0.0)
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("  %s: $%.0f (%d sales)\n", region, totalSales, count)
	}

	fmt.Println("\n✅ Pipeline composition works!")
	fmt.Println("💡 This is the StreamV2-style functional approach!")
	fmt.Println("   Each step is a pure function that can be composed.")
	fmt.Println("   filterStep(data) → groupStep(filtered) → aggregationStep(groups)")
}
