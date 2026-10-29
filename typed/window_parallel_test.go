package typed

import (
	"cmp"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// TestWindowParallelMatchesWindow: over a stream whose shards are
// contiguous runs of the input, WindowParallel yields exactly Window's
// output — the same rows in the same order, partitions in first-seen
// order with the partitions interleaved across shards, ties kept in
// input order.
func TestWindowParallelMatchesWindow(t *testing.T) {
	rows := []winRow{
		{"sales", "Bob", 65000, nil}, {"eng", "Alice", 95000, nil}, {"eng", "Carol", 105000, nil},
		{"ops", "Zed", 1, nil}, {"eng", "Eve", 88000, nil}, {"sales", "Frank", 82000, nil},
		{"eng", "Dan", 95000, nil}, {"ops", "Yan", 1, nil}, {"sales", "Gus", 65000, nil},
	}
	for _, ordered := range []bool{true, false} {
		clause := WindowClause[winRow]{
			Partition: func(r winRow) string { return r.Dept },
			HasOrder:  ordered,
			Frame:     WindowFrame{Preceding: -1, Following: 0},
			Specs: []WindowSpec[winRow]{
				{Kind: WRowNumber},
				{Kind: WRank},
				{Kind: WSum, Num: func(r winRow) (float64, bool) { return float64(r.Salary), true }},
				{Kind: WLag, N: 1, Val: func(r winRow) (any, bool) { return r.Salary, true }},
			},
		}
		if ordered {
			clause.Compare = func(a, b winRow) int {
				if a.Salary < b.Salary {
					return -1
				}
				if a.Salary > b.Salary {
					return 1
				}
				return 0
			}
		}
		build := func(r winRow, res WindowRow) []any { return append([]any{r.Name}, res.Values()...) }
		var serial, parallel [][]any
		for o := range Window([]WindowClause[winRow]{clause}, build)(slices.Values(rows)) {
			serial = append(serial, o)
		}
		for _, n := range []int{1, 2, 4, 9} {
			parallel = nil
			for o := range WindowParallel([]WindowClause[winRow]{clause}, build)(ParallelFromSlice(rows, n)).Serial() {
				parallel = append(parallel, o)
			}
			if !reflect.DeepEqual(serial, parallel) {
				t.Errorf("ordered=%v shards=%d:\n serial   %v\n parallel %v", ordered, n, serial, parallel)
			}
		}
	}
}

// TestReadParquetParallelShardsAreInFileOrder: the shards are contiguous
// runs of row groups, so concatenated in shard order they are the file —
// what WindowParallel and the parallel sinks rely on.
func TestReadParquetParallelShardsAreInFileOrder(t *testing.T) {
	type row struct {
		ID int64 `ssql:"id"`
	}
	var rows []row
	for i := int64(0); i < 23; i++ {
		rows = append(rows, row{ID: i})
	}
	path := filepath.Join(t.TempDir(), "o.parquet")
	if err := WriteParquet(slices.Values(rows), path, WithRowGroupSize(3)); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3, 5, 8, 20} {
		s := ReadParquetParallel[row](path, n)
		var got []row
		for _, shard := range s.shards {
			for r := range shard {
				got = append(got, r)
			}
		}
		if !reflect.DeepEqual(got, rows) {
			t.Errorf("n=%d: shard order is not file order: %v", n, got)
		}
	}
}

