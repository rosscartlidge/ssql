package typed

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

type inetRow struct {
	IP ssql.Inet `ssql:"ip"`
	N  int64     `ssql:"n"`
}

// A typed ssql.Inet field is its text in Parquet (DFC162: no columnar file
// keeps an IP address type exactly); it reads back into an Inet field,
// the zero value as an empty string, and a CSV cell the same way.
func TestParquetInetRoundTrip(t *testing.T) {
	rows := []inetRow{{ssql.MustParseInet("10.0.0.1/24"), 1}, {ssql.MustParseInet("2001:db8::1"), 2}, {ssql.Inet{}, 3}}
	path := filepath.Join(t.TempDir(), "ip.parquet")
	if err := WriteParquet(slices.Values(rows), path); err != nil {
		t.Fatal(err)
	}
	var back []inetRow
	for r := range ReadParquet[inetRow](path) {
		back = append(back, r)
	}
	if !slices.Equal(back, rows) {
		t.Errorf("parquet round trip: %v", back)
	}
	csvPath := filepath.Join(t.TempDir(), "ip.csv")
	if err := WriteCSV(slices.Values(rows[:2]), csvPath); err != nil {
		t.Fatal(err)
	}
	var fromCSV []inetRow
	for r := range ReadCSV[inetRow](csvPath) {
		fromCSV = append(fromCSV, r)
	}
	if !slices.Equal(fromCSV, rows[:2]) {
		t.Errorf("csv round trip: %v", fromCSV)
	}
	bad := filepath.Join(t.TempDir(), "bad.csv")
	if err := WriteCSV(slices.Values([]struct {
		IP string `ssql:"ip"`
	}{{"010.0.0.1"}}), bad); err != nil {
		t.Fatal(err)
	}
	err := ssql.Run(func() error {
		for range ReadCSV[inetRow](bad) {
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "010.0.0.1") {
		t.Errorf("a bad address in a CSV cell: want an error naming it, got %v", err)
	}
}
