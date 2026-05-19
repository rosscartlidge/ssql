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
				return printExamples(ctx)
			}

			if category == "" {
				return printAllCategories(ctx)
			}

			return printCategory(ctx, category)
		}).
		Done()
	return cmd
}

func printAllCategories(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "EXPRESSION FUNCTIONS AVAILABLE:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "String Functions (16):")
	fmt.Fprintln(ctx.Stdout(), "  upper(str), lower(str), trim(str), trimPrefix(str, prefix),")
	fmt.Fprintln(ctx.Stdout(), "  trimSuffix(str, suffix), split(str, sep), splitAfter(str, sep),")
	fmt.Fprintln(ctx.Stdout(), "  join(arr, sep), replace(str, old, new), replaceRegex(str, pat, repl),")
	fmt.Fprintln(ctx.Stdout(), "  repeat(str, n), indexOf(str, sub), lastIndexOf(str, sub),")
	fmt.Fprintln(ctx.Stdout(), "  hasPrefix(str, prefix), hasSuffix(str, suffix), contains(str, sub)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Math Functions (6):")
	fmt.Fprintln(ctx.Stdout(), "  round(num), floor(num), ceil(num), abs(num), min(a, b), max(a, b)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Array Functions (26):")
	fmt.Fprintln(ctx.Stdout(), "  len(arr), first(arr), last(arr), take(arr, n), reverse(arr),")
	fmt.Fprintln(ctx.Stdout(), "  sort(arr), sortBy(arr, pred), uniq(arr), flatten(arr), concat(a, b),")
	fmt.Fprintln(ctx.Stdout(), "  filter(arr, pred), map(arr, fn), reduce(arr, pred, init),")
	fmt.Fprintln(ctx.Stdout(), "  find(arr, pred), findIndex(arr, pred), findLast(arr, pred),")
	fmt.Fprintln(ctx.Stdout(), "  findLastIndex(arr, pred), groupBy(arr, pred),")
	fmt.Fprintln(ctx.Stdout(), "  all(arr, pred), any(arr, pred), one(arr, pred), none(arr, pred),")
	fmt.Fprintln(ctx.Stdout(), "  count(arr), sum(arr), mean(arr), median(arr)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Date Functions (4):")
	fmt.Fprintln(ctx.Stdout(), "  now(), date(str), duration(str), timezone(str)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Type & Encoding (10):")
	fmt.Fprintln(ctx.Stdout(), "  int(v), float(v), string(v), type(v),")
	fmt.Fprintln(ctx.Stdout(), "  toJSON(v), fromJSON(str), toBase64(str), fromBase64(str),")
	fmt.Fprintln(ctx.Stdout(), "  toPairs(map), fromPairs(arr)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Map Functions (3):")
	fmt.Fprintln(ctx.Stdout(), "  keys(map), values(map), get(v, key)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Bitwise Functions (8):")
	fmt.Fprintln(ctx.Stdout(), "  bitand(a, b), bitor(a, b), bitxor(a, b), bitnand(a, b),")
	fmt.Fprintln(ctx.Stdout(), "  bitnot(a), bitshl(a, n), bitshr(a, n), bitushr(a, n)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Hash Functions (3):")
	fmt.Fprintln(ctx.Stdout(), "  sha256(str), sha1(str), md5(str)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Helpers (2):")
	fmt.Fprintln(ctx.Stdout(), "  has(field), getOr(field, default)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Operators:")
	fmt.Fprintln(ctx.Stdout(), "  +  -  *  /  %  **  ==  !=  <  >  <=  >=  and  or  not  ?:  ??  in  |")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Use: ssql functions -category <name>   # Show detailed help for category")
	fmt.Fprintln(ctx.Stdout(), "     ssql functions -examples          # Show common expression patterns")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Full reference: doc/EXPRESSIONS.md")
	return nil
}

