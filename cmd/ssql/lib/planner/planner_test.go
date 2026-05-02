package planner

import (
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// frag is a tiny constructor for tests. Sets Capabilities and Var.
func frag(name string, caps *lib.Capabilities) *lib.CodeFragment {
	return &lib.CodeFragment{Var: name, Capabilities: caps}
}

func caps(accepts, produces lib.Shape, serialOnly bool) *lib.Capabilities {
	return &lib.Capabilities{Accepts: accepts, Produces: produces, SerialOnly: serialOnly}
}

func TestPlanEmpty(t *testing.T) {
	p := MakePlan(nil)
	if p.SourceParallel {
		t.Error("empty pipeline should not be parallel")
	}
	if len(p.SerialBoundaryBefore) != 0 {
		t.Error("empty pipeline should have no boundaries")
	}
}

func TestPlanSerialOnlyPipeline(t *testing.T) {
	// from → sort → to table — sort is SerialOnly, so source
	// should emit a serial primitive (no Stream).
	frags := []*lib.CodeFragment{
		frag("records", caps(lib.ShapeNone, lib.ShapeSeqTyped, false)), // serial source
		frag("sorted", caps(lib.ShapeSeqTyped, lib.ShapeSeqTyped, true)),
		frag("output", caps(lib.ShapeSeqTyped, lib.ShapeNone, true)),
	}
	p := MakePlan(frags)
	if p.SourceParallel {
		t.Error("serial-only pipeline shouldn't elect a parallel source")
	}
	if len(p.SerialBoundaryBefore) != 0 {
		t.Errorf("expected no boundaries (no Stream upstream); got %v", p.SerialBoundaryBefore)
	}
}

func TestPlanParallelFriendlyPipeline(t *testing.T) {
	// from → where → group-by → to table — all parallel-friendly.
	// Source goes parallel; group-by produces iter.Seq[T] (typical
	// of GroupByParallel which fans-in to iter.Seq); no Serial()
	// needed because no SerialOnly fragments downstream.
	frags := []*lib.CodeFragment{
		frag("records", caps(lib.ShapeNone, lib.ShapeStream, false)),
		frag("filtered", caps(lib.ShapeStream, lib.ShapeStream, false)),
		frag("grouped", caps(lib.ShapeStream, lib.ShapeSeqTyped, false)),
		frag("output", caps(lib.ShapeSeqTyped, lib.ShapeNone, false)),
	}
	p := MakePlan(frags)
	if !p.SourceParallel {
		t.Error("parallel-friendly pipeline should elect a parallel source")
	}
	if len(p.SerialBoundaryBefore) != 0 {
		t.Errorf("no SerialOnly fragments → no Serial() boundaries; got %v", p.SerialBoundaryBefore)
	}
}

func TestPlanMixedPipelineInsertsSerialBoundary(t *testing.T) {
	// from → where → sort → to table
	// where wants Stream input (parallel); sort is SerialOnly
	// → boundary before sort.
	frags := []*lib.CodeFragment{
		frag("records", caps(lib.ShapeNone, lib.ShapeStream, false)),
		frag("filtered", caps(lib.ShapeStream, lib.ShapeStream, false)),
		frag("sorted", caps(lib.ShapeSeqTyped, lib.ShapeSeqTyped, true)),
		frag("output", caps(lib.ShapeSeqTyped, lib.ShapeNone, false)),
	}
	p := MakePlan(frags)
	if !p.SourceParallel {
		t.Error("at least one Stream-friendly op exists → parallel source")
	}
	if !p.SerialBoundaryBefore[2] {
		t.Errorf("expected boundary before sort (idx 2); got %v", p.SerialBoundaryBefore)
	}
	if len(p.SerialBoundaryBefore) != 1 {
		t.Errorf("expected exactly 1 boundary; got %v", p.SerialBoundaryBefore)
	}
}

func TestPlanConsecutiveSerialOnly(t *testing.T) {
	// from → where → sort → distinct → to table
	// Boundary before sort, but NOT before distinct (we're already
	// serial after sort).
	frags := []*lib.CodeFragment{
		frag("records", caps(lib.ShapeNone, lib.ShapeStream, false)),
		frag("filtered", caps(lib.ShapeStream, lib.ShapeStream, false)),
		frag("sorted", caps(lib.ShapeSeqTyped, lib.ShapeSeqTyped, true)),
		frag("distinct", caps(lib.ShapeSeqTyped, lib.ShapeSeqTyped, true)),
		frag("output", caps(lib.ShapeSeqTyped, lib.ShapeNone, false)),
	}
	p := MakePlan(frags)
	if !p.SerialBoundaryBefore[2] {
		t.Errorf("expected boundary before sort (idx 2); got %v", p.SerialBoundaryBefore)
	}
	if p.SerialBoundaryBefore[3] {
		t.Errorf("should NOT have boundary before distinct (already serial); got %v", p.SerialBoundaryBefore)
	}
}

func TestPlanFragmentsWithoutCapabilities(t *testing.T) {
	// Mixed: typed-aware source + a fragment without capabilities
	// (e.g. a Record-mode helper or init fragment) + SerialOnly op.
	// The capability-less fragment is shape-agnostic; planner walks
	// over it.
	frags := []*lib.CodeFragment{
		frag("records", caps(lib.ShapeNone, lib.ShapeStream, false)),
		{Var: "helper"}, // no capabilities — pass-through
		frag("sorted", caps(lib.ShapeSeqTyped, lib.ShapeSeqTyped, true)),
	}
	p := MakePlan(frags)
	if !p.SourceParallel {
		t.Error("Stream source still implies parallel source choice")
	}
	if !p.SerialBoundaryBefore[2] {
		t.Errorf("expected boundary before sort even when intermediate fragment had no capabilities; got %v", p.SerialBoundaryBefore)
	}
}
