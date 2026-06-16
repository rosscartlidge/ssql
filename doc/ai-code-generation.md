# ssql AI Code Generation - Hybrid Prompt

*Complete reference combining anti-patterns with comprehensive examples for optimal code generation*

---

## System Prompt

```
You are an expert Go developer specializing in ssql, a modern Go stream processing library built on Go 1.23+ iterators. Generate high-quality, idiomatic ssql code from natural language descriptions.

## 🎯 PRIMARY GOAL: Human-Readable, Verifiable Code

Write code that humans can quickly read and verify - no clever tricks. Always prioritize clarity over cleverness.
```

---

## Core API Reference

For complete, always-current API documentation, use:
```bash
go doc github.com/rosscartlidge/ssql/v4
go doc github.com/rosscartlidge/ssql/v4.FunctionName
```

The godoc is generated directly from the source code and is always in sync with the actual implementation. When in doubt, consult `go doc`.

---

## Quick Reference

### Imports - CRITICAL RULE

**ONLY import packages that are actually used in your code.**

```go
import (
    "fmt"                                    // When using fmt.Printf, fmt.Println
    "log"                                    // When using log.Fatal, log.Printf
    "github.com/rosscartlidge/ssql/v4"     // ✅ CORRECT import path!
)

// Additional imports - ONLY when actually used:
// "slices"     - ONLY if using slices.Values()
// "time"       - ONLY if using time.Duration, time.Time
// "strings"    - ONLY if using strings.Fields, strings.Contains, etc.
// "os"         - ONLY if using os.WriteFile, os.ReadFile
```

**DO NOT import packages that aren't referenced in the code.** Go will refuse to compile with `imported and not used: "os"`. In particular:
- If you use `fmt.Printf` / `fmt.Println` for stdout output, you do NOT need to import `"os"`.
- Only import `"os"` when you actually reference `os.Stdout`, `os.Args`, `os.ReadFile`, etc.

**⚠️ Common Import Mistakes:**
- ❌ `github.com/rocketlaunchr/ssql` - Wrong! Different project
- ❌ `github.com/ssql/v3` - Wrong! Doesn't exist
- ✅ `github.com/rosscartlidge/ssql/v4` - Correct!

### Core Types & Creation

- `iter.Seq[T]` / `iter.Seq2[T, error]` - Go 1.23+ lazy iterators
- `Record` - Encapsulated struct (NOT a map) - use `GetOr()` to read, `MakeMutableRecord()` to create
- `ssql.MakeMutableRecord().String("key", "val").Int("num", int64(42)).Freeze()` - Build records

### Reading Data (Always Check Errors!)

```go
// CSV reading - ALWAYS handle errors
data, err := ssql.ReadCSV("file.csv")
if err != nil {
    log.Fatalf("Failed to read CSV: %v", err)
}

// JSON reading
data, err := ssql.ReadJSON("file.jsonl")
if err != nil {
    log.Fatalf("Failed to read JSON: %v", err)
}

// Arrow reading (10-20x faster than CSV for large datasets)
data, err := ssql.ReadArrow("file.arrow")
if err != nil {
    log.Fatalf("Failed to read Arrow: %v", err)
}

// Excel (XLSX) reading
data, err := ssql.ReadXLSX("file.xlsx")
if err != nil {
    log.Fatalf("Failed to read XLSX: %v", err)
}

// XLSX with specific sheet
data, err := ssql.ReadXLSX("file.xlsx", ssql.XLSXConfig{SheetName: "Sales"})

// List sheet names
sheets, err := ssql.ReadXLSXSheetNames("file.xlsx")

// WAV audio reading (returns records with amplitude field)
data, err := ssql.ExtractSignalFromWAV("audio.wav")
if err != nil {
    log.Fatalf("Failed to read WAV: %v", err)
}
```

### Writing Data

```go
// Write Arrow (10-20x faster than CSV)
err = ssql.WriteArrow(records, "output.arrow")

// Write XLSX
err = ssql.WriteXLSX(records, "output.xlsx")
err = ssql.WriteXLSX(records, "output.xlsx", ssql.XLSXConfig{SheetName: "Results"})

// Write WAV audio
metadata := ssql.WAVMetadata{SampleRate: 44100, Channels: 1, BitsPerSample: 16}
err = ssql.WriteWAV(signal, "output.wav", metadata)
```

**⚠️ CSV Auto-Parsing**: Numeric strings become `int64`/`float64`, not strings!
- CSV value `"25"` → `int64(25)`
- Use `GetOr(r, "age", int64(0))` not `GetOr(r, "age", "")`

### Core Operations (SQL-style naming)

**⚠️ CRITICAL: ssql uses SQL-style naming, NOT LINQ/functional programming names!**

- **Transform**: `Select(func(T) U)`, `SelectMany(func(T) iter.Seq[U])` ← NOT Map or FlatMap!
- **Update Records**: `Update(func(MutableRecord) MutableRecord)` - Convenience wrapper for field updates, eliminates ToMutable/Freeze boilerplate
- **Filter**: `Where(func(T) bool)` ← NOT Filter (Filter is the type name)
- **Limit**: `Limit(n)`, `Offset(n)` ← NOT Take/Skip
- **Sort**: `Sort()`, `SortBy(func(T) K)`, `SortDesc()`, `Reverse()`
  - **Descending order**: Use negative values in SortBy: `return -value`
  - **Ascending order**: Use positive values: `return value`
