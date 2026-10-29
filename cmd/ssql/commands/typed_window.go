package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Typed codegen for `ssql window` (DFC130 unit 4). The generated stage is
// one typed.Window call: per clause a partition-key closure, an order
// comparator, a RANGE key when the frame is RANGE, and per function a
// field-reading closure; plus a build function that assembles the output
// struct (the input's fields followed by one field per result). The
// runtime (typed/window.go) mirrors ssql.Window's arithmetic exactly, so
// the N-way equivalence gate's window cases are the gate for this too.
//
// Returns handled=false with a reason — WITHOUT writing a fragment — for
// the shapes that stay on the record path: a registry aggregate over the
// frame (no typed accumulator-over-frame yet), a RANGE frame over a text
// order column (exec parses dates from strings; typed cannot know), a
// LAG/LEAD default whose type differs from the field's, a pointer-typed
// (nullable) order or partition field, or a result name that collides
// with an input field. Unknown fields are loud, like every typed emitter.
func emitTypedWindow(inputVar string, in *lib.TypedSchema, configs []ssql.WindowConfig, presorted bool) (bool, string, error) {
	outType := in.TypeName + "Windowed"
	outVar := "windowed"

	// Output schema: input fields, then one per result.
	out := &lib.TypedSchema{TypeName: outType}
	out.Fields = append(out.Fields, in.Fields...)
	inNames := make(map[string]bool, len(in.Fields))
	for _, f := range in.Fields {
		inNames[strings.ToLower(f.Name)] = true
	}
	usedGo := make(map[string]bool, len(in.Fields))
	for _, f := range in.Fields {
		usedGo[f.GoName] = true
	}

	type resultField struct {
		goName, goType string
		assign         string // Go statement assigning res[i] into o.<goName>
	}
	var results []resultField
	var clauseCode []string
	var foldDefs, imports, hoisted []string // -stream-expr state structs and what their Go needs
	needFmt := false
	needStrconv, needStrings := false, false
	resIdx := 0

	for ci, cfg := range configs {
		var b strings.Builder
		b.WriteString("\t\t{\n")

		// Partition key.
		if len(cfg.PartitionBy) > 0 {
			var parts []string
			for _, p := range cfg.PartitionBy {
				f, ok := lookupSchemaField(in, p)
				if !ok {
					return true, "", lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'window' partitions by unknown field %q", p))
				}
				if strings.HasPrefix(f.GoType, "*") {
					return false, fmt.Sprintf("window: nullable partition field %q has no typed key yet", p), nil
				}
				// The key text fmt.Sprint would make, without fmt: a string
				// as is, an int through strconv (fmt.Sprint was 1.1 s of
				// the 14.6 M-row benchmark window).
				switch f.GoType {
				case "string":
					parts = append(parts, "r."+f.GoName)
				case "int64":
					parts = append(parts, "strconv.FormatInt(r."+f.GoName+", 10)")
					needStrconv = true
				default:
					parts = append(parts, "fmt.Sprint(r."+f.GoName+")")
					needFmt = true
				}
			}
			fmt.Fprintf(&b, "\t\t\tPartition: func(r %s) string { return %s },\n", in.TypeName, strings.Join(parts, ` + "\x1f" + `))
		}

		// Order comparator.
		if len(cfg.OrderBy) > 0 {
			var cmp strings.Builder
			for _, of := range cfg.OrderBy {
				f, ok := lookupSchemaField(in, of.Field)
				if !ok {
					return true, "", lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'window' orders by unknown field %q", of.Field))
				}
				if strings.HasPrefix(f.GoType, "*") {
					return false, fmt.Sprintf("window: nullable order field %q has no typed comparator yet", of.Field), nil
				}
				lhs, rhs := "a."+f.GoName, "b."+f.GoName
				if of.Desc {
					lhs, rhs = rhs, lhs
				}
				switch f.GoType {
				case "time.Time":
					fmt.Fprintf(&cmp, "if %s.Before(%s) { return -1 }; if %s.After(%s) { return 1 }; ", lhs, rhs, lhs, rhs)
				case "ssql.Int128", "ssql.Decimal": // not cmp.Ordered: the comparator form (DFC155 C, E)
					fmt.Fprintf(&cmp, "if c := %s.Cmp(%s); c != 0 { return c }; ", lhs, rhs)
				case "string":
					// One byte comparison per call: `<` then `>` compared
					// twice on every "greater" (10 s of the benchmark's sort).
					fmt.Fprintf(&cmp, "if c := strings.Compare(%s, %s); c != 0 { return c }; ", lhs, rhs)
					needStrings = true
				default:
					fmt.Fprintf(&cmp, "if %s < %s { return -1 }; if %s > %s { return 1 }; ", lhs, rhs, lhs, rhs)
				}
			}
			fmt.Fprintf(&b, "\t\t\tHasOrder: true,\n\t\t\tCompare: func(a, b %s) int { %sreturn 0 },\n", in.TypeName, cmp.String())
			if cfg.OrderBy[0].Desc {
				b.WriteString("\t\t\tDesc: true,\n")
			}
			// One order field of a plain type: its typed key beside the
			// comparator, so the runtime sorts (key, index) pairs instead
			// of rows (DFC158 §3.9). Compare still decides peers.
			if len(cfg.OrderBy) == 1 {
				f, _ := lookupSchemaField(in, cfg.OrderBy[0].Field)
				if key, ok := map[string]string{"string": "KeyString", "int64": "KeyInt", "float64": "KeyFloat"}[f.GoType]; ok {
					fmt.Fprintf(&b, "\t\t\t%s: func(r %s) %s { return r.%s },\n", key, in.TypeName, f.GoType, f.GoName)
				}
			}
		}

		// Frame.
		fr := cfg.Frame
		if fr.Range {
			if len(cfg.OrderBy) != 1 {
				return true, "", lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'window' RANGE frame needs exactly one -order field"))
			}
			f, _ := lookupSchemaField(in, cfg.OrderBy[0].Field)
			var key string
			switch {
			case isNumericGoType(f.GoType) && !fr.RangeTime:
				key = fmt.Sprintf("func(r %s) float64 { return float64(r.%s) }", in.TypeName, f.GoName)
			case f.GoType == "time.Time" && fr.RangeTime:
				key = fmt.Sprintf("func(r %s) float64 { return float64(r.%s.UnixNano()) / 1e9 }", in.TypeName, f.GoName)
			default:
				return false, fmt.Sprintf("window: RANGE frame over %s field %q (bound is %s) stays on the record path, which parses dates from text", f.GoType, f.Name, map[bool]string{true: "a duration", false: "a number"}[fr.RangeTime]), nil
			}
			fmt.Fprintf(&b, "\t\t\tRangeKey: %s,\n", key)
			fmt.Fprintf(&b, "\t\t\tFrame: typed.WindowFrame{Range: true, RangePreceding: %v, RangeFollowing: %v},\n", fr.RangePreceding, fr.RangeFollowing)
		} else {
			fmt.Fprintf(&b, "\t\t\tFrame: typed.WindowFrame{Preceding: %d, Following: %d},\n", fr.Preceding, fr.Following)
		}

		// Specs.
		b.WriteString("\t\t\tSpecs: []typed.WindowSpec[" + in.TypeName + "]{\n")
		for _, spec := range cfg.Specs {
			d := ssql.DescribeWindowFunc(spec.Function)
			if d.Kind == "aggregate" && !typedWindowAggs[d.Agg.Name] {
				return false, fmt.Sprintf("window: %s over a frame has no typed form yet (it runs on the record path)", d.Agg.Name), nil
			}
			if inNames[strings.ToLower(spec.ResultName)] {
				return false, fmt.Sprintf("window: result %q overwrites an input field; typed output keeps both, so this stays on the record path", spec.ResultName), nil
			}
			var f lib.TypedSchemaField
			if d.Field != "" {
				var ok bool
				f, ok = lookupSchemaField(in, d.Field)
				if !ok {
					return true, "", lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'window' %s reads unknown field %q", d.Kind, d.Field))
				}
			}
			var goType, assign, specLit string
			scale := 0 // a decimal result's scale (DFC155 E)
			if d.Kind == "stream_expr" {
				// A fold: the group-by lowering (expr_stream.go) gives the
				// state struct, the simultaneous EVERY assignment and FINAL
				// in its own type; unknown fields are loud, any other
				// refusal is the record path.
				plan, err := lowerStreamAgg(streamExprSpec{initExpr: d.Init, everyExpr: d.Every, finalExpr: d.Final, result: spec.ResultName}, in, resIdx)
				if err != nil {
					var unknownField *exprUnknownFieldError
					if errors.As(err, &unknownField) {
						return true, "", lib.WriteErrorAndExit(getCommandString(),
							fmt.Errorf("ssql generate go -typed: 'window -stream-expr': %w", err))
					}
					return false, fmt.Sprintf("window -stream-expr %s: %v", spec.ResultName, err), nil
				}
				stateType := fmt.Sprintf("%sFold%d", outType, resIdx)
				foldDefs = append(foldDefs, renderWindowFoldState(stateType, plan))
				imports = append(imports, plan.Imports...)
				hoisted = append(hoisted, plan.Hoisted...)
				goType = string(plan.FinalType)
				scale = plan.FinalScale
				assign = windowResultAssign(resIdx, goType, false)
				specLit = renderWindowFoldSpec(stateType, in.TypeName, plan)
			} else {
				var reason string
				_, goType, assign, specLit, reason = typedWindowSpec(d, f, in.TypeName, resIdx)
				if reason != "" {
					return false, reason, nil
				}
				if goType == "ssql.Decimal" {
					scale = f.Scale
				}
			}
			gn := lib.GoNameFromColumn(spec.ResultName)
			for usedGo[gn] {
				gn += "_"
			}
			usedGo[gn] = true
			results = append(results, resultField{goName: gn, goType: goType, assign: strings.ReplaceAll(assign, "GONAME", gn)})
			out.Fields = append(out.Fields, lib.TypedSchemaField{Name: spec.ResultName, GoName: gn, GoType: goType, Scale: scale})
			fmt.Fprintf(&b, "\t\t\t\t%s,\n", specLit)
			resIdx++
		}
		b.WriteString("\t\t\t},\n\t\t},")
		clauseCode = append(clauseCode, b.String())
		_ = ci
	}

	// Output struct definition.
	structDef := renderResultStructDef(out)
	structDef = strings.Replace(structDef, "is the per-group result type produced by typed.GroupBy", "is the input row plus the window results (typed.Window)", 1)

	// The build function.
	var build strings.Builder
	fmt.Fprintf(&build, "func(r %s, res typed.WindowRow) %s {\n\t\to := %s{", in.TypeName, outType, outType)
	for i, f := range in.Fields {
		if i > 0 {
			build.WriteString(", ")
		}
		fmt.Fprintf(&build, "%s: r.%s", f.GoName, f.GoName)
	}
	build.WriteString("}\n")
	for _, r := range results {
		build.WriteString("\t\t" + r.assign + "\n")
	}
	build.WriteString("\t\treturn o\n\t}")

	// Both templates: typed.WindowParallel takes the source's Stream (the
	// shards materialised concurrently, in shard order = input order) and
	// produces an ordered Stream whose shards are ranges of the window's
	// output order, so a group-by or a per-shard sink after it runs
	// parallel and a serial consumer gets the order through Serial();
	// typed.Window is the iter.Seq alternative. The window is a barrier
	// either way, but a SerialOnly template here made the planner read
	// the SOURCE serially too (DFC158 §4.3, 2026-10-28), and an iter.Seq
	// output made every consumer serial (DFC158 §3.11, 2026-10-29).
	clauses := fmt.Sprintf("[]typed.WindowClause[%s]{\n%s\n\t}, %s", in.TypeName, strings.Join(clauseCode, "\n"), build.String())
	// -presorted with every clause on the same partition and order (the
	// record path's streaming condition) takes typed.WindowPresorted:
	// contiguous runs, no sort, no gather; other shapes run the
	// materialised form as before.
	streamFn := "typed.WindowParallel"
	if presorted && windowClausesShareOrder(configs) {
		streamFn = "typed.WindowPresorted"
	}
	code := fmt.Sprintf("%s := %s(%s)(%s)", outVar, streamFn, clauses, inputVar)
	serialCode := fmt.Sprintf("%s := typed.Window(%s)(%s)", outVar, clauses, inputVar)

	imports = append(imports, "github.com/rosscartlidge/ssql/v4/typed")
	if needFmt {
		imports = append(imports, "fmt")
	}
	if needStrconv {
		imports = append(imports, "strconv")
	}
	if needStrings {
		imports = append(imports, "strings")
	}
	imports = dedupeImports(imports)
	frag := lib.NewStmtFragment(outVar, inputVar, code, imports, getCommandString())
	frag.InputTypedSchema = in
	frag.OutputTypedSchema = out
	frag.StructDefs = append(append([]string{structDef}, foldDefs...), hoisted...)
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return true, "", lib.WriteCodeFragment(frag)
}

