package commands

import (
	"fmt"
	"iter"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// ASOF join (DFC137 §2), a mode on `join`: each left row takes the right
// row that is current AS OF its time, the nearest at or before (or with
// -after, at or after) within the same key, the way a trade takes the
// quote in force when it happened. Two series sampled at different
// instants have no equal timestamps, so an equi-join finds nothing.
//
//	ssql from trades.csv | ssql join quotes.csv -using sym -asof ts
//	ssql from events.csv | ssql join readings.csv -asof-on at read_at -tolerance 5m
//
// The equality part (-using / -on) is optional: without one the whole
// right side is one series. Exec and record codegen call ssql.AsofJoin;
// typed emits typed.AsofJoin[Parallel]; generate sql emits DuckDB's
// ASOF JOIN (refused for the other dialects).

// asofSpec is the -asof family of flags, parsed once by the handler.
type asofSpec struct {
	leftTime, rightTime string
	forward, strict     bool
	tolerance           string // as written; parsed by asofTolerance
}

func (a asofSpec) active() bool { return a.leftTime != "" }

// parseAsofSpec reads the -asof flags from the parsed context.
func parseAsofSpec(ctx *cf.Context) (asofSpec, error) {
	var a asofSpec
	if v, ok := ctx.GlobalFlags["-asof"].(string); ok && v != "" {
		a.leftTime, a.rightTime = v, v
	}
	if m, ok := ctx.GlobalFlags["-asof-on"].(map[string]any); ok {
		l, _ := m["left-field"].(string)
		r, _ := m["right-field"].(string)
		if l == "" || r == "" {
			return a, fmt.Errorf("join -asof-on needs <left> <right>")
		}
		if a.leftTime != "" {
			return a, fmt.Errorf("join: -asof and -asof-on are alternatives")
		}
		a.leftTime, a.rightTime = l, r
	}
	a.forward, _ = ctx.GlobalFlags["-after"].(bool)
	a.strict, _ = ctx.GlobalFlags["-strict"].(bool)
	a.tolerance, _ = ctx.GlobalFlags["-tolerance"].(string)
	if !a.active() && (a.forward || a.strict || a.tolerance != "") {
		return a, fmt.Errorf("join: -after, -strict and -tolerance need -asof FIELD or -asof-on LEFT RIGHT")
	}
	return a, nil
}

// asofTolerance parses -tolerance: a number is the distance on a numeric
// axis; a duration (5m, 1h30m, 2d) is nanoseconds on a time axis. A
// number against a time axis is taken as nanoseconds.
func asofTolerance(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if f <= 0 {
			return 0, fmt.Errorf("join -tolerance %s: must be positive", s)
		}
		return f, nil
	}
	d, err := parseDurationWithDays(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("join -tolerance %s: not a positive number or duration (5m, 1h30m, 2d)", s)
	}
	return float64(d), nil
}

// asofConfig builds the library config from the clauses and spec.
func asofConfig(clauses []ssql.LookupClause, a asofSpec, joinType string) (ssql.AsofConfig, error) {
	cfg := ssql.AsofConfig{LeftTime: a.leftTime, RightTime: a.rightTime, Forward: a.forward, Strict: a.strict}
	for _, c := range clauses {
		cfg.LeftKeys = append(cfg.LeftKeys, c.LeftField)
		cfg.RightKeys = append(cfg.RightKeys, c.RightField)
	}
	switch joinType {
	case "", "inner":
	case "left":
		cfg.KeepUnmatched = true
	default:
		return cfg, fmt.Errorf("join -asof: -type %s has no ASOF meaning (inner or left)", joinType)
	}
	tol, err := asofTolerance(a.tolerance)
	if err != nil {
		return cfg, err
	}
	cfg.Tolerance = tol
	return cfg, nil
}

// asofCheck validates what the field lists allow: the time fields exist
// and are numeric or time (order on text is not "as of"), and the merge
// rules hold with the time pair counted as a key (resolveJoin). nil
// field lists are unknown and skipped.
func asofCheck(leftFields, rightFields []string, leftType, rightType func(string) string, clauses []ssql.LookupClause, a asofSpec, opts joinOptions) error {
	if opts.any() {
		return fmt.Errorf("join -asof: -suffix, -exclude-left and -exclude-right are not supported with ASOF; rename or drop columns before or after the join")
	}
	for _, c := range clauses {
		if len(c.FieldRenames) > 0 {
			return fmt.Errorf("join -asof: -as is not supported with ASOF; use `rename` after the join")
		}
	}
	ordered := func(t string) bool {
		return t == "" || t == lib.TypeInt || t == lib.TypeFloat || t == lib.TypeTime
	}
	if leftFields != nil {
		if !fieldListHasOrPath(leftFields, a.leftTime) {
			return fmt.Errorf("join -asof: left field %q not found (available: %s)", a.leftTime, strings.Join(leftFields, ", "))
		}
		if t := leftType(a.leftTime); !ordered(t) {
			return fmt.Errorf("join -asof: %q is %s; the ASOF field must be numeric or time (order on text is not \"as of\") — cast it, or read it with -type %s time", a.leftTime, t, a.leftTime)
		}
	}
	if rightFields != nil {
		if !fieldListHasOrPath(rightFields, a.rightTime) {
			return fmt.Errorf("join -asof: right field %q not found (available: %s)", a.rightTime, strings.Join(rightFields, ", "))
		}
		if t := rightType(a.rightTime); !ordered(t) {
			return fmt.Errorf("join -asof: right field %q is %s; the ASOF field must be numeric or time", a.rightTime, t)
		}
	}
	check := append(append([]ssql.LookupClause{}, clauses...), ssql.LookupClause{LeftField: a.leftTime, RightField: a.rightTime})
	_, err := resolveJoin(leftFields, rightFields, check, joinOptions{})
	return err
}