- **Group**: `GroupByFields("groupName", "field1", "field2", ...)`
- **Aggregate**: `Aggregate("groupName", map[string]AggregateFunc{...})`
- **Join**: `InnerJoin(rightSeq, predicate)`, `LeftJoin()`, `RightJoin()`, `FullJoin()`
- **Window (analytic)**: `Window([]WindowConfig{...})` — rankings, lag/lead, running aggregates without collapsing rows
- **Window (batching)**: `CountWindow[T](size)`, `TimeWindow[T](duration, "timeField")`

**Common Naming Mistakes:**
- ❌ `FlatMap` → ✅ `SelectMany`
- ❌ `Map` → ✅ `Select`
- ❌ `Filter(predicate)` → ✅ `Where(predicate)`
- ❌ `Take(n)` → ✅ `Limit(n)`

### Record Creation and Access (v1.0+)

**🚨 CRITICAL: Record fields are NOT directly accessible!**

Record is an **encapsulated struct**, not a map. You MUST use the provided API:

**❌ WRONG - Direct field access (will not compile):**
```go
// These will NOT work - Record is not map[string]any
record["name"] = "Alice"              // ❌ Compile error!
value := record["age"]                // ❌ Compile error!
for k, v := range record {            // ❌ Compile error!
```

**✅ CORRECT - Use the builder pattern and accessor functions:**

```go
// Creating Records - Use MutableRecord builder
record := ssql.MakeMutableRecord().
    String("name", "Alice").
    Int("age", int64(30)).
    Float("score", 95.5).
    Freeze()  // Returns immutable Record

// Reading fields - Use Get/GetOr
name := ssql.GetOr(record, "name", "")           // With default
age, exists := ssql.Get[int64](record, "age")    // With existence check

// Modifying (creates new record) - Use SetImmutable
updated := ssql.SetImmutable(record, "score", 98.0)

// Iterating - Use .All() method
for key, value := range record.All() {
    fmt.Printf("%s: %v\n", key, value)
}

// WARNING: record.All() returns iter.Seq2, not a map!
// WRONG: json.Marshal(record.All())  // Won't work - iterators can't be JSON encoded
// RIGHT: ssql.WriteJSONToWriter(records, os.Stdout)  // Use ssql's JSON writer

// Getting keys - Use .KeysIter()
for key := range record.KeysIter() {
    fmt.Println(key)
}

// Length - Use .Len() method
count := record.Len()
```

**This applies to ALL code outside the ssql package** - including:
- ✅ User code
- ✅ LLM-generated code
- ✅ Example programs
- ✅ Test files that import ssql

### Aggregation Functions

```go
ssql.Count()                    // ⚠️ NO PARAMETERS! Field name goes in map key
ssql.Sum("field")               // Takes field parameter
ssql.Avg("field")
ssql.Min[T]("field")
ssql.Max[T]("field")
ssql.First("field")
ssql.Last("field")
ssql.Collect("field")
```

**Important**: After `Aggregate()`, grouping fields retain their original names.

### Signal Processing

```go
// FFT analysis
spectrum, err := ssql.FFT(signal)                    // Magnitude only
spectrumWithPhase, err := ssql.FFTWithPhase(signal)  // Magnitude + phase

// Convert spectrum to records (for output/charting)
specRecords := ssql.SpectrumToRecords(spectrum, sampleRate)
// Each record: {index, frequency, magnitude, [phase]}

// Inverse FFT (signal reconstruction)
reconstructed, err := ssql.IFFT(magnitude, phase)

// Convolution with built-in kernels
smoothed, err := ssql.ConvolveSame(signal, ssql.GaussianKernel(11, 2.0))
edges, err := ssql.Convolve(signal, ssql.DiffKernel())

// Cross-correlation
corr, err := ssql.Correlate(signal1, signal2)

// Built-in kernels: MovingAverageKernel, GaussianKernel, DiffKernel, LaplacianKernel, SobelKernel
```

### Signal Processing - Record Integration

```go
// Extract signal from records
signal := ssql.ExtractSignal(records, "voltage")     // From iter.Seq[Record]
signal := ssql.ExtractSignalFromSlice(recSlice, "v") // From []Record

// Add signal back to records (appends new field to each record)
withSmoothed := ssql.WithSignal(records, "smoothed", smoothedSignal)
// Output using ssql.WriteJSONToWriter(withSmoothed, os.Stdout)

// Output a signal slice directly as JSON (without records)
import "encoding/json"
json.NewEncoder(os.Stdout).Encode(smoothedSignal) // Signal is []float64, JSON-encodable
```

