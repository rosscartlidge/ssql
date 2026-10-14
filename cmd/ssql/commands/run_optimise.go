package commands

import (
	"bytes"
	"context"

	cf "github.com/rosscartlidge/autocli/v4"
)

// docFromCmds turns the optimiser's commands into a pipeline document:
// the words renderCmd writes as shell text, as argument lists, an inner
// pipeline as a nested list. Nothing passes through a shell.
func docFromCmds(cmds []*pipelineCmd) []docStage {
	var doc []docStage
	for _, cmd := range cmds {
		if cmd.Removed {
			continue
		}
		var st docStage
		for i, w := range cmdWords(cmd) {
			if i == 0 {
				continue // "ssql": the program, not an argument
			}
			if w.Inner != nil {
				st = append(st, docArg{Nested: docFromCmds(w.Inner)})
				continue
			}
			st = append(st, docArg{Text: w.Text})
		}
		doc = append(doc, st)
	}
	return doc
}

// optimisePipelineDoc applies the optimiser's rules — the rewrites
// `generate ssql` prints — to a document before run executes it. The
// stages run once under SSQL_MODE=record to describe themselves (no
// data is read), the rules rewrite the fragments, and the result is a
// document again, checked against the command grammar like the one the
// user gave. The rules are equivalences: a where on a catalog or hive
// column becomes -if on the from stage, so the files that cannot match
// are never opened (3.1 s → 0.7 s over seven partition files, measured
// 2026-10-14); sort+limit becomes top; a dead sort goes. When no rule
// applies, or the stages cannot describe themselves, the document runs
// as written and reason says why (run -explain shows it).
func optimisePipelineDoc(ctx context.Context, root *cf.Command, self string, stages []docStage) (out []docStage, rules []ruleApplication, reason string) {
	var frags, errb bytes.Buffer
	if err := runPipelineDoc(ctx, root, self, stages, nil, &frags, &errb, "SSQL_MODE=record"); err != nil {
		return stages, nil, "the stages could not describe themselves: " + firstStderrLine(errb.Bytes(), err)
	}
	cmds, rules, err := optimizePipelineCmds(&frags)
	if err != nil {
		if err == errEmptyResult {
			return stages, rules, "contradictory predicates — the pipeline produces no rows"
		}
		return stages, nil, "optimiser: " + err.Error()
	}
	if len(rules) == 0 {
		return stages, nil, ""
	}
	out = docFromCmds(cmds)
	if err := checkPipelineDoc(root, out, ""); err != nil {
		return stages, rules, "the rewritten pipeline did not validate: " + err.Error()
	}
	return out, rules, ""
}
