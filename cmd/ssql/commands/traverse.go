package commands

import (
	"fmt"
	"iter"
	"os"
	"slices"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Graph traversal (DFC161): the edges are the stream, the starts are
// flags (-start values, a -start-file list), and the traversal is a
// join run to a fixpoint — each round joins the routes the last round
// found against the edges again. It was a mode of join (`join -follow`)
// in v4.135, its own command with the seeds on stdin in v4.136 (§10),
// and took the edges as its stream on 2026-11-03 (§11): the edge list
// is the big table and the starts a handful of values.
//
//	ssql from flights.csv | ssql traverse -from src -to dst -start SYD -depth hops
//	ssql from flights.csv | ssql traverse -from src -to dst -start SYD -lowest-sum mins total
//	ssql from flights.csv | ssql traverse -from src -to dst -start-file starts.csv -all-paths -path route
//
// Each output row is a start row's columns (SRC the start), DST the
// node reached, then the results.
//
// Exec and record codegen call ssql.Follow; typed emits
// typed.Follow[Parallel]; generate sql emits a recursive CTE (the keyed
// modes on DuckDB only).

// RegisterTraverse registers the traverse subcommand.
func RegisterTraverse(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("traverse").
		Description("Graph traversal: the input rows are the edges; from each start node, follow them until no new route is found — a join repeated to a fixpoint (DFC161)").
		Example("ssql from flights.csv | ssql traverse -from src -to dst -start SYD -depth hops", "Every city reachable from SYD, by the fewest hops (the default)").
		Example("ssql from flights.csv | ssql traverse -from src -to dst -start SYD -start PER -lowest-sum mins total -path route", "Each city's quickest route from SYD and from PER by total mins (Dijkstra)").
		Example("ssql from flights.csv | ssql traverse -from src -to dst -start-file <(ssql from cities.csv | ssql rename -as city src) -all-paths -sum mins total", "Every route that never revisits a city, from every city in a list, its other columns carried through").

		Flag("-from").
			String().
			FieldsFromFlag("").
			Global().
			Required().
			Help("The edge's source field; a start is a value of it").
			Done().

		Flag("-to").
			String().
			FieldsFromFlag("").
			Global().
			Required().
			Help("The edge's target field; the node reached is written to it").
			Done().

		Flag("-start").
			String().
			Completer(startValueCompleter{}).
			Accumulate().
			Global().
			Help("A start node, a value of the -from field (repeatable)").
			Done().

		Flag("-start-file").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl,csv,tsv}"}).
			Global().
			Help("A list of starts: CSV/TSV/JSON by extension, a _schema JSONL, or <(ssql …); its start column is named as -from, its other columns are carried through").
			Done().

		Flag("-fewest-hops").
			Bool().
			Global().
			Help("Each node once per start, by the fewest hops (the default)").
			Done().

		Flag("-lowest-sum").
			Arg("field").
				FieldsFromFlag("").
				Done().
			Arg("result").
				Completer(&cf.NoCompleter{Hint: "<result-field>"}).
				Done().
			Global().
			Help("Each node once per start, by the lowest total of edge FIELD, written to RESULT (Dijkstra; weights must be >= 0)").
			Done().

		Flag("-all-paths").
			Bool().
			Global().
			Help("Every simple path (no node twice), every prefix its own row; exponential on a dense graph").
			Done().

		Flag("-sum").
			Arg("field").
				FieldsFromFlag("").
				Done().
			Arg("result").
				Completer(&cf.NoCompleter{Hint: "<result-field>"}).
				Done().
			Accumulate().
			Global().
			Help("The running total of edge FIELD along the route, written to RESULT").
			Done().

		Flag("-depth").
			String().
			Completer(&cf.NoCompleter{Hint: "<result-field>"}).
			Global().
			Help("The hop count, 0 on the start's own row").
			Done().

		Flag("-path").
			String().
			Completer(&cf.NoCompleter{Hint: "<result-field>"}).
			Global().
			Help("The nodes visited, start first, as a list").
			Done().

		Flag("-max-depth").
			Int().
			Global().
			Help("Stop at this many hops").
			Done().

		Flag("-both").
			Bool().
			Global().
			Help("The graph is undirected; every edge is usable either way").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			f, err := parseFollowSpec(ctx)
			if err != nil {
				return err
			}
			generate, _ := ctx.GlobalFlags["-generate"].(bool)
			if shouldGenerate(generate) {
				return generateTraverseCode(f)
			}
			edges := lib.ReadJSONLWithSchema(ctx.Stdin())
			var startRecs iter.Seq[ssql.Record]
			var startSchema *lib.Schema
			if f.startFile != "" {
				startRecs, startSchema, err = readAuxInput(f.startFile)
				if err != nil {
					return err
				}
				// Bare .jsonl without a schema header must fail LOUDLY —
				// a headerless file silently loses field information.
				if startSchema == nil {
					return fmt.Errorf("traverse: start file %s has no schema header — pipe through ssql first: -start-file <(ssql from jsonl %s)", f.startFile, f.startFile)
				}
			}
			return execTraverse(ctx, edges.Records, edges.Schema, startRecs, startSchema, f)
		}).
		Done()
	return cmd
}