func noType(string) string { return "" }

// execAsofJoin runs the ASOF join in the interpreter.
func execAsofJoin(ctx *cf.Context, leftRecords iter.Seq[ssql.Record], leftSchema *lib.Schema, rightSeq iter.Seq[ssql.Record], rightSchema *lib.Schema, clauses []ssql.LookupClause, a asofSpec, joinType string, opts joinOptions) error {
	var leftFields []string
	leftType := noType
	if leftSchema != nil {
		leftFields = leftSchema.Fields
		leftType = leftSchema.TypeOf
	}
	if err := asofCheck(leftFields, rightSchema.Fields, leftType, rightSchema.TypeOf, clauses, a, opts); err != nil {
		return err
	}
	cfg, err := asofConfig(clauses, a, joinType)
	if err != nil {
		return err
	}
	joined := ssql.AsofJoin(rightSeq, cfg)(leftRecords)

	var outputSchema *lib.Schema
	if leftSchema != nil {
		outputSchema = lib.NewSchema()
		for _, f := range leftSchema.Fields {
			outputSchema.AddField(f, leftSchema.TypeOf(f))
		}
		for _, f := range rightSchema.Fields {
			if !outputSchema.HasField(f) {
				outputSchema.AddField(f, rightSchema.TypeOf(f))
			}
		}
	}
	if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, joined); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

// asofConfigGo renders the config as Go source for record codegen.
func asofConfigGo(cfg ssql.AsofConfig) string {
	var parts []string
	if len(cfg.LeftKeys) > 0 {
		parts = append(parts, fmt.Sprintf("LeftKeys: []string{%s}", quotedList(cfg.LeftKeys)), fmt.Sprintf("RightKeys: []string{%s}", quotedList(cfg.RightKeys)))
	}
	parts = append(parts, fmt.Sprintf("LeftTime: %q", cfg.LeftTime), fmt.Sprintf("RightTime: %q", cfg.RightTime))
	if cfg.Forward {
		parts = append(parts, "Forward: true")
	}
	if cfg.Strict {
		parts = append(parts, "Strict: true")
	}
	if cfg.Tolerance > 0 {
		parts = append(parts, fmt.Sprintf("Tolerance: %v", cfg.Tolerance))
	}
	if cfg.KeepUnmatched {
		parts = append(parts, "KeepUnmatched: true")
	}
	return "ssql.AsofConfig{" + strings.Join(parts, ", ") + "}"
}

// generateAsofStmt writes the record-mode stmt fragment.
func generateAsofStmt(inputVar, funcName string, cfg ssql.AsofConfig) error {
	code := fmt.Sprintf("joined := ssql.AsofJoin(%s(), %s)(%s)", funcName, asofConfigGo(cfg), inputVar)
	return lib.WriteCodeFragment(lib.NewStmtFragment("joined", inputVar, code, nil, getCommandString()))
}

