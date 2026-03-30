package commands

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"sort"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterFrom registers the from subcommand with nested format subcommands.
// Supports both explicit format: "ssql from csv data.csv"
// and extension inference: "ssql from data.csv" (bare form).
func RegisterFrom(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	fromCmd := cmd.Subcommand("from").
		Description("Read data from files or command output. Tab-complete the filename to enable field completion in downstream commands (where, group-by, etc).").
		Example("ssql from data.csv | ssql where -if age gt 18", "Read CSV (infers format from extension)").
		Example("ssql from csv data.csv -type zipcode string", "Read CSV with explicit format and type overrides")


	// Format subcommands
	registerFromCSV(fromCmd)
	registerFromTSV(fromCmd)
	registerFromJSON(fromCmd)
	registerFromJSONL(fromCmd)
	registerFromArrow(fromCmd)
	registerFromParquet(fromCmd)
	registerFromWAV(fromCmd)
	registerFromXLSX(fromCmd)

	// Operational subcommands
	registerFromCommand(fromCmd)
	registerFromSSH(fromCmd)
	registerFromCatalog(fromCmd)

	// Bare "from FILE" handler — infers format from extension
	fromCmd.

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.{csv,tsv,json,jsonl,arrow,parquet,wav,xlsx}"}).
			Global().
			Default("").
			Help("Input file (format inferred from extension). Reads JSONL from stdin if not specified.").
			Done().

		Handler(func(ctx *cf.Context) error {
			var files []string
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
					if v != "" {
						files = []string{v}
					}
				}
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Bare form only supports single file
			if len(files) > 1 {
				return fmt.Errorf("bare 'from' only supports a single file — for multiple files use: ssql from csv %s %s ...",
					files[0], files[1])
			}

			inputFile := ""
			if len(files) == 1 {
				inputFile = files[0]
			}

			if inputFile == "" {
				// No file — default to JSONL from stdin
				return executeFromJSON("", generate)
			}

			// Detect format from extension, delegate
			lower := strings.ToLower(inputFile)
			switch {
			case strings.HasSuffix(lower, ".csv"):
				return executeFromCSV(inputFile, nil, "auto", generate)
			case strings.HasSuffix(lower, ".tsv"):
				return executeFromTSV(inputFile, generate)
			case strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".jsonl"):
				return executeFromJSON(inputFile, generate)
			case strings.HasSuffix(lower, ".arrow"):
				return executeFromArrow(inputFile, generate)
			case strings.HasSuffix(lower, ".parquet"):
				return executeFromParquet(inputFile, nil, generate)
			case strings.HasSuffix(lower, ".wav"):
				return executeFromWAV(inputFile, -1, generate)
			case strings.HasSuffix(lower, ".xlsx"):
				return executeFromXLSX(inputFile, "", generate)
			default:
				return executeFromJSON(inputFile, generate)
			}
		}).
		Done()

	fromCmd.Done()
	return cmd
}

// --- Format subcommands ---

func registerFromCSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("csv").
		Description("Read CSV file(s) or stdin").
		Example("ssql from csv data.csv | ssql to table", "Read CSV file").
		Example("ssql from csv *.csv | ssql to table", "Read multiple CSV files").
		Example("ssql from csv *.csv -merge-schemas | ssql to table", "Merge files with different headers").
		Example("ssql from csv data.csv -type zipcode string -type phone string", "Force fields to string").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-merge-schemas").
			Bool().
			Global().
			Help("Allow files with different headers (merge schemas)").
			Done().

		Flag("-source").
			String().
			Global().
			Default("").
			Help("Add field with source filename: -source file").
			Done().

		Flag("-type", "-t").
			Arg("field").
				Completer(cf.NoCompleter{Hint: "<field-name>"}).
				Done().
			Arg("type").
				Completer(&cf.StaticCompleter{Options: []string{"string", "int", "float", "bool", "auto"}}).
				Done().
			Accumulate().
			Global().
			Help("Override type for field: -type zipcode string -type age int").
			Done().

		Flag("-default-type", "-dt").
			String().
			Global().
			Default("auto").
			Completer(&cf.StaticCompleter{Options: []string{"auto", "string", "int", "float", "bool"}}).
			Help("Default type for all fields: auto (default), string, int, float, bool").
			Done().

		Flag("FILE").
			String().
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.csv"}).
			Global().
			Default("").
			Help("Input CSV file(s) (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var files []string
			var generate, mergeSchemas bool
			var defaultType, sourceField string
			typeOverrides := make(map[string]string)

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
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if msVal, ok := ctx.GlobalFlags["-merge-schemas"]; ok {
				mergeSchemas = msVal.(bool)
			}
			if srcVal, ok := ctx.GlobalFlags["-source"]; ok {
				sourceField = srcVal.(string)
			}
			if dtVal, ok := ctx.GlobalFlags["-default-type"]; ok {
				defaultType = dtVal.(string)
			}
			if typeVal, ok := ctx.GlobalFlags["-type"]; ok {
				typeOverrides = parseTypeOverrides(typeVal)
			}

			if len(files) <= 1 {
				inputFile := ""
				if len(files) == 1 {
					inputFile = files[0]
				}
				return executeFromCSV(inputFile, typeOverrides, defaultType, generate)
			}

			return executeFromMultiCSV(files, typeOverrides, defaultType, mergeSchemas, sourceField, generate)
		}).
		Done()
}

func registerFromTSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("tsv").
		Description("Read TSV file(s) or stdin").
		Example("ssql from tsv data.tsv | ssql to table", "Read TSV file").
		Example("ssql from tsv *.tsv | ssql to table", "Read multiple TSV files").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-merge-schemas").
			Bool().
			Global().
			Help("Allow files with different headers (merge schemas)").
			Done().

		Flag("-source").
			String().
			Global().
			Default("").
			Help("Add field with source filename: -source file").
			Done().

		Flag("FILE").
			String().
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.tsv"}).
			Global().
			Default("").
			Help("Input TSV file(s) (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			cfg := extractMultiFileConfig(ctx)

			if len(cfg.files) <= 1 {
				inputFile := ""
				if len(cfg.files) == 1 {
					inputFile = cfg.files[0]
				}
				return executeFromTSV(inputFile, cfg.generate)
			}

			readFile := func(file *os.File) iter.Seq[ssql.Record] {
				return ssql.ReadTSVFromReader(file)
			}
			readHeaders := func(filename string) ([]string, error) {
				file, err := os.Open(filename)
				if err != nil {
					return nil, err
				}
				defer file.Close()
				return readTSVHeaders(file)
			}
			return executeFromMultiFile(cfg, "TSV", readFile, readHeaders)
		}).
		Done()
}

func registerFromJSON(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("json").
		Description("Read JSON file(s) or stdin").
		Example("ssql from json data.json | ssql to table", "Read JSON file").
		Example("ssql from json *.json | ssql to table", "Read multiple JSON files").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-merge-schemas").
			Bool().
			Global().
			Help("Allow files with different fields (merge schemas)").
			Done().

		Flag("-source").
			String().
			Global().
			Default("").
			Help("Add field with source filename: -source file").
			Done().

		Flag("FILE").
			String().
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.json"}).
			Global().
			Default("").
			Help("Input JSON file(s) (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			cfg := extractMultiFileConfig(ctx)

			if len(cfg.files) <= 1 {
				inputFile := ""
				if len(cfg.files) == 1 {
					inputFile = cfg.files[0]
				}
				return executeFromJSON(inputFile, cfg.generate)
			}

			readFile := func(file *os.File) iter.Seq[ssql.Record] {
				return readJSONSchemaAware(file)
			}
			return executeFromMultiFile(cfg, "JSON", readFile, nil)
		}).
		Done()
}

func registerFromJSONL(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("jsonl").
		Description("Read JSONL file(s) or stdin").
		Example("ssql from jsonl data.jsonl | ssql to table", "Read JSONL file").
		Example("ssql from jsonl *.jsonl | ssql to table", "Read multiple JSONL files").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-merge-schemas").
			Bool().
			Global().
			Help("Allow files with different fields (merge schemas)").
			Done().

		Flag("-source").
			String().
			Global().
			Default("").
			Help("Add field with source filename: -source file").
			Done().

		Flag("FILE").
			String().
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Default("").
			Help("Input JSONL file(s) (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			cfg := extractMultiFileConfig(ctx)

			if len(cfg.files) <= 1 {
				inputFile := ""
				if len(cfg.files) == 1 {
					inputFile = cfg.files[0]
				}
				return executeFromJSON(inputFile, cfg.generate)
			}

			readFile := func(file *os.File) iter.Seq[ssql.Record] {
				return readJSONSchemaAware(file)
			}
			return executeFromMultiFile(cfg, "JSONL", readFile, nil)
		}).
		Done()
}

func registerFromArrow(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("arrow").
		Description("Read Arrow file or stdin").
		Example("ssql from arrow data.arrow | ssql to table", "Read Arrow file (10-20x faster than CSV)").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.arrow"}).
			Global().
			Default("").
			Help("Input Arrow file (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			return executeFromArrow(inputFile, generate)
		}).
		Done()
}

func registerFromParquet(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("parquet").
		Description("Read Parquet file (columnar format, DuckDB compatible)").
		Example("ssql from parquet data.parquet | ssql to table", "Read Parquet file").
		Example("ssql from parquet data.parquet -columns name -columns age -columns dept | ssql to table", "Read only selected columns (faster for wide files)").
		Example("ssql from parquet data.parquet | ssql to csv output.csv", "Convert Parquet to CSV").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.parquet"}).
			Global().
			Required().
			Help("Input Parquet file (random access required, no stdin)").
			Done().

		Flag("-columns", "-c").
			Arg("column").
				FieldsFromFlag("FILE").
				Done().
			Accumulate().
			Global().
			Help("Column to read (repeat for multiple). Omit for all columns. Reduces I/O for wide files.").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Extract accumulated column names
			var columns []string
			if colVal, ok := ctx.GlobalFlags["-columns"]; ok && colVal != nil {
				if colSlice, ok := colVal.([]any); ok {
					for _, v := range colSlice {
						if s, ok := v.(string); ok && s != "" {
							columns = append(columns, s)
						}
					}
				}
			}

			return executeFromParquet(inputFile, columns, generate)
		}).
		Done()
}

func registerFromWAV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("wav").
		Description("Read WAV audio file").
		Example("ssql from wav audio.wav | ssql fft -field amplitude", "Read WAV for FFT analysis").
		Example("ssql from wav stereo.wav -channel 0 | ssql to table", "Read left channel only").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-channel", "-ch").
			Int().
			Global().
			Default(-1).
			Help("Extract specific channel (0=left, 1=right). Default: mix to mono.").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.wav"}).
			Global().
			Default("").
			Help("Input WAV file").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool
			var channel int = -1

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if chVal, ok := ctx.GlobalFlags["-channel"]; ok {
				channel = chVal.(int)
			}

			return executeFromWAV(inputFile, channel, generate)
		}).
		Done()
}

