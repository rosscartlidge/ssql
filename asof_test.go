package ssql

import (
	"slices"
	"testing"
	"time"
)

func asofRows(rows ...[]any) []Record {
	var out []Record
	for _, row := range rows {
		m := MakeMutableRecord()
		for i := 0; i+1 < len(row); i += 2 {
			switch v := row[i+1].(type) {
			case string:
				m = m.String(row[i].(string), v)
			case int:
				m = m.Int(row[i].(string), int64(v))
			case float64:
				m = m.Float(row[i].(string), v)
			case time.Time:
				m = m.Time(row[i].(string), v)
			}
		}
		out = append(out, m.Freeze())
	}
	return out
}

func asofOut(recs []Record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, GetOr(r, "trade", "")+"@"+GetOr(r, "q", "-"))
	}
	return out
}

func TestAsofJoinNumeric(t *testing.T) {
	// quotes per symbol, deliberately out of order, with a tie at ts 30
	quotes := asofRows(
		[]any{"sym", "A", "ts", 30, "q", "a30x"},
		[]any{"sym", "A", "ts", 10, "q", "a10"},
		[]any{"sym", "B", "ts", 20, "q", "b20"},
		[]any{"sym", "A", "ts", 30, "q", "a30y"},
		[]any{"sym", "A", "ts", 50, "q", "a50"},
		[]any{"sym", "C", "q", "no-ts"},
	)
	trades := asofRows(
		[]any{"trade", "t1", "sym", "A", "ts", 5},  // before any quote
		[]any{"trade", "t2", "sym", "A", "ts", 30}, // exactly at the tie
		[]any{"trade", "t3", "sym", "A", "ts", 49},
		[]any{"trade", "t4", "sym", "B", "ts", 100},
		[]any{"trade", "t5", "sym", "Z", "ts", 1}, // no series
		[]any{"trade", "t6", "sym", "A"},          // no ts
	)
	seq := func(rs []Record) func(func(Record) bool) { return slices.Values(rs) }
	base := AsofConfig{LeftKeys: []string{"sym"}, RightKeys: []string{"sym"}, LeftTime: "ts", RightTime: "ts"}
	with := func(f func(*AsofConfig)) AsofConfig { c := base; f(&c); return c }

	cases := []struct {
		name string
		cfg  AsofConfig
		want []string
	}{
		{"backward", base, []string{"t2@a30y", "t3@a30y", "t4@b20"}},
		{"backward left", with(func(c *AsofConfig) { c.KeepUnmatched = true }), []string{"t1@-", "t2@a30y", "t3@a30y", "t4@b20", "t5@-", "t6@-"}},
		{"backward strict", with(func(c *AsofConfig) { c.Strict = true }), []string{"t2@a10", "t3@a30y", "t4@b20"}},
		{"forward", with(func(c *AsofConfig) { c.Forward = true }), []string{"t1@a10", "t2@a30y", "t3@a50"}},
		{"forward strict", with(func(c *AsofConfig) { c.Forward = true; c.Strict = true }), []string{"t1@a10", "t2@a50", "t3@a50"}},
		{"tolerance", with(func(c *AsofConfig) { c.Tolerance = 10 }), []string{"t2@a30y"}},
		{"tolerance left", with(func(c *AsofConfig) { c.Tolerance = 10; c.KeepUnmatched = true }), []string{"t1@-", "t2@a30y", "t3@-", "t4@-", "t5@-", "t6@-"}},
	}
	for _, c := range cases {
		got := asofOut(slices.Collect(AsofJoin(seq(quotes), c.cfg)(seq(trades))))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	// The merged row: left fields, then the right's non-key/non-time ones.
	rows := slices.Collect(AsofJoin(seq(quotes), base)(seq(trades)))
	if got := slices.Collect(rows[0].KeysIter()); !slices.Equal(got, []string{"trade", "sym", "ts", "q"}) {
		t.Errorf("merged fields: %v", got)
	}
	if GetOr(rows[0], "ts", int64(0)) != 30 {
		t.Errorf("the left ts must be the row's, got %v", GetOr(rows[0], "ts", int64(0)))
	}
}

func TestAsofJoinTimeAndNoKey(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	readings := asofRows(
		[]any{"at", t0, "q", "r0"},
		[]any{"at", t0.Add(10 * time.Minute), "q", "r10"},
	)
	events := asofRows(
		[]any{"trade", "e1", "at", t0.Add(4 * time.Minute)},
		[]any{"trade", "e2", "at", t0.Add(12 * time.Minute)},
		[]any{"trade", "e3", "at", 5}, // a number against times
	)
	seq := func(rs []Record) func(func(Record) bool) { return slices.Values(rs) }
	cfg := AsofConfig{LeftTime: "at", RightTime: "at", Tolerance: float64(3 * time.Minute)}
	got := asofOut(slices.Collect(AsofJoin(seq(readings), cfg)(seq(events))))
	if want := []string{"e2@r10"}; !slices.Equal(got, want) {
		t.Errorf("time tolerance: got %v want %v", got, want)
	}
	cfg.Tolerance = 0
	got = asofOut(slices.Collect(AsofJoin(seq(readings), cfg)(seq(events))))
	if want := []string{"e1@r0", "e2@r10"}; !slices.Equal(got, want) {
		t.Errorf("no key, no tolerance: got %v want %v", got, want)
	}
}
