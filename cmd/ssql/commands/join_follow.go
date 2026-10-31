package commands

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Graph traversal (DFC161), a mode on `join`: the seeds are the stream,
// the edges the right-side file, and the join runs to a fixpoint — each
// round joins the routes the last round found against the edges again.
//
//	printf 'city\nSYD\n' | ssql from csv - | ssql join flights.csv -on city src -follow dst -depth hops
//	… | ssql join flights.csv -on city src -follow dst -lowest-sum mins total
//	… | ssql join flights.csv -on city src -follow dst -all-paths -path route
//
// Exec and record codegen call ssql.Follow; typed emits
// typed.Follow[Parallel]; generate sql emits a recursive CTE (the keyed
// modes on DuckDB only).

// followSpec is the -follow family of flags, parsed once by the handler.
type followSpec struct {
	dst         string
	mode        ssql.FollowMode
	lowest      ssql.FollowSum
	sums        []ssql.FollowSum
	depth, path string
	maxDepth    int
	both        bool
}

func (f followSpec) active() bool { return f.dst != "" }

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

// parseFollowSpec reads the -follow flags from the parsed context.
func parseFollowSpec(ctx *cf.Context) (followSpec, error) {
	var f followSpec
	g := ctx.GlobalFlags
	f.dst, _ = g["-follow"].(string)
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
	if !f.active() {
		if modes > 0 || f.depth != "" || f.path != "" || f.maxDepth != 0 || f.both || len(f.sums) > 0 {
			return f, fmt.Errorf("join: -fewest-hops, -lowest-sum, -all-paths, -depth, -path, -sum, -max-depth and -both need -follow FIELD")
		}
		return f, nil
	}
	if modes > 1 {
		return f, fmt.Errorf("join -follow: -fewest-hops, -lowest-sum and -all-paths are alternatives — give one (-fewest-hops is the default)")
	}
	switch {
	case all:
		f.mode = ssql.FollowAllPaths
	case len(lowest) > 0:
		f.mode, f.lowest = ssql.FollowLowestSum, lowest[0]
	}
	for _, s := range append(append([]ssql.FollowSum{}, f.sums...), lowest...) {
		if s.Field == "" || s.Name == "" {
			return f, fmt.Errorf("join -follow: -sum and -lowest-sum need FIELD NAME")
		}
	}
	if f.maxDepth < 0 {
		return f, fmt.Errorf("join -follow: -max-depth %d must be positive", f.maxDepth)
	}
	if f.maxDepth > 0 && f.mode == ssql.FollowLowestSum {
		return f, fmt.Errorf("join -follow: -max-depth is not defined with -lowest-sum (the cheapest route within N hops is not what the search finds; DFC161 §11)")
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
// allow: one node/source pair, no merge options, the fields present, the
// weights numeric, and no result column colliding with a seed column or
// another result. nil field lists are unknown and skipped.
func followCheck(leftFields, rightFields []string, rightType func(string) string, clauses []ssql.LookupClause, f followSpec, joinType string, opts joinOptions, asof asofSpec) (ssql.FollowConfig, error) {
	var cfg ssql.FollowConfig
	if len(clauses) != 1 || len(clauses[0].FieldRenames) > 0 {
		return cfg, fmt.Errorf("join -follow needs exactly one -on NODE SRC (or -using FIELD) and no -as: the seed's node field and the edge's source field")
	}
	if joinType != "" && joinType != "inner" {
		return cfg, fmt.Errorf("join -follow: -type %s has no traversal meaning (every seed is its own depth-0 row)", joinType)
	}
	if opts.any() {
		return cfg, fmt.Errorf("join -follow: -suffix, -exclude-left and -exclude-right are not supported with -follow (the output is the seed's columns plus the results)")
	}
	if asof.active() {
		return cfg, fmt.Errorf("join: -follow and -asof are different joins — give one")
	}
	c := clauses[0]
	if c.RightField == f.dst {
		return cfg, fmt.Errorf("join -follow %s: the edge's target must be a different field from its source (-on %s %s)", f.dst, c.LeftField, c.RightField)
	}
	if leftFields != nil && !fieldListHasOrPath(leftFields, c.LeftField) {
		return cfg, fmt.Errorf("join -follow: seed field %q not found (available: %s)", c.LeftField, strings.Join(leftFields, ", "))
	}
	if rightFields != nil {
		for _, rf := range append([]string{c.RightField, f.dst}, followFields(f.weights())...) {
			if !fieldListHasOrPath(rightFields, rf) {
				return cfg, fmt.Errorf("join -follow: edge field %q not found (available: %s)", rf, strings.Join(rightFields, ", "))
			}
		}
		for _, w := range f.weights() {
			switch base, _ := lib.ParseWireType(rightType(w.Field)); base {
			case "", lib.TypeInt, lib.TypeInt128, lib.TypeFloat, lib.TypeDecimal:
			default:
				return cfg, fmt.Errorf("join -follow: edge field %q is %s; a sum needs a number", w.Field, base)
			}
		}
	}
	seen := map[string]bool{}
	for _, n := range f.accNames() {
		if seen[n] || (leftFields != nil && fieldListHasOrPath(leftFields, n)) || n == c.LeftField {
			return cfg, fmt.Errorf("join -follow: result column %q is already a column (seed: %s)", n, strings.Join(leftFields, ", "))
		}
		seen[n] = true
	}
	return ssql.FollowConfig{
		NodeField: c.LeftField, SrcField: c.RightField, DstField: f.dst,
		Mode: f.mode, Lowest: f.lowest, Sums: f.sums,
		DepthField: f.depth, PathField: f.path, MaxDepth: f.maxDepth, Both: f.both,
	}, nil
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
	switch base, _ := lib.ParseWireType(edgeType); base {
	case lib.TypeInt, lib.TypeInt128:
		return lib.TypeInt128
	case lib.TypeDecimal:
		return edgeType
	}
	return lib.TypeFloat
}

// execFollowJoin runs the traversal in the interpreter.
func execFollowJoin(ctx *cf.Context, leftRecords iter.Seq[ssql.Record], leftSchema *lib.Schema, rightSeq iter.Seq[ssql.Record], rightSchema *lib.Schema, clauses []ssql.LookupClause, f followSpec, joinType string, opts joinOptions, asof asofSpec) error {
	var leftFields []string
	if leftSchema != nil {
		leftFields = leftSchema.Fields
	}
	cfg, err := followCheck(leftFields, rightSchema.Fields, rightSchema.TypeOf, clauses, f, joinType, opts, asof)
	if err != nil {
		return err
	}
	routes := ssql.Follow(rightSeq, cfg)(leftRecords)

	var outputSchema *lib.Schema
	if leftSchema != nil {
		outputSchema = lib.NewSchema()
		for _, fld := range leftSchema.Fields {
			outputSchema.AddField(fld, leftSchema.TypeOf(fld))
		}
		if !outputSchema.HasField(cfg.NodeField) {
			outputSchema.AddField(cfg.NodeField, rightSchema.TypeOf(cfg.DstField))
		}
		if f.depth != "" {
			outputSchema.AddField(f.depth, lib.TypeInt)
		}
		for _, w := range f.weights() {
			outputSchema.AddField(w.Name, followSumType(rightSchema.TypeOf(w.Field)))
		}
		if f.path != "" {
			outputSchema.AddField(f.path, lib.TypeJSON)
		}
	}
	if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, routes); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

// followConfigGo renders the config as Go source for record codegen.
func followConfigGo(cfg ssql.FollowConfig) string {
	parts := []string{fmt.Sprintf("NodeField: %q, SrcField: %q, DstField: %q", cfg.NodeField, cfg.SrcField, cfg.DstField)}
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

// generateFollowStmt writes the record-mode stmt fragment.
func generateFollowStmt(inputVar, funcName string, cfg ssql.FollowConfig, notes []string) error {
	code := fmt.Sprintf("joined := ssql.Follow(%s(), %s)(%s)", funcName, followConfigGo(cfg), inputVar)
	frag := lib.NewStmtFragment("joined", inputVar, code, nil, getCommandString())
	frag.PlanNotes = notes
	return lib.WriteCodeFragment(frag)
}

// typedWireType is a typed schema field's wire type name ("" when the
// field is not in the schema), for the checks written against wire types.
func typedWireType(s *lib.TypedSchema) func(string) string {
	return func(name string) string {
		f, ok := lookupSchemaField(s, name)
		if !ok {
			return ""
		}
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

// emitTypedFollow writes the func + stmt fragments for a typed
// traversal: typed.FollowParallel over the seed Stream (each seed's walk
// is independent), typed.Follow over a Seq. handled is false, with the
// reason, when the traversal has no typed form — a -path list (the typed
// tier has no list columns, DFC145) or node fields of different types —
// and nothing has been written: the caller joins in record mode.
func emitTypedFollow(inputVar, funcName string, leftSchema, rightSchema *lib.TypedSchema, rightFragments []*lib.CodeFragment, subCommandStr string, clauses []ssql.LookupClause, f followSpec, joinType string, opts joinOptions, asof asofSpec) (handled bool, reason string, err error) {
	cfg, err := followCheck(typedFieldNames(leftSchema), typedFieldNames(rightSchema), typedWireType(rightSchema), clauses, f, joinType, opts, asof)
	if err != nil {
		return false, "", lib.WriteErrorAndExit(getCommandString(), err)
	}
	if f.path != "" {
		return false, "-path is a list, and the typed tier has no list columns (DFC145)", nil
	}
	seedF, _ := lookupSchemaField(leftSchema, cfg.NodeField)
	srcF, _ := lookupSchemaField(rightSchema, cfg.SrcField)
	dstF, _ := lookupSchemaField(rightSchema, cfg.DstField)
	switch seedF.GoType {
	case "string", "int64", "float64":
	default:
		return false, fmt.Sprintf("node field %s is %s; typed keys are text or numbers", cfg.NodeField, seedF.GoType), nil
	}
	if srcF.GoType != seedF.GoType || dstF.GoType != seedF.GoType {
		return false, fmt.Sprintf("node fields of different types (%s %s, %s %s, %s %s)", cfg.NodeField, seedF.GoType, cfg.SrcField, srcF.GoType, cfg.DstField, dstF.GoType), nil
	}
	keyT := seedF.GoType
	keyFn := func(typ, v, field string) string {
		if keyT == "string" {
			return fmt.Sprintf("func(%s %s) (string, bool) { return %s.%s, %s.%s != \"\" }", v, typ, v, field, v, field)
		}
		return fmt.Sprintf("func(%s %s) (%s, bool) { return %s.%s, true }", v, typ, keyT, v, field)
	}

	// The running totals, one field per -sum (then -lowest-sum).
	route := &lib.TypedSchema{TypeName: leftSchema.TypeName + "Route"}
	acc := &lib.TypedSchema{TypeName: leftSchema.TypeName + "RouteAcc"}
	route.Fields = append(route.Fields, leftSchema.Fields...)
	if f.depth != "" {
		route.Fields = append(route.Fields, lib.TypedSchemaField{Name: f.depth, GoName: lib.GoNameFromColumn(f.depth), GoType: "int64"})
	}
	var steps, sumCmps, zeros []string
	var lowestCmp, negative string
	usesCmp := false
	for i, w := range f.weights() {
		ef, _ := lookupSchemaField(rightSchema, w.Field)
		af := lib.TypedSchemaField{Name: w.Name, GoName: lib.GoNameFromColumn(w.Name), Scale: ef.Scale}
		var step, cmpE, neg string
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

	seedT, edgeT, routeT, accT := leftSchema.TypeName, rightSchema.TypeName, route.TypeName, acc.TypeName
	var assigns []string
	for _, fl := range route.Fields {
		switch {
		case fl.Name == cfg.NodeField:
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
		"SeedNode: "+keyFn(seedT, "s", seedF.GoName),
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
	lines = append(lines, fmt.Sprintf("Emit: func(s %s, node %s, depth int64, a %s) %s {\n\t\t\treturn %s{%s}\n\t\t}", seedT, keyT, accT, routeT, routeT, strings.Join(assigns, ", ")))
	spec := fmt.Sprintf("typed.FollowSpec[%s, %s, %s, %s, %s]{\n\t\t%s,\n\t}", seedT, edgeT, routeT, accT, keyT, strings.Join(lines, ",\n\t\t"))

	if err := lib.WriteCodeFragment(lib.NewFuncFragment(funcName, rightFragments, subCommandStr)); err != nil {
		return true, "", fmt.Errorf("writing func fragment: %w", err)
	}
	parallel := fmt.Sprintf("joined := typed.FollowParallel(%s, %s(), %s)", inputVar, funcName, spec)
	serial := fmt.Sprintf("joined := typed.Follow(%s(), %s)(%s)", funcName, spec, inputVar)
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if strings.Contains(spec, "ssql.") {
		imports = append(imports, "github.com/rosscartlidge/ssql/v4")
	}
	if usesCmp {
		imports = append(imports, "cmp")
	}
	frag := lib.NewStmtFragment("joined", inputVar, parallel, imports, getCommandString())
	frag.InputTypedSchema = leftSchema
	frag.OutputTypedSchema = route
	frag.StructDefs = []string{
		lib.RenderStructDefWithComment(route, routeT+" is a route produced by join -follow: the seed's columns, the node reached, the totals."),
		lib.RenderStructDefWithComment(acc, accT+" is a route's running totals."),
	}
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serial
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return true, "", lib.WriteCodeFragment(frag)
}
