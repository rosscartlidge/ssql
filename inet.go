package ssql

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Inet is an IP address or prefix: the `inet` column type (DFC162 §4 I2),
// DuckDB's INET and Postgres's inet. One type holds both, as in DuckDB:
// an address is its full-length prefix (/32 or /128), and a prefix keeps
// its host bits (10.0.0.1/24 is not 10.0.0.0/24). It is a comparable
// value — a group-by key, a join key, a typed struct field, no allocation.
//
// The order is netip.Prefix.Compare: IPv4 before IPv6, then the masked
// address, the prefix length, the address — which is DuckDB's and
// Postgres's order exactly (measured, DFC162 §9):
// 9.0.0.1 < 10.0.0.0/8 < 10.0.0.0/16 < 10.0.0.1/24 < 10.0.0.1 < ::1.
// A v4-mapped address (::ffff:1.2.3.4) is IPv6, as in both engines. A
// zone (fe80::1%eth0) is refused: neither engine has one.
//
// A column is inet only by declaration — `cast -type F inet`, `-type F
// inet` on a source, a `_schema` header, inet(x) — never inferred from
// text that looks like an address. It has no arithmetic. The zero value
// is "no value": it prints as "" and marshals as JSON null.
type Inet struct {
	p netip.Prefix
}

// InetError is a value that is not an IP address or prefix.
type InetError struct {
	Value string
	Why   string
}

func (e *InetError) Error() string {
	return fmt.Sprintf("inet: %q is not an IP address or prefix (%s)", e.Value, e.Why)
}

// ParseInet reads an address (10.0.0.1, 2001:db8::1) or a prefix
// (10.0.0.0/8, 10.0.0.1/24), around spaces. netip's rules apply: no
// leading zeros in an IPv4 octet (DuckDB and Postgres read 010.0.0.1 as
// 10.0.0.1; ssql refuses it rather than guess), no zone.
func ParseInet(s string) (Inet, error) {
	t := strings.TrimSpace(s)
	if strings.ContainsRune(t, '%') {
		return Inet{}, &InetError{Value: s, Why: "a zone is not supported"}
	}
	if strings.ContainsRune(t, '/') {
		p, err := netip.ParsePrefix(t)
		if err != nil {
			return Inet{}, &InetError{Value: s, Why: inetWhy(err)}
		}
		return Inet{p: p}, nil
	}
	a, err := netip.ParseAddr(t)
	if err != nil {
		return Inet{}, &InetError{Value: s, Why: inetWhy(err)}
	}
	return Inet{p: netip.PrefixFrom(a, a.BitLen())}, nil
}

