package commands

import (
	"fmt"
	"sort"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func init() {
	// Schema op: ids + name column + value column. Default values (all
	// non-id fields) don't change the OUTPUT shape, so the schema is
	// known whenever -col/-val are.
	registerSchemaOp("unpivot", func(_ any, in []string, args []string) ([]string, bool) {
		_, flags := walkStage(args, map[string]int{"-id": 1, "-value": 1, "-col": 1, "-val": 1, "-generate": 0, "-g": 0})
		col, val := "name", "value"
		var ids []string
		for _, f := range flags {
			switch f.name {
			case "-id":
				ids = append(ids, f.args...)
			case "-col":
				if len(f.args) > 0 {
					col = f.args[0]
				}
			case "-val":
				if len(f.args) > 0 {
					val = f.args[0]
				}
			}
		}
		return append(append([]string(nil), ids...), col, val), true
	})
}

// RegisterUnpivot registers unpivot — pivot's inverse (SQL UNPIVOT;
// "melt" in pandas/tidyverse): fold value columns into name/value rows.
func RegisterUnpivot(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	// Order behavior (DFC123 §7): row-local expansion in input order —
	// neither consumes nor destroys record order.
	lib.DeclareOrder("unpivot", lib.OrderTransparent)

	cmd.Subcommand("unpivot").
		Description("Fold wide columns into name/value rows (SQL UNPIVOT, pivot's inverse; looking for melt? this is it)").
		Example("ssql from sales.csv | ssql unpivot -id product -value jan -value feb -value mar -col month -val revenue", "Twelve month columns become (product, month, revenue) rows").
		Example("ssql from wide.csv | ssql unpivot -id id", "Fold every non-id column into (id, name, value) rows").
		Example("ssql from wide.csv | ssql unpivot -id name -col month -val revenue | ssql pivot -row name -col month -val revenue", "unpivot then pivot round-trips the table").

		Flag("-id").
			String().
			Accumulate().
			FieldsFromFlag("").
			Global().
			Help("Identity field copied to every output row (repeat for several)").
			Done().

		Flag("-value").
			String().
			Accumulate().
			FieldsFromFlag("").
			Global().
			Help("Field to fold into a row (repeat; default: every non-id field, sorted by name)").
			Done().

		Flag("-col").
			String().
			Global().
			Default("name").
			Help("Output column holding the folded field's NAME (default: name)").
			Done().

		Flag("-val").
			String().
			Global().
			Default("value").
			Help("Output column holding the folded field's VALUE (default: value)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			cfg := ssql.UnpivotConfig{NameField: "name", ValueField: "value"}
			cfg.IDs = stringList(ctx.GlobalFlags["-id"])
			cfg.Values = stringList(ctx.GlobalFlags["-value"])
			if v, ok := ctx.GlobalFlags["-col"]; ok && v.(string) != "" {
				cfg.NameField = v.(string)
			}
			if v, ok := ctx.GlobalFlags["-val"]; ok && v.(string) != "" {
				cfg.ValueField = v.(string)
			}
			var generate bool
			if v, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = v.(bool)
			}
			for _, id := range cfg.IDs {
				for _, v := range cfg.Values {
					if id == v {
						return fmt.Errorf("unpivot: %q is both -id and -value", id)
					}
				}
			}
			if cfg.NameField == cfg.ValueField {
				return fmt.Errorf("unpivot: -col and -val must differ (both %q)", cfg.NameField)
			}
			// The output columns must not collide with a kept id (the
			// folded-field name would silently overwrite it — found by
			// the corpus with `-id name` and the default -col name).
			for _, id := range cfg.IDs {
				if id == cfg.NameField {
					return fmt.Errorf("unpivot: -id %q collides with the output name column (-col %q) — pick another -col", id, cfg.NameField)
				}
				if id == cfg.ValueField {
					return fmt.Errorf("unpivot: -id %q collides with the output value column (-val %q) — pick another -val", id, cfg.ValueField)
				}
			}

			if schemaMode() {
				return runSchemaModeTransform(ctx, "unpivot")
			}
			if shouldGenerate(generate) {
				return generateUnpivotCode(cfg)
			}

			sr := lib.ReadJSONLWithSchema(ctx.Stdin())
			if sr.Schema != nil {
				if err := validateFieldsSchema(sr.Schema, append(append([]string(nil), cfg.IDs...), cfg.Values...), "unpivot"); err != nil {
					return err
				}
			}
			out := ssql.UnpivotRecords(sr.Records, cfg)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), unpivotOutputSchema(sr.Schema, cfg), out); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

