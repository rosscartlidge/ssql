# Expr-lang Reference for ssql

This document is a comprehensive reference for the expr-lang expression language (v1.17) as used in ssql. It covers all language features, built-in functions, and ssql-specific usage patterns.

**Source**: https://expr-lang.org/

## Table of Contents

1. [Overview](#overview)
2. [Literals](#literals)
3. [Operators](#operators)
4. [Variables](#variables)
5. [Predicates](#predicates)
6. [Built-in Functions](#built-in-functions)
7. [Type System](#type-system)
8. [Compile-Time Type Checking](#compile-time-type-checking)
9. [ssql Integration](#ssql-integration)
10. [Common Patterns](#common-patterns)
11. [Future Improvements](#future-improvements)

---

## Overview

Expr is a simple, safe expression language designed for Go. Key features:

- **Safe execution** - No loops, no recursion, guaranteed termination
- **Type-safe** - Compile-time type checking available
- **Fast** - Compiles to bytecode, executes efficiently
- **Familiar syntax** - JavaScript-like with some Go influences

---

## Literals

| Type | Examples |
|------|----------|
| Boolean | `true`, `false` |
| Integer | `42`, `0x2A` (hex), `0o52` (octal), `0b101010` (binary) |
| Float | `0.5`, `.5` |
| String | `"foo"`, `'bar'`, `` `raw string` `` |
| Array | `[1, 2, 3]` |
| Map | `{a: 1, b: 2, c: 3}` |
| Nil | `nil` |
| Comment | `/* block */` or `// line` |

### Strings

```expr
"Hello\nWorld"           // Escape sequences supported
'single quotes work'     // Same as double quotes
`raw string
with newlines`           // No escape sequences
```

---

## Operators

### Arithmetic
| Operator | Description |
|----------|-------------|
| `+` | Addition |
| `-` | Subtraction |
| `*` | Multiplication |
| `/` | Division |
| `%` | Modulus |
| `^` or `**` | Exponent |

### Comparison
| Operator | Description |
|----------|-------------|
| `==` | Equal |
| `!=` | Not equal |
| `<` | Less than |
| `>` | Greater than |
| `<=` | Less than or equal |
| `>=` | Greater than or equal |

### Logical
| Operator | Description |
|----------|-------------|
| `not` or `!` | Logical NOT |
| `and` or `&&` | Logical AND |
| `or` or `\|\|` | Logical OR |

### Conditional
| Operator | Description |
|----------|-------------|
| `?:` | Ternary: `condition ? then : else` |
| `??` | Nil coalescing: `value ?? default` |

### Membership & Access
| Operator | Description |
|----------|-------------|
| `.` | Field access: `user.Name` |
| `[]` | Index access: `user["Name"]`, `array[0]` |
| `?.` | Optional chaining: `user?.Name` (returns nil if user is nil) |
| `in` | Membership test: `"x" in array`, `"key" in map` |

### String
| Operator | Description |
|----------|-------------|
| `+` | Concatenation |
| `contains` | `str contains "sub"` |
| `startsWith` | `str startsWith "prefix"` |
| `endsWith` | `str endsWith "suffix"` |
| `matches` | Regex: `str matches "pattern"` |

### Other
| Operator | Description |
|----------|-------------|
| `..` | Range: `1..5` creates `[1,2,3,4,5]` |
| `[:]` | Slice: `array[1:3]`, `array[:3]`, `array[2:]` |
| `\|` | Pipe: `x \| fn()` equivalent to `fn(x)` |

### Optional Chaining

```expr
author.User?.Name                    // nil if User is nil
author.User?.Name ?? "Anonymous"     // default if nil
```

### Slice Operator

```expr
array[1:4]   // [2, 3, 4] (indices 1, 2, 3)
array[1:-1]  // [2, 3, 4] (1 to second-to-last)
array[:3]    // [1, 2, 3] (first 3)
array[3:]    // [4, 5] (from index 3)
array[:]     // copy of array
```

### Pipe Operator

```expr
user.Name | lower() | split(" ")
// Equivalent to:
split(lower(user.Name), " ")
```

---

## Variables

### Declaration

```expr
let x = 42; x * 2                    // 84

let x = 42;
let y = 2;
x * y                                // 84

let name = user.Name | lower() | split(" ");
"Hello, " + name[0] + "!"
```

### $env Variable

Access the entire environment as a map:

```expr
foo.Name == $env["foo"].Name         // true
$env["var with spaces"]              // access vars with special chars
'foo' in $env                        // check if variable exists
```

---

## Predicates

Predicates are expressions used in array functions like `filter`, `all`, `any`, `map`, etc.

### Special Variables in Predicates
| Variable | Description |
|----------|-------------|
| `#` | Current element |
| `#acc` | Accumulator (in `reduce`) |
| `#index` | Current index |

### Examples

```expr
filter(0..9, {# % 2 == 0})           // [0, 2, 4, 6, 8]

// For maps/structs, # can be omitted:
filter(tweets, {len(.Content) > 240})

// Braces can be omitted:
filter(tweets, len(.Content) > 240)

// Nested predicates - use let to capture outer variable:
filter(posts, {
    let post = #;
    any(.Comments, .Author == post.Author)
})
```

---

## Built-in Functions

### String Functions

| Function | Description | Example |
|----------|-------------|---------|
| `trim(str[, chars])` | Remove whitespace/chars from ends | `trim(" Hello ") == "Hello"` |
| `trimPrefix(str, prefix)` | Remove prefix | `trimPrefix("HelloWorld", "Hello") == "World"` |
| `trimSuffix(str, suffix)` | Remove suffix | `trimSuffix("HelloWorld", "World") == "Hello"` |
| `upper(str)` | Convert to uppercase | `upper("hello") == "HELLO"` |
| `lower(str)` | Convert to lowercase | `lower("HELLO") == "hello"` |
| `split(str, delim[, n])` | Split string | `split("a,b,c", ",") == ["a","b","c"]` |
| `splitAfter(str, delim[, n])` | Split after delimiter | `splitAfter("a,b,c", ",") == ["a,","b,","c"]` |
| `replace(str, old, new)` | Replace all occurrences | `replace("foo", "o", "0") == "f00"` |
| `repeat(str, n)` | Repeat string | `repeat("ab", 3) == "ababab"` |
| `indexOf(str, sub)` | First index of substring (-1 if not found) | `indexOf("apple", "p") == 1` |
| `lastIndexOf(str, sub)` | Last index of substring | `lastIndexOf("apple", "p") == 2` |
| `hasPrefix(str, prefix)` | Check prefix | `hasPrefix("Hello", "He") == true` |
| `hasSuffix(str, suffix)` | Check suffix | `hasSuffix("Hello", "lo") == true` |

### Date Functions

| Function | Description | Example |
|----------|-------------|---------|
| `now()` | Current time | `now().Year() == 2024` |
| `duration(str)` | Parse duration | `duration("1h").Seconds() == 3600` |
| `date(str[, fmt[, tz]])` | Parse date | `date("2023-08-14")` |
| `timezone(str)` | Get timezone | `timezone("UTC")` |

**Valid duration units**: `"ns"`, `"us"` (or `"µs"`), `"ms"`, `"s"`, `"m"`, `"h"`

**Date formats** (when format arg omitted):
- `2006-01-02`
- `15:04:05`
- `2006-01-02 15:04:05`
- RFC3339, RFC822, RFC850, RFC1123

**Date methods**: `.Year()`, `.Month()`, `.Day()`, `.Hour()`, `.Minute()`, `.Second()`, `.Weekday()`, `.YearDay()`, `.In(tz)`

```expr
// Date arithmetic
createdAt - now()                    // duration between dates
createdAt + duration("1h")           // add duration
createdAt > now() - duration("1h")   // compare dates

// Timezone conversion
date("2023-08-14 00:00:00").In(timezone("Europe/Zurich"))
```

### Number Functions

| Function | Description | Example |
|----------|-------------|---------|
| `max(n1, n2)` | Maximum | `max(5, 7) == 7` |
| `min(n1, n2)` | Minimum | `min(5, 7) == 5` |
| `abs(n)` | Absolute value | `abs(-5) == 5` |
| `ceil(n)` | Round up | `ceil(1.5) == 2.0` |
| `floor(n)` | Round down | `floor(1.5) == 1.0` |
| `round(n)` | Round to nearest | `round(1.5) == 2.0` |

### Array Functions

| Function | Description | Example |
|----------|-------------|---------|
| `all(arr, pred)` | All match predicate | `all(nums, # > 0)` |
| `any(arr, pred)` | Any match predicate | `any(nums, # > 10)` |
| `one(arr, pred)` | Exactly one matches | `one(users, .Admin)` |
| `none(arr, pred)` | None match predicate | `none(nums, # < 0)` |
| `map(arr, pred)` | Transform elements | `map(users, .Name)` |
| `filter(arr, pred)` | Filter elements | `filter(nums, # > 5)` |
| `find(arr, pred)` | First matching element | `find(nums, # > 5)` |
| `findIndex(arr, pred)` | Index of first match | `findIndex(nums, # > 5)` |
| `findLast(arr, pred)` | Last matching element | `findLast(nums, # > 5)` |
| `findLastIndex(arr, pred)` | Index of last match | `findLastIndex(nums, # > 5)` |
| `groupBy(arr, pred)` | Group by predicate result | `groupBy(users, .Age)` |
| `count(arr[, pred])` | Count matching elements | `count(users, .Active)` |
| `concat(arr1, arr2, ...)` | Concatenate arrays | `concat([1,2], [3,4])` |
| `flatten(arr)` | Flatten nested arrays | `flatten([[1,2], [3,4]])` |
| `uniq(arr)` | Remove duplicates | `uniq([1,2,2,3]) == [1,2,3]` |
| `join(arr[, delim])` | Join to string | `join(["a","b"], ",") == "a,b"` |
| `reduce(arr, pred[, init])` | Reduce to single value | `reduce(1..5, #acc + #, 0)` |
| `sum(arr[, pred])` | Sum of elements | `sum([1,2,3]) == 6` |
| `mean(arr)` | Average | `mean([1,2,3]) == 2.0` |
| `median(arr)` | Median | `median([1,2,3]) == 2.0` |
| `first(arr)` | First element (or nil) | `first([1,2,3]) == 1` |
| `last(arr)` | Last element (or nil) | `last([1,2,3]) == 3` |
| `take(arr, n)` | First n elements | `take([1,2,3,4], 2) == [1,2]` |
| `reverse(arr)` | Reverse array | `reverse([1,2,3]) == [3,2,1]` |
| `sort(arr[, order])` | Sort array | `sort([3,1,2]) == [1,2,3]` |
| `sortBy(arr, pred[, order])` | Sort by predicate | `sortBy(users, .Age, "desc")` |

**Sort order**: `"asc"` (default) or `"desc"`

### Map Functions

| Function | Description | Example |
|----------|-------------|---------|
| `keys(map)` | Get all keys | `keys({a:1, b:2}) == ["a","b"]` |
| `values(map)` | Get all values | `values({a:1, b:2}) == [1, 2]` |

### Type Conversion Functions

| Function | Description | Example |
|----------|-------------|---------|
| `type(v)` | Get type name | `type(42) == "int"` |
| `int(v)` | Convert to int | `int("123") == 123` |
| `float(v)` | Convert to float | `float("1.5") == 1.5` |
| `string(v)` | Convert to string | `string(123) == "123"` |
| `toJSON(v)` | Convert to JSON string | `toJSON({a:1})` |
| `fromJSON(str)` | Parse JSON string | `fromJSON('{"a":1}')` |
| `toBase64(str)` | Encode to Base64 | `toBase64("Hello")` |
| `fromBase64(str)` | Decode from Base64 | `fromBase64("SGVsbG8=")` |
| `toPairs(map)` | Map to key-value pairs | `toPairs({a:1}) == [["a",1]]` |
| `fromPairs(arr)` | Key-value pairs to map | `fromPairs([["a",1]]) == {a:1}` |

**Type names returned by `type()`**: `"nil"`, `"bool"`, `"int"`, `"uint"`, `"float"`, `"string"`, `"array"`, `"map"`, or struct name

### Miscellaneous Functions

| Function | Description | Example |
|----------|-------------|---------|
| `len(v)` | Length of array/map/string | `len([1,2,3]) == 3` |
| `get(v, key)` | Safe get (returns nil if missing) | `get(arr, 10)` |

### Bitwise Functions

| Function | Description | Example |
|----------|-------------|---------|
| `bitand(a, b)` | Bitwise AND | `bitand(0b1010, 0b1100) == 0b1000` |
| `bitor(a, b)` | Bitwise OR | `bitor(0b1010, 0b1100) == 0b1110` |
| `bitxor(a, b)` | Bitwise XOR | `bitxor(0b1010, 0b1100) == 0b0110` |
| `bitnand(a, b)` | Bitwise AND NOT | `bitnand(0b1010, 0b1100) == 0b0010` |
| `bitnot(a)` | Bitwise NOT | `bitnot(0b1010)` |
| `bitshl(a, n)` | Left shift | `bitshl(0b101, 2) == 0b10100` |
| `bitshr(a, n)` | Right shift | `bitshr(0b101, 1) == 0b10` |
| `bitushr(a, n)` | Unsigned right shift | `bitushr(-5, 2)` |

---

## Type System

Expr has the following types:
- `nil` - null value
- `bool` - boolean
- `int` - integer (Go's int)
- `uint` - unsigned integer
- `float` - floating point (Go's float64)
- `string` - string
- `array` - slice/array (typically `[]any`)
- `map` - map (typically `map[string]any`)
- Named types and structs retain their type name

---

## Compile-Time Type Checking

Expr provides compile-time type checking options for the Go API:

### Return Type Enforcement

```go
// Expect boolean return
program, err := expr.Compile(code, expr.AsBool())

// Expect int return (float64, uint, int32 cast to int)
program, err := expr.Compile(code, expr.AsInt())

// Expect int64 return
program, err := expr.Compile(code, expr.AsInt64())

// Expect float64 return
program, err := expr.Compile(code, expr.AsFloat64())

// Accept any return type (default)
program, err := expr.Compile(code, expr.AsAny())

// Expect specific reflect.Kind
program, err := expr.Compile(code, expr.AsKind(reflect.String))
```

### Warn on Any

```go
// Warn if return type is 'any' (catches untyped expressions)
program, err := expr.Compile(code, expr.AsInt(), expr.WarnOnAny())
```

**Example problem:**
```expr
let arr = [1, 2, 3]; arr[0]  // Returns 'any' because arrays are []any
```

**Fix:**
```expr
let arr = [1, 2, 3]; int(arr[0])  // Explicitly convert to int
```

### Context Passing

```go
// Automatically pass context to functions that accept it
env := map[string]any{
    "ctx": context.Background(),
    "fetch": func(ctx context.Context, url string) string { ... },
}
program, err := expr.Compile(code, expr.Env(env), expr.WithContext("ctx"))

// Expression: fetch("http://...")
// Becomes:    fetch(ctx, "http://...")
```

### Constant Expression Evaluation

```go
// Evaluate at compile time if all args are constants
env := map[string]any{"fib": fib}
program, err := expr.Compile(`fib(10)`, expr.Env(env), expr.ConstExpr("fib"))

// fib(10)    -> 55 (computed at compile time)
// fib(12+12) -> 267914296 (computed at compile time)
// fib(x)     -> evaluated at runtime
```

### Timezone Configuration

```go
program, err := expr.Compile(code, expr.Timezone(time.UTC))
```

---

## ssql Integration

### Current Usage

ssql uses expr-lang in two contexts:

1. **`-if-expr`** - Boolean expressions for filtering records
2. **`-set-expr`** - Value expressions for updating fields

### How Records Are Accessed

In ssql expressions, the entire record is the environment:

```bash
# Direct field access
ssql where -if-expr 'age > 18 and status == "active"'

# Field names with spaces or special chars
ssql where -if-expr '$env["field name"] > 0'
```

### Current Implementation

```go
// In update.go and where.go
env := make(map[string]any)
for k, v := range record.All() {
    env[k] = v
}

// For -if-expr (boolean required)
program, err := expr.Compile(exprStr, expr.Env(env), expr.AsBool())

// For -set-expr (any type)
program, err := expr.Compile(exprStr, expr.Env(env))
```

### Example ssql Commands

```bash
# Filter with expression
ssql from data.csv | ssql where -if-expr 'age > 18 and verified == true'

# Complex conditions
ssql from data.csv | ssql where -if-expr 'status in ["active", "pending"]'

# Update with expression
ssql from data.csv | ssql update -set-expr discount 'price * 0.1'

# Conditional update
ssql from data.csv | ssql update -if status eq vip -set-expr discount 'price * 0.2'

# String manipulation
ssql from data.csv | ssql update -set-expr email 'lower(email)'

# Date calculations
ssql from data.csv | ssql where -if-expr 'date(created_at) > now() - duration("24h")'
```

---

## Common Patterns

### Conditional Values

```expr
status == "premium" ? price * 0.8 : price

// Nested ternary
age < 18 ? "minor" : age < 65 ? "adult" : "senior"

// Nil handling
user?.discount ?? 0
```

### String Operations

```expr
// Normalize email
lower(trim(email))

// Extract domain
split(email, "@")[1]

// Format name
upper(name[:1]) + lower(name[1:])

// Check format
email matches `^[\w.]+@[\w.]+$`
```

### Numeric Calculations

```expr
// Percentage
(completed / total) * 100

// Discount tiers
quantity >= 100 ? price * 0.7 :
quantity >= 50  ? price * 0.8 :
quantity >= 10  ? price * 0.9 : price

// Round to 2 decimal places
round(price * 100) / 100
```

### Array Operations

```expr
// Check if any tag matches
any(tags, # in ["urgent", "critical"])

// Sum specific values
sum(items, .price * .quantity)

// Get unique categories
uniq(map(products, .category))

// Filter and transform
map(filter(users, .active), .email) | join(", ")
```

### Date Operations

```expr
// Age in years (approximate)
(now() - date(birthdate)).Hours() / 8760

// Is recent (within 7 days)
date(created_at) > now() - duration("168h")

// Format check
date(timestamp).Year() == 2024
```

---

## Future Improvements

### 1. Type-Safe Expression Updates

**Problem**: `-set-expr` can change field types, causing inconsistencies.

**Solution**: Use compile-time type checking based on existing field type:

```go
// If field exists, enforce its type
fieldType := getFieldType(record, fieldName)
var opts []expr.Option
switch fieldType {
case "int64":
    opts = append(opts, expr.AsInt64())
case "float64":
    opts = append(opts, expr.AsFloat64())
case "string":
    // No direct enforcement, convert result to string
case "bool":
    opts = append(opts, expr.AsBool())
}
program, err := expr.Compile(exprStr, opts...)
```

**Benefits**:
- Compile-time error: "expression returns string, but field 'age' is int64"
- Prevents type drift in pipelines
- Better error messages

### 2. Custom Helper Functions

Add ssql-specific functions to the expression environment:

```go
env["coalesce"] = func(vals ...any) any {
    for _, v := range vals {
        if v != nil { return v }
    }
    return nil
}

env["nvl"] = func(val, def any) any {
    if val == nil { return def }
    return val
}

env["iif"] = func(cond bool, t, f any) any {
    if cond { return t }
    return f
}
```

Usage:
```bash
ssql update -set-expr discount 'coalesce(special_discount, default_discount, 0)'
```

### 3. Field Type Introspection

Expose field types in expressions:

```go
env["fieldType"] = func(name string) string {
    // Return type of field in current record
}
```

Usage:
```expr
fieldType("age") == "int64"
```

### 4. Warn on Any for All Expressions

Enable strict typing mode:

```bash
ssql update -set-expr -strict total 'price * quantity'
```

Would fail if expression returns `any` instead of typed value.

### 5. Expression Validation Command

Add a command to validate expressions without running:

```bash
ssql expr-check -if-expr 'age > 18 and status == "active"' -sample data.csv
# Output: Valid boolean expression
# Fields used: age (int64), status (string)
```

### 6. Documentation in CLI

Add expression help to ssql:

```bash
ssql expr-help              # Show all functions
ssql expr-help string       # Show string functions
ssql expr-help examples     # Show common patterns
```

---

## Quick Reference Card

### Operators
```
Arithmetic: + - * / % ^ **
Comparison: == != < > <= >=
Logical:    and or not && || !
Ternary:    condition ? then : else
Nil:        value ?? default
Access:     obj.field  obj["field"]  obj?.field
Membership: item in collection
String:     contains startsWith endsWith matches
Range:      1..10
Slice:      arr[1:3]
Pipe:       value | function()
```

### Most Used Functions
```
String:  lower upper trim split replace
Number:  min max abs round ceil floor
Array:   filter map any all first last len sort sum
Type:    int float string type
Date:    now date duration
```

### Expression Examples
```expr
// Filter: boolean expressions
age > 18 and verified == true
status in ["active", "pending"]
name startsWith "A" or name startsWith "B"
email matches `.*@company\.com$`

// Update: value expressions
price * 0.9                           // 10% discount
lower(trim(email))                    // normalize
status == "vip" ? price * 0.8 : price // conditional
date(created_at).Year()               // extract year
```
