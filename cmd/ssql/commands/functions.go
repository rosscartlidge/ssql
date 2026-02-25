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
		Completer(&cf.StaticCompleter{Options: []string{"string", "math", "array", "type", "date", "map", "bitwise", "hash", "operators", "helpers"}}).
		Global().
		Default("").
		Help("Show detailed help for a category").
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
	fmt.Println("String Functions (16):")
	fmt.Println("  upper(str), lower(str), trim(str), trimPrefix(str, prefix),")
	fmt.Println("  trimSuffix(str, suffix), split(str, sep), splitAfter(str, sep),")
	fmt.Println("  join(arr, sep), replace(str, old, new), replaceRegex(str, pat, repl),")
	fmt.Println("  repeat(str, n), indexOf(str, sub), lastIndexOf(str, sub),")
	fmt.Println("  hasPrefix(str, prefix), hasSuffix(str, suffix), contains(str, sub)")
	fmt.Println()
	fmt.Println("Math Functions (6):")
	fmt.Println("  round(num), floor(num), ceil(num), abs(num), min(a, b), max(a, b)")
	fmt.Println()
	fmt.Println("Array Functions (26):")
	fmt.Println("  len(arr), first(arr), last(arr), take(arr, n), reverse(arr),")
	fmt.Println("  sort(arr), sortBy(arr, pred), uniq(arr), flatten(arr), concat(a, b),")
	fmt.Println("  filter(arr, pred), map(arr, fn), reduce(arr, pred, init),")
	fmt.Println("  find(arr, pred), findIndex(arr, pred), findLast(arr, pred),")
	fmt.Println("  findLastIndex(arr, pred), groupBy(arr, pred),")
	fmt.Println("  all(arr, pred), any(arr, pred), one(arr, pred), none(arr, pred),")
	fmt.Println("  count(arr), sum(arr), mean(arr), median(arr)")
	fmt.Println()
	fmt.Println("Date Functions (4):")
	fmt.Println("  now(), date(str), duration(str), timezone(str)")
	fmt.Println()
	fmt.Println("Type & Encoding (10):")
	fmt.Println("  int(v), float(v), string(v), type(v),")
	fmt.Println("  toJSON(v), fromJSON(str), toBase64(str), fromBase64(str),")
	fmt.Println("  toPairs(map), fromPairs(arr)")
	fmt.Println()
	fmt.Println("Map Functions (3):")
	fmt.Println("  keys(map), values(map), get(v, key)")
	fmt.Println()
	fmt.Println("Bitwise Functions (8):")
	fmt.Println("  bitand(a, b), bitor(a, b), bitxor(a, b), bitnand(a, b),")
	fmt.Println("  bitnot(a), bitshl(a, n), bitshr(a, n), bitushr(a, n)")
	fmt.Println()
	fmt.Println("Hash Functions (3):")
	fmt.Println("  sha256(str), sha1(str), md5(str)")
	fmt.Println()
	fmt.Println("Helpers (2):")
	fmt.Println("  has(field), getOr(field, default)")
	fmt.Println()
	fmt.Println("Operators:")
	fmt.Println("  +  -  *  /  %  **  ==  !=  <  >  <=  >=  and  or  not  ?:  ??  in  |")
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
	case "date":
		return printDateFunctions()
	case "map":
		return printMapFunctions()
	case "bitwise":
		return printBitwiseFunctions()
	case "hash":
		return printHashFunctions()
	case "operators":
		return printOperators()
	case "helpers":
		return printHelpers()
	default:
		fmt.Printf("Unknown category: %s\n", category)
		fmt.Println()
		fmt.Println("Available categories: string, math, array, date, type, map, bitwise, hash, operators, helpers")
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
	fmt.Println("  trimPrefix(str, prefix) Remove prefix from string")
	fmt.Println("    Example: trimPrefix(\"hello world\", \"hello \") → \"world\"")
	fmt.Println()
	fmt.Println("  trimSuffix(str, suffix) Remove suffix from string")
	fmt.Println("    Example: trimSuffix(\"hello.csv\", \".csv\") → \"hello\"")
	fmt.Println()
	fmt.Println("  split(str, sep)         Split string into array")
	fmt.Println("    Example: split(\"a,b,c\", \",\") → [\"a\", \"b\", \"c\"]")
	fmt.Println()
	fmt.Println("  splitAfter(str, sep)    Split string, keeping separator attached")
	fmt.Println("    Example: splitAfter(\"a,b,c\", \",\") → [\"a,\", \"b,\", \"c\"]")
	fmt.Println()
	fmt.Println("  join(arr, sep)          Join array into string")
	fmt.Println("    Example: join([\"a\", \"b\"], \",\") → \"a,b\"")
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
	fmt.Println("  repeat(str, n)          Repeat string n times")
	fmt.Println("    Example: repeat(\"ab\", 3) → \"ababab\"")
	fmt.Println()
	fmt.Println("  indexOf(str, sub)       First index of substring (-1 if not found)")
	fmt.Println("    Example: indexOf(\"hello\", \"ll\") → 2")
	fmt.Println()
	fmt.Println("  lastIndexOf(str, sub)   Last index of substring (-1 if not found)")
	fmt.Println("    Example: lastIndexOf(\"hello\", \"l\") → 3")
	fmt.Println()
	fmt.Println("  hasPrefix(str, prefix)  Check if starts with prefix")
	fmt.Println("    Example: hasPrefix(\"hello\", \"he\") → true")
	fmt.Println()
	fmt.Println("  hasSuffix(str, suffix)  Check if ends with suffix")
	fmt.Println("    Example: hasSuffix(\"world\", \"ld\") → true")
	fmt.Println()
	fmt.Println("  contains(str, substr)   Check if contains substring")
	fmt.Println("    Example: contains(\"hello\", \"ll\") → true")
	fmt.Println()
	fmt.Println("  Note: contains, startsWith, endsWith also work as operators:")
	fmt.Println("    name startsWith \"A\"    email contains \"@\"    file endsWith \".csv\"")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Println("  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Println("  ssql update -set-expr clean 'replaceRegex(name, \"[^a-zA-Z]\", \"_\")'")
	fmt.Println("  ssql update -set-expr base 'trimSuffix(filename, \".csv\")'")
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
	fmt.Println("  ssql where -where-expr 'abs(actual - expected) < 0.01'")
	return nil
}

