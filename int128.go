package ssql

import (
	"fmt"
	"math"
	"math/bits"
	"strconv"
)

// Int128 is a signed 128-bit integer: the `int128` column type (DFC155 C),
// DuckDB's HUGEINT. It is a comparable two-word value — a group-by key, a
// sort key, a typed struct field, no allocation — with checked arithmetic:
// a result that does not fit is an *Int128OverflowError, never a wrap,
// the rule every int64 operation already follows (DFC155 B).
//
// A column is int128 because it was declared so — `cast -type int128`, a
// source literal past int64, an integer sum — never because a value
// overflowed. Int64 beside Int128 widens to Int128; Int128 beside a
// float64 widens to float64. The zero value is 0.
type Int128 struct {
	hi int64
	lo uint64
}

// MaxInt128 and MinInt128 are the bounds, 2^127−1 and −2^127.
var (
	MaxInt128 = Int128{hi: math.MaxInt64, lo: math.MaxUint64}
	MinInt128 = Int128{hi: math.MinInt64}
)

// Int128From widens an int64.
func Int128From(v int64) Int128 { return Int128{hi: v >> 63, lo: uint64(v)} }

// Int128FromWords builds the value hi·2^64 + lo from its two's-complement
// words (Arrow's Decimal128 HighBits/LowBits).
func Int128FromWords(hi int64, lo uint64) Int128 { return Int128{hi: hi, lo: lo} }

// Words returns the two's-complement words, hi·2^64 + lo.
func (x Int128) Words() (hi int64, lo uint64) { return x.hi, x.lo }

// IsZero reports x == 0.
func (x Int128) IsZero() bool { return x.hi == 0 && x.lo == 0 }

// Sign returns −1, 0 or 1.
func (x Int128) Sign() int {
	switch {
	case x.hi < 0:
		return -1
	case x.hi == 0 && x.lo == 0:
		return 0
	}
	return 1
}

// Cmp compares x and y: −1, 0 or 1.
func (x Int128) Cmp(y Int128) int {
	if x.hi != y.hi {
		if x.hi < y.hi {
			return -1
		}
		return 1
	}
	if x.lo != y.lo {
		if x.lo < y.lo {
			return -1
		}
		return 1
	}
	return 0
}

// IsInt64 reports whether x fits an int64.
func (x Int128) IsInt64() bool {
	return (x.hi == 0 && x.lo <= math.MaxInt64) || (x.hi == -1 && x.lo >= 1<<63)
}

// Int64 returns x as an int64 and whether it fits.
func (x Int128) Int64() (int64, bool) {
	if !x.IsInt64() {
		return 0, false
	}
	return int64(x.lo), true
}

// MustInt64 narrows an int128 that is known to fit an int64 (a cast, a
// typed `int()`); one that does not is a *CastError, never a wrap.
func MustInt64(x Int128) int64 {
	v, ok := x.Int64()
	if !ok {
		panic(&CastError{Value: x, Target: FieldTypeInt})
	}
	return v
}

// Int128Min and Int128Max are the ordered extremes (min()/max()).
func Int128Min(a, b Int128) Int128 {
	if b.Cmp(a) < 0 {
		return b
	}
	return a
}

// Int128Max is the larger of a and b.
func Int128Max(a, b Int128) Int128 {
	if b.Cmp(a) > 0 {
		return b
	}
	return a
}

// Int128OverflowError is the failure of an Int128 operation whose exact
// result does not fit 128 bits, the sibling of exprfn.OverflowError.
type Int128OverflowError struct {
	Op   string
	A, B Int128
}

func (e *Int128OverflowError) Error() string {
	switch e.Op {
	case "neg":
		return fmt.Sprintf("integer overflow: -(%s) does not fit an int128", e.B)
	case "abs":
		return fmt.Sprintf("integer overflow: abs(%s) does not fit an int128", e.B)
	}
	return fmt.Sprintf("integer overflow: %s %s %s does not fit an int128", e.A, e.Op, e.B)
}

