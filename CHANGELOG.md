# Changelog

All notable changes to ssql will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Breaking Changes
- **Mandatory Schema Headers**: `ssql from` now ALWAYS emits schema headers
  - Removed `-schema` flag - schema is now automatic
  - Enables strongly-typed pipelines and future GPU acceleration optimizations
  - No migration needed - downstream commands already handle schema headers
  - **Reason**: Schema information is essential for type-aware operations

- **JOIN API Change**: `JoinPredicate` changed from function type to interface
  - **Migration Required**: Custom join predicates must now use `OnCondition()` wrapper
  - **No Impact**: Code using `OnFields()` or `OnCondition()` remains unchanged
  - **Reason**: Enables hash join optimization for dramatic performance improvements

  **Before (v1.0.x):**
  ```go
  // This will NO LONGER compile:
  var pred ssql.JoinPredicate = func(left, right ssql.Record) bool {
      return left["id"] == right["id"]
  }
  ```

  **After (v1.1.0+):**
  ```go
  // Use OnCondition wrapper:
  pred := ssql.OnCondition(func(left, right ssql.Record) bool {
      return ssql.GetOr(left, "id", "") == ssql.GetOr(right, "id", "")
  })

  // OR use OnFields for automatic optimization:
  pred := ssql.OnFields("id")
  ```

### Performance Improvements
- **Hash Join Optimization**: 3-16x faster joins with `OnFields()`
  - `OnFields()` now uses O(n+m) hash join instead of O(n×m) nested loop
  - Custom predicates via `OnCondition()` still use nested loop (no change in behavior)
  - Applies to all join types: `InnerJoin`, `LeftJoin`, `RightJoin`, `FullJoin`
  - **Benchmark Results (1K×1K records)**:
    - InnerJoin: 3.6x faster (6.7ms vs 24ms)
    - LeftJoin: 3.7x faster (6.7ms vs 24.6ms)
    - Multi-field joins: 16x faster (1.4ms vs 22ms)
  - Automatic optimization - no code changes needed for existing `OnFields()` usage

### New Features
- **`-expr` Code Generation**: `group-by -expr` now supports code generation
  - `SSQLGO=1 ssql group-by dept -expr 'sum(salary * bonus)' total` generates compilable Go code
  - Uses `runtime.EvalBatchAgg()` for expression evaluation in generated code
  - Combined with built-in aggregations: `-count num -expr 'sum(salary)' total`

- Added `KeyExtractor` interface for custom optimized join predicates
  - Advanced users can implement both `JoinPredicate` and `KeyExtractor`
  - Enables custom hash-based join optimizations beyond field equality
  - See documentation for examples

### Fixed
- **Code Generation Error Handling**: Errors now prevent partial code output
  - Added error fragment type to code generation pipeline
  - `generate-go` detects errors and fails cleanly instead of outputting broken code
  - Unsupported features now fail fast with clear error messages

### Added
- **Runtime Package**: `cmd/ssql/lib/runtime` for generated code helpers
  - `EvalBatchAgg()` for evaluating aggregation expressions on record groups
  - `ApplyValue()` for type-safe value assignment in generated code
  - Enables `-expr` code generation without duplicating complex logic

- Comprehensive benchmark suite (`join_benchmark_test.go`)
  - Compares hash vs nested loop performance
  - Tests various dataset sizes (100, 1K, 10K records)
  - Includes multi-field join benchmarks

### Internal Changes
- Split join implementations into `*JoinHash` and `*JoinNested` helper functions
- Automatic dispatch based on `KeyExtractor` interface support
- Maintains backward compatibility for all `OnFields()` and `OnCondition()` usage

## [v4.2.0] - 2025-01-05

### New Features
- **Schema Headers (`-schema` flag)**: Preserve field order and types through CLI pipelines
  - `ssql from data.csv -schema` emits a schema header as first line of JSONL output
  - Schema contains field names in order and their inferred types (string, int, float, bool)
  - Output commands (`to csv`, `to json`, `to table`) use schema for consistent field ordering
  - Solves non-deterministic JSON field order issue in pipelines

### Added
- Schema header format: `{"_schema":{"fields":["name","age"],"types":{"name":"string","age":"int"}}}`
- `ReadJSONLWithSchema()` in lib package for reading JSONL with schema headers
- `WriteJSONLWithSchemaOrdered()` for writing JSONL with schema and field ordering
- `WriteJSONWithFieldOrder()` for ordered JSON/JSONL output
- `InferFromRecordOrdered()` for schema inference with field ordering

### Documentation
- Updated README.md with `-schema` flag example
- Added "Schema Headers" section to CLI tutorial (doc/cli/codelab-cli.md)
- Added "JSONL Schema Headers" section to API reference (doc/api-reference.md)
- Documented all `from` command flags: `-schema`, `-type`, `-default-type`, `-format`

## [v4.1.0] - 2025-01-04

### New Features
- **Custom Aggregation Expressions**: `group-by` now supports `-expr` flag for custom aggregations
  - `ssql group-by region -expr 'sum(revenue) / count()' avg_revenue`
  - Uses expr-lang for powerful aggregation expressions
  - Access group records via `records` variable in expressions

## [v1.0.5] - 2024-11-02

### Changed
- Version management now tied to git tags
- Added embedded version.txt for reliable version tracking
- Improved bash completion with alias support

## [v1.0.0] - 2024-11-01

### Breaking Changes
- Record migrated to encapsulated struct with private fields
- Use `MakeMutableRecord()` builder pattern for record creation
- Access fields via `Get()`, `GetOr()`, `.All()` methods

### Added
- Complete Record encapsulation for better API design
- MutableRecord builder for efficient record construction
- Comprehensive test suite

[Unreleased]: https://github.com/rosscartlidge/ssql/compare/v4.2.0...HEAD
[v4.2.0]: https://github.com/rosscartlidge/ssql/compare/v4.1.0...v4.2.0
[v4.1.0]: https://github.com/rosscartlidge/ssql/compare/v1.0.5...v4.1.0
[v1.0.5]: https://github.com/rosscartlidge/ssql/compare/v1.0.0...v1.0.5
[v1.0.0]: https://github.com/rosscartlidge/ssql/releases/tag/v1.0.0
