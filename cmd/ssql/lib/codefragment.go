package lib

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// funcCounter is used to generate unique function names across all process substitutions
var funcCounter atomic.Int32

// NextFuncName returns the next unique function name for subprocess functions
func NextFuncName() string {
	n := funcCounter.Add(1)
	return fmt.Sprintf("rightSource%d", n)
}

// CodeParam declares a parameterizable value in generated code.
// Each param becomes a flag.String/flag.Int declaration in the assembled program,
// with the original pipeline value as the default.
type CodeParam struct {
	Name    string `json:"name"`           // Flag name (e.g., "input", "catalog", "date-ge")
	Default string `json:"default"`        // Default value from original pipeline
	Help    string `json:"help"`           // Flag help text
	VarName string `json:"var"`            // Go variable name used in code (e.g., "flagInput")
	Type    string `json:"type,omitempty"` // "string" (default), "int"
}

// CodeFragment represents a piece of generated Go code in a pipeline
type CodeFragment struct {
	Type    string      `json:"type"`             // "stmt" (statement), "final" (no output var), "init" (first in chain), "func" (subprocess function), "error" (generation failed)
	Var     string      `json:"var"`              // Output variable name (e.g., "filtered0")
	Input   string      `json:"input"`            // Input variable name from previous command
	Code    string      `json:"code"`             // Go code for this operation
	Imports []string    `json:"imports"`          // Required imports (e.g., ["strings", "log"])
	Command string      `json:"command"`          // The ssql command that generated this fragment (e.g., "ssql from")
	Error   string      `json:"error,omitempty"`  // Error message for "error" type fragments
	Op      *Op         `json:"op,omitempty"`     // Structured operation descriptor (DFC123): what the stage MEANS; backends fall back to Command parsing when nil
	Params  []CodeParam `json:"params,omitempty"` // Parameterizable values for flag generation

	// For "func" type fragments (subprocess functions from process substitution)
	FuncName string          `json:"func_name,omitempty"` // Function name (e.g., "rightSource1")
	FuncBody []*CodeFragment `json:"func_body,omitempty"` // Fragments that make up the function body

	// Phase 2 typed-mode (SSQLGO=typed): schema-flow info populated by
	// commands that support typed-mode emission. Empty/nil for the
	// existing Record-based generator. The TypedSchema is distinct from
	// the existing lib.Schema (which describes runtime JSONL field
	// types) — it carries the Go-source-level type names that the
	// generated code needs.
	InputTypedSchema  *TypedSchema `json:"input_typed_schema,omitempty"`
	OutputTypedSchema *TypedSchema `json:"output_typed_schema,omitempty"`
	StructDefs        []string     `json:"struct_defs,omitempty"`

	// Phase 2 parallel-mode (SSQLGO=parallel): when true, the
	// fragment's output Var is a typed.Stream[T] rather than an
	// iter.Seq[T]. The typed-mode assembler uses this to decide
	// whether to import "runtime", whether to emit Serial/SerialCount
	// boundaries at sinks, etc.
	IsStream bool `json:"is_stream,omitempty"`

	// PlanNotes are per-fragment planning decisions surfaced by `generate
	// go -explain` (e.g. the expr transpiler's tier per expression:
	// native / VM with static env / record fallback). Emitting commands
	// append them; AssembleCodeFragments prints them to stderr when
	// SSQL_EXPLAIN_PLAN is set. Never part of the generated program.
	PlanNotes []string `json:"plan_notes,omitempty"`

	// AdvisoryTypes carries RECORD-mode type knowledge (CSV column name →
	// Go type, e.g. "pop" → "int64") inferred by the source (CSV sampling,
	// JSONL schema headers). Deliberately distinct from OutputTypedSchema:
	// setting THAT routes the pipeline through the typed assembler, while
	// this is advisory — the expr transpiler uses it to emit native
	// ssql.GetOr predicates in record mode (expr-transpiler Phase 4) and
	// falls back to the VM when it's absent. Commands that preserve the
	// schema propagate it; commands that change it drop or extend it.
	// The contract matches typed mode's: columns are well-typed (a value
	// of a different type in a column is out of contract, exactly as it
	// is for typed.ReadCSV and for the existing -if GetOr emission).
	AdvisoryTypes map[string]string `json:"advisory_types,omitempty"`

	// OutputRecordFields is the ordered list of field names this
	// fragment produces in Record mode (SSQLGO=1). When non-empty,
	// downstream sinks (e.g. `to table`) can use it to display
	// columns in the same natural order the JSONL CLI pipeline
	// would, rather than the alphabetical order ssql.GroupByFields /
	// MutableRecord.Freeze() default to. Commands that don't change
	// the schema can leave this nil; the assembler treats nil as
	// "schema unknown, fall back to alphabetical."
	OutputRecordFields []string `json:"output_record_fields,omitempty"`

	// Capabilities describes what this fragment expects from its
	// upstream and what it produces. Used by the typed-mode planner
	// (cmd/ssql/lib/planner) to decide where to insert Stream.Serial()
	// boundaries and whether the source should emit a parallel
	// (Stream[T]) or serial (iter.Seq[T]) primitive.
	//
	// Phase A only uses ShapeStream / ShapeSeqTyped. Phase B (mixed
	// mode) extends this with ShapeSeqRecord for Tier 3 / Record-only
	// commands.
	//
	// Nil for fragments that pre-date the planner (Record-mode
	// fragments, error fragments, init fragments etc.). The planner
	// treats nil as "no opinion; pass through unchanged."
	Capabilities *Capabilities `json:"capabilities,omitempty"`

	// AltCodeIfSeq, AltImportsIfSeq, AltCapabilitiesIfSeq are an
	// alternative template the planner can swap into Code/Imports/
	// Capabilities if it decides this fragment should produce
	// ShapeSeqTyped rather than ShapeStream. Used by source
	// fragments (typed.ReadCSVParallel ↔ typed.ReadCSV etc.) so
	// the planner's parallelism-reach analysis can downgrade the
	// source primitive when no downstream fragment can consume
	// Stream input.
	//
	// When set, the source emits the parallel form by default
	// (Code = ReadXParallel, Capabilities.Produces = ShapeStream).
	// The planner activates the serial alternative when
	// Plan.SourceParallel = false. Empty alternatives mean "no
	// downgrade possible" — the planner leaves the fragment
	// untouched.
	AltCodeIfSeq         string        `json:"alt_code_if_seq,omitempty"`
	AltImportsIfSeq      []string      `json:"alt_imports_if_seq,omitempty"`
	AltCapabilitiesIfSeq *Capabilities `json:"alt_capabilities_if_seq,omitempty"`
}

