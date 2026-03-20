---
title: "Building Production Go Tools with AI-Assisted Development"
subtitle: "A Case Study in ssql and autocli"
author: "Ross Cartlidge"
date: "Go Programming Conference 2026"
---

# The Challenge

## AI Coding Assistants: Promise vs Reality

**The Promise:**
- Dramatically accelerate development
- Handle tedious tasks
- Generate boilerplate code

**The Reality:**
- Context loss between sessions
- Hallucinated APIs
- Code sprawl and inconsistency
- Silent regressions

---

# What We Built

## Two Production Go Libraries

**ssql** (v4.0.0) - Stream Processing
- SQL-style API using Go 1.23+ iterators
- Unix pipeline CLI tool
- Self-generating code (CLI to compiled Go)

**autocli** (v4.3.3) - CLI Framework
- Fluent builder API
- Intelligent bash completion
- Field-aware suggestions from data files

---

# By The Numbers

## Project Statistics

| Metric | Value |
|--------|-------|
| Go source code | 27,663 lines |
| Test code | 6,305 lines |
| Documentation | 39,709 lines |
| Total releases | 73 |

**81,492 total lines** over several months of collaboration

---

# The CLAUDE.md Methodology

## Problem: AI Has No Memory

LLMs process each conversation independently:

- Forgets architectural decisions
- Uses outdated patterns
- Doesn't know your conventions

**Solution:** CLAUDE.md as persistent AI memory

---

# CLAUDE.md Structure

## What Goes In It

1. **Architectural decisions** with rationale
2. **API conventions** and naming patterns
3. **Anti-patterns** to avoid
4. **Code examples** of correct usage
5. **Development principles** learned from mistakes

ssql CLAUDE.md: **1,407 lines**

---

# Key Principle #1

## If It's Not Tested, It Will Break

**The Story:**
- Field completion worked for months
- AI refactored the code in v3.2.0
- Feature silently disappeared
- No test coverage = no detection

**The Fix:**
- Added TestFieldCompletionConfiguration
- Tests verify feature presence, not just correctness

---

# Key Principle #2

## Keep Generated Code Readable

**BAD: Inline complexity**
```go
result := ssql.LookupJoin(data, []ssql.LookupClause{
    {LeftField: "a", RightField: "b",
     FieldRenames: map[string]string{"x": "y"}},
})
```

**GOOD: Helper functions**
```go
result := ssql.LookupJoin(data, []ssql.LookupClause{
    ssql.Lookup("a", "b", "x", "y"),
})
```

---

# Code Generation

## CLI to Compiled Go

```bash
# Prototype with CLI
ssql from data.csv | ssql where -if age gt 25

# Generate production code
export SSQLGO=1
ssql from data.csv | ssql where -if age gt 25 | \
  ssql generate go > program.go

go build -o program program.go
./program  # 2.6x faster!
```

---

# Performance Results

## Real-World Benchmark

Enriching 10M+ records with multiple joins:

| Method | Time | Speedup |
|--------|------|---------|
| CLI pipeline | 2m 15s | baseline |
| Generated Go | 52s | **2.6x faster** |

Prototype fast, deploy faster.

---

# Human-AI Workflow

## Five Phases

1. **Problem Definition** (Human-Led)
2. **Design Exploration** (AI-Led, Human-Reviewed)
3. **Implementation** (AI-Led)
4. **Review and Refinement** (Human-Led)
5. **Knowledge Capture** (Both)

---

# Quality Gates

## Before Merging AI Code

- All existing tests pass
- New tests for new features
- Code is readable (not over-abstracted)
- Documentation updated
- CLAUDE.md updated if patterns discovered
- Breaking changes documented

---

# What Worked

## Successful Patterns

1. **CLAUDE.md as AI memory** - Essential for consistency
2. **Test everything worth keeping** - Prevents silent regressions
3. **Helper functions** - Keeps generated code clean
4. **Human reviews architecture** - AI implements
5. **Anti-pattern documentation** - Prevents repeated mistakes

---

# What Didn't Work

## Lessons Learned

1. **Trusting AI "improvements"** - Always verify
2. **Complex inline generation** - Became unreadable
3. **Missing context** - Inconsistent choices
4. **Skipping tests** - Silent regressions
5. **Over-abstraction** - AI tends to over-engineer

---

# Case Study: Multi-Clause Joins

## The Problem

```bash
# Old: Reads kind.csv TWICE
ssql join <(ssql from kind.csv) -on a_kind |
ssql join <(ssql from kind.csv) -on z_kind
```

## The Solution

```bash
# New: Reads kind.csv ONCE
ssql join <(ssql from kind.csv) \
  -on a_kind kind -as kind_name a_kind_name \
  - \
  -on z_kind kind -as kind_name z_kind_name
```

**Result:** 25% faster, cleaner syntax

---

# Case Study: AI Self-Improvement

## The Problem

LLMs hallucinate wrong APIs:
```go
// WRONG - this API doesn't exist!
ssql.GroupByFields([]string{"dept"},
    []ssql.Aggregation{ssql.Count("count")})
```

## The Solution

Added anti-patterns to AI prompt:
- Document exact wrong code
- Show correct alternative
- AI learns from its mistakes

---

# The Development Principles

## Seven Rules

1. Document everything in CLAUDE.md
2. Test everything you want to keep
3. Prefer compile-time type safety
4. Keep generated code simple and readable
5. Human reviews architecture, AI implements
6. Atomic changes with comprehensive commits
7. Update documentation alongside code

---

# Conclusion

## AI Augments, Not Replaces

**The Results:**
- 27,000+ lines of production Go code
- 40,000+ lines of documentation
- 2.6x performance improvement
- Clean API evolution through 4 versions

**The Key:** Treat AI as a capable but forgetful collaborator

---

# The Human-AI Team

## What Each Brings

**Human:**
- Architectural direction
- Quality review
- Business context
- Final decisions

**AI:**
- Fast implementation
- Tedious tasks (40+ file updates)
- Creative solutions
- Comprehensive documentation

---

# Resources

## Get Started

**Repositories:**
- github.com/rosscartlidge/ssql
- github.com/rosscartlidge/autocli

**Key Files:**
- CLAUDE.md in each repo
- doc/ai-human-guide.md
- doc/ai-code-generation.md

---

# Questions?

## Contact

**Ross Cartlidge**

- GitHub: @rosscartlidge
- ssql: github.com/rosscartlidge/ssql
- autocli: github.com/rosscartlidge/autocli

*"This is not AI replacing developers—it's AI augmenting them."*
