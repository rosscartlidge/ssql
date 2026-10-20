package ssql

import (
	"fmt"
	"strings"
	"time"

	"github.com/expr-lang/expr"
)

// Calendar functions over an instant (DFC147 §4, §5). One implementation
// for every lane: the expression VM (ExprTimeFunctions), transpiled typed
// and record programs (the Must* forms), and the SQL translator's oracle
// in the equivalence harness. The zone is a parameter: callers pass the
// pipeline's (StreamZone) or an explicit tz().
//
// Semantics pinned here, probed against DuckDB 1.5.0 (DFC147 §5):
//   - the calendar is read in loc; the result of a truncation or an
//     addition is an instant (time.Time in loc);
//   - week starts Monday (ISO); dow is Sunday = 0 (Go, DuckDB, Postgres);
//   - adding months or years clamps to the month's end (Jan 31 + 1 month
//     is Feb 28), where Go's AddDate would roll into March;
//   - a difference counts unit BOUNDARIES crossed (Jan 31 → Feb 1 is one
//     month; 00:59 → 01:00 is one hour), DuckDB's date_diff; week is the
//     day difference / 7, truncated.

// CalendarUnits are the units date_trunc, date_add and date_diff accept.
var CalendarUnits = []string{"year", "quarter", "month", "week", "day", "hour", "minute", "second"}

// TimeParts are the names of the part functions.
var TimeParts = []string{"year", "month", "day", "hour", "minute", "second", "dow", "doy", "week", "quarter", "epoch"}

// ValidCalendarUnit reports whether unit is one of CalendarUnits.
func ValidCalendarUnit(unit string) bool {
	for _, u := range CalendarUnits {
		if u == unit {
			return true
		}
	}
	return false
}

func badUnit(fn, unit string) error {
	return fmt.Errorf("%s: unit %q is not one of %s", fn, unit, strings.Join(CalendarUnits, ", "))
}

// TruncTime floors t to the start of its unit in loc.
func TruncTime(t time.Time, unit string, loc *time.Location) (time.Time, error) {
	lt := t.In(loc)
	y, m, d := lt.Date()
	switch unit {
	case "year":
		return time.Date(y, 1, 1, 0, 0, 0, 0, loc), nil
	case "quarter":
		return time.Date(y, (m-1)/3*3+1, 1, 0, 0, 0, 0, loc), nil
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, loc), nil
	case "week":
		return time.Date(y, m, d-int(lt.Weekday()+6)%7, 0, 0, 0, 0, loc), nil
	case "day":
		return time.Date(y, m, d, 0, 0, 0, 0, loc), nil
	case "hour":
		return time.Date(y, m, d, lt.Hour(), 0, 0, 0, loc), nil
	case "minute":
		return time.Date(y, m, d, lt.Hour(), lt.Minute(), 0, 0, loc), nil
	case "second":
		return time.Date(y, m, d, lt.Hour(), lt.Minute(), lt.Second(), 0, loc), nil
	}
	return time.Time{}, badUnit("date_trunc", unit)
}

// AddCalendar adds n units to t in loc's calendar. Month-based units keep
// the wall clock and clamp the day to the target month's length; day and
// week keep the wall clock across a DST change; hour, minute and second
// are absolute.
func AddCalendar(t time.Time, n int64, unit string, loc *time.Location) (time.Time, error) {
	lt := t.In(loc)
	y, m, d := lt.Date()
	hh, mm, ss, ns := lt.Hour(), lt.Minute(), lt.Second(), lt.Nanosecond()
	months := int64(0)
	switch unit {
	case "year":
		months = 12 * n
	case "quarter":
		months = 3 * n
	case "month":
		months = n
	case "week":
		return time.Date(y, m, d+int(7*n), hh, mm, ss, ns, loc), nil
	case "day":
		return time.Date(y, m, d+int(n), hh, mm, ss, ns, loc), nil
	case "hour":
		return t.Add(time.Duration(n) * time.Hour).In(loc), nil
	case "minute":
		return t.Add(time.Duration(n) * time.Minute).In(loc), nil
	case "second":
		return t.Add(time.Duration(n) * time.Second).In(loc), nil
	default:
		return time.Time{}, badUnit("date_add", unit)
	}
	// First of the target month, then the clamped day.
	first := time.Date(y, m+time.Month(months), 1, hh, mm, ss, ns, loc)
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, loc).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, hh, mm, ss, ns, loc), nil
}