// TestWindowKeySortMatchesCompare: a clause sorted through its typed key
// (string, int64, float64; ascending and descending) yields exactly what
// the same clause sorted through Compare yields — ties in input order,
// a NaN compared equal to everything as the generated comparator does,
// partitions above and below the chunked-sort threshold.
func TestWindowKeySortMatchesCompare(t *testing.T) {
	type row struct {
		Part int64
		S    string
		I    int64
		F    float64
		V    float64
	}
	rng := rand.New(rand.NewSource(11))
	n := 3*sortChunkMin + 777
	rows := make([]row, n)
	for i := range rows {
		rows[i] = row{Part: int64(rng.Intn(2)), S: fmt.Sprintf("s%04d", rng.Intn(3000)), I: int64(rng.Intn(500)) - 250, F: float64(rng.Intn(400)) / 8, V: float64(i)}
		if i%97 == 0 {
			rows[i].F = math.NaN()
		}
	}
	specs := []WindowSpec[row]{
		{Kind: WRowNumber},
		{Kind: WRank},
		{Kind: WSum, Num: func(r row) (float64, bool) { return r.V, true }},
	}
	build := func(r row, res WindowRow) [4]any { return [4]any{r.V, res.Value(0), res.Value(1), res.Value(2)} }
	run := func(c WindowClause[row]) [][4]any {
		var out [][4]any
		for o := range Window([]WindowClause[row]{c}, build)(slices.Values(rows)) {
			out = append(out, o)
		}
		return out
	}
	for _, desc := range []bool{false, true} {
		cases := []struct {
			name string
			cmp  func(a, b row) int
			key  func(c *WindowClause[row])
		}{
			{"string", func(a, b row) int {
				if a.S < b.S {
					return -1
				}
				if a.S > b.S {
					return 1
				}
				return 0
			}, func(c *WindowClause[row]) { c.KeyString = func(r row) string { return r.S } }},
			{"int", func(a, b row) int {
				if a.I < b.I {
					return -1
				}
				if a.I > b.I {
					return 1
				}
				return 0
			}, func(c *WindowClause[row]) { c.KeyInt = func(r row) int64 { return r.I } }},
			{"float with NaN", func(a, b row) int {
				if a.F < b.F {
					return -1
				}
				if a.F > b.F {
					return 1
				}
				return 0
			}, func(c *WindowClause[row]) { c.KeyFloat = func(r row) float64 { return r.F } }},
		}
		for _, c := range cases {
			cmpFn := c.cmp
			if desc {
				cmpFn = func(a, b row) int { return c.cmp(b, a) }
			}
			base := WindowClause[row]{
				Partition: func(r row) string { return strconv.FormatInt(r.Part, 10) },
				HasOrder:  true, Compare: cmpFn, Desc: desc,
				Frame: WindowFrame{Preceding: -1, Following: 0}, Specs: specs,
			}
			want := run(base)
			keyed := base
			c.key(&keyed)
			got := run(keyed)
			if !reflect.DeepEqual(got, want) {
				for i := range want {
					if i < len(got) && !reflect.DeepEqual(got[i], want[i]) {
						t.Fatalf("%s desc=%v: row %d differs: key %v, compare %v", c.name, desc, i, got[i], want[i])
					}
				}
				t.Fatalf("%s desc=%v: %d rows, want %d", c.name, desc, len(got), len(want))
			}
		}
	}
}

// TestPartitionIndexRangesMatchOnePass: the partition map built as
// ranges on several cores is the map one pass builds — the same
// partitions in the same first-seen order, each partition's indices
// ascending — for a key a few rows share, one most rows have to
// themselves, one that first appears in the last range, and no key.
func TestPartitionIndexRangesMatchOnePass(t *testing.T) {
	n := 3*rangeMin + 777
	rng := rand.New(rand.NewSource(7))
	rows := make([]winRow, n)
	for i := range rows {
		rows[i] = winRow{Dept: "d" + strconv.Itoa(rng.Intn(5)), Name: "n" + strconv.Itoa(rng.Intn(n)), Salary: int64(i)}
	}
	rows[n-3].Dept = "late" // first seen in the last range only
	keys := map[string]func(winRow) string{
		"few":  func(r winRow) string { return r.Dept },
		"many": func(r winRow) string { return r.Name },
		"none": nil,
	}
	for name, key := range keys {
		c := WindowClause[winRow]{Partition: key}
		wantParts, wantOrder := partitionIndexRanges(c, rows, 1)
		for _, r := range []int{2, 7, 24} {
			parts, order := partitionIndexRanges(c, rows, r)
			if !slices.Equal(order, wantOrder) {
				t.Fatalf("%s ranges=%d: first-seen order differs (%d vs %d partitions)", name, r, len(order), len(wantOrder))
			}
			if len(parts) != len(wantParts) {
				t.Fatalf("%s ranges=%d: %d partitions, want %d", name, r, len(parts), len(wantParts))
			}
			for k, idx := range wantParts {
				if !slices.Equal(parts[k], idx) {
					t.Fatalf("%s ranges=%d: partition %q differs", name, r, k)
				}
			}
		}
	}
}