// followSpec is traverse's flags, parsed once by the handler.
type followSpec struct {
	src, dst  string
	starts    []string // -start, as typed
	startFile string
	mode           ssql.FollowMode
	lowest         ssql.FollowSum
	sums           []ssql.FollowSum
	depth, path    string
	maxDepth       int
	both           bool
}

// followPairs reads an accumulated two-argument flag (-sum FIELD NAME).
func followPairs(raw any) []ssql.FollowSum {
	var out []ssql.FollowSum
	add := func(v any) {
		if m, ok := v.(map[string]any); ok {
			f, _ := m["field"].(string)
			n, _ := m["result"].(string)
			out = append(out, ssql.FollowSum{Field: f, Name: n})
		}
	}
	if arr, ok := raw.([]any); ok {
		for _, v := range arr {
			add(v)
		}
	} else {
		add(raw)
	}
	return out
}

// parseFollowSpec reads traverse's flags from the parsed context.
func parseFollowSpec(ctx *cf.Context) (followSpec, error) {
	var f followSpec
	g := ctx.GlobalFlags
	f.src, _ = g["-from"].(string)
	f.dst, _ = g["-to"].(string)
	switch v := g["-start"].(type) {
	case string:
		f.starts = []string{v}
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				f.starts = append(f.starts, s)
			}
		}
	}
	f.startFile, _ = g["-start-file"].(string)
	f.depth, _ = g["-depth"].(string)
	f.path, _ = g["-path"].(string)
	f.maxDepth, _ = g["-max-depth"].(int)
	f.both, _ = g["-both"].(bool)
	f.sums = followPairs(g["-sum"])
	fewest, _ := g["-fewest-hops"].(bool)
	all, _ := g["-all-paths"].(bool)
	lowest := followPairs(g["-lowest-sum"])
	modes := 0
	for _, on := range []bool{fewest, all, len(lowest) > 0} {
		if on {
			modes++
		}
	}
	if f.src == "" || f.dst == "" {
		return f, fmt.Errorf("traverse needs -from SRC and -to DST: the edge's source and target fields")
	}
	if len(f.starts) == 0 && f.startFile == "" {
		return f, fmt.Errorf("traverse needs a start: -start NODE (repeatable) or -start-file FILE, a list whose %s column holds the starts", f.src)
	}
	if modes > 1 {
		return f, fmt.Errorf("traverse: -fewest-hops, -lowest-sum and -all-paths are alternatives — give one (-fewest-hops is the default)")
	}
	switch {
	case all:
		f.mode = ssql.FollowAllPaths
	case len(lowest) > 0:
		f.mode, f.lowest = ssql.FollowLowestSum, lowest[0]
	}
	for _, s := range append(append([]ssql.FollowSum{}, f.sums...), lowest...) {
		if s.Field == "" || s.Name == "" {
			return f, fmt.Errorf("traverse: -sum and -lowest-sum need FIELD NAME")
		}
	}
	if f.maxDepth < 0 {
		return f, fmt.Errorf("traverse: -max-depth %d must be positive", f.maxDepth)
	}
	if f.maxDepth > 0 && f.mode == ssql.FollowLowestSum {
		return f, fmt.Errorf("traverse: -max-depth is not defined with -lowest-sum (the cheapest route within N hops is not what the search finds; DFC161 §11)")
	}
	return f, nil
}

