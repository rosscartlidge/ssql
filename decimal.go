package ssql

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Decimal is a fixed-point number — units × 10^-scale — the `decimal:S`
// column (DFC155 E): DuckDB's and DataFusion's DECIMAL(38,S), Postgres's
// NUMERIC(38,S), Parquet's Decimal128(38,S). The mantissa is an Int128
// kept under 10^38 (38 digits, the engines' precision), the scale the
// number of digits after the point, 0 to 38. Comparable (a group key, a
// join key, a typed struct field), no allocation; the zero value is 0 at
// scale 0.
//
// The rules every lane follows (measured on DuckDB 1.5.0): `+ - %`
// align to the larger scale; `*` adds the scales; `/` and an average are
// float64; a value brought to a smaller scale rounds half away from
// zero; a result past 38 digits is a *DecimalOverflowError naming the
// operands, never a wrap or a silent float.
type Decimal struct {
	units Int128
	scale uint8
}

// MaxDecimalScale is the largest scale a Decimal holds: the precision,
// 38 digits, all of them after the point.
const MaxDecimalScale = 38

// pow10Int128[n] is 10^n for n ≤ 38 (10^38 < 2^127); the uint64 table
// in int128.go stops at 10^19.
var pow10Int128 = func() (p [MaxDecimalScale + 1]Int128) {
	p[0] = Int128From(1)
	for i := 1; i < len(p); i++ {
		p[i] = p[i-1].Mul(Int128From(10))
	}
	return p
}()

// decimalBound is 10^38: |units| must stay below it.
var decimalBound = pow10Int128[MaxDecimalScale]

// decimalFits reports whether units is within ±(10^38 − 1).
func decimalFits(units Int128) bool {
	return units.Cmp(decimalBound) < 0 && units.Cmp(decimalBound.Neg()) > 0
}

// DecimalOverflowError is the failure of a Decimal operation whose exact
// result does not fit DECIMAL(38,Scale): the sibling of
// Int128OverflowError, naming the operands as every engine does.
type DecimalOverflowError struct {
	Op    string
	A, B  Decimal
	Scale int
}

func (e *DecimalOverflowError) Error() string {
	switch e.Op {
	case "rescale":
		return fmt.Sprintf("decimal overflow: %s does not fit DECIMAL(38,%d)", e.A, e.Scale)
	case "scale":
		return fmt.Sprintf("decimal overflow: %s * %s needs scale %d, past DECIMAL(38)", e.A, e.B, e.Scale)
	}
	return fmt.Sprintf("decimal overflow: %s %s %s does not fit DECIMAL(38,%d)", e.A, e.Op, e.B, e.Scale)
}

// decimalOverflow is the panic of the unchecked forms, kept out of line
// so Add, Sub and the others stay small enough to inline.
//
//go:noinline
func decimalOverflow(op string, a, b Decimal, scale int) {
	panic(&DecimalOverflowError{Op: op, A: a, B: b, Scale: scale})
}

// decimalScaleError is a scale outside 0..38.
func decimalScaleError(scale int) error {
	return fmt.Errorf("decimal scale %d is outside 0..%d", scale, MaxDecimalScale)
}

// NewDecimal is units × 10^-scale; an error when the scale is outside
// 0..38 or |units| is 10^38 or more.
func NewDecimal(units Int128, scale int) (Decimal, error) {
	if scale < 0 || scale > MaxDecimalScale {
		return Decimal{}, decimalScaleError(scale)
	}
	if !decimalFits(units) {
		return Decimal{}, &DecimalOverflowError{Op: "rescale", A: Decimal{units: units, scale: 0}, Scale: scale}
	}
	return Decimal{units: units, scale: uint8(scale)}, nil
}

// MustNewDecimal is NewDecimal for a value the caller has validated
// (generated code); a bad one panics.
func MustNewDecimal(units Int128, scale int) Decimal {
	d, err := NewDecimal(units, scale)
	if err != nil {
		panic(err)
	}
	return d
}

// DecimalFromInt64 is the integer v at the given scale, exactly: 5 at
// scale 2 is 5.00. It panics (a *DecimalOverflowError) only when
// v × 10^scale passes 38 digits.
func DecimalFromInt64(v int64, scale int) Decimal {
	d, err := DecimalFromInt128(Int128From(v), scale)
	if err != nil {
		panic(err)
	}
	return d
}

