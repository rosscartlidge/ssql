package ssql

import (
	"container/heap"
	"fmt"
	"iter"
	"slices"
	"strings"
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

// followState is one route's end: the node, how it got there and the
// running totals. Routes share their prefixes through parent.
type followState struct {
	node   any
	key    string
	depth  int64
	parent *followState
	sums   []followAcc // the Sums, then Lowest
	path   []any       // materialised on demand (pathOf)
	rank   int         // position in its BFS round, sorted by path
	index  int         // heap index
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
	adj    map[string][]followEdge
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
	g := &followGraph{cfg: cfg, adj: make(map[string][]followEdge), out: make(map[*Schema]*followShape)}
	fields := make([]string, 0, len(cfg.Sums)+1)
	for _, s := range cfg.Sums {
		fields = append(fields, s.Field)
		g.accs = append(g.accs, s.Name)
	}
	if cfg.Mode == FollowLowestSum {
		fields = append(fields, cfg.Lowest.Field)
		g.accs = append(g.accs, cfg.Lowest.Name)
	}
	if cfg.Mode == FollowLowestSum && cfg.MaxDepth > 0 {
		panic(fmt.Errorf("follow: a depth limit is not defined with the lowest-sum search (the cheapest route within N hops is not what Dijkstra finds)"))
	}
	var rows []Record
	for r := range edges {
		rows = append(rows, r)
	}
	if cfg.Both {
		rows = followUndirected(rows, cfg.SrcField, cfg.DstField)
	}
	g.kinds = make([]string, len(fields))
	g.scales = make([]int, len(fields))
	for i := range g.kinds {
		g.kinds[i] = "int"
	}
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
				g.scales[i] = max(g.scales[i], int(x.Scale()))
			}
		}
		g.adj[srcKey] = append(g.adj[srcKey], e)
	}
	return g
}

