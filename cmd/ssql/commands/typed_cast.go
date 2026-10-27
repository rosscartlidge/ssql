package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// emitTypedCast generates a typed.Select call that casts the named
// fields to new Go types, producing a derived struct.
func emitTypedCast(inputVar string, in *lib.TypedSchema, casts map[string]ssql.FieldType, invalidMissing bool, formats map[string]string) error {
	// Resolve every cast field, validating existence.
	type castField struct {
		f      lib.TypedSchemaField
		newGoT string // Go type after the cast (e.g. "int64")
		scale  int    // a decimal target's scale (DFC155 E)
	}
	resolvedCasts := make(map[string]castField, len(casts))
	for name, target := range casts {
		f, ok := lookupSchemaField(in, name)
		if !ok {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'cast': unknown field %q", name))
		}
		newGoT, err := castTargetGoType(target)
		if err != nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'cast' on field %q: %w", name, err))
		}
		resolvedCasts[strings.ToLower(name)] = castField{f: f, newGoT: newGoT, scale: target.Scale()}
	}

	// Build derived schema: same fields, with cast Go types replaced.
	derived := &lib.TypedSchema{TypeName: in.TypeName + "Cast"}
	derived.Fields = make([]lib.TypedSchemaField, len(in.Fields))
	for i, f := range in.Fields {
		out := f
		if cf, ok := resolvedCasts[strings.ToLower(f.Name)]; ok {
			out.GoType = cf.newGoT
			out.Scale = cf.scale
		}
		derived.Fields[i] = out
	}

	// Render struct definition.
	var sb strings.Builder
	fmt.Fprintf(&sb, "// %s is the row type after the cast operation.\n", derived.TypeName)
	fmt.Fprintf(&sb, "type %s struct {\n", derived.TypeName)
	maxName := 0
	maxType := 0
	for _, f := range derived.Fields {
		if len(f.GoName) > maxName {
			maxName = len(f.GoName)
		}
		if len(f.GoType) > maxType {
			maxType = len(f.GoType)
		}
	}
	for _, f := range derived.Fields {
		fmt.Fprintf(&sb, "\t%-*s %-*s %s\n", maxName, f.GoName, maxType, f.GoType, lib.FieldTag(f))
	}
	sb.WriteString("}\n")
	structDef := sb.String()

	// Build the merge function body.
	var assigns []string
	var timeImports []string
	usedStrconv := false
	for _, f := range in.Fields {
		out := f
		cf, isCast := resolvedCasts[strings.ToLower(f.Name)]
		if !isCast {
			assigns = append(assigns, fmt.Sprintf("%s: r.%s", out.GoName, out.GoName))
			continue
		}
		// A TEXT field cast to a number or a bool is the one place a value
		// can fail to convert. Strict by default, through the same
		// ssql.CastValue the other lanes use (it panics with a *CastError,
		// which the generated main reports as one Error line). Under
		// -invalid missing the typed lane's "missing" is the zero value
		// (DFC124 §3), which the lenient closures below already produce.
		if f.GoType == "string" && !invalidMissing {
			if target, ok := map[string]string{"int64": "ssql.FieldTypeInt", "float64": "ssql.FieldTypeFloat", "bool": "ssql.FieldTypeBool", "ssql.Int128": "ssql.FieldTypeInt128"}[cf.newGoT]; ok {
				timeImports = append(timeImports, "github.com/rosscartlidge/ssql/v4")
				assigns = append(assigns, fmt.Sprintf("%s: ssql.MustCast[%s](r.%s, %s, %q)", out.GoName, cf.newGoT, f.GoName, target, f.Name))
				continue
			}
		}
		if code, imps, ok := castTimeExpression("r."+f.GoName, f.Name, f.GoType, cf.newGoT, formats[f.Name]); ok {
			timeImports = append(timeImports, imps...)
			assigns = append(assigns, fmt.Sprintf("%s: %s", out.GoName, code))
			continue
		}
		expr, needsStrconv, err := castExpressionTo("r."+f.GoName, f.GoType, cf.newGoT, cf.scale)
		if err != nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: cast %s -> %s: %w", f.GoType, cf.newGoT, err))
		}
		if needsStrconv {
			usedStrconv = true
		}
		assigns = append(assigns, fmt.Sprintf("%s: %s", out.GoName, expr))
	}

	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if usedStrconv {
		imports = append(imports, "strconv")
	}
	if strings.Contains(strings.Join(assigns, ""), "ssql.") || schemaUsesInt128(derived) {
		timeImports = append(timeImports, "github.com/rosscartlidge/ssql/v4") // Int128 conversions and the derived struct's ssql.Int128 fields
	}
	if schemaUsesTime(derived) {
		timeImports = append(timeImports, "time") // the derived struct names time.Time
	}
	for _, imp := range timeImports {
		if !slices.Contains(imports, imp) {
			imports = append(imports, imp)
		}
	}

	// Both templates. A cast is a pure per-row map with no cross-shard
	// state, so the parallel form is typed.StreamSelect (per-shard
	// projection) as update/include/rename/exclude emit; typed.Select is
	// the iter.Seq alternative. A single SerialOnly template here
	// serialised the WHOLE pipeline until 2026-10-28 (DFC158 §2.3): the
	// planner downgraded the source read and the group-by after it, and
	// `cast -type x decimal:2 | group-by` ran 1.29 s where the parallel
	// group-by alone ran 0.15 s.
	closure := fmt.Sprintf(`func(r %s) %s {
		return %s{
			%s,
		}
	}`,
		in.TypeName, derived.TypeName,
		derived.TypeName,
		strings.Join(assigns, ",\n\t\t\t"),
	)
	parallelCode := fmt.Sprintf("casted := typed.StreamSelect(%s, %s)", inputVar, closure)
	serialCode := fmt.Sprintf("casted := typed.Select(%s)(%s)", closure, inputVar)

	frag := lib.NewStmtFragment("casted", inputVar, parallelCode, imports, getCommandString())
	frag.InputTypedSchema = in
	frag.OutputTypedSchema = derived
	frag.StructDefs = []string{structDef}
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return lib.WriteCodeFragment(frag)
}

