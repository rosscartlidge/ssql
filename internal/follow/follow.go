// Package follow is the graph walk behind ssql.Follow and typed.Follow
// (DFC161): one implementation of the three searches, so the record and
// typed lanes cannot drift. The callers adapt their rows: they build the
// adjacency, say how a hop changes the running totals, and turn a route
// into an output row.
package follow

import (
	"container/heap"
	"slices"
)

// Mode is the search.
type Mode int

const (
	FewestHops Mode = iota // each node once per seed, by (hops, path, sums): BFS
	LowestSum              // each node once per seed, by (lowest, hops, path, sums): Dijkstra
	AllPaths               // every simple path, depth first in edge order
)

// Spec is one graph, ready to walk from any number of seeds.
type Spec[K comparable, N, E, A any] struct {
	Mode     Mode
	MaxDepth int // > 0: stop at that many hops (not with LowestSum)
	// Adj is the out-edges of each node, in edge order.
	Adj map[K][]E
	// Target is an edge's target node and key (the caller dropped edges
	// whose target is no node).
	Target func(E) (N, K)
	// Step is the totals after one more hop along e from node from.
	Step func(acc A, from N, e E) A
	// CmpNode orders node values, as sort orders them.
	CmpNode func(a, b N) int
	// CmpLowest orders the LowestSum total; CmpSums the other totals,
	// the last part of every tie key.
	CmpLowest, CmpSums func(a, b A) int
}

// Route is a walk's end: the node, the hops, the totals; routes share
// their prefixes through Parent.
type Route[N, A any] struct {
	Node   N
	Depth  int64
	Acc    A
	Parent *Route[N, A]
	path   []N
	rank   int // position in its BFS round, sorted by path
	index  int // heap position
}

// Path is the route's nodes, seed first.
func (r *Route[N, A]) Path() []N {
	if r.path != nil {
		return r.path
	}
	n := int(r.Depth) + 1
	p := make([]N, n)
	for t := r; t != nil; t = t.Parent {
		n--
		p[n] = t.Node
	}
	r.path = p
	return p
}