**CRITICAL — `ssql.Signal` is `[]float64`, NOT `iter.Seq[Record]`:**
Functions like `ConvolveSame`, `GaussianKernel`, `Convolve`, `FFTMagnitude`, `IFFT`, `HannWindow`, `ApplyWindow` return a `Signal` (i.e. `[]float64`). You CANNOT pass a Signal to `ssql.WriteJSONToWriter` — that function takes records. To emit a Signal as JSON, use `encoding/json` directly:

```go
// WRONG — Signal is not iter.Seq[Record], will not compile
err := ssql.WriteJSONToWriter(smoothed, os.Stdout)  // ❌

// CORRECT — Signal is []float64, use encoding/json
import "encoding/json"
err := json.NewEncoder(os.Stdout).Encode(smoothed)  // ✓

// ALSO CORRECT — attach Signal back to records, then WriteJSONToWriter
withSmoothed := ssql.WithSignal(records, "smoothed", smoothed)
err := ssql.WriteJSONToWriter(withSmoothed, os.Stdout)  // ✓
```

**NOTE:** There is NO `ssql.Range()` function. To iterate with index, use standard Go:
```go
for i, v := range signal {
    record := ssql.MakeMutableRecord().Int("i", int64(i)).Float("v", v).Freeze()
    // ...
}
```

### Spectrogram (STFT)

```go
// Compute spectrogram with options
opts := ssql.SpectrogramOptions{
    WindowSize: 1024,
    HopSize:    512,
    Window:     "hann",     // "hann", "hamming", "blackman"
    SampleRate: 44100.0,
}
bins, err := ssql.Spectrogram(signal, opts)

// Convert to records for output
spectrogramRecords := ssql.SpectrogramToRecords(bins)
// Each record: {time_index, time, frequency, magnitude}

// Window functions (for custom use)
window := ssql.HannWindow(1024)
window := ssql.HammingWindow(1024)
window := ssql.BlackmanWindow(1024)
windowed := ssql.ApplyWindow(signal, window)
```

### JSON I/O

```go
// Read JSON/JSONL
data, err := ssql.ReadJSON("data.jsonl")
data, err := ssql.ReadJSONFast("data.jsonl")  // 2-5x faster, schema caching

// Write JSON/JSONL to file
err = ssql.WriteJSON(records, "output.jsonl")
err = ssql.WriteJSONFast(records, "output.jsonl")  // 2-5x faster

// Write JSON to stdout (IMPORTANT: use WriteJSONToWriter, not WriteJSON!)
import "os"
err = ssql.WriteJSONToWriter(records, os.Stdout)

// WRONG - WriteJSON takes a filename, not os.Stdout!
// err = ssql.WriteJSON(records, os.Stdout)  // WON'T COMPILE
```

**CRITICAL:** When writing to stdout, use `ssql.WriteJSONToWriter(records, os.Stdout)`. The function `ssql.WriteJSON()` takes a filename string, not an io.Writer.

### Distinct and Union

```go
// Remove duplicates by key function
unique := ssql.DistinctBy(func(r ssql.Record) string {
    return ssql.GetOr(r, "email", "")
})(records)

// Remove duplicates by all fields (uses SHA256 hash)
unique := ssql.DistinctBy(ssql.RecordKey)(records)

// Combine multiple sequences (SQL UNION ALL)
combined := ssql.Concat(seq1, seq2, seq3)

// UNION (distinct) = Concat + DistinctBy
allRecords := ssql.DistinctBy(ssql.RecordKey)(ssql.Concat(seq1, seq2))
```

### Join Operations

```go
// Inner join on same field name
joined := ssql.InnerJoin(rightSeq, ssql.OnFields("user_id"))(leftSeq)

// Inner join on different field names
joined := ssql.InnerJoin(rightSeq, ssql.OnFieldPair("dept_id", "id"))(leftSeq)

// Left join (keep all left records)
joined := ssql.LeftJoin(rightSeq, ssql.OnFields("id"))(leftSeq)

// Custom join condition
joined := ssql.InnerJoin(rightSeq, ssql.OnCondition(func(l, r ssql.Record) bool {
    return ssql.GetOr(l, "dept", "") == ssql.GetOr(r, "department", "")
}))(leftSeq)

// Lookup join (multi-clause enrichment from same lookup table)
enriched := ssql.LookupJoin(lookupSeq, []ssql.LookupClause{
    ssql.Lookup("source_type", "type", "description", "source_desc"),
    ssql.Lookup("dest_type", "type", "description", "dest_desc"),
})(records)
// Lookup(leftField, rightField, renameOld, renameNew)
```

---

## ⛔ CRITICAL ANTI-PATTERNS

**LLMs often hallucinate these WRONG APIs - DO NOT USE:**

### ❌ Wrong: Combined GroupBy + Aggregate API (doesn't exist!)
```go
// This API does NOT exist in ssql!
result := ssql.GroupByFields(
    []string{"department"},           // ❌ Wrong!
    []ssql.Aggregation{          // ❌ Wrong!
        ssql.Count("count"),     // ❌ Wrong!
    },
)
```

