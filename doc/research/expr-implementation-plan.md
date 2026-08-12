# Expression Evaluation Implementation Plan

Reference: DFC019
Created: 2025-11-12
Last modified: 2025-11-12

[Back to Index](./README.md)

**Decision:** Use **expr-lang/expr** for computed expressions in `ssql update`

**Date:** 2025-11-12

**Target Version:** v2.1.0 (new feature)

## Overview

Add expression evaluation to `ssql update` command using expr-lang/expr library, enabling computed field updates instead of just literal values.

**Before:**
```bash
ssql update -set status active         # Literals only
ssql update -set count 42
```

**After:**
```bash
ssql update -set total 'price * quantity'                    # Math expressions
ssql update -set tier 'sales > 10000 ? "gold" : "silver"'   # Conditionals
ssql update -set name 'upper(trim(name))'                   # String functions
```

## Phase 1: Add expr Dependency and Basic Infrastructure

**Goal:** Set up expr library and basic evaluation infrastructure

**Tasks:**

1. **Add expr dependency**
   ```bash
   go get github.com/expr-lang/expr
   ```

2. **Create expression evaluation helper** in `cmd/ssql/commands/helpers.go`:
   ```go
   import "github.com/expr-lang/expr"

   // evaluateExpression evaluates an expr expression against a record
   func evaluateExpression(expression string, record ssql.Record) (any, error) {
       // Build environment with all record fields
       env := make(map[string]interface{})
       for k, v := range record.All() {
           env[k] = v
       }

       // Compile and run expression
       program, err := expr.Compile(expression, expr.Env(env))
       if err != nil {
           return nil, fmt.Errorf("compile expression: %w", err)
       }

       result, err := expr.Run(program, env)
       if err != nil {
           return nil, fmt.Errorf("execute expression: %w", err)
       }

       return result, nil
   }
   ```

3. **Test basic evaluation**
   - Create test in `cmd/ssql/commands/helpers_test.go`
   - Test simple math: `2 + 2`
   - Test field reference: `price * quantity`
   - Test conditionals: `x > 10 ? "high" : "low"`

**Acceptance Criteria:**
- ✅ expr dependency added to go.mod
- ✅ evaluateExpression() function works for basic expressions
- ✅ Tests pass for math, field access, and conditionals

**Time Estimate:** 1 hour

## Phase 2: Implement Expression Detection and Evaluation

**Goal:** Modify update command to detect and evaluate expressions

**Tasks:**

1. **Add expression detection logic** in `cmd/ssql/commands/update.go`:
   ```go
   // isExpression checks if a value string is an expression vs a literal
   func isExpression(value string) bool {
       // Heuristics:
       // - Contains operators: +, -, *, /, >, <, ==, etc.
       // - Contains parentheses: (, )
       // - Contains ternary: ?
       // - Contains functions: upper(), trim(), etc.

       // Simple check: if it contains any operator or function call
       operators := []string{"+", "-", "*", "/", ">", "<", "==", "!=", "?", "("}
       for _, op := range operators {
           if strings.Contains(value, op) {
               return true
           }
       }
       return false
   }
   ```

2. **Modify update command handler** to use expressions:
   ```go
   // In the update loop, after parsing clause.updates
   for _, upd := range clause.updates {
       var parsedValue any

       if isExpression(upd.value) {
           // Evaluate as expression
           result, err := evaluateExpression(upd.value, frozen)
           if err != nil {
               // Fall back to literal? Or fail?
               parsedValue = parseValue(upd.value)
           } else {
               parsedValue = result
           }
       } else {
           // Parse as literal
           parsedValue = parseValue(upd.value)
       }

       // Apply value to record (existing logic)
       switch v := parsedValue.(type) {
       case int64:
           mut = mut.Int(upd.field, v)
       // ... rest of type handling
       }
   }
   ```

3. **Add integration tests**:
   ```bash
   # Test: Math expression
   echo '{"price":10,"qty":5}' | ssql update -set total 'price * qty'
   # Expected: {"price":10,"qty":5,"total":50}

   # Test: Conditional
   echo '{"sales":15000}' | ssql update -set tier 'sales > 10000 ? "gold" : "silver"'
   # Expected: {"sales":15000,"tier":"gold"}

   # Test: String function
   echo '{"name":"  ALICE  "}' | ssql update -set name 'trim(lower(name))'
   # Expected: {"name":"alice"}
   ```

