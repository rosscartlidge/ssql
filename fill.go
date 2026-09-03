package ssql

import "iter"

// FillDefault gives a field a constant when it is missing.
type FillDefault struct {
	Field string
	Value any
}

// FillConfig describes `ssql fill`: Down fields carry the last seen
// non-missing value forward over gaps (order-dependent); Defaults give
// a constant where a field is missing. Down is applied before
// Defaults, so a leading gap (nothing seen yet) takes the default.
// "Missing" is ssql's one definition (DFC124): absent, nil, or "".
type FillConfig struct {
	Down     []string
	Defaults []FillDefault
}

// isMissing is the DFC124 definition shared by describe/unpivot/fill.
func isMissing(v any, present bool) bool {
	return !present || v == nil || v == ""
}

// FillRecords carries values down and/or defaults them. Row-local
// except for the carried state, so it streams; Down makes it
// order-consuming (a sort before fill is live).
func FillRecords(records iter.Seq[Record], cfg FillConfig) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		last := map[string]any{}
		for r := range records {
			fields := map[string]any{}
			for k, v := range r.All() {
				fields[k] = v
			}
			m := r.ToMutable()
			for _, f := range cfg.Down {
				v, ok := fields[f]
				if isMissing(v, ok) {
					if carry, seen := last[f]; seen {
						m = setAnyField(m, f, carry)
						fields[f] = carry
					}
					continue
				}
				last[f] = v
			}
			for _, d := range cfg.Defaults {
				v, ok := fields[d.Field]
				if isMissing(v, ok) {
					m = setAnyField(m, d.Field, d.Value)
					fields[d.Field] = d.Value
				}
			}
			if !yield(m.Freeze()) {
				return
			}
		}
	}
}

// FillFilter is the filter-shaped form for generated code.
func FillFilter(cfg FillConfig) Filter[Record, Record] {
	return func(in iter.Seq[Record]) iter.Seq[Record] {
		return FillRecords(in, cfg)
	}
}
