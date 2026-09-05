package ssql

import "testing"

func TestParseJSONLineWithSchemaTypesCoercesDeclaredFloat(t *testing.T) {
	schema := NewSchema([]string{"t", "v"})
	types := []FieldType{FieldTypeInt, FieldTypeFloat}
	r, err := ParseJSONLineWithSchemaTypes([]byte(`{"t":3,"v":2}`), schema, types)
	if err != nil {
		t.Fatal(err)
	}
	// Type assertions on the raw value: Get[float64] converts int64 and
	// would pass without the coercion.
	if raw, _ := Get[any](r, "v"); raw != float64(2) {
		t.Errorf("v declared float: want float64(2), got %T %v", raw, raw)
	}
	if raw, _ := Get[any](r, "t"); raw != int64(3) {
		t.Errorf("t declared int: want int64(3), got %T %v", raw, raw)
	}
	// nil types: as parsed (the ParseJSONLineWithSchema contract is unchanged)
	r2, _ := ParseJSONLineWithSchema([]byte(`{"t":3,"v":2}`), schema)
	if raw, _ := Get[any](r2, "v"); raw != int64(2) {
		t.Errorf("without types, 2 parses as int64; got %T", raw)
	}
}
