package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v3"
)

func main() {
	fmt.Println("🔄 ssql Safe/Unsafe Filter Mixing Examples")
	fmt.Println("===============================================\n")

	// Pattern 1: Start Normal, Add Error Handling, Continue Normal
	fmt.Println("📋 Pattern 1: Mixed Pipeline (Normal → Safe → Normal)")
	fmt.Println("======================================================")
	demonstrateMixedPipeline()

	// Pattern 2: I/O with Safe, Processing with Normal
	fmt.Println("\n📊 Pattern 2: I/O with Safe, Processing with Normal")
	fmt.Println("===================================================")
	demonstrateIOWithSafe()

	// Pattern 3: Fail-Fast with Unsafe
	fmt.Println("\n⚡ Pattern 3: Fail-Fast with Unsafe")
	fmt.Println("===================================")
	demonstrateFailFast()

	// Pattern 4: Best-Effort Processing
	fmt.Println("\n🎯 Pattern 4: Best-Effort Processing")
	fmt.Println("====================================")
	demonstrateBestEffort()

	fmt.Println("\n✨ Summary: Conversion Utilities")
	fmt.Println("================================")
	printConversionSummary()
}

func demonstrateMixedPipeline() {
	fmt.Println("Processing transaction data with mixed error handling...")

	// Start with normal data
	transactions := []ssql.Record{
		ssql.MakeMutableRecord().String("id", "TXN001").String("amount_str", "125.50").String("category", "electronics").Freeze(),
		ssql.MakeMutableRecord().String("id", "TXN002").String("amount_str", "invalid").String("category", "books").Freeze(),
		ssql.MakeMutableRecord().String("id", "TXN003").String("amount_str", "89.99").String("category", "clothing").Freeze(),
		ssql.MakeMutableRecord().String("id", "TXN004").String("amount_str", "250.00").String("category", "electronics").Freeze(),
		ssql.MakeMutableRecord().String("id", "TXN005").String("amount_str", "45.bad").String("category", "food").Freeze(),
		ssql.MakeMutableRecord().String("id", "TXN006").String("amount_str", "180.25").String("category", "electronics").Freeze(),
	}

	// Start with normal iterator
	stream := ssql.From(transactions)

	// Apply normal filter - remove empty IDs
	filtered := ssql.Where(func(r ssql.Record) bool {
		return ssql.GetOr(r, "id", "") != ""
	})(stream)

	// Convert to Safe for error-prone parsing
	safeStream := ssql.Safe(filtered)

	// Use Safe filter for parsing amounts
	parsed := ssql.SelectSafe(func(r ssql.Record) (ssql.Record, error) {
		amountStr := ssql.GetOr(r, "amount_str", "")
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil {
			return ssql.MakeMutableRecord().Freeze(), fmt.Errorf("invalid amount '%s' in record %s",
				amountStr, ssql.GetOr(r, "id", "unknown"))
		}

		return ssql.MakeMutableRecord().
			String("id", ssql.GetOr(r, "id", "")).
			Float("amount", amount).
			String("category", ssql.GetOr(r, "category", "")).
			Freeze(), nil
	})(safeStream)

	// Convert back to normal, ignoring errors
	cleanData := ssql.IgnoreErrors(parsed)

	// Continue with normal filters
	final := ssql.Chain(
		ssql.Where(func(r ssql.Record) bool {
			amount := ssql.GetOr(r, "amount", 0.0)
			return amount > 100.0
		}),
		ssql.SortBy(func(r ssql.Record) float64 {
			// Return negative to sort in descending order
			return -ssql.GetOr(r, "amount", 0.0)
		}),
	)(cleanData)

	// Display results
	fmt.Println("✅ Successfully processed transactions (amount > $100):")
	for record := range final {
		id := ssql.GetOr(record, "id", "unknown")
		amount := ssql.GetOr(record, "amount", 0.0)
		category := ssql.GetOr(record, "category", "unknown")
		fmt.Printf("  • %s: $%.2f (%s)\n", id, amount, category)
	}
	fmt.Println("  ℹ️  Note: Records with invalid amounts were silently skipped")
}

