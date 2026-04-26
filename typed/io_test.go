package typed

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type person struct {
	Name   string
	Age    int64
	Salary float64
	Active bool
}

type withTags struct {
	First  string `ssql:"first_name"`
	Last   string `csv:"last_name"`
	Hidden string `ssql:"-"`
}

func TestReadCSVRoundTrip(t *testing.T) {
	src := "Name,Age,Salary,Active\nAlice,30,95000.5,true\nBob,25,65000,false\n"

	got := slices.Collect(ReadCSVFromReader[person](strings.NewReader(src)))
	want := []person{
		{Name: "Alice", Age: 30, Salary: 95000.5, Active: true},
		{Name: "Bob", Age: 25, Salary: 65000, Active: false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("round-trip mismatch:\n  got:  %#v\n  want: %#v", got, want)
	}
}

func TestReadCSVCaseInsensitiveHeaders(t *testing.T) {
	src := "name,AGE,salary,active\nAlice,30,95000.5,true\n"
	got := slices.Collect(ReadCSVFromReader[person](strings.NewReader(src)))
	if len(got) != 1 || got[0].Name != "Alice" || got[0].Age != 30 {
		t.Errorf("case-insensitive matching failed: %#v", got)
	}
}

func TestReadCSVUnknownColumnIgnored(t *testing.T) {
	src := "Name,Age,Salary,Active,Extra\nAlice,30,95000.5,true,ignored\n"
	got := slices.Collect(ReadCSVFromReader[person](strings.NewReader(src)))
	if len(got) != 1 || got[0].Name != "Alice" {
		t.Errorf("unknown column should be ignored: %#v", got)
	}
}

func TestReadCSVMissingFieldZeroed(t *testing.T) {
	// Header omits Active; field should be zero-valued.
	src := "Name,Age,Salary\nAlice,30,95000.5\n"
	got := slices.Collect(ReadCSVFromReader[person](strings.NewReader(src)))
	if len(got) != 1 || got[0].Active != false {
		t.Errorf("missing column should leave field zeroed: %#v", got)
	}
}

func TestReadCSVTagPrecedence(t *testing.T) {
	src := "first_name,last_name,Hidden\nAlice,Smith,never\n"
	got := slices.Collect(ReadCSVFromReader[withTags](strings.NewReader(src)))
	if len(got) != 1 || got[0].First != "Alice" || got[0].Last != "Smith" || got[0].Hidden != "" {
		t.Errorf("tag handling wrong: %#v", got)
	}
}

func TestReadCSVEmptyValueZero(t *testing.T) {
	src := "Name,Age,Salary,Active\nAlice,,,\n"
	got := slices.Collect(ReadCSVFromReader[person](strings.NewReader(src)))
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].Age != 0 || got[0].Salary != 0 || got[0].Active != false {
		t.Errorf("empty values should produce zero, got: %#v", got[0])
	}
}

