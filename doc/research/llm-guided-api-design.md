# LLM-Guided API Design: A Case Study in Iterative Prompt Engineering for Code Generation

**Authors:** Ross Cartlidge, with Claude (Anthropic)

**Abstract:** We present a methodology for designing APIs that are naturally expressible through large language models (LLMs). Using ssql, a Go stream processing library, as a case study, we demonstrate how iterative prompt testing with objective validation criteria can improve code generation accuracy. Our approach—which we call the "Ralph Wiggum Loop"—achieves 100% test pass rates across 30 structured test cases on both Claude and Gemini, verified through integration testing that executes generated code against real data. We find that SQL-style naming conventions, encapsulated types, and functional composition patterns significantly improve LLM code generation quality. Notably, integration testing revealed a 13% gap between syntactically correct and behaviorally correct code. Our comparative analysis shows that different LLMs require different teaching strategies: Claude reached 100% in 2 iterations through positive examples, while Gemini required 5 iterations and explicit anti-patterns to address function hallucination and type confusion. We argue this multi-LLM testing approach should become standard practice for library designers who want their APIs to be broadly LLM-accessible.

## 1. Introduction

As large language models become integral to software development workflows, a new design consideration emerges: *How well can an LLM generate correct code using this API?* Traditional API design focuses on human ergonomics—discoverability, consistency, documentation. We propose that LLM-friendliness should be an explicit design goal, measured through automated testing.

This paper presents three contributions:

1. **The Ralph Wiggum Loop**: An iterative methodology for improving code generation prompts using objective validation
2. **A test harness**: Pattern-based validation with integration testing for both compilation correctness and behavioral accuracy
3. **Design principles**: Specific API patterns that improve LLM code generation quality

### 1.1 Background: ssql

ssql is a Go library for Unix-style stream processing, combining the composability of Unix pipes with the expressiveness of SQL. It provides two interfaces:

**Go Library:** A functional API using Go 1.23+ iterators (`iter.Seq[T]`) for lazy, memory-efficient data processing:

```go
records := ssql.ReadCSV("data.csv")
filtered := ssql.Where(predicate)(records)
grouped := ssql.GroupByFields("_g", "dept")(filtered)
result := ssql.Aggregate("_g", aggregations)(grouped)
```

**CLI Tool:** A Unix pipeline interface where each command reads from stdin and writes to stdout:

```bash
ssql from data.csv | ssql where -where age gt 30 | ssql group-by dept -count count | ssql to json
```

The CLI is built on autocli, which provides:
- **Auto-generated help:** Every command has comprehensive `-help` output with usage, flags, and examples—no manual required
- **Data-aware tab completion:** Field names are extracted from data files and offered as completions (e.g., `ssql where -where <TAB>` shows actual column names)
- **Field value completion:** When filtering, actual data values are sampled and offered (e.g., `-where status eq <TAB>` shows "active", "pending", etc.)
- **Subcommand discovery:** `ssql <TAB>` shows all available commands with descriptions

**Design Philosophy:**

1. **Functional Composition:** Operations are pure functions that transform iterators. `Filter[T,U]` is defined as `func(iter.Seq[T]) iter.Seq[U]`, enabling composition via `Chain()` or manual nesting.

2. **SQL-Aligned Naming:** Operations use SQL terminology (`Where`, `GroupByFields`, `Limit`, `Offset`) rather than functional programming names (`filter`, `take`, `drop`), improving familiarity.

3. **Type-Safe Records:** The `Record` type uses encapsulated fields with accessor methods (`GetOr()`, `Get[T]()`), preventing unsafe direct map access that would cause runtime panics.

4. **Canonical Numeric Types:** Only `int64` and `float64` are used for numeric scalars, eliminating type conversion ambiguity.

5. **Zero-Allocation Paths:** Performance-critical operations reuse schemas and buffers, achieving 4x speedups on large datasets.

**Capabilities:** CSV/JSON/Arrow I/O, filtering, grouping, aggregation, joins, sorting, pagination, signal processing (FFT, convolution, spectrogram), and interactive chart generation.

The library's design choices directly support LLM code generation—a hypothesis we test in this paper.

### 1.2 Motivation

Consider a user who wants to process a CSV file:

```
"Filter users over 30, group by department, count each group,
sort by count descending, show top 5"
```

An LLM must translate this natural language into correct code. With a well-designed API and prompt, this becomes:

