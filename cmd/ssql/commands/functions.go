package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterFunctions registers the functions subcommand
func RegisterFunctions(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("functions").
		Description("Show available expression functions and operators").
		Example("ssql functions", "List all function categories").
		Example("ssql functions -category string", "Show string functions in detail").
		Example("ssql functions -examples", "Show common expression patterns").
		Flag("-category", "-c").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"string", "math", "array", "type", "hash", "operators", "helpers"}}).
		Global().
		Default("").
		Help("Show detailed help for a category (string, math, array, type, operators, helpers)").
		Done().
		Flag("-examples", "-e").
		Bool().
		Global().
		Help("Show common expression patterns and examples").
		Done().
		Handler(func(ctx *cf.Context) error {
			var category string
			var showExamples bool

			if cat, ok := ctx.GlobalFlags["-category"]; ok {
				category = cat.(string)
			}

			if ex, ok := ctx.GlobalFlags["-examples"]; ok {
				showExamples = ex.(bool)
			}

			if showExamples {
				return printExamples()
			}

			if category == "" {
				return printAllCategories()
			}

			return printCategory(category)
		}).
		Done()
	return cmd
}

func printAllCategories() error {
	fmt.Println("EXPRESSION FUNCTIONS AVAILABLE:")
	fmt.Println()
	fmt.Println("String Functions (10):")
	fmt.Println("  upper(str), lower(str), trim(str), split(str, sep), join(arr, sep),")
	fmt.Println("  startsWith(str, prefix), endsWith(str, suffix), contains(str, substr),")
	fmt.Println("  replace(str, old, new), replaceRegex(str, pattern, replacement)")
	fmt.Println()
	fmt.Println("Math Functions (6):")
	fmt.Println("  round(num), floor(num), ceil(num), abs(num), min(a, b), max(a, b)")
	fmt.Println()
	fmt.Println("Array Functions (7):")
	fmt.Println("  len(arr), filter(arr, pred), map(arr, fn), sum(arr), all(arr, pred),")
	fmt.Println("  any(arr, pred), count(arr)")
	fmt.Println()
	fmt.Println("Type Conversion (3):")
	fmt.Println("  int(value), float(value), string(value)")
	fmt.Println()
	fmt.Println("Hash Functions (3):")
	fmt.Println("  sha256(str), sha1(str), md5(str)")
	fmt.Println()
	fmt.Println("Helpers (2):")
	fmt.Println("  has(field), getOr(field, default)")
	fmt.Println()
	fmt.Println("Use: ssql functions -category <name>   # Show detailed help for category")
	fmt.Println("     ssql functions -examples          # Show common expression patterns")
	fmt.Println()
	fmt.Println("Full reference: doc/EXPRESSIONS.md")
	return nil
}

func printCategory(category string) error {
	switch strings.ToLower(category) {
	case "string":
		return printStringFunctions()
	case "math":
		return printMathFunctions()
	case "array":
		return printArrayFunctions()
	case "type":
		return printTypeFunctions()
	case "hash":
		return printHashFunctions()
	case "operators":
		return printOperators()
	case "helpers":
		return printHelpers()
	default:
		fmt.Printf("Unknown category: %s\n", category)
		fmt.Println()
		fmt.Println("Available categories: string, math, array, type, hash, operators, helpers")
		return nil
	}
}

