# AI Prompt Engineering System

**Status:** Production Ready
**Last Updated:** 2026-01-28

---

## Overview

ssql uses a **two-prompt system** for AI code generation:

| Prompt | File | Purpose |
|--------|------|---------|
| **Go Code Generation** | `doc/ai-code-generation.md` | Generate Go programs using the ssql library |
| **CLI Pipeline Generation** | `doc/ai-cli-generation.md` | Generate ssql CLI pipelines (Unix-style) |

Both prompts are validated against structured test cases using the Ralph Wiggum methodology for iterative improvement.

---

## When to Use Which

| Scenario | Use |
|----------|-----|
| Building a Go application | Go Code prompt |
| Processing data interactively | CLI Pipeline prompt |
| Prototyping a pipeline | CLI Pipeline prompt |
| Generating production code | Go Code prompt |
| Quick one-off data analysis | CLI Pipeline prompt |
| Teaching ssql patterns | Go Code prompt |
| FFT/signal analysis | Either (both cover signal processing) |

**Decision rule:** If the output should be a `.go` file, use the Go prompt. If the output should be a shell command, use the CLI prompt.

---

## How to Use

### For LLMs (Claude, GPT-4, etc.)

Copy the entire contents of the appropriate prompt file into your LLM conversation, then describe what you want.

```
[Paste full doc/ai-code-generation.md or doc/ai-cli-generation.md]

Now generate: <your natural language description>
```

### For Developers

Use the prompts when you need to:
- Generate ssql code examples
- Create data processing pipelines
- Build prototypes quickly
- Learn ssql patterns and API conventions

---

## Ralph Wiggum Methodology

The prompts are improved using an iterative feedback loop:

```
┌─────────────────────────────────────────┐
│  1. Run test cases against prompt       │
│  2. Validate output (compile / parse)   │
│  3. Collect failures                    │
│  4. Feed failures back to LLM          │
│  5. LLM updates the prompt             │
│  6. Repeat until all tests pass         │
└─────────────────────────────────────────┘
```

**Key properties:**
- **Objective validation**: Go code must compile; CLI pipelines must match expected patterns
- **Filesystem as memory**: Prompts live on disk, results in `/tmp`, changes tracked by git
- **Convergence**: Max iteration limit prevents infinite loops
- **Transparency**: `git diff` shows exactly what changed between iterations

---

## Running Tests

```bash
# Run all tests (Go + CLI)
make ai-test

# Run only Go code generation tests
make ai-test-go

# Run only CLI pipeline generation tests
make ai-test-cli

# Dry run (show test cases without executing)
./scripts/test-ai-prompts.sh all --dry-run

# Direct invocation with options
./scripts/test-ai-prompts.sh go --max-iterations 3
```

### Prerequisites

- `claude` CLI installed (`claude -p` for non-interactive mode)
- `go` compiler (for Go code validation)
- ssql built (`go build ./cmd/ssql`)

---

## Test Case Catalog

Test cases are defined in `doc/ai-test-cases.md` with 20 structured tests:

### Go Code Tests (10 cases)

| ID | Description | Key Patterns |
|----|-------------|-------------|
| GO-01 | Basic filter + aggregate | Where, GroupByFields, Count, Chain |
| GO-02 | Top N with sort | SortBy, Limit, descending |
| GO-03 | Signal processing FFT | FFT, SpectrumToRecords, ExtractSignal |
| GO-04 | Spectrogram analysis | Spectrogram, SpectrogramOptions, HannWindow |
| GO-05 | Update with computed fields | Update, MutableRecord, Freeze |
| GO-06 | Join with lookup | InnerJoin/LeftJoin, OnFields/OnFieldPair |
| GO-07 | Conditional update | Update, conditional logic, String setter |
| GO-08 | JSON I/O pipeline | ReadJSON, WriteJSON, Where |
| GO-09 | Convolution pipeline | ConvolveSame, GaussianKernel, ExtractSignal |
| GO-10 | Distinct + union | DistinctBy, Concat, RecordKey |

### CLI Pipeline Tests (10 cases)

