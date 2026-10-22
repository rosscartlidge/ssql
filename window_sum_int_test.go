package ssql

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// Window sums of an integer field are exact in int64, running and
// sliding alike, and overflow loudly (DFC155 B's batch, 2026-10-22);
// a float among the values makes the sum a float, as group-by's does.
func TestWindowSumIntegersExact(t *testing.T) {
	rows := func(vs ...any) []Record {
		var out []Record
		for _, v := range vs {
			out = append(out, NewRecord(map[string]any{"v": v}))
		}
		return out
	}
	sums := func(recs []Record, cfg WindowConfig) []any {
		var got []any
		for r := range Window([]WindowConfig{cfg})(slices.Values(recs)) {
			v, _ := Get[any](r, "t")
			got = append(got, v)
		}
		return got
	}
	wide := rows(int64(9007199254740993), int64(9007199254740994), int64(9007199254740995))
	running := WindowConfig{Specs: []WindowSpec{{Function: WSum("v"), ResultName: "t"}}, Frame: WindowFrame{Preceding: -1, Following: 0}}
	if got := sums(wide, running); !slices.Equal(got, []any{int64(9007199254740993), int64(18014398509481987), int64(27021597764222982)}) {
		t.Errorf("running: %v", got)
	}
	sliding := WindowConfig{Specs: []WindowSpec{{Function: WSum("v"), ResultName: "t"}}, Frame: WindowFrame{Preceding: 1, Following: 0}}
	if got := sums(wide, sliding); !slices.Equal(got, []any{int64(9007199254740993), int64(18014398509481987), int64(18014398509481989)}) {
		t.Errorf("sliding: %v", got)
	}
	mixed := rows(int64(1), 0.5, int64(2))
	if got := sums(mixed, sliding); !slices.Equal(got, []any{int64(1), 1.5, 2.5}) {
		t.Errorf("mixed sliding: %v", got)
	}
	over := rows(int64(math.MaxInt64), int64(1))
	err := Run(func() error { sums(over, running); return nil })
	var oe *exprfn.OverflowError
	if !errors.As(err, &oe) {
		t.Errorf("overflowing running sum: want *OverflowError, got %v", err)
	}
}