func printCategory(ctx *cf.Context, category string) error {
	switch strings.ToLower(category) {
	case "string":
		return printStringFunctions(ctx)
	case "math":
		return printMathFunctions(ctx)
	case "array":
		return printArrayFunctions(ctx)
	case "type":
		return printTypeFunctions(ctx)
	case "date":
		return printDateFunctions(ctx)
	case "map":
		return printMapFunctions(ctx)
	case "bitwise":
		return printBitwiseFunctions(ctx)
	case "hash":
		return printHashFunctions(ctx)
	case "operators":
		return printOperators(ctx)
	case "helpers":
		return printHelpers(ctx)
	default:
		fmt.Fprintf(ctx.Stdout(), "Unknown category: %s\n", category)
		fmt.Fprintln(ctx.Stdout())
		fmt.Fprintln(ctx.Stdout(), "Available categories: string, math, array, date, type, map, bitwise, hash, operators, helpers")
		return nil
	}
}

func printStringFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "STRING FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  upper(str)              Convert to uppercase")
	fmt.Fprintln(ctx.Stdout(), "    Example: upper(\"hello\") → \"HELLO\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  lower(str)              Convert to lowercase")
	fmt.Fprintln(ctx.Stdout(), "    Example: lower(\"WORLD\") → \"world\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  trim(str)               Remove leading/trailing whitespace")
	fmt.Fprintln(ctx.Stdout(), "    Example: trim(\"  text  \") → \"text\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  trimPrefix(str, prefix) Remove prefix from string")
	fmt.Fprintln(ctx.Stdout(), "    Example: trimPrefix(\"hello world\", \"hello \") → \"world\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  trimSuffix(str, suffix) Remove suffix from string")
	fmt.Fprintln(ctx.Stdout(), "    Example: trimSuffix(\"hello.csv\", \".csv\") → \"hello\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  split(str, sep)         Split string into array")
	fmt.Fprintln(ctx.Stdout(), "    Example: split(\"a,b,c\", \",\") → [\"a\", \"b\", \"c\"]")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  splitAfter(str, sep)    Split string, keeping separator attached")
	fmt.Fprintln(ctx.Stdout(), "    Example: splitAfter(\"a,b,c\", \",\") → [\"a,\", \"b,\", \"c\"]")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  join(arr, sep)          Join array into string")
	fmt.Fprintln(ctx.Stdout(), "    Example: join([\"a\", \"b\"], \",\") → \"a,b\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  replace(str, old, new)  Replace all occurrences of substring")
	fmt.Fprintln(ctx.Stdout(), "    Example: replace(\"hello world\", \"world\", \"there\") → \"hello there\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  replaceRegex(str, pattern, replacement)")
	fmt.Fprintln(ctx.Stdout(), "                          Regex replace with capture group support")
	fmt.Fprintln(ctx.Stdout(), "    Example: replaceRegex(\"abc 123\", \"[^0-9]\", \"\") → \"123\"")
	fmt.Fprintln(ctx.Stdout(), "    Example: replaceRegex(phone, \"(\\\\d{3})(\\\\d{4})\", \"$1-$2\")")
	fmt.Fprintln(ctx.Stdout(), "    Note: Use \\\\ for regex backslashes (e.g., \\\\d, \\\\w, \\\\s)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  repeat(str, n)          Repeat string n times")
	fmt.Fprintln(ctx.Stdout(), "    Example: repeat(\"ab\", 3) → \"ababab\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  indexOf(str, sub)       First index of substring (-1 if not found)")
	fmt.Fprintln(ctx.Stdout(), "    Example: indexOf(\"hello\", \"ll\") → 2")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  lastIndexOf(str, sub)   Last index of substring (-1 if not found)")
	fmt.Fprintln(ctx.Stdout(), "    Example: lastIndexOf(\"hello\", \"l\") → 3")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  hasPrefix(str, prefix)  Check if starts with prefix")
	fmt.Fprintln(ctx.Stdout(), "    Example: hasPrefix(\"hello\", \"he\") → true")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  hasSuffix(str, suffix)  Check if ends with suffix")
	fmt.Fprintln(ctx.Stdout(), "    Example: hasSuffix(\"world\", \"ld\") → true")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  contains(str, substr)   Check if contains substring")
	fmt.Fprintln(ctx.Stdout(), "    Example: contains(\"hello\", \"ll\") → true")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  Note: contains, startsWith, endsWith also work as operators:")
	fmt.Fprintln(ctx.Stdout(), "    name startsWith \"A\"    email contains \"@\"    file endsWith \".csv\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr clean 'replaceRegex(name, \"[^a-zA-Z]\", \"_\")'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr base 'trimSuffix(filename, \".csv\")'")
	return nil
}

func printMathFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "MATH FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  round(num)     Round to nearest integer")
	fmt.Fprintln(ctx.Stdout(), "    Example: round(3.7) → 4")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  floor(num)     Round down")
	fmt.Fprintln(ctx.Stdout(), "    Example: floor(3.7) → 3")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  ceil(num)      Round up")
	fmt.Fprintln(ctx.Stdout(), "    Example: ceil(3.2) → 4")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  abs(num)       Absolute value")
	fmt.Fprintln(ctx.Stdout(), "    Example: abs(-5) → 5")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  min(a, b)      Minimum of two values")
	fmt.Fprintln(ctx.Stdout(), "    Example: min(10, 20) → 10")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  max(a, b)      Maximum of two values")
	fmt.Fprintln(ctx.Stdout(), "    Example: max(10, 20) → 20")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr final_price 'round(price * 0.85)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr balance 'max(0, amount - fees)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'abs(actual - expected) < 0.01'")
	return nil
}

func printArrayFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "ARRAY FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Access:")
	fmt.Fprintln(ctx.Stdout(), "  len(arr)                  Length of array/string")
	fmt.Fprintln(ctx.Stdout(), "  first(arr)                First element")
	fmt.Fprintln(ctx.Stdout(), "  last(arr)                 Last element")
	fmt.Fprintln(ctx.Stdout(), "  get(v, key)               Safe access (returns nil if missing)")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Transform:")
	fmt.Fprintln(ctx.Stdout(), "  take(arr, n)              First n elements")
	fmt.Fprintln(ctx.Stdout(), "  reverse(arr)              Reverse array")
	fmt.Fprintln(ctx.Stdout(), "  sort(arr)                 Sort ascending")
	fmt.Fprintln(ctx.Stdout(), "  sortBy(arr, pred)         Sort by predicate result")
	fmt.Fprintln(ctx.Stdout(), "    Example: sortBy([{\"n\":3},{\"n\":1}], {.n}) → [{\"n\":1},{\"n\":3}]")
	fmt.Fprintln(ctx.Stdout(), "  uniq(arr)                 Remove duplicates")
	fmt.Fprintln(ctx.Stdout(), "  flatten(arr)              Flatten nested arrays")
	fmt.Fprintln(ctx.Stdout(), "  concat(arr1, arr2)        Concatenate arrays")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Filter & Search:")
	fmt.Fprintln(ctx.Stdout(), "  filter(arr, pred)         Filter elements matching predicate")
	fmt.Fprintln(ctx.Stdout(), "  find(arr, pred)           First matching element")
	fmt.Fprintln(ctx.Stdout(), "  findIndex(arr, pred)      Index of first match")
	fmt.Fprintln(ctx.Stdout(), "  findLast(arr, pred)       Last matching element")
	fmt.Fprintln(ctx.Stdout(), "  findLastIndex(arr, pred)  Index of last match")
	fmt.Fprintln(ctx.Stdout(), "  groupBy(arr, pred)        Group elements by predicate result")
	fmt.Fprintln(ctx.Stdout(), "    Example: groupBy([1,2,3,4], {# % 2 == 0 ? \"even\" : \"odd\"})")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Test:")
	fmt.Fprintln(ctx.Stdout(), "  all(arr, pred)            All elements match")
	fmt.Fprintln(ctx.Stdout(), "  any(arr, pred)            At least one matches")
	fmt.Fprintln(ctx.Stdout(), "  one(arr, pred)            Exactly one matches")
	fmt.Fprintln(ctx.Stdout(), "  none(arr, pred)           No elements match")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Aggregate:")
	fmt.Fprintln(ctx.Stdout(), "  count(arr)                Count of elements")
	fmt.Fprintln(ctx.Stdout(), "  sum(arr)                  Sum of elements")
	fmt.Fprintln(ctx.Stdout(), "  mean(arr)                 Average of elements")
	fmt.Fprintln(ctx.Stdout(), "  median(arr)               Median of elements")
	fmt.Fprintln(ctx.Stdout(), "  reduce(arr, pred, init)   Reduce to single value")
	fmt.Fprintln(ctx.Stdout(), "    Example: reduce([1,2,3,4], {# + #acc}, 0) → 10")
	fmt.Fprintln(ctx.Stdout(), "  map(arr, fn)              Transform each element")
	fmt.Fprintln(ctx.Stdout(), "    Example: map([1,2,3], {# * 2}) → [2,4,6]")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  Note: In predicates, # = current element, #acc = accumulator")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'all(scores, {# >= 60})'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr avg_score 'mean(scores)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr top3 'take(sort(scores), 3)'")
	return nil
}

func printDateFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "DATE FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  now()                   Current date/time")
	fmt.Fprintln(ctx.Stdout(), "    Example: now() → 2026-02-25T10:30:00+11:00")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  date(str)               Parse date string")
	fmt.Fprintln(ctx.Stdout(), "    Example: date(\"2026-01-15\")")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  duration(str)           Parse duration string")
	fmt.Fprintln(ctx.Stdout(), "    Example: duration(\"1h30m\") → 1h30m0s")
	fmt.Fprintln(ctx.Stdout(), "    Supports: ns, us, ms, s, m, h")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  timezone(str)           Get timezone location")
	fmt.Fprintln(ctx.Stdout(), "    Example: timezone(\"America/New_York\")")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr timestamp 'string(now())'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'date(created) > date(\"2026-01-01\")'")
	return nil
}

func printTypeFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "TYPE & ENCODING FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Type Conversion:")
	fmt.Fprintln(ctx.Stdout(), "  int(value)              Convert to integer")
	fmt.Fprintln(ctx.Stdout(), "    Example: int(\"123\") → 123")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  float(value)            Convert to float")
	fmt.Fprintln(ctx.Stdout(), "    Example: float(\"3.14\") → 3.14")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  string(value)           Convert to string")
	fmt.Fprintln(ctx.Stdout(), "    Example: string(123) → \"123\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  type(value)             Get type name as string")
	fmt.Fprintln(ctx.Stdout(), "    Example: type(42) → \"int\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "JSON:")
	fmt.Fprintln(ctx.Stdout(), "  toJSON(value)           Serialize to JSON string")
	fmt.Fprintln(ctx.Stdout(), "    Example: toJSON({\"a\": 1}) → \"{\\\"a\\\":1}\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  fromJSON(str)           Parse JSON string to value")
	fmt.Fprintln(ctx.Stdout(), "    Example: fromJSON(\"{\\\"a\\\":1}\") → {\"a\": 1}")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Base64:")
	fmt.Fprintln(ctx.Stdout(), "  toBase64(str)           Encode to Base64")
	fmt.Fprintln(ctx.Stdout(), "    Example: toBase64(\"hello\") → \"aGVsbG8=\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  fromBase64(str)         Decode from Base64")
	fmt.Fprintln(ctx.Stdout(), "    Example: fromBase64(\"aGVsbG8=\") → \"hello\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Map Conversion:")
	fmt.Fprintln(ctx.Stdout(), "  toPairs(map)            Map to [[key, value], ...] pairs")
	fmt.Fprintln(ctx.Stdout(), "    Example: toPairs({\"a\":1}) → [[\"a\", 1]]")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  fromPairs(arr)          [[key, value], ...] pairs to map")
	fmt.Fprintln(ctx.Stdout(), "    Example: fromPairs([[\"a\",1]]) → {\"a\": 1}")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr age_num 'int(age_str)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr label 'string(round(value * 100)) + \"%\"'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr encoded 'toBase64(email)'")
	return nil
}

func printMapFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "MAP FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  keys(map)               Get all keys from map")
	fmt.Fprintln(ctx.Stdout(), "    Example: keys({\"a\":1, \"b\":2}) → [\"a\", \"b\"]")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  values(map)             Get all values from map")
	fmt.Fprintln(ctx.Stdout(), "    Example: values({\"a\":1, \"b\":2}) → [1, 2]")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  get(v, key)             Safe access (returns nil if missing)")
	fmt.Fprintln(ctx.Stdout(), "    Example: get({\"a\":1}, \"a\") → 1")
	fmt.Fprintln(ctx.Stdout(), "    Example: get({\"a\":1}, \"z\") → nil")
	fmt.Fprintln(ctx.Stdout(), "    Also works on arrays: get([10,20,30], 1) → 20")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr field_count 'len(keys(metadata))'")
	return nil
}

func printBitwiseFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "BITWISE FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitand(a, b)     Bitwise AND")
	fmt.Fprintln(ctx.Stdout(), "    Example: bitand(0xFF, 0x0F) → 15")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitor(a, b)      Bitwise OR")
	fmt.Fprintln(ctx.Stdout(), "    Example: bitor(0xF0, 0x0F) → 255")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitxor(a, b)     Bitwise XOR")
	fmt.Fprintln(ctx.Stdout(), "    Example: bitxor(0xFF, 0x0F) → 240")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitnand(a, b)    Bitwise AND NOT")
	fmt.Fprintln(ctx.Stdout(), "    Example: bitnand(0xFF, 0x0F) → 240")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitnot(a)        Bitwise NOT")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitshl(a, n)     Left shift")
	fmt.Fprintln(ctx.Stdout(), "    Example: bitshl(1, 4) → 16")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitshr(a, n)     Right shift")
	fmt.Fprintln(ctx.Stdout(), "    Example: bitshr(16, 4) → 1")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  bitushr(a, n)    Unsigned right shift")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr masked 'bitand(flags, 0x0F)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'bitand(permissions, 4) != 0'")
	return nil
}

func printHashFunctions(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "HASH FUNCTIONS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  sha256(str)    SHA-256 hash (64 hex characters)")
	fmt.Fprintln(ctx.Stdout(), "    Example: sha256(\"hello\") → \"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  sha1(str)      SHA-1 hash (40 hex characters)")
	fmt.Fprintln(ctx.Stdout(), "    Example: sha1(\"hello\") → \"aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  md5(str)       MD5 hash (32 hex characters)")
	fmt.Fprintln(ctx.Stdout(), "    Example: md5(\"hello\") → \"5d41402abc4b2a76b9719d911017c592\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Common Usage:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr email_hash 'sha256(email)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr id 'sha256(name + \":\" + email)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr checksum 'md5(content)'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Note: For security-sensitive applications, prefer sha256 over md5/sha1.")
	return nil
}

