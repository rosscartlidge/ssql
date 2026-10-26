package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

// Schema type constants (stored in JSON)
const (
	TypeString = "string"
	TypeInt    = "int"
	TypeFloat  = "float"
	TypeBool   = "bool"
	TypeJSON   = "json"   // arrays, nested objects
	TypeTime   = "time"   // time.Time, RFC 3339 on the wire (DFC128 D1)
	TypeInt128 = "int128" // ssql.Int128, a bare number on the wire; DuckDB's HUGEINT (DFC155 C)

	// TypeDecimal is the BASE of the `decimal:S` wire type — ssql.Decimal,
	// a bare number with S digits after the point; DECIMAL(38,S) (DFC155
	// E). The scale is part of the name: build one with WireDecimal, read
	// one with ParseWireType, never compare the string whole.
	TypeDecimal = "decimal"
)

// WireDecimal is the wire type name of a decimal column of the scale.
func WireDecimal(scale int) string { return ssql.DecimalType(scale).String() }

// ParseWireType splits a wire type name into base and scale:
// ("decimal", 2) for "decimal:2", (name, 0) otherwise. ssql.ParseWireType.
func ParseWireType(s string) (base string, scale int) { return ssql.ParseWireType(s) }

// Schema represents the structure of JSONL records with ordered fields and types.
type Schema struct {
	Fields     []string          `json:"fields"`                // Ordered field names
	Types      map[string]string `json:"types"`                 // Field -> type string
	SampleRate int               `json:"sample_rate,omitempty"` // Audio sample rate (0 if not audio)

	// TZ is the pipeline's time zone (IANA name), chosen once by the
	// source command and carried in the header so every later command
	// reads the calendar in it (DFC147 §5.7). Empty: the reader resolves
	// for itself (an older stream).
	TZ string `json:"tz,omitempty"`
}

// NewSchema creates an empty schema.
func NewSchema() *Schema {
	return &Schema{
		Fields: []string{},
		Types:  make(map[string]string),
	}
}

// AddField adds a field with its type. If the field already exists,
// only the type is updated; otherwise the field is appended to the order.
func (s *Schema) AddField(name, typ string) {
	if !s.HasField(name) {
		s.Fields = append(s.Fields, name)
	}
	s.Types[name] = typ
}

// RemoveField removes a field from the schema.
func (s *Schema) RemoveField(name string) {
	delete(s.Types, name)
	s.Fields = slices.DeleteFunc(s.Fields, func(f string) bool { return f == name })
}

// RenameField renames a field, preserving its position and type.
func (s *Schema) RenameField(oldName, newName string) {
	if typ, ok := s.Types[oldName]; ok {
		delete(s.Types, oldName)
		s.Types[newName] = typ
		for i, f := range s.Fields {
			if f == oldName {
				s.Fields[i] = newName
				break
			}
		}
	}
}

// SetType updates a field's type. If the field doesn't exist, it is added.
func (s *Schema) SetType(name, typ string) {
	if !s.HasField(name) {
		s.Fields = append(s.Fields, name)
	}
	s.Types[name] = typ
}

// TypeOf returns the type of a field, or empty string if not found.
func (s *Schema) TypeOf(name string) string {
	return s.Types[name]
}

// HasField returns true if the schema contains the field — typed, or
// listed in Fields without a type (unpivot leaves a mixed-type value
// column untyped; until 2026-10-09 the next stage then refused the
// column as unknown while listing it as available).
func (s *Schema) HasField(name string) bool {
	if _, ok := s.Types[name]; ok {
		return true
	}
	return slices.Contains(s.Fields, name)
}

// HasFieldOrPath is HasField, or true for a dotted path (addr.city,
// tags.0) whose longest leading segment is a field holding a nested
// value — type `json`, or an untyped (`any`/unknown) column that may be
// one (DFC144 Level 2). Validation uses it; a literal dotted field is
// still found by HasField first.
func (s *Schema) HasFieldOrPath(name string) bool {
	if s.HasField(name) {
		return true
	}
	for i := strings.LastIndexByte(name, '.'); i > 0; i = strings.LastIndexByte(name[:i], '.') {
		if t, ok := s.Types[name[:i]]; ok {
			return t == TypeJSON || t == "any" || t == ""
		}
	}
	return false
}