// Shape tags the row-type a fragment expects (Accepts) or produces
// (Produces). Used by the typed-mode planner.
type Shape int

const (
	// ShapeNone is the placeholder for sources/sinks that have no
	// upstream input (or have no relevant output type).
	ShapeNone Shape = iota
	// ShapeStream means typed.Stream[T] — the parallel runtime's
	// shard-partitioned stream.
	ShapeStream
	// ShapeSeqTyped means iter.Seq[T] — serial typed Go.
	ShapeSeqTyped
	// ShapeSeqRecord means iter.Seq[ssql.Record] — Record-mode
	// runtime. Only set in Phase B (mixed mode).
	ShapeSeqRecord
)

// String returns the shape's human-readable name (used by the
// planner's --explain output).
func (s Shape) String() string {
	switch s {
	case ShapeNone:
		return "none"
	case ShapeStream:
		return "Stream[T]"
	case ShapeSeqTyped:
		return "iter.Seq[T]"
	case ShapeSeqRecord:
		return "iter.Seq[Record]"
	default:
		return "?"
	}
}

// Capabilities describes a fragment's input/output shapes for the
// typed-mode planner. See [CodeFragment.Capabilities] for usage.
type Capabilities struct {
	// Accepts is the row-type this fragment requires as input.
	// Sources use ShapeNone (no input).
	Accepts Shape `json:"accepts"`
	// Produces is the row-type this fragment outputs. Sinks use
	// ShapeNone (no row output; the sink writes externally).
	Produces Shape `json:"produces"`
	// SerialOnly means the fragment cannot accept ShapeStream input
	// even though it works in typed mode. The planner inserts a
	// Stream.Serial() boundary upstream when SerialOnly is true and
	// the upstream produces ShapeStream. Examples: sort, distinct,
	// limit, offset, top, union, cast, update, include, exclude,
	// rename, and group-by -presorted.
	SerialOnly bool `json:"serial_only,omitempty"`
}

// TypedSchema describes the row type flowing between two pipeline
// stages in typed-mode codegen (SSQLGO=typed). The TypeName is the Go
// identifier the StructDefs declares; Fields is the ordered list of
// columns with their CSV names, Go field names, and Go types.
type TypedSchema struct {
	TypeName string             `json:"type_name"`
	Fields   []TypedSchemaField `json:"fields"`
}

// TypedSchemaField is one column in a typed schema.
type TypedSchemaField struct {
	Name   string `json:"name"`    // CSV column name (e.g., "dept_id")
	GoName string `json:"go_name"` // Go field name (e.g., "DeptID")
	GoType string `json:"go_type"` // "string", "int64", "float64", "time.Time", "*int64", etc.
}