// DecimalFromInt128 is the integer w at the given scale, exactly, or an
// error when w × 10^scale passes 38 digits.
func DecimalFromInt128(w Int128, scale int) (Decimal, error) {
	d, err := NewDecimal(w, 0)
	if err != nil {
		return Decimal{}, err
	}
	return d.Rescale(scale)
}

// MustDecimalFromInt128 is DecimalFromInt128 for generated code; an
// overflow panics.
func MustDecimalFromInt128(w Int128, scale int) Decimal {
	d, err := DecimalFromInt128(w, scale)
	if err != nil {
		panic(err)
	}
	return d
}

// DecimalFromFloat is f at the given scale, rounding half away from zero
// from the float's shortest decimal form — "what you see": 1.005 is
// 1.01 at scale 2, where an engine rounding the binary value may say
// 1.00. ok is false for NaN, ±Inf, or a value past 38 digits.
func DecimalFromFloat(f float64, scale int) (Decimal, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Decimal{}, false
	}
	d, err := ParseDecimalScale(strconv.FormatFloat(f, 'f', -1, 64), scale)
	if err != nil {
		return Decimal{}, false
	}
	return d, true
}

// MustDecimalFromFloat is DecimalFromFloat for generated code; a value
// that is not a decimal panics.
func MustDecimalFromFloat(f float64, scale int) Decimal {
	d, ok := DecimalFromFloat(f, scale)
	if !ok {
		panic(fmt.Errorf("decimal: %v does not fit DECIMAL(38,%d)", f, scale))
	}
	return d
}

// Units is the mantissa: the value × 10^scale.
func (d Decimal) Units() Int128 { return d.units }

// Scale is the number of digits after the point.
func (d Decimal) Scale() int { return int(d.scale) }

// IsZero reports whether the value is 0 at any scale.
func (d Decimal) IsZero() bool { return d.units.IsZero() }

// Sign is −1, 0 or 1.
func (d Decimal) Sign() int { return d.units.Sign() }

// Rescale is the same value at another scale: up is exact, down rounds
// half away from zero (0.125 → 0.13, 2.5 → 3, −2.5 → −3, as DuckDB
// casts). The error is a *DecimalOverflowError when the value does not
// fit 38 digits at the new scale.
func (d Decimal) Rescale(scale int) (Decimal, error) {
	if scale < 0 || scale > MaxDecimalScale {
		return Decimal{}, decimalScaleError(scale)
	}
	switch {
	case scale == int(d.scale):
		return d, nil
	case scale > int(d.scale):
		u, err := d.units.CheckedMul(pow10Int128[scale-int(d.scale)])
		if err != nil || !decimalFits(u) {
			return Decimal{}, &DecimalOverflowError{Op: "rescale", A: d, Scale: scale}
		}
		return Decimal{units: u, scale: uint8(scale)}, nil
	}
	p := pow10Int128[int(d.scale)-scale]
	q := d.units.Quo(p)                         // toward zero; |p| ≤ 10^38 so neither can overflow
	r := d.units.Rem(p)                         // the sign of d
	if r.Abs().Cmp(p.Quo(Int128From(2))) >= 0 { // |r| ≥ p/2 (p is even): away from zero; 2|r| could pass 2^127
		if d.units.Sign() < 0 {
			q = q.Sub(Int128From(1))
		} else {
			q = q.Add(Int128From(1))
		}
	}
	return Decimal{units: q, scale: uint8(scale)}, nil
}

// MustRescale is Rescale for generated code; an overflow panics.
func (d Decimal) MustRescale(scale int) Decimal {
	r, err := d.Rescale(scale)
	if err != nil {
		panic(err)
	}
	return r
}

// align brings d and o to their larger scale.
func align(d, o Decimal) (Decimal, Decimal, error) {
	switch {
	case d.scale < o.scale:
		x, err := d.Rescale(int(o.scale))
		return x, o, err
	case d.scale > o.scale:
		y, err := o.Rescale(int(d.scale))
		return d, y, err
	}
	return d, o, nil
}

// CheckedAdd is d + o at the larger scale, or a *DecimalOverflowError.
func (d Decimal) CheckedAdd(o Decimal) (Decimal, error) {
	x, y, err := align(d, o)
	if err != nil {
		return Decimal{}, &DecimalOverflowError{Op: "+", A: d, B: o, Scale: max(d.Scale(), o.Scale())}
	}
	u, err := x.units.CheckedAdd(y.units)
	if err != nil || !decimalFits(u) {
		return Decimal{}, &DecimalOverflowError{Op: "+", A: d, B: o, Scale: x.Scale()}
	}
	return Decimal{units: u, scale: x.scale}, nil
}

