// Typed-mode pipeline planner.
//
// MakePlan walks the fragment list before code emission and decides:
//
//  1. Whether the source should emit a parallel primitive
//     (typed.ReadCSVParallel, ReadParquetParallel, etc.) or the
//     serial equivalent. The decision is "parallelism reach" —
//     parallel only if at least one downstream fragment can
//     consume ShapeStream input. Otherwise the parallel source +
//     Stream.Serial() boundary cost is pure overhead.
//
//  2. Where to insert Stream.Serial() boundaries. When a
//     SerialOnly fragment follows a fragment that produces
//     ShapeStream, a Serial() adapter must run between them.
//
// Phase A only handles the Stream[T] ↔ iter.Seq[T] boundary.
// Phase B (mixed mode) extends this with the
// iter.Seq[T] ↔ iter.Seq[Record] adapter — same algorithm,
// additional shape.
package lib

// Plan is the planner's per-fragment decision matrix. It maps
// fragment indices in the input list to plan annotations.
type Plan struct {
	// SourceParallel is true when the planner has decided the
	// source fragment should emit a parallel primitive (e.g.
	// typed.ReadCSVParallel). The source command's codegen reads
	// this and picks the right template.
	//
	// False when the pipeline has no downstream fragment that can
	// consume ShapeStream input — in that case the parallel
	// source + Serial() boundary would be pure overhead.
	SourceParallel bool

	// SerialBoundaryBefore is the set of fragment indices that need
	// a Stream.Serial() boundary inserted immediately before them.
	// These are fragments where:
	//   - the upstream produces ShapeStream, and
	//   - this fragment is SerialOnly (can't accept ShapeStream)
	SerialBoundaryBefore map[int]bool
}

// MakePlan builds a Plan from the fragment list. Fragments without
// Capabilities are treated as "no opinion" — they pass through and
// don't contribute to source-parallelism reach analysis.
func MakePlan(fragments []*CodeFragment) Plan {
	plan := Plan{
		SerialBoundaryBefore: map[int]bool{},
	}

	// Pass 1 — parallelism reach.
	//
	// The source goes parallel iff at least one downstream
	// fragment with Capabilities accepts ShapeStream OR produces
	// ShapeStream. Fragments without Capabilities don't count for
	// or against (they're shape-agnostic — Record-mode emitters,
	// init fragments, error fragments).
	for _, f := range fragments {
		if f == nil || f.Capabilities == nil {
			continue
		}
		c := f.Capabilities
		if c.Accepts == ShapeStream || c.Produces == ShapeStream {
			plan.SourceParallel = true
			break
		}
	}

	// Pass 2 — boundary insertion.
	//
	// Walk fragments in order, tracking the running output shape.
	// When a SerialOnly fragment receives ShapeStream input, mark
	// it as needing a Serial() boundary upstream (the renderer
	// emits the boundary fragment immediately before this one).
	currShape := ShapeNone
	for i, f := range fragments {
		if f == nil {
			continue
		}
		c := f.Capabilities

		// If this fragment can't even declare its requirements,
		// don't reason about it. It just runs as-is.
		if c == nil {
			continue
		}

		if c.SerialOnly && currShape == ShapeStream {
			plan.SerialBoundaryBefore[i] = true
			currShape = ShapeSeqTyped
		}

		// Update running shape from the fragment's declared output.
		// SerialOnly fragments produce ShapeSeqTyped (Phase A
		// invariant — no SerialOnly op produces a Stream).
		if c.Produces != ShapeNone {
			currShape = c.Produces
		}
	}

	return plan
}