func demonstrateIOWithSafe() {
	fmt.Println("Creating sample CSV file and processing with Safe filters...")

	// Create sample CSV file
	csvContent := `name,age,email
Alice,25,alice@example.com
Bob,invalid,bob@example.com
Carol,30,carol@example.com
David,28,
Eve,35,eve@example.com
Frank,abc,frank@example.com`

	tmpFile := "/tmp/ssql_safe_example.csv"
	err := os.WriteFile(tmpFile, []byte(csvContent), 0644)
	if err != nil {
		fmt.Printf("❌ Error writing CSV: %v\n", err)
		return
	}
	defer os.Remove(tmpFile)

	// Read CSV with Safe version
	csvStream := ssql.ReadCSVSafe(tmpFile)

	// Validate records with Safe filter
	validated := ssql.WhereSafe(func(r ssql.Record) (bool, error) {
		// Note: CSV parsing auto-converts numeric strings to int64/float64
		// So "25" becomes int64(25). We need to handle both cases.

		// Try to get age as int64 first (already parsed by CSV reader)
		age, ageOk := ssql.Get[int64](r, "age")
		if !ageOk {
			return false, fmt.Errorf("invalid or missing age for %s",
				ssql.GetOr(r, "name", "unknown"))
		}

		// Check if email exists
		email := ssql.GetOr(r, "email", "")
		if email == "" {
			return false, fmt.Errorf("missing email for %s",
				ssql.GetOr(r, "name", "unknown"))
		}

		// Only keep adults
		return age >= 18, nil
	})(csvStream)

	// Convert to normal for fast processing
	normalStream := ssql.IgnoreErrors(validated)

	// Process with normal filters
	final := ssql.Chain(
		ssql.SortBy(func(r ssql.Record) string {
			return ssql.GetOr(r, "name", "")
		}),
	)(normalStream)

	// Display results
	fmt.Println("✅ Valid records (age ≥ 18, valid data):")
	for record := range final {
		name := ssql.GetOr(record, "name", "unknown")
		age := ssql.GetOr(record, "age", int64(0))
		email := ssql.GetOr(record, "email", "unknown")
		fmt.Printf("  • %s (age %d): %s\n", name, age, email)
	}
	fmt.Println("  ℹ️  Note: Invalid records were filtered out during validation")
}

func demonstrateFailFast() {
	fmt.Println("Processing critical financial data with fail-fast error handling...")

	// Create sample CSV with critical data
	csvContent := `account,balance,status
ACC001,1250.50,active
ACC002,3400.75,active
ACC003,890.25,active`

	tmpFile := "/tmp/ssql_failfast_example.csv"
	err := os.WriteFile(tmpFile, []byte(csvContent), 0644)
	if err != nil {
		fmt.Printf("❌ Error writing CSV: %v\n", err)
		return
	}
	defer os.Remove(tmpFile)

	// Process with fail-fast approach
	result := processFailFast(tmpFile)
	if result != nil {
		fmt.Printf("❌ Processing failed: %v\n", result)
		fmt.Println("  ℹ️  Note: Pipeline stopped immediately on first error")
		return
	}

	fmt.Println("✅ All critical records processed successfully!")

	// Now demonstrate with corrupted data
	fmt.Println("\nTesting fail-fast with corrupted data:")
	corruptedContent := `account,balance,status
ACC001,1250.50,active
ACC002,CORRUPTED,active
ACC003,890.25,active`

	tmpFile2 := "/tmp/ssql_corrupted_example.csv"
	err = os.WriteFile(tmpFile2, []byte(corruptedContent), 0644)
	if err != nil {
		fmt.Printf("❌ Error writing CSV: %v\n", err)
		return
	}
	defer os.Remove(tmpFile2)

	result = processFailFast(tmpFile2)
	if result != nil {
		fmt.Printf("❌ Processing failed as expected: %v\n", result)
		fmt.Println("  ✅ Fail-fast successfully caught corrupted data!")
	}
}

func processFailFast(filename string) (err error) {
	// Use defer-recover to catch panics from Unsafe
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("critical error: %v", r)
		}
	}()

	// Read CSV with Safe version
	csvStream := ssql.ReadCSVSafe(filename)

	// Validate and parse with Safe filter
	parsed := ssql.SelectSafe(func(r ssql.Record) (ssql.Record, error) {
		// Note: CSV parsing auto-converts "1250.50" to float64(1250.50)
		balance, balanceOk := ssql.Get[float64](r, "balance")
		if !balanceOk {
			return ssql.MakeMutableRecord().Freeze(), fmt.Errorf("invalid balance in account %s",
				ssql.GetOr(r, "account", "unknown"))
		}

		return ssql.MakeMutableRecord().
			String("account", ssql.GetOr(r, "account", "")).
			Float("balance", balance).
			String("status", ssql.GetOr(r, "status", "")).
			Freeze(), nil
	})(csvStream)

	// Convert to Unsafe - will panic on any error
	unsafeStream := ssql.Unsafe(parsed)

	// Process all records - will panic on first error
	count := 0
	for record := range unsafeStream {
		account := ssql.GetOr(record, "account", "unknown")
		balance := ssql.GetOr(record, "balance", 0.0)
		fmt.Printf("  ✓ Processed %s: $%.2f\n", account, balance)
		count++
	}

	fmt.Printf("  📊 Total records processed: %d\n", count)
	return nil
}

