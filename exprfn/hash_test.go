package exprfn

import "testing"

// The vectors are from an independent Python implementation of the
// definition (lowbias32 over the folded input, the seed term xored in):
// the same numbers pin every lane's equivalence Golden.
func TestHash(t *testing.T) {
	cases := []struct {
		n               int64
		h0, h1, h7, hm3 int64
		hBig            int64 // seed 2^40: the seed folds too
	}{
		{-2, 3531918615, 2052500106, 558192479, 1007868863, 3628695143},
		{-1, 261369915, 1927069251, 1190723808, 3808435052, 2577573125},
		{0, 261369915, 1927069251, 1190723808, 3808435052, 2577573125},
		{1, 3531918615, 2052500106, 558192479, 1007868863, 3628695143},
		{2, 2840856084, 2715656727, 4045084947, 3877102822, 503018515},
		{3, 3125224334, 1239171971, 4081673763, 1201530588, 2932857214},
		{4294967295, 2905444864, 1417332593, 1472704555, 2662918238, 2651959281},
		{4294967296, 3531918615, 2052500106, 558192479, 1007868863, 3628695143},
		{9223372036854775807, 3975368635, 2305062820, 2402611643, 141408849, 1392174993},
		{-9223372036854775808, 3975368635, 2305062820, 2402611643, 141408849, 1392174993},
	}
	for _, c := range cases {
		for seed, want := range map[int64]int64{0: c.h0, 1: c.h1, 7: c.h7, -3: c.hm3, 1 << 40: c.hBig} {
			if got := Hash(c.n, seed); got != want {
				t.Errorf("Hash(%d, %d) = %d, want %d", c.n, seed, got, want)
			}
		}
	}
	if SeedTerm(-3) != 2004795789 || SeedTerm(0) != 0x27d4eb2f {
		t.Errorf("SeedTerm: %d %d", SeedTerm(-3), SeedTerm(0))
	}
}

func TestDigests(t *testing.T) {
	if MD5("1") != "c4ca4238a0b923820dcc509a6f75849b" || SHA1("1") != "356a192b7913b04c54574d18c28d46e6395428ab" ||
		SHA256("1") != "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b" {
		t.Errorf("digests of \"1\": %s %s %s", MD5("1"), SHA1("1"), SHA256("1"))
	}
}
