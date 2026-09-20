package ssql

import (
	"strings"
	"testing"
)

// TestZeroPaddedValuesStayText (DFC133 random differential): 007 and 02134
// are identifiers. Read as numbers they lose their zeros and nothing can
// put them back. 0, 0.5, -0.25 and 100 are still numbers.
func TestZeroPaddedValuesStayText(t *testing.T) {
	for v, want := range map[string]bool{"007": true, "02134": true, "-05": true, "00.5": true, "+01": true, " 010 ": true,
		"0": false, "0.5": false, "-0.25": false, "100": false, "7": false, "": false, "0x10": false, "abc": false} {
		if got := ZeroPaddedNumber(v); got != want {
			t.Errorf("ZeroPaddedNumber(%q) = %v, want %v", v, got, want)
		}
	}
	csv := "id,zip,amt\n1,02134,0.5\n2,90210,10\n3,,0\n"
	var recs []Record
	for r := range ReadCSVFromReader(strings.NewReader(csv)) {
		recs = append(recs, r)
	}
	if got, ok := Get[any](recs[0], "zip"); !ok || got != "02134" {
		t.Errorf("zip = %v (%T), want the string 02134", got, got)
	}
	if got, ok := Get[any](recs[1], "zip"); !ok || got != "90210" {
		t.Errorf("a column with one zero-padded value is text throughout: %v (%T)", got, got)
	}
	if got, ok := Get[any](recs[0], "amt"); !ok || got != 0.5 {
		t.Errorf("amt = %v (%T), want the float 0.5", got, got)
	}
	if got, ok := Get[any](recs[0], "id"); !ok || got != int64(1) {
		t.Errorf("id = %v (%T), want int64 1", got, got)
	}
}
