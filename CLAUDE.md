# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current Version

**ssql v4 is the current major version.** Always use the `/v4` module path:

```bash
# Install the CLI
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# Import in Go code
import "github.com/rosscartlidge/ssql/v4"
```

## Repository Hygiene (CRITICAL)

**⚠️ IMPORTANT: Keep the root directory clean!**

**Test Programs and Experiments:**
- **NEVER** build test programs in the root directory
- **ALWAYS** use `/tmp/` for temporary test programs
- **Example:**
  ```bash
  # ✅ CORRECT - build in /tmp
  cat > /tmp/test_feature.go << 'EOF'
  package main
  ...
  EOF
  go run /tmp/test_feature.go

  # ❌ WRONG - don't build in root
  cat > test_feature.go << 'EOF'
  ...
  EOF
  go run test_feature.go  # Creates binary in root!
  ```

**Documentation:**
- **NEVER** create documentation files in the root directory
- **ALWAYS** put research docs in `doc/research/`
- **ALWAYS** put archived docs in `doc/archive/`
- **Example:**
  ```bash
  # ✅ CORRECT - docs in proper location
  cat > doc/research/new-feature-analysis.md << 'EOF'
  ...
  EOF

  # ❌ WRONG - don't create docs in root
  cat > NEW-FEATURE-ANALYSIS.md << 'EOF'  # NO!
  ...
  EOF
  ```

**What Belongs in Root:**
- Core library source: `*.go` (chart.go, core.go, io.go, operations.go, sql.go)
- Core tests: `*_test.go`
- Essential docs: `README.md`, `CHANGELOG.md` only
- Build files: `go.mod`, `go.sum`, `Makefile`, `.gitignore`

## Development Journal (CRITICAL)

**⚠️ IMPORTANT: Maintain weekly journal entries in `journal/`**

The journal tracks development work for continuity across sessions.

**On session startup:** Read the latest journal file to understand recent work:
```bash
ls -t journal/*.md | head -1 | xargs cat
```
This provides context about what was done in previous sessions, decisions made, and work in progress.

**File naming:** `journal/YYYY-WNN.md` (e.g., `2026-W04.md` for week 4 of 2026)

**When to update:**
- At the end of each work session
- When completing significant tasks
- When making commits

**What to record:**
```markdown
## YYYY-MM-DD (Day)

### Brief Description of Work

- Files modified
- Issues found and how they were resolved
- Commits made (hash and brief message)
- Decisions or learnings worth noting
```

**Example entry:**
```markdown
## 2026-01-23 (Thursday)

### Documentation Verification and Fixes

Tested CLI examples and fixed outdated references.

**Files modified:**
- doc/cli-codelab.md - removed non-existent -schema flag
- doc/advanced-tutorial.md - fixed SetField -> SetImmutable

**Commits:**
- `36ba82f` - docs: fix incorrect examples in CLI and advanced tutorial docs
```

**At start of new week:** Create a new file for the current week.

**Why this matters:** Provides context for future sessions about recent work, decisions made, and issues encountered.

**Compiled Binaries:**
- The `.gitignore` prevents compiled examples from being committed
- But still avoid creating them - use `/tmp/` for test programs
- Main `ssql` binary is built in root but ignored by git

## Documentation Maintenance (CRITICAL)

**⚠️ IMPORTANT: Keep documentation in sync with API and CLI changes!**

When making changes to the library API or CLI commands, you MUST also update the relevant documentation:

**Documentation files that must stay in sync:**
- `README.md` - Main library documentation, examples, and installation instructions
- `doc/api-reference.md` - Complete API reference with examples
- `doc/cli-codelab.md` - CLI tutorial with command examples
- `doc/cli-debugging.md` - CLI debugging examples
- `doc/cli-troubleshooting.md` - Common issues and solutions
- `doc/EXPRESSIONS.md` - Expression language documentation (user-facing)
- `doc/ai-code-generation.md` - AI code generation examples
- `doc/ai-human-guide.md` - Human-AI collaboration guide

**Research documents (internal reference):**
- `doc/research/expr-lang-reference.md` - Comprehensive expr-lang v1.17 reference (compile-time type checking, all functions, ssql integration patterns)
- `doc/research/jsonl-schema-header.md` - Design for JSONL schema headers and pipeline field completion

**What to update when changing:**
- **Module path changes (v2 → v3)**: Update all import statements and `go get` commands
- **CLI command changes**: Update command names, flags, and examples in CLI docs
- **API signature changes**: Update function signatures and examples in api-reference.md
- **New features**: Add documentation and examples

**Validation:**
- Run `make doc-check` to validate documentation (Level 1: fast checks)
- Run `make doc-test` to test code examples compile (Level 2: medium checks)
- Run `make doc-verify` for comprehensive verification (Level 3: deep checks)
- All three levels must pass before releasing

**Periodic documentation review:**
- Every 2-3 minor releases, review ALL docs in `doc/` for:
  - Outdated import paths (e.g., missing `/v4` suffix)
  - Missing new features (Signal Processing, Arrow I/O, new commands)
  - Old API patterns or command syntax
  - Broken cross-references after file moves
- Files to review: `doc/*.md`, `README.md`, `CLAUDE.md`
- Last full review: v4.8.12 (January 2026)

**Common mistakes to avoid:**
- ❌ Changing API without updating doc/api-reference.md
- ❌ Changing CLI commands without updating doc/cli-*.md
- ❌ Using old import paths (`ssql/v2` instead of `ssql/v3`)
- ❌ Using old command names (`read-csv` instead of `from`, `write-csv` instead of `to csv`)
- ❌ Using old flag names (`-match` instead of `-where`, `-expr` instead of `-where-expr`)

## Development Principles (CRITICAL)

### If It's Not Tested, It Will Break

**⚠️ Features without tests will eventually be removed or broken during refactoring.**

This was learned the hard way when field/value completion was accidentally removed in v3.2.0 during a refactor. The feature worked, but had no test coverage, so when code was reorganized the completion configuration was lost.

**Rules:**
- ✅ Add tests for any feature you want to keep
- ✅ Tests act as documentation of expected behavior
- ✅ Tests catch accidental removal during refactoring
- ❌ Don't assume "obvious" features will survive refactoring

**Example - Completion Configuration Test:**
```go
// TestFieldCompletionConfiguration verifies that all commands that accept field names
// have proper field completion configured (FieldsFromFlag) instead of NoCompleter.
// This test prevents regression where field completion is accidentally removed.
func TestFieldCompletionConfiguration(t *testing.T) {
    // ... verifies FieldCompleter is used, not NoCompleter
}
```

### Compile-Time Type Safety Over Runtime

**⚠️ ALWAYS prefer compile-time type safety over runtime validation.**

ssql is built on Go's type system and generics (Go 1.23+). Type errors should be caught at compile time, not runtime.

**Core Principle:**
- ✅ Use generics and type constraints to enforce correctness at compile time
- ✅ Use sealed interfaces to prevent invalid type construction
- ✅ Leverage the type system to make invalid states unrepresentable
- ❌ Avoid runtime type checking and panics
- ❌ Never bypass type constraints with `any` or reflection

**Examples:**

**✅ GOOD - Compile-time safety with generics:**
```go
// AggregateResult sealed interface - can only be created by AggResult[V Value]
type AggregateResult interface {
    getValue() any
    sealed() // Prevents external implementations
}

type AggResult[V Value] struct {
    val V
}

// Compiler guarantees V satisfies Value constraint
func Count() AggregateFunc {
    return func(records []Record) AggregateResult {
        return AggResult[int64]{val: int64(len(records))}  // ✅ int64 is Value
    }
}
```

**❌ BAD - Runtime validation:**
```go
func Count() AggregateFunc {
    return func(records []Record) any {
        return int64(len(records))  // ❌ Could return anything!
    }
}

// Then need runtime checks:
func setValidated(field string, value any) {
    switch value.(type) {
    case int64, float64, string:  // ❌ Runtime checking
        m.fields[field] = value
    default:
        panic("invalid type")  // ❌ Panic at runtime
    }
}
```

**Historical Examples:**

1. **v1.22.0 - Sealed Interface for Aggregations:**
   - Replaced `AggregateFunc: func([]Record) any` with `func([]Record) AggregateResult`
   - Created `AggResult[V Value]` generic wrapper
   - Eliminated `setValidated()` runtime validation
   - Result: All aggregation type errors caught at compile time

2. **v2.0.0 - Removed SetAny():**
   - Removed `SetAny(field string, value any)` entirely
   - Enforced use of typed methods: `Int()`, `Float()`, `String()`, etc.
   - Updated JSON parsing to use type-safe methods
   - Result: Impossible to add invalid types to records

**When Implementing New Features:**
- Ask: "Can the type system prevent this error?"
- Use generic constraints (e.g., `Value`, `OrderedValue`)
- Create sealed interfaces for closed type sets
- Make invalid states unrepresentable
- If you need runtime validation, reconsider the design

