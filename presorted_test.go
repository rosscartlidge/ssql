package ssql

import (
	"slices"
	"strings"
	"testing"
)

// TestPresortedRefusesUnsortedInput: the streaming forms behind -presorted
// (StreamGroupByFields, StreamWindow) trusted the flag and, on input that
// was not sorted, made every run its own group — silently (2026-10-29: a
// four-row file with its partitions interleaved gave rank 1 and a sum of
// the row itself on every row). A key that returns after another has
// ended, or an order field that goes backwards within a key, is now a
// panic naming the row; sorted input is unchanged.
func TestPresortedRefusesUnsortedInput(t *testing.T) {
	rows := func(pairs ...[2]int64) []Record {
		var out []Record
		for _, p := range pairs {
			m := MakeMutableRecord()
			m.put("d", []string{"a", "b"}[p[0]])
			m.put("n", p[1])
			out = append(out, m.Freeze())
		}
		return out
	}
	drain := func(f Filter[Record, Record], in []Record) (n int, msg string) {
		defer func() {
			if r := recover(); r != nil {
				msg = r.(error).Error()
			}
		}()
		for range f(slices.Values(in)) {
			n++
		}
		return
	}
	interleaved := rows([2]int64{0, 1}, [2]int64{1, 1}, [2]int64{0, 2}, [2]int64{1, 2})
	sorted := rows([2]int64{0, 1}, [2]int64{0, 2}, [2]int64{1, 1}, [2]int64{1, 2})
	backwards := rows([2]int64{0, 2}, [2]int64{0, 1})

	group := StreamGroupByFields("_group", "d")
	if _, msg := drain(group, interleaved); !strings.Contains(msg, "group-by -presorted: row 3 returns to d=a") {
		t.Errorf("group-by on interleaved keys: got %q", msg)
	}
	if n, msg := drain(group, sorted); n != 2 || msg != "" {
		t.Errorf("group-by on sorted input: %d groups, %q", n, msg)
	}

	cfg := []WindowConfig{{PartitionBy: []string{"d"}, OrderBy: []OrderField{{Field: "n"}},
		Specs: []WindowSpec{{Function: WSum("n"), ResultName: "s"}}}}
	window, err := StreamWindow(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, msg := drain(window, interleaved); !strings.Contains(msg, "window -presorted: row 3 returns to d=a") {
		t.Errorf("window on interleaved partitions: got %q", msg)
	}
	if _, msg := drain(window, backwards); !strings.Contains(msg, "window -presorted: row 2 (n=1) comes before the row above it (n=2)") {
		t.Errorf("window on a backwards order: got %q", msg)
	}
	if n, msg := drain(window, sorted); n != 4 || msg != "" {
		t.Errorf("window on sorted input: %d rows, %q", n, msg)
	}
	// The delayed form (a LEAD spec buffers rows) guards the same way.
	cfg[0].Specs = append(cfg[0].Specs, WindowSpec{Function: WLead("n", 1), ResultName: "next"})
	delayed, err := StreamWindow(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, msg := drain(delayed, interleaved); !strings.Contains(msg, "window -presorted: row 3 returns to d=a") {
		t.Errorf("delayed window on interleaved partitions: got %q", msg)
	}
	if n, msg := drain(delayed, sorted); n != 4 || msg != "" {
		t.Errorf("delayed window on sorted input: %d rows, %q", n, msg)
	}
}
