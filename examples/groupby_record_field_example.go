package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v3"
	"slices"
)

func main() {
	fmt.Println("🔍 GroupBy with Record Field Test")
	fmt.Println("=================================\n")

	// Create some nested records
	location1 := ssql.MakeMutableRecord().String("city", "New York").String("country", "USA").Freeze()
	location2 := ssql.MakeMutableRecord().String("city", "New York").String("country", "USA").Freeze() // Same content, different Record
	location3 := ssql.MakeMutableRecord().String("city", "London").String("country", "UK").Freeze()    // Different content

	records := []ssql.Record{
		ssql.MakeMutableRecord().String("user", "Alice").Nested("location", location1).Freeze(),
		ssql.MakeMutableRecord().String("user", "Bob").Nested("location", location2).Freeze(),   // Same location content as Alice
		ssql.MakeMutableRecord().String("user", "Carol").Nested("location", location3).Freeze(), // Different location
	}

	fmt.Println("📊 Sample records:")
	for i, record := range records {
		user := ssql.GetOr(record, "user", "")
		if loc, ok := ssql.Get[ssql.Record](record, "location"); ok {
			city := ssql.GetOr(loc, "city", "")
			country := ssql.GetOr(loc, "country", "")
			fmt.Printf("  %d. %s at %s, %s\n", i+1, user, city, country)
		}
	}

	fmt.Println("\n🧪 Trying to group by 'location' field (Record):")

	// Test grouping by Record field
	results := ssql.Chain(
		ssql.GroupByFields("group_data", "location"),
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
		if loc, ok := ssql.Get[ssql.Record](result, "location"); ok {
			city := ssql.GetOr(loc, "city", "")
			country := ssql.GetOr(loc, "country", "")
			fmt.Printf("  Group %d: %d records, location = %s, %s\n", groupCount, count, city, country)
		} else {
			fmt.Printf("  Group %d: %d records\n", groupCount, count)
		}
	}

	fmt.Println("\n💭 Question: Do Records with same content group together?")
	fmt.Println("   Alice and Bob both have {city: 'New York', country: 'USA'}")
	if groupCount == 2 {
		fmt.Println("✅ Success: Records with same content grouped together!")
	} else if groupCount == 3 {
		fmt.Println("⚠️  Issue: Each Record creates a separate group (like iter.Seq)")
	} else {
		fmt.Printf("🤔 Unexpected: Got %d groups\n", groupCount)
	}
}
