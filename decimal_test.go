package ssql

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// The oracle is math/big: a Decimal is the rational units/10^scale, and
// every rule here (alignment, half-away rounding, the 38-digit bound) is
// restated on big.Int.

var bigDecBound = new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)

func decToRat(d Decimal) *big.Rat {
	return new(big.Rat).SetFrac(toBig(d.Units()), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d.Scale())), nil))
}

// bigRescale is units at scale from → to on big.Int, half away from zero.
func bigRescale(units *big.Int, from, to int) *big.Int {
	if to >= from {
		return new(big.Int).Mul(units, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(to-from)), nil))
	}
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(from-to)), nil)
	q, r := new(big.Int).QuoRem(units, p, new(big.Int))
	if new(big.Int).Abs(r).Mul(new(big.Int).Abs(r), big.NewInt(2)).Cmp(p) >= 0 {
		q.Add(q, big.NewInt(int64(units.Sign())))
	}
	return q
}

func fitsDec(u *big.Int) bool { return new(big.Int).Abs(u).Cmp(bigDecBound) < 0 }

func mustDec(t testing.TB, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, err)
	}
	return d
}

var decimalEdges = func() []Decimal {
	var out []Decimal
	for _, s := range []string{"0", "0.0", "0.00", "1", "-1", "0.1", "0.10", "-0.05", "12.34", "-12.34", "2.5", "-2.5", "0.125", "0.135",
		"1.005", "2.675", "99999999999999999999999999999999999999", "-99999999999999999999999999999999999999",
		"9999999999999999999999999999999999.9999", "0.00000000000000000000000000000000000001",
		"9223372036854775807", "9223372036854775808", "-9223372036854775808", "90071992547409.92", "1234567890.123456789",
		"170141183460469231731687303715884105.72", "0.5", "1.5", "-1.5", "3.375", "100", "1.00", "1.000"} {
		out = append(out, MustParseDecimal(s))
	}
	return out
}()

func randDecimal(r *rand.Rand) Decimal {
	n := 1 + r.IntN(38)
	var sb strings.Builder
	if r.IntN(2) == 0 {
		sb.WriteByte('-')
	}
	for i := 0; i < n; i++ {
		sb.WriteByte(byte('0' + r.IntN(10)))
	}
	u := MustParseInt128(sb.String())
	return MustNewDecimal(u, r.IntN(39))
}