### ✅ Correct: Separate GroupBy and Aggregate
```go
// Step 1: Group by fields (namespace + field names)
grouped := ssql.GroupByFields("analysis", "department")(data)

// Step 2: Aggregate with map (SAME namespace!)
results := ssql.Aggregate("analysis", map[string]ssql.AggregateFunc{
    "employee_count": ssql.Count(),  // ✅ Field name is map KEY
})(grouped)
```

**CRITICAL:** Namespace ("analysis") MUST match between GroupByFields and Aggregate!

### ❌ Wrong: Count() with parameter
```go
"count": ssql.Count("employee_count")  // ❌ Won't compile!
```

### ✅ Correct: Count() is parameterless
```go
"employee_count": ssql.Count()  // ✅ Field name is the map key
```

### ❌ Wrong: WriteJSON to stdout
```go
// These WON'T COMPILE - WriteJSON takes filename string, not io.Writer!
ssql.WriteJSON(records, os.Stdout)     // ❌ Wrong!
ssql.WriteJSON(records, "/dev/stdout") // ❌ Wrong! (even if it runs, not portable)
```

### ✅ Correct: WriteJSONToWriter for stdout
```go
import "os"
ssql.WriteJSONToWriter(records, os.Stdout)  // ✅ Correct!
```

### ❌ Wrong: Manual JSON encoding with record.All()
```go
// record.All() returns iter.Seq2, not a map - can't be JSON encoded!
for record := range records {
    json.Marshal(record.All())           // ❌ Won't work!
    json.NewEncoder(w).Encode(record.All())  // ❌ Won't work!
}
```

### ✅ Correct: Use ssql's JSON writer
```go
ssql.WriteJSONToWriter(records, os.Stdout)  // ✅ Handles iteration correctly
```

### ❌ Wrong: Different namespaces
```go
grouped := ssql.GroupByFields("sales", "region")(data)
results := ssql.Aggregate("analysis", aggs)(grouped)  // ❌ Won't work!
```

### ✅ Correct: Matching namespaces
```go
grouped := ssql.GroupByFields("sales", "region")(data)
results := ssql.Aggregate("sales", aggs)(grouped)  // ✅ Same namespace
```

### ❌ Wrong: Bare Join() function
```go
joined := ssql.Join(left, right, "id")  // ❌ Doesn't exist!
```

### ✅ Correct: Use typed join functions
```go
joined := ssql.InnerJoin(rightSeq, ssql.OnFields("id"))(leftSeq)  // ✅
joined := ssql.LeftJoin(rightSeq, ssql.OnFieldPair("dept_id", "id"))(leftSeq)  // ✅
```

### ❌ Wrong: Old import path
```go
import "github.com/rosscartlidge/ssql"      // ❌ Missing /v4!
import "github.com/rosscartlidge/ssql/v3"    // ❌ Old version!
```

### ✅ Correct: v4 import path
```go
import "github.com/rosscartlidge/ssql/v4"    // ✅ Current version
```

### ❌ Wrong: Using SetAny (removed in v2)
```go
mut.SetAny("field", value)  // ❌ Removed!
```

### ✅ Correct: Use typed setters
```go
mut.String("name", "Alice")    // ✅
mut.Int("count", int64(42))    // ✅
mut.Float("price", 99.99)     // ✅
mut.Bool("active", true)      // ✅
```

---

## Composition Style - CRITICAL RULE

### 🎯 ALWAYS Use Chain() for 2+ Operations

**RULE: When you have 2 or more operations on the same type (e.g., `Record → Record`), you MUST use `Chain()`.**

```go
// ✅ CORRECT - Always use Chain() for multi-step pipelines
result := ssql.Chain(
    ssql.Where(func(r ssql.Record) bool {
        return ssql.GetOr(r, "amount", 0.0) > 1000
    }),
    ssql.GroupByFields("analysis", "region"),
    ssql.Aggregate("analysis", map[string]ssql.AggregateFunc{
        "total": ssql.Sum("amount"),
        "count": ssql.Count(),
    }),
    ssql.SortBy(func(r ssql.Record) float64 {
        return -ssql.GetOr(r, "total", 0.0) // Negative = descending
    }),
    ssql.Limit[ssql.Record](10),
)(data)
```

```go
// ✅ CORRECT - Chain() even for just GroupBy + Aggregate
result := ssql.Chain(
    ssql.GroupByFields("sales", "product"),
    ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
        "total": ssql.Sum("amount"),
    }),
)(data)
```

```go
// ❌ WRONG - Don't use sequential steps for multi-step pipelines
grouped := ssql.GroupByFields("analysis", "field")(data)
result := ssql.Aggregate("analysis", aggregations)(grouped)  // ❌ Should use Chain()!
```

### ✅ ONLY Use Sequential Steps For:

**Single operations only:**

```go
// ✅ OK - Single operation, Chain() not needed
enriched := ssql.Select(func(r ssql.Record) ssql.Record {
    price := ssql.GetOr(r, "price", 0.0)
    tier := "Budget"
    if price > 100 {
        tier = "Premium"
    }
    return ssql.SetImmutable(r, "tier", tier)
})(data)
```

**When types change between steps (use Pipe instead):**

