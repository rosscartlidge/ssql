// Package exprfn holds tiny generic helpers called by Go code that ssql
// generates from expr-lang expressions (`-if-expr` / `-set-expr`). It exists
// so the generated code stays readable: only functions whose inline expansion
// would hurt readability live here — everything else is emitted as direct
// strings.*/math.* calls. Zero dependencies; everything inlinable.
package exprfn

import "unicode/utf8"

// Abs is expr-lang's type-preserving abs(): int stays int, float stays float.
func Abs[T int64 | float64](v T) T {
	if v < 0 {
		return -v
	}
	return v
}

// RuneLen is expr-lang's len() on strings: rune count, not bytes
// (len("héllo") == 5).
func RuneLen(s string) int64 {
	return int64(utf8.RuneCountInString(s))
}

// --- Time bucketing (DFC121). One implementation for every lane:
// ssql.ResampleRecords, the expr VM's bucket(), and transpiled
// typed/record code all call these — the snap can never drift
// between backends.

// SnapNanos floors an epoch-nanosecond timestamp to its epoch-aligned
// bucket start (Euclidean floor: correct pre-1970).
func SnapNanos(ns, everyNanos int64) int64 {
	q := ns / everyNanos
	if ns%everyNanos != 0 && ns < 0 {
		q--
	}
	return q * everyNanos
}

// DetectEpochUnitNanos guesses an epoch value's unit by magnitude
// (~1.7e9 s, 1.7e12 ms, 1.7e15 µs, 1.7e18 ns for current dates) and
// returns the unit in nanoseconds.
func DetectEpochUnitNanos(v float64) int64 {
	if v < 0 {
		v = -v
	}
	switch {
	case v >= 1e17:
		return 1
	case v >= 1e14:
		return 1_000
	case v >= 1e11:
		return 1_000_000
	default:
		return 1_000_000_000
	}
}

// BucketInt64 snaps an int64 epoch timestamp (unit auto-detected per
// value) to its bucket start, returned in the SAME unit.
func BucketInt64(v int64, everyNanos int64) int64 {
	u := DetectEpochUnitNanos(float64(v))
	return SnapNanos(v*u, everyNanos) / u
}

// BucketFloat64 is BucketInt64 for float64 epochs.
func BucketFloat64(v float64, everyNanos int64) float64 {
	u := DetectEpochUnitNanos(v)
	return float64(SnapNanos(int64(v*float64(u)), everyNanos)) / float64(u)
}
