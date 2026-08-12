# JSON Serialization Performance Analysis

Reference: DFC015
Created: 2025-11-04
Last modified: 2026-03-20

[Back to Index](./README.md)

**Date:** November 4, 2025
**Author:** Research based on performance benchmarking and code analysis
**Status:** Analysis and Recommendations

## Executive Summary

ssql has two different JSON serialization implementations with significantly different performance characteristics:

1. **`lib.WriteJSONL`** - Fast, simple CLI path (9.7s for 430K rows)
2. **`ssql.WriteJSONToWriter`** - Slower, feature-rich library path (15.7s for 430K rows)

The **62% performance difference** for simple CSV→JSON conversion is due to different design goals: CLI optimization vs. library flexibility. This document analyzes the trade-offs and proposes future actions.

## Current Implementations

### 1. CLI Path: `lib.WriteJSONL`

**Location:** `cmd/ssql/lib/jsonl.go`

**Implementation:**
```go
func WriteJSONL(w io.Writer, records iter.Seq[ssql.Record]) error {
    writer := bufio.NewWriter(w)  // ✅ Buffered
    defer writer.Flush()

    for record := range records {
        data := make(map[string]interface{})

        // Simple field conversion
        for k, v := range record.All() {
            data[k] = convertRecordValue(v)
        }

        // Marshal and write
        jsonBytes, err := json.Marshal(data)
        // ... write to buffer
    }
}

func convertRecordValue(v interface{}) interface{} {
    switch val := v.(type) {
    case ssql.Record:
        // Nested records → recursive conversion
    case int64, float64, bool, string, nil:
        return val  // ✅ Canonical types pass through
    default:
        return fmt.Sprintf("%v", v)  // ⚠️ iter.Seq → memory address!
    }
}
```

**Characteristics:**
- ✅ **Buffered I/O** - Uses `bufio.NewWriter`
- ✅ **Simple conversion** - Direct passthrough for canonical types
- ✅ **Minimal allocations** - No intermediate MutableRecord creation
- ❌ **Cannot serialize iter.Seq** - Outputs memory address like `"0x4f5080"`
- ❌ **Cannot handle JSONString** - Treats as regular string

**Performance:** Fast for simple data (9.7s for 430K rows CSV→JSON)

---

### 2. Library Path: `ssql.WriteJSONToWriter`

**Location:** `io.go:433-462`

**Implementation:**
```go
func WriteJSONToWriter(sb iter.Seq[Record], writer io.Writer) error {
    encoder := json.NewEncoder(writer)  // ⚠️ May not be buffered

    for record := range sb {
        jsonRecord := MakeMutableRecord()  // 🔴 Allocation per record

        for key, value := range record.All() {
            switch v := value.(type) {
            case JSONString:
                // Parse JSONString to avoid double-encoding
                if parsed, err := v.Parse(); err == nil {
                    jsonRecord.fields[key] = parsed
                } else {
                    jsonRecord.fields[key] = string(v)
                }
            default:
                if isIterSeq(value) {
                    // ✅ Materialize iter.Seq → array
                    jsonRecord.fields[key] = materializeSequence(value)
                } else {
                    jsonRecord.fields[key] = value
                }
            }
        }

        encoder.Encode(jsonRecord.Freeze())
    }
}
```

**Characteristics:**
- ⚠️ **Potentially unbuffered** - Caller must wrap writer in `bufio.Writer`
- ✅ **Full feature support** - Handles JSONString, iter.Seq, nested Records
- 🔴 **High allocation cost** - Creates MutableRecord for every record
- 🔴 **Complex field processing** - Switch statement + type checks per field
- ✅ **Materialize iter.Seq** - Converts `iter.Seq[T]` → `[]T` for JSON compatibility

**Performance:** Slower for simple data (15.7s for 430K rows CSV→JSON)

---

## Performance Benchmark Results

### Test Case 1: Simple CSV→JSON (430K rows)

**File:** `run_correlator_min.20190611.csv` (430,364 rows, 50MB)

| Implementation | Time | Performance |
|----------------|------|-------------|
| CLI (`lib.WriteJSONL`) | 9.7s | **Baseline** |
| Generated (`WriteJSONToWriter`) | 15.7s | **62% slower** |

**Why CLI is faster:**
- No MutableRecord allocation (430K × avoided allocation)
- No complex field type checking
- Direct `json.Marshal` vs. `json.Encoder` with preprocessing

---

### Test Case 2: Pipeline with GroupBy + Aggregate (430K rows)

**Pipeline:** `read-csv | group-by -by value -func count | write-csv`

| Implementation | Time | Speedup |
|----------------|------|---------|
| CLI (3 processes, JSONL IPC) | 10.2s | Baseline |
| Generated (single process) | 2.7s | **3.8× faster** |

**Why generated code is faster:**
- No inter-process communication
- No serialization between pipeline stages
- Records stay in memory
- Direct function composition

---

## Design Trade-offs Analysis

### CLI Design Philosophy