// Clone creates a deep copy of the schema, stream properties included.
func (s *Schema) Clone() *Schema {
	clone := s.Derive(nil, nil)
	clone.Fields = slices.Clone(s.Fields)
	maps.Copy(clone.Types, s.Types)
	return clone
}

// Derive is a new schema with the given fields and types that KEEPS the
// stream-level properties of s (the zone, the audio sample rate). Every
// command that rebuilds its output schema from its input's must derive
// it this way rather than from a struct literal, or the properties are
// lost at that stage (SampleRate was, before DFC147). s may be nil.
func (s *Schema) Derive(fields []string, types map[string]string) *Schema {
	out := NewSchema()
	if fields != nil {
		out.Fields = fields
	}
	if types != nil {
		out.Types = types
	}
	if s != nil {
		out.SampleRate = s.SampleRate
		out.TZ = s.TZ
	}
	return out
}

// Len returns the number of fields in the schema.
func (s *Schema) Len() int {
	return len(s.Fields)
}

// schemaHeader is the JSON structure for the schema header line.
type schemaHeader struct {
	Schema schemaContent `json:"_schema"`
}

type schemaContent struct {
	Fields     []string          `json:"fields"`
	Types      map[string]string `json:"types"`
	SampleRate int               `json:"sample_rate,omitempty"` // Audio sample rate (0 if not audio)
	TZ         string            `json:"tz,omitempty"`          // the pipeline's zone (DFC147)
}

// WriteHeader writes the schema as a JSONL header line. The zone is the
// schema's, else the process's (the one adopted from the input header, or
// resolved from the environment once): every stream a command writes
// names the pipeline's zone, whichever path built its schema (DFC147
// §5.7).
func (s *Schema) WriteHeader(w io.Writer) error {
	tz := s.TZ
	if tz == "" {
		tz = ssql.StreamZoneName()
	}
	header := schemaHeader{
		Schema: schemaContent{
			Fields:     s.Fields,
			Types:      s.Types,
			SampleRate: s.SampleRate,
			TZ:         tz,
		},
	}
	data, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

// MarshalJSON implements json.Marshaler for Schema.
func (s *Schema) MarshalJSON() ([]byte, error) {
	return json.Marshal(schemaContent{
		Fields:     s.Fields,
		Types:      s.Types,
		SampleRate: s.SampleRate,
		TZ:         s.TZ,
	})
}

// String returns the schema as a JSON string (for debugging).
func (s *Schema) String() string {
	var buf bytes.Buffer
	s.WriteHeader(&buf)
	return buf.String()
}

// ParseSchemaHeader checks if a parsed JSON object is a schema header.
// Returns the schema and true if it is, nil and false otherwise.
func ParseSchemaHeader(data map[string]any) (*Schema, bool) {
	schemaData, ok := data["_schema"]
	if !ok {
		return nil, false
	}

	schemaMap, ok := schemaData.(map[string]any)
	if !ok {
		return nil, false
	}

	schema := NewSchema()

	// Extract fields array
	if fields, ok := schemaMap["fields"].([]any); ok {
		for _, f := range fields {
			if name, ok := f.(string); ok {
				schema.Fields = append(schema.Fields, name)
			}
		}
	}

	// Extract types map
	if types, ok := schemaMap["types"].(map[string]any); ok {
		for k, v := range types {
			if typ, ok := v.(string); ok {
				schema.Types[k] = typ
			}
		}
	}

	// Extract sample_rate (for audio data)
	if sampleRate, ok := schemaMap["sample_rate"].(float64); ok {
		schema.SampleRate = int(sampleRate)
	}
	if tz, ok := schemaMap["tz"].(string); ok {
		schema.TZ = tz
	}

	return schema, true
}

// ParseSchemaHeaderFromBytes parses a schema header from JSON bytes.
// Returns nil, false if the bytes don't represent a schema header.
func ParseSchemaHeaderFromBytes(data []byte) (*Schema, bool) {
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, false
	}
	return ParseSchemaHeader(parsed)
}

