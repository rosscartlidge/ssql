// Self-contained data types for the WASM module.
// No dependency on ssql/v4 — enables TinyGo builds.
package main

// rawJSON is a string containing raw JSON for nested objects/arrays.
// Written verbatim during serialization instead of being parsed.
type rawJSON string

// schema tracks field names and their positions for fast lookup.
type schema struct {
	fields []string
	index  map[string]int
}

// newSchema creates a schema from an ordered list of field names.
func newSchema(fields []string) *schema {
	idx := make(map[string]int, len(fields))
	for i, f := range fields {
		idx[f] = i
	}
	return &schema{fields: fields, index: idx}
}

// record is an immutable row with a shared schema.
type record struct {
	s      *schema
	values []any
}

// get returns the value of a field (or nil, false if missing).
func (r record) get(field string) (any, bool) {
	if i, ok := r.s.index[field]; ok {
		return r.values[i], r.values[i] != nil
	}
	return nil, false
}

// mustGet returns the value of a field or nil if missing.
func (r record) mustGet(field string) any {
	if i, ok := r.s.index[field]; ok {
		return r.values[i]
	}
	return nil
}

// dataset is a collection of records sharing a schema.
type dataset struct {
	s    *schema
	rows []record
}