func registerFromXLSX(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("xlsx").
		Description("Read Excel spreadsheet").
		Example("ssql from xlsx data.xlsx | ssql to table", "Read Excel spreadsheet").
		Example("ssql from xlsx workbook.xlsx -sheet Sales | ssql to csv", "Read specific sheet").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-sheet").
			String().
			Global().
			Default("").
			Help("Sheet name to read (default: first sheet)").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.xlsx"}).
			Global().
			Help("Input XLSX file (required — XLSX cannot be read from stdin)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool
			var sheet string

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if sheetVal, ok := ctx.GlobalFlags["-sheet"]; ok {
				sheet = sheetVal.(string)
			}

			if inputFile == "" {
				return fmt.Errorf("XLSX format cannot be read from stdin (it requires random file access); use a file path")
			}

			return executeFromXLSX(inputFile, sheet, generate)
		}).
		Done()
}

// --- Operational subcommands ---

func registerFromCommand(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("command").
		Description("Execute a command and read its output").
		Example("ssql from command -- ps aux | ssql where -if USER eq root", "Parse ps output").
		Example("ssql from command -- docker ps | ssql to table", "Parse docker output").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if len(ctx.RemainingArgs) == 0 {
				return fmt.Errorf("usage: ssql from command -- <command> [args...]")
			}

			command := ctx.RemainingArgs[0]
			args := ctx.RemainingArgs[1:]

			if shouldGenerate(generate) {
				return generateFromExecCode(command, args)
			}

			records, err := ssql.ExecCommand(command, args)
			if err != nil {
				return fmt.Errorf("executing command: %w", err)
			}

			return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
		}).
		Done()
}

func registerFromSSH(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("ssh").
		Description("Read from a remote file via SSH. Tab-complete the path to enable field completion in downstream commands.").
		Example("ssql from ssh server /data/logs.csv | ssql to table", "Read remote CSV").
		Example("ssql from ssh server /data/logs.csv -- where -if status eq error", "Push filter to remote").
		Example("ssql from ssh server /data/logs.csv -- where -if age gt 25 + group-by -field dept -count", "Push multi-step pipeline to remote").

		Flag("HOST").
			String().
			CompleterFunc(completeSSHHost).
			Global().
			Help("SSH host (from ~/.ssh/config or user@host)").
			Done().

		Flag("PATH").
			String().
			CompleterFunc(completeSSHPath).
			Global().
			Help("Remote file path").
			Done().

		Flag("-gpu").
			Bool().
			Global().
			Default(false).
			Help("Use ssql_gpu on the remote machine").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Default(false).
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			host, _ := ctx.GlobalFlags["HOST"].(string)
			path, _ := ctx.GlobalFlags["PATH"].(string)
			gpu, _ := ctx.GlobalFlags["-gpu"].(bool)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)

			if host == "" || path == "" {
				return fmt.Errorf("usage: ssql from ssh HOST PATH [-- <remote-pipeline>]")
			}

			// If RemainingArgs present (after --), it's a push-down pipeline
			if len(ctx.RemainingArgs) > 0 {
				if shouldGenerate(generate) {
					return generateFromSSHRemoteCode(host, path, gpu, ctx.RemainingArgs)
				}
				return executeFromSSHRemote(host, path, gpu, ctx.RemainingArgs)
			}

			// Simple remote read
			if shouldGenerate(generate) {
				return generateFromSSHCode(host, path, gpu)
			}
			return executeFromSSH(host, path, gpu)
		}).
		Done()
}

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

			return executeFromCatalog(catalogFile, gpu, filters, shardField, ctx.RemainingArgs)
		}).
		Done()
}

// completeSSHHost completes SSH host names from ~/.ssh/config and warms
// the connection when a single host is matched.
func completeSSHHost(ctx cf.CompletionContext) ([]string, error) {
	hosts := parseSSHConfigHosts()
	if len(hosts) == 0 {
		return []string{"<host>"}, nil
	}

	var matches []string
	partial := strings.ToLower(ctx.Partial)
	for _, h := range hosts {
		if strings.HasPrefix(strings.ToLower(h), partial) {
			matches = append(matches, h)
		}
	}

	// Single match — warm the SSH connection in the background
	if len(matches) == 1 {
		exec.Command("ssh", "-o", "ConnectTimeout=3", "-N", "-f", matches[0]).Start()
	}

	if len(matches) == 0 {
		return []string{"<host>"}, nil
	}
	return matches, nil
}

