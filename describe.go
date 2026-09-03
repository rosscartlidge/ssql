package ssql

import (
	"fmt"
	"iter"
	"sort"
	"strconv"
)

// DescribeConfig selects which fields to profile; empty = every field
// seen, in first-seen order.
type DescribeConfig struct {
	Fields []string
}

// describeType names match the JSONL schema-header vocabulary
// (cmd/ssql/lib: TypeInt/TypeFloat/TypeString/TypeBool).
const (
	describeInt    = "int"
	describeFloat  = "float"
	describeString = "string"
	describeBool   = "bool"
)

// fieldStats accumulates one field's profile.
type fieldStats struct {
	count    int64 // non-missing values
	missing  int64 // absent, nil, or empty string
	distinct map[string]struct{}
	hasInt   bool
	hasFloat bool
	hasStr   bool
	hasBool  bool
	nums     []float64 // numeric values, for min/max/mean/median
}

// DescribeRecords profiles a record stream: one output record per
// field with field, type, count, missing, distinct, and — for numeric
// fields — min, max, mean, median (numeric stats are ABSENT on
// non-numeric fields, not zero). Exact everywhere (distinct is a set,
// median is the middle value or the mean of the two middles), so every
// backend agrees — `ssql describe`, and the DuckDB translation, share
// these definitions. A barrier: materializes per-field state, O(rows)
// memory for distinct sets and numeric values.
//
// Row order: the requested Fields order when given; otherwise fields
// sorted by name. (First-seen order would be lane-dependent — record
// iteration order differs between the CSV reader, generated record
// code, and SQL — so it cannot be the contract.)
//
// type is the most general kind seen: any string → "string"; else any
// float → "float"; else any int → "int"; else bool → "bool"; a field
// with no non-missing values is "string".
func DescribeRecords(records iter.Seq[Record], cfg DescribeConfig) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		order := append([]string(nil), cfg.Fields...)
		restrict := len(cfg.Fields) > 0
		stats := map[string]*fieldStats{}
		get := func(name string) *fieldStats {
			s, ok := stats[name]
			if !ok {
				s = &fieldStats{distinct: map[string]struct{}{}}
				stats[name] = s
				if !restrict {
					order = append(order, name)
				}
			}
			return s
		}
		var total int64
		want := map[string]bool{}
		for _, f := range cfg.Fields {
			want[f] = true
		}
		for r := range records {
			total++
			seen := map[string]bool{}
			for k, v := range r.All() {
				if restrict && !want[k] {
					continue
				}
				seen[k] = true
				get(k).observe(v)
			}
			// Fields known (restricted, or seen in earlier records) but
			// absent from this record are missing here.
			for _, f := range order {
				if !seen[f] {
					get(f).missing++
				}
			}
		}
		if !restrict {
			sort.Strings(order)
		}
		for _, f := range order {
			s := stats[f]
			if s == nil {
				s = &fieldStats{distinct: map[string]struct{}{}}
				s.missing = total
			}
			// Absent-in-early-records correction: a field first seen
			// at record k was missing in the k-1 records before it.
			if s.count+s.missing < total {
				s.missing = total - s.count
			}
			m := MakeMutableRecord().
				String("field", f).
				String("type", s.typeName()).
				Int("count", s.count).
				Int("missing", s.missing).
				Int("distinct", int64(len(s.distinct)))
			if len(s.nums) > 0 {
				sort.Float64s(s.nums)
				n := len(s.nums)
				sum := 0.0
				for _, x := range s.nums {
					sum += x
				}
				var median float64
				if n%2 == 1 {
					median = s.nums[n/2]
				} else {
					median = (s.nums[n/2-1] + s.nums[n/2]) / 2
				}
				if s.hasFloat {
					m = m.Float("min", s.nums[0]).Float("max", s.nums[n-1])
				} else {
					m = m.Int("min", int64(s.nums[0])).Int("max", int64(s.nums[n-1]))
				}
				m = m.Float("mean", sum/float64(n)).Float("median", median)
			}
			if !yield(m.Freeze()) {
				return
			}
		}
	}
}

func (s *fieldStats) observe(v any) {
	if v == nil {
		s.missing++
		return
	}
	switch x := v.(type) {
	case string:
		if x == "" {
			s.missing++
			return
		}
		s.hasStr = true
		s.distinct[x] = struct{}{}
	case int64:
		s.hasInt = true
		s.nums = append(s.nums, float64(x))
		s.distinct["i:"+strconv.FormatInt(x, 10)] = struct{}{}
	case int:
		s.hasInt = true
		s.nums = append(s.nums, float64(x))
		s.distinct["i:"+strconv.Itoa(x)] = struct{}{}
	case float64:
		s.hasFloat = true
		s.nums = append(s.nums, x)
		s.distinct["f:"+strconv.FormatFloat(x, 'g', -1, 64)] = struct{}{}
	case bool:
		s.hasBool = true
		if x {
			s.distinct["b:true"] = struct{}{}
		} else {
			s.distinct["b:false"] = struct{}{}
		}
	default:
		s.hasStr = true
		s.distinct[fmt.Sprintf("%v", x)] = struct{}{}
	}
	s.count++
}

func (s *fieldStats) typeName() string {
	switch {
	case s.hasStr:
		return describeString
	case s.hasFloat:
		return describeFloat
	case s.hasInt:
		return describeInt
	case s.hasBool:
		return describeBool
	}
	return describeString
}

// DescribeFilter is the filter-shaped form for generated code (the
// assembler composes stmt fragments via Chain).
func DescribeFilter(cfg DescribeConfig) Filter[Record, Record] {
	return func(in iter.Seq[Record]) iter.Seq[Record] {
		return DescribeRecords(in, cfg)
	}
}
