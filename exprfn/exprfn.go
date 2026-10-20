// Package exprfn holds tiny generic helpers called by Go code that ssql
// generates from expr-lang expressions (`-if-expr` / `-set-expr`). It exists
// so the generated code stays readable: only functions whose inline expansion
// would hurt readability live here — everything else is emitted as direct
// strings.*/math.* calls. Zero dependencies; everything inlinable.
package exprfn

import (
	"math"
	"regexp"
	"unicode/utf8"
)

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

// RegexMatch reports whether s matches pattern, compiled per call: the
// pattern is a row value (`where -if-field path regex pat`, DFC135), so
// there is nothing to hoist. An invalid pattern is false, as ssql.FieldOp
// treats it.
func RegexMatch(pattern, s string) bool {
	re, err := regexp.Compile(pattern)
	return err == nil && re.MatchString(s)
}

// --- Numeric functions (DFC153). One implementation for the expr VM and
// for transpiled code, so the lanes cannot drift.

// Sign is sign(x): -1, 0 or 1 as an int64, for an int or a float.
func Sign[T int64 | float64](v T) int64 {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

// RoundTo is round(x, n): half away from zero at n decimal places
// (round(2.5) is 3, round(-2.5) is -3, as DuckDB and math.Round).
func RoundTo(x float64, n int64) float64 {
	p := math.Pow(10, float64(n))
	return math.Round(x*p) / p
}

// Log is log(b, x): the logarithm of x in base b.
func Log(b, x float64) float64 {
	return math.Log(x) / math.Log(b)
}

// Lgamma is lgamma(x): the natural logarithm of the absolute value of
// the gamma function (math.Lgamma's first result).
func Lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}

// Degrees and Radians convert between the two angle units.
func Degrees(x float64) float64 { return x * 180 / math.Pi }
func Radians(x float64) float64 { return x * math.Pi / 180 }

// GCD and LCM are the greatest common divisor and least common multiple
// of two integers (non-negative results; gcd(0, 0) is 0, lcm with a zero
// is 0), as DuckDB and Postgres define them.
func GCD(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func LCM(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	l := a / GCD(a, b) * b
	if l < 0 {
		l = -l
	}
	return l
}

// Factorial is factorial(n) for an integer n: 1 for n < 2 (DuckDB gives
// 1 for a negative n too); exact in an int64 up to 20!, beyond which it
// overflows like any int64 product.
func Factorial(n int64) int64 {
	r := int64(1)
	for i := int64(2); i <= n; i++ {
		r *= i
	}
	return r
}
