package ssql

import (
	"errors"
	"strings"
	"testing"
)

// DFC142 step 4: Safely delivers a pipeline panic as the stream's last
// element, one test per kind of mid-stream failure the library raises.

func collectSafely[U any](t *testing.T, seq func(func(U, error) bool)) ([]U, error) {
	t.Helper()
	var got []U
	var last error
	for u, err := range seq {
		if err != nil {
			if last != nil {
				t.Fatal("more than one error element")
			}
			last = err
			continue
		}
		if last != nil {
			t.Fatal("an element arrived after the error")
		}
		got = append(got, u)
	}
	return got, last
}

func TestSafelyBadCell(t *testing.T) {
	cfg := DefaultCSVConfig()
	cfg.TypeOverrides = map[string]FieldType{"a": FieldTypeInt}
	src := ReadCSVFromReader(strings.NewReader("a\n1\n2\nx\n"), cfg)
	got, err := collectSafely(t, Safely(Where(func(Record) bool { return true }))(Safe(src)))
	if len(got) != 2 {
		t.Errorf("got %d rows before the failure, want 2", len(got))
	}
	var ce *CellError
	if !errors.As(err, &ce) || ce.Row != 3 {
		t.Fatalf("err = %v, want the row-3 *CellError", err)
	}
}

func TestSafelyCast(t *testing.T) {
	rows := From([]Record{Field("v", "abc")})
	cast := Select(func(r Record) Record {
		return CastField(r.ToMutable(), r, "v", FieldTypeInt, false, nil).Freeze()
	})
	_, err := collectSafely(t, Safely(cast)(Safe(rows)))
	var ce *CastError
	if !errors.As(err, &ce) || ce.Field != "v" {
		t.Fatalf("err = %v, want a *CastError on v", err)
	}
}

func TestSafelyMixedKindAggregate(t *testing.T) {
	rows := From([]Record{
		MakeMutableRecord().String("k", "a").Int("v", 1).Freeze(),
		MakeMutableRecord().String("k", "a").String("v", "x").Freeze(),
	})
	agg := Chain(GroupByFields("g", "k"), Aggregate("g", map[string]AggregateFunc{"m": MinOf("v")}))
	_, err := collectSafely(t, Safely(agg)(Safe(rows)))
	if err == nil {
		t.Fatal("a mixed-kind MinOf must fail, not pick a winner")
	}
}

func TestSafelyWindowRangePrecondition(t *testing.T) {
	rows := From([]Record{MakeMutableRecord().Int("a", 1).Int("b", 2).Int("v", 3).Freeze()})
	w := Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "a"}, {Field: "b"}},
		Frame:   WindowFrame{Range: true, RangePreceding: 1},
		Specs:   []WindowSpec{{Function: WSum("v"), ResultName: "s"}},
	}})
	_, err := collectSafely(t, Safely(w)(Safe(rows)))
	if err == nil || !strings.Contains(err.Error(), "RANGE") {
		t.Fatalf("err = %v, want the RANGE precondition", err)
	}
}

func TestSafelySpillUnwritable(t *testing.T) {
	rows := From([]Record{Field("v", int64(3)), Field("v", int64(1)), Field("v", int64(2))})
	sort := SortRecordsSpill([]OrderField{{Field: "v"}}, SpillConfig{Dir: "/nonexistent-ssql-dir", MemoryBytes: 1})
	_, err := collectSafely(t, Safely(sort)(Safe(rows)))
	if err == nil || !strings.Contains(err.Error(), "spill") {
		t.Fatalf("err = %v, want the spill failure", err)
	}
}

func TestSafelyPassesInputErrorThrough(t *testing.T) {
	upstream := errors.New("upstream")
	input := func(yield func(Record, error) bool) {
		if !yield(Field("v", int64(1)), nil) {
			return
		}
		yield(Record{}, upstream)
	}
	chain := ChainWithErrors(
		Safely(Where(func(Record) bool { return true })),
		Safely(Limit[Record](10)),
	)
	got, err := collectSafely(t, chain(input))
	if len(got) != 1 || err != upstream {
		t.Fatalf("got %d rows, err %v; want 1 row and the upstream error", len(got), err)
	}
}

func TestSafelyCleanAndEarlyStop(t *testing.T) {
	rows := From([]Record{Field("v", int64(1)), Field("v", int64(2)), Field("v", int64(3))})
	got, err := collectSafely(t, Safely(Where(func(Record) bool { return true }))(Safe(rows)))
	if len(got) != 3 || err != nil {
		t.Fatalf("clean: got %d rows, err %v", len(got), err)
	}
	n := 0
	for _, err := range Safely(Where(func(Record) bool { return true }))(Safe(panicOnThirdRecord())) {
		if err != nil {
			t.Fatal("stopping early must not surface a later failure")
		}
		n++
		break
	}
	if n != 1 {
		t.Fatal("early stop did not stop")
	}
}

func panicOnThirdRecord() func(func(Record) bool) {
	return func(yield func(Record) bool) {
		for i := 1; ; i++ {
			if i == 3 {
				panic(&CellError{Row: 3, Column: "v", Value: "x", Type: FieldTypeInt})
			}
			if !yield(Field("v", int64(i))) {
				return
			}
		}
	}
}