// followUndirected is the edges plus their reverses, deduplicated as
// whole rows (SQL's UNION): a pair the file lists both ways is one pair,
// and a duplicate row is one row.
func followUndirected(rows []Record, src, dst string) []Record {
	seen := make(map[string]bool, 2*len(rows))
	out := make([]Record, 0, 2*len(rows))
	add := func(r Record) {
		k := RecordKey(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	for _, r := range rows {
		add(r)
	}
	for _, r := range rows {
		s, sok := Get[any](r, src)
		d, dok := Get[any](r, dst)
		m := r.ToMutable()
		if dok {
			m.put(src, d)
		} else {
			m.put(src, nil)
		}
		if sok {
			m.put(dst, s)
		} else {
			m.put(dst, nil)
		}
		add(m.Freeze())
	}
	return out
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

func (g *followGraph) seedState(seed Record) *followState {
	node, _ := Get[any](seed, g.cfg.NodeField)
	key, _ := followKey(node)
	s := &followState{node: node, key: key, sums: make([]followAcc, len(g.accs))}
	for i := range s.sums {
		s.sums[i] = g.zero(i)
	}
	return s
}

// next is the route s extended by e.
func (g *followGraph) next(s *followState, e followEdge) *followState {
	n := &followState{node: e.dst, key: e.dstKey, depth: s.depth + 1, parent: s, sums: make([]followAcc, len(s.sums))}
	for i := range s.sums {
		if g.cfg.Mode == FollowLowestSum && i == len(s.sums)-1 {
			if err := followWeightCheck(e.weights[i], g.cfg.Lowest.Field, s.node, e.dst); err != nil {
				panic(err)
			}
		}
		n.sums[i] = g.add(i, s.sums[i], e.weights[i])
	}
	return n
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

// pathOf is the route's nodes, seed first.
func pathOf(s *followState) []any {
	if s.path != nil {
		return s.path
	}
	n := int(s.depth) + 1
	p := make([]any, n)
	for t := s; t != nil; t = t.parent {
		n--
		p[n] = t.node
	}
	s.path = p
	return p
}

func comparePaths(a, b []any) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := CompareAny(a[i], b[i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
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

// lessLowest is FollowLowestSum's order: (lowest, hops, path, sums).
func lessLowest(a, b *followState) bool {
	l := len(a.sums) - 1
	if c := compareSums(a.sums[l:], b.sums[l:]); c != 0 {
		return c < 0
	}
	if a.depth != b.depth {
		return a.depth < b.depth
	}
	if c := comparePaths(pathOf(a), pathOf(b)); c != 0 {
		return c < 0
	}
	return compareSums(a.sums[:l], b.sums[:l]) < 0
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

// row is the output row for the route s from seed.
func (g *followGraph) row(seed Record, sh *followShape, s *followState) Record {
	values := make([]any, len(sh.schema.fields))
	copy(values, seed.values)
	values[sh.nodeIdx] = s.node
	i := sh.nSeed
	if g.cfg.DepthField != "" {
		values[i] = s.depth
		i++
	}
	for _, a := range s.sums {
		if !a.absent {
			values[i] = a.v
		}
		i++
	}
	if g.cfg.PathField != "" {
		values[i] = JSONValue(pathOf(s))
	}
	return NewRecordFromSchema(sh.schema, values)
}

// walk emits seed's routes; false when the consumer stopped.
func (g *followGraph) walk(seed Record, yield func(Record) bool) bool {
	sh := g.shape(seed)
	start := g.seedState(seed)
	if !yield(g.row(seed, sh, start)) {
		return false
	}
	if start.key == "" {
		return true // no node, so nowhere to go; the seed row stands
	}
	switch g.cfg.Mode {
	case FollowAllPaths:
		onPath := map[string]bool{start.key: true}
		return g.allPaths(seed, sh, start, onPath, yield)
	case FollowLowestSum:
		return g.lowestSum(seed, sh, start, yield)
	}
	return g.fewestHops(seed, sh, start, yield)
}

func (g *followGraph) allPaths(seed Record, sh *followShape, s *followState, onPath map[string]bool, yield func(Record) bool) bool {
	if g.cfg.MaxDepth > 0 && s.depth >= int64(g.cfg.MaxDepth) {
		return true
	}
	for _, e := range g.adj[s.key] {
		if onPath[e.dstKey] {
			continue
		}
		n := g.next(s, e)
		if !yield(g.row(seed, sh, n)) {
			return false
		}
		onPath[e.dstKey] = true
		ok := g.allPaths(seed, sh, n, onPath, yield)
		delete(onPath, e.dstKey)
		if !ok {
			return false
		}
	}
	return true
}

// fewestHops is a BFS by rounds. The frontier is kept sorted by path, so
// a node's first candidate in frontier order has the least path; among
// candidates with the same path (duplicate edges) the least sums win.
func (g *followGraph) fewestHops(seed Record, sh *followShape, start *followState, yield func(Record) bool) bool {
	visited := map[string]bool{start.key: true}
	frontier := []*followState{start}
	for round := 1; len(frontier) > 0; round++ {
		if g.cfg.MaxDepth > 0 && round > g.cfg.MaxDepth {
			return true
		}
		best := make(map[string]*followState)
		var found []*followState
		for _, s := range frontier {
			for _, e := range g.adj[s.key] {
				if visited[e.dstKey] {
					continue
				}
				n := g.next(s, e)
				old, seen := best[e.dstKey]
				switch {
				case !seen:
					best[e.dstKey] = n
					found = append(found, n)
				case old.parent == s && compareSums(n.sums, old.sums) < 0:
					*old = *n // same path, lighter duplicate edge
				}
			}
		}
		for _, n := range found {
			visited[n.key] = true
		}
		slices.SortStableFunc(found, func(a, b *followState) int {
			if a.parent.rank != b.parent.rank {
				return a.parent.rank - b.parent.rank
			}
			return CompareAny(a.node, b.node)
		})
		for i, n := range found {
			n.rank = i
			if !yield(g.row(seed, sh, n)) {
				return false
			}
		}
		frontier = found
	}
	return true
}

type followHeap []*followState

func (h followHeap) Len() int           { return len(h) }
func (h followHeap) Less(i, j int) bool { return lessLowest(h[i], h[j]) }
func (h followHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *followHeap) Push(x any)        { s := x.(*followState); s.index = len(*h); *h = append(*h, s) }
func (h *followHeap) Pop() any {
	old := *h
	s := old[len(old)-1]
	*h = old[:len(old)-1]
	return s
}

// lowestSum is Dijkstra on (lowest, hops, path, sums): extending a route
// by a weight ≥ 0 never lowers that key, and appending one edge to two
// routes keeps their order, so the first time a node is settled is its
// least route.
func (g *followGraph) lowestSum(seed Record, sh *followShape, start *followState, yield func(Record) bool) bool {
	settled := map[string]bool{start.key: true}
	best := map[string]*followState{}
	h := &followHeap{}
	relax := func(s *followState) {
		for _, e := range g.adj[s.key] {
			if settled[e.dstKey] {
				continue
			}
			n := g.next(s, e)
			if old, ok := best[e.dstKey]; ok && !lessLowest(n, old) {
				continue
			}
			best[e.dstKey] = n
			heap.Push(h, n)
		}
	}
	relax(start)
	for h.Len() > 0 {
		s := heap.Pop(h).(*followState)
		if settled[s.key] || best[s.key] != s {
			continue
		}
		settled[s.key] = true
		if !yield(g.row(seed, sh, s)) {
			return false
		}
		relax(s)
	}
	return true
}
