package ssql

import (
	"fmt"
	"iter"
	"sort"
)

// UnpivotConfig describes a wide→long fold (SQL UNPIVOT; pandas melt):
// every IDs field is copied to each output row, each Values field
// becomes one output row carrying its name in NameField and its value
// in ValueField. Empty Values = every non-ID field of the record,
// sorted by name (record iteration order is lane-dependent, so the
// default has to be an order every backend can reproduce).
type UnpivotConfig struct {
	IDs        []string
	Values     []string
	NameField  string // default "name"
	ValueField string // default "value"
}

// UnpivotRecords folds wide records into long ones, the inverse of
// pivot: one output record per (input record, value field) pair, in
// input order then Values order. A MISSING value — absent, nil, or the
// empty string (ssql's one definition of missing, DFC124) — produces
// no row (SQL UNPIVOT's default; melt would emit NaN). Row-local — no
// buffering, order-preserving.
func UnpivotRecords(records iter.Seq[Record], cfg UnpivotConfig) iter.Seq[Record] {
	nameField, valueField := cfg.NameField, cfg.ValueField
	if nameField == "" {
		nameField = "name"
	}
	if valueField == "" {
		valueField = "value"
	}
	isID := map[string]bool{}
	for _, f := range cfg.IDs {
		isID[f] = true
	}
	return func(yield func(Record) bool) {
		for r := range records {
			fields := map[string]any{}
			for k, v := range r.All() {
				fields[k] = v
			}
			values := cfg.Values
			if len(values) == 0 {
				for k := range fields {
					if !isID[k] {
						values = append(values, k)
					}
				}
				sort.Strings(values)
			}
			for _, vf := range values {
				v, ok := fields[vf]
				if !ok || v == nil || v == "" {
					continue
				}
				m := MakeMutableRecord()
				for _, id := range cfg.IDs {
					if iv, ok := fields[id]; ok {
						m = setAnyField(m, id, iv)
					}
				}
				m = m.String(nameField, vf)
				m = setAnyField(m, valueField, v)
				if !yield(m.Freeze()) {
					return
				}
			}
		}
	}
}

// setAnyField writes v with the builder's typed setter for its dynamic
// type (canonical scalars int64/float64/string/bool; anything else is
// formatted).
func setAnyField(m MutableRecord, field string, v any) MutableRecord {
	switch x := v.(type) {
	case string:
		return m.String(field, x)
	case int64:
		return m.Int(field, x)
	case int:
		return m.Int(field, int64(x))
	case float64:
		return m.Float(field, x)
	case bool:
		return m.Bool(field, x)
	case Record:
		return m.Nested(field, x)
	default:
		return m.String(field, fmt.Sprintf("%v", x))
	}
}

// UnpivotFilter is the filter-shaped form for generated code.
func UnpivotFilter(cfg UnpivotConfig) Filter[Record, Record] {
	return func(in iter.Seq[Record]) iter.Seq[Record] {
		return UnpivotRecords(in, cfg)
	}
}
