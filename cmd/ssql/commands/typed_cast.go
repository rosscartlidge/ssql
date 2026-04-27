package commands

import (
	"fmt"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// emitTypedCast generates a typed.Select call that casts the named
// fields to new Go types, producing a derived struct.
func emitTypedCast(inputVar string, in *lib.TypedSchema, casts map[string]ssql.FieldType) error {
	// Resolve every cast field, validating existence.
	type castField struct {
		f       lib.TypedSchemaField
		newGoT  string // Go type after the cast (e.g. "int64")
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
		resolvedCasts[strings.ToLower(name)] = castField{f: f, newGoT: newGoT}
	}

	// Build derived schema: same fields, with cast Go types replaced.
	derived := &lib.TypedSchema{TypeName: in.TypeName + "Cast"}
	derived.Fields = make([]lib.TypedSchemaField, len(in.Fields))
	for i, f := range in.Fields {
		out := f
		if cf, ok := resolvedCasts[strings.ToLower(f.Name)]; ok {
			out.GoType = cf.newGoT
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
		fmt.Fprintf(&sb, "\t%-*s %-*s `ssql:%q`\n", maxName, f.GoName, maxType, f.GoType, f.Name)
	}
	sb.WriteString("}\n")
	structDef := sb.String()

	// Build the merge function body.
	var assigns []string
	usedStrconv := false
	for _, f := range in.Fields {
		out := f
		cf, isCast := resolvedCasts[strings.ToLower(f.Name)]
		if !isCast {
			assigns = append(assigns, fmt.Sprintf("%s: r.%s", out.GoName, out.GoName))
			continue
		}
		expr, needsStrconv, err := castExpression("r."+f.GoName, f.GoType, cf.newGoT)
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

	code := fmt.Sprintf(`casted := typed.Select(func(r %s) %s {
		return %s{
			%s,
		}
	})(%s)`,
		in.TypeName, derived.TypeName,
		derived.TypeName,
		strings.Join(assigns, ",\n\t\t\t"),
		inputVar,
	)

	frag := lib.NewStmtFragment("casted", inputVar, code, imports, getCommandString())
	frag.InputTypedSchema = in
	frag.OutputTypedSchema = derived
	frag.StructDefs = []string{structDef}
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
	default:
		return "", fmt.Errorf("typed cast does not support FieldType %v", t)
	}
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
	switch to {
	case "string":
		switch from {
		case "int64":
			return fmt.Sprintf("strconv.FormatInt(%s, 10)", expr), true, nil
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
