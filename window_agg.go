package ssql

import "fmt"

// WAggSpec describes a registry aggregate applied over a window frame
// (DFC130 unit 2): any group-by aggregate — stddev, median, count-distinct,
// string-agg, mode, arg-max, … — becomes a window function by feeding it
// the frame's records. The CLI builds one from its aggregate registry;
// Code is the Go expression that rebuilds Agg, so `generate go` can emit
// the same function (ssql.WindowFuncCode).
type WAggSpec struct {
	Name    string        // registry function name, e.g. "stddev" (for messages)
	Field   string        // the field the aggregate reads
	Extra   string        // its extra argument (percentile's P, string-agg's separator, arg-max's BY)
	Kind    string        // result wire type: "int", "float", "string", "json", or "" = the field's own type
	MinRows int           // frames with fewer rows yield an absent value (2 for sample stddev/variance — SQL's NULL)
	Agg     AggregateFunc // the aggregate itself
	Code    string        // Go source that rebuilds Agg, e.g. `ssql.StdDev("salary")`
}

type wAgg struct{ spec WAggSpec }

func (wAgg) windowFunc() {}

// WAggregate makes a window function from a registry aggregate; see
// WAggSpec. It runs on the materialised path only (the aggregate has no
// Remove), so `-presorted` streaming refuses it with the reason.
func WAggregate(spec WAggSpec) WindowFunc {
	if spec.Agg == nil {
		panic(fmt.Sprintf("ssql.WAggregate(%q): nil aggregate", spec.Name))
	}
	if spec.MinRows < 1 {
		spec.MinRows = 1
	}
	return wAgg{spec: spec}
}

// applyFrameAggregate evaluates a registry aggregate over the frame
// [start, end] of the partition. An aggregate's empty marker ("" from an
// all-missing frame) and a frame below MinRows both yield nil — the
// absent value, SQL's NULL.
func applyFrameAggregate(f wAgg, all []Record, indices []int, start, end int) any {
	n := end - start + 1
	if n < f.spec.MinRows {
		return nil
	}
	frame := make([]Record, 0, n)
	for i := start; i <= end; i++ {
		frame = append(frame, all[indices[i]])
	}
	v := f.spec.Agg(frame).GetValue()
	if s, ok := v.(string); ok && s == "" {
		return nil
	}
	return v
}