| ID | Description | Key Patterns |
|----|-------------|-------------|
| CLI-01 | Basic filter pipeline | from, where, include, to table |
| CLI-02 | Group-by with aggregation | group-by, -count, -sum, -avg |
| CLI-03 | Update if-else clauses | update, -where, -set, + separator |
| CLI-04 | Signal processing FFT | fft, -field, -rate |
| CLI-05 | Spectrogram | spectrogram, -window-size, -rate |
| CLI-06 | Join with rename | join, -on, -as |
| CLI-07 | Expression filter | -where-expr, -set-expr |
| CLI-08 | Sort + limit + offset | sort, limit, offset |
| CLI-09 | Code generation | SSQLGO=1, generate-go |
| CLI-10 | Multi-format pipeline | from CSV, to json |

---

## Improvement Cycle

When a test fails:

1. **Identify the failure**: Pattern missing? Wrong pattern present? Compile error?
2. **Diagnose the root cause**: Is the prompt unclear? Missing an example? Missing an anti-pattern?
3. **Update the prompt**: Add the missing pattern, clarify the instruction, or add a new example
4. **Re-run tests**: `make ai-test` to verify the fix
5. **Check for regressions**: Ensure fixing one test doesn't break others

The Ralph Wiggum loop automates steps 1-4, but manual review is recommended for understanding why failures occur.

---

## Validation Script

`scripts/validate-ai-patterns.sh` performs static checks on generated Go code:

| Check | What it validates |
|-------|-------------------|
| Import path | Uses `ssql/v4` (not v3, not unversioned) |
| Wrong imports | No rocketlaunchr or streamv3 paths |
| SQL naming | Where not Filter, Limit not Take |
| Error handling | I/O operations have `if err != nil` |
| GroupByFields | Correct API (namespace, fields...) |
| Aggregate | Count() parameterless, proper map syntax |
| Composition | Chain() used for multi-step pipelines |
| Record access | No direct map access, no SetAny |
| Join functions | InnerJoin/LeftJoin, not bare Join |
| Signal processing | ExtractSignal used with FFT |
| CLI-only patterns | No ExprAgg in Go code |
| Compilation | Code compiles successfully |

Run manually: `./scripts/validate-ai-patterns.sh <file.go>`

---

## Version Tracking

| Version | Date | Go Pass Rate | CLI Pass Rate | Changes |
|---------|------|-------------|---------------|---------|
| 4.0 | 2026-01-28 | - | - | Two-prompt system, Ralph Wiggum loop, signal processing, join patterns |
| 3.0 | 2025-10-23 | 100% | N/A | Chain() enforcement, anti-patterns |
| 2.0 | 2025-10-23 | 100% | N/A | Hybrid prompt, 5 examples |
| 1.0 | 2025-10-22 | 100% | N/A | Initial anti-patterns prompt |

---

## File Inventory

| File | Purpose |
|------|---------|
| `doc/ai-code-generation.md` | Go code generation prompt |
| `doc/ai-cli-generation.md` | CLI pipeline generation prompt |
| `doc/AI-PROMPT-README.md` | This file - system documentation |
| `doc/ai-test-cases.md` | Structured test cases (20 tests) |
| `doc/ai-test-results.md` | Latest test run results (generated) |
| `scripts/test-ai-prompts.sh` | Ralph Wiggum loop runner |
| `scripts/validate-ai-patterns.sh` | Static validation for Go code |

---

## What's Covered

### Go Code Prompt (`ai-code-generation.md`)

- Core API reference (types, creation, access)
- I/O operations (CSV, JSON)
- All operations (Where, Select, Update, GroupByFields, Aggregate, Sort, Limit, etc.)
- Signal processing (FFT, Spectrogram, Convolution, Correlation, Kernels)
- Join operations (InnerJoin, LeftJoin, LookupJoin, OnFields, OnFieldPair)
- Distinct and Union (DistinctBy, Concat, RecordKey)
- Anti-patterns (hallucinated APIs, wrong names, old paths)
- Composition rules (Chain for multi-step, Pipe for type changes)
- 7 complete examples
- Pattern recognition table

### CLI Pipeline Prompt (`ai-cli-generation.md`)

- Pipeline architecture (Source -> Transform -> Sink)
- All 21 data commands with flags
- Critical patterns (I/O, where clauses, update if-else, join multi-clause, signal processing, code generation)
- Anti-patterns (old commands, old flags, file args on transforms)
- 8 complete examples
- Pattern recognition table (natural language -> ssql commands)