**Benefits:**
- Bugs caught during development, not production
- Better IDE support (autocomplete, refactoring)
- Self-documenting code (types show intent)
- Zero runtime overhead for type checking
- More maintainable and refactorable code

### Performance-Critical Code Patterns

**⚠️ When writing code that processes records in a loop, follow these patterns to avoid performance regressions.**

ssql processes millions of records. Small inefficiencies multiply into significant slowdowns. The v4.5.0-v4.6.2 optimization work achieved 4x speedup by applying these principles.

**1. Schema Sharing - The #1 Performance Rule**

Creating a `Schema` involves sorting field names and building an index map. **Never create schemas per-record.**

```go
// ❌ BAD - Creates schema for every record (was 28% of CPU time!)
for row := range csvReader {
    record := MakeMutableRecord()
    for i, value := range row {
        record.fields[headers[i]] = parse(value)
    }
    yield(record.Freeze())  // Freeze() calls NewSchema() - expensive!
}

// ✅ GOOD - Create schema once, share across all records
schema := NewSchema(headers)
fieldIndices := make([]int, len(headers))
for i, h := range headers {
    fieldIndices[i] = schema.Index(h)
}

for row := range csvReader {
    values := make([]any, schema.Width())
    for i, value := range row {
        values[fieldIndices[i]] = parse(value)
    }
    yield(NewRecordFromSchema(schema, values))  // Reuses schema!
}
```

**Result: 43s → 10.4s (4.1x faster) for 14.6M records**

**2. Schema Caching for Variable-Schema Data**

When fields might vary between records (like JSONL without schema header), cache the schema and reuse when fields match:

```go
// ✅ GOOD - Cache schema for consecutive records with same fields
var cachedSchema *Schema
var cachedFields []string

for line := range lines {
    mutableRecord := ParseJSONLine(line)

    // Check if we can reuse cached schema
    if cachedSchema != nil && fieldsMatch(mutableRecord, cachedFields) {
        values := make([]any, cachedSchema.Width())
        for i, f := range cachedSchema.fields {
            values[i] = mutableRecord.fields[f]
        }
        record = Record{schema: cachedSchema, values: values}
    } else {
        record = mutableRecord.Freeze()  // Creates new schema only when needed
        cachedSchema = record.schema
        cachedFields = cachedSchema.fields
    }
}
```

**3. Buffer Reuse**

Pre-allocate buffers outside loops and reset with slice tricks:

```go
// ❌ BAD - Allocates new buffer for every record
for record := range records {
    buf, _ := json.Marshal(record)
    writer.Write(buf)
}

// ✅ GOOD - Reuse buffer across records
buf := make([]byte, 0, 4096)
for record := range records {
    buf = buf[:0]  // Reset to zero length, keep capacity
    buf = record.AppendJSON(buf)
    buf = append(buf, '\n')
    writer.Write(buf)
}
```

**4. Pre-compute Where Possible**

Store computed values in schemas or outside loops:

```go
// Schema stores pre-computed JSON field prefixes
type Schema struct {
    fields       []string
    jsonPrefixes [][]byte  // Pre-computed `"field":` for each field
}

// ✅ Computed once in NewSchema(), used millions of times in AppendJSON()
func (r Record) AppendJSON(buf []byte) []byte {
    for i, v := range r.values {
        buf = append(buf, r.schema.jsonPrefixes[i]...)  // No string alloc!
        buf = appendJSONValue(buf, v)
    }
}
```

**5. Avoid Hidden Double-Work**

Watch for code that does work twice:

```go
// ❌ BAD - Creates TWO schemas per record!
parsed := ParseJSONLine(line)
frozenParsed := parsed.Freeze()      // Schema #1

mut := MakeMutableRecord()
for k, v := range frozenParsed.All() {
    mut = setValueWithType(mut, k, v, ft)
}
record := mut.Freeze()               // Schema #2 - wasteful!

// ✅ GOOD - Create schema once via caching (see pattern #2)
```

**6. Profile Before Optimizing**

Use CPU profiling to find actual bottlenecks:

```bash
# Generate CPU profile
go test -cpuprofile cpu.prof -bench BenchmarkName

# Analyze with pprof
go tool pprof cpu.prof
(pprof) top10
(pprof) list FunctionName
```

The v4.6.0 fix came from profiling showing 28% CPU in `NewSchema` - not where we expected!

**Performance Checklist for Record-Processing Code:**

- [ ] Is schema created once and shared? (`NewRecordFromSchema`)
- [ ] For variable schemas, is caching implemented?
- [ ] Are buffers pre-allocated and reused?
- [ ] Is there any double-Freeze() or double-schema creation?
- [ ] Have you profiled to verify the optimization works?

**Reference:** See `doc/research/record-performance-optimization.md` for detailed analysis.

## Development Commands

**Building and Running:**
- `go build` - Build the module
- `go run doc/examples/chart_demo.go` - Run the comprehensive chart demo
- `go test` - Run all tests
- `go test -v` - Run tests with verbose output
- `go test -run TestSpecificFunction` - Run specific test
- `go fmt ./...` - Format all Go code
- `go vet ./...` - Run Go vet for static analysis
- `go mod tidy` - Clean up module dependencies

**Testing:**
- Tests are in `*_test.go` files using standard Go testing
- Main test files: `example_test.go`, `chart_demo_test.go`, `benchmark_test.go`
- No custom test runners or frameworks - use standard `go test`
- **Testing examples:** `go test -v -tags examples` - builds each example file individually to verify they compile

**Git Operations:**
- `git remote -v` - Show remote repository configuration
- `git fetch --dry-run` - Test GitHub connection without fetching
- `git push` - Push commits to GitHub
- `git push --tags` - Push tags to GitHub

## Release Process

**⚠️ CRITICAL: Version is manually maintained in version.txt**

Version is stored in `cmd/ssql/version/version.txt` and MUST be updated before creating tags.

**Correct Release Workflow (CRITICAL - Follow Exact Order):**

```bash
# 1. Make all code changes and commit them
git add .
git commit -m "Description of changes"

# 2. Update version.txt (WITHOUT "v" prefix)
echo "X.Y.Z" > cmd/ssql/version/version.txt

# 3. Commit the version change
git add cmd/ssql/version/version.txt
git commit -m "Bump version to vX.Y.Z"

# 4. Create annotated tag (WITH "v" prefix)
git tag -a vX.Y.Z -m "Release notes..."

# 5. Push everything
git push && git push --tags

# 6. CRITICAL: Verify go.mod has NO replace directive
cat go.mod  # Should NOT contain "replace" line

# 7. Verify install works from GitHub
GOPROXY=direct go install github.com/rosscartlidge/ssql/cmd/ssql@vX.Y.Z
ssql version  # Should show: ssql vX.Y.Z
```

**⚠️ CRITICAL:**
- **version.txt format**: Store WITHOUT "v" prefix (e.g., `1.2.0` not `v1.2.0`)
- **git tag format**: Use WITH "v" prefix (e.g., `v1.2.0`)
- **autocli adds "v"**: `.Version()` automatically adds "v" prefix to display
- **No replace directive**: `go.mod` must NOT contain `replace` line (breaks `go install`)
- **Annotated tags only**: Use `git tag -a vX.Y.Z -m "..."` not `git tag vX.Y.Z`
- **Test install**: Always verify with `GOPROXY=direct go install` before announcing release
- **Major version bumps**: Only bump major version (e.g., v4 → v5) when explicitly requested by the user. Major bumps require updating the module path (`/v4` → `/v5`) throughout the codebase. Use minor/patch versions for most releases.

**How It Works:**
- Version stored in `cmd/ssql/version/version.txt` (plain text, without "v")
- Embedded in binary via `//go:embed version.txt` in `cmd/ssql/version/version.go`
- autocli `.Version()` method adds "v" prefix automatically
- `ssql version` subcommand shows: "ssql vX.Y.Z"
- `ssql -help` header shows: "ssql vX.Y.Z - Unix-style data processing tools"

**Common Mistakes:**
- ❌ Including "v" in version.txt → Results in "vvX.Y.Z" display
- ❌ Having `replace` directive in go.mod → `go install` fails with error
- ❌ Using lightweight tags → Use annotated tags with `-a` flag
- ❌ Not testing install → Release may be broken for users

**Testing a Release:**
```bash
# After pushing tag, test from a different directory:
cd /tmp
GOPROXY=direct go install github.com/rosscartlidge/ssql/cmd/ssql@latest
ssql version  # Should show correct version
ssql -help    # Should work without errors
```

## Project History

**ssql v4.0.0 (December 2025):** Enhanced join command with multi-clause lookup support
- **Breaking Changes:**
  - `join` command: `-on FIELD` (same name both sides) → `-using FIELD`
  - `join` command: `-left-field`/`-right-field` removed → `-on LEFT RIGHT` (two args)
  - Module path: `github.com/rosscartlidge/ssql/v3` → `github.com/rosscartlidge/ssql/v4`
