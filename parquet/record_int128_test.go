package parquet

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// An int128 column is DECIMAL(38,0) in Parquet and Arrow (DFC155 C): the
// digits survive a round trip, an int64 column beside it stays INT64, a
// DECIMAL(p,0) column reads back as an int128, and a value with 39 digits
// refuses on write instead of being rounded.
func TestParquetInt128RoundTrip(t *testing.T) {
	wide := ssql.MustParseInt128("123456789012345678901234567890")
	rows := []ssql.Record{
		ssql.NewRecord(map[string]any{"id": wide, "n": int64(1)}),
		ssql.NewRecord(map[string]any{"id": ssql.Int128From(5), "n": int64(2)}),
		ssql.NewRecord(map[string]any{"id": ssql.Int128From(-7), "n": int64(3)}),
	}
	path := filepath.Join(t.TempDir(), "wide.parquet")
	if err := Write(slices.Values(rows), path); err != nil {
		t.Fatal(err)
	}
	back, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []any
	var ns []any
	for r := range back {
		id, _ := ssql.Get[any](r, "id")
		n, _ := ssql.Get[any](r, "n")
		ids = append(ids, id)
		ns = append(ns, n)
	}
	if !slices.Equal(ids, []any{wide, ssql.Int128From(5), ssql.Int128From(-7)}) {
		t.Errorf("ids: %v (%T)", ids, ids[0])
	}
	if !slices.Equal(ns, []any{int64(1), int64(2), int64(3)}) {
		t.Errorf("ns: %v (%T)", ns, ns[0])
	}
	// The header types name the column int128 (schema mode, typed codegen).
	names, types, err := SchemaFields(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"id", "n"}) || types["id"] != "int128" || types["n"] != "int" {
		t.Errorf("schema: %v %v", names, types)
	}
	// 39 digits do not fit DECIMAL(38,0): a loud refusal, never a rounding.
	too := []ssql.Record{ssql.NewRecord(map[string]any{"id": ssql.MaxInt128})}
	err = ssql.Run(func() error { return Write(slices.Values(too), filepath.Join(t.TempDir(), "too.parquet")) })
	if err == nil || !strings.Contains(err.Error(), "does not fit DECIMAL(38,0)") || !strings.Contains(err.Error(), ssql.MaxInt128.String()) {
		t.Errorf("39 digits: want a refusal naming the value, got %v", err)
	}
}
