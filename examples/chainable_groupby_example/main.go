package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"slices"
)

func main() {
	fmt.Println("🔗 Chainable GroupBy Example")
	fmt.Println("============================")

	// Sample data
	salesData := []ssql.Record{
		ssql.MakeMutableRecord().String("region", "North").String("product", "Laptop").Float("amount", 1200).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Phone").Float("amount", 800).Freeze(),
		ssql.MakeMutableRecord().String("region", "North").String("product", "Phone").Float("amount", 900).Freeze(),
		ssql.MakeMutableRecord().String("region", "East").String("product", "Laptop").Float("amount", 1100).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Laptop").Float("amount", 1300).Freeze(),
	}

	fmt.Println("🎯 Demonstrating Chain() with GroupBy + Aggregate + Where")
	fmt.Println("This shows NO TYPE CHANGES - pure iter.Seq[Record] throughout!")

	// Chainable pipeline: Group by region -> Aggregate -> Filter high-performing regions
	results := ssql.Chain(
		ssql.GroupByFields("sales_data", "region"),
		ssql.Aggregate("sales_data", map[string]ssql.AggregateFunc{
			"total_sales": ssql.Sum("amount"),
			"avg_sales":   ssql.Avg("amount"),
			"count":       ssql.Count(),
		}),
		ssql.Where(func(r ssql.Record) bool {
			totalSales := ssql.GetOr(r, "total_sales", 0.0)
			return totalSales >= 2000 // Only high-performing regions
		}),
	)(slices.Values(salesData))

	fmt.Println("High-performing regions (>= $2000 total sales):")
	for result := range results {
		region := ssql.GetOr(result, "region", "")
		totalSales := ssql.GetOr(result, "total_sales", 0.0)
		avgSales := ssql.GetOr(result, "avg_sales", 0.0)
		count := ssql.GetOr(result, "count", int64(0))

		fmt.Printf("  %s: $%.0f total, $%.0f avg (%d sales)\n",
			region, totalSales, avgSales, count)
	}

	fmt.Println("\n✅ Success! GroupBy is now fully chainable with no type changes!")
	fmt.Println("💡 You can add any Record-based operation before or after GroupBy/Aggregate.")
}