// TestWindowParallelLargeMatchesReference: past the range threshold —
// where the concatenation, the partition index and the gather into
// output order run on every core — WindowParallel's output is what the
// definition says: partitions in first-seen order, each sorted by the
// order field with ties in input order, the running sum carried along,
// checked against a reference written with sort.SliceStable over the
// rows alone.
func TestWindowParallelLargeMatchesReference(t *testing.T) {
	n := 3*rangeMin + 777
	rng := rand.New(rand.NewSource(11))
	rows := make([]winRow, n)
	for i := range rows {
		rows[i] = winRow{Dept: "d" + strconv.Itoa(rng.Intn(7)), Name: strconv.Itoa(rng.Intn(1000)), Salary: int64(rng.Intn(100))}
	}
	// The reference: rows by first-seen department, then name, then
	// input position; the running sum of salary within the department.
	type ref struct {
		dept, name string
		sum        int64
	}
	first := map[string]int{}
	for i, r := range rows {
		if _, ok := first[r.Dept]; !ok {
			first[r.Dept] = i
		}
	}
	pos := make([]int, n)
	for i := range pos {
		pos[i] = i
	}
	sort.SliceStable(pos, func(a, b int) bool {
		ra, rb := rows[pos[a]], rows[pos[b]]
		if ra.Dept != rb.Dept {
			return first[ra.Dept] < first[rb.Dept]
		}
		return ra.Name < rb.Name
	})
	want := make([]ref, n)
	running := map[string]int64{}
	for p, i := range pos {
		r := rows[i]
		running[r.Dept] += r.Salary
		want[p] = ref{r.Dept, r.Name, running[r.Dept]}
	}
	clause := WindowClause[winRow]{
		Partition: func(r winRow) string { return r.Dept },
		HasOrder:  true,
		Compare:   func(a, b winRow) int { return strings.Compare(a.Name, b.Name) },
		KeyString: func(r winRow) string { return r.Name },
		Frame:     WindowFrame{Preceding: -1, Following: 0},
		Specs:     []WindowSpec[winRow]{{Kind: WSum, Int: func(r winRow) (int64, bool) { return r.Salary, true }}},
	}
	build := func(r winRow, res WindowRow) ref {
		sum, _ := res.Value(0).(ssql.Int128).Int64()
		return ref{r.Dept, r.Name, sum}
	}
	for _, shards := range []int{1, 5, 24} {
		out := WindowParallel([]WindowClause[winRow]{clause}, build)(ParallelFromSlice(rows, shards))
		got := make([]ref, 0, n)
		for o := range out.Serial() {
			got = append(got, o)
		}
		if len(got) != n {
			t.Fatalf("shards=%d: %d rows, want %d", shards, len(got), n)
		}
		for p := range n {
			if got[p] != want[p] {
				t.Fatalf("shards=%d: row %d: got %v, want %v", shards, p, got[p], want[p])
			}
		}
		// The output Stream's shards are contiguous ranges of that order
		// — a group-by or a per-shard sink after the window reads them
		// concurrently, and concatenated in shard order they are the
		// window's output — and Serial walks them in order (above).
		if out.Shards() != runtime.GOMAXPROCS(0) {
			t.Fatalf("shards=%d: output has %d shards, want one per core", shards, out.Shards())
		}
		var byShard []ref
		for _, shard := range out.shards {
			lo := len(byShard)
			for o := range shard {
				byShard = append(byShard, o)
			}
			if hi := len(byShard); hi-lo > n/out.Shards()+1 {
				t.Fatalf("shards=%d: a shard holds %d rows of %d", shards, hi-lo, n)
			}
		}
		if !reflect.DeepEqual(byShard, want) {
			t.Fatalf("shards=%d: the output shards concatenated are not the window's output", shards)
		}
	}
}

