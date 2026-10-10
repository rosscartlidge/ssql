# ssql API Reference

The `Record` API of `github.com/rosscartlidge/ssql/v4`: every exported
type and function, with what it does and, where it is not obvious, an
example. Signatures are as `go doc` prints them; `go doc
github.com/rosscartlidge/ssql/v4.Name` has the source comment for any
one of them. The [Getting Started Guide](codelab-intro.md) is the
tutorial; the [`ssql/typed` reference](typed-reference.md) covers the
struct-based fast path.

[Back to Documentation](README.md)

## Table of Contents

1. [Overview](#overview)
2. [Core Types](#core-types) — Record, MutableRecord, Schema, Value, JSONString, times, Filter
3. [Record Helpers](#record-helpers) — Get/GetOr/Set, shaping, casting, comparisons, keys
4. [Creating and Consuming Iterators](#creating-and-consuming-iterators)
5. [Transform Operations](#transform-operations)
6. [Filter Operations](#filter-operations)
7. [Limiting & Pagination](#limiting--pagination) — limit, offset, tail, sampling, early termination
8. [Ordering Operations](#ordering-operations) — sort, spill, merge, top-k
9. [Aggregation & Analysis](#aggregation--analysis) — group-by, aggregates, rollup, describe, running stats
10. [Reshaping](#reshaping) — pivot, unpivot, fill, extract, flatten, hash
11. [Joins and Set Operations](#joins-and-set-operations)
12. [Window and Time](#window-and-time) — analytic functions, batch windows, resampling
13. [Composition](#composition) — Pipe, Chain, Tee
14. [I/O Operations](#io-operations) — CSV, TSV, JSON, lines, commands, Parquet, Arrow, XLSX, WAV, HTTP, tables
15. [Signal Processing](#signal-processing)
16. [Chart & Visualization](#chart--visualization)
17. [Error Handling](#error-handling)
18. [Appendix: CLI and Codegen Support](#appendix-cli-and-codegen-support)

---

## Overview

ssql pipelines are Go 1.23 iterators composed with functions:

```go
iter.Seq[T]           // a lazy sequence
iter.Seq2[T, error]   // a lazy sequence that can report a failure per element
type Filter[T, U any] func(iter.Seq[T]) iter.Seq[U]
type FilterWithErrors[T, U any] func(iter.Seq2[T, error]) iter.Seq2[U, error]
```

A `Filter` is a function from one sequence to another; a pipeline is a
chain of them applied to a source:

```go
data, err := ssql.ReadCSV("sales.csv")
if err != nil {
    log.Fatal(err)
}
top := ssql.Chain(
    ssql.GroupByFields("rows", "region"),
    ssql.Aggregate("rows", map[string]ssql.AggregateFunc{"total": ssql.Sum("amount")}),
    ssql.SortBy(func(r ssql.Record) float64 { return -ssql.GetOr(r, "total", 0.0) }),
    ssql.Limit[ssql.Record](5),
)(data)
for r := range top {
    fmt.Println(ssql.GetOr(r, "region", ""), ssql.GetOr(r, "total", 0.0))
}
```

Nothing runs until the `for` loop pulls; every operation streams, and a
consumer that stops early stops the source. The CLI is a thin layer over
these functions, and `ssql generate go` emits programs that call them.

---

## Core Types

### Record

A `Record` is an immutable, ordered set of named values. Its fields are
private; there is no map access.

```go
type Record struct { /* private */ }
```

**Wrong — Record is not a map (compile errors):**
```go
record["name"] = "Alice"
value := record["age"]
for k, v := range record { }
```

**Right — build with `MakeMutableRecord`, read with `Get`/`GetOr`:**
```go
record := ssql.MakeMutableRecord().
    String("name", "Alice").
    Int("age", int64(30)).
    Float("score", 95.5).
    Freeze()

name := ssql.GetOr(record, "name", "")
age, exists := ssql.Get[int64](record, "age")
updated := ssql.SetImmutable(record, "score", 98.0)   // a new Record; record is unchanged

for key, value := range record.All() {
    fmt.Printf("%s: %v\n", key, value)
}
```

Supported value types are `int64`, `float64`, `string`, `bool`,
`time.Time`, `JSONString`, a nested `Record`, `[]any`, and the sequence
types listed under [`Value`](#value-and-orderedvalue). Scalars are always
`int64` and `float64`; there is no `int`, `int32` or `float32` in a
Record.

**Reading and iterating:**

```go
func (r Record) Has(field string) bool        // the field exists (a nil slot counts)
func (r Record) HasValue(field string) bool   // the field exists and holds a value
func (r Record) Keys() []string
func (r Record) KeysIter() iter.Seq[string]
func (r Record) Values() iter.Seq[any]
func (r Record) All() iter.Seq2[string, any]  // fields in order
func (r Record) Len() int
func (r Record) Schema() *Schema
func (r Record) Clone() Record
func (r Record) Equal(other Record) bool
func (r Record) ToMutable() MutableRecord
func (r Record) AppendJSON(buf []byte) []byte                          // fast JSON, pre-computed field prefixes
func (r Record) AppendJSONOrdered(buf []byte, fieldOrder []string) []byte
```

**Immutable setters** — each returns a new Record with one field set:

```go
func (r Record) String(field, value string) Record
func (r Record) Int(field string, value int64) Record
func (r Record) Float(field string, value float64) Record
func (r Record) Bool(field string, value bool) Record
func (r Record) Time(field string, value time.Time) Record
func (r Record) JSONString(field string, value JSONString) Record
func (r Record) Nested(field string, value Record) Record
func (r Record) IntSeq(field string, value iter.Seq[int]) Record
func (r Record) Int64Seq(field string, value iter.Seq[int64]) Record
func (r Record) Float64Seq(field string, value iter.Seq[float64]) Record
func (r Record) StringSeq(field string, value iter.Seq[string]) Record
func (r Record) BoolSeq(field string, value iter.Seq[bool]) Record
func (r Record) TimeSeq(field string, value iter.Seq[time.Time]) Record
func (r Record) RecordSeq(field string, value iter.Seq[Record]) Record
```

Building several fields this way copies the record each time; use
`MutableRecord` for construction. `Record` and `MutableRecord` implement
`json.Marshaler` and `json.Unmarshaler`.

### MutableRecord

The builder: mutates in place, then `Freeze()` yields the `Record`.

```go
func MakeMutableRecord() MutableRecord
func MakeMutableRecordWithCapacity(capacity int) MutableRecord

func (m MutableRecord) String(field, value string) MutableRecord
func (m MutableRecord) Int(field string, value int64) MutableRecord
func (m MutableRecord) Float(field string, value float64) MutableRecord
func (m MutableRecord) Bool(field string, value bool) MutableRecord
func (m MutableRecord) Time(field string, value time.Time) MutableRecord
func (m MutableRecord) JSONString(field string, value JSONString) MutableRecord
func (m MutableRecord) Nested(field string, value Record) MutableRecord
func (m MutableRecord) IntSeq(field string, value iter.Seq[int]) MutableRecord
func (m MutableRecord) Int64Seq(field string, value iter.Seq[int64]) MutableRecord
func (m MutableRecord) Float64Seq(field string, value iter.Seq[float64]) MutableRecord
func (m MutableRecord) StringSeq(field string, value iter.Seq[string]) MutableRecord
func (m MutableRecord) BoolSeq(field string, value iter.Seq[bool]) MutableRecord
func (m MutableRecord) TimeSeq(field string, value iter.Seq[time.Time]) MutableRecord
func (m MutableRecord) RecordSeq(field string, value iter.Seq[Record]) MutableRecord
func (m MutableRecord) Null(field string) MutableRecord              // a present field with no value
func (m MutableRecord) Rename(oldField, newField string) MutableRecord
func (m MutableRecord) Delete(field string) MutableRecord
func (m MutableRecord) Len() int
func (m MutableRecord) Freeze() Record
```

```go
records := []ssql.Record{
    ssql.MakeMutableRecord().String("id", "001").Int("count", 42).Freeze(),
    ssql.MakeMutableRecord().String("id", "002").Int("count", 7).Freeze(),
}
```

There is no `SetAny`: a value of a type outside `Value` cannot be stored.
The generic [`Set`](#record-helpers) is the type-checked equivalent.

### Schema and fast construction

```go
type Schema struct { /* private */ }
func NewSchema(fields []string) *Schema                         // field order preserved
func NewRecordFromSchema(schema *Schema, values []any) Record   // the fast path: values used directly, not copied
func NewRecord(fields map[string]any) Record                    // from a map (compatibility; builds a Schema per call)
func (s *Schema) Fields() []string
func (s *Schema) Has(field string) bool
func (s *Schema) Index(field string) int   // -1 when absent
func (s *Schema) Width() int
```

Create one `Schema` per source with `NewSchema(headers)` and build every
record from it with `NewRecordFromSchema`, as the CSV and JSONL readers
do. A schema per record is several times slower on large files.

### Value and OrderedValue

```go
type Value interface {
    ~int64 | ~float64 |
    ~bool | string | time.Time |
    JSONString | Record |
    []any |
    iter.Seq[int] | iter.Seq[int64] | iter.Seq[float64] |
    iter.Seq[bool] | iter.Seq[string] | iter.Seq[time.Time] |
    iter.Seq[Record] | iter.Seq[any]
}
type OrderedValue interface { ~int64 | ~float64 | string }
```

`Value` is the constraint on what a Record holds and what `Set`, `Field`
and `SetImmutable` accept: canonical scalars (`int64`, `float64`), the
listed sequence types, `[]any` for `Collect` results and `iter.Seq[any]`
for `CollectSeq`. `OrderedValue` is the constraint for `Min[T]` /
`Max[T]`.

### JSONString

A string that holds valid JSON, for structured data embedded in a Record.
**It is the one representation of a nested value** (DFC144 Level 0,
2026-10-08): every reader — the wire, `from json` on an array file,
`ReadJSON`, `ReadJSONAuto` — turns a JSON array or object into a
`JSONString` holding its text (compacted, keys in source order), and
`Collect` returns its list as one. The `_schema` wire type of such a
field is `json`; `FieldTypeJSON` is its `FieldType` (`ParseFieldType("json")`,
`cast -type F json`, which validates the text). A typed program holds a
`json` field as a `string` and its typed→Record boundary calls
`JSONOrNull` to put it back on the wire as JSON. `[]any` and
`map[string]any` are no longer produced by any reader (they remain legal
`Value`s for in-memory use).

```go
func JSONOrNull(text string) any                    // JSONString(text), or nil for "" — a typed program's json field back on the wire
func ExprValue(v any) any                           // what an expression sees for a field: a JSONString parsed to []any / map[string]any (whole numbers int64), else v
func NestedValue(v any) (JSONString, bool)          // a slice, map or JSONString result as JSON text; false for scalars and strings
func JSONValue(v any) JSONString                    // json.Marshal as a JSONString (panics with an error on a value that cannot be marshalled)
func SynthesizeDottedFields(env map[string]any, identifiers []string) // columns named items.qty (after flatten) reachable as items.qty in an expression environment
```

`Get`, `GetOr`, `Has` and `HasValue` resolve a **dotted path** into a
nested value when the name is not a field: `GetOr(r, "addr.city", "")`,
`GetOr(r, "tags.0", "")` (0-based, negative from the end), `a.b.c`
through nested objects; the longest leading segment that is a field is
the head, so a literal field named `a.b` wins (DFC144 Level 2). Every
command's field reads go through `Get`, which is how `where -if
addr.city eq NYC` works with no command-level support.

`ExprValue` opens a nested value for the expression language (DFC144
Level 1): `len(tags)`, `tags[0]`, `addr.city` and `"go" in tags` work on
a `json` field. `NestedValue` closes a list or map result back into one;
`update -set-expr`, `group-by -expr` and the generated code call it, so
a `split(…)` result is a `json` list the next stage can open.

```go
type JSONString string
func NewJSONString(value any) (JSONString, error)
func (js JSONString) Parse() (any, error)
func (js JSONString) MustParse() any
func (js JSONString) IsValid() bool
func (js JSONString) Pretty() string
func (js JSONString) String() string
```

```go
meta, err := ssql.NewJSONString(map[string]any{"status": "active", "count": 42})
if err != nil {
    log.Fatal(err)
}
record := ssql.MakeMutableRecord().String("id", "user123").JSONString("metadata", meta).Freeze()
value, err := ssql.GetOr(record, "metadata", ssql.JSONString("")).Parse()
```

### Times

One parser serves every path that turns a value into a time: `GetOr(r,
"ts", time.Time{})`, the `time` wire type, `cast -type F time` and the
expression function `date()`.

```go
func ParseTime(val any) (time.Time, bool)       // time.Time as is; RFC 3339, "2006-01-02 15:04:05",
                                                 // "2006-01-02T15:04:05" (UTC), "2006-01-02 15:04:05+00",
                                                 // "2006-01-02"; int64 Unix seconds
func MustParseTime(val any, field string) time.Time   // panics naming the field: an explicit cast never yields a zero time
func ParseFieldType(s string) (FieldType, error)      // "int", "float", "bool", "string", "time" ("timestamp", "datetime", "date" are aliases)
func (ft FieldType) String() string

type FieldType int
const (
    FieldTypeAuto FieldType = iota
    FieldTypeString
    FieldTypeInt
    FieldTypeFloat
    FieldTypeBool
    FieldTypeTime
)

func ExprFieldShadowing() expr.Option   // expr.Compile option: a field named like a builtin (`date`) is the field when bare
func ExprDate() expr.Option             // expr.Compile option: ssql's date() in place of expr-lang's
```

A time renders as RFC 3339 wherever it becomes text: JSONL, CSV/TSV,
tables, and `GetOr(r, "ts", "")`.

### Filter types

```go
type Filter[T, U any] func(iter.Seq[T]) iter.Seq[U]
type FilterWithErrors[T, U any] func(iter.Seq2[T, error]) iter.Seq2[U, error]
```

Every operation below returns one of these, so operations compose with
`Pipe`, `Chain` or plain application.

---

## Record Helpers

### Field access

```go
func Get[T any](record Record, key string) (T, bool)        // typed read with numeric/string conversion
func GetOr[T any](record Record, key string, defaultValue T) T
func Set[V Value](m MutableRecord, field string, value V) MutableRecord   // type-checked set, in place
func SetImmutable[V Value](r Record, field string, value V) Record        // a new Record with the field set
func Field[V Value](key string, value V) Record                           // a one-field Record
func ValidateRecord(r Record) error                                       // every value is a Value
```

```go
age := ssql.GetOr(record, "age", int64(0))     // int64 for whole numbers
price := ssql.GetOr(record, "price", 0.0)      // float64 for decimals
name := ssql.GetOr(record, "name", "Unknown")
n, ok := ssql.Get[int64](record, "count")      // "42" converts; "abc" is !ok

mut := ssql.MakeMutableRecord()
mut = ssql.Set(mut, "name", "Alice")
mut = ssql.Set(mut, "age", int64(30))
record := mut.Freeze()
```

`Get` and `GetOr` convert between numeric kinds and from text
(`"42"` → `int64(42)`); an absent field or a failed conversion gives
`ok == false` / the default. Always read CSV numbers as `int64` or
`float64`, not as strings.

### Shaping

```go
func Project(r Record, fields ...string) Record   // only the named fields, in the order named (`include`)
func Without(r Record, fields ...string) Record   // the named fields removed, the rest in order (`exclude`)
func CopyField(m MutableRecord, src Record, target, source string) MutableRecord  // `update -set-field`: value and type; an absent source leaves the target absent
```

### Casting

```go
func CastValue(v any, target FieldType) (any, bool)   // the one conversion behind `cast`
func CastField(mut MutableRecord, src Record, field string, target FieldType, invalidMissing bool, invalid *int64) MutableRecord
type CastError struct{ Field string; Value any; Target FieldType }
func (e *CastError) Error() string
```

A value that cannot be converted is a `*CastError`, never a zero;
`CastField` panics with it unless `invalidMissing` leaves the field absent
and counts it.

### Comparisons

The primitives behind `where -if`, `-if-field` and `update -if`; exec
and generated code share them.

```go
func FieldOp(r Record, left, op, right string) bool          // left OP right, both fields of r
func ValueOp(a any, op string, b any) bool                    // FieldOp on two values
func FieldOpValues(a any, op string, b any) (bool, error)     // reports a mixed-kind pairing as *CompareError
func LiteralOp(v any, op, literal string) (bool, error)       // v OP literal, the literal read in v's kind
func CompareLiteral(r Record, field, op, literal string) bool // LiteralOp on a field; panics *CompareError
func CompareAny(a, b any) int                                 // -1/0/1: nil first, numbers numerically, then text
func CompareRecordFields(a, b Record, orderBy []OrderField) int
type CompareError struct{ Field, Op, Value, Kind string; Fields bool }
func (e *CompareError) Error() string
```

The operators are `where`'s: `eq ne gt ge lt le contains startswith
endswith regex`. Either side absent makes the condition false. A literal
not of the field's kind (`age gt abc`) is a `*CompareError`, never a
silent false; an int against a fractional literal compares as float64;
the string operators need text on both sides.

### Keys and inference

```go
func RecordKey(r Record) string          // one canonical string for the whole row, field order independent (DistinctBy(RecordKey) is SQL UNION)
func StableKey(value any) string         // canonical text for any value, independent of map order
func ZeroPaddedNumber(s string) bool     // 007, 02134: an identifier, never a number — one rule for every inference site
```

---

## Creating and Consuming Iterators

```go
func From[T any](slice []T) iter.Seq[T]                    // same as slices.Values, named for discoverability
func Concat[T any](seqs ...iter.Seq[T]) iter.Seq[T]        // all of the first, then the second, …; `union -all`
func ToChannelErr[T any](sb iter.Seq[T]) (<-chan T, func() error)  // pulled in a goroutine; wait() returns nil or the stage failure
func ToChannel[T any](sb iter.Seq[T]) <-chan T                      // Deprecated: a stage failure closes the channel and is lost; use ToChannelErr
func FromChannelSafe[T any](itemCh <-chan T, errCh <-chan error) iter.Seq2[T, error]
func ToChannelWithErrors[T any](sb iter.Seq2[T, error]) (<-chan T, <-chan error)

func Safe[T any](seq iter.Seq[T]) iter.Seq2[T, error]          // bridge into the error-aware world (never errors)
func Unsafe[T any](seq iter.Seq2[T, error]) iter.Seq[T]        // panic on the first failure
func IgnoreErrors[T any](seq iter.Seq2[T, error]) iter.Seq[T]  // skip failures
func CloseWhenDone[T any](seq iter.Seq[T], c io.Closer) iter.Seq[T]
```

```go
numbers := ssql.From([]int{1, 2, 3, 4, 5})
records := slices.Values([]ssql.Record{a, b, c})

itemCh, errCh := ssql.ToChannelWithErrors(ssql.ReadCSVSafe("data.csv"))
go func() {
    for err := range errCh {
        log.Printf("error: %v", err)
    }
}()
for record := range itemCh {
    _ = record
}
```

`CloseWhenDone` closes `c` when the sequence is fully read or abandoned;
a lazy reader cannot be paired with `defer f.Close()` in the function that
returns it.

---

## Transform Operations

```go
func Select[T, U any](fn func(T) U) Filter[T, U]                        // SQL SELECT: one output per input
func SelectSafe[T, U any](fn func(T) (U, error)) FilterWithErrors[T, U]
func SelectMany[T, U any](fn func(T) iter.Seq[U]) Filter[T, U]          // flatten: zero or more outputs per input
func Update(fn func(MutableRecord) MutableRecord) Filter[Record, Record] // Select for Records: ToMutable + Freeze done for you
```

```go
doubled := ssql.Select(func(x int) int { return x * 2 })(numbers)

words := ssql.SelectMany(func(line string) iter.Seq[string] {
    return slices.Values(strings.Fields(line))
})(lines)

updated := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
    frozen := mut.Freeze()
    price := ssql.GetOr(frozen, "price", float64(0))
    qty := ssql.GetOr(frozen, "quantity", int64(0))
    return mut.Float("total", price*float64(qty)).Time("updated_at", time.Now())
})(records)
```

`Update` is `Select(func(r Record) Record { return fn(r.ToMutable()).Freeze() })`.

---

## Filter Operations

```go
func Where[T any](predicate func(T) bool) Filter[T, T]                        // SQL WHERE
func WhereSafe[T any](predicate func(T) (bool, error)) FilterWithErrors[T, T]
func Distinct[T comparable]() Filter[T, T]                                    // first occurrence of each value
func DistinctBy[T any, K comparable](keyFn func(T) K) Filter[T, T]            // first occurrence of each key
```

```go
evens := ssql.Where(func(x int) bool { return x%2 == 0 })(numbers)
adults := ssql.Where(func(r ssql.Record) bool { return ssql.GetOr(r, "age", int64(0)) >= 18 })(records)
unique := ssql.DistinctBy(ssql.RecordKey)(records)   // whole-row distinct, SQL UNION's dedupe
```

`Distinct` and `DistinctBy` stream in O(distinct keys) memory.

---

## Limiting & Pagination

```go
func Limit[T any](n int) Filter[T, T]                 // SQL LIMIT: the first n, then the source stops
func LimitSafe[T any](n int) FilterWithErrors[T, T]
func Offset[T any](n int) Filter[T, T]                // SQL OFFSET: skip the first n
func OffsetSafe[T any](n int) FilterWithErrors[T, T]
func TakeLast[T any](n int) Filter[T, T]              // the last n in arrival order (`limit -last N`): a ring buffer, a barrier
func SampleN[T any](n int, seed int64) Filter[T, T]           // exactly n rows, reservoir sampling, input order
func SamplePercent[T any](p float64, seed int64) Filter[T, T] // each row with probability p/100, streaming
```

Sampling is deterministic under a seed; `sample` selects, it does not
shuffle. File-level byte-offset sampling is [`SampleCSVFile`](#sampling-tailing-and-counting).

### Early termination

```go
func TakeWhile[T any](predicate func(T) bool) Filter[T, T]
func TakeUntil[T any](predicate func(T) bool) Filter[T, T]
func SkipWhile[T any](predicate func(T) bool) Filter[T, T]
func SkipUntil[T any](predicate func(T) bool) Filter[T, T]
func Timeout[T any](duration time.Duration) Filter[T, T]                        // stop after wall-clock duration
func TimeBasedTimeout(timeField string, duration time.Duration) Filter[Record, Record]  // stop when the time field advances by duration
```

These are how a pipeline over an endless source ends; the [Getting
Started Guide](codelab-intro.md#infinite-streams) shows them on a live
stream.

---

## Ordering Operations

```go
func Sort[T cmp.Ordered]() Filter[T, T]
func SortDesc[T cmp.Ordered]() Filter[T, T]
func SortBy[T any, K cmp.Ordered](keyFn func(T) K) Filter[T, T]
func SortFunc[T any](cmpFn func(T, T) int) Filter[T, T]      // by comparator; stable
func SortRecords(orderBy []OrderField) Filter[Record, Record] // several fields, each ascending or descending, mixed types; stable
func Reverse[T any]() Filter[T, T]
type OrderField struct{ Field string; Desc bool }
```

Every sort materialises its input (O(N) memory) and is a barrier.
`SortFunc` and `SortRecords` are stable: equal elements keep input order,
so every lane agrees on ties.

### Sorting larger than memory

```go
type SpillConfig struct {
    Dir         string // run directory's parent ("" = the system temp dir)
    MemoryBytes int64  // run budget, estimated; 0 = 1 GiB
}
func SortRecordsSpill(orderBy []OrderField, cfg SpillConfig) Filter[Record, Record]
func ParseMemorySize(s string) (int64, error)   // "512M", "4G", bytes
func MergeSorted(orderBy []OrderField, sources ...iter.Seq[Record]) iter.Seq[Record]   // k-way merge of sorted sources, O(k) memory, stable
```

`SortRecordsSpill` is `SortRecords` with bounded memory: runs of at most
the budget are sorted in memory and written under `Dir` (gob-encoded, so
every value keeps its type), then k-way merged with `MergeSorted`. An
input that fits in one run is sorted in memory and nothing is written.
The run directory is removed when the merge ends, when the consumer stops
early, and on SIGINT/SIGTERM. A record holding a sequence or nested
record cannot be spilled (a panic with a clear message). `sort -spill`
and `group-by -spill` use it.

```go
shard1, _ := ssql.ReadCSV("shard1.csv")
shard2, _ := ssql.ReadCSV("shard2.csv")
merged := ssql.MergeSorted([]ssql.OrderField{{Field: "timestamp"}}, shard1, shard2)
```

### Top-k

```go
func TopBy[T any, K cmp.Ordered](n int, keyFn func(T) K) Filter[T, T]     // highest first
func BottomBy[T any, K cmp.Ordered](n int, keyFn func(T) K) Filter[T, T]  // lowest first
func TopByFunc[T any](n int, cmp func(a, b T) int) Filter[T, T]
func BottomByFunc[T any](n int, cmp func(a, b T) int) Filter[T, T]
```

A bounded heap of size n: O(N log n) time and O(n) memory, against a full
sort's O(N log N) and O(N). The `Func` forms rank by a comparator
(`CompareAny` orders numbers numerically and everything else lexically,
so one field may hold mixed values). Behind the `top` command.

---

## Aggregation & Analysis

### Group-by

```go
func GroupByFields(sequenceField string, fields ...string) Filter[Record, Record]     // one record per distinct key; the group's rows in sequenceField
func GroupBy[K comparable](sequenceField string, keyField string, keyFn func(Record) K) Filter[Record, Record]
func StreamGroupByFields(sequenceField string, fields ...string) Filter[Record, Record] // input already sorted by the fields: one group in memory (`group-by -presorted`)
func Aggregate(sequenceField string, aggregations map[string]AggregateFunc) Filter[Record, Record]
func AggregateOrdered(sequenceField string, aggregations []NamedAgg) Filter[Record, Record]   // result fields in the order given
type NamedAgg struct{ Name string; Fn AggregateFunc }
```

```go
grouped := ssql.GroupByFields("sales", "region", "product")(records)
results := ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
    "total_sales": ssql.Sum("amount"),
    "avg_sale":    ssql.Avg("amount"),
    "count":       ssql.Count(),
})(grouped)
```

`GroupByFields` collects each group's records into `sequenceField`;
`Aggregate` replaces that field with the named results. `AggregateOrdered`
is the same with a defined column order, which is what `group-by` emits.

### Aggregate functions

```go
type AggregateFunc func([]Record) AggregateResult
type AggregateResult interface{ GetValue() any }   // sealed; only AggResult[V] implements it
type AggResult[V Value] struct{ /* private */ }
func (a AggResult[V]) GetValue() any

func Count() AggregateFunc
func Sum(field string) AggregateFunc
func Avg(field string) AggregateFunc
func Min[T OrderedValue](field string) AggregateFunc
func Max[T OrderedValue](field string) AggregateFunc
func First[T Value](field string) AggregateFunc
func Last[T Value](field string) AggregateFunc
func Collect(field string) AggregateFunc            // every value, as a JSONString list (`json` on the wire)
func CollectSeq[T Value](field string) AggregateFunc // every value of type T, as iter.Seq[any]

func MinOf(field string) AggregateFunc      // keeps the field's own type: numbers, strings, times
func MaxOf(field string) AggregateFunc
func FirstOf(field string) AggregateFunc    // first present value in arrival order
func LastOf(field string) AggregateFunc
func CountDistinct(field string) AggregateFunc
func StringAgg(field, sep string) AggregateFunc   // string_agg; values formatted by AggValueString
func ArgMax(field, by string) AggregateFunc       // FIELD from the record where BY is largest
func ArgMin(field, by string) AggregateFunc
func Median(field string) AggregateFunc
func Mode(field string) AggregateFunc             // the most frequent present value, its own type
func Percentile(field string, p float64) AggregateFunc   // continuous p-quantile, 0 ≤ p ≤ 1
func StdDev(field string) AggregateFunc                  // sample
func Variance(field string) AggregateFunc                // sample (n−1)

func ExprAgg(expression string) AggregateFunc                             // sum(price*qty), count(), avg(x)
func StreamExprAgg(initExpr, everyExpr, finalExpr string) AggregateFunc  // a fold with mutable state
```

```go
aggs := map[string]ssql.AggregateFunc{
    "first_name":  ssql.First[string]("name"),
    "top_amount":  ssql.Max[float64]("amount"),
    "all_names":   ssql.Collect("name"),
    "all_amounts": ssql.CollectSeq[float64]("amount"),
}
```

These are the aggregates behind `group-by`'s flags. All skip records
where the field is missing; a group that cannot be ordered or mixes kinds
is an error, never a silent zero. `Collect` gives a slice (JSON output,
length checks); `CollectSeq[T]` gives an iterator and drops values not of
type T.

### Numeric accumulators

```go
type CompensatedSum struct{ Sum, C float64 }   // Neumaier compensated summation; what -sum, -avg and Welford use
func (s *CompensatedSum) Add(x float64)
func (s *CompensatedSum) Merge(o CompensatedSum)
func (s CompensatedSum) Value() float64

type Welford struct{ N int64 /* private mean, M2 */ }   // single-pass mean and variance
func (w *Welford) Add(x float64)
func (w *Welford) Merge(o Welford)          // Chan, Golub & LeVeque, for shard merges
func (w Welford) MeanValue() float64
func (w Welford) Variance() float64         // sample, n−1

func QuantileCont(sorted []float64, p float64) float64   // the continuous quantile every lane shares
func AggValueString(v any) string                        // StringAgg's text form: ints in full, floats shortest round-trip, times RFC 3339
```

### Rollup and cube

```go
func Rollup(config RollupConfig) Filter[Record, Record]
type RollupConfig struct {
    Fields       []string                 // group-by fields in order
    Aggregations map[string]AggregateFunc
    Mode         RollupMode
}
type RollupMode int
const (
    RollupHierarchical RollupMode = iota // (), (a), (a,b), (a,b,c)
    RollupCube                           // all 2^n combinations
)
```

One row per detail group, enriched with every parent level's aggregates
under a prefixed name: for grouping set `(dept, region)` and result `R`,
`dept_region_R`; for `(dept)`, `dept_R`; for the grand total, `R`.

```go
enriched := ssql.Rollup(ssql.RollupConfig{
    Fields:       []string{"dept", "region"},
    Aggregations: map[string]ssql.AggregateFunc{"count": ssql.Count(), "total": ssql.Sum("salary")},
    Mode:         ssql.RollupHierarchical,
})(records)
// each row: dept, region, dept_region_count, dept_region_total, dept_count, dept_total, count, total
```

### Describe

```go
func DescribeRecords(records iter.Seq[Record], cfg DescribeConfig) iter.Seq[Record]
func DescribeFilter(cfg DescribeConfig) Filter[Record, Record]
type DescribeConfig struct{ Fields []string }
```

One output record per field: `field`, `type` (the most general kind seen),
`count` (non-missing), `missing` (absent, null or empty string),
`distinct` (exact), and for numeric fields `min`, `max`, `mean`, `median`.
Numeric stats are absent, not zero, on other fields. Rows follow
`cfg.Fields` when given, otherwise field-name order. A barrier. Behind
`ssql describe`.

### Running statistics

```go
func RunningSum(fieldName string) Filter[Record, Record]
func RunningAverage(fieldName string, windowSize int) Filter[Record, Record]
func ExponentialMovingAverage(fieldName string, alpha float64) Filter[Record, Record]
func RunningMinMax(fieldName string) Filter[Record, Record]
func RunningCount(fieldName string) Filter[Record, Record]
```

Each adds a field to every record as it passes; the [window
functions](#window-and-time) are the general form.

---

## Reshaping

### Pivot and unpivot

```go
func Pivot(rowField, colField, valField, aggFunc string) Filter[Record, Record]   // cross-tab: colField's values become columns; aggFunc is count|sum|avg|min|max
func UnpivotRecords(records iter.Seq[Record], cfg UnpivotConfig) iter.Seq[Record]
func UnpivotFilter(cfg UnpivotConfig) Filter[Record, Record]
type UnpivotConfig struct{ IDs, Values []string; NameField, ValueField string }
```

Unpivot is the wide→long fold: one output per (record, value field),
copying `IDs`, with the field's name in `NameField` (default `name`) and
its value in `ValueField` (default `value`); empty `Values` means every
non-ID field. An absent or null value produces no row.

```go
long := ssql.UnpivotRecords(wide, ssql.UnpivotConfig{
    IDs: []string{"product"}, Values: []string{"jan", "feb"}, NameField: "month", ValueField: "revenue",
})
```

### Fill

```go
func FillRecords(records iter.Seq[Record], cfg FillConfig) iter.Seq[Record]
func FillFilter(cfg FillConfig) Filter[Record, Record]
type FillConfig struct{ Down []string; Defaults []FillDefault }
type FillDefault struct{ Field string; Value any }
```

Carries `Down` fields' last non-missing value forward over gaps, then
gives `Defaults` where a field is still missing. Streams with only the
carried state.

```go
filled := ssql.FillRecords(records, ssql.FillConfig{
    Down:     []string{"region"},
    Defaults: []ssql.FillDefault{{Field: "status", Value: "unknown"}},
})
```

### Extract

```go
func ExtractRecords(records iter.Seq[Record], cfg ExtractConfig) (iter.Seq[Record], error)
func ExtractFilter(cfg ExtractConfig) Filter[Record, Record]
func Explode(field string, keepEmpty bool) Filter[Record, Record]        // one row per element of a json list field (UNNEST); empty/missing → no row, or one null row with keepEmpty; a non-list value panics with an error
func FlattenField(field string, depth int, keep bool) Filter[Record, Record] // a json object's keys → fields field.key (depth levels); keys fixed by the first row, a new key later panics naming it
func CompileExtract(cfg ExtractConfig) (*regexp.Regexp, []string, error)
type ExtractConfig struct{ Field, Pattern string; Skip, Keep bool }
```

Applies a Go regexp to `Field`; every named group `(?P<name>…)` becomes a
string field. A non-matching or missing field is an error unless `Skip`
drops the record; the source field is removed unless `Keep`.

```go
out, err := ssql.ExtractRecords(lines, ssql.ExtractConfig{
    Field: "line", Pattern: `^(?P<ts>\S+) (?P<lvl>\w+) (?P<msg>.*)$`, Skip: true,
})
```

### Flattening sequences and nested records

```go
func DotFlatten(separator string, fields ...string) Filter[Record, Record]    // sequences zipped position by position; nested records become prefixed fields
func CrossFlatten(separator string, fields ...string) Filter[Record, Record]  // Cartesian product of the sequences
func Materialize(sourceField, targetField, separator string) Filter[Record, Record]   // a sequence joined into one string in targetField (a grouping key)
func MaterializeJSON(sourceField, targetField string) Filter[Record, Record]          // a sequence or nested record as JSON text in targetField
```

Field names are kept: `{tags: [a, b], scores: [10, 20]}` dot-flattens to
`{tags: a, scores: 10}`, `{tags: b, scores: 20}`; sequences of different
lengths stop at the shortest. A nested record `{user: {name: Alice}}`
flattens to `user<sep>name`.

### Hash

```go
func Hash(sourceField, targetField string) Filter[Record, Record]   // hex SHA-256 of a string field: a fixed-length grouping key
```

---

## Joins and Set Operations

### Join Operations

```go
type JoinPredicate interface {
    Match(left, right Record) bool
}
type KeyExtractor interface {          // optional on a JoinPredicate: enables the hash join
    ExtractKey(r Record) (string, bool)
}
func OnFields(fields ...string) JoinPredicate                          // equality on the same-named fields (hash join)
func OnFieldPair(leftField, rightField string) JoinPredicate           // left.a = right.b (hash join)
func OnCondition(condition func(left, right Record) bool) JoinPredicate // any predicate (nested loop)

func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record]
func LeftJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record]
func RightJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record]
func FullJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record]
```

```go
joined := ssql.InnerJoin(
    rightStream,
    ssql.OnFieldPair("user_id", "customer_id"),
)(leftStream)
```

The right side is read in full when the first left record arrives; the
left streams. Outer joins leave the unmatched side's fields absent.

### Lookup join

```go
func LookupJoin(rightSeq iter.Seq[Record], clauses []LookupClause) Filter[Record, Record]
type LookupClause struct {
    LeftField    string            // field of the left record to match on
    RightField   string            // field of the right record to match on
    FieldRenames map[string]string // right field → name it takes in the output
}
func Lookup(leftField, rightField string, renames ...string) LookupClause   // renames as old, new pairs
```

Several lookups from one right-side table in a single pass:

```go
clauses := []ssql.LookupClause{
    ssql.Lookup("origin_cat", "cat_id", "cat_name", "origin_name"),
    ssql.Lookup("dest_cat", "cat_id", "cat_name", "dest_name"),
}
enriched := ssql.LookupJoin(categories, clauses)(products)
```

The CLI form is `join FILE -on origin_cat cat_id -as cat_name origin_name - -on dest_cat cat_id -as cat_name dest_name`.

### AsofJoin

```go
type AsofConfig struct {
    LeftKeys, RightKeys []string // equality part, pairwise; may be empty (one series)
    LeftTime, RightTime string   // the ordered fields: numeric or time
    Forward       bool           // nearest at or after (default: at or before)
    Strict        bool           // never at the same time
    Tolerance     float64        // > 0: largest distance that matches (ns for times)
    KeepUnmatched bool           // left join: unmatched left rows, right fields absent
}
func AsofJoin(right iter.Seq[Record], cfg AsofConfig) Filter[Record, Record]
```

Each left row takes the right row that is current *as of* its time:
among the right rows with the same key, the nearest at or before the left
time (the quote in force when the trade happened). The right side is
indexed per key and sorted by time; the left streams in input order, so
the output is in left order. Ties on the right time take the last in
input order. A left row whose key or time is absent matches nothing.

```go
quoted := ssql.AsofJoin(quotes, ssql.AsofConfig{
    LeftKeys: []string{"sym"}, RightKeys: []string{"sym"},
    LeftTime: "ts", RightTime: "ts",
    Tolerance: float64(5 * time.Minute),
})(trades)
```

The CLI form is `join quotes.csv -using sym -asof ts -tolerance 5m`.

### Except and Intersect

```go
type SetKeyFunc func(r Record) (key string, ok bool)
var WholeRow SetKeyFunc                       // nil: the whole row, by RecordKey
func FieldsKey(fields ...string) SetKeyFunc   // the named fields; absent → no key
func Except(right iter.Seq[Record], leftKey, rightKey SetKeyFunc, all bool) Filter[Record, Record]
func Intersect(right iter.Seq[Record], leftKey, rightKey SetKeyFunc, all bool) Filter[Record, Record]
```

`Except` keeps the left rows whose key is absent from the right;
`Intersect` the rows whose key is present. With `WholeRow` on both sides
that is SQL `EXCEPT` / `INTERSECT`; with `FieldsKey` it is the anti-join /
semi-join (the left row comes out unchanged). `all=false` yields each
distinct left row once; `all=true` keeps duplicates (the multiset `EXCEPT
ALL` / `INTERSECT ALL` with whole-row keys). A left row with no key
matches nothing: except keeps it, intersect drops it. Numbers key as
numbers (an int 3 and a float 3 match); a number never keys like the text
that prints the same.

```go
noOrders := ssql.Except(orders, ssql.FieldsKey("customer_id"), ssql.FieldsKey("customer_id"), false)(customers)
changed := ssql.Except(yesterday, ssql.WholeRow, ssql.WholeRow, false)(today)
```

---

## Window and Time

### SQL window functions

Every input row comes out enriched with computed values; unlike
`GroupByFields`, nothing collapses.

```go
func Window(configs []WindowConfig) Filter[Record, Record]
func StreamWindow(configs []WindowConfig) (Filter[Record, Record], error)   // presorted input, O(frame) memory; refuses what it cannot stream
func MustStreamWindow(configs []WindowConfig) Filter[Record, Record]

type WindowConfig struct {
    PartitionBy []string      // PARTITION BY (empty = whole input)
    OrderBy     []OrderField  // ORDER BY
    Frame       WindowFrame
    Specs       []WindowSpec
}
type WindowSpec struct {
    Function   WindowFunc
    ResultName string
}
type WindowFrame struct {
    Preceding int   // rows before the current (-1 = UNBOUNDED PRECEDING)
    Following int   // rows after (-1 = UNBOUNDED FOLLOWING)
    Range          bool     // RANGE frame: Preceding/Following ignored, distances in the order field's units
    RangeTime      bool     // the order field is a time: distances in seconds
    RangePreceding float64  // -1 = unbounded
    RangeFollowing float64
}
```

**Window function constructors:**

| Constructor | SQL | Description |
|---|---|---|
| `WRowNumber()` | `ROW_NUMBER()` | sequential number within the partition |
| `WRank()` | `RANK()` | rank with gaps on ties (1,2,2,4) |
| `WDenseRank()` | `DENSE_RANK()` | rank without gaps (1,2,2,3) |
| `WNtile(n)` | `NTILE(n)` | bucket 1..n |
| `WPercentRank()` | `PERCENT_RANK()` | relative rank, 0..1 |
| `WCumeDist()` | `CUME_DIST()` | cumulative distribution, 0..1 |
| `WLag(field, n)` | `LAG(field, n)` | the value n rows before |
| `WLagDefault(field, n, def)` | `LAG(field, n, def)` | with a default where absent |
| `WLead(field, n)` | `LEAD(field, n)` | the value n rows after |
| `WLeadDefault(field, n, def)` | `LEAD(field, n, def)` | with a default |
| `WFirst(field)` | `FIRST_VALUE(field)` | first value in the frame |
| `WLast(field)` | `LAST_VALUE(field)` | last value in the frame |
| `WNthValue(field, n)` | `NTH_VALUE(field, n)` | absent while the frame has fewer than n rows |
| `WSum(field)` | `SUM(field)` | sum over the frame |
| `WAvg(field)` | `AVG(field)` | average over the frame |
| `WCount()` | `COUNT(*)` | rows in the frame |
| `WCountField(field)` | `COUNT(field)` | rows in the frame where field is present |
| `WMin(field)` | `MIN(field)` | minimum in the frame |
| `WMax(field)` | `MAX(field)` | maximum in the frame |
| `WAggregate(spec)` | any aggregate | a registry aggregate (`StdDev`, `Percentile`, …) over the frame; materialised path only |

```go
func WRowNumber() WindowFunc
func WRank() WindowFunc
func WDenseRank() WindowFunc
func WNtile(n int) WindowFunc
func WPercentRank() WindowFunc
func WCumeDist() WindowFunc
func WLag(field string, offset int) WindowFunc
func WLagDefault(field string, offset int, def any) WindowFunc
func WLead(field string, offset int) WindowFunc
func WLeadDefault(field string, offset int, def any) WindowFunc
func WFirst(field string) WindowFunc
func WLast(field string) WindowFunc
func WNthValue(field string, n int) WindowFunc
func WSum(field string) WindowFunc
func WAvg(field string) WindowFunc
func WCount() WindowFunc
func WCountField(field string) WindowFunc
func WMin(field string) WindowFunc
func WMax(field string) WindowFunc
func WAggregate(spec WAggSpec) WindowFunc
type WAggSpec struct {
    Name    string        // registry function name, e.g. "stddev"
    Field   string        // the field the aggregate reads
    Extra   string        // its extra argument (percentile's P, string-agg's separator, arg-max's BY)
    Kind    string        // result wire type: "int", "float", "string", "json", or "" = the field's own
    MinRows int           // frames with fewer rows yield an absent value (2 for sample stddev/variance)
    Agg     AggregateFunc
    Code    string        // Go source that rebuilds Agg (what `generate go` emits)
}
```

```go
// Rank employees by salary within each department
ranked := ssql.Window([]ssql.WindowConfig{{
    PartitionBy: []string{"dept"},
    OrderBy:     []ssql.OrderField{{Field: "salary", Desc: true}},
    Frame:       ssql.WindowFrame{Preceding: -1, Following: 0},
    Specs:       []ssql.WindowSpec{{Function: ssql.WRowNumber(), ResultName: "rank"}},
}})(employees)

// Running total and the previous row, per department in date order
enriched := ssql.Window([]ssql.WindowConfig{{
    PartitionBy: []string{"dept"},
    OrderBy:     []ssql.OrderField{{Field: "date"}},
    Frame:       ssql.WindowFrame{Preceding: -1, Following: 0},
    Specs: []ssql.WindowSpec{
        {Function: ssql.WSum("revenue"), ResultName: "running_total"},
        {Function: ssql.WLag("revenue", 1), ResultName: "prev_revenue"},
    },
}})(sales)

// 7-row moving average: ROWS BETWEEN 6 PRECEDING AND CURRENT ROW
smoothed := ssql.Window([]ssql.WindowConfig{{
    OrderBy: []ssql.OrderField{{Field: "date"}},
    Frame:   ssql.WindowFrame{Preceding: 6, Following: 0},
    Specs:   []ssql.WindowSpec{{Function: ssql.WAvg("price"), ResultName: "ma7"}},
}})(prices)
```

**`Window` vs `StreamWindow`.** `Window` accepts any input order and
holds a partition in memory. `StreamWindow` needs input already sorted by
the partition fields then the order fields, and then runs in O(frame size)
or O(1) memory with running accumulators, ring buffers and monotonic
deques; it supports the default frame (`Preceding: -1, Following: 0`) and
bounded frames with `Following: 0`, and returns an error for `NTILE`,
`PERCENT_RANK`, `Following > 0` or `WAggregate`. `MustStreamWindow` panics
instead of returning the error, for configurations known valid in
advance.

```go
filter, err := ssql.StreamWindow([]ssql.WindowConfig{{
    OrderBy: []ssql.OrderField{{Field: "date"}},
    Frame:   ssql.WindowFrame{Preceding: 9, Following: 0},   // 10-row window
    Specs: []ssql.WindowSpec{
        {Function: ssql.WMin("price"), ResultName: "low10"},
        {Function: ssql.WMax("price"), ResultName: "high10"},
    },
}})
if err != nil {
    log.Fatal(err)
}
result := filter(sortedPrices)
```

### Batch Window Operations

Windows that turn a stream of records into a stream of slices, for
batch processing and time buckets.

```go
func CountWindow[T any](size int) Filter[T, []T]                                   // fixed-size batches
func SlidingCountWindow[T any](windowSize, stepSize int) Filter[T, []T]
func TimeWindow[T any](duration time.Duration, timeField string) Filter[T, []T]     // by time interval
func SlidingTimeWindow[T any](windowDuration, slideDuration time.Duration, timeField string) Filter[T, []T]
```

```go
batches := ssql.CountWindow[int](3)(numbers)   // [1,2,3], [4,5,6], …
```

Only one window's records are held at a time, so these work on endless
streams; the [Getting Started Guide](codelab-intro.md#windows-batches-and-time-buckets)
shows them.

### Resampling and time buckets

```go
func ResampleRecords(records iter.Seq[Record], cfg ResampleConfig) (iter.Seq[Record], error)
func ResampleFilter(cfg ResampleConfig) Filter[Record, Record]
func SnapToBucket(ns int64, every time.Duration) int64     // epoch-aligned floor, correct before 1970
func BucketValue(v any, every time.Duration) (any, error)  // the bucket() expression function; keeps the input's family
```

`ResampleRecords` snaps to an epoch-aligned grid and emits one record per
grid point with each value field filled per `cfg.Fill` (previous, next,
linear). `BucketValue` shares the snap, so `update -set-bucket` +
`group-by` and `resample` land on the same grid.

---

## Composition

```go
func Pipe[T, U, V any](f1 Filter[T, U], f2 Filter[U, V]) Filter[T, V]
func Pipe3[T, U, V, W any](f1 Filter[T, U], f2 Filter[U, V], f3 Filter[V, W]) Filter[T, W]
func Chain[T any](filters ...Filter[T, T]) Filter[T, T]                    // any number of same-type filters
func PipeWithErrors[T, U, V any](f1 FilterWithErrors[T, U], f2 FilterWithErrors[U, V]) FilterWithErrors[T, V]
func ChainWithErrors[T any](filters ...FilterWithErrors[T, T]) FilterWithErrors[T, T]

func Tee[T any](input iter.Seq[T], n int) []iter.Seq[T]       // n independent copies (buffers)
func LazyTee[T any](input iter.Seq[T], n int) []iter.Seq[T]   // copies that share one pass
func TeeFile(filename string, fieldOrder ...string) Filter[Record, Record]   // Unix tee: write every record to a schema-headed JSONL file and pass it on
```

```go
double := ssql.Select(func(x int) int { return x * 2 })
addOne := ssql.Select(func(x int) int { return x + 1 })
composed := ssql.Pipe(double, addOne)

pipeline := ssql.Chain(
    ssql.Where(func(x int) bool { return x > 0 }),
    ssql.Where(func(x int) bool { return x < 100 }),
    ssql.Sort[int](),
)
result := pipeline(numbers)

teed := ssql.TeeFile("checkpoint.jsonl")(records)   // replay later with ssql from checkpoint.jsonl
```

`Pipe` changes the element type between stages; `Chain` keeps it.
`TeeFile` writes the pipeline wire format (a `_schema` header, then one
JSON object per line); `fieldOrder` sets the header's order.

---

## I/O Operations

Readers of files return `(iter.Seq[Record], error)`: the error is the
open failure, and the sequence is lazy. `*FromReader` variants take an
`io.Reader` and return the sequence alone. `*Safe` variants return
`iter.Seq2[Record, error]` and yield a row's problem instead of failing;
the plain readers **fail fast**: a cell that does not fit its column's
type panics with a [`*CellError`](#error-handling), never a coerced zero.

### CSV Operations

```go
func ReadCSV(filename string, config ...CSVConfig) (iter.Seq[Record], error)
func ReadCSVFromReader(reader io.Reader, config ...CSVConfig) iter.Seq[Record]
func ReadCSVSafe(filename string, config ...CSVConfig) iter.Seq2[Record, error]
func ReadCSVSafeFromReader(reader io.Reader, config ...CSVConfig) iter.Seq2[Record, error]
func WriteCSV(stream iter.Seq[Record], filename string, config ...CSVConfig) error
func WriteCSVToWriter(stream iter.Seq[Record], writer io.Writer, config ...CSVConfig) error
func DefaultCSVConfig() CSVConfig

type CSVConfig struct {
    HasHeaders    bool
    Delimiter     rune
    Comment       rune
    Fields        []string             // for writing: the columns and their order (nil = every field, alphabetically)
    TypeOverrides map[string]FieldType // per column, instead of inference
    DefaultType   FieldType            // for every column not overridden (FieldTypeAuto = infer)
    InferRows     int                  // leading data rows sampled to infer types (0 = DefaultInferRows, 1000)
}
```

**Column typing.** Each column's type is inferred from a sample of the
leading data rows (`InferRows`): the narrowest of int → float → bool →
string that every non-empty sampled value fits (`true`/`false` only for
bool; `1`/`0` are ints; zero-padded values such as `007` stay text).
Empty cells are absent, never zero. `TypeOverrides` and `DefaultType` win
over inference. A later cell that does not fit its column is a
`*CellError` (row, column, value, and how the type was decided): the plain
readers panic with it, `ReadCSVSafe` yields it. A column overridden to
`FieldTypeJSON` reads each cell's JSON array or object as a nested value
(a `JSONString`); any other text in that column is a `CellError`.

### Schema sidecars

```go
type TableField struct { Name string; Type FieldType; Shape string } // Shape: "object" / "array" for a json field, else ""
type TableSchema struct { Fields []TableField; Source, Kind string }  // Kind: "csvw" or "frictionless"

func FindTableSchema(csvPath string) (*TableSchema, error)                       // the discovery rule; (nil, nil) when none
func ReadTableSchema(sidecarPath, csvPath string) (*TableSchema, error)         // a named sidecar
func ParseTableSchema(data []byte, csvBase, dir string) (*TableSchema, error)  // any of the three shapes
func (s *TableSchema) TypeOverrides() map[string]string                         // column → ssql type name, for CSVConfig
func WriteDatapackage(csvPath string, fields []TableField) error                // datapackage.json beside csvPath (merged)
func WriteCSVSidecar(records iter.Seq[Record], filename string, known map[string]FieldType, config ...CSVConfig) error
func TableFieldsFromRecords(records iter.Seq[Record], known map[string]FieldType) (iter.Seq[Record], func() []TableField)
func TableSchemaJSON(fields []TableField) ([]byte, error)                       // a Frictionless Table Schema document
func TableTypeName(f TableField) string                                         // integer, number, boolean, datetime, string, object, array, any
```

A delimited file's types can be stated once, beside it, in a standard
sidecar (DFC146): W3C CSV on the Web metadata (`X.csv-metadata.json`, or
`csv-metadata.json` in the directory) or a Frictionless Data Package
(`datapackage.json` with a resource whose `path` names the file).
`FindTableSchema` applies that discovery rule in that order;
`TypeOverrides` turns the result into `CSVConfig.TypeOverrides` (via
`ParseFieldType`), so the reader takes the sidecar's types instead of
sampling. Types map: integer → int, number/decimal/double → float,
boolean → bool, date/datetime/time → time (ISO forms; any other `format`
is refused), object/array/json → json, string → string, any → infer. A
`dialect` without a header row, or with a delimiter the reader is not
using, is refused with the remedy. `WriteCSVSidecar` writes the CSV and
a package describing it, typing columns from the values written (or
from `known`); `WriteDatapackage` keeps an existing package's other
resources.

```go
schema, err := ssql.FindTableSchema("sales.csv")
cfg := ssql.DefaultCSVConfig()
if schema != nil {
    cfg.TypeOverrides = map[string]ssql.FieldType{}
    for col, name := range schema.TypeOverrides() {
        cfg.TypeOverrides[col], _ = ssql.ParseFieldType(name)
    }
}
rows, err := ssql.ReadCSV("sales.csv", cfg)

// Lossless round trip: the package beside out.csv types it for the next reader.
err = ssql.WriteCSVSidecar(rows, "out/out.csv", nil)
```

```go
data, err := ssql.ReadCSV("data.csv")
if err != nil {
    log.Fatalf("open: %v", err)
}
for record := range data {
    age := ssql.GetOr(record, "age", int64(0))    // CSV numbers are int64 or float64, not strings
    score := ssql.GetOr(record, "score", 0.0)
    _, _ = age, score
}

for record, err := range ssql.ReadCSVSafe("data.csv") {
    if err != nil {
        log.Printf("skipping: %v", err)
        continue
    }
    _ = record
}

var buf bytes.Buffer
if err := ssql.WriteCSVToWriter(records, &buf, ssql.CSVConfig{Fields: []string{"name", "age"}}); err != nil {
    log.Fatal(err)
}
```

### TSV and delimited text

```go
func ReadTSV(filename string) (iter.Seq[Record], error)
func ReadTSVWithConfig(filename string, cfg CSVConfig) (iter.Seq[Record], error)
func ReadTSVFromReader(r io.Reader) iter.Seq[Record]
func ReadTSVFromReaderWithSeparator(r io.Reader, sep rune) iter.Seq[Record]
func ReadTSVFromReaderWithConfig(r io.Reader, cfg CSVConfig) iter.Seq[Record]
func DefaultTSVConfig() CSVConfig
func WriteTSV(records iter.Seq[Record], filename string) error
func WriteTSVToWriter(records iter.Seq[Record], w io.Writer) error
func WriteTSVWithSeparator(records iter.Seq[Record], filename string, sep rune) error
func WriteTSVToWriterWithSeparator(records iter.Seq[Record], w io.Writer, sep rune) error
func DetectTSVSeparator(header string) rune          // the first non-identifier byte; tab by default
func ExtractFieldsFromTSV(filename string) ([]string, error)
```

Delimited text without quoting rules: a field is everything between
separators. The separator is auto-detected from the header line when
`Delimiter` is 0. Column typing is the CSV reader's.

### JSON and JSON Lines

ssql's JSON is JSON Lines: one object per line. It is also the wire
format between CLI stages, where the first line is a schema header:

```json
{"_schema":{"fields":["name","age","department"],"types":{"name":"string","age":"int","department":"string"}}}
```

The header carries field order and wire types (`string`, `int`, `float`,
`bool`, `time`, `json`) so that sinks print columns in the source's order
and a column's type survives every pipe hop. The readers below honour it
and the writers that say so emit it.

```go
func ReadJSON(filename string) (iter.Seq[Record], error)            // JSON Lines; nested values are JSONString (since 2026-10-08)
func ReadJSONFromReader(reader io.Reader) iter.Seq[Record]
func ReadJSONSafe(filename string) iter.Seq2[Record, error]
func ReadJSONSafeFromReader(reader io.Reader) iter.Seq2[Record, error]
func ReadJSONAuto(filename string) (iter.Seq[Record], error)        // a JSON array or lines, detected
func ReadJSONLFromReader(r io.Reader) iter.Seq[Record]              // honours a leading {"_schema":…} header
func ReadJSONLFromReaderSkipInvalid(r io.Reader, skipped *int64) iter.Seq[Record]   // `from jsonl -skip-invalid`
func ReadJSONFast(filename string) (iter.Seq[Record], error)        // no reflection, 3-5× ReadJSON
func ReadJSONFastFromReader(reader io.Reader) iter.Seq[Record]
func ReadJSONFastSafe(filename string) iter.Seq2[Record, error]
func ReadJSONFastSafeFromReader(reader io.Reader) iter.Seq2[Record, error]

func WriteJSON(stream iter.Seq[Record], filename string) error      // one object per line
func WriteJSONToWriter(stream iter.Seq[Record], writer io.Writer) error
func WriteJSONFast(sb iter.Seq[Record], filename string) error
func WriteJSONFastToWriter(sb iter.Seq[Record], writer io.Writer) error
func WriteJSONLWithInferredSchemaToWriter(sb iter.Seq[Record], writer io.Writer) error  // header inferred from the first record
func WriteJSONPretty(sb iter.Seq[Record], filename string) error    // a pretty-printed JSON array (`to json`)

func ParseJSONLine(line []byte) (MutableRecord, error)
func ParseJSONLineWithNulls(line []byte) (MutableRecord, error)              // a JSON null keeps its key as a nil slot
func ParseJSONLineWithSchema(line []byte, schema *Schema) (Record, error)     // shares one Schema across lines
func ParseJSONLineWithSchemaTypes(line []byte, schema *Schema, types []FieldType) (Record, error)
func ParseSchemaHeaderFields(line []byte) ([]string, bool)
func CoerceFieldTypes(records iter.Seq[Record], types map[string]FieldType) iter.Seq[Record]   // `-type FIELD TYPE` for inputs with no column typing
```

```go
data, err := ssql.ReadJSON("data.jsonl")
if err != nil {
    log.Fatalf("open: %v", err)
}
for record, err := range ssql.ReadJSONSafe("data.jsonl") {
    if err != nil {
        log.Printf("bad line: %v", err)
        continue
    }
    _ = record
}
```

The fast readers cache and reuse schemas while consecutive records share
a field set. `CoerceFieldTypes` converts the named fields of every record
strictly (a fraction into int, a number into bool or an unparsable string
is a `*CellError`); absent fields stay absent.

### Lines

```go
func ReadLines(filename string) (iter.Seq[Record], error)   // one record per text line: line_number (from 1) and line
func ReadLinesFromReader(r io.Reader) iter.Seq[Record]
func ReadLinesSafe(filename string) iter.Seq2[Record, error]
func WriteLines(stream iter.Seq[Record], filename string) error   // one line per record, from the line field
```

```go
lines, err := ssql.ReadLines("app.log")
if err != nil {
    log.Fatal(err)
}
for r := range lines {
    fmt.Println(ssql.GetOr(r, "line_number", int64(0)), ssql.GetOr(r, "line", ""))
}
```

Pair with [`ExtractRecords`](#extract) to parse the lines.

### Command output

```go
func ExecCommand(command string, args []string, config ...CommandConfig) (iter.Seq[Record], error)   // run it, parse column-aligned output
func ExecCommandSafe(command string, args []string, config ...CommandConfig) iter.Seq2[Record, error]
func ReadCommandOutput(filename string, config ...CommandConfig) (iter.Seq[Record], error)          // the same parse, from a captured file
func ReadCommandOutputSafe(filename string, config ...CommandConfig) iter.Seq2[Record, error]
func DefaultCommandConfig() CommandConfig

type CommandConfig struct {
    HasHeaders    bool   // the first line names the columns
    TrimSpaces    bool
    SkipEmpty     bool
    HeaderPattern string // optional regexp that identifies the header line
}
```

```go
processes, err := ssql.ExecCommand("ps", []string{"-efl"})
if err != nil {
    log.Fatalf("start: %v", err)
}
for p := range processes {
    fmt.Println(ssql.GetOr(p, "CMD", ""))
}
```

### Parquet

```go
func ReadParquet(filename string) (iter.Seq[Record], error)
func ReadParquetColumns(filename string, columns []string) (iter.Seq[Record], error)   // read only these columns: the big lever on wide files
func ReadParquetFromReader(r parquet.ReaderAtSeeker) (iter.Seq[Record], error)
func WriteParquet(records iter.Seq[Record], filename string, opts ...ParquetWriteOption) error
func WriteParquetToWriter(records iter.Seq[Record], w io.Writer, opts ...ParquetWriteOption) error
func WithCompression(name string) ParquetWriteOption   // snappy (default), gzip, zstd, none
func WithRowGroupSize(n int) ParquetWriteOption        // default 1_000_000; one row group is one parallel shard
func ParquetRowCount(filename string) (int64, error)                            // from the footer, no scan
func ParquetSchemaFields(filename string) ([]string, map[string]string, error)  // names and wire types from the footer
```

Parquet is a random-access format: a file, not stdin.

### Arrow

```go
func ReadArrow(filename string) (iter.Seq[Record], error)    // .arrow / .feather
func ReadArrowFromReader(r io.Reader) iter.Seq[Record]
func WriteArrow(records iter.Seq[Record], filename string) error   // ZSTD-compressed
func WriteArrowToWriter(records iter.Seq[Record], w io.Writer) error
```

Columnar, zero-copy, and the fastest way to move large record sets
between processes.

### XLSX

```go
func ReadXLSX(filename string, config ...XLSXConfig) (iter.Seq[Record], error)   // row 1 is the header; types inferred from cells
func WriteXLSX(records iter.Seq[Record], filename string, config ...XLSXConfig) error
func ReadXLSXSheetNames(filename string) ([]string, error)
func DefaultXLSXConfig() XLSXConfig
type XLSXConfig struct {
    SheetName string // to read (default: the first) or write (default "Sheet1")
}
```

```go
records, err := ssql.ReadXLSX("workbook.xlsx", ssql.XLSXConfig{SheetName: "Sales"})
```

XLSX loads the whole file; for streaming, use CSV.

### WAV audio

```go
func ReadWAV(filename string) (iter.Seq[Record], *WAVMetadata, error)        // sample, amplitude in [-1, 1]; stereo mixed to mono
func ReadWAVChannel(filename string, channel int) (iter.Seq[Record], *WAVMetadata, error)
func ReadWAVFromReader(r io.Reader) (iter.Seq[Record], *WAVMetadata, error)
func WriteWAV(records iter.Seq[Record], filename string, sampleRate int) error   // 16-bit PCM mono from an amplitude field
func WriteWAVToWriter(records iter.Seq[Record], w io.Writer, sampleRate int) error
func ExtractSignalFromWAV(filename string) (Signal, *WAVMetadata, error)         // straight to a Signal, no records
func ExtractSignalFromWAVChannel(filename string, channel int) (Signal, *WAVMetadata, error)
func ExtractSignalFromArrow(filename string, field string) (Signal, error)
func ExtractSignalFromArrowReader(r io.Reader, field string) (Signal, error)
```

### HTTP sources

```go
func IsHTTPURL(path string) bool
func OpenHTTPStream(url string) (io.ReadCloser, error)   // a plain GET: the `from https://` read path
func OpenHTTPFile(url string) (*HTTPFile, error)          // random access over Range requests (parquet footers, sampling)
func HTTPURLExt(rawurl string) string                     // the path's extension, ignoring a presigned query
type HTTPFile struct { /* private */ }                    // io.ReaderAt + io.Seeker over Range requests
func (h *HTTPFile) Read(p []byte) (int, error)
func (h *HTTPFile) ReadAt(p []byte, off int64) (int, error)
func (h *HTTPFile) Seek(offset int64, whence int) (int64, error)
func (h *HTTPFile) Size() int64
func (h *HTTPFile) Requests() int64   // Range requests made so far
```

A server that does not honour Range is refused rather than downloading
the whole file per read.

### Sampling, tailing and counting

```go
func SampleCSVFile(filename string, n int, seed int64, config ...CSVConfig) (iter.Seq[Record], error)
func SampleTSVFile(filename string, n int, seed int64, config ...CSVConfig) (iter.Seq[Record], error)
func SampleJSONLFile(filename string, n int, seed int64) (iter.Seq[Record], error)
func TailCSVFile(filename string, n int, config ...CSVConfig) (iter.Seq[Record], error)
func TailTSVFile(filename string, n int, config ...CSVConfig) (iter.Seq[Record], error)
func TailJSONLFile(filename string, n int) (iter.Seq[Record], error)
func CountFileLines(path string) (int64, error)      // a bytes.Count scan
```

Sampling seeks to n byte offsets instead of reading the file, and emits
rows in file order, approximately uniform (probability proportional to
line length). Tailing seeks to the end and reads the last n lines, parsed
under the header's schema with types inferred from those lines. Both
assume newline-terminated records; URLs fall back to a full streaming
read. Behind `from csv -sample N`, `from … -last N` and `from … -records`.

### Tables and Markdown

```go
func DisplayTable(records iter.Seq[Record], maxWidth int)
func DisplayTableTo(w io.Writer, records iter.Seq[Record], maxWidth int)
func DisplayTableWithFields(records iter.Seq[Record], maxWidth int, fieldOrder []string, onlySpecified bool)
func DisplayTableWithFieldsTo(w io.Writer, records iter.Seq[Record], maxWidth int, fieldOrder []string, onlySpecified bool)
func DisplayTableStreaming(records iter.Seq[Record], maxWidth int, sampleSize int, fieldOrder []string, onlySpecified bool)
func DisplayTableStreamingTo(w io.Writer, records iter.Seq[Record], maxWidth int, sampleSize int, fieldOrder []string, onlySpecified bool)
func WriteMarkdownTo(w io.Writer, records iter.Seq[Record], fieldOrder []string, onlySpecified bool) error   // a GitHub-flavored table; numeric columns right-aligned
```

The streaming forms size columns from the first `sampleSize` records and
then stream (O(sampleSize) memory); `to table` uses them. `to markdown`
uses `WriteMarkdownTo`.

```go
var buf bytes.Buffer
if err := ssql.WriteMarkdownTo(&buf, records, []string{"name", "count"}, false); err != nil {
    log.Fatal(err)
}
fmt.Print(buf.String())
```

---

## Signal Processing

The [Signal Processing tutorial](cli-signal-processing.md) is the
hands-on version; the GPU build accelerates FFTs of 16 K samples or more
and convolution kernels of 16 points or more, automatically.

```go
type Signal []float64
type Spectrum struct {
    Magnitude []float64 // magnitude per frequency bin
    Phase     []float64 // radians; nil unless FFTWithPhase
    N         int       // original signal length
}
func (s *Spectrum) FrequencyBin(index int, sampleRate float64) float64
func (s *Spectrum) Len() int
func GPUAvailable() bool   // true only in the CUDA build with a GPU present
```

### FFT

```go
func FFT(signal Signal) (*Spectrum, error)              // magnitude only
func FFTWithPhase(signal Signal) (*Spectrum, error)     // magnitude and phase (needed to invert)
func FFTMagnitude(signal Signal) ([]float64, error)
func IFFT(magnitude, phase []float64) (Signal, error)
func IFFTToLength(magnitude, phase []float64, length int) (Signal, error)
```

```go
signal := ssql.Signal{1, 2, 3, 4, 5, 6, 7, 8}
spectrum, err := ssql.FFT(signal)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("DC magnitude: %f\n", spectrum.Magnitude[0])

withPhase, _ := ssql.FFTWithPhase(signal)
reconstructed, _ := ssql.IFFT(withPhase.Magnitude, withPhase.Phase)
```

### Convolution and correlation

```go
func Convolve(signal, kernel Signal) (Signal, error)       // len(signal)+len(kernel)-1 outputs
func ConvolveSame(signal, kernel Signal) (Signal, error)   // same length as the signal
func AutoConvolve(signal Signal) (Signal, error)
func AutoConvolveSame(signal Signal) (Signal, error)
func Correlate(a, b Signal) (Signal, error)                // cross-correlation by lag; peaks mark matches
func CorrelateSame(a, b Signal) (Signal, error)
func AutoCorrelate(signal Signal) (Signal, error)          // periodicity
func AutoCorrelateMax(signal Signal, maxLag int) (Signal, error)

func MovingAverageKernel(size int) Signal
func GaussianKernel(size int, sigma float64) Signal
func DiffKernel() Signal      // first derivative: [-1, 1]
func LaplacianKernel() Signal // second derivative: [1, -2, 1]
func SobelKernel() Signal     // edge detection: [-1, 0, 1]
```

```go
smoothed, _ := ssql.ConvolveSame(signal, ssql.GaussianKernel(11, 2.0))
```

### Windows and spectrograms

```go
func HannWindow(n int) Signal
func HammingWindow(n int) Signal
func BlackmanWindow(n int) Signal
func ApplyWindow(signal, window Signal) Signal                       // element-wise product
func Spectrogram(signal Signal, opts SpectrogramOptions) ([]SpectrogramBin, error)   // STFT: time × frequency × magnitude
func SpectrogramToRecords(bins []SpectrogramBin) iter.Seq[Record]    // time_index, time, frequency, magnitude
func SpectrogramFilter(field string, opts SpectrogramOptions) Filter[Record, Record]
```

### Records and signals

```go
func ExtractSignal(records iter.Seq[Record], field string) Signal
func ExtractSignalFromSlice(records []Record, field string) Signal
func WithSignal(records iter.Seq[Record], field string, signal Signal) iter.Seq[Record]   // add the values as a field
func SpectrumToRecords(spectrum *Spectrum, sampleRate float64) iter.Seq[Record]           // index, frequency, magnitude, phase

func FFTFilter(field string, sampleRate float64, includePhase bool) Filter[Record, Record]
func IFFTFilter(magnitudeField, phaseField, outputField string) Filter[Record, Record]
func ConvolveFilter(field, outputField string, kernel Signal, same bool) Filter[Record, Record]
func AutoConvolveFilter(field, outputField string, same bool) Filter[Record, Record]
func CorrelateFilter(fieldA, fieldB, outputField string, same bool) Filter[Record, Record]
func AutoCorrelateFilter(field, outputField string, same bool) Filter[Record, Record]
func AutoCorrelateMaxFilter(field, outputField string, maxLag int) Filter[Record, Record]
```

The `*Filter` forms are the record-stream shape of each operation, for
use in a `Chain`; they are what `fft`, `convolve` and `correlate` run.

---

## Chart & Visualization

Self-contained HTML files: Chart.js for the standard charts, Plotly for
heatmaps and animations, AG-Grid for the explorer. The [Getting Started
Guide](codelab-intro.md#interactive-charts-made-easy) walks through them.

```go
func QuickChart(data iter.Seq[Record], xField, yField, filename string) error
func InteractiveChart(data iter.Seq[Record], filename string, config ...ChartConfig) error
func TimeSeriesChart(data iter.Seq[Record], timeField string, valueFields []string, filename string, config ...ChartConfig) error
func EnhancedChart(data iter.Seq[Record], config ChartConfig, filename string) error   // multi-series, heatmap, log axes, colour by field
func HeatmapChart(sb iter.Seq[Record], config HeatmapConfig, filename string) error    // spectrogram-shaped data: colour range, log frequency axis, cursor readout
func AnimateChart(sb iter.Seq[Record], config AnimateConfig, filename string) error    // a heatmap or histogram per frame, with player controls
func DefaultChartConfig() ChartConfig
func DefaultHeatmapConfig() HeatmapConfig
func DefaultAnimateConfig() AnimateConfig
```

```go
type ChartConfig struct {
    Title              string
    Width, Height      int
    ChartType          string            // line, bar, scatter, pie, doughnut, radar, polarArea, heatmap
    TimeFormat         string            // for a time X axis
    XAxisType          string            // linear, logarithmic, time, category
    YAxisType          string            // linear, logarithmic
    ShowLegend         bool
    ShowTooltips       bool
    EnableZoom         bool
    EnablePan          bool
    EnableAnimations   bool
    ShowDataLabels     bool
    EnableInteractive  bool              // field selection UI
    EnableCalculations bool              // running averages, etc.
    ColorScheme        string            // default, vibrant, pastel, monochrome
    Theme              string            // light, dark
    ExportFormats      []string          // png, svg, pdf, csv
    CustomCSS          string
    Fields             map[string]string // field → data type hints
    XField             string            // explicit X-axis field
    YFields            []string          // several Y fields: a multi-series chart
    ZField             string            // heatmap colour value
    ColorField         string            // scatter: colour points by this field
    ColorScale         string            // heatmap: viridis, plasma, inferno, magma
}
type HeatmapConfig struct {
    Title              string
    XField, YField, ZField string
    ColorScale         string   // viridis, plasma, inferno, magma, cividis, turbo
    ZMin, ZMax         float64  // 0 = auto
    LogFreq            bool     // logarithmic Y axis
    Theme              string
    Width, Height      int
}
type AnimateConfig struct {
    Title              string
    FrameField         string   // partitions the records into frames
    XField, YField, ZField string
    ChartType          string   // heatmap or histogram
    FPS                int
    Loop               bool
    ColorScale         string
    Theme              string
    Width, Height      int
}
```

```go
config := ssql.DefaultChartConfig()
config.Title = "Sales Analysis"
config.ChartType = "bar"
if err := ssql.InteractiveChart(salesData, "sales_chart.html", config); err != nil {
    log.Fatal(err)
}

multi := ssql.DefaultChartConfig()
multi.XField = "month"
multi.YFields = []string{"revenue", "expenses", "profit"}
if err := ssql.EnhancedChart(salesData, multi, "multi_series.html"); err != nil {
    log.Fatal(err)
}

heat := ssql.DefaultChartConfig()
heat.ChartType = "heatmap"
heat.XField = "time"
heat.YFields = []string{"frequency"}
heat.ZField = "magnitude"
heat.ColorScale = "viridis"
if err := ssql.EnhancedChart(spectrogramData, heat, "spectrogram.html"); err != nil {
    log.Fatal(err)
}
```

### Data Explorer

```go
func DataExplore(records iter.Seq[Record], config ExploreConfig, filename string) error
func DefaultExploreConfig() ExploreConfig   // light theme, 50 rows per page, 1400×800
type ExploreConfig struct {
    Title         string
    Theme         string // light or dark
    InitialXField string
    InitialYField string
    PageSize      int    // rows per page in the table (default 50)
    Width, Height int
    WasmEnabled   bool   // load the ssql engine for client-side transforms
    WasmExecJS    string // set by the CLI: the inlined runtime pieces
    FsPolyfillJS  string
    SsqlUIJS      string
    WasmBinary    string // base64 of the gzipped engine
    AllowEmpty    bool   // permit zero records (a served, empty workspace)
    Version       string // shown in the ready text
}
```

A self-contained data exploration page: sortable, filterable table; chart
type switcher; field selectors; group-by aggregation; CSV and PNG export.
With `WasmEnabled` the page runs ssql itself in the browser, which is
what `to explore -wasm` produces.

```go
data, err := ssql.ReadCSV("sales.csv")
if err != nil {
    log.Fatal(err)
}
cfg := ssql.DefaultExploreConfig()
cfg.Title = "Sales Analysis"
cfg.Theme = "dark"
if err := ssql.DataExplore(data, cfg, "sales_explorer.html"); err != nil {
    log.Fatal(err)
}
```

---

## Error Handling

Two conventions, used consistently:

- **Sources and sinks return an error** for what can fail before or
  after the stream: opening a file, starting a command, writing.
- **Per-element problems** go one of two ways. The plain form of an
  operation or reader stops loudly (a panic carrying a typed error, which
  a generated program turns into `Error: …` and exit status 1). The
  `*Safe` form yields `(value, error)` pairs so the consumer decides per
  element.

```go
data, err := ssql.ReadCSV("data.csv")        // open failure
if err != nil {
    log.Fatalf("open: %v", err)
}
if err := ssql.WriteJSON(records, "out.jsonl"); err != nil {   // write failure
    log.Fatal(err)
}

for r, err := range ssql.ReadCSVSafe("data.csv") {   // a bad cell does not stop the loop
    var cell *ssql.CellError
    if errors.As(err, &cell) {
        log.Printf("row %d: %q is not %s", cell.Row, cell.Value, cell.Type)
        continue
    }
    if err != nil {
        log.Fatal(err)
    }
    _ = r
}

safe := ssql.SelectSafe(func(x int) (int, error) {   // a transformation that can fail
    if x < 0 {
        return 0, fmt.Errorf("negative: %d", x)
    }
    return x * 2, nil
})(ssql.Safe(numbers))
```

### Recovering a pipeline failure in-process

```go
func Recover(err *error)                          // defer ssql.Recover(&err) in the function whose loop drives the pipeline
func Run(fn func() error) (err error)             // fn's error, or the pipeline panic as an error
func Safely[T, U any](f Filter[T, U]) FilterWithErrors[T, U]   // a stage failure becomes the stream's last element, (zero, err)
```

The contract: a plain form fails fast with a panic whose value is an
`error` (typed for data errors, so `errors.As` works); it surfaces in
the goroutine that pulls the pipeline, and the helpers that pull in a
goroutine of their own (`LazyTee`, `Timeout`, `ToChannelErr`,
`ToChannelWithErrors`) re-raise or report it there, never from the
background. So a service needs one of two things:

```go
// One recover around the loop: the CLI and every generated program do this.
func handle(path string) (err error) {
    defer ssql.Recover(&err)
    src, err := ssql.ReadCSV(path)
    if err != nil {
        return err
    }
    for r := range pipeline(src) { … }
    return nil
}
// or, as a value: err := ssql.Run(func() error { … })

// Or the failure as a stream element, composable with ChainWithErrors:
for r, err := range ssql.Safely(pipeline)(ssql.ReadCSVSafe(path)) {
    var cell *ssql.CellError
    if errors.As(err, &cell) { … }
}
```

Neither changes what is an error: an unparsable cell stops the run in
every ssql lane, and a program that turned it into a zero would disagree
with the CLI on the same file.

### Error types

```go
type CellError struct {                 // a delimited-text cell that does not parse as its column's type
    Row     int64     // 1-based data row (the header is not counted)
    Column  string    // column name (or col_N without headers)
    Value   string    // the offending cell, trimmed
    Type    FieldType // the type the column was fixed to
    Sampled int       // rows the type was inferred from; 0 = an explicit override
}
func (e *CellError) Error() string
type RowError struct{ Row int64; Err error }   // a row that could not be read as a row: a bare quote, a wrong field count
func (e *RowError) Error() string
type LineError struct {                 // a JSON Lines line that cannot be read
    Line int64  // 1-based, counting a _schema header line
    Text string // the start of the offending line
    Err  error
}
func NewLineError(line int64, text []byte, err error) *LineError
func (e *LineError) Error() string
```

`CastError` and `CompareError` are under [Record Helpers](#record-helpers).
All unwrap to their cause, so `errors.As` works through wrapping.

**Practice.** Check the error from every source and sink. Use the plain
forms for trusted data and prototypes, where the first bad row should
stop the run. Use the `*Safe` forms where one bad row must not stop the
run: user uploads, logs, external feeds. Never coerce: an unparsable cell
is an error in every ssql lane, and a program that turned it into a zero
would disagree with the CLI on the same file.

---

## Appendix: CLI and Codegen Support

Exported because the CLI, `generate go` programs and the SQL translator
call them, so that every lane runs the same code. They are stable, but
you are unlikely to want them in an application.

### Catalogs and remote execution

```go
func ReadCatalog(filename string) ([]CatalogEntry, error)        // host, path, optional format and bin; the rest is metadata
func WriteCatalog(w io.Writer, entries []CatalogEntry) error
func PruneCatalog(entries []CatalogEntry, filters []CatalogFilter) []CatalogEntry   // range (from/to) and exact-value pruning
func ExpandCatalogGlobs(entries []CatalogEntry) []CatalogEntry   // local globs and remote `echo` expansion
func ProcessCatalogShards(entries []CatalogEntry, remoteBin string, shardField string, pipelineArgs [][]string) iter.Seq[Record]
func ProcessCatalogShardsRemoteGo(entries []CatalogEntry, requireVersion string, pushdownGroups [][]string, mode string, shardField string, opts CatalogShardOpts) iter.Seq[Record]
func BuildRemoteCommand(remoteBin, path, format string, pipelineGroups [][]string) string
func RemoteBinPrologue(bin string) string     // resolves the remote binary from a fixed list of absolute paths
func RemoteScriptCommand(bin, remotePath, mode string) string
func SelfBin(fallback string) string          // the binary to run for shards that resolve to this machine
func IsLocalHost(host string) bool
func ShellQuote(s string) string
func SplitOnPlus(args []string) [][]string    // the `--` push-down's `+` separator
```

Every remote command goes through `RemoteBinPrologue`, never a hard-wired
path, and every dynamic value through `ShellQuote`.

### Window function introspection

```go
func DescribeWindowFunc(fn WindowFunc) WindowFuncDesc
func WindowFuncCode(fn WindowFunc) string          // the Go constructor call that rebuilds fn (what `generate go` emits)
func WindowFuncField(fn WindowFunc) (string, bool) // the source field, or "" for ranking/count
func WindowFuncResultKind(fn WindowFunc) string    // "int", "float", or "" (the source field's type)
type WindowFuncDesc struct{ Kind, Field string; N int; Default any; Agg *WAggSpec }
```

### Aggregate expressions and rollup sets

```go
func CompileAggExprPatched(expression string, fieldNames []string) (ast.Node, error)   // the patched normal form exec evaluates (sum(x) → sum(_records, #.x))
func ExprFieldName(node ast.Node) (string, bool)
func RollupGroupingSets(fields []string, mode RollupMode) [][]string  // the sets a rollup aggregates over, in emission order
func RollupFieldPrefix(fields []string) string                        // "" for the grand total, else "a_b_"
```

### Runtime values in generated programs

```go
func MustNumber(literal, field, op string) float64   // a flag value for a numeric comparison; *CompareError if not a number
func MustBool(literal, field, op string) bool
func MustCast[T any](v any, target FieldType, field string) T   // typed generated code; *CastError on failure
func ParseFloat64(s string) float64                  // 0 on failure; flag defaults
```