// errInt128DivideByZero is the error of x / 0 and x % 0.
type int128DivideByZero struct{}

func (int128DivideByZero) Error() string { return "integer divide by zero" }

// int128Overflow is the panic of the unchecked forms, kept out of line so
// Add, Sub and the others stay small enough to inline into a hot loop.
//
//go:noinline
func int128Overflow(op string, a, b Int128) {
	panic(&Int128OverflowError{Op: op, A: a, B: b})
}

// CheckedAdd is x + y, or an *Int128OverflowError.
func (x Int128) CheckedAdd(y Int128) (Int128, error) {
	lo, carry := bits.Add64(x.lo, y.lo, 0)
	hi := x.hi + y.hi + int64(carry)
	if (x.hi^hi)&(y.hi^hi) < 0 { // both operands' signs differ from the sum's
		return Int128{}, &Int128OverflowError{Op: "+", A: x, B: y}
	}
	return Int128{hi, lo}, nil
}

// Add is x + y; an overflow panics with an *Int128OverflowError.
func (x Int128) Add(y Int128) Int128 {
	lo, carry := bits.Add64(x.lo, y.lo, 0)
	hi := x.hi + y.hi + int64(carry)
	if (x.hi^hi)&(y.hi^hi) < 0 {
		int128Overflow("+", x, y)
	}
	return Int128{hi, lo}
}

// CheckedSub is x − y, or an *Int128OverflowError.
func (x Int128) CheckedSub(y Int128) (Int128, error) {
	lo, borrow := bits.Sub64(x.lo, y.lo, 0)
	hi := x.hi - y.hi - int64(borrow)
	if (x.hi^y.hi)&(x.hi^hi) < 0 { // operands of different sign, result not x's sign
		return Int128{}, &Int128OverflowError{Op: "-", A: x, B: y}
	}
	return Int128{hi, lo}, nil
}

// Sub is x − y; an overflow panics with an *Int128OverflowError.
func (x Int128) Sub(y Int128) Int128 {
	lo, borrow := bits.Sub64(x.lo, y.lo, 0)
	hi := x.hi - y.hi - int64(borrow)
	if (x.hi^y.hi)&(x.hi^hi) < 0 {
		int128Overflow("-", x, y)
	}
	return Int128{hi, lo}
}

// CheckedNeg is −x, or an *Int128OverflowError (MinInt128 has no negation).
func (x Int128) CheckedNeg() (Int128, error) {
	if x == MinInt128 {
		return Int128{}, &Int128OverflowError{Op: "neg", B: x}
	}
	h, l := neg128(uint64(x.hi), x.lo)
	return Int128{int64(h), l}, nil
}

// Neg is −x; MinInt128 panics with an *Int128OverflowError.
func (x Int128) Neg() Int128 {
	if x == MinInt128 {
		int128Overflow("neg", Int128{}, x)
	}
	h, l := neg128(uint64(x.hi), x.lo)
	return Int128{int64(h), l}
}

// CheckedAbs is |x|, or an *Int128OverflowError for MinInt128.
func (x Int128) CheckedAbs() (Int128, error) {
	if x.hi >= 0 {
		return x, nil
	}
	if x == MinInt128 {
		return Int128{}, &Int128OverflowError{Op: "abs", B: x}
	}
	h, l := neg128(uint64(x.hi), x.lo)
	return Int128{int64(h), l}, nil
}

// Abs is |x|; MinInt128 panics with an *Int128OverflowError.
func (x Int128) Abs() Int128 {
	v, err := x.CheckedAbs()
	if err != nil {
		int128Overflow("abs", Int128{}, x)
	}
	return v
}

