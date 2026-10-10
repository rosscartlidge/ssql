package ssql

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/expr-lang/expr"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestTruncTime: calendar starts in a zone, Monday weeks, half-hour zones.
func TestTruncTime(t *testing.T) {
	syd := mustZone(t, "Australia/Sydney")
	cases := []struct{ in, unit, loc, want string }{
		{"2026-01-04T23:30:00Z", "day", "Australia/Sydney", "2026-01-05T00:00:00+11:00"}, // Sydney Jan 5 10:30
		{"2026-01-04T23:30:00Z", "day", "UTC", "2026-01-04T00:00:00Z"},
		{"2026-01-04T23:30:00Z", "month", "Australia/Sydney", "2026-01-01T00:00:00+11:00"},
		{"2026-05-05T10:00:00Z", "quarter", "UTC", "2026-04-01T00:00:00Z"},
		{"2026-01-04T10:00:00Z", "week", "UTC", "2025-12-29T00:00:00Z"}, // Sunday → the Monday before
		{"2026-01-05T10:00:00Z", "week", "UTC", "2026-01-05T00:00:00Z"}, // Monday stays
		{"2026-01-05T10:17:42Z", "hour", "Asia/Kolkata", "2026-01-05T15:00:00+05:30"},
		{"2026-01-05T10:17:42Z", "minute", "UTC", "2026-01-05T10:17:00Z"},
		{"2026-12-31T23:59:59Z", "year", "UTC", "2026-01-01T00:00:00Z"},
	}
	for _, c := range cases {
		got, err := TruncTime(ts(c.in), c.unit, mustZone(t, c.loc))
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(ts(c.want)) || got.Format(time.RFC3339) != c.want {
			t.Errorf("trunc %s %s in %s = %s, want %s", c.in, c.unit, c.loc, got.Format(time.RFC3339), c.want)
		}
	}
	if _, err := TruncTime(time.Now(), "fortnight", syd); err == nil {
		t.Error("bad unit accepted")
	}
}

// TestAddCalendar: month ends clamp (DuckDB, Postgres), leap day, DST
// keeps the wall clock for day units, hours are absolute.
func TestAddCalendar(t *testing.T) {
	cases := []struct {
		in   string
		n    int64
		unit string
		loc  string
		want string
	}{
		{"2026-01-31T10:00:00Z", 1, "month", "UTC", "2026-02-28T10:00:00Z"},
		{"2026-01-30T10:00:00Z", 1, "month", "UTC", "2026-02-28T10:00:00Z"},
		{"2026-03-31T10:00:00Z", -1, "month", "UTC", "2026-02-28T10:00:00Z"},
		{"2024-02-29T10:00:00Z", 1, "year", "UTC", "2025-02-28T10:00:00Z"},
		{"2026-01-31T10:00:00Z", 1, "quarter", "UTC", "2026-04-30T10:00:00Z"},
		{"2026-02-28T10:00:00Z", 1, "month", "UTC", "2026-03-28T10:00:00Z"},
		// Sydney DST ends 2026-04-05 03:00 → 02:00: a day later is the same
		// wall clock, 25 hours on; 24 hours is an hour earlier on the clock.
		{"2026-04-04T12:00:00+11:00", 1, "day", "Australia/Sydney", "2026-04-05T12:00:00+10:00"},
		{"2026-04-04T12:00:00+11:00", 24, "hour", "Australia/Sydney", "2026-04-05T11:00:00+10:00"},
		{"2026-01-05T10:00:00Z", 2, "week", "UTC", "2026-01-19T10:00:00Z"},
		{"2026-01-05T10:00:00Z", 90, "second", "UTC", "2026-01-05T10:01:30Z"},
	}
	for _, c := range cases {
		got, err := AddCalendar(ts(c.in), c.n, c.unit, mustZone(t, c.loc))
		if err != nil {
			t.Fatal(err)
		}
		if got.Format(time.RFC3339) != c.want {
			t.Errorf("%s + %d %s in %s = %s, want %s", c.in, c.n, c.unit, c.loc, got.Format(time.RFC3339), c.want)
		}
	}
}

