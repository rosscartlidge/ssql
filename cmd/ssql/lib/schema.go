package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/rosscartlidge/ssql/v4"
)

// Schema type constants (stored in JSON)
const (
	TypeString = "string"
	TypeInt    = "int"
	TypeFloat  = "float"
	TypeBool   = "bool"
	TypeJSON   = "json" // arrays, nested objects
)

// Schema represents the structure of JSONL records with ordered fields and types.
type Schema struct {
	Fields     []string          `json:"fields"`                // Ordered field names
	Types      map[string]string `json:"types"`                 // Field -> type string
	SampleRate int               `json:"sample_rate,omitempty"` // Audio sample rate (0 if not audio)
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

// HasField returns true if the schema contains the field.
func (s *Schema) HasField(name string) bool {
	_, ok := s.Types[name]
	return ok
}

// Clone creates a deep copy of the schema.
func (s *Schema) Clone() *Schema {
	clone := NewSchema()
	clone.Fields = slices.Clone(s.Fields)
	for k, v := range s.Types {
		clone.Types[k] = v
	}
	return clone
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
}

// WriteHeader writes the schema as a JSONL header line.
func (s *Schema) WriteHeader(w io.Writer) error {
	header := schemaHeader{
		Schema: schemaContent{
			Fields:     s.Fields,
			Types:      s.Types,
			SampleRate: s.SampleRate,
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
	switch v.(type) {
	case int64:
		return TypeInt
	case float64:
		return TypeFloat
	case bool:
		return TypeBool
	case string:
		return TypeString
	case []any, ssql.Record, map[string]any:
		return TypeJSON
	default:
		return TypeString
	}
}

// FieldTypeToSchemaType converts ssql.FieldType to schema type string.
func FieldTypeToSchemaType(ft ssql.FieldType) string {
	switch ft {
	case ssql.FieldTypeInt:
		return TypeInt
	case ssql.FieldTypeFloat:
		return TypeFloat
	case ssql.FieldTypeBool:
		return TypeBool
	case ssql.FieldTypeString:
		return TypeString
	default:
		return TypeString
	}
}

// SchemaTypeToFieldType converts schema type string to ssql.FieldType.
func SchemaTypeToFieldType(typ string) ssql.FieldType {
	switch typ {
	case TypeInt:
		return ssql.FieldTypeInt
	case TypeFloat:
		return ssql.FieldTypeFloat
	case TypeBool:
		return ssql.FieldTypeBool
	case TypeString:
		return ssql.FieldTypeString
	case TypeJSON:
		return ssql.FieldTypeAuto
	default:
		return ssql.FieldTypeString
	}
}
