package typed

import (
	"cmp"
	"slices"
	"strings"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// The spec constructors (2026-10-29): a generated program names each
// window function in one line — typed.AvgOf(func(r T) int64 { return r.N })
// — and the package builds the readers the runtime wants (the float
// reader beside the exact one for an integer field, DFC155 B/C; the
// nil check for a nullable field). Before, the template spelled every
// reader into the program: two closures per numeric function. Ross,
// 2026-10-29: the generated code must stay easy to read, with the
// complexity of optimisation in the packages. The struct literal form
// (WindowSpec{Kind: …, Num: …}) is still accepted.

// WindowNumber is a numeric field type a window function reads: the
// typed schema's int64, float64, int128 or decimal column.
type WindowNumber interface {
	int64 | float64 | ssql.Int128 | ssql.Decimal
}

// numericSpec fills the readers of a numeric spec from one getter.
func numericSpec[T any, N WindowNumber](kind WindowKind, get func(T) (N, bool)) WindowSpec[T] {
	s := WindowSpec[T]{Kind: kind}
	switch g := any(get).(type) {
	case func(T) (int64, bool):
		s.Num = func(r T) (float64, bool) { v, ok := g(r); return float64(v), ok }
		s.Int = g
	case func(T) (float64, bool):
		s.Num = g
	case func(T) (ssql.Int128, bool):
		s.Num = func(r T) (float64, bool) { v, ok := g(r); return v.Float64(), ok }
		s.Wide = g
	case func(T) (ssql.Decimal, bool):
		s.Num = func(r T) (float64, bool) { v, ok := g(r); return v.Float64(), ok }
		s.Dec = g
	}
	return s
}

func present[T any, N any](get func(T) N) func(T) (N, bool) {
	return func(r T) (N, bool) { return get(r), true }
}

func deref[T any, N any](get func(T) *N) func(T) (N, bool) {
	return func(r T) (N, bool) {
		if p := get(r); p != nil {
			return *p, true
		}
		var zero N
		return zero, false
	}
}

func valSpec[T any, V any](kind WindowKind, get func(T) (V, bool)) WindowSpec[T] {
	return WindowSpec[T]{Kind: kind, Val: func(r T) (any, bool) {
		v, ok := get(r)
		if !ok {
			return nil, false
		}
		return v, true
	}}
}

// The ranking functions: no field.
func RowNumber[T any]() WindowSpec[T]   { return WindowSpec[T]{Kind: WRowNumber} }
func Rank[T any]() WindowSpec[T]        { return WindowSpec[T]{Kind: WRank} }
func DenseRank[T any]() WindowSpec[T]   { return WindowSpec[T]{Kind: WDenseRank} }
func PercentRank[T any]() WindowSpec[T] { return WindowSpec[T]{Kind: WPercentRank} }
func CumeDist[T any]() WindowSpec[T]    { return WindowSpec[T]{Kind: WCumeDist} }
func Ntile[T any](n int) WindowSpec[T]  { return WindowSpec[T]{Kind: WNtile, N: n} }

// CountRows counts the frame's rows; CountOf the rows whose field is present.
func CountRows[T any]() WindowSpec[T] { return WindowSpec[T]{Kind: WCount} }
func CountOf[T any, V any](get func(T) V) WindowSpec[T] {
	return valSpec(WCountField, present(get))
}
func CountOfPtr[T any, V any](get func(T) *V) WindowSpec[T] {
	return valSpec(WCountField, deref(get))
}

// The numeric aggregates over the frame; the Ptr forms read a nullable
// field (absent values are skipped).
func SumOf[T any, N WindowNumber](get func(T) N) WindowSpec[T] {
	return numericSpec(WSum, present(get))
}
func SumOfPtr[T any, N WindowNumber](get func(T) *N) WindowSpec[T] {
	return numericSpec(WSum, deref(get))
}
func AvgOf[T any, N WindowNumber](get func(T) N) WindowSpec[T] {
	return numericSpec(WAvg, present(get))
}
func AvgOfPtr[T any, N WindowNumber](get func(T) *N) WindowSpec[T] {
	return numericSpec(WAvg, deref(get))
}

// The value functions over the frame: extremes, ends, offsets.
func MinOf[T any, V any](get func(T) V) WindowSpec[T]       { return valSpec(WMin, present(get)) }
func MinOfPtr[T any, V any](get func(T) *V) WindowSpec[T]   { return valSpec(WMin, deref(get)) }
func MaxOf[T any, V any](get func(T) V) WindowSpec[T]       { return valSpec(WMax, present(get)) }
func MaxOfPtr[T any, V any](get func(T) *V) WindowSpec[T]   { return valSpec(WMax, deref(get)) }
func FirstOf[T any, V any](get func(T) V) WindowSpec[T]     { return valSpec(WFirst, present(get)) }
func FirstOfPtr[T any, V any](get func(T) *V) WindowSpec[T] { return valSpec(WFirst, deref(get)) }
func LastOf[T any, V any](get func(T) V) WindowSpec[T]      { return valSpec(WLast, present(get)) }
func LastOfPtr[T any, V any](get func(T) *V) WindowSpec[T]  { return valSpec(WLast, deref(get)) }

// NthOf is the frame's n-th value (1-based); LagOf and LeadOf the value n
// rows before or after the current row, absent past the partition's edge
// unless WithDefault says otherwise.
func NthOf[T any, V any](get func(T) V, n int) WindowSpec[T] {
	s := valSpec(WNth, present(get))
	s.N = n
	return s
}
func NthOfPtr[T any, V any](get func(T) *V, n int) WindowSpec[T] {
	s := valSpec(WNth, deref(get))
	s.N = n
	return s
}
func LagOf[T any, V any](get func(T) V, n int) WindowSpec[T] {
	s := valSpec(WLag, present(get))
	s.N = n
	return s
}
func LagOfPtr[T any, V any](get func(T) *V, n int) WindowSpec[T] {
	s := valSpec(WLag, deref(get))
	s.N = n
	return s
}
func LeadOf[T any, V any](get func(T) V, n int) WindowSpec[T] {
	s := valSpec(WLead, present(get))
	s.N = n
	return s
}
func LeadOfPtr[T any, V any](get func(T) *V, n int) WindowSpec[T] {
	s := valSpec(WLead, deref(get))
	s.N = n
	return s
}

// WithDefault is the value a lag or lead yields past the partition's edge.
func (s WindowSpec[T]) WithDefault(v any) WindowSpec[T] {
	s.Default = v
	return s
}

// The statistics over the frame (typed.WAgg): the record aggregates'
// arithmetic, absent below two rows for the sample statistics.
func MedianOf[T any, N WindowNumber](get func(T) N) WindowSpec[T] {
	return PercentileOf(get, 0.5)
}
func MedianOfPtr[T any, N WindowNumber](get func(T) *N) WindowSpec[T] {
	return PercentileOfPtr(get, 0.5)
}
func PercentileOf[T any, N WindowNumber](get func(T) N, p float64) WindowSpec[T] {
	s := numericSpec(WAgg, present(get))
	s.Agg, s.P = WAggQuantile, p
	return s
}
func PercentileOfPtr[T any, N WindowNumber](get func(T) *N, p float64) WindowSpec[T] {
	s := numericSpec(WAgg, deref(get))
	s.Agg, s.P = WAggQuantile, p
	return s
}
func StdDevOf[T any, N WindowNumber](get func(T) N) WindowSpec[T] {
	s := numericSpec(WAgg, present(get))
	s.Agg = WAggStdDev
	return s
}
func StdDevOfPtr[T any, N WindowNumber](get func(T) *N) WindowSpec[T] {
	s := numericSpec(WAgg, deref(get))
	s.Agg = WAggStdDev
	return s
}
func VarianceOf[T any, N WindowNumber](get func(T) N) WindowSpec[T] {
	s := numericSpec(WAgg, present(get))
	s.Agg = WAggVariance
	return s
}
func VarianceOfPtr[T any, N WindowNumber](get func(T) *N) WindowSpec[T] {
	s := numericSpec(WAgg, deref(get))
	s.Agg = WAggVariance
	return s
}
func ModeOf[T any, V any](get func(T) V) WindowSpec[T] {
	s := valSpec(WAggMode.kind(), present(get))
	s.Agg = WAggMode
	return s
}
func ModeOfPtr[T any, V any](get func(T) *V) WindowSpec[T] {
	s := valSpec(WAggMode.kind(), deref(get))
	s.Agg = WAggMode
	return s
}

func (WindowAgg) kind() WindowKind { return WAgg }

// withComparators fills a clause's Compare from its typed order key when
// the program gave only the key (KeyString, KeyInt or KeyFloat, with
// Desc): the comparator a template used to spell beside the key is the
// key's own order, so the program no longer repeats it. Compare decides
// peers, the presorted order check and the comparator sort; the keyed
// sorts read the key directly.
func withComparators[T any](clauses []WindowClause[T]) []WindowClause[T] {
	out := slices.Clone(clauses)
	for i := range out {
		c := &out[i]
		if !c.HasOrder || c.Compare != nil {
			continue
		}
		desc := c.Desc
		switch {
		case c.KeyString != nil:
			k := c.KeyString
			c.Compare = func(a, b T) int { return flip(strings.Compare(k(a), k(b)), desc) }
		case c.KeyInt != nil:
			k := c.KeyInt
			c.Compare = func(a, b T) int { return flip(cmp.Compare(k(a), k(b)), desc) }
		case c.KeyFloat != nil:
			k := c.KeyFloat
			c.Compare = func(a, b T) int { return flip(compareFloatLikeGenerated(k(a), k(b)), desc) }
		}
	}
	return out
}

func flip(r int, desc bool) int {
	if desc {
		return -r
	}
	return r
}