```go
// ✅ OK - Type changes, use Pipe() or sequential
stringSeq := ssql.Select(func(i int) string {
    return fmt.Sprintf("Value: %d", i)
})(intSeq)

boolSeq := ssql.Where(func(s string) bool {
    return len(s) > 10
})(stringSeq)
```

### Chain() Checklist

Before writing code, ask:
- ❓ Do I have 2+ operations?
- ❓ Do they all work on the same type (e.g., all `Record → Record`)?
- ✅ **YES to both** → **USE Chain()**
- ❌ NO → Single operation or use Pipe()

---

## Complete Examples Library

### Example 1: Basic Filtering and Aggregation

**Natural Language**: "Read employee data from employees.csv, filter for employees with salary over $80,000, and count how many employees there are by department"

**ssql Code**:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read employee data - always handle errors
    employees, err := ssql.ReadCSV("employees.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    // Use Chain() for clean multi-step pipeline
    result := ssql.Chain(
        // Filter for high salary employees
        ssql.Where(func(r ssql.Record) bool {
            // CSV auto-parses "80000" → int64(80000)
            salary := ssql.GetOr(r, "salary", 0.0)
            return salary > 80000
        }),
        // Group by department
        ssql.GroupByFields("dept_analysis", "department"),
        // Count employees per department
        ssql.Aggregate("dept_analysis", map[string]ssql.AggregateFunc{
            "employee_count": ssql.Count(),
        }),
    )(employees)

    // Display results
    fmt.Println("High-salary employees by department:")
    for record := range result {
        dept := ssql.GetOr(record, "department", "")
        count := ssql.GetOr(record, "employee_count", int64(0))
        fmt.Printf("%s: %d employees\n", dept, count)
    }
}
```

### Example 2: Top N Analysis

**Natural Language**: "Find the top 5 products by revenue from sales data, showing product name and total revenue"

**ssql Code**:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read sales data
    sales, err := ssql.ReadCSV("sales.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    // Use Chain() for the full pipeline: group → aggregate → sort → limit
    top5 := ssql.Chain(
        // Group by product name
        ssql.GroupByFields("product_analysis", "product_name"),
        // Calculate total revenue per product
        ssql.Aggregate("product_analysis", map[string]ssql.AggregateFunc{
            "total_revenue": ssql.Sum("revenue"),
        }),
        // Sort by revenue (descending)
        ssql.SortBy(func(r ssql.Record) float64 {
            return -ssql.GetOr(r, "total_revenue", 0.0) // Negative for descending
        }),
        // Take top 5
        ssql.Limit[ssql.Record](5),
    )(sales)

    fmt.Println("Top 5 products by revenue:")
    rank := 1
    for product := range top5 {
        name := ssql.GetOr(product, "product_name", "")
        revenue := ssql.GetOr(product, "total_revenue", 0.0)
        fmt.Printf("%d. %s: $%.2f\n", rank, name, revenue)
        rank++
    }
}
```

### Example 3: Data Enrichment with Transformation

**Natural Language**: "Read customer data, add a customer_tier field based on total purchases (Bronze < $1000, Silver $1000-$5000, Gold > $5000)"

**ssql Code**:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read customer data
    customers, err := ssql.ReadCSV("customers.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    // Add customer tier based on total purchases - using Update helper
    enrichedCustomers := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
        frozen := mut.Freeze()
        totalPurchases := ssql.GetOr(frozen, "total_purchases", 0.0)

        var tier string
        switch {
        case totalPurchases > 5000:
            tier = "Gold"
        case totalPurchases >= 1000:
            tier = "Silver"
        default:
            tier = "Bronze"
        }

        return mut.String("customer_tier", tier)
    })(customers)

    // Group by tier and calculate statistics
    result := ssql.Chain(
        ssql.GroupByFields("tier_analysis", "customer_tier"),
        ssql.Aggregate("tier_analysis", map[string]ssql.AggregateFunc{
            "customer_count": ssql.Count(),
            "avg_purchases":  ssql.Avg("total_purchases"),
        }),
    )(enrichedCustomers)

    fmt.Println("Customer tier distribution:")
    for record := range result {
        tier := ssql.GetOr(record, "customer_tier", "")
        count := ssql.GetOr(record, "customer_count", int64(0))
        avgPurchases := ssql.GetOr(record, "avg_purchases", 0.0)
        fmt.Printf("%s: %d customers (avg: $%.2f)\n", tier, count, avgPurchases)
    }
}
```

### Example 3b: Updating Record Fields (Computed Values)

**Natural Language**: "Read order data and add a total field calculated from price * quantity, then add a tax field (8% of total)"

**ssql Code**:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read order data
    orders, err := ssql.ReadCSV("orders.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    // Add computed total field
    withTotal := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
        frozen := mut.Freeze()  // Freeze to read values
        price := ssql.GetOr(frozen, "price", 0.0)
        qty := ssql.GetOr(frozen, "quantity", int64(0))
        return mut.Float("total", price * float64(qty))
    })(orders)

    // Add tax field (8% of total) - can chain Updates
    withTax := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
        frozen := mut.Freeze()
        total := ssql.GetOr(frozen, "total", 0.0)
        return mut.Float("tax", total * 0.08)
    })(withTotal)

    // Add grand total
    final := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
        frozen := mut.Freeze()
        total := ssql.GetOr(frozen, "total", 0.0)
        tax := ssql.GetOr(frozen, "tax", 0.0)
        return mut.Float("grand_total", total + tax)
    })(withTax)

    fmt.Println("Orders with computed fields:")
    for record := range final {
        product := ssql.GetOr(record, "product", "")
        total := ssql.GetOr(record, "total", 0.0)
        tax := ssql.GetOr(record, "tax", 0.0)
        grandTotal := ssql.GetOr(record, "grand_total", 0.0)
        fmt.Printf("%s: subtotal=$%.2f, tax=$%.2f, total=$%.2f\n",
            product, total, tax, grandTotal)
    }
}
```

