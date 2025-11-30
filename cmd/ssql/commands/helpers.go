package commands

import (
	"fmt"
	"iter"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib/runtime"
)

// Helper functions for command handlers

func extractNumeric(val any) float64 {
	switch v := val.(type) {
	case int64:
		return float64(v)
	case float64:
		return v
	case string:
		// For strings, use 0 (they'll maintain relative order)
		return 0
	default:
		return 0
	}
}


// applyOperator applies a comparison operator for where command
func applyOperator(fieldValue any, op string, compareValue string) bool {
	switch op {
	case "eq":
		return compareEqual(fieldValue, compareValue)
	case "ne":
		return !compareEqual(fieldValue, compareValue)
	case "gt":
		return compareGreater(fieldValue, compareValue)
	case "ge":
		return compareGreater(fieldValue, compareValue) || compareEqual(fieldValue, compareValue)
	case "lt":
		return compareLess(fieldValue, compareValue)
	case "le":
		return compareLess(fieldValue, compareValue) || compareEqual(fieldValue, compareValue)
	case "contains":
		return compareContains(fieldValue, compareValue)
	case "startswith":
		return compareStartsWith(fieldValue, compareValue)
	case "endswith":
		return compareEndsWith(fieldValue, compareValue)
	case "regex":
		return comparePattern(fieldValue, compareValue)
	default:
		return false
	}
}

func compareEqual(fieldValue any, compareValue string) bool {
	switch v := fieldValue.(type) {
	case string:
		return v == compareValue
	case int64:
		if num, err := strconv.ParseInt(compareValue, 10, 64); err == nil {
			return v == num
		}
	case float64:
		if num, err := strconv.ParseFloat(compareValue, 64); err == nil {
			return v == num
		}
	case bool:
		if b, err := strconv.ParseBool(compareValue); err == nil {
			return v == b
		}
	}
	return fmt.Sprintf("%v", fieldValue) == compareValue
}

func compareGreater(fieldValue any, compareValue string) bool {
	switch v := fieldValue.(type) {
	case int64:
		if num, err := strconv.ParseInt(compareValue, 10, 64); err == nil {
			return v > num
		}
	case float64:
		if num, err := strconv.ParseFloat(compareValue, 64); err == nil {
			return v > num
		}
	case string:
		return v > compareValue
	}
	return false
}

func compareLess(fieldValue any, compareValue string) bool {
	switch v := fieldValue.(type) {
	case int64:
		if num, err := strconv.ParseInt(compareValue, 10, 64); err == nil {
			return v < num
		}
	case float64:
		if num, err := strconv.ParseFloat(compareValue, 64); err == nil {
			return v < num
		}
	case string:
		return v < compareValue
	}
	return false
}

func compareContains(fieldValue any, compareValue string) bool {
	if str, ok := fieldValue.(string); ok {
		return contains(str, compareValue)
	}
	return false
}

func compareStartsWith(fieldValue any, compareValue string) bool {
	if str, ok := fieldValue.(string); ok {
		return len(str) >= len(compareValue) && str[:len(compareValue)] == compareValue
	}
	return false
}

func compareEndsWith(fieldValue any, compareValue string) bool {
	if str, ok := fieldValue.(string); ok {
		return len(str) >= len(compareValue) && str[len(str)-len(compareValue):] == compareValue
	}
	return false
}

func comparePattern(fieldValue any, pattern string) bool {
	if str, ok := fieldValue.(string); ok {
		matched, err := regexp.MatchString(pattern, str)
		if err != nil {
			return false
		}
		return matched
	}
	return false
}

func contains(str, substr string) bool {
	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// buildAggregator builds a StreamV3 AggregateFunc from a spec (for group-by command)
func buildAggregator(function, field string) (ssql.AggregateFunc, error) {
	switch function {
	case "count":
		return ssql.Count(), nil
	case "sum":
		return ssql.Sum(field), nil
	case "avg":
		return ssql.Avg(field), nil
	case "min":
		return ssql.Min[float64](field), nil
	case "max":
		return ssql.Max[float64](field), nil
	default:
		return nil, fmt.Errorf("unknown aggregation function: %s", function)
	}
}

// unionRecordToKey converts a record to a string key for deduplication (for union command)
func unionRecordToKey(r ssql.Record) string {
	// Use JSON representation as unique key
	return fmt.Sprintf("%v", r)
}

// chainRecords chains multiple data sources into a single stream (for union command)
func chainRecords(firstRecords iter.Seq[ssql.Record], additionalFiles []string) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		// Yield from first stream
		for record := range firstRecords {
			if !yield(record) {
				return
			}
		}

		// Yield from each additional file
		for _, file := range additionalFiles {
			var records iter.Seq[ssql.Record]

			if strings.HasSuffix(file, ".csv") {
				// Read CSV
				csvRecords, err := ssql.ReadCSV(file)
				if err != nil {
					// Skip file on error
					continue
				}
				records = csvRecords
			} else {
				// Read JSONL
				f, err := os.Open(file)
				if err != nil {
					continue
				}
				records = lib.ReadJSONL(f)
				defer f.Close()
			}

			// Yield from this file
			for record := range records {
				if !yield(record) {
					return
				}
			}
		}
	}
}