func printArrayFunctions() error {
	fmt.Println("ARRAY FUNCTIONS:")
	fmt.Println()
	fmt.Println("Access:")
	fmt.Println("  len(arr)                  Length of array/string")
	fmt.Println("  first(arr)                First element")
	fmt.Println("  last(arr)                 Last element")
	fmt.Println("  get(v, key)               Safe access (returns nil if missing)")
	fmt.Println()
	fmt.Println("Transform:")
	fmt.Println("  take(arr, n)              First n elements")
	fmt.Println("  reverse(arr)              Reverse array")
	fmt.Println("  sort(arr)                 Sort ascending")
	fmt.Println("  sortBy(arr, pred)         Sort by predicate result")
	fmt.Println("    Example: sortBy([{\"n\":3},{\"n\":1}], {.n}) → [{\"n\":1},{\"n\":3}]")
	fmt.Println("  uniq(arr)                 Remove duplicates")
	fmt.Println("  flatten(arr)              Flatten nested arrays")
	fmt.Println("  concat(arr1, arr2)        Concatenate arrays")
	fmt.Println()
	fmt.Println("Filter & Search:")
	fmt.Println("  filter(arr, pred)         Filter elements matching predicate")
	fmt.Println("  find(arr, pred)           First matching element")
	fmt.Println("  findIndex(arr, pred)      Index of first match")
	fmt.Println("  findLast(arr, pred)       Last matching element")
	fmt.Println("  findLastIndex(arr, pred)  Index of last match")
	fmt.Println("  groupBy(arr, pred)        Group elements by predicate result")
	fmt.Println("    Example: groupBy([1,2,3,4], {# % 2 == 0 ? \"even\" : \"odd\"})")
	fmt.Println()
	fmt.Println("Test:")
	fmt.Println("  all(arr, pred)            All elements match")
	fmt.Println("  any(arr, pred)            At least one matches")
	fmt.Println("  one(arr, pred)            Exactly one matches")
	fmt.Println("  none(arr, pred)           No elements match")
	fmt.Println()
	fmt.Println("Aggregate:")
	fmt.Println("  count(arr)                Count of elements")
	fmt.Println("  sum(arr)                  Sum of elements")
	fmt.Println("  mean(arr)                 Average of elements")
	fmt.Println("  median(arr)               Median of elements")
	fmt.Println("  reduce(arr, pred, init)   Reduce to single value")
	fmt.Println("    Example: reduce([1,2,3,4], {# + #acc}, 0) → 10")
	fmt.Println("  map(arr, fn)              Transform each element")
	fmt.Println("    Example: map([1,2,3], {# * 2}) → [2,4,6]")
	fmt.Println()
	fmt.Println("  Note: In predicates, # = current element, #acc = accumulator")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql where -where-expr 'all(scores, {# >= 60})'")
	fmt.Println("  ssql update -set-expr avg_score 'mean(scores)'")
	fmt.Println("  ssql update -set-expr top3 'take(sort(scores), 3)'")
	return nil
}