// TestWindowColumnsAcrossClauses: the results live in one typed column
// per spec in OUTPUT order (DFC158 §3.12). The first ORDERED clause
// decides the order and writes its positions sequentially; a clause
// before it (unordered, so it did not decide) and one after it write
// through the row → output-position map; absent values (a lag before
// the first row, an avg over no values) read as absent from a typed
// column. The reference is the per-row semantics by hand.
func TestWindowColumnsAcrossClauses(t *testing.T) {
	rows := []winRow{
		{Dept: "eng", Name: "Bob", Salary: 300}, {Dept: "ops", Name: "Eve", Salary: 100}, {Dept: "eng", Name: "Al", Salary: 100},
		{Dept: "ops", Name: "Cy", Salary: 200}, {Dept: "eng", Name: "Dee", Salary: 200},
	}
	bySalary := func(a, b winRow) int { return cmp.Compare(a.Salary, b.Salary) }
	clauses := []WindowClause[winRow]{
		{ // unordered, whole partition: a count per dept — runs through outPos
			Partition: func(r winRow) string { return r.Dept },
			Frame:     WindowFrame{Preceding: -1, Following: -1},
			Specs:     []WindowSpec[winRow]{{Kind: WCount}},
		},
		{ // the deciding clause: by dept, salary ascending
			Partition: func(r winRow) string { return r.Dept },
			HasOrder:  true, Compare: bySalary,
			Frame: WindowFrame{Preceding: -1, Following: 0},
			Specs: []WindowSpec[winRow]{
				{Kind: WRowNumber},
				{Kind: WSum, Int: func(r winRow) (int64, bool) { return r.Salary, true }},
				{Kind: WLag, N: 1, Val: func(r winRow) (any, bool) { return r.Name, true }},
				{Kind: WAvg, Dec: func(r winRow) (ssql.Decimal, bool) { return ssql.Decimal{}, false }}, // no values: absent
			},
		},
		{ // a later ordered clause over the whole input: rank by salary descending
			Partition: func(r winRow) string { return "" },
			HasOrder:  true, Compare: func(a, b winRow) int { return bySalary(b, a) },
			Frame: WindowFrame{Preceding: -1, Following: 0},
			Specs: []WindowSpec[winRow]{{Kind: WRank}},
		},
	}
	type out struct {
		name  string
		n     int64
		rn    int64
		sum   int64
		prev  any
		avgOK bool
		rank  int64
		boxed []any
	}
	build := func(r winRow, res WindowRow) out {
		n, _ := res.Int(0)
		rn, _ := res.Int(1)
		w, _ := res.Wide(2)
		sum, _ := w.Int64()
		_, avgOK := res.Float(4)
		rank, _ := res.Int(5)
		return out{r.Name, n, rn, sum, res.Value(3), avgOK, rank, res.Values()}
	}
	want := []out{ // eng first seen, by salary; then ops
		{"Al", 3, 1, 100, nil, false, 4, nil},
		{"Dee", 3, 2, 300, "Al", false, 2, nil},
		{"Bob", 3, 3, 600, "Dee", false, 1, nil},
		{"Eve", 2, 1, 100, nil, false, 4, nil},
		{"Cy", 2, 2, 300, "Eve", false, 2, nil}, // rank 2: tied with Dee at 200
	}
	check := func(name string, got []out) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %d rows, want %d", name, len(got), len(want))
		}
		for i := range want {
			g := got[i]
			if g.boxed[3] != g.prev || g.boxed[4] != nil || len(g.boxed) != 6 {
				t.Errorf("%s row %d: boxed values %v disagree with the readers", name, i, g.boxed)
			}
			g.boxed = nil
			if !reflect.DeepEqual(g, want[i]) {
				t.Errorf("%s row %d: got %+v, want %+v", name, i, g, want[i])
			}
		}
	}
	var serial []out
	for o := range Window(clauses, build)(slices.Values(rows)) {
		serial = append(serial, o)
	}
	check("Window", serial)
	for _, shards := range []int{1, 3} {
		var par []out
		for o := range WindowParallel(clauses, build)(ParallelFromSlice(rows, shards)).Serial() {
			par = append(par, o)
		}
		check(fmt.Sprintf("WindowParallel/%d", shards), par)
	}
}

