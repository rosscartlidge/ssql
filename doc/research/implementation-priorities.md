# ssql Feature Implementation Priorities

## Overview

This document summarizes unimplemented features from the research documents, ranked by value/effort ratio and readiness for implementation.

## Priority 1: High Value, Ready to Implement

### 1.1 Custom Aggregation Expressions (group-by)

**Source:** `custom-aggregation-expressions.md`

**Status:** Design complete, ready for implementation

**Features:**
- `-expr 'sum(salary)' total` - batch mode with field arrays
- `-stream-expr '{s:0}' '{s:s+salary}' 's' total` - streaming mode for large data

**Value:** High
- Enables complex aggregations not possible with built-in functions
- Closes gap with SQL expressiveness
- Natural extension of existing expr-lang integration

**Effort:** 2-3 days
- Flag registration and parsing
- Batch expression environment (field arrays + `_records`)
- Streaming state machine
- Code generation support

**Implementation Order:**
1. Add `-expr` flag and batch mode
2. Add `-stream-expr` flag and streaming mode
3. Add code generation for both
4. Tests and documentation

---

### 1.2 JSONL Schema Headers

**Source:** `jsonl-schema-header.md`

**Status:** Design complete, phased approach ready

**Features:**
- Optional `--schema` flag on `from` to emit schema header
- Schema passthrough for filter commands
- Schema transformation for structural commands
- Accurate field completion at any pipeline position

**Value:** Very High
- Enables field completion mid-pipeline (currently only works at first command)
- Consistent field ordering in CSV output
- Type validation and better error messages
- Self-documenting data streams

**Effort:** 2-3 weeks (phased)

**Implementation Order:**
1. Phase 1: Schema type and basic read/write (2-3 days)
2. Phase 2: Pass-through commands (where, limit, offset, sort, distinct) (1-2 days)
3. Phase 3: Modifying commands (include, exclude, rename, cast, update) (3-4 days)
4. Phase 4: Complex commands (group-by, join, union) (4-5 days)
5. Phase 5: Output commands (to csv/json/table) (1-2 days)

---

## Priority 2: High Value, Moderate Effort

### 2.1 Typed Code Generation

**Source:** `typed-code-generation.md`

**Status:** Research complete, requires typed helper library first

**Features:**
- `SSQLGO=typed` generates struct-based code instead of map[string]any
- 35x speedup, 82,000x less memory allocation
- Schema inference from CSV/JSON files at generation time

**Value:** Very High
- 35x performance improvement for generated code
- Makes generated code viable for production workloads
- Original 14.6M record benchmark: 70s → 2s projected

**Effort:** 2-4 weeks

**Implementation Order:**
1. Phase 1: Create `ssql/typed` helper library (2-3 days)
2. Phase 2: Schema inference from files (2-3 days)
3. Phase 3: Type flow tracking through pipeline (3-4 days)
4. Phase 4: Typed code generation per command (5-7 days)
5. Phase 5: Testing and documentation (2-3 days)

**Dependencies:** None, but benefits from schema header work

---

### 2.2 Autocli Helper Methods

**Source:** `autocli-improvements.md`

**Status:** Design complete, requires autocli changes

**Features:**
- `ctx.GetAccumulatedStrings("-flag", "argname")` - type-safe access
- `ctx.GetAccumulatedMaps("-flag", "arg1", "arg2")` - multi-arg access
- `ctx.BindAccumulated("-flag", &structSlice)` - struct binding

**Value:** Medium
- Cleaner command handler code
- Fewer type assertions and error-prone casting
- Better developer experience

**Effort:** 1-2 days (in autocli)

**Implementation Order:**
1. Add helper methods to autocli Context
2. Update ssql commands to use new methods
3. Tests

**Note:** This requires changes to the autocli library

---

## Priority 3: High Value, High Effort

### 3.1 Schema-Aware Records (Performance)

**Source:** `schema-aware-records.md`

**Status:** Research complete, deferred

**Features:**
- Replace `map[string]any` with `[]any` + shared `*Schema`
- 2-10x speedup for record merging (joins)
- 1.3-4x memory reduction

**Value:** High for join-heavy workloads