func printDateFunctions() error {
	fmt.Println("DATE FUNCTIONS:")
	fmt.Println()
	fmt.Println("  now()                   Current date/time")
	fmt.Println("    Example: now() → 2026-02-25T10:30:00+11:00")
	fmt.Println()
	fmt.Println("  date(str)               Parse date string")
	fmt.Println("    Example: date(\"2026-01-15\")")
	fmt.Println()
	fmt.Println("  duration(str)           Parse duration string")
	fmt.Println("    Example: duration(\"1h30m\") → 1h30m0s")
	fmt.Println("    Supports: ns, us, ms, s, m, h")
	fmt.Println()
	fmt.Println("  timezone(str)           Get timezone location")
	fmt.Println("    Example: timezone(\"America/New_York\")")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr timestamp 'string(now())'")
	fmt.Println("  ssql where -where-expr 'date(created) > date(\"2026-01-01\")'")
	return nil
}

func printTypeFunctions() error {
	fmt.Println("TYPE & ENCODING FUNCTIONS:")
	fmt.Println()
	fmt.Println("Type Conversion:")
	fmt.Println("  int(value)              Convert to integer")
	fmt.Println("    Example: int(\"123\") → 123")
	fmt.Println()
	fmt.Println("  float(value)            Convert to float")
	fmt.Println("    Example: float(\"3.14\") → 3.14")
	fmt.Println()
	fmt.Println("  string(value)           Convert to string")
	fmt.Println("    Example: string(123) → \"123\"")
	fmt.Println()
	fmt.Println("  type(value)             Get type name as string")
	fmt.Println("    Example: type(42) → \"int\"")
	fmt.Println()
	fmt.Println("JSON:")
	fmt.Println("  toJSON(value)           Serialize to JSON string")
	fmt.Println("    Example: toJSON({\"a\": 1}) → \"{\\\"a\\\":1}\"")
	fmt.Println()
	fmt.Println("  fromJSON(str)           Parse JSON string to value")
	fmt.Println("    Example: fromJSON(\"{\\\"a\\\":1}\") → {\"a\": 1}")
	fmt.Println()
	fmt.Println("Base64:")
	fmt.Println("  toBase64(str)           Encode to Base64")
	fmt.Println("    Example: toBase64(\"hello\") → \"aGVsbG8=\"")
	fmt.Println()
	fmt.Println("  fromBase64(str)         Decode from Base64")
	fmt.Println("    Example: fromBase64(\"aGVsbG8=\") → \"hello\"")
	fmt.Println()
	fmt.Println("Map Conversion:")
	fmt.Println("  toPairs(map)            Map to [[key, value], ...] pairs")
	fmt.Println("    Example: toPairs({\"a\":1}) → [[\"a\", 1]]")
	fmt.Println()
	fmt.Println("  fromPairs(arr)          [[key, value], ...] pairs to map")
	fmt.Println("    Example: fromPairs([[\"a\",1]]) → {\"a\": 1}")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr age_num 'int(age_str)'")
	fmt.Println("  ssql update -set-expr label 'string(round(value * 100)) + \"%\"'")
	fmt.Println("  ssql update -set-expr encoded 'toBase64(email)'")
	return nil
}

func printMapFunctions() error {
	fmt.Println("MAP FUNCTIONS:")
	fmt.Println()
	fmt.Println("  keys(map)               Get all keys from map")
	fmt.Println("    Example: keys({\"a\":1, \"b\":2}) → [\"a\", \"b\"]")
	fmt.Println()
	fmt.Println("  values(map)             Get all values from map")
	fmt.Println("    Example: values({\"a\":1, \"b\":2}) → [1, 2]")
	fmt.Println()
	fmt.Println("  get(v, key)             Safe access (returns nil if missing)")
	fmt.Println("    Example: get({\"a\":1}, \"a\") → 1")
	fmt.Println("    Example: get({\"a\":1}, \"z\") → nil")
	fmt.Println("    Also works on arrays: get([10,20,30], 1) → 20")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr field_count 'len(keys(metadata))'")
	return nil
}

