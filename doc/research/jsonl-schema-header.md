# JSONL Schema Header Design

## Overview

Add an optional schema header record to JSONL pipeline output, similar to CSV headers but with type information.

## Current State

- Field types inferred from first data record
- Type tracking scattered across multiple locations:
  - `io.go`: `inferJSONFieldType()`, `coerceToType()`
  - `cmd/ssql/lib/json.go`: `readJSONArray()`, `readJSONLines()` with `fieldTypes` map
  - `cmd/ssql/lib/jsonl.go`: `inferJSONFieldType()`, `setValueWithType()`
  - `cmd/ssql/commands/update.go`: `schemaFields`, `newFieldTypes`, type coercion logic
  - `cmd/ssql/commands/helpers.go`: `applyValueToRecordWithTypeCheck()`, coercion functions
- Field order not guaranteed (Go maps are unordered)
- Schema determined by first record - if first record is atypical, problems ensue

## Proposed Format

```jsonl
{"_schema": {"fields": ["name", "age", "active"], "types": {"name": "string", "age": "int", "active": "bool"}}}
{"name": "Alice", "age": 30, "active": true}
{"name": "Bob", "age": 25, "active": false}
```

### Schema Record Structure

```go
type Schema struct {
    Fields []string            `json:"fields"` // Ordered field names
    Types  map[string]string   `json:"types"`  // Field -> type mapping
}
```

### Supported Types

- `string`
- `int` (stored as int64)
- `float` (stored as float64)
- `bool`
- `json` (for arrays/objects - preserved as-is)

## Detection

Schema record identified by presence of `_schema` key:
```go
if _, hasSchema := record["_schema"]; hasSchema {
    // This is a schema record
}
```

## Benefits

1. **Explicit schema** - No inference needed, types declared upfront
2. **Field ordering** - Consistent output to CSV/table
3. **Early validation** - Type mismatches caught on first record
4. **Self-documenting** - Pipeline data describes itself
5. **Simplified code** - Remove scattered inference logic
6. **Better errors** - "field 'age' expects int, got string" vs silent coercion

## Impact Analysis

### Files Requiring Changes

| File | Changes | Complexity |
|------|---------|------------|
| `cmd/ssql/lib/jsonl.go` | Add `ReadJSONLWithSchema()`, `WriteJSONLWithSchema()`, `Schema` type | Medium |
| `cmd/ssql/lib/json.go` | Update `ReadJSON()` to detect/parse schema header | Medium |
| `cmd/ssql/commands/from.go` | Emit schema header on output | Low |
| `cmd/ssql/commands/update.go` | Use schema instead of `schemaFields`/`newFieldTypes` | Medium - removes code |
| `cmd/ssql/commands/helpers.go` | May simplify - schema carries type info | Low |
| `cmd/ssql/commands/where.go` | Pass through schema header | Low |
| `cmd/ssql/commands/limit.go` | Pass through schema header | Low |
| `cmd/ssql/commands/offset.go` | Pass through schema header | Low |
| `cmd/ssql/commands/sort.go` | Pass through schema header | Low |
| `cmd/ssql/commands/distinct.go` | Pass through schema header | Low |
| `cmd/ssql/commands/include.go` | Update schema (remove fields) | Medium |
| `cmd/ssql/commands/exclude.go` | Update schema (remove fields) | Medium |
| `cmd/ssql/commands/rename.go` | Update schema (rename fields) | Medium |
| `cmd/ssql/commands/cast.go` | Update schema (change types) | Medium |
| `cmd/ssql/commands/group-by.go` | Generate new schema for output | Medium |
| `cmd/ssql/commands/join.go` | Merge schemas from both inputs | High |
| `cmd/ssql/commands/union.go` | Merge/validate schemas | High |
| `cmd/ssql/commands/to.go` | Use schema for field ordering | Medium |

### Commands by Impact Level

**Low (pass-through only):**
- `where`, `limit`, `offset`, `sort`, `distinct`
- Just need to pass schema record through unchanged

**Medium (schema modification):**
- `from` - emit schema
- `include`/`exclude` - filter schema fields
- `rename` - rename schema fields
- `cast` - change schema types
- `update` - potentially add fields to schema
- `to` - consume schema for output ordering

**High (schema creation/merging):**
- `group-by` - creates new schema based on grouping + aggregations
- `join` - merge schemas from two sources
- `union` - validate/merge schemas from multiple sources

## Implementation Plan

### Phase 1: Core Infrastructure (Low Risk)