// parseSSHConfigHosts reads Host entries from ~/.ssh/config.
func parseSSHConfigHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	f, err := os.Open(home + "/.ssh/config")
	if err != nil {
		return nil
	}
	defer f.Close()

	var hosts []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToLower(line), "host ") {
			for _, h := range strings.Fields(line)[1:] {
				// Skip wildcards and patterns
				if strings.ContainsAny(h, "*?") {
					continue
				}
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// completeSSHPath completes the PATH arg for `from ssh` and emits a field_cache
// directive by SSH-fetching the first line of the remote file.
func completeSSHPath(ctx cf.CompletionContext) ([]string, error) {
	host, _ := ctx.GlobalFlags["HOST"].(string)
	if host == "" || ctx.Partial == "" {
		return []string{"<remote-path>"}, nil
	}

	// Try to fetch the CSV header from the remote file
	cmd := exec.Command("ssh", "-o", "ConnectTimeout=2", "-o", "BatchMode=yes", host,
		"/usr/bin/head -1 "+ssql.ShellQuote(ctx.Partial))
	out, err := cmd.Output()
	if err != nil {
		// SSH failed (host down, file not found, etc.) — just return the partial
		return []string{ctx.Partial}, nil
	}

	header := strings.TrimSpace(string(out))
	if header == "" {
		return []string{ctx.Partial}, nil
	}

	// Parse as CSV header
	reader := csv.NewReader(strings.NewReader(header))
	fields, err := reader.Read()
	if err != nil || len(fields) == 0 {
		return []string{ctx.Partial}, nil
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}

	// Emit field_cache directive for downstream commands
	directive := cf.CompletionDirective{
		Type:   "field_cache",
		Fields: fields,
	}
	directiveJSON, err := json.Marshal(directive)
	if err != nil {
		return []string{ctx.Partial}, nil
	}
	return []string{string(directiveJSON), ctx.Partial}, nil
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
func executeFromCatalog(catalogFile string, gpu bool, filters []ssql.CatalogFilter, shardField string, pipelineArgs []string) error {
	entries, err := ssql.ReadCatalog(catalogFile)
	if err != nil {
		return err
	}

	entries = ssql.PruneCatalog(entries, filters)
	if len(entries) == 0 {
		return nil
	}

	remoteBin := sshRemoteBin(gpu)
	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)

	records := ssql.ProcessCatalogShards(entries, remoteBin, shardField, pipelineGroups)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// --- Code generation for catalog ---

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

// executeFromSSH runs a simple remote read via SSH.
func executeFromSSH(host, path string, gpu bool) error {
	remoteBin := sshRemoteBin(gpu)
	remoteCmd := ssql.BuildRemoteCommand(remoteBin, path, "", nil)
	return runSSHAndStreamJSONL(host, remoteCmd)
}

// executeFromSSHRemote runs a remote pipeline via SSH with push-down.
func executeFromSSHRemote(host, path string, gpu bool, pipelineArgs []string) error {
	remoteBin := sshRemoteBin(gpu)
	remoteCmd := ssql.BuildRemoteCommand(remoteBin, path, "", ssql.SplitOnPlus(pipelineArgs))
	return runSSHAndStreamJSONL(host, remoteCmd)
}

// runSSHAndStreamJSONL executes an SSH command and streams JSONL output.
func runSSHAndStreamJSONL(host, remoteCmd string) error {
	cmd := exec.Command("ssh", host, remoteCmd)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ssh pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ssh start: %w", err)
	}

	records := readJSONSchemaAware(stdout)
	writeErr := writeWithInferredSchema(records, writeWithInferredSchemaOptions{})

	waitErr := cmd.Wait()
	if writeErr != nil {
		return writeErr
	}
	if waitErr != nil {
		return fmt.Errorf("ssh: %w", waitErr)
	}
	return nil
}

// sshRemoteBin returns the absolute path to the remote binary.
// Uses full path to prevent PATH manipulation attacks on remote machines.
func sshRemoteBin(gpu bool) string {
	if gpu {
		return "/usr/bin/ssql_gpu"
	}
	return "/usr/bin/ssql"
}

// --- Code generation for SSH ---

func generateFromSSHCode(host, path string, gpu bool) error {
	remoteBin := sshRemoteBin(gpu)

	params := []lib.CodeParam{
		{Name: "host", Default: host, Help: "SSH host", VarName: "flagHost"},
		{Name: "path", Default: path, Help: "remote file path", VarName: "flagPath"},
	}

	code := fmt.Sprintf(`remoteCmd := ssql.BuildRemoteCommand(%q, *flagPath, "", nil)
	sshCmd := exec.Command("ssh", *flagHost, remoteCmd)
	sshCmd.Stderr = os.Stderr
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	if err := sshCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	defer sshCmd.Wait()
	records := ssql.ReadJSONFromReader(sshStdout)`, remoteBin)

	imports := []string{"fmt", "os", "os/exec"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

func generateFromSSHRemoteCode(host, path string, gpu bool, pipelineArgs []string) error {
	remoteBin := sshRemoteBin(gpu)
	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)

	params := []lib.CodeParam{
		{Name: "host", Default: host, Help: "SSH host", VarName: "flagHost"},
		{Name: "path", Default: path, Help: "remote file path", VarName: "flagPath"},
	}

	// Build pipeline groups code
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

	code := fmt.Sprintf(`remoteCmd := ssql.BuildRemoteCommand(%q, *flagPath, "", %s)
	sshCmd := exec.Command("ssh", *flagHost, remoteCmd)
	sshCmd.Stderr = os.Stderr
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	if err := sshCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	defer sshCmd.Wait()
	records := ssql.ReadJSONFromReader(sshStdout)`, remoteBin, pipelineCode)

	imports := []string{"fmt", "os", "os/exec"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// --- Shared execution functions ---

// executeFromCSV handles CSV reading for both the subcommand and bare form.
func executeFromCSV(inputFile string, typeOverrides map[string]string, defaultType string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromCSVCode(inputFile, typeOverrides, defaultType)
	}

	csvConfig, err := buildCSVConfig(typeOverrides, defaultType)
	if err != nil {
		return err
	}

	var records iter.Seq[ssql.Record]
	var csvHeaders []string

	if inputFile == "" {
		records = ssql.ReadCSVFromReader(os.Stdin, csvConfig)
	} else {
		file, ferr := os.Open(inputFile)
		if ferr != nil {
			return fmt.Errorf("reading file: %w", ferr)
		}
		defer file.Close()
		if _, serr := file.Seek(0, 0); serr == nil {
			csvHeaders, _ = readCSVHeadersFromReader(file)
			file.Seek(0, 0)
		}
		records = ssql.ReadCSVFromReader(file, csvConfig)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{fieldOrder: csvHeaders})
}

// executeFromMultiCSV reads multiple CSV files and outputs merged JSONL.
func executeFromMultiCSV(files []string, typeOverrides map[string]string, defaultType string, mergeSchemas bool, sourceField string, generate bool) error {
	csvConfig, err := buildCSVConfig(typeOverrides, defaultType)
	if err != nil {
		return err
	}

	cfg := multiFileConfig{
		files:        files,
		mergeSchemas: mergeSchemas,
		sourceField:  sourceField,
		generate:     generate,
	}

	readFile := func(file *os.File) iter.Seq[ssql.Record] {
		return ssql.ReadCSVFromReader(file, csvConfig)
	}

	readHeaders := func(filename string) ([]string, error) {
		file, err := os.Open(filename)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", filename, err)
		}
		defer file.Close()
		return readCSVHeadersFromReader(file)
	}

	return executeFromMultiFile(cfg, "CSV", readFile, readHeaders)
}

// mergeCSVHeaders checks or merges headers from multiple files.
func mergeCSVHeaders(allHeaders [][]string, files []string, merge bool) ([]string, error) {
	if len(allHeaders) == 0 {
		return nil, fmt.Errorf("no files specified")
	}

	first := allHeaders[0]

	if !merge {
		// Require identical headers
		firstSet := make(map[string]bool, len(first))
		for _, h := range first {
			firstSet[h] = true
		}
		for i := 1; i < len(allHeaders); i++ {
			if len(allHeaders[i]) != len(first) {
				return nil, fmt.Errorf("schema mismatch: %s has %d fields, %s has %d — use -merge-schemas to combine",
					files[0], len(first), files[i], len(allHeaders[i]))
			}
			for j, h := range allHeaders[i] {
				if h != first[j] {
					return nil, fmt.Errorf("schema mismatch: field %d is %q in %s but %q in %s — use -merge-schemas to combine",
						j+1, first[j], files[0], h, files[i])
				}
			}
		}
		return first, nil
	}

	// Merge: union of all fields, preserving order from first file
	seen := make(map[string]bool)
	var merged []string
	for _, headers := range allHeaders {
		for _, h := range headers {
			if !seen[h] {
				seen[h] = true
				merged = append(merged, h)
			}
		}
	}
	return merged, nil
}

// generateFromMultiCSVCode generates Go code for multi-file CSV reading.
func generateFromMultiCSVCode(files []string, typeOverrides map[string]string, defaultType string, sourceField string) error {
	// For now, generate code that reads files sequentially
	// TODO: implement proper multi-file code generation
	return fmt.Errorf("code generation for multi-file CSV not yet implemented — use single file with -generate")
}

// multiFileConfig holds common options for multi-file reading.
type multiFileConfig struct {
	files        []string
	mergeSchemas bool
	sourceField  string
	generate     bool
}

// extractMultiFileConfig extracts multi-file flags from autocli context.
func extractMultiFileConfig(ctx *cf.Context) multiFileConfig {
	var cfg multiFileConfig

	if filesVal, ok := ctx.GlobalFlags["FILE"]; ok {
		switch v := filesVal.(type) {
		case []string:
			cfg.files = v
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					cfg.files = append(cfg.files, s)
				}
			}
		case string:
			if v != "" {
				cfg.files = []string{v}
			}
		}
	}
	if msVal, ok := ctx.GlobalFlags["-merge-schemas"]; ok {
		cfg.mergeSchemas = msVal.(bool)
	}
	if srcVal, ok := ctx.GlobalFlags["-source"]; ok {
		cfg.sourceField = srcVal.(string)
	}
	if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
		cfg.generate = genVal.(bool)
	}
	return cfg
}

