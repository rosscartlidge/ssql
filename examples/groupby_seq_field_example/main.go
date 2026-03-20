package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🔍 GroupBy with iter.Seq Field Test")
	fmt.Println("===================================")

	// Create records with iter.Seq fields
	tags1 := slices.Values([]string{"urgent", "work"})
	tags2 := slices.Values([]string{"urgent", "work"}) // Same content, different sequence
	tags3 := slices.Values([]string{"personal"})

	records := []ssql.Record{
		ssql.MakeMutableRecord().String("user", "Alice").StringSeq("tags", tags1).Freeze(),
		ssql.MakeMutableRecord().String("user", "Bob").StringSeq("tags", tags2).Freeze(),   // Same content as Alice
		ssql.MakeMutableRecord().String("user", "Carol").StringSeq("tags", tags3).Freeze(), // Different content
	}

	fmt.Println("📊 Sample records:")
	for i, record := range records {
		user := ssql.GetOr(record, "user", "")
		if tagsSeq, ok := ssql.Get[iter.Seq[string]](record, "tags"); ok {
			fmt.Printf("  %d. %s with tags: ", i+1, user)
			for tag := range tagsSeq {
				fmt.Printf("%s ", tag)
			}
			fmt.Println()
		}
	}

	fmt.Println("\n🧪 Trying to group by 'tags' field (iter.Seq[string]):")

	// This will likely produce unexpected results
	results := ssql.Chain(
		ssql.GroupByFields("group_data", "tags"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count": ssql.Count(),
		}),
	)(slices.Values(records))

	fmt.Println("Group results:")
	groupCount := 0
	for result := range results {
		groupCount++
		count := ssql.GetOr(result, "count", int64(0))

		// Try to show what the grouping key looks like
		if tagsField, ok := ssql.Get[iter.Seq[string]](result, "tags"); ok {
			fmt.Printf("  Group %d: %d records, tags field = %T\n", groupCount, count, tagsField)
		}
	}

	fmt.Println("\n⚠️  Problem: iter.Seq fields don't group meaningfully!")
	fmt.Println("💡 Solution: Extract/materialize sequence content for grouping")
	fmt.Println("   or use a different field that represents the sequence content")
}
