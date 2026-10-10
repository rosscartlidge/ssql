package ssql

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTableSchemaParse: the three sidecar shapes (DFC146) map to the
// same TableSchema; resources and tables are matched by file name; the
// dialects and formats the readers cannot honour are refused.
func TestTableSchemaParse(t *testing.T) {
	want := []TableField{
		{"id", FieldTypeInt, ""}, {"amount", FieldTypeFloat, ""}, {"ok", FieldTypeBool, ""},
		{"when", FieldTypeTime, ""}, {"zip", FieldTypeString, ""}, {"addr", FieldTypeJSON, "object"},
		{"tags", FieldTypeJSON, "array"}, {"free", FieldTypeAuto, ""},
	}
	cases := []struct{ name, doc, csvBase, kind string }{
		{"frictionless package", `{"resources":[{"name":"other","path":"other.csv","schema":{"fields":[{"name":"x","type":"string"}]}},
			{"name":"sales","path":"data/sales.csv","schema":{"fields":[
			{"name":"id","type":"integer"},{"name":"amount","type":"number"},{"name":"ok","type":"boolean"},
			{"name":"when","type":"datetime","format":"default"},{"name":"zip","type":"string"},
			{"name":"addr","type":"object"},{"name":"tags","type":"array"},{"name":"free","type":"any"}]}}]}`, "sales.csv", "frictionless"},
		{"bare table schema", `{"fields":[
			{"name":"id","type":"integer"},{"name":"amount","type":"number"},{"name":"ok","type":"boolean"},
			{"name":"when","type":"date"},{"name":"zip","type":"string"},
			{"name":"addr","type":"object"},{"name":"tags","type":"array"},{"name":"free","type":"any"}]}`, "", "frictionless"},
		{"csvw", `{"@context":"http://www.w3.org/ns/csvw","url":"sales.csv","tableSchema":{"columns":[
			{"name":"id","datatype":"integer"},{"name":"amount","datatype":"decimal"},{"name":"ok","datatype":"boolean"},
			{"name":"when","datatype":{"base":"date","format":"yyyy-MM-dd"}},{"titles":"zip","datatype":"string"},
			{"name":"addr","datatype":"json","virtual":false},{"name":"tags","datatype":"json"},{"name":"free","datatype":"any"},
			{"name":"ignored","virtual":true,"datatype":"string"}]}}`, "sales.csv", "csvw"},
		{"csvw tables", `{"tables":[{"url":"x.csv","tableSchema":{"columns":[{"name":"x"}]}},{"url":"http://h/sales.csv","tableSchema":{"columns":[
			{"name":"id","datatype":"long"},{"name":"amount","datatype":"double"},{"name":"ok","datatype":"boolean"},
			{"name":"when","datatype":"dateTime"},{"name":"zip","datatype":"string"},
			{"name":"addr","datatype":"json"},{"name":"tags","datatype":"json"},{"name":"free","datatype":"any"}]}}]}`, "sales.csv", "csvw"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseTableSchema([]byte(c.doc), c.csvBase, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != c.kind {
				t.Errorf("kind %q want %q", got.Kind, c.kind)
			}
			w := slices.Clone(want)
			if c.kind == "csvw" { // json without a shape
				w[5].Shape, w[6].Shape = "", ""
			}
			if !slices.Equal(got.Fields, w) {
				t.Errorf("fields\n got %v\nwant %v", got.Fields, w)
			}
			ov := got.TypeOverrides()
			if ov["id"] != "int" || ov["when"] != "time" || ov["addr"] != "json" {
				t.Errorf("overrides %v", ov)
			}
			if _, has := ov["free"]; has {
				t.Error("an `any` column is inferred, not overridden")
			}
		})
	}

	refused := []struct{ name, doc, want string }{
		{"no header", `{"fields":[{"name":"a","type":"string"}],"x":1,"resources":[{"path":"s.csv","dialect":{"header":false},"schema":{"fields":[{"name":"a"}]}}]}`, "no header row"},
		{"semicolon", `{"resources":[{"path":"s.csv","dialect":{"delimiter":";"},"schema":{"fields":[{"name":"a"}]}}]}`, "from tsv"},
		{"date format", `{"fields":[{"name":"d","type":"date","format":"%d/%m/%Y"}]}`, "date format"},
		{"unknown type", `{"fields":[{"name":"d","type":"money"}]}`, "unknown type"},
		{"other files only", `{"resources":[{"path":"other.csv","schema":{"fields":[{"name":"a"}]}}]}`, "other files"},
		{"not a sidecar", `{"hello":1}`, "neither"},
	}
	for _, c := range refused {
		_, err := ParseTableSchema([]byte(c.doc), "s.csv", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want %q", c.name, err, c.want)
		}
	}
}

