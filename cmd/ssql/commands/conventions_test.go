package commands

import (
	"bytes"
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// TestConventionsReference checks the overview names every category and carries
// the headline cross-cutting rules.
func TestConventionsReference(t *testing.T) {
	for _, want := range []string{
		"Evaluation", "Data model", "Pipeline", "Code generation",
		"ORIGINAL row", // the update-snapshot rule (the proof-of-need)
		"_schema",      // the JSONL header convention
		"SSQL_MODE",    // codegen modes
	} {
		if !strings.Contains(ConventionsReference, want) {
			t.Errorf("ConventionsReference missing %q", want)
		}
	}
}

// TestConventionsCategoryDispatch confirms every category the completer offers
// resolves to non-trivial detail (no silent unknown-category), and that the
// headline update semantics live in the evaluation category.
func TestConventionsCategoryDispatch(t *testing.T) {
	categories := []string{"evaluation", "data", "pipeline", "codegen"}
	for _, c := range categories {
		var buf bytes.Buffer
		ctx := &cf.Context{}
		ctx.SetStdout(&buf)
		if err := printConventionCategory(ctx, c); err != nil {
			t.Fatalf("printConventionCategory(%q): %v", c, err)
		}
		out := buf.String()
		if strings.Contains(out, "Unknown category") || len(strings.TrimSpace(out)) < 40 {
			t.Errorf("category %q produced no real detail:\n%s", c, out)
		}
	}

	// The update-snapshot rule must be in the evaluation category.
	var buf bytes.Buffer
	ctx := &cf.Context{}
	ctx.SetStdout(&buf)
	_ = printConventionCategory(ctx, "evaluation")
	for _, want := range []string{"ORIGINAL row", "UPDATE", "u = 101"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("evaluation detail missing %q", want)
		}
	}

	// An unknown category fails honestly.
	var bad bytes.Buffer
	bctx := &cf.Context{}
	bctx.SetStdout(&bad)
	_ = printConventionCategory(bctx, "nope")
	if !strings.Contains(bad.String(), "Unknown category") {
		t.Errorf("unknown category should say so, got:\n%s", bad.String())
	}
}
