package typed

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

type moneyRow struct {
	Amt ssql.Decimal `ssql:"amt,decimal:2"`
	N   int64        `ssql:"n"`
}

// A typed ssql.Decimal field is DECIMAL(38,S) in Parquet at the tag's
// scale (DFC155 E): the digits round-trip, a file with more places is
// read at the tag's scale rounding half away from zero, and the record
// reader sees the column as a decimal of the file's scale.
func TestParquetDecimalRoundTrip(t *testing.T) {
	rows := []moneyRow{{ssql.MustParseDecimal("12.34"), 1}, {ssql.MustParseDecimal("-0.05"), 2}, {ssql.MustParseDecimal("90071992547409.92"), 3}}
	path := filepath.Join(t.TempDir(), "money.parquet")
	if err := WriteParquet(slices.Values(rows), path); err != nil {
		t.Fatal(err)
	}
	var back []moneyRow
	for r := range ReadParquet[moneyRow](path) {
		back = append(back, r)
	}
	if !slices.Equal(back, rows) {
		t.Errorf("round trip: %v", back)
	}
	recs, err := ssql.ReadParquet(path)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for r := range recs {
		if d, ok := ssql.Get[ssql.Decimal](r, "amt"); ok && d.Scale() == 2 {
			seen = append(seen, d.String())
		}
	}
	if strings.Join(seen, " ") != "12.34 -0.05 90071992547409.92" {
		t.Errorf("record reader: %v", seen)
	}
	// A file at scale 3 read into a scale-2 field rounds half away from zero.
	type wide struct {
		Amt ssql.Decimal `ssql:"amt,decimal:3"`
	}
	path3 := filepath.Join(t.TempDir(), "three.parquet")
	if err := WriteParquet(slices.Values([]wide{{ssql.MustParseDecimal("2.345")}, {ssql.MustParseDecimal("-2.345")}}), path3); err != nil {
		t.Fatal(err)
	}
	var got []string
	for r := range ReadParquet[struct {
		Amt ssql.Decimal `ssql:"amt,decimal:2"`
	}](path3) {
		got = append(got, r.Amt.String())
	}
	if strings.Join(got, " ") != "2.35 -2.35" {
		t.Errorf("rescale on read: %v", got)
	}
	// A decimal field without its scale in the tag is a loud error.
	err = ssql.Run(func() error {
		return WriteParquet(slices.Values([]struct {
			Amt ssql.Decimal `ssql:"amt"`
		}{{ssql.MustParseDecimal("1.5")}}), filepath.Join(t.TempDir(), "notag.parquet"))
	})
	if err == nil || !strings.Contains(err.Error(), "needs its scale in the tag") {
		t.Errorf("untagged decimal: want a refusal, got %v", err)
	}
}