// TestFindTableSchema: the discovery rule, in order, and a package for
// other files is passed over; a sidecar that exists but is broken is an
// error, not silence.
func TestFindTableSchema(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "s.csv")
	os.WriteFile(csv, []byte("a\n1\n"), 0o644)
	if s, err := FindTableSchema(csv); s != nil || err != nil {
		t.Fatalf("nothing beside: %v %v", s, err)
	}
	os.WriteFile(filepath.Join(dir, "datapackage.json"), []byte(`{"resources":[{"path":"other.csv","schema":{"fields":[{"name":"a","type":"integer"}]}}]}`), 0o644)
	if s, err := FindTableSchema(csv); s != nil || err != nil {
		t.Fatalf("package for other files: %v %v", s, err)
	}
	os.WriteFile(filepath.Join(dir, "datapackage.json"), []byte(`{"resources":[{"path":"s.csv","schema":{"fields":[{"name":"a","type":"integer"}]}}]}`), 0o644)
	s, err := FindTableSchema(csv)
	if err != nil || s == nil || s.Kind != "frictionless" || s.Fields[0].Type != FieldTypeInt {
		t.Fatalf("package: %v %v", s, err)
	}
	os.WriteFile(csv+"-metadata.json", []byte(`{"tableSchema":{"columns":[{"name":"a","datatype":"string"}]}}`), 0o644)
	s, err = FindTableSchema(csv)
	if err != nil || s == nil || s.Kind != "csvw" || s.Fields[0].Type != FieldTypeString {
		t.Fatalf("csvw wins: %v %v", s, err)
	}
	os.WriteFile(csv+"-metadata.json", []byte(`{"tableSchema":{"columns":[{"name":"a","datatype":"money"}]}}`), 0o644)
	if _, err = FindTableSchema(csv); err == nil {
		t.Fatal("a broken sidecar is an error")
	}
}

// TestWriteDatapackage: a package is created, a second file is appended,
// the same file is replaced; the types round-trip through FindTableSchema.
func TestWriteDatapackage(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.csv")
	if err := WriteDatapackage(a, []TableField{{"id", FieldTypeInt, ""}, {"addr", FieldTypeJSON, "object"}, {"n", FieldTypeAuto, ""}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteDatapackage(filepath.Join(dir, "b.csv"), []TableField{{"x", FieldTypeFloat, ""}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteDatapackage(a, []TableField{{"id", FieldTypeString, ""}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "datapackage.json"))
	var pkg struct {
		Profile   string `json:"profile"`
		Resources []struct {
			Name, Path string
			Schema     struct{ Fields []struct{ Name, Type string } }
		}
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Profile != "tabular-data-package" || len(pkg.Resources) != 2 {
		t.Fatalf("package: %s", data)
	}
	if pkg.Resources[0].Path != "b.csv" || pkg.Resources[1].Path != "a.csv" || pkg.Resources[1].Schema.Fields[0].Type != "string" {
		t.Errorf("replace/append order: %s", data)
	}
	s, err := FindTableSchema(filepath.Join(dir, "b.csv"))
	if err != nil || s.Fields[0].Type != FieldTypeFloat {
		t.Errorf("round trip: %v %v", s, err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("no trailing newline")
	}
}

// TestTableFieldsFromRecords: first-appearance order, int→float
// widening, a json shape, a known type kept, a never-valued field is text.
func TestTableFieldsFromRecords(t *testing.T) {
	recs := func(yield func(Record) bool) {
		yield(MakeMutableRecord().Int("n", 1).String("s", "a").JSONString("j", `{"a":1}`).Freeze())
		yield(MakeMutableRecord().Float("n", 1.5).String("s", "b").String("late", "x").Freeze())
	}
	seq, fields := TableFieldsFromRecords(recs, map[string]FieldType{"s": FieldTypeJSON})
	for range seq {
	}
	got := fields()
	want := []TableField{{"n", FieldTypeFloat, ""}, {"s", FieldTypeJSON, ""}, {"j", FieldTypeJSON, "object"}, {"late", FieldTypeString, ""}}
	if !slices.Equal(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if TableTypeName(got[1]) != "any" || TableTypeName(got[2]) != "object" || TableTypeName(got[0]) != "number" {
		t.Errorf("type names: %v", got)
	}
}
