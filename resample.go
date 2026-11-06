package ssql

// Time-series resampling (DFC121): snap a timestamp field to an
// EPOCH-ALIGNED grid of -every periods and carry one or more numeric
// value fields onto the grid points via previous (LOCF), next
// (backfill), or linear interpolation — the industry-standard trio
// (pandas ffill/bfill/interpolate, TimescaleDB locf/interpolate,
// QuestDB FILL(PREV/LINEAR)).
//
// Design decisions (all recorded in the DFC):
//   - The grid is epoch-aligned (Postgres date_bin / Timescale
//     time_bucket convention): stability across appends/reruns,
//     joinability of independently resampled series, human
//     boundaries. SnapToBucket is exported and shared with the
//     bucket() expression function so downsampling (bucket +
//     group-by) lands on the SAME grid.
//   - Timestamp output preserves the input family: RFC3339 in →
//     RFC3339 out; epoch int in → epoch int out, same unit.
//   - Edge policy (amended 2026-09-01, forced by the typed lane and
//     better for charts): grid points outside a field's observed
//     range CLAMP to the nearest available observation, loudly
//     counted on Warn. Absent fields are unrepresentable in typed
//     structs, so absence would make the five lanes permanently
//     non-identical — and charts hate holes anyway.
//   - Unsorted input is fine (we sort); duplicate timestamps: last
//     one wins, counted loudly.

