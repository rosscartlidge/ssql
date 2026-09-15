package ssql

import "math"

// CompensatedSum is a floating-point accumulator that carries the
// rounding error of every addition in a second term and folds it back
// in at the end (Neumaier's variant of Kahan summation, which also
// handles a term larger than the running sum). A plain `+=` over a
// million floats drifts by many units in the last place; a compensated
// sum is accurate to about one. It is what -sum, -avg and the Welford
// variance use in every ssql lane; the typed codegen embeds this type
// for float columns (integer columns sum exactly in int64 and need
// nothing).
//
// It is NOT associative — a parallel merge can still differ from the
// serial pass in the last bit — but both land within an ulp of the true
// sum, so they agree far more often than plain addition does. DuckDB's
// SUM(DOUBLE) and even its kahan_sum lose the unit in
// [1e16, 1, -1e16]; Neumaier keeps it.
type CompensatedSum struct {
	Sum float64 // running sum
	C   float64 // accumulated compensation (lost low-order bits)
}

// Add folds x into the sum.
func (s *CompensatedSum) Add(x float64) {
	t := s.Sum + x
	if math.Abs(s.Sum) >= math.Abs(x) {
		s.C += (s.Sum - t) + x
	} else {
		s.C += (x - t) + s.Sum
	}
	s.Sum = t
}

// Merge folds another accumulator in (for shard merges): its sum and its
// compensation are each added with compensation, so the merge loses no
// more than an Add would.
func (s *CompensatedSum) Merge(o CompensatedSum) {
	s.Add(o.Sum)
	s.Add(o.C)
}

// Value is the compensated total.
func (s CompensatedSum) Value() float64 { return s.Sum + s.C }
