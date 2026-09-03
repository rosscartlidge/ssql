package commands

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Tests for pipelineCmdFor — the optimiser's fragment view (DFC123
// slice 2): prefer the structured Op descriptor, fall back to
// Command-string parsing, treat continuation fragments as removed.

func TestPipelineCmdForPrefersOp(t *testing.T) {
	// Command is deliberate garbage: if the Op path is taken, the
	// garbage is never parsed. This is the preference proof.
	frag := &lib.CodeFragment{
		Command: "ssql GARBAGE not a real command",
		Op:      &lib.Op{Kind: "sort", Argv: []string{"-desc", "salary"}},
	}
	cmd := pipelineCmdFor(frag)
	if cmd.Kind != "sort" || !cmd.SortDesc || cmd.SortField != "salary" {
		t.Errorf("Op path not taken: kind=%q desc=%v field=%q", cmd.Kind, cmd.SortDesc, cmd.SortField)
	}
}

func TestPipelineCmdForFallback(t *testing.T) {
	// No Op (fragment from an older ssql, e.g. across an SSH
	// boundary): the Command string still parses — version skew
	// degrades to the old behavior.
	frag := &lib.CodeFragment{Command: "ssql sort -desc salary"}
	cmd := pipelineCmdFor(frag)
	if cmd.Kind != "sort" || !cmd.SortDesc || cmd.SortField != "salary" {
		t.Errorf("fallback parse wrong: kind=%q desc=%v field=%q", cmd.Kind, cmd.SortDesc, cmd.SortField)
	}
}

func TestPipelineCmdForContinuationFragment(t *testing.T) {
	// A command's second fragment (group-by aggregation) has no
	// Command of its own and must not become a pipeline stage — even
	// if an Op were present.
	frag := &lib.CodeFragment{Command: "", Op: &lib.Op{Kind: "group-by"}}
	if cmd := pipelineCmdFor(frag); !cmd.Removed {
		t.Error("continuation fragment must be Removed")
	}
}

func TestOpArgvSurvivesQuoting(t *testing.T) {
	// THE bug the Op path fixes (found while building the slice): the
	// Command-string fallback re-tokenizes shell-quoted text with a
	// tokenizer that cannot represent an embedded single quote, so
	// `where -if name eq O'Brien` came back as `"OBrien"` — a
	// DIFFERENT VALUE, silently. Op.Argv is the process's own argv:
	// lossless.
	frag := &lib.CodeFragment{
		Command: `ssql where -if name eq 'O'\''Brien'`,
		Op:      &lib.Op{Kind: "where", Argv: []string{"-if", "name", "eq", "O'Brien"}},
	}
	cmd := pipelineCmdFor(frag)
	if len(cmd.RawArgs) != 4 || cmd.RawArgs[3] != "O'Brien" {
		t.Fatalf("Op argv not preserved: %q", cmd.RawArgs)
	}
	// And the fallback really does mangle it — this assertion pins
	// WHY the Op path exists; if the tokenizer is ever fixed, this
	// documents the change.
	fb := pipelineCmdFor(&lib.CodeFragment{Command: frag.Command})
	if len(fb.RawArgs) == 4 && fb.RawArgs[3] == "O'Brien" {
		t.Log("Command-string tokenizer now round-trips embedded quotes — fallback caveat in op.go can be softened")
	}
	if rendered := renderCmd(cmd); !strings.Contains(rendered, `'O'\''Brien'`) {
		t.Errorf("render did not shell-quote the value: %q", rendered)
	}
}

// Dead-sort elimination (DFC123 §7): a sort is dead iff its order
// reaches an order-reset stage across only order-transparent stages.
// The liveness rows are the miscompile shapes — limit/window/tee
// CONSUME order, so the sort must survive.
func TestRuleSortElimination(t *testing.T) {
	cases := []struct {
		name     string
		pipeline []string // command strings, parsed like fragments
		removed  []bool   // expected Removed per stage after the rule
	}{
		{"adjacent sorts", []string{"ssql sort pop", "ssql sort id"}, []bool{true, false}},
		{"across transparent", []string{"ssql sort pop", "ssql where -if id gt 3", "ssql include id", "ssql sort id"}, []bool{true, false, false, false}},
		{"live past limit", []string{"ssql sort pop", "ssql limit 5", "ssql sort id"}, []bool{false, false, false}},
		{"live past limit across transparent", []string{"ssql sort pop", "ssql where -if id gt 3", "ssql limit 5", "ssql sort id"}, []bool{false, false, false, false}},
		{"before group-by (legacy shape)", []string{"ssql sort pop", "ssql group-by dept -count n"}, []bool{true, false}},
		{"before group-by across where", []string{"ssql sort pop", "ssql where -if id gt 3", "ssql group-by dept -count n"}, []bool{true, false, false}},
		{"before resample across update", []string{"ssql sort pop", "ssql update -set x 1", "ssql resample -time t -every 5s -value v"}, []bool{true, false, false}},
		{"live past window", []string{"ssql sort pop", "ssql window -avg pop m -over 3", "ssql sort id"}, []bool{false, false, false}},
		{"live past tee", []string{"ssql sort pop", "ssql tee snap.jsonl", "ssql sort id"}, []bool{false, false, false}},
		{"live at sink", []string{"ssql sort pop", "ssql to csv"}, []bool{false, false}},
		{"chain of three", []string{"ssql sort a", "ssql sort b", "ssql sort c"}, []bool{true, true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var cmds []*pipelineCmd
			for _, p := range c.pipeline {
				cmds = append(cmds, parsePipelineCmd(p))
			}
			ruleSortElimination(cmds)
			for i, want := range c.removed {
				if cmds[i].Removed != want {
					t.Errorf("stage %d (%s): removed=%v, want %v", i, c.pipeline[i], cmds[i].Removed, want)
				}
			}
		})
	}
}