**Effort:** 3-4 weeks
- Breaking change to core type
- Requires major version bump
- Extensive test updates

**Recommendation:** Defer until:
- Join performance is a common user complaint
- Major version planned anyway
- Typed code generation provides alternative path

---

### 3.2 GPU Acceleration

**Source:** `gpu-acceleration.md`

**Status:** Research complete, very high effort

**Features:**
- 100-1000x speedup for numeric operations
- Automatic CPU/GPU query planning
- Arrow format for efficient GPU transfer

**Value:** Very High for suitable workloads

**Effort:** 6-9 developer-months (21-30 weeks)
- Phase 1: Columnar/Arrow storage (4-6 weeks)
- Phase 2: GPU infrastructure (4-6 weeks)
- Phase 3: Query planner (3-4 weeks)
- Phase 4: Advanced GPU operations (6-8 weeks)
- Phase 5: Production hardening (4-6 weeks)

**Recommendation:**
- Start with Phase 1 (Arrow format) - provides 10-20x I/O benefit standalone
- Evaluate GPU phases based on user demand and resource availability

---

## Priority 4: Lower Priority / Research Items

### 4.1 Parallelization

**Source:** `my_ideas.md`

**Status:** Idea only, no design

**Features:**
- Parallel execution of independent pipeline stages
- Multi-core utilization

**Value:** Medium-High

**Effort:** Unknown, likely 2-4 weeks

**Dependencies:** May conflict with iterator-based design

---

### 4.2 Restartable Pipelines

**Source:** `my_ideas.md`

**Status:** Idea only, no design

**Features:**
- Checkpoint and resume long-running pipelines
- Fault tolerance

**Value:** Medium (for very long pipelines)

**Effort:** Unknown, likely 2-3 weeks

---

### 4.3 Method Chaining API

**Source:** `my_ideas.md`

**Status:** Idea only

**Features:**
- Fluent API for Go library usage
- Alternative to functional composition

**Value:** Low-Medium (functional API works well)

**Effort:** 1-2 weeks

---

## Recommended Implementation Roadmap

### Phase A: Quick Wins (1-2 weeks)

1. **Custom Aggregation Expressions** (Priority 1.1)
   - Complete `-expr` and `-stream-expr` for group-by
   - Builds on existing expr-lang integration
   - High user value for data analysis

### Phase B: Infrastructure (2-3 weeks)

2. **JSONL Schema Headers** (Priority 1.2)
   - Enables accurate mid-pipeline completion
   - Foundation for typed code generation
   - Improves type safety throughout

### Phase C: Performance (2-4 weeks)

3. **Typed Code Generation** (Priority 2.1)
   - 35x speedup for generated programs
   - Makes code generation production-viable
   - Major differentiator

### Phase D: Future (as needed)

4. **Autocli Improvements** - When autocli updated
5. **Arrow Format** - First step toward GPU
6. **Schema-Aware Records** - If join performance critical
7. **GPU Acceleration** - If user demand exists

---

## Summary Table

| Feature | Value | Effort | Ready? | Recommended |
|---------|-------|--------|--------|-------------|
| Custom Aggregation Expr | High | 2-3 days | ✅ Yes | **Start here** |
| Schema Headers | Very High | 2-3 weeks | ✅ Yes | **Next** |
| Typed Code Gen | Very High | 2-4 weeks | ✅ Yes | **Then this** |
| Autocli Helpers | Medium | 1-2 days | ⚠️ Need autocli | When convenient |
| Schema-Aware Records | High | 3-4 weeks | ⚠️ Breaking | Defer |
| GPU Acceleration | Very High | 6-9 months | ⚠️ Major project | Evaluate later |
| Parallelization | Medium | 2-4 weeks | ❌ No design | Research needed |

---

## Next Steps

1. **Implement custom aggregation expressions** (`-expr`, `-stream-expr`)
   - Design is complete in `custom-aggregation-expressions.md`
   - Can start immediately

2. **Review schema header design** with focus on:
   - Backward compatibility approach
   - Schema-only mode for completion
   - Integration with existing commands

3. **Prototype typed code generation** with:
   - Simple typed helper library
   - Schema inference from one CSV file
   - Typed where command as proof of concept
