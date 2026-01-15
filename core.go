package ssql

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// SSQL - PURE ITER.SEQ DESIGN WITH DUAL ERROR HANDLING
// ============================================================================

// ============================================================================
// CORE FUNCTIONAL TYPES
// ============================================================================

// Filter transforms one iterator to another with full type flexibility.
// This is the core type for composable stream operations in ssql.
//
// Example:
//
//	// Create a filter that doubles integers
//	double := func(input iter.Seq[int]) iter.Seq[int] {
//	    return ssql.Select(func(x int) int { return x * 2 })(input)
//	}
//
//	// Use it in a pipeline
//	result := double(slices.Values([]int{1, 2, 3}))
type Filter[T, U any] func(iter.Seq[T]) iter.Seq[U]

// FilterWithErrors transforms an error-aware iterator to another error-aware iterator.
// Use this for operations that may fail during processing.
//
// Example:
//
//	// Create a filter that safely parses strings to integers
//	parseInt := func(input iter.Seq2[string, error]) iter.Seq2[int, error] {
//	    return ssql.SelectSafe(func(s string) (int, error) {
//	        return strconv.Atoi(s)
//	    })(input)
//	}
type FilterWithErrors[T, U any] func(iter.Seq2[T, error]) iter.Seq2[U, error]

// ============================================================================
// COMPOSITION FUNCTIONS
// ============================================================================

// Pipe composes two filters sequentially (T -> U -> V).
// This is the fundamental way to chain operations with type changes in ssql.
//
// Example:
//
//	// Compose a filter that parses strings to ints, then doubles them
//	parseInt := ssql.SelectSafe(func(s string) (int, error) {
//	    return strconv.Atoi(s)
//	})
//	double := ssql.Select(func(x int) int { return x * 2 })
//	pipeline := ssql.Pipe(parseInt, double)
//
//	// Use the composed filter
//	result := pipeline(slices.Values([]string{"1", "2", "3"}))
func Pipe[T, U, V any](f1 Filter[T, U], f2 Filter[U, V]) Filter[T, V] {
	return func(input iter.Seq[T]) iter.Seq[V] {
		return f2(f1(input))
	}
}

// Pipe3 composes three filters sequentially (T -> U -> V -> W).
// Use this for longer pipelines with multiple type transformations.
//
// Example:
//
//	// Parse strings → ints → floats → formatted strings
//	parseInt := ssql.Select(strconv.Atoi)
//	toFloat := ssql.Select(func(i int) float64 { return float64(i) })
//	format := ssql.Select(func(f float64) string { return fmt.Sprintf("%.2f", f) })
//	pipeline := ssql.Pipe3(parseInt, toFloat, format)
func Pipe3[T, U, V, W any](f1 Filter[T, U], f2 Filter[U, V], f3 Filter[V, W]) Filter[T, W] {
	return func(input iter.Seq[T]) iter.Seq[W] {
		return f3(f2(f1(input)))
	}
}

// Chain applies multiple same-type filters in sequence.
// Use this when all filters maintain the same type (no type transformations).
//
// Example:
//
//	// Apply multiple filtering operations in sequence
//	pipeline := ssql.Chain(
//	    ssql.Where(func(x int) bool { return x > 0 }),  // Keep positive
//	    ssql.Where(func(x int) bool { return x%2 == 0 }), // Keep even
//	    ssql.Take[int](10),                              // Limit to 10
//	)
//	result := pipeline(slices.Values([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}))
func Chain[T any](filters ...Filter[T, T]) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		if len(filters) == 0 {
			return input // Identity function for no filters
		}
		result := input
		for _, filter := range filters {
			result = filter(result)
		}
		return result
	}
}

// Error-aware composition functions
func PipeWithErrors[T, U, V any](f1 FilterWithErrors[T, U], f2 FilterWithErrors[U, V]) FilterWithErrors[T, V] {
	return func(input iter.Seq2[T, error]) iter.Seq2[V, error] {
		return f2(f1(input))
	}
}

func ChainWithErrors[T any](filters ...FilterWithErrors[T, T]) FilterWithErrors[T, T] {
	return func(input iter.Seq2[T, error]) iter.Seq2[T, error] {
		if len(filters) == 0 {
			return input
		}
		result := input
		for _, filter := range filters {
			result = filter(result)
		}
		return result
	}
}

// Concat combines multiple sequences into a single sequence.
// Elements are yielded in order: all elements from the first sequence,
// then all from the second, etc. Supports early termination.
//
// This is useful for combining data from multiple sources (SQL UNION ALL):
//
//	combined := ssql.Concat(
//	    ssql.ReadCSV("2023.csv"),
//	    ssql.ReadCSV("2024.csv"),
//	    ssql.ReadCSV("2025.csv"),
//	)
//
// For UNION (with deduplication), combine with DistinctBy:
//
//	result := ssql.DistinctBy(ssql.RecordKey)(combined)
func Concat[T any](seqs ...iter.Seq[T]) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, seq := range seqs {
			for item := range seq {
				if !yield(item) {
					return
				}
			}
		}
	}
}

// ============================================================================
// RECORD SYSTEM
// ============================================================================

// Schema defines the structure of records - field names, indices, and types.
// A Schema is shared across all records in a stream, avoiding per-record
// allocation of field name strings and enabling O(1) field access by index.
//
// Example:
//
//	schema := ssql.NewSchema([]string{"name", "age", "salary"})
//	// schema.Width == 3
//	// schema.Index("age") == 1
type Schema struct {
	fields      []string       // Field names in order
	indices     map[string]int // Field name → index for O(1) lookup
	width       int            // Number of fields (len(fields))
	jsonPrefixes [][]byte      // Pre-computed JSON prefixes: `"field":`
}

// NewSchema creates a Schema from field names.
// The order of fields is preserved for consistent iteration.
func NewSchema(fields []string) *Schema {
	indices := make(map[string]int, len(fields))
	jsonPrefixes := make([][]byte, len(fields))
	for i, f := range fields {
		indices[f] = i
		// Pre-compute JSON prefix: `"field_name":`
		jsonPrefixes[i] = appendJSONString(nil, f)
		jsonPrefixes[i] = append(jsonPrefixes[i], ':')
	}
	return &Schema{
		fields:       fields,
		indices:      indices,
		width:        len(fields),
		jsonPrefixes: jsonPrefixes,
	}
}

// Fields returns a copy of the field names in order
func (s *Schema) Fields() []string {
	result := make([]string, len(s.fields))
	copy(result, s.fields)
	return result
}

// Index returns the index of a field, or -1 if not found
func (s *Schema) Index(field string) int {
	if idx, ok := s.indices[field]; ok {
		return idx
	}
	return -1
}

// Has returns true if the schema contains the field
func (s *Schema) Has(field string) bool {
	_, ok := s.indices[field]
	return ok
}

// Width returns the number of fields
func (s *Schema) Width() int {
	return s.width
}

// Record represents structured data with native Go types.
// This is the primary data structure for working with CSV/JSON data and command output.
// Records use a slice-based storage with shared Schema for high performance.
//
// Key features:
//   - Type-safe field access with Get/GetOr
//   - Enforces canonical types (int64, float64, string, bool, time.Time) via setters
//   - Supports nested structures (Record, JSONString)
//   - Supports sequences (iter.Seq[T])
//   - Immutable updates via Record methods (creates copies)
//   - maps-style API (All, Keys, Values) for iteration
//   - Shared Schema across all records in a stream (memory efficient)
//   - O(1) field access via schema index lookup
//
// Example:
//
//	// Create a record
//	record := ssql.MakeMutableRecord().
//	    String("name", "Alice").
//	    Int("age", int64(30)).
//	    Float("salary", 95000.50).
//	    Freeze()
//
//	// Access fields with type conversion
//	name := ssql.GetOr(record, "name", "")
//	age := ssql.GetOr(record, "age", int64(0))
//
//	// Iterate over fields
//	for key, value := range record.All() {
//	    fmt.Printf("%s: %v\n", key, value)
//	}
//
//	// Immutable updates (creates new Record)
//	updated := record.Int("age", int64(31))
type Record struct {
	schema *Schema // Shared across all records in stream (for field name → index mapping)
	values []any   // Field values in schema order (exact size: schema.width)
}