- **New Features:**
  - `-using FIELD`: Join on same field name in both sides (what `-on` used to do)
  - `-on LEFT RIGHT`: Join on different field names (replaces `-left-field`/`-right-field`)
  - `-as OLD NEW`: Rename fields from right side when bringing them in
  - Clause support with `-` separator: Multiple lookups from same file in one pass
  - `LookupJoin()` core library function for efficient multi-clause joins
- **Reason**: Enables efficient enrichment from lookup tables without reading the file multiple times
- **Migration**:
  ```bash
  # Old (v3.x)
  ssql from users.csv | ssql join orders.jsonl -on user_id
  ssql from users.csv | ssql join orders.jsonl -left-field user_id -right-field customer_id

  # New (v4.0+)
  ssql from users.csv | ssql join orders.jsonl -using user_id
  ssql from users.csv | ssql join orders.jsonl -on user_id customer_id

  # New multi-clause feature
  ssql from data.csv | ssql join <(ssql from kind.csv) \
    -on a_kind kind -as kind_name a_kind_name \
    - \
    -on z_kind kind -as kind_name z_kind_name
  ```

**ssql v3.1.0 (December 2025):** Stdin-only transform commands (Unix philosophy)
- **Breaking Changes:**
  - `where` command: Removed `FILE` parameter - now reads from stdin only
  - `update` command: Removed `FILE` parameter - now reads from stdin only
  - `chart` command: Removed `FILE` parameter - now reads from stdin only
  - `union` command: Removed `-input` parameter - now reads from stdin only
  - `join` command: Changed from `-right FILE` to positional `FILE` for right-side file
- **Design Philosophy**:
  - Source command (`from`): Read from files, stdin, or command output
  - Transform commands (`where`, `update`, etc.): Pure filters - stdin only
  - This aligns with Unix philosophy of composable pipeline filters
- **Migration**:
  ```bash
  # Old (v3.0.x)
  ssql where FILE data.jsonl -where age gt 18
  ssql update FILE data.jsonl -set status done
  ssql join FILE left.jsonl -right right.csv -on id

  # New (v3.1.0)
  ssql from data.csv | ssql where -where age gt 18
  ssql from data.csv | ssql update -set status done
  ssql from left.csv | ssql join right.csv -on id
  ```

**ssql v3.0.0 (November 2025):** SQL-aligned flag naming and operator consolidation
- **Breaking Changes:**
  - `where` command: `-match` → `-where`, `-expr` → `-where-expr`
  - `update` command: `-match` → `-where`, added `-where-expr` flag
  - Regex operators: Removed `pattern` and `regexp` aliases, kept only `regex`
- **Reason**: Better SQL alignment (WHERE clause) and reduced confusion from duplicate operator names
- **Migration**: Replace `-match` with `-where` and `-expr` with `-where-expr` in pipelines
- **Example**:
  ```bash
  # Old (v2.x)
  ssql where -match age gt 18 -expr 'verified == true'
  ssql update -match status eq pending -set status approved

  # New (v3.0+)
  ssql where -where age gt 18 -where-expr 'verified == true'
  ssql update -where status eq pending -set status approved
  ssql update -where-expr 'total > 1000' -set-expr discount 'total * 0.1'
  ```

**ssql v1.14.0 (November 2025):** Renamed from streamv3 to ssql
- **Repository**: `streamv3` → `ssql`
- **Module path**: `github.com/rosscartlidge/streamv3` → `github.com/rosscartlidge/ssql`
- **Package name**: `streamv3` → `ssql` (throughout codebase)
- **CLI command**: `streamv3` → `ssql`
- **Reason**: Shorter, more memorable name that emphasizes SQL-style API design
- **Version**: Could not use v1.0.0 (v1.13.6 existed); started at v1.14.0 to continue sequence
- **Migration**: Update imports from `github.com/rosscartlidge/streamv3` to `github.com/rosscartlidge/ssql`

**Important**: Go's module proxy permanently caches old versions. The old `streamv3` versions (v1.0.0-v1.13.6) remain cached with the old module path. Users must update to `ssql` module path.

**autocli v3.0.0 (November 2025):** Renamed from completionflags
- **Repository**: `completionflags` → `autocli`
- **Module path**: `github.com/rosscartlidge/completionflags/v2` → `github.com/rosscartlidge/autocli/v3`
- **Reason**: Better reflects comprehensive CLI framework (commands, subcommands, help, completion)
- **Version**: v3.0.0 (major bump for breaking rename)
- **Important**: Always use `/v3` suffix - old cached versions (v1.x, v2.x) have wrong module path

## Architecture Overview

ssql is a modern Go library built on three core abstractions:

**Core Types:**
- `iter.Seq[T]` and `iter.Seq2[T,error]` - Go 1.23+ iterators (lazy sequences)
- `Record` - Encapsulated struct with private fields map (`struct { fields map[string]any }`)
- `MutableRecord` - Efficient record builder with in-place mutation
- `Filter[T,U]` - Composable transformations (`func(iter.Seq[T]) iter.Seq[U]`)

**Key Architecture Files:**
- `core.go` - Core types, Filter functions, Record system, composition functions
- `operations.go` - Stream operations (Map, Where, Reduce, etc.)
- `chart.go` - Interactive Chart.js visualization with Bootstrap 5 UI
- `io.go` - CSV/JSON I/O, command parsing, file operations
- `sql.go` - GROUP BY aggregations and SQL-style operations

**API Design - Functional Composition:**
- **Functional API** - Explicit Filter composition: `Pipe(Where(...), GroupByFields(...), Aggregate(...))`
  - Handles all operations including type-changing operations (GroupBy, Aggregate)
  - Flexible and composable for complex pipelines
  - One clear way to compose operations

**Error Handling:**
- Simple iterators: `iter.Seq[T]`
- Error-aware iterators: `iter.Seq2[T, error]`
- Conversion utilities: `Safe()`, `Unsafe()`, `IgnoreErrors()`

**Data Visualization:**
- Chart.js integration with interactive HTML output
- Field selection UI, zoom/pan, statistical overlays
- Multiple chart types: line, bar, scatter, pie, radar
- Export formats: PNG, CSV

**Entry Points:**
- `slices.Values(slice)` - Create iterator from slice
- `ReadCSV(filename)` - Parse CSV files returning `iter.Seq[Record]`
- `ExecCommand(cmd, args...)` - Parse command output returning `iter.Seq[Record]`
- `QuickChart(data, x, y, filename)` - Generate interactive charts

## API Naming Conventions (SQL-Style)

ssql uses SQL-like naming instead of functional programming conventions. **Always use these canonical names:**

**Stream Operations (operations.go):**
- **`SelectMany`** - Flattens nested sequences (NOT FlatMap)
  - `SelectMany[T, U any](fn func(T) iter.Seq[U]) Filter[T, U]`
  - Use for one-to-many transformations (e.g., splitting records)
- **`Where`** - Filters records based on predicate (NOT Filter)
  - Note: `Filter[T,U]` is the type name for transformations
- **`Select`** - Projects/transforms fields (similar to Map, but SQL-style)
- **`Update`** - Modifies record fields (convenience wrapper around Select)
  - `Update(fn func(MutableRecord) MutableRecord) Filter[Record, Record]`
  - Eliminates `ToMutable()` and `Freeze()` boilerplate
  - Example: `Update(func(mut MutableRecord) MutableRecord { return mut.String("status", "active") })`
  - Equivalent to: `Select(func(r Record) Record { return r.ToMutable().String("status", "active").Freeze() })`
- **`Reduce`** - Aggregates sequence to single value
- **`Take`** - Limits number of records (like SQL LIMIT)
- **`Skip`** - Skips first N records (like SQL OFFSET)

**Aggregation Operations (sql.go):**
- **`GroupByFields`** - Groups and aggregates (SQL GROUP BY)
- **`Aggregate`** - Applies aggregation functions (Count, Sum, Avg, etc.)

**Common Mistakes:**
- ❌ Looking for `FlatMap` → ✅ Use `SelectMany`
- ❌ Using `Filter` as function → ✅ Use `Where` (Filter is a type)
- ❌ Looking for LINQ-style names → ✅ Check operations.go for SQL-style names

When in doubt, check `operations.go` for the canonical API - don't assume LINQ or functional programming naming conventions.

## Canonical Numeric Types (Hybrid Approach)

ssql enforces a **hybrid type system** for clarity and consistency:

**Scalar Values - Canonical Types Only:**
- **Integers**: Always use `int64`, never `int`, `int32`, `uint`, etc.
- **Floats**: Always use `float64`, never `float32`
- **Reason**: Eliminates type conversion ambiguity, consistent with CSV auto-parsing

