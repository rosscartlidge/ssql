package ssql

import (
	"encoding/json"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// TestInetParseString: the texts are DuckDB 1.5's CAST(CAST(x AS INET) AS
// VARCHAR) for the same input, the oracle for the engines' spelling
// (netip alone prints ::1.2.3.4 as ::102:304).
func TestInetParseString(t *testing.T) {
	for in, want := range map[string]string{
		"::1.2.3.4": "::1.2.3.4", "::0.1.0.0": "::0.1.0.0", "::2": "::2", "::ffff": "::ffff",
		"::ffff:0.0.0.0": "::ffff:0.0.0.0", "::ffff:1.2.3.4": "::ffff:1.2.3.4",
		"2001:DB8::1": "2001:db8::1", "2001:db8:0:0:1:0:0:1": "2001:db8::1:0:0:1", "0:0:0:0:0:0:0:1": "::1",
		"10.0.0.1/32": "10.0.0.1", "10.0.0.1/24": "10.0.0.1/24", "::1/128": "::1", "2001:db8::/32": "2001:db8::/32",
		"::": "::", "0.0.0.0/0": "0.0.0.0/0", "fe80::1": "fe80::1", "1:0:0:0:0:0:0:8": "1::8",
		"::1.2.3.4/120": "::1.2.3.4/120", " 10.0.0.9 ": "10.0.0.9",
	} {
		v, err := ParseInet(in)
		if err != nil || v.String() != want {
			t.Errorf("ParseInet(%q) = %q, %v; want %q", in, v, err, want)
		}
	}
	for in, why := range map[string]string{
		"fe80::1%eth0": "zone", "010.0.0.1": "leading zero", "10.0.0.1/33": "", "10.0.0": "", "": "", "abc": "", "10.0.0.1/": "",
	} {
		_, err := ParseInet(in)
		if err == nil || !strings.Contains(err.Error(), "is not an IP address or prefix") || !strings.Contains(err.Error(), why) {
			t.Errorf("ParseInet(%q) = %v; want an InetError mentioning %q", in, err, why)
		}
	}
	if (Inet{}).String() != "" || (Inet{}).IsValid() {
		t.Error("the zero Inet is no value")
	}
}

// TestInetCompare: the order measured in DuckDB and Postgres (DFC162 §9),
// and equality across spellings.
func TestInetCompare(t *testing.T) {
	chain := []string{"9.0.0.1", "10.0.0.0/8", "10.0.0.0/16", "10.0.0.1/24", "10.0.0.1", "::1", "::ffff:1.2.3.4"}
	for i := 1; i < len(chain); i++ {
		a, b := MustParseInet(chain[i-1]), MustParseInet(chain[i])
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("%s should sort before %s", a, b)
		}
	}
	for _, pair := range [][2]string{{"10.0.0.1", "10.0.0.1/32"}, {"::1", "0:0:0:0:0:0:0:1"}, {"2001:DB8::1", "2001:db8::1/128"}} {
		if MustParseInet(pair[0]) != MustParseInet(pair[1]) {
			t.Errorf("%s and %s are one value", pair[0], pair[1])
		}
	}
	if MustParseInet("10.0.0.1/24") == MustParseInet("10.0.0.1") {
		t.Error("host bits are kept: 10.0.0.1/24 is not 10.0.0.1")
	}
	if c, ok := inetCmp(MustParseInet("9.0.0.1"), "10.0.0.0/8"); !ok || c >= 0 {
		t.Errorf("an inet beside a string compares as inet: %d %v", c, ok)
	}
	if _, ok := inetCmp("9.0.0.1", "10.0.0.1"); ok {
		t.Error("two strings are not an inet comparison")
	}
}

