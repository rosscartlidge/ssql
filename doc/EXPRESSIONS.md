# Expression Language Reference

ssql supports powerful expression evaluation via the [expr-lang](https://expr-lang.org/) library, enabling computed fields, complex filters, and data transformations without writing Go code.

## Quick Start

**Commands with expression support:**
- `ssql update -set-expr <field> '<expression>'` - Set field to computed value
- `ssql where -where-expr '<boolean-expression>'` - Filter with complex conditions

**Quick examples:**
```bash
# Calculate derived fields
ssql from sales.csv | ssql update -set-expr total 'price * qty'

# Complex filtering
ssql from users.csv | ssql where -where-expr 'age >= 18 and status == "active"'

# Conditional logic
ssql from sales.csv | ssql update -set-expr tier 'revenue > 10000 ? "gold" : "silver"'

# String manipulation
ssql from data.csv | ssql update -set-expr email 'lower(trim(email))'
```

## Operators

### Arithmetic Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `+` | Addition | `price + tax` |
| `-` | Subtraction | `revenue - cost` |
| `*` | Multiplication | `price * qty` |
| `/` | Division | `total / count` |
| `%` | Modulo | `value % 10` |
| `**` | Power | `base ** exponent` |

### Comparison Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `==` | Equal | `status == "active"` |
| `!=` | Not equal | `dept != "Sales"` |
| `<` | Less than | `age < 18` |
| `>` | Greater than | `salary > 50000` |
| `<=` | Less than or equal | `score <= 100` |
| `>=` | Greater than or equal | `age >= 21` |

### Logical Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `and` | Logical AND | `age >= 18 and status == "active"` |
| `or` | Logical OR | `dept == "Sales" or dept == "Marketing"` |
| `not` | Logical NOT | `not contains(email, "@test.com")` |

### Special Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `? :` | Ternary conditional | `age >= 18 ? "adult" : "minor"` |
| `??` | Nil coalescing | `value ?? "default"` |
| `in` | Membership test | `status in ["active", "pending"]` |
| `contains` | Contains (string or array) | `email contains "@"` |
| `\|` | Pipe operator (chain functions) | `name \| trim \| upper` |

## Built-in Functions

### String Functions

| Function | Description | Example |
|----------|-------------|---------|
| `upper(str)` | Convert to uppercase | `upper("hello")` → `"HELLO"` |
| `lower(str)` | Convert to lowercase | `lower("WORLD")` → `"world"` |
| `trim(str)` | Remove leading/trailing whitespace | `trim("  text  ")` → `"text"` |
| `trimPrefix(str, prefix)` | Remove prefix from string | `trimPrefix("hello world", "hello ")` → `"world"` |
| `trimSuffix(str, suffix)` | Remove suffix from string | `trimSuffix("file.csv", ".csv")` → `"file"` |
| `split(str, sep)` | Split string into array | `split("a,b,c", ",")` → `["a", "b", "c"]` |
| `splitAfter(str, sep)` | Split, keeping separator attached | `splitAfter("a,b,c", ",")` → `["a,", "b,", "c"]` |
| `join(arr, sep)` | Join array into string | `join(["a", "b"], ",")` → `"a,b"` |
| `replace(str, old, new)` | Replace all occurrences | `replace("hello world", "world", "there")` → `"hello there"` |
| `replaceRegex(str, pat, repl)` | Regex replace with capture groups | `replaceRegex("abc 123", "[^0-9]", "")` → `"123"` |
| `repeat(str, n)` | Repeat string n times | `repeat("ab", 3)` → `"ababab"` |
| `indexOf(str, sub)` | First index of substring (-1 if missing) | `indexOf("hello", "ll")` → `2` |
| `lastIndexOf(str, sub)` | Last index of substring (-1 if missing) | `lastIndexOf("hello", "l")` → `3` |
| `hasPrefix(str, prefix)` | Check if starts with prefix | `hasPrefix("hello", "he")` → `true` |
| `hasSuffix(str, suffix)` | Check if ends with suffix | `hasSuffix("world", "ld")` → `true` |
| `contains(str, substr)` | Check if contains substring | `contains("hello", "ll")` → `true` |

**Note:** `contains`, `startsWith`, `endsWith` also work as infix operators: `name startsWith "A"`.

**Examples:**
```bash
# Normalize email addresses
ssql update -set-expr email 'lower(trim(email))'

# Extract domain from email
ssql update -set-expr domain 'split(email, "@")[1]'

# Remove file extension
ssql update -set-expr base 'trimSuffix(filename, ".csv")'

# Regex replacement (use \\ for regex backslashes)
ssql update -set-expr clean 'replaceRegex(name, "[^a-zA-Z]", "_")'
ssql update -set-expr digits 'replaceRegex(code, "[^0-9]", "")'

# Capture group expansion ($1, $2, ${name})
ssql update -set-expr fmt_phone 'replaceRegex(phone, "(\\d{3})(\\d{3})(\\d{4})", "($1) $2-$3")'
```

### Math Functions

| Function | Description | Example |
|----------|-------------|---------|
| `round(num)` | Round to nearest integer | `round(3.7)` → `4` |
| `floor(num)` | Round down | `floor(3.7)` → `3` |
| `ceil(num)` | Round up | `ceil(3.2)` → `4` |
| `abs(num)` | Absolute value | `abs(-5)` → `5` |
| `min(a, b)` | Minimum of two values | `min(10, 20)` → `10` |
| `max(a, b)` | Maximum of two values | `max(10, 20)` → `20` |

**Examples:**
```bash
# Calculate discounted price (rounded)
ssql update -set-expr final_price 'round(price * 0.85)'

# Ensure non-negative values
ssql update -set-expr balance 'max(0, amount - fees)'

# Calculate absolute difference
ssql update -set-expr diff 'abs(actual - expected)'
```

### Array Functions

**Access:**

| Function | Description | Example |
|----------|-------------|---------|
| `len(arr)` | Length of array/string | `len([1, 2, 3])` → `3` |
| `first(arr)` | First element | `first([10, 20, 30])` → `10` |
| `last(arr)` | Last element | `last([10, 20, 30])` → `30` |
| `get(v, key)` | Safe access (nil if missing) | `get({"a": 1}, "z")` → `nil` |

**Transform:**

| Function | Description | Example |
|----------|-------------|---------|
| `take(arr, n)` | First n elements | `take([1, 2, 3, 4], 2)` → `[1, 2]` |
| `reverse(arr)` | Reverse array | `reverse([1, 2, 3])` → `[3, 2, 1]` |
| `sort(arr)` | Sort ascending | `sort([3, 1, 2])` → `[1, 2, 3]` |
| `sortBy(arr, pred)` | Sort by predicate | `sortBy([{"n":3},{"n":1}], {.n})` |
| `uniq(arr)` | Remove duplicates | `uniq([1, 1, 2, 3])` → `[1, 2, 3]` |
| `flatten(arr)` | Flatten nested arrays | `flatten([[1,2],[3,4]])` → `[1,2,3,4]` |
| `concat(a, b)` | Concatenate arrays | `concat([1,2], [3,4])` → `[1,2,3,4]` |
| `map(arr, fn)` | Transform each element | `map([1, 2, 3], {# * 2})` → `[2, 4, 6]` |

**Filter & Search:**

| Function | Description | Example |
|----------|-------------|---------|
| `filter(arr, pred)` | Filter elements | `filter([1, 2, 3], {# > 1})` → `[2, 3]` |
| `find(arr, pred)` | First matching element | `find([1, 2, 3], {# > 1})` → `2` |
| `findIndex(arr, pred)` | Index of first match | `findIndex([1, 2, 3], {# > 1})` → `1` |
| `findLast(arr, pred)` | Last matching element | `findLast([1, 2, 3], {# < 3})` → `2` |
| `findLastIndex(arr, pred)` | Index of last match | `findLastIndex([1, 2, 3], {# < 3})` → `1` |
| `groupBy(arr, pred)` | Group by predicate result | `groupBy([1,2,3,4], {# % 2 == 0 ? "even" : "odd"})` |

**Test:**

| Function | Description | Example |
|----------|-------------|---------|
| `all(arr, pred)` | All elements match | `all([2, 4, 6], {# % 2 == 0})` → `true` |
| `any(arr, pred)` | At least one matches | `any([1, 2, 3], {# > 2})` → `true` |
| `one(arr, pred)` | Exactly one matches | `one([1, 2, 3], {# == 2})` → `true` |
| `none(arr, pred)` | No elements match | `none([1, 2, 3], {# > 5})` → `true` |

**Aggregate:**

| Function | Description | Example |
|----------|-------------|---------|
| `count(arr)` | Count of elements | `count([1, 2, 3])` → `3` |
| `sum(arr)` | Sum of elements | `sum([1, 2, 3])` → `6` |
| `mean(arr)` | Average of elements | `mean([1, 2, 3, 4, 5])` → `3` |
| `median(arr)` | Median of elements | `median([1, 2, 3, 4, 5])` → `3` |
| `reduce(arr, pred, init)` | Reduce to single value | `reduce([1,2,3,4], {# + #acc}, 0)` → `10` |

**Note:** In predicates, `#` represents the current element, `#acc` the accumulator in reduce.

**Examples:**
```bash
# Check if all scores are passing
ssql where -where-expr 'all(scores, {# >= 60})'

# Average score
ssql update -set-expr avg_score 'mean(scores)'

# Top 3 scores
ssql update -set-expr top3 'take(sort(scores), 3)'

# Count high-value items
ssql update -set-expr high_count 'count(filter(prices, {# > 100}))'

# Check no failures
ssql where -where-expr 'none(scores, {# < 60})'
```

### Type & Encoding Functions

**Type Conversion:**

| Function | Description | Example |
|----------|-------------|---------|
| `int(value)` | Convert to integer | `int("123")` → `123` |
| `float(value)` | Convert to float | `float("3.14")` → `3.14` |
| `string(value)` | Convert to string | `string(123)` → `"123"` |
| `type(value)` | Get type name as string | `type(42)` → `"int"` |

**JSON:**

| Function | Description | Example |
|----------|-------------|---------|
| `toJSON(value)` | Serialize to JSON string | `toJSON({"a": 1})` → `'{"a":1}'` |
| `fromJSON(str)` | Parse JSON string to value | `fromJSON('{"a":1}')` → `{"a": 1}` |

**Base64:**

| Function | Description | Example |
|----------|-------------|---------|
| `toBase64(str)` | Encode to Base64 | `toBase64("hello")` → `"aGVsbG8="` |
| `fromBase64(str)` | Decode from Base64 | `fromBase64("aGVsbG8=")` → `"hello"` |

**Map Conversion:**

| Function | Description | Example |
|----------|-------------|---------|
| `toPairs(map)` | Map to key-value pairs | `toPairs({"a":1})` → `[["a", 1]]` |
| `fromPairs(arr)` | Key-value pairs to map | `fromPairs([["a",1]])` → `{"a": 1}` |

**Examples:**
```bash
# Parse string fields to numbers
ssql update -set-expr age_num 'int(age_str)'

# Format numbers as strings with calculations
ssql update -set-expr label 'string(round(value * 100)) + "%"'

# Encode/decode
ssql update -set-expr encoded 'toBase64(email)'
ssql update -set-expr payload 'toJSON({"name": name, "age": age})'
```

### Date Functions

| Function | Description | Example |
|----------|-------------|---------|
| `now()` | Current date/time | `now()` → `2026-02-25T10:30:00+11:00` |
| `date(str)` | Parse date string | `date("2026-01-15")` |
| `duration(str)` | Parse duration (ns, us, ms, s, m, h) | `duration("1h30m")` → `1h30m0s` |
| `timezone(str)` | Get timezone location | `timezone("America/New_York")` |

**Examples:**
```bash
# Add timestamp
ssql update -set-expr timestamp 'string(now())'

# Filter by date
ssql where -where-expr 'date(created) > date("2026-01-01")'
```

### Map Functions

| Function | Description | Example |
|----------|-------------|---------|
| `keys(map)` | Get all keys | `keys({"a":1, "b":2})` → `["a", "b"]` |
| `values(map)` | Get all values | `values({"a":1, "b":2})` → `[1, 2]` |
| `get(v, key)` | Safe access (nil if missing) | `get({"a":1}, "z")` → `nil` |

### Bitwise Functions

| Function | Description | Example |
|----------|-------------|---------|
| `bitand(a, b)` | Bitwise AND | `bitand(0xFF, 0x0F)` → `15` |
| `bitor(a, b)` | Bitwise OR | `bitor(0xF0, 0x0F)` → `255` |
| `bitxor(a, b)` | Bitwise XOR | `bitxor(0xFF, 0x0F)` → `240` |
| `bitnand(a, b)` | Bitwise AND NOT | `bitnand(0xFF, 0x0F)` → `240` |
| `bitnot(a)` | Bitwise NOT | `bitnot(0)` → `-1` |
| `bitshl(a, n)` | Left shift | `bitshl(1, 4)` → `16` |
| `bitshr(a, n)` | Right shift | `bitshr(16, 4)` → `1` |
| `bitushr(a, n)` | Unsigned right shift | |

**Examples:**
```bash
# Check permission flags
ssql where -where-expr 'bitand(permissions, 4) != 0'

# Mask bits
ssql update -set-expr masked 'bitand(flags, 0x0F)'
```

## Helper Functions (ssql-specific)

ssql provides additional helper functions for safe field access:

| Function | Description | Example |
|----------|-------------|---------|
| `has(field)` | Check if field exists | `has("email")` → `true/false` |
| `getOr(field, default)` | Get field value or default | `getOr("age", 0)` → field value or `0` |

**Why use these?**
- Prevents errors when fields are missing or sparse
- Enables expressions to work gracefully with incomplete data
- Provides sensible defaults for missing values

**Examples:**
```bash
# Only process records with email
ssql where -where-expr 'has("email") and contains(email, "@")'

# Use default values for missing fields
ssql update -set-expr total 'getOr("price", 0) * getOr("qty", 1)'

# Conditional based on optional field
ssql update -set-expr status 'has("verified") ? "active" : "pending"'
```

## Common Patterns

### 1. Data Validation

**Check email format:**
```bash
ssql where -where-expr 'has("email") and contains(email, "@") and contains(email, ".")'
```

**Validate required fields:**
```bash
ssql where -where-expr 'has("name") and has("email") and has("age")'
```

**Check value ranges:**
```bash
ssql where -where-expr 'age >= 0 and age <= 120 and salary >= 0'
```

### 2. Data Cleaning

**Normalize strings:**
```bash
ssql update -set-expr email 'lower(trim(email))'
ssql update -set-expr name 'trim(name)'
ssql update -set-expr code 'upper(trim(code))'
```

**Provide defaults for missing values:**
```bash
ssql update -set-expr status 'getOr("status", "pending")'
ssql update -set-expr qty 'getOr("qty", 0)'
```

**Remove invalid data:**
```bash
ssql where -where-expr 'getOr("price", 0) > 0 and getOr("qty", 0) > 0'
```

### 3. Calculations

**Simple arithmetic:**
```bash
ssql update -set-expr total 'price * qty'
ssql update -set-expr profit 'revenue - cost'
ssql update -set-expr avg 'total / count'
```

**Conditional calculations:**
```bash
ssql update -set-expr discount 'total > 1000 ? total * 0.1 : 0'
ssql update -set-expr shipping 'weight > 10 ? 15.00 : 5.00'
```

**Complex formulas:**
```bash
ssql update -set-expr final_price 'round((price * qty) * (1 - discount / 100))'
ssql update -set-expr bmi 'round(weight / (height ** 2))'
```

### 4. Complex Filters

**Multiple conditions (AND):**
```bash
ssql where -where-expr 'age >= 18 and age <= 65 and status == "active"'
```

**Multiple conditions (OR):**
```bash
ssql where -where-expr 'dept == "Sales" or dept == "Marketing" or dept == "Support"'
```

**Nested conditions:**
```bash
ssql where -where-expr '(age >= 18 and status == "active") or role == "admin"'
```

**Pattern matching:**
```bash
ssql where -where-expr 'startsWith(email, "admin@") or endsWith(email, "@company.com")'
```

### 5. String Manipulation

**Create composite fields:**
```bash
ssql update -set-expr full_name 'first + " " + last'
ssql update -set-expr address 'street + ", " + city + ", " + state'
```

**Extract parts:**
```bash
ssql update -set-expr domain 'split(email, "@")[1]'
ssql update -set-expr first_char 'upper(name)[0:1]'
```

**Format data:**
```bash
ssql update -set-expr display 'upper(first) + " " + upper(last[0:1]) + "."'
```

### 6. Categorization

**Age categories:**
```bash
ssql update -set-expr category 'age < 18 ? "minor" : (age < 65 ? "adult" : "senior")'
```

**Performance tiers:**
```bash
ssql update -set-expr tier 'score >= 90 ? "A" : (score >= 80 ? "B" : (score >= 70 ? "C" : "F"))'
```

**Revenue brackets:**
```bash
ssql update -set-expr level 'revenue > 10000 ? "gold" : (revenue > 5000 ? "silver" : "bronze")'
```

### 7. Boolean Logic

**Complex business rules:**
```bash
# Eligible if: (age >= 18 AND has email) OR is admin
ssql where -where-expr '(age >= 18 and has("email")) or role == "admin"'

# Active customer: has recent order AND good standing
ssql where -where-expr 'days_since_order <= 90 and balance >= 0 and status != "suspended"'
```

### 8. Null/Missing Field Handling

**Safe navigation:**
```bash
ssql update -set-expr total 'getOr("price", 0) * getOr("qty", 1) + getOr("tax", 0)'
```

**Conditional on field existence:**
```bash
ssql where -where-expr 'has("premium") and premium == true'
ssql update -set-expr type 'has("premium") ? "premium" : "standard"'
```

### 9. Pipeline Composition

**Combine with other ssql commands:**
```bash
# Read, filter with expression, calculate fields, filter again
ssql from sales.csv | \
  ssql where -where-expr 'region == "West" and year == 2024' | \
  ssql update -set-expr commission 'sales * 0.05' | \
  ssql where -where-expr 'commission > 1000' | \
  ssql to csv high_performers.csv
```

### 10. Code Generation

**Generate optimized Go code from expressions:**
```bash
# Set environment variable for code generation
export SSQLGO=1

# Build pipeline with expressions
ssql from data.csv | \
  ssql where -where-expr 'price * qty > 1000' | \
  ssql update -set-expr total 'price * qty' | \
  ssql update -set-expr tier 'total > 5000 ? "premium" : "standard"' | \
  ssql generate-go > program.go

# Compile and run (10-100x faster than CLI)
go run program.go
```

**Generated code features:**
- Expressions pre-compiled at package init (~100µs one-time cost)
- Zero compilation overhead at runtime
- Clean, readable Go code
- Full type safety

## Performance

**Expression Compilation:**
- **Compile-time:** ~100 microseconds per expression (one-time cost)
- **Runtime:** ~1-2 microseconds per evaluation
- **Total overhead:** <1 millisecond for typical pipelines (1M records)

**Optimization Strategy:**
- Expressions are compiled **once** at startup
- Reusable evaluation function called for each record
- Minimal memory allocation per evaluation

**Code Generation Performance:**
- Expressions pre-compiled at package init time
- Zero compilation overhead during execution
- Typically **10-100x faster** than CLI for large datasets

**Best Practices:**
1. ✅ Use expressions for complex logic (vs. multiple commands)
2. ✅ Pre-filter with simple `-where` before expensive expressions
3. ✅ Use code generation for production workloads
4. ✅ Profile with `SSQLGO=1` to generate optimized programs

## Examples by Use Case

### Financial Calculations

```bash
# Calculate tax and total
ssql update -set-expr tax 'subtotal * 0.08' | \
  ssql update -set-expr total 'subtotal + tax'

# Apply tiered discount
ssql update -set-expr discount 'amount > 1000 ? 0.15 : (amount > 500 ? 0.10 : 0.05)'

# Calculate profit margin
ssql update -set-expr margin '((revenue - cost) / revenue) * 100'
```

### User Segmentation

```bash
# Active users with recent activity
ssql where -where-expr 'status == "active" and days_since_login <= 30'

# VIP customers
ssql where -where-expr 'total_purchases > 10000 or subscription == "premium"'

# At-risk customers
ssql where -where-expr 'days_since_purchase > 90 and lifetime_value > 1000'
```

### Data Quality

```bash
# Valid email addresses
ssql where -where-expr 'has("email") and contains(email, "@") and len(email) > 5'

# Complete profiles
ssql where -where-expr 'has("name") and has("email") and has("phone") and has("address")'

# Reasonable values
ssql where -where-expr 'age >= 0 and age <= 120 and salary >= 0 and salary <= 10000000'
```

### Text Processing

```bash
# Standardize names
ssql update -set-expr name 'upper(trim(name))'

# Extract initials
ssql update -set-expr initials 'upper(first[0:1]) + upper(last[0:1])'

# Create slugs
ssql update -set-expr slug 'lower(join(split(trim(title), " "), "-"))'
```

## Expression Syntax Reference

### Precedence (highest to lowest)

1. Function calls: `upper(name)`, `round(value)`
2. Member access: `obj.field`, `arr[0]`
3. Unary: `not`, `-`
4. Power: `**`
5. Multiplication: `*`, `/`, `%`
6. Addition: `+`, `-`
7. Comparison: `<`, `>`, `<=`, `>=`
8. Equality: `==`, `!=`
9. Logical AND: `and`
10. Logical OR: `or`
11. Ternary: `? :`
12. Pipe: `|`

### Literals

```bash
# Numbers
42              # Integer
3.14            # Float
1.5e6           # Scientific notation

# Strings
"hello"         # Double quotes
'world'         # Single quotes
"it's \"ok\""   # Escaped quotes

# Booleans
true
false

# Arrays
[1, 2, 3]
["a", "b", "c"]
[1, "mixed", true]  # Mixed types

# Nil
nil
```

### Comments

Expressions do **not** support comments. Keep expressions concise and use command descriptions for documentation.

## Integration with ssql Commands

### update command

**Syntax:** `ssql update -set-expr <field> '<expression>'`

**Features:**
- Set multiple fields with multiple `-set-expr` flags
- Combine with `-set` for literal values
- Use `-where` for conditional updates (first-where-wins)
- Clause separators: `+` (OR), `-` (exclusive OR)

**Examples:**
```bash
# Single expression
ssql update -set-expr total 'price * qty'

# Multiple expressions
ssql update -set-expr total 'price * qty' -set-expr tax 'total * 0.08'

# Conditional with expression
ssql update -where dept eq Sales -set-expr commission 'revenue * 0.05'

# If-else logic with clauses
ssql update \
  -where age lt 18 -set-expr category 'minor' + \
  -where age ge 18 -set-expr category 'adult'
```

### where command

**Syntax:** `ssql where -where-expr '<boolean-expression>'`

**Features:**
- Expression must return boolean value
- Combine with `-where` conditions using OR logic
- Multiple `-expr` within clause use AND logic
- Clause separators: `+` (OR)

**Examples:**
```bash
# Single expression filter
ssql where -where-expr 'price * qty > 1000'

# Multiple expressions (AND within clause)
ssql where -where-expr 'age >= 18' -expr 'status == "active"'

# Multiple clauses (OR between clauses)
ssql where -where-expr 'dept == "Sales"' + -expr 'dept == "Marketing"'

# Combine with -where
ssql where -where verified eq true -expr 'age >= 18'
```

## Error Handling

**Compilation Errors:**
- Detected at startup before processing records
- Clear error messages with expression location
- CLI exits with error code

```bash
$ ssql where -where-expr 'age >'
Error: compiling expression "age >": unexpected end of expression
```

**Runtime Errors:**
- Logged to stderr, processing continues
- Failed expressions result in default/empty values
- Record marked with empty field value

**Type Errors:**
- `where -expr` requires boolean result (enforced at compile-time)
- Type mismatches in operations logged at runtime

**Best Practices:**
1. ✅ Test expressions on small datasets first
2. ✅ Use `has()` and `getOr()` for optional fields
3. ✅ Check error output with `2>&1 | grep Error`
4. ✅ Use `jq` to inspect intermediate results

## Further Reading

**expr-lang Documentation:**
- Official docs: https://expr-lang.org/docs/language-definition
- GitHub: https://github.com/expr-lang/expr

**ssql Documentation:**
- Getting Started: [doc/codelab-intro.md](codelab-intro.md)
- CLI Tutorial: [doc/cli-codelab.md](cli-codelab.md)
- API Reference: [doc/api-reference.md](api-reference.md)
- Debugging Pipelines: [doc/cli-debugging.md](cli-debugging.md)

**Implementation Details:**
- Expression Integration: [doc/research/expr-integration.md](research/expr-integration.md)
- Implementation Plan: [doc/research/expr-implementation-plan.md](research/expr-implementation-plan.md)
- Design Decisions: [doc/research/expression-evaluation-design.md](research/expression-evaluation-design.md)

---

**Ready to use expressions?** Start with simple examples and build up to complex pipelines. Use `ssql update -help` and `ssql where -help` for quick reference.

*Powered by [expr-lang](https://expr-lang.org/) - Fast, safe, and expressive* ✨
