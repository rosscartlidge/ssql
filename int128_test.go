package ssql

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"strconv"
	"testing"
)

// The oracle is math/big: the exact result fits 128 bits or it does not.

var (
	bigMin = new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
	bigMax = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
)

func toBig(x Int128) *big.Int {
	hi, lo := x.Words()
	b := new(big.Int).Lsh(big.NewInt(hi), 64)
	return b.Add(b, new(big.Int).SetUint64(lo))
}

func fits128(b *big.Int) bool { return b.Cmp(bigMin) >= 0 && b.Cmp(bigMax) <= 0 }

func fromBig(t testing.TB, b *big.Int) Int128 {
	t.Helper()
	v, err := ParseInt128(b.String())
	if err != nil {
		t.Fatalf("ParseInt128(%s): %v", b, err)
	}
	return v
}

var int128Edges = func() []Int128 {
	edges := []Int128{{}, MaxInt128, MinInt128,
		Int128From(1), Int128From(-1), Int128From(2), Int128From(-2), Int128From(7), Int128From(-7),
		Int128From(math.MaxInt64), Int128From(math.MinInt64), Int128From(math.MaxInt64 - 1), Int128From(math.MinInt64 + 1),
		Int128From(1 << 32), Int128From(-(1 << 32)), Int128From(1 << 62), Int128From(-(1 << 62)),
		Int128FromWords(0, 1<<63), Int128FromWords(0, math.MaxUint64), Int128FromWords(1, 0), Int128FromWords(-1, 0), Int128FromWords(-2, math.MaxUint64),
		Int128FromWords(1<<62, 0), Int128FromWords(-(1 << 62), 0), Int128FromWords(math.MaxInt64, 0), Int128FromWords(math.MinInt64, 1),
		Int128FromWords(1<<31, 1<<31), Int128FromWords(3, 5), Int128FromWords(-4, 9),
	}
	for _, s := range []string{"10000000000000000000", "99999999999999999999", "100000000000000000000", "18446744073709551616", "20000000000000000000",
		"170141183460469231731687303715884105727", "-170141183460469231731687303715884105728", "127127879743834334146972278486287885163",
		"123456789012345678901234567890", "-123456789012345678901234567890", "100000000000000000000000000000000000000", "99999999999999999999999999999999999999"} {
		v, err := ParseInt128(s)
		if err != nil {
			panic(err)
		}
		edges = append(edges, v)
	}
	return edges
}()

func randInt128(r *rand.Rand) Int128 {
	switch r.IntN(4) {
	case 0:
		return Int128From(int64(r.Uint64()))
	case 1:
		return Int128From(int64(r.Uint64()>>40) - 1<<23)
	case 2: // a 63-bit magnitude, so products of two fit
		return Int128From(int64(r.Uint64() >> 1))
	}
	return Int128FromWords(int64(r.Uint64()), r.Uint64())
}

func TestInt128Arithmetic(t *testing.T) {
	type op struct {
		name    string
		checked func(a, b Int128) (Int128, error)
		big     func(z, a, b *big.Int) *big.Int
	}
	ops := []op{
		{"+", Int128.CheckedAdd, (*big.Int).Add},
		{"-", Int128.CheckedSub, (*big.Int).Sub},
		{"*", Int128.CheckedMul, (*big.Int).Mul},
	}
	check := func(a, b Int128) {
		for _, o := range ops {
			want := o.big(new(big.Int), toBig(a), toBig(b))
			got, err := o.checked(a, b)
			if fits128(want) {
				if err != nil || toBig(got).Cmp(want) != 0 {
					t.Errorf("%s %s %s: got %s, %v; want %s", a, o.name, b, got, err, want)
				}
			} else {
				var oe *Int128OverflowError
				if !errors.As(err, &oe) || oe.Op != o.name {
					t.Errorf("%s %s %s: want overflow, got %s, %v", a, o.name, b, got, err)
				}
			}
		}
		// Quo and Rem: truncated division, remainder with the dividend's sign.
		if !b.IsZero() {
			q, r := new(big.Int).QuoRem(toBig(a), toBig(b), new(big.Int))
			gq, qerr := a.CheckedQuo(b)
			gr, rerr := a.CheckedRem(b)
			if fits128(q) {
				if qerr != nil || toBig(gq).Cmp(q) != 0 {
					t.Errorf("%s / %s: got %s, %v; want %s", a, b, gq, qerr, q)
				}
			} else if qerr == nil {
				t.Errorf("%s / %s: want overflow", a, b)
			}
			if rerr != nil || toBig(gr).Cmp(r) != 0 {
				t.Errorf("%s %% %s: got %s, %v; want %s", a, b, gr, rerr, r)
			}
		} else {
			if _, err := a.CheckedQuo(b); err == nil || err.Error() != "integer divide by zero" {
				t.Errorf("%s / 0: want divide by zero, got %v", a, err)
			}
		}
	}
	for _, a := range int128Edges {
		for _, b := range int128Edges {
			check(a, b)
		}
		// Neg and Abs.
		want := new(big.Int).Neg(toBig(a))
		got, err := a.CheckedNeg()
		if fits128(want) && (err != nil || toBig(got).Cmp(want) != 0) {
			t.Errorf("-%s: got %s, %v", a, got, err)
		}
		if !fits128(want) && err == nil {
			t.Errorf("-%s: want overflow", a)
		}
		wantAbs := new(big.Int).Abs(toBig(a))
		gotAbs, err := a.CheckedAbs()
		if fits128(wantAbs) && (err != nil || toBig(gotAbs).Cmp(wantAbs) != 0) {
			t.Errorf("abs(%s): got %s, %v", a, gotAbs, err)
		}
		if !fits128(wantAbs) && err == nil {
			t.Errorf("abs(%s): want overflow", a)
		}
	}
	r := rand.New(rand.NewPCG(155, 3))
	for range 200_000 {
		check(randInt128(r), randInt128(r))
	}
}

