package lib

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// CodeFragment represents a piece of generated Go code in a pipeline
type CodeFragment struct {
	Type    string   `json:"type"`    // "stmt" (statement), "final" (no output var), "init" (first in chain), "func" (subprocess function)
	Var     string   `json:"var"`     // Output variable name (e.g., "filtered0")
	Input   string   `json:"input"`   // Input variable name from previous command
	Code    string   `json:"code"`    // Go code for this operation
	Imports []string `json:"imports"` // Required imports (e.g., ["strings", "log"])
	Command string   `json:"command"` // The ssql command that generated this fragment (e.g., "ssql from")

	// For "func" type fragments (subprocess functions from process substitution)
	FuncName string          `json:"func_name,omitempty"` // Function name (e.g., "rightSource1")
	FuncBody []*CodeFragment `json:"func_body,omitempty"` // Fragments that make up the function body
}

// ReadAllCodeFragments reads all code fragments from stdin
// Returns empty slice if stdin is empty (EOF immediately)
func ReadAllCodeFragments() ([]*CodeFragment, error) {
	return ReadCodeFragmentsFromReader(os.Stdin)
}

// ReadCodeFragmentsFromReader reads code fragments from any io.Reader
// Returns empty slice if reader is empty (EOF immediately)
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
	return &CodeFragment{
		Type:    "init",
		Var:     varName,
		Input:   "",
		Code:    code,
		Imports: imports,
		Command: command,
	}
}

// NewStmtFragment creates a statement fragment that transforms data
func NewStmtFragment(varName, inputVar, code string, imports []string, command string) *CodeFragment {
	return &CodeFragment{
		Type:    "stmt",
		Var:     varName,
		Input:   inputVar,
		Code:    code,
		Imports: imports,
		Command: command,
	}
}