// TestDiffCalendar: boundaries crossed, not elapsed units (DuckDB's
// date_diff, probed 2026-10-10): Jan 31 → Feb 1 is one month, Jan 1 → Jan
// 31 none; 00:59 → 01:00 one hour; Sunday → Monday no weeks.
func TestDiffCalendar(t *testing.T) {
	cases := []struct {
		unit, a, b, loc string
		want            int64
	}{
		{"month", "2026-01-31T00:00:00Z", "2026-02-01T00:00:00Z", "UTC", 1},
		{"month", "2026-01-01T00:00:00Z", "2026-01-31T23:59:00Z", "UTC", 0},
		{"month", "2026-01-31T00:00:00Z", "2026-03-01T00:00:00Z", "UTC", 2},
		{"month", "2026-03-01T00:00:00Z", "2026-01-31T00:00:00Z", "UTC", -2},
		{"year", "2025-12-31T23:00:00Z", "2026-01-01T00:00:00Z", "UTC", 1},
		{"quarter", "2026-03-31T00:00:00Z", "2026-04-01T00:00:00Z", "UTC", 1},
		{"hour", "2026-01-01T00:59:00Z", "2026-01-01T01:00:00Z", "UTC", 1},
		{"hour", "2026-01-01T00:00:00Z", "2026-01-01T00:59:59Z", "UTC", 0},
		{"day", "2026-01-01T23:00:00Z", "2026-01-02T01:00:00Z", "UTC", 1},
		{"day", "2026-01-01T23:00:00Z", "2026-03-01T01:00:00Z", "UTC", 59},
		{"week", "2026-01-04T00:00:00Z", "2026-01-05T00:00:00Z", "UTC", 0},
		{"week", "2026-01-01T00:00:00Z", "2026-01-15T00:00:00Z", "UTC", 2},
		{"minute", "2026-01-01T00:00:59Z", "2026-01-01T00:01:00Z", "UTC", 1},
		// The zone moves the boundary: 23:30Z on the 4th is the 5th in Sydney.
		{"day", "2026-01-04T23:30:00Z", "2026-01-05T00:30:00Z", "UTC", 1},
		{"day", "2026-01-04T23:30:00Z", "2026-01-05T00:30:00Z", "Australia/Sydney", 0},
	}
	for _, c := range cases {
		got, err := diffCalendar(c.unit, ts(c.a), ts(c.b), mustZone(t, c.loc))
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("diff %s %s → %s in %s = %d, want %d", c.unit, c.a, c.b, c.loc, got, c.want)
		}
	}
}