**Goal:** Fast execution for common cases, simple implementation

**Constraints:**
- Commands communicate via stdin/stdout
- Must serialize/deserialize between processes
- iter.Seq fields never cross process boundaries (GroupBy → Aggregate happens in one command)

**Result:**
- Simple, fast JSON writer
- Limited to canonical types (int64, float64, string, bool, Record)
- Perfect for CLI pipelines where complex types are internal-only

---

### Library Design Philosophy

**Goal:** Maximum flexibility, support all ssql types

**Constraints:**
- Users might store iter.Seq fields and want to export them
- JSONString needs special handling to avoid double-encoding
- Must work with any writer (files, network, buffers)

**Result:**
- Feature-complete JSON writer
- Higher overhead for simple cases
- Necessary for complex library usage patterns

---

## Why iter.Seq Serialization Differs

### Experimental Test Results

```bash
# lib.WriteJSONL output (CLI)
{"group":"A","values":"0x4f5080"}  # ❌ Memory address!

# ssql.WriteJSONToWriter output (Library)
{"group":"B","values":[4,5,6]}     # ✅ Materialized array
```

### Why This Matters

**Current CLI Behavior:**
- `GroupByFields` creates iter.Seq field: `{"region": "North", "_group": <iter.Seq>}`
- `Aggregate` immediately consumes it: `{"region": "North", "total": 1000}`
- Output has no iter.Seq fields → `lib.WriteJSONL` limitation doesn't matter

**If we wanted cross-command iter.Seq:**
```bash
# Hypothetical future CLI command
ssql group-by -by region --no-aggregate | ssql custom-agg ...
```

This would require:
1. `WriteJSONToWriter` logic to materialize iter.Seq → arrays
2. `lib.ReadJSONL` to reconstruct arrays → iter.Seq when reading

---

## Code Generation Performance

### Simple Pipeline (read-csv only)

**CLI wins:** Simple path is faster than complex library path

```bash
# CLI: 9.7s
ssql read-csv data.csv > /dev/null

# Generated: 15.7s (uses WriteJSONToWriter)
ssql read-csv -g data.csv | ssql generate go > prog.go
go build prog.go && ./prog > /dev/null
```

**Root cause:** Generated code uses `WriteJSONToWriter` with complex field conversion overhead

---

### Complex Pipeline (read-csv | group-by | write-csv)

**Generated wins:** No IPC overhead, in-memory pipeline

```bash
# CLI: 10.2s (3 processes, 2× JSONL serialization)
ssql read-csv data.csv | ssql group-by ... | ssql write-csv

# Generated: 2.7s (single process, direct function composition)
export SSQLGO=1
ssql read-csv data.csv | ssql group-by ... | ssql write-csv | \
  ssql generate go > prog.go
go build prog.go && ./prog
```

**Why generated is 3.8× faster:**
- No process spawning overhead
- No JSONL serialization between stages
- Records stay in memory as Go objects
- Direct iterator composition

---

## Future Actions and Recommendations

### Immediate Optimizations

#### 1. Add Buffering to WriteJSONToWriter ⚡

**Current issue:** Generated code uses `WriteJSONToWriter(records, os.Stdout)` without buffering

**Fix:**
```go
// In generated code
writer := bufio.NewWriter(os.Stdout)
defer writer.Flush()
ssql.WriteJSONToWriter(records, writer)
```

**Expected improvement:** 10-20% faster (reduced syscall overhead)

**Effort:** Low (change code generation template)

---

#### 2. Create Fast Path for Simple Records 🚀

**Idea:** Add a simplified JSON writer for records with only canonical types

**Implementation:**
```go
// New function in io.go
func WriteJSONLSimple(sb iter.Seq[Record], writer io.Writer) error {
    // Similar to lib.WriteJSONL but in main package
    // No iter.Seq, no JSONString handling
    // Fast path for common case
}
```

**When to use:**
- Generated code for simple pipelines (no GroupBy before final output)
- CLI commands that don't need complex types
- User-facing API for fast JSON export

**Expected improvement:** Match CLI performance (9.7s vs 15.7s)

**Effort:** Medium (new function, need to update code generation logic)

---

#### 3. Smart Code Generation Selection 🎯

**Idea:** Code generator detects pipeline complexity and chooses appropriate writer

**Logic:**
```go
if pipelineHasGroupByWithoutAggregate() {
    // Use WriteJSONToWriter (can handle iter.Seq)
    generateCode("ssql.WriteJSONToWriter(records, writer)")
} else if pipelineHasOnlySimpleTypes() {
    // Use fast path
    generateCode("ssql.WriteJSONLSimple(records, writer)")
} else {
    // Use full-featured writer
    generateCode("ssql.WriteJSONToWriter(records, writer)")
}
```

**Expected improvement:** Best of both worlds (fast for simple, full-featured when needed)

**Effort:** High (requires pipeline analysis in code generator)

---

### Medium-term Enhancements

#### 4. Unified Serialization Strategy 🔄