// NewErrorFragment creates an error fragment that signals code generation failure
func NewErrorFragment(command string, err error) *CodeFragment {
	return &CodeFragment{
		Type:    "error",
		Command: command,
		Error:   err.Error(),
	}
}

// WriteErrorAndExit writes an error fragment and returns the error
// This should be used when code generation fails - it ensures the error
// propagates through the pipeline so generate go can detect it
func WriteErrorAndExit(command string, err error) error {
	frag := NewErrorFragment(command, err)
	WriteCodeFragment(frag)
	return err
}

// ReadAllCodeFragments reads all code fragments from stdin
// Returns empty slice if stdin is empty (EOF immediately)
func ReadAllCodeFragments() ([]*CodeFragment, error) {
	return ReadCodeFragmentsFromReader(os.Stdin)
}

// ReadCodeFragmentsFromReader reads code fragments from any io.Reader
// Returns empty slice if reader is empty (EOF immediately)
// Returns error if an error fragment is encountered (pipeline generation failed)
func ReadCodeFragmentsFromReader(r io.Reader) ([]*CodeFragment, error) {
	var fragments []*CodeFragment
	decoder := json.NewDecoder(r)

	for {
		var frag CodeFragment
		if err := decoder.Decode(&frag); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding fragment: %w", err)
		}

		// Check for error fragment - pipeline generation failed
		if frag.Type == "error" {
			return nil, fmt.Errorf("code generation failed in %s: %s", frag.Command, frag.Error)
		}

		fragments = append(fragments, &frag)
	}

	return fragments, nil
}

// ReadCodeFragmentsFromFile reads code fragments from a file
// Used for reading fragments from process substitution paths (/dev/fd/N)
func ReadCodeFragmentsFromFile(filename string) ([]*CodeFragment, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadCodeFragmentsFromReader(f)
}

// WriteCodeFragment writes a code fragment to stdout as JSONL
func WriteCodeFragment(frag *CodeFragment) error {
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(frag); err != nil {
		return fmt.Errorf("encoding code fragment: %w", err)
	}
	return nil
}

// NewInitFragment creates the first fragment in a pipeline (e.g., from)
func NewInitFragment(varName, code string, imports []string, command string) *CodeFragment {
	frag := &CodeFragment{
		Type:    "init",
		Var:     varName,
		Input:   "",
		Code:    code,
		Imports: imports,
		Command: command,
	}
	if command != "" {
		frag.Op = opFromProcessArgs()
	}
	return frag
}

// NewStmtFragment creates a statement fragment that transforms data
func NewStmtFragment(varName, inputVar, code string, imports []string, command string) *CodeFragment {
	frag := &CodeFragment{
		Type:    "stmt",
		Var:     varName,
		Input:   inputVar,
		Code:    code,
		Imports: imports,
		Command: command,
	}
	if command != "" {
		frag.Op = opFromProcessArgs()
	}
	return frag
}

// NewStmtFragmentWithRuntimeImport creates a statement fragment that requires the runtime package
// The runtime package provides EvalBatchAgg for expression evaluation in generated code
func NewStmtFragmentWithRuntimeImport(varName, inputVar, code string, imports []string, command string) *CodeFragment {
	// Add runtime import to the imports list
	runtimeImport := "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
	allImports := append(imports, runtimeImport)
	frag := &CodeFragment{
		Type:    "stmt",
		Var:     varName,
		Input:   inputVar,
		Code:    code,
		Imports: allImports,
		Command: command,
	}
	if command != "" {
		frag.Op = opFromProcessArgs()
	}
	return frag
}

// NewFinalFragment creates a final fragment with no output variable (e.g., write-csv)
func NewFinalFragment(inputVar, code string, imports []string, command string) *CodeFragment {
	frag := &CodeFragment{
		Type:    "final",
		Var:     "",
		Input:   inputVar,
		Code:    code,
		Imports: imports,
		Command: command,
	}
	if command != "" {
		frag.Op = opFromProcessArgs()
	}
	return frag
}

// NewFuncFragment creates a function fragment from subprocess fragments (process substitution)
// The function returns iter.Seq[ssql.Record] and encapsulates a complete sub-pipeline
func NewFuncFragment(funcName string, bodyFragments []*CodeFragment, command string) *CodeFragment {
	// Collect all imports from body fragments
	importSet := make(map[string]bool)
	for _, frag := range bodyFragments {
		for _, imp := range frag.Imports {
			if imp != "" {
				importSet[imp] = true
			}
		}
	}
	var imports []string
	for imp := range importSet {
		imports = append(imports, imp)
	}

	return &CodeFragment{
		Type:     "func",
		Var:      funcName, // The function name is used as the "variable" it produces
		Input:    "",
		Code:     "", // Code is generated from FuncBody during assembly
		Imports:  imports,
		Command:  command,
		FuncName: funcName,
		FuncBody: bodyFragments,
	}
}

