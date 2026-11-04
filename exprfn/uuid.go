package exprfn

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// --- UUIDs (DFC162 §3): uuid4(), uuid5(ns, name) and uuid7(), as the
// canonical lower-case 8-4-4-4-12 text. A UUID needs no type: that text
// is fixed-width, so string order is the 128-bit order (DuckDB's UUID
// sorts the same way), and equality is string equality. One
// implementation for the expr VM and for transpiled code.

// UUIDNamespaces are RFC 9562 §6.6's namespaces, by the names Python's
// uuid module gives them (NAMESPACE_DNS → "dns").
var UUIDNamespaces = map[string]string{
	"dns":  "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	"url":  "6ba7b811-9dad-11d1-80b4-00c04fd430c8",
	"oid":  "6ba7b812-9dad-11d1-80b4-00c04fd430c8",
	"x500": "6ba7b814-9dad-11d1-80b4-00c04fd430c8",
}

// UUID4 is a random (version 4) UUID: 122 bits from crypto/rand.
func UUID4() string {
	var b [16]byte
	rand.Read(b[:])
	return uuidString(&b, 4)
}

// UUID7 is a time-ordered (version 7) UUID: the Unix time in
// milliseconds, then 74 random bits (RFC 9562 §5.7). Ids sort by
// creation to the millisecond; within one, the order is random, as in
// every engine that has the function.
func UUID7() string {
	return uuid7At(time.Now())
}

func uuid7At(t time.Time) string {
	var b [16]byte
	rand.Read(b[6:])
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(t.UnixMilli()))
	copy(b[:6], ms[2:])
	return uuidString(&b, 7)
}

// TryUUID5 is the name-based (version 5, SHA-1) UUID of name in the
// namespace ns: a UUID or one of the names in UUIDNamespaces. The same
// inputs give the same UUID as Python's uuid.uuid5, Postgres's
// uuid_generate_v5 and Go's google/uuid; the name is its UTF-8 bytes.
func TryUUID5(ns, name string) (string, error) {
	nsb, err := ParseUUIDNamespace(ns)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	h.Write(nsb[:])
	h.Write([]byte(name))
	var b [16]byte
	copy(b[:], h.Sum(nil))
	return uuidString(&b, 5), nil
}

// UUID5 is TryUUID5 for generated code: a bad namespace panics with the
// error, which ssql.Recover turns into the program's error (as Add does).
func UUID5(ns, name string) string {
	s, err := TryUUID5(ns, name)
	if err != nil {
		panic(err)
	}
	return s
}

// ParseUUIDNamespace reads a uuid5 namespace: dns, url, oid or x500, or a
// UUID as 32 hex digits with or without the four hyphens, in either case.
func ParseUUIDNamespace(ns string) ([16]byte, error) {
	var b [16]byte
	s := ns
	if canon, ok := UUIDNamespaces[strings.ToLower(s)]; ok {
		s = canon
	}
	if len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-' {
		s = s[:8] + s[9:13] + s[14:18] + s[19:23] + s[24:]
	}
	if len(s) != 32 {
		return b, fmt.Errorf("uuid5: namespace %q is not a UUID or one of dns, url, oid, x500", ns)
	}
	if _, err := hex.Decode(b[:], []byte(s)); err != nil {
		return b, fmt.Errorf("uuid5: namespace %q is not a UUID or one of dns, url, oid, x500", ns)
	}
	return b, nil
}

// uuidString sets the version nibble and the RFC variant (binary 10) and
// writes the canonical text.
func uuidString(b *[16]byte, version byte) string {
	b[6] = b[6]&0x0f | version<<4
	b[8] = b[8]&0x3f | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:], b[10:])
	return string(out[:])
}