**Sequence Values - Flexible Types:**
- **Sequences**: Allow all numeric types (`iter.Seq[int]`, `iter.Seq[int32]`, `iter.Seq[float32]`, etc.)
- **Reason**: Works naturally with Go's standard library (`slices.Values([]int{...})`)

**Examples:**
```go
// ✅ CORRECT - Canonical scalar types
record := ssql.NewRecord().
    Int("count", int64(42)).           // int64 required
    Float("price", 99.99).             // float64 required
    IntSeq("scores", slices.Values([]int{1, 2, 3})).  // iter.Seq[int] allowed
    Build()

// ✅ CORRECT - Type conversion when needed
age := int(ssql.GetOr(record, "age", int64(0)))

// ❌ WRONG - Non-canonical scalar types
record := ssql.NewRecord().
    Int("count", 42).                  // Won't compile - int not allowed
    Float("price", float32(99.99)).    // Won't compile - float32 not allowed
    Build()
```

**CSV Auto-Parsing:**
- CSV reader produces `int64` for integers, `float64` for decimals
- Always use `int64(0)` and `float64(0)` as default values with `GetOr()`
- Example: `age := ssql.GetOr(record, "age", int64(0))`

**Type Conversion:**
- `Get[int64]()` works for string → int64 parsing
- `Get[float64]()` works for string → float64 parsing
- `Get[int]()` will NOT convert from strings (no automatic parsing)
- Users must explicitly convert: `age := int(GetOr(r, "age", int64(0)))`

This hybrid approach balances ergonomics (flexible sequences) with consistency (canonical scalars).

## Record Design - Encapsulated Struct (v1.0+)

**⚠️ BREAKING CHANGE in v1.0:** Record is now an encapsulated struct, not a bare `map[string]any`.

### Record vs MutableRecord

**Record (Immutable):**
- Struct with private `fields map[string]any`
- Immutable - methods return new copies
- Use for function parameters, return values, pipeline data
- Access via `Get()`, `GetOr()`, `.All()` iterator

**MutableRecord (Mutable Builder):**
- Struct with private `fields map[string]any`
- Mutable - methods modify in-place and return self for chaining
- Use for efficient record construction
- Convert to Record via `.Freeze()` (creates copy)

### Creating Records

```go
// ✅ CORRECT - Use MutableRecord builder
record := ssql.MakeMutableRecord().
    String("name", "Alice").
    Int("age", int64(30)).
    Float("salary", 95000.50).
    Bool("active", true).
    Freeze()  // Convert to immutable Record

// ✅ CORRECT - From map (for compatibility)
record := ssql.NewRecord(map[string]any{
    "name": "Alice",
    "age": int64(30),
})

// ❌ WRONG - Can't use struct literal
record := ssql.Record{"name": "Alice"}  // Won't compile!

// ❌ WRONG - Can't use make()
record := make(ssql.Record)  // Won't compile!
```

### Accessing Record Fields

**Within ssql package:**
```go
// ✅ Can access .fields directly (private field)
for k, v := range record.All() {
    record.fields[k] = v
}

// ✅ Direct field access for internal operations
value := record.fields["name"]
```

**Outside ssql package (CLI commands, tests, user code):**
```go
// ✅ CORRECT - Use Get/GetOr
name := ssql.GetOr(record, "name", "")
age := ssql.GetOr(record, "age", int64(0))

// ✅ CORRECT - Iterate with .All()
for k, v := range record.All() {
    fmt.Printf("%s: %v\n", k, v)
}

// ✅ CORRECT - Build with MutableRecord
mut := ssql.MakeMutableRecord()
mut = mut.String("city", "NYC")           // Chainable
mut = mut.SetAny("field", anyValue)       // For unknown types
frozen := mut.Freeze()                    // Convert to Record

// ❌ WRONG - Can't access .fields (private!)
value := record.fields["name"]            // Compile error!

// ❌ WRONG - Can't index directly
name := record["name"]                    // Compile error!

// ❌ WRONG - Can't iterate directly
for k, v := range record {                // Compile error!
    ...
}
```

### Iterating Over Records

```go
// ✅ CORRECT - Use .All() iterator (maps.All pattern)
for k, v := range record.All() {
    fmt.Printf("%s: %v\n", k, v)
}

// ✅ CORRECT - Use .KeysIter() for keys only
for k := range record.KeysIter() {
    fmt.Println(k)
}

// ✅ CORRECT - Use .Values() for values only
for v := range record.Values() {
    fmt.Println(v)
}

// ❌ WRONG - Can't iterate Record directly
for k, v := range record {                // Compile error!
    ...
}
```

### Migration Patterns

**Converting old code to v1.0:**

```go
// OLD (v0.x):
record := make(ssql.Record)
record["name"] = "Alice"
value := record["age"]
for k, v := range record {
    ...
}

// NEW (v1.0+):
record := ssql.MakeMutableRecord()
record = record.String("name", "Alice")
value := ssql.GetOr(record.Freeze(), "age", int64(0))
for k, v := range record.Freeze().All() {
    ...
}
```

**Test code migration:**

```go
// OLD (v0.x):
testData := []ssql.Record{
    {"name": "Alice", "age": int64(30)},
    {"name": "Bob", "age": int64(25)},
}

// NEW (v1.0+):
r1 := ssql.MakeMutableRecord()
r1.fields["name"] = "Alice"    // Within ssql package
r1.fields["age"] = int64(30)

r2 := ssql.MakeMutableRecord()
r2.fields["name"] = "Bob"
r2.fields["age"] = int64(25)

testData := []ssql.Record{r1.Freeze(), r2.Freeze()}
```

## Record Field Access (CRITICAL)

**⚠️ ALWAYS use `Get()` or `GetOr()` methods to read fields from Records. NEVER use direct map access or type assertions.**

**Why:**
- Direct access `r["field"]` requires type assertions: `r["field"].(string)` → **panics if field missing or wrong type**
- Type assertions `r["field"].(string)` are unsafe and fragile
- `Get()` and `GetOr()` handle type conversion, missing fields, and type mismatches gracefully

**Correct Field Access:**
```go
// ✅ CORRECT - Use GetOr with appropriate default
name := ssql.GetOr(r, "name", "")                    // String field
age := ssql.GetOr(r, "age", int64(0))                // Numeric field
price := ssql.GetOr(r, "price", float64(0.0))        // Float field

// ✅ CORRECT - Use in generated code
strings.Contains(ssql.GetOr(r, "email", ""), "@")
regexp.MustCompile("pattern").MatchString(ssql.GetOr(r, "name", ""))
ssql.GetOr(r, "salary", float64(0)) > 50000
```

**Wrong Field Access:**
```go
// ❌ WRONG - Direct map access with type assertion (WILL PANIC!)
name := r["name"].(string)                               // Panic if field missing or wrong type
r["email"].(string)                                      // Panic if field missing
asFloat64(r["price"])                                    // Don't create helper functions - use GetOr!

// ❌ WRONG - Direct map access in comparisons
r["status"] == "active"                                  // May work, but inconsistent
```

**Code Generation Rules:**
- **String operations**: Always use `ssql.GetOr(r, field, "")` with empty string default
- **Numeric operations**: Always use `ssql.GetOr(r, field, float64(0))` or `int64(0)` default
- **Never generate**: Type assertions like `r[field].(string)`
- **Never generate**: Custom helper functions like `asFloat64()`

**Examples in Generated Code:**
```go
// String operators (contains, startswith, endswith, regexp)
strings.Contains(ssql.GetOr(r, "name", ""), "test")
strings.HasPrefix(ssql.GetOr(r, "email", ""), "admin")
regexp.MustCompile("^[A-Z]").MatchString(ssql.GetOr(r, "code", ""))

// Numeric operators (eq, ne, gt, ge, lt, le)
ssql.GetOr(r, "age", float64(0)) > 18
ssql.GetOr(r, "salary", float64(0)) >= 50000
ssql.GetOr(r, "count", float64(0)) == 42
```

This approach eliminates runtime panics and makes generated code robust and maintainable.

This library emphasizes functional composition with Go 1.23+ iterators while providing comprehensive data visualization capabilities.

## CLI Tools Architecture (autocli v4.0.0+)

ssql CLI uses **autocli v4.0.0+** for native subcommand support with auto-generated help and tab completion. All 14 commands migrated as of v1.2.0. Migrated to autocli v3.0.0 as of ssql v1.13.4, updated to v3.0.1 as of ssql v1.14.1, updated to v3.2.0 for pipeline field caching support, updated to v4.0.0 for field value completion.

**Architecture Overview:**
- `cmd/ssql/main.go` - All subcommands defined using autocli builder API
- `cmd/ssql/helpers.go` - Shared utilities (comparison operators, aggregation, extractNumeric, chainRecords)
- `cmd/ssql/version/version.txt` - Version string (manually maintained)
- All commands use context-based flag access: `ctx.GlobalFlags` and `ctx.Clauses`