// windowResultAssign is the statement assigning result i of a
// typed.WindowRow into o.GONAME (its address when ptr — a nullable
// result): the column's typed reader for the three unboxed kinds (no
// allocation per row), the boxed value asserted to the Go type for the
// rest (the readers fall back across kinds, so a -min on an int64 field
// reads through Int either way).
func windowResultAssign(i int, goType string, ptr bool) string {
	var read string
	switch goType {
	case "int64":
		read = fmt.Sprintf("res.Int(%d)", i)
	case "float64":
		read = fmt.Sprintf("res.Float(%d)", i)
	case "ssql.Int128":
		read = fmt.Sprintf("res.Wide(%d)", i)
	default:
		read = fmt.Sprintf("res.Value(%d).(%s)", i, goType)
	}
	if ptr {
		return fmt.Sprintf("if v, ok := %s; ok { o.GONAME = &v }", read)
	}
	return fmt.Sprintf("if v, ok := %s; ok { o.GONAME = v }", read)
}

// windowClausesShareOrder reports whether every clause partitions and
// orders by the same fields in the same directions — the shape under
// which -presorted describes the input for all of them at once.
func windowClausesShareOrder(configs []ssql.WindowConfig) bool {
	for _, cfg := range configs[1:] {
		if len(cfg.PartitionBy) != len(configs[0].PartitionBy) || len(cfg.OrderBy) != len(configs[0].OrderBy) {
			return false
		}
		for i := range cfg.PartitionBy {
			if cfg.PartitionBy[i] != configs[0].PartitionBy[i] {
				return false
			}
		}
		for i := range cfg.OrderBy {
			if cfg.OrderBy[i] != configs[0].OrderBy[i] {
				return false
			}
		}
	}
	return true
}