// NewRecordFromSchema creates a Record with a pre-existing Schema.
// This is the efficient path used by CSV/JSON readers where schema is known upfront.
// The values slice is used directly (not copied) for efficiency.
func NewRecordFromSchema(schema *Schema, values []any) Record {
	return Record{schema: schema, values: values}
}

// Schema returns the record's schema (may be nil for empty records)
func (r Record) Schema() *Schema {
	return r.schema
}

// JSONString represents a string containing valid JSON data.
// This provides type safety and rich methods for working with JSON-structured data.
type JSONString string

// Parse parses the JSON string back to its original Go value
func (js JSONString) Parse() (any, error) {
	var result any
	err := json.Unmarshal([]byte(js), &result)
	return result, err
}

// MustParse parses the JSON string, panicking on error
func (js JSONString) MustParse() any {
	result, err := js.Parse()
	if err != nil {
		panic(fmt.Sprintf("failed to parse JSONString: %v", err))
	}
	return result
}

// IsValid returns true if the string contains valid JSON
func (js JSONString) IsValid() bool {
	var temp any
	return json.Unmarshal([]byte(js), &temp) == nil
}

// Pretty returns formatted JSON with indentation
func (js JSONString) Pretty() string {
	var temp any
	if err := json.Unmarshal([]byte(js), &temp); err != nil {
		return string(js) // Return original if invalid
	}
	pretty, err := json.MarshalIndent(temp, "", "  ")
	if err != nil {
		return string(js) // Return original if formatting fails
	}
	return string(pretty)
}

// String returns the underlying JSON string
func (js JSONString) String() string {
	return string(js)
}

// NewJSONString creates a JSONString by marshaling the given value
func NewJSONString(value any) (JSONString, error) {
	// Convert complex types to JSON-serializable form first
	jsonValue := convertToJSONValue(value)
	bytes, err := json.Marshal(jsonValue)
	if err != nil {
		return "", err
	}
	return JSONString(bytes), nil
}

// Value constraint for type-safe record values
// Hybrid approach: Canonical scalars (int64/float64), flexible sequences (any numeric type)
type Value interface {
	// Canonical scalar types only
	~int64 | ~float64 |

		// Other basic types
		~bool | string | time.Time |

		// JSON and Record types for structured data
		JSONString | Record |

		// Slice type for aggregation results (e.g., Collect)
		[]any |

		// Iterator types - common types for ergonomics with slices.Values()
		iter.Seq[int] | iter.Seq[int64] | iter.Seq[float64] |
		iter.Seq[bool] | iter.Seq[string] | iter.Seq[time.Time] |
		iter.Seq[Record] | iter.Seq[any]
}

// ============================================================================
// MUTABLE RECORD - EFFICIENT BUILDING WITH IN-PLACE MUTATION
// ============================================================================

// MutableRecord is a Record type optimized for efficient building.
// Methods mutate in place and return the same instance for chaining.
// Use .Freeze() to convert to a regular Record when done building.
//
// MutableRecord is the recommended way to build new records efficiently.
// Unlike Record methods which create copies, MutableRecord methods modify
// the same underlying map, avoiding unnecessary allocations.
//
// Example:
//
//	// Efficient building with MutableRecord
//	record := ssql.MakeMutableRecord().
//	    String("name", "Alice").
//	    Int("age", int64(30)).
//	    Float("salary", 95000.50).
//	    Bool("active", true).
//	    Freeze()  // Convert to immutable Record
//
//	// Later modifications create new copies (immutable)
//	updated := record.Int("age", int64(31))  // Creates new Record
type MutableRecord struct {
	fields map[string]any
}

// MakeMutableRecord creates an empty MutableRecord for efficient building.
// This is the recommended way to create new records.
//
// Example:
//
//	record := ssql.MakeMutableRecord().
//	    String("city", "San Francisco").
//	    Int("population", int64(873965)).
//	    Freeze()
func MakeMutableRecord() MutableRecord {
	return MutableRecord{fields: make(map[string]any)}
}

// MakeMutableRecordWithCapacity creates a MutableRecord with pre-allocated capacity
func MakeMutableRecordWithCapacity(capacity int) MutableRecord {
	return MutableRecord{fields: make(map[string]any, capacity)}
}

// NewRecord creates a Record from a map (for compatibility).
// This creates a Schema from the map keys for the new slice-based storage.
// For high-performance scenarios, use NewRecordFromSchema with a pre-existing Schema.
func NewRecord(fields map[string]any) Record {
	if len(fields) == 0 {
		return Record{schema: nil, values: nil}
	}

	// Extract sorted keys for consistent schema ordering
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Create schema and values slice
	schema := NewSchema(keys)
	values := make([]any, len(keys))
	for i, k := range keys {
		values[i] = fields[k]
	}

	return Record{schema: schema, values: values}
}

// Freeze converts a MutableRecord to an immutable Record.
// Creates a new Schema and values slice from the MutableRecord's map.
func (m MutableRecord) Freeze() Record {
	return NewRecord(m.fields)
}

// ToMutable creates a mutable copy of a Record for modification.
// This preserves immutability by creating a map copy from the Record's values.
func (r Record) ToMutable() MutableRecord {
	if r.schema == nil {
		return MutableRecord{fields: make(map[string]any)}
	}
	m := make(map[string]any, r.schema.width)
	for i, field := range r.schema.fields {
		m[field] = r.values[i]
	}
	return MutableRecord{fields: m}
}

// ============================================================================
// TYPE-SAFE RECORD ACCESS
// ============================================================================

// Get retrieves a typed value from a record with automatic conversion.
// Returns the value and a boolean indicating whether the field exists.
//
// Get performs smart type conversions:
//   - int64 ↔ float64 (with truncation/widening)
//   - string → int64/float64 (parsing)
//   - bool ↔ int64 (0/1)
//   - string → time.Time (RFC3339, SQL datetime)
//   - int64 → time.Time (Unix timestamp)
//
// Example:
//
//	record := ssql.MakeMutableRecord().
//	    String("age_str", "30").
//	    Int("count", int64(42)).
//	    Freeze()
//
//	// Direct type match
//	count, ok := ssql.Get[int64](record, "count")  // 42, true
//
//	// Smart conversion from string to int64
//	age, ok := ssql.Get[int64](record, "age_str")  // 30, true
//
//	// Field doesn't exist
//	missing, ok := ssql.Get[string](record, "missing")  // "", false
func Get[T any](r Record, field string) (T, bool) {
	var zero T
	if r.schema == nil {
		return zero, false
	}

	idx := r.schema.Index(field)
	if idx < 0 || idx >= len(r.values) {
		return zero, false
	}

	val := r.values[idx]

	// Direct type assertion first (fast path)
	if typed, ok := val.(T); ok {
		return typed, true
	}

	// Smart type conversion (slower path)
	if converted, ok := convertTo[T](val); ok {
		return converted, true
	}

	return zero, false
}