**Goal:** Single high-performance implementation that handles all cases

**Approach:**
```go
type SerializationOptions struct {
    MaterializeSequences bool  // false = skip, true = expand to arrays
    BufferSize          int    // default 4096
    HandleJSONString    bool   // Parse JSONString to avoid double-encoding
}

func WriteJSONWithOptions(records iter.Seq[Record], w io.Writer, opts SerializationOptions) error {
    // Single implementation with configurable behavior
    // Optimize common paths (canonical types)
    // Handle complex types when needed
}
```

**Benefits:**
- Single code path to maintain
- CLI uses `MaterializeSequences: false` (current behavior)
- Library uses `MaterializeSequences: true` (full-featured)
- Generated code uses appropriate options

**Effort:** High (refactoring existing code, thorough testing)

---

#### 5. Cross-Process iter.Seq Support 🌉

**Use case:** Allow iter.Seq fields to flow between CLI commands

**Requirements:**
1. Serialize iter.Seq as JSON arrays on write
2. Detect array fields that were iter.Seq on read
3. Reconstruct as iter.Seq in next command

**Example:**
```bash
# Future possibility
ssql group-by -by region --output-groups | \
  ssql custom-process | \
  ssql aggregate -func custom
```

**Implementation:**
- `lib.WriteJSONL` learns to materialize iter.Seq
- `lib.ReadJSONL` optionally reconstructs arrays as iter.Seq
- Metadata to track which fields were sequences

**Benefits:**
- More flexible CLI pipelines
- Advanced users can process grouped data across commands

**Challenges:**
- Performance cost (materialization + reconstruction)
- API complexity (how to specify which fields to reconstruct?)
- May be unnecessary given generated code is better for complex pipelines

**Effort:** Very High (design, implementation, testing, documentation)

---

### Long-term Considerations

#### 6. Alternative Serialization Formats 📦

**Current:** JSONL for CLI inter-process communication

**Alternatives:**
1. **MessagePack** - Binary, faster, smaller
2. **Gob** - Go-native, preserves types
3. **Cap'n Proto** - Zero-copy serialization

**Analysis:**

| Format | Speed | Size | Type Safety | Interop |
|--------|-------|------|-------------|---------|
| JSONL | Baseline | Baseline | Low | ✅ High |
| MessagePack | 2-3× faster | 30% smaller | Medium | Good |
| Gob | 3-5× faster | 40% smaller | High | ❌ Go-only |
| Cap'n Proto | 5-10× faster | 50% smaller | High | Medium |

**Recommendation:**
- Keep JSONL as default (human-readable, debuggable, interoperable)
- Consider MessagePack as opt-in performance mode: `ssql --format=msgpack ...`

**Effort:** Very High (implement alternative formats, maintain compatibility)

---

#### 7. Streaming JSON Parser 🌊

**Current:** `json.Encoder`/`json.Decoder` buffer internally

**Alternative:** Use streaming parser (e.g., `github.com/json-iterator/go`)

**Benefits:**
- Lower memory usage
- Potentially faster for large records

**Risks:**
- Additional dependency
- May not be significantly faster for our use case

**Recommendation:** Benchmark before implementing

**Effort:** Medium (replace json package, test compatibility)

---

## Recommended Priority

### Phase 1: Quick Wins (Next Release)
1. ✅ Add buffering to generated code output
2. ✅ Create `WriteJSONLSimple` for fast path
3. ✅ Document performance characteristics

**Expected impact:** Match CLI performance for simple pipelines

---

### Phase 2: Medium-term (v1.3-1.4)
4. Implement smart code generation selection
5. Refactor toward unified serialization strategy

**Expected impact:** Best-of-both-worlds performance

---

### Phase 3: Future Research (v2.0+)
6. Evaluate cross-process iter.Seq support (may not be needed)
7. Benchmark alternative serialization formats
8. Consider streaming JSON parser

**Expected impact:** Depends on benchmarking results

---

## Conclusion

The current dual-implementation approach is a **reasonable design** that optimizes for different use cases:
- CLI path: Fast, simple, sufficient for current command design
- Library path: Full-featured, handles all types, necessary for advanced usage

The performance gap for simple operations (62% slower) is **fixable** through:
1. Better buffering in generated code
2. Fast path for simple records
3. Smart selection logic

The **real value** of code generation is in complex pipelines (3.8× faster), where eliminating IPC overhead matters more than JSON writer performance.

**Key insight:** Don't optimize the wrong thing. For simple `read-csv` operations, users should just use the CLI. Code generation shines for multi-stage pipelines where the 3.8× speedup justifies compilation.

---

## References

- Benchmark data: 430K row CSV file (`run_correlator_min.20190611.csv`)
- CLI implementation: `cmd/ssql/lib/jsonl.go`
- Library implementation: `io.go:433-462` (`WriteJSONToWriter`)
- Related research: `doc/research/serialization-formats.md`
- Code generation: `cmd/ssql/commands/generatego.go`