func TestInt128PanickingFormsAndMessages(t *testing.T) {
	mustPanic := func(name string, f func()) (err error) {
		defer func() {
			r := recover()
			if r == nil {
				t.Errorf("%s: no panic", name)
				return
			}
			err, _ = r.(error)
		}()
		f()
		return nil
	}
	err := mustPanic("add", func() { MaxInt128.Add(Int128From(1)) })
	if err == nil || err.Error() != "integer overflow: 170141183460469231731687303715884105727 + 1 does not fit an int128" {
		t.Errorf("add message: %v", err)
	}
	err = mustPanic("neg", func() { MinInt128.Neg() })
	if err == nil || err.Error() != "integer overflow: -(-170141183460469231731687303715884105728) does not fit an int128" {
		t.Errorf("neg message: %v", err)
	}
	err = mustPanic("mul", func() { MaxInt128.Mul(Int128From(2)) })
	var oe *Int128OverflowError
	if !errors.As(err, &oe) || oe.Op != "*" {
		t.Errorf("mul: %v", err)
	}
	err = mustPanic("sub", func() { MinInt128.Sub(Int128From(1)) })
	if !errors.As(err, &oe) || oe.Op != "-" {
		t.Errorf("sub: %v", err)
	}
	err = mustPanic("quo", func() { Int128From(1).Quo(Int128{}) })
	if err == nil || err.Error() != "integer divide by zero" {
		t.Errorf("quo by zero: %v", err)
	}
	err = mustPanic("must-int64", func() { MustInt64(Int128FromWords(1, 0)) })
	var ce *CastError
	if !errors.As(err, &ce) {
		t.Errorf("MustInt64: %v", err)
	}
	if Int128From(6).Add(Int128From(7)) != Int128From(13) || Int128From(6).Mul(Int128From(-7)) != Int128From(-42) ||
		Int128From(-7).Rem(Int128From(3)) != Int128From(-1) || Int128From(-7).Quo(Int128From(3)) != Int128From(-2) {
		t.Error("small arithmetic")
	}
}

