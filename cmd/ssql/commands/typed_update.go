package commands

import (
	"fmt"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// emitTypedUpdate handles `update` in typed-codegen mode.
// Supports `-if FIELD OP VALUE` clauses (literal operators only) and
// `-set FIELD VALUE` (literal values only). Rejects `-if-expr` and
// `-set-expr` as Tier 3 (would need expression-language → Go).
//
// Multiple clauses use first-match-wins semantics — emitted as an
// if/else if chain. A clause with no `-if` is the "else" arm.
//
// The output type is the input type when every -set targets an
// existing field; otherwise a derived struct with the new fields
// appended is emitted.
func emitTypedUpdate(ctx *cf.Context, inputVar string, in *lib.TypedSchema, fragments []*lib.CodeFragment) error {
	type setOp struct {
		field string
		value string
	}
	type cond struct {
		field   string
		op      string
		value   string
		negated bool // +if
	}
	type updateClause struct {
		conds []cond
		sets  []setOp
	}

	var clauses []updateClause

	for _, clause := range ctx.Clauses {
		var uc updateClause

		// Reject expr-lang.
		if exprs, ok := clause.Flags["-if-expr"]; ok && exprs != nil {
			if list, ok := exprs.([]any); ok && len(list) > 0 {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'update -if-expr' not supported in typed mode (Tier 3); use 'update -if FIELD OP VALUE' or drop -typed"))
			}
		}
		if exprs, ok := clause.Flags["-set-expr"]; ok && exprs != nil {
			if list, ok := exprs.([]any); ok && len(list) > 0 {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'update -set-expr' not supported in typed mode (Tier 3); use 'update -set FIELD LITERAL' or drop -typed"))
			}
		}

		// Parse -if / +if conditions.
		if matchesRaw, ok := clause.Flags["-if"]; ok && matchesRaw != nil {
			matches, _ := matchesRaw.([]any)
			for _, mr := range matches {
				m, _ := mr.(map[string]any)
				field, _ := m["field"].(string)
				op, _ := m["operator"].(string)
				value, _ := m["value"].(string)
				negated, _ := m["_negated"].(bool)
				if field == "" || op == "" {
					continue
				}
				uc.conds = append(uc.conds, cond{field: field, op: op, value: value, negated: negated})
			}
		}

		// Parse -set assignments (literal values only).
		if setOpsRaw, ok := clause.Flags["-set"]; ok && setOpsRaw != nil {
			setList, _ := setOpsRaw.([]any)
			for _, setRaw := range setList {
				setMap, _ := setRaw.(map[string]any)
				field, _ := setMap["field"].(string)
				value, _ := setMap["value"].(string)
				if field == "" {
					continue
				}
				uc.sets = append(uc.sets, setOp{field: field, value: value})
			}
		}

		if len(uc.sets) > 0 {
			clauses = append(clauses, uc)
		}
	}

	if len(clauses) == 0 {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'update' has no -set assignments"))
	}

	// Build derived schema: input fields + any new fields introduced
	// by -set. Type of a new field is inferred from the literal value.
	// Existing fields keep their type (the literal must be assignable).
	derived := &lib.TypedSchema{TypeName: in.TypeName + "Updated"}
	derived.Fields = append(derived.Fields, in.Fields...)
	existing := make(map[string]int, len(in.Fields))
	for i, f := range in.Fields {
		existing[strings.ToLower(f.Name)] = i
	}

	// Track new fields added during this update so we don't double-add.
	newFieldGoNames := make(map[string]string) // CSV name → Go name
	for _, c := range clauses {
		for _, s := range c.sets {
			lower := strings.ToLower(s.field)
			if _, ok := existing[lower]; ok {
				continue
			}
			if _, dup := newFieldGoNames[lower]; dup {
				continue
			}
			goName := lib.GoNameFromColumn(s.field)
			goType := inferLiteralGoType(s.value)
			derived.Fields = append(derived.Fields, lib.TypedSchemaField{
				Name:   s.field,
				GoName: goName,
				GoType: goType,
			})
			newFieldGoNames[lower] = goName
		}
	}

	// If no new fields, output type == input type — reuse it directly
	// rather than emitting an identical "Updated" struct.
	useInputAsOutput := len(derived.Fields) == len(in.Fields)
	if useInputAsOutput {
		derived = in
	}

	// Build the Go body for the update closure. For each clause,
	// emit `if (cond1 && cond2) { return updatedRow }`. The final
	// (no-condition) clause becomes the unconditional return; if no
	// such clause exists, the closure returns the input row unchanged
	// at the end.
	var body strings.Builder
	body.WriteString("\t\tout := ")
	body.WriteString(initialRowConstructor(in, derived))
	body.WriteString("\n")

	for i, c := range clauses {
		hasCond := len(c.conds) > 0
		if hasCond {
			conds := make([]string, 0, len(c.conds))
			for _, cd := range c.conds {
				f, ok := lookupSchemaField(in, cd.field)
				if !ok {
					return lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'update -if': unknown field %q", cd.field))
				}
				expr, err := typedWhereCondition(f, cd.op, cd.value)
				if err != nil {
					return lib.WriteErrorAndExit(getCommandString(), err)
				}
				if cd.negated {
					expr = "!(" + expr + ")"
				}
				conds = append(conds, expr)
			}
			combined := strings.Join(conds, " && ")
			if i == 0 {
				fmt.Fprintf(&body, "\t\tif %s {\n", combined)
			} else {
				fmt.Fprintf(&body, "\t\t} else if %s {\n", combined)
			}
		} else {
			if i == 0 {
				body.WriteString("\t\t{\n") // unconditional first clause — open block
			} else {
				body.WriteString("\t\t} else {\n")
			}
		}
		// Emit the assignments.
		for _, s := range c.sets {
			f, exists := lookupDerivedField(derived, s.field)
			if !exists {
				return fmt.Errorf("internal error: derived schema missing field %q", s.field)
			}
			lit, err := typedLiteral(f.GoType, s.value)
			if err != nil {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: update -set %s = %q: %w", s.field, s.value, err))
			}
			fmt.Fprintf(&body, "\t\t\tout.%s = %s\n", f.GoName, lit)
		}
	}
	body.WriteString("\t\t}\n")
	body.WriteString("\t\treturn out\n")

	// Render the struct definition for new derived schemas.
	var defs []string
	if !useInputAsOutput {
		defs = append(defs, renderUpdateStructDef(derived))
	}

	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if schemaUsesTime(derived) {
		imports = append(imports, "time")
	}

	// Emit BOTH templates. update is a pure per-row map, so the parallel
	// form is typed.StreamSelect (per-shard projection) exactly like
	// include/rename/exclude; typed.Select is the iter.Seq alternative.
	// A single SerialOnly template here used to serialise the WHOLE
	// pipeline — the planner downgraded the source read and every
	// parallel-capable stage downstream because update only spoke Seq.
	closure := fmt.Sprintf(`func(r %s) %s {
%s	}`, in.TypeName, derived.TypeName, body.String())

	outputVar := uniqueVarName("updated", fragments)
	parallelCode := fmt.Sprintf("%s := typed.StreamSelect(%s, %s)", outputVar, inputVar, closure)
	serialCode := fmt.Sprintf("%s := typed.Select(%s)(%s)", outputVar, closure, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, parallelCode, imports, getCommandString())
	frag.InputTypedSchema = in
	frag.OutputTypedSchema = derived
	frag.StructDefs = defs
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return lib.WriteCodeFragment(frag)
}

// initialRowConstructor returns Go source for the "starting point"
// row inside the typed.Select closure. When output type == input,
// it's just `r`. When output is a wider derived struct, copy every
// input field and zero-value the new ones.
func initialRowConstructor(in, out *lib.TypedSchema) string {
	if in.TypeName == out.TypeName {
		return "r"
	}
	var assigns []string
	inByCSV := make(map[string]lib.TypedSchemaField, len(in.Fields))
	for _, f := range in.Fields {
		inByCSV[strings.ToLower(f.Name)] = f
	}
	for _, f := range out.Fields {
		if src, ok := inByCSV[strings.ToLower(f.Name)]; ok {
			assigns = append(assigns, fmt.Sprintf("%s: r.%s", f.GoName, src.GoName))
		}
		// New fields get the zero value (omitted from the literal).
	}
	return fmt.Sprintf("%s{%s}", out.TypeName, strings.Join(assigns, ", "))
}

// lookupDerivedField is like lookupSchemaField but case-insensitive
// over the derived schema's fields.
func lookupDerivedField(s *lib.TypedSchema, name string) (lib.TypedSchemaField, bool) {
	for _, f := range s.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return lib.TypedSchemaField{}, false
}

// inferLiteralGoType makes a best-effort guess at the Go type for a
// new field's literal value. Empty string defaults to "string".
func inferLiteralGoType(value string) string {
	if value == "" {
		return "string"
	}
	// Try parsing in priority order: bool, int64, float64, fall back to string.
	if value == "true" || value == "false" {
		return "bool"
	}
	if isAllDigits(value) || (len(value) > 1 && value[0] == '-' && isAllDigits(value[1:])) {
		return "int64"
	}
	if _, err := parseFloatLoose(value); err == nil {
		return "float64"
	}
	return "string"
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseFloatLoose delegates to strconv.ParseFloat.
func parseFloatLoose(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// renderUpdateStructDef renders the derived struct produced by an
// update that introduces new fields. New fields get the same ssql
// tag as the original CSV column name.
func renderUpdateStructDef(s *lib.TypedSchema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the row type after the typed update.\n", s.TypeName)
	fmt.Fprintf(&b, "type %s struct {\n", s.TypeName)
	maxName := 0
	maxType := 0
	for _, f := range s.Fields {
		if len(f.GoName) > maxName {
			maxName = len(f.GoName)
		}
		if len(f.GoType) > maxType {
			maxType = len(f.GoType)
		}
	}
	for _, f := range s.Fields {
		fmt.Fprintf(&b, "\t%-*s %-*s `ssql:%q`\n", maxName, f.GoName, maxType, f.GoType, f.Name)
	}
	b.WriteString("}\n")
	return b.String()
}