// followAccNames are the result columns in output order (DFC161 §10).
func (f followSpec) accNames() []string {
	var names []string
	if f.depth != "" {
		names = append(names, f.depth)
	}
	for _, s := range f.sums {
		names = append(names, s.Name)
	}
	if f.mode == ssql.FollowLowestSum {
		names = append(names, f.lowest.Name)
	}
	if f.path != "" {
		names = append(names, f.path)
	}
	return names
}

// weights are the edge fields the accumulators read.
func (f followSpec) weights() []ssql.FollowSum {
	w := append([]ssql.FollowSum{}, f.sums...)
	if f.mode == ssql.FollowLowestSum {
		w = append(w, f.lowest)
	}
	return w
}

// followCheck validates the traversal against what the field lists
// allow: the edge fields present, the weights numeric, the target not
// the source, a start file's start column named as -from and none named
// as -to, and no result column colliding with SRC, DST, a start column
// or another result. nil field lists are unknown and skipped.
func followCheck(edgeFields []string, edgeType func(string) string, startFields []string, f followSpec) (ssql.FollowConfig, error) {
	var cfg ssql.FollowConfig
	if f.src == f.dst {
		return cfg, fmt.Errorf("traverse -to %s: the edge's target must be a different field from its source (-from %s)", f.dst, f.src)
	}
	if edgeFields != nil {
		for _, ef := range append([]string{f.src, f.dst}, followFields(f.weights())...) {
			if !fieldListHasOrPath(edgeFields, ef) {
				return cfg, fmt.Errorf("traverse: edge field %q not found (available: %s)", ef, strings.Join(edgeFields, ", "))
			}
		}
		for _, w := range f.weights() {
			// ssql:domain numeric
			switch base, _ := lib.ParseWireType(edgeType(w.Field)); base {
			case "", lib.TypeInt, lib.TypeInt128, lib.TypeFloat, lib.TypeDecimal:
			default:
				return cfg, fmt.Errorf("traverse: edge field %q is %s; a sum needs a number", w.Field, base)
			}
		}
	}
	if startFields != nil {
		if !slices.Contains(startFields, f.src) {
			return cfg, fmt.Errorf("traverse -start-file %s: no %s column — the starts are read from the column -from names (available: %s); rename it in a procsub: -start-file <(ssql from FILE | ssql rename -as COLUMN %s)", f.startFile, f.src, strings.Join(startFields, ", "), f.src)
		}
		if slices.Contains(startFields, f.dst) {
			return cfg, fmt.Errorf("traverse -start-file %s: column %q is where -to writes the node reached — drop or rename it", f.startFile, f.dst)
		}
	}
	seen := map[string]bool{f.src: true, f.dst: true}
	for _, n := range f.accNames() {
		if seen[n] || slices.Contains(startFields, n) {
			return cfg, fmt.Errorf("traverse: result column %q is already a column (-from %s, -to %s, start columns: %s)", n, f.src, f.dst, strings.Join(startFields, ", "))
		}
		seen[n] = true
	}
	return ssql.FollowConfig{
		NodeField: f.dst, StartField: f.src, SrcField: f.src, DstField: f.dst,
		Mode: f.mode, Lowest: f.lowest, Sums: f.sums,
		DepthField: f.depth, PathField: f.path, MaxDepth: f.maxDepth, Both: f.both,
	}, nil
}

// traverseStartValues converts the -start texts to the type of the
// edges' SRC column (DFC161 §11): text stays text, an int or float
// column needs one, and an unknown type is inferred as a CSV field is.
// A value that does not convert is an error, never a start that matches
// nothing. Every lane converts through here.
func traverseStartValues(starts []string, field, typ string) ([]any, error) {
	out := make([]any, 0, len(starts))
	for _, t := range starts {
		var v any
		switch base, _ := lib.ParseWireType(typ); base {
		case "":
			v = parseValue(t)
		case lib.TypeString:
			v = t
		case lib.TypeInt:
			n, err := strconv.ParseInt(t, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("traverse -start %s: %s is an int column, and %q is not an int", t, field, t)
			}
			v = n
		case lib.TypeFloat:
			x, err := strconv.ParseFloat(t, 64)
			if err != nil {
				return nil, fmt.Errorf("traverse -start %s: %s is a float column, and %q is not a number", t, field, t)
			}
			v = x
		default:
			return nil, fmt.Errorf("traverse -start %s: %s is a %s column; give its starts with -start-file", t, field, base)
		}
		out = append(out, v)
	}
	return out, nil
}

