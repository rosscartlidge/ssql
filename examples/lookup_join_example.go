// Example: LookupJoin for efficient multi-field enrichment
//
// This example demonstrates using LookupJoin to enrich records with data from
// a lookup table, performing multiple lookups in a single pass.
//
// Use case: You have a dataset with multiple foreign keys pointing to the same
// lookup table (e.g., a_kind and z_kind both reference kind.csv). Instead of
// joining twice and reading the lookup file twice, LookupJoin does it in one pass.
package main

import (
	"fmt"
	"slices"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	// Main data: products with category codes for origin and destination
	products := slices.Values([]ssql.Record{
		ssql.NewRecord(map[string]any{"name": "Widget", "origin_cat": int64(1), "dest_cat": int64(2)}),
		ssql.NewRecord(map[string]any{"name": "Gadget", "origin_cat": int64(2), "dest_cat": int64(1)}),
		ssql.NewRecord(map[string]any{"name": "Gizmo", "origin_cat": int64(1), "dest_cat": int64(1)}),
	})

	// Lookup table: category codes to names
	categories := slices.Values([]ssql.Record{
		ssql.NewRecord(map[string]any{"cat_id": int64(1), "cat_name": "Electronics"}),
		ssql.NewRecord(map[string]any{"cat_id": int64(2), "cat_name": "Hardware"}),
	})

	// Define two lookups: one for origin, one for destination
	// Each lookup renames cat_name to a unique field name
	clauses := []ssql.LookupClause{
		ssql.Lookup("origin_cat", "cat_id", "cat_name", "origin_name"),
		ssql.Lookup("dest_cat", "cat_id", "cat_name", "dest_name"),
	}

	// Apply the lookup join - reads categories once, applies both lookups
	enriched := ssql.LookupJoin(categories, clauses)(products)

	// Display results
	fmt.Println("Enriched Products:")
	fmt.Println("------------------")
	for r := range enriched {
		fmt.Printf("%s: %s -> %s\n",
			ssql.GetOr(r, "name", ""),
			ssql.GetOr(r, "origin_name", ""),
			ssql.GetOr(r, "dest_name", ""))
	}

	// Output:
	// Enriched Products:
	// ------------------
	// Widget: Electronics -> Hardware
	// Gadget: Hardware -> Electronics
	// Gizmo: Electronics -> Electronics
}