// GetInputVar returns the input variable name, or "records" if this is the first command
func (f *CodeFragment) GetInputVar() string {
	if f == nil || f.Input == "" {
		return "records"
	}
	return f.Input
}

// AssembleCodeFragments reads all code fragments from stdin and assembles them into a complete Go program
// using ssql.Chain() for better readability. Handles func fragments for process substitution.
// Returns error if any error fragments are encountered (code generation failed in pipeline).
func AssembleCodeFragments(input io.Reader) (string, error) {
	// Read all fragments from stdin
	var fragments []*CodeFragment
	decoder := json.NewDecoder(input)

	for {
		var frag CodeFragment
		if err := decoder.Decode(&frag); err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("decoding fragment: %w", err)
		}

		// Check for error fragment - pipeline generation failed
		if frag.Type == "error" {
			return "", fmt.Errorf("code generation failed in %s: %s", frag.Command, frag.Error)
		}

		fragments = append(fragments, &frag)
	}

	if len(fragments) == 0 {
		return "", fmt.Errorf("no code fragments received")
	}

	// Assembler-owned variable bindings (DFC123 slice 1): assign
	// final unique names before either assembler or the planner sees
	// the fragments. Collisions between commands' base names become
	// structurally impossible here, whatever the commands emitted.
	ResolveBindings(fragments)

	// Surface per-fragment planning notes (expr tier decisions) under
	// -explain, for both the record and typed assemblers.
	if os.Getenv("SSQL_EXPLAIN_PLAN") != "" {
		for _, frag := range fragments {
			for _, note := range frag.PlanNotes {
				fmt.Fprintf(os.Stderr, "[plan] %s: %s\n", frag.Command, note)
			}
		}
	}

	// Phase 2: if any fragment carries a typed schema, route through the
	// typed assembler. We require all-typed-or-none: a mixed pipeline is
	// rejected because the runtime types don't compose.
	if isTypedPipeline(fragments) {
		return assembleTypedFragments(fragments)
	}

	// Separate fragments by type
	var initFragments []*CodeFragment
	var stmtFragments []*CodeFragment
	var finalFragments []*CodeFragment
	var funcFragments []*CodeFragment

	for _, frag := range fragments {
		switch frag.Type {
		case "init":
			initFragments = append(initFragments, frag)
		case "stmt":
			stmtFragments = append(stmtFragments, frag)
		case "final":
			finalFragments = append(finalFragments, frag)
		case "func":
			funcFragments = append(funcFragments, frag)
		}
	}

	// Defence in depth: a pipeline with no init fragment but with
	// stmt/final fragments produces broken Go (references an
	// undeclared `records` variable). Common cause: the user's
	// pipeline failed silently — e.g. `sql from x.csv | ssql ...`
	// (typo'd `sql` instead of `ssql`) where the upstream stage
	// didn't emit anything, but downstream stages happily emitted
	// fragments. With `set -o pipefail` in the script-exec path
	// this is caught at the bash layer; this check covers the
	// pipe-from-stdin case too.
	if len(initFragments) == 0 && (len(stmtFragments) > 0 || len(finalFragments) > 0) {
		return "", fmt.Errorf("assembler: pipeline has no source (init) fragment but produced %d stmt + %d final fragments — typically means an upstream `ssql from ...` stage failed silently. Check the pipeline for typos and shell errors.",
			len(stmtFragments), len(finalFragments))
	}

	// Collect all params from all fragments and deduplicate flag names
	allParams := collectParams(fragments)

	// Collect all imports and deduplicate
	importSet := make(map[string]bool)
	importSet["github.com/rosscartlidge/ssql/v4"] = true // Always needed
	// main() reports run()'s error and exits — the pipeline body runs
	// in an error-returning context, so fragments' `return fmt.Errorf`
	// is legal as written (DFC123 slice 4: no textual surgery).
	importSet["fmt"] = true
	importSet["os"] = true

	if len(allParams) > 0 {
		importSet["flag"] = true
	}

	// iter import only needed for subprocess functions (process substitution)
	if len(funcFragments) > 0 {
		importSet["iter"] = true // For iter.Seq return type in subprocess functions
	}

	// If there are no final fragments, we'll auto-add JSONL output
	if len(finalFragments) == 0 {
		importSet["os"] = true
		importSet["fmt"] = true
	}

	// Collect imports from all fragments (including func body fragments)
	for _, frag := range fragments {
		for _, imp := range frag.Imports {
			if imp != "" {
				importSet[imp] = true
			}
		}
		// Also collect from func body fragments
		for _, bodyFrag := range frag.FuncBody {
			for _, imp := range bodyFrag.Imports {
				if imp != "" {
					importSet[imp] = true
				}
			}
		}
	}

	// Build imports section
	var imports []string
	for imp := range importSet {
		imports = append(imports, imp)
	}
	sortImports(imports)

	// Build the complete program
	var code strings.Builder
	code.WriteString("package main\n\n")

	// Add command pipeline as block comment
	// Build a cleaner representation that shows process substitutions
	var commands []string
	funcIndex := 0
	for _, frag := range fragments {
		if frag.Type == "func" {
			// Skip func fragments - they'll be inlined into join commands
			continue
		}
		if frag.Command != "" {
			cmd := frag.Command
			// If this command has /dev/fd (process substitution), replace with readable <(...) syntax
			if findString(cmd, "/dev/fd/") != -1 && funcIndex < len(funcFragments) {
				// Get the subprocess command from the func fragment's body
				subCmd := ""
				for _, bodyFrag := range funcFragments[funcIndex].FuncBody {
					if bodyFrag.Command != "" {
						subCmd = bodyFrag.Command
					}
				}
				if subCmd != "" {
					// Replace /dev/fd/NN with <(subprocess)
					// Find the /dev/fd part and replace it
					devFdStart := findString(cmd, "/dev/fd/")
					if devFdStart != -1 {
						// Find end of /dev/fd/NN (next space or end)
						devFdEnd := devFdStart + 8 // len("/dev/fd/")
						for devFdEnd < len(cmd) && cmd[devFdEnd] >= '0' && cmd[devFdEnd] <= '9' {
							devFdEnd++
						}
						cmd = cmd[:devFdStart] + "<(" + subCmd + ")" + cmd[devFdEnd:]
					}
				}
				funcIndex++
			}
			commands = append(commands, cmd)
		}
	}
	if len(commands) > 0 {
		code.WriteString("/*\n")
		code.WriteString(fmt.Sprintf("Generated by ssql %s:\n\n", version.Version))
		code.WriteString("(export SSQLGO=1\n")
		for _, cmd := range commands {
			code.WriteString(cmd + " |\n")
		}
		code.WriteString("ssql generate go)\n")
		code.WriteString("*/\n\n")
	}

	// Add imports
	if len(imports) > 0 {
		code.WriteString("import (\n")
		for _, imp := range imports {
			code.WriteString("\t" + renderImport(imp) + "\n")
		}
		code.WriteString(")\n\n")
	}

	// Extract and add package-level pre-compile vars (from expr filters)
	preCompileVars := extractPreCompileVars(fragments)
	for _, funcFrag := range funcFragments {
		preCompileVars = append(preCompileVars, extractPreCompileVars(funcFrag.FuncBody)...)
	}
	if len(preCompileVars) > 0 {
		for _, varDecl := range preCompileVars {
			code.WriteString(varDecl + "\n")
		}
		code.WriteString("\n")
	}

	// Emit flag declarations for parameterized values
	if len(allParams) > 0 {
		code.WriteString("var (\n")
		for _, p := range allParams {
			if p.Type == "int" {
				code.WriteString(fmt.Sprintf("\t%s = flag.Int(%q, %s, %q)\n", p.VarName, p.Name, p.Default, p.Help))
			} else {
				code.WriteString(fmt.Sprintf("\t%s = flag.String(%q, %q, %q)\n", p.VarName, p.Name, p.Default, p.Help))
			}
		}
		code.WriteString(")\n\n")
	}

	// Generate subprocess functions (from process substitution)
	// Deduplicate function names: if two func fragments have the same name,
	// rename the second one and update its corresponding stmt fragment reference.
	// Track which stmt fragments have been "claimed" by a func with the same name.
	funcNameCount := make(map[string]int)
	stmtClaimed := make(map[int]bool) // indices of stmt fragments already matched to a func
	for _, funcFrag := range funcFragments {
		count := funcNameCount[funcFrag.FuncName]
		funcNameCount[funcFrag.FuncName] = count + 1
		if count > 0 {
			oldName := funcFrag.FuncName
			newName := fmt.Sprintf("%s_%d", oldName, count+1)
			// Find the next UNCLAIMED stmt fragment that references the old name
			for si, sf := range stmtFragments {
				if !stmtClaimed[si] && strings.Contains(sf.Code, oldName+"()") {
					sf.Code = strings.Replace(sf.Code, oldName+"()", newName+"()", 1)
					stmtClaimed[si] = true
					break
				}
			}
			funcFrag.FuncName = newName
			funcFrag.Var = newName
		} else {
			// First occurrence — claim its matching stmt fragment
			for si, sf := range stmtFragments {
				if !stmtClaimed[si] && strings.Contains(sf.Code, funcFrag.FuncName+"()") {
					stmtClaimed[si] = true
					break
				}
			}
		}
		code.WriteString(generateSubprocessFunction(funcFrag))
		code.WriteString("\n")
	}

	// main reports run()'s error; the pipeline body lives in run()
	// so every fragment's `return fmt.Errorf(...)` is legal as
	// emitted. This replaced fixErrorHandling's textual rewriting of
	// error returns (DFC123 slice 4 — protocol by contract, not by
	// string surgery).
	writeMainCallingRun(&code, len(allParams) > 0)

	// Add init fragments (the main data source)
	for _, frag := range initFragments {
		code.WriteString("\t" + frag.Code + "\n")
	}

	// The main pipeline root is the first init fragment's variable
	var mainPipelineRoot string
	if len(initFragments) > 0 {
		mainPipelineRoot = initFragments[0].Var
	} else {
		mainPipelineRoot = "records"
	}

	// Build code from stmt fragments using Chain for multiple filters
	if len(stmtFragments) > 0 {
		outputVar := stmtFragments[len(stmtFragments)-1].Var

		if len(stmtFragments) > 1 {
			code.WriteString("\t" + outputVar + " := ssql.Chain(\n")
			for _, frag := range stmtFragments {
				filterCode := extractFilter(frag.Code)
				code.WriteString("\t\t" + filterCode + ",\n")
			}
			code.WriteString("\t)(" + mainPipelineRoot + ")\n")
		} else {
			// Single fragment - apply directly
			filterCode := extractFilter(stmtFragments[0].Code)
			code.WriteString("\t" + outputVar + " := " + filterCode + "(" + mainPipelineRoot + ")\n")
		}
	}

	// Add final fragments (e.g., to table, to csv)
	if len(finalFragments) > 0 {
		for _, frag := range finalFragments {
			code.WriteString("\t" + frag.Code + "\n")
		}
	} else {
		// No final fragment - auto-add JSONL output
		var outputVar string
		if len(stmtFragments) > 0 {
			outputVar = stmtFragments[len(stmtFragments)-1].Var
		} else if len(initFragments) > 0 {
			outputVar = initFragments[0].Var
		} else {
			outputVar = "records"
		}

		// Emit a `{"_schema":…}` header inferred from the first
		// record, then JSONL — matches the wire format the v4.27
		// CLI multi-process pipeline produces. The typed assembler
		// already uses this helper (since v4.41.2); aligning the
		// record-mode fallback closes the last wire-format gap so
		// downstream consumers see the same shape regardless of
		// codegen mode.
		code.WriteString("\t// Output records as JSONL with inferred schema header\n")
		code.WriteString(fmt.Sprintf("\tif err := ssql.WriteJSONLWithInferredSchemaToWriter(%s, os.Stdout); err != nil {\n", outputVar))
		code.WriteString("\t\treturn fmt.Errorf(\"writing output: %w\", err)\n")
		code.WriteString("\t}\n")
	}

	code.WriteString("\treturn nil\n}\n")

	return code.String(), nil
}