**Why Update instead of Select?**
- ✅ More concise - no `ToMutable()` and `Freeze()` boilerplate
- ✅ Clear intent - "I'm updating fields" vs "I'm transforming"
- ✅ Type-safe - uses typed methods (`.String()`, `.Float()`, `.Int()`)

**Equivalent using Select (more verbose):**
```go
withTotal := ssql.Select(func(r ssql.Record) ssql.Record {
    price := ssql.GetOr(r, "price", 0.0)
    qty := ssql.GetOr(r, "quantity", int64(0))
    return r.ToMutable().Float("total", price * float64(qty)).Freeze()
})(orders)
```

### Example 4: Join Analysis

**Natural Language**: "Join customer data with order data to find customers who have placed orders totaling more than $1000"

**ssql Code**:
```go
package main

import (
    "fmt"
    "slices"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Create sample customer data using MutableRecord builder
    customers := []ssql.Record{
        ssql.MakeMutableRecord().String("customer_id", "C001").String("name", "Alice Johnson").Freeze(),
        ssql.MakeMutableRecord().String("customer_id", "C002").String("name", "Bob Smith").Freeze(),
        ssql.MakeMutableRecord().String("customer_id", "C003").String("name", "Carol Davis").Freeze(),
    }

    // Create sample order data
    orders := []ssql.Record{
        ssql.MakeMutableRecord().String("customer_id", "C001").Float("amount", 500.0).Freeze(),
        ssql.MakeMutableRecord().String("customer_id", "C001").Float("amount", 800.0).Freeze(),
        ssql.MakeMutableRecord().String("customer_id", "C002").Float("amount", 200.0).Freeze(),
        ssql.MakeMutableRecord().String("customer_id", "C003").Float("amount", 1200.0).Freeze(),
    }

    // Join, group, and aggregate using Chain()
    highValueCustomers := ssql.Chain(
        // Join customers with their orders
        ssql.InnerJoin(slices.Values(orders), ssql.OnFields("customer_id")),
        // Group by customer
        ssql.GroupByFields("customer_spending", "customer_id", "name"),
        // Calculate total spending
        ssql.Aggregate("customer_spending", map[string]ssql.AggregateFunc{
            "total_spent": ssql.Sum("amount"),
            "order_count": ssql.Count(),
        }),
        // Filter for customers with > $1000
        ssql.Where(func(r ssql.Record) bool {
            total := ssql.GetOr(r, "total_spent", 0.0)
            return total > 1000
        }),
    )(slices.Values(customers))

    fmt.Println("High-value customers (>$1000 total orders):")
    for customer := range highValueCustomers {
        name := ssql.GetOr(customer, "name", "")
        total := ssql.GetOr(customer, "total_spent", 0.0)
        orders := ssql.GetOr(customer, "order_count", int64(0))
        fmt.Printf("%s: $%.2f across %d orders\n", name, total, orders)
    }
}
```

### Example 5: Chart Creation

**Natural Language**: "Create an interactive chart showing monthly sales trends"

**ssql Code**:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read sales data
    sales, err := ssql.ReadCSV("monthly_sales.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    // Group by month and calculate metrics
    monthlyTrends := ssql.Chain(
        ssql.GroupByFields("monthly_analysis", "month"),
        ssql.Aggregate("monthly_analysis", map[string]ssql.AggregateFunc{
            "total_sales": ssql.Sum("sales_amount"),
            "order_count": ssql.Count(),
        }),
    )(sales)

    // Create interactive chart
    err = ssql.QuickChart(monthlyTrends, "month", "total_sales", "monthly_sales.html")
    if err != nil {
        log.Fatalf("Failed to create chart: %v", err)
    }

    fmt.Println("Chart created: monthly_sales.html")
}
```

### Example 6: Signal Processing Pipeline

**Natural Language**: "Read a sensor signal from sensor_data.csv (field 'voltage'), compute the FFT at 1000 Hz sample rate, and create a chart of the frequency spectrum"

**ssql Code**:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read sensor data
    data, err := ssql.ReadCSV("sensor_data.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    // Extract signal from records
    signal := ssql.ExtractSignal(data, "voltage")

    // Compute FFT
    spectrum, err := ssql.FFT(signal)
    if err != nil {
        log.Fatalf("Failed to compute FFT: %v", err)
    }

    // Convert spectrum to records with frequency labels
    spectrumRecords := ssql.SpectrumToRecords(spectrum, 1000.0)

    // Create interactive frequency chart
    err = ssql.QuickChart(spectrumRecords, "frequency", "magnitude", "spectrum.html")
    if err != nil {
        log.Fatalf("Failed to create chart: %v", err)
    }

    fmt.Println("Spectrum chart created: spectrum.html")
}
```

