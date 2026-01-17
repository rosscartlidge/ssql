# Arrow Integration Proposal for ssql

## Executive Summary

This proposal outlines how Apache Arrow can be integrated into ssql to provide:
1. **10-20x faster file I/O** via Arrow's zero-copy binary format
2. **Foundation for GPU acceleration** (Arrow is columnar, GPU-ready)
3. **Interoperability** with Python/Pandas, Spark, and other Arrow-compatible tools

The integration is designed to complement (not replace) the existing row-oriented streaming architecture and JSONL schema headers.

---

## Current Architecture

### Row-Oriented Streaming

```
┌─────────┐    ┌─────────┐    ┌─────────┐    ┌─────────┐
│  from   │───▶│  where  │───▶│ group-by│───▶│   to    │
│ (CSV)   │    │         │    │         │    │ (JSON)  │
└─────────┘    └─────────┘    └─────────┘    └─────────┘
     │              │              │              │
     ▼              ▼              ▼              ▼
  Record         Record         Record         Record
  Record         Record         Record         Output
  Record         Record           ▼
    ▼              ▼           (buffer)
 (stream)      (stream)
```

**Characteristics:**
- Lazy evaluation via `iter.Seq[Record]`
- Low memory footprint (one record at a time)
- Each Record is `map[string]any` (row-oriented)
- Good for: pipelines, streaming, unknown-size data

### JSONL Schema Headers

```json
{"_schema":{"fields":["name","age","salary"],"types":{"name":"string","age":"int","salary":"float"}}}
{"name":"Alice","age":30,"salary":75000}
{"name":"Bob","age":25,"salary":65000}
```

**Purpose:**
- Type hints for downstream commands
- Field completion in CLI
- Human-readable, streamable

---

## Proposed Arrow Integration

### Design Principles

1. **Additive** - Arrow is a new capability, not a replacement
2. **Opt-in** - Users choose when to use Arrow
3. **Interoperable** - Easy conversion between row/columnar
4. **Minimal API surface** - Few new functions, consistent with existing patterns

### Architecture with Arrow

```
                    ┌─────────────────────────────────────────┐
                    │           ssql Processing               │
                    ├─────────────────────────────────────────┤
                    │                                         │
┌─────────┐         │  ┌─────────────────────────────────┐   │         ┌─────────┐
│  CSV    │────────▶│  │     iter.Seq[Record]            │   │────────▶│  JSON   │
│  JSON   │         │  │     (row streaming)             │   │         │  CSV    │
│  JSONL  │         │  └─────────────────────────────────┘   │         │  JSONL  │
└─────────┘         │              ▲                         │         └─────────┘
                    │              │ Convert                 │
                    │              ▼                         │
┌─────────┐         │  ┌─────────────────────────────────┐   │         ┌─────────┐
│  Arrow  │◀───────▶│  │     Table (columnar)            │   │◀───────▶│  Arrow  │
│  (.arrow)         │  │     - For batch operations      │   │         │  (.arrow)
└─────────┘         │  │     - For GPU acceleration      │   │         └─────────┘
                    │  │     - For code generation       │   │
                    │  └─────────────────────────────────┘   │
                    │                                         │
                    └─────────────────────────────────────────┘
```

### What Arrow Provides

| Feature | Benefit |
|---------|---------|
| Binary format | 10-20x faster I/O than CSV/JSON |
| Zero-copy reads | Memory-mapped, no parsing |
| Columnar layout | Cache-efficient, SIMD-friendly |
| Schema embedded | Self-describing files |
| Compression | LZ4/ZSTD built-in |
| GPU-ready | Direct transfer to GPU memory |
| Ecosystem | Python, R, Spark, DuckDB interop |

---

## Implementation Phases

### Phase 1: Arrow I/O (1-2 weeks)

Add Arrow as a file format for `from` and `to` commands.

**CLI Usage:**
```bash
# Read Arrow file
ssql from data.arrow | ssql where -where age gt 30 | ssql to json

# Write Arrow file
ssql from data.csv | ssql to arrow output.arrow

# Convert between formats
ssql from data.csv | ssql to arrow data.arrow
ssql from data.arrow | ssql to csv data.csv
```

**Library API:**
```go
// Read Arrow file to Record stream
records, err := ssql.ReadArrow("data.arrow")

// Write Record stream to Arrow file
err := ssql.WriteArrow(records, "output.arrow")

// Read/write with io.Reader/io.Writer
records := ssql.ReadArrowFromReader(reader)
err := ssql.WriteArrowToWriter(records, writer)
```

**Implementation:**
- Use `github.com/apache/arrow/go/v14`
- Read: Arrow RecordBatch → iter.Seq[Record]
- Write: iter.Seq[Record] → Arrow RecordBatch → file
- Schema inferred from first record (or provided)

