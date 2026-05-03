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
	//
	// Phase B pre-pass: Record-mode fragments (pivot, signal, etc.
	// — anything emitted from a non-typed codegen path) have no
	// OutputTypedSchema and no Capabilities. Tag them now so the
	// planner sees the typed→Record transition and inserts a
	// toRecord adapter upstream.
	tagRecordModeFragments(fragments)
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

	// Init sources must be typed (Phase B doesn't yet support
	// Record-mode sources flowing into typed downstream — that's
	// Phase C, the harder direction with explicit struct hints).
	for _, f := range initFragments {
		if f.OutputTypedSchema == nil {
			return "", fmt.Errorf("ssql generate go -typed: init fragment from %q has no typed schema (Record-mode sources are Phase C)", f.Command)
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
		// JSONL fallback. Always needs fmt + os. Additionally needs
		// `iter` and the public ssql package because the typed-shape
		// branch emits an inline `func() iter.Seq[ssql.Record] {...}`
		// converter and calls ssql.WriteJSONLWithInferredSchemaToWriter.
		importSet["fmt"] = true
		importSet["os"] = true
		importSet["iter"] = true
		importSet["github.com/rosscartlidge/ssql/v4"] = true
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
		// Phase B: Record-mode fragments (pivot, signal, etc.)
		// reference the ssql package but historically passed nil
		// imports because the record-mode assembler hardcoded the
		// import. The typed assembler doesn't, so add it whenever
		// any fragment touches Record shape.
		if frag.Capabilities != nil &&
			(frag.Capabilities.Produces == ShapeSeqRecord ||
				frag.Capabilities.Accepts == ShapeSeqRecord) {
			importSet["github.com/rosscartlidge/ssql/v4"] = true
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
		// No sink — emit JSONL fallback to stdout with a
		// `{"_schema":...}` header (matches the wire format the
		// rest of the ssql CLI produces).
		//
		// Two cases:
		//
		//   - Record-shape last fragment (mixed-mode pipeline ended
		//     on a Tier 3 command like pivot): use
		//     ssql.WriteJSONLWithInferredSchemaToWriter directly.
		//
		//   - Typed-shape last fragment: emit an inline
		//     typed→Record converter (same shape as the planner's
		//     toRecord boundary), then call the same helper. This
		//     gives us lowercase CSV-style field names (from the
		//     typed schema's Name field, not the Go struct's
		//     CamelCase) AND a schema header — matches the format
		//     baseline `ssql ... | ssql to jsonl` produces.
		//
		// Wire format is critical for `from ssh -remote`: without
		// a schema header on the remote's output, the local
		// readJSONSchemaAware reader inserts `_line_number` into
		// every record (a real bug fixed by v4.41.2 for users who
		// chain pipelines).
		var outVar string
		var lastFrag *CodeFragment
		if len(stmtFragments) > 0 {
			lastFrag = stmtFragments[len(stmtFragments)-1]
		} else {
			lastFrag = initFragments[0]
		}
		outVar = lastFrag.Var
		isRecord := lastFrag.Capabilities != nil && lastFrag.Capabilities.Produces == ShapeSeqRecord
		code.WriteString("\t// No sink in pipeline — emit JSONL to stdout (with schema header)\n")
		if isRecord {
			fmt.Fprintf(&code, "\tif err := ssql.WriteJSONLWithInferredSchemaToWriter(%s, os.Stdout); err != nil {\n", outVar)
		} else if lastFrag.OutputTypedSchema != nil {
			// Inline typed→Record converter, then schema-aware writer.
			// We don't go through the planner's toRecord boundary
			// because that would alter the fragment list — we just
			// emit equivalent code at the assembly layer.
			converter := buildInlineToRecordExpr(outVar, lastFrag.OutputTypedSchema)
			fmt.Fprintf(&code, "\tif err := ssql.WriteJSONLWithInferredSchemaToWriter(%s, os.Stdout); err != nil {\n", converter)
		} else {
			// No typed schema (shouldn't happen for typed
			// pipelines after Phase A) — fall back to plain
			// JSONL without schema. Better than crashing.
			fmt.Fprintf(&code, "\tif err := typed.WriteJSONLToWriter(%s, os.Stdout); err != nil {\n", outVar)
		}
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

	if len(plan.SerialBoundaryBefore) == 0 && len(plan.RecordBoundaryBefore) == 0 {
		return fragments
	}
	out := make([]*CodeFragment, 0, len(fragments)+len(plan.SerialBoundaryBefore)+len(plan.RecordBoundaryBefore))
	for i, f := range fragments {
		needsSerial := plan.SerialBoundaryBefore[i]
		needsRecord := plan.RecordBoundaryBefore[i]
		if !needsSerial && !needsRecord {
			out = append(out, f)
			continue
		}

		// Inputs to f get rewritten as we splice in adapters.
		// upstreamVar tracks what the next adapter (or f itself)
		// reads from; upstreamSchema follows it for the toRecord
		// adapter's schema source.
		upstreamVar := f.Input
		upstreamSchema := f.InputTypedSchema

		if needsSerial {
			serialVar := upstreamVar + "Serial"
			boundary := &CodeFragment{
				Type:    "stmt",
				Var:     serialVar,
				Input:   upstreamVar,
				Code:    serialVar + " := " + upstreamVar + ".Serial()",
				Imports: []string{"github.com/rosscartlidge/ssql/v4/typed"},
				// Carry the upstream's typed schema through.
				InputTypedSchema:  upstreamSchema,
				OutputTypedSchema: upstreamSchema,
				Capabilities: &Capabilities{
					Accepts:  ShapeStream,
					Produces: ShapeSeqTyped,
				},
			}
			out = append(out, boundary)
			upstreamVar = serialVar
		}

		if needsRecord {
			// Phase B: typed→Record adapter. Build a stmt that
			// converts the typed iter.Seq[T] into iter.Seq[ssql.Record]
			// using ssql.NewRecordFromSchema with a once-built schema.
			if upstreamSchema == nil {
				// Should never happen — planner only inserts a Record
				// boundary when upstream is typed. Defensive: skip.
				out = append(out, f)
				continue
			}
			recordVar := upstreamVar + "AsRecord"
			boundary := buildToRecordBoundary(recordVar, upstreamVar, upstreamSchema)
			out = append(out, boundary)
			upstreamVar = recordVar
		}

		// Rewrite f's Code/Input to consume the new upstream variable.
		// Word-boundary regex so we don't mangle identifiers that
		// contain the input name as a substring.
		rewritten := *f
		if f.Input != "" {
			rewritten.Code = replaceIdentifier(f.Code, f.Input, upstreamVar)
		}
		rewritten.Input = upstreamVar
		out = append(out, &rewritten)
	}
	return out
}

// tagRecordModeFragments walks the fragment list before planning
// and gives each Record-mode fragment (no typed schema, no
// Capabilities) the {Accepts: SeqRecord, Produces: SeqRecord}
// declaration so the planner sees the typed→Record transition
// and can insert the toRecord adapter upstream. Sinks get
// Produces: ShapeNone instead. Init fragments are NOT tagged —
// Record-mode sources flowing into typed are Phase C.
//
// Also propagates the upstream's OutputTypedSchema onto each
// Record fragment's InputTypedSchema field — buildToRecordBoundary
// needs it to know which struct fields to convert.
func tagRecordModeFragments(fragments []*CodeFragment) {
	var lastTypedSchema *TypedSchema
	for _, f := range fragments {
		if f == nil {
			continue
		}
		if f.Capabilities == nil {
			switch f.Type {
			case "stmt":
				if f.OutputTypedSchema == nil {
					f.Capabilities = &Capabilities{
						Accepts:  ShapeSeqRecord,
						Produces: ShapeSeqRecord,
					}
					if f.InputTypedSchema == nil {
						f.InputTypedSchema = lastTypedSchema
					}
				}
			case "final":
				if f.InputTypedSchema == nil {
					f.Capabilities = &Capabilities{
						Accepts:  ShapeSeqRecord,
						Produces: ShapeNone,
					}
					f.InputTypedSchema = lastTypedSchema
				}
			}
		}
		// Track the most recent typed-schema-producing fragment
		// for the next Record-mode fragment to inherit.
		if f.OutputTypedSchema != nil {
			lastTypedSchema = f.OutputTypedSchema
		}
	}
}

// buildInlineToRecordExpr returns a Go expression (a self-contained
// IIFE) that converts an iter.Seq[T] of typed structs into an
// iter.Seq[ssql.Record]. Used by the assembler's JSONL fallback to
// route typed-shape output through ssql.WriteJSONLWithInferredSchemaToWriter
// without going through the planner's full boundary insertion.
//
// The expression evaluates to an iter.Seq[ssql.Record] inline —
// callers can drop it into a function call argument:
//
//	ssql.WriteJSONLWithInferredSchemaToWriter( <expr>, os.Stdout )
//
// Schema is built once outside the per-row loop (schema-sharing
// rule), then NewRecordFromSchema fills in values per row.
func buildInlineToRecordExpr(inputVar string, schema *TypedSchema) string {
	fieldNames := make([]string, len(schema.Fields))
	valueExtractors := make([]string, len(schema.Fields))
	for i, f := range schema.Fields {
		fieldNames[i] = fmt.Sprintf("%q", f.Name)
		valueExtractors[i] = "v." + f.GoName
	}
	return fmt.Sprintf(`func() iter.Seq[ssql.Record] {
		schema := ssql.NewSchema([]string{%s})
		return func(yield func(ssql.Record) bool) {
			for v := range %s {
				r := ssql.NewRecordFromSchema(schema, []any{%s})
				if !yield(r) {
					return
				}
			}
		}
	}()`,
		strings.Join(fieldNames, ", "),
		inputVar,
		strings.Join(valueExtractors, ", "),
	)
}

// buildToRecordBoundary emits the typed→Record adapter fragment.
// Produces an inline closure that builds an ssql.Schema once
// (outside the per-row loop, per the schema-sharing rule) and
// converts each typed struct value to an ssql.Record via
// NewRecordFromSchema.
func buildToRecordBoundary(outputVar, inputVar string, schema *TypedSchema) *CodeFragment {
	// Build the schema's field-name list (input CSV names) and the
	// matching value-extractor list (`v.GoName`), in declaration order.
	fieldNames := make([]string, len(schema.Fields))
	valueExtractors := make([]string, len(schema.Fields))
	for i, f := range schema.Fields {
		fieldNames[i] = fmt.Sprintf("%q", f.Name)
		valueExtractors[i] = "v." + f.GoName
	}
	code := fmt.Sprintf(`%s := func() iter.Seq[ssql.Record] {
		schema := ssql.NewSchema([]string{%s})
		return func(yield func(ssql.Record) bool) {
			for v := range %s {
				r := ssql.NewRecordFromSchema(schema, []any{%s})
				if !yield(r) {
					return
				}
			}
		}
	}()`,
		outputVar,
		strings.Join(fieldNames, ", "),
		inputVar,
		strings.Join(valueExtractors, ", "),
	)
	return &CodeFragment{
		Type:    "stmt",
		Var:     outputVar,
		Input:   inputVar,
		Code:    code,
		Imports: []string{"iter", "github.com/rosscartlidge/ssql/v4"},
		// No upstream typed schema beyond this point — fragment
		// produces Record-shaped data.
		InputTypedSchema: schema,
		Capabilities: &Capabilities{
			Accepts:  ShapeSeqTyped,
			Produces: ShapeSeqRecord,
		},
	}
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

// explainBoundaries prints which fragments got Stream.Serial() or
// typed→Record boundaries inserted upstream. Called after the
// boundary detection pass, so the output reflects the post-
// downgrade plan.
func explainBoundaries(fragments []*CodeFragment, plan Plan) {
	if len(plan.SerialBoundaryBefore) == 0 && len(plan.RecordBoundaryBefore) == 0 {
		fmt.Fprintln(os.Stderr, "[plan]   (no boundaries needed)")
		return
	}
	for i := range plan.SerialBoundaryBefore {
		f := fragments[i]
		describe := f.Var
		if f.Command != "" {
			describe = f.Command
		}
		fmt.Fprintf(os.Stderr, "[plan]   inserted Stream.Serial() before %s (SerialOnly or Record fragment with Stream upstream)\n", describe)
	}
	for i := range plan.RecordBoundaryBefore {
		f := fragments[i]
		describe := f.Var
		if f.Command != "" {
			describe = f.Command
		}
		fmt.Fprintf(os.Stderr, "[plan]   inserted typed→Record adapter before %s (Record fragment with typed upstream)\n", describe)
	}
}

func serialOnlyTag(serialOnly bool) string {
	if serialOnly {
		return ", SerialOnly=true"
	}
	return ""
}
