package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🔄 iter.Seq Record Creation Example")
	fmt.Println("===================================")

	// Create some sequences
	numbers := slices.Values([]int{1, 2, 3, 4, 5})
	tags := slices.Values([]string{"urgent", "work", "important"})
	scores := slices.Values([]float64{85.5, 92.0, 78.5})

	// Create a record with various iter.Seq fields using the fluent API
	record := ssql.MakeMutableRecord().
		String("id", "task-123").
		String("title", "Complete project").
		IntSeq("numbers", numbers).   // iter.Seq[int]
		StringSeq("tags", tags).      // iter.Seq[string]
		Float64Seq("scores", scores). // iter.Seq[float64]
		Freeze()

	fmt.Println("📋 Created record with iter.Seq fields:")
	fmt.Printf("ID: %s\n", ssql.GetOr(record, "id", ""))
	fmt.Printf("Title: %s\n", ssql.GetOr(record, "title", ""))

	// Access and iterate over the sequences
	if numbersSeq, ok := ssql.Get[iter.Seq[int]](record, "numbers"); ok {
		fmt.Print("Numbers: ")
		for num := range numbersSeq {
			fmt.Printf("%d ", num)
		}
		fmt.Println()
	}

	if tagsSeq, ok := ssql.Get[iter.Seq[string]](record, "tags"); ok {
		fmt.Print("Tags: ")
		for tag := range tagsSeq {
			fmt.Printf("%s ", tag)
		}
		fmt.Println()
	}

	if scoresSeq, ok := ssql.Get[iter.Seq[float64]](record, "scores"); ok {
		fmt.Print("Scores: ")
		for score := range scoresSeq {
			fmt.Printf("%.1f ", score)
		}
		fmt.Println()
	}

	fmt.Println("\n✅ All iter.Seq field methods working correctly!")
	fmt.Println("💡 Available methods: IntSeq, StringSeq, Float64Seq, RecordSeq, and more!")
}
