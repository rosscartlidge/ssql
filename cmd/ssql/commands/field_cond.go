package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Field-to-field structured flags (DFC135): `-if-field FIELD OP FIELD` on
// where and update, `-set-field FIELD SOURCE` on update. The value slot of
// -if / -set / -param stays a literal always; these flags carry a field
// name in a slot whose KIND the flag declares, so no value's spelling can
// turn it into a column reference (the @field sigil of DFC101 could).
//
// Exec and record codegen share ssql.FieldOp / ssql.CopyField; typed
// codegen compares struct fields natively and falls back to record on a
// type pairing it cannot express; the SQL lane renders the two columns.

// ifFieldFlag registers -if-field on a subcommand.
func ifFieldFlag(sb *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return sb.
		Flag("-if-field").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("operator").
		Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt", "ge", "lt", "le", "contains", "startswith", "endswith", "regex"}}).
		Done().
		Arg("other").
		FieldsFromFlag("").
		Done().
		Accumulate().
		Local().
		Help("Compare two fields: -if-field <field> <op> <field> (use +if-field to negate); either side absent is false").
		Done()
}

// setFieldFlag registers -set-field on update.
func setFieldFlag(sb *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return sb.
		Flag("-set-field").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("source").
		FieldsFromFlag("").
		Done().
		Accumulate().
		Local().
		Help("Set FIELD to SOURCE's value and type: -set-field <field> <source>; an absent source leaves the field absent").
		Done()
}

// parseFieldConditions reads -if-field / +if-field entries as Conditions
// with FieldRHS set: Value is then the right-hand field's name.
func parseFieldConditions(flagValue any) ([]Condition, error) {
	list, ok := flagValue.([]any)
	if !ok {
		return nil, nil
	}
	var out []Condition
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		field, _ := m["field"].(string)
		op, _ := m["operator"].(string)
		other, _ := m["other"].(string)
		negated, _ := m["_negated"].(bool)
		if field == "" || op == "" || other == "" {
			continue
		}
		if !validOperators[op] {
			return nil, fmt.Errorf("unknown operator %q (valid: eq, ne, gt, ge, lt, le, contains, startswith, endswith, regex)", op)
		}
		out = append(out, Condition{Field: field, Operator: op, Value: other, Negated: negated, FieldRHS: true})
	}
	return out, nil
}

// clauseConditions reads a clause's -if and -if-field conditions together.
func clauseConditions(clause cf.Clause) ([]Condition, error) {
	conds, err := parseConditions(clause.Flags["-if"])
	if err != nil {
		return nil, err
	}
	fields, err := parseFieldConditions(clause.Flags["-if-field"])
	if err != nil {
		return nil, err
	}
	return append(conds, fields...), nil
}

// setFieldOps reads -set-field entries: target, source pairs.
type setFieldOp struct{ target, source string }

func parseSetFields(flagValue any) []setFieldOp {
	list, ok := flagValue.([]any)
	if !ok {
		return nil
	}
	var out []setFieldOp
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["field"].(string)
		s, _ := m["source"].(string)
		if t != "" && s != "" {
			out = append(out, setFieldOp{t, s})
		}
	}
	return out
}

// fieldCondRecordGo renders a field condition for record mode: the shared
// runtime primitive, readable and exactly exec's semantics.
func fieldCondRecordGo(c Condition, recv string) string {
	src := fmt.Sprintf("ssql.FieldOp(%s, %q, %q, %q)", recv, c.Field, c.Operator, c.Value)
	if c.Negated {
		src = "!" + src
	}
	return src
}