func demonstrateBestEffort() {
	fmt.Println("Processing multiple data sources with best-effort approach...")

	// Create multiple CSV files with varying quality
	sources := map[string]string{
		"/tmp/ssql_source1.csv": `product,price,stock
Laptop,999.99,15
Phone,599.99,25
Tablet,399.99,10`,
		"/tmp/ssql_source2.csv": `product,price,stock
Monitor,299.99,8
Keyboard,invalid,20
Mouse,49.99,50`,
		"/tmp/ssql_source3.csv": `product,price,stock
Headphones,149.99,30
Speaker,bad_price,12
Webcam,89.99,18`,
	}

	// Create all CSV files
	for path, content := range sources {
		err := os.WriteFile(path, []byte(content), 0644)
		if err != nil {
			fmt.Printf("❌ Error writing %s: %v\n", path, err)
			continue
		}
		defer os.Remove(path)
	}

	// Process all sources with best-effort
	var allProducts []ssql.Record

	for path, _ := range sources {
		filename := strings.TrimPrefix(path, "/tmp/ssql_")
		fmt.Printf("\n📂 Processing %s...\n", filename)

		// Read CSV with Safe version
		csvStream := ssql.ReadCSVSafe(path)

		// Parse prices with Safe filter
		parsed := ssql.SelectSafe(func(r ssql.Record) (ssql.Record, error) {
			// Note: CSV parsing auto-converts "999.99" to float64(999.99) and "15" to int64(15)
			price, priceOk := ssql.Get[float64](r, "price")
			if !priceOk {
				return ssql.MakeMutableRecord().Freeze(), fmt.Errorf("invalid price for %s",
					ssql.GetOr(r, "product", "unknown"))
			}

			stock, stockOk := ssql.Get[int64](r, "stock")
			if !stockOk {
				return ssql.MakeMutableRecord().Freeze(), fmt.Errorf("invalid stock for %s",
					ssql.GetOr(r, "product", "unknown"))
			}

			return ssql.MakeMutableRecord().
				String("product", ssql.GetOr(r, "product", "")).
				Float("price", price).
				Int("stock", stock).
				String("source", filename).
				Freeze(), nil
		})(csvStream)

		// Use IgnoreErrors to collect valid records, skip invalid ones
		validRecords := ssql.IgnoreErrors(parsed)

		validCount := 0
		for record := range validRecords {
			allProducts = append(allProducts, record)
			product := ssql.GetOr(record, "product", "unknown")
			price := ssql.GetOr(record, "price", 0.0)
			stock := ssql.GetOr(record, "stock", int64(0))
			fmt.Printf("  ✓ %s: $%.2f (stock: %d)\n", product, price, stock)
			validCount++
		}
		fmt.Printf("  📊 Valid records from this source: %d\n", validCount)
	}

	// Display summary
	fmt.Println("\n📈 Final Inventory Summary:")
	fmt.Printf("  • Total valid products collected: %d\n", len(allProducts))

	// Calculate total inventory value
	totalValue := 0.0
	for _, record := range allProducts {
		price := ssql.GetOr(record, "price", 0.0)
		stock := ssql.GetOr(record, "stock", int64(0))
		totalValue += price * float64(stock)
	}
	fmt.Printf("  • Total inventory value: $%.2f\n", totalValue)
	fmt.Println("  ℹ️  Note: Invalid records were skipped, processing continued")
}

func printConversionSummary() {
	fmt.Println("🔄 Conversion Utility Reference:")
	fmt.Println()
	fmt.Println("1️⃣  Safe() - iter.Seq[T] → iter.Seq2[T, error]")
	fmt.Println("   • Converts simple iterator to error-aware")
	fmt.Println("   • Never produces errors (wraps values with nil error)")
	fmt.Println("   • Use before error-prone operations")
	fmt.Println()
	fmt.Println("2️⃣  Unsafe() - iter.Seq2[T, error] → iter.Seq[T]")
	fmt.Println("   • Converts error-aware to simple iterator")
	fmt.Println("   • PANICS on any error encountered")
	fmt.Println("   • Use for fail-fast, critical data processing")
	fmt.Println()
	fmt.Println("3️⃣  IgnoreErrors() - iter.Seq2[T, error] → iter.Seq[T]")
	fmt.Println("   • Converts error-aware to simple iterator")
	fmt.Println("   • Silently skips records with errors")
	fmt.Println("   • Use for best-effort, messy data, ETL pipelines")
	fmt.Println()
	fmt.Println("💡 Pattern Selection Guide:")
	fmt.Println("   • Use Safe() when entering error-aware processing")
	fmt.Println("   • Use Unsafe() when errors must stop processing")
	fmt.Println("   • Use IgnoreErrors() when partial data is acceptable")
}