**Version Access:**
- `ssql version` - Dedicated version subcommand (returns "ssql vX.Y.Z")
- `ssql -help` - Shows version in header
- ⚠️ No `-version` flag (autocli doesn't auto-add this)

**CLI Flag Design Principles:**

When designing CLI commands with autocli, follow these principles:

1. **Prefer Named Flags Over Positional Arguments**
   - ✅ Use: `-file data.csv` or `-input data.csv`
   - ❌ Avoid: `command data.csv` (positional)
   - Named flags are self-documenting and enable better tab completion
   - Positional arguments can consume arguments intended for other flags
   - Exception: Commands with a single, obvious positional argument (e.g., `cd directory`)

2. **Use Multi-Argument Flags Properly**
   - For flags with multiple related arguments, use `.Arg()` fluent API:
   ```go
   Flag("-where").
       Arg("field").Completer(cf.NoCompleter{Hint: "<field-name>"}).Done().
       Arg("operator").Completer(&cf.StaticCompleter{Options: operators}).Done().
       Arg("value").Completer(cf.NoCompleter{Hint: "<value>"}).Done().
   ```
   - This enables proper completion for each argument position
   - Always provide hints via `NoCompleter{Hint: "..."}` when no completion is available
   - Use `StaticCompleter{Options: [...]}` for constrained values
   - ❌ Don't use `.String()` and require quoting: `-where "field op value"`
   - ✅ Use separate arguments: `-where field op value`

3. **Use `.Accumulate()` for Repeated Flags**
   - When a flag can appear multiple times (e.g., `-where age gt 30 -where dept eq Sales`)
   - Enables building complex filters with AND/OR logic
   - The framework provides a slice of all flag occurrences

4. **Provide Completers for Constrained Arguments**
   - Use `StaticCompleter` for known options (operators, commands, etc.)
   - Use `FileCompleter` with patterns for file paths
   - Improves UX with tab completion

5. **Avoid In-Argument Delimiters (Use Multi-Arg Flags Instead)**
   - ❌ Don't parse arguments: `-rename "old:new"` (requires delimiter parsing)
   - ✅ Use framework: `-as old new` (framework separates args)
   - **Why**: Arguments with delimiters require custom parsing, escaping, and quote handling
   - Delimiters fail when values contain the delimiter character
   - autocli handles argument separation - leverage it!
   - **Example - Field names with special characters:**
   ```bash
   # ❌ BAD - Delimiter approach breaks
   ssql rename "url:port:status"      # Ambiguous! Which colon is the separator?
   ssql rename "file\:path:new_name"  # Requires ugly escaping

   # ✅ GOOD - Multi-arg approach works naturally
   ssql rename -as "url:port" status         # No ambiguity!
   ssql rename -as "file with spaces" clean  # Spaces work fine
   ssql rename -as "weird|chars" simple      # Any character works
   ```
   - **Implementation:**
   ```go
   // ✅ GOOD - No parsing needed, supports any field name
   Flag("-as").
       Arg("old-field").Completer(cf.NoCompleter{Hint: "<field-name>"}).Done().
       Arg("new-field").Completer(cf.NoCompleter{Hint: "<new-name>"}).Done().
       Accumulate().  // For multiple renames

   // ❌ BAD - Requires custom parsing, breaks on "field:with:colons"
   Flag("-rename").
       String().  // User must format as "old:new"
       Accumulate().
   ```

6. **Use Brace Expansion for File Completion Patterns**
   - ✅ Use brace expansion: `Pattern: "*.{json,jsonl}"` for multiple extensions
   - ❌ Don't use comma-separated: `Pattern: "*.json,*.jsonl"` (doesn't work)
   - **Why**: FileCompleter expects shell-style glob patterns with brace expansion
   - **Examples:**
   ```go
   // ✅ CORRECT - Brace expansion
   Flag("FILE").
       String().
       Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl}"}).  // Both .json and .jsonl
       Done().

   Flag("FILE").
       String().
       Completer(&cf.FileCompleter{Pattern: "*.csv"}).  // Single extension
       Done().

   Flag("FILE").
       String().
       Completer(&cf.FileCompleter{Pattern: "*.{csv,tsv,txt}"}).  // Multiple extensions
       Done().

   // ❌ WRONG - Comma-separated doesn't work
   Flag("FILE").
       String().
       Completer(&cf.FileCompleter{Pattern: "*.json,*.jsonl"}).  // Won't complete!
       Done().
   ```

7. **Follow Unix Philosophy: Support stdin/stdout for Pipeline Commands**
   - **CRITICAL**: All data processing commands MUST support stdin/stdout for Unix pipelines
   - Input commands (readers): Optionally read from file OR stdin
   - Output commands (writers): Optionally write to file OR stdout (buffered)
   - **Why**: Enables composable pipelines and tool chaining
   - **Pattern for input:**
   ```go
   // Read from file or stdin
   var records iter.Seq[ssql.Record]
   if inputFile == "" {
       records = ssql.ReadCSVFromReader(os.Stdin)
   } else {
       records, err = ssql.ReadCSV(inputFile)
   }
   ```
   - **Pattern for output:**
   ```go
   // Write to file or stdout
   if outputFile == "" {
       return ssql.WriteCSVToWriter(records, os.Stdout)
   } else {
       return ssql.WriteCSV(records, outputFile)
   }
   ```
   - **Consistency examples:**
   ```bash
   # ✅ GOOD - All work with pipelines
   ssql from data.csv | ssql where -where age gt 25 | ssql to csv output.csv
   ssql from data.csv | ssql include name age | ssql to json
   cat data.csv | ssql from | ssql limit 10 | ssql to table

   # ❌ BAD - Requiring files breaks pipelines
   ssql from data.csv | ssql to json output.json  # If FILE was required!
   ```
   - **FILE parameter guidelines:**
     - Input commands: FILE should be optional (default to stdin) or allow `-` for stdin
     - Output commands: FILE should be optional (default to stdout) or allow `-` for stdout
     - Make defaults explicit in help: "Input file (or stdin if not specified)"
     - Use `Default("")` for optional file parameters

8. **All Commands MUST Have Examples**
   - **CRITICAL**: Every CLI command MUST include 2-3 usage examples in its help text
   - Examples should demonstrate common use cases and showcase key features
   - Use `.Example()` calls immediately after `.Description()`
   - **Pattern:**
   ```go
   Subcommand("command-name").
       Description("Brief description").

       Example("ssql command arg1 arg2", "What this example demonstrates").
       Example("ssql command -flag value | ssql other", "Another common use case").

       Flag("-flag").
           // ...
   ```
   - **Why**: Examples are critical for discoverability and learning
   - Help users understand how to use the command without reading full documentation
   - Show common patterns and pipeline composition
   - **Verify**: Run `./ssql command -help` and ensure EXAMPLES section appears
   - **Test all commands**: Use this script to verify all have examples:
   ```bash
   for cmd in $(./ssql -help | grep "^    [a-z]" | awk '{print $1}'); do
     if ./ssql $cmd -help 2>&1 | grep -q "EXAMPLES:"; then
       echo "$cmd: ✅ has examples"
     else
       echo "$cmd: ❌ NO examples"
     fi
   done
   ```

9. **Automatic Pipeline Field Caching (NEW in autocli v4.1.0)**
   - **The Problem**: In pipelines like `ssql from users.csv | ssql where -where <TAB>`, the first command doesn't have flags with `FieldsFromFlag()`, so field names aren't available for completion in downstream commands
   - **The Solution**: Automatic! When `FileCompleter` completes to a single data file, it automatically extracts and caches field names
   - **How It Works**:
     1. User types `ssql from user<TAB>` which narrows to `users.csv`
     2. `FileCompleter` detects single data file match
     3. Automatically extracts field names and emits cache directive
     4. Bash completion script sets `AUTOCLI_FIELDS` environment variable
     5. Downstream commands with `FieldsFromFlag()` can use this cached list
   - **Usage Pattern**:
   ```bash
   # Tab complete the filename (narrows to single file)
   ssql from user<TAB>
   # Completes to: users.csv
   # Automatically caches fields: name, age, email, status

   # Now pipeline completion works!
   ssql from users.csv | ssql where -where <TAB>
   # Completes with: name, age, email, status
   ```
   - **No Configuration Needed**: Just use `FilePattern()` with data file extensions:
   ```go
   Flag("FILE").
       String().
       FilePattern("*.{csv,json,jsonl}").
       Done()
   ```
   - **Benefits**:
     - No special flags or workflow needed (the old `-cache DONE` pattern is obsolete)
     - Works automatically with any `FileCompleter` for data files
     - Seamless integration with Unix pipeline workflows

