package commands

import (
	"bufio"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

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
	registerFromSSH(fromCmd)
	registerFromCatalog(fromCmd)

	// Bare "from FILE" handler — infers format from extension
	fromCmd.
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-records").
		Bool().
		Global().
		Default(false).
		Help("Print only the record count this invocation would produce (cheapest per format: parquet footer, line count for csv/tsv/jsonl) and exit").
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
			if rv, _ := ctx.GlobalFlags["-records"].(bool); rv {
				return runFromRecords("", []string{inputFile}, -1)
			}
			// Route via the format authority table (DFC116) — the
			// extension grammar lives there; the reactions differ:
			// unknown URL exts refuse loudly (a presigned URL's query
			// never reaches pathExt), unknown local exts default to
			// JSON (historical contract).
			fi, known := formatForPath(inputFile)
			if ssql.IsHTTPURL(inputFile) && !known {
				return fmt.Errorf("from %s: cannot infer format from URL path — use an explicit subcommand (from csv URL, from parquet URL, …)", inputFile)
			}
			switch fi.Name {
			case "csv":
				return executeFromCSV(inputFile, nil, "auto", generate)
			case "tsv":
				return executeFromTSV(inputFile, generate)
			case "json", "jsonl":
				return executeFromJSON(inputFile, generate)
			case "arrow":
				return executeFromArrow(inputFile, generate)
			case "parquet":
				return executeFromParquet(inputFile, nil, generate)
			case "wav":
				return executeFromWAV(inputFile, -1, generate)
			case "xlsx":
				return executeFromXLSX(inputFile, "", generate)
			default:
				return executeFromJSON(inputFile, generate)
			}
		}).
		Done()

	fromCmd.Done()
	return cmd
}

// --- Shared types and helpers ---

// multiFileConfig holds common options for multi-file reading.
type multiFileConfig struct {
	files        []string
	mergeSchemas bool
	sourceField  string
	generate     bool
	unordered    bool
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
	if uVal, ok := ctx.GlobalFlags["-unordered"]; ok {
		cfg.unordered = uVal.(bool)
	}
	return cfg
}

// fileReaderFunc opens a file and returns a record iterator.
type fileReaderFunc func(file *os.File) iter.Seq[ssql.Record]

// headerReaderFunc reads headers from a file (for CSV/TSV). Returns nil for formats without headers.
type headerReaderFunc func(filename string) ([]string, error)

// executeFromMultiFile reads multiple files of any format and outputs merged JSONL.
func executeFromMultiFile(cfg multiFileConfig, format string, readFile fileReaderFunc, readHeaders headerReaderFunc) error {
	if shouldGenerate(cfg.generate) {
		// Emit an init fragment with the command string for the optimizer (generate ssql).
		// Go code generation for multi-file is not yet supported — the code field is empty,
		// but the command string allows generate ssql to optimize the pipeline.
		frag := lib.NewInitFragment("records", "", nil, getCommandString())
		return lib.WriteCodeFragment(frag)
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

// executeFromMultiFilePushdown runs a sub-pipeline per file in parallel and merges results.
// format is "csv", "tsv", etc. pipelineArgs are the args after "--".
// Uses SplitOnPlus to support multi-stage pushdown: -- where -if x eq 1 + group-by dept
// Files are processed concurrently (capped at NumCPU) but output preserves file order.
func executeFromMultiFilePushdown(files []string, format string, sourceField string, unordered bool, pipelineArgs []string) error {
	selfBin, err := os.Executable()
	if err != nil {
		selfBin = "ssql"
	}

	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)
	maxWorkers := runtime.NumCPU()
	if maxWorkers > len(files) {
		maxWorkers = len(files)
	}

	if unordered {
		return executeMultiFilePushdownUnordered(selfBin, files, format, sourceField, pipelineGroups, maxWorkers)
	}
	return executeMultiFilePushdownOrdered(selfBin, files, format, sourceField, pipelineGroups, maxWorkers)
}

func executeMultiFilePushdownOrdered(selfBin string, files []string, format, sourceField string, pipelineGroups [][]string, maxWorkers int) error {
	// Each file gets a channel; workers fill them concurrently, consumer reads in order.
	type fileResult struct {
		records []ssql.Record
		err     error
	}
	results := make([]chan fileResult, len(files))
	for i := range results {
		results[i] = make(chan fileResult, 1)
	}

	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for i, file := range files {
		wg.Add(1)
		go func(idx int, f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			recs, err := runFilePushdown(selfBin, f, format, sourceField, pipelineGroups)
			results[idx] <- fileResult{records: recs, err: err}
		}(i, file)
	}

	records := func(yield func(ssql.Record) bool) {
		for i := range files {
			res := <-results[i]
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "file %s: %v\n", files[i], res.err)
				continue
			}
			for _, r := range res.records {
				if !yield(r) {
					return
				}
			}
		}
	}

	err := writeWithInferredSchema(records)
	wg.Wait()
	return err
}

func executeMultiFilePushdownUnordered(selfBin string, files []string, format, sourceField string, pipelineGroups [][]string, maxWorkers int) error {
	// Shared channel — records stream as they arrive from any file.
	// No per-file buffering, lower memory, lower latency.
	ch := make(chan ssql.Record, 1024)

	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			recs, err := runFilePushdown(selfBin, f, format, sourceField, pipelineGroups)
			if err != nil {
				fmt.Fprintf(os.Stderr, "file %s: %v\n", f, err)
				return
			}
			for _, r := range recs {
				ch <- r
			}
		}(file)
	}

	// Close channel when all workers finish
	go func() {
		wg.Wait()
		close(ch)
	}()

	records := func(yield func(ssql.Record) bool) {
		for r := range ch {
			if !yield(r) {
				return
			}
		}
	}

	return writeWithInferredSchema(records)
}

// runFilePushdown executes a pushdown pipeline on a single file and returns all records.
func runFilePushdown(selfBin, file, format, sourceField string, pipelineGroups [][]string) ([]ssql.Record, error) {
	cmd := ssql.BuildRemoteCommand(selfBin, file, format, pipelineGroups)
	proc := exec.Command("bash", "-c", cmd)
	proc.Stderr = os.Stderr

	stdout, err := proc.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pipe: %w", err)
	}
	if err := proc.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	var records []ssql.Record
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
		if sourceField != "" {
			r = r.ToMutable().String(sourceField, file).Freeze()
		}
		records = append(records, r)
	}

	if err := proc.Wait(); err != nil {
		return records, fmt.Errorf("exit: %w", err)
	}
	return records, nil
}

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
		// Record (schema) order, NOT alphabetical: Record.All() is
		// deterministic since records carry ordered schemas, and the
		// header must preserve field order across wire hops — the old
		// sort scrambled column order on every tee/from round-trip
		// (found by the DFC108 cut-point equivalence gate).
		for k := range firstRecord.All() {
			order = append(order, k)
		}
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
