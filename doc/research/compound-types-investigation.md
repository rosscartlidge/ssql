# Compound Types in ssql: Investigation and Opportunities

## Current State

ssql is fundamentally scalar-focused. Records store fields as `int64`, `float64`, `string`, `bool`, or `time.Time`. When JSON input contains arrays or nested objects, they are stored as `JSONString` — an opaque raw JSON string, not a parsed Go data structure.

### The JSONString Problem

When reading `{"tags":["a","b","c"]}` from JSONL, the `tags` field becomes a `JSONString` with value `["a","b","c"]`. This has consequences:

```bash
# BROKEN: len() counts string characters, not array elements
echo '{"tags":["a","b","c"]}' | ssql update -set-expr n 'len(tags)'
# Result: n=13 (string length of '["a","b","c"]'), NOT 3

# BROKEN: indexing gets character codes, not elements
echo '{"scores":[10,20,30]}' | ssql update -set-expr first 'scores[0]'
# Result: first=91 (ASCII of '['), NOT 10

# BROKEN: dot access fails on nested objects
echo '{"addr":{"city":"NYC"}}' | ssql update -set-expr city 'addr.city'
# Error: invalid operation: int(string)

# WORKS: split() creates a real array at runtime
echo '{"tags":"a,b,c"}' | ssql update -set-expr n 'len(split(tags, ","))'
# Result: n=3 (correct)
```

The expr-lang library has full array support (30+ functions: `len`, `filter`, `map`, `sort`, `reduce`, `first`, `last`, `find`, `flatten`, `uniq`, `concat`, etc.) but these only work when the value is an actual Go `[]any`, not a `JSONString`.

### Where Arrays Exist Today

| Context | Type | Works with expr? | Notes |
|---------|------|-----------------|-------|
| `Collect()` aggregation output | `[]any` | Yes (in-process only) | Serializes to JSON array in JSONL pipeline |
| JSON array from input | `JSONString` | No | Stored as raw string, not parsed |
| JSON object from input | `JSONString` | No | Same issue |
| `split(str, sep)` in expr | `[]any` | Yes | Creates real array at runtime |
| `CollectSeq[T]()` | `iter.Seq[any]` | No | Not exposed to expressions |

### The Pipeline Boundary Problem

Even when `Collect()` produces a real `[]any`, piping through JSONL loses the type:

```bash
# Step 1: Collect creates []any in memory — correct
ssql from data.csv | ssql group-by dept -collect name names
# Output JSONL: {"dept":"eng","names":["Alice","Bob"]}

# Step 2: Next command reads it back as JSONString — broken
... | ssql update -set-expr n 'len(names)'
# Result: n=15 (string length), not 2
```

## The Gap

1. **JSON arrays from input are inert** — stored as opaque strings, invisible to expressions
2. **Pipeline serialization loses array types** — `[]any` becomes JSONString at process boundaries
3. **Nested object fields are inaccessible** — can't navigate `address.city`
4. **No CLI commands operate on arrays** — no explode, unnest, array_length, array_agg, etc.

## What Would "First-Class Arrays" Look Like?

### Option A: Parse JSON Arrays at Read Time

When the JSONL reader encounters a JSON array value, parse it into `[]any` instead of `JSONString`.

**Pros:**
- Expression functions (`len`, `filter`, `map`, `sort`, etc.) just work
- No new commands needed for basic array operations
- `Collect()` output survives pipeline boundaries

**Cons:**
- Performance regression: parsing every nested structure adds CPU time, even when not needed
- Type ambiguity: `[]any` can contain mixed types — int64, float64, string, nested arrays
- Breaks `isSimpleValue()` assumptions — arrays can't be group-by keys
- JSON objects would need to become `map[string]any` too, which isn't in the `Value` constraint

**Implementation:**
- Modify `parseJSONValue()` in `core.go` to recursively parse arrays and objects
- Add `map[string]any` to the `Value` type constraint
- Keep `JSONString` as a fallback for values that fail parsing

### Option B: Lazy Parsing with `parseJSON()` Expression Function

Add a `parseJSON(field)` function to the expression environment that parses `JSONString` into `[]any` or `map[string]any` on demand.

```bash
# Explicit parsing when needed
echo '{"tags":["a","b","c"]}' | ssql update -set-expr n 'len(parseJSON(tags))'
# Result: n=3

echo '{"addr":{"city":"NYC"}}' | ssql update -set-expr city 'parseJSON(addr).city'
# Result: city="NYC"
```

**Pros:**
- No performance regression for records where arrays aren't used
- Explicit — user opts in to parsing
- No changes to Value constraint or isSimpleValue
- Easy to implement (single function)

**Cons:**
- Verbose — `parseJSON(tags)` instead of just `tags`
- User must know which fields are JSONString
- Doesn't help with pipeline boundary problem (`Collect` → JSONL → JSONString)

### Option C: Auto-Parse at Expression Evaluation Time

When building the expression environment from a Record, detect `JSONString` values and auto-parse them before passing to expr-lang.

```go
// In runtime.go, when building env:
for k, v := range record.All() {
    if js, ok := v.(ssql.JSONString); ok {
        parsed, err := js.Parse()
        if err == nil {
            env[k] = parsed  // []any or map[string]any
        } else {
            env[k] = string(js)  // fallback to string
        }
    } else {
        env[k] = v
    }
}
```

