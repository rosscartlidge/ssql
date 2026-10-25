package typed

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

type wideRow struct {
	ID ssql.Int128 `ssql:"id"`
	N  int64       `ssql:"n"`
}

// A typed ssql.Int128 field is DECIMAL(38,0) in Parquet (DFC155 C): the
// digits round-trip, and a 39-digit value refuses on write.
func TestParquetInt128RoundTrip(t *testing.T) {
	wide := ssql.MustParseInt128("123456789012345678901234567890")
	rows := []wideRow{{wide, 1}, {ssql.Int128From(5), 2}, {ssql.Int128From(-7), 3}}
	path := filepath.Join(t.TempDir(), "wide.parquet")
	if err := WriteParquet(slices.Values(rows), path); err != nil {
		t.Fatal(err)
	}
	var back []wideRow
	for r := range ReadParquet[wideRow](path) {
		back = append(back, r)
	}
	if !slices.Equal(back, rows) {
		t.Errorf("round trip: %v", back)
	}
	// The record reader sees the same column as an int128.
	recs, err := ssql.ReadParquet(path)
	if err != nil {
		t.Fatal(err)
	}
	for r := range recs {
		if v, _ := ssql.Get[any](r, "id"); v == wide {
			goto found
		}
	}
	t.Errorf("the record reader did not see %s as an int128", wide)
found:
	err = ssql.Run(func() error {
		return WriteParquet(slices.Values([]wideRow{{ssql.MaxInt128, 1}}), filepath.Join(t.TempDir(), "too.parquet"))
	})
	if err == nil || !strings.Contains(err.Error(), "does not fit DECIMAL(38,0)") {
		t.Errorf("39 digits: want a refusal, got %v", err)
	}
}
