package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"slices"
	"strings"
)

func main() {
	fmt.Println("🧪 ssql GroupBy and Aggregation Test")
	fmt.Println("========================================")

	// Create sample sales data
	salesData := []ssql.Record{
		ssql.MakeMutableRecord().String("region", "North").String("product", "Laptop").Float("amount", 1200).Int("quantity", 2).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Phone").Float("amount", 800).Int("quantity", 4).Freeze(),
		ssql.MakeMutableRecord().String("region", "North").String("product", "Phone").Float("amount", 900).Int("quantity", 3).Freeze(),
		ssql.MakeMutableRecord().String("region", "East").String("product", "Laptop").Float("amount", 1100).Int("quantity", 1).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Laptop").Float("amount", 1300).Int("quantity", 2).Freeze(),
		ssql.MakeMutableRecord().String("region", "North").String("product", "Tablet").Float("amount", 500).Int("quantity", 5).Freeze(),
		ssql.MakeMutableRecord().String("region", "East").String("product", "Phone").Float("amount", 850).Int("quantity", 2).Freeze(),
		ssql.MakeMutableRecord().String("region", "South").String("product", "Tablet").Float("amount", 450).Int("quantity", 3).Freeze(),
		ssql.MakeMutableRecord().String("region", "West").String("product", "Laptop").Float("amount", 1400).Int("quantity", 1).Freeze(),
		ssql.MakeMutableRecord().String("region", "West").String("product", "Phone").Float("amount", 750).Int("quantity", 4).Freeze(),
	}

	fmt.Printf("📊 Sample Data (%d records):\n", len(salesData))
	for i, record := range salesData {
		region := ssql.GetOr(record, "region", "")
		product := ssql.GetOr(record, "product", "")
		amount := ssql.GetOr(record, "amount", 0.0)
		quantity := ssql.GetOr(record, "quantity", int64(0))
		fmt.Printf("  %d. %s/%s: $%.0f (qty: %d)\n", i+1, region, product, amount, quantity)
	}

	fmt.Println("\n" + strings.Repeat("=", 50))

	// Test 1: Group by single field (region)
	fmt.Println("\n🔍 Test 1: Group by Region")
	fmt.Println(strings.Repeat("-", 25))

	results1 := ssql.Chain(
		ssql.GroupByFields("group_data", "region"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"total_sales": ssql.Sum("amount"),
			"total_qty":   ssql.Sum("quantity"),
			"avg_amount":  ssql.Avg("amount"),
			"count":       ssql.Count(),
			"max_amount":  ssql.Max[float64]("amount"),
			"min_amount":  ssql.Min[float64]("amount"),
		}),
	)(slices.Values(salesData))

	fmt.Println("Results by Region:")
	for result := range results1 {
		region := ssql.GetOr(result, "region", "")
		totalSales := ssql.GetOr(result, "total_sales", 0.0)
		totalQty := ssql.GetOr(result, "total_qty", 0.0)
		avgAmount := ssql.GetOr(result, "avg_amount", 0.0)
		count := ssql.GetOr(result, "count", int64(0))
		maxAmount := ssql.GetOr(result, "max_amount", 0.0)
		minAmount := ssql.GetOr(result, "min_amount", 0.0)

		fmt.Printf("  %s: $%.0f total, %.0f items, $%.0f avg (min: $%.0f, max: $%.0f, count: %d)\n",
			region, totalSales, totalQty, avgAmount, minAmount, maxAmount, count)
	}

	// Test 2: Group by multiple fields (region + product)
	fmt.Println("\n🔍 Test 2: Group by Region + Product")
	fmt.Println(strings.Repeat("-", 35))

	results2 := ssql.Chain(
		ssql.GroupByFields("group_data", "region", "product"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"total_sales": ssql.Sum("amount"),
			"total_qty":   ssql.Sum("quantity"),
			"count":       ssql.Count(),
		}),
	)(slices.Values(salesData))

	fmt.Println("Results by Region + Product:")
	for result := range results2 {
		region := ssql.GetOr(result, "region", "")
		product := ssql.GetOr(result, "product", "")
		totalSales := ssql.GetOr(result, "total_sales", 0.0)
		totalQty := ssql.GetOr(result, "total_qty", 0.0)
		count := ssql.GetOr(result, "count", int64(0))

		fmt.Printf("  %s/%s: $%.0f total, %.0f items (%d sales)\n",
			region, product, totalSales, totalQty, count)
	}

	// Test 3: Functional pipeline with filtering + grouping
	fmt.Println("\n🔍 Test 3: High-Value Sales (>= $1000) by Product")
	fmt.Println(strings.Repeat("-", 45))

	highValueSales := ssql.Where(func(r ssql.Record) bool {
		amount := ssql.GetOr(r, "amount", 0.0)
		return amount >= 1000
	})(slices.Values(salesData))

	results3 := ssql.Chain(
		ssql.GroupByFields("group_data", "product"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"total_sales": ssql.Sum("amount"),
			"avg_amount":  ssql.Avg("amount"),
			"count":       ssql.Count(),
		}),
	)(highValueSales)

	fmt.Println("High-Value Sales by Product:")
	for result := range results3 {
		product := ssql.GetOr(result, "product", "")
		totalSales := ssql.GetOr(result, "total_sales", 0.0)
		avgAmount := ssql.GetOr(result, "avg_amount", 0.0)
		count := ssql.GetOr(result, "count", int64(0))

		fmt.Printf("  %s: $%.0f total, $%.0f avg (%d sales)\n",
			product, totalSales, avgAmount, count)
	}

	// Test 4: Complex aggregation with chain
	fmt.Println("\n🔍 Test 4: Complex Pipeline - Non-Tablet Sales by Region")
	fmt.Println(strings.Repeat("-", 55))

	complexPipeline := ssql.Chain(
		ssql.Where(func(r ssql.Record) bool {
			product := ssql.GetOr(r, "product", "")
			return product != "Tablet"
		}),
		ssql.Where(func(r ssql.Record) bool {
			amount := ssql.GetOr(r, "amount", 0.0)
			return amount >= 800
		}),
	)(slices.Values(salesData))

	results4 := ssql.Chain(
		ssql.GroupByFields("group_data", "region"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"total_sales":   ssql.Sum("amount"),
			"avg_amount":    ssql.Avg("amount"),
			"count":         ssql.Count(),
			"first_product": ssql.First[string]("product"),
			"last_product":  ssql.Last[string]("product"),
		}),
	)(complexPipeline)

	fmt.Println("Non-Tablet High-Value Sales by Region:")
	for result := range results4 {
		region := ssql.GetOr(result, "region", "")
		totalSales := ssql.GetOr(result, "total_sales", 0.0)
		avgAmount := ssql.GetOr(result, "avg_amount", 0.0)
		count := ssql.GetOr(result, "count", int64(0))
		firstProduct := ssql.GetOr(result, "first_product", "")
		lastProduct := ssql.GetOr(result, "last_product", "")

		fmt.Printf("  %s: $%.0f total, $%.0f avg (%d sales) [%s...%s]\n",
			region, totalSales, avgAmount, count, firstProduct, lastProduct)
	}

	// Test 5: Collect and CollectSeq aggregations
	fmt.Println("\n🔍 Test 5: Collect vs CollectSeq - Gathering All Values")
	fmt.Println(strings.Repeat("-", 55))

	results5 := ssql.Chain(
		ssql.GroupByFields("group_data", "region"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count":           ssql.Count(),
			"all_products":    ssql.Collect("product"),            // Returns []any
			"all_amounts_seq": ssql.CollectSeq[float64]("amount"), // Returns iter.Seq[any], type-safe
		}),
	)(slices.Values(salesData))

	fmt.Println("Collected Values by Region:")
	for result := range results5 {
		region := ssql.GetOr(result, "region", "")
		count := ssql.GetOr(result, "count", int64(0))

		// Collect returns []any - can check length, index directly
		products := ssql.GetOr(result, "all_products", []any{})
		fmt.Printf("  %s (%d sales):\n", region, count)
		fmt.Printf("    Products (Collect []any): %v\n", products)

		// CollectSeq returns iter.Seq[any] - iterate to access values
		amountsSeq, ok := ssql.Get[func(func(any) bool)](result, "all_amounts_seq")
		if ok {
			fmt.Printf("    Amounts (CollectSeq iter.Seq): ")
			first := true
			for amount := range amountsSeq {
				if !first {
					fmt.Print(", ")
				}
				fmt.Printf("$%.0f", amount)
				first = false
			}
			fmt.Println()
		}
	}

	fmt.Println("\n✅ All tests completed! You can modify this code to test your ideas.")
	fmt.Println("💡 Try changing grouping fields, aggregations, or filters to experiment.")
}
