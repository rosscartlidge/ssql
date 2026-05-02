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
	frags := []*CodeFragment{
		fragT("records", capsT(ShapeNone, ShapeStream, false)),
		{Var: "helper"}, // no capabilities — pass-through
		fragT("sorted", capsT(ShapeSeqTyped, ShapeSeqTyped, true)),
	}
	p := MakePlan(frags)
	if !p.SourceParallel {
		t.Error("Stream source still implies parallel source choice")
	}
	if !p.SerialBoundaryBefore[2] {
		t.Errorf("expected boundary before sort even when intermediate fragment had no capabilities; got %v", p.SerialBoundaryBefore)
	}
}
