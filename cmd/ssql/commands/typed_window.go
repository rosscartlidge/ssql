package commands

import (
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
func emitTypedWindow(inputVar string, in *lib.TypedSchema, configs []ssql.WindowConfig) (bool, string, error) {
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
	needFmt := false
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
				parts = append(parts, "fmt.Sprint(r."+f.GoName+")")
			}
			needFmt = true
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
				if f.GoType == "time.Time" {
					fmt.Fprintf(&cmp, "if %s.Before(%s) { return -1 }; if %s.After(%s) { return 1 }; ", lhs, rhs, lhs, rhs)
				} else {
					fmt.Fprintf(&cmp, "if %s < %s { return -1 }; if %s > %s { return 1 }; ", lhs, rhs, lhs, rhs)
				}
			}
			fmt.Fprintf(&b, "\t\t\tHasOrder: true,\n\t\t\tCompare: func(a, b %s) int { %sreturn 0 },\n", in.TypeName, cmp.String())
			if cfg.OrderBy[0].Desc {
				b.WriteString("\t\t\tDesc: true,\n")
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
			if d.Kind == "aggregate" {
				return false, fmt.Sprintf("window: %s over a frame has no typed form yet (registry aggregates run on the record path)", d.Agg.Name), nil
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
			kind, goType, assign, specLit, reason := typedWindowSpec(d, f, in.TypeName, resIdx)
			if reason != "" {
				return false, reason, nil
			}
			_ = kind
			gn := lib.GoNameFromColumn(spec.ResultName)
			for usedGo[gn] {
				gn += "_"
			}
			usedGo[gn] = true
			results = append(results, resultField{goName: gn, goType: goType, assign: strings.ReplaceAll(assign, "GONAME", gn)})
			out.Fields = append(out.Fields, lib.TypedSchemaField{Name: spec.ResultName, GoName: gn, GoType: goType})
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
	fmt.Fprintf(&build, "func(r %s, res []any) %s {\n\t\to := %s{", in.TypeName, outType, outType)
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

	code := fmt.Sprintf("%s := typed.Window([]typed.WindowClause[%s]{\n%s\n\t}, %s)(%s)",
		outVar, in.TypeName, strings.Join(clauseCode, "\n"), build.String(), inputVar)

	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if needFmt {
		imports = append(imports, "fmt")
	}
	frag := lib.NewStmtFragment(outVar, inputVar, code, imports, getCommandString())
	frag.InputTypedSchema = in
	frag.OutputTypedSchema = out
	frag.StructDefs = []string{structDef}
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
	return true, "", lib.WriteCodeFragment(frag)
}

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
	if isNumericGoType(base) {
		if strings.HasPrefix(f.GoType, "*") {
			num = fmt.Sprintf("Num: func(r %s) (float64, bool) { if r.%s == nil { return 0, false }; return float64(*r.%s), true }", rowType, f.GoName, f.GoName)
		} else {
			num = fmt.Sprintf("Num: func(r %s) (float64, bool) { return float64(r.%s), true }", rowType, f.GoName)
		}
	}
	res := fmt.Sprintf("res[%d]", i)
	switch d.Kind {
	case "row_number", "rank", "dense_rank", "count":
		k := map[string]string{"row_number": "WRowNumber", "rank": "WRank", "dense_rank": "WDenseRank", "count": "WCount"}[d.Kind]
		return d.Kind, "int64", fmt.Sprintf("if v, ok := %s.(int64); ok { o.GONAME = v }", res), fmt.Sprintf("{Kind: typed.%s}", k), ""
	case "ntile":
		return d.Kind, "int64", fmt.Sprintf("if v, ok := %s.(int64); ok { o.GONAME = v }", res), fmt.Sprintf("{Kind: typed.WNtile, N: %d}", d.N), ""
	case "percent_rank", "cume_dist":
		k := map[string]string{"percent_rank": "WPercentRank", "cume_dist": "WCumeDist"}[d.Kind]
		return d.Kind, "float64", fmt.Sprintf("if v, ok := %s.(float64); ok { o.GONAME = v }", res), fmt.Sprintf("{Kind: typed.%s}", k), ""
	case "sum", "avg":
		if num == "" {
			return "", "", "", "", fmt.Sprintf("window: %s over non-numeric field %q stays on the record path", d.Kind, f.Name)
		}
		k := map[string]string{"sum": "WSum", "avg": "WAvg"}[d.Kind]
		return d.Kind, "float64", fmt.Sprintf("if v, ok := %s.(float64); ok { o.GONAME = v }", res), fmt.Sprintf("{Kind: typed.%s, %s}", k, num), ""
	case "count_field":
		return d.Kind, "int64", fmt.Sprintf("if v, ok := %s.(int64); ok { o.GONAME = v }", res), fmt.Sprintf("{Kind: typed.WCountField, %s}", val), ""
	case "first", "last", "min", "max":
		k := map[string]string{"first": "WFirst", "last": "WLast", "min": "WMin", "max": "WMax"}[d.Kind]
		// Always present within a non-empty frame; keeps the field's type
		// (a nullable field stays nullable).
		if strings.HasPrefix(f.GoType, "*") {
			return d.Kind, f.GoType, fmt.Sprintf("if v, ok := %s.(%s); ok { o.GONAME = &v }", res, base), fmt.Sprintf("{Kind: typed.%s, %s}", k, val), ""
		}
		return d.Kind, f.GoType, fmt.Sprintf("if v, ok := %s.(%s); ok { o.GONAME = v }", res, f.GoType), fmt.Sprintf("{Kind: typed.%s, %s}", k, val), ""
	case "lag", "lead", "nth_value":
		k := map[string]string{"lag": "WLag", "lead": "WLead", "nth_value": "WNth"}[d.Kind]
		if d.Default != nil {
			// A default of the field's own type makes the result non-nullable.
			defGo := goLiteralType(d.Default)
			if defGo != base {
				return "", "", "", "", fmt.Sprintf("window: %s default %v is %s but field %q is %s — stays on the record path", d.Kind, d.Default, defGo, f.Name, f.GoType)
			}
			return d.Kind, base, fmt.Sprintf("if v, ok := %s.(%s); ok { o.GONAME = v }", res, base), fmt.Sprintf("{Kind: typed.%s, N: %d, Default: %s, %s}", k, d.N, goLiteralOf(d.Default), val), ""
		}
		return d.Kind, "*" + base, fmt.Sprintf("if v, ok := %s.(%s); ok { o.GONAME = &v }", res, base), fmt.Sprintf("{Kind: typed.%s, N: %d, %s}", k, d.N, val), ""
	}
	return "", "", "", "", fmt.Sprintf("window: %s has no typed form", d.Kind)
}

// goLiteralType names the Go type of a CLI literal default.
func goLiteralType(v any) string {
	switch v.(type) {
	case int64:
		return "int64"
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
	case float64:
		return fmt.Sprintf("float64(%v)", x)
	case bool:
		return fmt.Sprintf("%t", x)
	}
	return fmt.Sprintf("%#v", v)
}
