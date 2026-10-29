package typed

import ssql "github.com/rosscartlidge/ssql/v4"

// The window's results live in one typed column per spec, in the
// window's OUTPUT order (2026-10-29, DFC158 §3.12). Before, every row
// had a []any of results indexed by its input position: each value was
// boxed (an allocation per row per spec, 14.6 M objects for the GC to
// scan on the benchmark window), written by the partition's pass at a
// random row — a cache miss per row, 0.77 s on one core for the 4.75
// M-row partition after its sort — and gathered into output order at
// the end. The partition that decides the output order writes its
// positions sequentially; any other clause writes through the row →
// output-position map.

type colKind uint8

const (
	colAny   colKind = iota // lag, lead, first, last, nth, min, max, folds, decimal sums
	colInt                  // ranks, row_number, ntile, counts
	colFloat                // avg, percent_rank, cume_dist, float sums
	colWide                 // integer sums (ssql.Int128)
)

// windowColumn is one spec's results: one of the slices by kind; absent
// values (a lag before the first row, an avg over nothing) are nil in an
// any column and a set byte in absent for the typed ones — allocated
// with the column, since the partitions write concurrently (a lazy
// allocation raced, 2026-10-29).
type windowColumn struct {
	kind   colKind
	ints   []int64
	floats []float64
	wides  []ssql.Int128
	anys   []any
	absent []bool // typed columns only
}

// columnKind is the column a spec's values go in, by the type the
// window produces for it (windowValue, runningSpecValues).
func columnKind[T any](spec WindowSpec[T]) colKind {
	switch spec.Kind {
	case WRowNumber, WRank, WDenseRank, WNtile, WCount, WCountField:
		return colInt
	case WPercentRank, WCumeDist, WAvg:
		return colFloat
	case WSum:
		switch {
		case spec.Dec != nil:
			return colAny
		case spec.Int != nil || spec.Wide != nil:
			return colWide
		}
		return colFloat
	}
	return colAny
}

func newWindowColumn[T any](spec WindowSpec[T], n int) windowColumn {
	c := windowColumn{kind: columnKind(spec)}
	switch c.kind {
	case colInt:
		c.ints = make([]int64, n)
	case colFloat:
		c.floats = make([]float64, n)
	case colWide:
		c.wides = make([]ssql.Int128, n)
	default:
		c.anys = make([]any, n)
		return c
	}
	c.absent = make([]bool, n)
	return c
}

// setAbsent marks position p absent in a typed column.
func (c *windowColumn) setAbsent(p int) { c.absent[p] = true }

// set stores a value the per-row path produced (boxed already, so this
// allocates nothing); a nil, or a value of another type than the
// column's, is absent.
func (c *windowColumn) set(p int, v any) {
	switch c.kind {
	case colInt:
		if x, ok := v.(int64); ok {
			c.ints[p] = x
			return
		}
	case colFloat:
		if x, ok := v.(float64); ok {
			c.floats[p] = x
			return
		}
	case colWide:
		if x, ok := v.(ssql.Int128); ok {
			c.wides[p] = x
			return
		}
	default:
		c.anys[p] = v
		return
	}
	c.setAbsent(p)
}

func (c *windowColumn) present(p int) bool { return !c.absent[p] }

// value is the boxed value at p (nil when absent).
func (c *windowColumn) value(p int) any {
	switch c.kind {
	case colInt:
		if c.present(p) {
			return c.ints[p]
		}
	case colFloat:
		if c.present(p) {
			return c.floats[p]
		}
	case colWide:
		if c.present(p) {
			return c.wides[p]
		}
	default:
		return c.anys[p]
	}
	return nil
}

// WindowRow is one output row's window results, handed to the build
// function of Window and WindowParallel: the results in clause order
// then spec order, read by index. The typed readers (Int, Float, Wide)
// read their column without boxing — a generated program uses the one
// matching the result's Go type — and fall back to the boxed value for
// a column of another kind, so a -min on an int64 field reads as an
// int64 either way. The second result is false when the value is
// absent (a lag before the first row, a sum over nothing).
type WindowRow struct {
	cols []windowColumn
	p    int
}

// Len is the number of results.
func (w WindowRow) Len() int { return len(w.cols) }

// Int reads result i as an int64.
func (w WindowRow) Int(i int) (int64, bool) {
	c := &w.cols[i]
	if c.kind == colInt {
		return c.ints[w.p], c.present(w.p)
	}
	v, ok := c.value(w.p).(int64)
	return v, ok
}

// Float reads result i as a float64.
func (w WindowRow) Float(i int) (float64, bool) {
	c := &w.cols[i]
	if c.kind == colFloat {
		return c.floats[w.p], c.present(w.p)
	}
	v, ok := c.value(w.p).(float64)
	return v, ok
}

// Wide reads result i as an ssql.Int128 (an integer sum).
func (w WindowRow) Wide(i int) (ssql.Int128, bool) {
	c := &w.cols[i]
	if c.kind == colWide {
		return c.wides[w.p], c.present(w.p)
	}
	v, ok := c.value(w.p).(ssql.Int128)
	return v, ok
}

// Value is result i boxed; nil when absent.
func (w WindowRow) Value(i int) any { return w.cols[i].value(w.p) }

// Values is every result boxed, in order — the []any the build function
// received before the columns.
func (w WindowRow) Values() []any {
	out := make([]any, len(w.cols))
	for i := range w.cols {
		out[i] = w.cols[i].value(w.p)
	}
	return out
}