func TestDecimalArithmeticAgainstBig(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	pairs := make([][2]Decimal, 0, 200000+len(decimalEdges)*len(decimalEdges))
	for _, a := range decimalEdges {
		for _, b := range decimalEdges {
			pairs = append(pairs, [2]Decimal{a, b})
		}
	}
	for i := 0; i < 200000; i++ {
		pairs = append(pairs, [2]Decimal{randDecimal(r), randDecimal(r)})
	}
	for _, p := range pairs {
		a, b := p[0], p[1]
		ra, rb := decToRat(a), decToRat(b)
		scale := max(a.Scale(), b.Scale())
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
		// add / sub at the aligned scale
		for _, op := range []struct {
			name string
			f    func(Decimal, Decimal) (Decimal, error)
			rat  func(*big.Rat, *big.Rat) *big.Rat
		}{
			{"+", Decimal.CheckedAdd, func(x, y *big.Rat) *big.Rat { return new(big.Rat).Add(x, y) }},
			{"-", Decimal.CheckedSub, func(x, y *big.Rat) *big.Rat { return new(big.Rat).Sub(x, y) }},
		} {
			want := op.rat(ra, rb)
			wantUnits := new(big.Int).Mul(want.Num(), pow)
			wantUnits.Quo(wantUnits, want.Denom())
			got, err := op.f(a, b)
			// An operand that does not fit at the common scale is an overflow
			// too (DuckDB cannot bind DECIMAL(38,0) beside DECIMAL(38,38) either).
			aligned := fitsDec(bigRescale(toBig(a.Units()), a.Scale(), scale)) && fitsDec(bigRescale(toBig(b.Units()), b.Scale(), scale))
			if !fitsDec(wantUnits) || !aligned {
				if err == nil {
					t.Fatalf("%s %s %s: want overflow, got %s", a, op.name, b, got)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s %s %s: %v", a, op.name, b, err)
			}
			if got.Scale() != scale || toBig(got.Units()).Cmp(wantUnits) != 0 {
				t.Fatalf("%s %s %s = %s (scale %d), want units %s at %d", a, op.name, b, got, got.Scale(), wantUnits, scale)
			}
		}
		// mul at the summed scale
		{
			got, err := a.CheckedMul(b)
			wantUnits := new(big.Int).Mul(toBig(a.Units()), toBig(b.Units()))
			s := a.Scale() + b.Scale()
			if s > MaxDecimalScale || !fitsDec(wantUnits) {
				if err == nil {
					t.Fatalf("%s * %s: want overflow, got %s", a, b, got)
				}
			} else {
				if err != nil {
					t.Fatalf("%s * %s: %v", a, b, err)
				}
				if got.Scale() != s || toBig(got.Units()).Cmp(wantUnits) != 0 {
					t.Fatalf("%s * %s = %s, want units %s at %d", a, b, got, wantUnits, s)
				}
			}
		}
		// cmp
		if got, want := a.Cmp(b), ra.Cmp(rb); got != want {
			t.Fatalf("%s cmp %s = %d, want %d", a, b, got, want)
		}
		// rem
		if !b.IsZero() {
			ua, ub := bigRescale(toBig(a.Units()), a.Scale(), scale), bigRescale(toBig(b.Units()), b.Scale(), scale)
			if fitsDec(ua) && fitsDec(ub) {
				got, err := a.CheckedRem(b)
				if err != nil {
					t.Fatalf("%s %% %s: %v", a, b, err)
				}
				want := new(big.Int).Rem(ua, ub)
				if toBig(got.Units()).Cmp(want) != 0 || got.Scale() != scale {
					t.Fatalf("%s %% %s = %s, want units %s", a, b, got, want)
				}
			}
		}
	}
}

func TestDecimalRescaleRounding(t *testing.T) {
	for _, c := range []struct {
		in    string
		scale int
		want  string
	}{
		{"0.125", 2, "0.13"}, {"0.135", 2, "0.14"}, {"-0.125", 2, "-0.13"}, {"2.5", 0, "3"}, {"-2.5", 0, "-3"},
		{"3.5", 0, "4"}, {"12.345", 2, "12.35"}, {"-5.005", 2, "-5.01"}, {"1.005", 2, "1.01"}, {"2.675", 2, "2.68"},
		{"1.255", 2, "1.26"}, {"-1.255", 2, "-1.26"}, {"0.4999", 0, "0"}, {"-0.4999", 0, "0"}, {"1.5", 3, "1.500"},
		{"1", 2, "1.00"}, {"0", 3, "0.000"}, {"-0.001", 2, "0.00"}, {"-0.005", 2, "-0.01"},
	} {
		got, err := mustDec(t, c.in).Rescale(c.scale)
		if err != nil {
			t.Fatalf("%s at %d: %v", c.in, c.scale, err)
		}
		if got.String() != c.want {
			t.Errorf("%s at %d = %s, want %s", c.in, c.scale, got, c.want)
		}
	}
	// Against big on random values.
	r := rand.New(rand.NewPCG(3, 5))
	for i := 0; i < 100000; i++ {
		d := randDecimal(r)
		s := r.IntN(39)
		want := bigRescale(toBig(d.Units()), d.Scale(), s)
		got, err := d.Rescale(s)
		if !fitsDec(want) {
			if err == nil {
				t.Fatalf("%s at %d: want overflow, got %s", d, s, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s at %d: %v", d, s, err)
		}
		if toBig(got.Units()).Cmp(want) != 0 {
			t.Fatalf("%s at %d = %s, want units %s", d, s, got, want)
		}
	}
	// Overflow on the way up names the value and the scale.
	_, err := mustDec(t, "99999999999999999999999999999999999999").Rescale(1)
	var oe *DecimalOverflowError
	if !errors.As(err, &oe) || !strings.Contains(err.Error(), "does not fit DECIMAL(38,1)") {
		t.Errorf("rescale overflow: %v", err)
	}
}

func TestDecimalPanickingFormsAndMessages(t *testing.T) {
	big1 := mustDec(t, "999999999999999999999999999999999999.99") // 38 digits
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			r := recover()
			if r == nil {
				t.Errorf("%s: no panic", name)
				return
			}
			err, ok := r.(error)
			if !ok || !strings.Contains(err.Error(), "decimal overflow") {
				t.Errorf("%s: panic %v", name, r)
			}
		}()
		f()
	}
	mustPanic("Add", func() { big1.Add(big1) })
	mustPanic("Sub", func() { big1.Neg().Sub(big1) })
	mustPanic("Mul", func() { big1.Mul(mustDec(t, "10")) })
	mustPanic("Add aligned", func() { big1.Add(mustDec(t, "0.001")) })
	mustPanic("Mul scale", func() { mustDec(t, "1.0000000000000000000").Mul(mustDec(t, "1.00000000000000000000")) })

	if _, err := big1.CheckedAdd(big1); err == nil || err.Error() != "decimal overflow: 999999999999999999999999999999999999.99 + 999999999999999999999999999999999999.99 does not fit DECIMAL(38,2)" {
		t.Errorf("add message: %v", err)
	}
	a, b := mustDec(t, "1.0000000000000000000"), mustDec(t, "1.00000000000000000000")
	if _, err := a.CheckedMul(b); err == nil || !strings.Contains(err.Error(), "needs scale 39, past DECIMAL(38)") {
		t.Errorf("scale message: %v", err)
	}
	if _, err := mustDec(t, "1.5").CheckedRem(Decimal{}); err == nil || err.Error() != "integer divide by zero" {
		t.Errorf("rem by zero: %v", err)
	}
	if _, err := NewDecimal(Int128From(1), 39); err == nil {
		t.Error("scale 39 accepted")
	}
	if _, err := NewDecimal(decimalBound, 0); err == nil {
		t.Error("10^38 accepted")
	}
}

func TestDecimalStringParseAndJSON(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"12.34", "12.34"}, {"-12.34", "-12.34"}, {"0.10", "0.10"}, {"-0.05", "-0.05"}, {"5", "5"}, {"+5.0", "5.0"},
		{"007.50", "7.50"}, {".5", "0.5"}, {"5.", "5"}, {"-0.00", "0.00"}, {"1e2", "100"}, {"1e-3", "0.001"}, {"1.5E1", "15"},
		{"12.345e-1", "1.2345"}, {"99999999999999999999999999999999999999", "99999999999999999999999999999999999999"},
		{"0.00000000000000000000000000000000000001", "0.00000000000000000000000000000000000001"},
	} {
		d, err := ParseDecimal(c.in)
		if err != nil {
			t.Errorf("ParseDecimal(%q): %v", c.in, err)
			continue
		}
		if got := d.String(); got != c.want {
			t.Errorf("ParseDecimal(%q).String() = %q, want %q", c.in, got, c.want)
		}
		back, err := ParseDecimal(d.String())
		if err != nil || back != d {
			t.Errorf("round trip %q: %v %v", c.in, back, err)
		}
	}
	for _, bad := range []string{"", "-", ".", "1.2.3", "abc", "1,5", " 1", "1e", "0x10", "1e1e1"} {
		if _, err := ParseDecimal(bad); err == nil {
			t.Errorf("ParseDecimal(%q): no error", bad)
		} else if ne := new(strconv.NumError); !errors.As(err, &ne) || ne.Err != strconv.ErrSyntax {
			t.Errorf("ParseDecimal(%q): %v, want ErrSyntax", bad, err)
		}
	}
	for _, wide := range []string{"100000000000000000000000000000000000000", "1e39", "0.000000000000000000000000000000000000001", "1e-39"} {
		if _, err := ParseDecimal(wide); err == nil {
			t.Errorf("ParseDecimal(%q): no error", wide)
		} else if ne := new(strconv.NumError); !errors.As(err, &ne) || ne.Err != strconv.ErrRange {
			t.Errorf("ParseDecimal(%q): %v, want ErrRange", wide, err)
		}
	}
	// ParseDecimalScale rounds; the scale is the declared one.
	if d, err := ParseDecimalScale("12.345", 2); err != nil || d.String() != "12.35" || d.Scale() != 2 {
		t.Errorf("ParseDecimalScale: %v %v", d, err)
	}
	if d, err := ParseDecimalScale("7", 2); err != nil || d.String() != "7.00" {
		t.Errorf("ParseDecimalScale int: %v %v", d, err)
	}
	// JSON: bare out, bare or quoted in.
	d := mustDec(t, "0.10")
	b, err := json.Marshal(map[string]any{"p": d})
	if err != nil || string(b) != `{"p":0.10}` {
		t.Errorf("marshal: %s %v", b, err)
	}
	for _, in := range []string{`{"p":0.10}`, `{"p":"0.10"}`} {
		var out struct{ P Decimal }
		if err := json.Unmarshal([]byte(in), &out); err != nil || out.P != d {
			t.Errorf("unmarshal %s: %v %v", in, out.P, err)
		}
	}
	txt, _ := d.MarshalText()
	var back Decimal
	if err := back.UnmarshalText(txt); err != nil || back != d {
		t.Errorf("text round trip: %s %v", txt, err)
	}
}

