package exprfn

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestUUID5MatchesPython: the values are Python's uuid.uuid5, the
// independent oracle (DFC162 §3 U1).
func TestUUID5MatchesPython(t *testing.T) {
	for _, c := range []struct{ ns, name, want string }{
		{"dns", "python.org", "886313e1-3b8a-5372-9b90-0c9aee199e5d"},
		{"URL", "https://example.com", "4fd35a71-71ef-5a55-a9d9-aa75c889a6d0"},
		{"6ba7b812-9dad-11d1-80b4-00c04fd430c8", "ssql", "113a3051-1b54-575d-8ea8-ad7f60191fee"},
		{"6BA7B8129DAD11D180B400C04FD430C8", "ssql", "113a3051-1b54-575d-8ea8-ad7f60191fee"},
		{"dns", "héllo", "5ca283f8-ff14-58cb-ba86-bf52120a3414"},
		{"dns", "", "4ebd0208-8328-5d69-8c44-ec50939c0967"},
	} {
		got, err := TryUUID5(c.ns, c.name)
		if err != nil || got != c.want {
			t.Errorf("TryUUID5(%q, %q) = %q, %v; want %q", c.ns, c.name, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "dnss", "6ba7b812-9dad-11d1-80b4-00c04fd430c", "zba7b812-9dad-11d1-80b4-00c04fd430c8"} {
		if _, err := TryUUID5(bad, "x"); err == nil || !strings.Contains(err.Error(), "namespace") {
			t.Errorf("TryUUID5(%q) = %v; want a namespace error", bad, err)
		}
	}
}

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-([47])[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestUUID4And7Shape(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		for fn, want := range map[string]string{UUID4(): "4", UUID7(): "7"} {
			m := uuidShape.FindStringSubmatch(fn)
			if m == nil || m[1] != want {
				t.Fatalf("%q is not a version %s UUID", fn, want)
			}
			if seen[fn] {
				t.Fatalf("%q generated twice", fn)
			}
			seen[fn] = true
		}
	}
	// v7: the first 48 bits are the Unix milliseconds, so the text sorts by time.
	at := time.UnixMilli(0x0123456789ab)
	if got := uuid7At(at); !strings.HasPrefix(got, "01234567-89ab-7") {
		t.Errorf("uuid7At(%v) = %q; want the milliseconds 0123456789ab first", at, got)
	}
	if a, b := uuid7At(at), uuid7At(at.Add(time.Millisecond)); a >= b {
		t.Errorf("a later millisecond sorts first: %q >= %q", a, b)
	}
}
