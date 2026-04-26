package commands

import (
	"fmt"
	"strings"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// emitTypedProjection emits a typed.Select call that projects the
// input schema to a derived struct type. Used by include / exclude /
// rename in typed mode.
//
// Args:
//
//   - cmdName: source command name (for error messages and Go var name)
//   - typeSuffix: the generated struct name will be "<InputTypeName><Suffix>"
//     (e.g. "EmployeesRowSubset", "EmployeesRowRenamed")
//   - inputVar: name of the previous fragment's variable
//   - in: input schema
//   - fields: for include, fields to keep; for exclude, fields to drop;
//     ignored for rename
//   - exclude: when true, fields is the drop-list (exclude); when false,
//     fields is the keep-list (include); ignored when rename != nil
//   - rename: when non-nil, builds an "all fields, with renames"
//     projection (the include/exclude flags are ignored)
func emitTypedProjection(cmdName, typeSuffix, inputVar string, in *lib.TypedSchema, fields []string, exclude bool, rename map[string]string) error {
	derivedSchema, structDef, err := buildDerivedSchema(in, typeSuffix, fields, exclude, rename)
	if err != nil {
		return lib.WriteErrorAndExit(getCommandString(), err)
	}

	// Build merge function body. For each derived field, either copy
	// from the input field of the same Go name, or — for renamed
	// fields — copy from the input's old Go name.
	var assigns []string
	if rename != nil {
		// Map: input GoName -> output GoName. The schemas have the
		// same fields; only the Name (and thus GoName) is different
		// for renamed entries.
		oldToNewIdx := make(map[string]int, len(in.Fields))
		for i, f := range in.Fields {
			oldToNewIdx[f.GoName] = i
		}
		for i, df := range derivedSchema.Fields {
			// derivedSchema.Fields[i] corresponds to input.Fields[i]
			// (we kept order). The input's Go name is in.Fields[i].GoName.
			assigns = append(assigns, fmt.Sprintf("%s: r.%s", df.GoName, in.Fields[i].GoName))
			_ = oldToNewIdx
		}
	} else {
		// Subset: derived field set ⊆ input fields. Map by Go name.
		inGoByCSV := make(map[string]string, len(in.Fields))
		for _, f := range in.Fields {
			inGoByCSV[strings.ToLower(f.Name)] = f.GoName
		}
		for _, df := range derivedSchema.Fields {
			srcGo, ok := inGoByCSV[strings.ToLower(df.Name)]
			if !ok {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: %s: derived field %q has no source in input schema", cmdName, df.Name))
			}
			assigns = append(assigns, fmt.Sprintf("%s: r.%s", df.GoName, srcGo))
		}
	}

	outputVar := cmdName + "ed" // include -> includeed; ugly but safe
	if cmdName == "rename" {
		outputVar = "renamed"
	} else if cmdName == "include" {
		outputVar = "included"
	} else if cmdName == "exclude" {
		outputVar = "excluded"
	}

	code := fmt.Sprintf(`%s := typed.Select(func(r %s) %s {
		return %s{
			%s,
		}
	})(%s)`,
		outputVar,
		in.TypeName, derivedSchema.TypeName,
		derivedSchema.TypeName,
		strings.Join(assigns, ",\n\t\t\t"),
		inputVar,
	)

	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if schemaUsesTime(derivedSchema) {
		imports = append(imports, "time")
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	frag.InputTypedSchema = in
	frag.OutputTypedSchema = derivedSchema
	frag.StructDefs = []string{structDef}
	return lib.WriteCodeFragment(frag)
}

// buildDerivedSchema returns the projected schema plus the Go source
// for its struct declaration.
func buildDerivedSchema(in *lib.TypedSchema, typeSuffix string, fields []string, exclude bool, rename map[string]string) (*lib.TypedSchema, string, error) {
	derived := &lib.TypedSchema{
		TypeName: in.TypeName + typeSuffix,
	}

	if rename != nil {
		// Same field set, with some renamed.
		for _, f := range in.Fields {
			newCSVName, hasRename := rename[f.Name]
			if !hasRename {
				// Try case-insensitive match.
				for k, v := range rename {
					if strings.EqualFold(k, f.Name) {
						newCSVName, hasRename = v, true
						break
					}
				}
			}
			out := f
			if hasRename {
				out.Name = newCSVName
				// Recompute Go name from new CSV name to keep the
				// "<UpperCamel>" convention. We don't have direct
				// access to lib.goNameFromColumn, so reuse via a
				// public helper.
				out.GoName = lib.GoNameFromColumn(newCSVName)
			}
			derived.Fields = append(derived.Fields, out)
		}
	} else {
		// Subset. Either include (keep listed) or exclude (drop listed).
		listed := make(map[string]bool, len(fields))
		for _, f := range fields {
			listed[strings.ToLower(f)] = true
		}
		if !exclude {
			// Verify every requested field exists.
			byName := make(map[string]bool, len(in.Fields))
			for _, f := range in.Fields {
				byName[strings.ToLower(f.Name)] = true
			}
			for _, name := range fields {
				if !byName[strings.ToLower(name)] {
					return nil, "", fmt.Errorf("ssql generate go -typed: include: unknown field %q", name)
				}
			}
		}
		for _, f := range in.Fields {
			keep := true
			present := listed[strings.ToLower(f.Name)]
			if exclude {
				keep = !present
			} else {
				keep = present
			}
			if keep {
				derived.Fields = append(derived.Fields, f)
			}
		}
		if len(derived.Fields) == 0 {
			return nil, "", fmt.Errorf("ssql generate go -typed: %s: projection produced an empty schema", typeSuffix)
		}
	}

	// Render the derived struct (no tags — these are intermediate types,
	// not meant to round-trip through CSV). Tags are added back if a
	// downstream `to csv` writes the result, but that uses field
	// names; tagless is fine.
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the projected row type produced by the typed pipeline.\n", derived.TypeName)
	fmt.Fprintf(&b, "type %s struct {\n", derived.TypeName)
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
		fmt.Fprintf(&b, "\t%-*s %-*s `ssql:%q`\n", maxName, f.GoName, maxType, f.GoType, f.Name)
	}
	b.WriteString("}\n")
	return derived, b.String(), nil
}
