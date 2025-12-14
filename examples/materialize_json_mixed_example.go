package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🚀 MaterializeJSON with Mixed Complex Types")
	fmt.Println("==========================================\n")

	// Create records with multiple complex field types
	tags := slices.Values([]string{"critical", "security"})
	scores := slices.Values([]int{95, 88, 92})
	metadata := ssql.MakeMutableRecord().
		String("priority", "high").
		Int("version", 2).
		Float("weight", 1.5).
		Freeze()

	task := ssql.MakeMutableRecord().
		String("id", "COMPLEX-001").
		String("title", "Security Patch").
		StringSeq("tags", tags).
		IntSeq("scores", scores).
		Nested("metadata", metadata).
		Freeze()

	fmt.Println("📊 Original complex record:")
	fmt.Printf("  ID: %s\n", ssql.GetOr(task, "id", ""))
	fmt.Printf("  Title: %s\n", ssql.GetOr(task, "title", ""))

	if tagsSeq, ok := ssql.Get[iter.Seq[string]](task, "tags"); ok {
		fmt.Print("  Tags: ")
		for tag := range tagsSeq {
			fmt.Printf("%s ", tag)
		}
		fmt.Println()
	}

	if scoresSeq, ok := ssql.Get[iter.Seq[int]](task, "scores"); ok {
		fmt.Print("  Scores: ")
		for score := range scoresSeq {
			fmt.Printf("%d ", score)
		}
		fmt.Println()
	}

	if meta, ok := ssql.Get[ssql.Record](task, "metadata"); ok {
		priority := ssql.GetOr(meta, "priority", "")
		version := ssql.GetOr(meta, "version", 0)
		weight := ssql.GetOr(meta, "weight", 0.0)
		fmt.Printf("  Metadata: priority=%s, version=%d, weight=%.1f\n", priority, version, weight)
	}

	stream := ssql.From([]ssql.Record{task})

	fmt.Println("\n🔧 Test: MaterializeJSON with different complex field types")
	fmt.Println("-----------------------------------------------------------")

	// Test with string sequence
	fmt.Println("\n1. String sequence materialization:")
	tagsResult := ssql.Chain(
		ssql.MaterializeJSON("tags", "tags_json"),
	)(stream)
	for result := range tagsResult {
		fmt.Printf("   tags_json: %s\n", ssql.GetOr(result, "tags_json", ""))
	}

	// Test with int sequence
	fmt.Println("\n2. Int sequence materialization:")
	scoresResult := ssql.Chain(
		ssql.MaterializeJSON("scores", "scores_json"),
	)(ssql.From([]ssql.Record{task}))
	for result := range scoresResult {
		fmt.Printf("   scores_json: %s\n", ssql.GetOr(result, "scores_json", ""))
	}

	// Test with nested Record
	fmt.Println("\n3. Nested Record materialization:")
	metaResult := ssql.Chain(
		ssql.MaterializeJSON("metadata", "metadata_json"),
	)(ssql.From([]ssql.Record{task}))
	for result := range metaResult {
		fmt.Printf("   metadata_json: %s\n", ssql.GetOr(result, "metadata_json", ""))
	}

	// Test with simple field (should work too)
	fmt.Println("\n4. Simple field materialization:")
	titleResult := ssql.Chain(
		ssql.MaterializeJSON("title", "title_json"),
	)(ssql.From([]ssql.Record{task}))
	for result := range titleResult {
		fmt.Printf("   title_json: %s\n", ssql.GetOr(result, "title_json", ""))
	}

	fmt.Println("\n✅ MaterializeJSON successfully handles:")
	fmt.Println("  📚 iter.Seq[string] → JSON string arrays")
	fmt.Println("  🔢 iter.Seq[int] → JSON number arrays")
	fmt.Println("  🏗️ Record → JSON objects")
	fmt.Println("  📝 Simple values → JSON primitives")
	fmt.Println("  🌟 All types can now be used for consistent grouping!")
}
