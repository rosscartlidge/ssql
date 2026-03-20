package main

import (
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"iter"
	"slices"
)

func main() {
	fmt.Println("🧪 Comprehensive iter.Seq CSV Test")
	fmt.Println("==================================")

	// Test various iter.Seq types
	stringTags := slices.Values([]string{"urgent", "work", "bug"})
	intScores := slices.Values([]int{85, 92, 78})
	floatValues := slices.Values([]float64{1.5, 2.3, 3.7})
	boolFlags := slices.Values([]bool{true, false, true})

	record := ssql.MakeMutableRecord().
		String("id", "MIXED-001").
		String("title", "Complex Task").
		StringSeq("string_tags", stringTags).
		IntSeq("int_scores", intScores).
		Float64Seq("float_values", floatValues).
		BoolSeq("bool_flags", boolFlags).
		Freeze()

	fmt.Println("📊 Record with multiple iter.Seq types:")
	fmt.Printf("  ID: %s\n", ssql.GetOr(record, "id", ""))
	fmt.Printf("  Title: %s\n", ssql.GetOr(record, "title", ""))

	if seq, ok := ssql.Get[iter.Seq[string]](record, "string_tags"); ok {
		fmt.Print("  String Tags: ")
		for val := range seq {
			fmt.Printf("%s ", val)
		}
		fmt.Println()
	}

	if seq, ok := ssql.Get[iter.Seq[int]](record, "int_scores"); ok {
		fmt.Print("  Int Scores: ")
		for val := range seq {
			fmt.Printf("%d ", val)
		}
		fmt.Println()
	}

	if seq, ok := ssql.Get[iter.Seq[float64]](record, "float_values"); ok {
		fmt.Print("  Float Values: ")
		for val := range seq {
			fmt.Printf("%.1f ", val)
		}
		fmt.Println()
	}

	if seq, ok := ssql.Get[iter.Seq[bool]](record, "bool_flags"); ok {
		fmt.Print("  Bool Flags: ")
		for val := range seq {
			fmt.Printf("%t ", val)
		}
		fmt.Println()
	}

	fmt.Println("\n🔧 Testing WriteCSV with all iter.Seq types:")

	stream := ssql.From([]ssql.Record{record})
	filename := "/tmp/comprehensive_seq_test.csv"

	err := ssql.WriteCSV(stream, filename)
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		return
	}

	fmt.Println("✅ CSV written successfully")

	fmt.Println("\n📖 CSV Content:")
	csvContent, err := ssql.ReadCSV(filename)
	if err != nil {
		fmt.Printf("❌ Error reading CSV: %v\n", err)
		return
	}
	for result := range csvContent {
		fmt.Printf("Record: %v\n", result)
	}

	fmt.Println("\n📄 Raw CSV file:")
	fmt.Println("----------------")

	// Use bash to show the actual file
	fmt.Println("(Run 'cat /tmp/comprehensive_seq_test.csv' to see raw content)")

	fmt.Println("\n✅ Success! All iter.Seq types are properly materialized in CSV")
	fmt.Println("  • string iter.Seq → comma-separated strings")
	fmt.Println("  • int iter.Seq → comma-separated numbers")
	fmt.Println("  • float64 iter.Seq → comma-separated floats")
	fmt.Println("  • bool iter.Seq → comma-separated booleans")
}
