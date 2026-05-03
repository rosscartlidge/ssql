package lib

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// isTypedPipeline reports whether any fragment carries Phase 2 typed
// schema info — the marker we use to dispatch to the typed assembler.
// We tolerate a few stray non-typed fragments (e.g. an existing
// passthrough) by checking init/stmt/final fragments only.
func isTypedPipeline(fragments []*CodeFragment) bool {
	for _, f := range fragments {
		if f == nil {
			continue
		}
		if f.OutputTypedSchema != nil || f.InputTypedSchema != nil {
			return true
		}
	}
	return false
}

// assembleTypedFragments builds a complete Go program from typed-mode
// fragments. Differences vs the Record assembler:
//
//   - Imports github.com/rosscartlidge/ssql/v4/typed instead of v4
//   - Emits collected StructDefs at the top of the file
//   - Each stmt fragment's code is a complete typed.X[T] call already,
//     so we don't try to compose them via ssql.Chain (whose generic
//     parameters wouldn't match across stages)
//   - Errors out if any non-init/stmt/final fragment slips through
//   - Errors out if fragments are missing typed schema info partway
//     through (mixed-mode is unsupported)
func assembleTypedFragments(fragments []*CodeFragment) (string, error) {
	// Run the typed-mode planner first. It walks the fragment list and
	// returns:
	//   - which fragments need a Stream.Serial() boundary inserted
	//     before them (SerialOnly fragments downstream of a Stream
	//     producer)
	//   - whether the source should emit a parallel primitive (not
	//     yet wired — Phase A.2 deferred)
	//
	// Boundary fragments are spliced into the fragment list before the
	// renderer walks them, so the rest of the assembler logic doesn't
	// need to know.
	fragments = applyPlannerBoundaries(fragments)

	// Subprocess function bodies (process substitution sources) are
	// independent fragment chains. Plan each one too, so import
	// collection below sees the post-downgrade state. Without this,
	// a parallel-mode pipeline whose subprocess goes serial after
	// downgrade still pulls in `runtime` for the (no-longer-present)
	// ReadCSVParallel call and fails to compile.
	//
	// NewFuncFragment aggregates body imports onto the parent's
	// Imports field at construction time — that snapshot is now
	// stale, so re-aggregate after planning.
	for _, frag := range fragments {
		if frag.Type == "func" && len(frag.FuncBody) > 0 {
			frag.FuncBody = applyPlannerBoundaries(frag.FuncBody)
			seen := make(map[string]bool)
			frag.Imports = frag.Imports[:0]
			for _, body := range frag.FuncBody {
				for _, imp := range body.Imports {
					if imp == "" || seen[imp] {
						continue
					}
					seen[imp] = true
					frag.Imports = append(frag.Imports, imp)
				}
			}
		}
	}

	var initFragments []*CodeFragment
	var stmtFragments []*CodeFragment
	var finalFragments []*CodeFragment
	var funcFragments []*CodeFragment

	for _, frag := range fragments {
		switch frag.Type {
		case "init":
			initFragments = append(initFragments, frag)
		case "stmt":
			stmtFragments = append(stmtFragments, frag)
		case "final":
			finalFragments = append(finalFragments, frag)
		case "func":
			funcFragments = append(funcFragments, frag)
		}
	}

	if len(initFragments) == 0 {
		return "", fmt.Errorf("typed assembler: no source fragment (need 'from FILE.csv' as the first stage)")
	}

	// Strict-mode check: every init/stmt fragment must carry typed
	// schema info. If a fragment slipped through without it, that
	// command does not yet support typed-mode emission. Surface a
	// clear error rather than producing broken Go.
	for _, f := range initFragments {
		if f.OutputTypedSchema == nil {
			return "", fmt.Errorf("ssql generate go -typed: init fragment from %q has no typed schema (command does not yet support typed mode)", f.Command)
		}
	}
	for _, f := range stmtFragments {
		if f.OutputTypedSchema == nil {
			return "", fmt.Errorf("ssql generate go -typed: command %q does not yet support typed mode (Tier 2 or Tier 3); drop -typed or refactor the pipeline", f.Command)
		}
	}
	for _, f := range finalFragments {
		if f.InputTypedSchema == nil {
			return "", fmt.Errorf("ssql generate go -typed: sink %q does not yet support typed mode", f.Command)
		}
	}

	// Collect parameters and imports.
	allParams := collectParams(fragments)
	importSet := make(map[string]bool)
	importSet["github.com/rosscartlidge/ssql/v4/typed"] = true
	if len(allParams) > 0 {
		importSet["flag"] = true
	}
	if len(funcFragments) > 0 {
		importSet["iter"] = true
	}
	if len(finalFragments) == 0 {
		// Fall back to JSONL output via typed.WriteJSONLToWriter.
		importSet["fmt"] = true
		importSet["os"] = true
	}
	for _, frag := range fragments {
		for _, imp := range frag.Imports {
			if imp != "" {
				importSet[imp] = true
			}
		}
		for _, body := range frag.FuncBody {
			for _, imp := range body.Imports {
				if imp != "" {
					importSet[imp] = true
				}
			}
		}
	}
	imports := make([]string, 0, len(importSet))
	for imp := range importSet {
		imports = append(imports, imp)
	}
	sortImports(imports)

	// Collect and dedupe struct definitions.
	structDefs := collectTypedStructs(fragments)

	var code strings.Builder
	code.WriteString("package main\n\n")

	// Pipeline comment.
	var commands []string
	for _, frag := range fragments {
		if frag.Type == "func" {
			continue
		}
		if frag.Command != "" {
			commands = append(commands, frag.Command)
		}
	}
	if len(commands) > 0 {
		code.WriteString("/*\n")
		fmt.Fprintf(&code, "Generated by ssql %s (typed mode):\n\n", version.Version)
		code.WriteString("(export SSQLGO=typed\n")
		for _, cmd := range commands {
			code.WriteString(cmd + " |\n")
		}
		code.WriteString("ssql generate go)\n")
		code.WriteString("*/\n\n")
	}

	// Imports.
	if len(imports) > 0 {
		code.WriteString("import (\n")
		for _, imp := range imports {
			fmt.Fprintf(&code, "\t%q\n", imp)
		}
		code.WriteString(")\n\n")
	}

	// Struct definitions.
	for _, def := range structDefs {
		code.WriteString(def)
		if !strings.HasSuffix(def, "\n") {
			code.WriteString("\n")
		}
		code.WriteString("\n")
	}

	// Subprocess functions (process substitution).
	for _, ff := range funcFragments {
		code.WriteString(generateTypedSubprocessFunction(ff))
		code.WriteString("\n")
	}

	// Flag declarations.
	if len(allParams) > 0 {
		code.WriteString("var (\n")
		for _, p := range allParams {
			if p.Type == "int" {
				fmt.Fprintf(&code, "\t%s = flag.Int(%q, %s, %q)\n", p.VarName, p.Name, p.Default, p.Help)
			} else {
				fmt.Fprintf(&code, "\t%s = flag.String(%q, %q, %q)\n", p.VarName, p.Name, p.Default, p.Help)
			}
		}
		code.WriteString(")\n\n")
	}

	code.WriteString("func main() {\n")
	if len(allParams) > 0 {
		code.WriteString("\tflag.Parse()\n\n")
	}

	// init fragments — emit code as-is (they include `records := typed.ReadCSV[T]...`)
	for _, frag := range initFragments {
		code.WriteString("\t" + frag.Code + "\n")
	}

	// stmt fragments — each is a complete typed.X assignment chained from
	// the previous variable. Emit verbatim.
	for _, frag := range stmtFragments {
		code.WriteString("\t" + frag.Code + "\n")
	}

	// final fragments — sinks.
	if len(finalFragments) > 0 {
		for _, frag := range finalFragments {
			code.WriteString("\t" + frag.Code + "\n")
		}
	} else {
		// No sink — emit JSONL fallback to stdout.
		var outVar string
		if len(stmtFragments) > 0 {
			outVar = stmtFragments[len(stmtFragments)-1].Var
		} else {
			outVar = initFragments[0].Var
		}
		code.WriteString("\t// No sink in pipeline — emit JSONL to stdout\n")
		fmt.Fprintf(&code, "\tif err := typed.WriteJSONLToWriter(%s, os.Stdout); err != nil {\n", outVar)
		code.WriteString("\t\tfmt.Fprintf(os.Stderr, \"write: %v\\n\", err)\n")
		code.WriteString("\t\tos.Exit(1)\n")
		code.WriteString("\t}\n")
	}

	code.WriteString("}\n")
	return code.String(), nil
}