// stringList reads an Accumulate()d string flag (nil when unset).
func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if x != "" {
			return []string{x}
		}
	}
	return nil
}

func unpivotOutputSchema(in *lib.Schema, cfg ssql.UnpivotConfig) *lib.Schema {
	out := &lib.Schema{Types: map[string]string{}}
	for _, id := range cfg.IDs {
		out.Fields = append(out.Fields, id)
		if in != nil && in.Types != nil {
			if t, ok := in.Types[id]; ok {
				out.Types[id] = t
			}
		}
	}
	out.Fields = append(out.Fields, cfg.NameField, cfg.ValueField)
	out.Types[cfg.NameField] = lib.TypeString
	// The value column's type is the common type of the folded fields
	// when they agree; otherwise it is left untyped (per-row types).
	if in != nil && in.Types != nil {
		vals := cfg.Values
		if len(vals) == 0 {
			isID := map[string]bool{}
			for _, id := range cfg.IDs {
				isID[id] = true
			}
			for _, f := range in.Fields {
				if !isID[f] {
					vals = append(vals, f)
				}
			}
		}
		common := ""
		for _, v := range vals {
			t := in.Types[v]
			if common == "" {
				common = t
			} else if t != common {
				common = ""
				break
			}
		}
		if common != "" {
			out.Types[cfg.ValueField] = common
		}
	}
	return out
}

func unpivotConfigLiteral(cfg ssql.UnpivotConfig) string {
	q := func(ss []string) string {
		var parts []string
		for _, s := range ss {
			parts = append(parts, fmt.Sprintf("%q", s))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("ssql.UnpivotConfig{IDs: []string{%s}, Values: []string{%s}, NameField: %q, ValueField: %q}",
		q(cfg.IDs), q(cfg.Values), cfg.NameField, cfg.ValueField)
}

// generateUnpivotCode emits record codegen always, and a typed template
// when the folded fields share one Go type (or are all numeric →
// float64): the output struct is synthesized, the loop is row-local.
// Mixed types (string + number) cannot be one struct field, so those
// pipelines re-enter record mode here via the planner boundary.
func generateUnpivotCode(cfg ssql.UnpivotConfig) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}
	inputVar := "records"
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	}
	outputVar := "unpivoted"

	stamp := func(frag *lib.CodeFragment) {
		if frag.Op != nil {
			frag.Op.Fields = append(append([]string(nil), cfg.IDs...), cfg.Values...)
			frag.Op.Args = map[string]any{"ids": cfg.IDs, "values": cfg.Values, "col": cfg.NameField, "val": cfg.ValueField}
		}
	}

	if typedMode() && prevSchema != nil {
		if frag, ok := generateUnpivotTyped(cfg, fragments, inputVar, outputVar, prevSchema); ok {
			stamp(frag)
			return lib.WriteCodeFragment(frag)
		}
		// fall through: record-shaped stage (planner inserts the boundary)
	}
	code := fmt.Sprintf("%s := ssql.UnpivotFilter(%s)(%s)", outputVar, unpivotConfigLiteral(cfg), inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	stamp(frag)
	return lib.WriteCodeFragment(frag)
}

