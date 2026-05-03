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

	// RecordBoundaryBefore is the set of fragment indices that need
	// a typed→Record adapter inserted immediately before them.
	// These are fragments where:
	//   - the upstream produces ShapeSeqTyped (or ShapeStream — in
	//     which case both a Stream.Serial() AND a toRecord() get
	//     inserted), and
	//   - this fragment Accepts ShapeSeqRecord (it's a Record-mode
	//     command that follows a typed segment)
	//
	// Phase B: lets typed→Record mixed pipelines work without the
	// user having to drop the SSQLGO=typed env var — the parallel-
	// CSV-parse and any typed-supported transforms still happen,
	// then we fan in to Record at the boundary.
	RecordBoundaryBefore map[int]bool
}

// MakePlan builds a Plan from the fragment list. Fragments without
// Capabilities are treated as "no opinion" — they pass through and
// don't contribute to source-parallelism reach analysis.
func MakePlan(fragments []*CodeFragment) Plan {
	plan := Plan{
		SerialBoundaryBefore: map[int]bool{},
		RecordBoundaryBefore: map[int]bool{},
	}

	// Pass 1 — parallelism reach.
	//
	// The source goes parallel iff at least one *downstream*
	// fragment can consume Stream input (i.e. some
	// Capabilities.Accepts == ShapeStream). Fragments without
	// Capabilities don't count (Record-mode emitters, init
	// fragments, error fragments).
	//
	// Source fragments themselves don't count — they have
	// Accepts == ShapeNone, and their Produces is what we're
	// trying to decide. The parallelism question is "does anyone
	// need Stream input?", not "did some fragment claim to
	// produce Stream?".
	for _, f := range fragments {
		if f == nil || f.Capabilities == nil {
			continue
		}
		if f.Capabilities.Accepts == ShapeStream {
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

		// Phase B: typed→Record boundary. When this fragment
		// requires Record input but the running shape isn't Record
		// yet, mark it for a toRecord adapter upstream. If the
		// upstream is Stream, it'll get a Serial() boundary first
		// (next clause), then the Record boundary chains after —
		// the renderer emits both adapters in sequence.
		if c.Accepts == ShapeSeqRecord && currShape != ShapeSeqRecord && currShape != ShapeNone {
			plan.RecordBoundaryBefore[i] = true
			if currShape == ShapeStream {
				plan.SerialBoundaryBefore[i] = true
			}
			currShape = ShapeSeqRecord
		} else if c.SerialOnly && currShape == ShapeStream {
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
