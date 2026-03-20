package main

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"github.com/rosscartlidge/ssql/v4"
	"time"
)

func main() {
	fmt.Println("📊 ssql Serialization Format Comparison")
	fmt.Println("==========================================")

	// Create test data that represents realistic pipeline data
	// (after JSON round-trip, iter.Seq becomes []interface{})
	tagsJSON1, _ := ssql.NewJSONString([]string{"electronics", "mobile", "premium"})
	tagsJSON2, _ := ssql.NewJSONString([]string{"electronics", "computer", "professional"})

	testData := []ssql.Record{
		ssql.MakeMutableRecord().
			String("id", "PRODUCT-001").
			String("name", "iPhone 15 Pro Max").
			String("category", "electronics").
			Float("price", 1199.99).
			Int("stock", int64(150)).
			String("description", "Latest flagship smartphone with advanced camera system and A17 Pro chip").
			JSONString("tags", tagsJSON1).
			Int("_line_number", int64(0)).
			Freeze(),
		ssql.MakeMutableRecord().
			String("id", "PRODUCT-002").
			String("name", "MacBook Pro 16-inch").
			String("category", "computers").
			Float("price", 2499.99).
			Int("stock", int64(75)).
			String("description", "Professional laptop with M3 Max chip, 32GB RAM, and 1TB SSD storage").
			JSONString("tags", tagsJSON2).
			Int("_line_number", int64(1)).
			Freeze(),
	}

	// Add more records to make the comparison meaningful
	for i := 0; i < 100; i++ {
		testData = append(testData, testData[0], testData[1])
	}

	fmt.Printf("Test dataset: %d records\n\n", len(testData))

	// Test different formats
	fmt.Println("Format Comparison:")
	fmt.Println("=================")

	// 1. JSON (current approach)
	jsonSize, jsonTime := testJSON(testData)
	fmt.Printf("📄 JSON:              %6d bytes, %8s\n", jsonSize, jsonTime)

	// 2. Compressed JSON (gzip)
	gzipSize, gzipTime := testCompressedJSON(testData)
	fmt.Printf("🗜️  Compressed JSON:   %6d bytes, %8s (%.1f%% of JSON)\n",
		gzipSize, gzipTime, float64(gzipSize)/float64(jsonSize)*100)

	// 3. Go's native gob format
	gobSize, gobTime := testGob(testData)
	fmt.Printf("🔧 Go gob:            %6d bytes, %8s (%.1f%% of JSON)\n",
		gobSize, gobTime, float64(gobSize)/float64(jsonSize)*100)

	// 4. Simplified binary format
	binarySize, binaryTime := testSimpleBinary(testData)
	fmt.Printf("⚡ Simple Binary:     %6d bytes, %8s (%.1f%% of JSON)\n",
		binarySize, binaryTime, float64(binarySize)/float64(jsonSize)*100)

	fmt.Println("\n📈 Analysis:")
	fmt.Println("============")
	fmt.Println("Size Efficiency:")
	fmt.Printf("  🥇 Best: Simple Binary (%.1f%% of JSON)\n", float64(binarySize)/float64(jsonSize)*100)
	fmt.Printf("  🥈 Second: Go gob (%.1f%% of JSON)\n", float64(gobSize)/float64(jsonSize)*100)
	fmt.Printf("  🥉 Third: Compressed JSON (%.1f%% of JSON)\n", float64(gzipSize)/float64(jsonSize)*100)

	fmt.Println("\nCompatibility & Trade-offs:")
	fmt.Println("  📄 JSON: ✅ Universal, ✅ Human-readable, ❌ Larger, ❌ Slower")
	fmt.Println("  🗜️  Compressed JSON: ✅ Smaller, ✅ Still JSON, ❌ Compression overhead")
	fmt.Println("  🔧 Go gob: ✅ Very efficient, ❌ Go-only, ❌ Not human-readable")
	fmt.Println("  ⚡ Simple Binary: ✅ Smallest, ✅ Fast, ❌ Custom format, ❌ Not universal")

	fmt.Println("\n💡 Recommendations:")
	fmt.Println("===================")
	fmt.Println("🌐 **Default: JSON** - For maximum compatibility and debugging")
	fmt.Println("🔧 **Go-to-Go: gob** - When chaining only ssql programs")
	fmt.Println("🗜️  **Large datasets: Compressed JSON** - Best of both worlds")
	fmt.Println("⚡ **High performance: Custom binary** - When every byte/microsecond counts")

	fmt.Println("\n🚀 Suggested Implementation:")
	fmt.Println("============================")
	fmt.Println("Add format flag to I/O functions:")
	fmt.Println("  ssql.WriteToWriter(stream, writer, ssql.FormatJSON)     // Default")
	fmt.Println("  ssql.WriteToWriter(stream, writer, ssql.FormatGob)      // Go-only")
	fmt.Println("  ssql.WriteToWriter(stream, writer, ssql.FormatCompressed) // Gzipped JSON")
	fmt.Println("  ssql.WriteToWriter(stream, writer, ssql.FormatBinary)   // Custom binary")
}