// emitTypedAsofJoin writes the func + stmt fragments for a typed ASOF
// join: key accessors as typedSetOpKeys renders them (a composite key is
// a struct; int and float widen), the time axis as int64 nanoseconds for
// time fields or the number itself (widened to float64 across int/float),
// and the merged struct of mergeJoinSchemas (a same-named right field is
// dropped, which after asofCheck can only be a key or the time). Dual
// templates: AsofJoinParallel over a Stream, AsofJoin over a Seq.
func emitTypedAsofJoin(inputVar, funcName string, leftSchema, rightSchema *lib.TypedSchema, rightFragments []*lib.CodeFragment, subCommandStr string, clauses []ssql.LookupClause, a asofSpec, joinType string, opts joinOptions) error {
	fail := func(err error) error {
		return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: %w", err))
	}
	typeOf := func(s *lib.TypedSchema) func(string) string {
		return func(name string) string {
			f, ok := lookupSchemaField(s, name)
			if !ok {
				return ""
			}
			switch f.GoType {
			case "int64":
				return lib.TypeInt
			case "float64":
				return lib.TypeFloat
			case "time.Time":
				return lib.TypeTime
			}
			return lib.TypeString
		}
	}
	if err := asofCheck(typedFieldNames(leftSchema), typedFieldNames(rightSchema), typeOf(leftSchema), typeOf(rightSchema), clauses, a, opts); err != nil {
		return fail(err)
	}
	cfg, err := asofConfig(clauses, a, joinType)
	if err != nil {
		return fail(err)
	}
	if cfg.KeepUnmatched {
		return fail(fmt.Errorf("join -asof -type left has no typed form (an unmatched row's right fields are absent, which a struct cannot hold — DFC124 §3); use SSQL_MODE=record"))
	}

	// Key accessors: the equality part, or a unit key for one series.
	leftKey := fmt.Sprintf("func(l %s) struct{} { return struct{}{} }", leftSchema.TypeName)
	rightKey := fmt.Sprintf("func(r %s) struct{} { return struct{}{} }", rightSchema.TypeName)
	if len(cfg.LeftKeys) > 0 {
		leftKey, rightKey, err = typedSetOpKeys(leftSchema, rightSchema, setOpKey{left: cfg.LeftKeys, right: cfg.RightKeys})
		if err != nil {
			return fail(err)
		}
	}
	// Time accessors on one axis type.
	lf, _ := lookupSchemaField(leftSchema, cfg.LeftTime)
	rf, _ := lookupSchemaField(rightSchema, cfg.RightTime)
	axis := func(f lib.TypedSchemaField, v string) (string, string) {
		switch f.GoType {
		case "time.Time":
			return v + "." + f.GoName + ".UnixNano()", "int64"
		case "int64":
			return v + "." + f.GoName, "int64"
		default:
			return v + "." + f.GoName, "float64"
		}
	}
	lExpr, lT := axis(lf, "l")
	rExpr, rT := axis(rf, "r")
	if (lf.GoType == "time.Time") != (rf.GoType == "time.Time") {
		return fail(fmt.Errorf("join -asof: %s is %s and %s is %s; both ASOF fields must be times, or both numbers", lf.Name, lf.GoType, rf.Name, rf.GoType))
	}
	axisT := lT
	if lT != rT {
		axisT = "float64"
		lExpr, rExpr = "float64("+lExpr+")", "float64("+rExpr+")"
	}
	leftTime := fmt.Sprintf("func(l %s) %s { return %s }", leftSchema.TypeName, axisT, lExpr)
	rightTime := fmt.Sprintf("func(r %s) %s { return %s }", rightSchema.TypeName, axisT, rExpr)

	var optParts []string
	if cfg.Forward {
		optParts = append(optParts, "Forward: true")
	}
	if cfg.Strict {
		optParts = append(optParts, "Strict: true")
	}
	if cfg.Tolerance > 0 {
		optParts = append(optParts, fmt.Sprintf("Tolerance: %v", cfg.Tolerance))
	}
	optsGo := "typed.AsofOptions{" + strings.Join(optParts, ", ") + "}"

	mergedSchema, mergedDef := mergeJoinSchemas(leftSchema, rightSchema)
	var assigns []string
	for _, f := range mergedSchema.Fields {
		if lf, ok := lookupSchemaField(leftSchema, f.Name); ok {
			assigns = append(assigns, fmt.Sprintf("%s: l.%s", f.GoName, lf.GoName))
		} else {
			rf, _ := lookupSchemaField(rightSchema, f.Name)
			assigns = append(assigns, fmt.Sprintf("%s: r.%s", f.GoName, rf.GoName))
		}
	}
	merge := fmt.Sprintf("func(l %s, r %s) %s {\n\t\t\treturn %s{\n\t\t\t\t%s,\n\t\t\t}\n\t\t}",
		leftSchema.TypeName, rightSchema.TypeName, mergedSchema.TypeName, mergedSchema.TypeName, strings.Join(assigns, ",\n\t\t\t\t"))

	if err := lib.WriteCodeFragment(lib.NewFuncFragment(funcName, rightFragments, subCommandStr)); err != nil {
		return fmt.Errorf("writing func fragment: %w", err)
	}
	args := strings.Join([]string{funcName + "()", leftKey, rightKey, leftTime, rightTime, optsGo, merge}, ",\n\t\t")
	parallel := fmt.Sprintf("joined := typed.AsofJoinParallel(%s,\n\t\t%s)", inputVar, args)
	serial := fmt.Sprintf("joined := typed.AsofJoin(%s)(%s)", args, inputVar)
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	frag := lib.NewStmtFragment("joined", inputVar, parallel, imports, getCommandString())
	frag.InputTypedSchema = leftSchema
	frag.OutputTypedSchema = mergedSchema
	frag.StructDefs = []string{mergedDef}
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serial
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return lib.WriteCodeFragment(frag)
}
