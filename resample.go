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

// SnapToBucket floors an epoch-nanosecond timestamp to its
// epoch-aligned bucket start for the given period. Euclidean floor:
// correct for pre-1970 timestamps too.
func SnapToBucket(ns int64, every time.Duration) int64 {
	return exprfn.SnapNanos(ns, int64(every))
}

// tsCodec converts between the input's timestamp representation and
// epoch nanoseconds, preserving the family on output.
type tsCodec struct {
	kind   string // "int", "float", "string"
	unit   time.Duration
	layout string
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

func newTSCodec(sample any, cfg ResampleConfig, warn io.Writer) (*tsCodec, error) {
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
			fmt.Fprintf(warn, "resample: epoch unit auto-detected as %s (override with -time-unit)\n", unitName(u))
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
			fmt.Fprintf(warn, "resample: epoch unit auto-detected as %s (override with -time-unit)\n", unitName(u))
		}
		return &tsCodec{kind: "float", unit: u}, nil
	case string:
		if cfg.TimeFormat != "" {
			if _, err := time.Parse(cfg.TimeFormat, v); err != nil {
				return nil, fmt.Errorf("resample: %q does not parse with -time-format %q: %w", v, cfg.TimeFormat, err)
			}
			return &tsCodec{kind: "string", layout: cfg.TimeFormat}, nil
		}
		for _, l := range tsStringLayouts {
			if _, err := time.Parse(l, v); err == nil {
				return &tsCodec{kind: "string", layout: l}, nil
			}
		}
		return nil, fmt.Errorf("resample: cannot parse timestamp %q (tried RFC3339 and SQL datetime; use -time-format)", v)
	}
	return nil, fmt.Errorf("resample: timestamp field has unsupported type %T (want int64, float64 or string)", sample)
}

func (c *tsCodec) toNanos(v any) (int64, error) {
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
	case string:
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

func (c *tsCodec) fromNanos(ns int64) any {
	switch c.kind {
	case "int":
		return ns / int64(c.unit)
	case "float":
		return float64(ns) / float64(c.unit)
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
	n := 0
	for r := range records {
		tv, ok := Get[any](r, cfg.TimeField)
		if !ok {
			return nil, fmt.Errorf("resample: record missing time field %q", cfg.TimeField)
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
			if !ok {
				continue // ragged: this record has no such field
			}
			var fv float64
			switch x := v.(type) {
			case int64:
				fv = float64(x)
			case float64:
				fv = x
			default:
				return nil, fmt.Errorf("resample: value field %q is %T, want a numeric field", f, v)
			}
			series[f] = append(series[f], tsPoint{ns: ns, val: fv})
		}
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
			fmt.Fprintf(warn, "resample: -from snapped down to the epoch grid (%v)\n", codec.fromNanos(s))
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
			fmt.Fprintf(warn, "resample: -to snapped up to the epoch grid (%v)\n", codec.fromNanos(s))
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
		switch tv := codec.fromNanos(gp).(type) {
		case int64:
			m = m.Int(cfg.TimeField, tv)
		case float64:
			m = m.Float(cfg.TimeField, tv)
		case string:
			m = m.String(cfg.TimeField, tv)
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
	for _, f := range cfg.Values {
		if missing[f] > 0 {
			fmt.Fprintf(warn, "resample: %d grid points outside %s's observed range clamped to the nearest observation (-fill %s needs %s)\n",
				missing[f], f, fill, fillNeeds(fill))
		}
	}
	return func(yield func(Record) bool) {
		for _, r := range out {
			if !yield(r) {
				return
			}
		}
	}, nil
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
	switch x := v.(type) {
	case int64:
		return exprfn.BucketInt64(x, int64(every)), nil
	case float64:
		return exprfn.BucketFloat64(x, int64(every)), nil
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