// shouldGenerate checks if code generation is enabled via flag or environment variable
// Returns true if:
//   - The generate flag is explicitly set to true, OR
//   - The SSQLGO environment variable is set to "1" or "true"
func shouldGenerate(flagValue bool) bool {
	if flagValue {
		return true
	}
	envValue := os.Getenv("SSQLGO")
	return envValue == "1" || envValue == "true"
}

// getCommandString returns the command line that invoked this command
// Filters out the -generate flag since it's implied by the code generation context
// Returns something like "ssql read-csv data.csv" or "ssql where -match age gt 18"
// Properly quotes arguments that contain shell special characters
func getCommandString() string {
	// Filter out -generate and -g flags
	var args []string
	skipNext := false
	for i, arg := range os.Args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "-generate" || arg == "-g" {
			continue
		}
		// For the binary name, use just "ssql" instead of full path
		if i == 0 {
			args = append(args, "ssql")
		} else {
			// Quote the argument if it needs quoting for shell safety
			args = append(args, shellQuote(arg))
		}
	}
	return strings.Join(args, " ")
}

// shellQuote quotes a string for safe use in shell commands
// Returns the string with appropriate quoting if needed
func shellQuote(s string) string {
	// If the string is simple (alphanumeric, dash, underscore, dot, slash, colon), no quoting needed
	needsQuoting := false
	for _, c := range s {
		if !isSimpleShellChar(c) {
			needsQuoting = true
			break
		}
	}

	if !needsQuoting {
		return s
	}

	// If string contains single quotes, use double quotes and escape special chars
	if strings.Contains(s, "'") {
		// Use double quotes, escape $, `, \, ", and !
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		escaped = strings.ReplaceAll(escaped, `$`, `\$`)
		escaped = strings.ReplaceAll(escaped, "`", "\\`")
		return `"` + escaped + `"`
	}

	// Otherwise use single quotes (most literal, safest)
	return "'" + s + "'"
}

// isSimpleShellChar returns true if the character is safe in shell without quoting
func isSimpleShellChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '/' || c == ':'
}

func parseValue(s string) any {
	// Try bool
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}

	// Try int64
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}

	// Try float64
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	// Try time.Time (common formats)
	timeFormats := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05",
	}
	for _, format := range timeFormats {
		if t, err := time.Parse(format, s); err == nil {
			return t
		}
	}

	// Default to string
	return s
}

// isExpression checks if a value string is an expression vs a literal
// Returns true if the string contains operators or function calls
func isExpression(value string) bool {
	// Heuristics to detect expressions:
	// - Contains arithmetic operators: +, -, *, /, %
	// - Contains comparison operators: >, <, ==, !=, >=, <=
	// - Contains logical operators: &&, ||, !
	// - Contains ternary operator: ?
	// - Contains function calls: (
	// - Contains field references in context (tricky - just check for operators)

	// Quick checks for common operators
	operators := []string{
		" + ", " - ", " * ", " / ", " % ",  // Math (with spaces to avoid false positives)
		">", "<", "==", "!=", ">=", "<=",    // Comparison
		"&&", "||",                          // Logical
		"?",                                 // Ternary
		"(",                                 // Function call
	}

	for _, op := range operators {
		if strings.Contains(value, op) {
			return true
		}
	}

	return false
}

// applyValueToRecord applies a value to a mutable record with automatic type inference
// Handles type conversions (int→int64, float32→float64) and defaults unknown types to string
func applyValueToRecord(mut ssql.MutableRecord, field string, value any) ssql.MutableRecord {
	switch v := value.(type) {
	case int64:
		return mut.Int(field, v)
	case float64:
		return mut.Float(field, v)
	case bool:
		return mut.Bool(field, v)
	case time.Time:
		return ssql.Set(mut, field, v)
	case string:
		return mut.String(field, v)
	case int:
		// expr might return int instead of int64
		return mut.Int(field, int64(v))
	case int32:
		return mut.Int(field, int64(v))
	case int16:
		return mut.Int(field, int64(v))
	case int8:
		return mut.Int(field, int64(v))
	case uint:
		return mut.Int(field, int64(v))
	case uint64:
		return mut.Int(field, int64(v))
	case uint32:
		return mut.Int(field, int64(v))
	case uint16:
		return mut.Int(field, int64(v))
	case uint8:
		return mut.Int(field, int64(v))
	case float32:
		// expr might return float32 instead of float64
		return mut.Float(field, float64(v))
	case nil:
		// For nil values, set as empty string (or could skip)
		return mut.String(field, "")
	default:
		// For unknown types, convert to string
		return mut.String(field, fmt.Sprintf("%v", v))
	}
}

// compileExpression compiles an expression once and returns a function that can evaluate it on different records.
// This is much more efficient than compiling on every record.
// This is a wrapper around runtime.CompileExpr for use within CLI commands.
func compileExpression(expression string) (func(ssql.Record) (any, error), error) {
	return runtime.CompileExpr(expression)
}

// evaluateExpression evaluates an expression on a single record (convenience wrapper).
// For better performance when evaluating on multiple records, use compileExpression once
// and call the returned function for each record.
func evaluateExpression(expression string, record ssql.Record) (any, error) {
	eval, err := compileExpression(expression)
	if err != nil {
		return nil, err
	}
	return eval(record)
}