**Acceptance Criteria:**
- ✅ Expressions detected automatically (no flag needed)
- ✅ Literals still work (backward compatible)
- ✅ Math expressions work: `price * quantity`
- ✅ Conditionals work: `x > 10 ? "high" : "low"`
- ✅ Integration tests pass

**Time Estimate:** 2 hours

## Phase 3: Field Auto-Expansion (Optional Enhancement)

**Goal:** Make simple field references more intuitive

**Current:**
```bash
# User must reference fields exactly as they exist
ssql update -set total 'price * quantity'  # Works if fields exist
```

**Enhanced:**
```bash
# Auto-provide default values for missing fields
ssql update -set total 'price * quantity'  # Works even if fields missing (uses 0)
```

**Implementation:**

1. **Pre-process expression** to add GetOr() wrappers:
   ```go
   func expandFieldReferences(expression string, record ssql.Record) string {
       // Find all identifiers (field names)
       re := regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\b`)

       expanded := re.ReplaceAllStringFunc(expression, func(field string) string {
           // Skip keywords and built-in functions
           if isKeywordOrFunction(field) {
               return field
           }

           // Check if field exists in record
           if _, exists := ssql.Get[any](record, field); exists {
               return field  // Field exists, use as-is
           }

           // Field missing, provide default
           return fmt.Sprintf("(has(%q) ? %s : 0)", field, field)
       })

       return expanded
   }
   ```

2. **Update evaluateExpression** to use expansion:
   ```go
   func evaluateExpression(expression string, record ssql.Record) (any, error) {
       // Expand field references with defaults
       expanded := expandFieldReferences(expression, record)

       // Build environment
       env := make(map[string]interface{})
       for k, v := range record.All() {
           env[k] = v
       }

       // Add 'has' helper function
       env["has"] = func(field string) bool {
           _, exists := ssql.Get[any](record, field)
           return exists
       }

       // Compile and run
       program, err := expr.Compile(expanded, expr.Env(env))
       // ... rest of evaluation
   }
   ```

**Acceptance Criteria:**
- ✅ Expressions work even with missing fields
- ✅ Missing numeric fields default to 0
- ✅ Existing behavior unchanged

**Decision Point:** This might be optional - could keep it simple and require all fields to exist.

**Time Estimate:** 2 hours (if implemented)

## Phase 4: Type Inference

**Goal:** Automatically infer result type and call correct MutableRecord method

**Current Challenge:**
```go
// We get 'any' back from expr, need to determine type
result, _ := evaluateExpression("price * quantity", record)