// TestWindowPresortedMatchesWindow: over input already in the window's
// order, WindowPresorted (contiguous runs, no sort, results at the input
// positions, the sort's rows in place) equals Window over the same rows
// — running, bounded, whole and RANGE frames, ranks, lag, a frame
// statistic — straight from a slice and from the parallel sort's
// ordered Stream; and it holds -presorted's contract: a partition that
// returns, or an order that goes backwards, is a panic naming the row.
func TestWindowPresortedMatchesWindow(t *testing.T) {
	n := 2*rangeMin + 999
	rng := rand.New(rand.NewSource(3))
	rows := make([]winRow, n)
	for i := range rows {
		rows[i] = winRow{Dept: "d" + strconv.Itoa(rng.Intn(5)), Name: strconv.Itoa(rng.Intn(100)), Salary: int64(rng.Intn(1000))}
	}
	bySalary := func(a, b winRow) int { return cmp.Compare(a.Salary, b.Salary) }
	byDeptSalary := func(a, b winRow) int {
		if c := strings.Compare(a.Dept, b.Dept); c != 0 {
			return c
		}
		return bySalary(a, b)
	}
	num := func(r winRow) (float64, bool) { return float64(r.Salary), true }
	clause := func(fr WindowFrame) WindowClause[winRow] {
		specs := []WindowSpec[winRow]{
			{Kind: WSum, Int: func(r winRow) (int64, bool) { return r.Salary, true }},
			{Kind: WRank}, {Kind: WRowNumber},
			{Kind: WLag, N: 1, Val: func(r winRow) (any, bool) { return r.Name, true }},
			{Kind: WAvg, Num: num},
		}
		if !isRunningFrame(fr) { // a frame statistic scans the frame per row: quadratic over the running frame
			specs = append(specs, WindowSpec[winRow]{Kind: WAgg, Agg: WAggStdDev, Num: num})
		}
		return WindowClause[winRow]{
			Partition: func(r winRow) string { return r.Dept },
			HasOrder:  true, Compare: bySalary, KeyInt: func(r winRow) int64 { return r.Salary },
			RangeKey: func(r winRow) float64 { return float64(r.Salary) },
			Frame:    fr,
			Specs:    specs,
		}
	}
	build := func(r winRow, res WindowRow) []any { return append([]any{r.Dept, r.Name, r.Salary}, res.Values()...) }
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, byDeptSalary)
	// The running frame over the whole input (past the range threshold,
	// so the run scan is parallel); the other frames over a prefix — the
	// comparison of boxed rows is what the race detector pays for.
	for i, fr := range []WindowFrame{{Preceding: -1}, {Preceding: 2, Following: 1}, {Preceding: -1, Following: -1}, {Range: true, RangePreceding: 50, RangeFollowing: 0}} {
		c := clause(fr)
		in, inSorted := rows, sorted
		if i > 0 {
			in = rows[:4000]
			inSorted = slices.Clone(in)
			slices.SortStableFunc(inSorted, byDeptSalary)
		}
		want := slices.Collect(Window([]WindowClause[winRow]{c}, build)(slices.Values(inSorted)))
		got := slices.Collect(WindowPresorted([]WindowClause[winRow]{c}, build)(ParallelFromSlice(inSorted, 7)).Serial())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("frame %+v: presorted over a slice differs from Window (first diff at row %d)", fr, firstDiffAny(got, want))
		}
		// Through the parallel sort: its rows read in place.
		got = slices.Collect(WindowPresorted([]WindowClause[winRow]{c}, build)(SortParallelFunc(byDeptSalary)(ParallelFromSlice(in, 5))).Serial())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("frame %+v: presorted over the parallel sort differs from Window (first diff at row %d)", fr, firstDiffAny(got, want))
		}
	}
	// The contract.
	refuse := func(name string, in []winRow, want string) {
		t.Helper()
		defer func() {
			r := recover()
			if r == nil {
				t.Errorf("%s: no panic", name)
			} else if !strings.Contains(r.(error).Error(), want) {
				t.Errorf("%s: got %q", name, r)
			}
		}()
		_ = slices.Collect(WindowPresorted([]WindowClause[winRow]{clause(WindowFrame{Preceding: -1})}, build)(ParallelFromSlice(in, 3)).Serial())
	}
	refuse("returning partition", []winRow{{Dept: "a", Salary: 1}, {Dept: "b", Salary: 1}, {Dept: "a", Salary: 2}}, "row 3 returns to partition \"a\"")
	refuse("backwards order", []winRow{{Dept: "a", Salary: 2}, {Dept: "a", Salary: 1}}, "row 2 comes before the row above it")
}

func firstDiffAny(a, b [][]any) int {
	for i := range min(len(a), len(b)) {
		if !reflect.DeepEqual(a[i], b[i]) {
			return i
		}
	}
	return min(len(a), len(b))
}