// castTargetGoType maps an ssql.FieldType to a typed-mode Go type.
// FieldTypeAuto is rejected (the user explicitly asked for a cast).
func castTargetGoType(t ssql.FieldType) (string, error) {
	switch t {
	case ssql.FieldTypeString:
		return "string", nil
	case ssql.FieldTypeInt:
		return "int64", nil
	case ssql.FieldTypeFloat:
		return "float64", nil
	case ssql.FieldTypeBool:
		return "bool", nil
	case ssql.FieldTypeTime:
		return "time.Time", nil
	case ssql.FieldTypeInt128:
		return "ssql.Int128", nil
	}
	if t.IsDecimal() {
		return "ssql.Decimal", nil // the scale rides on the derived field (DFC155 E)
	}
	return "", fmt.Errorf("typed cast does not support FieldType %v", t)
}

// schemaUsesInt128 reports whether any field of the schema is an
// ssql.Int128, which makes the generated file import the root package.
func schemaUsesInt128(s *lib.TypedSchema) bool {
	for _, f := range s.Fields {
		switch strings.TrimPrefix(f.GoType, "*") {
		case "ssql.Int128", "ssql.Decimal":
			return true
		}
	}
	return false
}

// castExpression produces the Go expression that converts `expr`
// (with type `from`) to type `to`. Returns (expression, needsStrconvImport, error).
//
// Same-type casts return the expression unchanged. Otherwise we
// generate the smallest valid conversion: numeric casts are direct,
// string conversions go via strconv, bool↔numeric uses a small
// inline closure.
func castExpression(expr, from, to string) (string, bool, error) {
	if from == to {
		return expr, false, nil
	}
	if to == "ssql.Decimal" {
		return "", false, fmt.Errorf("a decimal target needs its scale (castExpressionTo)")
	}
	switch to {
	case "ssql.Int128":
		// The widening cast (DFC155 C): an integer through Int128From, a
		// float or a string through MustInt128 (a *CastError, never a wrap;
		// under -invalid missing the lenient closure gives the zero value).
		switch from {
		case "int64":
			return fmt.Sprintf("ssql.Int128From(%s)", expr), false, nil
		case "int", "int32":
			return fmt.Sprintf("ssql.Int128From(int64(%s))", expr), false, nil
		case "uint64":
			return fmt.Sprintf("ssql.Int128FromWords(0, %s)", expr), false, nil
		case "float64", "float32":
			return fmt.Sprintf("ssql.MustInt128(float64(%s))", expr), false, nil
		case "bool":
			return fmt.Sprintf("func() ssql.Int128 { if %s { return ssql.Int128From(1) }; return ssql.Int128{} }()", expr), false, nil
		case "string":
			return fmt.Sprintf("func() ssql.Int128 { v, _ := ssql.ParseInt128(%s); return v }()", expr), false, nil
		case "ssql.Decimal":
			return fmt.Sprintf("%s.Trunc()", expr), false, nil
		}
	case "string":
		switch from {
		case "int64":
			return fmt.Sprintf("strconv.FormatInt(%s, 10)", expr), true, nil
		case "ssql.Int128", "ssql.Decimal":
			return fmt.Sprintf("%s.String()", expr), false, nil
		case "int", "int32":
			return fmt.Sprintf("strconv.FormatInt(int64(%s), 10)", expr), true, nil
		case "uint64":
			return fmt.Sprintf("strconv.FormatUint(%s, 10)", expr), true, nil
		case "float64":
			return fmt.Sprintf("strconv.FormatFloat(%s, 'g', -1, 64)", expr), true, nil
		case "float32":
			return fmt.Sprintf("strconv.FormatFloat(float64(%s), 'g', -1, 32)", expr), true, nil
		case "bool":
			return fmt.Sprintf("strconv.FormatBool(%s)", expr), true, nil
		}
	case "int64":
		switch from {
		case "ssql.Int128":
			return fmt.Sprintf("ssql.MustInt64(%s)", expr), false, nil // narrowing never wraps
		case "ssql.Decimal":
			return fmt.Sprintf("ssql.MustInt64(%s.Trunc())", expr), false, nil // toward zero, as a float casts
		case "string":
			return fmt.Sprintf("func() int64 { v, _ := strconv.ParseInt(%s, 10, 64); return v }()", expr), true, nil
		case "int", "int32":
			return fmt.Sprintf("int64(%s)", expr), false, nil
		case "uint64":
			return fmt.Sprintf("int64(%s)", expr), false, nil
		case "float64", "float32":
			return fmt.Sprintf("int64(%s)", expr), false, nil
		case "bool":
			return fmt.Sprintf("func() int64 { if %s { return 1 }; return 0 }()", expr), false, nil
		}
	case "float64":
		switch from {
		case "ssql.Int128", "ssql.Decimal":
			return fmt.Sprintf("%s.Float64()", expr), false, nil
		case "string":
			return fmt.Sprintf("func() float64 { v, _ := strconv.ParseFloat(%s, 64); return v }()", expr), true, nil
		case "int", "int32", "int64", "uint64":
			return fmt.Sprintf("float64(%s)", expr), false, nil
		case "float32":
			return fmt.Sprintf("float64(%s)", expr), false, nil
		case "bool":
			return fmt.Sprintf("func() float64 { if %s { return 1 }; return 0 }()", expr), false, nil
		}
	case "bool":
		switch from {
		case "ssql.Int128", "ssql.Decimal":
			return fmt.Sprintf("!%s.IsZero()", expr), false, nil
		case "string":
			return fmt.Sprintf("func() bool { v, _ := strconv.ParseBool(%s); return v }()", expr), true, nil
		case "int", "int32", "int64", "uint64":
			return fmt.Sprintf("(%s != 0)", expr), false, nil
		case "float64", "float32":
			return fmt.Sprintf("(%s != 0)", expr), false, nil
		}
	}
	return "", false, fmt.Errorf("conversion from %s to %s not implemented", from, to)
}