// typedWindowAggs are the registry aggregates over a frame that have a
// typed form (typed.WAgg); the rest take the record path.
var typedWindowAggs = map[string]bool{"median": true, "percentile": true, "stddev": true, "variance": true, "mode": true}

// typedWindowSpec renders one typed.WindowSpec literal and decides the
// result's Go type and the assignment from res[i]. Results that can be
// absent (lag/lead without a default, nth_value) are pointer-typed —
// nullable, JSON null, CSV empty — so the lanes agree with exec's
// missing field. A reason (non-empty) means "record path".
func typedWindowSpec(d ssql.WindowFuncDesc, f lib.TypedSchemaField, rowType string, i int) (kind, goType, assign, lit, reason string) {
	val := fmt.Sprintf("Val: func(r %s) (any, bool) { return r.%s, true }", rowType, f.GoName)
	if strings.HasPrefix(f.GoType, "*") {
		val = fmt.Sprintf("Val: func(r %s) (any, bool) { if r.%s == nil { return nil, false }; return *r.%s, true }", rowType, f.GoName, f.GoName)
	}
	base := strings.TrimPrefix(f.GoType, "*")
	num := ""
	isInt := base == "int64"
	isWide := base == "ssql.Int128"
	isDec := base == "ssql.Decimal"
	if isNumericGoType(base) {
		toF := func(src string) string { // the float reader's conversion
			if isWide || isDec {
				return src + ".Float64()"
			}
			return "float64(" + src + ")"
		}
		if strings.HasPrefix(f.GoType, "*") {
			num = fmt.Sprintf("Num: func(r %s) (float64, bool) { if r.%s == nil { return 0, false }; return %s, true }", rowType, f.GoName, toF("*r."+f.GoName))
			if isInt { // the exact reader beside the float one (DFC155 B, C)
				num += fmt.Sprintf(", Int: func(r %s) (int64, bool) { if r.%s == nil { return 0, false }; return *r.%s, true }", rowType, f.GoName, f.GoName)
			}
			if isWide {
				num += fmt.Sprintf(", Wide: func(r %s) (ssql.Int128, bool) { if r.%s == nil { return ssql.Int128{}, false }; return *r.%s, true }", rowType, f.GoName, f.GoName)
			}
			if isDec {
				num += fmt.Sprintf(", Dec: func(r %s) (ssql.Decimal, bool) { if r.%s == nil { return ssql.Decimal{}, false }; return *r.%s, true }", rowType, f.GoName, f.GoName)
			}
		} else {
			num = fmt.Sprintf("Num: func(r %s) (float64, bool) { return %s, true }", rowType, toF("r."+f.GoName))
			if isInt {
				num += fmt.Sprintf(", Int: func(r %s) (int64, bool) { return r.%s, true }", rowType, f.GoName)
			}
			if isWide {
				num += fmt.Sprintf(", Wide: func(r %s) (ssql.Int128, bool) { return r.%s, true }", rowType, f.GoName)
			}
			if isDec {
				num += fmt.Sprintf(", Dec: func(r %s) (ssql.Decimal, bool) { return r.%s, true }", rowType, f.GoName)
			}
		}
	}
	read := func(t string) string { return windowResultAssign(i, t, false) }
	readPtr := func(t string) string { return windowResultAssign(i, t, true) }
	switch d.Kind {
	case "row_number", "rank", "dense_rank", "count":
		k := map[string]string{"row_number": "WRowNumber", "rank": "WRank", "dense_rank": "WDenseRank", "count": "WCount"}[d.Kind]
		return d.Kind, "int64", read("int64"), fmt.Sprintf("{Kind: typed.%s}", k), ""
	case "ntile":
		return d.Kind, "int64", read("int64"), fmt.Sprintf("{Kind: typed.WNtile, N: %d}", d.N), ""
	case "percent_rank", "cume_dist":
		k := map[string]string{"percent_rank": "WPercentRank", "cume_dist": "WCumeDist"}[d.Kind]
		return d.Kind, "float64", read("float64"), fmt.Sprintf("{Kind: typed.%s}", k), ""
	case "sum", "avg":
		if num == "" {
			return "", "", "", "", fmt.Sprintf("window: %s over non-numeric field %q stays on the record path", d.Kind, f.Name)
		}
		k := map[string]string{"sum": "WSum", "avg": "WAvg"}[d.Kind]
		if d.Kind == "sum" && (isInt || isWide) { // an integer column sums to an int128, exactly (DFC155 C)
			return d.Kind, "ssql.Int128", read("ssql.Int128"), fmt.Sprintf("{Kind: typed.WSum, %s}", num), ""
		}
		if d.Kind == "sum" && isDec { // a decimal column sums to a decimal at its scale (DFC155 E)
			return d.Kind, "ssql.Decimal", read("ssql.Decimal"), fmt.Sprintf("{Kind: typed.WSum, %s}", num), ""
		}
		return d.Kind, "float64", read("float64"), fmt.Sprintf("{Kind: typed.%s, %s}", k, num), ""
	case "count_field":
		return d.Kind, "int64", read("int64"), fmt.Sprintf("{Kind: typed.WCountField, %s}", val), ""
	case "first", "last", "min", "max":
		k := map[string]string{"first": "WFirst", "last": "WLast", "min": "WMin", "max": "WMax"}[d.Kind]
		// Always present within a non-empty frame; keeps the field's type
		// (a nullable field stays nullable).
		if strings.HasPrefix(f.GoType, "*") {
			return d.Kind, f.GoType, readPtr(base), fmt.Sprintf("{Kind: typed.%s, %s}", k, val), ""
		}
		return d.Kind, f.GoType, read(f.GoType), fmt.Sprintf("{Kind: typed.%s, %s}", k, val), ""
	case "aggregate":
		// The registry statistics over the frame (DFC130 unit 2): the
		// typed WAgg kinds compute the same arithmetic as the record
		// path's aggregates. Absent below two rows (variance, stddev) or
		// with no present value, so the result is nullable, as the record
		// lane's missing field.
		switch d.Agg.Name {
		case "median", "percentile":
			if num == "" {
				return "", "", "", "", fmt.Sprintf("window: %s over non-numeric field %q stays on the record path", d.Agg.Name, f.Name)
			}
			p := 0.5
			if d.Agg.Name == "percentile" {
				p, _ = percentileP(d.Agg.Extra)
			}
			return d.Agg.Name, "*float64", readPtr("float64"), fmt.Sprintf("{Kind: typed.WAgg, Agg: typed.WAggQuantile, P: %v, %s}", p, num), ""
		case "stddev", "variance":
			if num == "" {
				return "", "", "", "", fmt.Sprintf("window: %s over non-numeric field %q stays on the record path", d.Agg.Name, f.Name)
			}
			k := map[string]string{"stddev": "WAggStdDev", "variance": "WAggVariance"}[d.Agg.Name]
			return d.Agg.Name, "*float64", readPtr("float64"), fmt.Sprintf("{Kind: typed.WAgg, Agg: typed.%s, %s}", k, num), ""
		case "mode":
			return d.Agg.Name, "*" + base, readPtr(base), fmt.Sprintf("{Kind: typed.WAgg, Agg: typed.WAggMode, %s}", val), ""
		}
		return "", "", "", "", fmt.Sprintf("window: %s over a frame has no typed form yet (it runs on the record path)", d.Agg.Name)
	case "lag", "lead", "nth_value":
		k := map[string]string{"lag": "WLag", "lead": "WLead", "nth_value": "WNth"}[d.Kind]
		if d.Default != nil {
			// A default of the field's own type makes the result non-nullable.
			defGo := goLiteralType(d.Default)
			if defGo != base {
				return "", "", "", "", fmt.Sprintf("window: %s default %v is %s but field %q is %s — stays on the record path", d.Kind, d.Default, defGo, f.Name, f.GoType)
			}
			return d.Kind, base, read(base), fmt.Sprintf("{Kind: typed.%s, N: %d, Default: %s, %s}", k, d.N, goLiteralOf(d.Default), val), ""
		}
		return d.Kind, "*" + base, readPtr(base), fmt.Sprintf("{Kind: typed.%s, N: %d, %s}", k, d.N, val), ""
	}
	return "", "", "", "", fmt.Sprintf("window: %s has no typed form", d.Kind)
}