10. **Field Value Completion with FieldValuesFrom()**
   - **NEW in autocli v4.0.0**: Complete with actual data values from files, not just field names
   - **The Problem**: When filtering or matching data, users must type exact values manually
   - **The Solution**: Use `FieldValuesFrom("FILE", "field")` to complete with actual data values sampled from the file
   - **Pattern:**
   ```go
   Flag("-where").
       Arg("field").
           FieldsFromFlag("FILE").     // Complete field names
           Done().
       Arg("operator").
           Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt"}}).
           Done().
       Arg("value").
           FieldValuesFrom("FILE", "field").  // Complete with actual values from that field!
           Done().
       Done()
   ```
   - **How It Works**:
     1. User completes field name: `-where status <TAB>` → shows operators
     2. User completes operator: `-where status eq <TAB>`
     3. The completer reads the file, samples unique values from the "status" column
     4. Returns JSON directive with values + filtered completions
     5. Shows actual data: `active`, `pending`, `archived`, etc.
   - **Real Example from ssql:**
   ```bash
   # User workflow with tab completion
   ssql where FILE users.csv -where status <TAB>
   # Shows operators: eq, ne, gt, ge, lt, le, contains, startswith, endswith

   ssql where FILE users.csv -where status eq <TAB>
   # Shows actual data from status column: active  pending  archived

   ssql where FILE users.csv -where name eq Al<TAB>
   # Filters and completes: Alice

   # Final command
   ssql where FILE users.csv -where name eq Alice
   ```
   - **Performance**: Samples up to 100 unique values from first 10,000 records (configurable)
   - **Special Characters**: Handles spaces, quotes, commas correctly via JSON encoding
   - **Current Implementation**: Added to `where` and `update` commands for `-where` and `-set` flags
   - **Benefits**:
     - Users don't need to remember exact values
     - Reduces typos and errors
     - Faster data exploration and filtering
     - Works with CSV, TSV, JSON, and JSONL files

**Completionflags Subcommand Pattern:**

All commands follow this pattern in `main.go`:

```go
Subcommand("command-name").
    Description("Brief description").

    Handler(func(ctx *cf.Context) error {
        // 1. Extract flags from ctx.GlobalFlags (for Global flags)
        var myFlag string
        if val, ok := ctx.GlobalFlags["-myflag"]; ok {
            myFlag = val.(string)
        }

        // 2. Extract clause flags (for Local flags with + separators)
        if len(ctx.Clauses) > 0 {
            clause := ctx.Clauses[0]
            if val, ok := clause.Flags["-field"]; ok {
                // Handle accumulated flags: val.([]any)
            }
        }

        // 3. For commands with -- separator (like from with command execution)
        if len(ctx.RemainingArgs) > 0 {
            command := ctx.RemainingArgs[0]
            args := ctx.RemainingArgs[1:]
            // ...
        }

        // 4. Perform command operation
        // 5. Return error or nil
        return nil
    }).

    Flag("-myflag").
        String().
        Global().  // Or Local() for clause-based flags
        Help("Description").
        Done().

    Done().
```

**Key Patterns:**
- **Global flags**: Use `ctx.GlobalFlags["-flagname"]` - applies to entire command
- **Local flags**: Use `ctx.Clauses[i].Flags["-flagname"]` - applies per clause (with `+` separator)
- **Accumulated flags**: Use `.Accumulate()` and access as `[]any` slice
- **-- separator**: Use `ctx.RemainingArgs` for everything after `--` (requires autocli v3.0+)
- **Type assertions**: All flag values are `interface{}`, cast appropriately: `val.(string)`, `val.(int)`, `val.(bool)`

**Important Lessons Learned:**

1. **Release with replace directive fails** - `go install` fails if go.mod has `replace` directive
   - Always remove local `replace` before tagging releases
   - Test with `GOPROXY=direct go install github.com/user/repo/cmd/app@vX.Y.Z`

2. **Version display** - autocli `.Version()` adds "v" prefix automatically
   - Store version without "v" in version.txt: `1.2.0` not `v1.2.0`
   - Display will show: "ssql v1.2.0"

3. **Version subcommand needed** - autocli doesn't auto-add `-version` flag
   - Must manually add `version` subcommand if users need version access
   - Version also appears in help header automatically

4. **Context-based flag access** - Don't use `.Bind()` for complex commands
   - Use `ctx.GlobalFlags` and `ctx.Clauses` for flexibility
   - Enables dynamic flag handling and accumulation

5. **-- separator support** - Requires autocli v3.0+
   - Use for commands that pass args to other programs (like `from -- command args`)
   - Access via `ctx.RemainingArgs` slice

### autocli Migration History

**v3.0.1 (ssql v1.14.1):** Branding update
- Updated completion script comments: "Generated by autocli" (was "completionflags")
- Changed completion function name: `_autocli_complete` (was `_completionflags_complete`)
- Proper branding throughout completion scripts

**v3.0.0 (ssql v1.13.6):** Package rename from completionflags to autocli
- Repository renamed: `completionflags` → `autocli`
- Module path: `github.com/rosscartlidge/autocli/v3` (major version bump for rename)
- All imports updated from `completionflags/v2` to `autocli/v3`
- Reason: Better reflects comprehensive CLI framework capabilities beyond just completion

**v2.0.0 (ssql v1.13.4):** Breaking changes
- Removed `.Bind()` method
- Adopted Go semantic versioning with `/v2` module path

**Migration details for v2.0.0:**

1. **Module path change** - CRITICAL for Go semantic versioning
   - Old: `github.com/rosscartlidge/autocli`
   - New: `github.com/rosscartlidge/autocli/v2`
   - Required updating `go.mod` module declaration in autocli to include `/v2` suffix
   - Required updating all imports in ssql from `autocli` to `autocli/v2`

2. **Breaking change: ctx.Subcommand → ctx.SubcommandPath**
   - Old: `ctx.Subcommand` (string) - single subcommand name
   - New: `ctx.SubcommandPath` ([]string) - slice supporting nested subcommands like `git remote add`
   - Helper methods: `ctx.IsSubcommand(name)`, `ctx.SubcommandName()`
   - **No impact on ssql** - we don't access this field anywhere in our code

3. **Bug discovered during migration: .Example() return type**
   - Problem: `.Example()` returned `Builder` interface instead of concrete type
   - Impact: Prevented fluent chaining - couldn't call `.Flag()` after `.Example()`
   - Fix: Removed `Example()` from `Builder` interface, changed to return `*SubcommandBuilder`
   - Released as autocli v3.0.0

4. **No replace directive in releases** - CRITICAL lesson reinforced
   - Local `replace` directives break `go install` for users
   - Always remove before tagging releases
   - Test with: `GOPROXY=direct go install github.com/user/repo/cmd/app@vX.Y.Z`

5. **Import path updates for examples**
   - All autocli examples needed import path updates to `/v2`
   - All example `go.mod` files needed module path updates

**Migration checklist for future major version bumps:**

```bash
# 1. Update module path in library go.mod
echo "module github.com/user/lib/v2" > go.mod

# 2. Update all imports in consuming code
sed -i 's|github.com/user/lib"|github.com/user/lib/v2"|g' **/*.go

# 3. Update go.mod in consuming code
# Change: require github.com/user/lib v1.x.x
# To: require github.com/user/lib/v2 v2.x.x

# 4. Remove any replace directives before release
# Edit go.mod to remove "replace" line

# 5. Test installation from GitHub
GOPROXY=direct go install github.com/user/repo/cmd/app@vX.Y.Z

# 6. Verify version
app version
```

**Key learnings:**
- Go semantic versioning requires `/v2` (or higher) in module path for major versions
- Breaking changes (removed methods, changed types) require major version bump
- API design: Return concrete types from builder methods, not interfaces (enables fluent chaining)
- Always test `go install` from GitHub before announcing release

## Code Generation System (CRITICAL FEATURE)

**⚠️ CRITICAL: This is a core feature that enables 10-100x faster execution by generating standalone Go programs from CLI pipelines.**

### Overview

ssql supports **self-generating pipelines** where commands emit Go code fragments instead of executing. This allows users to:
1. Prototype data processing pipelines using the CLI
2. Generate optimized Go code from the working pipeline
3. Compile and run standalone programs 10-100x faster than CLI execution

### Generated Code Readability (CRITICAL)

**⚠️ ALWAYS keep generated code simple and readable!**

**Rules for Code Generation:**

1. **Move complexity to helper functions** - Generated code should call helper functions in the ssql package, NOT inline complex logic
   - ✅ GOOD: `ssql.DisplayTable(records, 50)` (one line, clear intent)
   - ❌ BAD: 80 lines of formatting logic inlined (hard to understand)

2. **Generated code should be self-documenting** - A reader should immediately understand what the pipeline does
   - Keep the main pipeline flow visible
   - Don't bury the logic in loops, switches, or complex algorithms

3. **When adding new commands:**
   - First: Add helper function to ssql package (io.go, operations.go, etc.)
   - Then: Generate code that calls the helper
   - Test: Read the generated code - is the intent clear?