// GetOr retrieves a typed value with a default fallback.
// This is the most common way to access Record fields safely.
//
// Always use canonical types for defaults:
//   - int64(0) not int(0)
//   - float64(0.0) not float32(0.0)
//
// Example:
//
//	// CSV data (auto-parsed to int64/float64)
//	data, _ := ssql.ReadCSV("people.csv")
//	for record := range data {
//	    // Always use int64 with CSV data
//	    age := ssql.GetOr(record, "age", int64(0))
//	    salary := ssql.GetOr(record, "salary", float64(0.0))
//	    name := ssql.GetOr(record, "name", "Unknown")
//
//	    // Convert to int if needed
//	    ageInt := int(age)
//	}
func GetOr[T any](r Record, field string, defaultVal T) T {
	if val, ok := Get[T](r, field); ok {
		return val
	}
	return defaultVal
}

// Has checks if a field exists
func (r Record) Has(field string) bool {
	if r.schema == nil {
		return false
	}
	return r.schema.Has(field)
}

// Len returns the number of fields in the record
func (r Record) Len() int {
	if r.schema == nil {
		return 0
	}
	return r.schema.width
}

// Keys returns all field names as a slice (in schema order)
func (r Record) Keys() []string {
	if r.schema == nil {
		return nil
	}
	return r.schema.Fields() // Returns a copy
}

// ============================================================================
// MAPS-STYLE ITERATOR API
// ============================================================================

// All returns an iterator over key-value pairs (matches maps.All).
// Iterates in schema order (consistent ordering).
func (r Record) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if r.schema == nil {
			return
		}
		for i, field := range r.schema.fields {
			if !yield(field, r.values[i]) {
				return
			}
		}
	}
}

// KeysIter returns an iterator over field names (matches maps.Keys).
// Iterates in schema order.
func (r Record) KeysIter() iter.Seq[string] {
	return func(yield func(string) bool) {
		if r.schema == nil {
			return
		}
		for _, field := range r.schema.fields {
			if !yield(field) {
				return
			}
		}
	}
}

// Values returns an iterator over field values (matches maps.Values).
// Iterates in schema order.
func (r Record) Values() iter.Seq[any] {
	return func(yield func(any) bool) {
		if r.schema == nil {
			return
		}
		for _, v := range r.values {
			if !yield(v) {
				return
			}
		}
	}
}

// Clone creates a shallow copy of the record (matches maps.Clone).
// The cloned record shares the same Schema (schema is immutable).
func (r Record) Clone() Record {
	if r.schema == nil {
		return Record{schema: nil, values: nil}
	}
	cloned := make([]any, len(r.values))
	copy(cloned, r.values)
	return Record{schema: r.schema, values: cloned}
}

// Equal checks if two records have the same fields and values (matches maps.Equal)
func (r Record) Equal(other Record) bool {
	// Both nil schemas means both empty
	if r.schema == nil && other.schema == nil {
		return true
	}
	// One nil, one not
	if r.schema == nil || other.schema == nil {
		return false
	}
	// Different number of fields
	if r.schema.width != other.schema.width {
		return false
	}
	// Check that all fields match
	for i, field := range r.schema.fields {
		otherIdx := other.schema.Index(field)
		if otherIdx < 0 {
			return false
		}
		if r.values[i] != other.values[otherIdx] {
			return false
		}
	}
	return true
}

// ============================================================================
// JSON MARSHALING
// ============================================================================

// MarshalJSON implements json.Marshaler.
// Records marshal as {"name": "Alice", "age": 30} not {"schema": ..., "values": ...}
func (r Record) MarshalJSON() ([]byte, error) {
	if r.schema == nil {
		return []byte("{}"), nil
	}
	// Build a map for JSON encoding
	m := make(map[string]any, r.schema.width)
	for i, field := range r.schema.fields {
		m[field] = r.values[i]
	}
	return json.Marshal(m)
}

// UnmarshalJSON implements json.Unmarshaler
func (r *Record) UnmarshalJSON(data []byte) error {
	fields := make(map[string]any)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// Use NewRecord to create schema and values
	*r = NewRecord(fields)
	return nil
}

// MarshalJSON implements json.Marshaler for MutableRecord
func (m MutableRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.fields)
}

// UnmarshalJSON implements json.Unmarshaler for MutableRecord
func (m *MutableRecord) UnmarshalJSON(data []byte) error {
	fields := make(map[string]any)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	m.fields = fields
	return nil
}

// ============================================================================
// MUTABLERECORD FIELD METHODS - IN-PLACE MUTATION (EFFICIENT)
// ============================================================================

// Set adds a field with compile-time type safety (mutates in place)
func Set[V Value](m MutableRecord, field string, value V) MutableRecord {
	m.fields[field] = value
	return m
}

// Delete removes a field (mutates in place)
func (m MutableRecord) Delete(field string) MutableRecord {
	delete(m.fields, field)
	return m
}

// Rename renames a field (mutates in place)
// If the old field doesn't exist, this is a no-op.
// If the new field name already exists, it will be overwritten.
func (m MutableRecord) Rename(oldField, newField string) MutableRecord {
	if val, exists := m.fields[oldField]; exists {
		m.fields[newField] = val
		delete(m.fields, oldField)
	}
	return m
}

// Len returns the number of fields
func (m MutableRecord) Len() int {
	return len(m.fields)
}

// String adds a string field (mutates in place)
func (m MutableRecord) String(field, value string) MutableRecord {
	return Set(m, field, value)
}

// JSONString adds a JSON string field (mutates in place)
func (m MutableRecord) JSONString(field string, value JSONString) MutableRecord {
	return Set(m, field, value)
}

// Int adds an integer field (mutates in place)
func (m MutableRecord) Int(field string, value int64) MutableRecord {
	return Set(m, field, value)
}

// Float adds a float field (mutates in place)
func (m MutableRecord) Float(field string, value float64) MutableRecord {
	return Set(m, field, value)
}

// Bool adds a boolean field (mutates in place)
func (m MutableRecord) Bool(field string, value bool) MutableRecord {
	return Set(m, field, value)
}

// Time adds a time field (mutates in place)
func (m MutableRecord) Time(field string, value time.Time) MutableRecord {
	return Set(m, field, value)
}

// Nested adds a nested record field (mutates in place)
func (m MutableRecord) Nested(field string, value Record) MutableRecord {
	return Set(m, field, value)
}

// ============================================================================
// RECORD FIELD METHODS - IMMUTABLE UPDATES (CREATES COPIES)
// ============================================================================

// SetImmutable adds a field with compile-time type safety - creates new Record (immutable).
// If the field exists, updates its value. If not, creates a new schema with the field.
func SetImmutable[V Value](r Record, field string, value V) Record {
	if r.schema == nil {
		// Empty record - create new with single field
		schema := NewSchema([]string{field})
		return Record{schema: schema, values: []any{value}}
	}

	idx := r.schema.Index(field)
	if idx >= 0 {
		// Field exists - update value (same schema)
		newValues := make([]any, len(r.values))
		copy(newValues, r.values)
		newValues[idx] = value
		return Record{schema: r.schema, values: newValues}
	}

	// Field doesn't exist - create new schema with additional field
	newFields := make([]string, len(r.schema.fields)+1)
	copy(newFields, r.schema.fields)
	newFields[len(r.schema.fields)] = field
	sort.Strings(newFields)

	newSchema := NewSchema(newFields)
	newValues := make([]any, newSchema.width)

	// Copy existing values to their new positions
	for i, f := range r.schema.fields {
		newIdx := newSchema.Index(f)
		newValues[newIdx] = r.values[i]
	}
	// Set the new field value
	newIdx := newSchema.Index(field)
	newValues[newIdx] = value

	return Record{schema: newSchema, values: newValues}
}

// String adds a string field (creates new Record)
func (r Record) String(field, value string) Record {
	return SetImmutable(r, field, value)
}

// JSONString adds a JSON string field (creates new Record)
func (r Record) JSONString(field string, value JSONString) Record {
	return SetImmutable(r, field, value)
}