func TestInt128StringParseAndJSON(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	vals := append([]Int128{}, int128Edges...)
	for range 20_000 {
		vals = append(vals, randInt128(r))
	}
	for _, v := range vals {
		want := toBig(v).String()
		if got := v.String(); got != want {
			t.Errorf("String: got %s want %s", got, want)
		}
		back, err := ParseInt128(want)
		if err != nil || back != v {
			t.Errorf("Parse(%s): %s, %v", want, back, err)
		}
		if plus, err := ParseInt128("+" + want); v.Sign() >= 0 && (err != nil || plus != v) {
			t.Errorf("Parse(+%s): %v", want, err)
		}
		b, _ := json.Marshal(v)
		if string(b) != want {
			t.Errorf("MarshalJSON: %s", b)
		}
		var u1, u2 Int128
		if err := json.Unmarshal(b, &u1); err != nil || u1 != v {
			t.Errorf("UnmarshalJSON bare %s: %v", b, err)
		}
		if err := json.Unmarshal([]byte(strconv.Quote(want)), &u2); err != nil || u2 != v {
			t.Errorf("UnmarshalJSON quoted %s: %v", want, err)
		}
		// Cmp agrees with big.
		for _, w := range int128Edges[:8] {
			if v.Cmp(w) != toBig(v).Cmp(toBig(w)) {
				t.Errorf("Cmp(%s, %s)", v, w)
			}
		}
		// Int64 round trip.
		if i, ok := v.Int64(); ok != toBig(v).IsInt64() || (ok && Int128From(i) != v) {
			t.Errorf("Int64(%s): %d %v", v, i, ok)
		}
	}
	// Leading zeros parse; junk, empty and out-of-range do not.
	if v, err := ParseInt128("000123"); err != nil || v != Int128From(123) {
		t.Errorf("leading zeros: %s %v", v, err)
	}
	for _, bad := range []string{"", "-", "+", "12a", " 12", "1.5", "1e3", "0x10", "--1"} {
		var ne *strconv.NumError
		if _, err := ParseInt128(bad); !errors.As(err, &ne) || !errors.Is(err, strconv.ErrSyntax) {
			t.Errorf("Parse(%q): want ErrSyntax, got %v", bad, err)
		}
	}
	for _, big := range []string{"170141183460469231731687303715884105728", "-170141183460469231731687303715884105729", "1000000000000000000000000000000000000000", "99999999999999999999999999999999999999999999"} {
		if _, err := ParseInt128(big); !errors.Is(err, strconv.ErrRange) {
			t.Errorf("Parse(%s): want ErrRange, got %v", big, err)
		}
	}
	if MaxInt128.String() != "170141183460469231731687303715884105727" || MinInt128.String() != "-170141183460469231731687303715884105728" {
		t.Error("bounds print wrong")
	}
}

func TestInt128Float64(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	vals := append([]Int128{}, int128Edges...)
	for range 50_000 {
		vals = append(vals, randInt128(r))
	}
	for _, v := range vals {
		want, _ := new(big.Float).SetInt(toBig(v)).Float64()
		if got := v.Float64(); got != want {
			t.Errorf("Float64(%s): got %v want %v", v, got, want)
		}
		// Back again: a whole float within range converts; the round trip
		// through float is exact only to 2^53, so compare with big.
		f := v.Float64()
		back, ok := Int128FromFloat(f)
		bf, _ := new(big.Float).SetFloat64(f).Int(nil)
		if fits128(bf) != ok || (ok && toBig(back).Cmp(bf) != 0) {
			t.Errorf("Int128FromFloat(%v): %s %v, want %s %v", f, back, ok, bf, fits128(bf))
		}
	}
	for _, f := range []float64{0.5, -1.5, math.NaN(), math.Inf(1), math.Inf(-1), math.Ldexp(1, 127), math.Ldexp(1, 200)} {
		if _, ok := Int128FromFloat(f); ok {
			t.Errorf("Int128FromFloat(%v): want false", f)
		}
	}
	if v, ok := Int128FromFloat(-math.Ldexp(1, 127)); !ok || v != MinInt128 {
		t.Errorf("Int128FromFloat(-2^127): %s %v", v, ok)
	}
	if v, ok := Int128FromFloat(math.Ldexp(1, 100)); !ok || v.String() != "1267650600228229401496703205376" {
		t.Errorf("Int128FromFloat(2^100): %s %v", v, ok)
	}
	// Ties go to even: 2^64 + 2^11 (a tie between two float64 neighbours
	// at that magnitude) rounds down to the even mantissa.
	tie := Int128FromWords(1, 1<<11)
	if want, _ := new(big.Float).SetInt(toBig(tie)).Float64(); tie.Float64() != want {
		t.Errorf("tie: %v want %v", tie.Float64(), want)
	}
}

func TestInt128MinMaxSign(t *testing.T) {
	a, b := Int128From(-5), Int128FromWords(1, 0)
	if Int128Min(a, b) != a || Int128Max(a, b) != b || Int128Min(b, a) != a || Int128Max(b, a) != b {
		t.Error("min/max")
	}
	if a.Sign() != -1 || b.Sign() != 1 || (Int128{}).Sign() != 0 || !(Int128{}).IsZero() || a.IsZero() {
		t.Error("sign")
	}
}

func BenchmarkInt128Add(b *testing.B) {
	x, y := Int128From(1<<62), Int128From(3)
	var acc Int128
	for i := 0; i < b.N; i++ {
		acc = acc.Add(y)
		x = x.Add(acc)
		if x.Cmp(MaxInt128) == 0 {
			b.Fatal("unreachable")
		}
		x = x.Sub(acc)
	}
	_ = x
}

func BenchmarkInt128String(b *testing.B) {
	v := MustParseInt128("127127879743834334146972278486287885163")
	for i := 0; i < b.N; i++ {
		_ = v.String()
	}
}
