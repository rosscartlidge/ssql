package commands

import (
	"fmt"
	"iter"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
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

// fieldNames returns the sorted field names from a record (for error messages).
func fieldNames(r ssql.Record) []string {
	var names []string
	for k := range r.All() {
		if k != "_row_number" {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names
}

// validateFields checks that all given field names exist in the record.
// Returns an error listing missing fields and available fields, or nil if all exist.
func validateFields(r ssql.Record, fields []string, command string) error {
	var missing []string
	for _, f := range fields {
		if _, exists := ssql.Get[any](r, f); !exists {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s references unknown field(s): %s (available: %s)",
			command, strings.Join(missing, ", "), strings.Join(fieldNames(r), ", "))
	}
	return nil
}

// validateFieldsSchema checks that all given field names exist in the schema.
// Returns an error listing missing fields and available fields, or nil if all exist.
func validateFieldsSchema(schema *lib.Schema, fields []string, command string) error {
	if schema == nil {
		return nil // no schema to validate against
	}
	var missing []string
	for _, f := range fields {
		if !schema.HasField(f) {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		available := make([]string, len(schema.Fields))
		copy(available, schema.Fields)
		sort.Strings(available)
		return fmt.Errorf("%s references unknown field(s): %s (available: %s)",
			command, strings.Join(missing, ", "), strings.Join(available, ", "))
	}
	return nil
}

// Condition represents a parsed -if / +if field operator value condition.
type Condition struct {
	Field    string
	Operator string
	Value    string
	Negated  bool // true when specified as +if (negate the match)
}

// parseConditions parses -if flag values from autocli into Conditions.
// Validates that all operators are recognized. Returns an error for unknown operators.
func parseConditions(flagValue any) ([]Condition, error) {
	if flagValue == nil {
		return nil, nil
	}
	matches, ok := flagValue.([]any)
	if !ok {
		return nil, nil
	}
	var conditions []Condition
	for _, matchRaw := range matches {
		matchMap, ok := matchRaw.(map[string]any)
		if !ok {
			continue
		}
		field, _ := matchMap["field"].(string)
		op, _ := matchMap["operator"].(string)
		value, _ := matchMap["value"].(string)
		negated, _ := matchMap["_negated"].(bool)
		if field == "" || op == "" {
			continue
		}
		if !validOperators[op] {
			return nil, fmt.Errorf("unknown operator %q (valid: eq, ne, gt, ge, lt, le, contains, startswith, endswith, regex)", op)
		}
		conditions = append(conditions, Condition{field, op, value, negated})
	}
	return conditions, nil
}

// conditionFields returns the unique field names from a slice of conditions.
func conditionFields(conditions []Condition) []string {
	seen := make(map[string]bool)
	var fields []string
	for _, c := range conditions {
		if !seen[c.Field] {
			seen[c.Field] = true
			fields = append(fields, c.Field)
		}
	}
	return fields
}

// applyOperator applies a comparison operator.
// validOperators is the set of recognized comparison operators.
var validOperators = map[string]bool{
	"eq": true, "ne": true, "gt": true, "ge": true, "lt": true, "le": true,
	"contains": true, "startswith": true, "endswith": true, "regex": true,
}

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

// buildAggregator creates an aggregation function for the given function name and field.
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
	case "collect":
		return ssql.Collect(field), nil
	default:
		return nil, fmt.Errorf("unknown aggregation function: %s", function)
	}
}

// unionRecordToKey converts a record to a string key for deduplication (for union command)
func unionRecordToKey(r ssql.Record) string {
	// Use JSON representation as unique key
	return fmt.Sprintf("%v", r)
}

// chainRecords chains multiple JSONL data sources into a single stream (for union command)
// Secondary sources must be JSONL format. For CSV files, use process substitution:
//
//	ssql from data1.csv | ssql union -file <(ssql from csv data2.csv)
func chainRecords(firstRecords iter.Seq[ssql.Record], additionalFiles []string) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		// Yield from first stream
		for record := range firstRecords {
			if !yield(record) {
				return
			}
		}

		// Yield from each additional file (JSONL only)
		for _, file := range additionalFiles {
			f, err := os.Open(file)
			if err != nil {
				// Provide helpful error for non-JSONL files
				if !strings.HasPrefix(file, "/dev/fd/") && !strings.HasSuffix(strings.ToLower(file), ".jsonl") {
					fmt.Fprintf(os.Stderr, "Warning: cannot open %s: %v\nFor CSV files use: -file <(ssql from csv %s)\n", file, err, file)
				} else {
					fmt.Fprintf(os.Stderr, "Warning: cannot open %s: %v\n", file, err)
				}
				continue
			}

			records := lib.ReadJSONLWithSchema(f).Records
			for record := range records {
				if !yield(record) {
					f.Close()
					return
				}
			}
			f.Close()
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
// Returns something like "ssql from data.csv" or "ssql where -if age gt 18"
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
			args = append(args, ssql.ShellQuote(arg))
		}
	}
	return strings.Join(args, " ")
}

