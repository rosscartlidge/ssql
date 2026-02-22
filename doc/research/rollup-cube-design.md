# ROLLUP and CUBE Aggregation Design

## Problem

`group-by a_kind z_kind -count count` only produces one level of aggregation — the full (a_kind, z_kind) combination. Users often need counts at every level:

| Level | Groups by | Useful for |
|-------|-----------|------------|
| Grand total | (nothing) | Total record count |
| a_kind | a_kind only | Count per a_kind |
| z_kind | z_kind only | Count per z_kind |
| a_kind, z_kind | both | Count per combination |

With all four levels you can compute things like "what percentage of a_kind=217's records have z_kind=212?" without running multiple pipelines.

## SQL Semantics

### ROLLUP — Hierarchical subtotals

`GROUP BY ROLLUP(a, b, c)` produces **n+1 levels**, peeling fields from the right:

```
(a, b, c)     — detail groups
(a, b)        — subtotals over all c
(a)           — subtotals over all b, c
()            — grand total
```

Good for hierarchical dimensions (year → quarter → month).

### CUBE — All combinations

`GROUP BY CUBE(a, b)` produces **2^n combinations**:

```
(a, b)        — detail groups
(a)           — subtotals over all b
(b)           — subtotals over all a
()            — grand total
```

Good for cross-dimensional analysis (the heatmap use case).

### GROUPING SETS — Explicit list

`GROUP BY GROUPING SETS((a,b), (a), ())` lets you pick exactly which levels.

### The NULL problem

SQL uses NULL to mark "all values" in rollup rows. But data might already contain NULLs. SQL provides `GROUPING(field)` → 0/1 to distinguish.

## Design Options

### Option A: Separate `rollup` command

```bash
ssql from data.csv | ssql rollup a_kind z_kind -count count
```

**Output:** One record per aggregation level, with a `_level` field:

```jsonl
{"_level":0, "count":10000}
{"_level":1, "a_kind":217, "count":104}
{"_level":1, "a_kind":111, "count":1331}
...
{"_level":2, "a_kind":217, "z_kind":212, "count":104}
{"_level":2, "a_kind":111, "z_kind":114, "count":1331}
...
```

Missing group fields are omitted (not set to null), making them easy to detect with `HasField()` or by checking the `_level` value. Level 0 = grand total, level N = full detail.

**Pros:**
- Clean separation from `group-by`
- Simple mental model: "rollup gives you all levels"
- No changes to existing `group-by` behavior
- `_level` field is unambiguous — no NULL confusion

**Cons:**
- New command to learn
- Duplicates some `group-by` infrastructure

### Option B: `-rollup` / `-cube` flag on `group-by`

```bash
ssql from data.csv | ssql group-by a_kind z_kind -count count -rollup
ssql from data.csv | ssql group-by a_kind z_kind -count count -cube
```

**Pros:**
- Discoverable — users already know `group-by`
- Mirrors SQL syntax closely

**Cons:**
- Overloads `group-by` which is already complex
- Unclear what `-rollup` means for the no-aggregation case
- Makes the command harder to document

### Option C: Library-only function, compose in CLI

Add `Rollup()` and `Cube()` to the ssql package. CLI users compose with existing commands:

```bash
# Manual rollup via multiple group-by + union
(ssql from data.csv | ssql group-by -count count;
 ssql from data.csv | ssql group-by a_kind -count count;
 ssql from data.csv | ssql group-by a_kind z_kind -count count) | ssql to table
```

**Pros:**
- Unix philosophy — composable
- No new commands

**Cons:**
- Reads the input N+1 times (or requires `tee`)
- Verbose and error-prone
- Aggregation function must be repeated identically
- Can't easily add `_level` field

## Recommended Approach: Option A — `rollup` command

A dedicated `rollup` command is cleanest because:

1. **Clear semantics** — always produces all aggregation levels
2. **Single pass** — materializes records once, runs aggregations at each level
3. **No NULL ambiguity** — uses `_level` field + field omission instead of SQL's NULL sentinel
4. **Composable** — output is regular JSONL, works with `where`, `sort`, `to table`, etc.
5. **Code generation** — straightforward `ssql.Rollup()` call

### Library API

```go
// RollupConfig specifies which levels to compute
type RollupConfig struct {
    Fields       []string                      // Group-by fields in order
    Aggregations map[string]AggregateFunc      // Named aggregation functions
    Mode         RollupMode                    // Rollup, Cube, or Custom
    LevelField   string                        // Field name for level indicator (default "_level")
}

type RollupMode int
const (
    RollupHierarchical RollupMode = iota  // ROLLUP: (a,b,c), (a,b), (a), ()
    RollupCube                             // CUBE: all 2^n combinations
)

// Rollup performs hierarchical or cube aggregation in a single pass.
func Rollup(config RollupConfig) Filter[Record, Record]
```

**Implementation sketch:**