### Visualization Functions

Beyond `QuickChart` and `InteractiveChart`, ssql provides specialized visualization:

```go
// Enhanced chart with multi-series, heatmap, log axes, color-by-field
config := ssql.DefaultChartConfig()
config.Title = "Revenue vs Expenses"
config.ChartType = "line"   // line, bar, scatter, pie, radar, heatmap
config.YFields = []string{"revenue", "expenses"}  // Multi-series
config.LogX = true          // Logarithmic X axis
config.LogY = true          // Logarithmic Y axis
config.ColorField = "region"  // Color scatter points by field
err := ssql.EnhancedChart(records, "chart.html", config)

// Heatmap/spectrogram visualization (Plotly.js) - uses HeatmapConfig
hConfig := ssql.DefaultHeatmapConfig()
hConfig.ColorScale = "viridis"  // viridis, plasma, inferno, etc.
hConfig.LogFreq = true          // Logarithmic frequency axis
err := ssql.HeatmapChart(records, "xField", "yField", "zField", "heatmap.html", hConfig)

// Interactive data explorer (AG-Grid + Plotly.js) - uses ExploreConfig
eConfig := ssql.DefaultExploreConfig()
eConfig.Title = "Sales Explorer"
eConfig.Theme = "dark"            // "light" or "dark"
eConfig.InitialXField = "date"
eConfig.InitialYField = "revenue"
err := ssql.DataExplore(records, "explorer.html", eConfig)

// Animated visualization (video-player controls) - uses AnimateConfig
aConfig := ssql.DefaultAnimateConfig()
aConfig.ChartType = "heatmap"     // "heatmap" or "histogram"
aConfig.FPS = 10
aConfig.Loop = true
aConfig.ColorScale = "plasma"
err := ssql.AnimateChart(records, "animation.html", aConfig)
```

### Example 7: Distinct and Union

**Natural Language**: "Read data from file_a.csv and file_b.csv, combine them, and remove duplicates"

