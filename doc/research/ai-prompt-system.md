# AI Prompt Engineering System for ssql

Reference: DFC042
Created: 2026-01-28
Last modified: 2026-03-12

[Back to Index](./README.md)

## What We Built

ssql now has a structured system for getting LLMs to generate correct ssql code — both Go library code and CLI pipelines. The system has three parts:

1. **Two specialised prompts** — one for Go code, one for CLI pipelines
2. **A test suite** — 20 structured test cases with objective pass/fail criteria
3. **An automated improvement loop** — feeds failures back to the LLM to fix the prompt

The core problem this solves: LLMs hallucinate ssql APIs. They invent functions that don't exist (`ssql.GroupBy()`, `ssql.Join()`, `Count("field")`), use old import paths, access Record fields directly (which won't compile since Record is an encapsulated struct), and mix up SQL-style naming with functional programming conventions. The prompts eliminate these failure modes by showing correct patterns, explicitly listing wrong patterns, and providing enough examples that the LLM can pattern-match its way to correct code.

## How It Works

### The Two Prompts

**Go Code Generation** (`doc/ai-code-generation.md`, ~960 lines)

This prompt teaches an LLM to write Go programs that use the ssql library. It covers:

- Core types (Record, MutableRecord, iter.Seq, Filter)
- All operations with correct SQL-style names (Where not Filter, Limit not Take, SelectMany not FlatMap)
- The GroupByFields + Aggregate two-step pattern (the #1 source of hallucinations)
- Chain() composition for multi-step pipelines
- Signal processing (FFT, Spectrogram, Convolution, Kernels)
- Join operations (InnerJoin, LeftJoin, LookupJoin with OnFields/OnFieldPair)
- Distinct and Union (DistinctBy, Concat, RecordKey)
- 7 complete runnable examples
- An anti-patterns section that explicitly shows wrong code LLMs tend to generate

The key insight is that anti-patterns are more effective than examples alone. Showing `❌ ssql.Count("field")` next to `✅ ssql.Count()` prevents the hallucination directly.

**CLI Pipeline Generation** (`doc/ai-cli-generation.md`, ~470 lines)

This prompt teaches an LLM to compose ssql CLI commands via Unix pipes. It covers:

- The Source → Transform → Sink pipeline model
- All 21 data commands with their flags
- Critical patterns that are hard to get right:
  - Update if-elseif-else with `+` clause separators
  - Join multi-clause with `-` separators and `-as` renames
  - Expression language (`-if-expr`, `-set-expr`)
  - Signal processing commands (fft, spectrogram, convolve)
  - Code generation with `SSQLGO=1`
- Anti-patterns: old command names (read-csv → from), old flags (-match → -if), file arguments on transform commands
- 8 complete pipeline examples
- A pattern recognition table mapping natural language intent to ssql commands

### The Test Suite

`doc/ai-test-cases.md` defines 20 test cases (10 Go, 10 CLI). Each test case has:

- A natural language prompt (what the user would ask)
- Expected patterns (strings that must appear in the output)
- Negative patterns (strings that must NOT appear)
- A validation type: `compile` for Go code, `parse` for CLI pattern matching

Example test case:

```
### GO-01: Basic Filter and Aggregate

Prompt: Read employee data from employees.csv, filter for employees
with salary over 80000, group by department, and count employees
per department.

Expected patterns:
- ssql.ReadCSV("employees.csv")
- ssql.Where(
- ssql.GroupByFields(
- ssql.Count()
- ssql.Chain(
- if err != nil
- github.com/rosscartlidge/ssql/v4

Negative patterns:
- record["          (direct map access)
- ssql.Filter(      (wrong name)
- Count("           (Count takes no parameters)
```

The patterns are chosen to catch the most common LLM failures. Expected patterns verify the LLM used the right API. Negative patterns catch hallucinations. The Go tests additionally compile the generated code to catch syntax errors and type mismatches.

### The Ralph Wiggum Loop

`scripts/test-ai-prompts.sh` automates the improvement cycle:

```
1. Parse all test cases from doc/ai-test-cases.md
2. For each test:
   a. Construct a prompt: system prompt + test case description
   b. Feed to claude -p (non-interactive)
   c. Check output against expected/negative patterns
   d. For Go tests: compile the code
3. Collect all failures
4. If failures exist, feed them back:
   "These tests failed: [details]. Update the prompt to fix them."
5. Repeat until all pass or max iterations reached
```

The name comes from the idea of iteratively improving through simple feedback — the loop doesn't need to understand *why* something failed, just *that* it failed, and lets the LLM figure out the fix.

Key design choices:

- **`claude -p`** for non-interactive execution — each test is independent
- **Filesystem as memory** — prompts on disk, results in `/tmp`, changes tracked by git
- **Objective validation** — `go build` for Go code, pattern grep for CLI
- **Max iterations** (default 5) as a safety guardrail
- **Git diff** between iterations shows exactly what the LLM changed in the prompt

### The Validation Script

`scripts/validate-ai-patterns.sh` performs 12 static checks on generated Go code:

1. Correct import path (`ssql/v4`)
2. No wrong imports (rocketlaunchr, streamv3)
3. SQL-style naming (no Filter, FlatMap, Take, Skip)
4. Error handling for I/O operations
5. Correct GroupByFields API
6. Correct Aggregate API (parameterless Count)
7. Chain() for multi-step pipelines
8. No direct Record map access
9. Typed join functions (not bare Join)
10. Signal processing patterns (ExtractSignal with FFT)
11. No CLI-only patterns in Go code (ExprAgg)
12. Compilation test

This script can be used standalone on any generated Go file:

```bash
./scripts/validate-ai-patterns.sh /tmp/my_generated_code.go
```

## How to Use the Prompts

### Generating Go Code

Copy the entire contents of `doc/ai-code-generation.md` into your LLM conversation (or use it as a system prompt), then describe what you want:

```
[paste doc/ai-code-generation.md]

Generate a Go program that reads sensor_data.csv, computes the FFT
of the voltage field at 1000 Hz sample rate, and creates a chart
of the frequency spectrum.
```

The LLM will produce a complete, compilable Go program using the ssql library.

### Generating CLI Pipelines

Copy `doc/ai-cli-generation.md` and describe your task:

```
[paste doc/ai-cli-generation.md]

Read sales.csv, group by region, show count and total revenue
per region, sorted by revenue descending, output as a table.
```

The LLM will produce a pipeline like:

```bash
ssql from sales.csv \
  | ssql group-by -field region -count -sum revenue \
  | ssql sort -field revenue_sum -desc \
  | ssql to table
```

### Running the Test Suite

```bash
# Test both prompts
make ai-test

# Test only Go code generation
make ai-test-go

# Test only CLI pipelines
make ai-test-cli

# Dry run (show what would be tested)
./scripts/test-ai-prompts.sh all --dry-run

# Limit iterations
./scripts/test-ai-prompts.sh go --max-iterations 3
```

### Validating Generated Code

For a quick check of any Go file generated by an LLM:

```bash
./scripts/validate-ai-patterns.sh /path/to/generated.go
```

This catches the most common errors without needing to set up a full test module.

### Adding New Test Cases

Edit `doc/ai-test-cases.md` and add a new case following the existing format. Use the next sequential ID (GO-11, CLI-11, etc.). Each case needs:

- A natural language prompt
- Expected patterns (things the output must contain)
- Negative patterns (things the output must not contain)
- A validation type (`compile` or `parse`)

Then run `make ai-test` to see if the prompts handle the new case. If not, the Ralph Wiggum loop will attempt to fix the prompt automatically.

### Improving the Prompts

When you discover a new LLM failure pattern:

1. Add a test case for it in `doc/ai-test-cases.md`
2. Run `make ai-test` — the new test should fail
3. Either:
   - Let the Ralph Wiggum loop fix it automatically, or
   - Manually add an anti-pattern or example to the appropriate prompt
4. Re-run `make ai-test` to verify the fix
5. Check that existing tests still pass (no regressions)

The most effective prompt fixes are anti-patterns: showing `❌ WRONG` next to `✅ CORRECT` for the specific hallucination. Examples help too, but anti-patterns are more targeted.

## What We Learned

**Anti-patterns beat examples.** The original prompt (v1.0) had only examples and got 100% API correctness. But when we expanded coverage to signal processing and joins, examples alone weren't enough — LLMs would interpolate wrong APIs from the examples. Adding explicit "don't do this" sections eliminated those failures.

**Two prompts are better than one.** Go code generation and CLI pipeline generation have completely different failure modes. Go code fails on type safety (Record access, generics, error handling). CLI pipelines fail on command syntax (old flags, file arguments on transforms, clause separators). A single prompt couldn't address both effectively without becoming unwieldy.

**Objective validation matters.** "Does it compile?" is a better test than "does it look right?" For Go code, compilation catches type errors, missing imports, and wrong function signatures that pattern matching alone would miss. For CLI pipelines, pattern matching catches the most important errors (wrong commands, wrong flags) without needing to execute against real data.

**Filesystem as memory works.** The Ralph Wiggum loop stores prompts on disk and uses `claude -p` for each test. This means the LLM sees the full prompt every time (no context window issues), changes are tracked by git, and the loop can run unattended. The tradeoff is cost — each test case requires an API call — but for 20 tests this is manageable.