func printBitwiseFunctions() error {
	fmt.Println("BITWISE FUNCTIONS:")
	fmt.Println()
	fmt.Println("  bitand(a, b)     Bitwise AND")
	fmt.Println("    Example: bitand(0xFF, 0x0F) → 15")
	fmt.Println()
	fmt.Println("  bitor(a, b)      Bitwise OR")
	fmt.Println("    Example: bitor(0xF0, 0x0F) → 255")
	fmt.Println()
	fmt.Println("  bitxor(a, b)     Bitwise XOR")
	fmt.Println("    Example: bitxor(0xFF, 0x0F) → 240")
	fmt.Println()
	fmt.Println("  bitnand(a, b)    Bitwise AND NOT")
	fmt.Println("    Example: bitnand(0xFF, 0x0F) → 240")
	fmt.Println()
	fmt.Println("  bitnot(a)        Bitwise NOT")
	fmt.Println()
	fmt.Println("  bitshl(a, n)     Left shift")
	fmt.Println("    Example: bitshl(1, 4) → 16")
	fmt.Println()
	fmt.Println("  bitshr(a, n)     Right shift")
	fmt.Println("    Example: bitshr(16, 4) → 1")
	fmt.Println()
	fmt.Println("  bitushr(a, n)    Unsigned right shift")
	fmt.Println()
	fmt.Println("Common Usage:")
	fmt.Println("  ssql update -set-expr masked 'bitand(flags, 0x0F)'")
	fmt.Println("  ssql where -where-expr 'bitand(permissions, 4) != 0'")
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
	fmt.Println("String/Array:")
	fmt.Println("  contains     Contains:      email contains \"@\"")
	fmt.Println("  startsWith   Starts with:   name startsWith \"A\"")
	fmt.Println("  endsWith     Ends with:     file endsWith \".csv\"")
	fmt.Println()
	fmt.Println("Special:")
	fmt.Println("  ? :  Ternary:          age >= 18 ? \"adult\" : \"minor\"")
	fmt.Println("  ??   Nil coalescing:   value ?? \"default\"")
	fmt.Println("  in   Membership:       status in [\"active\", \"pending\"]")
	fmt.Println("  |    Pipe:             name | trim | upper")
	fmt.Println("  ..   Range:            1..5 → [1, 2, 3, 4, 5]")
	return nil
}

func printHelpers() error {
	fmt.Println("HELPER FUNCTIONS (ssql-specific):")
	fmt.Println()
	fmt.Println("  has(field)              Check if field exists")
	fmt.Println("    Example: has(\"email\") → true/false")
	fmt.Println("    Usage:   ssql where -where-expr 'has(\"email\") and contains(email, \"@\")'")
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
	fmt.Println("  ssql where -where-expr 'has(\"email\") and contains(email, \"@\")'")
	fmt.Println("  ssql where -where-expr 'age >= 0 and age <= 120'")
	fmt.Println()
	fmt.Println("Data Cleaning:")
	fmt.Println("  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Println("  ssql update -set-expr status 'getOr(\"status\", \"pending\")'")
	fmt.Println("  ssql update -set-expr clean 'replaceRegex(name, \"[^a-zA-Z ]\", \"\")'")
	fmt.Println("  ssql update -set-expr base 'trimSuffix(filename, \".csv\")'")
	fmt.Println()
	fmt.Println("Calculations:")
	fmt.Println("  ssql update -set-expr total 'price * qty'")
	fmt.Println("  ssql update -set-expr discount 'total > 1000 ? total * 0.1 : 0'")
	fmt.Println("  ssql update -set-expr final 'round((price * qty) * (1 - discount / 100))'")
	fmt.Println()
	fmt.Println("Array Operations:")
	fmt.Println("  ssql update -set-expr avg_score 'mean(scores)'")
	fmt.Println("  ssql update -set-expr top3 'take(sort(scores), 3)'")
	fmt.Println("  ssql where -where-expr 'none(scores, {# < 60})'")
	fmt.Println()
	fmt.Println("Complex Filters:")
	fmt.Println("  ssql where -where-expr 'age >= 18 and age <= 65 and status == \"active\"'")
	fmt.Println("  ssql where -where-expr '(age >= 18 and verified) or role == \"admin\"'")
	fmt.Println()
	fmt.Println("String Manipulation:")
	fmt.Println("  ssql update -set-expr full_name 'first + \" \" + last'")
	fmt.Println("  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Println("  ssql update -set-expr slug 'lower(join(split(trim(title), \" \"), \"-\"))'")
	fmt.Println()
	fmt.Println("Encoding:")
	fmt.Println("  ssql update -set-expr encoded 'toBase64(email)'")
	fmt.Println("  ssql update -set-expr payload 'toJSON({\"name\": name, \"age\": age})'")
	fmt.Println()
	fmt.Println("Categorization:")
	fmt.Println("  ssql update -set-expr category 'age < 18 ? \"minor\" : \"adult\"'")
	fmt.Println("  ssql update -set-expr tier 'revenue > 10000 ? \"gold\" : (revenue > 5000 ? \"silver\" : \"bronze\")'")
	fmt.Println()
	fmt.Println("Full reference: doc/EXPRESSIONS.md")
	return nil
}
