//go:build !slim

package typed

import (
	"slices"
	"testing"
)

// ../testdata/nested_uuid.parquet (DuckDB 1.5; see the root package's
// parquet_nested_test.go): n, s struct, u UUID, l list, mp map, m — six
// fields over eight leaves. The reader takes leaf indices (DFC162 §1).
const nestedUUIDParquet = "../testdata/nested_uuid.parquet"

type nestedRow struct {
	N  int32  `ssql:"n"`
	S  string `ssql:"s"`
	U  string `ssql:"u"`
	L  string `ssql:"l"`
	Mp string `ssql:"mp"`
	M  int32  `ssql:"m"`
}

// Only the columns past the nested ones: a field-index read decoded the
// wrong leaves into them.
type pastNestedRow struct {
	U string `ssql:"u"`
	M int32  `ssql:"m"`
}

func TestReadParquetNestedAndUUID(t *testing.T) {
	want := []nestedRow{
		{1, `{"x":10,"y":"a"}`, "6ba7b810-9dad-11d1-80b4-00c04fd430c8", `[1,2]`, `{"k":1}`, 100},
		{2, `{"x":20,"y":"b"}`, "", `[]`, `{}`, 200},
	}
	if got := slices.Collect(ReadParquet[nestedRow](nestedUUIDParquet)); !slices.Equal(got, want) {
		t.Errorf("serial:\n got %#v\nwant %#v", got, want)
	}
	for _, n := range []int{1, 2} {
		if got := slices.Collect(ReadParquetParallel[nestedRow](nestedUUIDParquet, n).Serial()); !slices.Equal(got, want) {
			t.Errorf("parallel n=%d:\n got %#v\nwant %#v", n, got, want)
		}
	}
	wantPast := []pastNestedRow{{"6ba7b810-9dad-11d1-80b4-00c04fd430c8", 100}, {"", 200}}
	if got := slices.Collect(ReadParquet[pastNestedRow](nestedUUIDParquet)); !slices.Equal(got, wantPast) {
		t.Errorf("past the nested columns:\n got %#v\nwant %#v", got, wantPast)
	}
}