func printOperators(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "OPERATORS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Arithmetic:")
	fmt.Fprintln(ctx.Stdout(), "  +    Addition:         price + tax")
	fmt.Fprintln(ctx.Stdout(), "  -    Subtraction:      revenue - cost")
	fmt.Fprintln(ctx.Stdout(), "  *    Multiplication:   price * qty")
	fmt.Fprintln(ctx.Stdout(), "  /    Division:         total / count")
	fmt.Fprintln(ctx.Stdout(), "  %    Modulo:           value % 10")
	fmt.Fprintln(ctx.Stdout(), "  **   Power:            base ** exponent")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Comparison:")
	fmt.Fprintln(ctx.Stdout(), "  ==   Equal:            status == \"active\"")
	fmt.Fprintln(ctx.Stdout(), "  !=   Not equal:        dept != \"Sales\"")
	fmt.Fprintln(ctx.Stdout(), "  <    Less than:        age < 18")
	fmt.Fprintln(ctx.Stdout(), "  >    Greater than:     salary > 50000")
	fmt.Fprintln(ctx.Stdout(), "  <=   Less/equal:       score <= 100")
	fmt.Fprintln(ctx.Stdout(), "  >=   Greater/equal:    age >= 21")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Logical:")
	fmt.Fprintln(ctx.Stdout(), "  and  Logical AND:      age >= 18 and status == \"active\"")
	fmt.Fprintln(ctx.Stdout(), "  or   Logical OR:       dept == \"Sales\" or dept == \"Marketing\"")
	fmt.Fprintln(ctx.Stdout(), "  not  Logical NOT:      not contains(email, \"@test.com\")")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "String/Array:")
	fmt.Fprintln(ctx.Stdout(), "  contains     Contains:      email contains \"@\"")
	fmt.Fprintln(ctx.Stdout(), "  startsWith   Starts with:   name startsWith \"A\"")
	fmt.Fprintln(ctx.Stdout(), "  endsWith     Ends with:     file endsWith \".csv\"")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Special:")
	fmt.Fprintln(ctx.Stdout(), "  ? :  Ternary:          age >= 18 ? \"adult\" : \"minor\"")
	fmt.Fprintln(ctx.Stdout(), "  ??   Nil coalescing:   value ?? \"default\"")
	fmt.Fprintln(ctx.Stdout(), "  in   Membership:       status in [\"active\", \"pending\"]")
	fmt.Fprintln(ctx.Stdout(), "  |    Pipe:             name | trim | upper")
	fmt.Fprintln(ctx.Stdout(), "  ..   Range:            1..5 → [1, 2, 3, 4, 5]")
	return nil
}

func printHelpers(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "HELPER FUNCTIONS (ssql-specific):")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  has(field)              Check if field exists")
	fmt.Fprintln(ctx.Stdout(), "    Example: has(\"email\") → true/false")
	fmt.Fprintln(ctx.Stdout(), "    Usage:   ssql where -if-expr 'has(\"email\") and contains(email, \"@\")'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "  getOr(field, default)   Get field value or default")
	fmt.Fprintln(ctx.Stdout(), "    Example: getOr(\"age\", 0) → field value or 0")
	fmt.Fprintln(ctx.Stdout(), "    Usage:   ssql update -set-expr total 'getOr(\"price\", 0) * getOr(\"qty\", 1)'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Why use these?")
	fmt.Fprintln(ctx.Stdout(), "  - Prevents errors when fields are missing or sparse")
	fmt.Fprintln(ctx.Stdout(), "  - Enables expressions to work gracefully with incomplete data")
	fmt.Fprintln(ctx.Stdout(), "  - Provides sensible defaults for missing values")
	return nil
}

func printExamples(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "COMMON EXPRESSION PATTERNS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Data Validation:")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'has(\"email\") and contains(email, \"@\")'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'age >= 0 and age <= 120'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Data Cleaning:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr status 'getOr(\"status\", \"pending\")'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr clean 'replaceRegex(name, \"[^a-zA-Z ]\", \"\")'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr base 'trimSuffix(filename, \".csv\")'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Calculations:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr total 'price * qty'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr discount 'total > 1000 ? total * 0.1 : 0'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr final 'round((price * qty) * (1 - discount / 100))'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Array Operations:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr avg_score 'mean(scores)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr top3 'take(sort(scores), 3)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'none(scores, {# < 60})'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Complex Filters:")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'age >= 18 and age <= 65 and status == \"active\"'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr '(age >= 18 and verified) or role == \"admin\"'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "String Manipulation:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr full_name 'first + \" \" + last'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr slug 'lower(join(split(trim(title), \" \"), \"-\"))'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Encoding:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr encoded 'toBase64(email)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr payload 'toJSON({\"name\": name, \"age\": age})'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Categorization:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr category 'age < 18 ? \"minor\" : \"adult\"'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr tier 'revenue > 10000 ? \"gold\" : (revenue > 5000 ? \"silver\" : \"bronze\")'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Full reference: doc/EXPRESSIONS.md")
	return nil
}
