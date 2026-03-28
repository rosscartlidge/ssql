package commands

import (
	"fmt"
	"iter"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterMerge registers the merge subcommand
func RegisterMerge(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("merge").
		Description("K-way merge of pre-sorted inputs (streaming, O(K) memory)").
		Example("ssql from sorted1.csv | ssql merge sorted2.jsonl -by timestamp", "Merge two sorted files by timestamp").
		Example("ssql from chunk1.csv | ssql merge chunk2.jsonl chunk3.jsonl -by dept name", "Merge 3 sorted files by multiple fields").
		Example("ssql from data1.csv | ssql merge data2.jsonl -by amount -desc", "Merge sorted descending").
		Flag("FILE").
		String().
		Variadic().
		Required().
		Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl}"}).
		Global().
		Help("Additional pre-sorted JSONL files to merge. For CSV: <(ssql from csv FILE)").
		Done().
		Flag("-by", "-b").
		String().
		Variadic().
		Required().
		FieldsFromFlag("").
		Global().
		Help("Fields to merge by (must match sort order of inputs)").
		Done().
		Flag("-desc", "-d").
		Bool().
		Global().
		Help("Merge descending").
		Done().
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Handler(func(ctx *cf.Context) error {
			var files []string
			var byFields []string
			var desc bool
			var generate bool

			if filesVal, ok := ctx.GlobalFlags["FILE"]; ok {
				switch v := filesVal.(type) {
				case []string:
					files = v
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok {
							files = append(files, s)
						}
					}
				case string:
					files = []string{v}
				}
			}

			if byVal, ok := ctx.GlobalFlags["-by"]; ok {
				switch v := byVal.(type) {
				case []string:
					byFields = v
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok {
							byFields = append(byFields, s)
						}
					}
				case string:
					byFields = []string{v}
				}
			}

			if descVal, ok := ctx.GlobalFlags["-desc"]; ok {
				desc = descVal.(bool)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if len(files) == 0 {
				return fmt.Errorf("at least one FILE required for merge")
			}
			if len(byFields) == 0 {
				return fmt.Errorf("-by fields required for merge")
			}

			// Build OrderField slice
			orderBy := make([]ssql.OrderField, len(byFields))
			for i, f := range byFields {
				orderBy[i] = ssql.OrderField{Field: f, Desc: desc}
			}

			if shouldGenerate(generate) {
				return generateMergeCode(orderBy, files)
			}

			// Read stdin
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			stdinRecords := schemaAndRecords.Records

			// Validate merge fields against schema
			if err := validateFieldsSchema(schemaAndRecords.Schema, byFields, "merge"); err != nil {
				return err
			}

			// Open additional files as iterators
			sources := make([]iter.Seq[ssql.Record], 0, len(files)+1)
			sources = append(sources, stdinRecords)

			for _, file := range files {
				f, err := os.Open(file)
				if err != nil {
					return fmt.Errorf("opening %s: %w", file, err)
				}
				defer f.Close()
				fileRecords := lib.ReadJSONL(f)
				sources = append(sources, fileRecords)
			}

			// Merge
			merged := ssql.MergeSorted(orderBy, sources...)

			// Write output
			if err := lib.WriteJSONLWithSchema(os.Stdout, schemaAndRecords.Schema, merged); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateMergeCode generates Go code for the merge command
func generateMergeCode(orderBy []ssql.OrderField, files []string) error {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	var codeLines []string
	var sourceVars []string
	sourceVars = append(sourceVars, inputVar)
	needsLibImport := false

	for i, file := range files {
		varName := fmt.Sprintf("mergeSource%d", i+1)

		// Check for process substitution (non-regular files)
		fileInfo, statErr := os.Stat(file)
		if statErr == nil && !fileInfo.Mode().IsRegular() {
			fileFragments, err := lib.ReadCodeFragmentsFromFile(file)
			if err == nil && len(fileFragments) > 0 {
				prefix := fmt.Sprintf("merge%d_", i+1)
				newVarNames := make([]string, len(fileFragments))
				varRename := make(map[string]string)

				for j, frag := range fileFragments {
					if frag.Var != "" {
						var newName string
						if j == len(fileFragments)-1 {
							newName = varName
						} else {
							newName = fmt.Sprintf("%s%s_%d", prefix, frag.Var, j)
						}
						newVarNames[j] = newName
						varRename[frag.Var] = newName
					}
				}

				for j, frag := range fileFragments {
					if frag.Input != "" {
						if newInput, ok := varRename[frag.Input]; ok {
							oldInput := frag.Input
							frag.Input = newInput
							frag.Code = strings.Replace(frag.Code, ")("+oldInput+")", ")("+newInput+")", 1)
						}
					}
					if newVarNames[j] != "" {
						oldVar := frag.Var
						newVar := newVarNames[j]
						frag.Var = newVar
						frag.Code = strings.Replace(frag.Code, oldVar+", err :=", newVar+", err :=", 1)
						frag.Code = strings.Replace(frag.Code, oldVar+" :=", newVar+" :=", 1)
						varRename[oldVar] = newVar
					}
					if err := lib.WriteCodeFragment(frag); err != nil {
						return fmt.Errorf("writing fragment from %s: %w", file, err)
					}
				}
				sourceVars = append(sourceVars, varName)
				continue
			}
		}

		// Regular JSONL file
		needsLibImport = true
		sourceVars = append(sourceVars, varName)
		codeLines = append(codeLines, fmt.Sprintf(`%sFile, err := os.Open(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %%v\n", err)
		os.Exit(1)
	}
	defer %sFile.Close()
	%s := lib.ReadJSONL(%sFile)`, varName, file, file, varName, varName, varName))
	}

	// Build OrderField slice literal
	var fields []string
	for _, of := range orderBy {
		fields = append(fields, fmt.Sprintf(`{Field: %q, Desc: %v}`, of.Field, of.Desc))
	}

	codeLines = append(codeLines, fmt.Sprintf("merged := ssql.MergeSorted([]ssql.OrderField{%s}, %s)",
		strings.Join(fields, ", "), strings.Join(sourceVars, ", ")))

	outputVar := "merged"
	code := strings.Join(codeLines, "\n\t")
	var imports []string
	if needsLibImport {
		imports = []string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"}
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