// generateSubprocessFunction generates a function from a func fragment
// Each subprocess (process substitution) becomes its own function returning iter.Seq[ssql.Record]
func generateSubprocessFunction(funcFrag *CodeFragment) string {
	var code strings.Builder

	// Add comment with the subprocess command
	if funcFrag.Command != "" {
		code.WriteString(fmt.Sprintf("// %s\n", funcFrag.Command))
	}

	code.WriteString(fmt.Sprintf("func %s() iter.Seq[ssql.Record] {\n", funcFrag.FuncName))

	// Separate body fragments by type
	var initFrags []*CodeFragment
	var stmtFrags []*CodeFragment

	for _, frag := range funcFrag.FuncBody {
		switch frag.Type {
		case "init":
			initFrags = append(initFrags, frag)
		case "stmt":
			stmtFrags = append(stmtFrags, frag)
		}
	}

	// Add init fragments
	for _, frag := range initFrags {
		// For functions, convert error handling to return nil
		initCode := frag.Code
		// Simple replacement for common patterns
		initCode = replaceForFuncReturn(initCode)
		code.WriteString("\t" + initCode + "\n")
	}

	// Get the root variable
	var rootVar string
	if len(initFrags) > 0 {
		rootVar = initFrags[0].Var
	} else {
		rootVar = "records"
	}

	// Generate Chain if multiple stmt fragments, otherwise direct
	if len(stmtFrags) > 1 {
		code.WriteString("\treturn ssql.Chain(\n")
		for _, frag := range stmtFrags {
			filterCode := extractFilter(frag.Code)
			code.WriteString("\t\t" + filterCode + ",\n")
		}
		code.WriteString("\t)(" + rootVar + ")\n")
	} else if len(stmtFrags) == 1 {
		// Single transformation - extract filter and apply
		filterCode := extractFilter(stmtFrags[0].Code)
		code.WriteString(fmt.Sprintf("\treturn %s(%s)\n", filterCode, rootVar))
	} else {
		// No transformations, just return the init result
		code.WriteString(fmt.Sprintf("\treturn %s\n", rootVar))
	}

	code.WriteString("}\n")

	return code.String()
}

