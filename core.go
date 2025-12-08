package ssql

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
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

// Record represents structured data with native Go types.
// This is the primary data structure for working with CSV/JSON data and command output.
// Records use an encapsulated map to enforce type safety and canonical type conventions.
//
// Key features:
//   - Type-safe field access with Get/GetOr
//   - Enforces canonical types (int64, float64, string, bool, time.Time) via setters
//   - Supports nested structures (Record, JSONString)
//   - Supports sequences (iter.Seq[T])
//   - Immutable updates via Record methods (creates copies)
//   - maps-style API (All, Keys, Values) for iteration
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
	fields map[string]any
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

// NewRecord creates a Record from a map (for compatibility)
func NewRecord(fields map[string]any) Record {
	// Copy the map to maintain encapsulation
	m := make(map[string]any, len(fields))
	maps.Copy(m, fields)
	return Record{fields: m}
}

// Freeze converts a MutableRecord to an immutable Record
// Creates a copy to ensure mutations don't leak
func (m MutableRecord) Freeze() Record {
	frozen := make(map[string]any, len(m.fields))
	maps.Copy(frozen, m.fields)
	return Record{fields: frozen}
}

// ToMutable creates a mutable copy of a Record for modification
// This preserves immutability by creating a shallow copy of the original Record
func (r Record) ToMutable() MutableRecord {
	m := make(map[string]any, len(r.fields))
	maps.Copy(m, r.fields)
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
	val, exists := r.fields[field]
	if !exists {
		var zero T
		return zero, false
	}

	// Direct type assertion first (fast path)
	if typed, ok := val.(T); ok {
		return typed, true
	}

	// Smart type conversion (slower path)
	if converted, ok := convertTo[T](val); ok {
		return converted, true
	}

	var zero T
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
	_, exists := r.fields[field]
	return exists
}

// Len returns the number of fields in the record
func (r Record) Len() int {
	return len(r.fields)
}

// Keys returns all field names as a slice
func (r Record) Keys() []string {
	keys := make([]string, 0, len(r.fields))
	for k := range r.fields {
		keys = append(keys, k)
	}
	return keys
}

// ============================================================================
// MAPS-STYLE ITERATOR API
// ============================================================================

// All returns an iterator over key-value pairs (matches maps.All)
func (r Record) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range r.fields {
			if !yield(k, v) {
				return
			}
		}
	}
}

// KeysIter returns an iterator over field names (matches maps.Keys)
func (r Record) KeysIter() iter.Seq[string] {
	return func(yield func(string) bool) {
		for k := range r.fields {
			if !yield(k) {
				return
			}
		}
	}
}

// Values returns an iterator over field values (matches maps.Values)
func (r Record) Values() iter.Seq[any] {
	return func(yield func(any) bool) {
		for _, v := range r.fields {
			if !yield(v) {
				return
			}
		}
	}
}

// Clone creates a shallow copy of the record (matches maps.Clone)
func (r Record) Clone() Record {
	cloned := make(map[string]any, len(r.fields))
	maps.Copy(cloned, r.fields)
	return Record{fields: cloned}
}

// Equal checks if two records have the same fields and values (matches maps.Equal)
func (r Record) Equal(other Record) bool {
	return maps.Equal(r.fields, other.fields)
}

// ============================================================================
// JSON MARSHALING
// ============================================================================

// MarshalJSON implements json.Marshaler
// Records marshal as {"name": "Alice", "age": 30} not {"fields": {...}}
func (r Record) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.fields)
}

// UnmarshalJSON implements json.Unmarshaler
func (r *Record) UnmarshalJSON(data []byte) error {
	fields := make(map[string]any)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	r.fields = fields
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

// SetImmutable adds a field with compile-time type safety - creates new Record (immutable)
func SetImmutable[V Value](r Record, field string, value V) Record {
	result := make(map[string]any, len(r.fields)+1)
	maps.Copy(result, r.fields)
	result[field] = value
	return Record{fields: result}
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
	// Use JSON-like representation for consistent, deterministic keys
	// Sort keys for deterministic output
	keys := r.Keys() // Already returns sorted keys
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		v := r.fields[k]
		fmt.Fprintf(&b, "%q:%v", k, v)
	}
	b.WriteByte('}')
	return b.String()
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
	return Record{fields: map[string]any{key: value}}
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
				result := record.Clone()

				// Materialize the source field if it's an iter.Seq
				if sourceValue, exists := record.fields[sourceField]; exists && isIterSeq(sourceValue) {
					values := materializeSequence(sourceValue)
					var stringValues []string
					for _, val := range values {
						stringValues = append(stringValues, fmt.Sprintf("%v", val))
					}
					result.fields[targetField] = strings.Join(stringValues, separator)
				} else if exists {
					// Source field exists but isn't a sequence - convert to string
					result.fields[targetField] = fmt.Sprintf("%v", sourceValue)
				}
				// If source field doesn't exist, don't add target field

				if !yield(result) {
					return
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
				result := record.Clone()

				// Materialize the source field if it exists
				if sourceValue, exists := record.fields[sourceField]; exists {
					// Convert any complex field to JSON representation
					jsonValue := convertToJSONValue(sourceValue)
					if jsonBytes, err := json.Marshal(jsonValue); err == nil {
						result.fields[targetField] = JSONString(jsonBytes)
					} else {
						// Fallback to string representation if JSON fails
						result.fields[targetField] = fmt.Sprintf("%v", sourceValue)
					}
				}
				// If source field doesn't exist, don't add target field

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
				result := record.Clone()

				// Hash the source field if it exists
				if sourceValue, exists := record.fields[sourceField]; exists {
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
					result.fields[targetField] = hex.EncodeToString(hash[:])
				}
				// If source field doesn't exist, don't add target field

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
			maps.Copy(result.fields, flattened.fields)
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
			maps.Copy(nonSeqRecord.fields, flattened.fields)
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

		// Copy non-sequence fields
		maps.Copy(result.fields, nonSeqRecord.fields)

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
					newRecord := Record{fields: map[string]any{f: val}}
					rs = append(rs, newRecord)
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

	// Add non-sequence fields to each result record
	for _, cr := range crs {
		for _, f := range nonSeqFields {
			cr.fields[f] = r.fields[f]
		}
	}

	return crs
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
			maps.Copy(r.fields, rr.fields)
			maps.Copy(r.fields, lr.fields)
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