// generateUnpivotTyped returns the typed fragment, or ok=false when the
// folded fields have no common Go type. Serial iter.Seq form only: the
// typed Stream runtime has no 1:N operator yet (Where is 1:1), so the
// planner inserts a Serial() boundary upstream — correct, just not
// parallel. TODO(typed): Stream.SelectMany would make this parallel.
func generateUnpivotTyped(cfg ssql.UnpivotConfig, fragments []*lib.CodeFragment, inputVar, outputVar string, prev *lib.TypedSchema) (*lib.CodeFragment, bool) {
	isID := map[string]bool{}
	var idFields []lib.TypedSchemaField
	for _, id := range cfg.IDs {
		f, ok := lookupSchemaField(prev, id)
		if !ok {
			lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: 'unpivot' references unknown field %q", id))
			return nil, false
		}
		isID[id] = true
		idFields = append(idFields, f)
	}
	values := cfg.Values
	if len(values) == 0 {
		for _, f := range prev.Fields {
			if !isID[f.Name] {
				values = append(values, f.Name)
			}
		}
		sort.Strings(values)
	}
	var valFields []lib.TypedSchemaField
	common := ""
	allNumeric := true
	for _, v := range values {
		f, ok := lookupSchemaField(prev, v)
		if !ok {
			lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: 'unpivot' references unknown field %q", v))
			return nil, false
		}
		valFields = append(valFields, f)
		if common == "" {
			common = f.GoType
		} else if f.GoType != common {
			common = "MIXED"
		}
		switch f.GoType {
		case "int64", "float64", "int", "int32", "float32":
		default:
			allNumeric = false
		}
	}
	valType := common
	if common == "MIXED" {
		if !allNumeric {
			return nil, false // heterogeneous → record-shaped stage
		}
		valType = "float64"
	}
	if valType == "" {
		valType = "string" // no value fields at all: degenerate but typed
	}

	typeName := fmt.Sprintf("Unpivoted%d", len(fragments))
	nameGo := "Name"
	valueGo := "Value"
	for _, f := range idFields { // avoid clashing with id Go names
		if f.GoName == nameGo {
			nameGo = "Name_"
		}
		if f.GoName == valueGo {
			valueGo = "Value_"
		}
	}
	var def strings.Builder
	fmt.Fprintf(&def, "// %s is the synthesized unpivot output row (DFC122).\n", typeName)
	fmt.Fprintf(&def, "type %s struct {\n", typeName)
	for _, f := range idFields {
		fmt.Fprintf(&def, "\t%s %s `ssql:%q`\n", f.GoName, f.GoType, f.Name)
	}
	fmt.Fprintf(&def, "\t%s string `ssql:%q`\n", nameGo, cfg.NameField)
	fmt.Fprintf(&def, "\t%s %s `ssql:%q`\n", valueGo, valType, cfg.ValueField)
	def.WriteString("}")

	var idCopy strings.Builder
	for _, f := range idFields {
		fmt.Fprintf(&idCopy, "%s: r.%s, ", f.GoName, f.GoName)
	}
	var body strings.Builder
	fmt.Fprintf(&body, "%s := func(yield func(%s) bool) {\n\t\tfor r := range %s {\n", outputVar, typeName, inputVar)
	for i, f := range valFields {
		val := "r." + f.GoName
		if valType == "float64" && f.GoType != "float64" {
			val = "float64(" + val + ")"
		}
		fmt.Fprintf(&body, "\t\t\tif !yield(%s{%s%s: %q, %s: %s}) {\n\t\t\t\treturn\n\t\t\t}\n", typeName, idCopy.String(), nameGo, values[i], valueGo, val)
	}
	body.WriteString("\t\t}\n\t}")

	outSchema := &lib.TypedSchema{TypeName: typeName}
	for _, f := range idFields {
		outSchema.Fields = append(outSchema.Fields, f)
	}
	outSchema.Fields = append(outSchema.Fields,
		lib.TypedSchemaField{Name: cfg.NameField, GoName: nameGo, GoType: "string"},
		lib.TypedSchemaField{Name: cfg.ValueField, GoName: valueGo, GoType: valType})

	frag := lib.NewStmtFragment(outputVar, inputVar, body.String(), nil, getCommandString())
	frag.StructDefs = []string{def.String()}
	frag.InputTypedSchema = prev
	frag.OutputTypedSchema = outSchema
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
	return frag, true
}