**ssql Code**:
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // Read both files
    fileA, err := ssql.ReadCSV("file_a.csv")
    if err != nil {
        log.Fatalf("Failed to read file_a.csv: %v", err)
    }

    fileB, err := ssql.ReadCSV("file_b.csv")
    if err != nil {
        log.Fatalf("Failed to read file_b.csv: %v", err)
    }

    // Combine and deduplicate (SQL UNION)
    combined := ssql.DistinctBy(ssql.RecordKey)(ssql.Concat(fileA, fileB))

    for record := range combined {
        for k, v := range record.All() {
            fmt.Printf("%s=%v ", k, v)
        }
        fmt.Println()
    }
}
```

---

## Code Generation Rules

> When generating code from a shell pipeline (`ssql ... | ssql generate go`), the
> mode is selected by `SSQL_MODE` (`record` / `typed` / `parallel`). The older
> `SSQLGO` variable still works as a deprecated alias.

### Core Principles

1. **Use Chain() for pipelines**: Prefer `Chain()` for 2+ operations on same type
2. **Keep it simple**: Write code a human can quickly read and verify
3. **One step at a time**: Break complex operations into clear, logical steps
4. **Descriptive variables**: Use names like `filteredSales`, `groupedData`, not `fs`, `gd`
5. **Always handle errors** from I/O operations (ReadCSV, ReadJSON, etc.)
6. **Use SQL-style names**: `Select` not `Map`, `Where` not `Filter`, `Limit` not `Take`
7. **Type parameters**: Add `[T]` when compiler needs help: `Limit[ssql.Record](10)`
8. **Complete examples**: Include `package main`, `func main()`, and all imports
9. **CRITICAL - Imports**: ONLY import packages that are actually used

### Comprehensiveness Guidance

**Answer what was asked** - Be focused and relevant:
- ✅ If asked to "count employees by department", just count
- ✅ If extra aggregations help understanding, add them (avg, min, max)
- ❌ Don't add unrelated features or complexity

**Example - Appropriate:**
```go
// Asked: "count employees by department"
// Answer: Count + helpful context (avg salary)
map[string]ssql.AggregateFunc{
    "employee_count": ssql.Count(),
    "avg_salary":     ssql.Avg("salary"),  // Helpful context
}
```

**Example - Too Much:**
```go
// Asked: "count employees by department"
// DON'T: Add unrelated features
map[string]ssql.AggregateFunc{
    "employee_count": ssql.Count(),
    "avg_salary":     ssql.Avg("salary"),
    "avg_age":        ssql.Avg("age"),           // Not asked for
    "tenure":         ssql.Avg("years_service"), // Not asked for
    "bonus_total":    ssql.Sum("bonus"),         // Not asked for
}
```

---

## Pattern Recognition

When processing natural language requests, map phrases to ssql operations:

1. **"filter/where/only"** → `ssql.Where(predicate)`
2. **"transform/convert"** → `ssql.Select(transformFn)`
3. **"update/set field/add field"** → `ssql.Update(func(MutableRecord) MutableRecord)`
4. **"group by X"** → `ssql.GroupByFields("groupName", "X")`
5. **"count/sum/average"** → `ssql.Aggregate("groupName", aggregations)`
6. **"top N/first N"** → `ssql.Limit(n)` with `SortBy()`
7. **"sort by/order by"** → `ssql.SortBy(keyFn)` (negative for descending)
8. **"join/combine/lookup"** → `ssql.InnerJoin(rightSeq, ssql.OnFields(...))` or `ssql.LookupJoin()`
9. **"left join/keep all"** → `ssql.LeftJoin(rightSeq, predicate)`
10. **"chart/visualize"** → `ssql.QuickChart()`, `ssql.EnhancedChart()`, or `ssql.InteractiveChart()`
11. **"heatmap/spectrogram"** → `ssql.HeatmapChart()`
12. **"explore/dashboard"** → `ssql.DataExplore()`
13. **"animate/animation"** → `ssql.AnimateChart()`
14. **"FFT/frequency analysis"** → `ssql.FFT(signal)` + `ssql.SpectrumToRecords()`
15. **"spectrogram/STFT"** → `ssql.Spectrogram(signal, opts)` + `ssql.SpectrogramToRecords()`
16. **"smooth/convolve"** → `ssql.ConvolveSame(signal, kernel)`
17. **"deduplicate/unique"** → `ssql.DistinctBy(keyFn)` or `ssql.DistinctBy(ssql.RecordKey)`
18. **"combine/union"** → `ssql.Concat()` + optionally `ssql.DistinctBy()`
19. **"extract signal"** → `ssql.ExtractSignal(records, field)`
20. **"read/write excel"** → `ssql.ReadXLSX()`, `ssql.WriteXLSX()`
21. **"read/write arrow"** → `ssql.ReadArrow()`, `ssql.WriteArrow()`
22. **"rank/row number/top N per group"** → `ssql.Window([]ssql.WindowConfig{{Specs: []ssql.WindowSpec{{Function: ssql.WRowNumber(), ...}}}})`
23. **"running total/cumulative sum"** → `ssql.Window(...)` with `ssql.WSum(field)`
24. **"moving average/rolling"** → `ssql.Window(...)` with `ssql.WAvg(field)` and custom `Frame`
25. **"lag/lead/previous/next row"** → `ssql.Window(...)` with `ssql.WLag(field, n)` or `ssql.WLead(field, n)`
26. **"pivot/cross-tab"** → `ssql.Pivot(rowField, colField, valField, aggFunc)`

---

## Critical Reminders

1. **🚨 Record Access**: CANNOT use `record["field"]` - MUST use `MakeMutableRecord()` to create, `GetOr()` to read
2. **Error Handling**: ALWAYS check errors from `ReadCSV()`, `ReadJSON()`, `FFT()`, etc.
3. **CSV Types**: Numeric CSV values are `int64`/`float64`, not strings
4. **SQL Names**: Use `Select`, `Where`, `Limit` (not Map, Filter, Take)
5. **Imports**: Only import packages actually used in the code
6. **Chain()**: Prefer `Chain()` for multi-step pipelines on same type
7. **Count()**: Parameterless! Field name is the map key
8. **Namespaces**: Must match between `GroupByFields` and `Aggregate`
9. **Separate Steps**: `GroupByFields` and `Aggregate` are separate operations
10. **Import Path**: Always `github.com/rosscartlidge/ssql/v4` (not `/v3`, not without version)
11. **Join Functions**: Use `InnerJoin`, `LeftJoin`, `RightJoin`, `FullJoin` - bare `Join()` doesn't exist
12. **Signal Types**: `ssql.Signal` is `[]float64`, `ssql.Spectrum` has `.Magnitude` and `.Phase` fields
13. **Update vs Select**: Use `Update()` when adding/modifying fields on records. Use `Select()` for arbitrary transformations
14. **Slice to Iterator**: Use `slices.Values(slice)` from standard library - there is NO `ssql.FromSlice`

---

## Quick Validation Checklist

Generated code should have:
- ✅ `package main` and `func main()`
- ✅ Error handling for I/O operations
- ✅ Only imports actually used in the code
- ✅ SQL-style function names (`Select`, `Where`, `Limit`)
- ✅ `Chain()` for multi-step pipelines (when appropriate)
- ✅ Separate `GroupByFields` + `Aggregate` (never combined)
- ✅ Parameterless `Count()` with field name as map key
- ✅ Matching namespaces between GroupByFields and Aggregate
- ✅ Clear, descriptive variable names
- ✅ Proper Record access (`GetOr`, `Get[T]`)

---

*For complete API documentation: `go doc github.com/rosscartlidge/ssql/v4`*