// goAnyLiteral is the start values as a Go []any literal ("nil" when
// there are none) for record codegen.
func goAnyLiteral(values []any) (string, error) {
	if len(values) == 0 {
		return "nil", nil
	}
	parts := make([]string, len(values))
	for i, v := range values {
		switch x := v.(type) {
		case string:
			parts[i] = strconv.Quote(x)
		case int64:
			parts[i] = fmt.Sprintf("int64(%d)", x)
		case float64:
			parts[i] = fmt.Sprintf("float64(%s)", strconv.FormatFloat(x, 'g', -1, 64))
		case bool:
			parts[i] = strconv.FormatBool(x)
		default:
			return "", fmt.Errorf("traverse -start %v: a %T start has no literal form; give it with -start-file", v, v)
		}
	}
	return "[]any{" + strings.Join(parts, ", ") + "}", nil
}

func followFields(sums []ssql.FollowSum) []string {
	var fs []string
	for _, s := range sums {
		fs = append(fs, s.Field)
	}
	return fs
}

// followSumType is the wire type of a total over an edge field: an
// integer sum is an int128 (DFC155 C), a decimal keeps its scale.
func followSumType(edgeType string) string {
	// ssql:domain exact
	switch base, _ := lib.ParseWireType(edgeType); base {
	case lib.TypeInt, lib.TypeInt128:
		return lib.TypeInt128
	case lib.TypeDecimal:
		return edgeType
	}
	return lib.TypeFloat
}