4. **Examples:**
   ```go
   // ✅ GOOD - Clean, readable generated code
   records := ssql.ReadCSV("data.csv")
   filtered := ssql.Where(func(r ssql.Record) bool {
       return ssql.GetOr(r, "age", int64(0)) > 18
   })(records)
   ssql.DisplayTable(filtered, 50)

   // ❌ BAD - Inlined complexity obscures intent
   records := ssql.ReadCSV("data.csv")
   // ... 80 lines of table formatting logic ...
   // Reader can't see what the pipeline does!
   ```

**Why This Matters:**
- Users read generated code to understand what their pipeline does
- Generated code is often modified and maintained
- Simple code enables debugging and optimization
- The CLI handles complexity - generated code should be clear

### Enabling Code Generation

Two ways to enable generation mode:

```bash
# Method 1: Environment variable (affects entire pipeline)
export SSQLGO=1
ssql from data.csv | ssql where -where age gt 25 | ssql generate-go

# Method 2: -generate flag per command
ssql from -generate data.csv | ssql where -generate -where age gt 25 | ssql generate-go
```

The environment variable approach is preferred for full pipelines.

### Code Fragment System

**Architecture (`cmd/ssql/lib/codefragment.go`):**
- Commands communicate via JSONL code fragments on stdin/stdout
- Each fragment has: Type, Var (variable name), Input (input var), Code, Imports, Command
- The `generate-go` command assembles all fragments into a complete Go program
- Fragments are passed through the pipeline, with each command adding its own

**Fragment Types:**
- `init` - First command (e.g., from), creates initial variable, no input
- `stmt` - Middle command (e.g., where, group-by), has input and output variable
- `final` - Last command (e.g., write-csv), has input but no output variable

**Helper Functions (in `cmd/ssql/helpers.go`):**
- `shouldGenerate(flagValue bool)` - Checks flag or SSQLGO env var
- `getCommandString()` - Returns command line that invoked the command (filters out -generate flag)
- `shellQuote(s string)` - Quotes arguments for shell safety

### Generation Support Status (as of v3.1.0)

**✅ Commands with -generate support:**
1. `from` - Generates init fragment with `ssql.ReadCSV()` or `lib.ReadJSON()`
2. `where` - Generates stmt fragment with filter predicate
3. `to csv` - Generates final fragment with `ssql.WriteCSV()`
4. `to json` - Generates final fragment with `ssql.WriteJSON()`
5. `to table` - Generates final fragment with `ssql.DisplayTable()`
6. `to chart` - Generates final fragment with `ssql.QuickChart()`
7. `limit` - Generates stmt fragment with `ssql.Limit[ssql.Record](n)`
8. `offset` - Generates stmt fragment with `ssql.Offset[ssql.Record](n)`
9. `sort` - Generates stmt fragment with `ssql.SortBy()`
10. `distinct` - Generates stmt fragment with `ssql.DistinctBy()`
11. `group-by` - Generates TWO stmt fragments (GroupByFields + Aggregate)
12. `union` - Generates stmt fragment with `ssql.Concat()` and optionally `ssql.DistinctBy(ssql.RecordKey)`
13. `join` - Generates stmt fragment with `ssql.Join()`

**Commands that don't need -generate:**
- `generate-go` - it's the assembler that produces the final Go code
- `functions` - displays help information only
- `version` - displays version only

**⚠️ IMPORTANT:** Commands without generation support will break pipelines in generation mode. Always add generation support when creating new commands.

### Adding Generation Support to Commands

**Step 1: Add generation function to `cmd/ssql/helpers.go`:**

```go
// generateMyCommandCode generates Go code for the my-command command
func generateMyCommandCode(arg1 string, arg2 int) error {
    // 1. Read all previous code fragments from stdin
    fragments, err := lib.ReadAllCodeFragments()
    if err != nil {
        return fmt.Errorf("reading code fragments: %w", err)
    }

    // 2. Pass through all previous fragments
    for _, frag := range fragments {
        if err := lib.WriteCodeFragment(frag); err != nil {
            return fmt.Errorf("writing previous fragment: %w", err)
        }
    }

    // 3. Get input variable from last fragment (or default to "records")
    var inputVar string
    if len(fragments) > 0 {
        inputVar = fragments[len(fragments)-1].Var
    } else {
        inputVar = "records"
    }

    // 4. Generate your command's Go code
    outputVar := "result"
    code := fmt.Sprintf("%s := ssql.MyCommand(%q, %d)(%s)",
        outputVar, arg1, arg2, inputVar)

    // 5. Create and write your fragment
    imports := []string{"fmt"}  // Add any needed imports
    frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
    return lib.WriteCodeFragment(frag)
}
```

**Step 2: Add -generate flag and check to command handler in `cmd/ssql/main.go`:**

```go
Subcommand("my-command").
    Description("Description of my command").

    Handler(func(ctx *cf.Context) error {
        var arg1 string
        var arg2 int
        var generate bool

        // Extract flags
        if val, ok := ctx.GlobalFlags["-arg1"]; ok {
            arg1 = val.(string)
        }
        if val, ok := ctx.GlobalFlags["-arg2"]; ok {
            arg2 = val.(int)
        }
        if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
            generate = genVal.(bool)
        }

        // Check if generation is enabled (flag or env var)
        if shouldGenerate(generate) {
            return generateMyCommandCode(arg1, arg2)
        }

        // Normal execution follows...
        // ...
    }).

    Flag("-generate", "-g").
        Bool().
        Global().
        Help("Generate Go code instead of executing").
        Done().

    Flag("-arg1").
        String().
        Global().
        Help("First argument").
        Done().

    // ... other flags

    Done().
```

**Step 3: Add tests to `cmd/ssql/generation_test.go`:**

```go
func TestMyCommandGeneration(t *testing.T) {
    buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
    if err := buildCmd.Run(); err != nil {
        t.Fatalf("Failed to build ssql: %v", err)
    }
    defer os.Remove("/tmp/ssql_test")

    cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test my-command -arg1 test -arg2 42`
    cmd := exec.Command("bash", "-c", cmdLine)
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Logf("Command output: %s", output)
    }

    outputStr := string(output)
    want := []string{`"type":"stmt"`, `"var":"result"`, `ssql.MyCommand`}
    for _, expected := range want {
        if !strings.Contains(outputStr, expected) {
            t.Errorf("Expected output to contain %q, got: %s", expected, outputStr)
        }
    }
}
```

### Special Cases

**Commands with multiple fragments (like group-by):**

Some commands generate multiple code fragments. For example, `group-by` generates:
1. `GroupByFields` fragment (with command string)
2. `Aggregate` fragment (empty command string - part of same CLI command)

```go
// Fragment 1: GroupByFields
frag1 := lib.NewStmtFragment("grouped", inputVar, groupCode, nil, getCommandString())
lib.WriteCodeFragment(frag1)

// Fragment 2: Aggregate (note: empty command string)
frag2 := lib.NewStmtFragment("aggregated", "grouped", aggCode, nil, "")
lib.WriteCodeFragment(frag2)
```

### Testing Code Generation

**Manual testing:**
```bash
# Test individual command
export SSQLGO=1
echo '{"type":"init","var":"records"}' | ./ssql my-command -arg1 test

# Test full pipeline
export SSQLGO=1
./ssql from data.csv | \
  ./ssql where -where age gt 25 | \
  ./ssql my-command -arg1 test | \
  ./ssql generate-go > program.go

# Compile and run generated code
go run program.go
```

**Automated tests:**
- All generation tests are in `cmd/ssql/generation_test.go`
- Run with: `go test -v ./cmd/ssql -run TestGeneration`
- Tests ensure the feature is never lost during refactoring

### Why This Matters

**Code generation is a CRITICAL feature because:**
1. It enables 10-100x performance improvement over CLI execution
2. Generated programs can be deployed without ssql CLI
3. It bridges prototyping (CLI) and production (compiled Go)
4. Breaking it silently breaks user workflows

**Always ensure:**
- New commands include -generate support
- Tests cover generation mode
- Changes to helpers.go don't break fragment system

### CLI Commands Must Use ssql Package Primitives (CRITICAL)

**⚠️ CLI commands must ALWAYS be implemented using ssql package functions, not raw Go code!**

The ssql CLI exists to make the ssql package accessible from the command line. Every CLI command should:
1. Map directly to one or more ssql package functions
2. Generate code that calls those same functions
3. Use minimal glue code between commands

**If a CLI feature requires logic that doesn't exist in the ssql package:**
- ✅ CORRECT: Add the functionality to the ssql package first, then use it in CLI
- ❌ WRONG: Generate raw Go code (loops, maps, custom logic) in the CLI

**Why this matters:**
- Users of the ssql package get the same functionality as CLI users
- Generated code is readable and educational
- Code can be composed with Chain() and other ssql primitives
- Maintenance is centralized in the ssql package

**Example - group-by with expressions:**
```go
// ❌ WRONG - Generated raw loops and maps
groups := make(map[string][]ssql.Record)
for record := range records {
    // ... manual grouping logic
}

