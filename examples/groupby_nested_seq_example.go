package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🔍 GroupBy with Nested iter.Seq in Record Test")
	fmt.Println("===============================================\n")

	// Create Records with iter.Seq fields inside them
	tags1 := slices.Values([]string{"urgent", "work"})
	tags2 := slices.Values([]string{"urgent", "work"}) // Same content, different sequence
	tags3 := slices.Values([]string{"personal"})

	// Nested records containing iter.Seq fields
	profile1 := ssql.MakeMutableRecord().
		String("role", "developer").
		StringSeq("tags", tags1).
		Freeze()

	profile2 := ssql.MakeMutableRecord().
		String("role", "developer").
		StringSeq("tags", tags2). // Same content as profile1, but different sequence instance
		Freeze()

	profile3 := ssql.MakeMutableRecord().
		String("role", "manager").
		StringSeq("tags", tags3).
		Freeze()

	records := []ssql.Record{
		ssql.MakeMutableRecord().String("user", "Alice").Nested("profile", profile1).Freeze(),
		ssql.MakeMutableRecord().String("user", "Bob").Nested("profile", profile2).Freeze(),   // Same profile content as Alice
		ssql.MakeMutableRecord().String("user", "Carol").Nested("profile", profile3).Freeze(), // Different profile
	}

	fmt.Println("📊 Sample records:")
	for i, record := range records {
		user := ssql.GetOr(record, "user", "")
		if profile, ok := ssql.Get[ssql.Record](record, "profile"); ok {
			role := ssql.GetOr(profile, "role", "")
			if tagsSeq, ok := ssql.Get[iter.Seq[string]](profile, "tags"); ok {
				fmt.Printf("  %d. %s (%s) with tags: ", i+1, user, role)
				for tag := range tagsSeq {
					fmt.Printf("%s ", tag)
				}
				fmt.Println()
			}
		}
	}

	fmt.Println("\n🧪 Trying to group by 'profile' field (Record containing iter.Seq):")

	results := ssql.Chain(
		ssql.GroupByFields("group_data", "profile"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count": ssql.Count(),
		}),
	)(slices.Values(records))

	fmt.Println("Group results:")
	groupCount := 0
	for result := range results {
		groupCount++
		count := ssql.GetOr(result, "count", int64(0))

		if profile, ok := ssql.Get[ssql.Record](result, "profile"); ok {
			role := ssql.GetOr(profile, "role", "")
			fmt.Printf("  Group %d: %d records, role = %s\n", groupCount, count, role)
		}
	}

	fmt.Println("\n💭 Expected: Alice and Bob should group together (same role + same tag content)")
	fmt.Printf("💭 Actual: Got %d groups\n", groupCount)

	if groupCount == 2 {
		fmt.Println("✅ Records with same content (including seq content) grouped together")
	} else if groupCount == 3 {
		fmt.Println("⚠️  Problem: iter.Seq fields in Records prevent proper grouping!")
		fmt.Println("   Alice and Bob have identical content but different sequence instances")
	} else {
		fmt.Printf("🤔 Unexpected result: %d groups\n", groupCount)
	}

	fmt.Println("\n🔍 This demonstrates the deep problem:")
	fmt.Println("   Records are only 'equal' for grouping if ALL nested iter.Seq instances are identical")
	fmt.Println("   Same content in different sequence instances = different groups")
}