// Which method to call?
mut.Int(field, ???)      // If result is int64
mut.Float(field, ???)    // If result is float64
mut.String(field, ???)   // If result is string
```

**Implementation:**

1. **Add type inference function**:
   ```go
   func applyValueToRecord(mut ssql.MutableRecord, field string, value any) ssql.MutableRecord {
       switch v := value.(type) {
       case int:
           return mut.Int(field, int64(v))
       case int64:
           return mut.Int(field, v)
       case float64:
           return mut.Float(field, v)
       case float32:
           return mut.Float(field, float64(v))
       case string:
           return mut.String(field, v)
       case bool:
           return mut.Bool(field, v)
       case time.Time:
           return ssql.Set(mut, field, v)
       default:
           // Try to convert to string
           return mut.String(field, fmt.Sprintf("%v", v))
       }
   }
   ```

2. **Use in update command**:
   ```go
   if isExpression(upd.value) {
       result, err := evaluateExpression(upd.value, frozen)
       if err != nil {
           return fmt.Errorf("evaluating expression %q: %w", upd.value, err)
       }
       mut = applyValueToRecord(mut, upd.field, result)
   } else {
       parsedValue := parseValue(upd.value)
       // ... existing type switch
   }
   ```

**Acceptance Criteria:**
- ✅ Int expressions produce int64 fields
- ✅ Float expressions produce float64 fields
- ✅ String expressions produce string fields
- ✅ Bool expressions produce bool fields
- ✅ No manual type specification needed

**Time Estimate:** 1 hour

## Phase 5: Code Generation Support

**Goal:** Generate clean Go code from expressions

**Challenge:** Convert expr syntax to Go code

**Approach:**

1. **Simple expressions** - Direct translation:
   ```bash
   # Input
   ssql update -set total 'price * quantity'

   # Generated
   mut = mut.Float("total",
       ssql.GetOr(r, "price", 0.0) * ssql.GetOr(r, "quantity", 0.0))
   ```

2. **Complex expressions** - Use expr.Eval() in generated code:
   ```bash
   # Input
   ssql update -set tier 'sales > 10000 ? "gold" : sales > 5000 ? "silver" : "bronze"'

   # Generated (Option A: Inline expr)
   tierExpr, _ := expr.Compile(`sales > 10000 ? "gold" : sales > 5000 ? "silver" : "bronze"`)
   tierResult, _ := expr.Run(tierExpr, map[string]any{"sales": ssql.GetOr(r, "sales", 0.0)})
   mut = mut.String("tier", tierResult.(string))

   # OR (Option B: Translate to Go if-else)
   sales := ssql.GetOr(r, "sales", 0.0)
   var tier string
   if sales > 10000 {
       tier = "gold"
   } else if sales > 5000 {
       tier = "silver"
   } else {
       tier = "bronze"
   }
   mut = mut.String("tier", tier)
   ```

**Implementation:**

1. **Start with simple approach** - embed expr in generated code:
   ```go
   func generateUpdateCodeWithExpr(field, expression string) string {
       return fmt.Sprintf(`
   // Evaluate expression: %s
   env := map[string]any{
       "price": ssql.GetOr(frozen, "price", 0.0),
       "quantity": ssql.GetOr(frozen, "quantity", 0.0),
   }
   exprProg, _ := expr.Compile(%q)
   result, _ := expr.Run(exprProg, env)
   mut = applyValue(mut, %q, result)
   `, expression, expression, field)
   }
   ```

2. **Add expr import** to generated code:
   ```go
   imports := []string{"github.com/expr-lang/expr"}
   ```

3. **Later: Add smart translator** for common patterns:
   - Math expressions → Direct Go math
   - Ternary → Go if-else
   - String functions → Go strings package

**Acceptance Criteria:**
- ✅ Generated code compiles
- ✅ Generated code produces same results as CLI execution
- ✅ expr import added to generated imports

**Time Estimate:** 3 hours

## Phase 6: Custom Functions

**Goal:** Add useful custom functions to expr environment

**Standard expr functions:**
- `len`, `all`, `any`, `filter`, `map`

**Add ssql-specific functions:**

```go
func createExprEnvironment(record ssql.Record) map[string]interface{} {
    env := make(map[string]interface{})

    // Add all record fields
    for k, v := range record.All() {
        env[k] = v
    }

    // Add custom functions
    env["upper"] = strings.ToUpper
    env["lower"] = strings.ToLower
    env["trim"] = strings.TrimSpace
    env["replace"] = strings.ReplaceAll
    env["split"] = strings.Split
    env["join"] = strings.Join

    env["round"] = math.Round
    env["floor"] = math.Floor
    env["ceil"] = math.Ceil
    env["abs"] = math.Abs

    env["min"] = math.Min
    env["max"] = math.Max

    return env
}
```

**Update evaluateExpression:**
```go
func evaluateExpression(expression string, record ssql.Record) (any, error) {
    env := createExprEnvironment(record)

    program, err := expr.Compile(expression,
        expr.Env(env),
        expr.AllowUndefinedVariables(), // Handle missing fields gracefully
    )
    if err != nil {
        return nil, fmt.Errorf("compile expression: %w", err)
    }

    result, err := expr.Run(program, env)
    if err != nil {
        return nil, fmt.Errorf("execute expression: %w", err)
    }

    return result, nil
}
```

**Test custom functions:**
```bash
# String functions
echo '{"name":"  alice  "}' | ssql update -set name 'upper(trim(name))'
# Expected: {"name":"ALICE"}

# Math functions
echo '{"price":19.99}' | ssql update -set price 'round(price)'
# Expected: {"price":20}

