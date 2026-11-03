// Package pqcols maps the top-level fields of a Parquet file's Arrow
// schema to the leaf columns pqarrow reads, and names the columns whose
// Parquet logical type changes how a value reads (UUID). The record and
// typed readers share it.
//
// pqarrow's ReadRowGroups and GetRecordReader take LEAF column indices:
// a struct, list or map field is one Arrow field but several leaves, so
// handing them field indices reads the wrong columns past the first
// nested one, without an error (DFC162 §1).
package pqcols

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/parquet/file"
	"github.com/apache/arrow/go/v18/parquet/pqarrow"
	"github.com/apache/arrow/go/v18/parquet/schema"
)

// isLeaf is a field with no children. pqarrow's own IsLeaf tests
// ColIndex != -1, but a group field's ColIndex is 0 in its manifest,
// so every struct, list and map would pass as leaf 0.
func isLeaf(f *pqarrow.SchemaField) bool { return len(f.Children) == 0 }

// Leaves returns the leaf column indices under the given top-level
// fields, field by field in the order given.
func Leaves(m *pqarrow.SchemaManifest, fields []int) []int {
	var leaves []int
	var walk func(f *pqarrow.SchemaField)
	walk = func(f *pqarrow.SchemaField) {
		if isLeaf(f) {
			leaves = append(leaves, f.ColIndex)
			return
		}
		for i := range f.Children {
			walk(&f.Children[i])
		}
	}
	for _, i := range fields {
		walk(&m.Fields[i])
	}
	return leaves
}

// UUIDFields reports, per top-level field, whether it is a Parquet UUID
// column: FIXED_LEN_BYTE_ARRAY(16) with the UUID logical type, which
// pqarrow hands over as plain 16-byte binary.
func UUIDFields(pf *file.Reader, m *pqarrow.SchemaManifest) []bool {
	descr := pf.MetaData().Schema
	out := make([]bool, len(m.Fields))
	for i := range m.Fields {
		if f := &m.Fields[i]; isLeaf(f) {
			_, out[i] = descr.Column(f.ColIndex).LogicalType().(schema.UUIDLogicalType)
		}
	}
	return out
}

// UUIDString renders 16 bytes as the canonical lowercase 8-4-4-4-12 text.
func UUIDString(b []byte) string {
	var s [36]byte
	hex.Encode(s[0:8], b[0:4])
	hex.Encode(s[9:13], b[4:6])
	hex.Encode(s[14:18], b[6:8])
	hex.Encode(s[19:23], b[8:10])
	hex.Encode(s[24:36], b[10:16])
	s[8], s[13], s[18], s[23] = '-', '-', '-', '-'
	return string(s[:])
}

// Nested reports a struct, list or map type: a value the readers hand
// over as its JSON text, the `json` wire type (DFC144 Level 0).
func Nested(t arrow.DataType) bool {
	switch t.ID() {
	case arrow.STRUCT, arrow.LIST, arrow.LARGE_LIST, arrow.FIXED_SIZE_LIST, arrow.MAP:
		return true
	}
	return false
}

// JSON renders a nested value (struct, list, map) as JSON text: a struct
// an object in field order, a list an array, a map an object keyed by
// its keys' text — DuckDB's to_json of a MAP, where Arrow's own ValueStr
// gives a list of {"key","value"} pairs. Scalars inside are marshalled
// as Arrow hands them over.
func JSON(arr arrow.Array, i int) string {
	return string(appendJSON(nil, arr, i))
}

func appendJSON(b []byte, arr arrow.Array, i int) []byte {
	if arr.IsNull(i) {
		return append(b, "null"...)
	}
	switch a := arr.(type) {
	case *array.Struct:
		st := a.DataType().(*arrow.StructType)
		b = append(b, '{')
		for f := 0; f < a.NumField(); f++ {
			if f > 0 {
				b = append(b, ',')
			}
			b = appendMarshal(b, st.Field(f).Name)
			b = append(b, ':')
			b = appendJSON(b, a.Field(f), i)
		}
		return append(b, '}')
	case *array.Map:
		start, end := a.ValueOffsets(i)
		keys, items := a.Keys(), a.Items()
		b = append(b, '{')
		for j := start; j < end; j++ {
			if j > start {
				b = append(b, ',')
			}
			if s, ok := keys.(*array.String); ok {
				b = appendMarshal(b, s.Value(int(j)))
			} else {
				b = appendMarshal(b, keys.ValueStr(int(j)))
			}
			b = append(b, ':')
			b = appendJSON(b, items, int(j))
		}
		return append(b, '}')
	case array.ListLike:
		start, end := a.ValueOffsets(i)
		vals := a.ListValues()
		b = append(b, '[')
		for j := start; j < end; j++ {
			if j > start {
				b = append(b, ',')
			}
			b = appendJSON(b, vals, int(j))
		}
		return append(b, ']')
	case *array.Float64:
		return appendFloat(b, a.Value(i), 64)
	case *array.Float32:
		return appendFloat(b, float64(a.Value(i)), 32)
	case *array.Decimal128:
		// The digits at the column's scale, a bare number (arrow-go quotes it).
		return append(b, a.Value(i).ToString(a.DataType().(*arrow.Decimal128Type).Scale)...)
	}
	return appendMarshal(b, arr.GetOneForMarshal(i))
}

// appendFloat writes a JSON number; arrow-go marshals a float as a
// string. A non-finite value is null: absent, as everywhere in ssql.
func appendFloat(b []byte, f float64, bits int) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(b, "null"...)
	}
	return strconv.AppendFloat(b, f, 'g', -1, bits)
}

func appendMarshal(b []byte, v any) []byte {
	j, err := json.Marshal(v)
	if err != nil {
		return append(b, "null"...)
	}
	return append(b, j...)
}