1. Add `Schema` type to `cmd/ssql/lib/schema.go`:
   ```go
   type Schema struct {
       Fields []string          `json:"fields"`
       Types  map[string]string `json:"types"`
   }

   func NewSchema() *Schema
   func (s *Schema) AddField(name string, typ string)
   func (s *Schema) RemoveField(name string)
   func (s *Schema) RenameField(old, new string)
   func (s *Schema) SetType(name string, typ string)
   func (s *Schema) TypeOf(name string) string
   func (s *Schema) HasField(name string) bool
   func (s *Schema) WriteHeader(w io.Writer) error
   func ParseSchemaHeader(record map[string]any) (*Schema, bool)
   func InferSchema(record Record) *Schema
   ```

2. Add schema-aware JSONL reader:
   ```go
   func ReadJSONLWithSchema(r io.Reader) (schema *Schema, records iter.Seq[Record])
   ```

3. Add schema-aware JSONL writer:
   ```go
   func WriteJSONLWithSchema(w io.Writer, schema *Schema, records iter.Seq[Record]) error
   ```

### Phase 2: Pass-Through Commands

Update simple commands to detect and pass through schema:
- `where`, `limit`, `offset`, `sort`, `distinct`

Pattern:
```go
schema, records := lib.ReadJSONLWithSchema(os.Stdin)
filtered := ssql.Where(...)(records)
lib.WriteJSONLWithSchema(os.Stdout, schema, filtered)
```

### Phase 3: Schema-Modifying Commands

- `from` - infer and emit schema
- `include` - filter schema to included fields
- `exclude` - remove excluded fields from schema
- `rename` - update field names in schema
- `cast` - update field types in schema
- `update` - add new fields to schema

### Phase 4: Complex Commands

- `group-by` - create new schema from grouping keys + aggregation results
- `join` - merge schemas (handle field name conflicts)
- `union` - validate schemas match or merge compatible schemas

### Phase 5: Output Commands

- `to csv` - use schema for field ordering
- `to json` - use schema for field ordering
- `to table` - use schema for column ordering

## Backward Compatibility

**Option A: Always emit schema (breaking)**
- Simpler implementation
- Breaks existing pipelines that expect pure JSONL
- Other tools won't understand schema record

**Option B: Opt-in via flag (recommended)**
```bash
# New behavior with schema
ssql from data.csv --schema | ssql where ... | ssql to csv

# Legacy behavior without schema (default)
ssql from data.csv | ssql where ... | ssql to csv
```

**Option C: Detect and adapt**
- If input has schema, preserve it
- If no schema, work like today (infer from first record)
- `from` has flag to emit schema

## Estimated Effort

| Phase | Files | Effort |
|-------|-------|--------|
| Phase 1: Infrastructure | 1 new file | 2-3 hours |
| Phase 2: Pass-through | 5 commands | 1-2 hours |
| Phase 3: Modifying | 6 commands | 3-4 hours |
| Phase 4: Complex | 3 commands | 4-6 hours |
| Phase 5: Output | 1 command (to) | 1-2 hours |
| Testing | All | 2-3 hours |
| **Total** | | **13-20 hours** |

## Risks

1. **Breaking changes** - Need careful backward compatibility handling
2. **Schema drift** - What if data doesn't match schema?
3. **Performance** - Extra record to parse/emit (minimal impact)
4. **Complexity** - More state to manage through pipeline
5. **External tools** - Schema record may confuse other JSONL tools

## Alternatives Considered

### 1. Sidecar Schema File
```bash
ssql from data.csv --emit-schema > schema.json
ssql from data.csv | ssql validate --schema schema.json | ssql where ...
```
- Pro: Standard JSONL preserved
- Con: Extra file management, harder for pipelines

### 2. Environment Variable
```bash
export SSQL_SCHEMA='{"fields":["name","age"],"types":{"name":"string","age":"int"}}'
ssql from data.csv | ssql where ...
```
- Pro: No data format change
- Con: Awkward, size limits, doesn't flow through pipeline

### 3. JSON-LD Context (Standard)
```jsonl
{"@context": {"name": "xsd:string", "age": "xsd:integer"}, "@graph": [...]}
```
- Pro: Standards-based
- Con: Heavy, complex, overkill for this use case

## Recommendation

Implement **Option B (opt-in via flag)** with phases 1-3 first:

1. Add `--schema` flag to `from` command to emit schema header
2. All commands detect and pass through schema if present
3. Commands that modify structure update the schema
4. Default behavior (no flag) remains unchanged for compatibility

