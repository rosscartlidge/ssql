package lib

import "testing"

// fragT is a tiny constructor for planner tests. Sets Capabilities and Var.
func fragT(name string, c *Capabilities) *CodeFragment {
	return &CodeFragment{Var: name, Capabilities: c}
}

func capsT(accepts, produces Shape, serialOnly bool) *Capabilities {
	return &Capabilities{Accepts: accepts, Produces: produces, SerialOnly: serialOnly}
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
	frags := []*CodeFragment{
		fragT("records", capsT(ShapeNone, ShapeSeqTyped, false)),
		fragT("sorted", capsT(ShapeSeqTyped, ShapeSeqTyped, true)),
		fragT("output", capsT(ShapeSeqTyped, ShapeNone, true)),
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
	frags := []*CodeFragment{
		fragT("records", capsT(ShapeNone, ShapeStream, false)),
		fragT("filtered", capsT(ShapeStream, ShapeStream, false)),
		fragT("grouped", capsT(ShapeStream, ShapeSeqTyped, false)),
		fragT("output", capsT(ShapeSeqTyped, ShapeNone, false)),
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
	frags := []*CodeFragment{
		fragT("records", capsT(ShapeNone, ShapeStream, false)),
		fragT("filtered", capsT(ShapeStream, ShapeStream, false)),
		fragT("sorted", capsT(ShapeSeqTyped, ShapeSeqTyped, true)),
		fragT("output", capsT(ShapeSeqTyped, ShapeNone, false)),
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
	frags := []*CodeFragment{
		fragT("records", capsT(ShapeNone, ShapeStream, false)),
		fragT("filtered", capsT(ShapeStream, ShapeStream, false)),
		fragT("sorted", capsT(ShapeSeqTyped, ShapeSeqTyped, true)),
		fragT("distinct", capsT(ShapeSeqTyped, ShapeSeqTyped, true)),
		fragT("output", capsT(ShapeSeqTyped, ShapeNone, false)),
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
	// Source produces Stream, then a pass-through fragment with no
	// capabilities (e.g. a Record-mode helper or init fragment), then
	// SerialOnly sort. Reach analysis sees no downstream `Accepts==
	// ShapeStream`, so SourceParallel=false (downgrade is needed).
	//
	// The boundary detection in MakePlan still sees the source's
	// declared Produces=ShapeStream and the SerialOnly sort, so it
	// returns a boundary. In the higher-level assembler flow the
	// source gets swapped first and MakePlan is re-run — at which
	// point the boundary disappears. This test only checks the
	// raw output of MakePlan, so we just verify SourceParallel
	// here. Boundary inversion is exercised by the assembler-level
	// integration tests.
	frags := []*CodeFragment{
		fragT("records", capsT(ShapeNone, ShapeStream, false)),
		{Var: "helper"}, // no capabilities — pass-through
		fragT("sorted", capsT(ShapeSeqTyped, ShapeSeqTyped, true)),
	}
	p := MakePlan(frags)
	if p.SourceParallel {
		t.Error("no downstream wants Stream → SourceParallel should be false (parallelism-reach=0)")
	}
}

func TestPlanReachWithStreamConsumer(t *testing.T) {
	// Source produces Stream, then a fragment that explicitly
	// Accepts ShapeStream (e.g. parallel-friendly where), then
	// SerialOnly sort.
	frags := []*CodeFragment{
		fragT("records", capsT(ShapeNone, ShapeStream, false)),
		fragT("filtered", capsT(ShapeStream, ShapeStream, false)),
		fragT("sorted", capsT(ShapeSeqTyped, ShapeSeqTyped, true)),
	}
	p := MakePlan(frags)
	if !p.SourceParallel {
		t.Error("a downstream Accepts=ShapeStream → SourceParallel should be true")
	}
	if !p.SerialBoundaryBefore[2] {
		t.Errorf("expected boundary before sort (idx 2); got %v", p.SerialBoundaryBefore)
	}
}
