package ssql

import (
	"bytes"
	"fmt"
	"iter"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"
)

// rsRecords builds records from (ts, value) pairs; missing values (NaN
// marker) omit the field — the ragged case.
func rsRecords(tsField, valField string, pts [][2]int64) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for _, p := range pts {
			m := MakeMutableRecord().Int(tsField, p[0])
			if p[1] != -1 {
				m = m.Int(valField, p[1])
			}
			if !yield(m.Freeze()) {
				return
			}
		}
	}
}

func collectRS(t *testing.T, records iter.Seq[Record], cfg ResampleConfig) []Record {
	t.Helper()
	seq, err := ResampleRecords(records, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out []Record
	for r := range seq {
		out = append(out, r)
	}
	return out
}

// TestResampleGoldenHandComputed: a gappy, SHUFFLED fixture with the
// answer computed by hand — the independent oracle (DFC102: the
// fixture must discriminate; sorted/clean data tests nothing here).
// Observations (epoch seconds): 12→10, 17→20, 31→50 (shuffled order),
// every=10s → epoch grid points 10,20,30.
func TestResampleGoldenHandComputed(t *testing.T) {
	var warn bytes.Buffer
	obs := [][2]int64{{31, 50}, {12, 10}, {17, 20}} // shuffled on purpose
	cases := []struct {
		fill string
		want map[int64]any // grid ts → value (nil = field absent)
	}{
		// previous: at-or-before; leading edge CLAMPS to first obs.
		{FillPrevious, map[int64]any{10: float64(10), 20: float64(20), 30: float64(20)}},
		// next: at-or-after; trailing edge clamps to last.
		{FillNext, map[int64]any{10: float64(10), 20: float64(50), 30: float64(50)}},
		// linear between (17,20)-(31,50); leading edge clamps to first.
		{FillLinear, map[int64]any{10: float64(10), 20: float64(20) + 3.0/14.0*30.0, 30: float64(20) + 13.0/14.0*30.0}},
	}
	for _, c := range cases {
		t.Run(c.fill, func(t *testing.T) {
			got := collectRS(t, rsRecords("ts", "v", obs), ResampleConfig{
				TimeField: "ts", Every: 10 * time.Second, Values: []string{"v"},
				Fill: c.fill, Warn: &warn,
			})
			if len(got) != 3 {
				t.Fatalf("grid size = %d, want 3", len(got))
			}
			for _, r := range got {
				ts := GetOr(r, "ts", int64(-1))
				want, isGrid := c.want[ts]
				if !isGrid {
					t.Fatalf("unexpected grid point %d", ts)
				}
				v, ok := Get[float64](r, "v")
				if want == nil {
					if ok {
						t.Errorf("ts=%d: want absent v, got %v", ts, v)
					}
					continue
				}
				if !ok {
					t.Errorf("ts=%d: v absent, want %v", ts, want)
					continue
				}
				if diff := v - want.(float64); diff > 1e-9 || diff < -1e-9 {
					t.Errorf("ts=%d: v=%v, want %v", ts, v, want)
				}
			}
		})
	}
	if !strings.Contains(warn.String(), "epoch unit auto-detected as s") {
		t.Errorf("expected loud unit detection, warn=%q", warn.String())
	}
}

// TestResampleExactGrid: the property that kills float-drift bugs —
// every output timestamp is EXACTLY k*every from the epoch.
func TestResampleExactGrid(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	var pts [][2]int64
	base := int64(1_700_000_000)
	for i := 0; i < 2000; i++ {
		pts = append(pts, [2]int64{base + rnd.Int63n(1_000_000), rnd.Int63n(100)})
	}
	every := 7 * time.Second // deliberately awkward period
	got := collectRS(t, rsRecords("ts", "v", pts), ResampleConfig{
		TimeField: "ts", Every: every, Values: []string{"v"}, Warn: &bytes.Buffer{},
	})
	var prev int64
	for i, r := range got {
		ts := GetOr(r, "ts", int64(-1))
		if ts%7 != 0 {
			t.Fatalf("grid point %d not on the epoch grid (ts%%7=%d)", ts, ts%7)
		}
		if i > 0 && ts != prev+7 {
			t.Fatalf("grid gap: %d after %d", ts, prev)
		}
		prev = ts
	}
}

// TestResampleEdges: empty input, single observation (linear must not
// divide by zero), duplicates (last wins, loud), ragged fields.
func TestResampleEdges(t *testing.T) {
	var warn bytes.Buffer
	empty := collectRS(t, rsRecords("ts", "v", nil), ResampleConfig{
		TimeField: "ts", Every: time.Second, Values: []string{"v"}, Warn: &warn,
	})
	if len(empty) != 0 {
		t.Fatalf("empty input: want 0 records, got %d", len(empty))
	}

	single := collectRS(t, rsRecords("ts", "v", [][2]int64{{15, 7}}), ResampleConfig{
		TimeField: "ts", Every: 10 * time.Second, Values: []string{"v"},
		Fill: FillLinear, Warn: &warn,
	})
	if len(single) != 1 {
		t.Fatalf("single obs: want 1 grid point, got %d", len(single))
	}
	if v := GetOr(single[0], "v", float64(-1)); v != 7 {
		t.Errorf("single obs, linear: edge clamps to the observation, got %v", v)
	}
	if !strings.Contains(warn.String(), "clamped") {
		t.Errorf("clamping must be loud, warn=%q", warn.String())
	}

	warn.Reset()
	dup := collectRS(t, rsRecords("ts", "v", [][2]int64{{10, 1}, {10, 2}, {20, 3}}), ResampleConfig{
		TimeField: "ts", Every: 10 * time.Second, Values: []string{"v"}, Warn: &warn,
	})
	if v := GetOr(dup[0], "v", float64(-1)); v != 2 {
		t.Errorf("duplicate ts: highest value must win (order-independent for parallel lanes), got %v", v)
	}
	if !strings.Contains(warn.String(), "duplicate") {
		t.Errorf("duplicates must be loud, warn=%q", warn.String())
	}
}

// TestResamplePreserveFamily: RFC3339 strings in → RFC3339 out;
// epoch ms in → epoch ms out.
func TestResamplePreserveFamily(t *testing.T) {
	var warn bytes.Buffer
	strRecs := func(yield func(Record) bool) {
		for _, p := range []struct {
			ts string
			v  int64
		}{{"2026-01-01T00:00:12Z", 10}, {"2026-01-01T00:00:31Z", 50}} {
			if !yield(MakeMutableRecord().String("ts", p.ts).Int("v", p.v).Freeze()) {
				return
			}
		}
	}
	got := collectRS(t, strRecs, ResampleConfig{
		TimeField: "ts", Every: 10 * time.Second, Values: []string{"v"}, Warn: &warn,
	})
	first := GetOr(got[0], "ts", "")
	if first != "2026-01-01T00:00:10Z" {
		t.Errorf("string family not preserved: %q", first)
	}

	msRecs := rsRecords("ts", "v", [][2]int64{{1_700_000_000_123, 1}, {1_700_000_021_456, 2}})
	gotMS := collectRS(t, msRecs, ResampleConfig{
		TimeField: "ts", Every: 10 * time.Second, Values: []string{"v"}, Warn: &warn,
	})
	ts0 := GetOr(gotMS[0], "ts", int64(-1))
	if ts0%10_000 != 0 || ts0 < 1_700_000_000_000-10_000 {
		t.Errorf("ms family: got %d, want ms-scale grid point", ts0)
	}
	if !strings.Contains(warn.String(), "detected as ms") {
		t.Errorf("ms detection should be loud, warn=%q", warn.String())
	}
}

// TestResampleErrors: loudness gates.
func TestResampleErrors(t *testing.T) {
	strVal := func(yield func(Record) bool) {
		yield(MakeMutableRecord().Int("ts", 10).String("v", "oops").Freeze())
	}
	if _, err := ResampleRecords(strVal, ResampleConfig{
		TimeField: "ts", Every: time.Second, Values: []string{"v"}, Warn: &bytes.Buffer{},
	}); err == nil || !strings.Contains(err.Error(), "numeric") {
		t.Errorf("non-numeric value field must refuse loudly, got %v", err)
	}
	if _, err := ResampleRecords(rsRecords("ts", "v", [][2]int64{{1, 1}}), ResampleConfig{
		TimeField: "ts", Every: time.Second, Values: []string{"v"}, Fill: "bogus", Warn: &bytes.Buffer{},
	}); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("unknown fill must refuse loudly, got %v", err)
	}
	if _, err := ResampleRecords(rsRecords("ts", "v", [][2]int64{{1, 1}}), ResampleConfig{
		TimeField: "missing", Every: time.Second, Values: []string{"v"}, Warn: &bytes.Buffer{},
	}); err == nil {
		t.Error("missing time field must refuse loudly")
	}
}

// TestSnapToBucket incl. the pre-1970 Euclidean floor.
func TestSnapToBucket(t *testing.T) {
	e := 10 * time.Second
	cases := [][2]int64{
		{int64(25 * time.Second), int64(20 * time.Second)},
		{int64(20 * time.Second), int64(20 * time.Second)},
		{int64(-5 * time.Second), int64(-10 * time.Second)},
	}
	for _, c := range cases {
		if got := SnapToBucket(c[0], e); got != c[1] {
			t.Errorf("SnapToBucket(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}

var _ = slices.Contains[[]string] // keep imports honest if edited
var _ = fmt.Sprintf