// Add is d + o; an overflow panics with the *DecimalOverflowError.
func (d Decimal) Add(o Decimal) Decimal {
	if d.scale == o.scale { // the common case of a column: one add, one bound check
		u, err := d.units.CheckedAdd(o.units)
		if err == nil && decimalFits(u) {
			return Decimal{units: u, scale: d.scale}
		}
		decimalOverflow("+", d, o, d.Scale())
	}
	r, err := d.CheckedAdd(o)
	if err != nil {
		panic(err)
	}
	return r
}

// CheckedSub is d − o at the larger scale, or a *DecimalOverflowError.
func (d Decimal) CheckedSub(o Decimal) (Decimal, error) {
	x, y, err := align(d, o)
	if err != nil {
		return Decimal{}, &DecimalOverflowError{Op: "-", A: d, B: o, Scale: max(d.Scale(), o.Scale())}
	}
	u, err := x.units.CheckedSub(y.units)
	if err != nil || !decimalFits(u) {
		return Decimal{}, &DecimalOverflowError{Op: "-", A: d, B: o, Scale: x.Scale()}
	}
	return Decimal{units: u, scale: x.scale}, nil
}

// Sub is d − o; an overflow panics with the *DecimalOverflowError.
func (d Decimal) Sub(o Decimal) Decimal {
	if d.scale == o.scale {
		u, err := d.units.CheckedSub(o.units)
		if err == nil && decimalFits(u) {
			return Decimal{units: u, scale: d.scale}
		}
		decimalOverflow("-", d, o, d.Scale())
	}
	r, err := d.CheckedSub(o)
	if err != nil {
		panic(err)
	}
	return r
}

// CheckedMul is d × o at the sum of the scales (exact: 1.5 × 2.25 is
// 3.375), or a *DecimalOverflowError when the scale passes 38 or the
// digits do.
func (d Decimal) CheckedMul(o Decimal) (Decimal, error) {
	scale := d.Scale() + o.Scale()
	if scale > MaxDecimalScale {
		return Decimal{}, &DecimalOverflowError{Op: "scale", A: d, B: o, Scale: scale}
	}
	u, err := d.units.CheckedMul(o.units)
	if err != nil || !decimalFits(u) {
		return Decimal{}, &DecimalOverflowError{Op: "*", A: d, B: o, Scale: scale}
	}
	return Decimal{units: u, scale: uint8(scale)}, nil
}

// Mul is d × o; an overflow panics with the *DecimalOverflowError.
func (d Decimal) Mul(o Decimal) Decimal {
	r, err := d.CheckedMul(o)
	if err != nil {
		panic(err)
	}
	return r
}

// CheckedRem is d % o at the larger scale, with the sign of d (Go's and
// every engine's rule); a zero divisor is an error.
func (d Decimal) CheckedRem(o Decimal) (Decimal, error) {
	if o.IsZero() {
		return Decimal{}, int128DivideByZero{}
	}
	x, y, err := align(d, o)
	if err != nil {
		return Decimal{}, &DecimalOverflowError{Op: "%", A: d, B: o, Scale: max(d.Scale(), o.Scale())}
	}
	return Decimal{units: x.units.Rem(y.units), scale: x.scale}, nil
}

// Rem is d % o with the sign of d; a zero divisor panics.
func (d Decimal) Rem(o Decimal) Decimal {
	r, err := d.CheckedRem(o)
	if err != nil {
		panic(err)
	}
	return r
}

// Neg is −d; it cannot overflow (|units| < 10^38 on both sides of zero).
func (d Decimal) Neg() Decimal { return Decimal{units: d.units.Neg(), scale: d.scale} }

// Abs is |d|; it cannot overflow.
func (d Decimal) Abs() Decimal { return Decimal{units: d.units.Abs(), scale: d.scale} }

