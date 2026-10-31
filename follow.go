package ssql

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/rosscartlidge/ssql/v4/internal/follow"
)

// FollowMode selects how [Follow] walks the graph (DFC161 §3).
type FollowMode int

const (
	// FollowFewestHops reaches each node once per seed, by the fewest
	// hops (a breadth-first search) — the default.
	FollowFewestHops FollowMode = iota
	// FollowLowestSum reaches each node once per seed, by the lowest
	// total of an edge field (Dijkstra).
	FollowLowestSum
	// FollowAllPaths emits every simple path: no node twice on one
	// path, every prefix its own row. Exponential on a dense graph.
	FollowAllPaths
)

// FollowSum is a running total of the edge field Field along the path,
// written to the column Name.
type FollowSum struct{ Field, Name string }

// FollowConfig configures [Follow].
type FollowConfig struct {
	// NodeField is the seed's node column; SrcField and DstField are the
	// edge's endpoints. A reached node's value is written to NodeField.
	NodeField, SrcField, DstField string
	Mode                          FollowMode
	// Lowest is the sum FollowLowestSum minimises; it is written like a
	// Sums entry.
	Lowest FollowSum
	Sums   []FollowSum
	// DepthField, when set, is the hop count (0 on the seed row);
	// PathField, when set, is the nodes visited, seed first, as a list.
	DepthField, PathField string
	// MaxDepth, when > 0, stops expanding at that many hops. It is not
	// defined with FollowLowestSum: a depth-bounded cheapest route is
	// not what Dijkstra finds.
	MaxDepth int
	// Both makes the graph undirected: every edge is usable either way,
	// and the edges with their reverses are deduplicated as whole rows.
	Both bool
}

// followEdge is one usable edge, its endpoints keyed once.
type followEdge struct {
	dst     any
	dstKey  string
	weights []any // the Sums' fields, then Lowest's
}

// followAcc is a running total: an Int128 while the edge field holds
// integers, a Decimal once one is a decimal, a float64 once one is a
// float — the kind is the column's, decided over all the edges before
// the walk (an SQL column has one type, and the SQL lanes cast the
// anchor's 0 to it). absent: a hop's weight was missing, so the total
// is, as SQL's NULL + x is.
type followAcc struct {
	v      any
	absent bool
}

// followKey is a node's key, as [FieldsKey] keys one field: numbers by
// value (an int 3 and a float 3 are one node), else by printed value. An
// absent or empty value is no node.
func followKey(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	if s, ok := v.(string); ok && s == "" {
		return "", false
	}
	if n, isNum := joinNumber(v); isNum {
		return fmt.Sprintf("n:%v", n), true
	}
	return fmt.Sprintf("v:%v", v), true
}

// Follow walks a graph: every seed row's NodeField is a starting node,
// the edges are the rows of edges (SrcField → DstField), and the output
// is one row per route the mode keeps — the seed's columns with
// NodeField set to the node reached, plus the accumulator columns
// (DepthField, the Sums, Lowest, PathField, in that order). The seed
// itself is the depth-0 row. It is a join run to a fixpoint: the edges
// are read once into an adjacency map, as a hash join reads its right
// side, and each round joins the routes the last round found (DFC161).
//
// Ties are broken by value, never by input order, so every engine keeps
// the same row: FollowFewestHops keeps, per node, the least (hops, path,
// sums) and FollowLowestSum the least (lowest, hops, path, sums), paths
// compared node by node as sort compares values. Output is in seed
// order; within a seed, by round then path (fewest hops), by the order
// nodes are settled (lowest sum), or depth first in edge order (all
// paths). An absent or negative Lowest weight on an edge the search
// takes is an error, as an integer overflow of a sum is.
func Follow(edges iter.Seq[Record], cfg FollowConfig) Filter[Record, Record] {
	return func(seeds iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			g := newFollowGraph(edges, cfg)
			for seed := range seeds {
				if !g.walk(seed, yield) {
					return
				}
			}
		}
	}
}

type followGraph struct {
	cfg    FollowConfig
	spec   follow.Spec[string, any, followEdge, []followAcc]
	kinds  []string // per accumulator: "int", "decimal" or "float"
	scales []int    // decimal accumulators' scale
	accs   []string // accumulator column names, Sums then Lowest
	out    map[*Schema]*followShape
}

