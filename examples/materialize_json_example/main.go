package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🧪 MaterializeJSON vs Materialize Comparison")
	fmt.Println("============================================")

	// Create records with various complex field types
	tags1 := slices.Values([]string{"urgent", "work"})
	tags2 := slices.Values([]string{"feature", "enhancement"})
	numbers := slices.Values([]int{85, 92, 78})
	bools := slices.Values([]bool{true, false})

	// Create nested records
	user1 := ssql.MakeMutableRecord().String("name", "Alice").Int("age", 30).Freeze()
	user2 := ssql.MakeMutableRecord().String("name", "Bob").Int("age", 25).Freeze()

	tasks := []ssql.Record{
		ssql.MakeMutableRecord().
			String("id", "TASK-001").
			String("team", "Backend").
			StringSeq("tags", tags1).
			IntSeq("scores", numbers).
			BoolSeq("flags", bools).
			Nested("user", user1).
			Freeze(),
		ssql.MakeMutableRecord().
			String("id", "TASK-002").
			String("team", "Frontend").
			StringSeq("tags", tags2).
			Nested("user", user2).
			Freeze(),
	}

	fmt.Println("📊 Original records:")
	for i, task := range tasks {
		id := ssql.GetOr(task, "id", "")
		team := ssql.GetOr(task, "team", "")
		fmt.Printf("  %d. %s (%s team)\n", i+1, id, team)

		if user, ok := ssql.Get[ssql.Record](task, "user"); ok {
			name := ssql.GetOr(user, "name", "")
			age := ssql.GetOr(user, "age", 0)
			fmt.Printf("     User: %s (age %d)\n", name, age)
		}

		if tagsSeq, ok := ssql.Get[iter.Seq[string]](task, "tags"); ok {
			fmt.Print("     Tags: ")
			for tag := range tagsSeq {
				fmt.Printf("%s ", tag)
			}
			fmt.Println()
		}
	}

	fmt.Println("\n🔧 Test 1: Old Materialize() with string sequences (comma-separated)")
	fmt.Println("---------------------------------------------------------------------")

	oldResults := ssql.Chain(
		ssql.Materialize("tags", "tags_string", ","),
		ssql.GroupByFields("group_data", "tags_string"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count": ssql.Count(),
			"teams": ssql.Collect("team"),
		}),
	)(slices.Values(tasks))

	fmt.Println("Old Materialize results:")
	for result := range oldResults {
		tagsString := ssql.GetOr(result, "tags_string", "")
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("  Tags: \"%s\" → %d tasks\n", tagsString, count)
	}

	fmt.Println("\n🆕 Test 2: New MaterializeJSON() with string sequences (JSON arrays)")
	fmt.Println("--------------------------------------------------------------------")

	newResults := ssql.Chain(
		ssql.MaterializeJSON("tags", "tags_json"),
		ssql.GroupByFields("group_data", "tags_json"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count": ssql.Count(),
			"teams": ssql.Collect("team"),
		}),
	)(slices.Values(tasks))

	fmt.Println("MaterializeJSON results:")
	for result := range newResults {
		tagsJSON := ssql.GetOr(result, "tags_json", "")
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("  Tags: %s → %d tasks\n", tagsJSON, count)
	}

	fmt.Println("\n🏗️ Test 3: MaterializeJSON with Record fields (impossible with old Materialize)")
	fmt.Println("------------------------------------------------------------------------------")

	userResults := ssql.Chain(
		ssql.MaterializeJSON("user", "user_json"),
		ssql.GroupByFields("group_data", "user_json"),
		ssql.Aggregate("group_data", map[string]ssql.AggregateFunc{
			"count":    ssql.Count(),
			"task_ids": ssql.Collect("id"),
		}),
	)(slices.Values(tasks))

	fmt.Println("MaterializeJSON with Record fields:")
	for result := range userResults {
		userJSON := ssql.GetOr(result, "user_json", "")
		count := ssql.GetOr(result, "count", int64(0))
		fmt.Printf("  User: %s → %d tasks\n", userJSON, count)
	}

	fmt.Println("\n✅ Key Advantages of MaterializeJSON:")
	fmt.Println("  🎯 Type preservation: [\"urgent\",\"work\"] vs \"urgent,work\"")
	fmt.Println("  🏗️ Handles any complex type: Records, nested structures")
	fmt.Println("  📊 JSON standard: parseable, structured representation")
	fmt.Println("  🔄 Order sensitive: [\"A\",\"B\"] ≠ [\"B\",\"A\"] (correct for sequences)")
	fmt.Println("  🌐 Universal: works with any JSON-serializable data")
}
