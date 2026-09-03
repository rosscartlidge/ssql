package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func init() {
	// Schema op: source field removed (unless -keep), named groups added.
	registerSchemaOp("extract", func(_ any, in []string, args []string) ([]string, bool) {
		_, flags := walkStage(args, map[string]int{"-field": 1, "-re": 1, "-skip": 0, "-keep": 0, "-generate": 0, "-g": 0})
		var field, re string
		keep := false
		for _, f := range flags {
			switch f.name {
			case "-field":
				if len(f.args) > 0 {
					field = f.args[0]
				}
			case "-re":
				if len(f.args) > 0 {
					re = f.args[0]
				}
			case "-keep":
				keep = true
			}
		}
		_, names, err := ssql.CompileExtract(ssql.ExtractConfig{Pattern: re})
		if err != nil || field == "" {
			return nil, false
		}
		var out []string
		for _, f := range in {
			if f != field || keep {
				out = append(out, f)
			}
		}
		return append(out, names...), true
	})
}

// RegisterExtract registers extract — regex named groups become fields
// (the grep→awk gap: `from lines app.log | extract -field line -re …`).
func RegisterExtract(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	// Order behavior (DFC123 §7): row-local; -skip drops rows but never
	// reads a neighbour — transparent.
	lib.DeclareOrder("extract", lib.OrderTransparent)

	cmd.Subcommand("extract").
		Description("Turn text into fields with a regex: each (?P<name>…) group becomes a string field; non-matching records fail loudly unless -skip").
		Example(`ssql from lines app.log | ssql extract -field line -re '^(?P<ts>\S+) (?P<lvl>\w+) (?P<msg>.*)$' -skip`, "Parse a log: timestamp, level, message become fields; lines that don't match are dropped").
		Example(`ssql from users.csv | ssql extract -field email -re '@(?P<domain>[^@]+)$' -keep`, "Pull the domain out of an email, keeping the original field").
		Example(`ssql from lines access.log | ssql extract -field line -re '" (?P<status>\d{3}) ' -skip | ssql cast -int status | ssql group-by status -count n`, "Captures are strings — cast, then aggregate").

		Flag("-field").
			String().
			FieldsFromFlag("").
			Global().
			Help("Field holding the text to match (required)").
			Done().

		Flag("-re").
			String().
			Completer(cf.NoCompleter{Hint: "<regex with (?P<name>...) groups>"}).
			Global().
			Help("Go regular expression; every NAMED group (?P<name>…) becomes a field (required)").
			Done().

		Flag("-skip").
			Bool().
			Global().
			Help("Drop records whose field does not match (default: a non-match is an error)").
			Done().

		Flag("-keep").
			Bool().
			Global().
			Help("Keep the source field (default: it is replaced by the captures)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			cfg := ssql.ExtractConfig{}
			if v, ok := ctx.GlobalFlags["-field"]; ok {
				cfg.Field, _ = v.(string)
			}
			if v, ok := ctx.GlobalFlags["-re"]; ok {
				cfg.Pattern, _ = v.(string)
			}
			if v, ok := ctx.GlobalFlags["-skip"]; ok {
				cfg.Skip, _ = v.(bool)
			}
			if v, ok := ctx.GlobalFlags["-keep"]; ok {
				cfg.Keep, _ = v.(bool)
			}
			var generate bool
			if v, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = v.(bool)
			}
			if cfg.Field == "" || cfg.Pattern == "" {
				return fmt.Errorf("extract: -field FIELD and -re REGEX are required")
			}
			_, names, err := ssql.CompileExtract(cfg)
			if err != nil {
				return err
			}

			if schemaMode() {
				return runSchemaModeTransform(ctx, "extract")
			}
			if shouldGenerate(generate) {
				return generateExtractCode(cfg, names)
			}

			sr := lib.ReadJSONLWithSchema(ctx.Stdin())
			if sr.Schema != nil {
				if err := validateFieldsSchema(sr.Schema, []string{cfg.Field}, "extract"); err != nil {
					return err
				}
			}
			out, err := ssql.ExtractRecords(sr.Records, cfg)
			if err != nil {
				return err
			}
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), extractOutputSchema(sr.Schema, cfg, names), out); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

func extractOutputSchema(in *lib.Schema, cfg ssql.ExtractConfig, names []string) *lib.Schema {
	if in == nil {
		return nil
	}
	out := &lib.Schema{Types: map[string]string{}}
	for _, f := range in.Fields {
		if f == cfg.Field && !cfg.Keep {
			continue
		}
		out.Fields = append(out.Fields, f)
		if t, ok := in.Types[f]; ok {
			out.Types[f] = t
		}
	}
	for _, n := range names {
		out.Fields = append(out.Fields, n)
		out.Types[n] = lib.TypeString
	}
	return out
}

func extractConfigLiteral(cfg ssql.ExtractConfig) string {
	return fmt.Sprintf("ssql.ExtractConfig{Field: %q, Pattern: %q, Skip: %v, Keep: %v}", cfg.Field, cfg.Pattern, cfg.Skip, cfg.Keep)
}