// Int adds an integer field (creates new Record)
func (r Record) Int(field string, value int64) Record {
	return SetImmutable(r, field, value)
}

// Float adds a float field (creates new Record)
func (r Record) Float(field string, value float64) Record {
	return SetImmutable(r, field, value)
}

// Bool adds a boolean field (creates new Record)
func (r Record) Bool(field string, value bool) Record {
	return SetImmutable(r, field, value)
}

// Time adds a time field (creates new Record)
func (r Record) Time(field string, value time.Time) Record {
	return SetImmutable(r, field, value)
}

// Nested adds a nested record field (creates new Record)
func (r Record) Nested(field string, value Record) Record {
	return SetImmutable(r, field, value)
}

// ============================================================================
// MUTABLERECORD ITER.SEQ FIELD METHODS - IN-PLACE MUTATION
// ============================================================================

// IntSeq adds an iter.Seq[int] field (mutates in place)
func (m MutableRecord) IntSeq(field string, value iter.Seq[int]) MutableRecord {
	return Set(m, field, value)
}

// Int64Seq adds an iter.Seq[int64] field (mutates in place)
func (m MutableRecord) Int64Seq(field string, value iter.Seq[int64]) MutableRecord {
	return Set(m, field, value)
}

// Float64Seq adds an iter.Seq[float64] field (mutates in place)
func (m MutableRecord) Float64Seq(field string, value iter.Seq[float64]) MutableRecord {
	return Set(m, field, value)
}

// BoolSeq adds an iter.Seq[bool] field (mutates in place)
func (m MutableRecord) BoolSeq(field string, value iter.Seq[bool]) MutableRecord {
	return Set(m, field, value)
}

// StringSeq adds an iter.Seq[string] field (mutates in place)
func (m MutableRecord) StringSeq(field string, value iter.Seq[string]) MutableRecord {
	return Set(m, field, value)
}

// TimeSeq adds an iter.Seq[time.Time] field (mutates in place)
func (m MutableRecord) TimeSeq(field string, value iter.Seq[time.Time]) MutableRecord {
	return Set(m, field, value)
}

// RecordSeq adds an iter.Seq[Record] field (mutates in place)
func (m MutableRecord) RecordSeq(field string, value iter.Seq[Record]) MutableRecord {
	return Set(m, field, value)
}

// ============================================================================
// RECORD ITER.SEQ FIELD METHODS - IMMUTABLE UPDATES
// ============================================================================

// IntSeq adds an iter.Seq[int] field (creates new Record)
func (r Record) IntSeq(field string, value iter.Seq[int]) Record {
	return SetImmutable(r, field, value)
}

// Int64Seq adds an iter.Seq[int64] field (creates new Record)
func (r Record) Int64Seq(field string, value iter.Seq[int64]) Record {
	return SetImmutable(r, field, value)
}

// Float64Seq adds an iter.Seq[float64] field (creates new Record)
func (r Record) Float64Seq(field string, value iter.Seq[float64]) Record {
	return SetImmutable(r, field, value)
}

// BoolSeq adds an iter.Seq[bool] field (creates new Record)
func (r Record) BoolSeq(field string, value iter.Seq[bool]) Record {
	return SetImmutable(r, field, value)
}

// StringSeq adds an iter.Seq[string] field (creates new Record)
func (r Record) StringSeq(field string, value iter.Seq[string]) Record {
	return SetImmutable(r, field, value)
}

// TimeSeq adds an iter.Seq[time.Time] field (creates new Record)
func (r Record) TimeSeq(field string, value iter.Seq[time.Time]) Record {
	return SetImmutable(r, field, value)
}

// RecordSeq adds an iter.Seq[Record] field (creates new Record)
func (r Record) RecordSeq(field string, value iter.Seq[Record]) Record {
	return SetImmutable(r, field, value)
}

// ============================================================================
// SMART TYPE CONVERSION SYSTEM
// ============================================================================

func convertTo[T any](val any) (T, bool) {
	var zero T
	targetType := reflect.TypeOf(zero)

	// Handle nil
	if val == nil {
		return zero, false
	}

	sourceVal := reflect.ValueOf(val)

	// Try direct conversion for basic types
	if sourceVal.Type().ConvertibleTo(targetType) {
		converted := sourceVal.Convert(targetType)
		return converted.Interface().(T), true
	}

	// Custom conversions for common cases
	switch target := any(zero).(type) {
	case int64:
		if converted, ok := convertToInt64(val); ok {
			return any(converted).(T), true
		}
		return zero, false
	case float64:
		if converted, ok := convertToFloat64(val); ok {
			return any(converted).(T), true
		}
		return zero, false
	case string:
		if converted, ok := convertToString(val); ok {
			return any(converted).(T), true
		}
		return zero, false
	case bool:
		if converted, ok := convertToBool(val); ok {
			return any(converted).(T), true
		}
		return zero, false
	case time.Time:
		if converted, ok := convertToTime(val); ok {
			return any(converted).(T), true
		}
		return zero, false
	default:
		_ = target
		return zero, false
	}
}

// convertToInt64 only handles conversions TO canonical int64 type
// From: float64, string, bool (canonical sources only)
// Users must explicitly convert from int/int32/uint/etc to int64
func convertToInt64(val any) (int64, bool) {
	switch v := val.(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true // Allow float64 -> int64 truncation
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			return parsed, true
		}
		return 0, false
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// convertToFloat64 only handles conversions TO canonical float64 type
// From: int64, string (canonical sources only)
// Users must explicitly convert from float32/int/int32/etc to float64
func convertToFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true // Allow int64 -> float64 widening
	case string:
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func convertToString(val any) (string, bool) {
	switch v := val.(type) {
	case string:
		return v, true
	case []byte:
		return string(v), true
	default:
		return fmt.Sprintf("%v", val), true
	}
}

// convertToBool only handles conversions from canonical types
func convertToBool(val any) (bool, bool) {
	switch v := val.(type) {
	case bool:
		return v, true
	case int64:
		return v != 0, true
	case float64:
		return v != 0, true
	case string:
		return v != "", true
	default:
		return false, false
	}
}

func convertToTime(val any) (time.Time, bool) {
	switch v := val.(type) {
	case time.Time:
		return v, true
	case string:
		// Try RFC3339 first (most common for APIs)
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
		// Try standard SQL datetime format
		if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
			return t, true
		}
		// Try RFC3339 without timezone (assume UTC)
		if t, err := time.Parse("2006-01-02T15:04:05", v); err == nil {
			return t.UTC(), true
		}
		return time.Time{}, false
	case int64:
		// Unix timestamp - always UTC
		return time.Unix(v, 0).UTC(), true
	default:
		return time.Time{}, false
	}
}

// ============================================================================
// RECORD UTILITY FUNCTIONS
// ============================================================================

// RecordKey returns a unique string key for a Record based on all its fields.
// This is useful for deduplication with DistinctBy (e.g., SQL UNION):
//
//	distinct := ssql.DistinctBy(ssql.RecordKey)
//	result := distinct(combined)
func RecordKey(r Record) string {
	if r.schema == nil {
		return "{}"
	}

	// Use JSON-like representation for consistent, deterministic keys
	// Schema.fields is already in sorted order from NewSchema
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range r.schema.fields {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%v", k, r.values[i])
	}
	b.WriteByte('}')
	return b.String()
}

// ============================================================================
// FAST JSON ENCODING
// ============================================================================

// AppendJSON appends the JSON representation of this Record to buf.
// This is much faster than encoding/json because it uses pre-computed
// field prefixes and avoids reflection.
func (r Record) AppendJSON(buf []byte) []byte {
	if r.schema == nil {
		return append(buf, '{', '}')
	}

	buf = append(buf, '{')
	first := true
	for i, v := range r.values {
		if v == nil {
			continue // Skip nil values
		}
		if !first {
			buf = append(buf, ',')
		}
		first = false
		// Use pre-computed prefix: `"field":`
		buf = append(buf, r.schema.jsonPrefixes[i]...)
		buf = appendJSONValue(buf, v)
	}
	buf = append(buf, '}')
	return buf
}

