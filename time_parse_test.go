package ssql

import (
	"testing"
	"time"

	"github.com/expr-lang/expr"
)

// TestParseTime: one parser behind GetOr[time.Time], the `time` wire
// type, cast and date() — every form DuckDB, Postgres and ssql itself
// write (DFC128 F3/D1), zoneless forms as UTC, junk refused.
func TestParseTime(t *testing.T) {
	want := time.Date(2026, 1, 2, 10, 30, 0, 0, time.UTC)
	for _, in := range []any{
		"2026-01-02T10:30:00Z",      // RFC 3339 (ssql's own output)
		"2026-01-02T21:30:00+11:00", // RFC 3339 with offset (Postgres timestamptz JSON)
		"2026-01-02 10:30:00",       // SQL datetime (DuckDB, Postgres CSV timestamp)
		"2026-01-02T10:30:00",       // Postgres timestamp JSON — expr-lang's date() refused this
		"2026-01-02 10:30:00+00",    // Postgres CSV timestamptz — refused everywhere before
		"2026-01-02 21:30:00+11:00",
		want,
		want.Unix(),
	} {
		got, ok := ParseTime(in)
		if !ok || !got.Equal(want) {
			t.Errorf("ParseTime(%v) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if got, ok := ParseTime("2026-01-02 10:30:00.123456"); !ok || got.Nanosecond() != 123456000 {
		t.Errorf("fractional seconds: %v %v", got, ok)
	}
	if got, ok := ParseTime("2026-01-02"); !ok || !got.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("a DATE is midnight UTC, got %v %v", got, ok)
	}
	for _, bad := range []any{"", "banana", "02/01/2026", 2.5, nil, true} {
		if _, ok := ParseTime(bad); ok {
			t.Errorf("ParseTime(%v) must fail", bad)
		}
	}
	// GetOr agrees, and reading a time as a string gives the wire form.
	r := MakeMutableRecord().String("s", "2026-01-02T10:30:00").Time("t", want).Freeze()
	if got := GetOr(r, "s", time.Time{}); !got.Equal(want) {
		t.Errorf("GetOr[time.Time] = %v", got)
	}
	if got := GetOr(r, "t", ""); got != "2026-01-02T10:30:00Z" {
		t.Errorf("GetOr[string] of a time = %q, want RFC 3339", got)
	}
}

func TestMustParseTimePanicsWithTheField(t *testing.T) {
	defer func() {
		// An ERROR, not a string: generated programs report a panic as one
		// `Error:` line only when it is an error.
		err, _ := recover().(error)
		if err == nil || !contains(err.Error(), `field "ts"`) || !contains(err.Error(), "junk") {
			t.Errorf("panic %v must be an error naming the field and the value", err)
		}
	}()
	MustParseTime("junk", "ts")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestExprDate: ssql's date() replaces expr-lang's. It is bound at compile
// time, so a record field NAMED date still resolves as the field —
// date(date) works — and the explicit-layout forms are kept.
func TestExprDate(t *testing.T) {
	run := func(src string, env map[string]any) (any, error) {
		p, err := expr.Compile(src, expr.Env(map[string]any{}), expr.AllowUndefinedVariables(), ExprFieldShadowing(), ExprDate())
		if err != nil {
			return nil, err
		}
		return expr.Run(p, env)
	}
	env := map[string]any{"date": "2026-01-02", "pg": "2026-01-02T10:30:00", "tz": "2026-01-01 23:30:00+00"}
	for src, want := range map[string]any{
		`date(pg).Hour()`:                          10,
		`date(tz).UTC().Day()`:                     1,
		`date(date).Year()`:                        2026, // the field, inside the function
		`date > "2026"`:                            true, // the field alone
		`date(date) < date(pg)`:                    true,
		`date("02/01/2026", "02/01/2006").Month()`: time.January,
		`date("10:30:00").Hour()`:                  10, // expr-lang's clock-only layout, kept
	} {
		got, err := run(src, env)
		if err != nil || got != want {
			t.Errorf("%s = %v (%T), %v; want %v", src, got, got, err, want)
		}
	}
	if _, err := run(`date("banana")`, env); err == nil || !contains(err.Error(), "invalid date banana") {
		t.Errorf("junk must fail loudly, got %v", err)
	}
	if !ExprCompiledFunctions["date"] {
		t.Error("date must be listed as a compile-time function for field validators")
	}
}

// TestTimeRendersAsRFC3339: every text form of a time is the wire form —
// never Go's Time.String(), which nothing parses back.
func TestTimeRendersAsRFC3339(t *testing.T) {
	tm := time.Date(2026, 1, 2, 10, 30, 0, 0, time.UTC)
	const want = "2026-01-02T10:30:00Z"
	if got := formatValue(tm); got != want {
		t.Errorf("formatValue = %q", got)
	}
	if got := displayValue(tm); got != want {
		t.Errorf("displayValue = %q", got)
	}
	if got := displayValue(int64(7)); got != "7" {
		t.Errorf("displayValue(7) = %q", got)
	}
}

// TestBucketValueTime: a time in, a time out, on the epoch grid.
func TestBucketValueTime(t *testing.T) {
	got, err := BucketValue(time.Date(2026, 1, 20, 13, 45, 0, 0, time.UTC), 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if tm, ok := got.(time.Time); !ok || !tm.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("BucketValue = %v (%T)", got, got)
	}
}

func TestFieldTypeTime(t *testing.T) {
	for _, name := range []string{"time", "timestamp", "datetime", "date", "TIME"} {
		if ft, err := ParseFieldType(name); err != nil || ft != FieldTypeTime {
			t.Errorf("ParseFieldType(%q) = %v, %v", name, ft, err)
		}
	}
	if FieldTypeTime.String() != "time" {
		t.Errorf("String() = %q", FieldTypeTime.String())
	}
	if _, err := ParseFieldType("instant"); err == nil || !contains(err.Error(), "time") {
		t.Errorf("the unknown-type message must list time: %v", err)
	}
}
