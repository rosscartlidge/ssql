package commands

import "testing"

// TestParseRangeBound: RANGE frame bounds are a number (the order field's
// units), a duration with days allowed (seconds), or "unbounded" (−1);
// negatives and junk are loud.
func TestParseRangeBound(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		isTime bool
		err    bool
	}{
		{"10000", 10000, false, false},
		{"2.5", 2.5, false, false},
		{"unbounded", -1, false, false},
		{"UNBOUNDED", -1, false, false},
		{"5m", 300, true, false},
		{"2h", 7200, true, false},
		{"7d", 7 * 86400, true, false},
		{"1.5d", 1.5 * 86400, true, false},
		{"-5", 0, false, true},
		{"-5m", 0, true, true},
		{"soon", 0, false, true},
		{"", 0, false, true},
	}
	for _, c := range cases {
		v, isTime, err := parseRangeBound(c.in)
		if (err != nil) != c.err {
			t.Errorf("parseRangeBound(%q): err=%v, want err=%v", c.in, err, c.err)
			continue
		}
		if err == nil && (v != c.want || isTime != c.isTime) {
			t.Errorf("parseRangeBound(%q) = %v,%v want %v,%v", c.in, v, isTime, c.want, c.isTime)
		}
	}
}