// inetWhy is netip's reason without its own quoting of the input.
func inetWhy(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// MustParseInet is ParseInet for generated code: a bad value panics with
// the error, which ssql.Recover turns into the program's error.
func MustParseInet(s string) Inet {
	v, err := ParseInet(s)
	if err != nil {
		panic(err)
	}
	return v
}

// InetFromPrefix wraps a netip.Prefix.
func InetFromPrefix(p netip.Prefix) Inet { return Inet{p: p} }

// InetFromAddr is an address as its full-length prefix.
func InetFromAddr(a netip.Addr) Inet { return Inet{p: netip.PrefixFrom(a, a.BitLen())} }

// Prefix is the value as a netip.Prefix.
func (x Inet) Prefix() netip.Prefix { return x.p }

// Addr is the address, host bits and all.
func (x Inet) Addr() netip.Addr { return x.p.Addr() }

// IsValid reports whether x holds a value (the zero Inet does not).
func (x Inet) IsValid() bool { return x.p.IsValid() }

// Bits is the prefix length: 32 or 128 for an address.
func (x Inet) Bits() int { return x.p.Bits() }

// Family is 4 or 6 (a v4-mapped address is 6), 0 for the zero value.
func (x Inet) Family() int64 {
	switch {
	case !x.p.IsValid():
		return 0
	case x.p.Addr().Is4():
		return 4
	}
	return 6
}

// Compare orders as DuckDB and Postgres do (see Inet); the zero value
// sorts first.
func (x Inet) Compare(y Inet) int { return x.p.Compare(y.p) }

// String is the engines' text: the bare address for a full-length
// prefix, else addr/len; an IPv4-compatible address (the top 96 bits
// zero, the next 16 not) dotted, as DuckDB and Postgres print ::1.2.3.4
// where netip prints ::102:304; "" for the zero value.
func (x Inet) String() string {
	if !x.p.IsValid() {
		return ""
	}
	a := x.p.Addr()
	s := inetAddrString(a)
	if x.p.Bits() == a.BitLen() {
		return s
	}
	return s + "/" + strconv.Itoa(x.p.Bits())
}

func inetAddrString(a netip.Addr) string {
	if a.Is6() && !a.Is4In6() {
		b := a.As16()
		if binary.BigEndian.Uint64(b[:8]) == 0 && binary.BigEndian.Uint32(b[8:12]) == 0 && (b[12] != 0 || b[13] != 0) {
			return "::" + netip.AddrFrom4([4]byte(b[12:16])).String()
		}
	}
	return a.String()
}

// Network is the prefix of the given length containing x's address, host
// bits cleared: ipnet(x, bits).
func (x Inet) Network(bits int64) (Inet, error) {
	if !x.p.IsValid() {
		return Inet{}, fmt.Errorf("ipnet: no address")
	}
	a := x.p.Addr()
	if bits < 0 || bits > int64(a.BitLen()) {
		return Inet{}, fmt.Errorf("ipnet: %d bits is outside 0..%d for %s", bits, a.BitLen(), x)
	}
	return Inet{p: netip.PrefixFrom(a, int(bits)).Masked()}, nil
}

// MustNetwork is Network for generated code: a bits value outside the
// family's width panics with the error.
func (x Inet) MustNetwork(bits int64) Inet {
	n, err := x.Network(bits)
	if err != nil {
		panic(err)
	}
	return n
}

// IsInetText reports whether s is an address or a prefix: isip(s).
func IsInetText(s string) bool {
	_, err := ParseInet(s)
	return err == nil
}

// Within reports whether x lies in the prefix p, or equals it — the
// engines' <<=: the same family, p's network contains x's address, and
// x is no wider than p.
func (x Inet) Within(p Inet) bool {
	return x.p.IsValid() && p.p.IsValid() && x.p.Bits() >= p.p.Bits() && p.p.Masked().Contains(x.p.Addr())
}

// Int128 is the address as an integer: an IPv4 address its 32-bit value,
// an IPv6 address its 128 bits in two's complement, so every address has
// a value and the value maps back to it — an address at or above 2^127
// (fe80::, fc00::, ff00::) is negative. Not a sort key for IPv6 (sort
// the inet, or use Key); the prefix length is not part of it.
func (x Inet) Int128() Int128 {
	a := x.p.Addr()
	if a.Is4() {
		b := a.As4()
		return Int128From(int64(binary.BigEndian.Uint32(b[:])))
	}
	b := a.As16()
	return Int128FromWords(int64(binary.BigEndian.Uint64(b[:8])), binary.BigEndian.Uint64(b[8:]))
}

// Key is a text key that sorts and equals exactly as Compare: the family
// digit, the masked address in hex, the prefix length in two hex digits,
// the address in hex — 19 characters for IPv4, 67 for IPv6 (the family
// digit orders the families, so a width is fixed only within one).
func (x Inet) Key() string {
	if !x.p.IsValid() {
		return ""
	}
	a, m := x.p.Addr(), x.p.Masked().Addr()
	buf := make([]byte, 0, 67)
	if a.Is4() {
		buf = append(buf, '4')
		mb, ab := m.As4(), a.As4()
		buf = hex.AppendEncode(buf, mb[:])
		buf = append(buf, hexByte(byte(x.p.Bits()))...)
		return string(hex.AppendEncode(buf, ab[:]))
	}
	buf = append(buf, '6')
	mb, ab := m.As16(), a.As16()
	buf = hex.AppendEncode(buf, mb[:])
	buf = append(buf, hexByte(byte(x.p.Bits()))...)
	return string(hex.AppendEncode(buf, ab[:]))
}

func hexByte(b byte) []byte {
	const digits = "0123456789abcdef"
	return []byte{digits[b>>4], digits[b&0xf]}
}

// MarshalJSON writes the text, or null for the zero value.
func (x Inet) MarshalJSON() ([]byte, error) {
	if !x.p.IsValid() {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(x.String())), nil
}

// UnmarshalJSON reads the quoted text, or null.
func (x *Inet) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		*x = Inet{}
		return nil
	}
	u, err := strconv.Unquote(s)
	if err != nil {
		return &InetError{Value: s, Why: "not a JSON string"}
	}
	v, err := ParseInet(u)
	if err != nil {
		return err
	}
	*x = v
	return nil
}

// MarshalText writes the text ("" for the zero value).
func (x Inet) MarshalText() ([]byte, error) { return []byte(x.String()), nil }

// UnmarshalText reads the text; "" is the zero value.
func (x *Inet) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*x = Inet{}
		return nil
	}
	v, err := ParseInet(string(b))
	if err != nil {
		return err
	}
	*x = v
	return nil
}

// MarshalBinary and UnmarshalBinary are netip's, for gob (sort -spill).
func (x Inet) MarshalBinary() ([]byte, error) { return x.p.MarshalBinary() }

// UnmarshalBinary reads MarshalBinary's form.
func (x *Inet) UnmarshalBinary(b []byte) error { return x.p.UnmarshalBinary(b) }

// InetOrNull is v, or nil for the zero value: the typed lane's empty
// cell becomes an absent field at the Record boundary, as in exec.
func InetOrNull(v Inet) any {
	if !v.p.IsValid() {
		return nil
	}
	return v
}

// inetNoArithmetic is the error an arithmetic operation on an inet
// raises in every lane: an address is not a number.
func inetNoArithmetic(op string, v Inet) error {
	return fmt.Errorf("%s: %s is an inet, which has no arithmetic (count, min, max, sort and group-by work on it; ipint(x) is its address as an integer)", op, v)
}

// asInet reads an Inet, or a string that parses as one; ok is false for
// anything else, or for a string that does not parse.
func asInet(v any) (Inet, bool) {
	switch x := v.(type) {
	case Inet:
		return x, true
	case string:
		p, err := ParseInet(x)
		return p, err == nil
	}
	return Inet{}, false
}

// inetCmp compares two inet values, or an inet beside a string that
// parses as one (a literal, a string column); ok is false unless one side
// is an Inet and the other reads as one.
func inetCmp(a, b any) (int, bool) {
	_, ai := a.(Inet)
	_, bi := b.(Inet)
	if !ai && !bi {
		return 0, false
	}
	x, ok := asInet(a)
	if !ok {
		return 0, false
	}
	y, ok := asInet(b)
	if !ok {
		return 0, false
	}
	return x.Compare(y), true
}
