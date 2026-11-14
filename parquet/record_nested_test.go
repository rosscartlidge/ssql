package parquet

import (
	"reflect"
	"slices"
	"testing"

	ssql "github.com/rosscartlidge/ssql/v4"
)

func TestReadParquetNestedAndUUID(t *testing.T) {
	recs, err := Read(nestedUUIDParquet)
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Collect(recs)
	want := []map[string]any{
		{"n": int64(1), "s": ssql.JSONString(`{"x":10,"y":"a"}`), "u": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
			"l": ssql.JSONString(`[1,2]`), "mp": ssql.JSONString(`{"k":1}`), "m": int64(100)},
		{"n": int64(2), "s": ssql.JSONString(`{"x":20,"y":"b"}`), "l": ssql.JSONString(`[]`), "mp": ssql.JSONString(`{}`), "m": int64(200)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i, r := range got {
		row := map[string]any{}
		for k, v := range r.All() {
			if v != nil {
				row[k] = v
			}
		}
		if !reflect.DeepEqual(row, want[i]) {
			t.Errorf("row %d:\n got %#v\nwant %#v", i, row, want[i])
		}
	}

	// A projection that starts past the nested fields reads those columns.
	recs, err = ReadColumns(nestedUUIDParquet, []string{"m", "u", "s"})
	if err != nil {
		t.Fatal(err)
	}
	first := slices.Collect(recs)[0]
	if keys := slices.Collect(first.KeysIter()); !slices.Equal(keys, []string{"m", "u", "s"}) {
		t.Errorf("projected fields %v, want [m u s]", keys)
	}
	if m, u, s := ssql.GetOr(first, "m", int64(0)), ssql.GetOr(first, "u", ""), ssql.GetOr(first, "s", ssql.JSONString("")); m != 100 || u != "6ba7b810-9dad-11d1-80b4-00c04fd430c8" || s != `{"x":10,"y":"a"}` {
		t.Errorf("projected row: m=%v u=%q s=%q", m, u, s)
	}

	names, types, err := SchemaFields(nestedUUIDParquet)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := map[string]string{"n": "int", "s": "json", "u": "string", "l": "json", "mp": "json", "m": "int"}
	if !slices.Equal(names, []string{"n", "s", "u", "l", "mp", "m"}) || !reflect.DeepEqual(types, wantTypes) {
		t.Errorf("schema: %v %v", names, types)
	}
}

// testdata/nested_deep.parquet (DuckDB 1.5):
//
//	COPY (SELECT {'a': [1, NULL, 3], 'b': {'c': 'q"uo\te'}, 'm': MAP {1: 'one', 2: NULL}} d,
//	  [{'k': 'x'}, NULL] ls, [[1.5::DOUBLE, 2.25, 'NaN'::DOUBLE], []] lf, [12.50::DECIMAL(5,2)] ld)
//	TO 'nested_deep.parquet'
//
// The oracle is DuckDB's to_json of each column, with two documented
// departures: a NaN is null (DuckDB writes NaN, which is not JSON; a
// non-finite number is absent in ssql), and a decimal keeps its scale's
// digits (12.50, where DuckDB writes 12.5 — the same number).
func TestReadParquetNestedJSON(t *testing.T) {
	recs, err := Read("../testdata/nested_deep.parquet")
	if err != nil {
		t.Fatal(err)
	}
	r := slices.Collect(recs)[0]
	for field, want := range map[string]string{
		"d":  `{"a":[1,null,3],"b":{"c":"q\"uo\\te"},"m":{"1":"one","2":null}}`,
		"ls": `[{"k":"x"},null]`,
		"lf": `[[1.5,2.25,null],[]]`,
		"ld": `[12.50]`,
	} {
		got := ssql.GetOr(r, field, ssql.JSONString(""))
		if string(got) != want {
			t.Errorf("%s: got %s, want %s", field, got, want)
		}
		if !got.IsValid() {
			t.Errorf("%s: not valid JSON: %s", field, got)
		}
	}
}