// generateExtractCode: record codegen always; a typed template when the
// source field is a string in the typed schema — the output struct is
// synthesized (kept fields + one string per named group), the regex is
// compiled once, and a non-match without -skip fails loudly. Serial
// iter.Seq form: the typed Stream has no 1:1 map operator yet.
func generateExtractCode(cfg ssql.ExtractConfig, names []string) error {
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
	var prev *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prev = fragments[len(fragments)-1].OutputTypedSchema
	}
	outputVar := "extracted"
	stamp := func(frag *lib.CodeFragment) {
		if frag.Op != nil {
			frag.Op.Fields = append([]string{cfg.Field}, names...)
			frag.Op.Args = map[string]any{"field": cfg.Field, "re": cfg.Pattern, "skip": cfg.Skip, "keep": cfg.Keep, "names": names}
		}
	}

	if typedMode() && prev != nil {
		src, ok := lookupSchemaField(prev, cfg.Field)
		if !ok {
			return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: 'extract' references unknown field %q", cfg.Field))
		}
		if src.GoType != "string" {
			return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: 'extract' field %q has type %s (need string)", cfg.Field, src.GoType))
		}
		typeName := fmt.Sprintf("Extracted%d", len(fragments))
		out := &lib.TypedSchema{TypeName: typeName}
		var def, copyFields strings.Builder
		fmt.Fprintf(&def, "// %s is the synthesized extract output row (DFC122).\n", typeName)
		fmt.Fprintf(&def, "type %s struct {\n", typeName)
		for _, f := range prev.Fields {
			if f.Name == cfg.Field && !cfg.Keep {
				continue
			}
			fmt.Fprintf(&def, "\t%s %s `ssql:%q`\n", f.GoName, f.GoType, f.Name)
			fmt.Fprintf(&copyFields, "%s: r.%s, ", f.GoName, f.GoName)
			out.Fields = append(out.Fields, f)
		}
		used := map[string]bool{}
		for _, f := range out.Fields {
			used[f.GoName] = true
		}
		var capSets strings.Builder
		for i, n := range names {
			goName := flagVarName(n)
			for used[goName] {
				goName += "_"
			}
			used[goName] = true
			fmt.Fprintf(&def, "\t%s string `ssql:%q`\n", goName, n)
			fmt.Fprintf(&capSets, "%s: m[%d], ", goName, i+1)
			out.Fields = append(out.Fields, lib.TypedSchemaField{Name: n, GoName: goName, GoType: "string"})
		}
		def.WriteString("}")
		// m[i+1]: named groups are emitted in order and the pattern is
		// rewritten so ONLY named groups capture (unnamed → (?:...)).
		var body strings.Builder
		fmt.Fprintf(&body, "%s := func(yield func(%s) bool) {\n", outputVar, typeName)
		fmt.Fprintf(&body, "\t\tre := regexp.MustCompile(%q)\n\t\tfor r := range %s {\n", namedOnlyPattern(cfg.Pattern), inputVar)
		fmt.Fprintf(&body, "\t\t\tm := re.FindStringSubmatch(r.%s)\n\t\t\tif m == nil {\n", src.GoName)
		if cfg.Skip {
			body.WriteString("\t\t\t\tcontinue\n")
		} else {
			fmt.Fprintf(&body, "\t\t\t\tfmt.Fprintf(os.Stderr, \"extract: %%q does not match %%q (use -skip to drop non-matching records)\\n\", r.%s, re.String())\n\t\t\t\tos.Exit(1)\n", src.GoName)
		}
		fmt.Fprintf(&body, "\t\t\t}\n\t\t\tif !yield(%s{%s%s}) {\n\t\t\t\treturn\n\t\t\t}\n\t\t}\n\t}", typeName, copyFields.String(), capSets.String())
		imports := []string{"regexp"}
		if !cfg.Skip {
			imports = append(imports, "fmt", "os")
		}
		frag := lib.NewStmtFragment(outputVar, inputVar, body.String(), imports, getCommandString())
		frag.StructDefs = []string{def.String()}
		frag.InputTypedSchema = prev
		frag.OutputTypedSchema = out
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
		stamp(frag)
		return lib.WriteCodeFragment(frag)
	}

	code := fmt.Sprintf("%s := ssql.ExtractFilter(%s)(%s)", outputVar, extractConfigLiteral(cfg), inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	stamp(frag)
	return lib.WriteCodeFragment(frag)
}

// namedOnlyPattern makes every UNNAMED capturing group non-capturing so
// submatch index i+1 is the i-th named group (what the typed template
// assumes). Escaped parens and character classes are left alone.
func namedOnlyPattern(p string) string {
	var sb strings.Builder
	inClass := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\' && i+1 < len(p):
			sb.WriteByte(c)
			sb.WriteByte(p[i+1])
			i++
		case c == '[' && !inClass:
			inClass = true
			sb.WriteByte(c)
		case c == ']' && inClass:
			inClass = false
			sb.WriteByte(c)
		case c == '(' && !inClass && (i+1 >= len(p) || p[i+1] != '?'):
			sb.WriteString("(?:")
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}