import (
	"fmt"
	"io"
	"iter"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// Fill modes.
const (
	FillPrevious = "previous"
	FillNext     = "next"
	FillLinear   = "linear"
)

// ResampleConfig configures Resample. TimeField and Every are
// required; Values must name at least one numeric field.
type ResampleConfig struct {
	TimeField  string
	Every      time.Duration
	Values     []string
	Fill       string // previous (default) | next | linear
	From, To   string // optional bounds, same formats as the data
	TimeUnit   string // "", "s", "ms", "us", "ns" — override epoch detection
	TimeFormat string // optional Go layout for string timestamps
	Warn       io.Writer
}

// CheckFields refuses a -value that is the -time field (or named twice):
// the output has one column per name, and exec wrote the key twice where
// typed code did not compile (DFC163 §7). The command calls it at parse
// time, so every lane refuses alike.
func (cfg ResampleConfig) CheckFields() error {
	seen := map[string]bool{cfg.TimeField: true}
	for _, v := range cfg.Values {
		if v == cfg.TimeField {
			return fmt.Errorf("resample: -value %q is the -time field", v)
		}
		if seen[v] {
			return fmt.Errorf("resample: -value %q is named twice", v)
		}
		seen[v] = true
	}
	return nil
}

// SnapToBucket floors an epoch-nanosecond timestamp to its
// epoch-aligned bucket start for the given period. Euclidean floor:
// correct for pre-1970 timestamps too.
func SnapToBucket(ns int64, every time.Duration) int64 {
	return exprfn.SnapNanos(ns, int64(every))
}

// tsCodec converts between the input's timestamp representation and
// epoch nanoseconds, preserving the family on output.
type tsCodec struct {
	kind   string // "int", "int128", "decimal", "float", "string", "time"
	unit   time.Duration
	layout string
	scale  int // a decimal axis's
}

func detectEpochUnit(v float64) time.Duration {
	return time.Duration(exprfn.DetectEpochUnitNanos(v))
}

func unitName(u time.Duration) string {
	switch u {
	case time.Nanosecond:
		return "ns"
	case time.Microsecond:
		return "us"
	case time.Millisecond:
		return "ms"
	default:
		return "s"
	}
}

func unitFromName(n string) (time.Duration, error) {
	switch n {
	case "ns":
		return time.Nanosecond, nil
	case "us":
		return time.Microsecond, nil
	case "ms":
		return time.Millisecond, nil
	case "s":
		return time.Second, nil
	}
	return 0, fmt.Errorf("unknown -time-unit %q (ns|us|ms|s)", n)
}

var tsStringLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// noteEpochUnit reports an auto-detected epoch unit on stderr — but only
// when it is not seconds. A 10-digit epoch being seconds is the obvious
// reading and the note carried no information; a millisecond or
// nanosecond detection is the surprising case worth a line. `-time-unit`
// silences it either way.
func noteEpochUnit(warn io.Writer, u time.Duration) {
	if u == time.Second {
		return
	}
	fmt.Fprintf(warn, "resample: epoch unit auto-detected as %s (override with -time-unit)\n", unitName(u))
}

func newTSCodec(sample any, cfg ResampleConfig, warn io.Writer) (*tsCodec, error) {
	// ssql:domain fixed: a resample axis is an int, int128, decimal, float, string or time
	switch v := sample.(type) {
	case int64:
		u := time.Second
		if cfg.TimeUnit != "" {
			var err error
			if u, err = unitFromName(cfg.TimeUnit); err != nil {
				return nil, err
			}
		} else {
			u = detectEpochUnit(float64(v))
			noteEpochUnit(warn, u)
		}
		return &tsCodec{kind: "int", unit: u}, nil
	case float64:
		u := time.Second
		if cfg.TimeUnit != "" {
			var err error
			if u, err = unitFromName(cfg.TimeUnit); err != nil {
				return nil, err
			}
		} else {
			u = detectEpochUnit(v)
			noteEpochUnit(warn, u)
		}
		return &tsCodec{kind: "float", unit: u}, nil
	case string:
		if cfg.TimeFormat != "" {
			// strftime or a Go layout, by the % rule (DFC147 §7.3).
			layout, err := TimeLayout(cfg.TimeFormat)
			if err != nil {
				return nil, fmt.Errorf("resample -time-format: %w", err)
			}
			if _, err := time.Parse(layout, v); err != nil {
				return nil, fmt.Errorf("resample: %q does not parse with -time-format %q: %w", v, cfg.TimeFormat, err)
			}
			return &tsCodec{kind: "string", layout: layout}, nil
		}
		for _, l := range tsStringLayouts {
			if _, err := time.Parse(l, v); err == nil {
				return &tsCodec{kind: "string", layout: l}, nil
			}
		}
		return nil, fmt.Errorf("resample: cannot parse timestamp %q (tried RFC3339 and SQL datetime; use -time-format)", v)
	case time.Time:
		// A `time` column (cast -type F time, or a header that says so —
		// DFC128 D1): time in, time out. No layout to preserve, no unit to
		// detect.
		return &tsCodec{kind: "time"}, nil
	case Int128, Decimal:
		// A wide or decimal epoch (Ross, DFC163 §7): the unit as for an
		// int, the grid points back in the column's own type.
		kind, scale := "int128", 0
		mag := 0.0
		if d, ok := v.(Decimal); ok {
			kind, scale, mag = "decimal", d.Scale(), d.Float64()
		} else {
			mag = v.(Int128).Float64()
		}
		u := time.Second
		if cfg.TimeUnit != "" {
			var err error
			if u, err = unitFromName(cfg.TimeUnit); err != nil {
				return nil, err
			}
		} else {
			u = detectEpochUnit(mag)
			noteEpochUnit(warn, u)
		}
		return &tsCodec{kind: kind, unit: u, scale: scale}, nil
	}
	return nil, fmt.Errorf("resample: timestamp field has unsupported type %T (want an int, int128, decimal, float, string or time)", sample)
}

func (c *tsCodec) toNanos(v any) (int64, error) {
	// ssql:domain fixed: a resample axis is an int, int128, decimal, float, string or time
	switch x := v.(type) {
	case int64:
		if c.kind != "int" && c.kind != "float" {
			return 0, fmt.Errorf("resample: mixed timestamp types (%T after %s)", v, c.kind)
		}
		return x * int64(c.unit), nil
	case float64:
		if c.kind != "int" && c.kind != "float" {
			return 0, fmt.Errorf("resample: mixed timestamp types (%T after %s)", v, c.kind)
		}
		return int64(x * float64(c.unit)), nil
	case time.Time:
		if c.kind != "time" {
			return 0, fmt.Errorf("resample: mixed timestamp types (%T after %s)", v, c.kind)
		}
		return x.UnixNano(), nil
	case Int128:
		if c.kind != "int128" {
			return 0, fmt.Errorf("resample: mixed timestamp types (%T after %s)", v, c.kind)
		}
		return c.wideNanos(x.CheckedMul(Int128From(int64(c.unit))))
	case Decimal:
		if c.kind != "decimal" {
			return 0, fmt.Errorf("resample: mixed timestamp types (%T after %s)", v, c.kind)
		}
		// units·unit / 10^S, truncated toward zero as a float epoch is.
		ns, err := x.Units().CheckedMul(Int128From(int64(c.unit)))
		if err != nil {
			return c.wideNanos(ns, err)
		}
		return c.wideNanos(ns.Quo(pow10Int128[x.Scale()]), nil)
	case string:
		if c.kind == "time" {
			// A -from/-to bound against a time column: any form ParseTime reads.
			t, ok := ParseTime(x)
			if !ok {
				return 0, fmt.Errorf("resample: %q is not a time (use a form like 2026-01-31 or 2026-01-31T10:30:00Z)", x)
			}
			return t.UnixNano(), nil
		}
		if c.kind != "string" {
			return 0, fmt.Errorf("resample: mixed timestamp types (%T after %s)", v, c.kind)
		}
		t, err := time.Parse(c.layout, x)
		if err != nil {
			return 0, fmt.Errorf("resample: cannot parse timestamp %q: %w", x, err)
		}
		return t.UnixNano(), nil
	}
	return 0, fmt.Errorf("resample: unsupported timestamp value %T", v)
}

// wideNanos is an int128 or decimal axis value's nanoseconds, which must
// fit an int64: the grid is int64 nanoseconds, as every engine's is.
func (c *tsCodec) wideNanos(ns Int128, err error) (int64, error) {
	if err == nil {
		if n, ok := ns.Int64(); ok {
			return n, nil
		}
	}
	return 0, fmt.Errorf("resample: the %s timestamp is out of range: past ±292 years of nanoseconds from the epoch", c.kind)
}

func (c *tsCodec) fromNanos(ns int64) any {
	// ssql:domain fixed: a resample axis is an int, int128, decimal, float, string or time
	switch c.kind {
	case "int":
		return ns / int64(c.unit)
	case "int128":
		return Int128From(ns / int64(c.unit))
	case "decimal":
		// ns·10^S / unit: exact for a grid point at any width the scale holds.
		units := Int128From(ns).Mul(pow10Int128[c.scale]).Quo(Int128From(int64(c.unit)))
		return MustNewDecimal(units, c.scale)
	case "float":
		return float64(ns) / float64(c.unit)
	case "time":
		return time.Unix(0, ns).UTC()
	default:
		return time.Unix(0, ns).UTC().Format(c.layout)
	}
}

type tsPoint struct {
	ns  int64
	val float64
}

// ResampleRecords materializes the input, snaps to the epoch-aligned
// grid, and emits one record per grid point with the timestamp (input
// family preserved) and each requested value field filled per
// cfg.Fill. Edge points with no defined value omit that field, with a
// count on Warn.
func ResampleRecords(records iter.Seq[Record], cfg ResampleConfig) (iter.Seq[Record], error) {
	warn := cfg.Warn
	if warn == nil {
		warn = os.Stderr
	}
	if cfg.TimeField == "" {
		return nil, fmt.Errorf("resample: -time is required")
	}
	if cfg.Every <= 0 {
		return nil, fmt.Errorf("resample: -every must be a positive duration")
	}
	if len(cfg.Values) == 0 {
		return nil, fmt.Errorf("resample: at least one -value field is required")
	}
	if err := cfg.CheckFields(); err != nil {
		return nil, err
	}
	fill := cfg.Fill
	if fill == "" {
		fill = FillPrevious
	}
	switch fill {
	case FillPrevious, FillNext, FillLinear:
	default:
		return nil, fmt.Errorf("resample: unknown -fill %q (previous|next|linear)", fill)
	}

	// Materialize + coerce. Per-field series (fields may be ragged).
	var codec *tsCodec
	series := make(map[string][]tsPoint, len(cfg.Values))
	var minNS, maxNS int64
	n, untimed := 0, 0
	for r := range records {
		tv, ok := Get[any](r, cfg.TimeField)
		if !ok || tv == nil {
			// No place on the axis: skipped, as SQL's WHERE ts IS NOT NULL
			// and pandas drop it, and counted (it was an error).
			untimed++
			continue
		}
		if codec == nil {
			var err error
			if codec, err = newTSCodec(tv, cfg, warn); err != nil {
				return nil, err
			}
		}
		ns, err := codec.toNanos(tv)
		if err != nil {
			return nil, err
		}
		if n == 0 || ns < minNS {
			minNS = ns
		}
		if n == 0 || ns > maxNS {
			maxNS = ns
		}
		n++
		for _, f := range cfg.Values {
			v, ok := Get[any](r, f)
			if !ok || v == nil {
				continue // ragged: this record has no such value
			}
			var fv float64
			// ssql:domain fixed: a resample value is a number, carried as a float
			switch x := v.(type) {
			case int64:
				fv = float64(x)
			case float64:
				fv = x
			case Int128:
				fv = x.Float64()
			case Decimal:
				fv = x.Float64()
			default:
				return nil, fmt.Errorf("resample: value field %q is %T, want a numeric field", f, v)
			}
			series[f] = append(series[f], tsPoint{ns: ns, val: fv})
		}
	}
	if n == 0 && untimed > 0 {
		// No record has one: a field the input does not have, which the
		// CLI's schema check names too; never an empty grid in silence.
		return nil, fmt.Errorf("resample: no record has time field %q", cfg.TimeField)
	}
	if untimed > 0 {
		fmt.Fprintf(warn, "resample: skipped %d record(s) with no %s\n", untimed, cfg.TimeField)
	}
	if n == 0 {
		return func(yield func(Record) bool) {}, nil // a grid over nothing is invention
	}

	// Sort each series; duplicate timestamps keep the HIGHEST value —
	// a deterministic rule independent of input order, so exec and
	// parallel-sharded lanes (whose record order differs) agree
	// byte-for-byte. Input-order "last wins" was lane-dependent.
	dups := 0
	for f, pts := range series {
		sort.Slice(pts, func(i, j int) bool {
			if pts[i].ns != pts[j].ns {
				return pts[i].ns < pts[j].ns
			}
			return pts[i].val < pts[j].val
		})
		out := pts[:0]
		for i, p := range pts {
			if i+1 < len(pts) && pts[i+1].ns == p.ns {
				dups++
				continue
			}
			out = append(out, p)
		}
		series[f] = out
	}
	if dups > 0 {
		fmt.Fprintf(warn, "resample: %d duplicate timestamps (highest value kept)\n", dups)
	}

	// Grid bounds: epoch-aligned buckets covering [min, max], -from/-to
	// snapped onto the grid (down/up) with a note when snapping moved them.
	e := int64(cfg.Every)
	gridFrom := SnapToBucket(minNS, cfg.Every)
	gridTo := SnapToBucket(maxNS, cfg.Every)
	if cfg.From != "" {
		fns, err := codec.toNanos(parseBoundLiteral(cfg.From))
		if err != nil {
			return nil, fmt.Errorf("resample: -from: %w", err)
		}
		s := SnapToBucket(fns, cfg.Every)
		if s != fns {
			fmt.Fprintf(warn, "resample: -from snapped down to the epoch grid (%s)\n", displayValue(codec.fromNanos(s)))
		}
		gridFrom = s
	}
	if cfg.To != "" {
		tns, err := codec.toNanos(parseBoundLiteral(cfg.To))
		if err != nil {
			return nil, fmt.Errorf("resample: -to: %w", err)
		}
		s := SnapToBucket(tns, cfg.Every)
		if s != tns {
			s += e // snap UP: include the bucket containing -to
			fmt.Fprintf(warn, "resample: -to snapped up to the epoch grid (%s)\n", displayValue(codec.fromNanos(s)))
		}
		gridTo = s
	}
	if gridTo < gridFrom {
		return nil, fmt.Errorf("resample: -to is before -from")
	}

	// Merge: one pointer per field, O(n + m·fields).
	type cursor struct{ i int }
	cursors := make(map[string]*cursor, len(cfg.Values))
	for _, f := range cfg.Values {
		cursors[f] = &cursor{}
	}
	missing := make(map[string]int, len(cfg.Values))

	var out []Record
	for gp := gridFrom; gp <= gridTo; gp += e {
		m := MakeMutableRecordWithCapacity(1 + len(cfg.Values))
		// ssql:domain fixed: a resample axis is an int, int128, decimal, float, string or time
		switch tv := codec.fromNanos(gp).(type) {
		case int64:
			m = m.Int(cfg.TimeField, tv)
		case float64:
			m = m.Float(cfg.TimeField, tv)
		case string:
			m = m.String(cfg.TimeField, tv)
		case time.Time:
			m = m.Time(cfg.TimeField, tv)
		case Int128:
			m = m.Int128(cfg.TimeField, tv)
		case Decimal:
			m = m.Decimal(cfg.TimeField, tv)
		}
		for _, f := range cfg.Values {
			pts := series[f]
			c := cursors[f]
			// Advance so pts[c.i] is the first point with ns > gp
			// (prev = pts[c.i-1] has ns <= gp).
			for c.i < len(pts) && pts[c.i].ns <= gp {
				c.i++
			}
			if len(pts) == 0 {
				return nil, fmt.Errorf("resample: value field %q has no observations", f)
			}
			var val float64
			clamped := false
			switch fill {
			case FillPrevious:
				if c.i > 0 {
					val = pts[c.i-1].val
				} else {
					val, clamped = pts[0].val, true // leading edge: clamp to first
				}
			case FillNext:
				if c.i > 0 && pts[c.i-1].ns == gp {
					val = pts[c.i-1].val
				} else if c.i < len(pts) {
					val = pts[c.i].val
				} else {
					val, clamped = pts[len(pts)-1].val, true // trailing edge: clamp to last
				}
			case FillLinear:
				if c.i > 0 && pts[c.i-1].ns == gp {
					val = pts[c.i-1].val
				} else if c.i > 0 && c.i < len(pts) {
					p, q := pts[c.i-1], pts[c.i]
					frac := float64(gp-p.ns) / float64(q.ns-p.ns)
					val = p.val + frac*(q.val-p.val)
				} else if c.i == 0 {
					val, clamped = pts[0].val, true
				} else {
					val, clamped = pts[len(pts)-1].val, true
				}
			}
			if clamped {
				missing[f]++
			}
			m = m.Float(f, val)
		}
		out = append(out, m.Freeze())
	}
	// One line for all fields, and only when the clamping says something
	// about the data. An epoch-aligned grid almost always starts one grid
	// point before the first observation (and ends one after the last),
	// so a single clamped edge point per side is inherent to the grid,
	// not a gap — noting it on every run was noise (Ross, the codelab's
	// six-row example printed three notes). More than that means the
	// data really is missing where the grid reaches (`-from`, `-to`, or
	// a long lead-in) and is worth a line.
	var noted []string
	total := 0
	for _, f := range cfg.Values {
		if missing[f] > inherentEdgeClamps(fill) {
			noted = append(noted, fmt.Sprintf("%s (%d)", f, missing[f]))
			total += missing[f]
		}
	}
	if len(noted) > 0 {
		fmt.Fprintf(warn, "resample: %d grid points outside the observed range clamped to the nearest observation: %s (-fill %s needs %s)\n",
			total, strings.Join(noted, ", "), fill, fillNeeds(fill))
	}
	return func(yield func(Record) bool) {
		for _, r := range out {
			if !yield(r) {
				return
			}
		}
	}, nil
}

// inherentEdgeClamps is how many clamped grid points a fill mode
// produces on any input whose first/last observation is not itself a
// grid point: previous clamps the leading grid point, next the trailing
// one, linear both.
func inherentEdgeClamps(fill string) int {
	if fill == FillLinear {
		return 2
	}
	return 1
}

func fillNeeds(fill string) string {
	switch fill {
	case FillPrevious:
		return "an earlier observation"
	case FillNext:
		return "a later observation"
	default:
		return "observations on both sides"
	}
}

// parseBoundLiteral turns a -from/-to CLI string into the value shape
// the codec expects (int64 for pure integers, else the raw string).
func parseBoundLiteral(s string) any {
	var i int64
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil && fmt.Sprintf("%d", i) == s {
		return i
	}
	return s
}

// BucketValue snaps a timestamp VALUE (int64/float64 epoch with
// per-value unit detection, or a string in the standard layouts) to
// its epoch-aligned bucket start, preserving the input family — the
// engine behind the bucket() expression function, sharing exprfn's
// snap with ResampleRecords so downsampling (bucket + group-by) and
// resampling land on the SAME grid.
func BucketValue(v any, every time.Duration) (any, error) {
	// ssql:domain fixed: a bucket axis is an int, int128, decimal, float, string or time
	switch x := v.(type) {
	case nil:
		return nil, nil // a missing value has no bucket (SQL's NULL)
	case int64:
		return exprfn.BucketInt64(x, int64(every)), nil
	case Int128:
		return checkedBucket(func() any { return BucketInt128(x, every) })
	case Decimal:
		return BucketDecimal(x, every)
	case float64:
		return exprfn.BucketFloat64(x, int64(every)), nil
	case time.Time:
		// A `time` column (DFC128 D1): time in, time out, same grid.
		return BucketTime(x, every), nil
	case string:
		for _, l := range tsStringLayouts {
			if t, err := time.Parse(l, x); err == nil {
				return time.Unix(0, SnapToBucket(t.UnixNano(), every)).UTC().Format(l), nil
			}
		}
		return nil, fmt.Errorf("bucket: cannot parse timestamp %q", x)
	}
	return nil, fmt.Errorf("bucket: unsupported timestamp type %T", v)
}

// checkedBucket turns BucketInt128's overflow panic into the error the
// VM reports.
func checkedBucket(f func() any) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(error)
			if !ok {
				panic(r)
			}
			v, err = nil, fmt.Errorf("bucket: %w", e)
		}
	}()
	return f(), nil
}