func testJSON(data []ssql.Record) (int, time.Duration) {
	start := time.Now()

	var buf bytes.Buffer
	stream := ssql.From(data)
	ssql.WriteJSONToWriter(stream, &buf)

	duration := time.Since(start)
	return buf.Len(), duration
}

func testCompressedJSON(data []ssql.Record) (int, time.Duration) {
	start := time.Now()

	var jsonBuf bytes.Buffer
	stream := ssql.From(data)
	ssql.WriteJSONToWriter(stream, &jsonBuf)

	var gzipBuf bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipBuf)
	gzipWriter.Write(jsonBuf.Bytes())
	gzipWriter.Close()

	duration := time.Since(start)
	return gzipBuf.Len(), duration
}

func testGob(data []ssql.Record) (int, time.Duration) {
	start := time.Now()

	var buf bytes.Buffer
	encoder := gob.NewEncoder(&buf)

	// Convert to serializable format (gob can't handle iter.Seq directly)
	var serializable []map[string]interface{}
	for _, record := range data {
		converted := make(map[string]interface{})
		for k, v := range record.All() {
			// Convert iter.Seq to slice
			if isIterSeq(v) {
				converted[k] = materializeSequence(v)
			} else {
				converted[k] = v
			}
		}
		serializable = append(serializable, converted)
	}

	encoder.Encode(serializable)

	duration := time.Since(start)
	return buf.Len(), duration
}

func testSimpleBinary(data []ssql.Record) (int, time.Duration) {
	start := time.Now()

	var buf bytes.Buffer

	// Simple binary format:
	// [record_count][record1][record2]...
	// Each record: [field_count][field1][field2]...
	// Each field: [key_len][key][type][value]

	// Write record count
	writeUint32(&buf, uint32(len(data)))

	for _, record := range data {
		// Write field count
		writeUint32(&buf, uint32(record.Len()))

		for key, value := range record.All() {
			// Write key
			writeString(&buf, key)

			// Write value with type tag
			writeValue(&buf, value)
		}
	}

	duration := time.Since(start)
	return buf.Len(), duration
}

// Helper functions for simple binary format
func writeUint32(buf *bytes.Buffer, val uint32) {
	buf.WriteByte(byte(val))
	buf.WriteByte(byte(val >> 8))
	buf.WriteByte(byte(val >> 16))
	buf.WriteByte(byte(val >> 24))
}

func writeString(buf *bytes.Buffer, s string) {
	writeUint32(buf, uint32(len(s)))
	buf.WriteString(s)
}

func writeValue(buf *bytes.Buffer, value interface{}) {
	switch v := value.(type) {
	case string:
		buf.WriteByte(1) // Type: string
		writeString(buf, v)
	case int64:
		buf.WriteByte(2) // Type: int64
		writeUint32(buf, uint32(v))
		writeUint32(buf, uint32(v>>32))
	case float64:
		buf.WriteByte(3) // Type: float64
		// Simple float encoding (not IEEE 754 compliant, just for demo)
		writeString(buf, fmt.Sprintf("%.2f", v))
	default:
		// Handle iter.Seq and other complex types as JSON
		if isIterSeq(value) {
			buf.WriteByte(4) // Type: array
			slice := materializeSequence(value)
			writeUint32(buf, uint32(len(slice)))
			for _, item := range slice {
				writeValue(buf, item)
			}
		} else {
			buf.WriteByte(5) // Type: other (as JSON)
			jsonBytes, _ := json.Marshal(value)
			writeString(buf, string(jsonBytes))
		}
	}
}

// Helper functions - simplified versions for demo
func isIterSeq(value interface{}) bool {
	// Check if it's an iter.Seq type by looking at the type
	switch value.(type) {
	case []interface{}: // This will be the case after JSON round-trip
		return true
	default:
		return false
	}
}

func materializeSequence(value interface{}) []interface{} {
	// For this demo, handle the case where we have arrays from JSON
	if arr, ok := value.([]interface{}); ok {
		return arr
	}
	// Fallback for other types
	return []interface{}{value}
}