// fileReaderFunc opens a file and returns a record iterator.
type fileReaderFunc func(file *os.File) iter.Seq[ssql.Record]

// headerReaderFunc reads headers from a file (for CSV/TSV). Returns nil for formats without headers.
type headerReaderFunc func(filename string) ([]string, error)

// executeFromMultiFile reads multiple files of any format and outputs merged JSONL.
func executeFromMultiFile(cfg multiFileConfig, format string, readFile fileReaderFunc, readHeaders headerReaderFunc) error {
	if cfg.generate {
		return fmt.Errorf("code generation for multi-file %s not yet implemented — use single file with -generate", format)
	}

	var mergedHeaders []string

	// For formats with headers (CSV, TSV), pre-scan and validate
	if readHeaders != nil {
		allHeaders := make([][]string, len(cfg.files))
		for i, f := range cfg.files {
			headers, err := readHeaders(f)
			if err != nil {
				return fmt.Errorf("reading headers from %s: %w", f, err)
			}
			allHeaders[i] = headers
		}
		var err error
		mergedHeaders, err = mergeCSVHeaders(allHeaders, cfg.files, cfg.mergeSchemas)
		if err != nil {
			return err
		}
	}

	// Build schema from first file's first record
	firstFile, err := os.Open(cfg.files[0])
	if err != nil {
		return fmt.Errorf("opening %s: %w", cfg.files[0], err)
	}
	firstRecords := readFile(firstFile)
	next, stop := iter.Pull(firstRecords)
	firstRecord, hasFirst := next()
	stop()
	firstFile.Close()

	// For headerless formats (JSON/JSONL), derive headers from first record
	if readHeaders == nil && hasFirst {
		var firstFields []string
		for k := range firstRecord.All() {
			firstFields = append(firstFields, k)
		}
		sort.Strings(firstFields)
		mergedHeaders = firstFields

		// Scan remaining files' first records for schema validation/merge
		if len(cfg.files) > 1 {
			allHeaders := [][]string{firstFields}
			for _, f := range cfg.files[1:] {
				file, err := os.Open(f)
				if err != nil {
					return fmt.Errorf("opening %s: %w", f, err)
				}
				recs := readFile(file)
				nextR, stopR := iter.Pull(recs)
				rec, ok := nextR()
				stopR()
				file.Close()
				if ok {
					var fields []string
					for k := range rec.All() {
						fields = append(fields, k)
					}
					sort.Strings(fields)
					allHeaders = append(allHeaders, fields)
				}
			}
			mergedHeaders, err = mergeCSVHeaders(allHeaders, cfg.files, cfg.mergeSchemas)
			if err != nil {
				return err
			}
		}
	}

	schema := lib.NewSchema()
	if hasFirst {
		for _, h := range mergedHeaders {
			if v, exists := ssql.Get[any](firstRecord, h); exists {
				schema.AddField(h, lib.InferTypeString(v))
			} else {
				schema.AddField(h, lib.TypeString)
			}
		}
	}
	if cfg.sourceField != "" {
		schema.AddField(cfg.sourceField, lib.TypeString)
	}

	// Concatenate records from all files, lazily
	records := func(yield func(ssql.Record) bool) {
		for i, f := range cfg.files {
			file, err := os.Open(f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", f, err)
				return
			}
			for r := range readFile(file) {
				if cfg.sourceField != "" {
					r = r.ToMutable().String(cfg.sourceField, cfg.files[i]).Freeze()
				}
				if !yield(r) {
					file.Close()
					return
				}
			}
			file.Close()
		}
	}

	return lib.WriteJSONLWithSchema(os.Stdout, schema, records)
}

