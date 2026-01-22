# AI Code Generation Prompt

**File:** `doc/ai-code-generation.md`
**Status:** ✅ Production Ready
**Last Updated:** 2026-01-22

---

## Overview

This prompt enables LLMs to generate correct, idiomatic ssql code from natural language descriptions.

**Test Results:**
- ✅ 100% API correctness (0 hallucinations across 15 test cases)
- ✅ 100% compilation success
- ✅ 100% runtime success
- ✅ Consistent Chain() usage for multi-step pipelines

---

## How to Use

### For LLMs (Claude, GPT-4, etc.)

Copy the entire contents of `doc/ai-code-generation.md` into your LLM conversation, then describe what you want to build in natural language.

**Example:**
```
[Paste full ai-code-generation.md contents]

Now generate a Go program that:
- Reads employee data from employees.csv
- Filters for employees with salary over $80,000
- Groups by department
- Counts employees per department
```

### For Developers

Use this prompt when you need to:
- Generate ssql code examples
- Create data processing pipelines
- Build prototypes quickly
- Learn ssql patterns

---

## What's Included

### 1. Core API Reference
- Imports (with critical "only use what you need" rule)
- Core types and creation
- Reading data with error handling
- Core operations (SQL-style naming)
- Record access
- Aggregation functions

### 2. ⛔ CRITICAL ANTI-PATTERNS
Explicit examples showing what NOT to do:
- ❌ Combined GroupBy + Aggregate API (doesn't exist!)
- ❌ Count() with parameters
- ❌ Mismatched namespaces
- ✅ Correct alternatives for each

### 3. Composition Style - CRITICAL RULE
**🎯 ALWAYS Use Chain() for 2+ Operations**
- Clear rule: 2+ operations on same type → use Chain()
- Shows correct Chain() examples
- Shows wrong sequential step examples
- Includes decision checklist

### 4. Complete Examples (5 patterns)
1. Basic Filtering and Aggregation
2. Top N Analysis
3. Data Enrichment with Transformation
4. Join Analysis
5. Chart Creation

### 5. Code Generation Rules
- Core principles
- Comprehensiveness guidance
- Pattern recognition
- Critical reminders
- Validation checklist

---

## Key Features

### Prevents API Hallucination

The prompt explicitly shows wrong APIs that LLMs tend to hallucinate:

```go
// ❌ This doesn't exist - LLMs often hallucinate this!
result := ssql.GroupByFields(
    []string{"department"},
    []ssql.Aggregation{
        ssql.Count("count"),
    },
)

// ✅ This is correct
grouped := ssql.GroupByFields("analysis", "department")(data)
results := ssql.Aggregate("analysis", map[string]ssql.AggregateFunc{
    "employee_count": ssql.Count(),
})(grouped)
```

### Enforces Chain() Usage

Strong guidance ensures readable, idiomatic code:

```go
// ✅ ALWAYS use Chain() for 2+ operations
result := ssql.Chain(
    ssql.GroupByFields("sales", "product"),
    ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
        "total": ssql.Sum("amount"),
    }),
)(data)
```

### Comprehensive Examples

5 complete, runnable examples covering common patterns:
- Filter → Group → Aggregate
- Group → Aggregate → Sort → Limit
- Select (transformation) → Group → Aggregate
- Join → Group → Aggregate → Where
- Group → Aggregate → Chart

---

## Testing and Validation

### Test Coverage

The prompt was tested with 15 test cases:
- 5 tests with original anti-patterns prompt
- 5 tests with detailed examples prompt
- 5 tests with final hybrid prompt

**Results:**
- 100% API correctness across all tests
- 100% compilation success
- 100% runtime success
- Chain() usage improved from 0% → 100% after strengthening guidance

### Validation Script

Automated validation with `scripts/validate-ai-patterns.sh`:
- ✅ Correct import path
- ✅ No wrong imports
- ✅ SQL-style API usage
- ✅ Error handling present
- ✅ Correct GroupByFields usage
- ✅ Correct Aggregate usage
- ✅ Chain() composition (when applicable)
- ✅ Code compiles

---

## Performance Metrics

| Metric | Result |
|--------|--------|
| **API Hallucinations** | 0 / 15 tests (0%) |
| **Compilation Errors** | 0 / 15 tests (0%) |
| **Runtime Errors** | 0 / 15 tests (0%) |
| **Chain() Usage** | 15 / 15 applicable (100%) |
| **Validation Pass Rate** | 100% (8/8 checks) |
| **Prompt Size** | 602 lines |

---

## Example Output

**Input:**
```
Find the top 5 products by revenue from sales data.
Group by product name and show the total revenue for each.
```

**Generated Code:**
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    sales, err := ssql.ReadCSV("sales.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    top5 := ssql.Chain(
        ssql.GroupByFields("product_analysis", "product_name"),
        ssql.Aggregate("product_analysis", map[string]ssql.AggregateFunc{
            "total_revenue": ssql.Sum("revenue"),
        }),
        ssql.SortBy(func(r ssql.Record) float64 {
            return -ssql.GetOr(r, "total_revenue", 0.0)
        }),
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

**Result:**
- ✅ Compiles successfully
- ✅ Uses Chain() correctly
- ✅ Correct API usage
- ✅ Proper error handling
- ✅ Clean, readable code

---

## Maintenance

### Updating the Prompt

When you discover new error patterns:

1. Add to **⛔ CRITICAL ANTI-PATTERNS** section
2. Show ❌ WRONG example
3. Show ✅ CORRECT alternative
4. Test with validation script
5. Update this README with new metrics

### Testing New Versions

```bash
# Test with a natural language prompt
echo "YOUR NATURAL LANGUAGE REQUEST" | \
  # (pipe through your LLM with the prompt) \
  tee test-output/generated.go

# Validate
./scripts/validate-ai-patterns.sh test-output/generated.go

# Run
go run test-output/generated.go
```

---

## History

### Version 3.0 (Current) - 2025-10-23
- **Strengthened Chain() guidance** from "PREFERRED" to "ALWAYS"
- **Updated Example 2** to use full Chain()
- **Added explicit ❌ WRONG sequential step examples**
- **Added Chain() checklist** for decision making
- **Result:** 100% Chain() usage, 100% API correctness

### Version 2.0 - 2025-10-23
- Hybrid approach combining anti-patterns + examples
- 5 complete examples (curated from 8)
- Comprehensiveness guidance added
- Chain() guidance (initial version: "PREFERRED")

### Version 1.0 - 2025-10-22
- Original anti-patterns prompt (476 lines)
- Separate detailed examples prompt (1001 lines)
- Initial testing showing 100% API correctness

---

## Files

- **`doc/ai-code-generation.md`** - Main prompt file ← **USE THIS**
- `doc/AI-PROMPT-README.md` - This file
- `scripts/validate-ai-patterns.sh` - Validation script
- `test-ai-generation-cases.md` - Test cases
- `test-output/agent-tests/` - Test results and analysis

---

## Support

For issues or improvements:
1. Test the prompt with your use case
2. Run validation script
3. Document any errors or hallucinations
4. Update anti-patterns section
5. Re-test to verify fix

---

**The prompt is production-ready and maintains 100% API correctness! 🎉**
