package ssql

import (
	"strings"
	"testing"
	"time"
)

// TestFormatStrftime: every supported directive against Python's output
// for the same instant (2026-01-05 09:07:03.123456 UTC, a Monday, day 5,
// ISO week 2), the fast layout path and the slow path alike.
func TestFormatStrftime(t *testing.T) {
	ts := time.Date(2026, 1, 5, 9, 7, 3, 123456000, time.UTC)
	cases := []struct{ pattern, want string }{
		{"%Y-%m-%d %H:%M:%S", "2026-01-05 09:07:03"},
		{"%d/%m/%Y", "05/01/2026"},
		{"%-d/%-m/%y", "5/1/26"},
		{"%I:%M %p", "09:07 AM"},
		{"%b %B %a %A", "Jan January Mon Monday"},
		{"%j|%G|%V|%u", "005|2026|02|1"},
		{"%S.%f", "03.123456"},
		{"%f", "123456"},
		{"%Y%%", "2026%"},
		{"%z %Z", "+0000 UTC"},
		{"Week %V", "Week 02"},
		{"Day %j of %Y", "Day 005 of 2026"},
		{"T%H", "T09"},
		{"Month: %m", "Month: 01"},
	}
	for _, c := range cases {
		got, err := FormatStrftime(ts, c.pattern)
		if err != nil {
			t.Fatalf("%s: %v", c.pattern, err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.pattern, got, c.want)
		}
	}
	// Fast path for the common patterns, slow where Go has no token.
	for _, c := range []struct {
		pattern string
		slow    bool
	}{{"%Y-%m-%d", false}, {"%S.%f", false}, {"%f", true}, {"%j", true}, {"Month: %m", true}, {"T%H", false}} {
		if _, slow, err := StrftimeLayout(c.pattern); err != nil || slow != c.slow {
			t.Errorf("%s: slow=%v err=%v, want slow=%v", c.pattern, slow, err, c.slow)
		}
	}
	// A Sydney wall clock renders in Sydney.
	syd, _ := time.LoadLocation("Australia/Sydney")
	if got := MustFormatStrftime(ts, "%H:%M %Z", syd); got != "20:07 AEDT" {
		t.Errorf("Sydney: %q", got)
	}
	for _, bad := range []string{"%U", "%e", "%Q", "%"} {
		if _, err := FormatStrftime(ts, bad); err == nil || !strings.Contains(err.Error(), strings.TrimSuffix(bad, "")) {
			t.Errorf("%s: err=%v", bad, err)
		}
	}
}

// TestParseStrftime: strftime and Go layouts both parse; zoneless text is
// UTC; slow-path patterns are refused for parsing.
func TestParseStrftime(t *testing.T) {
	resetStreamZone()
	t.Cleanup(resetStreamZone)
	SetStreamZone("UTC") // zoneless text is a wall clock in the pipeline zone
	got, err := ParseStrftime("05/01/2026 10:30", "%d/%m/%Y %H:%M")
	if err != nil || !got.Equal(time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("strftime parse: %v %v", got, err)
	}
	got, err = ParseStrftime("05/01/2026", "02/01/2006")
	if err != nil || !got.Equal(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("go layout parse: %v %v", got, err)
	}
	got, err = ParseStrftime("2026-01-05 10:30 +1100", "%Y-%m-%d %H:%M %z")
	if err != nil || !got.Equal(time.Date(2026, 1, 4, 23, 30, 0, 0, time.UTC)) {
		t.Errorf("offset parse: %v %v", got, err)
	}
	// The pipeline zone is where zoneless text lives.
	SetStreamZone("Australia/Sydney")
	got, err = ParseStrftime("05/01/2026 10:30", "%d/%m/%Y %H:%M")
	if err != nil || !got.Equal(time.Date(2026, 1, 4, 23, 30, 0, 0, time.UTC)) {
		t.Errorf("Sydney wall clock: %v %v", got, err)
	}
	SetStreamZone("UTC")
	if _, err := ParseStrftime("5", "%j"); err == nil {
		t.Error("%j must not parse")
	}
	if _, err := ParseStrftime("x", "%d/%m/%Y"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("mismatch: %v", err)
	}
	if layout, err := TimeLayout("02/01/2006"); err != nil || layout != "02/01/2006" {
		t.Errorf("go layout passthrough: %q %v", layout, err)
	}
}

// TestPgToChar and TestUTS35ToStrftime: the two other pattern languages.
func TestPgToChar(t *testing.T) {
	cases := []struct{ in, want string }{
		{"%Y-%m-%d %H:%M:%S", `YYYY"-"MM"-"DD" "HH24":"MI":"SS`},
		{"%-d %b %Y", `FMDD" "Mon" "YYYY`},
		{"%I:%M %p", `HH12":"MI" "AM`},
		{"%j %G %V %u", `DDD" "IYYY" "IW" "ID`},
		{"%Y%%", `YYYY"%"`},
	}
	for _, c := range cases {
		got, err := PgToChar(c.in)
		if err != nil || got != c.want {
			t.Errorf("%s = %q (%v), want %q", c.in, got, err, c.want)
		}
	}
	if _, err := PgToChar("%z"); err == nil {
		t.Error("%z has no to_char form")
	}
	if _, err := PgToChar(`say "hi" %Y`); err == nil {
		t.Error("a literal double quote cannot be quoted")
	}
}

func TestUTS35ToStrftime(t *testing.T) {
	cases := []struct{ in, want string }{
		{"yyyy-MM-dd", "%Y-%m-%d"},
		{"dd/MM/yyyy HH:mm:ss", "%d/%m/%Y %H:%M:%S"},
		{"d/M/yy", "%-d/%-m/%y"},
		{"yyyy-MM-dd'T'HH:mm:ss", "%Y-%m-%dT%H:%M:%S"},
		{"MMM d, yyyy", "%b %-d, %Y"},
		{"hh:mm a", "%I:%M %p"},
		{"yyyy-MM-ddTHH:mm:ssZ", "%Y-%m-%dT%H:%M:%S%z"},
	}
	for _, c := range cases {
		got, err := UTS35ToStrftime(c.in)
		if err != nil || got != c.want {
			t.Errorf("%s = %q (%v), want %q", c.in, got, err, c.want)
		}
	}
	if _, err := UTS35ToStrftime("yyyy-QQ"); err == nil {
		t.Error("an unknown field must be refused by name")
	}
}