// CheckedMul is x · y, or an *Int128OverflowError.
func (x Int128) CheckedMul(y Int128) (Int128, error) {
	ah, al := mag128(x)
	bh, bl := mag128(y)
	neg := (x.hi < 0) != (y.hi < 0)
	if ah != 0 && bh != 0 {
		return Int128{}, &Int128OverflowError{Op: "*", A: x, B: y}
	}
	ph, pl := bits.Mul64(al, bl)
	var carry uint64
	if ah != 0 {
		ch, cl := bits.Mul64(ah, bl)
		ph, carry = bits.Add64(ph, cl, 0)
		if ch != 0 || carry != 0 {
			return Int128{}, &Int128OverflowError{Op: "*", A: x, B: y}
		}
	} else if bh != 0 {
		ch, cl := bits.Mul64(bh, al)
		ph, carry = bits.Add64(ph, cl, 0)
		if ch != 0 || carry != 0 {
			return Int128{}, &Int128OverflowError{Op: "*", A: x, B: y}
		}
	}
	r, ok := fromMag128(neg, ph, pl)
	if !ok {
		return Int128{}, &Int128OverflowError{Op: "*", A: x, B: y}
	}
	return r, nil
}

// Mul is x · y; an overflow panics with an *Int128OverflowError.
func (x Int128) Mul(y Int128) Int128 {
	r, err := x.CheckedMul(y)
	if err != nil {
		int128Overflow("*", x, y)
	}
	return r
}

// CheckedQuo is x / y truncated toward zero (Go's and every engine's
// integer division): a zero divisor is an error, and MinInt128 / −1 an
// *Int128OverflowError.
func (x Int128) CheckedQuo(y Int128) (Int128, error) {
	if y.IsZero() {
		return Int128{}, int128DivideByZero{}
	}
	if x == MinInt128 && y == Int128From(-1) {
		return Int128{}, &Int128OverflowError{Op: "/", A: x, B: y}
	}
	nh, nl := mag128(x)
	dh, dl := mag128(y)
	qh, ql, _, _ := divmod128(nh, nl, dh, dl)
	r, _ := fromMag128((x.hi < 0) != (y.hi < 0), qh, ql)
	return r, nil
}

// Quo is x / y truncated toward zero; a zero divisor or MinInt128 / −1 panics.
func (x Int128) Quo(y Int128) Int128 {
	r, err := x.CheckedQuo(y)
	if err != nil {
		panic(err)
	}
	return r
}

// CheckedRem is x % y with the sign of x (Go's and every engine's rule);
// a zero divisor is an error.
func (x Int128) CheckedRem(y Int128) (Int128, error) {
	if y.IsZero() {
		return Int128{}, int128DivideByZero{}
	}
	nh, nl := mag128(x)
	dh, dl := mag128(y)
	_, _, rh, rl := divmod128(nh, nl, dh, dl)
	r, _ := fromMag128(x.hi < 0, rh, rl)
	return r, nil
}

// Rem is x % y with the sign of x; a zero divisor panics.
func (x Int128) Rem(y Int128) Int128 {
	r, err := x.CheckedRem(y)
	if err != nil {
		panic(err)
	}
	return r
}

// Float64 is x rounded to the nearest float64, ties to even — the same
// value math/big gives, without the allocation. Exact to 2^53, as every
// integer-to-float conversion is.
func (x Int128) Float64() float64 {
	if v, ok := x.Int64(); ok {
		return float64(v)
	}
	neg := x.hi < 0
	h, l := mag128(x)
	var f float64
	if h == 0 {
		f = float64(l)
	} else {
		// 54 significant bits: 53 for the mantissa and one round bit;
		// the bits shifted out below them are the sticky bit.
		shift := 64 + bits.Len64(h) - 54
		var mant uint64
		var sticky bool
		if shift < 64 {
			mant = h<<(64-shift) | l>>shift
			sticky = l&(1<<shift-1) != 0
		} else {
			mant = h >> (shift - 64)
			sticky = l != 0 || h&(1<<(shift-64)-1) != 0
		}
		round := mant & 1
		mant >>= 1
		if round == 1 && (sticky || mant&1 == 1) {
			mant++ // 2^53 is still exact; Ldexp carries it
		}
		f = math.Ldexp(float64(mant), shift+1)
	}
	if neg {
		return -f
	}
	return f
}

