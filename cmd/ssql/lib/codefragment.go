package lib

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/rosscartlidge/ssql/v3/cmd/ssql/version"
)

// CodeFragment represents a piece of generated Go code in a pipeline
type CodeFragment struct {
	Type    string   `json:"type"`    // "stmt" (statement), "final" (no output var), "init" (first in chain)
	Var     string   `json:"var"`     // Output variable name (e.g., "filtered0")
	Input   string   `json:"input"`   // Input variable name from previous command
	Code    string   `json:"code"`    // Go code for this operation
	Imports []string `json:"imports"` // Required imports (e.g., ["strings", "log"])
	Command string   `json:"command"` // The ssql command that generated this fragment (e.g., "ssql from")
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

// GetInputVar returns the input variable name, or "records" if this is the first command
func (f *CodeFragment) GetInputVar() string {
	if f == nil || f.Input == "" {
		return "records"
	}
	return f.Input
}

// AssembleCodeFragments reads all code fragments from stdin and assembles them into a complete Go program
// using ssql.Chain() for better readability
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

	// Separate init fragments (setup) from stmt fragments (transformations)
	var initFragments []*CodeFragment
	var stmtFragments []*CodeFragment
	var finalFragments []*CodeFragment

	for _, frag := range fragments {
		switch frag.Type {
		case "init":
			initFragments = append(initFragments, frag)
		case "stmt":
			stmtFragments = append(stmtFragments, frag)
		case "final":
			finalFragments = append(finalFragments, frag)
		}
	}

	// Collect all imports and deduplicate
	importSet := make(map[string]bool)
	importSet["github.com/rosscartlidge/ssql/v3"] = true // Always needed

	// If there are no final fragments, we'll auto-add JSONL output
	if len(finalFragments) == 0 {
		importSet["os"] = true
		importSet["fmt"] = true
	}

	// If any init fragments have error handling (contain "return fmt.Errorf"), we need os
	for _, frag := range initFragments {
		if findString(frag.Code, "return fmt.Errorf") != -1 {
			importSet["os"] = true
			importSet["fmt"] = true
			break
		}
	}

	for _, frag := range fragments {
		for _, imp := range frag.Imports {
			if imp != "" {
				importSet[imp] = true
			}
		}
	}

	// Build imports section
	var imports []string
	for imp := range importSet {
		imports = append(imports, imp)
	}

	// Sort imports for consistent output
	sortImports(imports)

	// Build the complete program
	var code string
	code += "package main\n\n"

	// Add command pipeline as block comment for easy copy-paste
	var commands []string
	for _, frag := range fragments {
		if frag.Command != "" {
			commands = append(commands, frag.Command)
		}
	}
	if len(commands) > 0 {
		code += "/*\n"
		code += fmt.Sprintf("Generated by ssql %s:\n\n", version.Version)
		code += "export SSQLGO=1\n"
		for _, cmd := range commands {
			code += cmd
			code += " |\n"
		}
		code += "ssql generate-go\n"
		code += "unset SSQLGO\n"
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
	if len(preCompileVars) > 0 {
		for _, varDecl := range preCompileVars {
			code += varDecl + "\n"
		}
		code += "\n"
	}

	// Add main function
	code += "func main() {\n"

	// Add init fragments (with proper error handling)
	for _, frag := range initFragments {
		code += "\t" + fixErrorHandling(frag.Code) + "\n"
	}

	// Separate stmt fragments into main pipeline vs side computations
	// Main pipeline fragments trace back to first init through their input chain
	// Side computation fragments (e.g., from process substitution) should be emitted directly
	var mainPipelineFragments []*CodeFragment
	var sideFragments []*CodeFragment

	// Build a map of variable names to their producing fragments
	varProducers := make(map[string]*CodeFragment)
	for _, frag := range initFragments {
		varProducers[frag.Var] = frag
	}
	for _, frag := range stmtFragments {
		varProducers[frag.Var] = frag
	}

	// The main pipeline root is the first init fragment's variable
	var mainPipelineRoot string
	if len(initFragments) > 0 {
		mainPipelineRoot = initFragments[0].Var
	} else {
		mainPipelineRoot = "records"
	}

	// Helper to trace if a variable chains back to main pipeline root
	tracesToMain := func(varName string) bool {
		visited := make(map[string]bool)
		current := varName
		for {
			if current == mainPipelineRoot {
				return true
			}
			if current == "" {
				// Reached a root that is NOT the main pipeline root
				return false
			}
			if visited[current] {
				return false // cycle detection
			}
			visited[current] = true
			frag, ok := varProducers[current]
			if !ok {
				return false
			}
			current = frag.Input
		}
	}

	// Categorize stmt fragments
	for _, frag := range stmtFragments {
		if tracesToMain(frag.Input) {
			mainPipelineFragments = append(mainPipelineFragments, frag)
		} else {
			sideFragments = append(sideFragments, frag)
		}
	}

	// Emit side computation fragments first (they produce variables used by main pipeline)
	for _, frag := range sideFragments {
		code += "\t" + fixErrorHandling(frag.Code) + "\n"
	}

	// Build Chain() call if we have multiple main pipeline fragments
	if len(mainPipelineFragments) > 1 {
		// Extract the output variable (from last main pipeline fragment)
		outputVar := mainPipelineFragments[len(mainPipelineFragments)-1].Var

		// Build filters array
		code += "\t" + outputVar + " := ssql.Chain(\n"
		for _, frag := range mainPipelineFragments {
			// Extract just the filter function from the code
			// Pattern: "var := filter(input)" -> "filter"
			filterCode := extractFilter(frag.Code)
			code += "\t\t" + filterCode + ",\n"
		}
		code += "\t)(" + mainPipelineRoot + ")\n"
	} else if len(mainPipelineFragments) == 1 {
		// Single transformation - use directly
		code += "\t" + fixErrorHandling(mainPipelineFragments[0].Code) + "\n"
	}

	// Add final fragments (e.g., write-csv)
	if len(finalFragments) > 0 {
		for _, frag := range finalFragments {
			code += "\t" + fixErrorHandling(frag.Code) + "\n"
		}
	} else {
		// No final fragment - auto-add JSONL output
		// Find the last output variable from the main pipeline
		var outputVar string
		if len(mainPipelineFragments) > 0 {
			outputVar = mainPipelineFragments[len(mainPipelineFragments)-1].Var
		} else if len(initFragments) > 0 {
			outputVar = initFragments[0].Var // main pipeline root
		} else {
			outputVar = "records"
		}

		// Add JSONL output code using ssql.WriteJSONToWriter
		code += "\t// Output records as JSONL\n"
		code += fmt.Sprintf("\tif err := ssql.WriteJSONToWriter(%s, os.Stdout); err != nil {\n", outputVar)
		code += "\t\tfmt.Fprintf(os.Stderr, \"Error writing output: %v\\n\", err)\n"
		code += "\t\tos.Exit(1)\n"
		code += "\t}\n"
	}

	code += "}\n"

	return code, nil
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
