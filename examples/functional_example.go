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
		ssql.MakeMutableRecord().String("region", "North").String("product", "Tablet").Float("amount", 500).Freeze(),
		ssql.MakeMutableRecord().String("region", "East").String("product", "Phone").Float("amount", 850).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Tablet").Float("amount", 450).Freeze(),
	}

	fmt.Println("📊 Sales Analysis with Functional Composition")
	fmt.Println("==============================================\n")

	// Example 1: Filter high-value sales and group by region - FUNCTIONAL PIPELINE!
	fmt.Println("1. High-value sales (>= $1000) by region:")

	// Step 1: Filter high-value sales - using type-safe Get[T]
	filtered1 := ssql.Where(func(r ssql.Record) bool {
		amount := ssql.GetOr(r, "amount", 0.0)
		return amount >= 1000
	})(slices.Values(sales))

	// Step 2: Group by region
	groups1 := ssql.GroupByFields("sales_data", "region")(filtered1)

	// Step 3: Aggregate
	regionTotals := ssql.Aggregate("sales_data", map[string]ssql.AggregateFunc{
		"total_sales": ssql.Sum("amount"),
		"count":       ssql.Count(),
	})(groups1)

	for result := range regionTotals {
		region := ssql.GetOr(result, "region", "Unknown")
		totalSales := ssql.GetOr(result, "total_sales", 0.0)
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("   %s: $%.0f (%d sales)\n", region, totalSales, count)
	}

	fmt.Println("\n2. All sales by product category:")

	// Example 2: Group all sales by product and calculate totals - FUNCTIONAL PIPELINE!
	groups2 := ssql.GroupByFields("sales_data", "product")(slices.Values(sales))
	productTotals := ssql.Aggregate("sales_data", map[string]ssql.AggregateFunc{
		"total_revenue": ssql.Sum("amount"),
		"avg_price":     ssql.Avg("amount"),
		"sales_count":   ssql.Count(),
	})(groups2)

	for result := range productTotals {
		product := ssql.GetOr(result, "product", "Unknown")
		totalRevenue := ssql.GetOr(result, "total_revenue", 0.0)
		avgPrice := ssql.GetOr(result, "avg_price", 0.0)
		salesCount := ssql.GetOr(result, "sales_count", int64(0))
		fmt.Printf("   %s: $%.0f total, $%.0f avg (%d sales)\n", product, totalRevenue, avgPrice, salesCount)
	}

	fmt.Println("\n3. Chain everything together - North region laptop sales:")

	// Example 3: Complex pipeline with multiple filters and aggregation - FULL FUNCTIONAL PIPELINE!
	filtered3 := ssql.Chain(
		ssql.Where(func(r ssql.Record) bool {
			region := ssql.GetOr(r, "region", "")
			return region == "North"
		}),
		ssql.Where(func(r ssql.Record) bool {
			product := ssql.GetOr(r, "product", "")
			return product == "Laptop"
		}),
	)(slices.Values(sales))

	groups3 := ssql.GroupByFields("sales_data", "region")(filtered3)
	northLaptopTotals := ssql.Aggregate("sales_data", map[string]ssql.AggregateFunc{
		"total": ssql.Sum("amount"),
		"count": ssql.Count(),
	})(groups3)

	hasResults := false
	for result := range northLaptopTotals {
		total := ssql.GetOr(result, "total", 0.0)
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("   North region laptop sales: $%.0f (%d sales)\n", total, count)
		hasResults = true
	}

	if !hasResults {
		fmt.Println("   No laptop sales found in North region")
	}
}