**Conversion cost:**
- Read: O(n) to convert columnar → row
- Write: O(n) to convert row → columnar
- Still faster than CSV parsing due to no text parsing

### Phase 2: Columnar Table Type (2-3 weeks)

Add internal `Table` type for batch operations.

**Library API:**
```go
// Table is a columnar representation of records
type Table struct {
    schema  *arrow.Schema
    columns []arrow.Array
    nrows   int
}

// Convert between representations
table := ssql.ToTable(records)      // Materializes stream
records := ssql.FromTable(table)    // Streams rows

// Direct Arrow access (for advanced users)
arrowTable := table.ArrowTable()
```

**Use cases:**
- Operations that need random access (sort, join)
- GPU transfer preparation
- Code generation targets

### Phase 3: Columnar Operations (3-4 weeks)

Implement key operations directly on columnar data.

**Operations to optimize:**
```go
// Columnar filter (no row conversion)
filtered := ssql.TableWhere(table, func(col arrow.Array) []bool {
    // Evaluate predicate on column
})

// Columnar aggregation
result := ssql.TableAggregate(table, "dept", map[string]ssql.AggregateFunc{
    "total": ssql.Sum("salary"),
})

// Columnar sort
sorted := ssql.TableSort(table, "age", ssql.Descending)

// Columnar join
joined := ssql.TableJoin(left, right, "id")
```

**Performance benefit:**
- Filter: 2-5x (no row allocation)
- Aggregate: 3-10x (cache-friendly reduction)
- Sort: 2-5x (radix sort on typed arrays)
- Join: 2-5x (hash table on typed keys)

### Phase 4: Code Generation Target (2-3 weeks)

Generate code that uses columnar representation.

**Current generated code (row-based):**
```go
records, _ := ssql.ReadCSV("data.csv")
filtered := ssql.Where(func(r ssql.Record) bool {
    return ssql.GetOr(r, "age", int64(0)) > 30
})(records)
```

**New generated code (columnar):**
```go
table, _ := ssql.ReadCSVToTable("data.csv")
ages := table.Int64Column("age")
mask := make([]bool, table.Len())
for i, age := range ages {
    mask[i] = age > 30
}
filtered := table.Filter(mask)
```

**Expected speedup:** 5-10x (typed arrays, no interface{}, cache-friendly)

---

## API Design

### New Types

```go
// Table represents columnar data (batch, not streaming)
type Table struct {
    // Private - access via methods
}

// Table creation
func ToTable(records iter.Seq[Record]) *Table
func ReadArrowToTable(filename string) (*Table, error)
func ReadCSVToTable(filename string) (*Table, error)

// Table access
func (t *Table) Len() int
func (t *Table) Schema() *Schema
func (t *Table) Column(name string) Column
func (t *Table) Int64Column(name string) []int64
func (t *Table) Float64Column(name string) []float64
func (t *Table) StringColumn(name string) []string
func (t *Table) BoolColumn(name string) []bool

// Table operations
func (t *Table) Filter(mask []bool) *Table
func (t *Table) Sort(field string, desc bool) *Table
func (t *Table) Slice(start, end int) *Table

// Convert back to streaming
func FromTable(t *Table) iter.Seq[Record]
func (t *Table) Records() iter.Seq[Record]
```

### New I/O Functions

```go
// Arrow file I/O
func ReadArrow(filename string) (iter.Seq[Record], error)
func ReadArrowFromReader(r io.Reader) iter.Seq[Record]
func WriteArrow(records iter.Seq[Record], filename string) error
func WriteArrowToWriter(records iter.Seq[Record], w io.Writer) error

// Direct to Table (no intermediate rows)
func ReadArrowToTable(filename string) (*Table, error)
func WriteTableToArrow(table *Table, filename string) error
```

### CLI Commands

```bash
# from command - auto-detects .arrow extension
ssql from data.arrow

# to command - new arrow subcommand
ssql to arrow output.arrow
ssql to arrow output.arrow -compress lz4
ssql to arrow output.arrow -compress zstd

# Explicit format override
ssql from data.arrow --format arrow
ssql from data.bin --format arrow  # Force Arrow parsing
```

---

## Example Workflows

### Workflow 1: Fast Format Conversion

```bash
# Convert large CSV to Arrow for repeated use
ssql from huge.csv | ssql to arrow huge.arrow

# Subsequent queries are 10-20x faster to start
ssql from huge.arrow | ssql where -where status eq active | ssql to json
```

### Workflow 2: Python/Pandas Interop

```python
# Python side
import pyarrow as pa
import pandas as pd

df = pd.DataFrame({'name': ['Alice', 'Bob'], 'age': [30, 25]})
table = pa.Table.from_pandas(df)
pa.ipc.write_file(table, 'data.arrow')
```