func printStringFunctions() error {
	fmt.Println("STRING FUNCTIONS:")
	fmt.Println()
	fmt.Println("  upper(str)              Convert to uppercase")
	fmt.Println("    Example: upper(\"hello\") → \"HELLO\"")
	fmt.Println()
	fmt.Println("  lower(str)              Convert to lowercase")
	fmt.Println("    Example: lower(\"WORLD\") → \"world\"")
	fmt.Println()
	fmt.Println("  trim(str)               Remove leading/trailing whitespace")
	fmt.Println("    Example: trim(\"  text  \") → \"text\"")
	fmt.Println()
	fmt.Println("  split(str, sep)         Split string into array")
	fmt.Println("    Example: split(\"a,b,c\", \",\") → [\"a\", \"b\", \"c\"]")
	fmt.Println()
	fmt.Println("  join(arr, sep)          Join array into string")
	fmt.Println("    Example: join([\"a\", \"b\"], \",\") → \"a,b\"")
	fmt.Println()
	fmt.Println("  startsWith(str, prefix) Check if starts with prefix")
	fmt.Println("    Example: startsWith(\"hello\", \"he\") → true")
	fmt.Println()
	fmt.Println("  endsWith(str, suffix)   Check if ends with suffix")
	fmt.Println("    Example: endsWith(\"world\", \"ld\") → true")
	fmt.Println()
	fmt.Println("  contains(str, substr)   Check if contains substring")
	fmt.Println("    Example: contains(\"hello\", \"ll\") → true")
	fmt.Println()
	fmt.Println("  replace(str, old, new)  Replace all occurrences of substring")
	fmt.Println("    Example: replace(\"hello world\", \"world\", \"there\") → \"hello there\"")
	fmt.Println()
	fmt.Println("  replaceRegex(str, pattern, replacement)")
	fmt.Println("                          Regex replace with capture group support")
	fmt.Println("    Example: replaceRegex(\"abc 123\", \"[^0-9]\", \"\") → \"123\"")
	fmt.Println("    Example: replaceRegex(phone, \"(\\\\d{3})(\\\\d{4})\", \"$1-$2\")")
	fmt.Println("    Note: Use \\\\ for regex backslashes (e.g., \\\\d, \\\\w, \\\\s)")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Println("  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Println("  ssql update -set-expr clean 'replaceRegex(name, \"[^a-zA-Z]\", \"_\")'")
	fmt.Println("  ssql where -expr 'startsWith(name, \"A\")'")
	return nil
}

func printMathFunctions() error {
	fmt.Println("MATH FUNCTIONS:")
	fmt.Println()
	fmt.Println("  round(num)     Round to nearest integer")
	fmt.Println("    Example: round(3.7) → 4")
	fmt.Println()
	fmt.Println("  floor(num)     Round down")
	fmt.Println("    Example: floor(3.7) → 3")
	fmt.Println()
	fmt.Println("  ceil(num)      Round up")
	fmt.Println("    Example: ceil(3.2) → 4")
	fmt.Println()
	fmt.Println("  abs(num)       Absolute value")
	fmt.Println("    Example: abs(-5) → 5")
	fmt.Println()
	fmt.Println("  min(a, b)      Minimum of two values")
	fmt.Println("    Example: min(10, 20) → 10")
	fmt.Println()
	fmt.Println("  max(a, b)      Maximum of two values")
	fmt.Println("    Example: max(10, 20) → 20")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr final_price 'round(price * 0.85)'")
	fmt.Println("  ssql update -set-expr balance 'max(0, amount - fees)'")
	fmt.Println("  ssql where -expr 'abs(actual - expected) < 0.01'")
	return nil
}

func printArrayFunctions() error {
	fmt.Println("ARRAY FUNCTIONS:")
	fmt.Println()
	fmt.Println("  len(arr)                Length of array/string")
	fmt.Println("    Example: len([1, 2, 3]) → 3")
	fmt.Println()
	fmt.Println("  all(arr, predicate)     Check if all elements match")
	fmt.Println("    Example: all([2, 4, 6], {# % 2 == 0}) → true")
	fmt.Println("    Note: # represents current element")
	fmt.Println()
	fmt.Println("  any(arr, predicate)     Check if any element matches")
	fmt.Println("    Example: any([1, 2, 3], {# > 2}) → true")
	fmt.Println()
	fmt.Println("  filter(arr, predicate)  Filter array elements")
	fmt.Println("    Example: filter([1, 2, 3], {# > 1}) → [2, 3]")
	fmt.Println()
	fmt.Println("  map(arr, transform)     Transform array elements")
	fmt.Println("    Example: map([1, 2, 3], {# * 2}) → [2, 4, 6]")
	fmt.Println()
	fmt.Println("  sum(arr)                Sum of array elements")
	fmt.Println("    Example: sum([1, 2, 3]) → 6")
	fmt.Println()
	fmt.Println("  count(arr)              Count of array elements")
	fmt.Println("    Example: count([1, 2, 3]) → 3")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql where -expr 'all(scores, {# >= 60})'")
	fmt.Println("  ssql update -set-expr total 'sum(prices)'")
	return nil
}

func printTypeFunctions() error {
	fmt.Println("TYPE CONVERSION FUNCTIONS:")
	fmt.Println()
	fmt.Println("  int(value)      Convert to integer")
	fmt.Println("    Example: int(\"123\") → 123")
	fmt.Println()
	fmt.Println("  float(value)    Convert to float")
	fmt.Println("    Example: float(\"3.14\") → 3.14")
	fmt.Println()
	fmt.Println("  string(value)   Convert to string")
	fmt.Println("    Example: string(123) → \"123\"")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr age_num 'int(age_str)'")
	fmt.Println("  ssql update -set-expr label 'string(round(value * 100)) + \"%\"'")
	return nil
}

func printHashFunctions() error {
	fmt.Println("HASH FUNCTIONS:")
	fmt.Println()
	fmt.Println("  sha256(str)    SHA-256 hash (64 hex characters)")
	fmt.Println("    Example: sha256(\"hello\") → \"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\"")
	fmt.Println()
	fmt.Println("  sha1(str)      SHA-1 hash (40 hex characters)")
	fmt.Println("    Example: sha1(\"hello\") → \"aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d\"")
	fmt.Println()
	fmt.Println("  md5(str)       MD5 hash (32 hex characters)")
	fmt.Println("    Example: md5(\"hello\") → \"5d41402abc4b2a76b9719d911017c592\"")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr email_hash 'sha256(email)'")
	fmt.Println("  ssql update -set-expr id 'sha256(name + \":\" + email)'")
	fmt.Println("  ssql update -set-expr checksum 'md5(content)'")
	fmt.Println()
	fmt.Println("Note: For security-sensitive applications, prefer sha256 over md5/sha1.")
	return nil
}

func printOperators() error {
	fmt.Println("OPERATORS:")
	fmt.Println()
	fmt.Println("Arithmetic:")
	fmt.Println("  +    Addition:         price + tax")
	fmt.Println("  -    Subtraction:      revenue - cost")
	fmt.Println("  *    Multiplication:   price * qty")
	fmt.Println("  /    Division:         total / count")
	fmt.Println("  %    Modulo:           value % 10")
	fmt.Println("  **   Power:            base ** exponent")
	fmt.Println()
	fmt.Println("Comparison:")
	fmt.Println("  ==   Equal:            status == \"active\"")
	fmt.Println("  !=   Not equal:        dept != \"Sales\"")
	fmt.Println("  <    Less than:        age < 18")
	fmt.Println("  >    Greater than:     salary > 50000")
	fmt.Println("  <=   Less/equal:       score <= 100")
	fmt.Println("  >=   Greater/equal:    age >= 21")
	fmt.Println()
	fmt.Println("Logical:")
	fmt.Println("  and  Logical AND:      age >= 18 and status == \"active\"")
	fmt.Println("  or   Logical OR:       dept == \"Sales\" or dept == \"Marketing\"")
	fmt.Println("  not  Logical NOT:      not contains(email, \"@test.com\")")
	fmt.Println()
	fmt.Println("Special:")
	fmt.Println("  ? :  Ternary:          age >= 18 ? \"adult\" : \"minor\"")
	fmt.Println("  ??   Nil coalescing:   value ?? \"default\"")
	fmt.Println("  in   Membership:       status in [\"active\", \"pending\"]")
	fmt.Println("  |    Pipe:             name | trim | upper")
	return nil
}

func printHelpers() error {
	fmt.Println("HELPER FUNCTIONS (ssql-specific):")
	fmt.Println()
	fmt.Println("  has(field)              Check if field exists")
	fmt.Println("    Example: has(\"email\") → true/false")
	fmt.Println("    Usage:   ssql where -expr 'has(\"email\") and contains(email, \"@\")'")
	fmt.Println()
	fmt.Println("  getOr(field, default)   Get field value or default")
	fmt.Println("    Example: getOr(\"age\", 0) → field value or 0")
	fmt.Println("    Usage:   ssql update -set-expr total 'getOr(\"price\", 0) * getOr(\"qty\", 1)'")
	fmt.Println()
	fmt.Println("Why use these?")
	fmt.Println("  - Prevents errors when fields are missing or sparse")
	fmt.Println("  - Enables expressions to work gracefully with incomplete data")
	fmt.Println("  - Provides sensible defaults for missing values")
	return nil
}

func printExamples() error {
	fmt.Println("COMMON EXPRESSION PATTERNS:")
	fmt.Println()
	fmt.Println("Data Validation:")
	fmt.Println("  ssql where -expr 'has(\"email\") and contains(email, \"@\")'")
	fmt.Println("  ssql where -expr 'age >= 0 and age <= 120'")
	fmt.Println()
	fmt.Println("Data Cleaning:")
	fmt.Println("  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Println("  ssql update -set-expr status 'getOr(\"status\", \"pending\")'")
	fmt.Println()
	fmt.Println("Calculations:")
	fmt.Println("  ssql update -set-expr total 'price * qty'")
	fmt.Println("  ssql update -set-expr discount 'total > 1000 ? total * 0.1 : 0'")
	fmt.Println("  ssql update -set-expr final 'round((price * qty) * (1 - discount / 100))'")
	fmt.Println()
	fmt.Println("Complex Filters:")
	fmt.Println("  ssql where -expr 'age >= 18 and age <= 65 and status == \"active\"'")
	fmt.Println("  ssql where -expr '(age >= 18 and verified) or role == \"admin\"'")
	fmt.Println()
	fmt.Println("String Manipulation:")
	fmt.Println("  ssql update -set-expr full_name 'first + \" \" + last'")
	fmt.Println("  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Println()
	fmt.Println("Categorization:")
	fmt.Println("  ssql update -set-expr category 'age < 18 ? \"minor\" : \"adult\"'")
	fmt.Println("  ssql update -set-expr tier 'revenue > 10000 ? \"gold\" : (revenue > 5000 ? \"silver\" : \"bronze\")'")
	fmt.Println()
	fmt.Println("Full reference: doc/EXPRESSIONS.md")
	return nil
}