// replaceForFuncReturn converts main() error handling to function return nil
func replaceForFuncReturn(code string) string {
	// Replace patterns like "if err != nil { ... os.Exit(1) }" with "if err != nil { return nil }"
	// This is a simple heuristic - in practice the error handling should be cleaner

	// For now, just replace the error block pattern
	if findString(code, "if err != nil") != -1 {
		// Find and replace the error block
		start := findString(code, "if err != nil")
		if start != -1 {
			// Find the closing brace of the if block
			depth := 0
			inBlock := false
			blockStart := -1
			blockEnd := -1

			for i := start; i < len(code); i++ {
				if code[i] == '{' {
					if !inBlock {
						inBlock = true
						blockStart = i
					}
					depth++
				} else if code[i] == '}' {
					depth--
					if depth == 0 && inBlock {
						blockEnd = i
						break
					}
				}
			}

			if blockStart != -1 && blockEnd != -1 {
				// Replace the entire if block with simpler return nil
				code = code[:start] + "if err != nil {\n\t\treturn nil\n\t}" + code[blockEnd+1:]
			}
		}
	}

	return code
}

// extractPreCompileVars extracts package-level variable declarations from code fragments
// These are variables like "var exprFilter1 = runtime.MustCompileExprFilter(...)"
// that need to be moved outside main() to package level
func extractPreCompileVars(fragments []*CodeFragment) []string {
	var vars []string
	seen := make(map[string]bool)

	for _, frag := range fragments {
		lines := splitLines(frag.Code)
		for _, line := range lines {
			trimmed := trimSpace(line)
			// Look for var declarations with runtime.MustCompile*
			if startsWith(trimmed, "var ") && (findString(trimmed, "runtime.MustCompile") != -1) {
				if !seen[trimmed] {
					vars = append(vars, trimmed)
					seen[trimmed] = true
				}
			}
		}
	}

	return vars
}

