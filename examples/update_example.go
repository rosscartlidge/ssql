package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	fmt.Println("=== ssql Update Examples ===\n")

	// Example 1: Update single field
	fmt.Println("Example 1: Update status field")
	fmt.Println("-------------------------------")

	r1 := ssql.MakeMutableRecord()
	r1.String("name", "Alice")
	r1.String("status", "pending")
	r1.Int("age", int64(30))

	r2 := ssql.MakeMutableRecord()
	r2.String("name", "Bob")
	r2.String("status", "pending")
	r2.Int("age", int64(25))

	records := []ssql.Record{r1.Freeze(), r2.Freeze()}

	// Update all records to set status = "processed"
	updateFilter := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
		return mut.String("status", "processed")
	})
	updated := updateFilter(slices.Values(records))

	fmt.Println("Before: status=pending")
	fmt.Println("After update:")
	for record := range updated {
		name := ssql.GetOr(record, "name", "")
		status := ssql.GetOr(record, "status", "")
		fmt.Printf("  %s: status=%s\n", name, status)
	}
	fmt.Println()

	// Example 2: Update multiple fields with chaining
	fmt.Println("Example 2: Update multiple fields")
	fmt.Println("----------------------------------")

	records2 := []ssql.Record{r1.Freeze(), r2.Freeze()}

	multiUpdateFilter := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
		return mut.
			String("status", "active").
			Time("updated_at", time.Now()).
			Int("score", int64(100))
	})
	multiUpdated := multiUpdateFilter(slices.Values(records2))

	fmt.Println("Added fields: status, updated_at, score")
	for record := range multiUpdated {
		name := ssql.GetOr(record, "name", "")
		status := ssql.GetOr(record, "status", "")
		score := ssql.GetOr(record, "score", int64(0))
		fmt.Printf("  %s: status=%s, score=%d\n", name, status, score)
	}
	fmt.Println()

	// Example 3: Computed field update
	fmt.Println("Example 3: Add computed field (total = price * quantity)")
	fmt.Println("----------------------------------------------------------")

	order1 := ssql.MakeMutableRecord()
	order1.String("product", "Widget")
	order1.Float("price", float64(10.50))
	order1.Int("quantity", int64(5))

	order2 := ssql.MakeMutableRecord()
	order2.String("product", "Gadget")
	order2.Float("price", float64(25.00))
	order2.Int("quantity", int64(3))

	orders := []ssql.Record{order1.Freeze(), order2.Freeze()}

	totalFilter := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
		// Need to freeze to read values
		frozen := mut.Freeze()
		price := ssql.GetOr(frozen, "price", float64(0))
		qty := ssql.GetOr(frozen, "quantity", int64(0))
		total := price * float64(qty)

		return mut.Float("total", total)
	})
	withTotals := totalFilter(slices.Values(orders))

	fmt.Println("Orders with computed total:")
	for record := range withTotals {
		product := ssql.GetOr(record, "product", "")
		price := ssql.GetOr(record, "price", float64(0))
		qty := ssql.GetOr(record, "quantity", int64(0))
		total := ssql.GetOr(record, "total", float64(0))
		fmt.Printf("  %s: $%.2f x %d = $%.2f\n", product, price, qty, total)
	}
	fmt.Println()

	// Example 4: Conditional update
	fmt.Println("Example 4: Conditional update (age-based category)")
	fmt.Println("---------------------------------------------------")

	person1 := ssql.MakeMutableRecord()
	person1.String("name", "Alice")
	person1.Int("age", int64(30))

	person2 := ssql.MakeMutableRecord()
	person2.String("name", "Charlie")
	person2.Int("age", int64(17))

	person3 := ssql.MakeMutableRecord()
	person3.String("name", "Diana")
	person3.Int("age", int64(45))

	people := []ssql.Record{person1.Freeze(), person2.Freeze(), person3.Freeze()}

	categoryFilter := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
		frozen := mut.Freeze()
		age := ssql.GetOr(frozen, "age", int64(0))

		if age >= 18 {
			return mut.String("category", "adult")
		}
		return mut.String("category", "minor")
	})
	withCategory := categoryFilter(slices.Values(people))

	fmt.Println("People with age-based category:")
	for record := range withCategory {
		name := ssql.GetOr(record, "name", "")
		age := ssql.GetOr(record, "age", int64(0))
		category := ssql.GetOr(record, "category", "")
		fmt.Printf("  %s (age %d): category=%s\n", name, age, category)
	}
	fmt.Println()

	// Example 5: Chaining multiple updates
	fmt.Println("Example 5: Chain multiple Update operations")
	fmt.Println("--------------------------------------------")

	orders2 := []ssql.Record{order1.Freeze(), order2.Freeze()}

	// First, calculate total
	addTotal := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
		frozen := mut.Freeze()
		price := ssql.GetOr(frozen, "price", float64(0))
		qty := ssql.GetOr(frozen, "quantity", int64(0))
		return mut.Float("total", price*float64(qty))
	})

	// Then, calculate tax based on total
	addTax := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
		frozen := mut.Freeze()
		total := ssql.GetOr(frozen, "total", float64(0))
		return mut.Float("tax", total*0.08)
	})

	// Finally, calculate grand total
	addGrandTotal := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
		frozen := mut.Freeze()
		total := ssql.GetOr(frozen, "total", float64(0))
		tax := ssql.GetOr(frozen, "tax", float64(0))
		return mut.Float("grand_total", total+tax)
	})

	// Chain the updates (apply each sequentially)
	withTotal := addTotal(slices.Values(orders2))
	withTax := addTax(withTotal)
	withTaxAndTotal := addGrandTotal(withTax)

	fmt.Println("Orders with total, tax, and grand total:")
	for record := range withTaxAndTotal {
		product := ssql.GetOr(record, "product", "")
		total := ssql.GetOr(record, "total", float64(0))
		tax := ssql.GetOr(record, "tax", float64(0))
		grandTotal := ssql.GetOr(record, "grand_total", float64(0))
		fmt.Printf("  %s: subtotal=$%.2f, tax=$%.2f, total=$%.2f\n",
			product, total, tax, grandTotal)
	}
	fmt.Println()

	fmt.Println("=== Comparison with Select ===\n")
	fmt.Println("Update helper (concise):")
	fmt.Println(`  ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
      return mut.String("status", "processed")
  })`)
	fmt.Println()
	fmt.Println("Equivalent using Select (more verbose):")
	fmt.Println(`  ssql.Select(func(r ssql.Record) ssql.Record {
      return r.ToMutable().String("status", "processed").Freeze()
  })`)
	fmt.Println()
	fmt.Println("Update eliminates ToMutable() and Freeze() boilerplate!")
}