```go
records := ssql.ReadCSV("users.csv")
filtered := ssql.Where(func(r ssql.Record) bool {
    return ssql.GetOr(r, "age", int64(0)) > 30
})(records)
grouped := ssql.GroupByFields("_grouped", "dept")(filtered)
aggregated := ssql.Aggregate("_grouped", map[string]ssql.AggregateFunc{
    "count": ssql.Count(),
})(grouped)
sorted := ssql.SortBy(func(a, b ssql.Record) int {
    return -cmp.Compare(
        ssql.GetOr(a, "count", int64(0)),
        ssql.GetOr(b, "count", int64(0)),
    )
})(aggregated)
result := ssql.Limit[ssql.Record](5)(sorted)
```

The challenge is ensuring the LLM generates this correct code rather than inventing non-existent APIs, using wrong type signatures, or producing code that compiles but behaves incorrectly.

## 2. The Ralph Wiggum Loop

Named after the Simpsons character who learns through repeated attempts, this methodology iteratively improves prompts by:

1. Running test cases against the current prompt
2. Collecting failures with detailed diagnostics
3. Feeding failures back to improve the prompt
4. Repeating until convergence

### 2.1 Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Test Case Definition                         │
│  - ID: GO-01                                                     │
│  - Prompt: "Filter users over 30, count by department..."       │
│  - Expected: ["ssql.Where(", "ssql.GroupByFields("]             │
│  - Negative: ["streamv3.", "r[\"field\"]"]                      │
│  - Validation: compile                                           │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Prompt Construction                          │
│  System Prompt (doc/ai-code-generation.md)                       │
│  + Test Case Prompt                                              │
│  + "Generate ONLY Go code, no explanation"                       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     LLM Invocation                               │
│  claude -p "$prompt" < /dev/null                                 │
│  gemini -p "$prompt" < /dev/null                                 │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Validation                                   │
│  1. Extract code from markdown (```go ... ```)                   │
│  2. Check expected patterns present                              │
│  3. Check negative patterns absent                               │
│  4. For Go: compile with test go.mod                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Failure Collection                           │
│  - Which patterns failed                                         │
│  - Compilation errors                                            │
│  - Full generated output                                         │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Fix Request Generation                       │
│  doc/ai-fix-request.md with structured failure info             │
│  → Interactive Claude session to update prompts                  │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 Experimental Setup

**Models Tested:**

| LLM | Model Version | CLI Tool | Notes |
|-----|---------------|----------|-------|
| Claude | claude-opus-4-5-20251101 (Opus 4.5) | `claude` v1.0.x | Anthropic's most capable model |
| Gemini | gemini-2.0-flash | `gemini` v0.26.0 | Google's fast multimodal model |

**Testing Date:** January-February 2026

**Hardware:** Tests run on Intel Core Ultra 9 275HX, Ubuntu Linux

**Invocation:** Both models were invoked via their respective CLI tools in non-interactive mode:
- Claude: `claude -p "$prompt" < /dev/null`
- Gemini: `gemini --prompt "$prompt"`

No custom temperature or sampling parameters were used; both CLIs used their default settings.

### 2.3 Validation Types

We employ three validation strategies:

**Compilation Validation (Go code):**
- Write generated code to temporary file
- Attempt `go build` with proper module configuration
- Compilation failure = test failure

**Pattern Validation (CLI pipelines and semantic checks):**
- Expected patterns: strings that MUST appear in output
- Negative patterns: strings that MUST NOT appear
- Supports alternatives with ` or ` separator