// execTraverse runs the traversal in the interpreter.
func execTraverse(ctx *cf.Context, edges iter.Seq[ssql.Record], edgeSchema *lib.Schema, startRecs iter.Seq[ssql.Record], startSchema *lib.Schema, f followSpec) error {
	var edgeFields, startFields []string
	edgeType := noType
	if edgeSchema != nil {
		edgeFields, edgeType = edgeSchema.Fields, edgeSchema.TypeOf
	}
	if startSchema != nil {
		startFields = startSchema.Fields
	}
	cfg, err := followCheck(edgeFields, edgeType, startFields, f)
	if err != nil {
		return err
	}
	values, err := traverseStartValues(f.starts, f.src, edgeType(f.src))
	if err != nil {
		return err
	}
	routes := ssql.FollowEdges(ssql.FollowStarts(f.src, values, startRecs), cfg)(edges)

	// The start columns (a -start row has only SRC), DST, the results.
	out := lib.NewSchema()
	if startSchema != nil {
		for _, fld := range startSchema.Fields {
			out.AddField(fld, startSchema.TypeOf(fld))
		}
	}
	if !out.HasField(f.src) {
		out.AddField(f.src, edgeType(f.src))
	}
	out.AddField(f.dst, edgeType(f.dst))
	if f.depth != "" {
		out.AddField(f.depth, lib.TypeInt)
	}
	for _, w := range f.weights() {
		out.AddField(w.Name, followSumType(edgeType(w.Field)))
	}
	if f.path != "" {
		out.AddField(f.path, lib.TypeJSON)
	}
	if err := lib.WriteJSONLWithSchema(ctx.Stdout(), out, routes); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

// followConfigGo renders the config as Go source for record codegen.
func followConfigGo(cfg ssql.FollowConfig) string {
	parts := []string{fmt.Sprintf("NodeField: %q, StartField: %q, SrcField: %q, DstField: %q", cfg.NodeField, cfg.StartField, cfg.SrcField, cfg.DstField)}
	switch cfg.Mode {
	case ssql.FollowLowestSum:
		parts = append(parts, "Mode: ssql.FollowLowestSum", fmt.Sprintf("Lowest: ssql.FollowSum{Field: %q, Name: %q}", cfg.Lowest.Field, cfg.Lowest.Name))
	case ssql.FollowAllPaths:
		parts = append(parts, "Mode: ssql.FollowAllPaths")
	}
	if len(cfg.Sums) > 0 {
		var sums []string
		for _, s := range cfg.Sums {
			sums = append(sums, fmt.Sprintf("{Field: %q, Name: %q}", s.Field, s.Name))
		}
		parts = append(parts, "Sums: []ssql.FollowSum{"+strings.Join(sums, ", ")+"}")
	}
	if cfg.DepthField != "" {
		parts = append(parts, fmt.Sprintf("DepthField: %q", cfg.DepthField))
	}
	if cfg.PathField != "" {
		parts = append(parts, fmt.Sprintf("PathField: %q", cfg.PathField))
	}
	if cfg.MaxDepth > 0 {
		parts = append(parts, fmt.Sprintf("MaxDepth: %d", cfg.MaxDepth))
	}
	if cfg.Both {
		parts = append(parts, "Both: true")
	}
	return "ssql.FollowConfig{" + strings.Join(parts, ", ") + "}"
}

// generateTraverseCode generates Go code for traverse: the main input
// is the edges, the side source (generateSideCode) the -start-file list
// when there is one. A typed pipeline whose traversal has no typed form
// runs it in record mode, with a plan note saying why (surfaced by
// -explain).
func generateTraverseCode(f followSpec) error {
	var notes []string
	s := sideSource{name: "traverse", file: f.startFile, leftKeys: []string{f.src, f.dst}, rightKeys: []string{f.src}}
	s.typed = func(c sideCtx, startSchema *lib.TypedSchema, startFragments []*lib.CodeFragment, subCommandStr string) (bool, error) {
		handled, reason, err := emitTypedFollow(c, startSchema, startFragments, subCommandStr, f)
		if !handled && err == nil {
			notes = []string{fmt.Sprintf("record fallback (%s)", reason)}
		}
		return handled, err
	}
	s.record = func(c sideCtx, startFields []string, _ func(string) string) error {
		edgeType := c.leftType
		if c.leftSchema != nil {
			edgeType = typedWireType(c.leftSchema)
		}
		cfg, err := followCheck(c.leftFields, edgeType, startFields, f)
		if err != nil {
			return lib.WriteErrorAndExit(getCommandString(), err)
		}
		values, err := traverseStartValues(f.starts, f.src, edgeType(f.src))
		if err != nil {
			return lib.WriteErrorAndExit(getCommandString(), err)
		}
		lit, err := goAnyLiteral(values)
		if err != nil {
			return lib.WriteErrorAndExit(getCommandString(), err)
		}
		more := "nil"
		if f.startFile != "" {
			more = c.funcName + "()"
		}
		code := fmt.Sprintf("joined := ssql.FollowEdges(ssql.FollowStarts(%q, %s, %s), %s)(%s)", f.src, lit, more, followConfigGo(cfg), c.inputVar)
		frag := lib.NewStmtFragment("joined", c.inputVar, code, nil, getCommandString())
		frag.PlanNotes = notes
		return lib.WriteCodeFragment(frag)
	}
	return generateSideCode(s)
}

// typedWireType is a typed schema field's wire type name ("" when the
// field is not in the schema), for the checks written against wire types.
func typedWireType(s *lib.TypedSchema) func(string) string {
	return func(name string) string {
		f, ok := lookupSchemaField(s, name)
		if !ok {
			return ""
		}
		// ssql:domain fixed: a traverse edge type is a number or a time
		switch f.GoType {
		case "int64":
			return lib.TypeInt
		case "ssql.Int128":
			return lib.TypeInt128
		case "ssql.Decimal":
			return lib.WireDecimal(f.Scale)
		case "float64":
			return lib.TypeFloat
		case "time.Time":
			return lib.TypeTime
		}
		return lib.TypeString
	}
}

// emitTypedFollow writes the fragments for a typed traversal over the
// edge Stream (DFC161 §11): typed.FollowEdgesParallel, typed.Follow over
// a Seq, the starts typed.FollowStarts over the -start literals and the
// start file's func. handled is false, with the reason, when the
// traversal has no typed form — a -path list (the typed tier has no list
// columns, DFC145), node fields of different types, or -start values
// beside a start file with other columns (a typed row would hold zeros
// where the record lanes leave them absent) — and nothing has been
// written: the caller traverses in record mode.
func emitTypedFollow(c sideCtx, startSchema *lib.TypedSchema, startFragments []*lib.CodeFragment, subCommandStr string, f followSpec) (handled bool, reason string, err error) {
	edgeSchema := c.leftSchema
	var startFields []string
	if startSchema != nil {
		startFields = typedFieldNames(startSchema)
	}
	edgeType := typedWireType(edgeSchema)
	cfg, err := followCheck(typedFieldNames(edgeSchema), edgeType, startFields, f)
	if err != nil {
		return false, "", lib.WriteErrorAndExit(getCommandString(), err)
	}
	values, err := traverseStartValues(f.starts, f.src, edgeType(f.src))
	if err != nil {
		return false, "", lib.WriteErrorAndExit(getCommandString(), err)
	}
	if f.path != "" {
		return false, "-path is a list, and the typed tier has no list columns (DFC145)", nil
	}
	srcF, _ := lookupSchemaField(edgeSchema, cfg.SrcField)
	dstF, _ := lookupSchemaField(edgeSchema, cfg.DstField)
	keyT := srcF.GoType
	switch keyT {
	case "string", "int64", "float64":
	default:
		return false, fmt.Sprintf("node field %s is %s; typed keys are text or numbers", cfg.SrcField, keyT), nil
	}
	if dstF.GoType != keyT {
		return false, fmt.Sprintf("node fields of different types (%s %s, %s %s)", cfg.SrcField, keyT, cfg.DstField, dstF.GoType), nil
	}

	// The start rows: the start file's, or one field, SRC.
	var structDefs []string
	start := startSchema
	if start != nil {
		sf, _ := lookupSchemaField(start, f.src)
		if sf.GoType != keyT {
			return false, fmt.Sprintf("the start column %s is %s and the edges' is %s", f.src, sf.GoType, keyT), nil
		}
		if len(values) > 0 && len(start.Fields) > 1 {
			return false, fmt.Sprintf("a -start row has only %s, and a typed row would hold zeros for the start file's other columns", f.src), nil
		}
	} else {
		start = &lib.TypedSchema{TypeName: edgeSchema.TypeName + "Start",
			Fields: []lib.TypedSchemaField{{Name: f.src, GoName: lib.GoNameFromColumn(f.src), GoType: keyT}}}
		structDefs = append(structDefs, lib.RenderStructDefWithComment(start, start.TypeName+" is a traversal's start."))
	}
	startSrc, _ := lookupSchemaField(start, f.src)
	var lits []string
	for _, v := range values {
		var lit string
		switch x := v.(type) {
		case string:
			lit = strconv.Quote(x)
		case int64:
			lit = strconv.FormatInt(x, 10)
		case float64:
			lit = strconv.FormatFloat(x, 'g', -1, 64)
		default:
			return false, fmt.Sprintf("a %T start has no typed literal", v), nil
		}
		lits = append(lits, fmt.Sprintf("{%s: %s}", startSrc.GoName, lit))
	}

	keyFn := func(typ, v, field string) string {
		if keyT == "string" {
			return fmt.Sprintf("func(%s %s) (string, bool) { return %s.%s, %s.%s != \"\" }", v, typ, v, field, v, field)
		}
		return fmt.Sprintf("func(%s %s) (%s, bool) { return %s.%s, true }", v, typ, keyT, v, field)
	}

	// The route: the start's columns, DST, then the results.
	route := &lib.TypedSchema{TypeName: edgeSchema.TypeName + "Route"}
	acc := &lib.TypedSchema{TypeName: edgeSchema.TypeName + "RouteAcc"}
	route.Fields = append(route.Fields, start.Fields...)
	route.Fields = append(route.Fields, lib.TypedSchemaField{Name: f.dst, GoName: lib.GoNameFromColumn(f.dst), GoType: keyT})
	if f.depth != "" {
		route.Fields = append(route.Fields, lib.TypedSchemaField{Name: f.depth, GoName: lib.GoNameFromColumn(f.depth), GoType: "int64"})
	}
	var steps, sumCmps, zeros []string
	var lowestCmp, negative string
	usesCmp := false
	for i, w := range f.weights() {
		ef, _ := lookupSchemaField(edgeSchema, w.Field)
		af := lib.TypedSchemaField{Name: w.Name, GoName: lib.GoNameFromColumn(w.Name), Scale: ef.Scale}
		var step, cmpE, neg string
		// ssql:domain numeric
		switch ef.GoType {
		case "int64":
			af.GoType = "ssql.Int128"
			step = fmt.Sprintf("a.%s = a.%s.Add(ssql.Int128From(e.%s))", af.GoName, af.GoName, ef.GoName)
			neg = fmt.Sprintf("e.%s < 0", ef.GoName)
		case "ssql.Int128", "ssql.Decimal":
			af.GoType = ef.GoType
			step = fmt.Sprintf("a.%s = a.%s.Add(e.%s)", af.GoName, af.GoName, ef.GoName)
			neg = fmt.Sprintf("e.%s.Sign() < 0", ef.GoName)
			if ef.GoType == "ssql.Decimal" {
				zeros = append(zeros, fmt.Sprintf("%s: ssql.MustDecimalFromInt128(ssql.Int128{}, %d)", af.GoName, ef.Scale))
			}
		case "float64":
			af.GoType = "float64"
			step = fmt.Sprintf("a.%s += e.%s", af.GoName, ef.GoName)
			neg = fmt.Sprintf("e.%s < 0", ef.GoName)
		default:
			return false, fmt.Sprintf("edge field %s is %s; a typed total needs a number", w.Field, ef.GoType), nil
		}
		if af.GoType == "float64" {
			cmpE, usesCmp = fmt.Sprintf("cmp.Compare(a.%s, b.%s)", af.GoName, af.GoName), true
		} else {
			cmpE = fmt.Sprintf("a.%s.Cmp(b.%s)", af.GoName, af.GoName)
		}
		steps = append(steps, step)
		acc.Fields = append(acc.Fields, af)
		route.Fields = append(route.Fields, af)
		if cfg.Mode == ssql.FollowLowestSum && i == len(f.weights())-1 {
			lowestCmp, negative = cmpE, neg
		} else {
			sumCmps = append(sumCmps, cmpE)
		}
	}

	startT, edgeT, routeT, accT := start.TypeName, edgeSchema.TypeName, route.TypeName, acc.TypeName
	var assigns []string
	for _, fl := range route.Fields {
		switch {
		case fl.Name == f.dst:
			assigns = append(assigns, fmt.Sprintf("%s: node", fl.GoName))
		case fl.Name == f.depth:
			assigns = append(assigns, fmt.Sprintf("%s: depth", fl.GoName))
		case slices.ContainsFunc(acc.Fields, func(a lib.TypedSchemaField) bool { return a.Name == fl.Name }):
			assigns = append(assigns, fmt.Sprintf("%s: a.%s", fl.GoName, fl.GoName))
		default:
			assigns = append(assigns, fmt.Sprintf("%s: s.%s", fl.GoName, fl.GoName))
		}
	}
	lines := []string{}
	switch cfg.Mode {
	case ssql.FollowLowestSum:
		lines = append(lines, "Mode: typed.FollowLowestSum")
	case ssql.FollowAllPaths:
		lines = append(lines, "Mode: typed.FollowAllPaths")
	}
	if cfg.MaxDepth > 0 {
		lines = append(lines, fmt.Sprintf("MaxDepth: %d", cfg.MaxDepth))
	}
	lines = append(lines,
		"SeedNode: "+keyFn(startT, "s", startSrc.GoName),
		"Src: "+keyFn(edgeT, "e", srcF.GoName),
		"Dst: "+keyFn(edgeT, "e", dstF.GoName))
	if cfg.Both {
		lines = append(lines, "Both: true",
			fmt.Sprintf("Swap: func(e %s) %s { e.%s, e.%s = e.%s, e.%s; return e }", edgeT, edgeT, srcF.GoName, dstF.GoName, dstF.GoName, srcF.GoName))
	}
	if len(zeros) > 0 {
		lines = append(lines, fmt.Sprintf("Zero: %s{%s}", accT, strings.Join(zeros, ", ")))
	}
	lines = append(lines, fmt.Sprintf("Step: func(a %s, e %s) %s { %s }", accT, edgeT, accT, strings.Join(append(steps, "return a"), "; ")))
	if len(sumCmps) == 1 {
		lines = append(lines, fmt.Sprintf("CmpSums: func(a, b %s) int { return %s }", accT, sumCmps[0]))
	} else if len(sumCmps) > 1 {
		usesCmp = true
		lines = append(lines, fmt.Sprintf("CmpSums: func(a, b %s) int { return cmp.Or(%s) }", accT, strings.Join(sumCmps, ", ")))
	}
	if lowestCmp != "" {
		lines = append(lines,
			fmt.Sprintf("CmpLowest: func(a, b %s) int { return %s }", accT, lowestCmp),
			fmt.Sprintf("Negative: func(e %s) bool { return %s }", edgeT, negative),
			fmt.Sprintf("LowestField: %q", cfg.Lowest.Field))
	}
	lines = append(lines, fmt.Sprintf("Emit: func(s %s, node %s, depth int64, a %s) %s {\n\t\t\treturn %s{%s}\n\t\t}", startT, keyT, accT, routeT, routeT, strings.Join(assigns, ", ")))
	spec := fmt.Sprintf("typed.FollowSpec[%s, %s, %s, %s, %s]{\n\t\t%s,\n\t}", startT, edgeT, routeT, accT, keyT, strings.Join(lines, ",\n\t\t"))

	more := "nil"
	if startFragments != nil {
		if err := lib.WriteCodeFragment(lib.NewFuncFragment(c.funcName, startFragments, subCommandStr)); err != nil {
			return true, "", fmt.Errorf("writing func fragment: %w", err)
		}
		more = c.funcName + "()"
	}
	starts := fmt.Sprintf("typed.FollowStarts([]%s{%s}, %s)", startT, strings.Join(lits, ", "), more)
	parallel := fmt.Sprintf("joined := typed.FollowEdgesParallel(%s, %s, %s)", c.inputVar, starts, spec)
	serial := fmt.Sprintf("joined := typed.Follow(%s, %s)(%s)", c.inputVar, spec, starts)
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if strings.Contains(spec, "ssql.") {
		imports = append(imports, "github.com/rosscartlidge/ssql/v4")
	}
	if usesCmp {
		imports = append(imports, "cmp")
	}
	frag := lib.NewStmtFragment("joined", c.inputVar, parallel, imports, getCommandString())
	frag.InputTypedSchema = edgeSchema
	frag.OutputTypedSchema = route
	frag.StructDefs = append(structDefs,
		lib.RenderStructDefWithComment(route, routeT+" is a route produced by traverse: the start's columns, the node reached, the totals."),
		lib.RenderStructDefWithComment(acc, accT+" is a route's running totals."))
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serial
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return true, "", lib.WriteCodeFragment(frag)
}

// startValueCompleter completes a -start value: values of the field
// -from names, sampled from the cross-pipe source the Ctrl-O binding
// passes (AUTOCLI_CACHE_FILE). Without one it answers the value hint, so
// Tab says "Values-Use-Ctrl-O" and Ctrl-O knows a value slot.
type startValueCompleter struct{}

func (startValueCompleter) Complete(ctx cf.CompletionContext) ([]string, error) {
	field, _ := ctx.GlobalFlags["-from"].(string)
	if field == "" {
		return []string{"<VALUE>"}, nil
	}
	src := os.Getenv("AUTOCLI_CACHE_FILE")
	if src == "" {
		return []string{cf.FieldValueHint}, nil
	}
	values, err := completionFieldValueSource(src, field, 100, 10000)
	if err != nil || len(values) == 0 {
		return []string{"<VALUE>"}, nil
	}
	var out []string
	for _, v := range values {
		if strings.HasPrefix(v, ctx.Partial) {
			out = append(out, v)
		}
	}
	return out, nil
}
