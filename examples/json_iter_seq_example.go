package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v3"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🧪 Testing WriteJSON with iter.Seq fields")
	fmt.Println("=========================================\n")

	// Create records with iter.Seq fields
	tags1 := slices.Values([]string{"urgent", "work"})
	tags2 := slices.Values([]string{"feature", "enhancement"})

	records := []ssql.Record{
		ssql.MakeMutableRecord().
			String("id", "TASK-001").
			String("title", "Fix bug").
			StringSeq("tags", tags1).
			Freeze(),
		ssql.MakeMutableRecord().
			String("id", "TASK-002").
			String("title", "Add feature").
			StringSeq("tags", tags2).
			Freeze(),
	}

	fmt.Println("📊 Records with iter.Seq fields:")
	for i, record := range records {
		id := ssql.GetOr(record, "id", "")
		title := ssql.GetOr(record, "title", "")
		fmt.Printf("  %d. %s: %s\n", i+1, id, title)

		if tagsSeq, ok := ssql.Get[iter.Seq[string]](record, "tags"); ok {
			fmt.Print("     Tags: ")
			for tag := range tagsSeq {
				fmt.Printf("%s ", tag)
			}
			fmt.Println()
		}
	}

	fmt.Println("\n🔧 Testing WriteJSON with iter.Seq field included:")

	// Create a stream from records
	stream := ssql.From(records)

	// Try to write JSON including the iter.Seq field
	filename := "/tmp/test_with_iterseq.json"

	fmt.Printf("Writing to: %s\n", filename)

	err := ssql.WriteJSON(stream, filename)
	if err != nil {
		fmt.Printf("❌ Error writing JSON: %v\n", err)
		return
	}

	fmt.Println("✅ JSON written successfully")

	fmt.Println("\n📖 Raw JSON file:")
	fmt.Println("------------------")
}
