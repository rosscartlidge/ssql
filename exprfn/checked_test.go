package exprfn

import (
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

// The oracle is math/big: the exact result fits an int64 or it does not.
func bigFits(op string, a, b int64) (int64, bool) {
	x, y := big.NewInt(a), big.NewInt(b)
	var r big.Int
	switch op {
	case "+":
		r.Add(x, y)
	case "-":
		r.Sub(x, y)
	case "*":
		r.Mul(x, y)
	}
	return r.Int64(), r.IsInt64()
}

var checkedOps = map[string]func(a, b int64) (int64, error){
	"+": CheckedAdd, "-": CheckedSub, "*": CheckedMul,
}

func TestCheckedArithmeticEdges(t *testing.T) {
	edges := []int64{0, 1, -1, 2, -2, 3, 7, -7, 1 << 31, -(1 << 31), 1 << 32, -(1 << 32),
		1<<62 - 1, 1 << 62, -(1 << 62), 3037000499, 3037000500, -3037000499, -3037000500,
		math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1}
	for op, fn := range checkedOps {
		for _, a := range edges {
			for _, b := range edges {
				want, fits := bigFits(op, a, b)
				got, err := fn(a, b)
				if fits && (err != nil || got != want) {
					t.Errorf("%d %s %d: got %d, %v; want %d", a, op, b, got, err, want)
				}
				if !fits {
					var oe *OverflowError
					if !errors.As(err, &oe) || oe.Op != op || oe.A != a || oe.B != b {
						t.Errorf("%d %s %d: want *OverflowError, got %d, %v", a, op, b, got, err)
					}
				}
			}
		}
	}
}

func TestCheckedArithmeticRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(155, 2026))
	draw := func() int64 {
		// a mix of magnitudes so products straddle 2^63
		switch rng.IntN(4) {
		case 0:
			return rng.Int64()
		case 1:
			return rng.Int64N(1<<32) - 1<<31
		case 2:
			return rng.Int64N(1<<40) - 1<<39
		}
		return int64(rng.Uint64())
	}
	for i := 0; i < 200000; i++ {
		a, b := draw(), draw()
		for op, fn := range checkedOps {
			want, fits := bigFits(op, a, b)
			got, err := fn(a, b)
			if fits != (err == nil) || (fits && got != want) {
				t.Fatalf("%d %s %d: got %d, %v; want %d fits=%v", a, op, b, got, err, want, fits)
			}
		}
	}
}

func TestPanickingFormsCarryTheError(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add: %d", got)
	}
	if got := Mul(-4, 5); got != -20 {
		t.Fatalf("Mul: %d", got)
	}
	if got := Sub(-4, 5); got != -9 {
		t.Fatalf("Sub: %d", got)
	}
	for name, fn := range map[string]func(){
		"Add": func() { Add(math.MaxInt64, 1) },
		"Sub": func() { Sub(math.MinInt64, 1) },
		"Mul": func() { Mul(math.MinInt64, -1) },
	} {
		func() {
			defer func() {
				r := recover()
				err, ok := r.(error)
				if !ok {
					t.Errorf("%s: panic value %v is not an error", name, r)
					return
				}
				var oe *OverflowError
				if !errors.As(err, &oe) {
					t.Errorf("%s: %v is not an *OverflowError", name, err)
				}
			}()
			fn()
			t.Errorf("%s: no panic", name)
		}()
	}
}

func TestOverflowErrorMessage(t *testing.T) {
	_, err := CheckedMul(math.MaxInt64, 2)
	const want = "integer overflow: 9223372036854775807 * 2 does not fit an int64"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func BenchmarkCheckedMul(b *testing.B) {
	var acc int64
	for i := 0; i < b.N; i++ {
		v, _ := CheckedMul(int64(i&0xffff), 3)
		acc += v
	}
	_ = acc
}