// ✅ CORRECT - Use ssql package functions
grouped := ssql.GroupByFields("_group", "dept")(records)
aggregated := ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
    "total": ssql.ExprAgg("sum(salary * bonus)"),  // Add ExprAgg to ssql package
})(grouped)
```

**When adding new CLI features:**
1. First: Design and implement the ssql package function
2. Then: Update CLI to use that function
3. Finally: Update code generation to emit calls to that function

### Code Generation Requirements (CRITICAL)

**⚠️ NEVER release a ssql command that doesn't support code generation!**

Every data-processing command MUST support code generation (`-generate` flag / `SSQLGO=1`). This is non-negotiable because:
- Users rely on the CLI-to-compiled-Go workflow for production systems
- A single command without generation support breaks entire pipelines
- The feature is invisible until users try to generate code, then it fails

**Before releasing any new command:**
1. ✅ Implement `-generate` flag support
2. ✅ Add generation tests to `cmd/ssql/generation_test.go`
3. ✅ Test full pipeline: `SSQLGO=1 ssql from ... | ssql new-command ... | ssql generate-go`
4. ✅ Verify generated code compiles and runs correctly

**Exception:** Commands that don't process data (like `version`, `functions`, `generate-go` itself) don't need generation support.

### Error Handling Requirements (CRITICAL)

**⚠️ All errors MUST cause pipeline failure with clear error messages!**

This applies to BOTH execution mode AND code generation mode:

**Execution Mode:**
- Errors must be returned, not silently ignored
- Error messages must be clear and actionable
- Pipeline must stop on first error (fail-fast)

**Code Generation Mode:**
- Unsupported features must emit error fragments (`"type":"error"`)
- `generate-go` must detect error fragments and fail (no partial code output)
- Error messages must explain what's unsupported and suggest alternatives

**Example - Proper error fragment emission:**
```go
if unsupportedFeature {
    frag := lib.NewErrorFragment("feature X is not yet supported with -generate", getCommandString())
    lib.WriteCodeFragment(frag)
    return fmt.Errorf("feature X is not yet supported with -generate")
}
```

**Tests for error handling are in `cmd/ssql/generation_test.go`:**
- `TestGenerationErrorHandling` - errors prevent partial code
- `TestErrorFragmentPropagation` - errors propagate through pipeline
- `TestErrorFragmentFormat` - error fragments have correct format

## GPU Acceleration (Experimental)

**⚠️ GPU acceleration has been implemented and benchmarked. Results were surprising.**

### Actual Benchmark Results (RTX 5090 + Intel Core Ultra 9 275HX)

| Operation | CPU | GPU | Result |
|-----------|-----|-----|--------|
| Sum (1M float64) | 86μs | 601μs | **CPU 7x faster** |
| Filter+Sum (10M float64) | 0.8ms | 5.3ms | **CPU 6.6x faster** |
| Convolve (100K × 1K) | 195ms | 603μs | **GPU 320x faster** |
| FFT (1K points) | 5.2ms | 0.25ms | **GPU 21x faster** |
| FFT (1M points) | hours | 2.9ms | **GPU ∞ faster** |

**Key finding:** GPU wins big for compute-heavy operations (convolution: 18-320x, FFT: 21-100x+). For memory-bound operations (aggregations), CPU wins.

### Why GPU Loses for Aggregations

PCIe transfer overhead dominates:

```
1M float64 values (8MB):
  PCIe to GPU:    ~500μs+
  GPU sum:        ~0.1ms
  PCIe from GPU:  ~0.01ms
  Total GPU:      ~600μs

  CPU sum:        ~86μs (no transfer, fast memory)
```

Modern CPUs have 50-100 GB/s memory bandwidth. For simple arithmetic, the CPU finishes before the GPU transfer completes.

### The Record Extraction Problem

ssql's `Record` type uses Schema + `[]any`. Extracting values requires CPU work:

```go
// This is CPU-bound and often slower than the aggregation itself
values := make([]float64, len(records))
for i, r := range records {
    values[i] = ssql.GetOr(r, "price", 0.0)
}
```

**Arrow columnar format bypasses this** - data is already contiguous.

### Current GPU Implementation

```
gpu/
├── sum.cu           # CUDA kernels (sum, filter, FFT)
├── gpu.go           # Go wrappers (build tag: gpu)
├── gpu_stub.go      # Stubs for non-GPU builds
├── gpu_test.go      # Tests and benchmarks
└── Makefile         # Builds libssqlgpu.so
```

### Building with GPU Support

**Option 1: Docker Build (Recommended - no local CUDA needed)**

```bash
git clone https://github.com/rosscartlidge/ssql
cd ssql

# Build and extract the binary
make docker-gpu-extract

# Install the library and run
sudo cp libssqlgpu.so /usr/local/lib && sudo ldconfig
./ssql_gpu version
```

**Option 2: Local CUDA Toolkit**

Requires CUDA toolkit installed locally (nvcc compiler).

```bash
git clone https://github.com/rosscartlidge/ssql
cd ssql

# Build everything
make build-gpu

# Install library system-wide (one-time)
sudo make install-gpu

# Now ssql_gpu works without LD_LIBRARY_PATH
./ssql_gpu version
```

**Option 3: Docker Image (for container workflows)**

```bash
make docker-gpu-image
docker run --gpus all ssql:gpu version
docker run --gpus all -v $(pwd):/data ssql:gpu from /data/input.csv
```

**Available Makefile Targets:**

| Target | Description |
|--------|-------------|
| `make gpu` | Build CUDA library only (gpu/libssqlgpu.so) |
| `make build-gpu` | Build ssql_gpu binary with GPU support |
| `make install-gpu` | Install library to /usr/local/lib (requires sudo) |
| `make docker-gpu-image` | Build Docker image with ssql_gpu |
| `make docker-gpu-extract` | Build via Docker and extract binary |
| `make docker-gpu` | Alias for docker-gpu-extract |

**Running GPU Tests:**
```bash
# With local CUDA
make install-gpu
go test -tags gpu ./gpu/

# Or with LD_LIBRARY_PATH
LD_LIBRARY_PATH=./gpu go test -tags gpu ./gpu/
```

### What Works Now

```go
// Convolution (18-320x speedup) - compute-heavy
gpu.ConvolveDirect(signal, kernel)  // Best for kernel < 10K
gpu.ConvolveFFT(signal, kernel)     // Best for very large kernels

// FFT (21-100x+ speedup) - genuinely compute-bound
gpu.FFTMagnitude(data)
gpu.FFTMagnitudePhase(data)
```

### Don't Use GPU For

- **Simple aggregations** (sum, avg, count, min, max) - CPU is 7x faster
- **Chained filter operations** - CPU still wins on fast hardware
- **Small datasets** (<100K elements) - kernel launch overhead dominates
- **Anything memory-bound** - fast CPUs win

### Benchmark Validation Lesson (January 2026)

**⚠️ Always sanity-check benchmark results against theoretical expectations.**

We incorrectly concluded "GPU FFT provides no benefit" based on flawed benchmarks showing:
```
Old (WRONG):  1M-point FFT = 4.2ms CPU, 4.2ms GPU  → "Tie"
New (CORRECT): 1M-point FFT = 125ms CPU, 4.4ms GPU → GPU 28x faster
```

The old CPU benchmark was **30x too fast** - likely due to:
- Compiler optimizing away unused results
- Measuring setup/allocation instead of actual computation
- Some other measurement error

**How to catch this:** A 1M-point Cooley-Tukey FFT performs ~20M complex multiply-adds. At 125ms, that's ~6ns per operation (reasonable with cache effects). At 4.2ms, that would be 0.2ns per operation (faster than a single CPU cycle - impossible).

**Rule:** If benchmark results seem too good, they probably are. Verify that:
1. Results are actually being used (prevent dead code elimination)
2. You're timing the right code path
3. Numbers make sense given algorithm complexity

### Future GPU Opportunities

1. **FFT CLI command** - leverage existing cuFFT implementation
2. **Arrow → GPU direct transfer** - bypass Record extraction entirely
3. **Compute-heavy operations** - matrix ops, convolution, spectral analysis

**Reference:** See `doc/research/gpu-arrow-learnings.md` for detailed analysis and benchmark data.

## Arrow Format Support

ssql supports Apache Arrow format for high-performance I/O:

**Benefits:**
- 10-20x faster than CSV/JSON
- Zero-copy memory mapping
- Columnar layout (cache-friendly)
- ZSTD compression support
- GPU-ready (contiguous numeric arrays)

**Usage:**
```bash
ssql from data.arrow | ssql where -where age gt 25 | ssql to arrow output.arrow
```

**When to use Arrow:**
- Large datasets (>100K records)
- Repeated processing of same data
- GPU acceleration (data already columnar)
- Inter-process data sharing

**When to use CSV/JSON:**
- Human-readable output needed
- Small datasets
- Interop with non-Arrow tools