// followShape is the output schema for one seed schema, built once.
type followShape struct {
	schema  *Schema
	nodeIdx int // NodeField's position, -1 when the seed lacks it
	nSeed   int
}

func newFollowGraph(edges iter.Seq[Record], cfg FollowConfig) *followGraph {
	g := &followGraph{cfg: cfg, out: make(map[*Schema]*followShape)}
	fields := make([]string, 0, len(cfg.Sums)+1)
	for _, s := range cfg.Sums {
		fields = append(fields, s.Field)
		g.accs = append(g.accs, s.Name)
	}
	lowest := cfg.Mode == FollowLowestSum
	if lowest {
		fields = append(fields, cfg.Lowest.Field)
		g.accs = append(g.accs, cfg.Lowest.Name)
		if cfg.MaxDepth > 0 {
			panic(fmt.Errorf("follow: a depth limit is not defined with the lowest-sum search (the cheapest route within N hops is not what Dijkstra finds)"))
		}
	}
	var rows []Record
	for r := range edges {
		rows = append(rows, r)
	}
	if cfg.Both {
		rows = follow.Undirected(rows, func(r Record) Record {
			return followSwap(r, cfg.SrcField, cfg.DstField)
		}, RecordKey)
	}
	g.kinds = make([]string, len(fields))
	g.scales = make([]int, len(fields))
	for i := range g.kinds {
		g.kinds[i] = "int"
	}
	adj := make(map[string][]followEdge)
	for _, r := range rows {
		src, _ := Get[any](r, cfg.SrcField)
		srcKey, ok := followKey(src)
		if !ok {
			continue
		}
		dst, _ := Get[any](r, cfg.DstField)
		dstKey, ok := followKey(dst)
		if !ok {
			continue // SQL's step: NOT list_contains(path, NULL) is NULL, so no row
		}
		e := followEdge{dst: dst, dstKey: dstKey, weights: make([]any, len(fields))}
		for i, f := range fields {
			w, _ := Get[any](r, f)
			e.weights[i] = w
			switch x := w.(type) {
			case float64:
				g.kinds[i] = "float"
			case Decimal:
				if g.kinds[i] == "int" {
					g.kinds[i] = "decimal"
				}
				g.scales[i] = max(g.scales[i], x.Scale())
			}
		}
		adj[srcKey] = append(adj[srcKey], e)
	}
	nSums := len(cfg.Sums)
	g.spec = follow.Spec[string, any, followEdge, []followAcc]{
		MaxDepth: cfg.MaxDepth,
		Adj:      adj,
		Target:   func(e followEdge) (any, string) { return e.dst, e.dstKey },
		Step: func(acc []followAcc, from any, e followEdge) []followAcc {
			n := make([]followAcc, len(acc))
			for i := range acc {
				if lowest && i == nSums {
					if err := followWeightCheck(e.weights[i], cfg.Lowest.Field, from, e.dst); err != nil {
						panic(err)
					}
				}
				n[i] = g.add(i, acc[i], e.weights[i])
			}
			return n
		},
		CmpNode:   CompareAny,
		CmpLowest: func(a, b []followAcc) int { return compareSums(a[nSums:], b[nSums:]) },
		CmpSums:   func(a, b []followAcc) int { return compareSums(a[:nSums], b[:nSums]) },
	}
	switch cfg.Mode {
	case FollowLowestSum:
		g.spec.Mode = follow.LowestSum
	case FollowAllPaths:
		g.spec.Mode = follow.AllPaths
	}
	return g
}

// followSwap is the edge reversed: src and dst exchange values.
func followSwap(r Record, src, dst string) Record {
	s, _ := Get[any](r, src)
	d, _ := Get[any](r, dst)
	m := r.ToMutable()
	m.put(src, d)
	m.put(dst, s)
	return m.Freeze()
}

// zero is an accumulator's starting total, in its column's kind.
func (g *followGraph) zero(i int) followAcc {
	switch g.kinds[i] {
	case "float":
		return followAcc{v: float64(0)}
	case "decimal":
		return followAcc{v: MustDecimalFromInt128(Int128{}, g.scales[i])}
	}
	return followAcc{v: Int128{}}
}