// appendJSONValue appends a JSON-encoded value to buf.
// Handles all common types efficiently without reflection.
func appendJSONValue(buf []byte, v any) []byte {
	switch val := v.(type) {
	case nil:
		return append(buf, "null"...)
	case string:
		return appendJSONString(buf, val)
	case int64:
		return strconv.AppendInt(buf, val, 10)
	case float64:
		return strconv.AppendFloat(buf, val, 'g', -1, 64)
	case bool:
		if val {
			return append(buf, "true"...)
		}
		return append(buf, "false"...)
	case int:
		return strconv.AppendInt(buf, int64(val), 10)
	case int32:
		return strconv.AppendInt(buf, int64(val), 10)
	case float32:
		return strconv.AppendFloat(buf, float64(val), 'g', -1, 32)
	case JSONString:
		// JSONString is already valid JSON - append raw
		return append(buf, string(val)...)
	case []any:
		return appendJSONArray(buf, val)
	case map[string]any:
		return appendJSONMap(buf, val)
	case Record:
		return val.AppendJSON(buf)
	case time.Time:
		buf = append(buf, '"')
		buf = val.AppendFormat(buf, time.RFC3339Nano)
		buf = append(buf, '"')
		return buf
	default:
		// Fallback: use encoding/json for unknown types
		data, err := json.Marshal(val)
		if err != nil {
			return append(buf, "null"...)
		}
		return append(buf, data...)
	}
}

// appendJSONString appends a JSON-escaped string to buf.
func appendJSONString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			if c < 0x20 {
				// Control character - encode as \uXXXX
				buf = append(buf, '\\', 'u', '0', '0')
				buf = append(buf, "0123456789abcdef"[c>>4])
				buf = append(buf, "0123456789abcdef"[c&0xf])
			} else {
				buf = append(buf, c)
			}
		}
	}
	buf = append(buf, '"')
	return buf
}

// appendJSONArray appends a JSON array to buf.
func appendJSONArray(buf []byte, arr []any) []byte {
	buf = append(buf, '[')
	for i, v := range arr {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendJSONValue(buf, v)
	}
	buf = append(buf, ']')
	return buf
}

// appendJSONMap appends a JSON object to buf.
// Keys are sorted for consistent output.
func appendJSONMap(buf []byte, m map[string]any) []byte {
	// Sort keys for consistent output
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	buf = append(buf, '{')
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendJSONString(buf, k)
		buf = append(buf, ':')
		buf = appendJSONValue(buf, m[k])
	}
	buf = append(buf, '}')
	return buf
}

// ============================================================================
// FAST JSON PARSING (AVOIDS REFLECTION)
// ============================================================================

// ParseJSONLine parses a single JSON line into a MutableRecord.
// This is 3-5x faster than json.Unmarshal for typical JSONL records.
// Handles: strings, numbers (int64/float64), booleans, null, arrays, objects.
// Arrays and nested objects are stored as JSONString for later parsing if needed.
func ParseJSONLine(line []byte) (MutableRecord, error) {
	record := MakeMutableRecord()
	pos := 0
	n := len(line)

	// Skip leading whitespace
	for pos < n && isJSONWhitespace(line[pos]) {
		pos++
	}

	if pos >= n {
		return record, nil // Empty line
	}

	// Expect opening brace
	if line[pos] != '{' {
		return record, fmt.Errorf("expected '{' at position %d", pos)
	}
	pos++

	// Parse fields
	for {
		// Skip whitespace
		for pos < n && isJSONWhitespace(line[pos]) {
			pos++
		}

		if pos >= n {
			return record, fmt.Errorf("unexpected end of JSON")
		}

		// Check for closing brace (empty object or end)
		if line[pos] == '}' {
			break
		}

		// Parse field name (must be a string)
		if line[pos] != '"' {
			return record, fmt.Errorf("expected '\"' for field name at position %d", pos)
		}

		fieldName, newPos, err := parseJSONStringValue(line, pos)
		if err != nil {
			return record, err
		}
		pos = newPos

		// Skip whitespace
		for pos < n && isJSONWhitespace(line[pos]) {
			pos++
		}

		// Expect colon
		if pos >= n || line[pos] != ':' {
			return record, fmt.Errorf("expected ':' after field name at position %d", pos)
		}
		pos++

		// Skip whitespace
		for pos < n && isJSONWhitespace(line[pos]) {
			pos++
		}

		// Parse value
		value, newPos, err := parseJSONValue(line, pos)
		if err != nil {
			return record, err
		}
		pos = newPos

		// Add field to record (skip nil values from JSON null)
		if value != nil {
			record.fields[fieldName] = value
		}

		// Skip whitespace
		for pos < n && isJSONWhitespace(line[pos]) {
			pos++
		}

		if pos >= n {
			return record, fmt.Errorf("unexpected end of JSON")
		}

		// Check for comma or closing brace
		if line[pos] == ',' {
			pos++
			continue
		} else if line[pos] == '}' {
			break
		} else {
			return record, fmt.Errorf("expected ',' or '}' at position %d, got '%c'", pos, line[pos])
		}
	}

	return record, nil
}

// isJSONWhitespace returns true for JSON whitespace characters
func isJSONWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// parseJSONStringValue parses a JSON string starting at pos (which must be at '"')
// Returns the unescaped string value and the position after the closing quote
func parseJSONStringValue(line []byte, pos int) (string, int, error) {
	if pos >= len(line) || line[pos] != '"' {
		return "", pos, fmt.Errorf("expected '\"' at position %d", pos)
	}
	pos++ // Skip opening quote

	// Fast path: check if string has no escapes
	start := pos
	for pos < len(line) {
		c := line[pos]
		if c == '"' {
			// Simple string without escapes
			return string(line[start:pos]), pos + 1, nil
		}
		if c == '\\' {
			// Has escapes, use slow path
			break
		}
		pos++
	}

	// Slow path: handle escapes
	var buf []byte
	buf = append(buf, line[start:pos]...)

	for pos < len(line) {
		c := line[pos]
		if c == '"' {
			return string(buf), pos + 1, nil
		}
		if c == '\\' {
			pos++
			if pos >= len(line) {
				return "", pos, fmt.Errorf("unexpected end of string escape")
			}
			switch line[pos] {
			case '"', '\\', '/':
				buf = append(buf, line[pos])
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				// Unicode escape: \uXXXX
				if pos+4 >= len(line) {
					return "", pos, fmt.Errorf("invalid unicode escape")
				}
				r, err := strconv.ParseUint(string(line[pos+1:pos+5]), 16, 16)
				if err != nil {
					return "", pos, fmt.Errorf("invalid unicode escape: %w", err)
				}
				buf = append(buf, string(rune(r))...)
				pos += 4
			default:
				return "", pos, fmt.Errorf("invalid escape character '\\%c'", line[pos])
			}
			pos++
		} else {
			buf = append(buf, c)
			pos++
		}
	}

	return "", pos, fmt.Errorf("unterminated string")
}