// collectParams gathers all CodeParams from fragments, deduplicating flag names.
// First occurrence of a name gets the bare name; subsequent get the first FREE
// numbered suffix — renamed names count as taken, so a fragment that already
// declares pop-gt2 itself (duplicate field+op conditions get numbered names at
// emission) can't collide with a rename. When a param is renamed, the
// fragment's code is rewritten to the new variable name with a word-boundary
// match: a bare ReplaceAll of *flagPopGt also corrupted *flagPopGt2 (→
// *flagPopGt32, undeclared — the generated code didn't compile).
func collectParams(fragments []*CodeFragment) []CodeParam {
	type entry struct {
		param CodeParam
		frag  *CodeFragment
	}
	var entries []*entry
	for _, frag := range fragments {
		for _, p := range frag.Params {
			entries = append(entries, &entry{p, frag})
		}
		// Also collect from func body fragments
		for _, bodyFrag := range frag.FuncBody {
			for _, p := range bodyFrag.Params {
				entries = append(entries, &entry{p, bodyFrag})
			}
		}
	}

	// Pass 1: first occurrence of each name keeps it. Claiming ALL keepers
	// before renaming anything matters: a rename must not land on a name a
	// LATER param still holds (pop-gt → pop-gt2 while this fragment's own
	// pop-gt2 is unprocessed would recreate the double reference).
	used := make(map[string]bool)
	var renames []*entry
	for _, e := range entries {
		if used[e.param.Name] {
			renames = append(renames, e)
		} else {
			used[e.param.Name] = true
		}
	}

	// Pass 2: move each collision to the first genuinely free suffix and
	// rewrite its fragment's references. The word boundary keeps the rewrite
	// exact — *flagPopGt must not touch *flagPopGt2. (Names are unique WITHIN
	// a fragment — the emitters number duplicate field+op conditions — so the
	// pattern matches exactly this param's references.)
	for _, e := range renames {
		n := 2
		for used[fmt.Sprintf("%s%d", e.param.Name, n)] {
			n++
		}
		newName := fmt.Sprintf("%s%d", e.param.Name, n)
		newVarName := fmt.Sprintf("%s%d", e.param.VarName, n)
		re := regexp.MustCompile(`\*` + regexp.QuoteMeta(e.param.VarName) + `\b`)
		e.frag.Code = re.ReplaceAllString(e.frag.Code, "*"+newVarName)
		e.param.Name = newName
		e.param.VarName = newVarName
		used[newName] = true
	}

	result := make([]CodeParam, len(entries))
	for i, e := range entries {
		result[i] = e.param
	}
	return result
}