# Replace
echo '{"title":"Hello World"}' | ssql update -set slug 'lower(replace(title, " ", "-"))'
# Expected: {"title":"Hello World","slug":"hello-world"}
```

**Acceptance Criteria:**
- ✅ String functions work: upper, lower, trim, replace, split, join
- ✅ Math functions work: round, floor, ceil, abs, min, max
- ✅ Functions documented in help text
- ✅ Tests pass for all custom functions

**Time Estimate:** 2 hours

## Phase 7: Testing, Documentation, and Release

**Goal:** Ensure robustness and ship v2.1.0

**Tasks:**

1. **Comprehensive testing**:
   - Unit tests for expression evaluation
   - Integration tests for update command
   - Test error handling (malformed expressions)
   - Test edge cases (missing fields, type mismatches)
   - Test code generation output
   - Performance testing (ensure < 1ms overhead)

2. **Update documentation**:
   - Update `ssql update -help` with expression examples
   - Add expressions section to README
   - Document custom functions
   - Add cookbook examples

3. **Update CLAUDE.md**:
   - Document expression evaluation architecture
   - Add helpers.go documentation
   - Note expr dependency

4. **Changelog**:
   ```markdown
   # v2.1.0 (2025-11-XX)

   ## New Features

   - **Expression evaluation in update command** - Compute field values using expressions instead of just literals
     - Math expressions: `ssql update -set total 'price * quantity'`
     - Conditionals: `ssql update -set tier 'sales > 10000 ? "gold" : "silver"'`
     - String functions: `ssql update -set name 'upper(trim(name))'`
     - Custom functions: upper, lower, trim, replace, round, floor, ceil, abs, min, max
     - Code generation support for expressions
   - Uses expr-lang/expr library for fast, safe expression evaluation

   ## Dependencies

   - Added github.com/expr-lang/expr (~500KB binary size increase)
   ```

5. **Version bump**:
   ```bash
   echo "2.1.0" > cmd/ssql/version/version.txt
   ```

6. **Release process**:
   ```bash
   # Run all tests
   go test ./...

   # Build and test manually
   go build -o ssql cmd/ssql/*.go
   ./ssql version  # Should show v2.1.0

   # Test expressions
   echo '{"price":10,"qty":5}' | ./ssql update -set total 'price * qty'

   # Commit version
   git add cmd/ssql/version/version.txt
   git commit -m "Bump version to v2.1.0"

   # Create tag
   git tag -a v2.1.0 -m "Release v2.1.0: Expression evaluation in update command"

   # Push
   git push && git push --tags

   # Test installation
   cd /tmp
   GOPROXY=direct go install github.com/rosscartlidge/ssql/v2/cmd/ssql@v2.1.0
   ssql version
   ```

**Acceptance Criteria:**
- ✅ All tests pass
- ✅ Documentation complete
- ✅ v2.1.0 tagged and pushed
- ✅ Installation works from GitHub
- ✅ Example expressions work as documented

**Time Estimate:** 3 hours

## Total Time Estimate

- Phase 1: 1 hour (infrastructure)
- Phase 2: 2 hours (basic evaluation)
- Phase 3: 2 hours (field expansion - optional)
- Phase 4: 1 hour (type inference)
- Phase 5: 3 hours (code generation)
- Phase 6: 2 hours (custom functions)
- Phase 7: 3 hours (testing and release)

**Total: ~14 hours (2 days of focused work)**

## Open Questions / Design Decisions

1. **Field expansion** - Should we auto-expand field references with defaults, or require all fields to exist?
   - **Recommendation:** Start simple, require fields to exist. Add expansion later if needed.

2. **Error handling** - What happens if expression fails on some records?
   - **Option A:** Skip record (silent)
   - **Option B:** Fail entire pipeline (strict)
   - **Option C:** Set field to default value (lenient)
   - **Recommendation:** Option B (strict) - fail fast with clear error message

3. **Code generation strategy** - Embed expr or translate to Go?
   - **Phase 5 start:** Embed expr.Eval() in generated code (simple)
   - **Phase 5 enhancement:** Add translator for common patterns (optimization)
   - **Recommendation:** Start with embedding, optimize later

4. **Type coercion** - What if expression returns int but field expects float?
   - **Recommendation:** Use type inference in Phase 4, convert automatically

5. **Expression caching** - Should we cache compiled expressions?
   - **Recommendation:** Yes! Compile once, evaluate many times
   - Add in Phase 2

## Success Metrics

- ✅ 95% of update use cases covered (math, strings, conditionals)
- ✅ Performance overhead < 1ms per expression evaluation
- ✅ Zero breaking changes (backward compatible)
- ✅ Generated code compiles and runs correctly
- ✅ No regressions in existing tests

## Next Steps

1. Get approval on this plan
2. Start Phase 1: Add expr dependency
3. Implement phases sequentially
4. Test thoroughly at each phase
5. Ship v2.1.0!