// InferFromRecord creates a schema from a Record's fields and types.
// Note: Field order is non-deterministic since Go maps are unordered.
func InferFromRecord(record ssql.Record) *Schema {
	schema := NewSchema()
	for k, v := range record.All() {
		schema.AddField(k, InferTypeString(v))
	}
	return schema
}

// InferFromSample creates a schema from the union of a sample's fields,
// in first-seen order. Headerless sources describe each record only by
// itself, and a JSON null arrives as an ABSENT field, so one record is
// not enough: a column that is NULL in the first row must still be in
// the header, which is authoritative downstream (DFC128 D3). A field's
// type is the type of its values across the sample: int and float widen
// to float; any other disagreement is a string.
//
// A nil slot (a JSON null) names the field without typing it: the type
// comes from the field's values, and a column that is NULL in every
// sampled record is a string column — the same call the CSV reader makes
// for a column with no non-empty sample. Either way the column keeps its
// name and its place.
func InferFromSample(records []ssql.Record) *Schema {
	schema := NewSchema()
	typed := map[string]bool{}
	for _, record := range records {
		for k, v := range record.All() {
			if v == nil {
				if !schema.HasField(k) {
					schema.AddField(k, TypeString) // placeholder until a value types it
				}
				continue
			}
			t := InferTypeString(v)
			if typed[k] {
				t = widenWireType(schema.Types[k], t)
			}
			typed[k] = true
			schema.AddField(k, t)
		}
	}
	return schema
}

// widenWireType merges two observed wire types for one field.
func widenWireType(a, b string) string {
	if ab, as := ParseWireType(a); ab == TypeDecimal {
		return widenDecimalWire(as, b)
	} else if bb, bs := ParseWireType(b); bb == TypeDecimal {
		return widenDecimalWire(bs, a)
	}
	switch {
	case a == b:
		return a
	case (a == TypeInt && b == TypeFloat) || (a == TypeFloat && b == TypeInt):
		return TypeFloat
	case (a == TypeInt && b == TypeInt128) || (a == TypeInt128 && b == TypeInt):
		return TypeInt128 // int beside int128 widens to int128 (DFC155 C)
	case (a == TypeInt128 && b == TypeFloat) || (a == TypeFloat && b == TypeInt128):
		return TypeFloat
	}
	return TypeString
}

// widenDecimalWire merges a decimal of the scale with another wire type:
// another decimal at the larger scale, an integer of either width into
// the decimal, a float into a float (DFC155 E).
func widenDecimalWire(scale int, other string) string {
	switch ob, os := ParseWireType(other); ob {
	case TypeDecimal:
		return WireDecimal(max(scale, os))
	case TypeInt, TypeInt128:
		return WireDecimal(scale)
	case TypeFloat:
		return TypeFloat
	}
	return TypeString
}

// InferFromRecordOrdered creates a schema from a Record using the provided field order.
// Fields not in the order list are appended at the end.
func InferFromRecordOrdered(record ssql.Record, fieldOrder []string) *Schema {
	schema := NewSchema()

	// Add fields in specified order first
	for _, field := range fieldOrder {
		if v, ok := ssql.Get[any](record, field); ok {
			schema.AddField(field, InferTypeString(v))
		}
	}

	// Add any remaining fields (in iteration order, which is random)
	for k, v := range record.All() {
		if !schema.HasField(k) {
			schema.AddField(k, InferTypeString(v))
		}
	}

	return schema
}

