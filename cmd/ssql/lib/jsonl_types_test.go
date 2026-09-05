package lib

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

// A `_schema` header's types are authoritative for the values that
// follow: JSON writes the float 2.0 as `2`, and without this the next
// stage held int64(2) under a float column — `where -if v gt 0.4`
// then compared it as an int and dropped the row (found by the
// int_first equivalence case, 2026-09-05).
func TestReadJSONLWithSchemaHonoursDeclaredFloat(t *testing.T) {
	in := "{\"_schema\":{\"fields\":[\"t\",\"v\"],\"types\":{\"t\":\"int\",\"v\":\"float\"}}}\n{\"t\":3,\"v\":2}\n{\"t\":1,\"v\":0.5}\n"
	sr := ReadJSONLWithSchema(strings.NewReader(in))
	var got []ssql.Record
	for r := range sr.Records {
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %d", len(got))
	}
	// Raw values + type assertion: ssql.Get[float64] converts int64 and
	// passed with the coercion sabotaged out.
	if raw, _ := ssql.Get[any](got[0], "v"); raw != float64(2) {
		t.Errorf("v under a float header should be float64(2), got %T %v", raw, raw)
	}
	if raw, _ := ssql.Get[any](got[0], "t"); raw != int64(3) {
		t.Errorf("t under an int header should stay int64(3), got %T", raw)
	}
}