// parseJSONValue parses any JSON value starting at pos
// Returns the parsed value (or nil for null) and the position after the value
func parseJSONValue(line []byte, pos int) (any, int, error) {
	if pos >= len(line) {
		return nil, pos, fmt.Errorf("unexpected end of JSON value")
	}

	c := line[pos]

	switch {
	case c == '"':
		// String
		s, newPos, err := parseJSONStringValue(line, pos)
		return s, newPos, err

	case c == '-' || (c >= '0' && c <= '9'):
		// Number
		return parseJSONNumber(line, pos)

	case c == 't':
		// true
		if pos+4 <= len(line) && string(line[pos:pos+4]) == "true" {
			return true, pos + 4, nil
		}
		return nil, pos, fmt.Errorf("invalid JSON value at position %d", pos)

	case c == 'f':
		// false
		if pos+5 <= len(line) && string(line[pos:pos+5]) == "false" {
			return false, pos + 5, nil
		}
		return nil, pos, fmt.Errorf("invalid JSON value at position %d", pos)

	case c == 'n':
		// null
		if pos+4 <= len(line) && string(line[pos:pos+4]) == "null" {
			return nil, pos + 4, nil
		}
		return nil, pos, fmt.Errorf("invalid JSON value at position %d", pos)

	case c == '[':
		// Array - capture as JSONString
		return parseJSONRawValue(line, pos)

	case c == '{':
		// Nested object - capture as JSONString
		return parseJSONRawValue(line, pos)

	default:
		return nil, pos, fmt.Errorf("unexpected character '%c' at position %d", c, pos)
	}
}

// parseJSONNumber parses a JSON number starting at pos
// Returns int64 for integers, float64 for decimals
func parseJSONNumber(line []byte, pos int) (any, int, error) {
	start := pos
	n := len(line)

	// Handle negative sign
	if pos < n && line[pos] == '-' {
		pos++
	}

	// Parse integer part
	if pos >= n {
		return nil, pos, fmt.Errorf("invalid number at position %d", start)
	}

	if line[pos] == '0' {
		pos++
	} else if line[pos] >= '1' && line[pos] <= '9' {
		for pos < n && line[pos] >= '0' && line[pos] <= '9' {
			pos++
		}
	} else {
		return nil, pos, fmt.Errorf("invalid number at position %d", start)
	}

	// Check for decimal point or exponent
	isFloat := false
	if pos < n && line[pos] == '.' {
		isFloat = true
		pos++
		// Must have at least one digit after decimal
		if pos >= n || line[pos] < '0' || line[pos] > '9' {
			return nil, pos, fmt.Errorf("invalid number: expected digit after decimal at position %d", pos)
		}
		for pos < n && line[pos] >= '0' && line[pos] <= '9' {
			pos++
		}
	}

	// Check for exponent
	if pos < n && (line[pos] == 'e' || line[pos] == 'E') {
		isFloat = true
		pos++
		if pos < n && (line[pos] == '+' || line[pos] == '-') {
			pos++
		}
		if pos >= n || line[pos] < '0' || line[pos] > '9' {
			return nil, pos, fmt.Errorf("invalid number: expected digit in exponent at position %d", pos)
		}
		for pos < n && line[pos] >= '0' && line[pos] <= '9' {
			pos++
		}
	}

	numStr := string(line[start:pos])

	if isFloat {
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return nil, pos, fmt.Errorf("invalid float: %w", err)
		}
		return f, pos, nil
	}

	i, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		// Fall back to float for very large integers
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return nil, pos, fmt.Errorf("invalid number: %w", err)
		}
		return f, pos, nil
	}
	return i, pos, nil
}

// parseJSONRawValue captures a raw JSON array or object as JSONString
// Used for nested structures that we don't need to parse immediately
func parseJSONRawValue(line []byte, pos int) (JSONString, int, error) {
	if pos >= len(line) {
		return "", pos, fmt.Errorf("unexpected end of JSON")
	}

	start := pos
	c := line[pos]

	if c != '[' && c != '{' {
		return "", pos, fmt.Errorf("expected '[' or '{' at position %d", pos)
	}

	closeChar := byte(']')
	if c == '{' {
		closeChar = '}'
	}

	depth := 1
	pos++
	inString := false

	for pos < len(line) && depth > 0 {
		c := line[pos]

		if inString {
			if c == '\\' && pos+1 < len(line) {
				pos += 2 // Skip escaped character
				continue
			}
			if c == '"' {
				inString = false
			}
			pos++
			continue
		}

		switch c {
		case '"':
			inString = true
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
		pos++
	}

	if depth != 0 {
		return "", pos, fmt.Errorf("unterminated %c at position %d", closeChar, start)
	}

	return JSONString(line[start:pos]), pos, nil
}

// ============================================================================
// RUNTIME VALIDATION HELPERS
// ============================================================================

// validateRecord checks if a Record has only Value-compatible field types
func ValidateRecord(r Record) error {
	for field, value := range r.All() {
		if !isValueType(value) {
			return fmt.Errorf("field '%s' has invalid type %T", field, value)
		}
	}
	return nil
}

// IsValueType checks if a value conforms to the Value interface using type assertions
// Hybrid approach: Only canonical scalars (int64, float64), but all sequence variants allowed
func isValueType(value any) bool {
	switch value.(type) {
	// Canonical scalar types only
	case int64, float64:
		return true
	// Other basic types
	case bool, string:
		return true
	case time.Time:
		return true
	case JSONString:
		return true
	// Record type
	case Record:
		return true
	// Iterator types - all numeric variants allowed for ergonomics
	case iter.Seq[int], iter.Seq[int8], iter.Seq[int16], iter.Seq[int32], iter.Seq[int64]:
		return true
	case iter.Seq[uint], iter.Seq[uint8], iter.Seq[uint16], iter.Seq[uint32], iter.Seq[uint64]:
		return true
	case iter.Seq[float32], iter.Seq[float64]:
		return true
	case iter.Seq[bool], iter.Seq[string]:
		return true
	case iter.Seq[time.Time], iter.Seq[Record]:
		return true
	default:
		return false
	}
}

// Field creates a single-field Record with compile-time type safety
func Field[V Value](key string, value V) Record {
	schema := NewSchema([]string{key})
	return Record{schema: schema, values: []any{value}}
}

// ============================================================================
// NUMERIC AND COMPARABLE CONSTRAINTS - CANONICAL TYPES ONLY
// ============================================================================

// Numeric represents canonical types that support arithmetic operations
// Only int64 and float64 - users must convert from other numeric types
type Numeric interface {
	~int64 | ~float64
}

// Comparable represents canonical types that can be compared and sorted
// Only int64, float64, and string - users must convert from other numeric types
type Comparable interface {
	~int64 | ~float64 | ~string
}

// ============================================================================
// CONVERSION UTILITIES
// ============================================================================

// Safe converts a simple iterator to an error-aware iterator (never errors).
// Use this to bridge between error-unaware and error-aware operations.
//
// Example:
//
//	// Start with simple data
//	numbers := slices.Values([]int{1, 2, 3, 4, 5})
//
//	// Convert to error-aware for operations that might fail
//	numbersSafe := ssql.Safe(numbers)
//
//	// Use with error-aware operations
//	results := ssql.SelectSafe(func(x int) (string, error) {
//	    if x == 0 {
//	        return "", errors.New("cannot process zero")
//	    }
//	    return fmt.Sprintf("num_%d", x), nil
//	})(numbersSafe)
func Safe[T any](seq iter.Seq[T]) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for v := range seq {
			if !yield(v, nil) {
				return
			}
		}
	}
}

// Unsafe converts an error-aware iterator to simple iterator (panics on errors).
// Use this when you want to fail fast on any error.
//
// Example:
//
//	// Read CSV data (returns iter.Seq2[Record, error])
//	data, _ := ssql.ReadCSV("data.csv")
//
//	// Convert to simple iterator - panics if any error occurs
//	dataUnsafe := ssql.Unsafe(data)
//
//	// Use with simple operations
//	filtered := ssql.Where(func(r ssql.Record) bool {
//	    age := ssql.GetOr(r, "age", int64(0))
//	    return age > 25
//	})(dataUnsafe)
func Unsafe[T any](seq iter.Seq2[T, error]) iter.Seq[T] {
	return func(yield func(T) bool) {
		for v, err := range seq {
			if err != nil {
				panic(err)
			}
			if !yield(v) {
				return
			}
		}
	}
}