**Pros:**
- Expressions just work — `len(tags)`, `tags[0]`, `addr.city` all correct
- No user-facing API changes
- No new commands needed
- Only pays the parsing cost for records that go through expressions

**Cons:**
- Hidden behavior — same field is `JSONString` in Record but `[]any` in expression
- Parsing cost on every expression evaluation (could cache)
- Doesn't help non-expression contexts (table display, CSV output)

### Option D: Parse at Read Time, Only for Arrays/Objects

A hybrid: parse JSON arrays into `[]any` and JSON objects into `map[string]any` when reading JSONL, but DON'T add these to the `Value` constraint. Instead, store them via `SetAny()` or a new mechanism.

**Rejected**: The type system would need `map[string]any` in Value, which introduces a second way to represent records (nested maps vs nested Records). This conflicts with the sealed Record design.

## Recommended Approach: Option C (Auto-Parse in Expressions) + CLI Commands

### Phase 1: Auto-Parse JSONString in Expression Evaluation

Single change in `runtime.go`: when building the expression environment, parse `JSONString` values. This immediately enables all 30+ expr-lang array/object functions on JSON data:

```bash
# All of these just work:
ssql update -set-expr n 'len(tags)'                    # array length
ssql update -set-expr first 'tags[0]'                  # indexing
ssql update -set-expr city 'addr.city'                 # nested object access
ssql update -set-expr has_admin 'any(roles, {# == "admin"})'  # array predicates
ssql update -set-expr total 'reduce(scores, {# + #acc}, 0)'   # reduce
ssql where -where-expr 'len(tags) > 2'                # filter by array length
ssql update -set-expr sorted 'sort(tags)'              # sort array
ssql update -set-expr uniq_tags 'uniq(tags)'           # deduplicate
```

**What to do with the expression result**: if an expression returns `[]any` or `map[string]any`, the result should be stored as `JSONString` in the output Record (via `json.Marshal`). This keeps the Record type system intact.

### Phase 2: `explode` Command (Array → Rows)

The most impactful array CLI command: expand each element of an array field into a separate row. This is SQL's `UNNEST` / `LATERAL FLATTEN`.

```bash
# Input: {"name":"Alice","tags":["go","rust","python"]}
# Output: 3 rows, one per tag
echo '{"name":"Alice","tags":["go","rust","python"]}' | ssql explode tags
# {"name":"Alice","tags":"go"}
# {"name":"Alice","tags":"rust"}
# {"name":"Alice","tags":"python"}
```

**Library function:**
```go
func Explode(field string) Filter[Record, Record]
```

Algorithm: for each record, get the field value. If it's `JSONString`, parse it. If `[]any`, emit one record per element with the array field replaced by the element. If scalar, pass through unchanged.

This is high-value because it bridges between array-shaped data and ssql's scalar pipeline:

```bash
# Parse JSON with nested arrays, then flatten for analysis
ssql from events.jsonl | ssql explode tags | ssql group-by tags -count n | ssql sort n -desc
```

### Phase 3: Additional Array Commands (Lower Priority)

| Command | Description | Example |
|---------|-------------|---------|
| `implode FIELD` | Inverse of explode — collect values back into array | `ssql group-by id -collect tag tags` already does this |
| `array-length FIELD RESULT` | Add field with array length | `ssql update -set-expr n 'len(parseJSON(tags))'` covers this |
| `flatten FIELD` | Flatten nested arrays | Expr `flatten()` covers this |

Most array operations are already covered by expressions once Phase 1 is done. The `explode` command is the main structural operation that expressions can't replicate (it changes the number of rows).

### Phase 4: Pipeline-Aware Array Preservation (Optional)

To solve the pipeline boundary problem (Collect → JSONL → JSONString), the JSONL reader could optionally parse array values. This could be:

- A global flag: `ssql --parse-json from data.jsonl | ...`
- Schema-aware: if the schema header says a field is `json` type, parse it
- Heuristic: if a string value starts with `[` or `{`, try parsing

This is lower priority because Phase 1 (auto-parse in expressions) handles the common case of operating on arrays within a single command.

## Implementation Effort

| Phase | Scope | Files | Effort |
|-------|-------|-------|--------|
| 1: Auto-parse in expressions | 1 function change | `runtime.go` | Small (1-2 hours) |
| 2: `explode` command | New library function + CLI command | `operations.go`, `cmd/ssql/commands/explode.go`, `main.go` | Medium (half day) |
| 3: Additional array commands | Lower priority | Various | As needed |
| 4: Pipeline array preservation | Schema-aware JSONL parsing | `core.go` (JSONL reader) | Medium |

## Summary

ssql has the building blocks for compound type support (expr-lang array functions, `[]any` in Value constraint, JSONString with Parse()) but they don't connect. The main blocker is that JSON arrays are stored as `JSONString` and expressions see them as strings.

**Phase 1** (auto-parse JSONString in expressions) is the highest-value, lowest-effort fix — it unlocks 30+ array functions with a single change. **Phase 2** (`explode`) adds the one structural operation expressions can't do. Together they cover the vast majority of array use cases without changing the type system.