```go
func Rollup(config RollupConfig) Filter[Record, Record] {
    return func(records iter.Seq[Record]) iter.Seq[Record] {
        // 1. Materialize all records (required for multiple groupings)
        var allRecords []Record
        for r := range records {
            allRecords = append(allRecords, r)
        }

        return func(yield func(Record) bool) {
            // 2. Determine grouping sets based on mode
            sets := computeGroupingSets(config.Fields, config.Mode)
            // ROLLUP(a,b,c) → [[], [a], [a,b], [a,b,c]]
            // CUBE(a,b)     → [[], [a], [b], [a,b]]

            // 3. For each grouping set, group and aggregate
            for level, fields := range sets {
                groups := groupRecords(allRecords, fields)
                for key, members := range groups {
                    result := MakeMutableRecord()
                    result = result.Int(config.LevelField, int64(level))
                    // Set group key fields
                    for i, f := range fields {
                        result = result.SetAny(f, key[i])
                    }
                    // Apply aggregations
                    for name, aggFn := range config.Aggregations {
                        result.fields[name] = aggFn(members).getValue()
                    }
                    if !yield(result.Freeze()) {
                        return
                    }
                }
            }
        }
    }
}
```

### CLI Interface

```bash
# ROLLUP (hierarchical) — default mode
ssql from data.csv | ssql rollup a_kind z_kind -count count

# CUBE (all combinations)
ssql from data.csv | ssql rollup a_kind z_kind -count count -cube

# With multiple aggregations
ssql from data.csv | ssql rollup dept region \
    -count num \
    -sum salary total_salary \
    -avg salary avg_salary

# Filter to specific level
ssql from data.csv | ssql rollup a_kind z_kind -count count \
    | ssql where -where _level eq 1
```

**Flags** (mirrors `group-by` aggregation flags):

```
FIELDS [FIELDS ...]   - Fields to roll up (positional, variadic)
-cube                 - Use CUBE mode (all combinations) instead of ROLLUP
-count result-name    - Count aggregation
-sum field result     - Sum aggregation
-avg field result     - Average aggregation
-min field result     - Minimum
-max field result     - Maximum
-expr expr result     - Expression aggregation
-generate / -g        - Code generation mode
```

### Output Format

For `ssql rollup a_kind z_kind -count count`:

```jsonl
{"_level":0,"count":10000}
{"_level":1,"a_kind":217,"count":508}
{"_level":1,"a_kind":111,"count":2412}
{"_level":1,"a_kind":176,"count":1847}
...
{"_level":2,"a_kind":217,"z_kind":212,"count":104}
{"_level":2,"a_kind":217,"z_kind":114,"count":87}
...
```

For `ssql rollup a_kind z_kind -count count -cube`:

```jsonl
{"_level":0,"count":10000}
{"_level":1,"a_kind":217,"count":508}
{"_level":1,"a_kind":111,"count":2412}
...
{"_level":1,"z_kind":212,"count":623}
{"_level":1,"z_kind":114,"count":1558}
...
{"_level":2,"a_kind":217,"z_kind":212,"count":104}
{"_level":2,"a_kind":217,"z_kind":114,"count":87}
...
```

### Schema Header

The `_schema` header should list all possible fields:

```jsonl
{"_schema":{"fields":["_level","a_kind","z_kind","count"],"types":["int","int","int","int"]}}
```

### Practical Use Cases

**1. Heatmap with marginals (the motivating case):**

```bash
ssql from data.csv | ssql rollup a_kind z_kind -count count -cube | ssql to chart -type heatmap ...
```

The heatmap renderer could use level 0 for a title annotation, level 1 for row/column marginals.

**2. Percentage calculation:**

```bash
ssql from data.csv | ssql rollup dept -sum salary total \
    | ssql update -set-expr pct 'total / total_at_level_0 * 100'
```

This requires a way to reference the grand total. One approach: a `rollup-pct` convenience that auto-computes percentages. Or users `join` the level-0 row:

```bash
# Get grand total
TOTAL=$(ssql from data.csv | ssql rollup dept -sum salary total \
    | ssql where -where _level eq 0 | ssql include total | ssql to json)

# Join or compute inline
ssql from data.csv | ssql rollup dept -sum salary total \
    | ssql where -where _level eq 1 \
    | ssql update -set-expr pct "total / $TOTAL * 100"
```

**3. Dashboard data preparation:**

```bash
ssql from sales.csv | ssql rollup region product year \
    -sum revenue total \
    -count deals \
    -avg revenue avg_deal \
    | ssql to json > dashboard_data.json
```

One query produces grand total, per-region, per-region-product, and full detail.

## Implementation Order

1. **`Rollup()` in `sql.go`** — core library function
2. **Tests in `sql_test.go`** — rollup and cube modes, edge cases
3. **`rollup` CLI command** — `cmd/ssql/commands/rollup.go`
4. **Code generation** — `-generate` support
5. **Generation tests** — `cmd/ssql/generation_test.go`
6. **Documentation** — cli-codelab.md, api-reference.md

## Open Questions

1. **Level field name** — `_level` seems natural, but should it be configurable?
2. **Ordering** — Should levels be ascending (total first) or descending (detail first)? SQL does ascending (detail → subtotal → total), but total-first is more natural for display.
3. **GROUPING SETS** — Should we support arbitrary grouping sets, or just ROLLUP and CUBE? ROLLUP + CUBE cover 95% of use cases.
4. **Percentage helper** — Should rollup automatically compute percentages relative to parent level? This is very common but adds complexity.
