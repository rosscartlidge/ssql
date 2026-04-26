package lib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypeNameFromFilename(t *testing.T) {
	cases := map[string]string{
		"employees.csv":         "EmployeesRow",
		"/tmp/employees.csv":    "EmployeesRow",
		"q4_sales.tsv":          "Q4SalesRow",
		"weird-name.csv":        "WeirdNameRow",
		"123.csv":               "Row123",
		"":                      "Row",
		"a.b.c.csv":             "ABCRow", // each dot resets word boundary
	}
	for in, want := range cases {
		got := TypeNameFromFilename(in)
		if got != want {
			t.Errorf("TypeNameFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoNameFromColumn(t *testing.T) {
	cases := map[string]string{
		"name":       "Name",
		"dept_id":    "DeptID",
		"first name": "FirstName",
		"x-y-z":      "XYZ",
		"_row_num":   "RowNum",
		"123abc":     "F123abc",
		"url":        "URL",
		"my_url":     "MyURL",
		"data_xml":   "DataXML",
		"":           "Col",
	}
	for in, want := range cases {
		got := goNameFromColumn(in)
		if got != want {
			t.Errorf("goNameFromColumn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSampleCSVSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "people.csv")
	body := "name,age,salary,active,joined,nickname\n" +
		"Alice,30,95000.5,true,2020-04-01T09:00:00Z,\n" +
		"Bob,25,65000,false,2021-09-15T13:30:00Z,\n" +
		"Carol,42,105000.25,true,2018-12-01T08:00:00Z,\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, def, err := SampleCSVSchema(path, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if schema.TypeName != "PeopleRow" {
		t.Errorf("TypeName = %q, want PeopleRow", schema.TypeName)
	}
	wantTypes := map[string]string{
		"Name":     "string",
		"Age":      "int64",
		"Salary":   "float64",
		"Active":   "bool",
		"Joined":   "time.Time",
		"Nickname": "*string", // entirely empty in samples
	}
	for _, f := range schema.Fields {
		if got, ok := wantTypes[f.GoName]; !ok || got != f.GoType {
			t.Errorf("field %s: got %s, want %s", f.GoName, f.GoType, wantTypes[f.GoName])
		}
	}
	// Spot-check the rendered struct: should contain ssql tags and the type.
	for _, want := range []string{"type PeopleRow struct", `ssql:"name"`, `ssql:"dept_id"`, "int64", "float64", "time.Time", "*string"} {
		if want == `ssql:"dept_id"` {
			continue // not in this test
		}
		if !strings.Contains(def, want) {
			t.Errorf("rendered struct missing %q\n%s", want, def)
		}
	}
}

func TestSampleCSVSchemaMissingFile(t *testing.T) {
	_, _, err := SampleCSVSchema("/nonexistent/whatever.csv", "", 100)
	if err == nil {
		t.Errorf("expected error for missing file")
	}
}