// Cmp compares the values exactly across scales (1.25 and 1.250 are
// equal): −1, 0 or 1. Alignment cannot change the answer: a mantissa
// that overflows 128 bits when scaled up is already past 10^38 in the
// other's units, so its sign decides.
func (d Decimal) Cmp(o Decimal) int {
	switch {
	case d.scale == o.scale:
		return d.units.Cmp(o.units)
	case d.scale < o.scale:
		u, err := d.units.CheckedMul(pow10Int128[o.scale-d.scale])
		if err != nil {
			return d.units.Sign()
		}
		return u.Cmp(o.units)
	}
	u, err := o.units.CheckedMul(pow10Int128[d.scale-o.scale])
	if err != nil {
		return -o.units.Sign()
	}
	return d.units.Cmp(u)
}

// DecimalMin is the smaller of a and b (a on a tie).
func DecimalMin(a, b Decimal) Decimal {
	if b.Cmp(a) < 0 {
		return b
	}
	return a
}

// DecimalMax is the larger of a and b (a on a tie).
func DecimalMax(a, b Decimal) Decimal {
	if b.Cmp(a) > 0 {
		return b
	}
	return a
}

// Trunc is the integer part, toward zero: int()'s and `cast -type int`'s
// rule for a float, kept for a decimal.
func (d Decimal) Trunc() Int128 { return d.units.Quo(pow10Int128[d.scale]) }

// IsWhole reports whether the value has no fractional part (5.00 is).
func (d Decimal) IsWhole() bool { return d.units.Rem(pow10Int128[d.scale]).IsZero() }

// wholeInt128 is the value as an Int128 when it is whole.
func (d Decimal) wholeInt128() (Int128, bool) {
	if !d.IsWhole() {
		return Int128{}, false
	}
	return d.Trunc(), true
}

// Int64 is the value as an int64 when it is whole and fits.
func (d Decimal) Int64() (int64, bool) {
	if !d.IsWhole() {
		return 0, false
	}
	return d.Trunc().Int64()
}

// decimalFromFloatExact is f by its shortest decimal form at that form's
// own scale (0.1 → 0.1, 1234.5 → 1234.5): the decimal a float "is".
func decimalFromFloatExact(f float64) (Decimal, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Decimal{}, false
	}
	d, err := ParseDecimal(strconv.FormatFloat(f, 'f', -1, 64))
	return d, err == nil
}

// Round is the value at scale n, half away from zero (2.345 → 2.35 at
// 2, 2.5 → 3 at 0); n larger than the scale is exact.
func (d Decimal) Round(n int) (Decimal, error) { return d.Rescale(n) }

// Floor is the largest whole value not above d, at scale 0 (it always
// fits: |⌊d⌋| ≤ |units|).
func (d Decimal) Floor() Decimal {
	q := d.Trunc()
	if d.units.Sign() < 0 && !d.IsWhole() {
		q = q.Sub(Int128From(1))
	}
	return Decimal{units: q}
}

// Ceil is the smallest whole value not below d, at scale 0.
func (d Decimal) Ceil() Decimal {
	q := d.Trunc()
	if d.units.Sign() > 0 && !d.IsWhole() {
		q = q.Add(Int128From(1))
	}
	return Decimal{units: q}
}

// Float64 is the nearest float64 (correctly rounded, as strconv
// guarantees): the value a float path or an average sees.
func (d Decimal) Float64() float64 {
	f, _ := strconv.ParseFloat(d.String(), 64)
	return f
}

// String is the plain decimal form with the scale's digits after the
// point — "12.34", "-0.05", "0.10", "5" at scale 0 — the text every
// engine prints and every reader parses.
func (d Decimal) String() string {
	if d.scale == 0 {
		return d.units.String()
	}
	mag := d.units.Abs().String()
	if len(mag) <= int(d.scale) {
		mag = strings.Repeat("0", int(d.scale)-len(mag)+1) + mag
	}
	var b strings.Builder
	b.Grow(len(mag) + 2)
	if d.units.Sign() < 0 {
		b.WriteByte('-')
	}
	b.WriteString(mag[:len(mag)-int(d.scale)])
	b.WriteByte('.')
	b.WriteString(mag[len(mag)-int(d.scale):])
	return b.String()
}

// Normalized is the same value with the trailing zeros of the fraction
// dropped (5.00 → 5, 1.50 → 1.5): the form hash keys use, so an int, a
// decimal:2 and a decimal:3 of one value key together.
func (d Decimal) Normalized() Decimal {
	for d.scale > 0 && d.units.Rem(Int128From(10)).IsZero() {
		d = Decimal{units: d.units.Quo(Int128From(10)), scale: d.scale - 1}
	}
	return d
}