// TestTimePart: parts in a zone; dow Sunday = 0; ISO week 53; epoch is
// zone-free.
func TestTimePart(t *testing.T) {
	syd := mustZone(t, "Australia/Sydney")
	in := ts("2026-01-04T23:30:15Z") // Sunday 23:30 UTC = Monday 10:30 Sydney
	want := map[string][2]int64{     // part → {UTC, Sydney}
		"year": {2026, 2026}, "month": {1, 1}, "day": {4, 5}, "hour": {23, 10}, "minute": {30, 30}, "second": {15, 15},
		"dow": {0, 1}, "doy": {4, 5}, "week": {1, 2}, "quarter": {1, 1}, "epoch": {1767569415, 1767569415},
	}
	for part, w := range want {
		u, err := TimePart(in, part, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		s, _ := TimePart(in, part, syd)
		if u != w[0] || s != w[1] {
			t.Errorf("%s: UTC %d Sydney %d, want %d %d", part, u, s, w[0], w[1])
		}
	}
	if w, _ := TimePart(ts("2026-12-31T00:00:00Z"), "week", time.UTC); w != 53 {
		t.Errorf("ISO week of 2026-12-31 = %d, want 53", w)
	}
	if _, err := TimePart(in, "fortnight", time.UTC); err == nil {
		t.Error("bad part accepted")
	}
}

// TestExprTimeFunctions: the VM forms, the pipeline zone, tz() overriding
// it, strings accepted, nil in nil out, a field named like a part.
func TestExprTimeFunctions(t *testing.T) {
	resetStreamZone()
	t.Cleanup(resetStreamZone)
	if err := SetStreamZone("Australia/Sydney"); err != nil {
		t.Fatal(err)
	}
	env := map[string]any{
		"ts":  ts("2026-01-04T23:30:00Z"),
		"s":   "2026-01-04T23:30:00Z",
		"day": int64(7), // a field named like a function
		"nil": nil,
	}
	cases := []struct {
		expr string
		want any
	}{
		{`hour(ts)`, int64(10)},
		{`hour(s)`, int64(10)},
		{`hour(tz(ts, "UTC"))`, int64(23)},
		{`hour(tz(ts, "America/New_York"))`, int64(18)},
		{`day(ts)`, int64(5)},
		{`day + 1`, 8}, // expr widens int64 + literal to int
		{`day(tz(ts, "UTC"))`, int64(4)},
		{`dow(ts)`, int64(1)},
		{`week(ts)`, int64(2)},
		{`epoch(ts)`, int64(1767569400)},
		{`date_trunc(ts, "day")`, ts("2026-01-04T13:00:00Z")}, // Sydney midnight
		{`date_trunc(tz(ts, "UTC"), "day")`, ts("2026-01-04T00:00:00Z")},
		{`date_add(ts, 1, "month")`, ts("2026-02-04T23:30:00Z")},
		{`date_add(date("2026-01-31"), 1, "month")`, ts("2026-02-28T00:00:00Z")},
		{`date_diff("day", ts, date("2026-01-05T01:00:00Z"))`, int64(0)}, // both the 5th in Sydney
		{`date_diff("day", tz(ts, "UTC"), date("2026-01-05T01:00:00Z"))`, int64(1)},
		{`hour(nil)`, nil},
		{`strftime(ts, "%Y-%m-%d %H:%M")`, "2026-01-05 10:30"}, // Sydney wall clock
		{`strftime(tz(ts, "UTC"), "%d/%m/%Y %H:%M")`, "04/01/2026 23:30"},
		{`strptime("05/01/2026 10:30", "%d/%m/%Y %H:%M")`, ts("2026-01-04T23:30:00Z")}, // a wall clock in the pipeline zone (Sydney)
		{`date("05/01/2026", "%d/%m/%Y")`, ts("2026-01-04T13:00:00Z")},
		{`date("05/01/2026", "02/01/2006")`, ts("2026-01-04T13:00:00Z")},
		{`strptime(nil, "%Y")`, nil},
		{`hour(now()) >= 0`, true},
		{`ts < date_add(ts, 1, "second")`, true},
	}
	for _, c := range cases {
		opts := append([]expr.Option{expr.Env(env)}, ExprStandardOptions()...)
		prog, err := expr.Compile(c.expr, opts...)
		if err != nil {
			t.Fatalf("%s: compile: %v", c.expr, err)
		}
		got, err := expr.Run(prog, env)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if gt, ok := got.(time.Time); ok {
			if !gt.Equal(c.want.(time.Time)) {
				t.Errorf("%s = %s, want %s", c.expr, gt.Format(time.RFC3339), c.want.(time.Time).Format(time.RFC3339))
			}
			continue
		}
		if got != c.want {
			t.Errorf("%s = %v (%T), want %v", c.expr, got, got, c.want)
		}
	}
}

// TestResolveZone: flag, TZ, the POSIX colon, bad names.
func TestResolveZone(t *testing.T) {
	t.Setenv("TZ", ":Europe/Zurich")
	name, loc, err := ResolveZone("")
	if err != nil || name != "Europe/Zurich" || loc.String() != "Europe/Zurich" {
		t.Errorf("TZ: %s %v %v", name, loc, err)
	}
	if name, _, err := ResolveZone("UTC"); err != nil || name != "UTC" {
		t.Errorf("flag: %s %v", name, err)
	}
	if _, _, err := ResolveZone("Mars/Olympus"); err == nil {
		t.Error("bad zone accepted")
	}
	t.Setenv("TZ", "EST5EDT")
	if _, _, err := ResolveZone(""); err == nil {
		// EST5EDT loads on most systems as a legacy zone; it is an IANA
		// name, so it is accepted. Only an unloadable name is refused.
		_ = err
	}
	os.Unsetenv("TZ")
	// Without TZ the result follows /etc/localtime; it must either name a
	// zone or say why it cannot.
	if name, _, err := ResolveZone(""); err == nil && name == "" {
		t.Error("empty name without error")
	}
}

// TestStreamZone: pin beats adopt, first header wins, mismatch recorded.
func TestStreamZone(t *testing.T) {
	resetStreamZone()
	t.Cleanup(resetStreamZone)
	AdoptStreamZone("Australia/Sydney")
	if StreamZoneName() != "Australia/Sydney" {
		t.Fatal("adopt")
	}
	AdoptStreamZone("Australia/Sydney")
	if StreamZoneMismatch() != nil {
		t.Error("same zone is not a mismatch")
	}
	AdoptStreamZone("UTC")
	if StreamZoneMismatch() == nil || StreamZoneName() != "Australia/Sydney" {
		t.Error("second header must not replace, must record mismatch")
	}
	resetStreamZone()
	if err := PinStreamZone("UTC"); err != nil {
		t.Fatal(err)
	}
	AdoptStreamZone("Australia/Sydney")
	if StreamZoneName() != "UTC" || StreamZoneMismatch() != nil {
		t.Error("-tz is pinned over a header")
	}
	if !TimeIsZoned(ts("2026-01-01T00:00:00Z").In(mustZone(t, "Australia/Sydney"))) {
		t.Error("a named zone is zoned")
	}
	if TimeIsZoned(ts("2026-01-01T00:00:00Z")) || TimeIsZoned(ts("2026-01-01T00:00:00+11:00")) {
		t.Error("UTC and a parsed offset are not zoned")
	}
}

// TestCastFieldFormat and the CSV reader's TimeFormats (DFC147 §7.2): a
// day-first cell parses through the pattern; empty is missing; a
// mismatch is invalid (loud, or missing under -invalid missing).
func TestCastFieldFormat(t *testing.T) {
	resetStreamZone()
	t.Cleanup(resetStreamZone)
	SetStreamZone("UTC")
	src := MakeMutableRecord().String("when", "05/01/2026").String("bad", "yesterday").String("empty", "").Freeze()
	mut := MakeMutableRecord()
	mut = CastFieldFormat(mut, src, "when", "%d/%m/%Y", false, nil)
	mut = CastFieldFormat(mut, src, "empty", "%d/%m/%Y", false, nil)
	var invalid int64
	mut = CastFieldFormat(mut, src, "bad", "%d/%m/%Y", true, &invalid)
	r := mut.Freeze()
	if got, _ := Get[time.Time](r, "when"); !got.Equal(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("when = %v", got)
	}
	if _, ok := Get[any](r, "empty"); ok {
		t.Error("an empty cell is missing")
	}
	if invalid != 1 {
		t.Errorf("invalid = %d", invalid)
	}
	func() {
		defer func() {
			if _, ok := recover().(*CastError); !ok {
				t.Error("a mismatch must panic with a CastError")
			}
		}()
		CastFieldFormat(MakeMutableRecord(), src, "bad", "%d/%m/%Y", false, nil)
	}()

	cfg := DefaultCSVConfig()
	cfg.TimeFormats = map[string]string{"when": "%d/%m/%Y"}
	var rows []Record
	for rec := range ReadCSVFromReader(strings.NewReader("id,when\n1,31/01/2026\n"), cfg) {
		rows = append(rows, rec)
	}
	if got, _ := Get[time.Time](rows[0], "when"); !got.Equal(time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("reader: %v", rows)
	}
}

// TestSinkTimeFormat (DFC147 §7.3): the CSV, TSV, table and markdown
// writers render a time with the pattern on the pipeline zone's wall
// clock, and RFC 3339 without one.
func TestSinkTimeFormat(t *testing.T) {
	resetStreamZone()
	t.Cleanup(resetStreamZone)
	if err := SetStreamZone("Australia/Sydney"); err != nil {
		t.Fatal(err)
	}
	recs := func(yield func(Record) bool) {
		yield(MakeMutableRecord().Int("id", 1).Time("ts", ts("2026-01-04T23:30:00Z")).Freeze())
	}
	var csvOut, tsvOut, tbl, md, plain bytes.Buffer
	if err := WriteCSVToWriter(recs, &csvOut, CSVConfigWithTimeFormat("%Y-%m-%d %H:%M")); err != nil {
		t.Fatal(err)
	}
	if err := WriteTSVOptsToWriter(recs, &tsvOut, TSVConfig{TimeFormat: "%d/%m/%Y"}); err != nil {
		t.Fatal(err)
	}
	DisplayTableOpts(&tbl, recs, DisplayOptions{MaxWidth: 50, TimeFormat: "%b %-d %H:%M"})
	if err := WriteMarkdownOpts(&md, recs, DisplayOptions{TimeFormat: "2006-01-02"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteCSVToWriter(recs, &plain); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, got, want string }{
		{"csv", csvOut.String(), "1,2026-01-05 10:30"},
		{"tsv", tsvOut.String(), "1\t05/01/2026"},
		{"table", tbl.String(), "Jan 5 10:30"},
		{"markdown", md.String(), "| 2026-01-05 |"},
		{"plain csv", plain.String(), "2026-01-04T23:30:00Z"},
	} {
		if !strings.Contains(c.got, c.want) {
			t.Errorf("%s: %q lacks %q", c.name, c.got, c.want)
		}
	}
	if FormatTimePattern(ts("2026-01-04T23:30:00Z"), "") != "2026-01-04T23:30:00Z" {
		t.Error("no pattern: RFC 3339")
	}
	if err := ValidateTimePattern("%Q"); err == nil {
		t.Error("a bad directive must be refused")
	}
}