// DiffCalendar counts the unit boundaries crossed going from a to b in
// loc's calendar (negative when b precedes a).
func DiffCalendar(unit string, a, b time.Time, loc *time.Location) (int64, error) {
	la, lb := a.In(loc), b.In(loc)
	switch unit {
	case "year":
		return int64(lb.Year() - la.Year()), nil
	case "quarter":
		return int64((lb.Year()*4 + (int(lb.Month())-1)/3) - (la.Year()*4 + (int(la.Month())-1)/3)), nil
	case "month":
		return int64((lb.Year()*12 + int(lb.Month())) - (la.Year()*12 + int(la.Month()))), nil
	case "week":
		return civilDays(lb) - civilDays(la), nil // callers divide: see below
	case "day":
		return civilDays(lb) - civilDays(la), nil
	case "hour", "minute", "second":
		ta, err := TruncTime(a, unit, loc)
		if err != nil {
			return 0, err
		}
		tb, _ := TruncTime(b, unit, loc)
		d := map[string]time.Duration{"hour": time.Hour, "minute": time.Minute, "second": time.Second}[unit]
		return int64(tb.Sub(ta) / d), nil
	}
	return 0, badUnit("date_diff", unit)
}

// civilDays is the day number of lt's calendar date.
func civilDays(lt time.Time) int64 {
	y, m, d := lt.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

// diffCalendar applies the week rule on top of DiffCalendar.
func diffCalendar(unit string, a, b time.Time, loc *time.Location) (int64, error) {
	n, err := DiffCalendar(unit, a, b, loc)
	if err != nil {
		return 0, err
	}
	if unit == "week" {
		n /= 7
	}
	return n, nil
}

// TimePart reads one calendar part of t in loc: year, month, day, hour,
// minute, second, dow (Sunday = 0), doy, week (ISO), quarter, epoch (Unix
// seconds, zone-free).
func TimePart(t time.Time, part string, loc *time.Location) (int64, error) {
	lt := t.In(loc)
	switch part {
	case "year":
		return int64(lt.Year()), nil
	case "month":
		return int64(lt.Month()), nil
	case "day":
		return int64(lt.Day()), nil
	case "hour":
		return int64(lt.Hour()), nil
	case "minute":
		return int64(lt.Minute()), nil
	case "second":
		return int64(lt.Second()), nil
	case "dow":
		return int64(lt.Weekday()), nil
	case "doy":
		return int64(lt.YearDay()), nil
	case "week":
		_, w := lt.ISOWeek()
		return int64(w), nil
	case "quarter":
		return int64((lt.Month()-1)/3 + 1), nil
	case "epoch":
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("time part %q is not one of %s", part, strings.Join(TimeParts, ", "))
}

// The Must* forms serve generated code, where the unit was validated at
// generation time: the only possible error is a programming error.

func MustTruncTime(t time.Time, unit string, loc *time.Location) time.Time {
	r, err := TruncTime(t, unit, loc)
	if err != nil {
		panic(err)
	}
	return r
}

func MustAddCalendar(t time.Time, n int64, unit string, loc *time.Location) time.Time {
	r, err := AddCalendar(t, n, unit, loc)
	if err != nil {
		panic(err)
	}
	return r
}

func MustDiffCalendar(unit string, a, b time.Time, loc *time.Location) int64 {
	r, err := diffCalendar(unit, a, b, loc)
	if err != nil {
		panic(err)
	}
	return r
}

func MustTimePart(t time.Time, part string, loc *time.Location) int64 {
	r, err := TimePart(t, part, loc)
	if err != nil {
		panic(err)
	}
	return r
}

// TimeIsZoned reports whether t carries a NAMED zone — set by tz() or
// date(str, layout, zone) — rather than what a reader leaves on a parsed
// value: UTC, Local, or a nameless fixed offset. A zoned time is read in
// its own zone; an unzoned one in the pipeline zone.
func TimeIsZoned(t time.Time) bool {
	loc := t.Location()
	if loc == time.UTC || loc == time.Local {
		return false
	}
	name := loc.String()
	return name != "" && name != "Local"
}

// zonedLocation is the location tz() attaches: the named one, except UTC,
// which must not be time.UTC itself (that is what an unzoned value
// carries) — a fixed zone named "UTC" reads as zoned.
func zonedLocation(name string, loc *time.Location) *time.Location {
	if loc == time.UTC {
		return time.FixedZone("UTC", 0)
	}
	return loc
}

// --- expression functions -------------------------------------------

// exprTimeArg reads a time argument: a time, or anything ParseTime reads
// (bucket's rule — an untyped RFC 3339 column works without a cast). nil
// is nil.
func exprTimeArg(fn string, v any) (time.Time, bool, error) {
	if v == nil {
		return time.Time{}, false, nil
	}
	t, ok := ParseTime(v)
	if !ok {
		return time.Time{}, false, fmt.Errorf("%s: %v is not a time", fn, v)
	}
	return t, true, nil
}

func exprInt(fn string, v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case float64:
		if x == float64(int64(x)) {
			return int64(x), nil
		}
	}
	return 0, fmt.Errorf("%s: %v is not a whole number", fn, v)
}

func exprString(fn, what string, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: the %s must be a string, got %T", fn, what, v)
	}
	return s, nil
}

