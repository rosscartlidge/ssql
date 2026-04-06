package commands

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromCatalog(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("catalog").
		Description("Read from a shard catalog (CSV mapping host+path to data files)").
		Example("ssql from catalog shards.csv | ssql to table", "Read all shards in catalog").
		Example("ssql from catalog shards.csv -if date ge 2025-03-01 | ssql to table", "Partition pruning").
		Example("ssql from catalog shards.csv -- where -if status eq error", "Push filter to each shard").

		Flag("FILE").
			String().
			CompleterFunc(completeCatalogFile).
			Global().
			Help("Catalog CSV file (must have host and path columns)").
			Done().

		Flag("-if", "-i").
			Arg("field").
				FieldsFromFlag("FILE").
				Done().
			Arg("operator").
				Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt", "ge", "lt", "le"}}).
				Done().
			Arg("value").
				Completer(cf.CompletionFunc(completeCatalogFilterValue)).
				Done().
			Accumulate().
			Global().
			Help("Partition pruning: skip shards that don't match (uses catalog columns)").
			Done().

		Flag("-gpu").
			Bool().
			Global().
			Default(false).
			Help("Use ssql_gpu on remote machines").
			Done().

		Flag("-shard-field").
			String().
			Global().
			Default("").
			Help("Add a field to each record showing its shard origin (host:path)").
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
			Default(false).
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			catalogFile, _ := ctx.GlobalFlags["FILE"].(string)
			gpu, _ := ctx.GlobalFlags["-gpu"].(bool)
			shardField, _ := ctx.GlobalFlags["-shard-field"].(string)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)
			catalogUsed, _ := ctx.GlobalFlags["-catalog-used"].(string)

			if catalogFile == "" {
				return fmt.Errorf("usage: ssql from catalog FILE [-if field op value]...")
			}

			// Parse pruning filters
			var filters []ssql.CatalogFilter
			if ifVal, ok := ctx.GlobalFlags["-if"]; ok {
				filters = parseCatalogFilters(ifVal)
			}

			if shouldGenerate(generate) {
				return generateFromCatalogCode(catalogFile, gpu, filters, shardField, ctx.RemainingArgs)
			}

			return executeFromCatalog(catalogFile, gpu, filters, shardField, catalogUsed, ctx.RemainingArgs)
		}).
		Done()
}

// completeCatalogFile completes catalog CSV files and emits a field_cache directive
// with the catalog's metadata columns (for -if pruning completion).
// The FileCompleter would cache ALL headers (including host, path, format, fields),
// but -if only needs the metadata columns used for pruning.
func completeCatalogFile(ctx cf.CompletionContext) ([]string, error) {
	// Delegate to FileCompleter for the actual file completion
	fc := &cf.FileCompleter{Pattern: "*.csv"}
	results, err := fc.Complete(ctx)
	if err != nil || len(results) == 0 {
		return results, err
	}

	// If we got a single file match (not a directory), read catalog columns for -if completion
	matches := results
	// Skip any JSON directives from FileCompleter
	for len(matches) > 0 && strings.HasPrefix(matches[0], "{") {
		matches = matches[1:]
	}
	if len(matches) != 1 || strings.HasSuffix(matches[0], "/") {
		return results, nil
	}

	// Read catalog headers, filtering out structural columns
	catalogFields := readCatalogColumns(matches[0])
	if len(catalogFields) == 0 {
		return results, nil
	}

	// Replace FileCompleter's field_cache with catalog metadata columns
	directive := cf.CompletionDirective{
		Type:   "field_cache",
		Fields: catalogFields,
	}
	directiveJSON, err := json.Marshal(directive)
	if err != nil {
		return results, nil
	}

	// Strip any existing JSON directives from FileCompleter, prepend ours
	var clean []string
	for _, r := range results {
		if !strings.HasPrefix(r, "{") {
			clean = append(clean, r)
		}
	}
	return append([]string{string(directiveJSON)}, clean...), nil
}

// completeCatalogFilterValue completes -if value args from catalog CSV data.
// For range fields (where X_from/X_to exist), samples from X_from so users
// can see the value format.
func completeCatalogFilterValue(ctx cf.CompletionContext) ([]string, error) {
	catalogFile, _ := ctx.GlobalFlags["FILE"].(string)
	// Fallback: scan args for the catalog file (positional flags may not be in GlobalFlags during completion)
	if catalogFile == "" {
		for _, arg := range ctx.Args {
			if strings.HasSuffix(arg, ".csv") {
				catalogFile = arg
				break
			}
		}
	}
	if catalogFile == "" {
		return []string{"<value>"}, nil
	}

	// Field name is the first arg of the -if flag.
	// PreviousArgs may not be populated during completion; fall back to
	// scanning ctx.Args for the token after -if/-i.
	field := ""
	if len(ctx.PreviousArgs) >= 1 {
		field = ctx.PreviousArgs[0]
	}
	if field == "" {
		for i, arg := range ctx.Args {
			if (arg == "-if" || arg == "-i") && i+1 < len(ctx.Args) {
				field = ctx.Args[i+1]
				break
			}
		}
	}
	if field == "" {
		return []string{"<value>"}, nil
	}

	f, err := os.Open(catalogFile)
	if err != nil {
		return []string{"<value>"}, nil
	}
	defer f.Close()

	reader := csv.NewReader(f)
	headers, err := reader.Read()
	if err != nil {
		return []string{"<value>"}, nil
	}

	// Find the column: try exact match, then field_from for range fields
	colIdx := -1
	for i, h := range headers {
		if strings.TrimSpace(strings.ToLower(h)) == field {
			colIdx = i
			break
		}
	}
	if colIdx < 0 {
		for i, h := range headers {
			if strings.TrimSpace(strings.ToLower(h)) == field+"_from" {
				colIdx = i
				break
			}
		}
	}
	if colIdx < 0 {
		return []string{"<value>"}, nil
	}

	// Collect unique values
	seen := map[string]bool{}
	var values []string
	for {
		row, err := reader.Read()
		if err != nil {
			break
		}
		if colIdx < len(row) {
			v := strings.TrimSpace(row[colIdx])
			if v != "" && !seen[v] {
				seen[v] = true
				if strings.HasPrefix(strings.ToLower(v), strings.ToLower(ctx.Partial)) {
					values = append(values, v)
				}
			}
		}
	}
	sort.Strings(values)

	if len(values) == 0 {
		return []string{"<value>"}, nil
	}
	return values, nil
}

