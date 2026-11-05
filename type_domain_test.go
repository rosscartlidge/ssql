package ssql

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDomainLintGaps pins the library-path fixes DFC163's domain lint
// found: switches over record values that a newer type was never added to.
// The CLI lanes are pinned by equivalence cases; these paths are the
// library's own.
func TestDomainLintGaps(t *testing.T) {
	d := MustParseDecimalScale("1.50", 2)

	t.Run("a decimal converts to bool as an int128 does", func(t *testing.T) {
		r := MakeMutableRecord().Decimal("d", d).Decimal("z", MustParseDecimalScale("0", 2)).Freeze()
		if !GetOr(r, "d", false) || GetOr(r, "z", true) {
			t.Errorf("GetOr bool of 1.50, 0.00 = %v, %v; want true, false", GetOr(r, "d", false), GetOr(r, "z", true))
		}
	})

	t.Run("a decimal is a number in a comparison message", func(t *testing.T) {
		if got := kindName(d); got != "number" {
			t.Errorf("kindName(Decimal) = %q", got)
		}
	})

	t.Run("one instant is one stable key in any zone", func(t *testing.T) {
		utc := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
		syd := utc.In(time.FixedZone("AEDT", 11*3600))
		if StableKey(utc) != StableKey(syd) || StableKey(utc) != "2026-01-31T12:00:00Z" {
			t.Errorf("StableKey: %q vs %q", StableKey(utc), StableKey(syd))
		}
	})

	t.Run("a decimal column's sidecar type is its decimal", func(t *testing.T) {
		recs := slices.Values([]Record{MakeMutableRecord().Decimal("d", d).Freeze()})
		seq, fields := TableFieldsFromRecords(recs, nil)
		for range seq {
		}
		if fs := fields(); len(fs) != 1 || fs[0].Type != DecimalType(2) {
			t.Errorf("fields = %+v; want d decimal:2", fs)
		}
	})

	t.Run("pretty JSON writes a time as RFC 3339", func(t *testing.T) {
		at := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
		path := filepath.Join(t.TempDir(), "out.json")
		if err := WriteJSONPretty(slices.Values([]Record{MakeMutableRecord().Time("t", at).Freeze()}), path); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"2026-01-31T12:00:00Z"`) {
			t.Errorf("WriteJSONPretty: %s", b)
		}
	})

	t.Run("string-agg text of a decimal keeps its scale", func(t *testing.T) {
		if got := AggValueString(d); got != "1.50" {
			t.Errorf("AggValueString(1.50) = %q", got)
		}
	})

	t.Run("a decimal is numeric to a chart", func(t *testing.T) {
		if !isNumericValue(d) {
			t.Error("isNumericValue(Decimal) = false")
		}
	})
}