// IgnoreErrors converts an error-aware iterator to simple iterator (skips errors).
// Use this for best-effort processing where you want to skip problematic records.
//
// Example:
//
//	// Read CSV with some malformed rows
//	data, _ := ssql.ReadCSV("messy_data.csv")
//
//	// Process valid records, skip errors silently
//	validData := ssql.IgnoreErrors(data)
//
//	// Continue with normal processing
//	results := ssql.Where(func(r ssql.Record) bool {
//	    age := ssql.GetOr(r, "age", int64(0))
//	    return age > 0
//	})(validData)
func IgnoreErrors[T any](seq iter.Seq2[T, error]) iter.Seq[T] {
	return func(yield func(T) bool) {
		for v, err := range seq {
			if err != nil {
				continue // Skip items with errors
			}
			if !yield(v) {
				return
			}
		}
	}
}

// ============================================================================
// SEQUENCE CREATION UTILITIES
// ============================================================================

// From creates an iterator from a slice - convenience wrapper around slices.Values
// This provides a more discoverable API for users coming from other streaming libraries
func From[T any](slice []T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, v := range slice {
			if !yield(v) {
				return
			}
		}
	}
}

// ============================================================================
// SEQUENCE MATERIALIZATION - EFFICIENT GROUPING SUPPORT
// ============================================================================

// Materialize converts an iter.Seq field to a string representation for efficient grouping.
// This is much more efficient than using CrossFlatten/DotFlatten when you only need
// to group by sequence content, not expand records.
//
// Example: {"tags": iter.Seq["urgent", "work"]} → {"tags": iter.Seq[...], "tags_key": "urgent,work"}
func Materialize(sourceField, targetField, separator string) Filter[Record, Record] {
	if separator == "" {
		separator = ","
	}

	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			for record := range input {
				// Materialize the source field if it's an iter.Seq
				sourceValue, exists := Get[any](record, sourceField)
				if exists && isIterSeq(sourceValue) {
					values := materializeSequence(sourceValue)
					var stringValues []string
					for _, val := range values {
						stringValues = append(stringValues, fmt.Sprintf("%v", val))
					}
					result := SetImmutable(record, targetField, strings.Join(stringValues, separator))
					if !yield(result) {
						return
					}
				} else if exists {
					// Source field exists but isn't a sequence - convert to string
					result := SetImmutable(record, targetField, fmt.Sprintf("%v", sourceValue))
					if !yield(result) {
						return
					}
				} else {
					// If source field doesn't exist, pass through unchanged
					if !yield(record) {
						return
					}
				}
			}
		}
	}
}

// MaterializeJSON converts complex fields (iter.Seq, Record, etc.) to JSON strings for grouping.
// This provides better type preservation and handles any JSON-serializable data structure.
// Unlike Materialize(), this works with Records, nested structures, and preserves type information.
func MaterializeJSON(sourceField, targetField string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			for record := range input {
				// Materialize the source field if it exists
				sourceValue, exists := Get[any](record, sourceField)
				var result Record
				if exists {
					// Convert any complex field to JSON representation
					jsonValue := convertToJSONValue(sourceValue)
					if jsonBytes, err := json.Marshal(jsonValue); err == nil {
						result = SetImmutable(record, targetField, JSONString(jsonBytes))
					} else {
						// Fallback to string representation if JSON fails
						result = SetImmutable(record, targetField, fmt.Sprintf("%v", sourceValue))
					}
				} else {
					// If source field doesn't exist, pass through unchanged
					result = record
				}

				if !yield(result) {
					return
				}
			}
		}
	}
}

// Hash creates a SHA256 hash of a string field for efficient grouping.
// Useful for grouping on long strings or when you need fixed-length grouping keys.
// The hash is hex-encoded (64 characters) for readability and compatibility.
//
// Example: {"url": "https://example.com/very/long/path"} → {"url_hash": "a3f2c8b1..."}
//
// Use cases:
//   - Group by long text fields without massive memory overhead
//   - Create stable, fixed-length keys for any string value
//   - Avoid separator ambiguity issues (unlike Materialize with commas)
//
// Note: This is a one-way operation - you cannot reconstruct the original value from the hash.
func Hash(sourceField, targetField string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			for record := range input {
				// Hash the source field if it exists
				sourceValue, exists := Get[any](record, sourceField)
				var result Record
				if exists {
					// Convert value to string
					var strValue string
					if str, ok := sourceValue.(string); ok {
						strValue = str
					} else {
						// Convert other types to string representation
						strValue = fmt.Sprintf("%v", sourceValue)
					}

					// Compute SHA256 hash
					hash := sha256.Sum256([]byte(strValue))
					// Encode as hex string (64 characters, human-readable)
					result = SetImmutable(record, targetField, hex.EncodeToString(hash[:]))
				} else {
					// If source field doesn't exist, pass through unchanged
					result = record
				}

				if !yield(result) {
					return
				}
			}
		}
	}
}

// ============================================================================
// RECORD FLATTENING OPERATIONS
// ============================================================================

// DotFlatten flattens nested records using dot product flattening (single output per input).
// Nested records become prefixed fields: {"user": {"name": "Alice"}} → {"user.name": "Alice"}
// iter.Seq fields are expanded using dot product (linear, one-to-one mapping).
// When sequences have different lengths, uses minimum length and discards excess elements.
// Example with sequences: {"id": 1, "tags": iter.Seq["a", "b"], "scores": iter.Seq[10, 20]} →
//
//	[{"id": 1, "tags": "a", "scores": 10}, {"id": 1, "tags": "b", "scores": 20}]
//
// Example with different lengths: {"short": iter.Seq["a", "b"], "long": iter.Seq[1, 2, 3, 4]} →
//
//	[{"short": "a", "long": 1}, {"short": "b", "long": 2}] (elements 3, 4 discarded)
func DotFlatten(separator string, fields ...string) Filter[Record, Record] {
	if separator == "" {
		separator = "."
	}

	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			for record := range input {
				// Expand the record (handling both nested records and sequences)
				expandedRecords := dotFlattenRecordWithSeqs(record, "", separator, fields...)

				// Yield all expanded records
				for _, expanded := range expandedRecords {
					if !yield(expanded) {
						return
					}
				}
			}
		}
	}
}

// CrossFlatten expands iter.Seq fields using cross product (cartesian product) expansion.
// Creates multiple output records from each input record containing sequence fields.
// Example: {"id": 1, "tags": iter.Seq["a", "b"]} → [{"id": 1, "tags": "a"}, {"id": 1, "tags": "b"}]
func CrossFlatten(separator string, fields ...string) Filter[Record, Record] {
	if separator == "" {
		separator = "."
	}

	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			for record := range input {
				// Use field-specific flatten algorithm
				flattened := crossFlattenRecord(record, separator, fields...)

				// Yield all flattened records
				for _, expanded := range flattened {
					if !yield(expanded) {
						return
					}
				}
			}
		}
	}
}

