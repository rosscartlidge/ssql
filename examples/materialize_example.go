package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v3"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🔧 Materialize() Function Example")
	fmt.Println("=================================\n")

	// Create records with iter.Seq fields
	tags1 := slices.Values([]string{"urgent", "work", "bug"})
	tags2 := slices.Values([]string{"urgent", "feature"})
	tags3 := slices.Values([]string{"work", "documentation"})

	records := []ssql.Record{
		ssql.MakeMutableRecord().
			String("id", "TASK-123").
			String("assignee", "Alice").
			StringSeq("tags", tags1).
			Freeze(),
		ssql.MakeMutableRecord().
			String("id", "TASK-124").
			String("assignee", "Bob").
			StringSeq("tags", tags2).
			Freeze(),
		ssql.MakeMutableRecord().
			String("id", "TASK-125").
			String("assignee", "Alice").
			StringSeq("tags", tags3).
			Freeze(),
	}

	fmt.Println("📊 Original Records:")
	for i, record := range records {
		id := ssql.GetOr(record, "id", "")
		assignee := ssql.GetOr(record, "assignee", "")
		fmt.Printf("  %d. %s (assignee: %s)\n", i+1, id, assignee)

		if tagsSeq, ok := ssql.Get[iter.Seq[string]](record, "tags"); ok {
			fmt.Print("     Tags: ")
			for tag := range tagsSeq {
				fmt.Printf("%s ", tag)
			}
			fmt.Println()
		}
	}

	fmt.Println("\n🔧 Step 1: Materialize iter.Seq fields for grouping")
	fmt.Println("--------------------------------------------------")

	// Use Materialize to convert iter.Seq to string representation
	materializedResults := ssql.Materialize("tags", "tags_key", ",")(slices.Values(records))

	fmt.Println("Materialized Records (with tags_key field):")
	var materializedRecords []ssql.Record
	for result := range materializedResults {
		materializedRecords = append(materializedRecords, result)
		id := ssql.GetOr(result, "id", "")
		assignee := ssql.GetOr(result, "assignee", "")
		tagsKey := ssql.GetOr(result, "tags_key", "")
		fmt.Printf("  %s (assignee: %s) - tags_key: \"%s\"\n", id, assignee, tagsKey)
	}

	fmt.Println("\n🏷️ Step 2: Group by materialized field (efficient!)")
	fmt.Println("--------------------------------------------------")

	// Now group by the materialized string field (fast and predictable)
	groupedResults := ssql.Chain(
		ssql.GroupByFields("group_data", "assignee"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count": ssql.Count(),
		}),
	)(slices.Values(materializedRecords))

	fmt.Println("Grouped by assignee:")
	for result := range groupedResults {
		assignee := ssql.GetOr(result, "assignee", "")
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("  %s: %d tasks\n", assignee, count)
	}

	fmt.Println("\n⚠️ Step 3: Demonstrate GroupBy validation (rejects complex fields)")
	fmt.Println("----------------------------------------------------------------")

	// Try to group by the original iter.Seq field - should be rejected
	fmt.Println("Attempting to group by 'tags' field (iter.Seq[string])...")

	invalidGroupResults := ssql.Chain(
		ssql.GroupByFields("group_data", "tags"), // This will skip records with iter.Seq fields
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count": ssql.Count(),
		}),
	)(slices.Values(records))

	resultCount := 0
	for result := range invalidGroupResults {
		resultCount++
		fmt.Printf("  Group %d: %v\n", resultCount, result)
	}

	if resultCount == 0 {
		fmt.Println("  ✅ No results - GroupByFields correctly rejected iter.Seq fields!")
	} else {
		fmt.Printf("  ❌ Unexpected: Got %d results when should have been rejected\n", resultCount)
	}

	fmt.Println("\n💡 Key Benefits of Materialize():")
	fmt.Println("  🚀 Performance: Only materialize when needed for grouping")
	fmt.Println("  🎯 Control: User decides materialization format (separator, etc.)")
	fmt.Println("  🔒 Predictable: GroupBy behavior guaranteed with simple values")
	fmt.Println("  🧠 Explicit: Code clearly shows intent to group by sequence content")

	fmt.Println("\n🔄 Compare with flattening approaches:")
	fmt.Println("  • Materialize: 3 records → 3 records (same count, adds key field)")
	fmt.Println("  • CrossFlatten: 3 records → 8+ records (exponential expansion)")
	fmt.Println("  • DotFlatten: 3 records → varies (depends on sequence lengths)")
}