// executeFromTSV handles TSV reading for both the subcommand and bare form.
func executeFromTSV(inputFile string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromTSVCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = ssql.ReadTSVFromReader(os.Stdin)
	} else {
		file, err := os.Open(inputFile)
		if err != nil {
			return fmt.Errorf("reading TSV file: %w", err)
		}
		defer file.Close()
		records = ssql.ReadTSVFromReader(file)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// executeFromJSON handles JSON/JSONL reading for both the subcommand and bare form.
func executeFromJSON(inputFile string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromJSONCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = readJSONSchemaAware(os.Stdin)
	} else {
		file, err := lib.OpenInputFile(inputFile)
		if err != nil {
			return err
		}
		defer file.Close()
		records = readJSONSchemaAware(file)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// executeFromArrow handles Arrow reading for both the subcommand and bare form.
func executeFromArrow(inputFile string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromArrowCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = ssql.ReadArrowFromReader(os.Stdin)
	} else {
		var err error
		records, err = ssql.ReadArrow(inputFile)
		if err != nil {
			return fmt.Errorf("reading Arrow file: %w", err)
		}
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// executeFromParquet handles Parquet reading.
func executeFromParquet(inputFile string, columns []string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromParquetCode(inputFile, columns)
	}

	if inputFile == "" {
		return fmt.Errorf("Parquet requires a file (random access format, no stdin support)")
	}

	records, err := ssql.ReadParquetColumns(inputFile, columns)
	if err != nil {
		return fmt.Errorf("reading Parquet file: %w", err)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// executeFromWAV handles WAV reading for both the subcommand and bare form.
func executeFromWAV(inputFile string, channel int, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromWAVCode(inputFile, channel)
	}

	var records iter.Seq[ssql.Record]
	var wavMeta *ssql.WAVMetadata

	if inputFile == "" {
		var err error
		records, wavMeta, err = ssql.ReadWAVFromReader(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading WAV from stdin: %w", err)
		}
	} else {
		var err error
		if channel >= 0 {
			records, wavMeta, err = ssql.ReadWAVChannel(inputFile, channel)
		} else {
			records, wavMeta, err = ssql.ReadWAV(inputFile)
		}
		if err != nil {
			return fmt.Errorf("reading WAV file: %w", err)
		}
	}

	records = wrapWithFieldCaching(records, inputFile)
	opts := writeWithInferredSchemaOptions{}
	if wavMeta != nil {
		opts.sampleRate = wavMeta.SampleRate
	}
	return writeWithInferredSchema(records, opts)
}

// executeFromXLSX handles XLSX reading for both the subcommand and bare form.
func executeFromXLSX(inputFile string, sheet string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromXLSXCode(inputFile, sheet)
	}

	var xlsxConfig []ssql.XLSXConfig
	if sheet != "" {
		xlsxConfig = append(xlsxConfig, ssql.XLSXConfig{SheetName: sheet})
	}

	records, err := ssql.ReadXLSX(inputFile, xlsxConfig...)
	if err != nil {
		return fmt.Errorf("reading XLSX file: %w", err)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// --- Shared helpers ---

// wrapWithFieldCaching wraps a record iterator with field name caching for
// pipeline completion support.
func wrapWithFieldCaching(originalRecords iter.Seq[ssql.Record], inputFile string) iter.Seq[ssql.Record] {
	var firstRecord *ssql.Record
	var fieldNames []string
	return func(yield func(ssql.Record) bool) {
		for r := range originalRecords {
			if firstRecord == nil {
				firstRecord = &r
				for k := range r.All() {
					fieldNames = append(fieldNames, k)
				}
				os.Setenv("AUTOCLI_FIELDS", strings.Join(fieldNames, ","))
				if inputFile != "" {
					cleanName := strings.ReplaceAll(inputFile, ".", "_")
					cleanName = strings.ReplaceAll(cleanName, "/", "_")
					os.Setenv("AUTOCLI_FIELDS_"+cleanName, strings.Join(fieldNames, ","))
				}
			}
			if !yield(r) {
				return
			}
		}
	}
}

// parseTypeOverrides extracts type override map from autocli accumulated flag value.
func parseTypeOverrides(typeVal any) map[string]string {
	typeOverrides := make(map[string]string)
	if typeSlice, ok := typeVal.([]any); ok {
		for _, item := range typeSlice {
			if argMap, ok := item.(map[string]any); ok {
				field := fmt.Sprintf("%v", argMap["field"])
				typ := fmt.Sprintf("%v", argMap["type"])
				typeOverrides[field] = typ
			}
		}
	}
	return typeOverrides
}

// buildCSVConfig creates a CSVConfig from type override parameters
func buildCSVConfig(typeOverrides map[string]string, defaultType string) (ssql.CSVConfig, error) {
	config := ssql.DefaultCSVConfig()

	// Parse default type
	if defaultType != "" && defaultType != "auto" {
		dt, err := ssql.ParseFieldType(defaultType)
		if err != nil {
			return config, err
		}
		config.DefaultType = dt
	}

	// Parse type overrides
	if len(typeOverrides) > 0 {
		config.TypeOverrides = make(map[string]ssql.FieldType)
		for field, typeName := range typeOverrides {
			ft, err := ssql.ParseFieldType(typeName)
			if err != nil {
				return config, fmt.Errorf("field %q: %w", field, err)
			}
			config.TypeOverrides[field] = ft
		}
	}

	return config, nil
}

// readJSONSchemaAware reads JSON/JSONL input, stripping any _schema header.
// Handles JSON arrays (starts with '[') and JSONL (one object per line).
// When a _schema header is present, it is consumed and records are type-coerced.
func readJSONSchemaAware(r io.Reader) iter.Seq[ssql.Record] {
	br := bufio.NewReader(r)

	// Peek at first non-whitespace byte to detect JSON array vs JSONL
	for {
		b, err := br.Peek(1)
		if err != nil {
			return func(yield func(ssql.Record) bool) {}
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r' {
			br.ReadByte()
			continue
		}
		if b[0] == '[' {
			// JSON array — no schema headers possible, use standard reader
			return lib.ReadJSON(br)
		}
		break
	}

	// JSONL — use schema-aware reader that strips _schema headers
	return lib.ReadJSONLWithSchema(br).Records
}

// readCSVHeadersFromReader reads just the header row from a reader
func readCSVHeadersFromReader(r io.Reader) ([]string, error) {
	reader := csv.NewReader(r)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	return headers, nil
}

func readTSVHeaders(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty file")
	}
	header := scanner.Text()
	sep := ssql.DetectTSVSeparator(header)
	return strings.Split(header, string(sep)), nil
}

// writeWithInferredSchemaOptions configures writeWithInferredSchema behavior
type writeWithInferredSchemaOptions struct {
	fieldOrder []string
	sampleRate int // For audio data (0 means not audio)
}

// writeWithInferredSchema infers schema from first record and writes with schema header
// If fieldOrder is provided, uses that order; otherwise uses sorted field names for determinism
// Only buffers first record (O(1) memory), then streams remaining records.
func writeWithInferredSchema(records iter.Seq[ssql.Record], opts ...writeWithInferredSchemaOptions) error {
	var options writeWithInferredSchemaOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	// Use pull-style iteration to peek at first record
	next, stop := iter.Pull(records)
	defer stop()

	// Get first record to infer schema
	firstRecord, ok := next()
	if !ok {
		// No records - nothing to write
		return nil
	}

	// Determine field order
	var order []string
	if len(options.fieldOrder) > 0 {
		order = options.fieldOrder
	} else {
		// Use sorted field names for deterministic output
		for k := range firstRecord.All() {
			order = append(order, k)
		}
		sort.Strings(order)
	}

	// Infer schema from first record
	schema := lib.InferFromRecordOrdered(firstRecord, order)

	// Set sample rate for audio data
	if options.sampleRate > 0 {
		schema.SampleRate = options.sampleRate
	}

	// Create streaming iterator: first record + remaining records
	allRecords := func(yield func(ssql.Record) bool) {
		// Yield first record
		if !yield(firstRecord) {
			return
		}
		// Stream remaining records
		for {
			r, ok := next()
			if !ok {
				return
			}
			if !yield(r) {
				return
			}
		}
	}

	// Write with schema header and ordered fields (streams without buffering)
	return lib.WriteJSONLWithSchemaOrdered(os.Stdout, schema, allRecords)
}

// --- Code generation functions ---

// generateFromCSVCode generates Go code for reading CSV.
func generateFromCSVCode(filename string, typeOverrides map[string]string, defaultType string) error {
	var code string
	var imports []string

	configCode := generateCSVConfigCode(typeOverrides, defaultType)
	hasConfig := configCode != ""

	var params []lib.CodeParam

	if filename == "" {
		if hasConfig {
			code = configCode + "\n\trecords := ssql.ReadCSVFromReader(os.Stdin, csvConfig)"
		} else {
			code = `records := ssql.ReadCSVFromReader(os.Stdin)`
		}
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input CSV file", VarName: "flagInput"})
		if hasConfig {
			code = configCode + `
	records, err := ssql.ReadCSV(*flagInput, csvConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading CSV: %w", err))
		os.Exit(1)
	}`
		} else {
			code = `records, err := ssql.ReadCSV(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading CSV: %w", err))
		os.Exit(1)
	}`
		}
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromTSVCode generates Go code for reading TSV.
func generateFromTSVCode(filename string) error {
	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records := ssql.ReadTSVFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input TSV file", VarName: "flagInput"})
		code = `records, err := ssql.ReadTSV(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading TSV: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromJSONCode generates Go code for reading JSON/JSONL.
func generateFromJSONCode(filename string) error {
	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records := ssql.ReadJSONFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input JSON file", VarName: "flagInput"})
		code = `records, err := ssql.ReadJSONAuto(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading JSON: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromArrowCode generates Go code for reading Arrow.
func generateFromArrowCode(filename string) error {
	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records := ssql.ReadArrowFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input Arrow file", VarName: "flagInput"})
		code = `records, err := ssql.ReadArrow(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading Arrow: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromParquetCode generates Go code for reading Parquet.
func generateFromParquetCode(filename string, columns []string) error {
	if filename == "" {
		return fmt.Errorf("Parquet code generation requires a file (no stdin support)")
	}

	params := []lib.CodeParam{
		{Name: "input", Default: filename, Help: "input Parquet file", VarName: "flagInput"},
	}

	var code string
	if len(columns) > 0 {
		// Build Go string slice literal for the columns
		quoted := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = fmt.Sprintf("%q", c)
		}
		colLiteral := "[]string{" + strings.Join(quoted, ", ") + "}"
		code = fmt.Sprintf(`records, err := ssql.ReadParquetColumns(*flagInput, %s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading Parquet: %%w", err))
		os.Exit(1)
	}`, colLiteral)
	} else {
		code = `records, err := ssql.ReadParquetColumns(*flagInput, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading Parquet: %w", err))
		os.Exit(1)
	}`
	}

	frag := lib.NewInitFragment("records", code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromWAVCode generates Go code for reading WAV.
func generateFromWAVCode(filename string, channel int) error {
	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records, _, err := ssql.ReadWAVFromReader(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading WAV: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input WAV file", VarName: "flagInput"})
		if channel >= 0 {
			code = fmt.Sprintf(`records, _, err := ssql.ReadWAVChannel(*flagInput, %d)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading WAV: %%w", err))
		os.Exit(1)
	}`, channel)
		} else {
			code = `records, _, err := ssql.ReadWAV(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading WAV: %w", err))
		os.Exit(1)
	}`
		}
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromXLSXCode generates Go code for reading XLSX.
func generateFromXLSXCode(filename string, sheet string) error {
	var code string
	var params []lib.CodeParam

	params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input XLSX file", VarName: "flagInput"})
	if sheet != "" {
		code = fmt.Sprintf(`records, err := ssql.ReadXLSX(*flagInput, ssql.XLSXConfig{SheetName: %q})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading XLSX: %%w", err))
		os.Exit(1)
	}`, sheet)
	} else {
		code = `records, err := ssql.ReadXLSX(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading XLSX: %w", err))
		os.Exit(1)
	}`
	}

	imports := []string{"fmt", "os"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromExecCode generates Go code for from command with command execution
func generateFromExecCode(command string, args []string) error {
	// Build the args slice literal
	var argsCode string
	if len(args) == 0 {
		argsCode = "nil"
	} else {
		quotedArgs := make([]string, len(args))
		for i, arg := range args {
			quotedArgs[i] = fmt.Sprintf("%q", arg)
		}
		argsCode = fmt.Sprintf("[]string{%s}", strings.Join(quotedArgs, ", "))
	}

	code := fmt.Sprintf(`records, err := ssql.ExecCommand(%q, %s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("executing command: %%w", err))
		os.Exit(1)
	}`, command, argsCode)

	imports := []string{"fmt", "os"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// --- CSV config helpers ---

// generateCSVConfigCode generates Go code for CSV config with type overrides
func generateCSVConfigCode(typeOverrides map[string]string, defaultType string) string {
	// No config needed if using defaults
	if len(typeOverrides) == 0 && (defaultType == "" || defaultType == "auto") {
		return ""
	}

	var parts []string
	parts = append(parts, "csvConfig := ssql.CSVConfig{")
	parts = append(parts, "\t\tHasHeaders: true,")
	parts = append(parts, "\t\tDelimiter:  ',',")
	parts = append(parts, "\t\tComment:    '#',")

	// Default type
	if defaultType != "" && defaultType != "auto" {
		parts = append(parts, fmt.Sprintf("\t\tDefaultType: ssql.FieldType%s,", capitalizeFieldType(defaultType)))
	}

	// Type overrides
	if len(typeOverrides) > 0 {
		parts = append(parts, "\t\tTypeOverrides: map[string]ssql.FieldType{")
		for field, typeName := range typeOverrides {
			parts = append(parts, fmt.Sprintf("\t\t\t%q: ssql.FieldType%s,", field, capitalizeFieldType(typeName)))
		}
		parts = append(parts, "\t\t},")
	}

	parts = append(parts, "\t}")

	return strings.Join(parts, "\n\t")
}

// capitalizeFieldType converts "string" to "String", "int" to "Int", etc.
func capitalizeFieldType(typeName string) string {
	switch strings.ToLower(typeName) {
	case "string", "str", "text":
		return "String"
	case "int", "integer", "int64":
		return "Int"
	case "float", "float64", "double", "number":
		return "Float"
	case "bool", "boolean":
		return "Bool"
	default:
		return "Auto"
	}
}