// fieldCondTypedGo renders a field condition over typed struct fields.
// ok=false means the pairing has no native form (mixed kinds, a time with
// a string operator); the caller falls back to record mode.
func fieldCondTypedGo(schema *lib.TypedSchema, c Condition) (exprGo, bool, error) {
	l, lok := lookupSchemaField(schema, c.Field)
	r, rok := lookupSchemaField(schema, c.Value)
	if !lok || !rok {
		missing := c.Field
		if lok {
			missing = c.Value
		}
		return exprGo{}, false, fmt.Errorf("ssql generate go -typed: '-if-field' references unknown field %q (schema has %s)", missing, fieldNamesList(schema))
	}
	lhs, lok := typedOperand(l)
	rhs, rok := typedOperand(r)
	if !lok || !rok {
		if l.GoType == "time.Time" && r.GoType == "time.Time" {
			form, ok := map[string]string{
				"eq": "%s.Equal(%s)", "ne": "!%s.Equal(%s)",
				"gt": "%s.After(%s)", "ge": "!%s.Before(%s)",
				"lt": "%s.Before(%s)", "le": "!%s.After(%s)",
			}[c.Operator]
			if !ok {
				return exprGo{}, false, nil
			}
			src := "(" + fmt.Sprintf(form, "r."+l.GoName, "r."+r.GoName) + ")"
			if c.Negated {
				src = "!" + src
			}
			return exprGo{Src: src, Type: exprGoBool}, true, nil
		}
		return exprGo{}, false, nil
	}
	var res exprGo
	var err error
	switch c.Operator {
	case "eq", "ne", "gt", "ge", "lt", "le":
		sym := map[string]string{"eq": "==", "ne": "!=", "gt": ">", "ge": ">=", "lt": "<", "le": "<="}[c.Operator]
		res, err = exprCompare(sym, lhs, rhs, c.Operator == "eq" || c.Operator == "ne")
	case "contains", "startswith", "endswith":
		if lhs.Type != exprGoString || rhs.Type != exprGoString {
			return exprGo{}, false, nil
		}
		fn := map[string]string{"contains": "strings.Contains", "startswith": "strings.HasPrefix", "endswith": "strings.HasSuffix"}[c.Operator]
		res = exprGo{Src: fn + "(" + lhs.Src + ", " + rhs.Src + ")", Type: exprGoBool, Imports: []string{"strings"}}
	case "regex":
		if lhs.Type != exprGoString || rhs.Type != exprGoString {
			return exprGo{}, false, nil
		}
		// The pattern is a row value: compiled per row, as exec does.
		res = exprGo{Src: "exprfn.RegexMatch(" + rhs.Src + ", " + lhs.Src + ")", Type: exprGoBool, Imports: []string{exprfnImport}}
	default:
		return exprGo{}, false, nil
	}
	if err != nil {
		return exprGo{}, false, nil // e.g. int against string: no native form, record decides
	}
	if c.Negated {
		res.Src = "!(" + res.Src + ")"
	}
	return res, true, nil
}

// typedOperand is a struct field as a transpiler operand for the four
// scalar kinds; ok=false for time and anything else.
func typedOperand(f lib.TypedSchemaField) (exprGo, bool) {
	switch f.GoType {
	case "int64", "int", "int32", "uint64":
		return exprGo{Src: "r." + f.GoName, Type: exprGoInt}, true
	case "float64", "float32":
		return exprGo{Src: "r." + f.GoName, Type: exprGoFloat}, true
	case "string":
		return exprGo{Src: "r." + f.GoName, Type: exprGoString}, true
	case "bool":
		return exprGo{Src: "r." + f.GoName, Type: exprGoBool}, true
	}
	return exprGo{}, false
}

// translateFieldCondition renders `a OP b` over two columns for the SQL
// lane. The string operators use forms every dialect has (strpos, left,
// right, length); regex goes through the dialect's own spelling.
func translateFieldCondition(a, op, b string) string {
	qa, qb := quoteIdent(a), quoteIdent(b)
	switch op {
	case "contains":
		return fmt.Sprintf("strpos(%s, %s) > 0", qa, qb)
	case "startswith":
		return fmt.Sprintf("left(%s, length(%s)) = %s", qa, qb, qb)
	case "endswith":
		return fmt.Sprintf("right(%s, length(%s)) = %s", qa, qb, qb)
	case "regex":
		return sqlRegexMatch(qa, qb)
	}
	return fmt.Sprintf("%s %s %s", qa, sqlOperator(op), qb)
}

