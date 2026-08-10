package commands

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
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
		Flag("-shard-order").
		String().
		Global().
		Default("completion").
		Completer(&cf.StaticCompleter{Options: []string{"completion", "catalog"}}).
		Help("Output ordering: 'completion' (default, low memory) or 'catalog' (deterministic, buffers per shard)").
		Done().
		Flag("-shard-concurrency").
		Int().
		Global().
		Default(int64(0)).
		Help("Cap concurrent ssh-pushdown shards (default 0 = uncapped)").
		Done().
		Flag("-keep-going").
		Bool().
		Global().
		Default(false).
		Help("Run all shards to completion on error (default: fail-fast)").
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
			shardOrder, _ := ctx.GlobalFlags["-shard-order"].(string)
			shardConcurrency64, _ := ctx.GlobalFlags["-shard-concurrency"].(int64)
			keepGoing, _ := ctx.GlobalFlags["-keep-going"].(bool)

			if catalogFile == "" {
				return fmt.Errorf("usage: ssql from catalog FILE [-if field op value]...")
			}

			// Parse pruning filters
			var filters []ssql.CatalogFilter
			if ifVal, ok := ctx.GlobalFlags["-if"]; ok {
				filters = parseCatalogFilters(ifVal)
			}

			opts := ssql.CatalogShardOpts{
				Concurrency: int(shardConcurrency64),
				Order:       shardOrder,
				KeepGoing:   keepGoing,
			}

			if shouldGenerate(generate) {
				return generateFromCatalogCode(catalogFile, gpu, filters, shardField, opts, ctx.RemainingArgs)
			}

			return executeFromCatalog(catalogFile, gpu, filters, shardField, catalogUsed, opts, ctx.RemainingArgs)
		}).
		Done()
}