// Int128FromFloat converts a whole float64 that fits; a fraction, a
// non-finite value or one past 2^127 reports false.
func Int128FromFloat(f float64) (Int128, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return Int128{}, false
	}
	if f > -(1<<63) && f < 1<<63 {
		return Int128From(int64(f)), true
	}
	neg := f < 0
	frac, exp := math.Frexp(math.Abs(f)) // |f| = frac · 2^exp, frac in [0.5, 1)
	mant := uint64(math.Ldexp(frac, 53)) // the 53 mantissa bits, exact
	shift := exp - 53                    // ≥ 11 here
	if bits.Len64(mant)+shift > 128 {
		return Int128{}, false
	}
	var h, l uint64
	if shift >= 64 {
		h = mant << (shift - 64)
	} else {
		h = mant >> (64 - shift)
		l = mant << shift
	}
	return fromMag128(neg, h, l)
}

// String is the decimal form, the same digits every engine prints.
func (x Int128) String() string {
	if v, ok := x.Int64(); ok {
		return strconv.FormatInt(v, 10)
	}
	neg := x.hi < 0
	h, l := mag128(x)
	var buf [40]byte
	i := len(buf)
	const chunk = 1e19 // 19 digits at a time: two Div64 passes per chunk
	for h != 0 {
		qh, r1 := bits.Div64(0, h, chunk)
		ql, r := bits.Div64(r1, l, chunk)
		for k := 0; k < 19; k++ {
			i--
			buf[i] = byte('0' + r%10)
			r /= 10
		}
		h, l = qh, ql
	}
	for {
		i--
		buf[i] = byte('0' + l%10)
		l /= 10
		if l == 0 {
			break
		}
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ParseInt128 reads a decimal integer with an optional sign; the error is
// a *strconv.NumError with ErrSyntax or ErrRange, as ParseInt gives.
func ParseInt128(s string) (Int128, error) {
	digits := s
	neg := false
	if len(digits) > 0 && (digits[0] == '-' || digits[0] == '+') {
		neg = digits[0] == '-'
		digits = digits[1:]
	}
	if len(digits) == 0 {
		return Int128{}, &strconv.NumError{Func: "ParseInt128", Num: s, Err: strconv.ErrSyntax}
	}
	var h, l uint64
	for len(digits) > 0 {
		n := min(len(digits), 19)
		chunk := digits[:n]
		digits = digits[n:]
		c, err := strconv.ParseUint(chunk, 10, 64)
		if err != nil {
			return Int128{}, &strconv.NumError{Func: "ParseInt128", Num: s, Err: strconv.ErrSyntax}
		}
		var ok bool
		h, l, ok = mulAdd128(h, l, pow10[n], c)
		if !ok {
			return Int128{}, &strconv.NumError{Func: "ParseInt128", Num: s, Err: strconv.ErrRange}
		}
	}
	r, ok := fromMag128(neg, h, l)
	if !ok {
		return Int128{}, &strconv.NumError{Func: "ParseInt128", Num: s, Err: strconv.ErrRange}
	}
	return r, nil
}

// MustParseInt128 is ParseInt128 for a literal the caller has validated
// (generated code); a bad literal panics.
func MustParseInt128(s string) Int128 {
	v, err := ParseInt128(s)
	if err != nil {
		panic(err)
	}
	return v
}

// MarshalJSON writes the bare number, as DuckDB writes a HUGEINT; JSON
// has no integer width.
func (x Int128) MarshalJSON() ([]byte, error) { return []byte(x.String()), nil }

// UnmarshalJSON reads a bare number or a quoted digit string (the form
// `duckdb -json` gives a HUGEINT).
func (x *Int128) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := ParseInt128(s)
	if err != nil {
		return err
	}
	*x = v
	return nil
}

// MarshalText and UnmarshalText give encoding/csv-style encoders the
// decimal form.
func (x Int128) MarshalText() ([]byte, error) { return []byte(x.String()), nil }

// UnmarshalText reads the decimal form.
func (x *Int128) UnmarshalText(b []byte) error {
	v, err := ParseInt128(string(b))
	if err != nil {
		return err
	}
	*x = v
	return nil
}

// asInt128 widens an integer of either width (int64, the VM's int, or an
// Int128); ok is false for anything else.
func asInt128(v any) (Int128, bool) {
	switch x := v.(type) {
	case Int128:
		return x, true
	case int64:
		return Int128From(x), true
	case int:
		return Int128From(int64(x)), true
	}
	return Int128{}, false
}

// exactIntCmp compares two integers of either width exactly, where a
// float64 detour would round past 2^53; ok is false unless both sides
// are integers. The fast path is the int64 pair every comparison had.
func exactIntCmp(a, b any) (int, bool) {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			switch {
			case x < y:
				return -1, true
			case x > y:
				return 1, true
			}
			return 0, true
		}
	}
	x, ok := asInt128(a)
	if !ok {
		return 0, false
	}
	y, ok := asInt128(b)
	if !ok {
		return 0, false
	}
	return x.Cmp(y), true
}