func TestDecimalConversions(t *testing.T) {
	// Float64 is the nearest float, pinned against big.Rat.
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20000; i++ {
		d := randDecimal(r)
		want, _ := decToRat(d).Float64()
		if got := d.Float64(); got != want {
			t.Fatalf("%s.Float64() = %v, want %v", d, got, want)
		}
	}
	tenth := 0.1 // a variable: 0.1 + 0.2 as constants folds to 0.3 exactly
	inexact := tenth + 0.2
	for _, c := range []struct {
		f     float64
		scale int
		want  string
	}{
		{1.005, 2, "1.01"}, {2.675, 2, "2.68"}, {inexact, 2, "0.30"}, {inexact, 17, "0.30000000000000004"}, {-2.5, 0, "-3"},
		{1e21, 0, "1000000000000000000000"}, {1234.5, 2, "1234.50"}, {0, 3, "0.000"},
	} {
		got, ok := DecimalFromFloat(c.f, c.scale)
		if !ok || got.String() != c.want {
			t.Errorf("DecimalFromFloat(%v, %d) = %v %v, want %s", c.f, c.scale, got, ok, c.want)
		}
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e40} {
		if _, ok := DecimalFromFloat(f, 2); ok {
			t.Errorf("DecimalFromFloat(%v) accepted", f)
		}
	}
	// Trunc toward zero, Int64 only when whole, Floor and Ceil at scale 0.
	for _, c := range []struct {
		in                 string
		trunc, floor, ceil string
		whole              bool
	}{
		{"-2.7", "-2", "-3", "-2", false}, {"2.7", "2", "2", "3", false}, {"5.00", "5", "5", "5", true}, {"-0.5", "0", "-1", "0", false}, {"0", "0", "0", "0", true},
	} {
		d := mustDec(t, c.in)
		if got := d.Trunc().String(); got != c.trunc {
			t.Errorf("%s.Trunc() = %s", c.in, got)
		}
		if fl, _ := d.Floor(); fl.String() != c.floor {
			t.Errorf("%s.Floor() = %s", c.in, fl)
		}
		if ce, _ := d.Ceil(); ce.String() != c.ceil {
			t.Errorf("%s.Ceil() = %s", c.in, ce)
		}
		if _, ok := d.Int64(); ok != c.whole {
			t.Errorf("%s.Int64() ok = %v", c.in, ok)
		}
	}
	if v, ok := mustDec(t, "9223372036854775807.00").Int64(); !ok || v != math.MaxInt64 {
		t.Errorf("Int64 max: %v %v", v, ok)
	}
	if _, ok := mustDec(t, "9223372036854775808").Int64(); ok {
		t.Error("Int64 past max accepted")
	}
	// Round, Normalized, Min/Max, Neg/Abs, Sign, from ints.
	if d, _ := mustDec(t, "2.345").Round(2); d.String() != "2.35" {
		t.Errorf("Round: %s", d)
	}
	for _, c := range []struct{ in, want string }{{"5.00", "5"}, {"1.50", "1.5"}, {"0.000", "0"}, {"12.34", "12.34"}, {"-1.0", "-1"}} {
		if got := mustDec(t, c.in).Normalized().String(); got != c.want {
			t.Errorf("%s.Normalized() = %s, want %s", c.in, got, c.want)
		}
	}
	a, b := mustDec(t, "1.25"), mustDec(t, "1.250")
	if a.Cmp(b) != 0 || a == b || a.Normalized() != b.Normalized() {
		t.Error("1.25 vs 1.250: equal values, distinct structs, equal normalized")
	}
	if DecimalMin(a, mustDec(t, "-3")).String() != "-3" || DecimalMax(a, mustDec(t, "1.3")).String() != "1.3" {
		t.Error("min/max")
	}
	if n := mustDec(t, "-0.05").Neg(); n.String() != "0.05" || n.Abs() != n || mustDec(t, "-0.05").Abs() != n || n.Sign() != 1 {
		t.Error("neg/abs/sign")
	}
	if DecimalFromInt64(5, 2).String() != "5.00" || MustDecimalFromInt128(Int128From(-7), 0).String() != "-7" {
		t.Error("from ints")
	}
	if _, err := DecimalFromInt128(MaxInt128, 0); err == nil {
		t.Error("MaxInt128 accepted as a decimal")
	}
	if d, ok := asDecimal(int64(3)); !ok || d.String() != "3" {
		t.Error("asDecimal int64")
	}
	if _, ok := asDecimal(1.5); ok {
		t.Error("asDecimal float")
	}
}

func BenchmarkDecimalAdd(b *testing.B) {
	x, y := MustParseDecimal("12345.67"), MustParseDecimal("0.01")
	var acc Decimal
	for i := 0; i < b.N; i++ {
		acc = acc.Add(y)
		if acc.Cmp(x) > 0 {
			acc = Decimal{}
		}
	}
	_ = acc
}

func BenchmarkDecimalString(b *testing.B) {
	d := MustParseDecimal("1234567890123.4567")
	for i := 0; i < b.N; i++ {
		_ = d.String()
	}
}
