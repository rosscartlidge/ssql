package ssql

// Seeded random row sampling (DFC110). The selection of every row is a
// pure function of (seed, row index) computed by a SPEC-STABLE
// generator defined in this file — never math/rand, whose stream Go
// does not guarantee across versions. That purity is what lets a
// seeded sample be byte-identical across every backend (exec, record
// and typed codegen call these same functions), which is what the
// N-way equivalence gate asserts.

import (
	"iter"
	"sort"
)

// splitmix64 is the reference SplitMix64 output function (Steele,
// Lea, Flood 2014). Fixed forever: changing any constant changes
// every seeded sample ever taken.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// sampleHash draws the decision value for row i under seed: a uniform
// uint64 that is a pure function of (seed, i).
func sampleHash(seed int64, i int64) uint64 {
	return splitmix64(splitmix64(uint64(seed)) ^ uint64(i)*0x9E3779B97F4A7C15)
}

// SamplePercent keeps each row independently with probability p/100
// (Bernoulli sampling), streaming — row i survives iff
// sampleHash(seed, i) < p/100 · 2⁶⁴. Order is input order. p ≥ 100
// keeps everything.
func SamplePercent[T any](p float64, seed int64) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if p >= 100 {
				for v := range input {
					if !yield(v) {
						return
					}
				}
				return
			}
			threshold := uint64(p / 100 * float64(1<<63) * 2) // p/100 · 2⁶⁴, avoiding uint64 overflow at 100
			var i int64
			for v := range input {
				if sampleHash(seed, i) < threshold {
					if !yield(v) {
						return
					}
				}
				i++
			}
		}
	}
}

// SampleN keeps exactly n rows (or all rows when the input has fewer),
// chosen uniformly by reservoir sampling (Algorithm R with
// sampleHash-driven draws, so the result is deterministic under a
// seed). Rows are emitted in INPUT order — sample selects, it does
// not shuffle. The reservoir necessarily materializes n rows and
// emits only at end-of-input.
func SampleN[T any](n int, seed int64) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			type held struct {
				idx int64
				val T
			}
			reservoir := make([]held, 0, n)
			var i int64
			for v := range input {
				if len(reservoir) < n {
					reservoir = append(reservoir, held{i, v})
				} else {
					// Uniform j in [0, i]: replacement keeps each row
					// with probability n/(i+1) — Algorithm R.
					j := int64(sampleHash(seed, i) % uint64(i+1))
					if j < int64(n) {
						reservoir[j] = held{i, v}
					}
				}
				i++
			}
			sort.Slice(reservoir, func(a, b int) bool { return reservoir[a].idx < reservoir[b].idx })
			for _, h := range reservoir {
				if !yield(h.val) {
					return
				}
			}
		}
	}
}
