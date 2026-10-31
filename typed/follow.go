package typed

import (
	"cmp"
	"fmt"
	"iter"

	"github.com/rosscartlidge/ssql/v4/internal/follow"
)

// FollowMode selects the search, as ssql.FollowMode does (DFC161).
type FollowMode int

const (
	FollowFewestHops FollowMode = iota // each node once per seed, by the fewest hops (BFS) — the default
	FollowLowestSum                    // each node once per seed, by the lowest total (Dijkstra)
	FollowAllPaths                     // every simple path
)

// FollowSpec is a typed graph traversal: seeds S, edges E, output rows
// O, running totals A, node keys K. The walk is ssql.Follow's — the same
// package does both — so the typed and record lanes agree row for row.
type FollowSpec[S any, E comparable, O, A any, K cmp.Ordered] struct {
	Mode     FollowMode
	MaxDepth int  // > 0: stop at that many hops (not with FollowLowestSum)
	Both     bool // undirected: the edges plus their reverses (Swap), deduplicated as whole rows

	// SeedNode, Src and Dst are the node keys; false is no node (an
	// empty text key).
	SeedNode func(S) (K, bool)
	Src, Dst func(E) (K, bool)
	// Swap is the edge reversed (Both only).
	Swap func(E) E

	// Zero is the totals on the seed row; Step adds one hop.
	Zero A
	Step func(acc A, e E) A
	// CmpLowest orders the FollowLowestSum total; CmpSums the other
	// totals, the last part of every tie key (nil: no other totals).
	CmpLowest, CmpSums func(a, b A) int
	// Negative reports an edge whose FollowLowestSum weight is below
	// zero, which the search refuses (LowestField names it).
	Negative    func(E) bool
	LowestField string

	// Emit is the output row for a route from s to node.
	Emit func(s S, node K, depth int64, acc A) O
}

func (fs FollowSpec[S, E, O, A, K]) spec(edges iter.Seq[E]) *follow.Spec[K, K, E, A] {
	var rows []E
	for e := range edges {
		rows = append(rows, e)
	}
	if fs.Both {
		rows = follow.Undirected(rows, fs.Swap, func(e E) E { return e })
	}
	adj := make(map[K][]E)
	for _, e := range rows {
		s, ok := fs.Src(e)
		if !ok {
			continue
		}
		if _, ok := fs.Dst(e); !ok {
			continue
		}
		adj[s] = append(adj[s], e)
	}
	cmpSums := fs.CmpSums
	if cmpSums == nil {
		cmpSums = func(A, A) int { return 0 }
	}
	sp := &follow.Spec[K, K, E, A]{
		MaxDepth: fs.MaxDepth,
		Adj:      adj,
		Target:   func(e E) (K, K) { k, _ := fs.Dst(e); return k, k },
		Step: func(acc A, from K, e E) A {
			if fs.Negative != nil && fs.Negative(e) {
				to, _ := fs.Dst(e)
				panic(fmt.Errorf("follow -lowest-sum: edge %v → %v has a negative %s; the search needs weights ≥ 0", from, to, fs.LowestField))
			}
			return fs.Step(acc, e)
		},
		CmpNode:   cmp.Compare[K],
		CmpLowest: fs.CmpLowest,
		CmpSums:   cmpSums,
	}
	switch fs.Mode {
	case FollowLowestSum:
		sp.Mode = follow.LowestSum
		if fs.MaxDepth > 0 {
			panic(fmt.Errorf("follow: a depth limit is not defined with the lowest-sum search"))
		}
	case FollowAllPaths:
		sp.Mode = follow.AllPaths
	}
	return sp
}

func (fs FollowSpec[S, E, O, A, K]) walk(sp *follow.Spec[K, K, E, A], s S, yield func(O) bool) bool {
	k, ok := fs.SeedNode(s)
	return sp.Walk(k, k, ok, fs.Zero, func(r *follow.Route[K, A]) bool {
		return yield(fs.Emit(s, r.Node, r.Depth, r.Acc))
	})
}

// Follow walks the graph of edges from every seed (DFC161): one output
// row per route the mode keeps, the seed itself first. The edges are
// read once into an adjacency map; the seeds stream in order.
func Follow[S any, E comparable, O, A any, K cmp.Ordered](edges iter.Seq[E], fs FollowSpec[S, E, O, A, K]) func(iter.Seq[S]) iter.Seq[O] {
	return func(seeds iter.Seq[S]) iter.Seq[O] {
		return func(yield func(O) bool) {
			sp := fs.spec(edges)
			for s := range seeds {
				if !fs.walk(sp, s, yield) {
					return
				}
			}
		}
	}
}

// FollowParallel is the sharded [Follow]: the adjacency is built once and
// every seed shard walks it independently (each seed's walk is its own),
// so the output is the seeds' order whenever the seeds' is.
func FollowParallel[S any, E comparable, O, A any, K cmp.Ordered](seeds Stream[S], edges iter.Seq[E], fs FollowSpec[S, E, O, A, K]) Stream[O] {
	sp := fs.spec(edges)
	out := make([]iter.Seq[O], len(seeds.shards))
	for i, shard := range seeds.shards {
		shard := shard
		out[i] = func(yield func(O) bool) {
			for s := range shard {
				if !fs.walk(sp, s, yield) {
					return
				}
			}
		}
	}
	return Stream[O]{shards: out, n: seeds.n, ordered: seeds.ordered}
}