// castExpressionTo is castExpression with the scale a decimal target
// needs (DFC155 E): an integer exactly at the scale, a float by its
// shortest decimal form, a string by ParseDecimalScale (lenient, as the
// other string casts here are), a decimal brought to the scale; the
// Must forms panic with the overflow, never wrap.
func castExpressionTo(expr, from, to string, scale int) (string, bool, error) {
	if to != "ssql.Decimal" {
		return castExpression(expr, from, to)
	}
	switch from {
	case "ssql.Decimal":
		return fmt.Sprintf("%s.MustRescale(%d)", expr, scale), false, nil
	case "int64":
		return fmt.Sprintf("ssql.DecimalFromInt64(%s, %d)", expr, scale), false, nil
	case "int", "int32":
		return fmt.Sprintf("ssql.DecimalFromInt64(int64(%s), %d)", expr, scale), false, nil
	case "uint64":
		return fmt.Sprintf("ssql.MustDecimalFromInt128(ssql.Int128FromWords(0, %s), %d)", expr, scale), false, nil
	case "ssql.Int128":
		return fmt.Sprintf("ssql.MustDecimalFromInt128(%s, %d)", expr, scale), false, nil
	case "float64", "float32":
		return fmt.Sprintf("ssql.MustDecimalFromFloat(float64(%s), %d)", expr, scale), false, nil
	case "bool":
		return fmt.Sprintf("func() ssql.Decimal { if %s { return ssql.DecimalFromInt64(1, %d) }; return ssql.MustNewDecimal(ssql.Int128{}, %d) }()", expr, scale, scale), false, nil
	case "string":
		return fmt.Sprintf("func() ssql.Decimal { v, _ := ssql.ParseDecimalScale(%s, %d); return v }()", expr, scale), false, nil
	}
	return "", false, fmt.Errorf("conversion from %s to decimal:%d not implemented", from, scale)
}

// castTimeExpression is castExpression's time.Time leg, kept apart because
// it needs the ssql and time imports rather than strconv: a string or
// int64 column parses with ssql.MustParseTime (the one parser every lane
// shares; a value that is not a time stops the program, as in exec), and
// a time column renders as RFC 3339 when cast to string.
func castTimeExpression(expr, field, from, to, pattern string) (code string, imports []string, ok bool) {
	switch {
	case to == "time.Time" && pattern != "" && from == "string":
		// -format: the pattern's layout, strftime or Go (DFC147 §7.2).
		return fmt.Sprintf("ssql.MustParseTimeLayout(%s, %q, %q)", expr, pattern, field), []string{"github.com/rosscartlidge/ssql/v4"}, true
	case to == "time.Time" && (from == "string" || from == "int64"):
		return fmt.Sprintf("ssql.MustParseTime(%s, %q)", expr, field), []string{"github.com/rosscartlidge/ssql/v4"}, true
	case to == "string" && from == "time.Time":
		return fmt.Sprintf("%s.Format(time.RFC3339Nano)", expr), []string{"time"}, true
	}
	return "", nil, false
}
