package commands

import (
	"bufio"
	"fmt"
	"iter"
	"os"
	"os/exec"
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
		Example("ssql merge -catalog shards.csv -by timestamp -- where -if level eq ERROR", "Merge catalog shards with pushdown").

		Flag("FILE").
			String().
			Variadic().
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
			Help("Merge descending (use +desc for ascending)").
			Done().

		Flag("-catalog", "-c").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.csv"}).
			Global().
			Default("").
			Help("Catalog CSV file listing shards (host,path columns)").
			Done().

		Flag("-if").
			Arg("field").
				Completer(&cf.NoCompleter{Hint: "<field>"}).
				Done().
			Arg("op").
				Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt", "ge", "lt", "le", "contains"}}).
				Done().
			Arg("value").
				Completer(&cf.NoCompleter{Hint: "<value>"}).
				Done().
			Accumulate().
			Global().
			Help("Partition pruning filter on catalog metadata: -if date_from le 2024-03-01").
			Done().

		Flag("-gpu").
			Bool().
			Global().
			Help("Use GPU-enabled ssql binary on remote hosts").
			Done().

		Flag("-shard-field").
			String().
			Global().
			Default("").
			Help("Add field with host:path to each record").
			Done().

		Flag("-catalog-used").
			String().
			Global().
			Default("").
			Completer(&cf.FileCompleter{Pattern: "*.csv"}).
			Help("Write expanded catalog (after glob expansion + pruning) to CSV file").
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
			var catalogFile string
			var gpu bool
			var shardField string
			var catalogUsed string

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
					if v != "" {
						files = []string{v}
					}
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
			if catVal, ok := ctx.GlobalFlags["-catalog"]; ok {
				catalogFile = catVal.(string)
			}
			if gpuVal, ok := ctx.GlobalFlags["-gpu"]; ok {
				gpu = gpuVal.(bool)
			}
			if sfVal, ok := ctx.GlobalFlags["-shard-field"]; ok {
				shardField = sfVal.(string)
			}
			if cuVal, ok := ctx.GlobalFlags["-catalog-used"]; ok {
				catalogUsed = cuVal.(string)
			}

			if len(byFields) == 0 {
				return fmt.Errorf("-by fields required for merge")
			}

			// Build OrderField slice
			orderBy := make([]ssql.OrderField, len(byFields))
			for i, f := range byFields {
				orderBy[i] = ssql.OrderField{Field: f, Desc: desc}
			}

			// Catalog mode
			if catalogFile != "" {
				var filters []ssql.CatalogFilter
				if ifVal, ok := ctx.GlobalFlags["-if"]; ok {
					filters = parseCatalogFilters(ifVal)
				}
				if shouldGenerate(generate) {
					frag := lib.NewInitFragment("merged", "", nil, getCommandString())
					return lib.WriteCodeFragment(frag)
				}
				return executeMergeCatalog(catalogFile, filters, orderBy, gpu, shardField, catalogUsed, ctx.RemainingArgs)
			}

			// File mode
			if len(files) == 0 {
				return fmt.Errorf("at least one FILE or -catalog required for merge")
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
				fileSchemaAndRecords := lib.ReadJSONLWithSchema(f)
				if fileSchemaAndRecords.Schema == nil {
					return fmt.Errorf("file %s has no schema header — pipe through ssql first: <(ssql from jsonl %s)", file, file)
				}
				sources = append(sources, fileSchemaAndRecords.Records)
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

// executeMergeCatalog runs a K-way sorted merge across catalog shards.
// Each shard becomes an iter.Seq[Record] source via SSH (or local bash).
// All connections start simultaneously; the merge heap pulls in sort order.
func executeMergeCatalog(catalogFile string, filters []ssql.CatalogFilter, orderBy []ssql.OrderField, gpu bool, shardField string, catalogUsedFile string, pipelineArgs []string) error {
	entries, err := ssql.ReadCatalog(catalogFile)
	if err != nil {
		return fmt.Errorf("reading catalog: %w", err)
	}
	entries = ssql.PruneCatalog(entries, filters)
	entries = ssql.ExpandCatalogGlobs(entries)

	if len(entries) == 0 {
		return fmt.Errorf("no catalog entries match the filter criteria")
	}

	// Write expanded catalog if requested
	if catalogUsedFile != "" {
		f, err := os.Create(catalogUsedFile)
		if err != nil {
			return fmt.Errorf("creating catalog-used file: %w", err)
		}
		if err := ssql.WriteCatalog(f, entries); err != nil {
			f.Close()
			return fmt.Errorf("writing catalog-used: %w", err)
		}
		f.Close()
		fmt.Fprintf(os.Stderr, "Expanded catalog written to %s (%d entries)\n", catalogUsedFile, len(entries))
	}

	remoteBin := sshRemoteBin(gpu)
	localBin, _ := os.Executable()
	if localBin == "" {
		localBin = "ssql"
	}
	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)

	// Create one iterator per shard — each runs an SSH/local subprocess
	sources := make([]iter.Seq[ssql.Record], len(entries))
	var procs []*exec.Cmd

	for i, entry := range entries {
		var proc *exec.Cmd
		if entry.Host == "local" || entry.Host == "localhost" {
			remoteCmd := ssql.BuildRemoteCommand(localBin, entry.Path, entry.Format, pipelineGroups)
			proc = exec.Command("bash", "-c", remoteCmd)
		} else {
			remoteCmd := ssql.BuildRemoteCommand(remoteBin, entry.Path, entry.Format, pipelineGroups)
			proc = exec.Command("ssh", entry.Host, remoteCmd)
		}
		proc.Stderr = os.Stderr

		stdout, err := proc.StdoutPipe()
		if err != nil {
			return fmt.Errorf("shard %s:%s pipe: %w", entry.Host, entry.Path, err)
		}
		if err := proc.Start(); err != nil {
			return fmt.Errorf("shard %s:%s start: %w", entry.Host, entry.Path, err)
		}
		procs = append(procs, proc)

		// Create streaming iterator from subprocess stdout
		entryCapture := entry
		sources[i] = func(yield func(ssql.Record) bool) {
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)

			for scanner.Scan() {
				line := scanner.Bytes()
				if len(line) == 0 {
					continue
				}
				// Skip _schema header lines
				if len(line) > 10 && strings.Contains(string(line[:20]), `"_schema"`) {
					continue
				}

				rec, err := ssql.ParseJSONLine(line)
				if err != nil {
					continue
				}
				r := rec.Freeze()
				if shardField != "" {
					r = r.ToMutable().String(shardField, entryCapture.Host+":"+entryCapture.Path).Freeze()
				}
				if !yield(r) {
					return
				}
			}
		}
	}

	// K-way merge across all shard iterators
	merged := ssql.MergeSorted(orderBy, sources...)
	writeErr := writeWithInferredSchema(merged)

	// Wait for all subprocesses
	for i, proc := range procs {
		if err := proc.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "shard %s:%s: %v\n", entries[i].Host, entries[i].Path, err)
		}
	}

	return writeErr
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

	outputVar := uniqueVarName("merged", fragments)
	code := strings.Join(codeLines, "\n\t")
	var imports []string
	if needsLibImport {
		imports = []string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"}
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