// ExprTimeFunctions binds the calendar functions at compile time (like
// ExprDate, so a field named `day` or `month` still resolves as the
// field): year(ts), month, day, hour, minute, second, dow, doy, week,
// quarter, epoch; date_trunc(ts, unit); date_add(ts, n, unit);
// date_diff(unit, a, b); tz(ts, zone); now(). The zone is the pipeline's
// (StreamZone) unless the argument came through tz().
func ExprTimeFunctions() []expr.Option {
	zone := func(fn string) (*time.Location, error) {
		_, loc, err := StreamZone()
		if err != nil {
			return nil, fmt.Errorf("%s: %v", fn, err)
		}
		return loc, nil
	}
	// A value that came through tz() (or date(str, layout, zone)) carries a
	// named zone in the time itself and is read there; a value a reader
	// parsed carries UTC, Local or a nameless fixed offset and is read in
	// the pipeline zone (DFC147 §5.1).
	locOf := func(fn string, t time.Time) (*time.Location, error) {
		if TimeIsZoned(t) {
			return t.Location(), nil
		}
		return zone(fn)
	}
	var opts []expr.Option
	// Each function is bound under its name and under exprFnAlias(name):
	// ExprFieldShadowing rewrites a CALL to the alias, so a compile env
	// that declares a variable of the same name (group-by -expr's field
	// arrays; a column called `day`) does not make the call "not callable".
	bind := func(name string, fn func(args ...any) (any, error), sig any) {
		opts = append(opts, expr.Function(name, fn, sig), expr.Function(exprFnAlias(name), fn, sig))
	}
	for _, part := range TimeParts {
		part := part
		bind(part, func(args ...any) (any, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("%s() takes one argument", part)
			}
			t, ok, err := exprTimeArg(part, args[0])
			if err != nil || !ok {
				return nil, err
			}
			loc, err := locOf(part, t)
			if err != nil {
				return nil, err
			}
			return TimePart(t, part, loc)
		}, new(func(any) int64))
	}
	bind("date_trunc", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("date_trunc(ts, unit) takes two arguments")
		}
		unit, err := exprString("date_trunc", "unit", args[1])
		if err != nil {
			return nil, err
		}
		t, ok, err := exprTimeArg("date_trunc", args[0])
		if err != nil || !ok {
			return nil, err
		}
		loc, err := locOf("date_trunc", t)
		if err != nil {
			return nil, err
		}
		return TruncTime(t, unit, loc)
	}, new(func(any, string) time.Time))
	bind("date_add", func(args ...any) (any, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("date_add(ts, n, unit) takes three arguments")
		}
		n, err := exprInt("date_add", args[1])
		if err != nil {
			return nil, err
		}
		unit, err := exprString("date_add", "unit", args[2])
		if err != nil {
			return nil, err
		}
		t, ok, err := exprTimeArg("date_add", args[0])
		if err != nil || !ok {
			return nil, err
		}
		loc, err := locOf("date_add", t)
		if err != nil {
			return nil, err
		}
		return AddCalendar(t, n, unit, loc)
	}, new(func(any, any, string) time.Time))
	bind("date_diff", func(args ...any) (any, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("date_diff(unit, start, end) takes three arguments")
		}
		unit, err := exprString("date_diff", "unit", args[0])
		if err != nil {
			return nil, err
		}
		a, okA, err := exprTimeArg("date_diff", args[1])
		if err != nil {
			return nil, err
		}
		b, okB, err := exprTimeArg("date_diff", args[2])
		if err != nil {
			return nil, err
		}
		if !okA || !okB {
			return nil, nil
		}
		loc, err := locOf("date_diff", a)
		if err != nil {
			return nil, err
		}
		return diffCalendar(unit, a, b, loc)
	}, new(func(string, any, any) int64))
	bind("tz", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("tz(ts, zone) takes two arguments")
		}
		name, err := exprString("tz", "zone", args[1])
		if err != nil {
			return nil, err
		}
		loc, err := time.LoadLocation(name)
		if err != nil {
			return nil, fmt.Errorf("tz: %v", err)
		}
		t, ok, err := exprTimeArg("tz", args[0])
		if err != nil || !ok {
			return nil, err
		}
		return t.In(zonedLocation(name, loc)), nil
	}, new(func(any, string) time.Time))
	bind("strftime", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("strftime(ts, pattern) takes two arguments")
		}
		pattern, err := exprString("strftime", "pattern", args[1])
		if err != nil {
			return nil, err
		}
		t, ok, err := exprTimeArg("strftime", args[0])
		if err != nil || !ok {
			return nil, err
		}
		loc, err := locOf("strftime", t)
		if err != nil {
			return nil, err
		}
		return FormatStrftime(t.In(loc), pattern)
	}, new(func(any, string) string))
	bind("strptime", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("strptime(text, pattern) takes two arguments")
		}
		if args[0] == nil {
			return nil, nil
		}
		s, err := exprString("strptime", "text", args[0])
		if err != nil {
			return nil, err
		}
		pattern, err := exprString("strptime", "pattern", args[1])
		if err != nil {
			return nil, err
		}
		return ParseStrftime(s, pattern)
	}, new(func(any, string) time.Time))
	opts = append(opts, expr.DisableBuiltin("now"))
	bind("now", func(args ...any) (any, error) {
		loc, err := zone("now")
		if err != nil {
			return nil, err
		}
		return time.Now().In(loc), nil
	}, new(func() time.Time))
	return opts
}

// ExprFnAliasPrefix marks the collision-free name ExprFieldShadowing
// rewrites a call to a compile-time function into; an identifier walker
// must not take it for a field.
const ExprFnAliasPrefix = "__fn_"

func exprFnAlias(name string) string { return ExprFnAliasPrefix + name }

// ExprStandardOptions are the compile options every ssql expression site
// passes — field shadowing, ssql's date(), the calendar functions — so
// exec, generated code and aggregation expressions agree.
func ExprStandardOptions() []expr.Option {
	opts := append([]expr.Option{ExprFieldShadowing(), ExprDate()}, ExprTimeFunctions()...)
	return append(opts, ExprMathFunctions()...)
}