// ParseDecimal reads a plain decimal — an optional sign, digits, an
// optional point and digits, an optional exponent — and takes its scale
// from the text: "12.34" is scale 2, "5" scale 0, "1e-3" scale 3. The
// error is a *strconv.NumError (ErrSyntax, or ErrRange past 38 digits).
func ParseDecimal(s string) (Decimal, error) {
	syntax := func() (Decimal, error) {
		return Decimal{}, &strconv.NumError{Func: "ParseDecimal", Num: s, Err: strconv.ErrSyntax}
	}
	rng := func() (Decimal, error) {
		return Decimal{}, &strconv.NumError{Func: "ParseDecimal", Num: s, Err: strconv.ErrRange}
	}
	rest := s
	neg := false
	if len(rest) > 0 && (rest[0] == '-' || rest[0] == '+') {
		neg = rest[0] == '-'
		rest = rest[1:]
	}
	exp := 0
	if i := strings.IndexAny(rest, "eE"); i >= 0 {
		e, err := strconv.Atoi(rest[i+1:])
		if err != nil {
			return syntax()
		}
		exp, rest = e, rest[:i]
	}
	intPart, frac, hasPoint := strings.Cut(rest, ".")
	if intPart == "" && frac == "" {
		return syntax()
	}
	digits := intPart + frac
	for _, c := range digits {
		if c < '0' || c > '9' {
			return syntax()
		}
	}
	_ = hasPoint
	scale := len(frac) - exp
	digits = strings.TrimLeft(digits, "0")
	if scale < 0 {
		if -scale > MaxDecimalScale || len(digits)-scale > MaxDecimalScale {
			return rng()
		}
		digits += strings.Repeat("0", -scale)
		scale = 0
	}
	if scale > MaxDecimalScale || len(digits) > MaxDecimalScale {
		return rng()
	}
	if digits == "" {
		return Decimal{scale: uint8(scale)}, nil
	}
	if neg {
		digits = "-" + digits
	}
	u, err := ParseInt128(digits)
	if err != nil {
		return rng()
	}
	return Decimal{units: u, scale: uint8(scale)}, nil
}

// ParseDecimalScale is ParseDecimal brought to the given scale, rounding
// half away from zero ("12.345" at scale 2 is 12.35) — what a declared
// decimal column does to the text it reads.
func ParseDecimalScale(s string, scale int) (Decimal, error) {
	d, err := ParseDecimal(s)
	if err != nil {
		return Decimal{}, err
	}
	return d.Rescale(scale)
}

// MustParseDecimal is ParseDecimal for a literal the caller has validated
// (generated code); a bad literal panics.
func MustParseDecimal(s string) Decimal {
	d, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

// MustParseDecimalScale is ParseDecimalScale for generated code; a bad
// literal or an overflow panics.
func MustParseDecimalScale(s string, scale int) Decimal {
	d, err := ParseDecimalScale(s, scale)
	if err != nil {
		panic(err)
	}
	return d
}

// MarshalJSON writes the plain decimal form as a bare number, as
// DuckDB's COPY TO json writes a DECIMAL.
func (d Decimal) MarshalJSON() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalJSON reads a bare number or the quoted form `duckdb -json`
// prints; the scale is the text's.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := ParseDecimal(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalText writes the plain decimal form.
func (d Decimal) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText reads the plain decimal form.
func (d *Decimal) UnmarshalText(b []byte) error {
	v, err := ParseDecimal(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// exactNumCmp compares two exact numbers — integers of either width, a
// Decimal — without a float64 detour; ok is false unless both sides are
// exact. The int64 pair is the fast path every comparison had.
func exactNumCmp(a, b any) (int, bool) {
	if c, ok := exactIntCmp(a, b); ok {
		return c, true
	}
	x, ok := asDecimal(a)
	if !ok {
		return 0, false
	}
	y, ok := asDecimal(b)
	if !ok {
		return 0, false
	}
	return x.Cmp(y), true
}

// asDecimal is v as a Decimal when it is one or an integer of either
// width (at scale 0); ok is false for anything else.
func asDecimal(v any) (Decimal, bool) {
	switch x := v.(type) {
	case Decimal:
		return x, true
	case int64:
		return Decimal{units: Int128From(x)}, true
	case int:
		return Decimal{units: Int128From(int64(x))}, true
	case Int128:
		if !decimalFits(x) {
			return Decimal{}, false
		}
		return Decimal{units: x}, true
	}
	return Decimal{}, false
}