// extractFilter extracts the filter function from a statement like "var := filter(input)"
// Returns just "filter" for use in Chain()
// Skips pre-compile var declarations (moved to package level)
func extractFilter(code string) string {
	// Remove pre-compile var lines first
	var filteredLines []string
	lines := splitLines(code)
	for _, line := range lines {
		trimmed := trimSpace(line)
		// Skip var declarations with runtime.MustCompile*
		if startsWith(trimmed, "var ") && findString(trimmed, "runtime.MustCompile") != -1 {
			continue
		}
		filteredLines = append(filteredLines, line)
	}
	code = joinLines(filteredLines)

	// Pattern: "outputVar := filterCall(inputVar)" or "outputVar := filterCall(...)(inputVar)"
	// We need to extract everything between ":=" and the final "(inputVar)"

	colonEqIdx := findString(code, ":=")
	if colonEqIdx == -1 {
		return code // Fallback: return as-is
	}

	// Start after ":= "
	start := colonEqIdx + 2
	for start < len(code) && (code[start] == ' ' || code[start] == '\t' || code[start] == '\n') {
		start++
	}

	// Find the last ")(" pattern which separates the filter from its application
	// E.g., "ssql.Where(func...)(records)" - we want everything up to the last "("
	lastApplyIdx := findLastApplyParen(code)
	if lastApplyIdx == -1 {
		// No application found, might be already a filter
		return code[start:]
	}

	return code[start:lastApplyIdx]
}

// findLastApplyParen finds the last "(" that applies the filter to input
// Looks for ")(" pattern and returns the index of the second "("
func findLastApplyParen(code string) int {
	// Search backwards for ")(" or "}(" pattern
	// ")(" handles standard filter application: ssql.Where(func...)(records)
	// "}(" handles inline closure application: func(...) { ... }(records)
	for i := len(code) - 1; i > 0; i-- {
		if code[i] == '(' && i > 0 && (code[i-1] == ')' || code[i-1] == '}') {
			return i
		}
	}
	return -1
}

// findString finds substring in string (simple helper)
func findString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// writeMainCallingRun emits the program entry: main parses flags (when
// any), calls run(), and reports its error on stderr with exit 1. The
// pipeline body is emitted into run() by the caller, which must close
// it with "return nil\n}". Shared by the record and typed assemblers.
func writeMainCallingRun(code *strings.Builder, hasParams bool) {
	code.WriteString("func main() {\n")
	if hasParams {
		code.WriteString("\tflag.Parse()\n")
	}
	code.WriteString("\tif err := run(); err != nil {\n")
	code.WriteString("\t\tfmt.Fprintln(os.Stderr, \"Error:\", err)\n")
	code.WriteString("\t\tos.Exit(1)\n")
	code.WriteString("\t}\n}\n\n")
	code.WriteString("func run() error {\n")
}

// renderImport renders one import line. An entry of the form "alias path"
// (single space) becomes an aliased import — used when a package's base name
// would collide with another import (e.g. the expr Tier-V runtime package
// `.../lib/runtime` vs Go's stdlib "runtime" in parallel-mode programs).
func renderImport(imp string) string {
	if idx := strings.IndexByte(imp, ' '); idx > 0 {
		return imp[:idx] + " " + fmt.Sprintf("%q", imp[idx+1:])
	}
	return fmt.Sprintf("%q", imp)
}

// sortImports sorts imports with standard library first, then third-party
func sortImports(imports []string) {
	// Simple bubble sort - good enough for small import lists
	for i := range imports {
		for j := i + 1; j < len(imports); j++ {
			// Standard library imports (no dots) come first
			iStd := !containsChar(imports[i], '.')
			jStd := !containsChar(imports[j], '.')

			if (!iStd && jStd) || (iStd == jStd && imports[i] > imports[j]) {
				imports[i], imports[j] = imports[j], imports[i]
			}
		}
	}
}

// containsChar checks if string contains a character
func containsChar(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// splitLines splits a string into lines
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// joinLines joins lines back into a string
func joinLines(lines []string) string {
	var result strings.Builder
	for _, line := range lines {
		result.WriteString(line)
	}
	return result.String()
}

// trimSpace removes leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	end := len(s)

	// Trim left
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	// Trim right
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}

// startsWith checks if string starts with prefix
func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}