func TestReadCSVSafeReportsParseErrors(t *testing.T) {
	src := "Name,Age,Salary,Active\nAlice,not-a-number,95000.5,true\n"
	var rows []person
	var errs []error
	for r, err := range ReadCSVSafeFromReader[person](strings.NewReader(src)) {
		rows = append(rows, r)
		errs = append(errs, err)
	}
	if len(errs) != 1 || errs[0] == nil {
		t.Fatalf("expected one error row, got %d errors", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "Age") {
		t.Errorf("error should name the failing column, got: %v", errs[0])
	}
}

func TestReadCSVSafeNoErrorOnGoodFile(t *testing.T) {
	src := "Name,Age,Salary,Active\nAlice,30,95000.5,true\n"
	for r, err := range ReadCSVSafeFromReader[person](strings.NewReader(src)) {
		if err != nil {
			t.Errorf("unexpected error: %v (row %#v)", err, r)
		}
	}
}

func TestReadCSVSafeMissingFile(t *testing.T) {
	got := false
	for _, err := range ReadCSVSafe[person]("/nonexistent/does/not/exist.csv") {
		if err != nil {
			got = true
		}
	}
	if !got {
		t.Errorf("missing file should produce an error from ReadCSVSafe")
	}
}

func TestReadCSVRejectsNonStruct(t *testing.T) {
	src := "x\n1\n"
	rows := slices.Collect(ReadCSVFromReader[int](strings.NewReader(src)))
	if len(rows) != 0 {
		t.Errorf("non-struct T should silently produce no rows in lossy ReadCSV, got %#v", rows)
	}
}

func TestReadCSVSafeRejectsNonStruct(t *testing.T) {
	src := "x\n1\n"
	var sawErr error
	for _, err := range ReadCSVSafeFromReader[int](strings.NewReader(src)) {
		if err != nil && sawErr == nil {
			sawErr = err
		}
	}
	if sawErr == nil {
		t.Fatalf("non-struct T should yield an error from ReadCSVSafe")
	}
	if !strings.Contains(sawErr.Error(), "must be a struct") {
		t.Errorf("error should explain the constraint, got: %v", sawErr)
	}
}

func TestWriteCSVHeaderAndRows(t *testing.T) {
	rows := []person{
		{Name: "Alice", Age: 30, Salary: 95000.5, Active: true},
		{Name: "Bob", Age: 25, Salary: 65000, Active: false},
	}
	var buf bytes.Buffer
	if err := WriteCSVToWriter(slices.Values(rows), &buf); err != nil {
		t.Fatal(err)
	}
	want := "Name,Age,Salary,Active\nAlice,30,95000.5,true\nBob,25,65000,false\n"
	if buf.String() != want {
		t.Errorf("WriteCSV mismatch:\n  got:  %q\n  want: %q", buf.String(), want)
	}
}

func TestWriteCSVHonorsTags(t *testing.T) {
	rows := []withTags{{First: "Alice", Last: "Smith", Hidden: "shh"}}
	var buf bytes.Buffer
	if err := WriteCSVToWriter(slices.Values(rows), &buf); err != nil {
		t.Fatal(err)
	}
	want := "first_name,last_name\nAlice,Smith\n"
	if buf.String() != want {
		t.Errorf("WriteCSV tag handling wrong:\n  got:  %q\n  want: %q", buf.String(), want)
	}
}

func TestWriteCSVRejectsNonStruct(t *testing.T) {
	err := WriteCSVToWriter(slices.Values([]int{1, 2, 3}), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must be a struct") {
		t.Errorf("WriteCSV[int] should fail with struct-required error, got: %v", err)
	}
}

func TestWriteThenReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "people.csv")

	in := []person{
		{Name: "Alice", Age: 30, Salary: 95000.5, Active: true},
		{Name: "Bob", Age: 25, Salary: 65000, Active: false},
		{Name: "Carol", Age: 42, Salary: 105000, Active: true},
	}

	if err := WriteCSV(slices.Values(in), path); err != nil {
		t.Fatal(err)
	}
	out := slices.Collect(ReadCSV[person](path))
	if !slices.Equal(in, out) {
		t.Errorf("write/read round-trip mismatch:\n  in:  %#v\n  out: %#v", in, out)
	}
}

type wideTypes struct {
	I32  int32
	U64  uint64
	F32  float32
	When time.Time `ssql:"when"`
}

func TestReadCSVWideTypes(t *testing.T) {
	src := "I32,U64,F32,when\n42,18000000000000000000,3.14,2026-04-26T10:00:00Z\n"
	got := slices.Collect(ReadCSVFromReader[wideTypes](strings.NewReader(src)))
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].I32 != 42 {
		t.Errorf("int32: got %d, want 42", got[0].I32)
	}
	if got[0].U64 != 18000000000000000000 {
		t.Errorf("uint64: got %d", got[0].U64)
	}
	if got[0].F32 < 3.13 || got[0].F32 > 3.15 {
		t.Errorf("float32: got %v, want ~3.14", got[0].F32)
	}
	want := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	if !got[0].When.Equal(want) {
		t.Errorf("time: got %v, want %v", got[0].When, want)
	}
}

