package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// emitTypedUnion produces typed-mode code for the union command.
// Validates that every additional source has a schema compatible with
// the left side (same field set + Go types), then emits a typed.Concat
// or typed.Union call wired up through subprocess functions.
func emitTypedUnion(inputVar string, leftSchema *lib.TypedSchema, additionalFiles []string, unionAll bool) error {
	// Collect right-side sources as func fragments. All must have the
	// same schema as the left side.
	var sourceCalls []string

	for i, file := range additionalFiles {
		funcName := fmt.Sprintf("unionSource%d", i+1)
		var sourceFragments []*lib.CodeFragment

		fileInfo, statErr := os.Stat(file)
		if statErr == nil && !fileInfo.Mode().IsRegular() {
			// Process-substitution: read fragments from the inner pipeline.
			subFragments, err := lib.ReadCodeFragmentsFromFile(file)
			if err != nil {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: union: reading subprocess fragments: %w", err))
			}
			rightSchema := findOutputSchema(subFragments)
			if rightSchema == nil {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: union: subprocess source did not produce a typed schema (inner pipeline must use typed-mode commands)"))
			}
			if err := assertCompatibleSchemas(leftSchema, rightSchema); err != nil {
				return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: %w", err))
			}
			sourceFragments = subFragments
		} else {
			// Regular file: sample its schema and synthesize an init
			// fragment that reads it.
			if !strings.HasSuffix(strings.ToLower(file), ".csv") {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'union' on non-CSV files not yet supported in typed mode"))
			}
			rightSchema, _, err := lib.SampleCSVSchema(file, leftSchema.TypeName, 0)
			if err != nil {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: %w", err))
			}
			if err := assertCompatibleSchemas(leftSchema, rightSchema); err != nil {
				return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: %w", err))
			}
			// Right side reuses the LEFT struct type (we just verified
			// the schemas match). No new struct definition needed.
			rightInit := lib.NewInitFragment(
				fmt.Sprintf("unionFile%d", i+1),
				fmt.Sprintf("unionFile%d := typed.ReadCSV[%s](%q)", i+1, leftSchema.TypeName, file),
				[]string{"github.com/rosscartlidge/ssql/v4/typed"},
				fmt.Sprintf("ssql from %s", file),
			)
			rightInit.OutputTypedSchema = leftSchema
			sourceFragments = []*lib.CodeFragment{rightInit}
		}

		funcFrag := lib.NewFuncFragment(funcName, sourceFragments, fmt.Sprintf("ssql from %s", file))
		if err := lib.WriteCodeFragment(funcFrag); err != nil {
			return fmt.Errorf("writing func fragment: %w", err)
		}
		sourceCalls = append(sourceCalls, funcName+"()")
	}

	// Build the typed.Concat / typed.Union call.
	var code string
	if unionAll {
		// `union -all`: keep duplicates → typed.Concat.
		args := []string{inputVar}
		args = append(args, sourceCalls...)
		code = fmt.Sprintf("unioned := typed.Concat(%s)", strings.Join(args, ", "))
	} else {
		// `union` (default): dedup by full row → typed.Union with
		// identity key. Requires the row type to be Go-comparable; all
		// supported field types satisfy this.
		args := []string{
			fmt.Sprintf("func(r %s) %s { return r }", leftSchema.TypeName, leftSchema.TypeName),
			inputVar,
		}
		args = append(args, sourceCalls...)
		code = fmt.Sprintf("unioned := typed.Union(%s)", strings.Join(args, ", "))
	}

	frag := lib.NewStmtFragment("unioned", inputVar, code,
		[]string{"github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
	frag.InputTypedSchema = leftSchema
	frag.OutputTypedSchema = leftSchema
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
	return lib.WriteCodeFragment(frag)
}

// assertCompatibleSchemas returns an error when the two schemas don't
// have the same field set (by CSV column name) and the same Go types
// for matching fields. Field order is irrelevant; type-name matching
// is exact (no implicit widening).
func assertCompatibleSchemas(a, b *lib.TypedSchema) error {
	if len(a.Fields) != len(b.Fields) {
		return fmt.Errorf("union: schemas have different field counts (%s: %d, %s: %d)",
			a.TypeName, len(a.Fields), b.TypeName, len(b.Fields))
	}
	bByName := make(map[string]lib.TypedSchemaField, len(b.Fields))
	for _, f := range b.Fields {
		bByName[strings.ToLower(f.Name)] = f
	}
	for _, af := range a.Fields {
		bf, ok := bByName[strings.ToLower(af.Name)]
		if !ok {
			return fmt.Errorf("union: field %q present in %s but absent from %s",
				af.Name, a.TypeName, b.TypeName)
		}
		if af.GoType != bf.GoType {
			return fmt.Errorf("union: field %q has different types (%s.%s: %s, %s.%s: %s)",
				af.Name, a.TypeName, af.GoName, af.GoType, b.TypeName, bf.GoName, bf.GoType)
		}
	}
	return nil
}