// dotFlattenRecord recursively flattens a record using dot notation
// If fields are specified, only flattens those top-level fields
func dotFlattenRecord(record Record, prefix, separator string, fields ...string) Record {
	result := MakeMutableRecord()

	// Create a set of fields to flatten for quick lookup
	fieldsToFlatten := make(map[string]bool)
	if len(fields) > 0 {
		for _, field := range fields {
			fieldsToFlatten[field] = true
		}
	}

	for key, value := range record.All() {
		newKey := key
		if prefix != "" {
			newKey = prefix + separator + key
		}

		// Check if this field should be flattened (only applies to top-level fields)
		shouldFlatten := len(fields) == 0 || prefix != "" || fieldsToFlatten[key]

		// If the value is a nested record, flatten it recursively
		if nestedRecord, ok := value.(Record); ok && shouldFlatten {
			flattened := dotFlattenRecord(nestedRecord, newKey, separator)
			// Copy all fields from flattened record to result
			for k, v := range flattened.All() {
				result.fields[k] = v
			}
		} else {
			// For non-record values (including sequences), or fields not to be flattened, keep as-is
			result.fields[newKey] = value
		}
	}

	return result.Freeze()
}

// dotFlattenRecordWithSeqs flattens a record using dot product expansion for sequences
// Returns multiple records when sequences are present (dot product expansion)
// Uses minimum length when sequences have different lengths, discarding excess elements
func dotFlattenRecordWithSeqs(record Record, prefix, separator string, fields ...string) []Record {
	// Create a set of fields to flatten for quick lookup
	fieldsToFlatten := make(map[string]bool)
	if len(fields) > 0 {
		for _, field := range fields {
			fieldsToFlatten[field] = true
		}
	}

	// Collect all sequence fields that should be expanded
	var seqFields []string
	var seqValues [][]any
	nonSeqRecord := MakeMutableRecord()

	for key, value := range record.All() {
		newKey := key
		if prefix != "" {
			newKey = prefix + separator + key
		}

		// Check if this field should be flattened (only applies to top-level fields)
		shouldFlatten := len(fields) == 0 || prefix != "" || fieldsToFlatten[key]

		// If the value is a nested record, flatten it recursively
		if nestedRecord, ok := value.(Record); ok && shouldFlatten {
			flattened := dotFlattenRecord(nestedRecord, newKey, separator)
			// Copy all fields from flattened record
			for k, v := range flattened.All() {
				nonSeqRecord.fields[k] = v
			}
		} else if shouldFlatten && isIterSeq(value) {
			// This is an iter.Seq field - collect its values for dot product expansion
			values := materializeSequence(value)
			if len(values) > 0 {
				seqFields = append(seqFields, newKey)
				seqValues = append(seqValues, values)
			}
		} else {
			// For non-record, non-sequence values, or fields not to be flattened, keep as-is
			nonSeqRecord.fields[newKey] = value
		}
	}

	// If no sequence fields, return single record
	if len(seqFields) == 0 {
		return []Record{nonSeqRecord.Freeze()}
	}

	// Determine the length for dot product (use minimum length of all sequences)
	minLen := len(seqValues[0])
	for _, values := range seqValues[1:] {
		if len(values) < minLen {
			minLen = len(values)
		}
	}

	// Create dot product expansion - pair corresponding elements from each sequence
	var results []Record
	for i := range minLen {
		result := MakeMutableRecord()

		// Copy non-sequence fields from nonSeqRecord
		for k, v := range nonSeqRecord.fields {
			result.fields[k] = v
		}

		// Add corresponding element from each sequence
		for j, fieldName := range seqFields {
			result.fields[fieldName] = seqValues[j][i]
		}

		results = append(results, result.Freeze())
	}

	return results
}

// crossFlattenRecord expands specified sequence fields using cartesian product
// If no fields specified, expands all sequence fields
func crossFlattenRecord(r Record, _ string, fields ...string) []Record {
	var columns [][]Record
	var nonSeqFields []string

	// Create a set of fields to expand for quick lookup
	fieldsToExpand := make(map[string]bool)
	if len(fields) > 0 {
		for _, field := range fields {
			fieldsToExpand[field] = true
		}
	}

	for f, value := range r.All() {
		if isIterSeq(value) {
			// Check if this field should be expanded
			shouldExpand := len(fields) == 0 || fieldsToExpand[f]

			if shouldExpand {
				values := materializeSequence(value)
				var rs []Record
				for _, val := range values {
					// Create a record with this sequence value
					rs = append(rs, NewRecord(map[string]any{f: val}))
				}
				if len(rs) > 0 {
					columns = append(columns, rs)
				}
			} else {
				// Keep sequence field as-is (don't expand)
				nonSeqFields = append(nonSeqFields, f)
			}
		} else {
			// Non-sequence field
			nonSeqFields = append(nonSeqFields, f)
		}
	}

	// If no sequence fields to expand, return original record
	if len(columns) == 0 {
		return []Record{r}
	}

	// Create cartesian product of expanded fields
	crs := cartesianProduct(columns)

	// Add non-sequence fields to each result record - need to rebuild records
	var results []Record
	for _, cr := range crs {
		mut := cr.ToMutable()
		for _, f := range nonSeqFields {
			if val, ok := Get[any](r, f); ok {
				mut.fields[f] = val
			}
		}
		results = append(results, mut.Freeze())
	}

	return results
}

// cartesianProduct performs cartesian product of record slices
func cartesianProduct(columns [][]Record) []Record {
	if len(columns) == 0 {
		return nil
	}
	if len(columns) == 1 {
		return columns[0]
	}
	var rs []Record
	for _, lr := range cartesianProduct(columns[1:]) {
		for _, rr := range columns[0] {
			r := MakeMutableRecord()
			// Copy fields from both records
			for k, v := range rr.All() {
				r.fields[k] = v
			}
			for k, v := range lr.All() {
				r.fields[k] = v
			}
			rs = append(rs, r.Freeze())
		}
	}
	return rs
}

// isIterSeq checks if a value is an iter.Seq type using reflection
func isIterSeq(value any) bool {
	if value == nil {
		return false
	}
	// Check for common iter.Seq types in Value constraint
	switch value.(type) {
	case iter.Seq[int], iter.Seq[int8], iter.Seq[int16], iter.Seq[int32], iter.Seq[int64]:
		return true
	case iter.Seq[uint], iter.Seq[uint8], iter.Seq[uint16], iter.Seq[uint32], iter.Seq[uint64]:
		return true
	case iter.Seq[float32], iter.Seq[float64]:
		return true
	case iter.Seq[bool], iter.Seq[string], iter.Seq[time.Time]:
		return true
	case iter.Seq[Record]:
		return true
	default:
		return false
	}
}

// isSimpleValue checks if a value is a simple type suitable for grouping
// Only canonical scalar types allowed
func isSimpleValue(value any) bool {
	if value == nil {
		return true // nil is simple
	}
	switch value.(type) {
	// Canonical scalar types only
	case int64, float64:
		return true
	// Other basic types
	case bool, string, time.Time:
		return true
	case JSONString:
		return true
	// Complex types not allowed for grouping
	case Record:
		return false
	default:
		// Check if it's an iter.Seq (not allowed)
		return !isIterSeq(value)
	}
}

// convertToJSONValue converts any value to a JSON-serializable representation
func convertToJSONValue(value any) any {
	switch v := value.(type) {
	case Record:
		// Convert nested Records recursively
		jsonRecord := make(map[string]any)
		for key, val := range v.All() {
			jsonRecord[key] = convertToJSONValue(val)
		}
		return jsonRecord
	default:
		if isIterSeq(value) {
			// Convert iter.Seq to array
			return materializeSequence(value)
		}
		// Return other values as-is (primitives, already JSON-serializable)
		return value
	}
}

// materializeSequence converts an iter.Seq to a slice of any
func materializeSequence(value any) []any {
	var result []any

	switch seq := value.(type) {
	case iter.Seq[int]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[int8]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[int16]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[int32]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[int64]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[uint]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[uint8]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[uint16]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[uint32]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[uint64]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[float32]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[float64]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[bool]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[string]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[time.Time]:
		for v := range seq {
			result = append(result, v)
		}
	case iter.Seq[Record]:
		for v := range seq {
			result = append(result, v)
		}
	}

	return result
}