```bash
# ssql side
ssql from data.arrow | ssql where -where age gt 25 | ssql to arrow result.arrow
```

```python
# Back to Python
result = pa.ipc.open_file('result.arrow').read_all().to_pandas()
```

### Workflow 3: Code Generation with Columnar

```bash
# Generate columnar code (future)
SSQLGO=columnar ssql from data.csv | ssql where -where age gt 30 | ssql generate-go
```

```go
// Generated code uses Table API
func main() {
    table, _ := ssql.ReadCSVToTable("data.csv")
    ages := table.Int64Column("age")

    mask := make([]bool, table.Len())
    for i := range ages {
        mask[i] = ages[i] > 30
    }

    result := table.Filter(mask)
    ssql.WriteTableToJSON(result, os.Stdout)
}
```

---

## Relationship to JSONL Schema Headers

Arrow and JSONL schema headers serve different purposes:

| Aspect | JSONL Schema | Arrow |
|--------|--------------|-------|
| Use case | CLI pipeline metadata | High-performance storage |
| Format | Text (JSON) | Binary |
| Streaming | ✅ Yes | ❌ No (batched) |
| Human readable | ✅ Yes | ❌ No |
| Schema location | First line | File header |
| Compression | ❌ No | ✅ Built-in |
| Zero-copy | ❌ No | ✅ Yes |

**They complement each other:**
- JSONL+schema for: CLI pipelines, debugging, small data, streaming
- Arrow for: large files, repeated access, interop, GPU preparation

**No changes needed to JSONL schema headers** - Arrow integration is additive.

---

## Dependencies

```go
// go.mod addition
require github.com/apache/arrow/go/v14 v14.x.x
```

**Arrow Go package size:**
- Core: ~5MB compiled
- With compression: ~7MB compiled

**Build tags option:**
```go
//go:build arrow

// Arrow support can be optional via build tag
```

---

## Performance Expectations

### I/O Performance (10M records, 10 fields)

| Operation | CSV | JSON | Arrow | Arrow (mmap) |
|-----------|-----|------|-------|--------------|
| Read | 5-10s | 8-12s | 0.3-0.5s | 0.1s |
| Write | 3-5s | 5-8s | 0.2-0.3s | 0.2s |
| File size | 500MB | 800MB | 280MB | 280MB |

### Processing Performance

| Operation | Row-based | Columnar | Speedup |
|-----------|-----------|----------|---------|
| Filter (numeric) | 1.0x | 2-5x | 2-5x |
| Aggregate | 1.0x | 3-10x | 3-10x |
| Sort | 1.0x | 2-5x | 2-5x |
| Join | 1.0x | 2-5x | 2-5x |

### End-to-End (14.6M record benchmark)

| Pipeline | Current | With Arrow I/O | With Columnar Ops |
|----------|---------|----------------|-------------------|
| Read + filter + aggregate | 15s | 8s | 3s |
| Read + 3x join | 70s | 40s | 15s |

---

## Risks and Mitigations

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Arrow Go API changes | Low | Medium | Pin version, abstract interface |
| Binary size increase | Medium | Low | Optional build tag |
| Memory for materialization | Medium | Medium | Chunked processing, streaming fallback |
| Learning curve | Low | Low | Keep row API as primary |

---

## Implementation Recommendation

### Start with Phase 1 (Arrow I/O)

**Minimal viable integration:**
1. Add `ssql from *.arrow` support
2. Add `ssql to arrow` command
3. Document Arrow format benefits

**Effort:** 1-2 weeks

**Immediate value:**
- 10-20x faster repeated reads
- Python/Pandas interop
- Foundation for columnar/GPU work

### Defer Phases 2-4 until:
- Arrow I/O proves valuable
- GPU acceleration is prioritized
- Typed code generation is prioritized

---

## Decision Points

**Before starting:**
1. Is Arrow I/O sufficient, or do we need columnar operations?
2. Should Arrow be optional (build tag) or always included?
3. What Arrow version to target? (v14 is current stable)

**After Phase 1:**
1. Measure actual I/O improvement on real workloads
2. Evaluate memory impact of Table materialization
3. Assess user demand for columnar operations

---

## Summary

| Phase | Effort | Benefit | Dependency |
|-------|--------|---------|------------|
| 1: Arrow I/O | 1-2 weeks | 10-20x I/O | None |
| 2: Table type | 2-3 weeks | Foundation for ops | Phase 1 |
| 3: Columnar ops | 3-4 weeks | 2-10x processing | Phase 2 |
| 4: Code gen | 2-3 weeks | 5-10x generated code | Phase 2 |

**Recommendation:** Start with Phase 1 to validate value before committing to deeper integration.