Example pattern specification:
```
Expected: `ssql.FFT(` or `ssql.FFTWithPhase(`
Negative: `streamv3.`, `r["field"].(string)`
```

**Integration Validation (behavioral correctness):**
- Execute generated code against real test data
- Verify output contains expected values
- Catches semantic errors that pass compilation

### 2.4 Integration Testing

Pattern matching and compilation verify *syntactic* correctness but cannot detect *semantic* errors—code that compiles but produces wrong results. Integration testing addresses this gap.

**Architecture:**

```
┌─────────────────────────────────────────────────────────────────┐
│                     Test Case with Data                         │
│  - Prompt: "Filter users over 30..."                           │
│  - Test Data: users.csv, employees.csv                         │
│  - Expected Output: "Engineering", "count", "2"                │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Code Generation                             │
│  LLM generates Go code or CLI pipeline                          │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Pattern Validation                          │
│  Check expected/negative patterns (pass/fail)                   │
└─────────────────────────────────────────────────────────────────┘
                              │ (if pass)
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Compilation (Go only)                       │
│  go build → binary                                              │
└─────────────────────────────────────────────────────────────────┘
                              │ (if pass)
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Execution                                   │
│  Run with test data, capture stdout                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Output Validation                           │
│  Check expected output patterns in result                       │
│  - "Engineering" present? ✓                                     │
│  - "count" present? ✓                                           │
│  - "2" present? ✓                                               │
└─────────────────────────────────────────────────────────────────┘
```

**Test Data Files:**

We maintain a `test-data/` directory with sample files:

| File | Description | Records |
|------|-------------|---------|
| `users.csv` | Users with name, age, dept, salary, status | 8 |
| `employees.csv` | Employees with name, age, dept, salary | 5 |
| `orders.csv` | Orders with customer_id, product_id, totals | 5 |
| `customers.csv` | Customers with id, name, city, tier | 3 |
| `measurements.csv` | Sensor readings with timestamp, value | 11 |

**Example Integration Test Case:**

```markdown
### GO-01: Basic Filter and Aggregate

**Prompt:** Read employees.csv. Filter to those over 30. Group by dept. Count each.

**Test Data:** employees.csv

**Expected Output:**
- `Engineering`
- `count`
- `2`

**Expected patterns:** `ssql.Where(`, `ssql.GroupByFields(`
```

The test passes only if:
1. All expected patterns appear in generated code
2. No negative patterns appear
3. Code compiles successfully (Go)
4. Execution produces output containing "Engineering", "count", and "2"

**Error Detection Examples:**

Integration testing catches errors that pattern matching misses:

| Error Type | Pattern Check | Integration Check |
|------------|---------------|-------------------|
| Wrong field name (`GetOr(r, "Age", ...)` vs `"age"`) | ✓ Pass | ✗ Fail (empty results) |
| Wrong comparison (`> 30` vs `>= 30`) | ✓ Pass | ✗ Fail (wrong count) |
| Missing output write (`WriteJSON` to `nil`) | ✓ Pass | ✗ Fail (runtime panic) |
| Inverted filter logic | ✓ Pass | ✗ Fail (wrong records) |

### 2.5 Multi-LLM Testing

To ensure prompts are LLM-agnostic, we test across multiple models:

```bash
./scripts/test-ai-prompts.sh --all-llms
```

This runs all test cases against each supported LLM and produces a summary:

```
╔═══════════════════════════════════════════════════════════════╗
║                    MULTI-LLM TEST SUMMARY                      ║
╠═══════════════════════════════════════════════════════════════╣
║  LLM      │ Go Tests │ CLI Tests │ Total  │ Rate              ║
╠═══════════════════════════════════════════════════════════════╣
║  claude   │   15/15  │   15/15   │ 30/30  │ 100%              ║
║  gemini   │   12/15  │   15/15   │ 27/30  │  90%              ║
╚═══════════════════════════════════════════════════════════════╝
```

Failures on specific LLMs indicate prompt areas needing improvement for broader compatibility.

## 3. Test Case Design

Our test suite contains 30 test cases (15 Go, 15 CLI) covering the full API surface.

### 3.1 Go Code Generation Tests

| ID | Description | Key Patterns |
|----|-------------|--------------|
| GO-01 | Basic filter + aggregate | `Where(`, `GroupByFields(`, `Count()` |
| GO-02 | Top N with sort | `SortBy(`, `Limit[` |
| GO-03 | Signal processing FFT | `FFT(` or `FFTWithPhase(`, `SpectrumToRecords(` |
| GO-04 | Spectrogram analysis | `Spectrogram(`, `SpectrogramOptions`, `HannWindow()` |
| GO-05 | Record field access | `GetOr(`, `MakeMutableRecord()` |
| GO-06 | Join operations | `InnerJoin(` or `LookupJoin(`, `OnFields(` |
| GO-07 | Update with transformation | `Update(`, `MutableRecord` |
| GO-08 | Convolution | `Convolve(`, `GaussianKernel(` |
| GO-09 | Distinct + union | `DistinctBy(`, `Concat(`, `slices.Values(` |
| GO-10 | Chart generation | `QuickChart(` |
| GO-11 | Multi-stage pipeline | `Chain(` |
| GO-12 | Computed fields | `Update(`, `.Float(` or `.Int(` |
| GO-13 | Safe field access | `GetOr(`, default value |
| GO-14 | Early limit optimization | `Limit[` before expensive ops |
| GO-15 | JSON input | `ReadJSON(` or `ReadJSONL(` |

### 3.2 CLI Pipeline Tests

| ID | Description | Key Patterns |
|----|-------------|--------------|
| CLI-01 | Basic filter | `from`, `where -where`, `to` |
| CLI-02 | Group aggregation | `group-by`, `-count` or `-sum` |
| CLI-03 | Update with conditions | `update`, `-where`, `-set` |
| CLI-04 | Signal processing | `fft`, `-field`, `-rate` |
| CLI-05 | Spectrogram | `spectrogram`, `-window-size` |
| CLI-06 | Join with rename | `join`, `-on`, `-as` |
| CLI-07 | Expression filter | `-where-expr`, `-set-expr` |
| CLI-08 | Sort + pagination | `sort`, `limit`, `offset` |
| CLI-09 | Code generation | `SSQLGO=1`, `generate-go` |
| CLI-10 | Format conversion | `from`, `to arrow` or `to json` |
| CLI-11 | Complex multi-stage | Multiple piped commands |
| CLI-12 | Chart visualization | `to chart`, `-x`, `-y` |
| CLI-13 | Distinct union | `union`, `-distinct` |
| CLI-14 | Offset pagination | `offset`, `limit` |
| CLI-15 | Field selection | `include` or `exclude` |

### 3.3 Negative Pattern Examples

Negative patterns catch common LLM mistakes:

| Pattern | Catches |
|---------|---------|
| `streamv3.` | Old package name before rename |
| `ssql/v3` | Old import path (now v4) |
| `r["field"].(string)` | Unsafe direct map access |
| `FlatMap` | Wrong naming (should be SelectMany) |
| `ssql.FromSlice` | Non-existent function |
| `-match` | Old CLI flag (now `-where`) |
| `-expr` (standalone) | Old flag form |

## 4. API Design for LLM Compatibility

Our experience with ssql revealed several API design principles that improve LLM code generation.

### 4.1 SQL-Style Naming

LLMs have extensive training data on SQL. Using SQL-aligned names improves generation accuracy:

| Functional Name | SQL-Style Name | LLM Benefit |
|-----------------|----------------|-------------|
| `Filter` | `Where` | Direct SQL mapping |
| `FlatMap` | `SelectMany` | LINQ familiarity |
| `Map` | `Select` | SQL projection |
| `Take` | `Limit` | SQL LIMIT |
| `Drop` | `Skip`/`Offset` | SQL OFFSET |

### 4.2 Encapsulated Types Prevent Errors

The `Record` type uses private fields with accessor methods:

```go
// LLMs can't generate this (won't compile):
value := record["name"].(string)  // ❌ Private fields

// LLMs must use the safe accessor:
value := ssql.GetOr(record, "name", "")  // ✅ Compiles
```

This design makes incorrect patterns uncompilable, forcing LLMs toward correct usage. The compiler becomes an additional validation layer.

### 4.3 Functional Composition with Chain

The `Chain` function enables readable multi-stage pipelines:

```go
result := ssql.Chain(records,
    ssql.Where(ageFilter),
    ssql.GroupByFields("_g", "dept"),
    ssql.Aggregate("_g", aggregations),
    ssql.SortBy(byCount),
    ssql.Limit[ssql.Record](10),
)
```

This pattern:
- Reads top-to-bottom like natural language
- Avoids nested function calls
- Each stage is independently testable
- LLMs can add/remove stages easily

### 4.4 Canonical Types Eliminate Ambiguity

ssql uses only `int64` and `float64` for numeric scalars:

```go
// Always int64, never int, int32, etc.
age := ssql.GetOr(r, "age", int64(0))

// Always float64, never float32
price := ssql.GetOr(r, "price", float64(0.0))
```

This eliminates type conversion confusion. LLMs don't need to guess which numeric type to use.

### 4.5 Builder Pattern for Complex Objects

The `MutableRecord` builder provides a fluent, type-safe interface:

```go
record := ssql.MakeMutableRecord().
    String("name", "Alice").
    Int("age", int64(30)).
    Float("salary", 95000.50).
    Bool("active", true).
    Freeze()
```

Each method is named after its type, making correct usage obvious to LLMs.

## 5. Quantitative Results

### 5.1 Pass Rates by LLM

**Current Results (with Integration Testing):**

| LLM | Go Tests | CLI Tests | Total | Rate |
|-----|----------|-----------|-------|------|
| Claude (Opus 4.5) | 15/15 | 15/15 | 30/30 | 100% |
| Gemini | 15/15 | 15/15 | 30/30 | 100% |

Both LLMs achieve 100% pass rate after iterative prompt improvements.

**Historical (before prompt improvements):**

| LLM | Go Tests | CLI Tests | Total | Rate |
|-----|----------|-----------|-------|------|
| Claude (initial) | 15/15 | 11/15 | 26/30 | 87% |
| Gemini (initial) | 10/15 | 13/15 | 23/30 | 77% |

The improvement from 77% to ~93% for Gemini demonstrates the methodology's effectiveness across different LLMs.

### 5.2 Common Failure Modes

**Pattern-Only Failures (Gemini):**

- **GO-09 (Distinct + union):** Used `ssql.FromSlice` (non-existent) instead of `slices.Values()`
- **CLI-13 (Distinct union):** Minor flag ordering difference

**Integration Test Failures:**

| Test | Error Type | Pattern? | Integration? |
|------|------------|----------|--------------|
| GO-01/02 | `WriteJSONToWriter(result, nil)` | ✓ Pass | ✗ Panic |
| CLI-02 | Used `-field dept` (doesn't exist) | ✓ Pass | ✗ Wrong syntax |
| CLI-09 | Used `-field` with group-by | ✓ Pass | ✗ Wrong syntax |
| CLI-11 | Wrong aggregation flags | ✓ Pass | ✗ Wrong output |

The most common integration failure was LLMs generating `nil` instead of `os.Stdout` for output writers—code that compiles but panics at runtime.

### 5.3 Prompt Improvements from Integration Testing

Integration test failures led to specific prompt improvements:

1. **Explicit output destination:** Added "Write results to `os.Stdout`" instead of just "write as JSON"
2. **Positional argument reminder:** CLI group-by uses positional args, not `-field` flag
3. **Flag existence validation:** Added non-existent flags to negative patterns

These changes improved Claude's integration pass rate from 87% to 100% on subsequent runs.

### 5.4 Iteration History

| Version | Tests | Claude (Pattern) | Claude (Integration) | Changes |
|---------|-------|------------------|----------------------|---------|
| v1 | 20 | 18/20 | N/A | Initial prompt |
| v2 | 20 | 20/20 | N/A | Fixed stdin bug, pattern matching |
| v3 | 30 | 30/30 | N/A | Added 10 tests, multi-LLM |
| v4 | 30 | 30/30 | 26/30 | Added integration testing |
| v5 | 30 | 30/30 | 30/30 | Fixed nil/stdout, flag syntax |
| v6 | 30 | 30/30 | 30/30 | Fixed group-by/sort positional args, union flags, export SSQLGO |
| v7 | 30 | 30/30 | ~27/30 | Go prompt: WriteJSONToWriter, record.All() warning, ssql.Range anti-pattern |
| v8 | 30 | 30/30 | 30/30 | CLI-03 workaround for update schema bug (use raw JSONL output) |

**Gemini Improvement:** Achieved 100% pass rate (30/30) after iterative prompt improvements:
- Go tests: 10/15 → 15/15 (WriteJSONToWriter docs, record.All() warning, ssql.Range anti-pattern)
- CLI tests: ~13/15 → 15/15 (CLI-03 workaround for schema header bug in update command)

### 5.5 Case Study: Fixing CLI Failures Through Iteration

Version 6 demonstrates the Ralph Wiggum methodology in action. Integration testing revealed 4 CLI failures (11/15 pass rate):

**Failure Analysis:**

| Test | Generated | Problem | Root Cause |
|------|-----------|---------|------------|
| CLI-02 | `group-by -field dept` | Flag doesn't exist | Prompt documented wrong syntax |
| CLI-09 | `group-by -field dept` | Same | Same |
| CLI-11 | `group-by -field dept` | Same | Same |
| CLI-13 | `union -distinct` | Flag doesn't exist | Prompt documented wrong flag |

**Diagnosis:** The CLI prompt incorrectly documented `group-by` as using `-field F` syntax, but the actual CLI uses positional arguments (`group-by dept`). Similarly, `union` was documented with `-distinct` flag, but it deduplicates by default.

**Fixes Applied to `doc/ai-cli-generation.md`:**

1. **Command reference table:** Changed `group-by` from `-field F` to `FIELDS...` (positional)
2. **All examples:** Updated 12 occurrences of `group-by -field X` to `group-by X`
3. **Anti-patterns section:** Added explicit "WRONG/CORRECT" examples for `-field` and `-distinct`
4. **Code generation section:** Fixed `SSQLGO=1` to `export SSQLGO=1 &&` (env var must be exported for pipeline)

**Additional discovery:** CLI-09 continued failing after the `-field` fix because `SSQLGO=1 cmd1 | cmd2` only sets the variable for `cmd1`. The prompt was updated to show `export SSQLGO=1 &&` pattern.

**Result:** After these targeted prompt fixes, all 30 tests pass with integration (15/15 Go, 15/15 CLI).

**Key insight:** The methodology works—failures pinpoint exactly where prompts are wrong, enabling surgical fixes rather than guesswork.

## 6. Discussion

### 6.1 Should This Be a General Technique?

We argue that LLM-friendly API design should become standard practice for libraries intended for broad use. The marginal cost is low—SQL-style naming and encapsulated types are often good design regardless—while the benefit is substantial as LLM-assisted coding becomes ubiquitous.

**Recommendations for library designers:**

1. **Choose familiar naming:** Prefer SQL/LINQ terminology over novel terms
2. **Make wrong things uncompilable:** Use type system to prevent common mistakes
3. **Provide composition primitives:** Chain/Pipe patterns are easier for LLMs than nested calls
4. **Limit type variety:** Canonical types reduce guessing
5. **Test with multiple LLMs:** Prompts that work on one may fail on another

### 6.2 Did ssql's Design Make Generation Easier?

Comparing ssql to alternative approaches:

| Approach | LLM Challenge |
|----------|---------------|
| Raw `map[string]any` | No type safety, easy to generate wrong code |
| Deep generics | Complex type signatures confuse LLMs |
| Method chaining on types | Harder to compose than function pipelines |
| Novel terminology | LLMs hallucinate familiar alternatives |

ssql's design choices—encapsulated Record, functional composition, SQL naming—directly address common LLM failure modes. The 100% pass rate with Claude suggests these patterns are highly effective.

### 6.3 The Value of Integration Testing

Our results demonstrate that pattern matching alone is insufficient for validating LLM-generated code. The gap between pattern-only (100%) and integration (87%) pass rates for Claude reveals a class of errors invisible to syntactic validation:

- **Runtime errors:** Code that compiles but panics (e.g., `nil` writer)
- **Logic errors:** Inverted predicates, wrong field names
- **API misuse:** Using non-existent flags that pattern matching doesn't catch

Integration testing transforms prompt engineering from "does it look right?" to "does it work?"—a substantially harder but more meaningful bar.

### 6.4 Limitations

- Test cases are synthetic; real user prompts may reveal gaps
- Integration tests require maintained test data and expected outputs
- Multi-LLM testing limited to models with CLI interfaces
- Go-specific findings may not generalize to other languages
- Expected output patterns may be too strict or too loose

### 6.5 Comparative Analysis: Teaching Claude vs Gemini

Our iterative improvement process revealed distinct learning characteristics between Claude and Gemini. Both achieved 100% pass rates, but the path to get there differed substantially.

**Convergence Speed:**

| LLM | Initial Rate | Iterations to 100% | Primary Blockers |
|-----|--------------|-------------------|------------------|
| Claude | 87% | 2 | Output destination (nil vs os.Stdout), CLI flag syntax |
| Gemini | 77% | 5 | Function hallucination, signature confusion, type misunderstanding |

Claude reached 100% faster, requiring fewer prompt iterations and less explicit negative documentation.

**Failure Mode Analysis:**

*Claude's failures* were primarily about implicit context:
- Used `nil` instead of `os.Stdout` when prompt said "write JSON" without explicit destination
- CLI flag ordering differences (minor)

These failures were easy to fix—adding explicit output destinations resolved them immediately.

*Gemini's failures* revealed deeper comprehension gaps:

1. **Function hallucination:** Gemini generated calls to `ssql.Range()` and `ssql.FromSlice()`—functions that don't exist but *sound* like they should. Claude never hallucinated API functions.

2. **Signature confusion:** Gemini used `ssql.WriteJSON(records, os.Stdout)` despite documentation showing `WriteJSON` takes a filename string. It conflated `WriteJSON` with `WriteJSONToWriter`. Claude correctly distinguished between these.

3. **Type system misunderstanding:** Gemini attempted `json.Marshal(record.All())`, not recognizing that `All()` returns an iterator (`iter.Seq2`), not a materialised map. This suggests weaker inference about Go's type system.

4. **Standard library preference:** Gemini more aggressively reached for Go standard library patterns (manual loops, json.Encoder) even when ssql provided idiomatic alternatives.

**Teaching Strategy Differences:**

| Aspect | Claude | Gemini |
|--------|--------|--------|
| Positive examples | Highly effective | Effective |
| Negative examples | Occasionally needed | Essential |
| Explicit "DO NOT" warnings | Rarely needed | Critical |
| Function signature docs | Inferred well | Needed explicit detail |
| Anti-pattern section | Nice to have | Must have |

To teach Gemini effectively, we found these patterns essential:

```markdown
// WRONG - WriteJSON takes a filename, not a writer!
err = ssql.WriteJSON(records, os.Stdout)  // WON'T COMPILE

// CORRECT - Use WriteJSONToWriter for io.Writer
err = ssql.WriteJSONToWriter(records, os.Stdout)

**NOTE:** There is NO `ssql.Range()` function. Use standard Go:
for i, v := range signal { ... }
```

Claude rarely needed such explicit warnings—it inferred constraints from positive examples.

**Why the Difference?**

We hypothesise several factors:

1. **Training data composition:** Gemini may have more exposure to general Go codebases where `Range()` functions are common (e.g., Python's `range()` influence). Claude may weight documentation more heavily.

2. **Uncertainty handling:** Claude appears more conservative—when uncertain, it sticks to documented patterns. Gemini is more exploratory, attempting familiar patterns even when not documented.

3. **Context prioritisation:** Claude seems to give system prompts higher weight relative to pre-training knowledge. Gemini may allow pre-training to "leak through" more readily.

4. **Type inference:** Claude showed stronger understanding of Go's type system, correctly inferring that `iter.Seq2` cannot be JSON-encoded. Gemini treated types more loosely.

**Practical Implications:**

For prompt engineers targeting multiple LLMs:

1. **Start with Gemini:** If your prompt works with Gemini, it will likely work with Claude. The reverse is not true.

2. **Include explicit anti-patterns:** Document what NOT to do, with concrete WRONG/CORRECT examples.

3. **Be explicit about function signatures:** Don't assume LLMs will infer parameter types correctly.

4. **Warn about non-existent functions:** If your API lacks a commonly-expected function (like `Range`), say so explicitly.

5. **Test iteratively:** Use Gemini for development iteration (faster, cheaper) and validate with Claude before release.

**Is One Better?**

Neither LLM is definitively "better"—they have different strengths:

- **Claude:** More reliable for documentation-heavy tasks, fewer surprises, faster convergence
- **Gemini:** More creative exploration, may find novel solutions, but requires more guardrails

For code generation from domain-specific APIs, Claude's conservative approach is advantageous—it's less likely to generate plausible-looking but incorrect code. For open-ended tasks where exploration is valuable, Gemini's tendency to try familiar patterns might occasionally discover better solutions.

The key insight is that **prompt portability across LLMs is not guaranteed**. A prompt achieving 100% on Claude may score 77% on Gemini. Multi-LLM testing isn't optional—it's essential for robust prompt engineering.

### 6.6 Future Work

1. **Fuzzing:** Generate random valid prompts to find edge cases
2. **Cross-language:** Apply methodology to TypeScript, Python versions
3. **User study:** Compare code generation quality with/without LLM-friendly design
4. **Automated prompt repair:** Use integration failures to automatically improve prompts
5. **Coverage metrics:** Measure what percentage of API surface is exercised by tests

## 7. Related Work

- **GitHub Copilot evaluations:** Benchmark studies on code completion accuracy
- **API usability research:** Human factors in API design
- **Prompt engineering:** Techniques for improving LLM outputs
- **Type-directed synthesis:** Using types to guide code generation

Our work differs in explicitly designing the underlying API to improve LLM generation, rather than improving prompts for fixed APIs.

## 8. Conclusion

We presented a methodology for designing and validating LLM-friendly APIs. The Ralph Wiggum Loop provides an iterative process for improving code generation prompts with objective validation. Our case study with ssql demonstrates that specific design choices—SQL-style naming, encapsulated types, functional composition—significantly improve LLM code generation quality.

A key finding is the importance of integration testing. Pattern matching alone verified 100% syntactic correctness, but integration testing revealed 13% of generated code failed at runtime—producing panics, wrong results, or using non-existent flags. This gap demonstrates that "does it compile?" is insufficient; "does it work?" requires executing code against real data.

Our comparative analysis of Claude and Gemini revealed that **prompt portability across LLMs is not guaranteed**. Claude reached 100% pass rate in 2 iterations; Gemini required 5. The failure modes differed qualitatively: Claude's failures were about implicit context (output destinations), while Gemini hallucinated non-existent functions, confused similar API signatures, and misunderstood Go's type system. Teaching Gemini required explicit anti-patterns and "DO NOT" warnings that Claude inferred from positive examples alone. For multi-LLM robustness, we recommend developing prompts against Gemini (the more demanding target) and validating with Claude.

As LLM-assisted programming becomes ubiquitous, library designers should consider LLM-friendliness alongside traditional usability metrics. The techniques presented here—iterative prompt refinement with pattern and integration testing across multiple LLMs—provide a practical framework for measuring and improving this new dimension of API quality.

## Appendix A: Implementation

The test harness is implemented in `scripts/test-ai-prompts.sh` (~600 lines of Bash). Test cases are defined in `doc/ai-test-cases.md` using a structured format. System prompts are in `doc/ai-code-generation.md` (Go) and `doc/ai-cli-generation.md` (CLI). Test data files are in `test-data/`.

**File Inventory:**

| File | Purpose | Lines |
|------|---------|-------|
| [`doc/ai-code-generation.md`](../ai-code-generation.md) | Go code generation prompt | ~900 |
| [`doc/ai-cli-generation.md`](../ai-cli-generation.md) | CLI pipeline generation prompt | ~500 |
| [`doc/ai-test-cases.md`](../ai-test-cases.md) | 30 structured test cases | ~600 |
| [`scripts/test-ai-prompts.sh`](../../scripts/test-ai-prompts.sh) | Test runner with Ralph Wiggum loop | ~600 |
| `test-data/*.csv` | Test data files | 8 files |

To run the test suite:

```bash
# Development iteration: use Gemini to save Claude quota
./scripts/test-ai-prompts.sh --llm gemini --integration

# Final validation: test with Claude
./scripts/test-ai-prompts.sh --llm claude --integration

# Or test all supported LLMs
./scripts/test-ai-prompts.sh --all-llms --integration

# With interactive fix application
./scripts/test-ai-prompts.sh --apply-fixes
```

**Recommended workflow:** Use `--llm gemini` during iterative development (cheaper/faster), then validate with `--llm claude` or `--all-llms` before finalizing prompt changes.

Makefile targets:

```bash
make ai-test              # Pattern validation only
make ai-test-integration  # Full integration testing
```

## Appendix B: Example Test Case

```markdown
### GO-01: Basic Filter and Aggregate

**Prompt:**
Read employees.csv. Filter to employees over 30 years old.
Group by dept. Count employees in each department.
Write the results as JSON to os.Stdout.

**Expected patterns:**
- `ssql.ReadCSV(`
- `ssql.Where(`
- `ssql.GroupByFields(`
- `ssql.Count()`
- `ssql.GetOr(`
- `int64(0)` or `int64(`
- `os.Stdout`

**Negative patterns:**
- `streamv3.`
- `r["field"]`

**Validation:** compile

**Test Data:** employees.csv

**Expected Output:**
- `Engineering`
- `count`
- `2`
```

The test data (`test-data/employees.csv`):
```csv
name,age,dept,salary
Alice,35,Engineering,95000
Bob,28,Sales,65000
Charlie,42,Engineering,110000
Diana,31,Marketing,75000
Eve,25,Sales,55000
```

With this data, filtering `age > 30` yields Alice (35), Charlie (42), and Diana (31). Grouping by `dept` produces Engineering: 2, Marketing: 1. The expected output patterns verify the result contains these values.

## Appendix C: System Prompt Structure

The Go code generation prompt (~900 lines) includes:

1. **Module context:** Import paths, version information
2. **Core type documentation:** Record, MutableRecord, Filter
3. **Operation reference:** All functions with signatures and examples
4. **Critical patterns:** Type-safe field access, composition
5. **Anti-patterns:** What NOT to generate (old APIs, wrong types)
6. **Complete examples:** 6 full programs showing common patterns
7. **Pattern recognition table:** Natural language → API mapping

The CLI prompt (~500 lines) follows a similar structure for pipeline commands.

## Appendix D: Example Generated Outputs

These are actual outputs generated by Claude during testing, demonstrating the methodology produces working code.

### GO-01: Basic Filter and Aggregate

**Prompt:** Read employees.csv. Filter to employees over 30 years old. Group by dept. Count employees in each department. Write the results as JSON to os.Stdout.

**Generated Go Code:**
```go
package main

import (
    "log"
    "os"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    employees, err := ssql.ReadCSV("employees.csv")
    if err != nil {
        log.Fatalf("Failed to read CSV: %v", err)
    }

    result := ssql.Chain(
        ssql.Where(func(r ssql.Record) bool {
            age := ssql.GetOr(r, "age", int64(0))
            return age > 30
        }),
        ssql.GroupByFields("_grouped", "dept"),
        ssql.Aggregate("_grouped", map[string]ssql.AggregateFunc{
            "count": ssql.Count(),
        }),
    )(employees)

    if err := ssql.WriteJSONToWriter(result, os.Stdout); err != nil {
        log.Fatalf("Failed to write JSON: %v", err)
    }
}
```

### CLI-02: Group-By with Aggregation

**Prompt:** Read users.csv, group by dept, count the number of users in each department.

**Generated CLI Pipeline:**
```bash
ssql from users.csv | ssql group-by dept -count count | ssql to table
```

### CLI-09: Code Generation Pipeline

**Prompt:** Generate a standalone Go program from this pipeline: read users.csv, filter where status equals active, group by dept, count per dept, output to stdout.

**Generated CLI Pipeline:**
```bash
export SSQLGO=1 && ssql from users.csv | ssql where -where status eq active | ssql group-by dept -count count | ssql generate-go
```

Note how the generated code correctly uses:
- `export SSQLGO=1` (not just `SSQLGO=1`) so all pipeline stages see the variable
- Positional argument `dept` for group-by (not `-field dept`)
- `-count count` with result field name
