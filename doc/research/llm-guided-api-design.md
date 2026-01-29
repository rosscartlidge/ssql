# LLM-Guided API Design: A Case Study in Iterative Prompt Engineering for Code Generation

**Authors:** Ross Cartlidge, with Claude (Anthropic)

**Abstract:** We present a methodology for designing APIs that are naturally expressible through large language models (LLMs). Using ssql, a Go stream processing library, as a case study, we demonstrate how iterative prompt testing with objective validation criteria can improve code generation accuracy. Our approach—which we call the "Ralph Wiggum Loop"—achieves 100% test pass rates on Claude and 90% on Gemini across 30 structured test cases. We find that SQL-style naming conventions, encapsulated types, and functional composition patterns significantly improve LLM code generation quality. We argue this approach should become a standard practice for library designers who want their APIs to be LLM-accessible.

## 1. Introduction

As large language models become integral to software development workflows, a new design consideration emerges: *How well can an LLM generate correct code using this API?* Traditional API design focuses on human ergonomics—discoverability, consistency, documentation. We propose that LLM-friendliness should be an explicit design goal, measured through automated testing.

This paper presents three contributions:

1. **The Ralph Wiggum Loop**: An iterative methodology for improving code generation prompts using objective validation
2. **A test harness**: Pattern-based validation for both compilation correctness and semantic accuracy
3. **Design principles**: Specific API patterns that improve LLM code generation quality

### 1.1 Motivation

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

### 2.2 Validation Types

We employ two validation strategies:

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

### 2.3 Multi-LLM Testing

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

| LLM | Go Tests | CLI Tests | Total | Rate |
|-----|----------|-----------|-------|------|
| Claude (Opus 4.5) | 15/15 | 15/15 | 30/30 | 100% |
| Gemini | 12/15 | 15/15 | 27/30 | 90% |

### 5.2 Common Failure Modes

Analysis of Gemini failures revealed:

**GO-02 (Top N with sort):** Generated correct logic but used slightly different pattern
**GO-09 (Distinct + union):** Used `ssql.FromSlice` (non-existent) instead of `slices.Values()`
**CLI-13 (Distinct union):** Minor flag ordering difference

These failures led to prompt improvements:
- Added explicit reminder about `slices.Values()` vs non-existent `FromSlice`
- Expanded pattern alternatives to accept semantically equivalent code

### 5.3 Iteration History

| Version | Tests | Claude | Gemini | Changes |
|---------|-------|--------|--------|---------|
| v1 | 20 | 18/20 | N/A | Initial prompt |
| v2 | 20 | 20/20 | N/A | Fixed stdin bug, pattern matching |
| v3 | 30 | 30/30 | 27/30 | Added 10 tests, multi-LLM |

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

### 6.3 Limitations

- Test cases are synthetic; real user prompts may reveal gaps
- Pattern matching can't verify semantic correctness, only presence of expected patterns
- Multi-LLM testing limited to models with CLI interfaces
- Go-specific findings may not generalize to other languages

### 6.4 Future Work

1. **Semantic validation:** Execute generated code against test data
2. **Fuzzing:** Generate random valid prompts to find edge cases
3. **Cross-language:** Apply methodology to TypeScript, Python versions
4. **User study:** Compare code generation quality with/without LLM-friendly design

## 7. Related Work

- **GitHub Copilot evaluations:** Benchmark studies on code completion accuracy
- **API usability research:** Human factors in API design
- **Prompt engineering:** Techniques for improving LLM outputs
- **Type-directed synthesis:** Using types to guide code generation

Our work differs in explicitly designing the underlying API to improve LLM generation, rather than improving prompts for fixed APIs.

## 8. Conclusion

We presented a methodology for designing and validating LLM-friendly APIs. The Ralph Wiggum Loop provides an iterative process for improving code generation prompts with objective validation. Our case study with ssql demonstrates that specific design choices—SQL-style naming, encapsulated types, functional composition—significantly improve LLM code generation quality.

As LLM-assisted programming becomes ubiquitous, library designers should consider LLM-friendliness alongside traditional usability metrics. The techniques presented here provide a practical framework for measuring and improving this new dimension of API quality.

## Appendix A: Implementation

The test harness is implemented in `scripts/test-ai-prompts.sh` (~400 lines of Bash). Test cases are defined in `doc/ai-test-cases.md` using a structured format. System prompts are in `doc/ai-code-generation.md` (Go) and `doc/ai-cli-generation.md` (CLI).

To run the test suite:

```bash
# Single LLM
./scripts/test-ai-prompts.sh --llm claude

# All supported LLMs
./scripts/test-ai-prompts.sh --all-llms

# With interactive fix application
./scripts/test-ai-prompts.sh --apply-fixes
```

## Appendix B: Example Test Case

```markdown
### GO-01: Basic Filter and Aggregate

**Prompt:**
Read a CSV file of employees. Filter to those over 30 years old.
Group by department. Count employees in each department.
Write results to stdout as JSON.

**Expected patterns:**
- `ssql.ReadCSV(`
- `ssql.Where(`
- `ssql.GroupByFields(`
- `ssql.Count()`
- `ssql.GetOr(`
- `int64(0)` or `int64(`

**Negative patterns:**
- `streamv3.`
- `r["field"]`

**Validation:** compile
```

## Appendix C: System Prompt Structure

The Go code generation prompt (~900 lines) includes:

1. **Module context:** Import paths, version information
2. **Core type documentation:** Record, MutableRecord, Filter
3. **Operation reference:** All functions with signatures and examples
4. **Critical patterns:** Type-safe field access, composition
5. **Anti-patterns:** What NOT to generate (old APIs, wrong types)
6. **Complete examples:** 6 full programs showing common patterns
7. **Pattern recognition table:** Natural language → API mapping

The CLI prompt (~400 lines) follows a similar structure for pipeline commands.