// Undirected is the edges plus their reverses (swap), deduplicated by
// key as whole rows: SQL's UNION, so a pair listed both ways is one
// pair and a duplicate row one row.
func Undirected[E any, R comparable](edges []E, swap func(E) E, key func(E) R) []E {
	seen := make(map[R]bool, 2*len(edges))
	out := make([]E, 0, 2*len(edges))
	add := func(e E) {
		if k := key(e); !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	for _, e := range edges {
		add(e)
	}
	for _, e := range edges {
		add(swap(e))
	}
	return out
}

// Walk emits the seed's routes, the seed itself first (depth 0); a seed
// with no node (ok false) is that one row. It returns false when yield
// stopped.
func (sp *Spec[K, N, E, A]) Walk(node N, key K, ok bool, zero A, yield func(*Route[N, A]) bool) bool {
	start := &Route[N, A]{Node: node, Acc: zero}
	if !yield(start) {
		return false
	}
	if !ok {
		return true
	}
	switch sp.Mode {
	case AllPaths:
		return sp.allPaths(start, key, map[K]bool{key: true}, yield)
	case LowestSum:
		return sp.lowestSum(start, key, yield)
	}
	return sp.fewestHops(start, key, yield)
}

func (sp *Spec[K, N, E, A]) next(r *Route[N, A], e E) (*Route[N, A], K) {
	n, k := sp.Target(e)
	return &Route[N, A]{Node: n, Depth: r.Depth + 1, Acc: sp.Step(r.Acc, r.Node, e), Parent: r}, k
}

func (sp *Spec[K, N, E, A]) cmpPaths(a, b []N) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := sp.CmpNode(a[i], b[i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

func (sp *Spec[K, N, E, A]) allPaths(r *Route[N, A], key K, onPath map[K]bool, yield func(*Route[N, A]) bool) bool {
	if sp.MaxDepth > 0 && r.Depth >= int64(sp.MaxDepth) {
		return true
	}
	for _, e := range sp.Adj[key] {
		_, k := sp.Target(e)
		if onPath[k] {
			continue
		}
		n, k := sp.next(r, e)
		if !yield(n) {
			return false
		}
		onPath[k] = true
		ok := sp.allPaths(n, k, onPath, yield)
		delete(onPath, k)
		if !ok {
			return false
		}
	}
	return true
}

// fewestHops is a BFS by rounds. The frontier is sorted by path, so a
// node's first candidate in frontier order has the least path; among
// candidates with the same path (duplicate edges) the least sums win.
func (sp *Spec[K, N, E, A]) fewestHops(start *Route[N, A], key K, yield func(*Route[N, A]) bool) bool {
	type item struct {
		r *Route[N, A]
		k K
	}
	visited := map[K]bool{key: true}
	frontier := []item{{start, key}}
	for round := 1; len(frontier) > 0; round++ {
		if sp.MaxDepth > 0 && round > sp.MaxDepth {
			return true
		}
		best := make(map[K]*Route[N, A])
		var found []item
		for _, it := range frontier {
			for _, e := range sp.Adj[it.k] {
				if _, k := sp.Target(e); visited[k] {
					continue
				}
				n, k := sp.next(it.r, e)
				old, seen := best[k]
				switch {
				case !seen:
					best[k] = n
					found = append(found, item{n, k})
				case old.Parent == it.r && sp.CmpSums(n.Acc, old.Acc) < 0:
					old.Acc = n.Acc // same path, lighter duplicate edge
				}
			}
		}
		for _, it := range found {
			visited[it.k] = true
		}
		slices.SortStableFunc(found, func(a, b item) int {
			if a.r.Parent.rank != b.r.Parent.rank {
				return a.r.Parent.rank - b.r.Parent.rank
			}
			return sp.CmpNode(a.r.Node, b.r.Node)
		})
		for i, it := range found {
			it.r.rank = i
			if !yield(it.r) {
				return false
			}
		}
		frontier = found
	}
	return true
}

// lessLowest is LowestSum's order: (lowest, hops, path, sums).
func (sp *Spec[K, N, E, A]) lessLowest(a, b *Route[N, A]) bool {
	if c := sp.CmpLowest(a.Acc, b.Acc); c != 0 {
		return c < 0
	}
	if a.Depth != b.Depth {
		return a.Depth < b.Depth
	}
	if c := sp.cmpPaths(a.Path(), b.Path()); c != 0 {
		return c < 0
	}
	return sp.CmpSums(a.Acc, b.Acc) < 0
}

type routeHeap[K comparable, N, E, A any] struct {
	sp *Spec[K, N, E, A]
	rs []*Route[N, A]
	ks []K
}

func (h *routeHeap[K, N, E, A]) Len() int           { return len(h.rs) }
func (h *routeHeap[K, N, E, A]) Less(i, j int) bool { return h.sp.lessLowest(h.rs[i], h.rs[j]) }
func (h *routeHeap[K, N, E, A]) Swap(i, j int) {
	h.rs[i], h.rs[j] = h.rs[j], h.rs[i]
	h.ks[i], h.ks[j] = h.ks[j], h.ks[i]
	h.rs[i].index, h.rs[j].index = i, j
}
func (h *routeHeap[K, N, E, A]) Push(x any) {
	it := x.(struct {
		r *Route[N, A]
		k K
	})
	it.r.index = len(h.rs)
	h.rs, h.ks = append(h.rs, it.r), append(h.ks, it.k)
}
func (h *routeHeap[K, N, E, A]) Pop() any {
	n := len(h.rs) - 1
	it := struct {
		r *Route[N, A]
		k K
	}{h.rs[n], h.ks[n]}
	h.rs, h.ks = h.rs[:n], h.ks[:n]
	return it
}

// lowestSum is Dijkstra on (lowest, hops, path, sums): a hop weighing
// ≥ 0 never lowers that key (the caller's Step refuses a negative or
// absent weight), and appending one edge to two routes keeps their
// order, so the first time a node is settled is its least route.
func (sp *Spec[K, N, E, A]) lowestSum(start *Route[N, A], key K, yield func(*Route[N, A]) bool) bool {
	type item = struct {
		r *Route[N, A]
		k K
	}
	settled := map[K]bool{key: true}
	best := map[K]*Route[N, A]{}
	h := &routeHeap[K, N, E, A]{sp: sp}
	relax := func(r *Route[N, A], from K) {
		for _, e := range sp.Adj[from] {
			if _, k := sp.Target(e); settled[k] {
				continue
			}
			n, k := sp.next(r, e)
			if old, ok := best[k]; ok && !sp.lessLowest(n, old) {
				continue
			}
			best[k] = n
			heap.Push(h, item{n, k})
		}
	}
	relax(start, key)
	for h.Len() > 0 {
		it := heap.Pop(h).(item)
		if settled[it.k] || best[it.k] != it.r {
			continue
		}
		settled[it.k] = true
		if !yield(it.r) {
			return false
		}
		relax(it.r, it.k)
	}
	return true
}