// flagVarName converts a flag name segment to a Go-safe identifier part.
// "timestamp" -> "Timestamp", "ge" -> "Ge", "date-start" -> "DateStart"
func flagVarName(s string) string {
	var result strings.Builder
	upper := true
	for _, c := range s {
		if c == '-' || c == '_' {
			upper = true
			continue
		}
		if upper {
			result.WriteRune(unicode.ToUpper(c))
			upper = false
		} else {
			result.WriteRune(c)
		}
	}
	return result.String()
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
		" + ", " - ", " * ", " / ", " % ", // Math (with spaces to avoid false positives)
		">", "<", "==", "!=", ">=", "<=", // Comparison
		"&&", "||", // Logical
		"?", // Ternary
		"(", // Function call
	}

	for _, op := range operators {
		if strings.Contains(value, op) {
			return true
		}
	}

	return false
}

// applyValueToRecordWithTypeCheck applies a value to a mutable record, coercing to the existing field's type.
// If the field exists, the new value is coerced to match the existing type.
// If the field doesn't exist, the value is applied with its natural type.
// Returns the modified record and true if a type coercion occurred.
func applyValueToRecordWithTypeCheck(mut ssql.MutableRecord, field string, value any, existingValue any, exists bool) (ssql.MutableRecord, bool) {
	if !exists {
		// Field doesn't exist, apply with natural type
		return applyValueToRecord(mut, field, value), false
	}

	// Determine the target type from existing value
	var coerced bool
	switch existingValue.(type) {
	case int64:
		coercedVal, didCoerce := coerceToInt64(value)
		if didCoerce {
			coerced = true
		}
		return mut.Int(field, coercedVal), coerced

	case float64:
		coercedVal, didCoerce := coerceToFloat64(value)
		if didCoerce {
			coerced = true
		}
		return mut.Float(field, coercedVal), coerced

	case bool:
		coercedVal, didCoerce := coerceToBool(value)
		if didCoerce {
			coerced = true
		}
		return mut.Bool(field, coercedVal), coerced

	case string:
		coercedVal, didCoerce := coerceToString(value)
		if didCoerce {
			coerced = true
		}
		return mut.String(field, coercedVal), coerced

	default:
		// Unknown existing type, apply with natural type
		return applyValueToRecord(mut, field, value), false
	}
}

// coerceToInt64 converts a value to int64, returning whether coercion occurred
func coerceToInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, false
	case int:
		return int64(v), false // Same logical type
	case float64:
		return int64(v), true
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int64(f), true
		}
		return 0, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, true
	}
}

// coerceToFloat64 converts a value to float64, returning whether coercion occurred
func coerceToFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, false
	case float32:
		return float64(v), false // Same logical type
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		return 0, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, true
	}
}

// coerceToBool converts a value to bool, returning whether coercion occurred
func coerceToBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, false
	case int64:
		return v != 0, true
	case int:
		return v != 0, true
	case float64:
		return v != 0, true
	case string:
		lower := strings.ToLower(v)
		switch lower {
		case "true", "1", "yes", "y", "on":
			return true, true
		default:
			return false, true
		}
	default:
		return false, true
	}
}

// coerceToString converts a value to string, returning whether coercion occurred
func coerceToString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, false
	case int64:
		return strconv.FormatInt(v, 10), true
	case int:
		return strconv.Itoa(v), true
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

// getDefaultForValue returns the default/zero value for the same type as the input value
func getDefaultForValue(value any) any {
	switch value.(type) {
	case int64, int, int32, int16, int8, uint, uint64, uint32, uint16, uint8:
		return int64(0)
	case float64, float32:
		return float64(0)
	case bool:
		return false
	case string:
		return ""
	default:
		return ""
	}
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