// collectTypedStructs gathers and dedupes StructDefs from every
// fragment, preserving insertion order so referenced types appear
// before referring ones (since each fragment lists its own definitions
// in the order they're declared).
func collectTypedStructs(fragments []*CodeFragment) []string {
	seen := make(map[string]bool)
	var out []string
	for _, frag := range fragments {
		for _, def := range frag.StructDefs {
			key := strings.TrimSpace(def)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, def)
		}
		for _, body := range frag.FuncBody {
			for _, def := range body.StructDefs {
				key := strings.TrimSpace(def)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, def)
			}
		}
	}
	return out
}

// generateTypedSubprocessFunction emits a `func name() iter.Seq[T]`
// for a process-sub source, mirroring generateSubprocessFunction but
// with typed-mode return types derived from the func body's
// OutputTypedSchema.
func generateTypedSubprocessFunction(funcFrag *CodeFragment) string {
	var b strings.Builder
	if funcFrag.Command != "" {
		fmt.Fprintf(&b, "// %s\n", funcFrag.Command)
	}

	// Find the body's output type from the last init/stmt fragment.
	var typeName string
	for i := len(funcFrag.FuncBody) - 1; i >= 0; i-- {
		f := funcFrag.FuncBody[i]
		if f.OutputTypedSchema != nil {
			typeName = f.OutputTypedSchema.TypeName
			break
		}
	}
	if typeName == "" {
		typeName = "any"
	}

	fmt.Fprintf(&b, "func %s() iter.Seq[%s] {\n", funcFrag.FuncName, typeName)

	// FuncBody has already been run through applyPlannerBoundaries
	// by assembleTypedFragments (so its Imports / Capabilities
	// reflect any source downgrade). Just iterate.
	var initFrags, stmtFrags []*CodeFragment
	for _, f := range funcFrag.FuncBody {
		switch f.Type {
		case "init":
			initFrags = append(initFrags, f)
		case "stmt":
			stmtFrags = append(stmtFrags, f)
		}
	}
	for _, f := range initFrags {
		b.WriteString("\t" + f.Code + "\n")
	}
	for _, f := range stmtFrags {
		b.WriteString("\t" + f.Code + "\n")
	}

	// Return the last variable. If the last fragment produces a
	// Stream (e.g. a parallel-form include or where in the
	// subprocess body), call .Serial() to fan in — the function
	// signature is iter.Seq[T].
	var lastVar string
	var lastFrag *CodeFragment
	if len(stmtFrags) > 0 {
		lastFrag = stmtFrags[len(stmtFrags)-1]
		lastVar = lastFrag.Var
	} else if len(initFrags) > 0 {
		lastFrag = initFrags[0]
		lastVar = lastFrag.Var
	}
	if lastVar != "" {
		needsSerial := lastFrag != nil && lastFrag.Capabilities != nil && lastFrag.Capabilities.Produces == ShapeStream
		if needsSerial {
			fmt.Fprintf(&b, "\treturn %s.Serial()\n", lastVar)
		} else {
			fmt.Fprintf(&b, "\treturn %s\n", lastVar)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// applyPlannerBoundaries runs the typed-mode planner over the
// fragment list and:
//
//  1. Downgrades source fragments to their serial alternative
//     (typed.ReadCSV instead of typed.ReadCSVParallel etc.) when
//     no downstream fragment needs Stream input. Avoids the
//     Serial()-channel-cost trap from the prototype's negative
//     result (parallel source + Serial() boundary is *slower*
//     than serial source on pipelines where parallelism reaches
//     no work).
//
//  2. Splices Stream.Serial() boundary fragments before each
//     SerialOnly fragment whose upstream produces ShapeStream.
//
// The returned slice is the fragment list as it should be
// rendered.
func applyPlannerBoundaries(fragments []*CodeFragment) []*CodeFragment {
	explain := os.Getenv("SSQL_EXPLAIN_PLAN") != ""
	plan := MakePlan(fragments)

	if explain {
		explainPlanInitial(fragments, plan)
	}

	// Phase 1 — source primitive selection.
	// If no downstream fragment needs Stream, swap each source's
	// parallel template for its serial alternative. Then walk all
	// pass-through fragments (where, group-by, sinks…) that have
	// alternatives and swap them too — their Stream-form code
	// won't compile against an iter.Seq upstream. Re-plan after
	// the swap so boundary detection sees the updated shapes.
	if !plan.SourceParallel {
		for _, f := range fragments {
			if f != nil && f.AltCodeIfSeq != "" {
				applySerialSourceAlternative(f)
				if explain {
					describe := f.Var
					if f.Command != "" {
						describe = f.Command
					}
					fmt.Fprintf(os.Stderr, "[plan] downgraded %s to serial alternative\n", describe)
				}
			}
		}
		plan = MakePlan(fragments)
	}

	// Phase 1b — per-fragment shape coercion. Walk fragments
	// tracking the running output shape. When a fragment with an
	// AltCodeIfSeq alternative receives input of a different shape
	// than its declared Accepts (e.g. parallel-form `count` after
	// a SerialOnly `sort` whose boundary turns the running shape
	// into iter.Seq[T]), swap that fragment to its serial form.
	// This complements the source-level downgrade in the previous
	// step — that only fires when reach=0 across the whole pipeline,
	// missing the case where some downstream fragment needs Stream
	// but a SerialOnly op upstream forces iter.Seq locally.
	anySwapped := false
	currShape := ShapeNone
	for _, f := range fragments {
		if f == nil {
			continue
		}
		c := f.Capabilities
		if c == nil {
			continue
		}
		if f.AltCodeIfSeq != "" && c.Accepts == ShapeStream && currShape == ShapeSeqTyped {
			applySerialSourceAlternative(f)
			anySwapped = true
			if explain {
				describe := f.Var
				if f.Command != "" {
					describe = f.Command
				}
				fmt.Fprintf(os.Stderr, "[plan] downgraded %s to serial alternative (upstream is iter.Seq[T])\n", describe)
			}
			c = f.Capabilities
		}
		// SerialOnly fragments will get an upstream Serial() boundary
		// (handled in the next phase). The boundary's Produces is
		// ShapeSeqTyped, so reflect that here.
		if c.SerialOnly && currShape == ShapeStream {
			currShape = ShapeSeqTyped
		}
		if c.Produces != ShapeNone {
			currShape = c.Produces
		}
	}
	plan = MakePlan(fragments)
	// Phase 1b may have removed the last Stream consumer (e.g.
	// `from | sort | count` — count's parallel form was the only
	// Accepts=ShapeStream, swap removed it). Re-run the
	// source-downgrade pass so reach=0 actually downgrades the
	// source instead of leaving a gratuitous ReadCSVParallel.
	if anySwapped && !plan.SourceParallel {
		for _, f := range fragments {
			if f != nil && f.AltCodeIfSeq != "" {
				applySerialSourceAlternative(f)
				if explain {
					describe := f.Var
					if f.Command != "" {
						describe = f.Command
					}
					fmt.Fprintf(os.Stderr, "[plan] downgraded %s to serial alternative\n", describe)
				}
			}
		}
		plan = MakePlan(fragments)
	}

	if explain {
		explainBoundaries(fragments, plan)
	}

	if len(plan.SerialBoundaryBefore) == 0 {
		return fragments
	}
	out := make([]*CodeFragment, 0, len(fragments)+len(plan.SerialBoundaryBefore))
	for i, f := range fragments {
		if plan.SerialBoundaryBefore[i] {
			// The fragment at index i is SerialOnly and its upstream
			// produces ShapeStream. Insert a Serial() boundary that
			// reads from f.Input and produces a new var.
			serialVar := f.Input + "Serial"
			boundary := &CodeFragment{
				Type:    "stmt",
				Var:     serialVar,
				Input:   f.Input,
				Code:    serialVar + " := " + f.Input + ".Serial()",
				Imports: []string{"github.com/rosscartlidge/ssql/v4/typed"},
				// Carry the upstream's typed schema through.
				InputTypedSchema:  f.InputTypedSchema,
				OutputTypedSchema: f.InputTypedSchema,
				Capabilities: &Capabilities{
					Accepts:  ShapeStream,
					Produces: ShapeSeqTyped,
				},
			}
			out = append(out, boundary)
			// Rewrite the SerialOnly fragment's code/input to consume
			// the serialised var instead of the original Stream var.
			// Word-boundary regex so we don't mangle identifiers
			// that contain the input name as a substring (e.g. if
			// the input is "records" we mustn't replace inside
			// "recordsSerial" itself).
			rewritten := *f
			rewritten.Code = replaceIdentifier(f.Code, f.Input, serialVar)
			rewritten.Input = serialVar
			out = append(out, &rewritten)
			continue
		}
		out = append(out, f)
	}
	return out
}

// replaceIdentifier replaces all word-boundary occurrences of `old` in
// `s` with `new`. Used by applyPlannerBoundaries to rewrite
// references to the upstream variable when inserting a Serial()
// boundary, without mangling identifiers that contain `old` as a
// substring (e.g. when old="records" we mustn't touch
// "recordsSerial" — both halves of the boundary share the prefix).
func replaceIdentifier(s, old, new string) string {
	if !strings.Contains(s, old) {
		return s
	}
	re := identRegexpFor(old)
	return re.ReplaceAllString(s, new)
}

var identRegexpCache = map[string]*regexp.Regexp{}

func identRegexpFor(name string) *regexp.Regexp {
	if re, ok := identRegexpCache[name]; ok {
		return re
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	identRegexpCache[name] = re
	return re
}

// applySerialSourceAlternative mutates a source fragment in place
// to use its serial alternative (typed.ReadCSV instead of
// typed.ReadCSVParallel etc.). Called by applyPlannerBoundaries
// when the parallelism-reach analysis determines no downstream
// fragment can consume Stream input.
//
// After the swap the fragment's IsStream flag is cleared (so any
// downstream code keying off that signal sees iter.Seq), and the
// alternative fields are nil'd so a subsequent planner pass
// (idempotency) doesn't re-apply the swap.
func applySerialSourceAlternative(f *CodeFragment) {
	f.Code = f.AltCodeIfSeq
	f.Imports = f.AltImportsIfSeq
	if f.AltCapabilitiesIfSeq != nil {
		f.Capabilities = f.AltCapabilitiesIfSeq
	}
	f.IsStream = false
	f.AltCodeIfSeq = ""
	f.AltImportsIfSeq = nil
	f.AltCapabilitiesIfSeq = nil
}

// explainPlanInitial prints the planner's initial decisions to
// stderr — what reach analysis returned, and what each source's
// chosen mode was. Called when SSQL_EXPLAIN_PLAN is set.
func explainPlanInitial(fragments []*CodeFragment, plan Plan) {
	fmt.Fprintln(os.Stderr, "[plan] typed-mode planner — initial decisions")
	if plan.SourceParallel {
		fmt.Fprintln(os.Stderr, "[plan]   source: parallel (Stream[T]) — at least one downstream accepts ShapeStream")
	} else {
		fmt.Fprintln(os.Stderr, "[plan]   source: serial (iter.Seq[T]) — parallelism reach = 0; downgrade source primitive")
	}
	for _, f := range fragments {
		if f == nil || f.Capabilities == nil {
			continue
		}
		describe := f.Var
		if f.Command != "" {
			describe = f.Command
		}
		fmt.Fprintf(os.Stderr, "[plan]   %s: accepts=%s, produces=%s%s\n",
			describe, f.Capabilities.Accepts, f.Capabilities.Produces, serialOnlyTag(f.Capabilities.SerialOnly))
	}
}

// explainBoundaries prints which fragments got a Stream.Serial()
// boundary inserted upstream. Called after the boundary detection
// pass, so the output reflects the post-downgrade plan.
func explainBoundaries(fragments []*CodeFragment, plan Plan) {
	if len(plan.SerialBoundaryBefore) == 0 {
		fmt.Fprintln(os.Stderr, "[plan]   (no Stream.Serial() boundaries needed)")
		return
	}
	for i := range plan.SerialBoundaryBefore {
		f := fragments[i]
		describe := f.Var
		if f.Command != "" {
			describe = f.Command
		}
		fmt.Fprintf(os.Stderr, "[plan]   inserted Stream.Serial() before %s (SerialOnly fragment with Stream upstream)\n", describe)
	}
}

func serialOnlyTag(serialOnly bool) string {
	if serialOnly {
		return ", SerialOnly=true"
	}
	return ""
}