// TestInetFunctions: ipint is Python's int(ip) in two's complement for
// IPv6 at or above 2^127; ipnet clears the host bits; ipin is <<=.
func TestInetFunctions(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.1": "167772161", "0.0.0.0": "0", "255.255.255.255": "4294967295", "::1": "1",
		"2001:db8::1": "42540766411282592856903984951653826561", "fe80::1": "-1993841993677373809355710590420516863",
		"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff": "-1", "::ffff:1.2.3.4": "281470698652420",
	} {
		if got := MustParseInet(in).Int128().String(); got != want {
			t.Errorf("ipint(%s) = %s, want %s", in, got, want)
		}
	}
	for _, c := range []struct {
		in   string
		bits int64
		want string
	}{{"10.1.2.3", 16, "10.1.0.0/16"}, {"10.1.2.3/24", 8, "10.0.0.0/8"}, {"10.1.2.3", 32, "10.1.2.3"}, {"2001:db8::1", 32, "2001:db8::/32"}} {
		if got, err := MustParseInet(c.in).Network(c.bits); err != nil || got.String() != c.want {
			t.Errorf("ipnet(%s, %d) = %s, %v; want %s", c.in, c.bits, got, err, c.want)
		}
	}
	if _, err := MustParseInet("10.0.0.1").Network(33); err == nil {
		t.Error("ipnet past the family's width is an error")
	}
	for _, c := range []struct {
		x, p string
		want bool
	}{
		{"10.1.2.3", "10.0.0.0/8", true}, {"10.0.0.0/8", "10.0.0.0/8", true}, {"10.0.0.0/8", "10.0.0.0/16", false},
		{"11.0.0.1", "10.0.0.0/8", false}, {"10.1.2.3/24", "10.1.2.0/24", true}, {"::ffff:10.0.0.1", "10.0.0.0/8", false},
		{"2001:db8::1", "2001:db8::/32", true},
	} {
		if got := MustParseInet(c.x).Within(MustParseInet(c.p)); got != c.want {
			t.Errorf("ipin(%s, %s) = %v", c.x, c.p, got)
		}
	}
	if f := MustParseInet("::ffff:1.2.3.4").Family(); f != 6 {
		t.Errorf("a v4-mapped address is family 6, got %d", f)
	}
}

// TestInetKeyOrder: Key sorts and equals exactly as Compare.
func TestInetKeyOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var vs []Inet
	for range 10000 {
		var a netip.Addr
		if r.IntN(2) == 0 {
			a = netip.AddrFrom4([4]byte{byte(r.IntN(4)), byte(r.IntN(3)), byte(r.UintN(256)), byte(r.UintN(256))})
		} else {
			var b [16]byte
			for i := range b {
				b[i] = byte(r.UintN(4))
			}
			a = netip.AddrFrom16(b)
		}
		vs = append(vs, InetFromPrefix(netip.PrefixFrom(a, r.IntN(a.BitLen()+1))))
	}
	byCmp := slices.Clone(vs)
	slices.SortFunc(byCmp, Inet.Compare)
	byKey := slices.Clone(vs)
	slices.SortStableFunc(byKey, func(a, b Inet) int { return strings.Compare(a.Key(), b.Key()) })
	for i := range byCmp {
		if byCmp[i] != byKey[i] && byCmp[i].Compare(byKey[i]) != 0 {
			t.Fatalf("at %d: Compare order %s, Key order %s", i, byCmp[i], byKey[i])
		}
	}
	if k := MustParseInet("10.0.0.1").Key(); k != "40a000001200a000001" || len(k) != 19 {
		t.Errorf("ipkey(10.0.0.1) = %q", k)
	}
	if k := MustParseInet("::1").Key(); len(k) != 67 || k[0] != '6' {
		t.Errorf("ipkey(::1) = %q", k)
	}
}

func TestInetMarshal(t *testing.T) {
	type row struct {
		A Inet `json:"a"`
		B Inet `json:"b"`
	}
	b, _ := json.Marshal(row{A: MustParseInet("10.0.0.0/8")})
	if string(b) != `{"a":"10.0.0.0/8","b":null}` {
		t.Errorf("json: %s", b)
	}
	var back row
	if err := json.Unmarshal(b, &back); err != nil || back.A != MustParseInet("10.0.0.0/8") || back.B.IsValid() {
		t.Errorf("json round trip: %+v %v", back, err)
	}
	bin, _ := MustParseInet("2001:db8::1/64").MarshalBinary()
	var x Inet
	if err := x.UnmarshalBinary(bin); err != nil || x.String() != "2001:db8::1/64" {
		t.Errorf("binary round trip: %s %v", x, err)
	}
	if InetOrNull(Inet{}) != nil || InetOrNull(x) != x {
		t.Error("InetOrNull")
	}
}

func BenchmarkInetParse(b *testing.B) {
	for range b.N {
		_, _ = ParseInet("2001:db8::1")
	}
}

func BenchmarkInetCompare(b *testing.B) {
	x, y := MustParseInet("10.0.0.1"), MustParseInet("10.0.0.2")
	for range b.N {
		_ = x.Compare(y)
	}
}

func BenchmarkInetString(b *testing.B) {
	x := MustParseInet("2001:db8::1")
	for range b.N {
		_ = x.String()
	}
}