// completeCatalogFile completes catalog CSV files. FileCompleter caches the
// source file PATH (for downstream value sampling); we no longer emit a
// field-NAME cache. Same-command `-if <field>` still completes from the
// catalog file directly via FieldsFromFlag("FILE"); downstream field names
// come from the pipeline-aware path (ssql's Ctrl-O). Previously this replaced
// FileCompleter's directive with the catalog's metadata-column names — a
// cross-pipe name cache that went stale on transforms, now removed.
func completeCatalogFile(ctx cf.CompletionContext) ([]string, error) {
	fc := &cf.FileCompleter{Pattern: "*.csv"}
	return fc.Complete(ctx)
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

// parseCatalogFilters extracts catalog filters from autocli accumulated flag value.
func parseCatalogFilters(ifVal any) []ssql.CatalogFilter {
	var filters []ssql.CatalogFilter
	if ifSlice, ok := ifVal.([]any); ok {
		for _, item := range ifSlice {
			if argMap, ok := item.(map[string]any); ok {
				negated, _ := argMap["_negated"].(bool)
				filters = append(filters, ssql.CatalogFilter{
					Field:    fmt.Sprintf("%v", argMap["field"]),
					Operator: fmt.Sprintf("%v", argMap["operator"]),
					Value:    fmt.Sprintf("%v", argMap["value"]),
					Negated:  negated,
				})
			}
		}
	}
	return filters
}

// executeFromCatalog reads all shards in a catalog, applying pruning and optional push-down.
//
// opts only affects the v4.43 codegen-symmetric remote-Go path; the
// CLI baseline (this function) still uses the v4.27 sequential
// ProcessCatalogShards. Phase C of the catalog-remote-Go proposal
// extends per-shard parallelism to this path too.
func executeFromCatalog(catalogFile string, gpu bool, filters []ssql.CatalogFilter, shardField string, catalogUsedFile string, opts ssql.CatalogShardOpts, pipelineArgs []string) error {
	_ = opts // unused in CLI baseline — Phase C
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

// generateFromCatalogCode emits an init fragment that orchestrates
// ship-and-run across catalog shards. v4.43 codegen-symmetric design
// (see catalog-remote-go-proposal.md):
//
// Each shard runs the same mode the local pipeline runs in. The local
// generated Go embeds the per-shard pushdown stages as a const slice,
// reads the catalog at runtime, prunes, and calls
// ssql.ProcessCatalogShardsRemoteGo. The function builds the per-
// shard .ssql script, ships+runs it on each host (concurrently by
// default), and returns iter.Seq[Record] of the merged output.
//
// The `# require: vX.Y.Z` directive baked into each shipped script
// catches version-skew on stale shards before any records flow.
//
// gpu is currently unused in the codegen path — ssql vs ssql_gpu is
// a runtime choice on the remote (the shipped script always invokes
// `ssql generate go -script ... -run`).
func generateFromCatalogCode(catalogFile string, gpu bool, filters []ssql.CatalogFilter, shardField string, opts ssql.CatalogShardOpts, pipelineArgs []string) error {
	_ = gpu

	var params []lib.CodeParam
	params = append(params, lib.CodeParam{Name: "catalog", Default: catalogFile, Help: "catalog CSV file", VarName: "flagCatalog"})

	// Filter values become per-flag CodeParams so the compiled
	// binary can override them at runtime.
	var filterCode string
	if len(filters) > 0 {
		var parts []string
		for _, f := range filters {
			flagName := f.Field + "-" + f.Operator
			varName := "flag" + flagVarName(f.Field) + flagVarName(f.Operator)
			if f.Negated {
				// Distinct flag identity for the +if form — a -if and +if on
				// the same field+op must not collide.
				flagName = "not-" + flagName
				varName = "flagNot" + flagVarName(f.Field) + flagVarName(f.Operator)
			}
			params = append(params, lib.CodeParam{Name: flagName, Default: f.Value, Help: f.Field + " " + f.Operator + " filter", VarName: varName})
			if f.Negated {
				parts = append(parts, fmt.Sprintf(`{Field: %q, Operator: %q, Value: *%s, Negated: true}`, f.Field, f.Operator, varName))
			} else {
				parts = append(parts, fmt.Sprintf(`{Field: %q, Operator: %q, Value: *%s}`, f.Field, f.Operator, varName))
			}
		}
		filterCode = fmt.Sprintf("[]ssql.CatalogFilter{%s}", strings.Join(parts, ", "))
	} else {
		filterCode = "nil"
	}

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

	mode := pipelineModeFromEnv()
	requireVersion := version.Version

	// Add runtime-overridable params for shard-level controls so the
	// compiled binary exposes the same UX as the source CLI.
	concurrencyDefault := strconv.Itoa(opts.Concurrency)
	orderDefault := opts.Order
	if orderDefault == "" {
		orderDefault = "completion"
	}
	keepGoingDefault := strconv.FormatBool(opts.KeepGoing)
	params = append(params,
		lib.CodeParam{Name: "shard-concurrency", Default: concurrencyDefault, Help: "max concurrent shards (0 = uncapped)", VarName: "flagShardConcurrency"},
		lib.CodeParam{Name: "shard-order", Default: orderDefault, Help: "shard output ordering: completion or catalog", VarName: "flagShardOrder"},
		lib.CodeParam{Name: "keep-going", Default: keepGoingDefault, Help: "keep running on shard error", VarName: "flagKeepGoing"},
	)

	code := fmt.Sprintf(`entries, err := ssql.ReadCatalog(*flagCatalog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	entries = ssql.PruneCatalog(entries, %s)
	entries = ssql.ExpandCatalogGlobs(entries)
	shardConcurrency, _ := strconv.Atoi(*flagShardConcurrency)
	keepGoing, _ := strconv.ParseBool(*flagKeepGoing)
	records := ssql.ProcessCatalogShardsRemoteGo(
		entries,
		%q,
		%s,
		%q,
		%q,
		ssql.CatalogShardOpts{
			Concurrency: shardConcurrency,
			Order:       *flagShardOrder,
			KeepGoing:   keepGoing,
		},
	)`,
		filterCode, requireVersion, pipelineCode, mode, shardField,
	)

	imports := []string{"fmt", "os", "strconv"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
