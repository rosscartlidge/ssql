# Testing AI Code Generation - Quick Reference

This document explains how to regularly test that the AI code generation documentation is working correctly.

## Quick Commands

### Run All Tests
```bash
./scripts/test-ai-code-generation.sh
```

This runs all 5 reference implementations, validates patterns, and ensures they execute successfully.

**Expected output:**
```
========================================
ssql AI Code Generation Test Suite
========================================

Testing Reference Implementations...

Test 1: test_case_1_manual.go
  ✓ Validation passed
  ✓ Execution passed

Test 2: test_case_2_top_n.go
  ✓ Validation passed
  ✓ Execution passed

... (3 more tests)

========================================
Test Summary
========================================
Total tests: 5
Passed: 5
Failed: 0

✓ All AI code generation tests passed!
```

### Validate a Single File
```bash
./scripts/validate-ai-patterns.sh <file.go>
```

Checks for:
- ✅ Correct import path
- ✅ SQL-style API usage (Where not Filter)
- ✅ Error handling for ReadCSV
- ✅ Proper GroupByFields/Aggregate syntax
- ✅ Code compilation

### Run a Single Test
```bash
go run test-output/test_case_1_manual.go
```

## When to Run These Tests

### Regularly (Recommended)
- **Before pushing changes** to AI documentation (`doc/ai-code-generation.md`)
- **After updating the API** - Ensure reference implementations still work
- **Weekly** - As part of regular maintenance

### As Needed
- **When testing AI-generated code** - Validate it follows best practices
- **When onboarding new contributors** - Show examples of correct code
- **When debugging issues** - Compare against working reference implementations

## Test Coverage

The test suite covers these common patterns:

1. **Basic Filtering and Grouping** (`test_case_1_manual.go`)
   - Where clause for filtering
   - GroupByFields for grouping
   - Aggregate with Count

2. **Top N with Chain** (`test_case_2_top_n.go`)
   - GroupByFields + Aggregate
   - SortBy with descending order (negative values)
   - Limit for top N
   - Chain composition

3. **Join Operation** (`test_case_3_join.go`)
   - InnerJoin with OnFields
   - Multiple GroupByFields
   - Sum aggregation

4. **Transformation** (`test_case_4_transform.go`)
   - Select for record transformation
   - SetImmutable for adding fields
   - Switch statement logic

5. **Chart Creation** (`test_case_5_chart.go`)
   - GroupByFields + Aggregate
   - QuickChart for visualization

## What Gets Validated

Each test checks:

### 1. Import Path
```go
✅ import "github.com/rosscartlidge/ssql"
❌ import "github.com/rocketlaunchr/ssql"  // Wrong!
```

### 2. SQL-Style Naming
```go
✅ ssql.Where(predicate)
❌ ssql.Filter(predicate)  // Wrong! Filter is a type, not a function
```

### 3. Error Handling
```go
✅ data, err := ssql.ReadCSV("file.csv")
   if err != nil {
       log.Fatalf("Failed: %v", err)
   }

❌ data, _ := ssql.ReadCSV("file.csv")  // Wrong! Always check errors
```

### 4. GroupByFields Syntax
```go
✅ ssql.GroupByFields("namespace", "field1", "field2")

❌ ssql.GroupByFields([]string{"field1"}, []Aggregation{...})  // Wrong API!
```

### 5. Aggregate Syntax
```go
✅ ssql.Aggregate("namespace", map[string]ssql.AggregateFunc{
       "count": ssql.Count(),
       "total": ssql.Sum("amount"),
   })

❌ ssql.Aggregate("namespace", ssql.Count("count"))  // Wrong!
```

### 6. Count Syntax
```go
✅ "employee_count": ssql.Count()  // Field name is map key

❌ ssql.Count("employee_count")  // Wrong! Count takes no parameters
```

### 7. Composition Style
```go
✅ ssql.Chain(
       ssql.Where(pred),
       ssql.Select(transform),
   )(data)

⚠️  ssql.Select(transform)(data)  // Works but Chain is clearer
```

### 8. Compilation
All code must compile with `go build`.

## CI/CD Integration

To add this to your CI pipeline:

```yaml
# .github/workflows/test.yml
name: Test AI Code Generation

on: [push, pull_request]

jobs:
  test-ai-generation:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'
      - name: Run AI code generation tests
        run: ./scripts/test-ai-code-generation.sh
```

## Updating Test Cases

If you add new patterns to the AI documentation:

1. **Create reference implementation:**
   ```bash
   vim test-output/test_case_6_new_pattern.go
   ```

2. **Validate it:**
   ```bash
   ./scripts/validate-ai-patterns.sh test-output/test_case_6_new_pattern.go
   go run test-output/test_case_6_new_pattern.go
   ```

3. **Add to test cases:**
   - Update `test-ai-generation-cases.md` with natural language prompt
   - Document expected patterns

4. **Run full suite:**
   ```bash
   ./scripts/test-ai-code-generation.sh
   ```

5. **Commit:**
   ```bash
   git add test-output/test_case_6_new_pattern.go test-ai-generation-cases.md
   git commit -m "Add test case for new pattern"
   ```

## Troubleshooting

### Test fails with "Validation failed"
Run the validation script directly to see details:
```bash
./scripts/validate-ai-patterns.sh test-output/test_case_X.go
```

### Test fails with "Execution failed"
Run the code directly to see the error:
```bash
go run test-output/test_case_X.go
```

### All tests fail
Check Go version:
```bash
go version  # Should be 1.23 or higher
```

### New pattern not validated
Add check to `scripts/validate-ai-patterns.sh`:
```bash
# Check X: New pattern
echo -n "Checking new pattern... "
if grep -q "expected_pattern" "$FILE"; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    ((ERRORS++))
fi
```

## Files Reference

- `scripts/test-ai-code-generation.sh` - Main test runner
- `scripts/validate-ai-patterns.sh` - Pattern validation script
- `test-ai-generation-cases.md` - Natural language test prompts
- `test-output/test_case_*.go` - Reference implementations
- `test-output/README.md` - Detailed test suite documentation
- `test-output/ai-validation-report.md` - Analysis of AI prompt quality

## Summary

**To test regularly, just run:**
```bash
./scripts/test-ai-code-generation.sh
```

This ensures:
- ✅ All reference implementations compile
- ✅ All code follows ssql best practices
- ✅ AI documentation stays accurate
- ✅ Examples work as expected

**Quick validation of your own code:**
```bash
./scripts/validate-ai-patterns.sh your-code.go
```
