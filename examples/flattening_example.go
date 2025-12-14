package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"iter"
	"slices"
	"strings"
)

func main() {
	fmt.Println("🔄 DotFlatten and CrossFlatten Examples")
	fmt.Println("=====================================\n")

	// Test DotFlatten with dot product expansion
	fmt.Println("🔍 DotFlatten Example - Dot Product Expansion")
	fmt.Println("---------------------------------------------")

	tags1 := slices.Values([]string{"urgent", "work"})
	scores1 := slices.Values([]int{85, 92})

	record1 := ssql.MakeMutableRecord().
		String("id", "task-123").
		String("user", "Alice").
		StringSeq("tags", tags1).
		IntSeq("scores", scores1).
		Freeze()

	fmt.Println("Input record:")
	fmt.Printf("  ID: %s, User: %s\n", ssql.GetOr(record1, "id", ""), ssql.GetOr(record1, "user", ""))

	// Show input sequences
	if tagsSeq, ok := ssql.Get[iter.Seq[string]](record1, "tags"); ok {
		fmt.Print("  Tags: ")
		for tag := range tagsSeq {
			fmt.Printf("%s ", tag)
		}
		fmt.Println()
	}

	if scoresSeq, ok := ssql.Get[iter.Seq[int]](record1, "scores"); ok {
		fmt.Print("  Scores: ")
		for score := range scoresSeq {
			fmt.Printf("%d ", score)
		}
		fmt.Println()
	}

	// Apply DotFlatten - pairs corresponding elements (dot product)
	dotResults := ssql.DotFlatten(".", "tags", "scores")(slices.Values([]ssql.Record{record1}))

	fmt.Println("\nDotFlatten Results (paired elements):")
	count := 1
	for result := range dotResults {
		id := ssql.GetOr(result, "id", "")
		user := ssql.GetOr(result, "user", "")
		tag := ssql.GetOr(result, "tags", "")
		score := ssql.GetOr(result, "scores", 0)
		fmt.Printf("  %d. ID: %s, User: %s, Tag: %s, Score: %d\n", count, id, user, tag, score)
		count++
	}

	fmt.Println("\n" + strings.Repeat("=", 50))

	// Test CrossFlatten with cartesian product expansion
	fmt.Println("\n🔍 CrossFlatten Example - Cartesian Product Expansion")
	fmt.Println("--------------------------------------------------")

	priorities := slices.Values([]string{"high", "low"})
	types := slices.Values([]string{"bug", "feature"})

	record2 := ssql.MakeMutableRecord().
		String("project", "ssql").
		StringSeq("priorities", priorities).
		StringSeq("types", types).
		Freeze()

	fmt.Println("Input record:")
	fmt.Printf("  Project: %s\n", ssql.GetOr(record2, "project", ""))

	if priSeq, ok := ssql.Get[iter.Seq[string]](record2, "priorities"); ok {
		fmt.Print("  Priorities: ")
		for pri := range priSeq {
			fmt.Printf("%s ", pri)
		}
		fmt.Println()
	}

	if typSeq, ok := ssql.Get[iter.Seq[string]](record2, "types"); ok {
		fmt.Print("  Types: ")
		for typ := range typSeq {
			fmt.Printf("%s ", typ)
		}
		fmt.Println()
	}

	// Apply CrossFlatten - all combinations (cartesian product)
	crossResults := ssql.CrossFlatten(".", "priorities", "types")(slices.Values([]ssql.Record{record2}))

	fmt.Println("\nCrossFlatten Results (all combinations):")
	count = 1
	for result := range crossResults {
		project := ssql.GetOr(result, "project", "")
		priority := ssql.GetOr(result, "priorities", "")
		typ := ssql.GetOr(result, "types", "")
		fmt.Printf("  %d. Project: %s, Priority: %s, Type: %s\n", count, project, priority, typ)
		count++
	}

	fmt.Println("\n" + strings.Repeat("=", 50))

	// Test nested record flattening
	fmt.Println("\n🔍 Nested Record Flattening Example")
	fmt.Println("-----------------------------------")

	userInfo := ssql.MakeMutableRecord().
		String("name", "Bob").
		String("email", "bob@example.com").
		Freeze()

	nestedRecord := ssql.MakeMutableRecord().
		String("id", "order-456").
		Nested("customer", userInfo).
		Float("amount", 199.99).
		Freeze()

	fmt.Println("Input nested record:")
	fmt.Printf("  ID: %s, Amount: $%.2f\n", ssql.GetOr(nestedRecord, "id", ""), ssql.GetOr(nestedRecord, "amount", 0.0))
	if customer, ok := ssql.Get[ssql.Record](nestedRecord, "customer"); ok {
		fmt.Printf("  Customer: %s (%s)\n", ssql.GetOr(customer, "name", ""), ssql.GetOr(customer, "email", ""))
	}

	// Apply DotFlatten to flatten nested structure
	nestedResults := ssql.DotFlatten(".")(slices.Values([]ssql.Record{nestedRecord}))

	fmt.Println("\nFlattened nested record:")
	for result := range nestedResults {
		for key, value := range result.All() {
			fmt.Printf("  %s: %v\n", key, value)
		}
		break // Only one result expected
	}

	fmt.Println("\n✅ All flattening operations completed!")
	fmt.Println("💡 DotFlatten: Pairs corresponding elements (linear expansion)")
	fmt.Println("💡 CrossFlatten: All combinations (exponential expansion)")
}
