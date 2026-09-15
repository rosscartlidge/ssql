package ssql

import (
	"fmt"
	"time"
)

// RANGE frames (DFC130 unit 3). A RANGE frame selects the rows whose ORDER
// value lies within a distance of the current row's — "the last five
// minutes", "salaries within 10000 below" — where a ROWS frame counts
// rows. It needs exactly one order field, numeric or temporal; peers (rows
// with the same order value) are always in the frame, which is what SQL's
// RANGE … CURRENT ROW means.
//
// Distances are in the order field's own units for numbers and in SECONDS
// for times (a duration flag on the CLI). −1 means unbounded on that side.
// Per row the RANGE frame is resolved to the equivalent ROWS frame
// (rangeFrameBounds), so every window function computes exactly as it
// does over ROWS.

// rangeFrameBounds returns the [start, end] index range (within the
// sorted partition) of the RANGE frame around pos.
func rangeFrameBounds(all []Record, indices []int, pos, partLen int, frame WindowFrame, orderBy []OrderField) (int, int) {
	if len(orderBy) != 1 {
		panic(fmt.Errorf("window: a RANGE frame needs exactly one -order field, got %d", len(orderBy)))
	}
	field := orderBy[0].Field
	dir := 1.0
	if orderBy[0].Desc {
		dir = -1 // "preceding" rows have LARGER values under DESC
	}
	cur := rangeOrderValue(all[indices[pos]], field, frame.RangeTime)
	start, end := pos, pos
	for start > 0 {
		d := (cur - rangeOrderValue(all[indices[start-1]], field, frame.RangeTime)) * dir
		if d < 0 {
			break // not sorted as declared — should not happen; stay safe
		}
		if frame.RangePreceding >= 0 && d > frame.RangePreceding {
			break
		}
		start--
	}
	for end+1 < partLen {
		d := (rangeOrderValue(all[indices[end+1]], field, frame.RangeTime) - cur) * dir
		if d < 0 {
			break
		}
		if frame.RangeFollowing >= 0 && d > frame.RangeFollowing {
			break
		}
		end++
	}
	return start, end
}

// rangeOrderValue is the order field as a float64: numbers as themselves,
// times (time.Time, or a string convertToTime accepts, including a plain
// date) as Unix seconds. The bound's kind must match the field's: a
// duration bound over a numeric field, or a number over a time field, is
// loud — SQL rejects both, and a silent "864000 salary units" is worse
// than an error.
func rangeOrderValue(r Record, field string, wantTime bool) float64 {
	v, ok := Get[any](r, field)
	if !ok || v == nil {
		panic(fmt.Errorf("window: RANGE order field %q is missing on a row", field))
	}
	if isNumeric(v) {
		if wantTime {
			panic(fmt.Errorf("window: RANGE bound is a duration but order field %q is numeric (%v); use a plain number", field, v))
		}
		return toFloat64(v)
	}
	if t, ok := convertToTime(v); ok {
		if !wantTime {
			panic(fmt.Errorf("window: order field %q is a time (%v) — give the RANGE bound as a duration (5m, 2h, 7d)", field, v))
		}
		return float64(t.UnixNano()) / float64(time.Second)
	}
	panic(fmt.Errorf("window: RANGE order field %q must be numeric or a time, got %T (%v)", field, v, v))
}