// readCatalogColumns reads a catalog CSV header and returns the prunable field names.
// Range columns (X_from/X_to) are collapsed to their logical field name (X).
// The "fields" column is excluded (it's a schema hint, not a pruning target).
func readCatalogColumns(catalogFile string) []string {
	f, err := os.Open(catalogFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	headers, err := csv.NewReader(f).Read()
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	var cols []string
	for _, h := range headers {
		h = strings.TrimSpace(strings.ToLower(h))
		if h == "fields" {
			continue
		}
		// Collapse range columns: date_from/date_to → date
		if strings.HasSuffix(h, "_from") {
			h = strings.TrimSuffix(h, "_from")
		} else if strings.HasSuffix(h, "_to") {
			h = strings.TrimSuffix(h, "_to")
		}
		if !seen[h] {
			seen[h] = true
			cols = append(cols, h)
		}
	}
	return cols
}

// parseCatalogFilters extracts catalog filters from autocli accumulated flag value.
func parseCatalogFilters(ifVal any) []ssql.CatalogFilter {
	var filters []ssql.CatalogFilter
	if ifSlice, ok := ifVal.([]any); ok {
		for _, item := range ifSlice {
			if argMap, ok := item.(map[string]any); ok {
				filters = append(filters, ssql.CatalogFilter{
					Field:    fmt.Sprintf("%v", argMap["field"]),
					Operator: fmt.Sprintf("%v", argMap["operator"]),
					Value:    fmt.Sprintf("%v", argMap["value"]),
				})
			}
		}
	}
	return filters
}

// executeFromCatalog reads all shards in a catalog, applying pruning and optional push-down.
func executeFromCatalog(catalogFile string, gpu bool, filters []ssql.CatalogFilter, shardField string, catalogUsedFile string, pipelineArgs []string) error {
	entries, err := ssql.ReadCatalog(catalogFile)
	if err != nil {
		return err
	}

	entries = ssql.PruneCatalog(entries, filters)
	entries = ssql.ExpandCatalogGlobs(entries)
	if len(entries) == 0 {
		return nil
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
	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)

	records := ssql.ProcessCatalogShards(entries, remoteBin, shardField, pipelineGroups)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// generateFromCatalogCode generates Go code for catalog reading.
func generateFromCatalogCode(catalogFile string, gpu bool, filters []ssql.CatalogFilter, shardField string, pipelineArgs []string) error {
	remoteBin := sshRemoteBin(gpu)

	var params []lib.CodeParam
	params = append(params, lib.CodeParam{Name: "catalog", Default: catalogFile, Help: "catalog CSV file", VarName: "flagCatalog"})

	// Build filter code with parameterized values
	var filterCode string
	if len(filters) > 0 {
		var parts []string
		for _, f := range filters {
			// Create a flag for each filter value
			flagName := f.Field + "-" + f.Operator
			varName := "flag" + flagVarName(f.Field) + flagVarName(f.Operator)
			params = append(params, lib.CodeParam{Name: flagName, Default: f.Value, Help: f.Field + " " + f.Operator + " filter", VarName: varName})
			parts = append(parts, fmt.Sprintf(`{Field: %q, Operator: %q, Value: *%s}`, f.Field, f.Operator, varName))
		}
		filterCode = fmt.Sprintf("[]ssql.CatalogFilter{%s}", strings.Join(parts, ", "))
	} else {
		filterCode = "nil"
	}

	// Build pipeline groups code
	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)
	var pipelineCode string
	if len(pipelineGroups) > 0 {
		var groupStrs []string
		for _, group := range pipelineGroups {
			var quotedArgs []string
			for _, arg := range group {
				quotedArgs = append(quotedArgs, fmt.Sprintf("%q", arg))
			}
			groupStrs = append(groupStrs, fmt.Sprintf("{%s}", strings.Join(quotedArgs, ", ")))
		}
		pipelineCode = fmt.Sprintf("[][]string{%s}", strings.Join(groupStrs, ", "))
	} else {
		pipelineCode = "nil"
	}

	code := fmt.Sprintf(`entries, err := ssql.ReadCatalog(*flagCatalog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	entries = ssql.PruneCatalog(entries, %s)
	records := ssql.ProcessCatalogShards(entries, %q, %q, %s)`,
		filterCode, remoteBin, shardField, pipelineCode)

	imports := []string{"fmt", "os"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