// pow10[n] is 10^n for the chunked parse (n ≤ 19).
var pow10 = [...]uint64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19}

// --- unsigned 128-bit helpers on (hi, lo) word pairs ---

// neg128 is the two's-complement negation of (h, l).
func neg128(h, l uint64) (uint64, uint64) {
	nl, carry := bits.Add64(^l, 1, 0)
	return ^h + carry, nl
}

// mag128 is |x| as an unsigned pair.
func mag128(x Int128) (h, l uint64) {
	if x.hi < 0 {
		return neg128(uint64(x.hi), x.lo)
	}
	return uint64(x.hi), x.lo
}

// fromMag128 applies a sign to a magnitude; false when it does not fit
// (a positive magnitude of 2^127 or more, a negative one past 2^127).
func fromMag128(neg bool, h, l uint64) (Int128, bool) {
	if h > 1<<63 || (h == 1<<63 && (l != 0 || !neg)) {
		return Int128{}, false
	}
	if neg {
		h, l = neg128(h, l)
	}
	return Int128{int64(h), l}, true
}

// mulAdd128 is (h, l) · m + c, false on overflow of 128 bits.
func mulAdd128(h, l, m, c uint64) (uint64, uint64, bool) {
	lh, ll := bits.Mul64(l, m)
	hh, hl := bits.Mul64(h, m)
	if hh != 0 {
		return 0, 0, false
	}
	nh, carry := bits.Add64(lh, hl, 0)
	if carry != 0 {
		return 0, 0, false
	}
	nl, c1 := bits.Add64(ll, c, 0)
	nh, c2 := bits.Add64(nh, 0, c1)
	if c2 != 0 {
		return 0, 0, false
	}
	return nh, nl, true
}

// divmod128 divides the unsigned pair (nh, nl) by (dh, dl), d ≠ 0,
// returning quotient and remainder pairs. A one-word divisor takes two
// Div64 passes; a two-word divisor (rare) takes a 128-step long division.
func divmod128(nh, nl, dh, dl uint64) (qh, ql, rh, rl uint64) {
	if dh == 0 {
		if nh < dl {
			ql, rl = bits.Div64(nh, nl, dl)
			return 0, ql, 0, rl
		}
		var r1 uint64
		qh, r1 = bits.Div64(0, nh, dl)
		ql, rl = bits.Div64(r1, nl, dl)
		return qh, ql, 0, rl
	}
	// Two-word divisor: the quotient fits one word. Shift-subtract over
	// the dividend's bits, most significant first.
	for i := 127; i >= 0; i-- {
		// r = r<<1 | bit i of n
		rh = rh<<1 | rl>>63
		rl <<= 1
		if i >= 64 {
			rl |= nh >> (i - 64) & 1
		} else {
			rl |= nl >> i & 1
		}
		if rh > dh || (rh == dh && rl >= dl) {
			var borrow uint64
			rl, borrow = bits.Sub64(rl, dl, 0)
			rh = rh - dh - borrow
			if i >= 64 {
				qh |= 1 << (i - 64)
			} else {
				ql |= 1 << i
			}
		}
	}
	return qh, ql, rh, rl
}