// add is the total after one more hop weighing w.
func (g *followGraph) add(i int, a followAcc, w any) followAcc {
	if a.absent {
		return a
	}
	switch g.kinds[i] {
	case "float":
		f, ok := toFloat(w)
		if !ok || w == nil {
			return followAcc{absent: true}
		}
		return followAcc{v: a.v.(float64) + f}
	case "decimal":
		var d Decimal
		switch x := w.(type) {
		case Decimal:
			d = x
		case int64:
			d = MustDecimalFromInt128(Int128From(x), g.scales[i])
		case Int128:
			d = MustDecimalFromInt128(x, g.scales[i])
		default:
			return followAcc{absent: true}
		}
		return followAcc{v: a.v.(Decimal).Add(d)}
	}
	switch x := w.(type) {
	case int64:
		return followAcc{v: a.v.(Int128).Add(Int128From(x))}
	case Int128:
		return followAcc{v: a.v.(Int128).Add(x)}
	}
	return followAcc{absent: true}
}

// followWeightCheck refuses a Dijkstra weight that would make the
// search wrong instead of slow: absent, not a number, or negative.
func followWeightCheck(w any, field string, from, to any) error {
	if w == nil {
		return fmt.Errorf("follow -lowest-sum: edge %v → %v has no %s", from, to, field)
	}
	if _, ok := joinNumber(w); !ok {
		return fmt.Errorf("follow -lowest-sum: edge %v → %v has a non-numeric %s (%v)", from, to, field, w)
	}
	if CompareAny(w, int64(0)) < 0 {
		return fmt.Errorf("follow -lowest-sum: edge %v → %v has a negative %s (%v); the search needs weights ≥ 0", from, to, field, w)
	}
	return nil
}

func compareSums(a, b []followAcc) int {
	for i := range a {
		switch {
		case a[i].absent && b[i].absent:
			continue
		case a[i].absent:
			return 1 // NULLS LAST, as the SQL lanes order
		case b[i].absent:
			return -1
		}
		if c := CompareAny(a[i].v, b[i].v); c != 0 {
			return c
		}
	}
	return 0
}

func (g *followGraph) shape(seed Record) *followShape {
	if sh, ok := g.out[seed.schema]; ok {
		return sh
	}
	var fields []string
	if seed.schema != nil {
		fields = slices.Clone(seed.schema.fields)
	}
	sh := &followShape{nodeIdx: slices.Index(fields, g.cfg.NodeField), nSeed: len(fields)}
	if sh.nodeIdx < 0 {
		sh.nodeIdx = len(fields)
		fields = append(fields, g.cfg.NodeField)
		sh.nSeed++
	}
	acc := make([]string, 0, len(g.accs)+2)
	if g.cfg.DepthField != "" {
		acc = append(acc, g.cfg.DepthField)
	}
	acc = append(acc, g.accs...)
	if g.cfg.PathField != "" {
		acc = append(acc, g.cfg.PathField)
	}
	for _, a := range acc {
		if slices.Contains(fields, a) {
			panic(fmt.Errorf("follow: result column %q is already a seed column (available: %s)", a, strings.Join(fields, ", ")))
		}
		fields = append(fields, a)
	}
	sh.schema = NewSchema(fields)
	g.out[seed.schema] = sh
	return sh
}

// row is the output row for the route r from seed.
func (g *followGraph) row(seed Record, sh *followShape, r *follow.Route[any, []followAcc]) Record {
	values := make([]any, len(sh.schema.fields))
	copy(values, seed.values)
	values[sh.nodeIdx] = r.Node
	i := sh.nSeed
	if g.cfg.DepthField != "" {
		values[i] = r.Depth
		i++
	}
	for _, a := range r.Acc {
		if !a.absent {
			values[i] = a.v
		}
		i++
	}
	if g.cfg.PathField != "" {
		values[i] = JSONValue(r.Path())
	}
	return NewRecordFromSchema(sh.schema, values)
}

// walk emits seed's routes; false when the consumer stopped.
func (g *followGraph) walk(seed Record, yield func(Record) bool) bool {
	sh := g.shape(seed)
	node, _ := Get[any](seed, g.cfg.NodeField)
	key, ok := followKey(node)
	zero := make([]followAcc, len(g.accs))
	for i := range zero {
		zero[i] = g.zero(i)
	}
	return g.spec.Walk(node, key, ok, zero, func(r *follow.Route[any, []followAcc]) bool {
		return yield(g.row(seed, sh, r))
	})
}
