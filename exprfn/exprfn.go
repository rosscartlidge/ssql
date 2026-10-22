// Package exprfn holds tiny generic helpers called by Go code that ssql
// generates from expr-lang expressions (`-if-expr` / `-set-expr`). It exists
// so the generated code stays readable: only functions whose inline expansion
// would hurt readability live here — everything else is emitted as direct
// strings.*/math.* calls. Zero dependencies; everything inlinable.
package exprfn

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/bits"
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

// Hash is the expression language's hash(n[, seed]): a reproducible
// 32-bit mix of an integer, the same value in every lane and on every
// machine (DFC154 §6: Go's maphash is seeded per process and each SQL
// engine's hash() is its own, so neither could be the definition).
// Its use is spread without a file — `hash(n) % 1000` over `from range`
// gives a fixture, a demo or a benchmark its group keys and amounts —
// not security (md5/sha1/sha256 are the digests). The seed selects an
// independent stream, so two columns from one n do not correlate.
//
// The definition, which `generate sql` renders with &, >>, * and xor so
// that no 64-bit engine overflows (every product has a factor below
// 2^31): the 64-bit input folds to 32 bits (low word xor high word),
// xors with (fold(seed) + 1) * 0x27d4eb2f, then lowbias32 (Wellons,
// 2022: xorshift 16, * 0x21f0aaad, xorshift 15, * 0x735a2d97,
// xorshift 15). The result is in [0, 2^32).
func Hash(n, seed int64) int64 {
	x := fold32(n) ^ SeedTerm(seed)
	x ^= x >> 16
	x *= 0x21f0aaad
	x ^= x >> 15
	x *= 0x735a2d97
	x ^= x >> 15
	return int64(x)
}

// SeedTerm is the constant a seed contributes before the mix,
// (fold32(seed) + 1) * 0x27d4eb2f, which generate sql folds for a literal.
func SeedTerm(seed int64) uint32 { return (fold32(seed) + 1) * 0x27d4eb2f }

func fold32(v int64) uint32 {
	u := uint64(v)
	return uint32(u) ^ uint32(u>>32)
}

// MD5, SHA1 and SHA256 are the expression language's digests of a
// string, as lower-case hex — the one implementation behind the VM
// helpers and transpiled code.
func MD5(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func SHA1(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func SHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// OverflowError is the failure of an int64 +, - or * whose exact result
// does not fit. ssql's integers are exact to 2^63 and an overflow stops
// the pipeline, as it does in DuckDB, Postgres and DataFusion, instead of
// wrapping to a wrong number nobody is told about (DFC155 B). The lever
// is the data: a column that needs more than 63 bits is a float (lossy
// past 2^53) or, once DFC155 C ships, an int128.
type OverflowError struct {
	Op   string
	A, B int64
}

func (e *OverflowError) Error() string {
	return fmt.Sprintf("integer overflow: %d %s %d does not fit an int64", e.A, e.Op, e.B)
}

// CheckedAdd is a + b, or an *OverflowError when the sum does not fit.
func CheckedAdd(a, b int64) (int64, error) {
	s := a + b
	if (s^a)&(s^b) < 0 { // both operands' signs differ from the sum's
		return 0, &OverflowError{"+", a, b}
	}
	return s, nil
}

// CheckedSub is a - b, or an *OverflowError when the difference does not fit.
func CheckedSub(a, b int64) (int64, error) {
	d := a - b
	if (a^b)&(a^d) < 0 { // operands of different sign, result not a's sign
		return 0, &OverflowError{"-", a, b}
	}
	return d, nil
}

// CheckedMul is a * b, or an *OverflowError when the product does not fit.
// The 128-bit product's high word must be the sign extension of its low
// word; the unsigned bits.Mul64 is corrected to the signed high word by
// subtracting each negative operand's partner (no division, ~1 ns).
func CheckedMul(a, b int64) (int64, error) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if a < 0 {
		hi -= uint64(b)
	}
	if b < 0 {
		hi -= uint64(a)
	}
	if hi != uint64(int64(lo)>>63) {
		return 0, &OverflowError{"*", a, b}
	}
	return int64(lo), nil
}

// Add, Sub and Mul are the forms generated code and the aggregates call:
// the checked operation, with an overflow raised as a panic carrying the
// *OverflowError — the library's error channel for a data error met
// mid-stream, which ssql.Recover turns into the program's error (DFC142).
func Add(a, b int64) int64 {
	v, err := CheckedAdd(a, b)
	if err != nil {
		panic(err)
	}
	return v
}

// Sub is the checked a - b; see Add.
func Sub(a, b int64) int64 {
	v, err := CheckedSub(a, b)
	if err != nil {
		panic(err)
	}
	return v
}

// Mul is the checked a * b; see Add.
func Mul(a, b int64) int64 {
	v, err := CheckedMul(a, b)
	if err != nil {
		panic(err)
	}
	return v
}
