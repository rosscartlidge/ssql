package ssql

import (
	"iter"
	"testing"
)

// kv is an ordered field list — a Go map would randomize field order,
// and describe's row order follows first-seen field order.
type kv struct {
	k string
	v any
}

func recsOf(rows ...[]kv) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for _, row := range rows {
			mr := MakeMutableRecord()
			for _, f := range row {
				switch x := f.v.(type) {
				case string:
					mr = mr.String(f.k, x)
				case int64:
					mr = mr.Int(f.k, x)
				case float64:
					mr = mr.Float(f.k, x)
				case bool:
					mr = mr.Bool(f.k, x)
				}
			}
			if !yield(mr.Freeze()) {
				return
			}
		}
	}
}

func TestDescribeGoldenHandComputed(t *testing.T) {
	// Shuffled, with a duplicate, a missing cell, an empty string,
	// and a field that appears late (absent in the first two rows).
	in := recsOf(
		[]kv{{"id", int64(3)}, {"city", "Cairo"}, {"pop", 10.5}},
		[]kv{{"id", int64(1)}, {"city", ""}, {"pop", 4.0}},
		[]kv{{"id", int64(2)}, {"city", "Oslo"}, {"pop", 4.0}, {"late", true}},
		[]kv{{"id", int64(4)}, {"city", "Lima"}},
	)
	var rows []Record
	for r := range DescribeRecords(in, DescribeConfig{}) {
		rows = append(rows, r)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (id, city, pop, late)", len(rows))
	}
	byField := map[string]Record{}
	for _, r := range rows {
		byField[GetOr(r, "field", "")] = r
	}
	// Unrestricted output is sorted by field name — the only order
	// that is identical across exec, generated code, and SQL (record
	// iteration order differs between them).
	for i, want := range []string{"city", "id", "late", "pop"} {
		if got := GetOr(rows[i], "field", ""); got != want {
			t.Errorf("row %d field = %q, want %q", i, got, want)
		}
	}
	id := byField["id"]
	if GetOr(id, "type", "") != "int" || GetOr(id, "count", int64(0)) != 4 || GetOr(id, "missing", int64(0)) != 0 ||
		GetOr(id, "distinct", int64(0)) != 4 || GetOr(id, "min", int64(0)) != 1 || GetOr(id, "max", int64(0)) != 4 ||
		GetOr(id, "mean", 0.0) != 2.5 || GetOr(id, "median", 0.0) != 2.5 {
		t.Errorf("id stats wrong: %v", id)
	}
	city := byField["city"]
	// "" counts as missing; distinct excludes it; no numeric stats.
	if GetOr(city, "type", "") != "string" || GetOr(city, "count", int64(0)) != 3 || GetOr(city, "missing", int64(0)) != 1 ||
		GetOr(city, "distinct", int64(0)) != 3 {
		t.Errorf("city stats wrong: %v", city)
	}
	if city.Has("mean") {
		t.Error("string field must not carry numeric stats")
	}
	pop := byField["pop"]
	// values 10.5, 4, 4 (absent in row 4): median 4, mean 6.1666…
	if GetOr(pop, "type", "") != "float" || GetOr(pop, "count", int64(0)) != 3 || GetOr(pop, "missing", int64(0)) != 1 ||
		GetOr(pop, "distinct", int64(0)) != 2 || GetOr(pop, "min", 0.0) != 4 || GetOr(pop, "max", 0.0) != 10.5 ||
		GetOr(pop, "median", 0.0) != 4 {
		t.Errorf("pop stats wrong: %v", pop)
	}
	late := byField["late"]
	// Appeared first in row 3: missing in rows 1, 2, 4.
	if GetOr(late, "type", "") != "bool" || GetOr(late, "count", int64(0)) != 1 || GetOr(late, "missing", int64(0)) != 3 {
		t.Errorf("late stats wrong: %v", late)
	}
}

func TestDescribeRestrictedFields(t *testing.T) {
	in := recsOf([]kv{{"a", int64(1)}, {"b", "x"}}, []kv{{"a", int64(2)}})
	var fields []string
	for r := range DescribeRecords(in, DescribeConfig{Fields: []string{"b", "a"}}) {
		fields = append(fields, GetOr(r, "field", ""))
	}
	if len(fields) != 2 || fields[0] != "b" || fields[1] != "a" {
		t.Errorf("restricted order = %v, want [b a]", fields)
	}
}

func TestDescribeEmptyInput(t *testing.T) {
	n := 0
	for range DescribeRecords(recsOf(), DescribeConfig{}) {
		n++
	}
	if n != 0 {
		t.Errorf("empty input → %d rows, want 0", n)
	}
	// Restricted fields on empty input still describe each as all-missing.
	n = 0
	for r := range DescribeRecords(recsOf(), DescribeConfig{Fields: []string{"x"}}) {
		n++
		if GetOr(r, "missing", int64(-1)) != 0 || GetOr(r, "count", int64(-1)) != 0 {
			t.Errorf("empty restricted: %v", r)
		}
	}
	if n != 1 {
		t.Errorf("restricted empty → %d rows, want 1", n)
	}
}