This gives us:
- Explicit schemas when wanted
- Backward compatibility
- Incremental adoption
- Clear field ordering for output

## Schema-Only Mode for Tab Completion

### The Problem

Currently, field completion only works for the first command in a pipeline:
```bash
ssql from users.csv | ssql where -where <TAB>  # Works - reads users.csv
ssql from users.csv | ssql include name age | ssql where -where <TAB>  # Doesn't know fields changed!
```

The `include` command reduced the available fields, but completion doesn't know that.

### The Solution: Schema Propagation for Completion

Run the pipeline with **schema-only mode** - no data records, just the schema header flowing through. Each command transforms the schema and passes it along. The final schema tells us exactly what fields are available at that point.

```bash
# Schema-only dry run
SSQL_SCHEMA_ONLY=1 ssql from users.csv | ssql include name age | ssql rename -as name username

# Output (no data, just schema transformation):
{"_schema": {"fields": ["username", "age"], "types": {"username": "string", "age": "int"}}}
```

### Implementation

**1. Environment variable triggers schema-only mode:**
```go
if os.Getenv("SSQL_SCHEMA_ONLY") == "1" {
    // Emit schema header only, no data records
    schema := inferSchemaFromSource(...)
    schema.WriteHeader(os.Stdout)
    return nil
}
```

**2. Each command in schema-only mode:**
- Reads schema header from stdin
- Transforms schema (e.g., `include` filters fields, `rename` renames them)
- Writes transformed schema to stdout
- Exits immediately (no data processing)

**3. Completion script integration:**
```bash
_ssql_complete() {
    # Get the partial pipeline up to cursor position
    local pipeline_prefix="ssql from users.csv | ssql include name age"

    # Run schema-only to get available fields at this point
    local schema=$(SSQL_SCHEMA_ONLY=1 eval "$pipeline_prefix" 2>/dev/null)

    # Extract fields from schema
    local fields=$(echo "$schema" | jq -r '._schema.fields[]')

    # Use fields for completion
    COMPREPLY=($(compgen -W "$fields" -- "${cur}"))
}
```

**4. Cache in environment variable (like current -cache):**
```bash
# After schema-only run, cache result
export SSQL_PIPELINE_FIELDS="username,age"
export SSQL_PIPELINE_TYPES="username:string,age:int"

# Subsequent completions use cache without re-running pipeline
```

### Benefits

1. **Accurate completion at ANY pipeline position** - not just first command
2. **Fast** - schema-only mode skips all data processing
3. **Type-aware** - completion could show type hints
4. **Composable** - works with any pipeline combination

### Example: Full Pipeline Completion

```bash
ssql from users.csv | ssql include name age status | ssql where -where status eq active | ssql rename -as name full_name | ssql where -where <TAB>
```

Schema flows through:
1. `from users.csv` → `{fields: [name, age, status, email, ...], types: {...}}`
2. `include name age status` → `{fields: [name, age, status], types: {...}}`
3. `where -where status eq active` → `{fields: [name, age, status], types: {...}}` (unchanged)
4. `rename -as name full_name` → `{fields: [full_name, age, status], types: {...}}`
5. `where -where <TAB>` → offers: `full_name`, `age`, `status`

The completion correctly shows `full_name` (not `name`) because it knows the rename happened!

### Schema Transformations by Command

| Command | Schema Transformation |
|---------|----------------------|
| `from` | Creates initial schema |
| `where` | Pass through (no change) |
| `limit` | Pass through |
| `offset` | Pass through |
| `sort` | Pass through |
| `distinct` | Pass through |
| `include` | Filter to specified fields |
| `exclude` | Remove specified fields |
| `rename` | Rename fields |
| `cast` | Change field types |
| `update -set newfield` | Add field to schema |
| `group-by` | New schema: group keys + aggregation results |
| `join` | Merge schemas from both inputs |
| `union` | Validate/merge schemas |

### Performance Considerations

- Schema-only mode is **instant** - no file reading beyond headers
- For CSV, only need to read header row
- For JSONL, only need to read first record (or schema header if present)
- Cache result in environment variable to avoid re-running

## Open Questions

1. Should schema include nullable/optional field markers?
2. How to handle schema conflicts in `union`?
3. Should `to csv` require schema for deterministic field order?
4. Error behavior: warn vs error on schema mismatch?
5. How to handle dynamic fields from expressions in schema-only mode?

## Next Steps

1. Review this design
2. Decide on backward compatibility approach
3. Implement Phase 1 (infrastructure)
4. Test with simple pipeline
5. Proceed with remaining phases