// floorSnap128 floors v to a multiple of step (step > 0), toward −∞ as
// exprfn.SnapNanos does.
func floorSnap128(v, step Int128) Int128 {
	q := v.Quo(step)
	if !v.Rem(step).IsZero() && v.Sign() < 0 {
		q = q.Sub(Int128From(1))
	}
	return q.Mul(step)
}

// BucketInt128 is exprfn.BucketInt64 for an int128 epoch, in int128's
// exact arithmetic (the unit read from the magnitude, the snap toward
// −∞), so a value past int64 buckets as the engines' HUGEINT does (Ross,
// DFC163 §7). It panics on an overflow, as Int128's operators do.
func BucketInt128(v Int128, every time.Duration) Int128 {
	u := Int128From(exprfn.DetectEpochUnitNanos(v.Float64()))
	return floorSnap128(v.Mul(u), Int128From(int64(every))).Quo(u)
}

// BucketDecimal is bucket() for a decimal epoch, exact at the value's
// own scale: the width in the decimal's units (1/10^S of the epoch unit
// its magnitude says) and the snap toward −∞, the result at the same
// scale. A width finer than the scale can hold is an error (Ross,
// DFC163 §7).
func BucketDecimal(d Decimal, every time.Duration) (Decimal, error) {
	u := exprfn.DetectEpochUnitNanos(d.Float64())
	w := Int128From(int64(every)).Mul(pow10Int128[d.Scale()])
	if !w.Rem(Int128From(u)).IsZero() {
		return Decimal{}, fmt.Errorf("bucket: a width of %s is finer than decimal:%d holds", every, d.Scale())
	}
	return NewDecimal(floorSnap128(d.Units(), w.Quo(Int128From(u))), d.Scale())
}

// MustBucketDecimal is BucketDecimal for transpiled code: it panics with
// the error, as the Int128 and Decimal operators do.
func MustBucketDecimal(d Decimal, every time.Duration) Decimal {
	out, err := BucketDecimal(d, every)
	if err != nil {
		panic(err)
	}
	return out
}

// BucketTime snaps a time to its epoch-aligned bucket of width every
// (zone-free: the grid is the Unix epoch's). One form for the VM
// (BucketValue) and transpiled code.
func BucketTime(t time.Time, every time.Duration) time.Time {
	return time.Unix(0, SnapToBucket(t.UnixNano(), every)).UTC()
}

// ResampleFilter wraps ResampleRecords as a Filter for generated
// record-mode pipelines — the assembler composes stmt fragments as
// filter applications (out := F(in)), so the fragment surface must be
// filter-shaped. Errors are fatal-loud, matching CLI behaviour.
func ResampleFilter(cfg ResampleConfig) Filter[Record, Record] {
	return func(in iter.Seq[Record]) iter.Seq[Record] {
		out, err := ResampleRecords(in, cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return out
	}
}