// InferTypeString returns the schema type string for a value.
func InferTypeString(v any) string {
	switch x := v.(type) {
	case int64:
		return TypeInt
	case ssql.Int128:
		return TypeInt128
	case ssql.Decimal:
		return WireDecimal(x.Scale())
	case float64:
		return TypeFloat
	case bool:
		return TypeBool
	case string:
		return TypeString
	case time.Time:
		return TypeTime
	case ssql.JSONString, []any, ssql.Record, map[string]any:
		return TypeJSON
	default:
		return TypeString
	}
}

// FieldTypeToSchemaType converts ssql.FieldType to schema type string.
func FieldTypeToSchemaType(ft ssql.FieldType) string {
	if ft.IsDecimal() {
		return ft.String()
	}
	switch ft {
	case ssql.FieldTypeInt:
		return TypeInt
	case ssql.FieldTypeFloat:
		return TypeFloat
	case ssql.FieldTypeBool:
		return TypeBool
	case ssql.FieldTypeString:
		return TypeString
	case ssql.FieldTypeTime:
		return TypeTime
	case ssql.FieldTypeJSON:
		return TypeJSON
	case ssql.FieldTypeInt128:
		return TypeInt128
	default:
		return TypeString
	}
}

// SchemaTypeToFieldType converts schema type string to ssql.FieldType.
func SchemaTypeToFieldType(typ string) ssql.FieldType {
	if base, scale := ParseWireType(typ); base == TypeDecimal && scale >= 0 {
		return ssql.DecimalType(scale)
	}
	switch typ {
	case TypeInt:
		return ssql.FieldTypeInt
	case TypeFloat:
		return ssql.FieldTypeFloat
	case TypeBool:
		return ssql.FieldTypeBool
	case TypeString:
		return ssql.FieldTypeString
	case TypeTime:
		return ssql.FieldTypeTime
	case TypeJSON:
		return ssql.FieldTypeJSON
	case TypeInt128:
		return ssql.FieldTypeInt128
	default:
		return ssql.FieldTypeString
	}
}

// NestedPath is one completable path into a nested object: its dotted
// name (addr.city) and the wire type of the value there.
type NestedPath struct {
	Name string
	Type string
}

// NestedPathDepth bounds how far NestedPaths walks into an object:
// addr.city is one level, addr.geo.lat two. Deep documents (an API
// export with a config blob) would otherwise flood a completion list.
const NestedPathDepth = 3

// NestedPaths lists an object's keys as paths under field, in the
// object's own key order, typed from their values (a nested value is
// `json`), and walks into nested objects to NestedPathDepth so
// addr.geo.lat is offered as well as addr.geo. Lists are not walked (an
// index is typed by hand). Schema mode emits them so Ctrl-O completes
// addr.city after `from events.jsonl |` (DFC144 Level 2).
func NestedPaths(field string, js ssql.JSONString) []NestedPath {
	return nestedPaths(field, js, NestedPathDepth)
}

func nestedPaths(field string, js ssql.JSONString, depth int) []NestedPath {
	if depth == 0 {
		return nil
	}
	keys, err := jsonObjectKeys(string(js))
	if err != nil {
		return nil
	}
	// Raw children keep their own text, so a nested object's keys come
	// out in document order (re-marshalling a map would sort them).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		return nil
	}
	out := make([]NestedPath, 0, len(keys))
	for _, k := range keys {
		child := ssql.JSONString(raw[k])
		t := "any"
		if v := ssql.ExprValue(child); v != nil {
			if nested, isNested := ssql.NestedValue(v); isNested {
				t = InferTypeString(nested)
			} else {
				t = InferTypeString(v)
			}
		}
		name := field + "." + k
		out = append(out, NestedPath{Name: name, Type: t})
		if t == TypeJSON {
			out = append(out, nestedPaths(name, child, depth-1)...)
		}
	}
	return out
}

// jsonObjectKeys returns a JSON object's top-level keys in document order.
func jsonObjectKeys(text string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not an object")
	}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("malformed object")
		}
		keys = append(keys, k)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
