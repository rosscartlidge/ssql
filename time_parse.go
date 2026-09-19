package ssql

import (
	"fmt"
	"time"

	"github.com/expr-lang/expr"
)

// timeLayouts are the string forms ssql reads as a time, in the order
// tried. One list serves GetOr[time.Time], the `time` wire type, `cast
// -type F time` and the expression function date(), so they cannot
// disagree (DFC128 F3: expr-lang's own date() rejected Postgres's zoneless
// JSON timestamp, which GetOr accepted, and both rejected its CSV
// timestamptz). Go accepts fractional seconds in the input for any layout
// that has seconds.
var timeLayouts = []string{
	time.RFC3339,             // 2026-01-02T10:30:00Z, …+11:00 (APIs, ssql's own output, Postgres timestamptz JSON)
	"2006-01-02 15:04:05",    // SQL datetime: DuckDB JSON/CSV, Postgres CSV timestamp
	"2006-01-02T15:04:05",    // Postgres timestamp JSON (no zone → UTC)
	"2006-01-02 15:04:05Z07", // Postgres CSV timestamptz: 2026-01-01 23:30:00+00
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02", // DATE → midnight UTC
}

// ParseTime converts a value to a time: a time.Time as is, a string in any
// of timeLayouts (zoneless forms are UTC), an int64 as Unix seconds (UTC).
func ParseTime(val any) (time.Time, bool) {
	switch v := val.(type) {
	case time.Time:
		return v, true
	case string:
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, v); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	case int64:
		return time.Unix(v, 0).UTC(), true
	}
	return time.Time{}, false
}

// MustParseTime is ParseTime for an explicit request — `cast -type F
// time` in every lane — where a value that is not a time must stop the
// pipeline rather than become a zero time.
func MustParseTime(val any, field string) time.Time {
	t, ok := ParseTime(val)
	if !ok {
		panic(fmt.Sprintf("cast: field %q value %v is not a time (accepted: RFC 3339, \"2006-01-02 15:04:05\", \"2006-01-02T15:04:05\", \"2006-01-02\", Unix seconds; use date(value, layout) in -set-expr for another layout)", field, val))
	}
	return t
}

// exprDateExtraLayouts are the forms expr-lang's builtin date() accepted
// that ParseTime does not; kept so no expression that worked stops working.
var exprDateExtraLayouts = []string{"15:04:05", time.RFC822, time.RFC850, time.RFC1123}

// exprDate is ssql's date(): date(v) parses with ParseTime (so it agrees
// with GetOr[time.Time] and the `time` wire type), date(s, layout) and
// date(s, layout, zone) keep expr-lang's explicit-layout forms.
func exprDate(args ...any) (any, error) {
	switch len(args) {
	case 1:
		if t, ok := ParseTime(args[0]); ok {
			return t, nil
		}
		if s, ok := args[0].(string); ok {
			for _, layout := range exprDateExtraLayouts {
				if t, err := time.Parse(layout, s); err == nil {
					return t, nil
				}
			}
		}
		return nil, fmt.Errorf("invalid date %v", args[0])
	case 2, 3:
		s, ok1 := args[0].(string)
		layout, ok2 := args[1].(string)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("date(value, layout[, zone]) takes strings")
		}
		if len(args) == 2 {
			t, err := time.Parse(layout, s)
			if err != nil {
				return nil, fmt.Errorf("invalid date %v", err)
			}
			return t, nil
		}
		zone, ok := args[2].(string)
		if !ok {
			return nil, fmt.Errorf("date(value, layout, zone) takes strings")
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return nil, err
		}
		t, err := time.ParseInLocation(layout, s, loc)
		if err != nil {
			return nil, fmt.Errorf("invalid date %v", err)
		}
		return t, nil
	}
	return nil, fmt.Errorf("date() takes 1 to 3 arguments, got %d", len(args))
}

// ExprCompiledFunctions names the functions ssql binds at COMPILE time
// (expr.Function) rather than through the environment. A validator that
// checks an expression's identifiers against the record must not read a
// call to one of these as a reference to a field of the same name.
var ExprCompiledFunctions = map[string]bool{"date": true}

// ExprDate replaces expr-lang's builtin date() with ssql's. It is a
// compile-time function, not an environment entry, so a record field
// named `date` still resolves as the field and date(date) works.
func ExprDate() expr.Option {
	return expr.Function("date", exprDate,
		new(func(any) time.Time),
		new(func(string, string) time.Time),
		new(func(string, string, string) time.Time),
	)
}