// renderWindowFoldState renders the state struct of one window fold: one
// field per INIT key, in its unified type.
func renderWindowFoldState(stateType string, p streamAggPlan) string {
	var d strings.Builder
	fmt.Fprintf(&d, "// %s is the state of `window -stream-expr` %q (init / every / final).\n", stateType, p.Spec.result)
	fmt.Fprintf(&d, "type %s struct {\n", stateType)
	for _, st := range p.States {
		fmt.Fprintf(&d, "\t%s %s // state %q\n", st.GoName, st.Type, st.Key)
	}
	d.WriteString("}\n")
	return d.String()
}

// renderWindowFoldSpec renders the typed.WindowSpec literal of one fold:
// Init builds the state with its INIT values, Step is ONE simultaneous
// multi-assignment (every RHS reads the old state, as the VM replaces the
// whole object — {a: b, b: a} must swap), Final reads the result.
func renderWindowFoldSpec(stateType, rowType string, p streamAggPlan) string {
	var inits, lhs, rhs []string
	for i, st := range p.States {
		inits = append(inits, fmt.Sprintf("%s: %s", st.GoName, st.Init))
		lhs = append(lhs, "a."+st.GoName)
		rhs = append(rhs, p.Every[i])
	}
	final := p.Final
	if strings.Contains(final, "a.") {
		final = "a := s.(*" + stateType + "); return " + final
	} else {
		final = "return " + final // a constant FINAL reads no state
	}
	return fmt.Sprintf("{Kind: typed.WFold, Init: func() any { return &%s{%s} }, Step: func(s any, r %s) any { a := s.(*%s); %s = %s; return a }, Final: func(s any) any { %s }}",
		stateType, strings.Join(inits, ", "), rowType, stateType, strings.Join(lhs, ", "), strings.Join(rhs, ", "), final)
}

// goLiteralType names the Go type of a CLI literal default.
func goLiteralType(v any) string {
	switch v.(type) {
	case int64:
		return "int64"
	case ssql.Int128:
		return "ssql.Int128"
	case float64:
		return "float64"
	case bool:
		return "bool"
	case string:
		return "string"
	}
	return fmt.Sprintf("%T", v)
}

// goLiteralOf renders a CLI literal default as Go source.
func goLiteralOf(v any) string {
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("%q", x)
	case int64:
		return fmt.Sprintf("int64(%d)", x)
	case ssql.Int128:
		return fmt.Sprintf("ssql.MustParseInt128(%q)", x.String())
	case float64:
		return fmt.Sprintf("float64(%v)", x)
	case bool:
		return fmt.Sprintf("%t", x)
	}
	return fmt.Sprintf("%#v", v)
}