func TestWriteCSVWideTypes(t *testing.T) {
	row := wideTypes{
		I32:  42,
		U64:  18000000000000000000,
		F32:  3.14,
		When: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := WriteCSVToWriter(slices.Values([]wideTypes{row}), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "42") || !strings.Contains(out, "18000000000000000000") {
		t.Errorf("missing wide-int values: %q", out)
	}
	if !strings.Contains(out, "2026-04-26T10:00:00Z") {
		t.Errorf("missing time: %q", out)
	}
}

func TestWriteCSVZeroTimeRendersEmpty(t *testing.T) {
	row := wideTypes{I32: 1}
	var buf bytes.Buffer
	if err := WriteCSVToWriter(slices.Values([]wideTypes{row}), &buf); err != nil {
		t.Fatal(err)
	}
	// Header line + one data line; the time column should be empty (",,")
	// rather than rendered as "0001-01-01T00:00:00Z".
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	cols := strings.Split(lines[1], ",")
	if cols[3] != "" {
		t.Errorf("zero time should render as empty string, got %q", cols[3])
	}
}

type nullable struct {
	Name *string
	Age  *int64
}

func TestReadCSVNullable(t *testing.T) {
	src := "Name,Age\nAlice,30\n,\nBob,\n"
	got := slices.Collect(ReadCSVFromReader[nullable](strings.NewReader(src)))
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	if got[0].Name == nil || *got[0].Name != "Alice" {
		t.Errorf("row 0 name: %v", got[0].Name)
	}
	if got[0].Age == nil || *got[0].Age != 30 {
		t.Errorf("row 0 age: %v", got[0].Age)
	}
	if got[1].Name != nil || got[1].Age != nil {
		t.Errorf("row 1 should be all-null, got %#v", got[1])
	}
	if got[2].Name == nil || *got[2].Name != "Bob" || got[2].Age != nil {
		t.Errorf("row 2: name=%v age=%v", got[2].Name, got[2].Age)
	}
}

func TestWriteCSVNullable(t *testing.T) {
	name := "Alice"
	age := int64(30)
	rows := []nullable{
		{Name: &name, Age: &age},
		{Name: nil, Age: nil},
		{Name: &name, Age: nil},
	}
	var buf bytes.Buffer
	if err := WriteCSVToWriter(slices.Values(rows), &buf); err != nil {
		t.Fatal(err)
	}
	want := "Name,Age\nAlice,30\n,\nAlice,\n"
	if buf.String() != want {
		t.Errorf("nullable write:\n  got:  %q\n  want: %q", buf.String(), want)
	}
}

func TestNullableRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "n.csv")
	name := "Alice"
	in := []nullable{
		{Name: &name, Age: nil},
		{Name: nil, Age: nil},
	}
	if err := WriteCSV(slices.Values(in), path); err != nil {
		t.Fatal(err)
	}
	out := slices.Collect(ReadCSV[nullable](path))
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	if out[0].Name == nil || *out[0].Name != "Alice" || out[0].Age != nil {
		t.Errorf("row 0: %#v", out[0])
	}
	if out[1].Name != nil || out[1].Age != nil {
		t.Errorf("row 1: %#v", out[1])
	}
}

func TestReadCSVMalformedHeader(t *testing.T) {
	// Empty input — csv.Reader returns EOF on header read.
	var sawErr error
	for _, err := range ReadCSVSafeFromReader[person](strings.NewReader("")) {
		if err != nil {
			sawErr = err
		}
	}
	if sawErr == nil {
		t.Fatalf("empty input should yield a header error from ReadCSVSafe")
	}
	if !errors.Is(sawErr, sawErr) { // sanity — error is non-nil
		t.Errorf("got: %v", sawErr)
	}
}