// NewFinalFragment creates a final fragment with no output variable (e.g., write-csv)
func NewFinalFragment(inputVar, code string, imports []string, command string) *CodeFragment {
	return &CodeFragment{
		Type:    "final",
		Var:     "",
		Input:   inputVar,
		Code:    code,
		Imports: imports,
		Command: command,
	}
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
		fragments = append(fragments, &frag)
	}

	if len(fragments) == 0 {
		return "", fmt.Errorf("no code fragments received")
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

	// Collect all imports and deduplicate
	importSet := make(map[string]bool)
	importSet["github.com/rosscartlidge/ssql/v4"] = true // Always needed

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
	var code string
	code += "package main\n\n"

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
			// If this is a join command with /dev/fd, replace with process substitution
			if findString(cmd, "ssql join /dev/fd/") != -1 && funcIndex < len(funcFragments) {
				// Get the subprocess command from the func fragment
				subCmd := funcFragments[funcIndex].Command
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
		code += "/*\n"
		code += fmt.Sprintf("Generated by ssql %s:\n\n", version.Version)
		code += "(export SSQLGO=1\n"
		for _, cmd := range commands {
			code += cmd + " |\n"
		}
		code += "ssql generate-go)\n"
		code += "*/\n\n"
	}

	// Add imports
	if len(imports) > 0 {
		code += "import (\n"
		for _, imp := range imports {
			code += fmt.Sprintf("\t%q\n", imp)
		}
		code += ")\n\n"
	}

	// Extract and add package-level pre-compile vars (from expr filters)
	preCompileVars := extractPreCompileVars(fragments)
	for _, funcFrag := range funcFragments {
		preCompileVars = append(preCompileVars, extractPreCompileVars(funcFrag.FuncBody)...)
	}
	if len(preCompileVars) > 0 {
		for _, varDecl := range preCompileVars {
			code += varDecl + "\n"
		}
		code += "\n"
	}

	// Generate subprocess functions (from process substitution)
	for _, funcFrag := range funcFragments {
		code += generateSubprocessFunction(funcFrag)
		code += "\n"
	}

	// Add main function
	code += "func main() {\n"

	// Add init fragments (the main data source)
	for _, frag := range initFragments {
		code += "\t" + fixErrorHandling(frag.Code) + "\n"
	}

	// The main pipeline root is the first init fragment's variable
	var mainPipelineRoot string
	if len(initFragments) > 0 {
		mainPipelineRoot = initFragments[0].Var
	} else {
		mainPipelineRoot = "records"
	}

	// Build Chain() call if we have stmt fragments
	if len(stmtFragments) > 0 {
		outputVar := stmtFragments[len(stmtFragments)-1].Var

		if len(stmtFragments) > 1 {
			code += "\t" + outputVar + " := ssql.Chain(\n"
			for _, frag := range stmtFragments {
				filterCode := extractFilter(frag.Code)
				code += "\t\t" + filterCode + ",\n"
			}
			code += "\t)(" + mainPipelineRoot + ")\n"
		} else {
			// Single transformation
			code += "\t" + fixErrorHandling(stmtFragments[0].Code) + "\n"
		}
	}

	// Add final fragments (e.g., to table, to csv)
	if len(finalFragments) > 0 {
		for _, frag := range finalFragments {
			code += "\t" + fixErrorHandling(frag.Code) + "\n"
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

		code += "\t// Output records as JSONL\n"
		code += fmt.Sprintf("\tif err := ssql.WriteJSONToWriter(%s, os.Stdout); err != nil {\n", outputVar)
		code += "\t\tfmt.Fprintf(os.Stderr, \"Error writing output: %%v\\n\", err)\n"
		code += "\t\tos.Exit(1)\n"
		code += "\t}\n"
	}

	code += "}\n"

	return code, nil
}

// generateSubprocessFunction generates a function from a func fragment
// Each subprocess (process substitution) becomes its own function returning iter.Seq[ssql.Record]
func generateSubprocessFunction(funcFrag *CodeFragment) string {
	var code string

	// Add comment with the subprocess command
	if funcFrag.Command != "" {
		code += fmt.Sprintf("// %s\n", funcFrag.Command)
	}

	code += fmt.Sprintf("func %s() iter.Seq[ssql.Record] {\n", funcFrag.FuncName)

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
		code += "\t" + initCode + "\n"
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
		code += "\treturn ssql.Chain(\n"
		for _, frag := range stmtFrags {
			filterCode := extractFilter(frag.Code)
			code += "\t\t" + filterCode + ",\n"
		}
		code += "\t)(" + rootVar + ")\n"
	} else if len(stmtFrags) == 1 {
		// Single transformation - extract filter and apply
		filterCode := extractFilter(stmtFrags[0].Code)
		code += fmt.Sprintf("\treturn %s(%s)\n", filterCode, rootVar)
	} else {
		// No transformations, just return the init result
		code += fmt.Sprintf("\treturn %s\n", rootVar)
	}

	code += "}\n"

	return code
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
	// Search backwards for ")(" pattern
	for i := len(code) - 1; i > 0; i-- {
		if code[i] == '(' && i > 0 && code[i-1] == ')' {
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

// fixErrorHandling fixes "return fmt.Errorf(...)" to proper main() error handling
func fixErrorHandling(code string) string {
	// Replace "return fmt.Errorf(...)" with proper error handling for main()
	// Pattern: if err != nil { return fmt.Errorf("...", err) }

	// For now, use simple string replacement
	// TODO: Use more sophisticated parsing if needed

	replaced := code

	// Pattern 1: return fmt.Errorf("message: %w", err)
	if findString(replaced, "return fmt.Errorf(") != -1 {
		replaced = replaceReturnError(replaced)
	}

	return replaced
}

// replaceReturnError replaces "return fmt.Errorf(...)" with proper error handling
func replaceReturnError(code string) string {
	// Find "return fmt.Errorf(" and replace with proper error handling
	returnIdx := findString(code, "return fmt.Errorf(")
	if returnIdx == -1 {
		return code
	}

	// Find the full error message (up to the closing paren)
	msgStart := returnIdx + len("return fmt.Errorf(")
	depth := 1
	msgEnd := msgStart

	for msgEnd < len(code) && depth > 0 {
		if code[msgEnd] == '(' {
			depth++
		} else if code[msgEnd] == ')' {
			depth--
		}
		if depth > 0 {
			msgEnd++
		}
	}

	errorMsg := code[msgStart:msgEnd]

	// Build replacement: fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf(...)) + os.Exit(1)
	replacement := fmt.Sprintf("fmt.Fprintf(os.Stderr, \"Error: %%v\\n\", fmt.Errorf(%s))\n\t\tos.Exit(1)", errorMsg)

	return code[:returnIdx] + replacement + code[msgEnd+1:]
}

// sortImports sorts imports with standard library first, then third-party
func sortImports(imports []string) {
	// Simple bubble sort - good enough for small import lists
	for i := 0; i < len(imports); i++ {
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
	result := ""
	for _, line := range lines {
		result += line
	}
	return result
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
