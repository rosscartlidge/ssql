package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToCSV registers the "to csv" subcommand
func registerToCSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("csv").
		Description("Write as CSV file").
		Example("ssql from data.json | ssql to csv output.csv", "Convert JSON to CSV").
		Example("ssql from data.csv | ssql where -if status eq active | ssql to csv active.csv", "Filter and save to CSV").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-sidecar").
			Bool().
			Global().
			Help("Also write datapackage.json beside FILE — a Frictionless Table Schema with the columns' types, which `from csv` reads back (DFC146). An existing package there keeps its other resources").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.csv"}).
			Global().
			Default("").
			Help("Output CSV file (or stdout if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "to") // a sink passes the schema through
			}
			var outputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			sidecar, _ := ctx.GlobalFlags["-sidecar"].(bool)
			if sidecar && outputFile == "" {
				return sidecarNeedsFile("to csv")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToCSVCode(outputFile, sidecar)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Build CSV config with field order from schema if present
			config := ssql.DefaultCSVConfig()
			known := map[string]ssql.FieldType{}
			if schemaAndRecords.Schema != nil {
				config.Fields = schemaAndRecords.Schema.Fields
				for _, f := range config.Fields {
					if t := wireFieldType(schemaAndRecords.Schema.TypeOf(f)); t != ssql.FieldTypeAuto {
						known[f] = t
					}
				}
			}

			// Write as CSV with field order
			switch {
			case outputFile == "":
				return ssql.WriteCSVToWriter(records, ctx.Stdout(), config)
			case sidecar:
				// The header's types are exact; the values fill in what
				// it does not say (a json column's shape).
				return ssql.WriteCSVSidecar(records, outputFile, known, config)
			default:
				return ssql.WriteCSV(records, outputFile, config)
			}
		}).
		Done()
}

// sidecarFieldsLiteral renders a typed schema as the []ssql.TableField
// literal a generated program hands to WriteDatapackage: the struct's
// types are known at generation time (a json column has no shape there:
// `any`).
func sidecarFieldsLiteral(schema *lib.TypedSchema) string {
	var b strings.Builder
	b.WriteString("[]ssql.TableField{")
	for i, f := range schema.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Name: %q, Type: %s}", f.Name, fieldTypeConst(goFieldType(f.GoType, f.JSON)))
	}
	b.WriteString("}")
	return b.String()
}

// sidecarWriteCode is the statement after a typed CSV write that writes
// the package beside the output file.
func sidecarWriteCode(schema *lib.TypedSchema) string {
	return fmt.Sprintf(`
	if err := ssql.WriteDatapackage(*flagOutput, %s); err != nil {
		fmt.Fprintf(os.Stderr, "write sidecar: %%v\n", err)
		os.Exit(1)
	}`, sidecarFieldsLiteral(schema))
}

func generateToCSVCode(filename string, sidecar bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	var prevSchema *lib.TypedSchema
	var prevIsStream bool
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
		prevIsStream = fragments[len(fragments)-1].IsStream
	} else {
		inputVar = "records"
	}

	var code string
	var imports []string
	var params []lib.CodeParam

	// Phase B mixed-mode: prevSchema==nil means the upstream is
	// Record-shaped (a typed→Record boundary inserted by the
	// planner, or a Tier 3 source). Fall through to the record-
	// mode code below, which uses ssql.WriteCSV{,ToWriter}.
	if typedMode() && prevSchema != nil {
		// When the upstream fragment carries a Stream[T] (parallel
		// mode + a Stream-producing op), emit the per-shard buffer
		// sink directly — each shard formats into its own bytes.Buffer
		// in parallel and the final dump is sequential. Avoids the
		// ~100ns/row Serial() fan-in cost that otherwise erases the
		// parallel-filter savings on transform-and-write-everything
		// pipelines. Trade-off: peak memory ~2× output size, output
		// is shard-concatenation order. When the upstream carries
		// iter.Seq[T] (serial typed, or a parallel-mode op like
		// GroupByParallel that fans in to iter.Seq before emitting),
		// we still use the package-level typed.WriteCSV helpers.
		// Emit BOTH templates so the planner can pick. The Stream
		// (per-shard buffer-dump) form is preferred when the
		// upstream produces ShapeStream; the iter.Seq form is the
		// alternative for serial pipelines (or after a planner-
		// inserted Serial() / source downgrade).
		var streamCode, serialCode string
		if filename == "" {
			streamCode = fmt.Sprintf(`if err := %s.WriteCSVToWriter(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
			serialCode = fmt.Sprintf(`if err := typed.WriteCSVToWriter(%s, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
		} else {
			params = append(params, lib.CodeParam{Name: "output", Default: filename, Help: "output CSV file", VarName: "flagOutput"})
			streamCode = fmt.Sprintf(`if err := %s.WriteCSV(*flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
			serialCode = fmt.Sprintf(`if err := typed.WriteCSV(%s, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
			if sidecar {
				streamCode += sidecarWriteCode(prevSchema)
				serialCode += sidecarWriteCode(prevSchema)
			}
		}
		imports = []string{"github.com/rosscartlidge/ssql/v4/typed", "fmt", "os"}
		if sidecar && filename != "" {
			imports = append(imports, "github.com/rosscartlidge/ssql/v4")
		}
		// Default code: pick based on the upstream IsStream flag at
		// emission time. The planner will swap to AltCodeIfSeq if it
		// later determines the upstream is iter.Seq.
		if prevIsStream {
			code = streamCode
		} else {
			code = serialCode
		}
		frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		// Capabilities: this sink accepts whichever the upstream is.
		// Setting Accepts=ShapeStream when emitting the Stream form
		// keeps the planner's reach analysis correct.
		if prevIsStream {
			frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeNone}
			frag.AltCodeIfSeq = serialCode
			frag.AltImportsIfSeq = imports
			frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeNone}
		} else {
			frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeNone}
		}
		return lib.WriteCodeFragment(frag)
	}

	// The writer's error is checked: until 2026-10-05 these two emissions
	// were bare calls, so a record program whose output file could not be
	// created exited 0 with nothing written (DFC142 step 0; the typed
	// emissions above always checked).
	imports = append(imports, "fmt", "os")
	if filename == "" {
		code = fmt.Sprintf(`if err := ssql.WriteCSVToWriter(%s, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
	} else {
		params = append(params, lib.CodeParam{Name: "output", Default: filename, Help: "output CSV file", VarName: "flagOutput"})
		writer := "ssql.WriteCSV(%s, *flagOutput)"
		if sidecar {
			// The package's types are observed from the records as they
			// are written (a record program has no schema header).
			writer = "ssql.WriteCSVSidecar(%s, *flagOutput, nil)"
		}
		code = fmt.Sprintf(`if err := `+writer+`; err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
	}

	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
